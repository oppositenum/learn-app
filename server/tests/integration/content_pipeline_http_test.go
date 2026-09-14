package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type passingContentReviewer struct{}

func (passingContentReviewer) Review(context.Context, contentpipeline.Asset) (contentpipeline.Review, contentpipeline.ReviewEvidence, error) {
	return contentpipeline.Review{
		Result: contentpipeline.ReviewPass, AgeAppropriate: true, FactuallySound: true,
		Unambiguous: true, NoAnswerLeak: true, SafeValues: true,
	}, contentpipeline.ReviewEvidence{Provider: "openai", Model: "reviewer-v1", RequestID: "review-response-1"}, nil
}

func TestOwnerContentPipelineCannotForgeOrSkipReleaseGates(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	ownerToken := seedOwner(t, ctx, pool)
	reviewService, err := contentpipeline.NewReviewService("codex:generator-v1", "openai:reviewer-v1", passingContentReviewer{})
	if err != nil {
		t.Fatal(err)
	}
	pipeline := contentpipeline.NewService(contentpipeline.NewRepository(pool), contentpipeline.Validator{}, reviewService)
	router := api.NewRouter(api.Dependencies{
		Authenticate:    auth.NewSessionAuthenticator(pool).Middleware,
		ContentPipeline: contentpipeline.NewHandler(pipeline),
	})

	asset := pipelineAsset(uuid.New())
	forged := map[string]any{
		"asset": asset, "generator": map[string]any{"provider": "codex", "model": "generator-v1"},
		"validation_passed": true,
	}
	response := performJSON(router, http.MethodPost, "/api/v1/owner/content/drafts", ownerToken, forged)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("forged import status=%d body=%s", response.Code, response.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM questions WHERE id=$1`, asset.QuestionID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("forged import persisted count=%d err=%v", count, err)
	}

	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/drafts", ownerToken, map[string]any{
		"asset": asset, "generator": map[string]any{"provider": "codex", "model": "generator-v1", "request_id": "generation-1"},
	})
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"status":"DRAFT"`) {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	questionID := uuid.MustParse(asset.QuestionID)
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.Draft)

	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/release", ownerToken, map[string]any{"reason": "attempted bypass"})
	if response.Code != http.StatusConflict {
		t.Fatalf("DRAFT release bypass status=%d body=%s", response.Code, response.Body.String())
	}
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.Draft)

	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/validate", ownerToken, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"AUTOMATIC_VALIDATED"`) {
		t.Fatalf("validate status=%d body=%s", response.Code, response.Body.String())
	}
	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/release", ownerToken, map[string]any{"reason": "still bypassing review"})
	if response.Code != http.StatusConflict {
		t.Fatalf("AUTOMATIC_VALIDATED release bypass status=%d body=%s", response.Code, response.Body.String())
	}
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.AutomaticValidated)

	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/review", ownerToken, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"AI_REVIEWED"`) {
		t.Fatalf("review status=%d body=%s", response.Code, response.Body.String())
	}
	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/release", ownerToken, map[string]any{"reason": "all executable gates passed"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"RELEASED"`) {
		t.Fatalf("release status=%d body=%s", response.Code, response.Body.String())
	}
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.Released)

	response = performJSON(router, http.MethodPost, "/api/v1/owner/content/"+questionID.String()+"/quarantine", ownerToken, map[string]any{"reason": "wording disputed"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"QUARANTINED"`) {
		t.Fatalf("quarantine status=%d body=%s", response.Code, response.Body.String())
	}
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.Quarantined)

	var releases int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM content_release_records WHERE question_id=$1 AND actor_user_id IS NOT NULL`, questionID).Scan(&releases); err != nil || releases != 2 {
		t.Fatalf("audited transitions=%d err=%v", releases, err)
	}
}

func TestContentReviewMustBeIndependentFromImportedGenerator(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	reviewService, err := contentpipeline.NewReviewService("codex:generator-v1", "openai:reviewer-v1", passingContentReviewer{})
	if err != nil {
		t.Fatal(err)
	}
	service := contentpipeline.NewService(contentpipeline.NewRepository(pool), contentpipeline.Validator{}, reviewService)
	asset := pipelineAsset(uuid.New())
	if err := service.ImportDraft(ctx, asset, contentpipeline.GenerationMetadata{Provider: "openai", Model: "reviewer-v1"}); err != nil {
		t.Fatal(err)
	}
	questionID := uuid.MustParse(asset.QuestionID)
	if _, _, _, err := service.Validate(ctx, questionID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := service.Review(ctx, questionID); err == nil || !strings.Contains(err.Error(), "differ from generator") {
		t.Fatalf("same generator/reviewer was accepted: %v", err)
	}
	assertPipelineStatus(t, ctx, pool, questionID, contentpipeline.AutomaticValidated)
}

func pipelineAsset(id uuid.UUID) contentpipeline.Asset {
	return contentpipeline.Asset{
		QuestionID:       id.String(),
		KnowledgePointID: "30000000-0000-4000-8000-000000000001",
		SubjectCode:      "MATH", Difficulty: "L2", QuestionType: "FREE_TEXT",
		PromptPublic: "小航用4个相同盒子装完一批卡片。请写出求每盒数量的运算思路。",
		TeacherPrivate: contentpipeline.PrivateAnswer{
			Answer: "总数除以4", Solution: "运算是总数除以4，也就是用卡片总数除以盒子数。",
			Misconceptions: []string{"MULTIPLIES_INSTEAD_OF_DIVIDES"},
		},
		InputSchema:    json.RawMessage(`{"type":"string"}`),
		SourceID:       "50000000-0000-4000-8000-000000000001",
		ContentVersion: "owner-import-v1", SchemaVersion: contentpipeline.CurrentSchemaVersion,
		Status: contentpipeline.Draft,
	}
}

func assertPipelineStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID, want contentpipeline.Status) {
	t.Helper()
	var got contentpipeline.Status
	if err := pool.QueryRow(ctx, `SELECT status FROM questions WHERE id=$1`, questionID).Scan(&got); err != nil || got != want {
		t.Fatalf("question status=%s want=%s err=%v", got, want, err)
	}
}
