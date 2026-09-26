package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

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
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// countingPrerequisiteGapAgent reports a prerequisite gap and counts every
// model call, so a test can prove leaving the supply calls no model.
type countingPrerequisiteGapAgent struct {
	prerequisiteGapAgent
	calls *atomic.Int64
}

func (agent countingPrerequisiteGapAgent) AnalyzeAnswer(ctx context.Context, request ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.calls.Add(1)
	return agent.prerequisiteGapAgent.AnalyzeAnswer(ctx, request)
}
func (agent countingPrerequisiteGapAgent) GenerateTurn(ctx context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.calls.Add(1)
	return agent.prerequisiteGapAgent.GenerateTurn(ctx, request)
}
func (agent countingPrerequisiteGapAgent) GenerateAnalogy(ctx context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	agent.calls.Add(1)
	return agent.prerequisiteGapAgent.GenerateAnalogy(ctx, request)
}
func (agent countingPrerequisiteGapAgent) GenerateParallelExample(ctx context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	agent.calls.Add(1)
	return agent.prerequisiteGapAgent.GenerateParallelExample(ctx, request)
}
func (agent countingPrerequisiteGapAgent) GenerateExplanation(ctx context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	agent.calls.Add(1)
	return agent.prerequisiteGapAgent.GenerateExplanation(ctx, request)
}

type backtrackSupplyClassroom struct {
	pool                   *pgxpool.Pool
	router                 http.Handler
	studentToken           string
	sessionID              uuid.UUID
	originalQuestion       uuid.UUID
	originalKnowledgePoint uuid.UUID
	easierSibling          uuid.UUID
	modelCalls             *atomic.Int64
	studentEvents          <-chan []byte
}

// startBacktrackSupplyClassroom opens a physics classroom whose question has a
// released cross-subject prerequisite. The original knowledge point also has
// an easier released question, so returning to "the first released question
// of the knowledge point" would land on a different question than the one the
// classroom left.
func startBacktrackSupplyClassroom(t *testing.T) backtrackSupplyClassroom {
	t.Helper()
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
	easierSibling := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO questions(id,knowledge_point_id,template_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version)
SELECT $2,knowledge_point_id,template_id,'L0',question_type,prompt_public||'（另一题）',scene_public_json,input_schema_json,'DRAFT',content_version FROM questions WHERE id=$1`, originalQuestion, easierSibling); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO question_private_answers(question_id,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json)
SELECT $2,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json FROM question_private_answers WHERE question_id=$1`, originalQuestion, easierSibling); err != nil {
		t.Fatal(err)
	}
	releaseCopiedQuestion(t, ctx, pool, originalQuestion, easierSibling)
	var originalDifficulty string
	if err := pool.QueryRow(ctx, `SELECT difficulty FROM questions WHERE id=$1`, originalQuestion).Scan(&originalDifficulty); err != nil {
		t.Fatal(err)
	}
	if originalDifficulty <= "L0" {
		t.Fatalf("original question difficulty %s does not sort after the easier sibling", originalDifficulty)
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
	calls := &atomic.Int64{}
	service := classroom.NewService(pool, hub, nil, nil, plannerService).WithTeachingAgent(countingPrerequisiteGapAgent{calls: calls})
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Classroom: classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService)})
	studentEvents, stop := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	t.Cleanup(stop)
	return backtrackSupplyClassroom{
		pool: pool, router: router, studentToken: fixture.studentToken, sessionID: sessionID,
		originalQuestion: originalQuestion, originalKnowledgePoint: originalKnowledgePoint, easierSibling: easierSibling,
		modelCalls: calls, studentEvents: studentEvents,
	}
}

// releaseCopiedQuestion moves a copied DRAFT question through validation,
// review and release, reusing the content source of the question it copies.
func releaseCopiedQuestion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sourceQuestion, questionID uuid.UUID) {
	t.Helper()
	validationID, reviewID := uuid.New(), uuid.New()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO content_versions(id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json)
SELECT $1,$2,q.content_version,'content-question-v1','fixture','fixture-generator',(SELECT source_id FROM content_versions WHERE question_id=$3 ORDER BY version DESC LIMIT 1),'{}' FROM questions q WHERE q.id=$2`, []any{uuid.New(), questionID, sourceQuestion}},
		{`INSERT INTO content_validations(id,question_id,content_version,schema_version,status,checks_json,validator_version)
SELECT $1,$2,content_version,'content-question-v1','PASS','[]','fixture-v1' FROM questions WHERE id=$2`, []any{validationID, questionID}},
		{`UPDATE questions SET status='AUTOMATIC_VALIDATED' WHERE id=$1`, []any{questionID}},
		{`INSERT INTO content_reviews(id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json)
SELECT $1,$2,content_version,'content-question-v1','PASS','fixture-secondary','fixture-reviewer','[]' FROM questions WHERE id=$2`, []any{reviewID, questionID}},
		{`UPDATE questions SET status='AI_REVIEWED' WHERE id=$1`, []any{questionID}},
		{`UPDATE questions SET status='RELEASED' WHERE id=$1`, []any{questionID}},
		{`INSERT INTO content_release_records(id,question_id,from_status,to_status,validation_id,review_id,reason)
VALUES($1,$2,'AI_REVIEWED','RELEASED',$3,$4,'backtrack supply integration fixture')`, []any{uuid.New(), questionID, validationID, reviewID}},
	} {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func (classroomUnderTest backtrackSupplyClassroom) read(t *testing.T) (classroom.StudentSession, []byte) {
	t.Helper()
	response := performJSON(classroomUnderTest.router, http.MethodGet, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String(), classroomUnderTest.studentToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read session=%d %s", response.Code, response.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	return session, response.Body.Bytes()
}

func (classroomUnderTest backtrackSupplyClassroom) backtrack(t *testing.T) classroom.StudentSession {
	t.Helper()
	response := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/answers", classroomUnderTest.studentToken, map[string]any{"answer": "30/2"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"action":"BACKTRACK"`) {
		t.Fatalf("backtrack=%d %s", response.Code, response.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
	awaitEventType(t, classroomUnderTest.studentEvents, "BACKTRACK_STARTED")
	session, _ := classroomUnderTest.read(t)
	if session.State != "BACKTRACK" || session.SubjectCode != "MATH" || session.SubjectName == "" || session.KnowledgePoint != "分数通分" || session.QuestionID == classroomUnderTest.originalQuestion {
		t.Fatalf("classroom did not switch to the prerequisite: state=%s subject=%s", session.State, session.SubjectCode)
	}
	return session
}

type backtrackEvidence struct {
	answers, analyses, stageEvidence, skillStates int
	skillDigest                                   string
}

func takeBacktrackEvidence(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID) backtrackEvidence {
	t.Helper()
	var evidence backtrackEvidence
	if err := pool.QueryRow(context.Background(), `
SELECT
  (SELECT count(*) FROM student_answers WHERE session_id=$1),
  (SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1),
  (SELECT count(*) FROM classroom_stage_evidence WHERE session_id=$1),
  (SELECT count(*) FROM student_skill_states ss JOIN learning_sessions ls ON ls.student_id=ss.student_id WHERE ls.id=$1),
  (SELECT COALESCE(md5(string_agg(ss.knowledge_point_id::text||ss.state||ss.score_internal::text,'|' ORDER BY ss.knowledge_point_id)),'') FROM student_skill_states ss JOIN learning_sessions ls ON ls.student_id=ss.student_id WHERE ls.id=$1)`, sessionID).Scan(
		&evidence.answers, &evidence.analyses, &evidence.stageEvidence, &evidence.skillStates, &evidence.skillDigest); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestLeavingTheBacktrackSupplyRestoresTheOriginalQuestion(t *testing.T) {
	classroomUnderTest := startBacktrackSupplyClassroom(t)
	ctx := context.Background()
	before, _ := classroomUnderTest.read(t)
	if before.QuestionID != classroomUnderTest.originalQuestion {
		t.Fatalf("classroom did not start on the original question")
	}
	classroomUnderTest.backtrack(t)

	var failsBefore int
	if err := classroomUnderTest.pool.QueryRow(ctx, `SELECT socratic_fail_count FROM learning_sessions WHERE id=$1`, classroomUnderTest.sessionID).Scan(&failsBefore); err != nil {
		t.Fatal(err)
	}
	evidenceBefore := takeBacktrackEvidence(t, classroomUnderTest.pool, classroomUnderTest.sessionID)
	callsBefore := classroomUnderTest.modelCalls.Load()

	returned := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/backtrack/return", classroomUnderTest.studentToken, nil)
	if returned.Code != http.StatusOK || !strings.Contains(returned.Body.String(), `"action":"RETURN"`) {
		t.Fatalf("return=%d %s", returned.Code, returned.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, returned.Body.Bytes())
	var result classroom.SubmitResult
	if err := json.Unmarshal(returned.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SocraticRound != failsBefore {
		t.Fatalf("return socratic round=%d want %d", result.SocraticRound, failsBefore)
	}
	event := awaitEventType(t, classroomUnderTest.studentEvents, "BACKTRACK_COMPLETED")
	assertStudentPayloadHasNoPrivateFields(t, event)

	after, _ := classroomUnderTest.read(t)
	if after.QuestionID != classroomUnderTest.originalQuestion || after.SubjectCode != before.SubjectCode || after.KnowledgePointID != before.KnowledgePointID {
		t.Fatalf("return landed on question=%s subject=%s, want the original question", after.QuestionID, after.SubjectCode)
	}
	if after.QuestionID == classroomUnderTest.easierSibling {
		t.Fatal("return landed on the easier sibling instead of the original question")
	}
	if after.Prompt != before.Prompt {
		t.Fatal("original question text changed after returning")
	}
	if after.State == "BACKTRACK" {
		t.Fatal("classroom is still backtracking after returning")
	}

	var subjectID, activeTaskID uuid.UUID
	var state, evidenceForm string
	var fails int
	var originQuestion *uuid.UUID
	var originForm *string
	if err := classroomUnderTest.pool.QueryRow(ctx, `SELECT subject_id,active_task_id,current_state,evidence_form,socratic_fail_count,backtrack_origin_question_id,backtrack_origin_evidence_form FROM learning_sessions WHERE id=$1`, classroomUnderTest.sessionID).Scan(&subjectID, &activeTaskID, &state, &evidenceForm, &fails, &originQuestion, &originForm); err != nil {
		t.Fatal(err)
	}
	if subjectID != uuid.MustParse("00000000-0000-4000-8000-000000000004") || activeTaskID != classroomUnderTest.originalKnowledgePoint {
		t.Fatalf("restored subject=%s active task=%s", subjectID, activeTaskID)
	}
	if state != "RETURN" || evidenceForm != "LIFE" || fails != failsBefore || originQuestion != nil || originForm != nil {
		t.Fatalf("restored state=%s evidence_form=%s fails=%d origin question=%v origin form=%v", state, evidenceForm, fails, originQuestion, originForm)
	}
	if evidenceAfter := takeBacktrackEvidence(t, classroomUnderTest.pool, classroomUnderTest.sessionID); evidenceAfter != evidenceBefore {
		t.Fatalf("returning wrote answer evidence: before=%+v after=%+v", evidenceBefore, evidenceAfter)
	}
	if calls := classroomUnderTest.modelCalls.Load(); calls != callsBefore {
		t.Fatalf("returning called the model %d times", calls-callsBefore)
	}

	again := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/backtrack/return", classroomUnderTest.studentToken, nil)
	if again.Code != http.StatusConflict {
		t.Fatalf("second return=%d %s", again.Code, again.Body.String())
	}
}

func TestBacktrackReturnIsRefusedWithoutABacktrack(t *testing.T) {
	classroomUnderTest := startBacktrackSupplyClassroom(t)
	before, _ := classroomUnderTest.read(t)

	response := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/backtrack/return", classroomUnderTest.studentToken, nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("return without backtrack=%d %s", response.Code, response.Body.String())
	}
	after, _ := classroomUnderTest.read(t)
	if after.QuestionID != before.QuestionID || after.State != before.State || after.Version != before.Version {
		t.Fatalf("refused return changed the classroom: state=%s version=%d", after.State, after.Version)
	}
}

// Finishing the prerequisite returns to the question the classroom left, not
// to the easiest released question of the original knowledge point.
func TestFinishingThePrerequisiteReturnsToTheExactOriginalQuestion(t *testing.T) {
	classroomUnderTest := startBacktrackSupplyClassroom(t)
	before, _ := classroomUnderTest.read(t)
	prerequisite := classroomUnderTest.backtrack(t)

	answer := privateTeacherAnswer(t, context.Background(), classroomUnderTest.pool, prerequisite.QuestionID)
	returned := performJSON(classroomUnderTest.router, http.MethodPost, "/api/v1/student/sessions/"+classroomUnderTest.sessionID.String()+"/answers", classroomUnderTest.studentToken, map[string]any{"answer": answer})
	if returned.Code != http.StatusOK || !strings.Contains(returned.Body.String(), `"action":"RETURN"`) {
		t.Fatalf("prerequisite completion=%d", returned.Code)
	}
	assertStudentPayloadHasNoPrivateFields(t, returned.Body.Bytes())
	after, _ := classroomUnderTest.read(t)
	if after.QuestionID != classroomUnderTest.originalQuestion || after.Prompt != before.Prompt || after.State != "RETURN" {
		t.Fatalf("completion returned to question=%s state=%s, want the original question", after.QuestionID, after.State)
	}
}
