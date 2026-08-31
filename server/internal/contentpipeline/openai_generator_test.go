package contentpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

type generationStructuredClient struct {
	output  string
	request ai.StructuredRequest
}

func (client *generationStructuredClient) GenerateStructured(_ context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	client.request = request
	return ai.StructuredResult{ResponseID: "resp-generation-1", OutputJSON: json.RawMessage(client.output)}, nil
}

func TestOpenAIGeneratorUsesStrictSchemaAndServerCurriculumContext(t *testing.T) {
	client := &generationStructuredClient{output: `{
		"questions":[{
			"prompt_public":"一张票价未知，三张票和服务费共36元。请列出等量关系。",
			"answer":"3x+6=36","numeric_value":null,"unit":"",
			"solution":"设票价为x元，因此等量关系是3x+6=36。","solution_result":null,
			"misconceptions":["忽略固定服务费"],"choices":[]
		}]
	}`}
	generator, err := NewOpenAIGenerator(client, "openai", "generator-v1")
	if err != nil {
		t.Fatal(err)
	}
	input := GenerationContext{
		KnowledgePointID: "30000000-0000-4000-8000-000000000001",
		SubjectCode:      "MATH", KnowledgePointName: "一元一次方程", GradeBandCode: "JUNIOR_SECONDARY",
		Difficulty: "L2", QuestionType: "FREE_TEXT", Count: 1,
		SourceName: "V1 原创示范课程", SourceType: "INTERNAL_RULE", SourceLicense: "INTERNAL-ORIGINAL",
	}
	result, evidence, err := generator.Generate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Questions) != 1 || evidence.RequestID != "resp-generation-1" {
		t.Fatalf("result=%+v evidence=%+v", result, evidence)
	}
	if client.request.Purpose != ai.PurposeContentGeneration || client.request.SchemaName != contentGenerationSchemaFile {
		t.Fatalf("structured request=%+v", client.request)
	}
	if !strings.Contains(string(client.request.Input), `"knowledge_point_name":"一元一次方程"`) || !strings.Contains(string(client.request.Input), `"source_license":"INTERNAL-ORIGINAL"`) {
		t.Fatalf("trusted curriculum context missing: %s", client.request.Input)
	}
	if strings.Contains(string(client.request.Input), "question_id") || strings.Contains(string(client.request.Input), "status") {
		t.Fatalf("server-owned asset fields leaked into model authority: %s", client.request.Input)
	}
}

func TestOpenAIGeneratorRejectsMalformedStructuredOutput(t *testing.T) {
	client := &generationStructuredClient{output: `{
		"questions":[{
			"prompt_public":"题目","answer":"答案","numeric_value":null,"unit":"",
			"solution":"答案","solution_result":null,"misconceptions":[],"choices":[],
			"status":"RELEASED"
		}]
	}`}
	generator, err := NewOpenAIGenerator(client, "openai", "generator-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := generator.Generate(context.Background(), GenerationContext{Count: 1}); err == nil || !strings.Contains(err.Error(), "validate content generation output") {
		t.Fatalf("malformed generation output accepted: %v", err)
	}
}
