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
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type parentOverviewFixture struct {
	pool     *pgxpool.Pool
	router   http.Handler
	security securityFixture
}

func newParentOverviewFixture(t *testing.T) parentOverviewFixture {
	t.Helper()
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	security := seedSecurityFixture(t, ctx, pool)
	parents := parent.NewRepository(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, realtime.NewHub(), nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Parents:      parents,
		Classroom:    classroom.NewHandler(service, pool, parents, plannerService),
	})
	return parentOverviewFixture{pool: pool, router: router, security: security}
}

func (fixture parentOverviewFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := fixture.pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// overview reads the parent home page and checks that it never carries the
// question, the answers or the tutor's words.
func (fixture parentOverviewFixture) overview(t *testing.T) (parent.OverviewDTO, string) {
	t.Helper()
	response := performJSON(fixture.router, http.MethodGet, "/api/v1/parent/child/"+fixture.security.studentID.String()+"/overview", fixture.security.parentToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("overview=%d", response.Code)
	}
	body := response.Body.String()
	assertOverviewCarriesNoAnswers(t, fixture.pool, fixture.security, body)
	var overview parent.OverviewDTO
	if err := json.Unmarshal(response.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	return overview, body
}

func assertOverviewCarriesNoAnswers(t *testing.T, pool *pgxpool.Pool, security securityFixture, body string) {
	t.Helper()
	var prompt, tutorMessage, childAnswer string
	if err := pool.QueryRow(context.Background(), `SELECT prompt_public FROM questions WHERE id=$1`, security.releasedQuestionID).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT message FROM tutor_turns WHERE session_id=$1 AND actor='TUTOR' ORDER BY sequence DESC LIMIT 1`, security.sessionID).Scan(&tutorMessage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT answer_text FROM student_answers WHERE session_id=$1 LIMIT 1`, security.sessionID).Scan(&childAnswer); err != nil {
		t.Fatal(err)
	}
	for name, private := range map[string]string{"private answer": security.privateCanary, "question text": prompt, "tutor words": tutorMessage, "child answer": `"` + childAnswer + `"`} {
		if strings.Contains(body, private) {
			t.Fatalf("parent overview carries the %s", name)
		}
	}
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range jsonKeys(payload) {
		lower := strings.ToLower(key)
		for _, forbidden := range []string{"answer", "solution", "prompt", "question", "preview", "message", "encourag", "reason"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("parent overview carries key %q", key)
			}
		}
	}
}

func (fixture parentOverviewFixture) assertStudentAPIsStayPrivate(t *testing.T) {
	t.Helper()
	for _, path := range []string{"/api/v1/student/sessions/current", "/api/v1/student/sessions/" + fixture.security.sessionID.String()} {
		response := performJSON(fixture.router, http.MethodGet, path, fixture.security.studentToken, nil)
		if response.Code != http.StatusOK && response.Code != http.StatusNotFound && response.Code != http.StatusNoContent && response.Code != http.StatusConflict {
			t.Fatalf("%s=%d", path, response.Code)
		}
		if response.Body.Len() > 0 && strings.HasPrefix(strings.TrimSpace(response.Body.String()), "{") {
			assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
		}
		if strings.Contains(response.Body.String(), fixture.security.privateCanary) {
			t.Fatalf("%s carries the private answer", path)
		}
	}
}

func TestParentOverviewFollowsTheOpenClassroomAndTodaysPlan(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	security := fixture.security

	// Before any plan exists the home page shows the explanation instead.
	overview, _ := fixture.overview(t)
	if overview.TodayPlan != nil {
		t.Fatalf("plan before one exists: %+v", overview.TodayPlan)
	}

	// Yesterday's plan never shows as today's.
	fixture.exec(t, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date-1,30,'ACTIVE')`, uuid.New(), security.studentID)

	planID, doneBlock, classroomBlock, laterBlock := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	fixture.exec(t, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,(CURRENT_TIMESTAMP AT TIME ZONE 'Asia/Shanghai')::date,45,'ACTIVE')`, planID, security.studentID)
	fixture.exec(t, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status)
SELECT block.id,$1,block.sequence,subject.id,CASE WHEN subject.code='MATH' THEN question.knowledge_point_id END,block.minutes,'CURRENT_GRADE','integration plan',block.status
FROM (VALUES($2::uuid,1::smallint,'MATH',10::smallint,'COMPLETED'),($3::uuid,2::smallint,'MATH',20::smallint,'ACTIVE'),($4::uuid,3::smallint,'PHYSICS',15::smallint,'AVAILABLE'))
     AS block(id,sequence,subject_code,minutes,status)
JOIN subjects subject ON subject.code=block.subject_code
CROSS JOIN questions question WHERE question.id=$5`, planID, doneBlock, classroomBlock, laterBlock, security.releasedQuestionID)
	// The classroom has run 60 seconds before and 65 seconds since it resumed.
	fixture.exec(t, `
UPDATE learning_sessions
SET plan_block_id=$2,socratic_fail_count=2,target_minutes=20,accumulated_seconds=60,
    last_resumed_at=CURRENT_TIMESTAMP-interval '65 seconds',last_activity_at=CURRENT_TIMESTAMP
WHERE id=$1`, security.sessionID, classroomBlock)

	// ACTIVE: the home page agrees with the live classroom.
	overview, _ = fixture.overview(t)
	live := performParentSessionRequest(fixture.router, security.parentToken, security.studentID, security.sessionID)
	if live.Code != http.StatusOK {
		t.Fatalf("live=%d", live.Code)
	}
	var liveSession parent.LiveSessionDTO
	if err := json.Unmarshal(live.Body.Bytes(), &liveSession); err != nil {
		t.Fatal(err)
	}
	session := overview.Session
	if session == nil || session.SessionID != security.sessionID || session.Status != "ACTIVE" {
		t.Fatalf("active session=%+v", session)
	}
	if session.Subject != liveSession.Subject || session.Subject != "数学" || session.KnowledgePoint != liveSession.KnowledgePoint || session.KnowledgePoint != "固定费用方程" {
		t.Fatalf("active subject=%q/%q knowledge point=%q/%q", session.Subject, liveSession.Subject, session.KnowledgePoint, liveSession.KnowledgePoint)
	}
	if session.SocraticRound != 2 || session.SocraticRound != liveSession.SocraticRound || session.TargetMinutes != 20 || session.TargetMinutes != liveSession.TargetMinutes {
		t.Fatalf("active round=%d/%d target=%d/%d", session.SocraticRound, liveSession.SocraticRound, session.TargetMinutes, liveSession.TargetMinutes)
	}
	// The overview was read first, so it cannot be ahead of the live page.
	if session.ActiveSeconds < 125 || session.ActiveSeconds > liveSession.ActiveSeconds {
		t.Fatalf("active seconds=%d live=%d", session.ActiveSeconds, liveSession.ActiveSeconds)
	}
	if session.TutorAction != liveSession.TutorAction || session.TutorAction != "PROBE" || session.ErrorType != liveSession.ErrorType || session.ErrorType != "FIXED_COST_IGNORED" {
		t.Fatalf("active action=%q/%q judgement=%q/%q", session.TutorAction, liveSession.TutorAction, session.ErrorType, liveSession.ErrorType)
	}
	assertOverviewPlan(t, overview, planID, map[uuid.UUID][2]any{
		doneBlock:      {10, parent.ProgressCompleted},
		classroomBlock: {20, parent.ProgressInProgress},
		laterBlock:     {15, parent.ProgressNotStarted},
	})
	fixture.assertStudentAPIsStayPrivate(t)

	// PAUSED: the child pauses; subject and time stay, the block is paused.
	pause := performJSON(fixture.router, http.MethodPost, "/api/v1/student/sessions/"+security.sessionID.String()+"/pause", security.studentToken, map[string]any{})
	if pause.Code != http.StatusOK {
		t.Fatalf("pause=%d", pause.Code)
	}
	var accumulated int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT accumulated_seconds FROM learning_sessions WHERE id=$1`, security.sessionID).Scan(&accumulated); err != nil {
		t.Fatal(err)
	}
	overview, _ = fixture.overview(t)
	session = overview.Session
	if session == nil || session.Status != "PAUSED" || session.Subject != "数学" || session.KnowledgePoint != "固定费用方程" || session.ActiveSeconds != accumulated || accumulated < 125 {
		t.Fatalf("paused session=%+v accumulated=%d", session, accumulated)
	}
	assertOverviewPlan(t, overview, planID, map[uuid.UUID][2]any{
		doneBlock:      {10, parent.ProgressCompleted},
		classroomBlock: {20, parent.ProgressPaused},
		laterBlock:     {15, parent.ProgressNotStarted},
	})
	fixture.assertStudentAPIsStayPrivate(t)

	// No classroom: the page waits, with nothing from the last question.
	abandon := performJSON(fixture.router, http.MethodPost, "/api/v1/student/sessions/"+security.sessionID.String()+"/abandon", security.studentToken, map[string]any{})
	if abandon.Code != http.StatusOK {
		t.Fatalf("abandon=%d", abandon.Code)
	}
	overview, body := fixture.overview(t)
	if overview.Session != nil || !strings.Contains(body, `"session":null`) {
		t.Fatalf("session after abandon=%+v", overview.Session)
	}
	assertOverviewPlan(t, overview, planID, map[uuid.UUID][2]any{
		doneBlock:      {10, parent.ProgressCompleted},
		classroomBlock: {20, parent.ProgressNotStarted},
		laterBlock:     {15, parent.ProgressNotStarted},
	})
	fixture.assertStudentAPIsStayPrivate(t)
}

func TestParentOverviewTreatsAQuietClassroomAsPaused(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	security := fixture.security
	// Quiet for five minutes after one minute of work since resuming: the
	// stale-session recovery would pause it, so the parent reads it as paused
	// with the time counted up to the last activity.
	fixture.exec(t, `
UPDATE learning_sessions
SET accumulated_seconds=60,last_resumed_at=CURRENT_TIMESTAMP-interval '6 minutes',last_activity_at=CURRENT_TIMESTAMP-interval '5 minutes'
WHERE id=$1`, security.sessionID)
	overview, _ := fixture.overview(t)
	if overview.Session == nil || overview.Session.Status != "PAUSED" || overview.Session.ActiveSeconds != 120 || overview.Session.Subject != "数学" {
		t.Fatalf("quiet session=%+v", overview.Session)
	}
	if overview.TodayPlan != nil {
		t.Fatalf("plan without one=%+v", overview.TodayPlan)
	}
}

func TestParentOverviewWithoutAJudgementLeavesItEmpty(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	fixture.exec(t, `DELETE FROM answer_analyses WHERE student_answer_id IN (SELECT id FROM student_answers WHERE session_id=$1)`, fixture.security.sessionID)
	overview, body := fixture.overview(t)
	if overview.Session == nil || overview.Session.ErrorType != "" || string(overview.Session.Misconceptions) != "[]" || !strings.Contains(body, `"misconceptions":[]`) {
		t.Fatalf("session without judgement=%+v", overview.Session)
	}
}

func TestParentOverviewIsOnlyForTheLinkedParent(t *testing.T) {
	fixture := newParentOverviewFixture(t)
	path := "/api/v1/parent/child/" + fixture.security.studentID.String() + "/overview"
	if response := performJSON(fixture.router, http.MethodGet, path, fixture.security.studentToken, nil); response.Code != http.StatusForbidden {
		t.Fatalf("student read the parent overview: %d", response.Code)
	}
	fixture.exec(t, `DELETE FROM parent_student_links WHERE parent_user_id=$1`, fixture.security.parentUserID)
	if response := performJSON(fixture.router, http.MethodGet, path, fixture.security.parentToken, nil); response.Code != http.StatusForbidden {
		t.Fatalf("unlinked parent read the overview: %d", response.Code)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if response := performJSON(fixture.router, method, path, fixture.security.parentToken, map[string]any{}); response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s overview=%d", method, response.Code)
		}
	}
}

func assertOverviewPlan(t *testing.T, overview parent.OverviewDTO, planID uuid.UUID, want map[uuid.UUID][2]any) {
	t.Helper()
	plan := overview.TodayPlan
	if plan == nil || plan.PlanID != planID || plan.Date != overview.LearningDate || plan.TargetMinutes != 45 || len(plan.Blocks) != len(want) {
		t.Fatalf("plan=%+v date=%s", plan, overview.LearningDate)
	}
	for index, block := range plan.Blocks {
		expected, ok := want[block.ID]
		if !ok || block.Sequence != index+1 || block.Minutes != expected[0].(int) || block.Progress != expected[1].(string) {
			t.Fatalf("block %d=%+v want %v", index+1, block, expected)
		}
	}
	if plan.Blocks[2].Subject != "物理" || plan.Blocks[2].KnowledgePoint != "" || plan.Blocks[0].KnowledgePoint != "固定费用方程" {
		t.Fatalf("block subjects=%+v", plan.Blocks)
	}
}
