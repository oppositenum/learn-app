package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const contentReviewSchemaFile = "content_review.schema.json"

type OpenAIReviewer struct {
	client   ai.StructuredClient
	provider string
	model    string
	schema   *jsonschema.Schema
	raw      json.RawMessage
}

func NewOpenAIReviewer(client ai.StructuredClient, provider, model string) (*OpenAIReviewer, error) {
	if client == nil {
		return nil, errors.New("structured review client is required")
	}
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return nil, errors.New("review provider and model are required")
	}
	raw, err := fs.ReadFile(aioutputs.Files, contentReviewSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("read content review schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode content review schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(contentReviewSchemaFile, document); err != nil {
		return nil, fmt.Errorf("register content review schema: %w", err)
	}
	compiled, err := compiler.Compile(contentReviewSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("compile content review schema: %w", err)
	}
	return &OpenAIReviewer{client: client, provider: provider, model: model, schema: compiled, raw: raw}, nil
}

func (reviewer *OpenAIReviewer) Review(ctx context.Context, asset Asset) (Review, ReviewEvidence, error) {
	input, err := json.Marshal(asset)
	if err != nil {
		return Review{}, ReviewEvidence{}, fmt.Errorf("encode review asset: %w", err)
	}
	requestID := uuid.NewString()
	result, err := reviewer.client.GenerateStructured(ctx, ai.StructuredRequest{
		RequestID:    requestID,
		Purpose:      ai.PurposeContentReview,
		Instructions: "Act as an independent educational content safety reviewer. Check factual correctness, age appropriateness, ambiguity, public-prompt answer leakage, units and values. Return PASS only when every boolean criterion is true. Do not rewrite or publish the asset.",
		Input:        input,
		SchemaName:   contentReviewSchemaFile,
		Schema:       reviewer.raw,
	})
	if err != nil {
		return Review{}, ReviewEvidence{}, err
	}
	var untyped any
	if err := json.Unmarshal(result.OutputJSON, &untyped); err != nil {
		return Review{}, ReviewEvidence{}, fmt.Errorf("decode content review output: %w", err)
	}
	if err := reviewer.schema.Validate(untyped); err != nil {
		return Review{}, ReviewEvidence{}, fmt.Errorf("validate content review output: %w", err)
	}
	var review Review
	if err := json.Unmarshal(result.OutputJSON, &review); err != nil {
		return Review{}, ReviewEvidence{}, fmt.Errorf("decode typed content review: %w", err)
	}
	providerRequestID := result.ResponseID
	if providerRequestID == "" {
		providerRequestID = requestID
	}
	evidence := ReviewEvidence{Provider: reviewer.provider, Model: reviewer.model, RequestID: providerRequestID}
	return review, evidence, nil
}
