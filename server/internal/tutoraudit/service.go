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
	ErrReviewerUnavailable    = errors.New("independent Tutor output reviewer is unavailable")
	ErrNonIndependent         = errors.New("Tutor output reviewer must be independent from generator")
	ErrInvalidProvenance      = errors.New("Tutor output reviewer returned invalid provenance")
	ErrOutputRejected         = ai.ErrTutorOutputRephraseRequired
	ErrAuditRecord            = errors.New("Tutor output audit record could not be persisted")
	ErrInvalidReviewOutput    = errors.New("invalid Tutor output review")
	ErrInconsistentViolations = errors.New("inconsistent Tutor output review violations")
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
	Violations   []Violation  `json:"violations"`
}

type Violation struct {
	ViolationType string `json:"violation_type"`
	PayloadKind   string `json:"payload_kind"`
	SegmentIndex  int    `json:"segment_index"`
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
	StudentID             string
	SessionID             string
	GenerationResponseID  string
	ReviewerProvider      string
	ReviewerModel         string
	ReviewerRequestID     string
	PolicyVersion         string
	DeterministicResult   string
	ReviewerResult        string
	FinalResult           string
	ReasonCode            string
	Violations            []Violation
	CandidateSegmentCount int
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
	if reviewErr == nil {
		if err := validateReviewVerdict(review, len(request.Candidate.Segments)); err != nil {
			reviewErr = fmt.Errorf("%w: inconsistent verdict: %w", ErrInvalidReviewOutput, err)
		}
	}

	reviewerResult := string(review.Result)
	finalResult := "REJECT"
	reasonCode := deterministic.ReasonCode
	finalErr := ErrOutputRejected

	if reviewErr != nil {
		reviewerResult = reviewFailureResult(reviewErr)
		reasonCode = reviewFailureReason(reviewErr, reviewerResult)
		finalErr = fmt.Errorf("%w: %w: %w", ai.ErrTutorOutputReviewUnavailable, ErrReviewerUnavailable, reviewErr)
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

	var violations []Violation
	if reviewErr == nil && evidence.Provider+":"+evidence.Model == service.reviewerIdentity && strings.TrimSpace(evidence.RequestID) != "" {
		violations = make([]Violation, len(review.Violations))
		copy(violations, review.Violations)
	}
	record := AuditRecord{
		StudentID: request.StudentID, SessionID: request.SessionID,
		GenerationResponseID: request.GeneratorResponseID,
		ReviewerProvider:     evidence.Provider, ReviewerModel: evidence.Model, ReviewerRequestID: evidence.RequestID,
		PolicyVersion: PolicyVersion, DeterministicResult: deterministic.Result(),
		ReviewerResult: reviewerResult, FinalResult: finalResult, ReasonCode: reasonCode,
		Violations: violations, CandidateSegmentCount: len(request.Candidate.Segments),
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

func validateReviewVerdict(review Review, segmentCount int) error {
	if review.Result == ReviewPass {
		if !review.NoAnswerLeak || len(review.ReasonCodes) != 1 || review.ReasonCodes[0] != "NONE" {
			return errors.New("PASS must have no_answer_leak=true and reason_codes=[NONE]")
		}
		if len(review.Violations) != 0 {
			return fmt.Errorf("%w: PASS must have no violations", ErrInconsistentViolations)
		}
		return nil
	}
	if review.Result != ReviewReject {
		return fmt.Errorf("unsupported result %q", review.Result)
	}
	if review.NoAnswerLeak {
		return errors.New("REJECT must have no_answer_leak=false")
	}
	if len(review.ReasonCodes) == 0 || len(review.ReasonCodes) > 8 {
		return errors.New("REJECT must have between one and eight reason codes")
	}
	allowed := map[string]struct{}{
		"DIRECT_ANSWER": {}, "EQUIVALENT_ANSWER": {}, "FULL_SOLUTION": {}, "SEGMENT_ANSWER_LEAK": {},
	}
	seen := make(map[string]struct{}, len(review.ReasonCodes))
	for _, code := range review.ReasonCodes {
		if _, ok := allowed[code]; !ok {
			return fmt.Errorf("REJECT has unsupported reason code %q", code)
		}
		if _, duplicate := seen[code]; duplicate {
			return fmt.Errorf("REJECT has duplicate reason code %q", code)
		}
		seen[code] = struct{}{}
	}
	if len(review.Violations) == 0 {
		return fmt.Errorf("%w: REJECT must have at least one violation", ErrInconsistentViolations)
	}
	violationTypes, err := validateViolations(review.Violations, segmentCount)
	if err != nil {
		return err
	}
	if len(violationTypes) != len(seen) {
		return fmt.Errorf("%w: violation types do not match reason codes", ErrInconsistentViolations)
	}
	for code := range seen {
		if _, ok := violationTypes[code]; !ok {
			return fmt.Errorf("%w: violation types do not match reason codes", ErrInconsistentViolations)
		}
	}
	return nil
}

func validateViolations(violations []Violation, segmentCount int) (map[string]struct{}, error) {
	allowedTypes := map[string]struct{}{
		"DIRECT_ANSWER": {}, "EQUIVALENT_ANSWER": {}, "FULL_SOLUTION": {}, "SEGMENT_ANSWER_LEAK": {},
	}
	types := make(map[string]struct{}, len(violations))
	seen := make(map[Violation]struct{}, len(violations))
	for _, violation := range violations {
		if _, ok := allowedTypes[violation.ViolationType]; !ok {
			return nil, fmt.Errorf("%w: unsupported violation type", ErrInconsistentViolations)
		}
		switch violation.PayloadKind {
		case "MESSAGE":
			if violation.SegmentIndex != -1 {
				return nil, fmt.Errorf("%w: MESSAGE must use segment index -1", ErrInconsistentViolations)
			}
		case "SEGMENT":
			if violation.SegmentIndex < 0 || violation.SegmentIndex >= segmentCount {
				return nil, fmt.Errorf("%w: SEGMENT index is outside the candidate", ErrInconsistentViolations)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported payload kind", ErrInconsistentViolations)
		}
		if _, duplicate := seen[violation]; duplicate {
			return nil, fmt.Errorf("%w: duplicate violation", ErrInconsistentViolations)
		}
		seen[violation] = struct{}{}
		types[violation.ViolationType] = struct{}{}
	}
	return types, nil
}

func reviewFailureReason(err error, reviewerResult string) string {
	if errors.Is(err, ErrInconsistentViolations) {
		return "INCONSISTENT_VIOLATIONS"
	}
	if errors.Is(err, ErrReviewerRetriesExhausted) {
		return "RETRY_EXHAUSTED"
	}
	return reviewerResult
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
