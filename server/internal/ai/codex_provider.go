package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
)

var ErrTutorOutputAuditorUnavailable = errors.New("independent Tutor output auditor is unavailable")

const tutorOutputOperationTimeout = 85 * time.Second

type StructuredClient interface {
	GenerateStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error)
}

type CodexProvider struct {
	client   StructuredClient
	auditor  TutorOutputAuditor
	schemas  map[string]*jsonschema.Schema
	rawFiles fs.FS
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
	return &CodexProvider{client: client, auditor: auditor, schemas: schemas, rawFiles: aioutputs.Files}, nil
}

func (provider *CodexProvider) AnalyzeAnswer(ctx context.Context, request AnalyzeAnswerRequest) (AnalyzeAnswerResult, error) {
	var result AnalyzeAnswerResult
	if err := provider.generate(ctx, PurposeAnswerAnalysis, "analyze_answer.schema.json", request, "Analyze the answer. Return diagnosis only; never mutate mastery or planning state.", "", "", &result); err != nil {
		return AnalyzeAnswerResult{}, err
	}
	return result, nil
}

func (provider *CodexProvider) GenerateTurn(ctx context.Context, request GenerateTurnRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeSocraticTurn, request, "Generate exactly the Tutor action authorized by the server decision. Do not reveal the original answer.")
}

func (provider *CodexProvider) GenerateAnalogy(ctx context.Context, request AnalogyRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeSocraticTurn, GenerateTurnRequest(request), "Generate a short life analogy without revealing the original answer.")
}

func (provider *CodexProvider) GenerateParallelExample(ctx context.Context, request ExampleRequest) (TutorTurn, error) {
	return provider.generateTurn(ctx, PurposeExplanation, GenerateTurnRequest(request), "Explain with a parallel example using different values. Do not solve the original question.")
}

func (provider *CodexProvider) GenerateExplanation(ctx context.Context, request ExplainRequest) (Explanation, error) {
	return provider.generateTurn(ctx, PurposeExplanation, GenerateTurnRequest(request), "Give a concise explanation or voice script using a parallel example. Do not reveal the original answer unless the server explicitly authorizes it.")
}

const turnStyleInstructions = "Write the message in warm, conversational Chinese for a primary or junior-secondary student. Keep it brief. Plain text only: never use markdown syntax such as headings, asterisks, bullet or dash list markers."

func (provider *CodexProvider) generateTurn(ctx context.Context, purpose Purpose, request GenerateTurnRequest, instructions string) (TutorTurn, error) {
	if provider.auditor == nil {
		return TutorTurn{}, ErrTutorOutputAuditorUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, tutorOutputOperationTimeout)
	defer cancel()
	instructions = instructions + " " + turnStyleInstructions
	requiredAction := string(request.TutorDecision.NextState)
	if requiredAction != "" {
		instructions = fmt.Sprintf(`%s Set the "action" output field to exactly %q.`, instructions, requiredAction)
	}
	var turn TutorTurn
	if err := provider.generate(ctx, purpose, "tutor_turn.schema.json", request, instructions, request.PreviousResponseID, requiredAction, &turn); err != nil {
		return TutorTurn{}, err
	}
	if err := provider.auditor.AuditTutorOutput(ctx, TutorOutputAuditRequest{
		StudentID: request.StudentID, SessionID: request.SessionID, Question: request.Question,
		PrivateAnswer: request.AuditPrivateAnswer, Candidate: turn, GeneratorResponseID: turn.ResponseID,
	}); err != nil {
		return TutorTurn{}, err
	}
	return turn, nil
}

func (provider *CodexProvider) generate(ctx context.Context, purpose Purpose, schemaFile string, input any, instructions, previousResponseID, requiredAction string, target any) error {
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
		RequestID: uuid.NewString(), StudentID: studentID(input), SessionID: sessionID(input),
		Purpose: purpose, Instructions: instructions, Input: payload,
		SchemaName: schemaFile, Schema: schemaBytes, PreviousResponseID: previousResponseID,
	})
	if err != nil {
		return err
	}
	var untyped any
	if err := json.Unmarshal(result.OutputJSON, &untyped); err != nil {
		return fmt.Errorf("decode structured output: %w", err)
	}
	if err := provider.schemas[schemaFile].Validate(untyped); err != nil {
		return fmt.Errorf("validate structured output: %w", err)
	}
	if err := json.Unmarshal(result.OutputJSON, target); err != nil {
		return fmt.Errorf("decode typed output: %w", err)
	}
	if turn, ok := target.(*TutorTurn); ok {
		turn.ResponseID = result.ResponseID
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
