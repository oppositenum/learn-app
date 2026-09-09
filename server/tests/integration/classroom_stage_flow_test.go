package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type stageFixture struct {
	security         securityFixture
	studentUserID    uuid.UUID
	knowledgePointID uuid.UUID
	lineageID        uuid.UUID
	planBlockID      uuid.UUID
}

type blockingStageAgent struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStageAgent() *blockingStageAgent {
	return &blockingStageAgent{started: make(chan struct{}), release: make(chan struct{})}
}

func (agent *blockingStageAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	return ai.AnalyzeAnswerResult{}, errors.New("stage feedback must not request model correctness")
}

func (agent *blockingStageAgent) GenerateTurn(ctx context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.once.Do(func() { close(agent.started) })
	select {
	case <-ctx.Done():
		return ai.TutorTurn{}, ctx.Err()
	case <-agent.release:
	}
	return ai.TutorTurn{Action: request.TutorDecision.NextState, Message: "只提供反馈，不决定阶段结果。"}, nil
}

func (agent *blockingStageAgent) GenerateAnalogy(ctx context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func (agent *blockingStageAgent) GenerateParallelExample(ctx context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func (agent *blockingStageAgent) GenerateExplanation(ctx context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func TestFourStageClassroomCompletesRealStagesWithoutAIOrPrivateDisclosure(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	agent := &provenanceTeachingAgent{analysis: ai.AnalyzeAnswerResult{AnswerCorrect: true, Confidence: 0.99}}
	service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithTeachingAgent(agent)
	router := stageRouter(pool, service)

	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, start.Body.Bytes())
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.StageFlow == nil || session.StageFlow.Stage != classroom.StageOriginal || session.State != string(classroom.StageOriginal) {
		t.Fatalf("stage start=%+v flow=%+v", session, session.StageFlow)
	}

	wantStages := []classroom.Stage{classroom.StageVariant, classroom.StageAbstract, classroom.StageVerify, classroom.StageComplete}
	for _, wantStage := range wantStages {
		response := correctStageResponse(t, ctx, pool, session.QuestionID)
		submit := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
			"operation_id": uuid.New(), "stage": session.StageFlow.Stage,
			"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
			"response": response,
		})
		if submit.Code != http.StatusOK {
			t.Fatalf("submit stage=%s status=%d body=%s", session.StageFlow.Stage, submit.Code, submit.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, submit.Body.Bytes())
		if strings.Contains(submit.Body.String(), "deterministic_result") {
			t.Fatalf("student response exposed internal correctness: %s", submit.Body.String())
		}
		read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
		if read.Code != http.StatusOK {
			t.Fatalf("read=%d %s", read.Code, read.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, read.Body.Bytes())
		if err := json.Unmarshal(read.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if wantStage == classroom.StageComplete {
			if session.Status != "COMPLETED" || session.State != string(classroom.StageComplete) {
				t.Fatalf("completed session=%+v", session)
			}
			continue
		}
		if session.StageFlow == nil || session.StageFlow.Stage != wantStage || session.State != string(wantStage) {
			t.Fatalf("next stage=%+v want=%s", session.StageFlow, wantStage)
		}
	}
	if agent.analyzeCalls != 0 || agent.generateCalls != 0 {
		t.Fatalf("deterministic correct path called AI analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	}

	var attempts, completedStages, evidenceRows, authorizedRows, responseColumns int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int,count(*) FILTER(WHERE stage_completed)::int
FROM classroom_stage_attempts WHERE session_id=$1`, session.ID).Scan(&attempts, &completedStages); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int,count(*) FILTER(WHERE authorized_for_mastery)::int
FROM classroom_stage_evidence WHERE session_id=$1`, session.ID).Scan(&evidenceRows, &authorizedRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name IN('classroom_stage_attempts','classroom_stage_evidence')
  AND column_name IN('answer','answer_text','student_answer','response','response_body','model_reason','provider_response')`).Scan(&responseColumns); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || completedStages != 4 || evidenceRows != 4 || authorizedRows != 4 || responseColumns != 0 {
		t.Fatalf("attempts=%d completed=%d evidence=%d authorized=%d content_columns=%d", attempts, completedStages, evidenceRows, authorizedRows, responseColumns)
	}
	var independent, life, variant, textbook int
	var state string
	if err := pool.QueryRow(ctx, `
SELECT state,independent_successes,life_context_successes,variant_successes,textbook_successes
FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.security.studentID, fixture.knowledgePointID).Scan(&state, &independent, &life, &variant, &textbook); err != nil {
		t.Fatal(err)
	}
	if state != "UNDERSTOOD" || independent != 4 || life != 1 || variant != 2 || textbook != 1 {
		t.Fatalf("skill state=%s independent=%d forms=%d/%d/%d", state, independent, life, variant, textbook)
	}
}

func TestFourStageHelpAndFailureRequireFreshNeverPresentedReproof(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	agent := &provenanceTeachingAgent{}
	service := classroom.NewService(pool, nil, nil, nil).WithTeachingAgent(agent)
	router := stageRouter(pool, service)
	session := startStageSession(t, router, fixture)
	firstTask := session.QuestionID

	helpOperation := uuid.New()
	helpBody := map[string]any{
		"type": "HINT", "operation_id": helpOperation, "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
	}
	help := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, helpBody)
	if help.Code != http.StatusOK {
		t.Fatalf("help=%d %s", help.Code, help.Body.String())
	}
	repeatedHelp := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, helpBody)
	if repeatedHelp.Code != http.StatusOK || repeatedHelp.Body.String() != help.Body.String() {
		t.Fatalf("idempotent help first=%s repeated=%d/%s", help.Body.String(), repeatedHelp.Code, repeatedHelp.Body.String())
	}
	if agent.generateCalls != 1 {
		t.Fatalf("idempotent help generated %d model turns", agent.generateCalls)
	}

	assistedOperation := uuid.New()
	assistedBody := map[string]any{
		"operation_id": assistedOperation, "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, session.QuestionID),
	}
	assisted := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, assistedBody)
	if assisted.Code != http.StatusOK {
		t.Fatalf("assisted=%d %s", assisted.Code, assisted.Body.String())
	}
	var assistedResult classroom.StageSubmitResult
	if err := json.Unmarshal(assisted.Body.Bytes(), &assistedResult); err != nil {
		t.Fatal(err)
	}
	if assistedResult.EvidenceKind != classroom.StageEvidenceAssisted || assistedResult.StageCompleted || assistedResult.TaskID == nil || *assistedResult.TaskID == firstTask {
		t.Fatalf("assisted result=%+v", assistedResult)
	}
	secondTask := *assistedResult.TaskID

	conflictBody := map[string]any{
		"operation_id": assistedOperation, "stage": classroom.StageOriginal,
		"task_id": firstTask, "task_version": session.StageFlow.TaskVersion,
		"response": map[string]any{"selected_option_ids": []string{"different"}},
	}
	conflict := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, conflictBody)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("response-bound operation conflict=%d %s", conflict.Code, conflict.Body.String())
	}

	read := readStageSession(t, router, fixture.security.studentToken, session.ID)
	wrong := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": read.StageFlow.Stage,
		"task_id": read.QuestionID, "task_version": read.StageFlow.TaskVersion,
		"response": map[string]any{"selected_option_ids": []string{"wrong-option"}},
	})
	if wrong.Code != http.StatusOK {
		t.Fatalf("wrong=%d %s", wrong.Code, wrong.Body.String())
	}
	read = readStageSession(t, router, fixture.security.studentToken, session.ID)
	if read.QuestionID == firstTask || read.QuestionID == secondTask || read.StageFlow.Stage != classroom.StageOriginal {
		t.Fatalf("failed task was repeated or stage advanced: %+v", read)
	}
	thirdTask := read.QuestionID

	finishStage := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": read.StageFlow.Stage,
		"task_id": thirdTask, "task_version": read.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, thirdTask),
	})
	if finishStage.Code != http.StatusOK {
		t.Fatalf("fresh reproof=%d %s", finishStage.Code, finishStage.Body.String())
	}
	var stageResult classroom.StageSubmitResult
	if err := json.Unmarshal(finishStage.Body.Bytes(), &stageResult); err != nil {
		t.Fatal(err)
	}
	if !stageResult.StageCompleted || stageResult.Stage != classroom.StageVariant || stageResult.EvidenceKind != classroom.StageEvidenceIndependent {
		t.Fatalf("fresh independent reproof=%+v", stageResult)
	}
	var presented int
	if err := pool.QueryRow(ctx, `
SELECT count(DISTINCT question_id)::int FROM classroom_stage_attempts
WHERE session_id=$1 AND submitted_stage='ORIGINAL'`, session.ID).Scan(&presented); err != nil {
		t.Fatal(err)
	}
	if presented != 3 {
		t.Fatalf("presented original tasks=%d", presented)
	}
}

func TestFourStageContentPreflightAndRuntimeDriftFailClosed(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedStageFixture(t, ctx, pool, 2)
	service := classroom.NewService(pool, nil, nil, nil)
	router := stageRouter(pool, service)
	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), "CLASSROOM_STAGE_CONTENT_INCOMPLETE") {
		t.Fatalf("incomplete preflight=%d %s", start.Code, start.Body.String())
	}
	var sessions int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM classroom_stage_sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("incomplete preflight created sessions=%d err=%v", sessions, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE classroom_task_lineages SET status='RETIRED' WHERE id=$1`, fixture.lineageID); err != nil {
		t.Fatal(err)
	}
	complete := seedAdditionalReadyLineage(t, ctx, pool, fixture, 3)
	fixture.lineageID = complete
	session := startStageSession(t, router, fixture)
	if _, err := pool.Exec(ctx, `UPDATE questions SET status='QUARANTINED' WHERE id=$1`, session.QuestionID); err != nil {
		t.Fatal(err)
	}
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, session.QuestionID),
	})
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "CLASSROOM_STAGE_UNAVAILABLE") {
		t.Fatalf("quarantined runtime task=%d %s", response.Code, response.Body.String())
	}
	var attempts int
	var state string
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1),current_state
FROM learning_sessions WHERE id=$1`, session.ID).Scan(&attempts, &state); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || state != "ORIGINAL" {
		t.Fatalf("runtime drift mutated classroom attempts=%d state=%s", attempts, state)
	}
}

func TestFourStageFeedbackCommitPreservesVisibilityPause(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	clock := newLifecycleTestClock(time.Now().UTC())
	agent := newBlockingStageAgent()
	service := classroom.NewService(pool, nil, nil, nil).WithClock(clock.Now).WithTeachingAgent(agent)
	router := stageRouter(pool, service)
	session := startStageSession(t, router, fixture)
	response, err := json.Marshal(incorrectStageResponse(t, ctx, pool, session.QuestionID))
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result classroom.StageSubmitResult
		err    error
	}
	resultChannel := make(chan outcome, 1)
	clock.Add(20 * time.Second)
	go func() {
		result, submitErr := service.SubmitStage(ctx, fixture.studentUserID, classroom.StageSubmitRequest{
			SessionID: session.ID, OperationID: uuid.New(), Stage: session.StageFlow.Stage,
			TaskID: session.QuestionID, TaskVersion: session.StageFlow.TaskVersion,
			Kind: classroom.StageAttemptAnswer, Response: response,
		})
		resultChannel <- outcome{result: result, err: submitErr}
	}()
	awaitAgentStart(t, agent.started)
	clock.Add(5 * time.Second)
	pausedAt := clock.Now()
	paused, err := service.PauseSession(ctx, fixture.studentUserID, session.ID)
	if err != nil || paused.Status != "PAUSED" || paused.ActiveSeconds != 25 {
		t.Fatalf("pause during stage feedback=%+v err=%v", paused, err)
	}
	close(agent.release)
	var submitted outcome
	select {
	case submitted = <-resultChannel:
	case <-time.After(5 * time.Second):
		t.Fatal("stage feedback did not finish")
	}
	if submitted.err != nil || submitted.result.Status != "PAUSED" || submitted.result.CurrentSeconds != 0 || submitted.result.ActiveSeconds != 25 {
		t.Fatalf("stage result after pause=%+v err=%v", submitted.result, submitted.err)
	}
	var status string
	var lastActivity time.Time
	if err := pool.QueryRow(ctx, `SELECT status,last_activity_at FROM learning_sessions WHERE id=$1`, session.ID).Scan(&status, &lastActivity); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" || !lastActivity.Equal(pausedAt) {
		t.Fatalf("stage submit rewrote pause status=%s last_activity=%s want=%s", status, lastActivity, pausedAt)
	}
}

func seedCompleteStageFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) stageFixture {
	t.Helper()
	return seedStageFixture(t, ctx, pool, 3)
}

func seedStageFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tasksPerStage int) stageFixture {
	t.Helper()
	security := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, security.sessionID); err != nil {
		t.Fatal(err)
	}
	studentUserID := fixtureStudentUserID(t, ctx, pool, security.studentID)
	var knowledgePointID, subjectID uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT question.knowledge_point_id,knowledge_point.subject_id
FROM questions question JOIN knowledge_points knowledge_point ON knowledge_point.id=question.knowledge_point_id
WHERE question.id=$1`, security.releasedQuestionID).Scan(&knowledgePointID, &subjectID); err != nil {
		t.Fatal(err)
	}
	fixture := stageFixture{security: security, studentUserID: studentUserID, knowledgePointID: knowledgePointID}
	fixture.lineageID = seedAdditionalReadyLineage(t, ctx, pool, fixture, tasksPerStage)
	fixture.planBlockID = uuid.New()
	planID := uuid.New()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	date := time.Now().In(shanghai).Format("2006-01-02")
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,$3::date,20,'PROPOSED')`, planID, security.studentID, date); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status)
VALUES($1,$2,1,$3,$4,20,'CURRENT_GRADE','four-stage integration fixture','AVAILABLE')`, fixture.planBlockID, planID, subjectID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func seedAdditionalReadyLineage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture stageFixture, tasksPerStage int) uuid.UUID {
	t.Helper()
	lineageID := uuid.New()
	version := "stage-integration-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
INSERT INTO classroom_task_lineages(id,knowledge_point_id,version,status)
VALUES($1,$2,$3,'READY')`, lineageID, fixture.knowledgePointID, version); err != nil {
		t.Fatal(err)
	}
	sourceID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO content_sources(id,name,source_type,license_code)
VALUES($1,'four-stage integration fixture','INTERNAL_RULE','INTERNAL')`, sourceID); err != nil {
		t.Fatal(err)
	}
	forms := map[classroom.Stage]string{
		classroom.StageOriginal: "LIFE", classroom.StageVariant: "VARIANT",
		classroom.StageAbstract: "VARIANT", classroom.StageVerify: "TEXTBOOK",
	}
	for _, stage := range []classroom.Stage{classroom.StageOriginal, classroom.StageVariant, classroom.StageAbstract, classroom.StageVerify} {
		for order := 1; order <= tasksPerStage; order++ {
			questionID := uuid.New()
			optionPrefix := strings.ToLower(string(stage)) + fmt.Sprintf("-%d", order)
			scene, _ := json.Marshal(map[string]any{
				"kind": "CHOICE_SET",
				"options": []map[string]string{
					{"id": optionPrefix + "-a", "text": "选项一"},
					{"id": optionPrefix + "-b", "text": "选项二"},
					{"id": optionPrefix + "-c", "text": "选项三"},
				},
			})
			schema := `{"type":"object","properties":{"selected_option_ids":{"type":"array","items":{"type":"string"}}},"required":["selected_option_ids"],"additionalProperties":false}`
			if _, err := pool.Exec(ctx, `
INSERT INTO questions(id,knowledge_point_id,difficulty,question_type,prompt_public,scene_public_json,input_schema_json,status,content_version)
VALUES($1,$2,'L2','STRUCTURED_SELECTION',$3,$4,$5,'DRAFT',$6)`, questionID,
				fixture.knowledgePointID, fmt.Sprintf("%s 阶段任务 %d", stage, order), scene, schema, version); err != nil {
				t.Fatal(err)
			}
			rule, _ := json.Marshal(map[string]any{
				"rule_type": "EXACT_OPTION_SET", "expected_option_ids": []string{optionPrefix + "-a"},
				"allowed_option_ids": []string{optionPrefix + "-a", optionPrefix + "-b", optionPrefix + "-c"},
			})
			if _, err := pool.Exec(ctx, `
INSERT INTO question_private_answers(question_id,correct_answer_json,full_solution_private,teacher_reference_answer,scoring_key_json,misconceptions_private_json,hint_policy_private_json)
VALUES($1,$2,'integration private solution','integration private answer',$2,'[]','{}')`, questionID, rule); err != nil {
				t.Fatal(err)
			}
			versionID, validationID, reviewID := uuid.New(), uuid.New(), uuid.New()
			if _, err := pool.Exec(ctx, `
INSERT INTO content_versions(id,question_id,version,schema_version,generator_provider,generator_model,source_id,asset_json)
VALUES($1,$2,$3,'content-question-v1','fixture','fixture-generator',$4,'{}')`, versionID, questionID, version, sourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO content_validations(id,question_id,content_version,schema_version,status,checks_json,validator_version)
VALUES($1,$2,$3,'content-question-v1','PASS','[]','fixture-v1')`, validationID, questionID, version); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE questions SET status='AUTOMATIC_VALIDATED' WHERE id=$1`, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO content_reviews(id,question_id,content_version,schema_version,result,reviewer_provider,reviewer_model,findings_json)
VALUES($1,$2,$3,'content-question-v1','PASS','fixture-secondary','fixture-reviewer','[]')`, reviewID, questionID, version); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE questions SET status='AI_REVIEWED' WHERE id=$1`, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE questions SET status='RELEASED' WHERE id=$1`, questionID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO content_release_records(id,question_id,from_status,to_status,validation_id,review_id,reason)
VALUES($1,$2,'AI_REVIEWED','RELEASED',$3,$4,'four-stage integration fixture')`, uuid.New(), questionID, validationID, reviewID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO classroom_stage_tasks(question_id,lineage_id,stage_role,selection_order,scoring_rule_version,scoring_rule_private_json,evidence_form)
VALUES($1,$2,$3,$4,'exact-option-set-v1',$5,$6)`, questionID, lineageID, stage, order, rule, forms[stage]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return lineageID
}

func correctStageResponse(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID) map[string]any {
	t.Helper()
	var rule struct {
		Expected []string `json:"expected_option_ids"`
	}
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT scoring_rule_private_json FROM classroom_stage_tasks WHERE question_id=$1`, questionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"selected_option_ids": rule.Expected}
}

func incorrectStageResponse(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID) map[string]any {
	t.Helper()
	var rule struct {
		Expected []string `json:"expected_option_ids"`
		Allowed  []string `json:"allowed_option_ids"`
	}
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT scoring_rule_private_json FROM classroom_stage_tasks WHERE question_id=$1`, questionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	for _, option := range rule.Allowed {
		matched := false
		for _, expected := range rule.Expected {
			matched = matched || option == expected
		}
		if !matched {
			return map[string]any{"selected_option_ids": []string{option}}
		}
	}
	t.Fatal("stage fixture has no deterministic incorrect option")
	return nil
}

func stageRouter(pool *pgxpool.Pool, service *classroom.Service) http.Handler {
	parents := parent.NewRepository(pool)
	return api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parents, planner.NewService(pool)),
	})
}

func startStageSession(t *testing.T, router http.Handler, fixture stageFixture) classroom.StudentSession {
	t.Helper()
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if response.Code != http.StatusOK {
		t.Fatalf("start stage session=%d %s", response.Code, response.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.StageFlow == nil {
		t.Fatal("stage session did not expose stage flow contract")
	}
	return session
}

func readStageSession(t *testing.T, router http.Handler, token string, sessionID uuid.UUID) classroom.StudentSession {
	t.Helper()
	response := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+sessionID.String(), token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("read stage session=%d %s", response.Code, response.Body.String())
	}
	var session classroom.StudentSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}
