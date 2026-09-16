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

type probeAccounting interface {
	EnsurePrice(context.Context, string, string, time.Time) error
	RecordAIUsage(context.Context, ai.UsageRecord) error
	RecordAIRequestOutcome(context.Context, ai.RequestOutcomeRecord) error
}

type providerProbeConfig struct {
	Name       string
	Provider   string
	BaseURL    string
	APIKey     string
	Model      string
	Region     string
	AuthHeader string
	AuthPrefix string
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
	EvidenceKind        string                       `json:"evidence_kind"`
	Provider            string                       `json:"provider"`
	Model               string                       `json:"model"`
	Region              string                       `json:"region"`
	StartedAt           time.Time                    `json:"started_at"`
	CompletedAt         time.Time                    `json:"completed_at"`
	SampleCount         int                          `json:"sample_count"`
	EndpointShapes      map[string]probeObservation  `json:"endpoint_shapes,omitempty"`
	SchemaAcceptance    map[string]probeObservation  `json:"schema_acceptance,omitempty"`
	KeywordEvidence     map[string]probeObservation  `json:"keyword_evidence,omitempty"`
	ConstraintEvidence  map[string]constraintFinding `json:"constraint_evidence,omitempty"`
	UsageAndModel       probeObservation             `json:"usage_and_model,omitempty"`
	ConversationLink    map[string]probeObservation  `json:"conversation_link,omitempty"`
	ErrorShape          probeObservation             `json:"error_shape,omitempty"`
	Cancellation        probeObservation             `json:"cancellation,omitempty"`
	GenerationLatencyMS latencyDistribution          `json:"generation_latency_ms,omitempty"`
	ReviewLatencyMS     latencyDistribution          `json:"review_latency_ms,omitempty"`
	EndToEndLatencyMS   latencyDistribution          `json:"end_to_end_latency_ms,omitempty"`
	BudgetConclusion    string                       `json:"budget_conclusion,omitempty"`
}

type probeObservation struct {
	Classification    string   `json:"classification"`
	RequestShape      string   `json:"request_shape,omitempty"`
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
}

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
		EndpointShapes: make(map[string]probeObservation), SchemaAcceptance: make(map[string]probeObservation),
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
	shape := preferredShape(report.EndpointShapes)
	if shape == "" {
		report.BudgetConclusion = "UNVERIFIED: neither request shape produced accounted output"
		report.CompletedAt = time.Now().UTC()
		return report, nil
	}
	for keyword := range aioutputs.BlockedProviderSchemaKeywords() {
		observation, _ := runner.call(ctx, config, shape, schemaWithKeyword(keyword), "Return JSON with value set to ok.", ai.PurposeSocraticTurn, "")
		report.KeywordEvidence[keyword] = observation
	}
	for _, keyword := range []string{"additionalProperties", "required", "enum", "minLength", "maxLength", "minimum", "maximum"} {
		schema, prompt := constraintProbe(keyword)
		observation, output := runner.call(ctx, config, shape, schema, prompt, ai.PurposeSocraticTurn, "")
		report.ConstraintEvidence[keyword] = classifyConstraint(shape, observation, schema, output)
	}
	usageObservation, _ := runner.call(ctx, config, shape, schemas["tutor_turn.schema.json"], "Return a brief PROBE question as strict JSON.", ai.PurposeSocraticTurn, "")
	report.UsageAndModel = usageObservation
	report.ConversationLink[shape] = runner.probeConversationLink(ctx, config, shape, schemas["tutor_turn.schema.json"])
	report.ErrorShape = runner.probeInvalidSchema(ctx, config, shape)
	report.Cancellation = runner.probeCancellation(ctx, config, shape, schemas["tutor_turn.schema.json"])
	report.GenerationLatencyMS = runner.sampleLatency(ctx, config, shape, schemas["tutor_turn.schema.json"], ai.PurposeSocraticTurn)
	report.ReviewLatencyMS = runner.sampleLatency(ctx, config, shape, schemas["tutor_output_review.schema.json"], ai.PurposeTutorOutputReview)
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

func preferredShape(observations map[string]probeObservation) string {
	for _, shape := range []string{"responses", "chat_completions"} {
		if observations[shape].Classification == "ACCEPTED_ACCOUNTED" {
			return shape
		}
	}
	return ""
}

func (runner *probeRunner) call(ctx context.Context, config providerProbeConfig, shape string, schema json.RawMessage, prompt string, purpose ai.Purpose, previousID string) (probeObservation, json.RawMessage) {
	started := time.Now()
	observation := probeObservation{RequestShape: shape, RequestModel: config.Model}
	if err := runner.accounting.EnsurePrice(ctx, config.Provider, config.Model, started); err != nil {
		observation.Classification = "PRICE_PREFLIGHT_FAILED"
		observation.Note = "No provider request was sent."
		return observation, nil
	}
	payload, endpoint, err := requestForShape(shape, config.Model, schema, prompt, previousID)
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
	observation.ErrorCodePaths = findJSONPaths(envelope, func(path string, _ any) bool {
		return strings.HasSuffix(path, ".code") || strings.HasSuffix(path, ".type")
	})
	if response.StatusCode < 200 || response.StatusCode >= 300 {
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

func requestForShape(shape, model string, schema json.RawMessage, prompt, previousID string) ([]byte, string, error) {
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
		encoded, err := json.Marshal(map[string]any{
			"model": model, "messages": messages,
			"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "provider_compat_probe", "strict": true, "schema": schemaValue}},
		})
		return encoded, "/chat/completions", err
	default:
		return nil, "", fmt.Errorf("unsupported request shape %q", shape)
	}
}

func promptForSchema(schemaName string) string {
	switch schemaName {
	case "analyze_answer.schema.json":
		return `Return an analysis with answer_correct=false, reasoning_quality="PARTIAL", confidence=0.5, error_type="PROBE", empty misconception and ability arrays, neutral emotion, normal engagement, recommended_action="PROBE", and no difficulty increase.`
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
	var values []int64
	for range runner.samples {
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
