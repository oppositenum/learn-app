package classroom

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

func TestLogTutorReviewUnavailableUsesOnlyBoundedStructuredDiagnostics(t *testing.T) {
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	log.SetFlags(0)
	log.SetPrefix("")

	unsafeCause := errors.New(strings.Join([]string{
		"raw_provider_response_canary",
		"candidate_body_canary",
		"private_answer_canary",
		"student_input_canary",
	}, " "))
	tests := []struct {
		name         string
		status       int
		category     string
		providerCode string
		requestID    string
		want         string
	}{
		{
			name: "400 invalid schema", status: 400, category: ai.TutorReviewFailureInvalidSchema,
			providerCode: "invalid_json_schema", requestID: "review-request-400",
			want: "classroom tutor_review_unavailable operation=support failure_category=INVALID_SCHEMA http_status=400 provider_error_code=invalid_json_schema reviewer_request_id=review-request-400\n",
		},
		{
			name: "500 retry exhausted", status: 500, category: ai.TutorReviewFailureRetryExhausted,
			providerCode: "server_error", requestID: "review-request-500",
			want: "classroom tutor_review_unavailable operation=support failure_category=RETRY_EXHAUSTED http_status=500 provider_error_code=server_error reviewer_request_id=review-request-500\n",
		},
		{
			name: "overlong provider code", status: 500, category: ai.TutorReviewFailureTransport,
			providerCode: strings.Repeat("x", 65), requestID: strings.Repeat("r", 65),
			want: "classroom tutor_review_unavailable operation=support failure_category=TRANSPORT http_status=500 provider_error_code=UNAVAILABLE reviewer_request_id=UNAVAILABLE\n",
		},
		{
			name: "malformed provider code", status: 500, category: ai.TutorReviewFailureTransport,
			providerCode: "server error\nraw", requestID: "review request\nmalformed",
			want: "classroom tutor_review_unavailable operation=support failure_category=TRANSPORT http_status=500 provider_error_code=UNAVAILABLE reviewer_request_id=UNAVAILABLE\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			log.SetOutput(&output)
			err := ai.NewTutorOutputReviewFailure(test.category, test.status, test.providerCode, test.requestID, unsafeCause)
			logTutorReviewUnavailable("support", err)
			if output.String() != test.want {
				t.Fatalf("structured log=%q want=%q", output.String(), test.want)
			}
			t.Log(strings.TrimSpace(output.String()))
			for _, forbidden := range []string{
				"raw_provider_response_canary",
				"candidate_body_canary",
				"private_answer_canary",
				"student_input_canary",
			} {
				if strings.Contains(output.String(), forbidden) {
					t.Fatalf("structured log exposed forbidden content marker")
				}
			}
		})
	}
}

func TestLogTutorGenerationBusyUsesOnlyBoundedStructuredDiagnostics(t *testing.T) {
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	log.SetFlags(0)
	log.SetPrefix("")

	unsafeCause := errors.New(strings.Join([]string{
		"raw_provider_response_canary",
		"candidate_body_canary",
		"private_answer_canary",
		"student_input_canary",
	}, " "))
	var output bytes.Buffer
	log.SetOutput(&output)
	err := ai.NewTutorGenerationBusyFailure(
		ai.TutorReviewFailureRetryExhausted,
		429,
		"gateway_concurrency_limit",
		"generation-request-429",
		unsafeCause,
	)
	logTutorGenerationBusy("support", err)

	// rejected_paths stays "none" here: this failure came from provider
	// congestion, not from output local validation refused, and the unsafe
	// cause above must not reach the log through the new field either.
	want := "classroom tutor_generation_busy operation=support failure_category=RETRY_EXHAUSTED http_status=429 provider_error_code=gateway_concurrency_limit generation_request_id=generation-request-429 rejected_paths=none\n"
	if output.String() != want {
		t.Fatalf("structured log=%q want=%q", output.String(), want)
	}
	t.Log(strings.TrimSpace(output.String()))
	for _, forbidden := range []string{
		"raw_provider_response_canary",
		"candidate_body_canary",
		"private_answer_canary",
		"student_input_canary",
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("structured log exposed forbidden content marker")
		}
	}
}
