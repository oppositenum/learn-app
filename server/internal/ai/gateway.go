package ai

import (
	"context"
	"errors"
)

const (
	TutorGenerationBusyCode                  = "TUTOR_GENERATION_BUSY"
	TutorOutputReviewUnavailableCode         = "TUTOR_REVIEW_TEMPORARILY_UNAVAILABLE"
	TutorOutputRephraseRequiredCode          = "TUTOR_OUTPUT_REPHRASE_REQUIRED"
	TutorReviewFailureTimeout                = "TIMEOUT"
	TutorReviewFailureRetryExhausted         = "RETRY_EXHAUSTED"
	TutorReviewFailureInvalidSchema          = "INVALID_SCHEMA"
	TutorReviewFailureInconsistentViolations = "INCONSISTENT_VIOLATIONS"
	TutorReviewFailureTransport              = "TRANSPORT"
	TutorReviewFailureOther                  = "OTHER"
	TutorReviewDiagnosticUnavailable         = "UNAVAILABLE"
)

var (
	ErrTutorGenerationBusy          = errors.New("Tutor generation is temporarily busy")
	ErrTutorOutputReviewUnavailable = errors.New("Tutor output review is temporarily unavailable")
	ErrTutorOutputRephraseRequired  = errors.New("Tutor output must be rephrased before publication")
)

type TutorGenerationFailureDetails struct {
	Category     string
	HTTPStatus   int
	ProviderCode string
	RequestID    string
}

type tutorGenerationBusyFailure struct {
	details TutorGenerationFailureDetails
	cause   error
}

func (failure *tutorGenerationBusyFailure) Error() string {
	return ErrTutorGenerationBusy.Error()
}

func (failure *tutorGenerationBusyFailure) Unwrap() []error {
	return []error{ErrTutorGenerationBusy, failure.cause}
}

func NewTutorGenerationBusyFailure(category string, httpStatus int, providerCode, requestID string, cause error) error {
	if !validTutorReviewFailureCategory(category) {
		category = TutorReviewFailureOther
	}
	if httpStatus < 100 || httpStatus > 599 {
		httpStatus = 0
	}
	return &tutorGenerationBusyFailure{
		details: TutorGenerationFailureDetails{
			Category:     category,
			HTTPStatus:   httpStatus,
			ProviderCode: sanitizeResponsesProviderErrorCode(providerCode),
			RequestID:    sanitizeDiagnosticIdentifier(requestID),
		},
		cause: cause,
	}
}

func TutorGenerationBusyFailureDetails(err error) (TutorGenerationFailureDetails, bool) {
	var failure *tutorGenerationBusyFailure
	if !errors.As(err, &failure) {
		return TutorGenerationFailureDetails{}, false
	}
	return failure.details, true
}

type TutorReviewFailureDetails struct {
	Category     string
	HTTPStatus   int
	ProviderCode string
	RequestID    string
}

type tutorOutputReviewFailure struct {
	details TutorReviewFailureDetails
	cause   error
}

func (failure *tutorOutputReviewFailure) Error() string {
	return ErrTutorOutputReviewUnavailable.Error()
}

func (failure *tutorOutputReviewFailure) Unwrap() []error {
	return []error{ErrTutorOutputReviewUnavailable, failure.cause}
}

func NewTutorOutputReviewFailure(category string, httpStatus int, providerCode, requestID string, cause error) error {
	if !validTutorReviewFailureCategory(category) {
		category = TutorReviewFailureOther
	}
	if httpStatus < 100 || httpStatus > 599 {
		httpStatus = 0
	}
	providerCode = sanitizeResponsesProviderErrorCode(providerCode)
	requestID = sanitizeDiagnosticIdentifier(requestID)
	return &tutorOutputReviewFailure{
		details: TutorReviewFailureDetails{
			Category: category, HTTPStatus: httpStatus,
			ProviderCode: providerCode, RequestID: requestID,
		},
		cause: cause,
	}
}

func TutorOutputReviewFailureDetails(err error) (TutorReviewFailureDetails, bool) {
	var failure *tutorOutputReviewFailure
	if !errors.As(err, &failure) {
		return TutorReviewFailureDetails{}, false
	}
	return failure.details, true
}

func validTutorReviewFailureCategory(category string) bool {
	switch category {
	case TutorReviewFailureTimeout, TutorReviewFailureRetryExhausted,
		TutorReviewFailureInvalidSchema, TutorReviewFailureInconsistentViolations,
		TutorReviewFailureTransport, TutorReviewFailureOther:
		return true
	default:
		return false
	}
}

func sanitizeDiagnosticIdentifier(value string) string {
	if len(value) == 0 || len(value) > 64 {
		return TutorReviewDiagnosticUnavailable
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return TutorReviewDiagnosticUnavailable
	}
	return value
}

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
