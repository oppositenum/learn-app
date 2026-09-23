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

	seen := map[uuid.UUID]struct{}{session.QuestionID: {}}
	for wantRound := 1; wantRound <= 3; wantRound++ {
		current := readStageSession(t, router, fixture.security.studentToken, session.ID)
		operationID := uuid.New()
		body := map[string]any{
			"operation_id": operationID, "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": incorrectStageResponse(t, ctx, pool, current.QuestionID),
		}
		response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if response.Code != http.StatusOK {
			t.Fatalf("failure round %d=%d %s", wantRound, response.Code, response.Body.String())
		}
		var result classroom.StageSubmitResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.SocraticRound != wantRound {
			t.Fatalf("failure round result=%+v want=%d", result, wantRound)
		}
		if wantRound < 3 {
			if result.TaskID == nil {
				t.Fatalf("round %d did not select a task: %+v", wantRound, result)
			}
			if _, duplicate := seen[*result.TaskID]; duplicate {
				t.Fatalf("round %d repeated task %s", wantRound, *result.TaskID)
			}
			seen[*result.TaskID] = struct{}{}
		} else if result.TaskID == nil || *result.TaskID != current.QuestionID || result.Action != "EXPLAIN" {
			t.Fatalf("third failure did not keep and explain current task: %+v", result)
		}
		repeated := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, body)
		if repeated.Code != http.StatusOK || repeated.Body.String() != response.Body.String() {
			t.Fatalf("round %d idempotency first=%s repeated=%d/%s", wantRound, response.Body.String(), repeated.Code, repeated.Body.String())
		}
	}
	if len(seen) != 3 || agent.generateCalls != 3 {
		t.Fatalf("presented=%d feedback_calls=%d", len(seen), agent.generateCalls)
	}

	current := readStageSession(t, router, fixture.security.studentToken, session.ID)
	finalOperation := uuid.New()
	finalBody := map[string]any{
		"operation_id": finalOperation, "stage": current.StageFlow.Stage,
		"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
		"response": correctStageResponse(t, ctx, pool, current.QuestionID),
	}
	final := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, finalBody)
	if final.Code != http.StatusOK {
		t.Fatalf("content exhaustion=%d %s", final.Code, final.Body.String())
	}
	var result classroom.StageSubmitResult
	if err := json.Unmarshal(final.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != "CONTENT_EXHAUSTED" || result.Status != "ABANDONED" || result.EvidenceKind != classroom.StageEvidenceAssisted || result.StageCompleted {
		t.Fatalf("content exhaustion result=%+v", result)
	}
	repeatedFinal := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, finalBody)
	if repeatedFinal.Code != http.StatusOK || repeatedFinal.Body.String() != final.Body.String() {
		t.Fatalf("content exhaustion idempotency first=%s repeated=%d/%s", final.Body.String(), repeatedFinal.Code, repeatedFinal.Body.String())
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
	if attempts != 4 || independentEvidence != 0 || rewards != 0 || activityDays != 0 || status != "ABANDONED" || blockStatus != "AVAILABLE" || reviewStatus != "PENDING" || reviewAttempts != 0 {
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

	for wantRound := 1; wantRound <= 3; wantRound++ {
		current := readStageSession(t, reproofRouter, reproofFixture.security.studentToken, reproofSession.ID)
		response := performJSON(reproofRouter, http.MethodPost, "/api/v1/student/sessions/"+reproofSession.ID.String()+"/answers", reproofFixture.security.studentToken, map[string]any{
			"operation_id": uuid.New(), "stage": current.StageFlow.Stage,
			"task_id": current.QuestionID, "task_version": current.StageFlow.TaskVersion,
			"response": incorrectStageResponse(t, ctx, reproofPool, current.QuestionID),
		})
		if response.Code != http.StatusOK {
			t.Fatalf("reproof setup round %d=%d %s", wantRound, response.Code, response.Body.String())
		}
	}
	postExplain := readStageSession(t, reproofRouter, reproofFixture.security.studentToken, reproofSession.ID)
	reproofOperationID := uuid.New()
	reproofBody := map[string]any{
		"operation_id": reproofOperationID, "stage": postExplain.StageFlow.Stage,
		"task_id": postExplain.QuestionID, "task_version": postExplain.StageFlow.TaskVersion,
		"response": incorrectStageResponse(t, ctx, reproofPool, postExplain.QuestionID),
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
	if reproofAgent.generateCalls != 3 {
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
	if reproofAttempts != 4 || reproofEvidence != 0 || reproofRewards != 0 || reproofActivityDays != 0 ||
		reproofExplanations != 1 || reproofStatus != "ABANDONED" || reproofBlockStatus != "AVAILABLE" ||
		reproofReviewStatus != "PENDING" || reproofReviewAttempts != 0 {
		t.Fatalf("post-explanation state attempts=%d evidence=%d rewards=%d activity=%d explanations=%d session=%s block=%s review=%s/%d",
			reproofAttempts, reproofEvidence, reproofRewards, reproofActivityDays, reproofExplanations,
			reproofStatus, reproofBlockStatus, reproofReviewStatus, reproofReviewAttempts)
	}
	awaitEventType(t, reproofStudentEvents, string(realtime.EventSessionAbandoned))
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
	driftedSelection := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
		"response": incorrectStageResponse(t, ctx, pool, session.QuestionID),
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
