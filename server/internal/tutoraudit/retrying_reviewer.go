package tutoraudit

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

const (
	reviewerMaxAttempts    = ai.TutorRetryMaxAttempts
	reviewerBaseDelay      = ai.TutorRetryBaseDelay
	reviewerMaxJitter      = ai.TutorRetryMaxJitter
	reviewerMaxRetryWait   = ai.TutorRetryMaxWait
	reviewerOverallTimeout = ai.TutorRetryOverallTimeout
)

var ErrReviewerRetriesExhausted = errors.New("Tutor output reviewer retries exhausted")

type retryPolicy struct {
	maxAttempts    int
	baseDelay      time.Duration
	maxJitter      time.Duration
	maxRetryWait   time.Duration
	overallTimeout time.Duration
}

type retryingReviewer struct {
	reviewer Reviewer
	policy   retryPolicy
	wait     func(context.Context, time.Duration) error
	jitter   func(time.Duration) time.Duration
}

type retryExhaustedError struct {
	attempts int
	err      error
}

func (err *retryExhaustedError) Error() string {
	return fmt.Sprintf("%s after %d attempt(s): %v", ErrReviewerRetriesExhausted, err.attempts, err.err)
}

func (err *retryExhaustedError) Unwrap() []error {
	return []error{ErrReviewerRetriesExhausted, err.err}
}

func NewRetryingReviewer(reviewer Reviewer) (Reviewer, error) {
	return newRetryingReviewer(reviewer, retryPolicy{
		maxAttempts: reviewerMaxAttempts, baseDelay: reviewerBaseDelay,
		maxJitter: reviewerMaxJitter, maxRetryWait: reviewerMaxRetryWait,
		overallTimeout: reviewerOverallTimeout,
	})
}

func newRetryingReviewer(reviewer Reviewer, policy retryPolicy) (*retryingReviewer, error) {
	if reviewer == nil {
		return nil, ErrReviewerUnavailable
	}
	if policy.maxAttempts < 1 || policy.baseDelay < 0 || policy.maxJitter < 0 || policy.maxRetryWait < 0 || policy.overallTimeout <= 0 {
		return nil, errors.New("invalid Tutor output reviewer retry policy")
	}
	return &retryingReviewer{
		reviewer: reviewer,
		policy:   policy,
		wait:     waitForRetry,
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(max.Nanoseconds() + 1))
		},
	}, nil
}

func (reviewer *retryingReviewer) ReviewTutorOutput(ctx context.Context, request ai.TutorOutputAuditRequest) (Review, ReviewEvidence, error) {
	ctx, cancel := context.WithTimeout(ctx, reviewer.policy.overallTimeout)
	defer cancel()

	var evidence ReviewEvidence
	var lastErr error
	var waited time.Duration
	var attempts int
	for attempt := 1; attempt <= reviewer.policy.maxAttempts; attempt++ {
		attempts = attempt
		review, attemptEvidence, err := reviewer.reviewer.ReviewTutorOutput(ctx, request)
		evidence = attemptEvidence
		if err == nil {
			return review, evidence, nil
		}
		lastErr = err
		if !ai.IsRetryableResponsesError(err) {
			return Review{}, evidence, err
		}
		if attempt == reviewer.policy.maxAttempts {
			break
		}

		delay := reviewer.policy.baseDelay << (attempt - 1)
		delay += reviewer.jitter(reviewer.policy.maxJitter)
		if retryAfter, ok := ai.ResponsesRetryAfter(err); ok && retryAfter > delay {
			delay = retryAfter
		}
		if delay > reviewer.policy.maxRetryWait-waited {
			break
		}
		if err := reviewer.wait(ctx, delay); err != nil {
			lastErr = err
			break
		}
		waited += delay
	}
	return Review{}, evidence, &retryExhaustedError{attempts: attempts, err: lastErr}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
