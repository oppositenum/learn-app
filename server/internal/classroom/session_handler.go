package classroom

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
)

type StudentSession struct {
	ID             uuid.UUID        `json:"id"`
	SubjectCode    string           `json:"subject_code"`
	SubjectName    string           `json:"subject_name"`
	KnowledgePoint string           `json:"knowledge_point"`
	Difficulty     string           `json:"difficulty"`
	QuestionID     uuid.UUID        `json:"question_id"`
	Prompt         string           `json:"prompt"`
	Scene          json.RawMessage  `json:"scene"`
	InputSchema    json.RawMessage  `json:"input_schema"`
	StartedAt      time.Time        `json:"started_at"`
	TargetMinutes  int              `json:"target_minutes"`
	State          string           `json:"state"`
	SocraticRound  int              `json:"socratic_round"`
	Timeline       []StudentTurn    `json:"timeline"`
	VoiceSegments  []speech.Segment `json:"voice_segments,omitempty"`
	VoiceAudio     string           `json:"voice_audio,omitempty"`
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
	var sessionID uuid.UUID
	var startedEvents []realtime.Event
	err = pgx.BeginFunc(request.Context(), handler.pool, func(tx pgx.Tx) error {
		var studentID, subjectID, knowledgePointID, questionID uuid.UUID
		var minutes int
		var mode string
		var subjectCode, knowledgePointName, prompt string
		var originalTaskID *uuid.UUID
		err := tx.QueryRow(request.Context(), `
SELECT p.student_id,b.subject_id,b.knowledge_point_id,b.minutes,b.mode,b.original_task_id,q.id,s.code,kp.name,q.prompt_public
FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id
JOIN students st ON st.id=p.student_id
JOIN subjects s ON s.id=b.subject_id
JOIN knowledge_points kp ON kp.id=b.knowledge_point_id
JOIN questions q ON q.knowledge_point_id=b.knowledge_point_id AND q.status='RELEASED'
WHERE b.id=$1 AND st.user_id=$2 AND p.plan_date=current_date AND p.status IN('PROPOSED','ACTIVE')
		ORDER BY q.difficulty,q.id LIMIT 1 FOR UPDATE OF p`, body.PlanBlockID, userID).Scan(&studentID, &subjectID, &knowledgePointID, &minutes, &mode, &originalTaskID, &questionID, &subjectCode, &knowledgePointName, &prompt)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(request.Context(), `SELECT id FROM learning_sessions WHERE student_id=$1 AND status='ACTIVE' ORDER BY started_at DESC LIMIT 1`, studentID).Scan(&sessionID); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
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
		if _, err := tx.Exec(request.Context(), `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,original_task_id,active_task_id,evidence_form)VALUES($1,$2,$3,$4,'ACTIVE',$5,'ASK',$6,$7,$8)`, sessionID, studentID, subjectID, questionID, minutes, originalTaskID, knowledgePointID, evidenceForm); err != nil {
			return err
		}
		if _, err := tx.Exec(request.Context(), `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message,reason_private)SELECT $1,$2,1,'TUTOR','ASK',prompt_public,'server selected released plan content' FROM questions WHERE id=$3`, uuid.New(), sessionID, questionID); err != nil {
			return err
		}
		now := time.Now()
		startedStudent, _ := json.Marshal(map[string]any{"action": "ASK", "subject": subjectCode, "knowledge_point": knowledgePointName})
		startedParent, _ := json.Marshal(map[string]any{"action": "ASK", "subject": subjectCode, "knowledge_point": knowledgePointName, "target_minutes": minutes})
		started := makeEvent(studentID, sessionID, 1, realtime.EventSessionStarted, startedStudent, startedParent, now)
		if err := insertEvent(request.Context(), tx, started); err != nil {
			return err
		}
		questionStudent, _ := json.Marshal(map[string]any{"action": "ASK", "prompt": prompt})
		questionParent, _ := json.Marshal(map[string]any{"action": "ASK", "prompt": prompt, "question_id": questionID})
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
	var sessionID uuid.UUID
	err = handler.pool.QueryRow(request.Context(), `SELECT ls.id FROM learning_sessions ls JOIN students st ON st.id=ls.student_id WHERE st.user_id=$1 AND ls.status='ACTIVE' ORDER BY ls.started_at DESC LIMIT 1`, userID).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	handler.writeStudentSession(writer, request, userID, sessionID)
}

func (handler *Handler) writeStudentSession(writer http.ResponseWriter, request *http.Request, userID, sessionID uuid.UUID) {
	var session StudentSession
	err := handler.pool.QueryRow(request.Context(), `
SELECT ls.id,s.code,s.name_zh,kp.name,q.difficulty,q.id,q.prompt_public,q.scene_public_json,q.input_schema_json,ls.started_at,ls.target_minutes,ls.current_state,ls.socratic_fail_count
FROM learning_sessions ls JOIN students st ON st.id=ls.student_id
JOIN subjects s ON s.id=ls.subject_id JOIN questions q ON q.id=ls.current_question_id AND q.status='RELEASED'
JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
WHERE ls.id=$1 AND st.user_id=$2`, sessionID, userID).Scan(&session.ID, &session.SubjectCode, &session.SubjectName, &session.KnowledgePoint, &session.Difficulty, &session.QuestionID, &session.Prompt, &session.Scene, &session.InputSchema, &session.StartedAt, &session.TargetMinutes, &session.State, &session.SocraticRound)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := handler.pool.Query(request.Context(), `SELECT sequence,actor,action,message,created_at FROM tutor_turns WHERE session_id=$1 ORDER BY sequence`, sessionID)
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var turn StudentTurn
		if err := rows.Scan(&turn.Sequence, &turn.Actor, &turn.Action, &turn.Message, &turn.At); err != nil {
			http.Error(writer, "session unavailable", http.StatusInternalServerError)
			return
		}
		session.Timeline = append(session.Timeline, turn)
	}
	if err := rows.Err(); err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	if session.State == "VOICE_EXPLAIN" {
		var outputID uuid.UUID
		err := handler.pool.QueryRow(request.Context(), `SELECT id,audio_data_url FROM speech_outputs WHERE session_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, sessionID).Scan(&outputID, &session.VoiceAudio)
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(writer, "voice explanation unavailable", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(writer, "session unavailable", http.StatusInternalServerError)
			return
		}
		segmentRows, err := handler.pool.Query(request.Context(), `SELECT id::text,text,start_ms,end_ms FROM speech_segments WHERE speech_output_id=$1 ORDER BY sequence`, outputID)
		if err != nil {
			http.Error(writer, "session unavailable", http.StatusInternalServerError)
			return
		}
		defer segmentRows.Close()
		for segmentRows.Next() {
			var segment speech.Segment
			if err := segmentRows.Scan(&segment.ID, &segment.Text, &segment.StartMS, &segment.EndMS); err != nil {
				http.Error(writer, "session unavailable", http.StatusInternalServerError)
				return
			}
			session.VoiceSegments = append(session.VoiceSegments, segment)
		}
		if err := segmentRows.Err(); err != nil {
			http.Error(writer, "session unavailable", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(writer, http.StatusOK, session)
}
