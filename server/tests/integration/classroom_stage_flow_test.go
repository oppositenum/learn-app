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
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
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
	agent := &provenanceTeachingAgent{analysis: ai.AnalyzeAnswerResult{AnswerCorrect: true, Confidence: 0.99, WeaknessLayer: ai.WeaknessLayerNone}}
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
	if session.Interaction.Version != studentinteraction.Version || session.Interaction.Renderer != studentinteraction.RendererSingleChoice || session.Interaction.Fallback || session.Interaction.Scene == nil {
		t.Fatalf("student interaction=%+v", session.Interaction)
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

func TestStudentInteractionSessionContractFailsClosedAfterMaterialDrift(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	router := stageRouter(pool, classroom.NewService(pool, nil, nil, nil))
	session := startStageSession(t, router, fixture)
	if session.Interaction.Version != studentinteraction.Version || session.Interaction.Fallback || session.Interaction.Scene == nil {
		t.Fatalf("valid material=%+v", session.Interaction)
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET scene_public_json=jsonb_set(scene_public_json,'{renderer}','"UNKNOWN"') WHERE id=$1`, session.QuestionID); err != nil {
		t.Fatal(err)
	}
	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read drifted material=%d %s", read.Code, read.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, read.Body.Bytes())
	var drifted classroom.StudentSession
	if err := json.Unmarshal(read.Body.Bytes(), &drifted); err != nil {
		t.Fatal(err)
	}
	if !drifted.Interaction.Fallback || drifted.Interaction.Renderer != studentinteraction.RendererTextFallback || string(drifted.Scene) != "{}" || strings.Contains(read.Body.String(), "UNKNOWN") {
		t.Fatalf("drifted material was not sanitized: %s", read.Body.String())
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1`, session.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("fallback read wrote attempts=%d err=%v", attempts, err)
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
	if read.QuestionID != secondTask || read.StageFlow.Stage != classroom.StageOriginal {
		t.Fatalf("a failure left the current task or advanced the stage: %+v", read)
	}

	// Success on a task that received Socratic guidance is assisted, so the
	// independent proof still needs a fresh task.
	guided := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": read.StageFlow.Stage,
		"task_id": secondTask, "task_version": read.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, secondTask),
	})
	if guided.Code != http.StatusOK {
		t.Fatalf("guided success=%d %s", guided.Code, guided.Body.String())
	}
	var guidedResult classroom.StageSubmitResult
	if err := json.Unmarshal(guided.Body.Bytes(), &guidedResult); err != nil {
		t.Fatal(err)
	}
	if guidedResult.EvidenceKind != classroom.StageEvidenceAssisted || guidedResult.StageCompleted ||
		guidedResult.TaskID == nil || *guidedResult.TaskID == firstTask || *guidedResult.TaskID == secondTask {
		t.Fatalf("guided success result=%+v", guidedResult)
	}
	read = readStageSession(t, router, fixture.security.studentToken, session.ID)
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

func TestFourStageSocraticLimitAndContentExhaustionTerminateSafely(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	reviewQueueID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at)
VALUES($1,$2,$3,'MASTERY',now())`, reviewQueueID, fixture.security.studentID, fixture.knowledgePointID); err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	studentEvents, stopStudent := hub.Subscribe(fixture.security.studentID.String(), auth.RoleStudent)
	defer stopStudent()
	agent := &provenanceTeachingAgent{}
	service := classroom.NewService(pool, hub, nil, nil).WithTeachingAgent(agent)
	router := stageRouter(pool, service)
	session := startStageSession(t, router, fixture)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET review_queue_id=$2,evidence_form='REVIEW' WHERE id=$1`,
		session.ID, reviewQueueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_plan_blocks SET review_queue_id=$2,mode='REVIEW' WHERE id=$1`,
		fixture.planBlockID, reviewQueueID); err != nil {
		t.Fatal(err)
	}

	// §13.1: three effective Socratic rounds on the same task, then the limit
	// explanation with a parallel example, still on that task.
	firstTask := session.QuestionID
	ladder := []struct {
		action tutor.State
		code   string
		round  int
	}{
		{tutor.StateProbe, "SOCRATIC_GUIDED", 1},
		{tutor.StateScaffold, "SOCRATIC_GUIDED", 2},
		{tutor.StateAnalogy, "SOCRATIC_GUIDED", 3},
		{tutor.StateExplain, "SOCRATIC_LIMIT_EXPLAINED", 3},
	}
	for index, want := range ladder {
		current := readStageSession(t, router, fixture.security.studentToken, session.ID)
		if current.QuestionID != firstTask {
			t.Fatalf("failure %d left the original task: %s", index+1, current.QuestionID)
		}
		body := map[string]any{
			"operation_id": uuid.New(), "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": incorrectStageResponseAt(t, ctx, pool, current.QuestionID, index),
		}
		response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if response.Code != http.StatusOK {
			t.Fatalf("failure %d=%d %s", index+1, response.Code, response.Body.String())
		}
		var result classroom.StageSubmitResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var recordedCode string
		if err := pool.QueryRow(ctx, `SELECT response_code FROM classroom_stage_attempts WHERE operation_id=$1`, body["operation_id"]).Scan(&recordedCode); err != nil {
			t.Fatal(err)
		}
		if result.Action != want.action || recordedCode != want.code || result.Code != "" || result.SocraticRound != want.round ||
			result.Status != "ACTIVE" || result.EvidenceKind != classroom.StageEvidenceNone || result.StageCompleted ||
			result.TaskID == nil || *result.TaskID != firstTask {
			t.Fatalf("failure %d result=%+v want=%+v", index+1, result, want)
		}
		repeated := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if repeated.Code != http.StatusOK || repeated.Body.String() != response.Body.String() {
			t.Fatalf("failure %d idempotency first=%s repeated=%d/%s", index+1, response.Body.String(), repeated.Code, repeated.Body.String())
		}
	}
	if agent.generateCalls != 4 || strings.Join(agent.generators, ",") != "analogy,parallel-example" {
		t.Fatalf("feedback calls=%d generators=%v", agent.generateCalls, agent.generators)
	}
	var turnActions []string
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(array_agg(action ORDER BY sequence),'{}') FROM tutor_turns
WHERE session_id=$1 AND action IN ('PROBE','SCAFFOLD','ANALOGY','HINT','BACKTRACK','EXPLAIN','VOICE_EXPLAIN')`, session.ID).Scan(&turnActions); err != nil {
		t.Fatal(err)
	}
	if strings.Join(turnActions, ",") != "PROBE,SCAFFOLD,ANALOGY,EXPLAIN" {
		t.Fatalf("persisted tutor actions=%v", turnActions)
	}

	// Success on the original task after the explanation is assisted and moves
	// to a fresh task. Help on the remaining tasks then exhausts the stage.
	for step := 0; step < 3; step++ {
		current := readStageSession(t, router, fixture.security.studentToken, session.ID)
		if step > 0 {
			help := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, map[string]any{
				"type": "HINT", "operation_id": uuid.New(), "stage": current.StageFlow.Stage,
				"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			})
			if help.Code != http.StatusOK {
				t.Fatalf("help on task %d=%d %s", step+1, help.Code, help.Body.String())
			}
		}
		body := map[string]any{
			"operation_id": uuid.New(), "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": correctStageResponse(t, ctx, pool, current.QuestionID),
		}
		response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if response.Code != http.StatusOK {
			t.Fatalf("assisted success on task %d=%d %s", step+1, response.Code, response.Body.String())
		}
		var result classroom.StageSubmitResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if step < 2 {
			if result.EvidenceKind != classroom.StageEvidenceAssisted || result.StageCompleted ||
				result.TaskID == nil || *result.TaskID == current.QuestionID {
				t.Fatalf("assisted success on task %d=%+v", step+1, result)
			}
			continue
		}
		if result.Code != "CONTENT_EXHAUSTED" || result.Status != "ABANDONED" || result.EvidenceKind != classroom.StageEvidenceAssisted || result.StageCompleted {
			t.Fatalf("content exhaustion result=%+v", result)
		}
		repeated := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if repeated.Code != http.StatusOK || repeated.Body.String() != response.Body.String() {
			t.Fatalf("content exhaustion idempotency first=%s repeated=%d/%s", response.Body.String(), repeated.Code, repeated.Body.String())
		}
	}

	var attempts, independentEvidence, rewards, activityDays int
	var status, blockStatus, reviewStatus string
	var reviewAttempts int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1),
  (SELECT count(*)::int FROM classroom_stage_evidence WHERE session_id=$1 AND authorized_for_mastery),
  (SELECT count(*)::int FROM reward_events WHERE session_id=$1),
  (SELECT count(*)::int FROM student_activity_days WHERE student_id=$2),
  (SELECT status FROM learning_sessions WHERE id=$1),
  (SELECT status FROM learning_plan_blocks WHERE id=$3),
  (SELECT status FROM review_queue WHERE id=$4),
  (SELECT attempts FROM review_queue WHERE id=$4)`,
		session.ID, fixture.security.studentID, fixture.planBlockID, reviewQueueID).Scan(
		&attempts, &independentEvidence, &rewards, &activityDays,
		&status, &blockStatus, &reviewStatus, &reviewAttempts,
	); err != nil {
		t.Fatal(err)
	}
	if attempts != 9 || independentEvidence != 0 || rewards != 0 || activityDays != 0 || status != "ABANDONED" || blockStatus != "AVAILABLE" || reviewStatus != "PENDING" || reviewAttempts != 0 {
		t.Fatalf("terminal state attempts=%d independent=%d rewards=%d activity=%d session=%s block=%s review=%s/%d",
			attempts, independentEvidence, rewards, activityDays, status, blockStatus, reviewStatus, reviewAttempts)
	}
	awaitEventType(t, studentEvents, string(realtime.EventSessionAbandoned))

	reproofPool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, reproofPool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	reproofFixture := seedCompleteStageFixture(t, ctx, reproofPool)
	reproofQueueID := uuid.New()
	if _, err := reproofPool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at)
VALUES($1,$2,$3,'MASTERY',now())`, reproofQueueID, reproofFixture.security.studentID, reproofFixture.knowledgePointID); err != nil {
		t.Fatal(err)
	}
	reproofHub := realtime.NewHub()
	reproofStudentEvents, stopReproofStudent := reproofHub.Subscribe(reproofFixture.security.studentID.String(), auth.RoleStudent)
	defer stopReproofStudent()
	reproofAgent := &provenanceTeachingAgent{}
	reproofService := classroom.NewService(reproofPool, reproofHub, nil, nil).WithTeachingAgent(reproofAgent)
	reproofRouter := stageRouter(reproofPool, reproofService)
	reproofSession := startStageSession(t, reproofRouter, reproofFixture)
	if _, err := reproofPool.Exec(ctx, `UPDATE learning_sessions SET review_queue_id=$2,evidence_form='REVIEW' WHERE id=$1`,
		reproofSession.ID, reproofQueueID); err != nil {
		t.Fatal(err)
	}
	if _, err := reproofPool.Exec(ctx, `UPDATE learning_plan_blocks SET review_queue_id=$2,mode='REVIEW' WHERE id=$1`,
		reproofFixture.planBlockID, reproofQueueID); err != nil {
		t.Fatal(err)
	}

	for failure := 1; failure <= 4; failure++ {
		current := readStageSession(t, reproofRouter, reproofFixture.security.studentToken, reproofSession.ID)
		response := performJSON(reproofRouter, http.MethodPost, "/api/v1/student/sessions/"+reproofSession.ID.String()+"/answers", reproofFixture.security.studentToken, map[string]any{
			"operation_id": uuid.New(), "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": incorrectStageResponseAt(t, ctx, reproofPool, current.QuestionID, failure),
		})
		if response.Code != http.StatusOK {
			t.Fatalf("reproof setup failure %d=%d %s", failure, response.Code, response.Body.String())
		}
	}
	postExplain := readStageSession(t, reproofRouter, reproofFixture.security.studentToken, reproofSession.ID)
	reproofOperationID := uuid.New()
	reproofBody := map[string]any{
		"operation_id": reproofOperationID, "stage": postExplain.StageFlow.Stage,
		"task_id": postExplain.QuestionID, "task_version": postExplain.StageFlow.TaskVersion,
		// The fourth failure used index 4; the reproof differs from it.
		"response": incorrectStageResponseAt(t, ctx, reproofPool, postExplain.QuestionID, 5),
	}
	reproofFailure := performJSON(reproofRouter, http.MethodPost, "/api/v1/student/sessions/"+reproofSession.ID.String()+"/answers", reproofFixture.security.studentToken, reproofBody)
	if reproofFailure.Code != http.StatusOK {
		t.Fatalf("post-explanation reproof=%d %s", reproofFailure.Code, reproofFailure.Body.String())
	}
	var reproofResult classroom.StageSubmitResult
	if err := json.Unmarshal(reproofFailure.Body.Bytes(), &reproofResult); err != nil {
		t.Fatal(err)
	}
	if reproofResult.Code != "SOCRATIC_REPROOF_FAILED" || reproofResult.Status != "ABANDONED" ||
		reproofResult.SocraticRound != 3 || reproofResult.EvidenceKind != classroom.StageEvidenceNone || reproofResult.StageCompleted {
		t.Fatalf("post-explanation reproof result=%+v", reproofResult)
	}
	replayedReproofFailure := performJSON(reproofRouter, http.MethodPost, "/api/v1/student/sessions/"+reproofSession.ID.String()+"/answers", reproofFixture.security.studentToken, reproofBody)
	if replayedReproofFailure.Code != http.StatusOK || replayedReproofFailure.Body.String() != reproofFailure.Body.String() {
		t.Fatalf("post-explanation idempotency first=%s repeated=%d/%s", reproofFailure.Body.String(), replayedReproofFailure.Code, replayedReproofFailure.Body.String())
	}
	if reproofAgent.generateCalls != 4 {
		t.Fatalf("post-explanation failure generated another model turn: calls=%d", reproofAgent.generateCalls)
	}

	var reproofAttempts, reproofEvidence, reproofRewards, reproofActivityDays, reproofExplanations int
	var reproofStatus, reproofBlockStatus, reproofReviewStatus string
	var reproofReviewAttempts int
	if err := reproofPool.QueryRow(ctx, `
SELECT
  (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1),
  (SELECT count(*)::int FROM classroom_stage_evidence WHERE session_id=$1),
  (SELECT count(*)::int FROM reward_events WHERE session_id=$1),
  (SELECT count(*)::int FROM student_activity_days WHERE student_id=$2),
  (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1 AND response_code='SOCRATIC_LIMIT_EXPLAINED'),
  (SELECT status FROM learning_sessions WHERE id=$1),
  (SELECT status FROM learning_plan_blocks WHERE id=$3),
  (SELECT status FROM review_queue WHERE id=$4),
  (SELECT attempts FROM review_queue WHERE id=$4)`,
		reproofSession.ID, reproofFixture.security.studentID, reproofFixture.planBlockID, reproofQueueID).Scan(
		&reproofAttempts, &reproofEvidence, &reproofRewards, &reproofActivityDays, &reproofExplanations,
		&reproofStatus, &reproofBlockStatus, &reproofReviewStatus, &reproofReviewAttempts,
	); err != nil {
		t.Fatal(err)
	}
	if reproofAttempts != 5 || reproofEvidence != 0 || reproofRewards != 0 || reproofActivityDays != 0 ||
		reproofExplanations != 1 || reproofStatus != "ABANDONED" || reproofBlockStatus != "AVAILABLE" ||
		reproofReviewStatus != "PENDING" || reproofReviewAttempts != 0 {
		t.Fatalf("post-explanation state attempts=%d evidence=%d rewards=%d activity=%d explanations=%d session=%s block=%s review=%s/%d",
			reproofAttempts, reproofEvidence, reproofRewards, reproofActivityDays, reproofExplanations,
			reproofStatus, reproofBlockStatus, reproofReviewStatus, reproofReviewAttempts)
	}
	awaitEventType(t, reproofStudentEvents, string(realtime.EventSessionAbandoned))
}

// §13.2 in the stage classroom: a fixed emotion signal on a wrong answer takes
// a BREAK on the same task before any Socratic round. The round, the help level
// and the model call count stay where they were, and the next wrong answer
// without a signal continues the M02 rounds.
func TestFourStageEmotionSignalBreaksBeforeSocraticRounds(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	var fillTask uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT question_id FROM classroom_stage_tasks
WHERE lineage_id=$1 AND stage_role='ORIGINAL' AND selection_order=1`, fixture.lineageID).Scan(&fillTask); err != nil {
		t.Fatal(err)
	}
	scene := studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks,
		AccessibleFallback: "填写内容。", Slots: []studentinteraction.Item{{ID: "slot-1", Label: "内容"}},
	}
	rawScene, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := studentinteraction.AnswerSchema(scene)
	if !ok {
		t.Fatal("fill scene did not produce answer schema")
	}
	rule := json.RawMessage(`{"rule_type":"EXACT_FILL","expected_values":[{"slot_id":"slot-1","accepted_values":["已发布值"]}],"allowed_slot_ids":["slot-1"]}`)
	if _, err := pool.Exec(ctx, `UPDATE questions SET question_type='FILL_BLANKS',scene_public_json=$2,input_schema_json=$3 WHERE id=$1`,
		fillTask, rawScene, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE classroom_stage_tasks SET scoring_rule_version='exact-fill-v1',scoring_rule_private_json=$2 WHERE question_id=$1`,
		fillTask, rule); err != nil {
		t.Fatal(err)
	}
	agent := &provenanceTeachingAgent{}
	service := classroom.NewService(pool, realtime.NewHub(), nil, nil).WithTeachingAgent(agent)
	router := stageRouter(pool, service)
	session := startStageSession(t, router, fixture)
	if session.QuestionID != fillTask {
		t.Fatalf("session started on %s, want the fill task %s", session.QuestionID, fillTask)
	}
	answersPath := "/api/v1/student/sessions/" + session.ID.String() + "/answers"

	type sessionRow struct {
		round, assistance int
		state             string
	}
	readRow := func() sessionRow {
		var row sessionRow
		if err := pool.QueryRow(ctx, `SELECT socratic_fail_count,assistance_level,current_state FROM learning_sessions WHERE id=$1`,
			session.ID).Scan(&row.round, &row.assistance, &row.state); err != nil {
			t.Fatal(err)
		}
		return row
	}
	recorded := func(operationID uuid.UUID) (string, string) {
		var code, action string
		if err := pool.QueryRow(ctx, `SELECT response_code,response_action FROM classroom_stage_attempts WHERE operation_id=$1`,
			operationID).Scan(&code, &action); err != nil {
			t.Fatal(err)
		}
		return code, action
	}
	// submit answers the current task and checks the action, the recorded
	// code, the round, the task and the model call count after it.
	submit := func(label string, response map[string]any, action tutor.State, code string, round, calls int) {
		t.Helper()
		current := readStageSession(t, router, fixture.security.studentToken, session.ID)
		operationID := uuid.New()
		body := map[string]any{
			"operation_id": operationID, "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": response,
		}
		before := readRow()
		reply := performJSON(router, http.MethodPost, answersPath, fixture.security.studentToken, body)
		if reply.Code != http.StatusOK {
			t.Fatalf("%s=%d %s", label, reply.Code, reply.Body.String())
		}
		var result classroom.StageSubmitResult
		if err := json.Unmarshal(reply.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		recordedCode, recordedAction := recorded(operationID)
		if result.Action != action || recordedAction != string(action) || recordedCode != code || result.Code != "" ||
			result.SocraticRound != round || result.Status != "ACTIVE" || result.EvidenceKind != classroom.StageEvidenceNone ||
			result.TaskID == nil || *result.TaskID != current.QuestionID {
			t.Fatalf("%s result=%+v recorded=%s/%s want=%s/%s/%d", label, result, recordedCode, recordedAction, action, code, round)
		}
		if agent.generateCalls != calls {
			t.Fatalf("%s model calls=%d want=%d", label, agent.generateCalls, calls)
		}
		after := readRow()
		if after.round != round || after.state != string(current.StageFlow.Stage) {
			t.Fatalf("%s session=%+v want round %d on %s", label, after, round, current.StageFlow.Stage)
		}
		if action == tutor.StateBreak {
			if after.assistance != before.assistance || before.round != round {
				t.Fatalf("%s break changed session before=%+v after=%+v", label, before, after)
			}
			if result.Message != "先停一下，喝口水、动一动。准备好了再回到这道题，不着急。" {
				t.Fatalf("%s break message=%q", label, result.Message)
			}
		}
		repeated := performJSON(router, http.MethodPost, answersPath, fixture.security.studentToken, body)
		if repeated.Code != http.StatusOK || repeated.Body.String() != reply.Body.String() {
			t.Fatalf("%s idempotency first=%s repeated=%d/%s", label, reply.Body.String(), repeated.Code, repeated.Body.String())
		}
	}
	fill := func(value string) map[string]any {
		return map[string]any{"values": []map[string]string{{"slot_id": "slot-1", "value": value}}}
	}

	submit("first wrong fill", fill("5"), tutor.StateProbe, "SOCRATIC_GUIDED", 1, 1)
	for _, saying := range []string{"烦死了", "不想做", "随便吧", "你直接告诉我吧", "我不知道不知道"} {
		submit("fill saying "+saying, fill(saying), tutor.StateBreak, "EMOTION_BREAK", 1, 1)
	}
	if row := readRow(); row.assistance != 0 {
		t.Fatalf("emotion breaks raised the help level: %+v", row)
	}
	submit("different wrong fill after the break", fill("7"), tutor.StateScaffold, "SOCRATIC_GUIDED", 2, 2)
	submit("same wrong fill again", fill("7"), tutor.StateBreak, "EMOTION_BREAK", 2, 2)
	// "我不会" is not an emotion saying; typed into a blank it is an ordinary wrong answer.
	submit("我不会 typed into the blank", fill("我不会"), tutor.StateAnalogy, "SOCRATIC_GUIDED", 3, 3)
	if row := readRow(); row.assistance != 0 {
		t.Fatalf("Socratic rounds raised the help level: %+v", row)
	}

	// Asking for help stays the existing HINT, not a BREAK.
	current := readStageSession(t, router, fixture.security.studentToken, session.ID)
	helpOperation := uuid.New()
	help := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, map[string]any{
		"type": "HINT", "operation_id": helpOperation, "stage": current.StageFlow.Stage,
		"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
	})
	if help.Code != http.StatusOK {
		t.Fatalf("help=%d %s", help.Code, help.Body.String())
	}
	if code, action := recorded(helpOperation); code != "HELP_DELIVERED" || action != string(tutor.StateHint) {
		t.Fatalf("help recorded=%s/%s", code, action)
	}
	if agent.generateCalls != 4 {
		t.Fatalf("help model calls=%d", agent.generateCalls)
	}

	// The assisted success moves to a fresh selection task. There only an
	// identical wrong answer counts: no saying is read from option content.
	success := performJSON(router, http.MethodPost, answersPath, fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": current.StageFlow.Stage,
		"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
		"response": fill("已发布值"),
	})
	if success.Code != http.StatusOK {
		t.Fatalf("assisted success=%d %s", success.Code, success.Body.String())
	}
	optionTask := readStageSession(t, router, fixture.security.studentToken, session.ID).QuestionID
	if optionTask == fillTask {
		t.Fatal("assisted success stayed on the fill task")
	}
	submit("first wrong option", incorrectStageResponseAt(t, ctx, pool, optionTask, 0), tutor.StateProbe, "SOCRATIC_GUIDED", 1, 5)
	submit("same wrong option again", incorrectStageResponseAt(t, ctx, pool, optionTask, 0), tutor.StateBreak, "EMOTION_BREAK", 1, 5)
	submit("different wrong option after the break", incorrectStageResponseAt(t, ctx, pool, optionTask, 1), tutor.StateScaffold, "SOCRATIC_GUIDED", 2, 6)

	var breakTurns, analyzeCalls int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM tutor_turns WHERE session_id=$1 AND action='BREAK'`, session.ID).Scan(&breakTurns); err != nil {
		t.Fatal(err)
	}
	analyzeCalls = agent.analyzeCalls
	if breakTurns != 7 || analyzeCalls != 0 {
		t.Fatalf("break turns=%d analyze calls=%d", breakTurns, analyzeCalls)
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
	var invalidQuestionID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT question_id FROM classroom_stage_tasks WHERE lineage_id=$1 ORDER BY stage_role,selection_order LIMIT 1`, complete).Scan(&invalidQuestionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET scene_public_json=jsonb_set(scene_public_json,'{version}','"student-interaction-v2"') WHERE id=$1`, invalidQuestionID); err != nil {
		t.Fatal(err)
	}
	invalidStart := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if invalidStart.Code != http.StatusConflict || !strings.Contains(invalidStart.Body.String(), "CLASSROOM_STAGE_CONTENT_INCOMPLETE") {
		t.Fatalf("unknown interaction version preflight=%d %s", invalidStart.Code, invalidStart.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET scene_public_json=jsonb_set(scene_public_json,'{version}','"student-interaction-v1"') WHERE id=$1`, invalidQuestionID); err != nil {
		t.Fatal(err)
	}
	session := startStageSession(t, router, fixture)
	var driftedCandidateID uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT question_id FROM classroom_stage_tasks
WHERE lineage_id=$1 AND stage_role='ORIGINAL' AND question_id<>$2
ORDER BY selection_order LIMIT 1`, fixture.lineageID, session.QuestionID).Scan(&driftedCandidateID); err != nil {
		t.Fatal(err)
	}
	var originalCandidateRule json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT scoring_rule_private_json FROM classroom_stage_tasks WHERE question_id=$1`, driftedCandidateID).Scan(&originalCandidateRule); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE classroom_stage_tasks SET scoring_rule_private_json='{}' WHERE question_id=$1`, driftedCandidateID); err != nil {
		t.Fatal(err)
	}
	// A failure stays on the current task, so the candidate is selected by the
	// assisted success that follows help.
	help := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, map[string]any{
		"type": "HINT", "operation_id": uuid.New(), "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
	})
	if help.Code != http.StatusOK {
		t.Fatalf("help before drifted selection=%d %s", help.Code, help.Body.String())
	}
	driftedSelection := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, session.QuestionID),
	})
	if driftedSelection.Code != http.StatusServiceUnavailable || driftedSelection.Header().Get("Cache-Control") != "no-store" || !strings.Contains(driftedSelection.Body.String(), "CLASSROOM_STAGE_UNAVAILABLE") || strings.Contains(driftedSelection.Body.String(), "CONTENT_EXHAUSTED") {
		t.Fatalf("drifted candidate selection=%d %s", driftedSelection.Code, driftedSelection.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE classroom_stage_tasks SET scoring_rule_private_json=$2 WHERE question_id=$1`, driftedCandidateID, originalCandidateRule); err != nil {
		t.Fatal(err)
	}
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
	var answers, helps int
	var state string
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1 AND attempt_kind='ANSWER'),
       (SELECT count(*)::int FROM classroom_stage_attempts WHERE session_id=$1 AND attempt_kind='HELP'),
       current_state
FROM learning_sessions WHERE id=$1`, session.ID).Scan(&answers, &helps, &state); err != nil {
		t.Fatal(err)
	}
	if answers != 0 || helps != 1 || state != "ORIGINAL" {
		t.Fatalf("runtime drift mutated classroom answers=%d helps=%d state=%s", answers, helps, state)
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
	// PostgreSQL timestamptz persists microseconds; compare against the value
	// the database can represent rather than platform-specific clock nanos.
	pausedAt := clock.Now().Truncate(time.Microsecond)
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
			interactionScene := studentinteraction.Scene{
				Version: studentinteraction.Version, Renderer: studentinteraction.RendererSingleChoice,
				AccessibleFallback: "请选择唯一符合题目要求的选项。",
				Options: []studentinteraction.Item{
					{ID: optionPrefix + "-a", Label: "选项一"},
					{ID: optionPrefix + "-b", Label: "选项二"},
					{ID: optionPrefix + "-c", Label: "选项三"},
				},
			}
			scene, _ := json.Marshal(interactionScene)
			schema, ok := studentinteraction.AnswerSchema(interactionScene)
			if !ok {
				t.Fatal("integration scene did not produce a schema")
			}
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
	return incorrectStageResponseAt(t, ctx, pool, questionID, 0)
}

// incorrectStageResponseAt picks one of the wrong options by index. Consecutive
// failures alternate indexes so that no wrong answer repeats the previous one,
// which the stage classroom reads as an emotion signal.
func incorrectStageResponseAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID, index int) map[string]any {
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
	var wrong []string
	for _, option := range rule.Allowed {
		matched := false
		for _, expected := range rule.Expected {
			matched = matched || option == expected
		}
		if !matched {
			wrong = append(wrong, option)
		}
	}
	if len(wrong) == 0 {
		t.Fatal("stage fixture has no deterministic incorrect option")
	}
	return map[string]any{"selected_option_ids": []string{wrong[index%len(wrong)]}}
}

func stageRouter(pool *pgxpool.Pool, service *classroom.Service) http.Handler {
	parents := parent.NewRepository(pool)
	return api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Parents:      parents,
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
