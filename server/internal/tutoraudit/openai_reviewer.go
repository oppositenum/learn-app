package tutoraudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const reviewSchemaFile = "tutor_output_review.schema.json"

type OpenAIReviewer struct {
	client   ai.StructuredClient
	provider string
	model    string
	schema   *jsonschema.Schema
	raw      json.RawMessage
}

func NewOpenAIReviewer(client ai.StructuredClient, provider, model string) (*OpenAIReviewer, error) {
	if client == nil {
		return nil, ErrReviewerUnavailable
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return nil, errors.New("Tutor output review provider and model are required")
	}
	raw, err := fs.ReadFile(aioutputs.Files, reviewSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("read Tutor output review schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode Tutor output review schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(reviewSchemaFile, document); err != nil {
		return nil, fmt.Errorf("register Tutor output review schema: %w", err)
	}
	compiled, err := compiler.Compile(reviewSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("compile Tutor output review schema: %w", err)
	}
	return &OpenAIReviewer{client: client, provider: provider, model: model, schema: compiled, raw: raw}, nil
}

type reviewInput struct {
	QuestionPrompt string                 `json:"question_prompt"`
	PrivateAnswer  privateReviewAnswer    `json:"private_answer"`
	Candidate      candidateReviewContent `json:"candidate"`
}

type privateReviewAnswer struct {
	CorrectAnswer          json.RawMessage `json:"correct_answer"`
	FullSolution           string          `json:"full_solution"`
	TeacherReferenceAnswer string          `json:"teacher_reference_answer"`
}

type candidateReviewContent struct {
	Action   string   `json:"action"`
	Message  string   `json:"message"`
	Segments []string `json:"segments"`
}

func (reviewer *OpenAIReviewer) ReviewTutorOutput(ctx context.Context, request ai.TutorOutputAuditRequest) (Review, ReviewEvidence, error) {
	requestID := uuid.NewString()
	evidence := ReviewEvidence{Provider: reviewer.provider, Model: reviewer.model, RequestID: requestID}
	segments := make([]string, 0, len(request.Candidate.Segments))
	for _, segment := range request.Candidate.Segments {
		segments = append(segments, segment.Text)
	}
	input, err := json.Marshal(reviewInput{
		QuestionPrompt: request.Question.Prompt,
		PrivateAnswer: privateReviewAnswer{
			CorrectAnswer: request.PrivateAnswer.CorrectAnswer, FullSolution: request.PrivateAnswer.FullSolution,
			TeacherReferenceAnswer: request.PrivateAnswer.TeacherReferenceAnswer,
		},
		Candidate: candidateReviewContent{Action: string(request.Candidate.Action), Message: request.Candidate.Message, Segments: segments},
	})
	if err != nil {
		return Review{}, evidence, fmt.Errorf("encode Tutor output review input: %w", err)
	}
	result, err := reviewer.client.GenerateStructured(ctx, ai.StructuredRequest{
		RequestID: requestID, StudentID: request.StudentID, SessionID: request.SessionID,
		Purpose:      ai.PurposeTutorOutputReview,
		Instructions: "Act only as an independent answer-disclosure reviewer for a child-facing Tutor response. Compare the candidate message and every segment with the original question and private answer. Return PASS only when the candidate does not state, derive, or equivalently disclose the original answer or full solution. Do not rewrite the response and do not perform unrelated safety classification.",
		Input:        input, SchemaName: reviewSchemaFile, Schema: reviewer.raw,
	})
	if err != nil {
		return Review{}, evidence, err
	}
	evidence.Provider = result.Usage.Provider
	evidence.Model = result.Usage.Model
	var untyped any
	if err := json.Unmarshal(result.OutputJSON, &untyped); err != nil {
		return Review{}, evidence, fmt.Errorf("%w: decode output: %v", ErrInvalidReviewOutput, err)
	}
	if err := reviewer.schema.Validate(untyped); err != nil {
		return Review{}, evidence, fmt.Errorf("%w: schema validation: %v", ErrInvalidReviewOutput, err)
	}
	var review Review
	if err := json.Unmarshal(result.OutputJSON, &review); err != nil {
		return Review{}, evidence, fmt.Errorf("%w: decode typed output: %v", ErrInvalidReviewOutput, err)
	}
	if err := validateReviewVerdict(review); err != nil {
		return Review{}, evidence, fmt.Errorf("%w: inconsistent verdict: %v", ErrInvalidReviewOutput, err)
	}
	return review, evidence, nil
}
