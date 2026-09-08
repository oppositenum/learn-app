package classroompilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type Service struct {
	pool  *pgxpool.Pool
	agent ai.TeachingAgent
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// WithTeachingAgent supplies the existing audited Teaching Agent. Its
// GenerateTurn implementation is responsible for the established dual gate.
func (service *Service) WithTeachingAgent(agent ai.TeachingAgent) *Service {
	service.agent = agent
	return service
}

type sessionSnapshot struct {
	sessionID         uuid.UUID
	studentID         uuid.UUID
	lineageID         uuid.UUID
	stage             Stage
	task              Task
	sessionStatus     string
	pilotVersion      int64
	learningVersion   int64
	reproofRequired   bool
	actualTaskStatus  string
	actualTaskVersion string
}

type storedAttempt struct {
	digest              string
	deterministicResult DeterministicResult
	evidenceKind        EvidenceKind
	stageCompleted      bool
	responseCode        string
	responseStage       Stage
	responseTaskID      *uuid.UUID
	responseTaskVersion *string
	sessionCompleted    bool
}

func (service *Service) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	if service == nil || service.pool == nil {
		return StartResult{}, ErrPreparationIncomplete
	}
	if request.StudentID == uuid.Nil || request.KnowledgePointID == uuid.Nil ||
		request.TargetMinutes < 1 || request.TargetMinutes > 120 {
		return StartResult{}, ErrPreparationIncomplete
	}

	now := service.now()
	var result StartResult
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var subjectID uuid.UUID
		if err := tx.QueryRow(ctx, `
SELECT knowledge_point.subject_id
FROM knowledge_points knowledge_point
JOIN grade_bands grade_band ON grade_band.code=knowledge_point.grade_band_code
JOIN students student ON student.id=$2 AND student.grade_level>=grade_band.min_grade
WHERE knowledge_point.id=$1 AND knowledge_point.status='RELEASED'`,
			request.KnowledgePointID, request.StudentID).Scan(&subjectID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPreparationIncomplete
			}
			return err
		}

		tasks, err := loadReadyTasks(ctx, tx, request.KnowledgePointID)
		if err != nil {
			return err
		}
		if !completeTaskSet(tasks) {
			return ErrPreparationIncomplete
		}
		original := tasks[StageOriginal][0]
		sessionID := uuid.New()
		if _, err := tx.Exec(ctx, `
INSERT INTO learning_sessions
    (id,student_id,subject_id,current_question_id,started_at,status,target_minutes,current_state,
     original_task_id,active_task_id,evidence_form,last_resumed_at,last_activity_at)
VALUES ($1,$2,$3,$4,$5,'ACTIVE',$6,'ORIGINAL',$7,$7,'LIFE',$5,$5)`,
			sessionID, request.StudentID, subjectID, original.ID, now, request.TargetMinutes,
			request.KnowledgePointID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO b4_pilot_sessions
    (session_id,lineage_id,knowledge_point_id,current_task_id,current_task_version,started_at)
VALUES ($1,$2,$3,$4,$5,$6)`,
			sessionID, original.LineageID, request.KnowledgePointID, original.ID,
			original.ContentVersion, now); err != nil {
			return err
		}
		result = StartResult{
			SessionID: sessionID, Stage: StageOriginal, TaskID: original.ID,
			TaskVersion: original.ContentVersion, Message: fixedMessage("NEXT_STAGE"),
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrPreparationIncomplete) {
			return StartResult{Message: MessagePreparationIncomplete}, ErrPreparationIncomplete
		}
		return StartResult{}, err
	}
	return result, nil
}

func loadReadyTasks(ctx context.Context, tx pgx.Tx, knowledgePointID uuid.UUID) (map[Stage][]Task, error) {
	rows, err := tx.Query(ctx, `
SELECT stage_task.question_id,lineage.id,lineage.knowledge_point_id,subject.code,
       stage_task.stage_role,stage_task.selection_order,question.content_version,
       stage_task.scoring_rule_version,stage_task.scoring_rule_private_json
FROM classroom_task_lineages lineage
JOIN classroom_stage_tasks stage_task ON stage_task.lineage_id=lineage.id
JOIN questions question ON question.id=stage_task.question_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=lineage.knowledge_point_id
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE lineage.knowledge_point_id=$1
  AND lineage.status='PILOT_READY'
  AND knowledge_point.status='RELEASED'
  AND question.status='RELEASED'
  AND question.knowledge_point_id=lineage.knowledge_point_id
  AND EXISTS (
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
ORDER BY stage_task.stage_role,stage_task.selection_order`, knowledgePointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := make(map[Stage][]Task, len(orderedStages))
	var lineageID uuid.UUID
	for rows.Next() {
		var task Task
		if err := rows.Scan(
			&task.ID, &task.LineageID, &task.KnowledgePointID, &task.SubjectCode,
			&task.Stage, &task.SelectionOrder, &task.ContentVersion,
			&task.ScoringRuleVersion, &task.ScoringRule,
		); err != nil {
			return nil, err
		}
		if !validTask(task) || (lineageID != uuid.Nil && task.LineageID != lineageID) {
			return nil, ErrPreparationIncomplete
		}
		lineageID = task.LineageID
		tasks[task.Stage] = append(tasks[task.Stage], task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}

func completeTaskSet(tasks map[Stage][]Task) bool {
	if len(tasks[StageOriginal]) != 1 {
		return false
	}
	for _, stage := range []Stage{StageVariant, StageAbstract, StageVerify} {
		if len(tasks[stage]) < 2 {
			return false
		}
	}
	return true
}

func (service *Service) Submit(ctx context.Context, request SubmitRequest) (SubmitResult, error) {
	if service == nil || service.pool == nil || request.SessionID == uuid.Nil ||
		request.OperationID == uuid.Nil || request.TaskID == uuid.Nil ||
		!request.Stage.Valid() || request.TaskVersion == "" ||
		(request.Kind != AttemptAnswer && request.Kind != AttemptHelp) {
		return SubmitResult{}, ErrTaskMismatch
	}
	if request.Kind == AttemptHelp && len(request.Response) != 0 {
		return SubmitResult{}, ErrTaskMismatch
	}
	digest := requestDigest(request)
	if existing, found, err := service.loadStoredAttempt(ctx, request.SessionID, request.OperationID); err != nil {
		return SubmitResult{}, err
	} else if found {
		return storedResult(request.SessionID, digest, existing)
	}

	snapshot, err := service.loadSnapshot(ctx, request.SessionID)
	if err != nil {
		return SubmitResult{}, err
	}
	// A concurrent copy may commit between the first idempotency lookup and
	// this snapshot. Resolve it before treating the advanced stage as stale.
	if existing, found, err := service.loadStoredAttempt(ctx, request.SessionID, request.OperationID); err != nil {
		return SubmitResult{}, err
	} else if found {
		return storedResult(request.SessionID, digest, existing)
	}
	if err := validateRequest(snapshot, request); err != nil {
		if errors.Is(err, ErrStageUnavailable) {
			return unavailableResult(request), err
		}
		return SubmitResult{}, err
	}

	result := ResultHelpRequested
	if request.Kind == AttemptAnswer {
		result = Score(snapshot.task, request.Response)
	}
	var feedback *ai.TutorTurn
	if result != ResultCorrect {
		turn, err := service.generateAuditedFeedback(ctx, snapshot, request, result)
		if err != nil {
			if errors.Is(err, ErrStageUnavailable) {
				return unavailableResult(request), err
			}
			return SubmitResult{}, err
		}
		feedback = &turn
	}
	submitted, err := service.commitAttempt(ctx, snapshot, request, digest, result)
	if err != nil {
		if errors.Is(err, ErrStageUnavailable) {
			return unavailableResult(request), err
		}
		return SubmitResult{}, err
	}
	if feedback != nil {
		submitted.Feedback = feedback
		submitted.Message = feedback.Message
	}
	return submitted, nil
}

func unavailableResult(request SubmitRequest) SubmitResult {
	return SubmitResult{
		SessionID: request.SessionID, Stage: request.Stage, TaskID: request.TaskID,
		TaskVersion: request.TaskVersion, Message: MessageStageUnavailable,
		ControlsEnabled: true, PreserveInput: true,
	}
}

func (service *Service) loadSnapshot(ctx context.Context, sessionID uuid.UUID) (sessionSnapshot, error) {
	var snapshot sessionSnapshot
	var subjectCode subject.Code
	err := service.pool.QueryRow(ctx, `
SELECT learning_session.id,learning_session.student_id,pilot.lineage_id,
       learning_session.current_state,learning_session.status,pilot.version,learning_session.version,
       pilot.requires_independent_reproof,pilot.current_task_id,pilot.knowledge_point_id,
       subject.code,stage_task.stage_role,stage_task.selection_order,pilot.current_task_version,
       stage_task.scoring_rule_version,stage_task.scoring_rule_private_json,
       question.status,question.content_version
FROM b4_pilot_sessions pilot
JOIN learning_sessions learning_session ON learning_session.id=pilot.session_id
JOIN classroom_stage_tasks stage_task ON stage_task.question_id=pilot.current_task_id
JOIN questions question ON question.id=pilot.current_task_id
JOIN knowledge_points knowledge_point ON knowledge_point.id=pilot.knowledge_point_id
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE pilot.session_id=$1`, sessionID).Scan(
		&snapshot.sessionID, &snapshot.studentID, &snapshot.lineageID,
		&snapshot.stage, &snapshot.sessionStatus, &snapshot.pilotVersion, &snapshot.learningVersion,
		&snapshot.reproofRequired, &snapshot.task.ID, &snapshot.task.KnowledgePointID,
		&subjectCode, &snapshot.task.Stage, &snapshot.task.SelectionOrder,
		&snapshot.task.ContentVersion, &snapshot.task.ScoringRuleVersion, &snapshot.task.ScoringRule,
		&snapshot.actualTaskStatus, &snapshot.actualTaskVersion,
	)
	snapshot.task.LineageID = snapshot.lineageID
	snapshot.task.SubjectCode = subjectCode
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionSnapshot{}, ErrSessionNotFound
	}
	if err != nil {
		return sessionSnapshot{}, err
	}
	return snapshot, nil
}

func validateRequest(snapshot sessionSnapshot, request SubmitRequest) error {
	if snapshot.sessionStatus == "COMPLETED" || snapshot.stage == StageComplete {
		return ErrSessionComplete
	}
	if snapshot.sessionStatus != "ACTIVE" && snapshot.sessionStatus != "PAUSED" {
		return ErrSessionNotFound
	}
	if snapshot.stage != request.Stage || snapshot.task.Stage != request.Stage {
		return ErrStageMismatch
	}
	if snapshot.task.ID != request.TaskID {
		return ErrTaskMismatch
	}
	if snapshot.task.ContentVersion != request.TaskVersion {
		return ErrVersionDrift
	}
	if snapshot.actualTaskStatus != "RELEASED" ||
		snapshot.actualTaskVersion != snapshot.task.ContentVersion ||
		!validTask(snapshot.task) {
		return ErrStageUnavailable
	}
	return nil
}

func (service *Service) generateAuditedFeedback(
	ctx context.Context,
	snapshot sessionSnapshot,
	request SubmitRequest,
	result DeterministicResult,
) (ai.TutorTurn, error) {
	if service.agent == nil {
		return ai.TutorTurn{}, ErrFeedbackUnavailable
	}
	question, err := content.NewRepository(service.pool).ReleasedQuestionForTeaching(ctx, snapshot.task.ID)
	if err != nil {
		if errors.Is(err, content.ErrQuestionNotFound) {
			return ai.TutorTurn{}, ErrStageUnavailable
		}
		return ai.TutorTurn{}, err
	}
	response := ""
	if request.Kind == AttemptAnswer {
		response = string(request.Response)
	}
	// The diagnosis can shape feedback, but its correctness flag is never read
	// by the stage transition code.
	if _, err := service.agent.AnalyzeAnswer(ctx, ai.AnalyzeAnswerRequest{
		StudentID: snapshot.studentID.String(), SessionID: snapshot.sessionID.String(),
		Question: question, StudentAnswer: response,
	}); err != nil {
		return ai.TutorTurn{}, err
	}
	action := tutor.StateProbe
	if result == ResultHelpRequested {
		action = tutor.StateHint
	}
	turn, err := service.agent.GenerateTurn(ctx, ai.GenerateTurnRequest{
		StudentID: snapshot.studentID.String(), SessionID: snapshot.sessionID.String(),
		Question: question.Public, AuditPrivateAnswer: question.Private, StudentAnswer: response,
		TutorDecision: tutor.Decision{
			NextState: action,
			Reason:    "B4 pilot feedback only; deterministic scorer retains transition authority",
		},
	})
	if err != nil {
		return ai.TutorTurn{}, err
	}
	if turn.Action != action {
		return ai.TutorTurn{}, ErrFeedbackUnavailable
	}
	return turn, nil
}

func requestDigest(request SubmitRequest) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s|%s|%s|%s|%s", request.SessionID, request.Stage,
		request.TaskID, request.TaskVersion, request.Kind)
	return hex.EncodeToString(hash.Sum(nil))
}

func (service *Service) commitAttempt(
	ctx context.Context,
	snapshot sessionSnapshot,
	request SubmitRequest,
	digest string,
	deterministic DeterministicResult,
) (SubmitResult, error) {
	var result SubmitResult
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		var currentStage Stage
		var currentTaskID uuid.UUID
		var currentTaskVersion, learningStatus string
		var pilotVersion, learningVersion int64
		if err := tx.QueryRow(ctx, `
SELECT learning_session.current_state,pilot.current_task_id,pilot.current_task_version,
       pilot.version,learning_session.version,learning_session.status
FROM b4_pilot_sessions pilot
JOIN learning_sessions learning_session ON learning_session.id=pilot.session_id
WHERE pilot.session_id=$1
FOR UPDATE OF pilot,learning_session`, request.SessionID).Scan(
			&currentStage, &currentTaskID, &currentTaskVersion,
			&pilotVersion, &learningVersion, &learningStatus,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrSessionNotFound
			}
			return err
		}
		if existing, found, err := loadStoredAttempt(ctx, tx, request.SessionID, request.OperationID); err != nil {
			return err
		} else if found {
			stored, err := storedResult(request.SessionID, digest, existing)
			if err != nil {
				return err
			}
			result = stored
			return nil
		}
		if learningStatus == "COMPLETED" || currentStage == StageComplete {
			return ErrSessionComplete
		}
		if pilotVersion != snapshot.pilotVersion || learningVersion != snapshot.learningVersion ||
			currentStage != snapshot.stage || currentTaskID != snapshot.task.ID ||
			currentTaskVersion != snapshot.task.ContentVersion {
			return ErrClassroomChanged
		}

		var taskStatus, taskVersion string
		var taskStage Stage
		if err := tx.QueryRow(ctx, `
SELECT question.status,question.content_version,stage_task.stage_role
FROM questions question
JOIN classroom_stage_tasks stage_task ON stage_task.question_id=question.id
WHERE question.id=$1`, currentTaskID).Scan(&taskStatus, &taskVersion, &taskStage); err != nil {
			return ErrStageUnavailable
		}
		if taskStatus != "RELEASED" || taskVersion != currentTaskVersion || taskStage != currentStage {
			return ErrStageUnavailable
		}

		now := service.now()
		if deterministic != ResultCorrect {
			responseCode := "TRY_AGAIN"
			helpDelivered := false
			if deterministic == ResultHelpRequested {
				responseCode = "HELP_DELIVERED"
				helpDelivered = true
			}
			attempt := storedAttempt{
				digest: digest, deterministicResult: deterministic, evidenceKind: EvidenceNone,
				responseCode: responseCode, responseStage: currentStage,
				responseTaskID: &currentTaskID, responseTaskVersion: &currentTaskVersion,
			}
			if err := insertAttempt(ctx, tx, request, attempt, helpDelivered, false, false, true, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE b4_pilot_sessions SET version=version+1 WHERE session_id=$1`, request.SessionID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET last_activity_at=CASE WHEN status='ACTIVE' THEN $2 ELSE last_activity_at END,
    version=version+1
WHERE id=$1`, request.SessionID, now); err != nil {
				return err
			}
			result, _ = storedResult(request.SessionID, digest, attempt)
			return nil
		}

		var alreadySucceeded bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM b4_pilot_attempts
    WHERE session_id=$1 AND submitted_stage=$2 AND question_id=$3 AND task_success
)`, request.SessionID, currentStage, currentTaskID).Scan(&alreadySucceeded); err != nil {
			return err
		}
		if alreadySucceeded {
			return ErrDuplicateSuccess
		}
		var assisted bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM b4_pilot_attempts
    WHERE session_id=$1 AND question_id=$2 AND model_feedback_used
)`, request.SessionID, currentTaskID).Scan(&assisted); err != nil {
			return err
		}

		responseStage := currentStage
		responseCode := "ASSISTED_REPROOF"
		evidence := EvidenceAssisted
		stageCompleted := false
		sessionCompleted := false
		var nextTask *Task
		if assisted {
			task, err := selectAvailableTask(ctx, tx, snapshot.lineageID, currentStage, request.SessionID, currentTaskID)
			if err != nil {
				return err
			}
			nextTask = &task
		} else {
			evidence = EvidenceIndependent
			stageCompleted = true
			next, ok := NextStage(currentStage)
			if !ok {
				return ErrStageMismatch
			}
			responseStage = next
			responseCode = "NEXT_STAGE"
			if next == StageComplete {
				sessionCompleted = true
				responseCode = "PILOT_COMPLETE"
			} else {
				task, err := selectAvailableTask(ctx, tx, snapshot.lineageID, next, request.SessionID, uuid.Nil)
				if err != nil {
					return err
				}
				nextTask = &task
			}
		}

		attempt := storedAttempt{
			digest: digest, deterministicResult: deterministic, evidenceKind: evidence,
			stageCompleted: stageCompleted, responseCode: responseCode,
			responseStage: responseStage, sessionCompleted: sessionCompleted,
		}
		if nextTask != nil {
			attempt.responseTaskID = &nextTask.ID
			attempt.responseTaskVersion = &nextTask.ContentVersion
		}
		if err := insertAttempt(ctx, tx, request, attempt, false, true, stageCompleted, false, now); err != nil {
			return err
		}
		if sessionCompleted {
			if _, err := tx.Exec(ctx, `
UPDATE b4_pilot_sessions
SET requires_independent_reproof=false,completed_at=$2,version=version+1
WHERE session_id=$1`, request.SessionID, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET current_state='COMPLETE',status='COMPLETED',ended_at=$2,last_resumed_at=NULL,
    last_activity_at=$2,version=version+1
WHERE id=$1`, request.SessionID, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `
UPDATE b4_pilot_sessions
SET current_task_id=$2,current_task_version=$3,requires_independent_reproof=$4,
    version=version+1
WHERE session_id=$1`, request.SessionID, nextTask.ID, nextTask.ContentVersion, assisted); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET current_question_id=$2,current_state=$3,
    last_activity_at=CASE WHEN status='ACTIVE' THEN $4 ELSE last_activity_at END,
    version=version+1
WHERE id=$1`, request.SessionID, nextTask.ID, responseStage, now); err != nil {
				return err
			}
		}
		result, _ = storedResult(request.SessionID, digest, attempt)
		return nil
	})
	return result, err
}

func selectAvailableTask(
	ctx context.Context,
	tx pgx.Tx,
	lineageID uuid.UUID,
	stage Stage,
	sessionID uuid.UUID,
	excludeTaskID uuid.UUID,
) (Task, error) {
	var task Task
	var excluded any
	if excludeTaskID != uuid.Nil {
		excluded = excludeTaskID
	}
	err := tx.QueryRow(ctx, `
SELECT stage_task.question_id,stage_task.lineage_id,lineage.knowledge_point_id,subject.code,
       stage_task.stage_role,stage_task.selection_order,question.content_version,
       stage_task.scoring_rule_version,stage_task.scoring_rule_private_json
FROM classroom_stage_tasks stage_task
JOIN classroom_task_lineages lineage ON lineage.id=stage_task.lineage_id AND lineage.status='PILOT_READY'
JOIN questions question ON question.id=stage_task.question_id AND question.status='RELEASED'
JOIN knowledge_points knowledge_point ON knowledge_point.id=lineage.knowledge_point_id AND knowledge_point.status='RELEASED'
JOIN subjects subject ON subject.id=knowledge_point.subject_id
WHERE stage_task.lineage_id=$1 AND stage_task.stage_role=$2
  AND ($4::uuid IS NULL OR stage_task.question_id<>$4)
  AND NOT EXISTS (
      SELECT 1 FROM b4_pilot_attempts attempt
      WHERE attempt.session_id=$3 AND attempt.question_id=stage_task.question_id AND attempt.task_success
  )
  AND EXISTS (
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
  )
ORDER BY stage_task.selection_order
LIMIT 1`, lineageID, stage, sessionID, excluded).Scan(
		&task.ID, &task.LineageID, &task.KnowledgePointID, &task.SubjectCode,
		&task.Stage, &task.SelectionOrder, &task.ContentVersion,
		&task.ScoringRuleVersion, &task.ScoringRule,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrStageUnavailable
	}
	if err != nil {
		return Task{}, err
	}
	if !validTask(task) {
		return Task{}, ErrStageUnavailable
	}
	return task, nil
}

func insertAttempt(
	ctx context.Context,
	tx pgx.Tx,
	request SubmitRequest,
	attempt storedAttempt,
	helpDelivered bool,
	taskSuccess bool,
	stageCompleted bool,
	modelFeedbackUsed bool,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
INSERT INTO b4_pilot_attempts
    (id,session_id,operation_id,request_digest,submitted_stage,question_id,question_version,
     attempt_kind,deterministic_result,help_delivered,task_success,evidence_kind,
     stage_completed,model_feedback_used,response_code,response_stage,response_task_id,
     response_task_version,session_completed,created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		uuid.New(), request.SessionID, request.OperationID, attempt.digest, request.Stage,
		request.TaskID, request.TaskVersion, request.Kind, attempt.deterministicResult,
		helpDelivered, taskSuccess, attempt.evidenceKind, stageCompleted, modelFeedbackUsed,
		attempt.responseCode, attempt.responseStage, attempt.responseTaskID,
		attempt.responseTaskVersion, attempt.sessionCompleted, now,
	)
	return err
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (service *Service) loadStoredAttempt(
	ctx context.Context,
	sessionID uuid.UUID,
	operationID uuid.UUID,
) (storedAttempt, bool, error) {
	return loadStoredAttempt(ctx, service.pool, sessionID, operationID)
}

func loadStoredAttempt(
	ctx context.Context,
	query rowQuerier,
	sessionID uuid.UUID,
	operationID uuid.UUID,
) (storedAttempt, bool, error) {
	var attempt storedAttempt
	err := query.QueryRow(ctx, `
SELECT request_digest,deterministic_result,evidence_kind,stage_completed,response_code,
       response_stage,response_task_id,response_task_version,session_completed
FROM b4_pilot_attempts
WHERE session_id=$1 AND operation_id=$2`, sessionID, operationID).Scan(
		&attempt.digest, &attempt.deterministicResult, &attempt.evidenceKind,
		&attempt.stageCompleted, &attempt.responseCode, &attempt.responseStage,
		&attempt.responseTaskID, &attempt.responseTaskVersion, &attempt.sessionCompleted,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedAttempt{}, false, nil
	}
	if err != nil {
		return storedAttempt{}, false, err
	}
	return attempt, true, nil
}

func storedResult(sessionID uuid.UUID, digest string, attempt storedAttempt) (SubmitResult, error) {
	if attempt.digest != digest {
		return SubmitResult{}, ErrOperationConflict
	}
	result := SubmitResult{
		SessionID: sessionID, Stage: attempt.responseStage,
		DeterministicResult: attempt.deterministicResult,
		EvidenceKind:        attempt.evidenceKind, StageCompleted: attempt.stageCompleted,
		Complete: attempt.sessionCompleted, Message: fixedMessage(attempt.responseCode),
		ControlsEnabled: true,
	}
	if attempt.responseTaskID != nil {
		result.TaskID = *attempt.responseTaskID
	}
	if attempt.responseTaskVersion != nil {
		result.TaskVersion = *attempt.responseTaskVersion
	}
	return result, nil
}
