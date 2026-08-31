package contentpipeline

import "encoding/json"

const CurrentSchemaVersion = "content-question-v1"

type Status string

const (
	Draft              Status = "DRAFT"
	AutomaticValidated Status = "AUTOMATIC_VALIDATED"
	AIReviewed         Status = "AI_REVIEWED"
	Released           Status = "RELEASED"
	Quarantined        Status = "QUARANTINED"
	RejectedAutomatic  Status = "REJECTED_AUTOMATIC"
)

type PrivateAnswer struct {
	Answer         string   `json:"answer"`
	NumericValue   *float64 `json:"numeric_value,omitempty"`
	Unit           string   `json:"unit,omitempty"`
	Solution       string   `json:"solution"`
	SolutionResult *float64 `json:"solution_result,omitempty"`
	Misconceptions []string `json:"misconceptions"`
}

type Choice struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Correct bool   `json:"correct"`
}

type Asset struct {
	QuestionID       string          `json:"question_id"`
	KnowledgePointID string          `json:"knowledge_point_id"`
	SubjectCode      string          `json:"subject_code"`
	Difficulty       string          `json:"difficulty"`
	QuestionType     string          `json:"question_type"`
	PromptPublic     string          `json:"prompt_public"`
	TeacherPrivate   PrivateAnswer   `json:"teacher_private"`
	Choices          []Choice        `json:"choices,omitempty"`
	InputSchema      json.RawMessage `json:"input_schema"`
	SourceID         string          `json:"source_id"`
	ContentVersion   string          `json:"content_version"`
	SchemaVersion    string          `json:"schema_version"`
	Status           Status          `json:"status"`
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}
type Validation struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

type ReviewResult string

const (
	ReviewPass        ReviewResult = "PASS"
	ReviewPassWithFix ReviewResult = "PASS_WITH_FIX"
	ReviewReject      ReviewResult = "REJECT"
	ReviewNeedsHuman  ReviewResult = "NEEDS_HUMAN"
)

type Review struct {
	Result         ReviewResult `json:"result"`
	AgeAppropriate bool         `json:"age_appropriate"`
	FactuallySound bool         `json:"factually_sound"`
	Unambiguous    bool         `json:"unambiguous"`
	NoAnswerLeak   bool         `json:"no_answer_leak"`
	SafeValues     bool         `json:"safe_values"`
	Findings       []string     `json:"findings"`
}
