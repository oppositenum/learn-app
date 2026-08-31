package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type generatedContentStub struct {
	input contentpipeline.GenerationContext
}

func (stub *generatedContentStub) Generate(_ context.Context, input contentpipeline.GenerationContext) (contentpipeline.GenerationResult, contentpipeline.GenerationMetadata, error) {
	stub.input = input
	return contentpipeline.GenerationResult{Questions: []contentpipeline.GeneratedQuestion{{
		PromptPublic: "三份相同的学习资料和6元装订费共36元。请写出表示每份资料价格的等量关系。",
		Answer:       "3x+6=36",
		Solution:     "设每份资料价格为x元，三份资料与装订费构成3x+6=36。",
		Misconceptions: []string{
			"忽略固定装订费",
		},
		Choices: []contentpipeline.GeneratedChoice{},
	}}}, contentpipeline.GenerationMetadata{Provider: "openai", Model: "generator-v1", RequestID: "generation-response-1"}, nil
}

func TestOwnerAIGenerationCreatesDraftWithoutReturningPrivateAnswer(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	ownerToken := seedOwner(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
INSERT INTO knowledge_points (id,subject_id,domain_id,unit_id,grade_band_code,code,name,default_difficulty,status,curriculum_version)
SELECT '72000000-0000-4000-8000-000000000001',subject_id,domain_id,unit_id,grade_band_code,
       'MATH-DRAFT-NOT-SELECTABLE','未发布知识点','L1','DRAFT','test'
FROM knowledge_points WHERE id='30000000-0000-4000-8000-000000000001';
INSERT INTO knowledge_point_sources (knowledge_point_id,curriculum_source_id,source_ref,basis_kind) VALUES
('72000000-0000-4000-8000-000000000001','60000000-0000-4000-8000-000000000001','test/draft','TASK_019_DETAIL')`); err != nil {
		t.Fatal(err)
	}
	stub := &generatedContentStub{}
	service := contentpipeline.NewService(contentpipeline.NewRepository(pool), contentpipeline.Validator{}, nil).WithGenerator(stub)
	router := api.NewRouter(api.Dependencies{
		Authenticate:    auth.NewSessionAuthenticator(pool).Middleware,
		ContentPipeline: contentpipeline.NewHandler(service),
	})

	options := performJSON(router, http.MethodGet, "/api/v1/owner/content/generation-options", ownerToken, nil)
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), `"generator_available":true`) || !strings.Contains(options.Body.String(), `"subject_code":"MATH"`) {
		t.Fatalf("generation options=%d %s", options.Code, options.Body.String())
	}
	if strings.Contains(options.Body.String(), "teacher_private") || strings.Contains(options.Body.String(), "correct_answer") {
		t.Fatalf("generation options exposed private answer fields: %s", options.Body.String())
	}
	var optionPayload struct {
		KnowledgePoints []struct {
			Code                 string `json:"knowledge_point_code"`
			Domain               string `json:"domain_name"`
			Unit                 string `json:"unit_name"`
			CurriculumSourceName string `json:"curriculum_source_name"`
		} `json:"knowledge_points"`
	}
	if err := json.Unmarshal(options.Body.Bytes(), &optionPayload); err != nil {
		t.Fatal(err)
	}
	if len(optionPayload.KnowledgePoints) != 142 {
		t.Fatalf("generation knowledge points=%d, want 142", len(optionPayload.KnowledgePoints))
	}
	for _, option := range optionPayload.KnowledgePoints {
		if option.Code == "MATH-DRAFT-NOT-SELECTABLE" {
			t.Fatal("generation options included a DRAFT knowledge point")
		}
		if option.Code == "" || option.Domain == "" || option.Unit == "" || option.CurriculumSourceName == "" {
			t.Fatalf("generation option lacks curriculum context: %+v", option)
		}
	}

	path := "/api/v1/owner/content/generate"
	requestBody := map[string]any{
		"knowledge_point_id": "30000000-0000-4000-8000-000000000001",
		"source_id":          "50000000-0000-4000-8000-000000000001",
		"difficulty":         "L2",
		"question_type":      "FREE_TEXT",
		"count":              1,
		"requirements":       "使用原创生活场景",
	}
	unauthorized := performJSON(router, http.MethodPost, path, "", requestBody)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized generation status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}
	generated := performJSON(router, http.MethodPost, path, ownerToken, requestBody)
	if generated.Code != http.StatusCreated || !strings.Contains(generated.Body.String(), `"status":"DRAFT"`) {
		t.Fatalf("generation status=%d body=%s", generated.Code, generated.Body.String())
	}
	for _, privateValue := range []string{"3x+6=36", "teacher_private", "solution", "misconceptions"} {
		if strings.Contains(generated.Body.String(), privateValue) {
			t.Fatalf("generation response exposed private value %q: %s", privateValue, generated.Body.String())
		}
	}
	if stub.input.SubjectCode != "MATH" || stub.input.SourceLicense != "INTERNAL-ORIGINAL" || stub.input.Count != 1 {
		t.Fatalf("generator received untrusted or incomplete context: %+v", stub.input)
	}

	assertGeneratedDraftPersistence(t, ctx, pool)
}

func assertGeneratedDraftPersistence(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	var status, provider, model, requestID, privateAnswer string
	var validations, reviews int
	err := pool.QueryRow(ctx, `
SELECT q.status,cv.generator_provider,cv.generator_model,cv.generator_request_id,
       qpa.correct_answer_json->>'value',
       (SELECT count(*) FROM content_validations v WHERE v.question_id=q.id),
       (SELECT count(*) FROM content_reviews r WHERE r.question_id=q.id)
FROM questions q
JOIN content_versions cv ON cv.question_id=q.id AND cv.version=q.content_version
JOIN question_private_answers qpa ON qpa.question_id=q.id
WHERE cv.generator_request_id='generation-response-1'`).Scan(&status, &provider, &model, &requestID, &privateAnswer, &validations, &reviews)
	if err != nil {
		t.Fatal(err)
	}
	if status != "DRAFT" || provider != "openai" || model != "generator-v1" || requestID != "generation-response-1" || privateAnswer != "3x+6=36" {
		t.Fatalf("draft persistence status=%s provider=%s model=%s request=%s answer=%s", status, provider, model, requestID, privateAnswer)
	}
	if validations != 0 || reviews != 0 {
		t.Fatalf("generation forged gate evidence: validations=%d reviews=%d", validations, reviews)
	}
}
