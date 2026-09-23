package ai

import (
	"context"
	"encoding/json"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type Purpose string

const (
	PurposeAnswerAnalysis    Purpose = "ANSWER_ANALYSIS"
	PurposeSocraticTurn      Purpose = "SOCRATIC_TURN"
	PurposeExplanation       Purpose = "EXPLANATION"
	PurposeTutorOutputReview Purpose = "TUTOR_OUTPUT_REVIEW"
	PurposeContentGeneration Purpose = "CONTENT_GENERATION"
	PurposeContentReview     Purpose = "CONTENT_REVIEW"
	PurposePlanSummary       Purpose = "PLAN_SUMMARY"
	PurposeParentGuide       Purpose = "PARENT_GUIDE"
	PurposeSTTTranscription  Purpose = "STT_TRANSCRIPTION"
	PurposeTTSExplanation    Purpose = "TTS_EXPLANATION"
)

type AnalyzeAnswerRequest struct {
	StudentID     string                      `json:"-"`
	SessionID     string                      `json:"session_id"`
	Question      content.QuestionForTeaching `json:"question"`
	StudentAnswer string                      `json:"student_answer"`
	PriorTurns    []TutorTurn                 `json:"prior_turns"`
}

type AbilitySignal struct {
	AbilityID string `json:"ability_id"`
	Signal    string `json:"signal"`
}

type AnalyzeAnswerResult struct {
	AnswerCorrect            bool            `json:"answer_correct"`
	ReasoningQuality         string          `json:"reasoning_quality"`
	Confidence               float64         `json:"confidence"`
	ErrorType                string          `json:"error_type"`
	Misconceptions           []string        `json:"misconceptions"`
	CoreAbilitySignals       []AbilitySignal `json:"core_ability_signals"`
	EmotionSignal            string          `json:"emotion_signal"`
	Engagement               string          `json:"engagement"`
	RecommendedAction        tutor.State     `json:"recommended_action"`
	SafeToIncreaseDifficulty bool            `json:"safe_to_increase_difficulty"`
	// WeaknessLayer names the layer a wrong answer is stuck at, L1 to L6. A
	// correct answer carries NONE. ValidateWeaknessLayer enforces both.
	WeaknessLayer string `json:"weakness_layer"`
}

type GenerateTurnRequest struct {
	StudentID string                 `json:"-"`
	SessionID string                 `json:"session_id"`
	Question  content.QuestionPublic `json:"question"`
	// Teaching carries the subject and knowledge point. It is a sibling of
	// Question rather than a field inside it because QuestionPublic is
	// serialized into the student response.
	Teaching           content.TeachingContext       `json:"teaching_context"`
	AuditPrivateAnswer content.QuestionPrivateAnswer `json:"-"`
	StudentAnswer      string                        `json:"student_answer"`
	TutorDecision      tutor.Decision                `json:"tutor_decision"`
	PriorTurns         []TutorTurn                   `json:"prior_turns"`
	PreviousResponseID string                        `json:"previous_response_id,omitempty"`
	// WeaknessLayer is set only for the turns that keep probing the same
	// question. The generator reads it to pick that layer's teaching move.
	WeaknessLayer string `json:"weakness_layer,omitempty"`
}

type AnalogyRequest GenerateTurnRequest
type ExampleRequest GenerateTurnRequest
type ExplainRequest GenerateTurnRequest

type SpeechSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type TutorTurn struct {
	Message    string          `json:"message"`
	Action     tutor.State     `json:"action"`
	Segments   []SpeechSegment `json:"segments"`
	ResponseID string          `json:"-"`
}

type Explanation = TutorTurn

type TutorOutputAuditRequest struct {
	StudentID           string
	SessionID           string
	Question            content.QuestionPublic
	Teaching            content.TeachingContext
	PrivateAnswer       content.QuestionPrivateAnswer
	Candidate           TutorTurn
	GeneratorResponseID string
}

type TutorOutputAuditor interface {
	AuditTutorOutput(ctx context.Context, request TutorOutputAuditRequest) error
}

type StructuredRequest struct {
	RequestID          string
	StudentID          string
	SessionID          string
	Purpose            Purpose
	Instructions       string
	Input              json.RawMessage
	SchemaName         string
	Schema             json.RawMessage
	PreviousResponseID string
}

type StructuredResult struct {
	RequestID  string
	ResponseID string
	// ReportedModel is the raw model string the provider echoed back. Price
	// preflight, usage accounting, and request outcomes all use the configured
	// (provider, model) pair so they cannot diverge, so this value exists only
	// to keep a provider that serves a different model than the configured one
	// detectable by callers that verify provenance.
	ReportedModel string
	OutputJSON    json.RawMessage
	Usage         ModelUsage
}

type ModelUsage struct {
	Provider           string
	Model              string
	InputTokens        int64
	CachedInputTokens  int64
	OutputTokens       int64
	AudioInputSeconds  string
	AudioOutputSeconds string
}

type UsageRecord struct {
	RequestID string
	StudentID string
	SessionID string
	Purpose   Purpose
	Usage     ModelUsage
	Latency   time.Duration
	CreatedAt time.Time
}

type UsageRecorder interface {
	RecordAIUsage(ctx context.Context, record UsageRecord) error
}

type RequestOutcome string

const (
	RequestSucceeded       RequestOutcome = "SUCCEEDED"
	RequestProviderError   RequestOutcome = "PROVIDER_ERROR"
	RequestTransportError  RequestOutcome = "TRANSPORT_ERROR"
	RequestInvalidResponse RequestOutcome = "INVALID_RESPONSE"
	RequestAccountingError RequestOutcome = "ACCOUNTING_ERROR"
)

type RequestOutcomeRecord struct {
	RequestID  string
	StudentID  string
	SessionID  string
	Provider   string
	Model      string
	Purpose    Purpose
	Outcome    RequestOutcome
	HTTPStatus *int
	Latency    time.Duration
	CreatedAt  time.Time
}

type RequestOutcomeRecorder interface {
	RecordAIRequestOutcome(ctx context.Context, record RequestOutcomeRecord) error
}

type PriceGuard interface {
	EnsurePrice(ctx context.Context, provider, model string, at time.Time) error
}
