package contentpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

type reviewStructuredClient struct {
	output  string
	request ai.StructuredRequest
}

func (client *reviewStructuredClient) GenerateStructured(_ context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	client.request = request
	return ai.StructuredResult{ResponseID: "resp-review-1", OutputJSON: json.RawMessage(client.output)}, nil
}

func TestOpenAIReviewerUsesStrictSchemaAndPrivateServerAsset(t *testing.T) {
	client := &reviewStructuredClient{output: `{
		"result":"PASS","age_appropriate":true,"factually_sound":true,
		"unambiguous":true,"no_answer_leak":true,"safe_values":true,"findings":[]
	}`}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	review, evidence, err := reviewer.Review(context.Background(), validAsset())
	if err != nil {
		t.Fatal(err)
	}
	if review.Result != ReviewPass || evidence.RequestID != "resp-review-1" {
		t.Fatalf("review=%+v evidence=%+v", review, evidence)
	}
	if client.request.Purpose != ai.PurposeContentReview || client.request.SchemaName != contentReviewSchemaFile {
		t.Fatalf("structured request=%+v", client.request)
	}
	if !strings.Contains(string(client.request.Input), `"teacher_private"`) {
		t.Fatalf("reviewer did not receive private server asset: %s", client.request.Input)
	}
}

func TestOpenAIReviewerRejectsMalformedStructuredOutput(t *testing.T) {
	client := &reviewStructuredClient{output: `{
		"result":"PASS","age_appropriate":true,"factually_sound":true,
		"unambiguous":true,"no_answer_leak":true,"safe_values":true,"findings":[],"forged_release":true
	}`}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reviewer.Review(context.Background(), validAsset()); err == nil || !strings.Contains(err.Error(), "validate content review output") {
		t.Fatalf("malformed review accepted: %v", err)
	}
}
