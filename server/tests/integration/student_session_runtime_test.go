package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestStudentStartsAndReadsReleasedPlanSessionWithoutPrivateAnswer(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	parents := parent.NewRepository(pool)
	classroomService := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(classroomService, pool, parents, plannerService),
	})

	today := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil)
	if today.Code != http.StatusOK {
		t.Fatalf("today=%d %s", today.Code, today.Body.String())
	}
	var payload struct {
		LearningDate string `json:"learning_date"`
		Plans        []struct {
			Date   string `json:"date"`
			Blocks []struct {
				ID uuid.UUID `json:"id"`
			} `json:"blocks"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(today.Body.Bytes(), &payload); err != nil || len(payload.Plans) != 1 || len(payload.Plans[0].Blocks) == 0 || payload.Plans[0].Blocks[0].ID == uuid.Nil {
		t.Fatalf("today plan lacks executable block: %s err=%v", today.Body.String(), err)
	}
	if payload.LearningDate == "" || payload.Plans[0].Date != payload.LearningDate || len(payload.Plans[0].Date) != len("2006-01-02") {
		t.Fatalf("today plan date contract is inconsistent: %s", today.Body.String())
	}
	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": payload.Plans[0].Blocks[0].ID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, start.Body.Bytes())
	if strings.Contains(start.Body.String(), fixture.privateCanary) || strings.Contains(strings.ToLower(start.Body.String()), "correct_answer") {
		t.Fatalf("started session leaked private answer: %s", start.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.ID == uuid.Nil || session.Prompt == "" || session.State != "ASK" || len(session.Timeline) != 1 {
		t.Fatalf("started session=%+v", session)
	}
	var lifecycleEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_events WHERE session_id=$1 AND type IN('SESSION_STARTED','QUESTION_PRESENTED')`, session.ID).Scan(&lifecycleEvents); err != nil || lifecycleEvents != 2 {
		t.Fatalf("session start lifecycle events=%d err=%v", lifecycleEvents, err)
	}
	var questionStatus, planStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM questions WHERE id=$1`, session.QuestionID).Scan(&questionStatus); err != nil || questionStatus != "RELEASED" {
		t.Fatalf("runtime question status=%s err=%v", questionStatus, err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plans WHERE id=(SELECT plan_id FROM learning_plan_blocks WHERE id=$1)`, payload.Plans[0].Blocks[0].ID).Scan(&planStatus); err != nil || planStatus != "ACTIVE" {
		t.Fatalf("plan status=%s err=%v", planStatus, err)
	}

	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.studentToken, nil)
	var readSession classroom.StudentSession
	if err := json.Unmarshal(read.Body.Bytes(), &readSession); err != nil {
		t.Fatal(err)
	}
	if read.Code != http.StatusOK || readSession.ID != session.ID || readSession.QuestionID != session.QuestionID || readSession.Prompt != session.Prompt || readSession.State != session.State || readSession.Status != session.Status {
		t.Fatalf("read=%d %s start=%s", read.Code, read.Body.String(), start.Body.String())
	}
	resume := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": payload.Plans[0].Blocks[0].ID})
	if resume.Code != http.StatusOK || !strings.Contains(resume.Body.String(), session.ID.String()) {
		t.Fatalf("resume=%d %s", resume.Code, resume.Body.String())
	}
	if len(payload.Plans[0].Blocks) > 1 {
		conflict := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": payload.Plans[0].Blocks[1].ID})
		if conflict.Code != http.StatusConflict || conflict.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("different block while session open=%d %s", conflict.Code, conflict.Body.String())
		}
	}
	parentRead := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.parentToken, nil)
	if parentRead.Code != http.StatusForbidden {
		t.Fatalf("parent used student session endpoint: %d", parentRead.Code)
	}
}

func TestPlanBlockStatusFollowsSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var planID, subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT kp.subject_id,q.knowledge_point_id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$1`, fixture.releasedQuestionID).Scan(&subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	planID = uuid.New()
	blockID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES($1,$2,current_date,20,'PROPOSED')`, planID, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status) VALUES($1,$2,1,$3,$4,20,'CURRENT_GRADE','plan-block-state-fixture','AVAILABLE')`, blockID, planID, subjectID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService),
	})

	today := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil)
	if today.Code != http.StatusOK {
		t.Fatalf("today=%d %s", today.Code, today.Body.String())
	}
	var payload struct {
		Plans []struct {
			Blocks []struct {
				ID     uuid.UUID `json:"id"`
				Status string    `json:"status"`
			} `json:"blocks"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(today.Body.Bytes(), &payload); err != nil || len(payload.Plans) != 1 || len(payload.Plans[0].Blocks) == 0 {
		t.Fatalf("today payload=%s err=%v", today.Body.String(), err)
	}
	if payload.Plans[0].Blocks[0].ID != blockID {
		t.Fatalf("today selected block=%s want fixture block=%s", payload.Plans[0].Blocks[0].ID, blockID)
	}
	if payload.Plans[0].Blocks[0].Status != "AVAILABLE" {
		t.Fatalf("initial block status=%q want AVAILABLE", payload.Plans[0].Blocks[0].Status)
	}

	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	var activeStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&activeStatus); err != nil {
		t.Fatal(err)
	}
	if activeStatus != "ACTIVE" {
		t.Fatalf("started block status=%q want ACTIVE", activeStatus)
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	secondBlockID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status) VALUES($1,$2,(SELECT COALESCE(max(sequence),0)+1 FROM learning_plan_blocks WHERE plan_id=$2),$3,$4,20,'CURRENT_GRADE','cache-conflict-fixture','AVAILABLE')`, secondBlockID, planID, subjectID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	conflict := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": secondBlockID})
	if conflict.Code != http.StatusConflict || conflict.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("second block start=%d cache=%q body=%s", conflict.Code, conflict.Header().Get("Cache-Control"), conflict.Body.String())
	}
	paused := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/pause", fixture.studentToken, nil)
	if paused.Code != http.StatusOK {
		t.Fatalf("pause=%d %s", paused.Code, paused.Body.String())
	}
	resumed := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/resume", fixture.studentToken, nil)
	if resumed.Code != http.StatusOK {
		t.Fatalf("resume=%d %s", resumed.Code, resumed.Body.String())
	}
	var correctAnswer string
	if err := pool.QueryRow(ctx, `SELECT correct_answer_json->>'value' FROM question_private_answers WHERE question_id=$1`, session.QuestionID).Scan(&correctAnswer); err != nil {
		t.Fatal(err)
	}
	complete := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.studentToken, map[string]any{"answer": correctAnswer})
	if complete.Code != http.StatusOK {
		t.Fatalf("complete=%d %s", complete.Code, complete.Body.String())
	}
	var completedStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&completedStatus); err != nil {
		t.Fatal(err)
	}
	if completedStatus != "COMPLETED" {
		t.Fatalf("completed block status=%q want COMPLETED", completedStatus)
	}
}
