package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const contentGenerationSchemaFile = "content_generation.schema.json"

var (
	ErrGeneratorUnavailable     = errors.New("content generator is unavailable")
	ErrInvalidGenerationRequest = errors.New("invalid content generation request")
)

type GenerateRequest struct {
	KnowledgePointID string `json:"knowledge_point_id"`
	SourceID         string `json:"source_id"`
	Difficulty       string `json:"difficulty"`
	QuestionType     string `json:"question_type"`
	Count            int    `json:"count"`
	Requirements     string `json:"requirements"`
}

type KnowledgePointOption struct {
	ID                   string `json:"id"`
	SubjectCode          string `json:"subject_code"`
	SubjectName          string `json:"subject_name"`
	KnowledgePointCode   string `json:"knowledge_point_code"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	GradeBandCode        string `json:"grade_band_code"`
	GradeBandName        string `json:"grade_band_name"`
	DomainCode           string `json:"domain_code"`
	DomainName           string `json:"domain_name"`
	UnitCode             string `json:"unit_code"`
	UnitName             string `json:"unit_name"`
	DefaultDifficulty    string `json:"default_difficulty"`
	CurriculumSourceName string `json:"curriculum_source_name"`
	CurriculumSourceRef  string `json:"curriculum_source_ref"`
}

type SourceOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceType  string `json:"source_type"`
	LicenseCode string `json:"license_code"`
	Attribution string `json:"attribution"`
}

type GenerationOptions struct {
	GeneratorAvailable bool                   `json:"generator_available"`
	KnowledgePoints    []KnowledgePointOption `json:"knowledge_points"`
	Sources            []SourceOption         `json:"sources"`
}

type GenerationContext struct {
	KnowledgePointID     string          `json:"knowledge_point_id"`
	SubjectCode          string          `json:"subject_code"`
	SubjectName          string          `json:"subject_name"`
	KnowledgePointCode   string          `json:"knowledge_point_code"`
	KnowledgePointName   string          `json:"knowledge_point_name"`
	KnowledgeDescription string          `json:"knowledge_description"`
	GradeBandCode        string          `json:"grade_band_code"`
	DomainName           string          `json:"domain_name"`
	UnitName             string          `json:"unit_name"`
	CurriculumSourceName string          `json:"curriculum_source_name"`
	CurriculumSourceRef  string          `json:"curriculum_source_ref"`
	WhyItMatters         json.RawMessage `json:"why_it_matters"`
	Difficulty           string          `json:"difficulty"`
	QuestionType         string          `json:"question_type"`
	Count                int             `json:"count"`
	Requirements         string          `json:"requirements"`
	SourceName           string          `json:"source_name"`
	SourceType           string          `json:"source_type"`
	SourceLicense        string          `json:"source_license"`
	SourceAttribution    string          `json:"source_attribution"`
}

type GeneratedChoice struct {
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

type GeneratedQuestion struct {
	PromptPublic   string            `json:"prompt_public"`
	Answer         string            `json:"answer"`
	NumericValue   *float64          `json:"numeric_value"`
	Unit           string            `json:"unit"`
	Solution       string            `json:"solution"`
	SolutionResult *float64          `json:"solution_result"`
	Misconceptions []string          `json:"misconceptions"`
	Choices        []GeneratedChoice `json:"choices"`
}

type GenerationResult struct {
	Questions []GeneratedQuestion `json:"questions"`
}

type ContentGenerator interface {
	Generate(ctx context.Context, input GenerationContext) (GenerationResult, GenerationMetadata, error)
}

type OpenAIGenerator struct {
	client   ai.StructuredClient
	provider string
	model    string
	schema   *jsonschema.Schema
	raw      json.RawMessage
}

func NewOpenAIGenerator(client ai.StructuredClient, provider, model string) (*OpenAIGenerator, error) {
	if client == nil {
		return nil, errors.New("structured generation client is required")
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return nil, errors.New("generation provider and model are required")
	}
	raw, err := fs.ReadFile(aioutputs.Files, contentGenerationSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("read content generation schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode content generation schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(contentGenerationSchemaFile, document); err != nil {
		return nil, fmt.Errorf("register content generation schema: %w", err)
	}
	compiled, err := compiler.Compile(contentGenerationSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("compile content generation schema: %w", err)
	}
	return &OpenAIGenerator{client: client, provider: provider, model: model, schema: compiled, raw: raw}, nil
}

// contentGenerationMaxAttempts bounds provider calls for one generation.
// Output the provider returned successfully but this service could not accept
// — invalid JSON, or JSON the schema rejects — is worth one more sample, and a
// second failure fails closed. Every attempt is a real, separately priced and
// separately accounted call, so this stays small on purpose.
const contentGenerationMaxAttempts = 2

func (generator *OpenAIGenerator) Generate(ctx context.Context, input GenerationContext) (GenerationResult, GenerationMetadata, error) {
	var lastErr error
	for attempt := 1; attempt <= contentGenerationMaxAttempts; attempt++ {
		// The caller owns the deadline. Content generation is an offline
		// pipeline and must not borrow the classroom submit budget.
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return GenerationResult{}, GenerationMetadata{}, lastErr
			}
			return GenerationResult{}, GenerationMetadata{}, err
		}
		result, metadata, err := generator.generateAttempt(ctx, input)
		if err == nil {
			return result, metadata, nil
		}
		lastErr = err
		// Only a reply this service refused is resampled. A failure building
		// our own request would repeat identically, and provider-side HTTP
		// failures are deliberately left unretried on this path for now.
		if !errors.Is(err, ai.ErrInvalidProviderOutput) {
			return GenerationResult{}, GenerationMetadata{}, err
		}
		// No backoff: the provider is healthy, and an offline pipeline gains
		// nothing from waiting. Scheduling belongs to the caller.
	}
	return GenerationResult{}, GenerationMetadata{}, lastErr
}

func (generator *OpenAIGenerator) generateAttempt(ctx context.Context, input GenerationContext) (GenerationResult, GenerationMetadata, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return GenerationResult{}, GenerationMetadata{}, fmt.Errorf("encode generation context: %w", err)
	}
	requestID := uuid.NewString()
	result, err := generator.client.GenerateStructured(ctx, ai.StructuredRequest{
		RequestID: requestID,
		Purpose:   ai.PurposeContentGeneration,
		Instructions: strings.Join([]string{
			"Create original educational assessment questions for the supplied released curriculum context.",
			"Return exactly the requested number and requested question type. Use an age-appropriate real-world setting without copying commercial question banks or named third-party materials.",
			"The public prompt must not reveal the answer. The private solution must include the exact answer text. For numeric answers, numeric_value and solution_result must be equal; use null for both when the answer is not numeric.",
			"For PHYSICS or CHEMISTRY numeric answers, provide the expected unit. FREE_TEXT must return an empty choices array. MULTIPLE_CHOICE must return four distinct choices with exactly one correct choice.",
			"Do not create identifiers, provenance, status, validation, review, release, mastery, pricing, or runtime fields; the server owns those fields.",
		}, " "),
		Input:      payload,
		SchemaName: contentGenerationSchemaFile,
		Schema:     generator.raw,
	})
	if err != nil {
		return GenerationResult{}, GenerationMetadata{}, err
	}
	var untyped any
	if err := json.Unmarshal(result.OutputJSON, &untyped); err != nil {
		return GenerationResult{}, GenerationMetadata{}, fmt.Errorf("%w: decode content generation output: %v", ai.ErrInvalidProviderOutput, err)
	}
	if err := generator.schema.Validate(untyped); err != nil {
		return GenerationResult{}, GenerationMetadata{}, fmt.Errorf("%w: validate content generation output: %v", ai.ErrInvalidProviderOutput, err)
	}
	var generated GenerationResult
	if err := json.Unmarshal(result.OutputJSON, &generated); err != nil {
		return GenerationResult{}, GenerationMetadata{}, fmt.Errorf("%w: decode typed content generation: %v", ai.ErrInvalidProviderOutput, err)
	}
	providerRequestID := result.ResponseID
	if providerRequestID == "" {
		providerRequestID = requestID
	}
	return generated, GenerationMetadata{Provider: generator.provider, Model: generator.model, RequestID: providerRequestID}, nil
}

func validateGenerateRequest(request GenerateRequest) error {
	if _, err := uuid.Parse(request.KnowledgePointID); err != nil {
		return fmt.Errorf("%w: invalid knowledge point", ErrInvalidGenerationRequest)
	}
	if _, err := uuid.Parse(request.SourceID); err != nil {
		return fmt.Errorf("%w: invalid content source", ErrInvalidGenerationRequest)
	}
	if !validDifficulties[request.Difficulty] {
		return fmt.Errorf("%w: difficulty must be L0 through L5", ErrInvalidGenerationRequest)
	}
	if request.QuestionType != "FREE_TEXT" && request.QuestionType != "MULTIPLE_CHOICE" {
		return fmt.Errorf("%w: unsupported question type", ErrInvalidGenerationRequest)
	}
	if request.Count < 1 || request.Count > 5 {
		return fmt.Errorf("%w: count must be between 1 and 5", ErrInvalidGenerationRequest)
	}
	if utf8.RuneCountInString(strings.TrimSpace(request.Requirements)) > 500 {
		return fmt.Errorf("%w: requirements exceed 500 characters", ErrInvalidGenerationRequest)
	}
	return nil
}

func generatedAsset(input GenerationContext, sourceID string, generated GeneratedQuestion, index int) (Asset, error) {
	choices := make([]Choice, 0, len(generated.Choices))
	for choiceIndex, item := range generated.Choices {
		choices = append(choices, Choice{ID: string(rune('A' + choiceIndex)), Text: strings.TrimSpace(item.Text), Correct: item.Correct})
	}
	inputSchema := json.RawMessage(`{"type":"string","minLength":1,"maxLength":2000}`)
	if input.QuestionType == "MULTIPLE_CHOICE" {
		ids := make([]string, 0, len(choices))
		for _, item := range choices {
			ids = append(ids, item.ID)
		}
		raw, err := json.Marshal(map[string]any{"type": "string", "enum": ids})
		if err != nil {
			return Asset{}, fmt.Errorf("encode choice input schema: %w", err)
		}
		inputSchema = raw
	}
	return Asset{
		QuestionID:       uuid.NewString(),
		KnowledgePointID: input.KnowledgePointID,
		SubjectCode:      input.SubjectCode,
		Difficulty:       input.Difficulty,
		QuestionType:     input.QuestionType,
		PromptPublic:     strings.TrimSpace(generated.PromptPublic),
		TeacherPrivate: PrivateAnswer{
			Answer: strings.TrimSpace(generated.Answer), NumericValue: generated.NumericValue,
			Unit: strings.TrimSpace(generated.Unit), Solution: strings.TrimSpace(generated.Solution),
			SolutionResult: generated.SolutionResult, Misconceptions: generated.Misconceptions,
		},
		Choices:        choices,
		InputSchema:    inputSchema,
		SourceID:       sourceID,
		ContentVersion: fmt.Sprintf("ai-generated-v1-%d", index+1),
		SchemaVersion:  CurrentSchemaVersion,
		Status:         Draft,
	}, nil
}
