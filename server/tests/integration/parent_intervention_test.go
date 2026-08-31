package integration

import (
	"context"
	"net/http"
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

func TestParentDiscoversChildAndIntervenesWithoutAnswerChannel(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	hub := realtime.NewHub()
	parents := parent.NewRepository(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, hub, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})

	children := performJSON(router, http.MethodGet, "/api/v1/parent/children", fixture.parentToken, nil)
	if children.Code != http.StatusOK || !strings.Contains(children.Body.String(), fixture.studentID.String()) || !strings.Contains(children.Body.String(), fixture.sessionID.String()) {
		t.Fatalf("children=%d %s", children.Code, children.Body.String())
	}
	studentEvents, unsubscribe := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer unsubscribe()
	forged := performJSON(router, http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/interventions", fixture.parentToken, map[string]any{"type": "ENCOURAGEMENT", "answer": fixture.privateCanary})
	if forged.Code != http.StatusBadRequest {
		t.Fatalf("intervention accepted answer field: %d %s", forged.Code, forged.Body.String())
	}
	encourage := performJSON(router, http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/interventions", fixture.parentToken, map[string]any{"type": "ENCOURAGEMENT"})
	if encourage.Code != http.StatusOK || !strings.Contains(encourage.Body.String(), `"answer_controls_available":false`) {
		t.Fatalf("encouragement=%d %s", encourage.Code, encourage.Body.String())
	}
	event := <-studentEvents
	if strings.Contains(string(event), fixture.privateCanary) || strings.Contains(strings.ToLower(string(event)), "answer") || !strings.Contains(string(event), "家长在关注你的努力") {
		t.Fatalf("unsafe student intervention event: %s", event)
	}
	reduce := performJSON(router, http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/interventions", fixture.parentToken, map[string]any{"type": "STATE_NOT_GOOD"})
	if reduce.Code != http.StatusOK {
		t.Fatalf("state intervention=%d %s", reduce.Code, reduce.Body.String())
	}
	<-studentEvents
	var target int
	var engagement string
	if err := pool.QueryRow(ctx, `SELECT target_minutes,engagement_state FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&target, &engagement); err != nil || target >= 20 || engagement != "LOW" {
		t.Fatalf("session intervention target=%d engagement=%s err=%v", target, engagement, err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM parent_interventions WHERE parent_user_id=$1 AND student_id=$2`, fixture.parentUserID, fixture.studentID).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("intervention audits=%d err=%v", auditCount, err)
	}
	var dailyMinutes int
	var reviewOnly, reduceIntensity bool
	if err := pool.QueryRow(ctx, `SELECT daily_minutes,review_only,reduce_intensity FROM parent_preferences WHERE parent_user_id=$1 AND student_id=$2`, fixture.parentUserID, fixture.studentID).Scan(&dailyMinutes, &reviewOnly, &reduceIntensity); err != nil {
		t.Fatal(err)
	}
	if dailyMinutes != 30 || reviewOnly || !reduceIntensity {
		t.Fatalf("first intervention preference daily=%d review=%v reduce=%v", dailyMinutes, reviewOnly, reduceIntensity)
	}
}

func TestParentPreferencesReplaceUnstartedTodayPlan(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	parents := parent.NewRepository(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})
	if response := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil); response.Code != http.StatusOK {
		t.Fatalf("initial plan=%d %s", response.Code, response.Body.String())
	}
	preferences := performJSON(router, http.MethodPut, "/api/v1/parent/child/"+fixture.studentID.String()+"/preferences", fixture.parentToken, map[string]any{"daily_minutes": 20, "priority_subject_codes": []string{"ENGLISH"}, "review_only": true, "reduce_intensity": false})
	if preferences.Code != http.StatusOK || !strings.Contains(preferences.Body.String(), `"plan_replaced":true`) {
		t.Fatalf("preferences=%d %s", preferences.Code, preferences.Body.String())
	}
	var replaced, proposed, reviewBlocks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='REPLACED'),count(*) FILTER(WHERE status='PROPOSED') FROM learning_plans WHERE student_id=$1 AND plan_date=current_date`, fixture.studentID).Scan(&replaced, &proposed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id WHERE p.student_id=$1 AND p.plan_date=current_date AND p.status='PROPOSED' AND b.mode='REVIEW'`, fixture.studentID).Scan(&reviewBlocks); err != nil {
		t.Fatal(err)
	}
	if replaced != 1 || proposed != 1 || reviewBlocks == 0 {
		t.Fatalf("plan revisions replaced=%d proposed=%d review_blocks=%d", replaced, proposed, reviewBlocks)
	}
}
