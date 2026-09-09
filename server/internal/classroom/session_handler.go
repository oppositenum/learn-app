package classroom

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
)

type StudentSession struct {
	ID             uuid.UUID                   `json:"id"`
	Version        int64                       `json:"version"`
	TimingVersion  int64                       `json:"timing_version"`
	PlanBlockID    *uuid.UUID                  `json:"plan_block_id,omitempty"`
	SubjectCode    string                      `json:"subject_code"`
	SubjectName    string                      `json:"subject_name"`
	KnowledgePoint string                      `json:"knowledge_point"`
	Difficulty     string                      `json:"difficulty"`
	QuestionID     uuid.UUID                   `json:"question_id"`
	Prompt         string                      `json:"prompt"`
	Scene          json.RawMessage             `json:"scene"`
	InputSchema    json.RawMessage             `json:"input_schema"`
	StartedAt      time.Time                   `json:"started_at"`
	TargetMinutes  int                         `json:"target_minutes"`
	Status         string                      `json:"status"`
	ActiveSeconds  int                         `json:"active_seconds"`
	CurrentSeconds int                         `json:"current_active_seconds"`
	ActiveSince    *time.Time                  `json:"active_since,omitempty"`
	TimingAt       time.Time                   `json:"timing_observed_at"`
	State          string                      `json:"state"`
	SocraticRound  int                         `json:"socratic_round"`
	Timeline       []StudentTurn               `json:"timeline"`
	VoiceSegments  []speech.Segment            `json:"voice_segments,omitempty"`
	VoiceAudio     string                      `json:"voice_audio,omitempty"`
	StageFlow      *StudentStageFlow           `json:"stage_flow,omitempty"`
	Interaction    studentinteraction.Material `json:"interaction"`
}

type StudentTurn struct {
	Sequence int64     `json:"sequence"`
	Actor    string    `json:"actor"`
	Action   *string   `json:"action,omitempty"`
	Message  string    `json:"message"`
	At       time.Time `json:"at"`
}

func (handler *Handler) StartSession(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	var body struct {
		PlanBlockID uuid.UUID `json:"plan_block_id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.PlanBlockID == uuid.Nil {
		http.Error(writer, "invalid plan block", http.StatusBadRequest)
		return
	}
	if err := handler.service.RecoverStaleSessions(request.Context(), userID); err != nil {
		if errors.Is(err, auth.ErrSessionRevoked) {
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}
		http.Error(writer, "session could not start", http.StatusInternalServerError)
		return
	}
	var sessionID uuid.UUID
	var startedEvents []realtime.Event
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		if err := auth.LockPrincipalSession(request.Context(), tx, userID); err != nil {
			return err
		}
		var studentID, subjectID, knowledgePointID, questionID uuid.UUID
		var minutes int
		var mode, blockStatus string
		var subjectCode, knowledgePointName, prompt string
		var originalTaskID, reviewQueueID *uuid.UUID
		var stageStart *stageTask
		if err := tx.QueryRow(request.Context(), `SELECT id FROM students WHERE user_id=$1 FOR NO KEY UPDATE`, userID).Scan(&studentID); err != nil {
			return err
		}
		now := handler.now()
		err := tx.QueryRow(request.Context(), `
			SELECT p.student_id,b.subject_id,b.knowledge_point_id,b.minutes,b.mode,b.status,b.original_task_id,b.review_queue_id,q.id,s.code,kp.name,q.prompt_public
	FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id
	JOIN students st ON st.id=p.student_id
	JOIN subjects s ON s.id=b.subject_id
	JOIN knowledge_points kp ON kp.id=b.knowledge_point_id AND kp.status='RELEASED'
	JOIN grade_bands grade_band ON grade_band.code=kp.grade_band_code
	JOIN questions q ON q.knowledge_point_id=b.knowledge_point_id AND q.status='RELEASED'
		WHERE b.id=$1 AND st.user_id=$2 AND p.plan_date=$3::date AND p.status IN('PROPOSED','ACTIVE')
		  AND grade_band.min_grade<=st.grade_level
		  AND (b.mode<>'CURRENT_GRADE' OR st.grade_level<=grade_band.max_grade)
				ORDER BY q.difficulty,q.id LIMIT 1 FOR UPDATE OF p,b`, body.PlanBlockID, userID, learningDate(now)).Scan(&studentID, &subjectID, &knowledgePointID, &minutes, &mode, &blockStatus, &originalTaskID, &reviewQueueID, &questionID, &subjectCode, &knowledgePointName, &prompt)
		if err != nil {
			return err
		}
		var openBlockID *uuid.UUID
		if err := tx.QueryRow(request.Context(), `SELECT id,plan_block_id FROM learning_sessions WHERE student_id=$1 AND status IN ('ACTIVE','PAUSED') ORDER BY started_at DESC LIMIT 1`, studentID).Scan(&sessionID, &openBlockID); err == nil {
			if openBlockID != nil && *openBlockID == body.PlanBlockID {
				return nil
			}
			return ErrAnotherSessionOpen
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if blockStatus != "AVAILABLE" {
			return ErrAnotherSessionOpen
		}
		if mode == "REVIEW" {
			if reviewQueueID == nil {
				return pgx.ErrNoRows
			}
			var lockedQueueID uuid.UUID
			if err := tx.QueryRow(request.Context(), `
SELECT id FROM review_queue
WHERE id=$1 AND student_id=$2 AND knowledge_point_id=$3
  AND status='PENDING' AND due_at<=$4
FOR UPDATE`, *reviewQueueID, studentID, knowledgePointID, now).Scan(&lockedQueueID); err != nil {
				return err
			}
		}
		if mode != "REVIEW" {
			task, found, err := loadReadyStageStartTask(request.Context(), tx, knowledgePointID)
			if err != nil {
				return err
			}
			if found {
				stageStart = &task
				questionID = task.ID
				prompt = task.Prompt
			}
		}
		sessionID = uuid.New()
		evidenceForm := "LIFE"
		switch mode {
		case "REVIEW":
			evidenceForm = "REVIEW"
		case "REMEDIATION":
			evidenceForm = "VARIANT"
		case "MICRO_BACKTRACK":
			evidenceForm = "TEXTBOOK"
		}
		initialState := "ASK"
		if stageStart != nil {
			initialState = string(StageOriginal)
			evidenceForm = stageStart.EvidenceForm
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO learning_sessions(id,student_id,plan_block_id,review_queue_id,subject_id,current_question_id,started_at,status,target_minutes,current_state,original_task_id,active_task_id,evidence_form,last_resumed_at,last_activity_at)VALUES($1,$2,$3,$4,$5,$6,$11,'ACTIVE',$7,$12,$8,$9,$10,$11,$11)`, sessionID, studentID, body.PlanBlockID, reviewQueueID, subjectID, questionID, minutes, originalTaskID, knowledgePointID, evidenceForm, now, initialState); err != nil {
			return err
		}
		if stageStart != nil {
			if _, err := tx.Exec(request.Context(), `
INSERT INTO classroom_stage_sessions(
    session_id,lineage_id,knowledge_point_id,current_task_id,current_task_version,started_at
) VALUES($1,$2,$3,$4,$5,$6)`, sessionID, stageStart.LineageID,
				knowledgePointID, stageStart.ID, stageStart.ContentVersion, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(request.Context(), `UPDATE learning_plan_blocks SET status='ACTIVE' WHERE id=$1`, body.PlanBlockID); err != nil {
			return err
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private)SELECT $1,$2,1,'TUTOR',$4,prompt_public,$5 FROM questions WHERE id=$3`, uuid.New(), sessionID, questionID, initialState, map[bool]string{true: "server selected complete released four-stage lineage", false: "server selected released plan content"}[stageStart != nil]); err != nil {
			return err
		}
		startedStudent, _ := json.Marshal(map[string]any{"action": initialState, "subject": subjectCode, "knowledge_point": knowledgePointName})
		startedParent, _ := json.Marshal(map[string]any{"action": initialState, "subject": subjectCode, "knowledge_point": knowledgePointName, "target_minutes": minutes})
		started := makeEvent(studentID, sessionID, 1, realtime.EventSessionStarted, startedStudent, startedParent, now)
		if err := insertEvent(request.Context(), tx, started); err != nil {
			return err
		}
		questionStudent, _ := json.Marshal(map[string]any{"action": initialState, "prompt": prompt})
		questionParent, _ := json.Marshal(map[string]any{"action": initialState, "prompt": prompt, "question_id": questionID})
		presented := makeEvent(studentID, sessionID, 2, realtime.EventQuestionPresented, questionStudent, questionParent, now)
		if err := insertEvent(request.Context(), tx, presented); err != nil {
			return err
		}
		startedEvents = []realtime.Event{started, presented}
		_, err = tx.Exec(request.Context(), `UPDATE learning_plans SET status='ACTIVE' WHERE id=(SELECT plan_id FROM learning_plan_blocks WHERE id=$1) AND status='PROPOSED'`, body.PlanBlockID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "plan block not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrAnotherSessionOpen) {
		http.Error(writer, "finish or leave the current session before starting another", http.StatusConflict)
		return
	}
	if errors.Is(err, ErrStagePreparationIncomplete) {
		writeJSON(writer, http.StatusConflict, map[string]string{"code": "CLASSROOM_STAGE_CONTENT_INCOMPLETE"})
		return
	}
	if errors.Is(err, auth.ErrSessionRevoked) {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(writer, "session could not start", http.StatusConflict)
		return
	}
	for _, event := range startedEvents {
		if handler.service != nil && handler.service.hub != nil {
			_ = handler.service.hub.Publish(event)
		}
	}
	handler.writeStudentSession(writer, request, userID, sessionID)
}

func (handler *Handler) GetSession(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	if err := handler.service.RecoverStaleSessions(request.Context(), userID); err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	handler.writeStudentSession(writer, request, userID, sessionID)
}

func (handler *Handler) CurrentSession(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	if err := handler.service.RecoverStaleSessions(request.Context(), userID); err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	var session StudentSession
	err = pgx.BeginTxFunc(request.Context(), handler.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var sessionID uuid.UUID
		if err := tx.QueryRow(request.Context(), `SELECT ls.id FROM learning_sessions ls JOIN students st ON st.id=ls.student_id WHERE st.user_id=$1 AND ls.status IN ('ACTIVE','PAUSED') ORDER BY ls.started_at DESC LIMIT 1`, userID).Scan(&sessionID); err != nil {
			return err
		}
		var err error
		session, err = readStudentSession(request.Context(), tx, userID, sessionID, true, handler.now())
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if errors.Is(err, errVoiceExplanationUnavailable) {
		http.Error(writer, "voice explanation unavailable", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func (handler *Handler) writeStudentSession(writer http.ResponseWriter, request *http.Request, userID, sessionID uuid.UUID) {
	var session StudentSession
	err := pgx.BeginTxFunc(request.Context(), handler.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		session, err = readStudentSession(request.Context(), tx, userID, sessionID, false, handler.now())
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, errVoiceExplanationUnavailable) {
		http.Error(writer, "voice explanation unavailable", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

var errVoiceExplanationUnavailable = errors.New("voice explanation unavailable")

type studentSessionQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readStudentSession(ctx context.Context, db studentSessionQueryer, userID, sessionID uuid.UUID, requireOpen bool, now time.Time) (StudentSession, error) {
	var session StudentSession
	var accumulatedSeconds int
	var lastResumedAt *time.Time
	var stageTaskVersion *string
	err := db.QueryRow(ctx, `
		SELECT ls.id,ls.version,ls.timing_version,ls.plan_block_id,s.code,s.name_zh,kp.name,q.difficulty,q.id,q.prompt_public,q.scene_public_json,q.input_schema_json,ls.started_at,ls.target_minutes,ls.status,ls.accumulated_seconds,ls.last_resumed_at,ls.current_state,ls.socratic_fail_count,stage_session.current_task_version
		FROM learning_sessions ls JOIN students st ON st.id=ls.student_id
		JOIN subjects s ON s.id=ls.subject_id JOIN questions q ON q.id=ls.current_question_id AND q.status='RELEASED'
		JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
		LEFT JOIN classroom_stage_sessions stage_session ON stage_session.session_id=ls.id
			WHERE ls.id=$1 AND st.user_id=$2
			  AND (NOT $3 OR ls.status IN ('ACTIVE','PAUSED'))`, sessionID, userID, requireOpen).Scan(&session.ID, &session.Version, &session.TimingVersion, &session.PlanBlockID, &session.SubjectCode, &session.SubjectName, &session.KnowledgePoint, &session.Difficulty, &session.QuestionID, &session.Prompt, &session.Scene, &session.InputSchema, &session.StartedAt, &session.TargetMinutes, &session.Status, &accumulatedSeconds, &lastResumedAt, &session.State, &session.SocraticRound, &stageTaskVersion)
	if err != nil {
		return session, err
	}
	timing := timingFromRow(lifecycleRow{sessionID: session.ID, status: session.Status, startedAt: session.StartedAt, accumulatedSeconds: accumulatedSeconds, lastResumedAt: lastResumedAt, timingVersion: session.TimingVersion}, now)
	session.ActiveSeconds = timing.ActiveSeconds
	session.CurrentSeconds = timing.CurrentActiveSeconds
	session.ActiveSince = timing.ActiveSince
	session.TimingAt = timing.TimingObservedAt
	if stageTaskVersion != nil {
		session.StageFlow = &StudentStageFlow{Version: stageFlowVersion, Stage: Stage(session.State), TaskVersion: *stageTaskVersion}
	}
	session.Interaction = studentinteraction.Resolve(session.Prompt, session.Scene, session.InputSchema)
	if session.Interaction.Fallback {
		session.Scene = json.RawMessage(`{}`)
		session.InputSchema = session.Interaction.AnswerSchema
	}
	rows, err := db.Query(ctx, `SELECT sequence,actor,action,message,created_at FROM tutor_turns WHERE session_id=$1 ORDER BY sequence`, sessionID)
	if err != nil {
		return session, err
	}
	session.Timeline = []StudentTurn{}
	for rows.Next() {
		var turn StudentTurn
		if err := rows.Scan(&turn.Sequence, &turn.Actor, &turn.Action, &turn.Message, &turn.At); err != nil {
			rows.Close()
			return session, err
		}
		session.Timeline = append(session.Timeline, turn)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return session, err
	}
	rows.Close()
	if session.State == "VOICE_EXPLAIN" {
		var outputID uuid.UUID
		err := db.QueryRow(ctx, `SELECT id,audio_data_url FROM speech_outputs WHERE session_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, sessionID).Scan(&outputID, &session.VoiceAudio)
		if errors.Is(err, pgx.ErrNoRows) {
			return session, errVoiceExplanationUnavailable
		}
		if err != nil {
			return session, err
		}
		segmentRows, err := db.Query(ctx, `SELECT id::text,text,start_ms,end_ms FROM speech_segments WHERE speech_output_id=$1 ORDER BY sequence`, outputID)
		if err != nil {
			return session, err
		}
		for segmentRows.Next() {
			var segment speech.Segment
			if err := segmentRows.Scan(&segment.ID, &segment.Text, &segment.StartMS, &segment.EndMS); err != nil {
				segmentRows.Close()
				return session, err
			}
			session.VoiceSegments = append(session.VoiceSegments, segment)
		}
		if err := segmentRows.Err(); err != nil {
			segmentRows.Close()
			return session, err
		}
		segmentRows.Close()
	}
	return session, nil
}

func (handler *Handler) PauseSession(writer http.ResponseWriter, request *http.Request) {
	handler.writeLifecycleResult(writer, request, handler.service.PauseSession)
}

func (handler *Handler) ResumeSession(writer http.ResponseWriter, request *http.Request) {
	handler.writeLifecycleResult(writer, request, handler.service.ResumeSession)
}

func (handler *Handler) AbandonSession(writer http.ResponseWriter, request *http.Request) {
	handler.writeLifecycleResult(writer, request, handler.service.AbandonSession)
}

func (handler *Handler) HeartbeatSession(writer http.ResponseWriter, request *http.Request) {
	handler.writeLifecycleResult(writer, request, handler.service.HeartbeatSession)
}

func (handler *Handler) writeLifecycleResult(writer http.ResponseWriter, request *http.Request, transition func(context.Context, uuid.UUID, uuid.UUID) (SessionTiming, error)) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}
	result, err := transition(request.Context(), userID, sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, ErrSessionNotActive) {
		http.Error(writer, "session can no longer change", http.StatusConflict)
		return
	}
	if errors.Is(err, auth.ErrSessionRevoked) {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(writer, "session state could not be changed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}
