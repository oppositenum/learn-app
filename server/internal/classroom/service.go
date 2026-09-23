package classroom

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/reward"
	"github.com/oppositenum/ai-learning-tutor/server/internal/safety"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

var (
	ErrSessionNotFound     = errors.New("classroom session not found")
	ErrInvalidSupport      = errors.New("unsupported classroom support type")
	ErrClassroomChanged    = errors.New("classroom changed while support response was generated")
	ErrVoiceReturnRequired = errors.New("voice explanation must return to the original question before answering")
	ErrVoiceNotActive      = errors.New("voice explanation is not active")
	ErrSessionNotActive    = errors.New("classroom session is not active")
	ErrAnotherSessionOpen  = errors.New("another classroom session is already open")
	ErrSubmitTimedOut      = errors.New("classroom submission timed out")
)

// SessionResumeRequiredCode tells the client the session was paused by
// idle-session recovery rather than by anything the student did wrong. The
// student has simply been thinking; the client resumes and retries instead of
// showing a conflict.
const SessionResumeRequiredCode = "SESSION_RESUME_REQUIRED"

type VoiceResult struct {
	Provider        string
	Model           string
	RequestID       string
	DurationSeconds string
	Segments        []speech.Segment
	AudioDataURL    string
}

type VoiceProvider interface {
	VoiceUsageIdentity() (provider, model string)
	Explain(ctx context.Context, message string) (VoiceResult, error)
}

type Service struct {
	pool          *pgxpool.Pool
	hub           *realtime.Hub
	voice         VoiceProvider
	usage         ai.UsageRecorder
	planner       *planner.Service
	agent         ai.TeachingAgent
	now           func() time.Time
	submitTimeout time.Duration
}

func (service *Service) WithTeachingAgent(agent ai.TeachingAgent) *Service {
	service.agent = agent
	return service
}

func (service *Service) WithClock(now func() time.Time) *Service {
	if now != nil {
		service.now = now
	}
	return service
}

func NewService(pool *pgxpool.Pool, hub *realtime.Hub, voice VoiceProvider, usage ai.UsageRecorder, planners ...*planner.Service) *Service {
	service := &Service{pool: pool, hub: hub, voice: voice, usage: usage, now: time.Now, submitTimeout: SubmitOverallTimeout}
	if len(planners) > 0 {
		service.planner = planners[0]
	}
	return service
}

// WithSubmitTimeout is intentionally narrow: production uses the single
// SubmitOverallTimeout value, while integration tests can inject a short
// deadline to exercise the real cancellation and cleanup path.
func (service *Service) WithSubmitTimeout(timeout time.Duration) *Service {
	if timeout > 0 {
		service.submitTimeout = timeout
	}
	return service
}

type SubmitResult struct {
	SessionID       string           `json:"session_id"`
	Version         int64            `json:"version"`
	TimingVersion   int64            `json:"timing_version"`
	Action          tutor.State      `json:"action"`
	SocraticRound   int              `json:"socratic_round"`
	Message         string           `json:"message"`
	Status          string           `json:"status,omitempty"`
	ActiveSeconds   int              `json:"active_seconds,omitempty"`
	CurrentSeconds  int              `json:"current_active_seconds,omitempty"`
	TimingAt        time.Time        `json:"timing_observed_at"`
	VoiceSegments   []speech.Segment `json:"voice_segments,omitempty"`
	VoiceAudio      string           `json:"voice_audio,omitempty"`
	MasteryState    mastery.State    `json:"mastery_state,omitempty"`
	Energy          int              `json:"energy,omitempty"`
	TomorrowChanged bool             `json:"tomorrow_plan_changed,omitempty"`
	Safety          *SafetyNotice    `json:"safety,omitempty"`
}

type SafetyNotice struct {
	PolicyVersion  string          `json:"policy_version"`
	Category       safety.Category `json:"category"`
	Severity       safety.Severity `json:"severity"`
	FixedAction    safety.Action   `json:"fixed_action"`
	ParentNotified bool            `json:"parent_notified"`
}

type SupportType string

const (
	SupportHint    SupportType = "HINT"
	SupportExplain SupportType = "EXPLAIN"
)

type sessionRow struct {
	studentID, questionID, knowledgePointID, subjectID uuid.UUID
	planBlockID, reviewQueueID                         *uuid.UUID
	state                                              tutor.State
	fails                                              int
	answer                                             string
	scoringKey                                         json.RawMessage
	misconceptions                                     json.RawMessage
	version                                            int64
	timingVersion                                      int64
	responseID                                         string
	evidenceForm                                       mastery.Form
	originalTaskID, activeTaskID                       *uuid.UUID
	startedAt                                          time.Time
	accumulatedSeconds                                 int
	lastResumedAt                                      *time.Time
	lastActivityAt                                     time.Time
	assistanceLevel                                    int
	status                                             string
	processingToken                                    *uuid.UUID
	processingUntil                                    *time.Time
	reviewAttemptFailedAt                              *time.Time
}

type preparedAgent struct {
	version            int64
	analysis           ai.AnalyzeAnswerResult
	decision           tutor.Decision
	turn               ai.TutorTurn
	deterministicMatch bool
}

type preparedVoice struct {
	version int64
	result  VoiceResult
}

func (service *Service) Submit(ctx context.Context, studentUserID, sessionID uuid.UUID, answer string) (SubmitResult, error) {
	timeout := service.submitTimeout
	if timeout <= 0 {
		timeout = SubmitOverallTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, err := service.submitWithinBudget(requestCtx, studentUserID, sessionID, answer)
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded)) {
		return SubmitResult{}, ErrSubmitTimedOut
	}
	return result, err
}

func (service *Service) submitWithinBudget(ctx context.Context, studentUserID, sessionID uuid.UUID, answer string) (SubmitResult, error) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return SubmitResult{}, errors.New("answer is required")
	}
	if err := service.RecoverStaleSessions(ctx, studentUserID); err != nil {
		return SubmitResult{}, err
	}
	state, err := service.currentState(ctx, studentUserID, sessionID)
	if err != nil {
		return SubmitResult{}, err
	}
	if state == tutor.StateVoiceExplain {
		return SubmitResult{}, ErrVoiceReturnRequired
	}
	operationStartedAt := service.now()
	operationToken, err := service.beginSessionOperationAt(ctx, studentUserID, sessionID, operationStartedAt)
	if err != nil {
		return SubmitResult{}, err
	}
	defer service.endSessionOperation(ctx, sessionID, operationToken)
	classification := safety.Classify(answer)
	if classification.Matched {
		return service.handleSafetyClassification(ctx, studentUserID, sessionID, operationToken, classification)
	}
	var prepared *preparedAgent
	if service.agent != nil {
		prepared, err = service.prepareAgent(ctx, studentUserID, sessionID, operationToken, answer)
		if err != nil {
			return SubmitResult{}, err
		}
	}
	usageTime := service.now()
	voice, err := service.prepareVoice(ctx, studentUserID, sessionID, operationToken, answer, prepared, usageTime)
	if err != nil {
		return SubmitResult{}, err
	}
	var published []realtime.Event
	var result SubmitResult
	var planStudentID uuid.UUID
	var now time.Time
	err = pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		row, err := service.loadSessionForOperation(ctx, tx, studentUserID, sessionID, operationToken)
		if err != nil {
			return err
		}
		if prepared != nil && prepared.version != row.version {
			return ErrClassroomChanged
		}
		if voice != nil && voice.version != row.version {
			return ErrClassroomChanged
		}
		// AI/provider wait is not learning time. The operation start is the
		// authoritative activity checkpoint for this submission.
		now = latestTime(operationStartedAt, row.lastActivityAt)
		evaluation := evaluateSubmittedAnswer(answer, row.answer, row.scoringKey, prepared)
		answerID := uuid.New()
		evaluationProvenanceID := uuid.New()
		var rawAnalysis *ai.AnalyzeAnswerResult
		if prepared != nil && !prepared.deterministicMatch {
			rawAnalysis = &prepared.analysis
		}
		provenance := legacyAnswerEvaluationProvenance(evaluation.deterministicCorrect, rawAnalysis, evaluation.correct)
		turnSequence, eventSequence, err := nextSequences(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns (id,session_id,sequence,actor,action,message) VALUES ($1,$2,$3,'STUDENT',NULL,$4)`, uuid.New(), sessionID, turnSequence, answer); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO student_answers (id,session_id,question_id,answer_text) VALUES ($1,$2,$3,$4)`, answerID, sessionID, row.questionID, answer); err != nil {
			return err
		}
		errorType := "NONE"
		reasoningQuality := reasoning(evaluation.correct)
		emotionSignal := "NEUTRAL"
		engagement := "NORMAL"
		analysisMisconceptions := row.misconceptions
		recommended := string(tutor.StateVariant)
		if !evaluation.correct {
			errorType, recommended = firstMisconception(row.misconceptions), string(nextDecision(row).NextState)
		}
		confidence := 1.0
		var weaknessLayer *string
		if prepared != nil && !prepared.deterministicMatch {
			reasoningQuality = prepared.analysis.ReasoningQuality
			emotionSignal = prepared.analysis.EmotionSignal
			engagement = prepared.analysis.Engagement
			confidence = prepared.analysis.Confidence
			if !evaluation.correct {
				if prepared.analysis.ErrorType != "" {
					errorType = prepared.analysis.ErrorType
				}
				if len(prepared.analysis.Misconceptions) > 0 {
					analysisMisconceptions, _ = json.Marshal(prepared.analysis.Misconceptions)
				}
				recommended = string(prepared.decision.NextState)
				if ai.ValidWeaknessLayer(prepared.analysis.WeaknessLayer) {
					weaknessLayer = &prepared.analysis.WeaknessLayer
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO answer_analyses (id,student_answer_id,answer_correct,reasoning_quality,confidence,error_type,misconceptions_private_json,emotion_signal,engagement,recommended_action,weakness_layer) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, uuid.New(), answerID, evaluation.correct, reasoningQuality, confidence, errorType, misconceptionPayload(evaluation.correct, analysisMisconceptions), emotionSignal, engagement, recommended, weaknessLayer); err != nil {
			return err
		}
		if err := insertAnswerEvaluationProvenance(ctx, tx, evaluationProvenanceID, answerID, provenance, now); err != nil {
			return err
		}
		misconceptionCode := firstMisconception(analysisMisconceptions)
		submittedStudent, _ := json.Marshal(map[string]any{"status": "RECEIVED"})
		submittedParent, _ := json.Marshal(map[string]any{"answer_visibility": "REFRESH_LIVE_ENDPOINT"})
		submittedEvent := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventAnswerSubmitted, submittedStudent, submittedParent, now)
		if err := insertEvent(ctx, tx, submittedEvent); err != nil {
			return err
		}
		published = append(published, submittedEvent)
		analyzedStudent, _ := json.Marshal(map[string]any{"status": "ANALYZED"})
		analyzedParent, _ := json.Marshal(map[string]any{"answer_visibility": "REFRESH_LIVE_ENDPOINT", "correct_answer": row.answer, "answer_correct": evaluation.correct, "reasoning_quality": reasoningQuality, "confidence": confidence, "error_type": errorType, "misconception": misconceptionCode, "emotion_signal": emotionSignal, "engagement": engagement, "recommended_action": recommended, "weakness_layer": weaknessLayer})
		analyzedEvent := makeEvent(row.studentID, sessionID, eventSequence+1, realtime.EventAnswerAnalyzed, analyzedStudent, analyzedParent, now)
		if err := insertEvent(ctx, tx, analyzedEvent); err != nil {
			return err
		}
		published = append(published, analyzedEvent)
		eventSequence += 2
		if prepared != nil && !prepared.deterministicMatch {
			aiStudent, _ := json.Marshal(map[string]any{"status": "COMPLETED"})
			aiParent, _ := json.Marshal(map[string]any{"status": "COMPLETED", "purpose": "ANSWER_ANALYSIS"})
			aiEvent := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventAITurnCompleted, aiStudent, aiParent, now)
			if err := insertEvent(ctx, tx, aiEvent); err != nil {
				return err
			}
			published = append(published, aiEvent)
			eventSequence++
		}
		if !evaluation.correct {
			if err := recordMisconceptions(ctx, tx, row, sessionID, now, misconceptionCodes(analysisMisconceptions)); err != nil {
				return err
			}
			if err := recordReviewFailure(ctx, tx, row, sessionID, now); err != nil {
				return err
			}
		}

		if evaluation.correct {
			var completedEvents []realtime.Event
			result, completedEvents, err = service.complete(ctx, tx, row, sessionID, answerID, evaluationProvenanceID, provenance, turnSequence+1, eventSequence, now)
			published = append(published, completedEvents...)
			if result.Action == tutor.StateComplete {
				planStudentID = row.studentID
			}
			return err
		}
		decision := nextDecision(row)
		message := tutorMessage(decision.NextState)
		responseID := ""
		if prepared != nil && !prepared.deterministicMatch {
			decision = prepared.decision
			message = prepared.turn.Message
			responseID = prepared.turn.ResponseID
		}
		if decision.NextState == tutor.StateBacktrack {
			backtrackResult, backtrackEvent, found, err := service.beginBacktrack(ctx, tx, row, sessionID, turnSequence+1, eventSequence, now, responseID, engagement)
			if err != nil {
				return err
			}
			if found {
				result = backtrackResult
				published = append(published, backtrackEvent)
				return nil
			}
			decision = tutor.Decision{NextState: tutor.StateScaffold, SocraticRound: max(1, row.fails+1), Reason: "prerequisite gap reported but no released dependency is available; use a smaller local step"}
			message = tutorMessage(decision.NextState)
			responseID = ""
		}
		result = sessionSubmitResult(row, sessionID, row.version+1, decision.NextState, decision.SocraticRound, message, now)
		if decision.NextState == tutor.StateVoiceExplain {
			if voice == nil {
				return errors.New("voice explanation was not prepared")
			}
			result.VoiceSegments = voice.result.Segments
			result.VoiceAudio = voice.result.AudioDataURL
			outputID := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO speech_outputs (id,student_id,session_id,provider,model,duration_seconds,audio_data_url) VALUES ($1,$2,$3,$4,$5,$6::numeric,$7)`, outputID, row.studentID, sessionID, voice.result.Provider, voice.result.Model, voice.result.DurationSeconds, voice.result.AudioDataURL); err != nil {
				return err
			}
			for index, segment := range voice.result.Segments {
				if _, err := tx.Exec(ctx, `INSERT INTO speech_segments(id,speech_output_id,sequence,text,start_ms,end_ms)VALUES($1,$2,$3,$4,$5,$6)`, uuid.New(), outputID, index+1, segment.Text, segment.StartMS, segment.EndMS); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET current_state=$2,socratic_fail_count=$3,teaching_response_id=COALESCE(NULLIF($4,''),teaching_response_id),engagement_state=$5,assistance_level=GREATEST(assistance_level,$6),last_activity_at=CASE WHEN status='ACTIVE' THEN $7 ELSE last_activity_at END,processing_token=NULL,processing_until=NULL,version=version+1 WHERE id=$1`, sessionID, decision.NextState, max(row.fails, decision.SocraticRound), responseID, engagement, assistanceForState(decision.NextState), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns (id,session_id,sequence,actor,action,message,reason_private) VALUES ($1,$2,$3,'TUTOR',$4,$5,$6)`, uuid.New(), sessionID, turnSequence+1, decision.NextState, message, decision.Reason); err != nil {
			return err
		}
		studentPayload, _ := json.Marshal(map[string]any{"action": decision.NextState, "round": decision.SocraticRound, "message": message})
		parentPayload, _ := json.Marshal(map[string]any{"answer_visibility": "REFRESH_LIVE_ENDPOINT", "correct_answer": row.answer, "answer_correct": false, "error_type": errorType, "misconception": misconceptionCode, "action": decision.NextState, "round": decision.SocraticRound, "message": message, "reason": decision.Reason})
		event := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventTutorActionSelected, studentPayload, parentPayload, now)
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
		published = append(published, event)
		if decision.NextState == tutor.StateVoiceExplain {
			voiceStudent, _ := json.Marshal(map[string]any{"action": decision.NextState, "message": message})
			voiceParent, _ := json.Marshal(map[string]any{"action": decision.NextState, "message": message, "reason": decision.Reason})
			voiceEvent := makeEvent(row.studentID, sessionID, eventSequence+1, realtime.EventVoiceExplainStarted, voiceStudent, voiceParent, now)
			if err := insertEvent(ctx, tx, voiceEvent); err != nil {
				return err
			}
			published = append(published, voiceEvent)
		}
		return nil
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if planStudentID != uuid.Nil {
		if service.planner == nil {
			log.Printf("completed classroom %s without adaptive planner", sessionID)
		} else if _, changed, err := service.planner.EnsureWithStatus(ctx, planStudentID, now.AddDate(0, 0, 1), sessionID); err != nil {
			log.Printf("refresh tomorrow plan after classroom %s: %v", sessionID, err)
		} else if changed {
			result.TomorrowChanged = true
			planEvent, err := service.recordEvent(ctx, planStudentID, sessionID, realtime.EventPlanModified,
				map[string]any{"tomorrow_plan_changed": true},
				map[string]any{"action": tutor.StateComplete, "tomorrow_plan_changed": true, "reason": "session performance changed the next-day plan"}, now)
			if err != nil {
				log.Printf("record tomorrow plan refresh for classroom %s: %v", sessionID, err)
			} else {
				published = append(published, planEvent)
			}
		}
	}
	for _, event := range published {
		if service.hub != nil {
			_ = service.hub.Publish(event)
		}
	}
	return result, nil
}

func (service *Service) handleSafetyClassification(ctx context.Context, studentUserID, sessionID, operationToken uuid.UUID, classification safety.Classification) (SubmitResult, error) {
	var result SubmitResult
	var event realtime.Event
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		row, err := service.loadSessionForOperation(ctx, tx, studentUserID, sessionID, operationToken)
		if err != nil {
			return err
		}
		now := latestTime(service.now(), row.lastActivityAt)
		_, eventSequence, err := nextSequences(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		var notice *SafetyNotice
		_, event, notice, err = recordSafetyIntervention(ctx, tx, row.studentID, sessionID, eventSequence, classification, now)
		if err != nil {
			return err
		}
		result = sessionSubmitResult(row, sessionID, row.version, row.state, row.fails, classification.StudentMessage, now)
		result.Safety = notice
		return nil
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if service.hub != nil {
		_ = service.hub.Publish(event)
	}
	return result, nil
}

func recordSafetyIntervention(
	ctx context.Context,
	tx pgx.Tx,
	studentID, sessionID uuid.UUID,
	eventSequence int64,
	classification safety.Classification,
	now time.Time,
) (uuid.UUID, realtime.Event, *SafetyNotice, error) {
	incidentID := uuid.New()
	if _, err := tx.Exec(ctx, `
INSERT INTO minor_safety_incidents(
    id,student_id,session_id,policy_version,category,severity,fixed_action,parent_escalated,created_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		incidentID, studentID, sessionID, safety.PolicyVersion, classification.Category,
		classification.Severity, classification.Action, classification.EscalateToParent, now); err != nil {
		return uuid.Nil, realtime.Event{}, nil, err
	}
	studentPayload, _ := json.Marshal(map[string]any{
		"policy_version":  safety.PolicyVersion,
		"category":        classification.Category,
		"severity":        classification.Severity,
		"fixed_action":    classification.Action,
		"message":         classification.StudentMessage,
		"parent_notified": classification.EscalateToParent,
	})
	parentPayload := json.RawMessage(`{}`)
	if classification.EscalateToParent {
		parentPayload, _ = json.Marshal(map[string]any{
			"policy_version": safety.PolicyVersion,
			"category":       classification.Category,
			"severity":       classification.Severity,
			"fixed_action":   classification.Action,
			"occurred_at":    now,
		})
	}
	event := makeEvent(studentID, sessionID, eventSequence, realtime.EventSafetyIntervention, studentPayload, parentPayload, now)
	event.ParentSuppressed = !classification.EscalateToParent
	if err := insertEvent(ctx, tx, event); err != nil {
		return uuid.Nil, realtime.Event{}, nil, err
	}
	return incidentID, event, &SafetyNotice{
		PolicyVersion:  safety.PolicyVersion,
		Category:       classification.Category,
		Severity:       classification.Severity,
		FixedAction:    classification.Action,
		ParentNotified: classification.EscalateToParent,
	}, nil
}

func (service *Service) beginBacktrack(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID uuid.UUID, turnSequence, eventSequence int64, now time.Time, responseID, engagement string) (SubmitResult, realtime.Event, bool, error) {
	var prerequisiteKnowledgePointID, prerequisiteSubjectID, prerequisiteQuestionID uuid.UUID
	var prerequisiteName, prerequisitePrompt, prerequisiteSubject string
	err := tx.QueryRow(ctx, `
SELECT dep.target_knowledge_point_id,kp.subject_id,s.code,kp.name,q.id,q.prompt_public
	FROM cross_subject_dependencies dep
	JOIN knowledge_points kp ON kp.id=dep.target_knowledge_point_id AND kp.status='RELEASED'
	JOIN grade_bands grade_band ON grade_band.code=kp.grade_band_code
	JOIN students student ON student.id=$2
	JOIN subjects s ON s.id=kp.subject_id
JOIN questions q ON q.knowledge_point_id=kp.id AND q.status='RELEASED'
LEFT JOIN student_skill_states ss ON ss.student_id=$2 AND ss.knowledge_point_id=kp.id
	WHERE dep.source_knowledge_point_id=$1
	  AND grade_band.min_grade<=student.grade_level
	  AND COALESCE(ss.state,'UNKNOWN') NOT IN('UNDERSTOOD','MASTERED')
ORDER BY dep.strength DESC,q.difficulty,q.id LIMIT 1`, row.knowledgePointID, row.studentID).Scan(&prerequisiteKnowledgePointID, &prerequisiteSubjectID, &prerequisiteSubject, &prerequisiteName, &prerequisiteQuestionID, &prerequisitePrompt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubmitResult{}, realtime.Event{}, false, nil
	}
	if err != nil {
		return SubmitResult{}, realtime.Event{}, false, err
	}
	message := "发现可能的底层缺口，先补一小步：" + prerequisiteName + "。完成后会自动回到原题。"
	if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET original_task_id=COALESCE(original_task_id,$2),active_task_id=$3,current_question_id=$4,subject_id=$5,current_state='BACKTRACK',socratic_fail_count=0,evidence_form='TEXTBOOK',teaching_response_id=COALESCE(NULLIF($6,''),teaching_response_id),engagement_state=$7,assistance_level=GREATEST(assistance_level,3),last_activity_at=CASE WHEN status='ACTIVE' THEN $8 ELSE last_activity_at END,processing_token=NULL,processing_until=NULL,version=version+1 WHERE id=$1`, sessionID, row.knowledgePointID, prerequisiteKnowledgePointID, prerequisiteQuestionID, prerequisiteSubjectID, responseID, engagement, now); err != nil {
		return SubmitResult{}, realtime.Event{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private,response_id) VALUES($1,$2,$3,'TUTOR','BACKTRACK',$4,'released cross-subject prerequisite selected; original task preserved',NULLIF($5,''))`, uuid.New(), sessionID, turnSequence, message, responseID); err != nil {
		return SubmitResult{}, realtime.Event{}, false, err
	}
	studentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateBacktrack, "message": message, "subject": prerequisiteSubject, "knowledge_point": prerequisiteName, "prompt": prerequisitePrompt})
	parentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateBacktrack, "message": message, "reason": "released cross-subject prerequisite selected; original task preserved", "original_knowledge_point_id": row.knowledgePointID, "prerequisite_knowledge_point_id": prerequisiteKnowledgePointID, "prerequisite_subject": prerequisiteSubject, "prerequisite_prompt": prerequisitePrompt})
	event := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventBacktrackStarted, studentPayload, parentPayload, now)
	if err := insertEvent(ctx, tx, event); err != nil {
		return SubmitResult{}, realtime.Event{}, false, err
	}
	return sessionSubmitResult(row, sessionID, row.version+1, tutor.StateBacktrack, 0, message, now), event, true, nil
}

func (service *Service) currentState(ctx context.Context, studentUserID, sessionID uuid.UUID) (tutor.State, error) {
	var state tutor.State
	var status string
	err := service.pool.QueryRow(ctx, `SELECT ls.current_state,ls.status FROM learning_sessions ls JOIN students st ON st.id=ls.student_id WHERE ls.id=$1 AND st.user_id=$2`, sessionID, studentUserID).Scan(&state, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "ACTIVE" {
		return "", ErrSessionNotActive
	}
	return state, nil
}

func (service *Service) prepareVoice(ctx context.Context, studentUserID, sessionID, operationToken uuid.UUID, answer string, prepared *preparedAgent, now time.Time) (*preparedVoice, error) {
	var row sessionRow
	err := service.pool.QueryRow(ctx, `SELECT ls.student_id,ls.current_state,ls.socratic_fail_count,a.teacher_reference_answer,a.scoring_key_json,ls.version,ls.status,ls.processing_token,ls.processing_until FROM learning_sessions ls JOIN students st ON st.id=ls.student_id JOIN questions q ON q.id=ls.current_question_id AND q.status='RELEASED' JOIN question_private_answers a ON a.question_id=q.id WHERE ls.id=$1 AND st.user_id=$2`, sessionID, studentUserID).Scan(&row.studentID, &row.state, &row.fails, &row.answer, &row.scoringKey, &row.version, &row.status, &row.processingToken, &row.processingUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if row.status != "ACTIVE" && row.status != "PAUSED" {
		return nil, ErrSessionNotActive
	}
	if !service.operationLeaseValid(row.processingToken, row.processingUntil, operationToken) {
		return nil, ErrClassroomChanged
	}
	if evaluateSubmittedAnswer(answer, row.answer, row.scoringKey, prepared).correct {
		return nil, nil
	}
	decision := nextDecision(row)
	message := tutorMessage(decision.NextState)
	if prepared != nil {
		decision = prepared.decision
		message = prepared.turn.Message
	}
	if decision.NextState != tutor.StateVoiceExplain {
		return nil, nil
	}
	if service.voice == nil || service.usage == nil {
		return nil, errors.New("voice explanation provider and usage recorder are required")
	}
	started := time.Now()
	guard, ok := service.usage.(ai.PriceGuard)
	if !ok {
		return nil, errors.New("voice usage price guard is required")
	}
	provider, model := service.voice.VoiceUsageIdentity()
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(model) == "" {
		return nil, errors.New("voice provider billing identity is required")
	}
	if err := guard.EnsurePrice(ctx, provider, model, started); err != nil {
		return nil, err
	}
	voice, err := service.voice.Explain(ctx, message)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(voice.AudioDataURL, "data:audio/") {
		return nil, errors.New("voice explanation audio data URL is required")
	}
	if len(voice.AudioDataURL) > 24<<20 {
		return nil, errors.New("voice explanation audio exceeds persistence limit")
	}
	usageRecord := ai.UsageRecord{RequestID: voice.RequestID, StudentID: row.studentID.String(), SessionID: sessionID.String(), Purpose: ai.PurposeTTSExplanation, Latency: time.Since(started), CreatedAt: now, Usage: ai.ModelUsage{Provider: voice.Provider, Model: voice.Model, AudioOutputSeconds: voice.DurationSeconds}}
	if err := service.usage.RecordAIUsage(ctx, usageRecord); err != nil {
		return nil, err
	}
	return &preparedVoice{version: row.version, result: voice}, nil
}

func (service *Service) RequestSupport(ctx context.Context, studentUserID, sessionID uuid.UUID, support SupportType) (SubmitResult, error) {
	if err := service.RecoverStaleSessions(ctx, studentUserID); err != nil {
		return SubmitResult{}, err
	}
	decision := tutor.Decision{AnswerRevealAllowed: false}
	eventType := realtime.EventHintRequested
	switch support {
	case SupportHint:
		decision.NextState, decision.Reason = tutor.StateHint, "student requested one bounded hint"
	case SupportExplain:
		decision.NextState, decision.Reason = tutor.StateExplain, "student requested a parallel example without the original answer"
		eventType = realtime.EventAITurnCompleted
	default:
		return SubmitResult{}, ErrInvalidSupport
	}
	operationToken, err := service.beginSessionOperation(ctx, studentUserID, sessionID)
	if err != nil {
		return SubmitResult{}, err
	}
	defer service.endSessionOperation(ctx, sessionID, operationToken)
	var studentID, questionID uuid.UUID
	var version int64
	var responseID, status string
	var processingToken *uuid.UUID
	var processingUntil *time.Time
	if err := service.pool.QueryRow(ctx, `SELECT ls.student_id,ls.current_question_id,ls.version,COALESCE(ls.teaching_response_id,''),ls.status,ls.processing_token,ls.processing_until FROM learning_sessions ls JOIN students st ON st.id=ls.student_id WHERE ls.id=$1 AND st.user_id=$2`, sessionID, studentUserID).Scan(&studentID, &questionID, &version, &responseID, &status, &processingToken, &processingUntil); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SubmitResult{}, ErrSessionNotFound
		}
		return SubmitResult{}, err
	}
	if status != "ACTIVE" && status != "PAUSED" {
		return SubmitResult{}, ErrSessionNotActive
	}
	if !service.operationLeaseValid(processingToken, processingUntil, operationToken) {
		return SubmitResult{}, ErrClassroomChanged
	}
	message := tutorMessage(decision.NextState)
	newResponseID := ""
	if service.agent != nil {
		question, err := content.NewRepository(service.pool).ReleasedQuestionForTeaching(ctx, questionID)
		if err != nil {
			return SubmitResult{}, err
		}
		prior, err := service.priorTurns(ctx, sessionID)
		if err != nil {
			return SubmitResult{}, err
		}
		request := ai.GenerateTurnRequest{StudentID: studentID.String(), SessionID: sessionID.String(), Question: question.Public, Teaching: question.Teaching, AuditPrivateAnswer: question.Private, TutorDecision: decision, PriorTurns: prior, PreviousResponseID: responseID}
		var turn ai.TutorTurn
		if support == SupportExplain {
			turn, err = service.agent.GenerateParallelExample(ctx, ai.ExampleRequest(request))
		} else {
			turn, err = service.agent.GenerateTurn(ctx, request)
		}
		if err != nil {
			return SubmitResult{}, err
		}
		if turn.Action != decision.NextState {
			return SubmitResult{}, errors.New("teaching agent action does not match support decision")
		}
		message, newResponseID = turn.Message, turn.ResponseID
	}
	var event realtime.Event
	var resultRow sessionRow
	var resultAt time.Time
	var resultVersion int64
	err = pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		row, err := service.loadSessionForOperation(ctx, tx, studentUserID, sessionID, operationToken)
		if err != nil {
			return err
		}
		if row.version != version {
			return ErrClassroomChanged
		}
		now := latestTime(service.now(), row.lastActivityAt)
		resultRow = row
		resultAt = now
		resultVersion = row.version + 1
		turnSequence, eventSequence, err := nextSequences(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET current_state=$2,teaching_response_id=COALESCE(NULLIF($3,''),teaching_response_id),assistance_level=GREATEST(assistance_level,$4),last_activity_at=CASE WHEN status='ACTIVE' THEN $5 ELSE last_activity_at END,processing_token=NULL,processing_until=NULL,version=version+1 WHERE id=$1`, sessionID, decision.NextState, newResponseID, assistanceForState(decision.NextState), now); err != nil {
			return err
		}
		turnID := uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private,response_id) VALUES($1,$2,$3,'TUTOR',$4,$5,$6,NULLIF($7,''))`, turnID, sessionID, turnSequence, decision.NextState, message, decision.Reason, newResponseID); err != nil {
			return err
		}
		if err := recordLegacySupportLearningEffect(ctx, tx, row, sessionID, turnID, support, string(decision.NextState), max(row.assistanceLevel, assistanceForState(decision.NextState)), now); err != nil {
			return err
		}
		studentPayload, _ := json.Marshal(map[string]any{"action": decision.NextState, "message": message})
		parentPayload, _ := json.Marshal(map[string]any{"action": decision.NextState, "message": message, "reason": decision.Reason})
		event = makeEvent(row.studentID, sessionID, eventSequence, eventType, studentPayload, parentPayload, now)
		return insertEvent(ctx, tx, event)
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if service.hub != nil {
		_ = service.hub.Publish(event)
	}
	result := sessionSubmitResult(resultRow, sessionID, resultVersion, decision.NextState, 0, message, resultAt)
	return result, nil
}

func (service *Service) ReturnFromVoice(ctx context.Context, studentUserID, sessionID uuid.UUID) (SubmitResult, error) {
	if err := service.RecoverStaleSessions(ctx, studentUserID); err != nil {
		return SubmitResult{}, err
	}
	var event realtime.Event
	var socraticRound int
	var version int64
	var resultRow sessionRow
	var resultAt time.Time
	message := "语音讲解已经结束，现在回到原题，用刚才的方法自己验证一次。"
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		row, err := loadSession(ctx, tx, studentUserID, sessionID)
		if err != nil {
			return err
		}
		if row.state != tutor.StateVoiceExplain {
			return ErrVoiceNotActive
		}
		now := latestTime(service.now(), row.lastActivityAt)
		resultRow = row
		resultAt = now
		socraticRound = row.fails
		version = row.version + 1
		turnSequence, eventSequence, err := nextSequences(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET current_state='RETURN',last_activity_at=$2,version=version+1 WHERE id=$1`, sessionID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private) VALUES($1,$2,$3,'TUTOR','RETURN',$4,'voice explanation completed; original released question remains active')`, uuid.New(), sessionID, turnSequence, message); err != nil {
			return err
		}
		studentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateReturn, "message": message})
		parentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateReturn, "message": message, "reason": "voice explanation completed; original released question remains active"})
		event = makeEvent(row.studentID, sessionID, eventSequence, realtime.EventVoiceExplainCompleted, studentPayload, parentPayload, now)
		return insertEvent(ctx, tx, event)
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if service.hub != nil {
		_ = service.hub.Publish(event)
	}
	return sessionSubmitResult(resultRow, sessionID, version, tutor.StateReturn, socraticRound, message, resultAt), nil
}

func loadSession(ctx context.Context, tx pgx.Tx, userID, sessionID uuid.UUID) (sessionRow, error) {
	row, err := loadSessionRow(ctx, tx, userID, sessionID)
	if err != nil {
		return row, err
	}
	if row.status != "ACTIVE" {
		return row, ErrSessionNotActive
	}
	return row, nil
}

func (service *Service) loadSessionForOperation(ctx context.Context, tx pgx.Tx, userID, sessionID, operationToken uuid.UUID) (sessionRow, error) {
	if err := auth.LockPrincipalSession(ctx, tx, userID); err != nil {
		return sessionRow{}, err
	}
	row, err := loadSessionRow(ctx, tx, userID, sessionID)
	if err != nil {
		return row, err
	}
	if row.status != "ACTIVE" && row.status != "PAUSED" {
		return row, ErrSessionNotActive
	}
	if !service.operationLeaseValid(row.processingToken, row.processingUntil, operationToken) {
		return row, ErrClassroomChanged
	}
	return row, nil
}

func (service *Service) operationLeaseValid(token *uuid.UUID, until *time.Time, expected uuid.UUID) bool {
	return token != nil && *token == expected && until != nil && service.now().Before(*until)
}

func loadSessionRow(ctx context.Context, tx pgx.Tx, userID, sessionID uuid.UUID) (sessionRow, error) {
	var row sessionRow
	err := tx.QueryRow(ctx, `SELECT ls.student_id,ls.current_question_id,q.knowledge_point_id,ls.subject_id,ls.plan_block_id,ls.review_queue_id,ls.current_state,ls.socratic_fail_count,a.teacher_reference_answer,a.scoring_key_json,a.misconceptions_private_json,ls.version,ls.timing_version,COALESCE(ls.teaching_response_id,''),ls.evidence_form,ls.original_task_id,ls.active_task_id,ls.started_at,ls.accumulated_seconds,ls.last_resumed_at,ls.last_activity_at,ls.assistance_level,ls.status,ls.processing_token,ls.processing_until,ls.review_attempt_failed_at FROM learning_sessions ls JOIN students st ON st.id=ls.student_id JOIN questions q ON q.id=ls.current_question_id AND q.status='RELEASED' JOIN question_private_answers a ON a.question_id=q.id WHERE ls.id=$1 AND st.user_id=$2 FOR UPDATE OF ls`, sessionID, userID).Scan(&row.studentID, &row.questionID, &row.knowledgePointID, &row.subjectID, &row.planBlockID, &row.reviewQueueID, &row.state, &row.fails, &row.answer, &row.scoringKey, &row.misconceptions, &row.version, &row.timingVersion, &row.responseID, &row.evidenceForm, &row.originalTaskID, &row.activeTaskID, &row.startedAt, &row.accumulatedSeconds, &row.lastResumedAt, &row.lastActivityAt, &row.assistanceLevel, &row.status, &row.processingToken, &row.processingUntil, &row.reviewAttemptFailedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, ErrSessionNotFound
	}
	if err != nil {
		return row, err
	}
	return row, nil
}

func (service *Service) prepareAgent(ctx context.Context, userID, sessionID, operationToken uuid.UUID, answer string) (*preparedAgent, error) {
	var studentID, questionID uuid.UUID
	var state tutor.State
	var fails int
	var version int64
	var responseID, status, referenceAnswer string
	var scoringKey json.RawMessage
	var processingToken *uuid.UUID
	var processingUntil *time.Time
	err := service.pool.QueryRow(ctx, `SELECT ls.student_id,ls.current_question_id,ls.current_state,ls.socratic_fail_count,ls.version,COALESCE(ls.teaching_response_id,''),ls.status,ls.processing_token,ls.processing_until,a.teacher_reference_answer,a.scoring_key_json FROM learning_sessions ls JOIN students st ON st.id=ls.student_id JOIN questions q ON q.id=ls.current_question_id AND q.status='RELEASED' JOIN question_private_answers a ON a.question_id=q.id WHERE ls.id=$1 AND st.user_id=$2`, sessionID, userID).Scan(&studentID, &questionID, &state, &fails, &version, &responseID, &status, &processingToken, &processingUntil, &referenceAnswer, &scoringKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "ACTIVE" && status != "PAUSED" {
		return nil, ErrSessionNotActive
	}
	if !service.operationLeaseValid(processingToken, processingUntil, operationToken) {
		return nil, ErrClassroomChanged
	}
	if answersMatch(answer, referenceAnswer, scoringKey) {
		return &preparedAgent{
			version: version,
			analysis: ai.AnalyzeAnswerResult{
				AnswerCorrect: true, ReasoningQuality: "STRONG", Confidence: 1,
				ErrorType: "NONE", EmotionSignal: "NEUTRAL", Engagement: "NORMAL",
				RecommendedAction: tutor.StateVariant,
			},
			decision:           tutor.Decision{NextState: tutor.StateVariant, Reason: "deterministic match; skip model analysis"},
			deterministicMatch: true,
		}, nil
	}
	question, err := content.NewRepository(service.pool).ReleasedQuestionForTeaching(ctx, questionID)
	if err != nil {
		return nil, err
	}
	prior, err := service.priorTurns(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	helpRequested := unmatchedHelpRequest(answer, referenceAnswer, scoringKey)
	var analysis ai.AnalyzeAnswerResult
	if helpRequested {
		analysis = ai.AnalyzeAnswerResult{
			AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: 1,
			ErrorType: "HELP_REQUEST", EmotionSignal: "NEUTRAL", Engagement: "NORMAL",
			RecommendedAction: tutor.StateHint,
		}
	} else {
		analysis, err = service.agent.AnalyzeAnswer(ctx, ai.AnalyzeAnswerRequest{StudentID: studentID.String(), SessionID: sessionID.String(), Question: question, StudentAnswer: answer, PriorTurns: prior})
		if err != nil {
			return nil, err
		}
		// The provider already checks this; the classroom checks again because
		// the agent is an interface, and a wrong answer without a layer must
		// take the same busy path as any other rejected analysis.
		if err := ai.ValidateWeaknessLayer(analysis); err != nil {
			return nil, ai.NewTutorGenerationBusyFailure(ai.TutorReviewFailureInvalidSchema, 0, ai.TutorReviewDiagnosticUnavailable, "", err)
		}
	}
	serverAnalysis := tutor.Analysis{
		AnswerCorrect: analysis.AnswerCorrect, ReasoningQuality: parseReasoning(analysis.ReasoningQuality),
		PrerequisiteGap: analysis.RecommendedAction == tutor.StateBacktrack, Emotion: parseEmotion(analysis.EmotionSignal),
		HintRequested: helpRequested || analysis.RecommendedAction == tutor.StateHint,
		DontKnow:      helpRequested, VoicePreferred: analysis.RecommendedAction == tutor.StateVoiceExplain,
	}
	decision := tutor.NewEngine(3).Decide(tutor.Session{State: state, SocraticFailedRounds: fails, ActiveTaskID: questionID.String()}, serverAnalysis)
	prepared := &preparedAgent{version: version, analysis: analysis, decision: decision}
	if decision.NextState == tutor.StateVariant {
		return prepared, nil
	}
	request := ai.GenerateTurnRequest{StudentID: studentID.String(), SessionID: sessionID.String(), Question: question.Public, Teaching: question.Teaching, AuditPrivateAnswer: question.Private, StudentAnswer: answer, TutorDecision: decision, PriorTurns: prior, PreviousResponseID: responseID}
	// Only the rounds that keep probing the same question follow the layer.
	// Help, emotion, backtracking and the failed-round limit decide their own
	// next step, so the layer never reaches them.
	switch decision.NextState {
	case tutor.StateProbe, tutor.StateScaffold, tutor.StateAnalogy:
		if !analysis.AnswerCorrect {
			request.WeaknessLayer = analysis.WeaknessLayer
		}
	}
	var turn ai.TutorTurn
	switch decision.NextState {
	case tutor.StateAnalogy:
		turn, err = service.agent.GenerateAnalogy(ctx, ai.AnalogyRequest(request))
	case tutor.StateExplain, tutor.StateVoiceExplain:
		turn, err = service.agent.GenerateExplanation(ctx, ai.ExplainRequest(request))
	default:
		turn, err = service.agent.GenerateTurn(ctx, request)
	}
	if err != nil {
		return nil, err
	}
	if turn.Action != decision.NextState {
		return nil, fmt.Errorf("teaching agent action %s does not match server decision %s", turn.Action, decision.NextState)
	}
	prepared.turn = turn
	return prepared, nil
}

func (service *Service) priorTurns(ctx context.Context, sessionID uuid.UUID) ([]ai.TutorTurn, error) {
	rows, err := service.pool.Query(ctx, `SELECT message,COALESCE(action,'') FROM tutor_turns WHERE session_id=$1 ORDER BY sequence DESC LIMIT 12`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reverse := []ai.TutorTurn{}
	for rows.Next() {
		var turn ai.TutorTurn
		var action string
		if err := rows.Scan(&turn.Message, &action); err != nil {
			return nil, err
		}
		if action != "" {
			turn.Action = tutor.State(action)
		}
		reverse = append(reverse, turn)
	}
	turns := make([]ai.TutorTurn, len(reverse))
	for i := range reverse {
		turns[len(reverse)-1-i] = reverse[i]
	}
	return turns, rows.Err()
}
func parseReasoning(value string) tutor.ReasoningQuality {
	switch value {
	case "STRONG":
		return tutor.ReasoningStrong
	case "PARTIAL":
		return tutor.ReasoningPartial
	default:
		return tutor.ReasoningWeak
	}
}
func parseEmotion(value string) tutor.Emotion {
	switch value {
	case "FRUSTRATED", "ANXIOUS":
		return tutor.EmotionFrustrated
	case "BORED":
		return tutor.EmotionBored
	default:
		return tutor.EmotionNeutral
	}
}

func nextDecision(row sessionRow) tutor.Decision {
	return tutor.NewEngine(3).Decide(tutor.Session{State: row.state, SocraticFailedRounds: row.fails, ActiveTaskID: row.questionID.String()}, tutor.Analysis{ReasoningQuality: tutor.ReasoningWeak, VoicePreferred: true})
}

func sessionSubmitResult(row sessionRow, sessionID uuid.UUID, version int64, action tutor.State, socraticRound int, message string, now time.Time) SubmitResult {
	timing := timingFromRow(lifecycleRow{
		sessionID:          sessionID,
		status:             row.status,
		startedAt:          row.startedAt,
		accumulatedSeconds: row.accumulatedSeconds,
		lastResumedAt:      row.lastResumedAt,
		version:            version,
		timingVersion:      row.timingVersion,
	}, now)
	return SubmitResult{
		SessionID:      sessionID.String(),
		Version:        version,
		TimingVersion:  timing.TimingVersion,
		Action:         action,
		SocraticRound:  socraticRound,
		Message:        message,
		Status:         row.status,
		ActiveSeconds:  timing.ActiveSeconds,
		CurrentSeconds: timing.CurrentActiveSeconds,
		TimingAt:       timing.TimingObservedAt,
	}
}

func (service *Service) complete(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID, studentAnswerID, evaluationProvenanceID uuid.UUID, provenance answerEvaluationProvenance, turnSequence, eventSequence int64, now time.Time) (SubmitResult, []realtime.Event, error) {
	skill, err := loadSkill(ctx, tx, row.studentID, row.knowledgePointID)
	if err != nil {
		return SubmitResult{}, nil, err
	}
	independent := row.assistanceLevel == 0
	skill = mastery.NewEngine().Apply(skill, mastery.Evidence{Correct: true, Independent: independent, Form: row.evidenceForm, At: now})
	if err := resolveSuccessfulReview(ctx, tx, row, skill, independent, now); err != nil {
		return SubmitResult{}, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE student_misconceptions SET successful_corrections=successful_corrections+1,status='MONITORING',last_seen_at=$3 WHERE student_id=$1 AND knowledge_point_id=$2 AND status='ACTIVE'`, row.studentID, row.knowledgePointID, now); err != nil {
		return SubmitResult{}, nil, err
	}
	score := mastery.NewEngine().Score(skill)
	if err := saveSkill(ctx, tx, row.studentID, row.knowledgePointID, skill, score, now); err != nil {
		return SubmitResult{}, nil, err
	}
	if independent {
		if err := insertMasteryEvidenceProvenance(ctx, tx, studentAnswerID, evaluationProvenanceID, row, provenance, now); err != nil {
			return SubmitResult{}, nil, err
		}
	}
	rewardType, rewardSource := reward.Effort, sessionID.String()
	if skill.State == mastery.Mastered {
		rewardType, rewardSource = reward.Mastery, row.knowledgePointID.String()
	}
	returnToOriginal := row.originalTaskID != nil && row.activeTaskID != nil && *row.originalTaskID != *row.activeTaskID
	if returnToOriginal {
		rewardType, rewardSource = reward.CrossSubjectInsight, sessionID.String()+":"+row.knowledgePointID.String()
	}
	energy, err := grantReward(ctx, tx, row.studentID, sessionID, rewardType, rewardSource)
	if err != nil {
		return SubmitResult{}, nil, err
	}
	if returnToOriginal {
		return service.returnToOriginal(ctx, tx, row, sessionID, turnSequence, eventSequence, now, skill.State, energy)
	}
	message := "这次思路已经记录。"
	if skill.State == mastery.Mastered {
		message = "你已经在生活、变式、课本和跨天复习中都能独立解决，掌握证据完整。"
	}
	activeSeconds := checkpointTotal(lifecycleRow{startedAt: row.startedAt, accumulatedSeconds: row.accumulatedSeconds, lastResumedAt: row.lastResumedAt, status: row.status}, now)
	if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET current_state='COMPLETE',status='COMPLETED',ended_at=$2,accumulated_seconds=$3,actual_seconds=$3,last_resumed_at=NULL,last_activity_at=CASE WHEN status='ACTIVE' THEN $2 ELSE last_activity_at END,processing_token=NULL,processing_until=NULL,version=version+1,timing_version=timing_version+1 WHERE id=$1`, sessionID, now, activeSeconds); err != nil {
		return SubmitResult{}, nil, err
	}
	if row.planBlockID != nil {
		if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='COMPLETED' WHERE id=$1`, *row.planBlockID); err != nil {
			return SubmitResult{}, nil, err
		}
	}
	if err := recordStudentActivity(ctx, tx, row.studentID, sessionID, now); err != nil {
		return SubmitResult{}, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns (id,session_id,sequence,actor,action,message,reason_private) VALUES ($1,$2,$3,'TUTOR','COMPLETE',$4,'rules engine verified spaced mastery')`, uuid.New(), sessionID, turnSequence, message); err != nil {
		return SubmitResult{}, nil, err
	}
	studentPayload, _ := json.Marshal(map[string]any{"mastery_state": skill.State, "mastery_score": score, "energy": energy, "tomorrow_plan_changed": false, "message": message})
	parentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateComplete, "mastery_state": skill.State, "mastery_score": score, "evidence_form": row.evidenceForm, "energy": energy, "tomorrow_plan_changed": false, "message": message})
	masteryEvent := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventMasteryUpdated, studentPayload, parentPayload, now)
	if err := insertEvent(ctx, tx, masteryEvent); err != nil {
		return SubmitResult{}, nil, err
	}
	rewardStudent, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": rewardType})
	rewardParent, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": rewardType, "source_id": rewardSource})
	rewardEvent := makeEvent(row.studentID, sessionID, eventSequence+1, realtime.EventRewardGranted, rewardStudent, rewardParent, now)
	if err := insertEvent(ctx, tx, rewardEvent); err != nil {
		return SubmitResult{}, nil, err
	}
	completedStudent, _ := json.Marshal(map[string]any{"action": tutor.StateComplete, "message": message})
	completedParent, _ := json.Marshal(map[string]any{"action": tutor.StateComplete, "mastery_state": skill.State, "mastery_score": score, "energy": energy, "message": message})
	completedEvent := makeEvent(row.studentID, sessionID, eventSequence+2, realtime.EventSessionCompleted, completedStudent, completedParent, now)
	if err := insertEvent(ctx, tx, completedEvent); err != nil {
		return SubmitResult{}, nil, err
	}
	return SubmitResult{SessionID: sessionID.String(), Version: row.version + 1, TimingVersion: row.timingVersion + 1, Action: tutor.StateComplete, Message: message, Status: "COMPLETED", ActiveSeconds: activeSeconds, CurrentSeconds: 0, TimingAt: now, MasteryState: skill.State, Energy: energy}, []realtime.Event{masteryEvent, rewardEvent, completedEvent}, nil
}

func (service *Service) returnToOriginal(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID uuid.UUID, turnSequence, eventSequence int64, now time.Time, skillState mastery.State, energy int) (SubmitResult, []realtime.Event, error) {
	var questionID, subjectID uuid.UUID
	var prompt string
	if err := tx.QueryRow(ctx, `
	SELECT q.id,kp.subject_id,q.prompt_public FROM questions q
	JOIN knowledge_points kp ON kp.id=q.knowledge_point_id AND kp.status='RELEASED'
	JOIN grade_bands grade_band ON grade_band.code=kp.grade_band_code
	JOIN students student ON student.id=$2
	WHERE q.knowledge_point_id=$1 AND q.status='RELEASED' AND grade_band.min_grade<=student.grade_level
	ORDER BY q.difficulty,q.id LIMIT 1`, *row.originalTaskID, row.studentID).Scan(&questionID, &subjectID, &prompt); err != nil {
		return SubmitResult{}, nil, fmt.Errorf("released original task unavailable: %w", err)
	}
	message := "底层知识已经补好，现在回到原问题，用刚才的方法再验证一次。"
	if _, err := tx.Exec(ctx, `UPDATE learning_sessions SET current_question_id=$2,subject_id=$3,current_state='RETURN',socratic_fail_count=0,active_task_id=original_task_id,evidence_form='VARIANT',last_activity_at=CASE WHEN status='ACTIVE' THEN $4 ELSE last_activity_at END,processing_token=NULL,processing_until=NULL,version=version+1 WHERE id=$1`, sessionID, questionID, subjectID, now); err != nil {
		return SubmitResult{}, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private) VALUES($1,$2,$3,'TUTOR','RETURN',$4,'cross-subject prerequisite verified; original task restored')`, uuid.New(), sessionID, turnSequence, message); err != nil {
		return SubmitResult{}, nil, err
	}
	studentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateReturn, "message": message, "prompt": prompt})
	parentPayload, _ := json.Marshal(map[string]any{"action": tutor.StateReturn, "message": message, "remediated_knowledge_point_id": row.knowledgePointID, "restored_knowledge_point_id": row.originalTaskID, "mastery_state": skillState, "energy": energy})
	masteryStudent, _ := json.Marshal(map[string]any{"mastery_state": skillState, "energy": energy})
	masteryParent, _ := json.Marshal(map[string]any{"mastery_state": skillState, "energy": energy, "knowledge_point_id": row.knowledgePointID})
	masteryEvent := makeEvent(row.studentID, sessionID, eventSequence, realtime.EventMasteryUpdated, masteryStudent, masteryParent, now)
	if err := insertEvent(ctx, tx, masteryEvent); err != nil {
		return SubmitResult{}, nil, err
	}
	rewardStudent, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": reward.CrossSubjectInsight})
	rewardParent, _ := json.Marshal(map[string]any{"energy": energy, "reward_type": reward.CrossSubjectInsight})
	rewardEvent := makeEvent(row.studentID, sessionID, eventSequence+1, realtime.EventRewardGranted, rewardStudent, rewardParent, now)
	if err := insertEvent(ctx, tx, rewardEvent); err != nil {
		return SubmitResult{}, nil, err
	}
	event := makeEvent(row.studentID, sessionID, eventSequence+2, realtime.EventBacktrackCompleted, studentPayload, parentPayload, now)
	if err := insertEvent(ctx, tx, event); err != nil {
		return SubmitResult{}, nil, err
	}
	result := sessionSubmitResult(row, sessionID, row.version+1, tutor.StateReturn, 0, message, now)
	result.MasteryState = skillState
	result.Energy = energy
	return result, []realtime.Event{masteryEvent, rewardEvent, event}, nil
}

func loadSkill(ctx context.Context, tx pgx.Tx, studentID, knowledgePointID uuid.UUID) (mastery.Skill, error) {
	var skill mastery.Skill
	err := tx.QueryRow(ctx, `SELECT state,independent_successes,assisted_successes,life_context_successes,variant_successes,textbook_successes,review_successes,consecutive_review_failures,next_review_at FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, studentID, knowledgePointID).Scan(&skill.State, &skill.IndependentSuccesses, &skill.AssistedSuccesses, &skill.LifeContextSuccesses, &skill.VariantSuccesses, &skill.TextbookSuccesses, &skill.ReviewSuccesses, &skill.ConsecutiveReviewFailures, &skill.NextReviewAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return mastery.Skill{}, nil
	}
	return skill, err
}

func grantReward(ctx context.Context, tx pgx.Tx, studentID, sessionID uuid.UUID, rewardType reward.Type, sourceID string) (int, error) {
	growth, _, err := (reward.Engine{}).Apply(reward.Growth{}, reward.Event{Type: rewardType, SourceID: sourceID})
	if err != nil {
		return 0, err
	}
	points := growth.TotalEnergy
	if _, err := tx.Exec(ctx, `INSERT INTO student_growth(student_id,total_energy,buildings_json) VALUES($1,0,'{}') ON CONFLICT(student_id) DO NOTHING`, studentID); err != nil {
		return 0, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO reward_events(id,student_id,session_id,type,points,source_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(student_id,type,source_id) DO NOTHING`, uuid.New(), studentID, sessionID, rewardType, points, sourceID)
	if err != nil {
		return 0, err
	}
	if command.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `INSERT INTO student_growth(student_id,total_energy,buildings_json) VALUES($1,$2,'{}') ON CONFLICT(student_id) DO UPDATE SET total_energy=student_growth.total_energy+EXCLUDED.total_energy,updated_at=now()`, studentID, points); err != nil {
			return 0, err
		}
	}
	var energy int
	err = tx.QueryRow(ctx, `SELECT total_energy FROM student_growth WHERE student_id=$1`, studentID).Scan(&energy)
	return energy, err
}

func recordStudentActivity(ctx context.Context, tx pgx.Tx, studentID, sessionID uuid.UUID, now time.Time) error {
	activityDate := learningDate(now)
	if _, err := tx.Exec(ctx, `
INSERT INTO student_activity_days(student_id,activity_date,completed_sessions,active_seconds,first_completed_at,last_completed_at)
SELECT $1,$2::date,1,actual_seconds,$3,$3 FROM learning_sessions WHERE id=$4
ON CONFLICT(student_id,activity_date) DO UPDATE SET
completed_sessions=student_activity_days.completed_sessions+1,
active_seconds=student_activity_days.active_seconds+EXCLUDED.active_seconds,
last_completed_at=EXCLUDED.last_completed_at`, studentID, activityDate, now, sessionID); err != nil {
		return err
	}
	streak, err := currentStreak(ctx, tx, studentID, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
UPDATE student_growth SET streak_days=$2,buildings_json=jsonb_build_object(
    'completed_days',(SELECT count(*) FROM student_activity_days WHERE student_id=$1),
    'completed_sessions',(SELECT COALESCE(sum(completed_sessions),0) FROM student_activity_days WHERE student_id=$1),
    'mastered_knowledge_points',(SELECT count(*) FROM student_skill_states WHERE student_id=$1 AND state='MASTERED'),
    'mastered_by_subject',COALESCE((
        SELECT jsonb_object_agg(code,total) FROM (
            SELECT s.code,count(*) AS total FROM student_skill_states ss
            JOIN knowledge_points kp ON kp.id=ss.knowledge_point_id
            JOIN subjects s ON s.id=kp.subject_id
            WHERE ss.student_id=$1 AND ss.state='MASTERED' GROUP BY s.code
        ) counts
    ),'{}'::jsonb),
    'corrected_misconceptions',(SELECT COALESCE(sum(successful_corrections),0) FROM student_misconceptions WHERE student_id=$1),
    'cross_subject_insights',(SELECT count(*) FROM reward_events WHERE student_id=$1 AND type='CROSS_SUBJECT_INSIGHT')
),updated_at=$3 WHERE student_id=$1`, studentID, streak, now)
	return err
}

func nextSequences(ctx context.Context, tx pgx.Tx, sessionID uuid.UUID) (int64, int64, error) {
	var turns, events int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM tutor_turns WHERE session_id=$1`, sessionID).Scan(&turns); err != nil {
		return 0, 0, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM tutor_events WHERE session_id=$1`, sessionID).Scan(&events); err != nil {
		return 0, 0, err
	}
	return turns, events, nil
}
func makeEvent(studentID, sessionID uuid.UUID, sequence int64, eventType realtime.EventType, student, parent json.RawMessage, now time.Time) realtime.Event {
	return realtime.Event{EventID: uuid.NewString(), StudentID: studentID.String(), SessionID: sessionID.String(), Sequence: sequence, Type: eventType, CreatedAt: now, StudentPayload: student, ParentPayload: parent}
}
func insertEvent(ctx context.Context, tx pgx.Tx, event realtime.Event) error {
	_, err := tx.Exec(ctx, `INSERT INTO tutor_events(id,session_id,student_id,sequence,type,student_payload_json,parent_payload_json,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, event.EventID, event.SessionID, event.StudentID, event.Sequence, event.Type, event.StudentPayload, event.ParentPayload, event.CreatedAt)
	return err
}

func (service *Service) recordEvent(ctx context.Context, studentID, sessionID uuid.UUID, eventType realtime.EventType, studentPayload, parentPayload map[string]any, now time.Time) (realtime.Event, error) {
	var event realtime.Event
	err := pgx.BeginFunc(ctx, service.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM learning_sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&sessionID); err != nil {
			return err
		}
		_, sequence, err := nextSequences(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		student, _ := json.Marshal(studentPayload)
		parent, _ := json.Marshal(parentPayload)
		event = makeEvent(studentID, sessionID, sequence, eventType, student, parent, now)
		return insertEvent(ctx, tx, event)
	})
	return event, err
}
func normalized(value string) string { return strings.Join(strings.Fields(strings.ToLower(value)), "") }

func foldAnswer(value string) string {
	folded := strings.ToLower(value)
	replacer := strings.NewReplacer(
		"＝", "=", "－", "-", "—", "-", "–", "-", "＋", "+", "×", "*", "÷", "/",
		"（", "(", "）", ")", "，", ",", "。", "", "；", ";", "：", ":",
		"元", "", "每张", "", "门票", "", "的价格", "", "价格", "",
	)
	folded = replacer.Replace(folded)
	return strings.Join(strings.Fields(folded), "")
}

type submittedEvaluation struct {
	deterministicCorrect bool
	correct              bool
}

func evaluateSubmittedAnswer(answer, reference string, scoringKey json.RawMessage, prepared *preparedAgent) submittedEvaluation {
	matched := answersMatch(answer, reference, scoringKey)
	if prepared != nil && prepared.deterministicMatch {
		matched = true
	}
	evaluation := submittedEvaluation{deterministicCorrect: matched, correct: matched}
	if unmatchedHelpRequest(answer, reference, scoringKey) {
		return evaluation
	}
	if prepared != nil && !prepared.deterministicMatch && prepared.analysis.AnswerCorrect && prepared.analysis.Confidence >= 0.9 {
		evaluation.correct = true
	}
	return evaluation
}

func unmatchedHelpRequest(answer, reference string, scoringKey json.RawMessage) bool {
	return !answersMatch(answer, reference, scoringKey) && isHelpRequest(answer)
}

func answersMatch(answer, reference string, scoringKey json.RawMessage) bool {
	if foldAnswer(answer) != "" && foldAnswer(answer) == foldAnswer(reference) {
		return true
	}
	if normalized(answer) != "" && normalized(answer) == normalized(reference) {
		return true
	}
	for _, candidate := range acceptedAnswers(scoringKey) {
		if foldAnswer(answer) == foldAnswer(candidate) || normalized(answer) == normalized(candidate) {
			return true
		}
	}
	return false
}

func acceptedAnswers(scoringKey json.RawMessage) []string {
	if len(scoringKey) == 0 {
		return nil
	}
	var parsed struct {
		Accepted []string `json:"accepted_answers"`
	}
	if err := json.Unmarshal(scoringKey, &parsed); err != nil {
		return nil
	}
	return parsed.Accepted
}

func isHelpRequest(answer string) bool {
	folded := strings.ToLower(strings.TrimSpace(answer))
	if folded == "" {
		return false
	}
	phrases := []string{
		"看不懂", "我看不懂", "我不会", "我不知道", "我不懂", "什么意思", "是什么意思",
		"求助", "帮我", "给我提示", "unknown word", "don't know", "dont know",
		"i don't know", "i dont know", "cannot read", "can't read", "cant read",
	}
	for _, phrase := range phrases {
		if strings.Contains(folded, phrase) {
			return true
		}
	}
	switch folded {
	case "不会", "不懂", "不知道", "提示", "hint", "help":
		return true
	default:
		return false
	}
}
func reasoning(correct bool) string {
	if correct {
		return "STRONG"
	}
	return "WEAK"
}
func misconceptionPayload(correct bool, values json.RawMessage) json.RawMessage {
	if correct {
		return json.RawMessage(`[]`)
	}
	if len(values) == 0 {
		return json.RawMessage(`["REASONING_GAP"]`)
	}
	return values
}
func firstMisconception(values json.RawMessage) string {
	codes := misconceptionCodes(values)
	if len(codes) > 0 {
		return codes[0]
	}
	return "REASONING_GAP"
}

func misconceptionCodes(values json.RawMessage) []string {
	var reported []string
	if json.Unmarshal(values, &reported) != nil {
		return nil
	}
	seen := make(map[string]struct{}, len(reported))
	codes := make([]string, 0, len(reported))
	for _, code := range reported {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes
}

func recordMisconceptions(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID uuid.UUID, now time.Time, codes []string) error {
	recorded := false
	for _, code := range codes {
		var id uuid.UUID
		var allowed bool
		err := tx.QueryRow(ctx, `
SELECT misconception.id,link.knowledge_point_id IS NOT NULL
FROM misconceptions misconception
LEFT JOIN knowledge_misconception_links link
  ON link.misconception_id=misconception.id AND link.knowledge_point_id=$2
WHERE misconception.code=$1`, code, row.knowledgePointID).Scan(&id, &allowed)
		if errors.Is(err, pgx.ErrNoRows) {
			if err := recordMisconceptionQualityEvent(ctx, tx, row, sessionID, now, code, nil, "UNKNOWN_CODE"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !allowed {
			if err := recordMisconceptionQualityEvent(ctx, tx, row, sessionID, now, code, &id, "CROSS_KNOWLEDGE_POINT"); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO student_misconceptions(student_id,knowledge_point_id,misconception_id,first_seen_at,last_seen_at) VALUES($1,$2,$3,$4,$4) ON CONFLICT(student_id,knowledge_point_id,misconception_id) DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at,occurrences=student_misconceptions.occurrences+1,status='ACTIVE'`, row.studentID, row.knowledgePointID, id, now); err != nil {
			return err
		}
		recorded = true
	}
	if !recorded {
		return nil
	}
	_, err := tx.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority)
VALUES($1,$2,$3,'MISCONCEPTION',$4,80)
ON CONFLICT(student_id,knowledge_point_id,source) WHERE status='PENDING'
DO UPDATE SET due_at=LEAST(review_queue.due_at,EXCLUDED.due_at),priority=GREATEST(review_queue.priority,EXCLUDED.priority)`, uuid.New(), row.studentID, row.knowledgePointID, now.Add(24*time.Hour))
	return err
}

func recordMisconceptionQualityEvent(ctx context.Context, tx pgx.Tx, row sessionRow, sessionID uuid.UUID, now time.Time, code string, recognizedID *uuid.UUID, reason string) error {
	hash := sha256.Sum256([]byte(code))
	_, err := tx.Exec(ctx, `INSERT INTO misconception_quality_events(id,student_id,session_id,knowledge_point_id,code_hash,recognized_misconception_id,reason,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.New(), row.studentID, sessionID, row.knowledgePointID, fmt.Sprintf("%x", hash), recognizedID, reason, now)
	return err
}
func tutorMessage(state tutor.State) string {
	switch state {
	case tutor.StateProbe:
		return "先说说你用了哪些已知量？"
	case tutor.StateScaffold:
		return "先只完成最小的一步：圈出题目要求你判断或求出的对象。"
	case tutor.StateAnalogy:
		return "换成一个相似的生活场景，再比较两部分。"
	case tutor.StateVoiceExplain:
		return "我们听一个不同数值的平行例子，然后回到原题。"
	case tutor.StateHint:
		return "先找出题目中最能支持判断的一个关键信息。"
	case tutor.StateExplain:
		return "先换一个不同情境：用自己的话说清要判断的对象，再找能支持判断的证据，最后把同样的方法带回原题。"
	default:
		return "再想一小步。"
	}
}

func assistanceForState(state tutor.State) int {
	switch state {
	case tutor.StateProbe, tutor.StateHint:
		return 1
	case tutor.StateScaffold:
		return 2
	case tutor.StateAnalogy, tutor.StateBacktrack:
		return 3
	case tutor.StateExplain, tutor.StateVoiceExplain:
		return 4
	default:
		return 0
	}
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
