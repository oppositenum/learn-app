package classroom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/reward"
	"github.com/oppositenum/ai-learning-tutor/server/internal/safety"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type stageSnapshot struct {
	sessionID           uuid.UUID
	studentID           uuid.UUID
	lineageID           uuid.UUID
	knowledgePointID    uuid.UUID
	planBlockID         *uuid.UUID
	reviewQueueID       *uuid.UUID
	stage               Stage
	task                stageTask
	status              string
	stageVersion        int64
	learningVersion     int64
	timingVersion       int64
	socraticFailCount   int
	startedAt           time.Time
	accumulatedSeconds  int
	lastResumedAt       *time.Time
	lastActivityAt      time.Time
	processingToken     *uuid.UUID
	processingUntil     *time.Time
	actualTaskStatus    string
	actualTaskVersion   string
	contentReleaseValid bool
}

type storedStageAttempt struct {
	digest              string
	deterministicResult StageScore
	evidenceKind        StageEvidenceKind
	stageCompleted      bool
	responseCode        string
	responseStage       Stage
	responseTaskID      *uuid.UUID
	responseTaskVersion *string
	responseVersion     int64
	responseTiming      int64
	responseSocratic    int
	responseAction      tutor.State
	responseStatus      string
	activeSeconds       int
	currentSeconds      int
	observedAt          time.Time
	sessionCompleted    bool
}

type storedStageSafetyOperation struct {
	attemptKind      StageAttemptKind
	submittedStage   Stage
	questionID       uuid.UUID
	questionVersion  string
	policyVersion    string
	category         safety.Category
	severity         safety.Severity
	fixedAction      safety.Action
	parentEscalated  bool
	responseVersion  int64
	responseTiming   int64
	responseSocratic int
	responseStatus   string
	activeSeconds    int
	currentSeconds   int
	observedAt       time.Time
}

// PostgreSQL timestamptz stores microsecond precision. Normalize timestamps
// used in persisted stage responses so the first response and an idempotent
// replay serialize the same value on platforms whose clocks expose nanoseconds.
func persistedStageTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func loadReadyStageStartTask(ctx context.Context, tx pgx.Tx, knowledgePointID uuid.UUID) (stageTask, bool, error) {
	rows, err := tx.Query(ctx, `
SELECT lineage.id,stage_task.question_id,lineage.knowledge_point_id,subject.code,
       stage_task.stage_role,stage_task.selection_order,question.content_version,
       stage_task.scoring_rule_version,stage_task.scoring_rule_private_json,
       stage_task.evidence_form,question.prompt_public,question.scene_public_json,
       question.input_schema_json,question.status,
       EXISTS (
           SELECT 1
           FROM content_versions version
           JOIN content_validations validation
             ON validation.question_id=version.question_id
            AND validation.content_version=version.version
            AND validation.schema_version=version.schema_version
            AND validation.status='PASS'
           JOIN content_reviews review
             ON review.question_id=version.question_id
            AND review.content_version=version.version
            AND review.schema_version=version.schema_version
            AND review.result='PASS'
           JOIN content_release_records release_record
             ON release_record.question_id=version.question_id
            AND release_record.validation_id=validation.id
            AND release_record.review_id=review.id
            AND release_record.to_status='RELEASED'
           WHERE version.question_id=question.id
             AND version.version=question.content_version
       ) AS release_valid
FROM classroom_task_lineages lineage
JOIN classroom_stage_tasks stage_task ON stage_task.lineage_id=lineage.id
JOIN questions question ON question.id=stage_task.question_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=lineage.knowledge_point_id
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE lineage.knowledge_point_id=$1 AND lineage.status='READY'
ORDER BY lineage.created_at,lineage.id,stage_task.stage_role,stage_task.selection_order`, knowledgePointID)
	if err != nil {
		return stageTask{}, false, err
	}
	defer rows.Close()

	lineages := map[uuid.UUID]struct{}{}
	counts := map[Stage]int{}
	var firstOriginal stageTask
	valid := true
	for rows.Next() {
		var task stageTask
		var status string
		var releaseValid bool
		if err := rows.Scan(
			&task.LineageID, &task.ID, &task.KnowledgePointID, &task.SubjectCode,
			&task.Stage, &task.SelectionOrder, &task.ContentVersion,
			&task.ScoringRuleVersion, &task.ScoringRule, &task.EvidenceForm,
			&task.Prompt, &task.Scene, &task.InputSchema, &status, &releaseValid,
		); err != nil {
			return stageTask{}, false, err
		}
		lineages[task.LineageID] = struct{}{}
		counts[task.Stage]++
		if task.Stage == StageOriginal && (firstOriginal.ID == uuid.Nil || task.SelectionOrder < firstOriginal.SelectionOrder) {
			firstOriginal = task
		}
		if status != "RELEASED" || !releaseValid || task.KnowledgePointID != knowledgePointID || !validStageTask(task) || !validEvidenceForm(task.EvidenceForm) {
			valid = false
		}
	}
	if err := rows.Err(); err != nil {
		return stageTask{}, false, err
	}
	if len(lineages) == 0 {
		return stageTask{}, false, nil
	}
	if len(lineages) != 1 || !valid || firstOriginal.ID == uuid.Nil {
		return stageTask{}, true, ErrStagePreparationIncomplete
	}
	for _, stage := range orderedStages {
		if counts[stage] < 3 {
			return stageTask{}, true, ErrStagePreparationIncomplete
		}
	}
	return firstOriginal, true, nil
}

func validEvidenceForm(value string) bool {
	switch mastery.Form(value) {
	case mastery.FormLife, mastery.FormVariant, mastery.FormTextbook, mastery.FormReview:
		return true
	default:
		return false
	}
}

func (service *Service) hasStageSession(ctx context.Context, studentUserID, sessionID uuid.UUID) (bool, error) {
	var found bool
	err := service.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM classroom_stage_sessions stage_session
    JOIN learning_sessions session ON session.id=stage_session.session_id
    JOIN students student ON student.id=session.student_id
    WHERE stage_session.session_id=$1 AND student.user_id=$2
)`, sessionID, studentUserID).Scan(&found)
	return found, err
}

func (service *Service) SubmitStage(ctx context.Context, studentUserID uuid.UUID, request StageSubmitRequest) (StageSubmitResult, error) {
	timeout := service.submitTimeout
	if timeout <= 0 {
		timeout = SubmitOverallTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := service.submitStageWithinBudget(requestCtx, studentUserID, request)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded)) {
		return StageSubmitResult{}, ErrSubmitTimedOut
	}
	return result, err
}

func (service *Service) submitStageWithinBudget(ctx context.Context, studentUserID uuid.UUID, request StageSubmitRequest) (StageSubmitResult, error) {
	if err := validateStageSubmitRequest(request); err != nil {
		return StageSubmitResult{}, err
	}
	if stored, found, err := service.loadStoredStageSafetyOperation(ctx, studentUserID, request.SessionID, request.OperationID); err != nil {
		return StageSubmitResult{}, err
	} else if found {
		return storedStageSafetyResult(request, stored)
	}
	digest, err := stageRequestDigest(request)
	if err != nil {
		return StageSubmitResult{}, err
	}
	if stored, found, err := service.loadStoredStageAttempt(ctx, studentUserID, request.SessionID, request.OperationID); err != nil {
		return StageSubmitResult{}, err
	} else if found {
		return storedStageResult(request.SessionID, digest, stored)
	}
	if err := service.RecoverStaleSessions(ctx, studentUserID); err != nil {
		return StageSubmitResult{}, err
	}
	operationStartedAt := service.now()
	operationToken, err := service.beginSessionOperationAt(ctx, studentUserID, request.SessionID, operationStartedAt)
	if err != nil {
		if stored, found, loadErr := service.loadStoredStageSafetyOperation(ctx, studentUserID, request.SessionID, request.OperationID); loadErr == nil && found {
			return storedStageSafetyResult(request, stored)
		}
		if stored, found, loadErr := service.loadStoredStageAttempt(ctx, studentUserID, request.SessionID, request.OperationID); loadErr == nil && found {
			return storedStageResult(request.SessionID, digest, stored)
		}
		return StageSubmitResult{}, err
	}
	defer service.endSessionOperation(ctx, request.SessionID, operationToken)

	snapshot, err := service.loadStageSnapshot(ctx, studentUserID, request.SessionID)
	if err != nil {
		return StageSubmitResult{}, err
	}
	if err := service.validateStageRequest(snapshot, request, operationToken); err != nil {
		return StageSubmitResult{}, err
	}
	if classification := classifyStageResponse(snapshot.task, request.Response); classification.Matched {
		return service.handleStageSafetyClassification(ctx, studentUserID, operationToken, operationStartedAt, snapshot, request, classification)
	}
	score := StageScoreHelpRequested
	if request.Kind == StageAttemptAnswer {
		score = scoreStageTask(snapshot.task, request.Response)
	}
	feedback, err := service.prepareStageFeedback(ctx, snapshot, request, score)
	if err != nil {
		return StageSubmitResult{}, err
	}
	result, published, completedAt, err := service.commitStageAttempt(
		ctx, studentUserID, operationToken, operationStartedAt, snapshot, request, digest, score, feedback,
	)
	if err != nil {
		return StageSubmitResult{}, err
	}
	for _, event := range published {
		if service.hub != nil {
			_ = service.hub.Publish(event)
		}
	}
	if completedAt != nil {
		service.refreshStageCompletionPlan(ctx, snapshot.studentID, request.SessionID, *completedAt)
	}
	return result, nil
}

func validateStageSubmitRequest(request StageSubmitRequest) error {
	if request.SessionID == uuid.Nil || request.OperationID == uuid.Nil || request.TaskID == uuid.Nil ||
		!request.Stage.Valid() || request.TaskVersion == "" ||
		(request.Kind != StageAttemptAnswer && request.Kind != StageAttemptHelp) {
		return ErrStageTaskMismatch
	}
	if request.Kind == StageAttemptHelp {
		if len(request.Response) != 0 || (request.Support != SupportHint && request.Support != SupportExplain) {
			return ErrStageTaskMismatch
		}
		return nil
	}
	if request.Support != "" || len(request.Response) == 0 {
		return ErrStageResponseRequired
	}
	return nil
}

func stageRequestDigest(request StageSubmitRequest) (string, error) {
	canonical := []byte{}
	if request.Kind == StageAttemptAnswer {
		var value any
		if err := json.Unmarshal(request.Response, &value); err != nil {
			canonical = request.Response
		} else {
			var err error
			canonical, err = json.Marshal(value)
			if err != nil {
				return "", err
			}
		}
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s|%s|%s|%s|%s|%s|", request.SessionID, request.Stage, request.TaskID, request.TaskVersion, request.Kind, request.Support)
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (service *Service) loadStageSnapshot(ctx context.Context, studentUserID, sessionID uuid.UUID) (stageSnapshot, error) {
	return queryStageSnapshot(ctx, service.pool, studentUserID, sessionID, false)
}

type stageSnapshotQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryStageSnapshot(ctx context.Context, db stageSnapshotQueryer, studentUserID, sessionID uuid.UUID, forUpdate bool) (stageSnapshot, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE OF session,stage_session"
	}
	var snapshot stageSnapshot
	err := db.QueryRow(ctx, `
SELECT session.id,session.student_id,stage_session.lineage_id,stage_session.knowledge_point_id,
       session.plan_block_id,session.review_queue_id,session.current_state,session.status,
       stage_session.version,session.version,session.timing_version,session.socratic_fail_count,session.started_at,
       session.accumulated_seconds,session.last_resumed_at,session.last_activity_at,
       session.processing_token,session.processing_until,
       stage_task.question_id,stage_task.lineage_id,stage_task.stage_role,
       stage_task.selection_order,stage_task.scoring_rule_version,
       stage_task.scoring_rule_private_json,stage_task.evidence_form,
       question.content_version,question.prompt_public,question.scene_public_json,
       question.input_schema_json,subject.code,question.status,question.content_version,
       EXISTS (
           SELECT 1
           FROM content_versions version
           JOIN content_validations validation
             ON validation.question_id=version.question_id
            AND validation.content_version=version.version
            AND validation.schema_version=version.schema_version
            AND validation.status='PASS'
           JOIN content_reviews review
             ON review.question_id=version.question_id
            AND review.content_version=version.version
            AND review.schema_version=version.schema_version
            AND review.result='PASS'
           JOIN content_release_records release_record
             ON release_record.question_id=version.question_id
            AND release_record.validation_id=validation.id
            AND release_record.review_id=review.id
            AND release_record.to_status='RELEASED'
           WHERE version.question_id=question.id
             AND version.version=question.content_version
       )
FROM classroom_stage_sessions stage_session
JOIN learning_sessions session ON session.id=stage_session.session_id
JOIN students student ON student.id=session.student_id
JOIN classroom_stage_tasks stage_task ON stage_task.question_id=stage_session.current_task_id
JOIN questions question ON question.id=stage_task.question_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=stage_session.knowledge_point_id
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE stage_session.session_id=$1 AND student.user_id=$2`+lock, sessionID, studentUserID).Scan(
		&snapshot.sessionID, &snapshot.studentID, &snapshot.lineageID, &snapshot.knowledgePointID,
		&snapshot.planBlockID, &snapshot.reviewQueueID, &snapshot.stage, &snapshot.status,
		&snapshot.stageVersion, &snapshot.learningVersion, &snapshot.timingVersion, &snapshot.socraticFailCount,
		&snapshot.startedAt, &snapshot.accumulatedSeconds, &snapshot.lastResumedAt,
		&snapshot.lastActivityAt, &snapshot.processingToken, &snapshot.processingUntil,
		&snapshot.task.ID, &snapshot.task.LineageID, &snapshot.task.Stage,
		&snapshot.task.SelectionOrder, &snapshot.task.ScoringRuleVersion,
		&snapshot.task.ScoringRule, &snapshot.task.EvidenceForm,
		&snapshot.task.ContentVersion, &snapshot.task.Prompt, &snapshot.task.Scene,
		&snapshot.task.InputSchema, &snapshot.task.SubjectCode, &snapshot.actualTaskStatus,
		&snapshot.actualTaskVersion, &snapshot.contentReleaseValid,
	)
	snapshot.task.KnowledgePointID = snapshot.knowledgePointID
	if errors.Is(err, pgx.ErrNoRows) {
		return stageSnapshot{}, ErrSessionNotFound
	}
	return snapshot, err
}

func (service *Service) validateStageRequest(snapshot stageSnapshot, request StageSubmitRequest, operationToken uuid.UUID) error {
	if snapshot.status == "COMPLETED" || snapshot.stage == StageComplete {
		return ErrStageSessionComplete
	}
	if snapshot.status != "ACTIVE" && snapshot.status != "PAUSED" {
		return ErrSessionNotActive
	}
	if !service.operationLeaseValid(snapshot.processingToken, snapshot.processingUntil, operationToken) {
		return ErrClassroomChanged
	}
	if snapshot.stage != request.Stage || snapshot.task.Stage != request.Stage {
		return ErrStageMismatch
	}
	if snapshot.task.ID != request.TaskID || snapshot.task.LineageID != snapshot.lineageID {
		return ErrStageTaskMismatch
	}
	if snapshot.task.ContentVersion != request.TaskVersion {
		return ErrStageVersionDrift
	}
	if snapshot.actualTaskStatus != "RELEASED" || snapshot.actualTaskVersion != snapshot.task.ContentVersion ||
		!snapshot.contentReleaseValid || !validStageTask(snapshot.task) || !validEvidenceForm(snapshot.task.EvidenceForm) {
		return ErrStageUnavailable
	}
	return nil
}

func (service *Service) prepareStageFeedback(ctx context.Context, snapshot stageSnapshot, request StageSubmitRequest, score StageScore) (ai.TutorTurn, error) {
	if score == StageScoreCorrect {
		return ai.TutorTurn{}, nil
	}
	if (score == StageScoreIncorrect || score == StageScoreIndeterminate) && snapshot.socraticFailCount >= 3 {
		return ai.TutorTurn{
			Action:  tutor.State(snapshot.stage),
			Message: stageMessage("SOCRATIC_REPROOF_FAILED"),
		}, nil
	}
	action := tutor.StateProbe
	fallback := "换一道任务，再按问题要求逐项检查。"
	explain := request.Support == SupportExplain ||
		((score == StageScoreIncorrect || score == StageScoreIndeterminate) && snapshot.socraticFailCount+1 >= 3)
	if score == StageScoreHelpRequested || explain {
		if explain {
			action = tutor.StateExplain
			fallback = "先看一个同结构的完整示范，再回到当前任务自己验证。"
		} else {
			action = tutor.StateHint
			fallback = "先确认问题要求的信息类别，再逐项核对。"
		}
	}
	if service.agent == nil {
		return ai.TutorTurn{Action: action, Message: fallback}, nil
	}
	question, err := content.NewRepository(service.pool).ReleasedQuestionForTeaching(ctx, snapshot.task.ID)
	if err != nil {
		if errors.Is(err, content.ErrQuestionNotFound) {
			return ai.TutorTurn{}, ErrStageUnavailable
		}
		return ai.TutorTurn{}, err
	}
	generationRequest := ai.GenerateTurnRequest{
		StudentID:          snapshot.studentID.String(),
		SessionID:          snapshot.sessionID.String(),
		Question:           question.Public,
		Teaching:           question.Teaching,
		AuditPrivateAnswer: question.Private,
		TutorDecision: tutor.Decision{
			NextState: action,
			Reason:    "deterministic stage scorer retains all transition and evidence authority",
		},
	}
	var turn ai.TutorTurn
	if explain {
		turn, err = service.agent.GenerateParallelExample(ctx, ai.ExampleRequest(generationRequest))
	} else {
		turn, err = service.agent.GenerateTurn(ctx, generationRequest)
	}
	if err != nil {
		return ai.TutorTurn{}, err
	}
	if turn.Action != action {
		return ai.TutorTurn{}, errors.New("teaching agent action does not match deterministic stage feedback action")
	}
	return turn, nil
}

func classifyStageResponse(task stageTask, response json.RawMessage) safety.Classification {
	scene, ok := validStageTaskScene(task)
	if !ok || scene.Renderer != studentinteraction.RendererFillBlanks {
		return safety.Classification{}
	}
	var submitted fillResponse
	if err := json.Unmarshal(response, &submitted); err != nil {
		return safety.Classification{}
	}
	for _, value := range submitted.Values {
		if classification := safety.Classify(value.Value); classification.Matched {
			return classification
		}
	}
	return safety.Classification{}
}

func (service *Service) handleStageSafetyClassification(
	ctx context.Context,
	studentUserID, operationToken uuid.UUID, operationStartedAt time.Time,
	prepared stageSnapshot,
	request StageSubmitRequest,
	classification safety.Classification,
) (StageSubmitResult, error) {
	var result StageSubmitResult
	var event realtime.Event
	publish := false
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if err := auth.LockPrincipalSession(ctx, tx, studentUserID); err != nil {
			return err
		}
		snapshot, err := queryStageSnapshot(ctx, tx, studentUserID, request.SessionID, true)
		if err != nil {
			return err
		}
		if stored, found, err := loadStoredStageSafetyOperation(ctx, tx, studentUserID, request.SessionID, request.OperationID); err != nil {
			return err
		} else if found {
			result, err = storedStageSafetyResult(request, stored)
			return err
		}
		if snapshot.stageVersion != prepared.stageVersion || snapshot.learningVersion != prepared.learningVersion {
			return ErrClassroomChanged
		}
		if err := service.validateStageRequest(snapshot, request, operationToken); err != nil {
			return err
		}
		now := persistedStageTimestamp(latestTime(operationStartedAt, snapshot.lastActivityAt))
		_, eventSequence, err := nextSequences(ctx, tx, request.SessionID)
		if err != nil {
			return err
		}
		var notice *SafetyNotice
		incidentID, recordedEvent, notice, err := recordSafetyIntervention(
			ctx, tx, snapshot.studentID, snapshot.sessionID, eventSequence, classification, now,
		)
		if err != nil {
			return err
		}
		event = recordedEvent
		activeSeconds := checkpointTotal(lifecycleRow{
			status: snapshot.status, startedAt: snapshot.startedAt,
			accumulatedSeconds: snapshot.accumulatedSeconds, lastResumedAt: snapshot.lastResumedAt,
		}, now)
		currentSeconds := 0
		if snapshot.status == "ACTIVE" {
			currentSeconds = max(0, activeSeconds-snapshot.accumulatedSeconds)
		}
		taskID := snapshot.task.ID
		result = StageSubmitResult{
			SessionID: snapshot.sessionID, Version: snapshot.learningVersion,
			TimingVersion: snapshot.timingVersion, Action: tutor.State(snapshot.stage), Stage: snapshot.stage,
			TaskID: &taskID, TaskVersion: snapshot.task.ContentVersion,
			EvidenceKind: StageEvidenceNone, Message: classification.StudentMessage,
			SocraticRound: snapshot.socraticFailCount, Status: snapshot.status,
			ActiveSeconds: activeSeconds, CurrentSeconds: currentSeconds, TimingAt: now,
			Safety: notice,
		}
		if err := insertStageSafetyOperation(ctx, tx, incidentID, request, result, now); err != nil {
			return err
		}
		publish = true
		return nil
	})
	if err != nil {
		return StageSubmitResult{}, err
	}
	if publish && service.hub != nil {
		_ = service.hub.Publish(event)
	}
	return result, nil
}

func (service *Service) commitStageAttempt(
	ctx context.Context,
	studentUserID, operationToken uuid.UUID, operationStartedAt time.Time,
	prepared stageSnapshot,
	request StageSubmitRequest,
	digest string,
	score StageScore,
	feedback ai.TutorTurn,
) (StageSubmitResult, []realtime.Event, *time.Time, error) {
	var result StageSubmitResult
	var published []realtime.Event
	var completedAt *time.Time
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if err := auth.LockPrincipalSession(ctx, tx, studentUserID); err != nil {
			return err
		}
		snapshot, err := queryStageSnapshot(ctx, tx, studentUserID, request.SessionID, true)
		if err != nil {
			return err
		}
		if stored, found, err := loadStoredStageAttempt(ctx, tx, studentUserID, request.SessionID, request.OperationID); err != nil {
			return err
		} else if found {
			result, err = storedStageResult(request.SessionID, digest, stored)
			return err
		}
		if snapshot.stageVersion != prepared.stageVersion || snapshot.learningVersion != prepared.learningVersion {
			return ErrClassroomChanged
		}
		if err := service.validateStageRequest(snapshot, request, operationToken); err != nil {
			return err
		}
		now := persistedStageTimestamp(latestTime(operationStartedAt, snapshot.lastActivityAt))
		turnSequence, eventSequence, err := nextSequences(ctx, tx, request.SessionID)
		if err != nil {
			return err
		}

		responseStage := snapshot.stage
		responseCode := "HELP_DELIVERED"
		evidenceKind := StageEvidenceNone
		stageCompleted := false
		sessionCompleted := false
		abandonCode := ""
		responseSocratic := snapshot.socraticFailCount
		var nextTask *stageTask

		switch score {
		case StageScoreHelpRequested:
			// Help keeps the current task active. A later success on this task is assisted.
		case StageScoreIncorrect, StageScoreIndeterminate:
			responseSocratic = min(3, snapshot.socraticFailCount+1)
			if snapshot.socraticFailCount >= 3 {
				responseCode = "SOCRATIC_REPROOF_FAILED"
				abandonCode = responseCode
			} else if responseSocratic >= 3 {
				responseCode = "SOCRATIC_LIMIT_EXPLAINED"
			} else {
				responseCode = "TRY_NEW_TASK"
				task, err := selectUnpresentedStageTask(ctx, tx, snapshot.lineageID, snapshot.stage, snapshot.sessionID, snapshot.task.ID)
				if errors.Is(err, ErrStageContentExhausted) {
					responseCode = "CONTENT_EXHAUSTED"
					abandonCode = responseCode
				} else if err != nil {
					return err
				} else {
					nextTask = &task
				}
			}
		case StageScoreCorrect:
			responseSocratic = 0
			assisted, err := stageTaskWasHelped(ctx, tx, snapshot.sessionID, snapshot.task.ID)
			if err != nil {
				return err
			}
			if assisted {
				responseCode = "ASSISTED_REPROOF"
				evidenceKind = StageEvidenceAssisted
				task, err := selectUnpresentedStageTask(ctx, tx, snapshot.lineageID, snapshot.stage, snapshot.sessionID, snapshot.task.ID)
				if errors.Is(err, ErrStageContentExhausted) {
					responseCode = "CONTENT_EXHAUSTED"
					abandonCode = responseCode
				} else if err != nil {
					return err
				} else {
					nextTask = &task
				}
			} else {
				responseCode = "NEXT_STAGE"
				evidenceKind = StageEvidenceIndependent
				stageCompleted = true
				next, ok := nextStage(snapshot.stage)
				if !ok {
					return ErrStageMismatch
				}
				responseStage = next
				if next == StageComplete {
					responseCode = "CLASSROOM_COMPLETE"
					sessionCompleted = true
				} else {
					task, err := selectUnpresentedStageTask(ctx, tx, snapshot.lineageID, next, snapshot.sessionID, uuid.Nil)
					if errors.Is(err, ErrStageContentExhausted) {
						responseCode = "CONTENT_EXHAUSTED"
						abandonCode = responseCode
					} else if err != nil {
						return err
					} else {
						nextTask = &task
					}
				}
			}
		default:
			return ErrStageUnavailable
		}

		attemptID := uuid.New()
		newLearningVersion := snapshot.learningVersion + 1
		newTimingVersion := snapshot.timingVersion
		activeSeconds := checkpointTotal(lifecycleRow{
			status: snapshot.status, startedAt: snapshot.startedAt,
			accumulatedSeconds: snapshot.accumulatedSeconds, lastResumedAt: snapshot.lastResumedAt,
		}, now)
		currentSeconds := max(0, activeSeconds-snapshot.accumulatedSeconds)
		responseStatus := snapshot.status
		if sessionCompleted {
			responseStatus = "COMPLETED"
			currentSeconds = 0
			newTimingVersion++
		} else if abandonCode != "" {
			responseStatus = "ABANDONED"
			currentSeconds = 0
			newTimingVersion++
		}
		turnAction := tutor.State(responseStage)
		turnMessage := stageMessage(responseCode)
		turnReason := "deterministic scorer selected the stage outcome"
		turnResponseID := ""
		if score != StageScoreCorrect && abandonCode == "" {
			turnAction = feedback.Action
			turnMessage = feedback.Message
			turnReason = "feedback only; deterministic scorer selected the stage outcome"
			turnResponseID = feedback.ResponseID
		}
		stored := storedStageAttempt{
			digest: digest, deterministicResult: score, evidenceKind: evidenceKind,
			stageCompleted: stageCompleted, responseCode: responseCode,
			responseStage: responseStage, responseVersion: newLearningVersion,
			responseTiming: newTimingVersion, responseStatus: responseStatus,
			responseSocratic: responseSocratic, responseAction: turnAction,
			activeSeconds: activeSeconds, currentSeconds: currentSeconds,
			observedAt: now, sessionCompleted: sessionCompleted,
		}
		if nextTask != nil {
			stored.responseTaskID = &nextTask.ID
			stored.responseTaskVersion = &nextTask.ContentVersion
		} else if !sessionCompleted {
			currentTaskID := snapshot.task.ID
			currentTaskVersion := snapshot.task.ContentVersion
			stored.responseTaskID = &currentTaskID
			stored.responseTaskVersion = &currentTaskVersion
		}
		if err := insertStageAttempt(ctx, tx, attemptID, request, stored, score != StageScoreCorrect, now); err != nil {
			return err
		}

		var skill mastery.Skill
		if evidenceKind != StageEvidenceNone {
			skill, err = loadSkill(ctx, tx, snapshot.studentID, snapshot.knowledgePointID)
			if err != nil {
				return err
			}
			independent := evidenceKind == StageEvidenceIndependent
			skill = mastery.NewEngine().Apply(skill, mastery.Evidence{
				Correct: true, Independent: independent, Form: mastery.Form(snapshot.task.EvidenceForm), At: now,
			})
			if err := saveSkill(ctx, tx, snapshot.studentID, snapshot.knowledgePointID, skill, mastery.NewEngine().Score(skill), now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO classroom_stage_evidence(
    id,session_id,attempt_id,student_id,knowledge_point_id,stage_role,evidence_kind,
    evidence_form,authorized_for_mastery,authorization_policy_version,
    scoring_rule_version,question_version,created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				uuid.New(), snapshot.sessionID, attemptID, snapshot.studentID,
				snapshot.knowledgePointID, snapshot.stage, evidenceKind,
				snapshot.task.EvidenceForm, independent, stageMasteryAuthorizationPolicy,
				snapshot.task.ScoringRuleVersion, snapshot.task.ContentVersion, now); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private,response_id)
VALUES($1,$2,$3,'TUTOR',$4,$5,$6,NULLIF($7,''))`, uuid.New(), snapshot.sessionID,
			turnSequence, turnAction, turnMessage, turnReason, turnResponseID); err != nil {
			return err
		}

		if sessionCompleted {
			if err := service.completeStageSession(ctx, tx, snapshot, skill, now); err != nil {
				return err
			}
			completed := now
			completedAt = &completed
		} else if abandonCode != "" {
			if err := abandonStageSession(ctx, tx, snapshot, responseSocratic, activeSeconds, now); err != nil {
				return err
			}
		} else {
			if err := updateActiveStageSession(ctx, tx, snapshot, nextTask, responseStage, score, responseSocratic, feedback, now); err != nil {
				return err
			}
		}

		transitionStudent, _ := json.Marshal(map[string]any{
			"action": turnAction, "message": stageMessage(responseCode), "stage_completed": stageCompleted,
		})
		transitionParent, _ := json.Marshal(map[string]any{
			"action": turnAction, "message": stageMessage(responseCode), "stage_completed": stageCompleted,
			"evidence_kind": evidenceKind, "deterministic_result": score,
		})
		eventType := realtime.EventTutorActionSelected
		if score == StageScoreHelpRequested {
			eventType = realtime.EventHintRequested
		}
		transition := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence, eventType, transitionStudent, transitionParent, now)
		if err := insertEvent(ctx, tx, transition); err != nil {
			return err
		}
		published = append(published, transition)
		eventSequence++
		if abandonCode != "" {
			studentAbandoned, _ := json.Marshal(map[string]any{
				"status": "ABANDONED", "active_seconds": activeSeconds, "code": abandonCode,
			})
			parentAbandoned, _ := json.Marshal(map[string]any{
				"status": "ABANDONED", "active_seconds": activeSeconds, "reason": abandonCode,
			})
			abandoned := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence, realtime.EventSessionAbandoned, studentAbandoned, parentAbandoned, now)
			if err := insertEvent(ctx, tx, abandoned); err != nil {
				return err
			}
			published = append(published, abandoned)
			eventSequence++
		}

		if nextTask != nil {
			questionStudent, _ := json.Marshal(map[string]any{
				"action": responseStage, "prompt": nextTask.Prompt, "stage": responseStage,
			})
			questionParent, _ := json.Marshal(map[string]any{
				"action": responseStage, "prompt": nextTask.Prompt, "stage": responseStage,
				"question_id": nextTask.ID,
			})
			presented := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence, realtime.EventQuestionPresented, questionStudent, questionParent, now)
			if err := insertEvent(ctx, tx, presented); err != nil {
				return err
			}
			published = append(published, presented)
		}

		if sessionCompleted {
			completionEvents, err := service.stageCompletionEvents(ctx, tx, snapshot, skill, eventSequence, now)
			if err != nil {
				return err
			}
			published = append(published, completionEvents...)
		}
		result, err = storedStageResult(request.SessionID, digest, stored)
		result.Feedback = nil
		return err
	})
	return result, published, completedAt, err
}

func updateActiveStageSession(ctx context.Context, tx pgx.Tx, snapshot stageSnapshot, nextTask *stageTask, responseStage Stage, score StageScore, socraticFailCount int, feedback ai.TutorTurn, now time.Time) error {
	currentTaskID := snapshot.task.ID
	currentTaskVersion := snapshot.task.ContentVersion
	evidenceForm := snapshot.task.EvidenceForm
	if nextTask != nil {
		currentTaskID = nextTask.ID
		currentTaskVersion = nextTask.ContentVersion
		evidenceForm = nextTask.EvidenceForm
	}
	if _, err := tx.Exec(ctx, `
UPDATE classroom_stage_sessions
SET current_task_id=$2,current_task_version=$3,version=version+1
WHERE session_id=$1`, snapshot.sessionID, currentTaskID, currentTaskVersion); err != nil {
		return err
	}
	assistance := 0
	if score == StageScoreHelpRequested || socraticFailCount >= 3 {
		assistance = assistanceForState(feedback.Action)
	}
	_, err := tx.Exec(ctx, `
UPDATE learning_sessions
	SET current_question_id=$2,current_state=$3,
	    evidence_form=$4,assistance_level=GREATEST(assistance_level,$5),
	    teaching_response_id=COALESCE(NULLIF($6,''),teaching_response_id),
	    socratic_fail_count=$7,
	    last_activity_at=CASE WHEN status='ACTIVE' THEN $8 ELSE last_activity_at END,
	    processing_token=NULL,processing_until=NULL,version=version+1
	WHERE id=$1`, snapshot.sessionID, currentTaskID, responseStage, evidenceForm, assistance, feedback.ResponseID, socraticFailCount, now)
	return err
}

func abandonStageSession(ctx context.Context, tx pgx.Tx, snapshot stageSnapshot, socraticFailCount, activeSeconds int, now time.Time) error {
	if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET status='ABANDONED',ended_at=$2,accumulated_seconds=$3,actual_seconds=$3,
    last_resumed_at=NULL,last_activity_at=CASE WHEN status='ACTIVE' THEN $2 ELSE last_activity_at END,
    socratic_fail_count=$4,processing_token=NULL,processing_until=NULL,
    version=version+1,timing_version=timing_version+1
WHERE id=$1`, snapshot.sessionID, now, activeSeconds, socraticFailCount); err != nil {
		return err
	}
	if snapshot.planBlockID != nil {
		if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='AVAILABLE' WHERE id=$1 AND status='ACTIVE'`, *snapshot.planBlockID); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) completeStageSession(ctx context.Context, tx pgx.Tx, snapshot stageSnapshot, skill mastery.Skill, now time.Time) error {
	activeSeconds := checkpointTotal(lifecycleRow{
		status: snapshot.status, startedAt: snapshot.startedAt,
		accumulatedSeconds: snapshot.accumulatedSeconds, lastResumedAt: snapshot.lastResumedAt,
	}, now)
	if _, err := tx.Exec(ctx, `
UPDATE classroom_stage_sessions
SET completed_at=$2,version=version+1
WHERE session_id=$1`, snapshot.sessionID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET current_state='COMPLETE',status='COMPLETED',ended_at=$2,
    accumulated_seconds=$3,actual_seconds=$3,last_resumed_at=NULL,
    last_activity_at=CASE WHEN status='ACTIVE' THEN $2 ELSE last_activity_at END,
    processing_token=NULL,processing_until=NULL,version=version+1,timing_version=timing_version+1
WHERE id=$1`, snapshot.sessionID, now, activeSeconds); err != nil {
		return err
	}
	if snapshot.planBlockID != nil {
		if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='COMPLETED' WHERE id=$1`, *snapshot.planBlockID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE student_misconceptions
SET successful_corrections=successful_corrections+1,status='MONITORING',last_seen_at=$3
WHERE student_id=$1 AND knowledge_point_id=$2 AND status='ACTIVE'`, snapshot.studentID, snapshot.knowledgePointID, now); err != nil {
		return err
	}
	if snapshot.reviewQueueID != nil {
		row := sessionRow{
			studentID: snapshot.studentID, knowledgePointID: snapshot.knowledgePointID,
			reviewQueueID: snapshot.reviewQueueID, evidenceForm: mastery.FormReview,
		}
		if skill.NextReviewAt == nil {
			next := now.Add(mastery.NewEngine().Intervals[0])
			skill.NextReviewAt = &next
		}
		if err := resolveSuccessfulReview(ctx, tx, row, skill, true, now); err != nil {
			return err
		}
	}
	rewardType, sourceID := reward.Effort, snapshot.sessionID.String()
	if skill.State == mastery.Mastered {
		rewardType, sourceID = reward.Mastery, snapshot.knowledgePointID.String()
	}
	if _, err := grantReward(ctx, tx, snapshot.studentID, snapshot.sessionID, rewardType, sourceID); err != nil {
		return err
	}
	return recordStudentActivity(ctx, tx, snapshot.studentID, snapshot.sessionID, now)
}

func (service *Service) stageCompletionEvents(ctx context.Context, tx pgx.Tx, snapshot stageSnapshot, skill mastery.Skill, eventSequence int64, now time.Time) ([]realtime.Event, error) {
	score := mastery.NewEngine().Score(skill)
	var energy int
	if err := tx.QueryRow(ctx, `SELECT total_energy FROM student_growth WHERE student_id=$1`, snapshot.studentID).Scan(&energy); err != nil {
		return nil, err
	}
	rewardType := reward.Effort
	if skill.State == mastery.Mastered {
		rewardType = reward.Mastery
	}
	message := stageMessage("CLASSROOM_COMPLETE")
	studentMastery, _ := json.Marshal(map[string]any{"mastery_state": skill.State, "mastery_score": score, "energy": energy, "message": message})
	parentMastery, _ := json.Marshal(map[string]any{"action": StageComplete, "mastery_state": skill.State, "mastery_score": score, "energy": energy, "message": message})
	masteryEvent := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence, realtime.EventMasteryUpdated, studentMastery, parentMastery, now)
	if err := insertEvent(ctx, tx, masteryEvent); err != nil {
		return nil, err
	}
	studentReward, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": rewardType})
	parentReward, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": rewardType})
	rewardEvent := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence+1, realtime.EventRewardGranted, studentReward, parentReward, now)
	if err := insertEvent(ctx, tx, rewardEvent); err != nil {
		return nil, err
	}
	studentCompleted, _ := json.Marshal(map[string]any{"action": StageComplete, "message": message})
	parentCompleted, _ := json.Marshal(map[string]any{"action": StageComplete, "mastery_state": skill.State, "mastery_score": score, "energy": energy, "message": message})
	completedEvent := makeEvent(snapshot.studentID, snapshot.sessionID, eventSequence+2, realtime.EventSessionCompleted, studentCompleted, parentCompleted, now)
	if err := insertEvent(ctx, tx, completedEvent); err != nil {
		return nil, err
	}
	return []realtime.Event{masteryEvent, rewardEvent, completedEvent}, nil
}

func (service *Service) refreshStageCompletionPlan(ctx context.Context, studentID, sessionID uuid.UUID, now time.Time) {
	if service.planner == nil {
		log.Printf("completed four-stage classroom %s without adaptive planner", sessionID)
		return
	}
	_, changed, err := service.planner.EnsureWithStatus(ctx, studentID, now.AddDate(0, 0, 1), sessionID)
	if err != nil {
		log.Printf("refresh tomorrow plan after four-stage classroom %s: %v", sessionID, err)
		return
	}
	if !changed {
		return
	}
	event, err := service.recordEvent(ctx, studentID, sessionID, realtime.EventPlanModified,
		map[string]any{"tomorrow_plan_changed": true},
		map[string]any{"action": StageComplete, "tomorrow_plan_changed": true, "reason": "session performance changed the next-day plan"}, now)
	if err != nil {
		log.Printf("record tomorrow plan refresh for four-stage classroom %s: %v", sessionID, err)
		return
	}
	if service.hub != nil {
		_ = service.hub.Publish(event)
	}
}

func stageTaskWasHelped(ctx context.Context, tx pgx.Tx, sessionID, taskID uuid.UUID) (bool, error) {
	var helped bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM classroom_stage_attempts
    WHERE session_id=$1 AND question_id=$2
      AND (attempt_kind='HELP' OR response_code='SOCRATIC_LIMIT_EXPLAINED')
)`, sessionID, taskID).Scan(&helped)
	return helped, err
}

func selectUnpresentedStageTask(ctx context.Context, tx pgx.Tx, lineageID uuid.UUID, stage Stage, sessionID, excludeTaskID uuid.UUID) (stageTask, error) {
	rows, err := tx.Query(ctx, `
SELECT stage_task.question_id,stage_task.lineage_id,lineage.knowledge_point_id,
       subject.code,stage_task.stage_role,stage_task.selection_order,
       question.content_version,stage_task.scoring_rule_version,
       stage_task.scoring_rule_private_json,stage_task.evidence_form,
       question.prompt_public,question.scene_public_json,question.input_schema_json,
       lineage.status,knowledge_point.status,question.status,
       EXISTS (
           SELECT 1
           FROM content_versions version
           JOIN content_validations validation
             ON validation.question_id=version.question_id
            AND validation.content_version=version.version
            AND validation.schema_version=version.schema_version
            AND validation.status='PASS'
           JOIN content_reviews review
             ON review.question_id=version.question_id
            AND review.content_version=version.version
            AND review.schema_version=version.schema_version
            AND review.result='PASS'
           JOIN content_release_records release_record
             ON release_record.question_id=version.question_id
            AND release_record.validation_id=validation.id
            AND release_record.review_id=review.id
            AND release_record.to_status='RELEASED'
           WHERE version.question_id=question.id AND version.version=question.content_version
       ) AS release_valid,
       EXISTS (
           SELECT 1 FROM classroom_stage_attempts attempt
           WHERE attempt.session_id=$3 AND attempt.question_id=stage_task.question_id
       ) AS already_presented
FROM classroom_stage_tasks stage_task
JOIN classroom_task_lineages lineage ON lineage.id=stage_task.lineage_id
JOIN questions question ON question.id=stage_task.question_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=lineage.knowledge_point_id
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE stage_task.lineage_id=$1 AND stage_task.stage_role=$2
ORDER BY stage_task.selection_order,stage_task.question_id`, lineageID, stage, sessionID)
	if err != nil {
		return stageTask{}, err
	}
	defer rows.Close()
	found := false
	valid := true
	var selected stageTask
	for rows.Next() {
		found = true
		var task stageTask
		var lineageStatus, knowledgeStatus, questionStatus string
		var releaseValid, presented bool
		if err := rows.Scan(
			&task.ID, &task.LineageID, &task.KnowledgePointID, &task.SubjectCode,
			&task.Stage, &task.SelectionOrder, &task.ContentVersion,
			&task.ScoringRuleVersion, &task.ScoringRule, &task.EvidenceForm,
			&task.Prompt, &task.Scene, &task.InputSchema,
			&lineageStatus, &knowledgeStatus, &questionStatus, &releaseValid, &presented,
		); err != nil {
			return stageTask{}, err
		}
		if lineageStatus != "READY" || knowledgeStatus != "RELEASED" || questionStatus != "RELEASED" ||
			!releaseValid || !validStageTask(task) || !validEvidenceForm(task.EvidenceForm) {
			valid = false
		}
		if selected.ID == uuid.Nil && task.ID != excludeTaskID && !presented {
			selected = task
		}
	}
	if err := rows.Err(); err != nil {
		return stageTask{}, err
	}
	if !found || !valid {
		return stageTask{}, ErrStageUnavailable
	}
	if selected.ID == uuid.Nil {
		return stageTask{}, ErrStageContentExhausted
	}
	return selected, nil
}

func insertStageAttempt(ctx context.Context, tx pgx.Tx, attemptID uuid.UUID, request StageSubmitRequest, stored storedStageAttempt, feedbackDelivered bool, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO classroom_stage_attempts(
    id,session_id,operation_id,request_digest,submitted_stage,question_id,
    question_version,attempt_kind,support_type,deterministic_result,feedback_delivered,
	    task_success,evidence_kind,stage_completed,response_code,response_stage,
	    response_task_id,response_task_version,response_session_version,
	    response_timing_version,response_socratic_round,response_action,response_status,response_active_seconds,
	    response_current_seconds,response_observed_at,session_completed,created_at
) VALUES(
	    $1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14,$15,$16,$17,$18,
	    $19,$20,$21,$22,$23,$24,$25,$26,$27,$28
)`, attemptID, request.SessionID, request.OperationID, stored.digest, request.Stage,
		request.TaskID, request.TaskVersion, request.Kind, request.Support, stored.deterministicResult,
		feedbackDelivered, stored.evidenceKind != StageEvidenceNone, stored.evidenceKind,
		stored.stageCompleted, stored.responseCode, stored.responseStage,
		stored.responseTaskID, stored.responseTaskVersion, stored.responseVersion,
		stored.responseTiming, stored.responseSocratic, stored.responseAction, stored.responseStatus, stored.activeSeconds,
		stored.currentSeconds, stored.observedAt, stored.sessionCompleted, now)
	return err
}

func insertStageSafetyOperation(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID, request StageSubmitRequest, result StageSubmitResult, now time.Time) error {
	_, err := tx.Exec(ctx, `
INSERT INTO classroom_stage_safety_operations(
    session_id,operation_id,incident_id,attempt_kind,submitted_stage,question_id,
    question_version,response_session_version,response_timing_version,
    response_socratic_round,response_status,response_active_seconds,
    response_current_seconds,response_observed_at,created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		request.SessionID, request.OperationID, incidentID, request.Kind, request.Stage,
		request.TaskID, request.TaskVersion, result.Version, result.TimingVersion,
		result.SocraticRound, result.Status, result.ActiveSeconds, result.CurrentSeconds,
		result.TimingAt, now)
	return err
}

type stageAttemptQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (service *Service) loadStoredStageSafetyOperation(ctx context.Context, userID, sessionID, operationID uuid.UUID) (storedStageSafetyOperation, bool, error) {
	return loadStoredStageSafetyOperation(ctx, service.pool, userID, sessionID, operationID)
}

func loadStoredStageSafetyOperation(ctx context.Context, db stageAttemptQueryer, userID, sessionID, operationID uuid.UUID) (storedStageSafetyOperation, bool, error) {
	var stored storedStageSafetyOperation
	err := db.QueryRow(ctx, `
SELECT operation.attempt_kind,operation.submitted_stage,operation.question_id,
       operation.question_version,incident.policy_version,incident.category,
       incident.severity,incident.fixed_action,incident.parent_escalated,
       operation.response_session_version,operation.response_timing_version,
       operation.response_socratic_round,operation.response_status,
       operation.response_active_seconds,operation.response_current_seconds,
       operation.response_observed_at
FROM classroom_stage_safety_operations operation
JOIN minor_safety_incidents incident ON incident.id=operation.incident_id
JOIN learning_sessions session ON session.id=operation.session_id
JOIN students student ON student.id=session.student_id
WHERE operation.session_id=$1 AND operation.operation_id=$2 AND student.user_id=$3`,
		sessionID, operationID, userID).Scan(
		&stored.attemptKind, &stored.submittedStage, &stored.questionID,
		&stored.questionVersion, &stored.policyVersion, &stored.category,
		&stored.severity, &stored.fixedAction, &stored.parentEscalated,
		&stored.responseVersion, &stored.responseTiming, &stored.responseSocratic,
		&stored.responseStatus, &stored.activeSeconds, &stored.currentSeconds,
		&stored.observedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedStageSafetyOperation{}, false, nil
	}
	if err != nil {
		return storedStageSafetyOperation{}, false, err
	}
	return stored, true, nil
}

func storedStageSafetyResult(request StageSubmitRequest, stored storedStageSafetyOperation) (StageSubmitResult, error) {
	if request.Kind != stored.attemptKind || request.Stage != stored.submittedStage ||
		request.TaskID != stored.questionID || request.TaskVersion != stored.questionVersion {
		return StageSubmitResult{}, ErrStageOperationConflict
	}
	classification, ok := safety.FixedClassification(stored.policyVersion, stored.category)
	if !ok || classification.Severity != stored.severity || classification.Action != stored.fixedAction ||
		classification.EscalateToParent != stored.parentEscalated {
		return StageSubmitResult{}, errors.New("stored stage safety classification is inconsistent")
	}
	taskID := stored.questionID
	return StageSubmitResult{
		SessionID: request.SessionID, Version: stored.responseVersion,
		TimingVersion: stored.responseTiming, Action: tutor.State(stored.submittedStage),
		Stage: stored.submittedStage, TaskID: &taskID, TaskVersion: stored.questionVersion,
		EvidenceKind: StageEvidenceNone, Message: classification.StudentMessage,
		SocraticRound: stored.responseSocratic, Status: stored.responseStatus,
		ActiveSeconds: stored.activeSeconds, CurrentSeconds: stored.currentSeconds,
		TimingAt: stored.observedAt.UTC(),
		Safety: &SafetyNotice{
			PolicyVersion: stored.policyVersion, Category: stored.category,
			Severity: stored.severity, FixedAction: stored.fixedAction,
			ParentNotified: stored.parentEscalated,
		},
	}, nil
}

func (service *Service) loadStoredStageAttempt(ctx context.Context, userID, sessionID, operationID uuid.UUID) (storedStageAttempt, bool, error) {
	return loadStoredStageAttempt(ctx, service.pool, userID, sessionID, operationID)
}

func loadStoredStageAttempt(ctx context.Context, db stageAttemptQueryer, userID, sessionID, operationID uuid.UUID) (storedStageAttempt, bool, error) {
	var stored storedStageAttempt
	err := db.QueryRow(ctx, `
SELECT attempt.request_digest,attempt.deterministic_result,attempt.evidence_kind,
       attempt.stage_completed,attempt.response_code,attempt.response_stage,
	       attempt.response_task_id,attempt.response_task_version,
	       attempt.response_session_version,attempt.response_timing_version,attempt.response_socratic_round,
	       attempt.response_action,attempt.response_status,attempt.response_active_seconds,
       attempt.response_current_seconds,attempt.response_observed_at,
       attempt.session_completed
FROM classroom_stage_attempts attempt
JOIN learning_sessions session ON session.id=attempt.session_id
JOIN students student ON student.id=session.student_id
WHERE attempt.session_id=$1 AND attempt.operation_id=$2 AND student.user_id=$3`, sessionID, operationID, userID).Scan(
		&stored.digest, &stored.deterministicResult, &stored.evidenceKind,
		&stored.stageCompleted, &stored.responseCode, &stored.responseStage,
		&stored.responseTaskID, &stored.responseTaskVersion, &stored.responseVersion,
		&stored.responseTiming, &stored.responseSocratic, &stored.responseAction, &stored.responseStatus, &stored.activeSeconds,
		&stored.currentSeconds, &stored.observedAt, &stored.sessionCompleted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedStageAttempt{}, false, nil
	}
	if err != nil {
		return storedStageAttempt{}, false, err
	}
	return stored, true, nil
}

func storedStageResult(sessionID uuid.UUID, digest string, stored storedStageAttempt) (StageSubmitResult, error) {
	if stored.digest != digest {
		return StageSubmitResult{}, ErrStageOperationConflict
	}
	return StageSubmitResult{
		SessionID: sessionID, Version: stored.responseVersion,
		TimingVersion: stored.responseTiming, Action: stored.responseAction,
		Stage: stored.responseStage, TaskID: stored.responseTaskID,
		TaskVersion: func() string {
			if stored.responseTaskVersion == nil {
				return ""
			}
			return *stored.responseTaskVersion
		}(),
		DeterministicResult: stored.deterministicResult, EvidenceKind: stored.evidenceKind,
		StageCompleted: stored.stageCompleted, Code: stageResultCode(stored.responseCode),
		Message: stageMessage(stored.responseCode), SocraticRound: stored.responseSocratic,
		Status: stored.responseStatus, ActiveSeconds: stored.activeSeconds,
		CurrentSeconds: stored.currentSeconds, TimingAt: stored.observedAt.UTC(),
	}, nil
}

func stageResultCode(responseCode string) string {
	if responseCode == "CONTENT_EXHAUSTED" || responseCode == "SOCRATIC_REPROOF_FAILED" {
		return responseCode
	}
	return ""
}
