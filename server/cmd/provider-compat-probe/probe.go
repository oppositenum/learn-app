package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

// uncontrolledLatencySamples bounds the reasoning-on comparison series. Each
// such call can take most of a minute, so a full series would dominate the run
// without improving the conclusion it supports.
const uncontrolledLatencySamples = 3

type probeAccounting interface {
	EnsurePrice(context.Context, string, string, time.Time) error
	RecordAIUsage(context.Context, ai.UsageRecord) error
	RecordAIRequestOutcome(context.Context, ai.RequestOutcomeRecord) error
}

type providerProbeConfig struct {
	Name     string
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Region   string
	// ReasoningControl is a raw JSON object merged into the top level of every
	// probe request. Reasoning-by-default burns most of the 75s submit budget
	// on a single generation, so the probe must state which control it applied
	// rather than silently sampling a configuration production would not use.
	// An empty value means no control was applied.
	ReasoningControl string
	AuthHeader       string
	AuthPrefix       string
}

func (config providerProbeConfig) reasoningOverlay() (map[string]any, error) {
	trimmed := strings.TrimSpace(config.ReasoningControl)
	if trimmed == "" {
		return nil, nil
	}
	var overlay map[string]any
	if err := json.Unmarshal([]byte(trimmed), &overlay); err != nil {
		return nil, fmt.Errorf("%s reasoning control must be a JSON object: %w", config.Name, err)
	}
	for _, reserved := range []string{"model", "messages", "input", "instructions", "response_format", "text", "previous_response_id"} {
		if _, present := overlay[reserved]; present {
			return nil, fmt.Errorf("%s reasoning control must not override %q", config.Name, reserved)
		}
	}
	return overlay, nil
}

func (config providerProbeConfig) reasoningControlLabel() string {
	if strings.TrimSpace(config.ReasoningControl) == "" {
		return "NONE"
	}
	return config.ReasoningControl
}

type probeRunner struct {
	httpClient *http.Client
	accounting probeAccounting
	samples    int
}

func newProbeRunner(client *http.Client, accounting probeAccounting, samples int) *probeRunner {
	return &probeRunner{httpClient: client, accounting: accounting, samples: samples}
}

type providerCompatibilityReport struct {
	EvidenceKind       string                       `json:"evidence_kind"`
	Provider           string                       `json:"provider"`
	Model              string                       `json:"model"`
	Region             string                       `json:"region"`
	StartedAt          time.Time                    `json:"started_at"`
	CompletedAt        time.Time                    `json:"completed_at"`
	SampleCount        int                          `json:"sample_count"`
	EndpointShapes     map[string]probeObservation  `json:"endpoint_shapes,omitempty"`
	SchemaAcceptance   map[string]probeObservation  `json:"schema_acceptance,omitempty"`
	KeywordEvidence    map[string]probeObservation  `json:"keyword_evidence,omitempty"`
	ConstraintEvidence map[string]constraintFinding `json:"constraint_evidence,omitempty"`
	UsageAndModel      probeObservation             `json:"usage_and_model,omitempty"`
	// PreferredShape is chosen on observed constraint enforcement, not on an
	// accounted status code, and the reason travels with it.
	PreferredShape       string                      `json:"preferred_shape,omitempty"`
	PreferredShapeReason string                      `json:"preferred_shape_reason,omitempty"`
	ConversationLink     map[string]probeObservation `json:"conversation_link,omitempty"`
	ErrorShape           probeObservation            `json:"error_shape,omitempty"`
	Cancellation         probeObservation            `json:"cancellation,omitempty"`
	// ReasoningControl states the request parameter the probe applied to every
	// call. Latency below is only interpretable together with it.
	ReasoningControl    string              `json:"reasoning_control"`
	GenerationLatencyMS latencyDistribution `json:"generation_latency_ms,omitempty"`
	ReviewLatencyMS     latencyDistribution `json:"review_latency_ms,omitempty"`
	// UncontrolledLatencyMS samples generation with the reasoning control
	// removed, so the report can show what the control is worth instead of
	// asserting the provider is fast.
	UncontrolledLatencyMS latencyDistribution `json:"uncontrolled_generation_latency_ms,omitempty"`
	EndToEndLatencyMS     latencyDistribution `json:"end_to_end_latency_ms,omitempty"`
	BudgetConclusion      string              `json:"budget_conclusion,omitempty"`
}

type probeObservation struct {
	Classification    string   `json:"classification"`
	RequestShape      string   `json:"request_shape,omitempty"`
	ReasoningControl  string   `json:"reasoning_control,omitempty"`
	HTTPStatus        int      `json:"http_status,omitempty"`
	LatencyMS         int64    `json:"latency_ms,omitempty"`
	ResponseIDPresent bool     `json:"response_id_present,omitempty"`
	RequestModel      string   `json:"request_model,omitempty"`
	ResponseModel     string   `json:"response_model,omitempty"`
	ModelMatches      bool     `json:"model_matches,omitempty"`
	UsagePaths        []string `json:"usage_paths,omitempty"`
	CachedTokenPaths  []string `json:"cached_token_paths,omitempty"`
	ErrorCodePaths    []string `json:"error_code_paths,omitempty"`
	RetryAfterPresent bool     `json:"retry_after_present,omitempty"`
	AccountingWritten bool     `json:"accounting_written,omitempty"`
	CancelPropagated  bool     `json:"cancel_propagated,omitempty"`
	Note              string   `json:"note,omitempty"`
	responseLink      string
}

type constraintFinding struct {
	Classification string `json:"classification"`
	RequestShape   string `json:"request_shape"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	// Attempts is how many violation-seeking samples produced this finding.
	// ENFORCED is only credible across several attempts; see constraintAttempts.
	Attempts int `json:"attempts,omitempty"`
}

// constraintAttempts is how many times each constraint probe asks the model to
// violate the schema. The two verdicts are not symmetric: under real
// constrained decoding a violation cannot occur, so one violating sample
// settles IGNORED, while a single compliant sample only shows the model
// happened to comply. Sampling once made verdicts flip between runs.
const constraintAttempts = 3

type latencyDistribution struct {
	Samples int64 `json:"samples"`
	P50     int64 `json:"p50"`
	P95     int64 `json:"p95"`
	P99     int64 `json:"p99"`
	Maximum int64 `json:"maximum"`
}

type runtimeSchemas map[string]json.RawMessage

func loadRuntimeSchemas(directory string) (runtimeSchemas, error) {
	result := make(runtimeSchemas, 3)
	for _, filename := range []string{"analyze_answer.schema.json", "tutor_turn.schema.json", "tutor_output_review.schema.json"} {
		contents, err := os.ReadFile(directory + "/" + filename)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filename, err)
		}
		if !json.Valid(contents) {
			return nil, fmt.Errorf("%s is not valid JSON", filename)
		}
		result[filename] = contents
	}
	return result, nil
}

func (runner *probeRunner) runProvider(ctx context.Context, config providerProbeConfig, schemas runtimeSchemas) (providerCompatibilityReport, error) {
	report := providerCompatibilityReport{
		EvidenceKind: "REAL_PROVIDER", Provider: config.Provider, Model: config.Model, Region: config.Region,
		StartedAt: time.Now().UTC(), SampleCount: runner.samples,
		ReasoningControl: config.reasoningControlLabel(),
		EndpointShapes:   make(map[string]probeObservation), SchemaAcceptance: make(map[string]probeObservation),
		KeywordEvidence: make(map[string]probeObservation), ConstraintEvidence: make(map[string]constraintFinding),
		ConversationLink: make(map[string]probeObservation),
	}
	for _, shape := range []string{"responses", "chat_completions"} {
		observation, _ := runner.call(ctx, config, shape, schemas["tutor_turn.schema.json"], "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
		report.EndpointShapes[shape] = observation
		for schemaName, schema := range schemas {
			observation, _ = runner.call(ctx, config, shape, schema, promptForSchema(schemaName), purposeForSchema(schemaName), "")
			report.SchemaAcceptance[shape+":"+schemaName] = observation
		}
	}
	// Run the constraint battery on every accounted shape before choosing one.
	// A shape that returns 200 may still ignore the schema entirely, so the
	// preferred shape has to be picked on observed enforcement rather than on
	// the order this list happens to be written in.
	for _, candidate := range []string{"responses", "chat_completions"} {
		if report.EndpointShapes[candidate].Classification != "ACCEPTED_ACCOUNTED" {
			continue
		}
		for _, keyword := range constraintKeywords {
			report.ConstraintEvidence[candidate+":"+keyword] = runner.probeConstraint(ctx, config, candidate, keyword)
		}
	}
	shape, reason := preferredShape(report.EndpointShapes, report.ConstraintEvidence)
	report.PreferredShape, report.PreferredShapeReason = shape, reason
	if shape == "" {
		report.BudgetConclusion = "UNVERIFIED: neither request shape produced accounted output"
		report.CompletedAt = time.Now().UTC()
		return report, nil
	}
	for keyword := range aioutputs.BlockedProviderSchemaKeywords() {
		observation, _ := runner.call(ctx, config, shape, schemaWithKeyword(keyword), "Return JSON with value set to ok.", ai.PurposeSocraticTurn, "")
		report.KeywordEvidence[keyword] = observation
	}
	usageObservation, _ := runner.call(ctx, config, shape, schemas["tutor_turn.schema.json"], "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
	report.UsageAndModel = usageObservation
	report.ConversationLink[shape] = runner.probeConversationLink(ctx, config, shape, schemas["tutor_turn.schema.json"])
	report.ErrorShape = runner.probeInvalidSchema(ctx, config, shape)
	report.Cancellation = runner.probeCancellation(ctx, config, shape, schemas["tutor_turn.schema.json"])
	report.GenerationLatencyMS = runner.sampleLatency(ctx, config, shape, schemas["tutor_turn.schema.json"], ai.PurposeSocraticTurn)
	report.ReviewLatencyMS = runner.sampleLatency(ctx, config, shape, schemas["tutor_output_review.schema.json"], ai.PurposeTutorOutputReview)
	if strings.TrimSpace(config.ReasoningControl) != "" {
		// A provider's own default may reason for most of a minute per call, so
		// the comparison series stays small on purpose. It exists to show the
		// control's effect, not to produce a publishable percentile.
		uncontrolled := config
		uncontrolled.ReasoningControl = ""
		report.UncontrolledLatencyMS = runner.sampleLatencyCount(
			ctx, uncontrolled, shape, schemas["tutor_turn.schema.json"],
			ai.PurposeSocraticTurn, min(runner.samples, uncontrolledLatencySamples),
		)
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func (runner *probeRunner) runDirection(ctx context.Context, generator, reviewer providerProbeConfig, schemas runtimeSchemas) providerCompatibilityReport {
	report := providerCompatibilityReport{
		EvidenceKind: "REAL_PROVIDER_DIRECTION", Provider: generator.Provider + "->" + reviewer.Provider,
		Model: generator.Model + "->" + reviewer.Model, Region: generator.Region + "->" + reviewer.Region,
		StartedAt: time.Now().UTC(), SampleCount: runner.samples,
	}
	var durations []int64
	for range runner.samples {
		started := time.Now()
		generatorShape := "responses"
		generatorObservation, _ := runner.callWithFiniteRetry(ctx, generator, generatorShape, schemas["tutor_turn.schema.json"], "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
		if generatorObservation.Classification != "ACCEPTED_ACCOUNTED" {
			generatorShape = "chat_completions"
			generatorObservation, _ = runner.callWithFiniteRetry(ctx, generator, generatorShape, schemas["tutor_turn.schema.json"], "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
		}
		if generatorObservation.Classification != "ACCEPTED_ACCOUNTED" {
			continue
		}
		reviewerObservation, _ := runner.callWithFiniteRetry(ctx, reviewer, "responses", schemas["tutor_output_review.schema.json"], promptForSchema("tutor_output_review.schema.json"), ai.PurposeTutorOutputReview, "")
		if reviewerObservation.Classification != "ACCEPTED_ACCOUNTED" {
			reviewerObservation, _ = runner.callWithFiniteRetry(ctx, reviewer, "chat_completions", schemas["tutor_output_review.schema.json"], promptForSchema("tutor_output_review.schema.json"), ai.PurposeTutorOutputReview, "")
		}
		if reviewerObservation.Classification == "ACCEPTED_ACCOUNTED" {
			durations = append(durations, time.Since(started).Milliseconds())
		}
	}
	report.EndToEndLatencyMS = distribution(durations)
	if len(durations) == 0 {
		report.BudgetConclusion = "UNVERIFIED: no fully accounted generation-plus-review sample completed"
	} else if report.EndToEndLatencyMS.P99 < 75_000 {
		report.BudgetConclusion = "OBSERVED_SAMPLES_WITHIN_75_SECONDS"
	} else {
		report.BudgetConclusion = "OBSERVED_P99_EXCEEDS_75_SECONDS"
	}
	report.CompletedAt = time.Now().UTC()
	return report
}

// constraintKeywords are the schema controls the runtime schemas depend on.
// enum carries the most weight: the server pins the authorized Tutor action by
// constraining that enum before the request leaves the process.
var constraintKeywords = []string{"additionalProperties", "required", "enum", "nested_enum", "minLength", "maxLength", "minimum", "maximum"}

// preferredShape picks the request shape with the most observed enforcement,
// because an accounted 200 only proves the provider took the request, not that
// it honoured the schema. Ties fall back to declaration order.
func preferredShape(observations map[string]probeObservation, constraints map[string]constraintFinding) (string, string) {
	best, bestScore, bestEnforced := "", -1, 0
	for _, shape := range []string{"responses", "chat_completions"} {
		if observations[shape].Classification != "ACCEPTED_ACCOUNTED" {
			continue
		}
		enforced, rejected := 0, 0
		for _, keyword := range constraintKeywords {
			switch constraints[shape+":"+keyword].Classification {
			case "ENFORCED":
				enforced++
			case "REJECTED":
				rejected++
			}
		}
		// A rejection is worse than an ignore: the request cannot even be sent.
		score := enforced*2 - rejected
		if score > bestScore {
			best, bestScore, bestEnforced = shape, score, enforced
		}
	}
	if best == "" {
		return "", "no request shape produced accounted output"
	}
	return best, fmt.Sprintf("%s enforced %d of %d probed schema constraints", best, bestEnforced, len(constraintKeywords))
}

func (runner *probeRunner) call(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage, prompt string, purpose ai.Purpose, previousID string) (probeObservation, json.RawMessage) {
	started := time.Now()
	observation := probeObservation{RequestShape: shape, RequestModel: config.Model}
	if err := runner.accounting.EnsurePrice(ctx, config.Provider, config.Model, started); err != nil {
		observation.Classification = "PRICE_PREFLIGHT_FAILED"
		observation.Note = "No provider request was sent."
		return observation, nil
	}
	reasoning, err := config.reasoningOverlay()
	if err != nil {
		observation.Classification = "REQUEST_BUILD_FAILED"
		observation.Note = "Invalid reasoning control; no provider request was sent."
		return observation, nil
	}
	observation.ReasoningControl = config.reasoningControlLabel()
	payload, endpoint, err := requestForShape(shape, config.Model, schema, prompt, previousID, reasoning)
	if err != nil {
		observation.Classification = "REQUEST_BUILD_FAILED"
		return observation, nil
	}
	requestID := uuid.NewString()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.BaseURL+endpoint, bytes.NewReader(payload))
	if err != nil {
		observation.Classification = "REQUEST_BUILD_FAILED"
		return observation, nil
	}
	request.Header.Set(config.AuthHeader, config.AuthPrefix+config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := runner.httpClient.Do(request)
	observation.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		observation.Classification = "TRANSPORT_ERROR"
		runner.recordOutcome(config, requestID, purpose, ai.RequestTransportError, nil, started)
		return observation, nil
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	observation.HTTPStatus = response.StatusCode
	observation.RetryAfterPresent = response.Header.Get("Retry-After") != ""
	if readErr != nil {
		observation.Classification = "RESPONSE_READ_FAILED"
		runner.recordOutcome(config, requestID, purpose, ai.RequestTransportError, &response.StatusCode, started)
		return observation, nil
	}
	var envelope any
	_ = json.Unmarshal(body, &envelope)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Only an error response carries error-code paths. Harvesting ".type"
		// from a success body just records ordinary content fields as if they
		// were diagnostics.
		observation.ErrorCodePaths = findJSONPaths(envelope, func(path string, _ any) bool {
			return strings.HasSuffix(path, ".code") || strings.HasSuffix(path, ".type")
		})
		observation.Classification = "PROVIDER_REJECTED"
		runner.recordOutcome(config, requestID, purpose, ai.RequestProviderError, &response.StatusCode, started)
		return observation, nil
	}
	output, responseID, responseModel := responseMetadata(shape, envelope)
	observation.ResponseIDPresent = responseID != ""
	observation.responseLink = responseID
	observation.ResponseModel = responseModel
	observation.ModelMatches = responseModel == config.Model
	observation.UsagePaths = findJSONPaths(envelope, tokenUsagePath)
	observation.CachedTokenPaths = findJSONPaths(envelope, cachedUsagePath)
	usageValue, usageOK := recognizedUsage(shape, envelope)
	if !usageOK {
		observation.Classification = "USAGE_MAPPING_UNVERIFIED"
		runner.recordOutcome(config, requestID, purpose, ai.RequestAccountingError, &response.StatusCode, started)
		return observation, output
	}
	accountingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = runner.accounting.RecordAIUsage(accountingCtx, ai.UsageRecord{
		RequestID: requestID, Purpose: purpose,
		Usage:   ai.ModelUsage{Provider: config.Provider, Model: config.Model, InputTokens: usageValue.input, CachedInputTokens: usageValue.cached, OutputTokens: usageValue.output},
		Latency: time.Since(started), CreatedAt: started,
	})
	if err != nil {
		observation.Classification = "ACCOUNTING_FAILED"
		runner.recordOutcome(config, requestID, purpose, ai.RequestAccountingError, &response.StatusCode, started)
		return observation, output
	}
	observation.AccountingWritten = true
	observation.Classification = "ACCEPTED_ACCOUNTED"
	runner.recordOutcome(config, requestID, purpose, ai.RequestSucceeded, &response.StatusCode, started)
	return observation, output
}

func (runner *probeRunner) callWithFiniteRetry(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage, prompt string, purpose ai.Purpose, previousID string) (probeObservation, json.RawMessage) {
	var observation probeObservation
	var output json.RawMessage
	for attempt := 1; attempt <= ai.TutorRetryMaxAttempts; attempt++ {
		observation, output = runner.call(ctx, config, shape, schema, prompt, purpose, previousID)
		if observation.Classification != "PROVIDER_REJECTED" ||
			(observation.HTTPStatus != http.StatusTooManyRequests && observation.HTTPStatus < http.StatusInternalServerError) {
			return observation, output
		}
		if attempt == ai.TutorRetryMaxAttempts {
			return observation, output
		}
		delay := ai.TutorRetryBaseDelay << (attempt - 1)
		select {
		case <-ctx.Done():
			observation.Classification = "RETRY_CANCELED"
			return observation, output
		case <-time.After(delay):
		}
	}
	return observation, output
}

func (runner *probeRunner) recordOutcome(config providerProbeConfig, requestID string, purpose ai.Purpose, outcome ai.RequestOutcome, status *int, started time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runner.accounting.RecordAIRequestOutcome(ctx, ai.RequestOutcomeRecord{
		RequestID: requestID, Provider: config.Provider, Model: config.Model, Purpose: purpose,
		Outcome: outcome, HTTPStatus: status, Latency: time.Since(started), CreatedAt: started,
	})
}

func requestForShape(shape, model string, schema json.RawMessage, prompt, previousID string, reasoning map[string]any) ([]byte, string, error) {
	var schemaValue any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return nil, "", err
	}
	switch shape {
	case "responses":
		payload := map[string]any{
			"model": model, "instructions": "Return only strict JSON.", "input": prompt,
			"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "provider_compat_probe", "strict": true, "schema": schemaValue}},
		}
		if previousID != "" {
			payload["previous_response_id"] = previousID
		}
		applyReasoningControl(payload, reasoning)
		encoded, err := json.Marshal(payload)
		return encoded, "/responses", err
	case "chat_completions":
		messages := []map[string]string{{"role": "system", "content": "Return only strict JSON."}}
		if previousID != "" {
			messages = append(messages,
				map[string]string{"role": "user", "content": "Return a brief schema-valid result."},
				map[string]string{"role": "assistant", "content": previousID},
			)
		}
		messages = append(messages, map[string]string{"role": "user", "content": prompt})
		payload := map[string]any{
			"model": model, "messages": messages,
			"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "provider_compat_probe", "strict": true, "schema": schemaValue}},
		}
		applyReasoningControl(payload, reasoning)
		encoded, err := json.Marshal(payload)
		return encoded, "/chat/completions", err
	default:
		return nil, "", fmt.Errorf("unsupported request shape %q", shape)
	}
}

func applyReasoningControl(payload map[string]any, reasoning map[string]any) {
	for key, value := range reasoning {
		payload[key] = value
	}
}

func promptForSchema(schemaName string) string {
	switch schemaName {
	case "analyze_answer.schema.json":
		return `Return an analysis with answer_correct=false, reasoning_quality="PARTIAL", confidence=0.5, error_type="PROBE", empty misconception and ability arrays, neutral emotion, normal engagement, recommended_action="PROBE", no difficulty increase, and weakness_layer="L5".`
	case "tutor_output_review.schema.json":
		return `Return a PASS review with no_answer_leak=true, reason_codes=["NONE"], and no violations.`
	default:
		return `Return a brief PROBE message, answer_revealed=false, and no segments.`
	}
}

func purposeForSchema(schemaName string) ai.Purpose {
	if schemaName == "analyze_answer.schema.json" {
		return ai.PurposeAnswerAnalysis
	}
	if schemaName == "tutor_output_review.schema.json" {
		return ai.PurposeTutorOutputReview
	}
	return ai.PurposeSocraticTurn
}

func schemaWithKeyword(keyword string) json.RawMessage {
	document := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
	switch keyword {
	case "oneOf", "anyOf", "allOf":
		document[keyword] = []any{map[string]any{"required": []string{"value"}}}
	case "not", "if", "then", "else":
		document[keyword] = map[string]any{"required": []string{"blocked"}}
	case "patternProperties":
		document[keyword] = map[string]any{"^x": map[string]any{"type": "string"}}
	case "dependencies":
		document[keyword] = map[string]any{"value": []string{"other"}}
	case "dependentSchemas":
		document[keyword] = map[string]any{"value": map[string]any{"required": []string{"other"}}}
	case "dependentRequired":
		document[keyword] = map[string]any{"value": []string{"other"}}
	}
	encoded, _ := json.Marshal(document)
	return encoded
}

func constraintProbe(keyword string) (json.RawMessage, string) {
	property := map[string]any{}
	document := map[string]any{"type": "object", "properties": map[string]any{"value": property}}
	prompt := "Return value as requested."
	switch keyword {
	case "additionalProperties":
		document["additionalProperties"] = false
		property["type"] = "string"
		prompt = `Return {"value":"ok","extra":"must be removed"}.`
	case "required":
		document["required"] = []string{"value"}
		property["type"] = "string"
		prompt = `Return an empty object without value.`
	case "enum":
		property["enum"] = []string{"ALLOWED"}
		prompt = `Return value="BLOCKED".`
	case "nested_enum":
		// A top-level enum being honoured says nothing about one inside array
		// items. analyze_answer.schema.json carries exactly that shape, and a
		// provider was observed returning an out-of-enum value there while
		// honouring every top-level enum in the same response.
		property["type"] = "array"
		property["items"] = map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"signal"},
			"properties": map[string]any{
				"signal": map[string]any{"enum": []string{"ALLOWED"}},
			},
		}
		prompt = `Return value=[{"signal":"BLOCKED"}].`
	case "minLength":
		property["type"], property["minLength"] = "string", 5
		prompt = `Return value="x".`
	case "maxLength":
		property["type"], property["maxLength"] = "string", 1
		prompt = `Return value="long".`
	case "minimum":
		property["type"], property["minimum"] = "number", 5
		prompt = `Return value=1.`
	case "maximum":
		property["type"], property["maximum"] = "number", 5
		prompt = `Return value=10.`
	}
	encoded, _ := json.Marshal(document)
	return encoded, prompt
}

// probeConstraint repeatedly asks the provider to violate one schema control.
// Any violating or rejected sample settles the verdict immediately; ENFORCED
// requires every attempt to have complied.
func (runner *probeRunner) probeConstraint(ctx context.Context, config providerProbeConfig, shape, keyword string) constraintFinding {
	schema, prompt := constraintProbe(keyword)
	finding := constraintFinding{Classification: "UNVERIFIED", RequestShape: shape}
	for attempt := 1; attempt <= constraintAttempts; attempt++ {
		observation, output := runner.call(ctx, config, shape, schema, prompt, ai.PurposeSocraticTurn, "")
		finding = classifyConstraint(shape, observation, schema, output)
		finding.Attempts = attempt
		if finding.Classification != "ENFORCED" {
			return finding
		}
	}
	return finding
}

func classifyConstraint(shape string, observation probeObservation, schema, output json.RawMessage) constraintFinding {
	finding := constraintFinding{RequestShape: shape, HTTPStatus: observation.HTTPStatus}
	if observation.Classification == "PROVIDER_REJECTED" {
		finding.Classification = "REJECTED"
		return finding
	}
	if observation.Classification != "ACCEPTED_ACCOUNTED" || len(output) == 0 {
		finding.Classification = "UNVERIFIED"
		return finding
	}
	compiler := jsonschema.NewCompiler()
	var schemaDocument any
	if json.Unmarshal(schema, &schemaDocument) != nil || compiler.AddResource("probe.json", schemaDocument) != nil {
		finding.Classification = "UNVERIFIED"
		return finding
	}
	compiled, err := compiler.Compile("probe.json")
	if err != nil {
		finding.Classification = "UNVERIFIED"
		return finding
	}
	var outputValue any
	if json.Unmarshal(output, &outputValue) != nil {
		finding.Classification = "IGNORED"
		return finding
	}
	if compiled.Validate(outputValue) == nil {
		finding.Classification = "ENFORCED"
	} else {
		finding.Classification = "IGNORED"
	}
	return finding
}

func (runner *probeRunner) probeConversationLink(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage) probeObservation {
	first, output := runner.call(ctx, config, shape, schema, "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
	if first.Classification != "ACCEPTED_ACCOUNTED" {
		first.Note = "Initial request did not complete; link was not tested."
		return first
	}
	link := first.responseLink
	if shape == "responses" {
		if link == "" {
			first.Note = "The response did not contain an ID; previous_response_id was not tested."
			return first
		}
	} else if len(output) > 0 {
		link = string(output)
	}
	second, _ := runner.call(ctx, config, shape, schema, "Continue with another brief PROBE question.", ai.PurposeSocraticTurn, link)
	return second
}

func (runner *probeRunner) probeInvalidSchema(ctx context.Context, config providerProbeConfig, shape string) probeObservation {
	schema := json.RawMessage(`{"type":"definitely-not-a-json-schema-type"}`)
	observation, _ := runner.call(ctx, config, shape, schema, "Return JSON.", ai.PurposeSocraticTurn, "")
	return observation
}

func (runner *probeRunner) probeCancellation(parent context.Context, config providerProbeConfig, shape string, schema json.RawMessage) probeObservation {
	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	observation, _ := runner.call(ctx, config, shape, schema, "Produce a detailed but schema-valid response after careful consideration.", ai.PurposeSocraticTurn, "")
	observation.CancelPropagated = errors.Is(ctx.Err(), context.Canceled) && time.Since(started) < 5*time.Second
	if observation.CancelPropagated {
		observation.Classification = "CANCEL_PROPAGATED"
	}
	return observation
}

func (runner *probeRunner) sampleLatency(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage, purpose ai.Purpose) latencyDistribution {
	return runner.sampleLatencyCount(ctx, config, shape, schema, purpose, runner.samples)
}

func (runner *probeRunner) sampleLatencyCount(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage, purpose ai.Purpose, count int) latencyDistribution {
	var values []int64
	for range count {
		observation, _ := runner.callWithFiniteRetry(ctx, config, shape, schema, promptForSchema(schemaNameForPurpose(purpose)), purpose, "")
		if observation.Classification == "ACCEPTED_ACCOUNTED" {
			values = append(values, observation.LatencyMS)
		}
	}
	return distribution(values)
}

func schemaNameForPurpose(purpose ai.Purpose) string {
	if purpose == ai.PurposeTutorOutputReview {
		return "tutor_output_review.schema.json"
	}
	return "tutor_turn.schema.json"
}

func distribution(values []int64) latencyDistribution {
	if len(values) == 0 {
		return latencyDistribution{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return latencyDistribution{Samples: int64(len(values)), P50: percentile(values, 0.50), P95: percentile(values, 0.95), P99: percentile(values, 0.99), Maximum: values[len(values)-1]}
}

func percentile(values []int64, percentile float64) int64 {
	index := int(math.Ceil(percentile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}

type recognizedTokenUsage struct{ input, cached, output int64 }

func recognizedUsage(shape string, envelope any) (recognizedTokenUsage, bool) {
	root, ok := envelope.(map[string]any)
	if !ok {
		return recognizedTokenUsage{}, false
	}
	usageMap, ok := root["usage"].(map[string]any)
	if !ok {
		return recognizedTokenUsage{}, false
	}
	if shape == "responses" {
		input, inputOK := int64JSON(usageMap["input_tokens"])
		output, outputOK := int64JSON(usageMap["output_tokens"])
		cached := nestedInt64(usageMap, "input_tokens_details", "cached_tokens")
		return recognizedTokenUsage{input: input, cached: cached, output: output}, inputOK && outputOK
	}
	input, inputOK := int64JSON(usageMap["prompt_tokens"])
	output, outputOK := int64JSON(usageMap["completion_tokens"])
	cached := nestedInt64(usageMap, "prompt_tokens_details", "cached_tokens")
	return recognizedTokenUsage{input: input, cached: cached, output: output}, inputOK && outputOK
}

func nestedInt64(root map[string]any, object, key string) int64 {
	nested, _ := root[object].(map[string]any)
	value, _ := int64JSON(nested[key])
	return value
}

func int64JSON(value any) (int64, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || math.Trunc(number) != number {
		return 0, false
	}
	return int64(number), true
}

func responseMetadata(shape string, envelope any) (json.RawMessage, string, string) {
	root, _ := envelope.(map[string]any)
	responseID, _ := root["id"].(string)
	model, _ := root["model"].(string)
	if shape == "responses" {
		outputs, _ := root["output"].([]any)
		for _, output := range outputs {
			item, _ := output.(map[string]any)
			contents, _ := item["content"].([]any)
			for _, content := range contents {
				part, _ := content.(map[string]any)
				if text, ok := part["text"].(string); ok {
					return json.RawMessage(text), responseID, model
				}
			}
		}
		return nil, responseID, model
	}
	choices, _ := root["choices"].([]any)
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		if content, ok := message["content"].(string); ok {
			return json.RawMessage(content), responseID, model
		}
	}
	return nil, responseID, model
}

func findJSONPaths(value any, predicate func(string, any) bool) []string {
	var paths []string
	var walk func(any, string)
	walk = func(current any, path string) {
		switch typed := current.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childPath := path + "." + key
				if predicate(childPath, typed[key]) {
					paths = append(paths, childPath)
				}
				walk(typed[key], childPath)
			}
		case []any:
			for index, item := range typed {
				walk(item, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	walk(value, "$")
	return paths
}

func tokenUsagePath(path string, value any) bool {
	_, numeric := value.(float64)
	lower := strings.ToLower(path)
	return numeric && strings.Contains(lower, "token")
}

func cachedUsagePath(path string, value any) bool {
	return tokenUsagePath(path, value) && strings.Contains(strings.ToLower(path), "cache")
}
