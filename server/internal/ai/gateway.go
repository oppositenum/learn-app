package ai

import (
	"context"
	"errors"
)

const (
	TutorOutputReviewUnavailableCode = "TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE"
	TutorOutputRephraseRequiredCode  = "TUTOR_OUTPUT_REPHRASE_REQUIRED"
)

var (
	ErrTutorOutputReviewUnavailable = errors.New("Tutor output review is temporarily unavailable")
	ErrTutorOutputRephraseRequired  = errors.New("Tutor output must be rephrased before publication")
)

type TeachingAgent interface {
	AnalyzeAnswer(ctx context.Context, request AnalyzeAnswerRequest) (AnalyzeAnswerResult, error)
	GenerateTurn(ctx context.Context, request GenerateTurnRequest) (TutorTurn, error)
	GenerateAnalogy(ctx context.Context, request AnalogyRequest) (TutorTurn, error)
	GenerateParallelExample(ctx context.Context, request ExampleRequest) (TutorTurn, error)
	GenerateExplanation(ctx context.Context, request ExplainRequest) (Explanation, error)
}

type Gateway struct {
	agent TeachingAgent
}

func NewGateway(agent TeachingAgent) *Gateway {
	return &Gateway{agent: agent}
}

func (gateway *Gateway) Agent() TeachingAgent {
	return gateway.agent
}
