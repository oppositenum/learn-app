package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

var ErrTutorOutputAuditorUnavailable = errors.New("independent Tutor output auditor is unavailable")

// ErrTutorTeachingContextMissing keeps a teaching request from reaching a
// provider without its subject and knowledge point.
var ErrTutorTeachingContextMissing = errors.New("teaching context is required for a Tutor request")

// ErrTutorSubjectUnsupported keeps a teaching request from reaching a provider
// with a subject the five-subject classroom does not teach. An empty default
// would let an unknown code inherit no rules and then invent its own.
var ErrTutorSubjectUnsupported = errors.New("teaching subject is not supported")

// subjectInstructions adds the rules that differ by subject. The subject comes
// from released curriculum data, never from reading the question text.
func subjectInstructions(teaching content.TeachingContext) (string, error) {
	switch teaching.SubjectCode {
	case "ENGLISH":
		return " " + languageReadingInstructions, nil
	case "CHINESE":
		return " " + chineseInstructions, nil
	case "MATH":
		return " " + mathGoalInstructions, nil
	case "PHYSICS":
		return " " + physicsInstructions, nil
	case "CHEMISTRY":
		return " " + chemistryInstructions, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrTutorSubjectUnsupported, teaching.SubjectCode)
	}
}

const tutorOutputOperationTimeout = 85 * time.Second

type StructuredClient interface {
	GenerateStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error)
}

type CodexProvider struct {
	client                 StructuredClient
	auditor                TutorOutputAuditor
	schemas                map[string]*jsonschema.Schema
	rawFiles               fs.FS
	generationRetry        generationRetryPolicy
	waitForGenerationRetry func(context.Context, time.Duration) error
	generationRetryJitter  func(time.Duration) time.Duration
}

type generationRetryPolicy struct {
	maxAttempts    int
	baseDelay      time.Duration
	maxJitter      time.Duration
	maxRetryWait   time.Duration
	overallTimeout time.Duration
}

func NewCodexProvider(client StructuredClient, auditors ...TutorOutputAuditor) (*CodexProvider, error) {
	if len(auditors) > 1 {
		return nil, errors.New("only one Tutor output auditor may be configured")
	}
	var auditor TutorOutputAuditor
	if len(auditors) == 1 {
		auditor = auditors[0]
	}
	compiler := jsonschema.NewCompiler()
	for _, filename := range []string{"analyze_answer.schema.json", "tutor_turn.schema.json"} {
		contents, err := fs.ReadFile(aioutputs.Files, filename)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", filename, err)
		}
		var document any
		if err := json.Unmarshal(contents, &document); err != nil {
			return nil, fmt.Errorf("decode %s: %w", filename, err)
		}
		if err := compiler.AddResource(filename, document); err != nil {
			return nil, fmt.Errorf("register %s: %w", filename, err)
		}
	}

	schemas := make(map[string]*jsonschema.Schema, 2)
	for _, filename := range []string{"analyze_answer.schema.json", "tutor_turn.schema.json"} {
		schema, err := compiler.Compile(filename)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", filename, err)
		}
		schemas[filename] = schema
	}
	return &CodexProvider{
		client: client, auditor: auditor, schemas: schemas, rawFiles: aioutputs.Files,
		generationRetry: generationRetryPolicy{
			maxAttempts: TutorRetryMaxAttempts, baseDelay: TutorRetryBaseDelay,
			maxJitter: TutorRetryMaxJitter, maxRetryWait: TutorRetryMaxWait,
			overallTimeout: TutorRetryOverallTimeout,
		},
		waitForGenerationRetry: waitForTutorGenerationRetry,
		generationRetryJitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(max.Nanoseconds() + 1))
		},
	}, nil
}

func (provider *CodexProvider) AnalyzeAnswer(ctx context.Context, request AnalyzeAnswerRequest) (AnalyzeAnswerResult, error) {
	if !request.Question.Teaching.Complete() {
		return AnalyzeAnswerResult{}, ErrTutorTeachingContextMissing
	}
	var result AnalyzeAnswerResult
	if err := provider.analyzeAnswerWithRetry(ctx, request, &result); err != nil {
		return AnalyzeAnswerResult{}, err
	}
	return result, nil
}

func (provider *CodexProvider) analyzeAnswerWithRetry(ctx context.Context, request AnalyzeAnswerRequest, target *AnalyzeAnswerResult) error {
	ctx, cancel := context.WithTimeout(ctx, provider.generationRetry.overallTimeout)
	defer cancel()

	var lastErr error
	var lastRequestID string
	var lastHTTPStatus int
	lastProviderCode := TutorReviewDiagnosticUnavailable
	var waited time.Duration
	// The analysis stage decides what the answer was an attempt at, so it needs
	// the subject rules as much as generation does. Carrying the context only in
	// the payload was not enough: a reading answer could still be judged as
	// arithmetic here, and by the time generation applied the rules the wrong
	// reading had already been fixed.
	subjectRule, err := subjectInstructions(request.Question.Teaching)
	if err != nil {
		return err
	}
	instructions := analyzeAnswerInstructions + " " + teachingContextInstructions +
		subjectRule + " " + outputShapeInstructions
	for attempt := 1; attempt <= provider.generationRetry.maxAttempts; attempt++ {
		requestID := uuid.NewString()
		lastRequestID = requestID
		err := provider.generateAttempt(
			ctx,
			PurposeAnswerAnalysis,
			"analyze_answer.schema.json",
			request,
			instructions,
			"",
			"",
			target,
			requestID,
		)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryableGenerationError(err) {
			return err
		}
		if attempt == provider.generationRetry.maxAttempts {
			break
		}
		if errors.Is(err, ErrInvalidProviderOutput) {
			// The provider is healthy; it just produced output this service
			// rejected. Backing off would spend submit budget without making
			// the next sample any more likely to validate — but repeating the
			// identical request would not either, so tell the next attempt
			// which location was refused. Locations only, never values.
			instructions = withSchemaCorrection(instructions, err)
			continue
		}
		lastHTTPStatus, lastProviderCode, _ = ResponsesErrorDiagnostics(err)

		delay := provider.generationRetry.baseDelay << (attempt - 1)
		delay += provider.generationRetryJitter(provider.generationRetry.maxJitter)
		if retryAfter, ok := ResponsesRetryAfter(err); ok && retryAfter > delay {
			delay = retryAfter
		}
		if delay > provider.generationRetry.maxRetryWait-waited {
			break
		}
		if err := provider.waitForGenerationRetry(ctx, delay); err != nil {
			lastErr = err
			break
		}
		waited += delay
	}
	category := TutorReviewFailureRetryExhausted
	if errors.Is(lastErr, ErrInvalidProviderOutput) {
		category = TutorReviewFailureInvalidSchema
	}
	if errors.Is(lastErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		category = TutorReviewFailureTimeout
	}
	return NewTutorGenerationBusyFailure(category, lastHTTPStatus, lastProviderCode, lastRequestID, lastErr)
}

func (provider *CodexProvider) GenerateTurn(ctx context.Context, request GenerateTurnRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeSocraticTurn, request, generateTurnInstructions)
}

func (provider *CodexProvider) GenerateAnalogy(ctx context.Context, request AnalogyRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeSocraticTurn, GenerateTurnRequest(request), generateAnalogyInstructions)
}

func (provider *CodexProvider) GenerateParallelExample(ctx context.Context, request ExampleRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeExplanation, GenerateTurnRequest(request), generateParallelExampleInstructions)
}

func (provider *CodexProvider) GenerateExplanation(ctx context.Context, request ExplainRequest) (Explanation, error) {
	return provider.generateTurn(ctx, PurposeExplanation, GenerateTurnRequest(request), generateExplanationInstructions)
}

func (provider *CodexProvider) generateTurn(ctx context.Context, purpose Purpose, request GenerateTurnRequest, instructions string) (TutorTurn, error) {
	if provider.auditor == nil {
		return TutorTurn{}, ErrTutorOutputAuditorUnavailable
	}
	// Fail closed rather than fall back to a request carrying only a knowledge
	// point UUID. That fallback is what made the Tutor answer an English
	// reading task with a Chinese arithmetic analogy.
	if !request.Teaching.Complete() {
		return TutorTurn{}, ErrTutorTeachingContextMissing
	}
	subjectRule, err := subjectInstructions(request.Teaching)
	if err != nil {
		return TutorTurn{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, tutorOutputOperationTimeout)
	defer cancel()
	instructions = instructions + " " + teachingContextInstructions + subjectRule +
		" " + turnStyleInstructions + " " + outputShapeInstructions
	requiredAction := string(request.TutorDecision.NextState)
	if request.TutorDecision.NextState == tutor.StateHint {
		instructions = instructions + " " + hintInstructions
	}
	// enforceTutorMaterialPolicy rejects new numeric material for exactly these
	// actions. Stating the rule to the generator does not relax that gate; it
	// stops the gate from being the first place the model learns about it.
	switch request.TutorDecision.NextState {
	case tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy:
		instructions = instructions + " " + materialDisciplineInstructions
	}
	if layer := weaknessLayerInstructions(request.WeaknessLayer); layer != "" {
		instructions = instructions + " " + layer
	}
	if requiredAction != "" {
		instructions = fmt.Sprintf(`%s Set the "action" output field to exactly %q.`, instructions, requiredAction)
	}
	var turn TutorTurn
	if err := provider.generateTutorTurn(ctx, purpose, request, instructions, requiredAction, &turn); err != nil {
		return TutorTurn{}, err
	}
	if err := provider.auditor.AuditTutorOutput(ctx, TutorOutputAuditRequest{
		StudentID: request.StudentID, SessionID: request.SessionID, Question: request.Question,
		Teaching:      request.Teaching,
		PrivateAnswer: request.AuditPrivateAnswer, Candidate: turn, GeneratorResponseID: turn.ResponseID,
	}); err != nil {
		return TutorTurn{}, err
	}
	if err := enforceTutorMaterialPolicy(request.Question, turn); err != nil {
		return TutorTurn{}, errors.Join(ErrTutorOutputRephraseRequired, err)
	}
	return ensureOriginalTaskVerification(turn), nil
}

func (provider *CodexProvider) generateTutorTurn(ctx context.Context, purpose Purpose, request GenerateTurnRequest, instructions, requiredAction string, target *TutorTurn) error {
	ctx, cancel := context.WithTimeout(ctx, provider.generationRetry.overallTimeout)
	defer cancel()

	var lastErr error
	var lastRequestID string
	var lastHTTPStatus int
	lastProviderCode := TutorReviewDiagnosticUnavailable
	var waited time.Duration
	for attempt := 1; attempt <= provider.generationRetry.maxAttempts; attempt++ {
		requestID := uuid.NewString()
		lastRequestID = requestID
		err := provider.generateAttempt(ctx, purpose, "tutor_turn.schema.json", request, instructions, request.PreviousResponseID, requiredAction, target, requestID)
		if err == nil {
			return nil
		}
		lastErr = err
		if !IsRetryableGenerationError(err) {
			return err
		}
		if attempt == provider.generationRetry.maxAttempts {
			break
		}
		if errors.Is(err, ErrInvalidProviderOutput) {
			// The provider is healthy; it just produced output this service
			// rejected. Backing off would spend submit budget without making
			// the next sample any more likely to validate — but repeating the
			// identical request would not either, so tell the next attempt
			// which location was refused. Locations only, never values.
			instructions = withSchemaCorrection(instructions, err)
			continue
		}
		lastHTTPStatus, lastProviderCode, _ = ResponsesErrorDiagnostics(err)

		delay := provider.generationRetry.baseDelay << (attempt - 1)
		delay += provider.generationRetryJitter(provider.generationRetry.maxJitter)
		if retryAfter, ok := ResponsesRetryAfter(err); ok && retryAfter > delay {
			delay = retryAfter
		}
		if delay > provider.generationRetry.maxRetryWait-waited {
			break
		}
		if err := provider.waitForGenerationRetry(ctx, delay); err != nil {
			lastErr = err
			break
		}
		waited += delay
	}
	category := TutorReviewFailureRetryExhausted
	if errors.Is(lastErr, ErrInvalidProviderOutput) {
		category = TutorReviewFailureInvalidSchema
	}
	if errors.Is(lastErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		category = TutorReviewFailureTimeout
	}
	return NewTutorGenerationBusyFailure(category, lastHTTPStatus, lastProviderCode, lastRequestID, lastErr)
}

func waitForTutorGenerationRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (provider *CodexProvider) generate(ctx context.Context, purpose Purpose, schemaFile string, input any, instructions, previousResponseID, requiredAction string, target any) error {
	return provider.generateAttempt(ctx, purpose, schemaFile, input, instructions, previousResponseID, requiredAction, target, uuid.NewString())
}

func (provider *CodexProvider) generateAttempt(ctx context.Context, purpose Purpose, schemaFile string, input any, instructions, previousResponseID, requiredAction string, target any, requestID string) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal teaching request: %w", err)
	}
	schemaBytes, err := fs.ReadFile(provider.rawFiles, schemaFile)
	if err != nil {
		return fmt.Errorf("read output schema: %w", err)
	}
	if requiredAction != "" {
		// Constrain the strict output schema so the provider cannot label the
		// turn with any action other than the server-authorized one. The local
		// full-enum validation below still applies unchanged.
		schemaBytes, err = constrainActionEnum(schemaBytes, requiredAction)
		if err != nil {
			return fmt.Errorf("constrain output schema: %w", err)
		}
	}
	result, err := provider.client.GenerateStructured(ctx, StructuredRequest{
		RequestID: requestID, StudentID: studentID(input), SessionID: sessionID(input),
		Purpose: purpose, Instructions: instructions, Input: payload,
		SchemaName: schemaFile, Schema: schemaBytes, PreviousResponseID: previousResponseID,
	})
	if err != nil {
		return err
	}
	var untyped any
	if err := json.Unmarshal(result.OutputJSON, &untyped); err != nil {
		return fmt.Errorf("%w: decode structured output: %v", ErrInvalidProviderOutput, err)
	}
	if err := provider.schemas[schemaFile].Validate(untyped); err != nil {
		return fmt.Errorf("%w: validate structured output at %s", ErrInvalidProviderOutput, schemaRejectionPaths(err))
	}
	if err := json.Unmarshal(result.OutputJSON, target); err != nil {
		return fmt.Errorf("%w: decode typed output: %v", ErrInvalidProviderOutput, err)
	}
	if turn, ok := target.(*TutorTurn); ok {
		turn.ResponseID = result.ResponseID
	}
	// The schema allows NONE and L1 to L6 independently of answer_correct;
	// agreement between the two is checked here so that a wrong answer
	// without a layer is retried like any other rejected sample.
	if analysis, ok := target.(*AnalyzeAnswerResult); ok {
		if err := ValidateWeaknessLayer(*analysis); err != nil {
			return err
		}
	}
	return nil
}

func constrainActionEnum(schemaBytes []byte, action string) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, err
	}
	properties, ok := document["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("schema has no properties object")
	}
	actionSchema, ok := properties["action"].(map[string]any)
	if !ok {
		return nil, errors.New("schema has no action property")
	}
	actionSchema["enum"] = []string{action}
	return json.Marshal(document)
}

func studentID(input any) string {
	switch request := input.(type) {
	case AnalyzeAnswerRequest:
		return request.StudentID
	case GenerateTurnRequest:
		return request.StudentID
	case AnalogyRequest:
		return request.StudentID
	case ExampleRequest:
		return request.StudentID
	case ExplainRequest:
		return request.StudentID
	default:
		return ""
	}
}

func sessionID(input any) string {
	switch request := input.(type) {
	case AnalyzeAnswerRequest:
		return request.SessionID
	case GenerateTurnRequest:
		return request.SessionID
	case AnalogyRequest:
		return request.SessionID
	case ExampleRequest:
		return request.SessionID
	case ExplainRequest:
		return request.SessionID
	default:
		return ""
	}
}

// schemaRejectionPaths renders where local validation refused the provider's
// output, as a compact list of JSON pointers. Values are deliberately dropped:
// a rejected analysis can carry student content, and this string reaches logs.
func schemaRejectionPaths(err error) string {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return "unknown"
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0, 4)
	var walk func(node *jsonschema.ValidationError)
	walk = func(node *jsonschema.ValidationError) {
		if len(node.Causes) == 0 {
			location := "/" + strings.Join(node.InstanceLocation, "/")
			if location == "/" {
				location = "/(root)"
			}
			if _, done := seen[location]; !done {
				seen[location] = struct{}{}
				paths = append(paths, location)
			}
			return
		}
		for _, cause := range node.Causes {
			walk(cause)
		}
	}
	walk(validation)
	sort.Strings(paths)
	if len(paths) > 6 {
		paths = paths[:6]
	}
	return strings.Join(paths, ",")
}

// RejectedSchemaPaths exposes the rejected locations for diagnostics.
func RejectedSchemaPaths(err error) string {
	if err == nil || !errors.Is(err, ErrInvalidProviderOutput) {
		return ""
	}
	var validation *jsonschema.ValidationError
	if errors.As(err, &validation) {
		return schemaRejectionPaths(err)
	}
	if _, rest, found := strings.Cut(err.Error(), "validate structured output at "); found {
		return rest
	}
	return ""
}

// withSchemaCorrection appends a one-line correction naming the schema
// locations local validation refused, so a retry is informed rather than an
// identical repeat. Providers have been observed not enforcing nested enums,
// and the two enums in analyze_answer overlap on two of three values, so an
// uncorrected retry tends to reproduce the same rejection.
func withSchemaCorrection(instructions string, err error) string {
	paths := RejectedSchemaPaths(err)
	if paths == "" || paths == "unknown" {
		return instructions
	}
	return instructions + " Your previous reply was rejected by local schema validation at " + paths +
		". Re-read the schema for those fields and return a value the schema allows there."
}
