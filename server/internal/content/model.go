package content

import (
	"encoding/json"
	"strings"
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

// TeachingContext tells the Tutor which subject and knowledge point it is
// teaching. Without it the model receives a bare knowledge point UUID and has
// to guess from the prompt text, which it gets wrong in both directions: an
// English reading task whose sentence mentions two clock times is answered with
// a Chinese arithmetic analogy, and a linear-equation task is coached only to
// pick an unknown because nothing states that forming the equation is the goal.
//
// It deliberately lives outside QuestionPublic. QuestionPublic is serialized
// straight into the student question response, so teaching metadata added there
// would leak to the student client.
type TeachingContext struct {
	SubjectCode        string `json:"subject_code"`
	SubjectName        string `json:"subject_name"`
	KnowledgePointCode string `json:"knowledge_point_code"`
	KnowledgePointName string `json:"knowledge_point_name"`
}

// Complete reports whether every field is present. Teaching requests fail
// closed on an incomplete context rather than falling back to the old
// UUID-only request, because that fallback is the defect itself.
func (context TeachingContext) Complete() bool {
	return strings.TrimSpace(context.SubjectCode) != "" &&
		strings.TrimSpace(context.SubjectName) != "" &&
		strings.TrimSpace(context.KnowledgePointCode) != "" &&
		strings.TrimSpace(context.KnowledgePointName) != ""
}

type QuestionForTeaching struct {
	Public   QuestionPublic
	Private  QuestionPrivateAnswer
	Teaching TeachingContext `json:"teaching_context"`
}
