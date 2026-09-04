package parent

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrLiveSessionNotFound = errors.New("live session not found")

type LiveSessionDTO struct {
	SessionID      uuid.UUID       `json:"session_id"`
	StudentID      uuid.UUID       `json:"student_id"`
	Subject        string          `json:"subject"`
	KnowledgePoint string          `json:"knowledge_point"`
	StartedAt      time.Time       `json:"started_at"`
	Status         string          `json:"status"`
	CurrentState   string          `json:"current_state"`
	SocraticRound  int16           `json:"socratic_round"`
	Engagement     string          `json:"engagement"`
	QuestionPrompt string          `json:"question_prompt"`
	CorrectAnswer  json.RawMessage `json:"correct_answer"`
	FullSolution   string          `json:"full_solution"`
	StudentAnswer  string          `json:"student_answer"`
	AnswerCorrect  bool            `json:"answer_correct"`
	ErrorType      string          `json:"error_type"`
	Misconceptions json.RawMessage `json:"misconceptions"`
	TutorAction    string          `json:"tutor_action"`
	TutorReason    string          `json:"tutor_reason"`
	TargetMinutes  int16           `json:"target_minutes"`
	ActiveSeconds  int             `json:"active_seconds"`
	MasteryState   string          `json:"mastery_state"`
	MasteryScore   float64         `json:"mastery_score"`
	Timeline       []LiveTurnDTO   `json:"timeline"`
}

type LiveTurnDTO struct {
	Sequence int64     `json:"sequence"`
	Actor    string    `json:"actor"`
	Action   *string   `json:"action,omitempty"`
	Message  string    `json:"message"`
	Reason   string    `json:"reason,omitempty"`
	At       time.Time `json:"at"`
}

func (repository *Repository) LiveSession(ctx context.Context, studentID, sessionID uuid.UUID) (LiveSessionDTO, error) {
	var live LiveSessionDTO
	err := repository.pool.QueryRow(ctx, `
SELECT ls.id, ls.student_id, s.name_zh, kp.name, ls.started_at, ls.status,
       ls.current_state, ls.socratic_fail_count, ls.engagement_state,
       q.prompt_public, qa.correct_answer_json, qa.full_solution_private,
       COALESCE(sa.answer_text, ''), COALESCE(aa.answer_correct, false),
       COALESCE(aa.error_type, ''), COALESCE(aa.misconceptions_private_json, '[]'::jsonb),
		   COALESCE(tt.action, ''), COALESCE(tt.reason_private, ''),ls.target_minutes,
			   ls.accumulated_seconds + CASE WHEN ls.status='ACTIVE' THEN GREATEST(0,EXTRACT(EPOCH FROM ((CASE WHEN ls.last_activity_at>=CURRENT_TIMESTAMP-interval '90 seconds' THEN CURRENT_TIMESTAMP ELSE ls.last_activity_at END)-COALESCE(ls.last_resumed_at,ls.started_at)))::integer) ELSE 0 END,
	   COALESCE(ss.state,'UNKNOWN'),COALESCE(ss.score_internal,0)::float8
FROM learning_sessions ls
JOIN subjects s ON s.id = ls.subject_id
LEFT JOIN questions q ON q.id = ls.current_question_id
LEFT JOIN knowledge_points kp ON kp.id = q.knowledge_point_id
LEFT JOIN question_private_answers qa ON qa.question_id = q.id
LEFT JOIN LATERAL (
    SELECT * FROM student_answers WHERE session_id = ls.id ORDER BY submitted_at DESC LIMIT 1
) sa ON true
LEFT JOIN answer_analyses aa ON aa.student_answer_id = sa.id
LEFT JOIN LATERAL (
    SELECT * FROM tutor_turns WHERE session_id = ls.id AND actor = 'TUTOR' ORDER BY sequence DESC LIMIT 1
) tt ON true
LEFT JOIN student_skill_states ss ON ss.student_id=ls.student_id AND ss.knowledge_point_id=q.knowledge_point_id
WHERE ls.id = $1 AND ls.student_id = $2`, sessionID, studentID).Scan(
		&live.SessionID, &live.StudentID, &live.Subject, &live.KnowledgePoint,
		&live.StartedAt, &live.Status, &live.CurrentState, &live.SocraticRound,
		&live.Engagement, &live.QuestionPrompt, &live.CorrectAnswer, &live.FullSolution,
		&live.StudentAnswer, &live.AnswerCorrect, &live.ErrorType, &live.Misconceptions,
		&live.TutorAction, &live.TutorReason, &live.TargetMinutes, &live.ActiveSeconds, &live.MasteryState, &live.MasteryScore,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return LiveSessionDTO{}, ErrLiveSessionNotFound
	}
	if err != nil {
		return live, err
	}
	rows, err := repository.pool.Query(ctx, `SELECT sequence,actor,action,message,reason_private,created_at FROM tutor_turns WHERE session_id=$1 ORDER BY sequence`, sessionID)
	if err != nil {
		return live, err
	}
	defer rows.Close()
	for rows.Next() {
		var turn LiveTurnDTO
		if err := rows.Scan(&turn.Sequence, &turn.Actor, &turn.Action, &turn.Message, &turn.Reason, &turn.At); err != nil {
			return live, err
		}
		live.Timeline = append(live.Timeline, turn)
	}
	return live, rows.Err()
}
