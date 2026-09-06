package tutoraudit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const PolicyVersion = "tutor-output-audit-v1"

var (
	ErrReviewerUnavailable = errors.New("independent Tutor output reviewer is unavailable")
	ErrNonIndependent      = errors.New("Tutor output reviewer must be independent from generator")
	ErrInvalidProvenance   = errors.New("Tutor output reviewer returned invalid provenance")
	ErrOutputRejected      = errors.New("Tutor output was rejected by the answer-disclosure audit")
	ErrAuditRecord         = errors.New("Tutor output audit record could not be persisted")
	ErrInvalidReviewOutput = errors.New("invalid Tutor output review")
)

type ReviewResult string

const (
	ReviewPass   ReviewResult = "PASS"
	ReviewReject ReviewResult = "REJECT"
)

type Review struct {
	Result       ReviewResult `json:"result"`
	NoAnswerLeak bool         `json:"no_answer_leak"`
	ReasonCodes  []string     `json:"reason_codes"`
}

type ReviewEvidence struct {
	Provider  string
	Model     string
	RequestID string
}

type Reviewer interface {
	ReviewTutorOutput(ctx context.Context, request ai.TutorOutputAuditRequest) (Review, ReviewEvidence, error)
}

type AuditRecord struct {
	StudentID            string
	SessionID            string
	GenerationResponseID string
	ReviewerProvider     string
	ReviewerModel        string
	ReviewerRequestID    string
	PolicyVersion        string
	DeterministicResult  string
	ReviewerResult       string
	FinalResult          string
	ReasonCode           string
}

type Recorder interface {
	RecordTutorOutputAudit(ctx context.Context, record AuditRecord) error
}

type Service struct {
	generatorIdentity string
	reviewerIdentity  string
	reviewer          Reviewer
	recorder          Recorder
	checker           DeterministicChecker
}

func NewService(generatorIdentity, reviewerIdentity string, reviewer Reviewer, recorder Recorder) (*Service, error) {
	generatorIdentity = strings.TrimSpace(generatorIdentity)
	reviewerIdentity = strings.TrimSpace(reviewerIdentity)
	if generatorIdentity == "" || reviewerIdentity == "" || generatorIdentity == reviewerIdentity {
		return nil, ErrNonIndependent
	}
	if reviewer == nil {
		return nil, ErrReviewerUnavailable
	}
	if recorder == nil {
		return nil, errors.New("Tutor output audit recorder is required")
	}
	return &Service{
		generatorIdentity: generatorIdentity,
		reviewerIdentity:  reviewerIdentity,
		reviewer:          reviewer,
		recorder:          recorder,
	}, nil
}

func (service *Service) AuditTutorOutput(ctx context.Context, request ai.TutorOutputAuditRequest) error {
	if service == nil || service.reviewer == nil {
		return ErrReviewerUnavailable
	}
	deterministic := service.checker.Check(request)
	review, evidence, reviewErr := service.reviewer.ReviewTutorOutput(ctx, request)

	reviewerResult := string(review.Result)
	finalResult := "REJECT"
	reasonCode := deterministic.ReasonCode
	finalErr := ErrOutputRejected

	if reviewErr != nil {
		reviewerResult = reviewFailureResult(reviewErr)
		reasonCode = reviewerResult
		finalErr = fmt.Errorf("%w: %v", ErrReviewerUnavailable, reviewErr)
	} else if evidence.Provider+":"+evidence.Model != service.reviewerIdentity || strings.TrimSpace(evidence.RequestID) == "" {
		reviewerResult = "INVALID_PROVENANCE"
		reasonCode = reviewerResult
		finalErr = ErrInvalidProvenance
	} else if !deterministic.Passed {
		// Both gates have run. The deterministic rejection remains authoritative.
		finalErr = ErrOutputRejected
	} else if !reviewApproves(review) {
		reasonCode = "REVIEWER_REJECTED"
		finalErr = ErrOutputRejected
	} else {
		finalResult = "PASS"
		reasonCode = "APPROVED"
		finalErr = nil
	}

	record := AuditRecord{
		StudentID: request.StudentID, SessionID: request.SessionID,
		GenerationResponseID: request.GeneratorResponseID,
		ReviewerProvider:     evidence.Provider, ReviewerModel: evidence.Model, ReviewerRequestID: evidence.RequestID,
		PolicyVersion: PolicyVersion, DeterministicResult: deterministic.Result(),
		ReviewerResult: reviewerResult, FinalResult: finalResult, ReasonCode: reasonCode,
	}
	recordContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := service.recorder.RecordTutorOutputAudit(recordContext, record); err != nil {
		return fmt.Errorf("%w: %v", ErrAuditRecord, err)
	}
	return finalErr
}

func reviewApproves(review Review) bool {
	return review.Result == ReviewPass && review.NoAnswerLeak && len(review.ReasonCodes) == 1 && review.ReasonCodes[0] == "NONE"
}

func reviewFailureResult(err error) string {
	if errors.Is(err, ErrInvalidReviewOutput) {
		return "INVALID_SCHEMA"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "TIMEOUT"
	}
	return "ERROR"
}
