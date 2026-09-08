package tutoraudit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

type reviewerStep struct {
	review   Review
	evidence ReviewEvidence
	err      error
}

type scriptedReviewer struct {
	steps []reviewerStep
	calls int
}

func (reviewer *scriptedReviewer) ReviewTutorOutput(context.Context, ai.TutorOutputAuditRequest) (Review, ReviewEvidence, error) {
	index := reviewer.calls
	reviewer.calls++
	if index >= len(reviewer.steps) {
		return Review{}, ReviewEvidence{}, errors.New("unexpected reviewer call")
	}
	return reviewer.steps[index].review, reviewer.steps[index].evidence, reviewer.steps[index].err
}

func retryableResponseError(status int, retryAfter time.Duration, retryAfterSet bool) error {
	return &ai.ResponsesAPIError{StatusCode: status, RetryAfter: retryAfter, RetryAfterSet: retryAfterSet}
}

func testRetryingReviewer(t *testing.T, reviewer Reviewer, policy retryPolicy, waits *[]time.Duration) *retryingReviewer {
	t.Helper()
	retrying, err := newRetryingReviewer(reviewer, policy)
	if err != nil {
		t.Fatal(err)
	}
	retrying.jitter = func(time.Duration) time.Duration { return 0 }
	retrying.wait = func(_ context.Context, delay time.Duration) error {
		*waits = append(*waits, delay)
		return nil
	}
	return retrying
}

func fastRetryPolicy() retryPolicy {
	return retryPolicy{
		maxAttempts: 3, baseDelay: 2 * time.Second, maxJitter: time.Second,
		maxRetryWait: 20 * time.Second, overallTimeout: time.Second,
	}
}

func TestReviewerRetryPolicyUsesSharedTutorParameters(t *testing.T) {
	if reviewerMaxAttempts != ai.TutorRetryMaxAttempts || reviewerBaseDelay != ai.TutorRetryBaseDelay ||
		reviewerMaxJitter != ai.TutorRetryMaxJitter || reviewerMaxRetryWait != ai.TutorRetryMaxWait ||
		reviewerOverallTimeout != ai.TutorRetryOverallTimeout {
		t.Fatalf("reviewer retry policy=%d/%s/%s/%s/%s", reviewerMaxAttempts, reviewerBaseDelay, reviewerMaxJitter, reviewerMaxRetryWait, reviewerOverallTimeout)
	}
	t.Logf("reviewer retry max_attempts=%d base_delay=%s max_jitter=%s max_wait=%s overall_timeout=%s",
		reviewerMaxAttempts, reviewerBaseDelay, reviewerMaxJitter, reviewerMaxRetryWait, reviewerOverallTimeout)
}

func TestRetryingReviewerRecoversFrom429And5xx(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			passing := passingReviewer()
			reviewer := &scriptedReviewer{steps: []reviewerStep{
				{err: retryableResponseError(status, 0, false)},
				{review: passing.review, evidence: passing.evidence},
			}}
			var waits []time.Duration
			retrying := testRetryingReviewer(t, reviewer, fastRetryPolicy(), &waits)
			review, evidence, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
			if err != nil {
				t.Fatal(err)
			}
			if reviewer.calls != 2 || review.Result != ReviewPass || evidence.RequestID != passing.evidence.RequestID {
				t.Fatalf("calls=%d review=%+v evidence=%+v", reviewer.calls, review, evidence)
			}
			if !slices.Equal(waits, []time.Duration{2 * time.Second}) {
				t.Fatalf("waits=%v", waits)
			}
		})
	}
}

func TestRetryingReviewerExhaustsAfterThreeTransientFailures(t *testing.T) {
	reviewer := &scriptedReviewer{steps: []reviewerStep{
		{err: retryableResponseError(http.StatusTooManyRequests, 0, false)},
		{err: retryableResponseError(http.StatusTooManyRequests, 0, false)},
		{err: retryableResponseError(http.StatusTooManyRequests, 0, false)},
	}}
	var waits []time.Duration
	retrying := testRetryingReviewer(t, reviewer, fastRetryPolicy(), &waits)
	_, _, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
	if !errors.Is(err, ErrReviewerRetriesExhausted) || !ai.IsRetryableResponsesError(err) {
		t.Fatalf("retry exhaustion error=%v", err)
	}
	if reviewer.calls != 3 || !slices.Equal(waits, []time.Duration{2 * time.Second, 4 * time.Second}) {
		t.Fatalf("calls=%d waits=%v", reviewer.calls, waits)
	}
}

func TestRetryingReviewerDoesNotRetryNonTransientOutcomes(t *testing.T) {
	passing := passingReviewer()
	for _, test := range []struct {
		name string
		step reviewerStep
	}{
		{name: "non-429 4xx", step: reviewerStep{err: retryableResponseError(http.StatusBadRequest, 0, false)}},
		{name: "invalid schema", step: reviewerStep{err: ErrInvalidReviewOutput}},
		{name: "review rejection", step: reviewerStep{
			review:   Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"DIRECT_ANSWER"}},
			evidence: passing.evidence,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reviewer := &scriptedReviewer{steps: []reviewerStep{test.step}}
			var waits []time.Duration
			retrying := testRetryingReviewer(t, reviewer, fastRetryPolicy(), &waits)
			review, _, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
			if reviewer.calls != 1 || len(waits) != 0 {
				t.Fatalf("calls=%d waits=%v", reviewer.calls, waits)
			}
			if test.name == "review rejection" {
				if err != nil || review.Result != ReviewReject {
					t.Fatalf("review=%+v err=%v", review, err)
				}
			} else if err == nil || errors.Is(err, ErrReviewerRetriesExhausted) {
				t.Fatalf("immediate failure=%v", err)
			}
		})
	}
}

func TestRetryingReviewerHonorsRetryAfterWithinWaitBudget(t *testing.T) {
	passing := passingReviewer()
	reviewer := &scriptedReviewer{steps: []reviewerStep{
		{err: retryableResponseError(http.StatusTooManyRequests, 7*time.Second, true)},
		{review: passing.review, evidence: passing.evidence},
	}}
	var waits []time.Duration
	retrying := testRetryingReviewer(t, reviewer, fastRetryPolicy(), &waits)
	if _, _, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(waits, []time.Duration{7 * time.Second}) {
		t.Fatalf("Retry-After waits=%v", waits)
	}
}

func TestRetryingReviewerWillNotExceedRetryWaitBudgetForRetryAfter(t *testing.T) {
	reviewer := &scriptedReviewer{steps: []reviewerStep{
		{err: retryableResponseError(http.StatusTooManyRequests, 21*time.Second, true)},
	}}
	var waits []time.Duration
	retrying := testRetryingReviewer(t, reviewer, fastRetryPolicy(), &waits)
	_, _, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
	if !errors.Is(err, ErrReviewerRetriesExhausted) || reviewer.calls != 1 || len(waits) != 0 {
		t.Fatalf("error=%v calls=%d waits=%v", err, reviewer.calls, waits)
	}
}

func TestRetryingReviewerOverallTimeoutBoundsRetryWait(t *testing.T) {
	reviewer := &scriptedReviewer{steps: []reviewerStep{
		{err: retryableResponseError(http.StatusTooManyRequests, 0, false)},
	}}
	retrying, err := newRetryingReviewer(reviewer, retryPolicy{
		maxAttempts: 3, baseDelay: time.Second, maxRetryWait: 20 * time.Second,
		overallTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	retrying.jitter = func(time.Duration) time.Duration { return 0 }
	started := time.Now()
	_, _, err = retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
	elapsed := time.Since(started)
	if !errors.Is(err, ErrReviewerRetriesExhausted) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error=%v", err)
	}
	if elapsed > 250*time.Millisecond || reviewer.calls != 1 {
		t.Fatalf("elapsed=%v calls=%d", elapsed, reviewer.calls)
	}
}

type reviewStructuredClient struct {
	requests []ai.StructuredRequest
	results  []ai.StructuredResult
	errors   []error
}

func (client *reviewStructuredClient) GenerateStructured(_ context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	index := len(client.requests)
	client.requests = append(client.requests, request)
	if index < len(client.errors) && client.errors[index] != nil {
		return ai.StructuredResult{}, client.errors[index]
	}
	return client.results[index], nil
}

func TestRetryingOpenAIReviewerUsesANewRequestIDPerAttempt(t *testing.T) {
	client := &reviewStructuredClient{
		errors: []error{retryableResponseError(http.StatusTooManyRequests, 0, false), nil},
		results: []ai.StructuredResult{{}, {
			OutputJSON: json.RawMessage(`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`),
			Usage:      ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"},
		}},
	}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	policy := fastRetryPolicy()
	policy.baseDelay = 0
	policy.maxJitter = 0
	retrying := testRetryingReviewer(t, reviewer, policy, &waits)
	_, evidence, err := retrying.ReviewTutorOutput(context.Background(), validAuditRequest("先找固定费用。"))
	if err != nil {
		t.Fatal(err)
	}
	if len(client.requests) != 2 || client.requests[0].RequestID == "" || client.requests[0].RequestID == client.requests[1].RequestID {
		t.Fatalf("request IDs=%q %q", client.requests[0].RequestID, client.requests[1].RequestID)
	}
	if evidence.RequestID != client.requests[1].RequestID {
		t.Fatalf("evidence request ID=%q want=%q", evidence.RequestID, client.requests[1].RequestID)
	}
}
