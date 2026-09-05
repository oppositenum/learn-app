package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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

func TestParentInterventionStudentLockDoesNotDeadlockSessionEvent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	parents := parent.NewRepository(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})

	sessionTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sessionTx.Rollback(ctx) }()
	if _, err := sessionTx.Exec(ctx, `SET LOCAL lock_timeout = '500ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionTx.Exec(ctx, `SELECT id FROM learning_sessions WHERE id=$1 FOR UPDATE`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}

	interventionDone := make(chan int, 1)
	go func() {
		payload, _ := json.Marshal(map[string]string{"type": "ENCOURAGEMENT"})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/parent/child/"+fixture.studentID.String()+"/interventions", bytes.NewReader(payload)).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+fixture.parentToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		interventionDone <- response.Code
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case status := <-interventionDone:
			t.Fatalf("parent intervention returned before waiting for the session lock: status=%d", status)
		default:
		}
		var lockedStudentID uuid.UUID
		err := pool.QueryRow(ctx, `SELECT id FROM students WHERE id=$1 FOR NO KEY UPDATE NOWAIT`, fixture.studentID).Scan(&lockedStudentID)
		if err == nil {
			if time.Now().After(deadline) {
				t.Fatal("parent intervention did not acquire the student serialization lock")
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
			t.Fatalf("probe student serialization lock: %v", err)
		}
		break
	}

	if _, err := sessionTx.Exec(ctx, `
INSERT INTO tutor_events(id,session_id,student_id,sequence,type,student_payload_json,parent_payload_json)
VALUES($1,$2,$3,999,'SESSION_PAUSED','{}','{}')`, uuid.New(), fixture.sessionID, fixture.studentID); err != nil {
		t.Fatalf("session event foreign key was blocked by production student lock: %v", err)
	}
	if err := sessionTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case status := <-interventionDone:
		if status != http.StatusOK {
			t.Fatalf("parent intervention status=%d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent intervention did not finish after the session lock was released")
	case <-ctx.Done():
		t.Fatalf("parent intervention context ended: %v", ctx.Err())
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
	if _, err := pool.Exec(ctx, `UPDATE learning_plans SET status='ACTIVE' WHERE student_id=$1 AND plan_date=current_date AND status='PROPOSED'`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	preferences := performJSON(router, http.MethodPut, "/api/v1/parent/child/"+fixture.studentID.String()+"/preferences", fixture.parentToken, map[string]any{"daily_minutes": 20, "priority_subject_codes": []string{"ENGLISH"}, "review_only": true, "reduce_intensity": false})
	if preferences.Code != http.StatusOK {
		t.Fatalf("preferences=%d %s", preferences.Code, preferences.Body.String())
	}
	firstUpdate := decodePreferenceUpdate(t, preferences)
	if !firstUpdate.PlanUpdated || firstUpdate.TodayPreserved || firstUpdate.AppliesFrom == "" {
		t.Fatalf("preferences update contract=%+v", firstUpdate)
	}
	secondPreferences := performJSON(router, http.MethodPut, "/api/v1/parent/child/"+fixture.studentID.String()+"/preferences", fixture.parentToken, map[string]any{"daily_minutes": 25, "priority_subject_codes": []string{"MATH"}, "review_only": true, "reduce_intensity": false})
	if secondPreferences.Code != http.StatusOK {
		t.Fatalf("second preferences=%d %s", secondPreferences.Code, secondPreferences.Body.String())
	}
	secondUpdate := decodePreferenceUpdate(t, secondPreferences)
	if !secondUpdate.PlanUpdated || secondUpdate.TodayPreserved || secondUpdate.AppliesFrom != firstUpdate.AppliesFrom {
		t.Fatalf("second preferences update contract=%+v first=%+v", secondUpdate, firstUpdate)
	}
	var replaced, proposed, reviewBlocks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='REPLACED'),count(*) FILTER(WHERE status='PROPOSED') FROM learning_plans WHERE student_id=$1 AND plan_date=current_date`, fixture.studentID).Scan(&replaced, &proposed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id WHERE p.student_id=$1 AND p.plan_date=current_date AND p.status='PROPOSED' AND b.mode='REVIEW'`, fixture.studentID).Scan(&reviewBlocks); err != nil {
		t.Fatal(err)
	}
	if replaced != 2 || proposed != 1 || reviewBlocks == 0 {
		t.Fatalf("plan revisions replaced=%d proposed=%d review_blocks=%d", replaced, proposed, reviewBlocks)
	}
}

func TestParentPreferencesPreserveStartedTodayPlan(t *testing.T) {
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
	today := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil)
	var payload struct {
		LearningDate string `json:"learning_date"`
		Plans        []struct {
			ID     uuid.UUID `json:"id"`
			Blocks []struct {
				ID uuid.UUID `json:"id"`
			} `json:"blocks"`
		} `json:"plans"`
	}
	if today.Code != http.StatusOK || json.Unmarshal(today.Body.Bytes(), &payload) != nil || len(payload.Plans) != 1 || len(payload.Plans[0].Blocks) == 0 {
		t.Fatalf("today=%d %s", today.Code, today.Body.String())
	}
	learningDay, err := time.Parse("2006-01-02", payload.LearningDate)
	if err != nil {
		t.Fatal(err)
	}
	tomorrowPlan, err := plannerService.Ensure(ctx, fixture.studentID, learningDay.AddDate(0, 0, 1), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	blockID := payload.Plans[0].Blocks[0].ID
	started := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	var session classroom.StudentSession
	if started.Code != http.StatusOK || json.Unmarshal(started.Body.Bytes(), &session) != nil {
		t.Fatalf("start=%d %s", started.Code, started.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='COMPLETED',ended_at=now() WHERE id=$1`, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_plan_blocks SET status='COMPLETED' WHERE id=$1`, blockID); err != nil {
		t.Fatal(err)
	}

	preferences := performJSON(router, http.MethodPut, "/api/v1/parent/child/"+fixture.studentID.String()+"/preferences", fixture.parentToken, map[string]any{"daily_minutes": 20, "priority_subject_codes": []string{"ENGLISH"}, "review_only": true, "reduce_intensity": false})
	if preferences.Code != http.StatusOK {
		t.Fatalf("preferences=%d %s", preferences.Code, preferences.Body.String())
	}
	update := decodePreferenceUpdate(t, preferences)
	expectedAppliesFrom := learningDay.AddDate(0, 0, 1).Format("2006-01-02")
	if !update.PlanUpdated || !update.TodayPreserved || update.AppliesFrom != expectedAppliesFrom {
		t.Fatalf("preserved plan update contract=%+v expected applies_from=%s", update, expectedAppliesFrom)
	}
	var planStatus, blockStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plans WHERE id=$1`, payload.Plans[0].ID).Scan(&planStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&blockStatus); err != nil {
		t.Fatal(err)
	}
	if planStatus != "ACTIVE" || blockStatus != "COMPLETED" {
		t.Fatalf("started history changed: plan=%s block=%s", planStatus, blockStatus)
	}
	var oldTomorrowStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plans WHERE id=$1`, tomorrowPlan.ID).Scan(&oldTomorrowStatus); err != nil {
		t.Fatal(err)
	}
	var newTomorrowID uuid.UUID
	var targetMinutes, reviewBlocks int
	if err := pool.QueryRow(ctx, `SELECT id,target_minutes FROM learning_plans WHERE student_id=$1 AND plan_date=$2 AND status='PROPOSED'`, fixture.studentID, learningDay.AddDate(0, 0, 1)).Scan(&newTomorrowID, &targetMinutes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks WHERE plan_id=$1 AND mode='REVIEW'`, newTomorrowID).Scan(&reviewBlocks); err != nil {
		t.Fatal(err)
	}
	if oldTomorrowStatus != "REPLACED" || newTomorrowID == tomorrowPlan.ID || targetMinutes != 20 || reviewBlocks == 0 {
		t.Fatalf("tomorrow plan was not rebuilt from current preferences: old_status=%s old=%s new=%s minutes=%d review_blocks=%d", oldTomorrowStatus, tomorrowPlan.ID, newTomorrowID, targetMinutes, reviewBlocks)
	}
}
