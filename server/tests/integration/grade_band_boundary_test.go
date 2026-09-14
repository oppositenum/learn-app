package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

var b1aPrimaryKnowledgePoints = map[string]uuid.UUID{
	"MATH":      uuid.MustParse("30000000-0000-4000-8000-000000000001"),
	"CHINESE":   uuid.MustParse("30000000-0000-4000-8000-000000000004"),
	"ENGLISH":   uuid.MustParse("30000000-0000-4000-8000-000000000007"),
	"PHYSICS":   uuid.MustParse("30000000-0000-4000-8000-000000000010"),
	"CHEMISTRY": uuid.MustParse("30000000-0000-4000-8000-000000000013"),
}

func TestB1AGradeBandPlannerModesAndFiveSubjects(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	makeB1APrimaryFixtures(t, ctx, pool)
	plannerService := planner.NewService(pool)

	for _, grade := range []int{1, 6, 7, 9} {
		studentID := seedB1AGradeStudent(t, ctx, pool, grade)
		plan, err := plannerService.Ensure(ctx, studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatalf("grade %d plan: %v", grade, err)
		}
		assertB1APlanBoundary(t, ctx, pool, plan.ID, grade, 5)
		var wrongBand int
		expectedBand := "JUNIOR_SECONDARY"
		if grade <= 6 {
			expectedBand = "PRIMARY"
		}
		if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM learning_plan_blocks block
JOIN knowledge_points knowledge_point ON knowledge_point.id=block.knowledge_point_id
WHERE block.plan_id=$1 AND knowledge_point.grade_band_code<>$2`, plan.ID, expectedBand).Scan(&wrongBand); err != nil {
			t.Fatal(err)
		}
		if wrongBand != 0 {
			t.Fatalf("grade %d current plan crossed grade band: wrong_band_blocks=%d", grade, wrongBand)
		}
	}

	studentID := seedB1AGradeStudent(t, ctx, pool, 7)
	var misconceptionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM misconceptions ORDER BY code LIMIT 1`).Scan(&misconceptionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO student_misconceptions(student_id,knowledge_point_id,misconception_id,first_seen_at,last_seen_at,status)
VALUES($1,$2,$3,now(),now(),'ACTIVE')`, studentID, b1aPrimaryKnowledgePoints["MATH"], misconceptionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,status)
VALUES($1,$2,$3,'PARENT_PRIORITY',now()-interval '1 day','PENDING')`, uuid.New(), studentID, b1aPrimaryKnowledgePoints["CHINESE"]); err != nil {
		t.Fatal(err)
	}
	originalTaskID := uuid.MustParse("30000000-0000-4000-8000-000000000015")
	if _, err := pool.Exec(ctx, `
INSERT INTO student_skill_states(student_id,knowledge_point_id,state,score_internal)
VALUES($1,$2,'REGRESSED',20)`, studentID, originalTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO cross_subject_dependencies(id,source_knowledge_point_id,target_knowledge_point_id,relation,strength)
VALUES($1,$2,$3,'REQUIRES',0.9)`, uuid.New(), originalTaskID, b1aPrimaryKnowledgePoints["ENGLISH"]); err != nil {
		t.Fatal(err)
	}
	plan, err := plannerService.Ensure(ctx, studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	assertB1APlanBoundary(t, ctx, pool, plan.ID, 7, 5)
	wantModes := map[uuid.UUID]planner.Mode{
		b1aPrimaryKnowledgePoints["MATH"]:    planner.ModeRemediation,
		b1aPrimaryKnowledgePoints["CHINESE"]: planner.ModeReview,
		b1aPrimaryKnowledgePoints["ENGLISH"]: planner.ModeMicroBacktrack,
	}
	for _, block := range plan.Blocks {
		if block.KnowledgePointID == nil {
			continue
		}
		if want, ok := wantModes[*block.KnowledgePointID]; ok {
			if block.Mode != want {
				t.Fatalf("lower-band block %s mode=%s want=%s", *block.KnowledgePointID, block.Mode, want)
			}
			delete(wantModes, *block.KnowledgePointID)
		}
	}
	if len(wantModes) != 0 {
		t.Fatalf("lower-band support modes missing from plan: %v", wantModes)
	}
}

func TestB1AGradeBandStudentReadsStartsAndReturns(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	primaryReadable := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	primaryRunnable := uuid.MustParse("30000000-0000-4000-8000-000000000003")
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=ANY($1)`, []uuid.UUID{primaryReadable, primaryRunnable}); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	parents := parent.NewRepository(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate:    auth.NewSessionAuthenticator(pool).Middleware,
		PublicQuestions: content.NewRepository(pool),
		Classroom:       classroom.NewHandler(service, pool, parents, plannerService),
	})

	primaryQuestion := uuid.MustParse("40000000-0000-4000-8000-000000000002")
	response := performQuestionRequest(router, fixture.studentToken, primaryQuestion)
	if response.Code != http.StatusOK {
		t.Fatalf("grade 7 lower-band direct read=%d %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())

	if _, err := pool.Exec(ctx, `UPDATE questions SET status='QUARANTINED' WHERE id=$1`, primaryQuestion); err != nil {
		t.Fatal(err)
	}
	if response := performQuestionRequest(router, fixture.studentToken, primaryQuestion); response.Code != http.StatusNotFound {
		t.Fatalf("quarantined lower-band question=%d %s", response.Code, response.Body.String())
	}
	if response := performQuestionRequest(router, fixture.studentToken, fixture.draftQuestionID); response.Code != http.StatusNotFound {
		t.Fatalf("draft question=%d %s", response.Code, response.Body.String())
	}

	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=6 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	juniorQuestion := uuid.MustParse("40000000-0000-4000-8000-000000000001")
	if response := performQuestionRequest(router, fixture.studentToken, juniorQuestion); response.Code != http.StatusNotFound {
		t.Fatalf("grade 6 upward direct read=%d %s", response.Code, response.Body.String())
	}

	planID, blockID := uuid.New(), uuid.New()
	juniorKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000001")
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,current_date,20,'PROPOSED')`, planID, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status)
VALUES($1,$2,1,'00000000-0000-4000-8000-000000000001',$3,20,'CURRENT_GRADE','forged_upper_band','AVAILABLE')`, blockID, planID, juniorKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	if response := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID}); response.Code != http.StatusNotFound {
		t.Fatalf("forged upper-band block started=%d %s", response.Code, response.Body.String())
	}

	originalTaskID := uuid.MustParse("30000000-0000-4000-8000-000000000010")
	if _, err := pool.Exec(ctx, `
UPDATE learning_plan_blocks
SET knowledge_point_id=$2,mode='MICRO_BACKTRACK',reason='server_authoritative_lower_band',original_task_id=$3
WHERE id=$1`, blockID, primaryRunnable, originalTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=7 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	started := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	if started.Code != http.StatusOK {
		t.Fatalf("database-mode lower-band block start=%d %s", started.Code, started.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(started.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.KnowledgePoint != "百分数与折扣" {
		t.Fatalf("lower-band session=%+v", session)
	}

	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=6 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	answer := privateTeacherAnswer(t, ctx, pool, session.QuestionID)
	returned := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.studentToken, map[string]any{"answer": answer})
	if returned.Code != http.StatusInternalServerError {
		t.Fatalf("upward original task restored=%d %s", returned.Code, returned.Body.String())
	}
	var currentState, status string
	var activeTaskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT current_state,status,active_task_id FROM learning_sessions WHERE id=$1`, session.ID).Scan(&currentState, &status, &activeTaskID); err != nil {
		t.Fatal(err)
	}
	if currentState != "ASK" || status != "ACTIVE" || activeTaskID != primaryRunnable {
		t.Fatalf("failed return changed session state=%s status=%s active_task=%s", currentState, status, activeTaskID)
	}
}

func TestB1AGradeBandBacktrackNeverMovesUp(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	originalKnowledgePoint := b1aPrimaryKnowledgePoints["PHYSICS"]
	originalQuestion := uuid.MustParse("40000000-0000-4000-8000-000000000010")
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=$1`, originalKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=6 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,active_task_id,evidence_form)
VALUES($1,$2,'00000000-0000-4000-8000-000000000004',$3,'ACTIVE',20,'ASK',$4,'LIFE')`, sessionID, fixture.studentID, originalQuestion, originalKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private)
SELECT $1,$2,1,'TUTOR','ASK',prompt_public,'grade boundary fixture' FROM questions WHERE id=$3`, uuid.New(), sessionID, originalQuestion); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService).WithTeachingAgent(prerequisiteGapAgent{})
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService),
	})
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "wrong"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"action":"SCAFFOLD"`) || strings.Contains(response.Body.String(), `"action":"BACKTRACK"`) {
		t.Fatalf("upward backtrack was not rejected: %d %s", response.Code, response.Body.String())
	}
	var activeTaskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT active_task_id FROM learning_sessions WHERE id=$1`, sessionID).Scan(&activeTaskID); err != nil {
		t.Fatal(err)
	}
	if activeTaskID != originalKnowledgePoint {
		t.Fatalf("upward backtrack changed active task to %s", activeTaskID)
	}
}

func TestB1AGradeBandUnavailableParentSubjectsAreExplicit(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=6 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=$1`, b1aPrimaryKnowledgePoints["MATH"]); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	parents := parent.NewRepository(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parents, plannerService),
	})
	path := "/api/v1/parent/child/" + fixture.studentID.String() + "/preferences"
	response := performJSON(router, http.MethodPut, path, fixture.parentToken, map[string]any{
		"daily_minutes":          30,
		"priority_subject_codes": []string{},
		"review_only":            false,
		"reduce_intensity":       false,
		"enabled_subject_codes":  []string{"PHYSICS"},
	})
	var payload struct {
		Saved                 bool     `json:"saved"`
		PlanUpdated           bool     `json:"plan_updated"`
		Error                 string   `json:"error"`
		AvailableSubjectCodes []string `json:"available_subject_codes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode unavailable-subject response: %v body=%s", err, response.Body.String())
	}
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Cache-Control") != "no-store" || !payload.Saved || payload.PlanUpdated || payload.Error != "no_available_content_for_enabled_subjects" || strings.Join(payload.AvailableSubjectCodes, ",") != "MATH" {
		t.Fatalf("unavailable-subject response=%d %+v body=%s", response.Code, payload, response.Body.String())
	}
	var enabled []string
	if err := pool.QueryRow(ctx, `SELECT enabled_subject_codes FROM parent_preferences WHERE student_id=$1`, fixture.studentID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if strings.Join(enabled, ",") != "PHYSICS" {
		t.Fatalf("saved enabled subjects=%v", enabled)
	}
	var runtimePlans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plans WHERE student_id=$1 AND status IN('PROPOSED','ACTIVE')`, fixture.studentID).Scan(&runtimePlans); err != nil {
		t.Fatal(err)
	}
	if runtimePlans != 0 {
		t.Fatalf("subject fallback silently created %d plan(s)", runtimePlans)
	}

	todayPlanID, todayBlockID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,current_date,20,'ACTIVE')`, todayPlanID, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status)
VALUES($1,$2,1,'00000000-0000-4000-8000-000000000001',$3,20,'CURRENT_GRADE','started_today_fixture','COMPLETED')`, todayBlockID, todayPlanID, b1aPrimaryKnowledgePoints["MATH"]); err != nil {
		t.Fatal(err)
	}
	response = performJSON(router, http.MethodPut, path, fixture.parentToken, map[string]any{
		"daily_minutes":          30,
		"priority_subject_codes": []string{},
		"review_only":            false,
		"reduce_intensity":       false,
		"enabled_subject_codes":  []string{"PHYSICS"},
	})
	var preservedPayload struct {
		TodayPreserved bool   `json:"today_preserved"`
		AppliesFrom    string `json:"applies_from"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preservedPayload); err != nil {
		t.Fatalf("decode preserved unavailable-subject response: %v body=%s", err, response.Body.String())
	}
	var tomorrow string
	if err := pool.QueryRow(ctx, `SELECT (current_date+1)::text`).Scan(&tomorrow); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || !preservedPayload.TodayPreserved || preservedPayload.AppliesFrom != tomorrow {
		t.Fatalf("preserved unavailable-subject response=%d %+v body=%s", response.Code, preservedPayload, response.Body.String())
	}
}

func TestB1AGradeBandMigrationRemovesOnlyInvalidAvailableBlocks(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, "000022_grade_band_boundary.sql")); err != nil {
		t.Fatal(err)
	}
	studentID := seedB1AGradeStudent(t, ctx, pool, 6)
	validKnowledgePoint := b1aPrimaryKnowledgePoints["MATH"]
	invalidKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=$1`, validKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	mixedPlan, emptyPlan, historyPlan := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES
($1,$2,current_date,20,'PROPOSED'),
($3,$2,current_date+1,20,'PROPOSED'),
($4,$2,current_date-1,20,'ACTIVE')`, mixedPlan, studentID, emptyPlan, historyPlan); err != nil {
		t.Fatal(err)
	}
	validAvailable, invalidAvailable, emptyInvalid := uuid.New(), uuid.New(), uuid.New()
	historyAvailable, historyActive, historyCompleted := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status) VALUES
($1,$7,1,'00000000-0000-4000-8000-000000000001',$8,10,'CURRENT_GRADE','valid_available','AVAILABLE'),
($2,$7,2,'00000000-0000-4000-8000-000000000001',$9,10,'CURRENT_GRADE','invalid_available','AVAILABLE'),
($3,$10,1,'00000000-0000-4000-8000-000000000001',$9,20,'CURRENT_GRADE','empty_invalid','AVAILABLE'),
($4,$11,1,'00000000-0000-4000-8000-000000000001',$9,6,'CURRENT_GRADE','history_available','AVAILABLE'),
($5,$11,2,'00000000-0000-4000-8000-000000000001',$9,7,'CURRENT_GRADE','history_active','ACTIVE'),
($6,$11,3,'00000000-0000-4000-8000-000000000001',$9,7,'CURRENT_GRADE','history_completed','COMPLETED')`,
		validAvailable, invalidAvailable, emptyInvalid, historyAvailable, historyActive, historyCompleted,
		mixedPlan, validKnowledgePoint, invalidKnowledgePoint, emptyPlan, historyPlan); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("grade boundary migration is not idempotent: %v", err)
	}
	var keptBlocks, removedBlocks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks WHERE id=ANY($1)`, []uuid.UUID{validAvailable, historyActive, historyCompleted}).Scan(&keptBlocks); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks WHERE id=ANY($1)`, []uuid.UUID{invalidAvailable, emptyInvalid, historyAvailable}).Scan(&removedBlocks); err != nil {
		t.Fatal(err)
	}
	var mixedPlans, emptyPlans, historyPlans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plans WHERE id=$1`, mixedPlan).Scan(&mixedPlans); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plans WHERE id=$1`, emptyPlan).Scan(&emptyPlans); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plans WHERE id=$1`, historyPlan).Scan(&historyPlans); err != nil {
		t.Fatal(err)
	}
	if keptBlocks != 3 || removedBlocks != 0 || mixedPlans != 1 || emptyPlans != 0 || historyPlans != 1 {
		t.Fatalf("migration kept=%d removed=%d plans mixed=%d empty=%d history=%d", keptBlocks, removedBlocks, mixedPlans, emptyPlans, historyPlans)
	}
}

func makeB1APrimaryFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	ids := make([]uuid.UUID, 0, len(b1aPrimaryKnowledgePoints))
	for _, id := range b1aPrimaryKnowledgePoints {
		ids = append(ids, id)
	}
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
}

func seedB1AGradeStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, grade int) uuid.UUID {
	t.Helper()
	userID, studentID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT',$2)`, userID, "B1A grade student"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,$3)`, studentID, userID, grade); err != nil {
		t.Fatal(err)
	}
	return studentID
}

func assertB1APlanBoundary(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID, grade, wantBlocks int) {
	t.Helper()
	var blocks, subjects, upward, lowerCurrent int
	if err := pool.QueryRow(ctx, `
SELECT count(*),count(DISTINCT subject.code),
       count(*) FILTER(WHERE grade_band.min_grade>$2),
       count(*) FILTER(WHERE block.mode='CURRENT_GRADE' AND $2>grade_band.max_grade)
FROM learning_plan_blocks block
JOIN subjects subject ON subject.id=block.subject_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=block.knowledge_point_id
JOIN grade_bands grade_band ON grade_band.code=knowledge_point.grade_band_code
WHERE block.plan_id=$1`, planID, grade).Scan(&blocks, &subjects, &upward, &lowerCurrent); err != nil {
		t.Fatal(err)
	}
	if blocks != wantBlocks || subjects != wantBlocks || upward != 0 || lowerCurrent != 0 {
		t.Fatalf("plan=%s grade=%d blocks=%d subjects=%d upward=%d lower_current=%d", planID, grade, blocks, subjects, upward, lowerCurrent)
	}
}
