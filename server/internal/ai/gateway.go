package ai

import (
	"context"
	"errors"
)

const TutorOutputReviewUnavailableCode = "TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE"

var ErrTutorOutputReviewUnavailable = errors.New("Tutor output review is temporarily unavailable")

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
