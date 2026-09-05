package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type preferenceUpdateResponse struct {
	Saved                   bool   `json:"saved"`
	PlanUpdated             bool   `json:"plan_updated"`
	TodayPreserved          bool   `json:"today_preserved"`
	AppliesFrom             string `json:"applies_from"`
	AnswerControlsAvailable bool   `json:"answer_controls_available"`
}

func decodePreferenceUpdate(t *testing.T, response *httptest.ResponseRecorder) preferenceUpdateResponse {
	t.Helper()
	var payload preferenceUpdateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode preference update: %v; body=%s", err, response.Body.String())
	}
	return payload
}

func TestParentEnabledSubjectsControlStudentPlan(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	parents := parent.NewRepository(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, hub, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})
	preferencesPath := "/api/v1/parent/child/" + fixture.studentID.String() + "/preferences"

	unconfigured := performJSON(router, http.MethodGet, preferencesPath, fixture.parentToken, nil)
	if unconfigured.Code != http.StatusOK || !strings.Contains(unconfigured.Body.String(), `"configured":false`) {
		t.Fatalf("unconfigured read=%d %s", unconfigured.Code, unconfigured.Body.String())
	}
	for _, code := range []string{"MATH", "CHINESE", "ENGLISH", "PHYSICS", "CHEMISTRY"} {
		if !strings.Contains(unconfigured.Body.String(), code) {
			t.Fatalf("unconfigured read should expand to all subjects, missing %s: %s", code, unconfigured.Body.String())
		}
	}

	invalid := performJSON(router, http.MethodPut, preferencesPath, fixture.parentToken, map[string]any{"daily_minutes": 30, "priority_subject_codes": []string{}, "review_only": false, "reduce_intensity": false, "enabled_subject_codes": []string{"MUSIC"}})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unknown subject code accepted: %d %s", invalid.Code, invalid.Body.String())
	}
	empty := performJSON(router, http.MethodPut, preferencesPath, fixture.parentToken, map[string]any{"daily_minutes": 30, "priority_subject_codes": []string{}, "review_only": false, "reduce_intensity": false, "enabled_subject_codes": []string{}})
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty enabled set accepted: %d %s", empty.Code, empty.Body.String())
	}

	mathOnly := performJSON(router, http.MethodPut, preferencesPath, fixture.parentToken, map[string]any{"daily_minutes": 30, "priority_subject_codes": []string{}, "review_only": false, "reduce_intensity": false, "enabled_subject_codes": []string{"MATH"}})
	if mathOnly.Code != http.StatusOK {
		t.Fatalf("math-only preferences=%d %s", mathOnly.Code, mathOnly.Body.String())
	}
	update := decodePreferenceUpdate(t, mathOnly)
	if !update.Saved || !update.PlanUpdated || update.TodayPreserved || update.AppliesFrom == "" || update.AnswerControlsAvailable || strings.Contains(mathOnly.Body.String(), "plan_replaced") {
		t.Fatalf("math-only update contract=%+v body=%s", update, mathOnly.Body.String())
	}
	if response := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil); response.Code != http.StatusOK {
		t.Fatalf("student plan=%d %s", response.Code, response.Body.String())
	}
	var blockCount, mathBlocks int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE s.code='MATH') FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id JOIN subjects s ON s.id=b.subject_id WHERE p.student_id=$1 AND p.plan_date=current_date AND p.status='PROPOSED'`, fixture.studentID).Scan(&blockCount, &mathBlocks); err != nil {
		t.Fatal(err)
	}
	if blockCount != 1 || mathBlocks != 1 {
		t.Fatalf("math-only plan blocks=%d math=%d", blockCount, mathBlocks)
	}
	t.Logf("verified math-only plan content: blockCount=%d mathBlocks=%d", blockCount, mathBlocks)

	saved := performJSON(router, http.MethodGet, preferencesPath, fixture.parentToken, nil)
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"enabled_subject_codes":["MATH"]`) || !strings.Contains(saved.Body.String(), `"configured":true`) {
		t.Fatalf("saved read=%d %s", saved.Code, saved.Body.String())
	}

	intervention := performJSON(router, http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/interventions", fixture.parentToken, map[string]any{"type": "STATE_NOT_GOOD"})
	if intervention.Code != http.StatusOK {
		t.Fatalf("intervention=%d %s", intervention.Code, intervention.Body.String())
	}
	var enabled []string
	if err := pool.QueryRow(ctx, `SELECT enabled_subject_codes FROM parent_preferences WHERE parent_user_id=$1 AND student_id=$2`, fixture.parentUserID, fixture.studentID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0] != "MATH" {
		t.Fatalf("intervention clobbered enabled subjects: %v", enabled)
	}
}
