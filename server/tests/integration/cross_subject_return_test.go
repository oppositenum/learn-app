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

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type prerequisiteGapAgent struct{}

func (prerequisiteGapAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	return ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .95, ErrorType: "PREREQUISITE_GAP", Misconceptions: []string{"REASONING_GAP"}, EmotionSignal: "NEUTRAL", Engagement: "NORMAL", RecommendedAction: tutor.StateBacktrack}, nil
}
func (prerequisiteGapAgent) GenerateTurn(_ context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	return ai.TutorTurn{Action: request.TutorDecision.NextState, Message: "先检查底层知识。"}, nil
}
func (agent prerequisiteGapAgent) GenerateAnalogy(ctx context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}
func (agent prerequisiteGapAgent) GenerateParallelExample(ctx context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}
func (agent prerequisiteGapAgent) GenerateExplanation(ctx context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func TestTutorPrerequisiteDiagnosisBacktracksAndReturnsWithinSameSession(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	originalKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000010")
	originalQuestion := uuid.MustParse("40000000-0000-4000-8000-000000000010")
	prerequisiteKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=$1`, prerequisiteKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,active_task_id,evidence_form) VALUES($1,$2,'00000000-0000-4000-8000-000000000004',$3,'ACTIVE',20,'ASK',$4,'LIFE')`, sessionID, fixture.studentID, originalQuestion, originalKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private) SELECT $1,$2,1,'TUTOR','ASK',prompt_public,'integration original question' FROM questions WHERE id=$3`, uuid.New(), sessionID, originalQuestion); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	plannerService := planner.NewService(pool)
	parents := parent.NewRepository(pool)
	service := classroom.NewService(pool, hub, nil, nil, plannerService).WithTeachingAgent(prerequisiteGapAgent{})
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})
	studentEvents, stopStudent := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stopStudent()
	parentEvents, stopParent := hub.Subscribe(fixture.studentID.String(), auth.RoleParent)
	defer stopParent()

	backtracked := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "30/2"})
	if backtracked.Code != http.StatusOK || !strings.Contains(backtracked.Body.String(), `"action":"BACKTRACK"`) {
		t.Fatalf("automatic backtrack=%d %s", backtracked.Code, backtracked.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, backtracked.Body.Bytes())
	studentBacktrackEvent := awaitEventType(t, studentEvents, "BACKTRACK_STARTED")
	parentBacktrackEvent := awaitEventType(t, parentEvents, "BACKTRACK_STARTED")
	if !strings.Contains(string(studentBacktrackEvent), `"type":"BACKTRACK_STARTED"`) || strings.Contains(string(studentBacktrackEvent), "correct_answer") {
		t.Fatalf("Student backtrack event=%s", studentBacktrackEvent)
	}
	if !strings.Contains(string(parentBacktrackEvent), prerequisiteKnowledgePoint.String()) || !strings.Contains(string(parentBacktrackEvent), originalKnowledgePoint.String()) {
		t.Fatalf("Parent backtrack context incomplete: %s", parentBacktrackEvent)
	}

	current := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+sessionID.String(), fixture.studentToken, nil)
	if current.Code != http.StatusOK {
		t.Fatalf("backtrack session=%d %s", current.Code, current.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(current.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.State != "BACKTRACK" || session.SubjectCode != "MATH" || session.KnowledgePoint != "分数通分" {
		t.Fatalf("runtime did not switch to prerequisite: %+v", session)
	}
	var originalTaskID, activeTaskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT original_task_id,active_task_id FROM learning_sessions WHERE id=$1`, sessionID).Scan(&originalTaskID, &activeTaskID); err != nil {
		t.Fatal(err)
	}
	if originalTaskID != originalKnowledgePoint || activeTaskID != prerequisiteKnowledgePoint {
		t.Fatalf("preserved task context original=%s active=%s", originalTaskID, activeTaskID)
	}

	prerequisiteAnswer := privateTeacherAnswer(t, ctx, pool, session.QuestionID)
	returned := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": prerequisiteAnswer})
	if returned.Code != http.StatusOK || !strings.Contains(returned.Body.String(), `"action":"RETURN"`) || strings.Contains(returned.Body.String(), prerequisiteAnswer) {
		t.Fatalf("prerequisite completion=%d %s", returned.Code, returned.Body.String())
	}
	studentReturnEvent := awaitEventType(t, studentEvents, "BACKTRACK_COMPLETED")
	parentReturnEvent := awaitEventType(t, parentEvents, "BACKTRACK_COMPLETED")
	if !strings.Contains(string(studentReturnEvent), `"type":"BACKTRACK_COMPLETED"`) || strings.Contains(string(studentReturnEvent), prerequisiteAnswer) || !strings.Contains(string(parentReturnEvent), originalKnowledgePoint.String()) {
		t.Fatalf("return events student=%s parent=%s", studentReturnEvent, parentReturnEvent)
	}

	restored := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+sessionID.String(), fixture.studentToken, nil)
	if err := json.Unmarshal(restored.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.State != "RETURN" || session.SubjectCode != "PHYSICS" || session.QuestionID != originalQuestion {
		t.Fatalf("original task not restored: %+v", session)
	}
	originalAnswer := privateTeacherAnswer(t, ctx, pool, originalQuestion)
	completed := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": originalAnswer})
	if completed.Code != http.StatusOK || !strings.Contains(completed.Body.String(), `"action":"COMPLETE"`) || strings.Contains(completed.Body.String(), originalAnswer) {
		t.Fatalf("original verification=%d %s", completed.Code, completed.Body.String())
	}
}

func TestCrossSubjectRemediationReturnsSameSessionToReleasedOriginalTask(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	originalKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000010")
	remediationKnowledgePoint := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `UPDATE knowledge_points SET grade_band_code='PRIMARY' WHERE id=$1`, remediationKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_skill_states(student_id,knowledge_point_id,state,score_internal) VALUES($1,$2,'REGRESSED',20)`, fixture.studentID, originalKnowledgePoint); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	plan, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	var blockID uuid.UUID
	for _, block := range plan.Blocks {
		if block.KnowledgePointID != nil && *block.KnowledgePointID == remediationKnowledgePoint {
			blockID = block.ID
			break
		}
	}
	if blockID == uuid.Nil {
		t.Fatalf("micro-backtrack block missing: %+v", plan.Blocks)
	}
	parents := parent.NewRepository(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parents, plannerService)})
	started := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	if started.Code != http.StatusOK {
		t.Fatalf("start remediation=%d %s", started.Code, started.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(started.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.KnowledgePoint != "分数通分" || session.State != "ASK" {
		t.Fatalf("remediation session=%+v", session)
	}
	remediationAnswer := privateTeacherAnswer(t, ctx, pool, session.QuestionID)
	returned := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.studentToken, map[string]any{"answer": remediationAnswer})
	if returned.Code != http.StatusOK || !strings.Contains(returned.Body.String(), `"action":"RETURN"`) {
		t.Fatalf("return response=%d %s", returned.Code, returned.Body.String())
	}
	if strings.Contains(returned.Body.String(), remediationAnswer) {
		t.Fatalf("Student return response leaked remediation answer: %s", returned.Body.String())
	}
	restored := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.studentToken, nil)
	if restored.Code != http.StatusOK {
		t.Fatalf("restored session=%d %s", restored.Code, restored.Body.String())
	}
	if err := json.Unmarshal(restored.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.State != "RETURN" || session.SubjectCode != "PHYSICS" || session.KnowledgePoint != "速度" {
		t.Fatalf("original task not restored: %+v", session)
	}
	var originalTaskID, activeTaskID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT original_task_id,active_task_id FROM learning_sessions WHERE id=$1`, session.ID).Scan(&originalTaskID, &activeTaskID); err != nil {
		t.Fatal(err)
	}
	if originalTaskID != originalKnowledgePoint || activeTaskID != originalKnowledgePoint {
		t.Fatalf("task context original=%s active=%s", originalTaskID, activeTaskID)
	}
	originalAnswer := privateTeacherAnswer(t, ctx, pool, session.QuestionID)
	completed := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.studentToken, map[string]any{"answer": originalAnswer})
	if completed.Code != http.StatusOK || !strings.Contains(completed.Body.String(), `"action":"COMPLETE"`) {
		t.Fatalf("original verification=%d %s", completed.Code, completed.Body.String())
	}
	if strings.Contains(completed.Body.String(), originalAnswer) {
		t.Fatalf("Student completion response leaked original answer: %s", completed.Body.String())
	}
	var status, state string
	var insights, points int
	if err := pool.QueryRow(ctx, `SELECT status,current_state FROM learning_sessions WHERE id=$1`, session.ID).Scan(&status, &state); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(points),0) FROM reward_events WHERE student_id=$1 AND session_id=$2 AND type='CROSS_SUBJECT_INSIGHT'`, fixture.studentID, session.ID).Scan(&insights, &points); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETED" || state != "COMPLETE" || insights != 1 || points != 6 {
		t.Fatalf("completion status=%s state=%s insights=%d points=%d", status, state, insights, points)
	}
}

func privateTeacherAnswer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID) string {
	t.Helper()
	var answer string
	if err := pool.QueryRow(ctx, `SELECT teacher_reference_answer FROM question_private_answers WHERE question_id=$1`, questionID).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	return answer
}
