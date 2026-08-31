package content

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/curriculum"
)

type Status string

const (
	StatusDraft              Status = "DRAFT"
	StatusAutomaticValidated Status = "AUTOMATIC_VALIDATED"
	StatusAIReviewed         Status = "AI_REVIEWED"
	StatusReleased           Status = "RELEASED"
	StatusQuarantined        Status = "QUARANTINED"
	StatusRejectedAutomatic  Status = "REJECTED_AUTOMATIC"
)

type QuestionPublic struct {
	ID               uuid.UUID             `json:"id"`
	KnowledgePointID uuid.UUID             `json:"knowledge_point_id"`
	Difficulty       curriculum.Difficulty `json:"difficulty"`
	QuestionType     string                `json:"question_type"`
	Prompt           string                `json:"prompt"`
	Scene            json.RawMessage       `json:"scene"`
	InputSchema      json.RawMessage       `json:"input_schema"`
	ContentVersion   string                `json:"content_version"`
}

type QuestionPrivateAnswer struct {
	QuestionID             uuid.UUID       `json:"question_id"`
	CorrectAnswer          json.RawMessage `json:"correct_answer"`
	FullSolution           string          `json:"full_solution"`
	TeacherReferenceAnswer string          `json:"teacher_reference_answer"`
	ScoringKey             json.RawMessage `json:"scoring_key"`
	Misconceptions         json.RawMessage `json:"misconceptions"`
	HintPolicy             json.RawMessage `json:"hint_policy"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type QuestionForTeaching struct {
	Public  QuestionPublic
	Private QuestionPrivateAnswer
}
