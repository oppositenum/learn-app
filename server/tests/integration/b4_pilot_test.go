package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroompilot"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

var pilotKnowledgePointID = uuid.MustParse("30000000-0000-4000-8000-000000000004")

type pilotStructuredClient struct {
	analysisCorrect bool
	mu              sync.Mutex
	analysisCalls   int
	generationCalls int
}

func (client *pilotStructuredClient) GenerateStructured(
	_ context.Context,
	request ai.StructuredRequest,
) (ai.StructuredResult, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	switch request.Purpose {
	case ai.PurposeAnswerAnalysis:
		client.analysisCalls++
		output, _ := json.Marshal(map[string]any{
			"answer_correct": client.analysisCorrect, "reasoning_quality": "WEAK",
			"confidence": 0.99, "error_type": "PILOT_SELECTION_MISMATCH",
			"misconceptions": []string{}, "core_ability_signals": []any{},
			"emotion_signal": "NEUTRAL", "engagement": "NORMAL",
			"recommended_action": "PROBE", "safe_to_increase_difficulty": false,
		})
		return ai.StructuredResult{
			ResponseID: fmt.Sprintf("pilot-analysis-%d", client.analysisCalls),
			OutputJSON: output,
		}, nil
	case ai.PurposeSocraticTurn:
		client.generationCalls++
		var input struct {
			TutorDecision struct {
				NextState tutor.State
			} `json:"tutor_decision"`
		}
		if err := json.Unmarshal(request.Input, &input); err != nil {
			return ai.StructuredResult{}, err
		}
		output, _ := json.Marshal(map[string]any{
			"message": "先按问题要求给信息分类，再逐项核对。",
			"action":  input.TutorDecision.NextState, "answer_revealed": false,
			"segments": []any{},
		})
		return ai.StructuredResult{
			ResponseID: fmt.Sprintf("pilot-turn-%d", client.generationCalls),
			OutputJSON: output,
		}, nil
	default:
		return ai.StructuredResult{}, fmt.Errorf("unexpected pilot AI purpose %s", request.Purpose)
	}
}

func (client *pilotStructuredClient) counts() (int, int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.analysisCalls, client.generationCalls
}

type pilotReviewer struct {
	mu    sync.Mutex
	calls int
}

func (reviewer *pilotReviewer) ReviewTutorOutput(
	_ context.Context,
	_ ai.TutorOutputAuditRequest,
) (tutoraudit.Review, tutoraudit.ReviewEvidence, error) {
	reviewer.mu.Lock()
	defer reviewer.mu.Unlock()
	reviewer.calls++
	return tutoraudit.Review{
			Result: tutoraudit.ReviewPass, NoAnswerLeak: true,
			ReasonCodes: []string{"NONE"}, Violations: []tutoraudit.Violation{},
		}, tutoraudit.ReviewEvidence{
			Provider: "pilot-reviewer", Model: "pilot-reviewer-v1",
			RequestID: fmt.Sprintf("pilot-review-%d", reviewer.calls),
		}, nil
}

func (reviewer *pilotReviewer) count() int {
	reviewer.mu.Lock()
	defer reviewer.mu.Unlock()
	return reviewer.calls
}

func newPilotTeachingAgent(
	t *testing.T,
	pool *pgxpool.Pool,
	analysisCorrect bool,
) (*ai.CodexProvider, *pilotStructuredClient, *pilotReviewer) {
	t.Helper()
	client := &pilotStructuredClient{analysisCorrect: analysisCorrect}
	reviewer := &pilotReviewer{}
	auditor, err := tutoraudit.NewService(
		"pilot-generator:pilot-generator-v1",
		"pilot-reviewer:pilot-reviewer-v1",
		reviewer,
		tutoraudit.NewPostgresRecorder(pool),
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	return agent, client, reviewer
}

func TestB4PilotContentHasCompleteReleasedStageSet(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `
SELECT stage_task.stage_role,count(*),
       count(*) FILTER (
           WHERE question.status='RELEASED'
             AND validation.status='PASS'
             AND review.result='PASS'
             AND release_record.to_status='RELEASED'
             AND version.schema_version=validation.schema_version
             AND validation.schema_version=review.schema_version
       )
FROM classroom_stage_tasks stage_task
JOIN classroom_task_lineages lineage ON lineage.id=stage_task.lineage_id
JOIN questions question ON question.id=stage_task.question_id
JOIN content_versions version
  ON version.question_id=question.id AND version.version=question.content_version
JOIN content_validations validation
  ON validation.question_id=question.id AND validation.content_version=question.content_version
JOIN content_reviews review
  ON review.question_id=question.id AND review.content_version=question.content_version
JOIN content_release_records release_record
  ON release_record.question_id=question.id
 AND release_record.validation_id=validation.id
 AND release_record.review_id=review.id
WHERE lineage.knowledge_point_id=$1 AND lineage.status='PILOT_READY'
GROUP BY stage_task.stage_role`, pilotKnowledgePointID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	released := 0
	for rows.Next() {
		var stage string
		var count, valid int
		if err := rows.Scan(&stage, &count, &valid); err != nil {
			t.Fatal(err)
		}
		counts[stage] = count
		released += valid
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if counts["ORIGINAL"] != 1 || counts["VARIANT"] != 2 ||
		counts["ABSTRACT"] != 2 || counts["VERIFY"] != 2 || released != 7 {
		t.Fatalf("pilot stage counts=%v complete released evidence=%d", counts, released)
	}
	var forbiddenAttemptColumns int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema=current_schema()
  AND table_name='b4_pilot_attempts'
  AND column_name IN ('answer','answer_text','student_answer','response_body','payload','message')`).Scan(&forbiddenAttemptColumns); err != nil {
		t.Fatal(err)
	}
	if forbiddenAttemptColumns != 0 {
		t.Fatalf("pilot attempts contain %d answer or free-text columns", forbiddenAttemptColumns)
	}
	var migrationApplied bool
	if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM schema_migrations WHERE version='000027_b4_pilot.sql'
)`).Scan(&migrationApplied); err != nil || !migrationApplied {
		t.Fatalf("migration 000027 applied=%v err=%v", migrationApplied, err)
	}
}

func TestB4PilotIndependentPathPersistsFourStagesWithoutAI(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	studentID := seedPilotStudent(t, ctx, pool)
	agent, client, reviewer := newPilotTeachingAgent(t, pool, false)
	service := classroompilot.NewService(pool).WithTeachingAgent(agent)
	started, err := service.Start(ctx, classroompilot.StartRequest{
		StudentID: studentID, KnowledgePointID: pilotKnowledgePointID, TargetMinutes: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	pathStarted := time.Now()
	firstRequest := correctPilotRequest(t, ctx, pool, started.SessionID, started.Stage, started.TaskID, started.TaskVersion)
	var firstResults [2]classroompilot.SubmitResult
	var firstErrors [2]error
	var wait sync.WaitGroup
	for index := range firstResults {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			firstResults[index], firstErrors[index] = service.Submit(ctx, firstRequest)
		}(index)
	}
	wait.Wait()
	for index, submitErr := range firstErrors {
		if submitErr != nil {
			t.Fatalf("concurrent original submit %d: %v", index, submitErr)
		}
		if !firstResults[index].StageCompleted || firstResults[index].Stage != classroompilot.StageVariant {
			t.Fatalf("concurrent result %d=%+v", index, firstResults[index])
		}
	}
	current := firstResults[0]
	for _, submittedStage := range []classroompilot.Stage{
		classroompilot.StageVariant, classroompilot.StageAbstract, classroompilot.StageVerify,
	} {
		if current.Stage != submittedStage {
			t.Fatalf("current stage=%s want=%s", current.Stage, submittedStage)
		}
		request := correctPilotRequest(t, ctx, pool, started.SessionID, current.Stage, current.TaskID, current.TaskVersion)
		current, err = service.Submit(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if !current.StageCompleted || current.EvidenceKind != classroompilot.EvidenceIndependent {
			t.Fatalf("independent stage result=%+v", current)
		}
		var persistedState string
		if err := pool.QueryRow(ctx, `
SELECT current_state FROM learning_sessions WHERE id=$1`, started.SessionID).Scan(&persistedState); err != nil {
			t.Fatal(err)
		}
		if persistedState != string(current.Stage) {
			t.Fatalf("persisted state=%s result stage=%s", persistedState, current.Stage)
		}
	}
	elapsed := time.Since(pathStarted)
	if !current.Complete || current.Stage != classroompilot.StageComplete {
		t.Fatalf("independent path final=%+v", current)
	}
	analysisCalls, generationCalls := client.counts()
	if analysisCalls != 0 || generationCalls != 0 || reviewer.count() != 0 {
		t.Fatalf("correct path AI calls analysis=%d generation=%d review=%d",
			analysisCalls, generationCalls, reviewer.count())
	}
	var attempts, successes, stages, audits int
	if err := pool.QueryRow(ctx, `
SELECT count(*),count(*) FILTER (WHERE task_success),
       count(DISTINCT submitted_stage) FILTER (WHERE stage_completed)
FROM b4_pilot_attempts WHERE session_id=$1`, started.SessionID).Scan(
		&attempts, &successes, &stages,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, started.SessionID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || successes != 4 || stages != 4 || audits != 0 {
		t.Fatalf("attempts=%d successes=%d completed_stages=%d audits=%d",
			attempts, successes, stages, audits)
	}
	assertNoPilotLegacyOutcomes(t, ctx, pool, studentID, started.SessionID)
	t.Logf("B4_PATH_A stages=4 attempts=%d successes=%d generation_calls=%d elapsed_ms=%d",
		attempts, successes, generationCalls, elapsed.Milliseconds())
}

func TestB4PilotAssistedSuccessRequiresNonRepeatedIndependentReproof(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	studentID := seedPilotStudent(t, ctx, pool)
	agent, client, reviewer := newPilotTeachingAgent(t, pool, false)
	service := classroompilot.NewService(pool).WithTeachingAgent(agent)
	started, err := service.Start(ctx, classroompilot.StartRequest{
		StudentID: studentID, KnowledgePointID: pilotKnowledgePointID, TargetMinutes: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := service.Submit(ctx, correctPilotRequest(
		t, ctx, pool, started.SessionID, started.Stage, started.TaskID, started.TaskVersion,
	))
	if err != nil {
		t.Fatal(err)
	}
	if current.Stage != classroompilot.StageVariant {
		t.Fatalf("stage after original=%s", current.Stage)
	}

	pathStarted := time.Now()
	helpRequest := classroompilot.SubmitRequest{
		SessionID: started.SessionID, OperationID: uuid.New(), Stage: current.Stage,
		TaskID: current.TaskID, TaskVersion: current.TaskVersion, Kind: classroompilot.AttemptHelp,
	}
	feedbackStarted := time.Now()
	helpResult, err := service.Submit(ctx, helpRequest)
	if err != nil {
		t.Fatal(err)
	}
	feedbackElapsed := time.Since(feedbackStarted)
	if helpResult.Stage != classroompilot.StageVariant || helpResult.StageCompleted ||
		helpResult.DeterministicResult != classroompilot.ResultHelpRequested {
		t.Fatalf("help result=%+v", helpResult)
	}
	if _, err := service.Submit(ctx, helpRequest); err != nil {
		t.Fatalf("idempotent help retry: %v", err)
	}
	analysisCalls, generationCalls := client.counts()
	if analysisCalls != 1 || generationCalls != 1 || reviewer.count() != 1 {
		t.Fatalf("help retry AI calls analysis=%d generation=%d review=%d",
			analysisCalls, generationCalls, reviewer.count())
	}

	assistedTaskID := helpResult.TaskID
	assistedRequest := correctPilotRequest(
		t, ctx, pool, started.SessionID, helpResult.Stage, helpResult.TaskID, helpResult.TaskVersion,
	)
	reproof, err := service.Submit(ctx, assistedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if reproof.Stage != classroompilot.StageVariant || reproof.StageCompleted ||
		reproof.EvidenceKind != classroompilot.EvidenceAssisted ||
		reproof.TaskID == assistedTaskID {
		t.Fatalf("assisted success did not select non-repeated reproof: %+v", reproof)
	}
	duplicate, err := service.Submit(ctx, assistedRequest)
	if err != nil {
		t.Fatalf("idempotent success retry: %v", err)
	}
	if duplicate.TaskID != reproof.TaskID || duplicate.EvidenceKind != classroompilot.EvidenceAssisted {
		t.Fatalf("idempotent success changed result: first=%+v duplicate=%+v", reproof, duplicate)
	}
	conflicting := assistedRequest
	conflicting.TaskID = uuid.New()
	if _, err := service.Submit(ctx, conflicting); !errors.Is(err, classroompilot.ErrOperationConflict) {
		t.Fatalf("operation id reused for another task error=%v", err)
	}

	current, err = service.Submit(ctx, correctPilotRequest(
		t, ctx, pool, started.SessionID, reproof.Stage, reproof.TaskID, reproof.TaskVersion,
	))
	if err != nil {
		t.Fatal(err)
	}
	if current.Stage != classroompilot.StageAbstract || !current.StageCompleted ||
		current.EvidenceKind != classroompilot.EvidenceIndependent {
		t.Fatalf("independent reproof result=%+v", current)
	}
	for _, stage := range []classroompilot.Stage{classroompilot.StageAbstract, classroompilot.StageVerify} {
		if current.Stage != stage {
			t.Fatalf("current stage=%s want=%s", current.Stage, stage)
		}
		current, err = service.Submit(ctx, correctPilotRequest(
			t, ctx, pool, started.SessionID, current.Stage, current.TaskID, current.TaskVersion,
		))
		if err != nil {
			t.Fatal(err)
		}
	}
	if !current.Complete || current.Stage != classroompilot.StageComplete {
		t.Fatalf("assisted path final=%+v", current)
	}

	var attempts, successes, completedStages, variantTasks, audits int
	if err := pool.QueryRow(ctx, `
SELECT count(*),count(*) FILTER (WHERE task_success),
       count(*) FILTER (WHERE stage_completed),
       count(DISTINCT question_id) FILTER (WHERE submitted_stage='VARIANT' AND task_success)
FROM b4_pilot_attempts
WHERE session_id=$1`, started.SessionID).Scan(
		&attempts, &successes, &completedStages, &variantTasks,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, started.SessionID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if attempts != 6 || successes != 5 || completedStages != 4 || variantTasks != 2 || audits != 1 {
		t.Fatalf("attempts=%d successes=%d completed=%d variant_tasks=%d audits=%d",
			attempts, successes, completedStages, variantTasks, audits)
	}
	var variantEvidence []string
	if err := pool.QueryRow(ctx, `
SELECT array_agg(evidence_kind ORDER BY created_at,question_id)
FROM b4_pilot_attempts
WHERE session_id=$1 AND submitted_stage='VARIANT' AND task_success`, started.SessionID).Scan(&variantEvidence); err != nil {
		t.Fatal(err)
	}
	if len(variantEvidence) != 2 || variantEvidence[0] != "ASSISTED" || variantEvidence[1] != "INDEPENDENT" {
		t.Fatalf("variant evidence=%v", variantEvidence)
	}
	assertNoPilotLegacyOutcomes(t, ctx, pool, studentID, started.SessionID)
	t.Logf("B4_PATH_B attempts=%d successes=%d analysis_calls=%d generation_calls=%d review_calls=%d feedback_elapsed_ms=%d end_to_end_elapsed_ms=%d",
		attempts, successes, analysisCalls, generationCalls, reviewer.count(),
		feedbackElapsed.Milliseconds(), time.Since(pathStarted).Milliseconds())
}

func TestB4PilotModelJudgmentAndInvalidRequestsCannotAdvance(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	studentID := seedPilotStudent(t, ctx, pool)
	agent, client, reviewer := newPilotTeachingAgent(t, pool, true)
	service := classroompilot.NewService(pool).WithTeachingAgent(agent)
	started, err := service.Start(ctx, classroompilot.StartRequest{
		StudentID: studentID, KnowledgePointID: pilotKnowledgePointID, TargetMinutes: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := classroompilot.SubmitRequest{
		SessionID: started.SessionID, OperationID: uuid.New(), Stage: started.Stage,
		TaskID: started.TaskID, TaskVersion: started.TaskVersion,
		Kind:     classroompilot.AttemptAnswer,
		Response: json.RawMessage(`{"selected_option_ids":[]}`),
	}
	wrongStage := base
	wrongStage.OperationID = uuid.New()
	wrongStage.Stage = classroompilot.StageVariant
	if _, err := service.Submit(ctx, wrongStage); !errors.Is(err, classroompilot.ErrStageMismatch) {
		t.Fatalf("wrong stage error=%v", err)
	}
	wrongTask := base
	wrongTask.OperationID = uuid.New()
	wrongTask.TaskID = uuid.New()
	if _, err := service.Submit(ctx, wrongTask); !errors.Is(err, classroompilot.ErrTaskMismatch) {
		t.Fatalf("wrong task error=%v", err)
	}
	wrongVersion := base
	wrongVersion.OperationID = uuid.New()
	wrongVersion.TaskVersion = "different-version"
	if _, err := service.Submit(ctx, wrongVersion); !errors.Is(err, classroompilot.ErrVersionDrift) {
		t.Fatalf("wrong version error=%v", err)
	}
	if analysis, generation := client.counts(); analysis != 0 || generation != 0 || reviewer.count() != 0 {
		t.Fatalf("invalid requests invoked AI analysis=%d generation=%d review=%d",
			analysis, generation, reviewer.count())
	}

	result, err := service.Submit(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if result.DeterministicResult != classroompilot.ResultIncorrect ||
		result.Stage != classroompilot.StageOriginal || result.StageCompleted {
		t.Fatalf("model-reported correct advanced deterministic failure: %+v", result)
	}
	analysisCalls, generationCalls := client.counts()
	if analysisCalls != 1 || generationCalls != 1 || reviewer.count() != 1 {
		t.Fatalf("feedback calls analysis=%d generation=%d review=%d",
			analysisCalls, generationCalls, reviewer.count())
	}
	var state string
	var successes int
	if err := pool.QueryRow(ctx, `
SELECT current_state FROM learning_sessions WHERE id=$1`, started.SessionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM b4_pilot_attempts WHERE session_id=$1 AND task_success`, started.SessionID).Scan(&successes); err != nil {
		t.Fatal(err)
	}
	if state != "ORIGINAL" || successes != 0 {
		t.Fatalf("model judgment mutated stage state=%s successes=%d", state, successes)
	}
	assertNoPilotLegacyOutcomes(t, ctx, pool, studentID, started.SessionID)
}

func TestB4PilotPreparationAndRuntimeContentFailuresPreserveState(t *testing.T) {
	t.Run("preflight shortage", func(t *testing.T) {
		ctx := context.Background()
		pool := isolatedPool(t, ctx, testDatabaseURL(t))
		if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
			t.Fatal(err)
		}
		studentID := seedPilotStudent(t, ctx, pool)
		if _, err := pool.Exec(ctx, `
UPDATE questions SET status='QUARANTINED'
WHERE id='41000000-0000-4000-8000-000000000003'`); err != nil {
			t.Fatal(err)
		}
		var before, after, pilots, attempts int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_sessions`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		result, err := classroompilot.NewService(pool).Start(ctx, classroompilot.StartRequest{
			StudentID: studentID, KnowledgePointID: pilotKnowledgePointID, TargetMinutes: 20,
		})
		if !errors.Is(err, classroompilot.ErrPreparationIncomplete) ||
			result.Message != classroompilot.MessagePreparationIncomplete {
			t.Fatalf("preflight result=%+v err=%v", result, err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_sessions`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM b4_pilot_sessions`).Scan(&pilots); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM b4_pilot_attempts`).Scan(&attempts); err != nil {
			t.Fatal(err)
		}
		if before != after || pilots != 0 || attempts != 0 {
			t.Fatalf("preflight failure sessions=%d/%d pilots=%d attempts=%d", before, after, pilots, attempts)
		}
		assertNoPilotLegacyOutcomes(t, ctx, pool, studentID, uuid.Nil)
	})

	t.Run("runtime quarantine", func(t *testing.T) {
		ctx := context.Background()
		pool := isolatedPool(t, ctx, testDatabaseURL(t))
		if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
			t.Fatal(err)
		}
		studentID := seedPilotStudent(t, ctx, pool)
		service := classroompilot.NewService(pool)
		started, err := service.Start(ctx, classroompilot.StartRequest{
			StudentID: studentID, KnowledgePointID: pilotKnowledgePointID, TargetMinutes: 20,
		})
		if err != nil {
			t.Fatal(err)
		}
		request := correctPilotRequest(
			t, ctx, pool, started.SessionID, started.Stage, started.TaskID, started.TaskVersion,
		)
		if _, err := pool.Exec(ctx, `
UPDATE questions SET status='QUARANTINED' WHERE id=$1`, started.TaskID); err != nil {
			t.Fatal(err)
		}
		result, err := service.Submit(ctx, request)
		if !errors.Is(err, classroompilot.ErrStageUnavailable) ||
			result.Message != classroompilot.MessageStageUnavailable ||
			!result.ControlsEnabled || !result.PreserveInput {
			t.Fatalf("runtime failure result=%+v err=%v", result, err)
		}
		var state, status string
		var attempts, completions, successes int
		if err := pool.QueryRow(ctx, `
SELECT current_state,status FROM learning_sessions WHERE id=$1`, started.SessionID).Scan(&state, &status); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `
SELECT count(*),count(*) FILTER (WHERE session_completed),count(*) FILTER (WHERE task_success)
FROM b4_pilot_attempts WHERE session_id=$1`, started.SessionID).Scan(
			&attempts, &completions, &successes,
		); err != nil {
			t.Fatal(err)
		}
		if state != "ORIGINAL" || status != "ACTIVE" || attempts != 0 || completions != 0 || successes != 0 {
			t.Fatalf("runtime failure state=%s status=%s attempts=%d completions=%d successes=%d",
				state, status, attempts, completions, successes)
		}
		assertNoPilotLegacyOutcomes(t, ctx, pool, studentID, started.SessionID)
	})
}

func seedPilotStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	studentID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT','B4 pilot student')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,7)`, studentID, userID); err != nil {
		t.Fatal(err)
	}
	return studentID
}

func correctPilotRequest(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	sessionID uuid.UUID,
	stage classroompilot.Stage,
	taskID uuid.UUID,
	taskVersion string,
) classroompilot.SubmitRequest {
	t.Helper()
	var expectedJSON []byte
	if err := pool.QueryRow(ctx, `
SELECT scoring_rule_private_json->'expected_option_ids'
FROM classroom_stage_tasks WHERE question_id=$1`, taskID).Scan(&expectedJSON); err != nil {
		t.Fatal(err)
	}
	var expected []string
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]any{"selected_option_ids": expected})
	if err != nil {
		t.Fatal(err)
	}
	return classroompilot.SubmitRequest{
		SessionID: sessionID, OperationID: uuid.New(), Stage: stage,
		TaskID: taskID, TaskVersion: taskVersion, Kind: classroompilot.AttemptAnswer,
		Response: response,
	}
}

func assertNoPilotLegacyOutcomes(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	studentID uuid.UUID,
	sessionID uuid.UUID,
) {
	t.Helper()
	var skill, reward, growth, activity, turns, answers, analyses, events int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM student_skill_states WHERE student_id=$1),
  (SELECT count(*) FROM reward_events WHERE student_id=$1),
  (SELECT count(*) FROM student_growth WHERE student_id=$1),
  (SELECT count(*) FROM student_activity_days WHERE student_id=$1),
  (SELECT count(*) FROM tutor_turns WHERE session_id=$2),
  (SELECT count(*) FROM student_answers WHERE session_id=$2),
  (SELECT count(*) FROM answer_analyses analysis
     JOIN student_answers answer ON answer.id=analysis.student_answer_id
    WHERE answer.session_id=$2),
  (SELECT count(*) FROM tutor_events WHERE session_id=$2)`, studentID, sessionID).Scan(
		&skill, &reward, &growth, &activity, &turns, &answers, &analyses, &events,
	); err != nil {
		t.Fatal(err)
	}
	if skill != 0 || reward != 0 || growth != 0 || activity != 0 ||
		turns != 0 || answers != 0 || analyses != 0 || events != 0 {
		t.Fatalf("legacy outcomes skill=%d reward=%d growth=%d activity=%d turns=%d answers=%d analyses=%d events=%d",
			skill, reward, growth, activity, turns, answers, analyses, events)
	}
}
