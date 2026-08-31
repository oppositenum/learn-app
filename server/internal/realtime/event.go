package realtime

import (
	"encoding/json"
	"time"
)

type EventType string

const (
	EventSessionStarted        EventType = "SESSION_STARTED"
	EventQuestionPresented     EventType = "QUESTION_PRESENTED"
	EventAnswerSubmitted       EventType = "ANSWER_SUBMITTED"
	EventAnswerAnalyzed        EventType = "ANSWER_ANALYZED"
	EventTutorActionSelected   EventType = "TUTOR_ACTION_SELECTED"
	EventAITurnStarted         EventType = "AI_TURN_STARTED"
	EventAITurnStream          EventType = "AI_TURN_STREAM"
	EventAITurnCompleted       EventType = "AI_TURN_COMPLETED"
	EventHintRequested         EventType = "HINT_REQUESTED"
	EventBacktrackStarted      EventType = "BACKTRACK_STARTED"
	EventBacktrackCompleted    EventType = "BACKTRACK_COMPLETED"
	EventVoiceExplainStarted   EventType = "VOICE_EXPLAIN_STARTED"
	EventVoiceExplainCompleted EventType = "VOICE_EXPLAIN_COMPLETED"
	EventMasteryUpdated        EventType = "MASTERY_UPDATED"
	EventRewardGranted         EventType = "REWARD_GRANTED"
	EventPlanModified          EventType = "PLAN_MODIFIED"
	EventSessionPaused         EventType = "SESSION_PAUSED"
	EventSessionCompleted      EventType = "SESSION_COMPLETED"
	EventParentIntervention    EventType = "PARENT_INTERVENTION"
)

type Event struct {
	EventID        string
	StudentID      string
	SessionID      string
	Sequence       int64
	Type           EventType
	CreatedAt      time.Time
	StudentPayload json.RawMessage
	ParentPayload  json.RawMessage
}

type StudentEventDTO struct {
	EventID   string          `json:"event_id"`
	SessionID string          `json:"session_id"`
	Sequence  int64           `json:"sequence"`
	Type      EventType       `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

type ParentEventDTO struct {
	EventID   string          `json:"event_id"`
	StudentID string          `json:"student_id"`
	SessionID string          `json:"session_id"`
	Sequence  int64           `json:"sequence"`
	Type      EventType       `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

func ProjectStudent(event Event) StudentEventDTO {
	return StudentEventDTO{
		EventID: event.EventID, SessionID: event.SessionID, Sequence: event.Sequence,
		Type: event.Type, CreatedAt: event.CreatedAt, Payload: event.StudentPayload,
	}
}

func ProjectParent(event Event) ParentEventDTO {
	return ParentEventDTO{
		EventID: event.EventID, StudentID: event.StudentID, SessionID: event.SessionID,
		Sequence: event.Sequence, Type: event.Type, CreatedAt: event.CreatedAt, Payload: event.ParentPayload,
	}
}
