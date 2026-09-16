package tutoraudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type reviewerStub struct {
	review   Review
	evidence ReviewEvidence
	err      error
	calls    int
}

func (stub *reviewerStub) ReviewTutorOutput(context.Context, ai.TutorOutputAuditRequest) (Review, ReviewEvidence, error) {
	stub.calls++
	return stub.review, stub.evidence, stub.err
}

type recorderStub struct {
	records []AuditRecord
	err     error
	ctxErr  error
}

func (stub *recorderStub) RecordTutorOutputAudit(ctx context.Context, record AuditRecord) error {
	stub.records = append(stub.records, record)
	stub.ctxErr = ctx.Err()
	return stub.err
}

func validAuditRequest(message string) ai.TutorOutputAuditRequest {
	return ai.TutorOutputAuditRequest{
		StudentID: "00000000-0000-0000-0000-000000000001",
		SessionID: "00000000-0000-0000-0000-000000000002",
		Question:  content.QuestionPublic{Prompt: "三杯饮料共36元"},
		PrivateAnswer: content.QuestionPrivateAnswer{
			CorrectAnswer:          json.RawMessage(`{"value":"每杯10元"}`),
			FullSolution:           "用总价减去配送费后除以杯数。",
			TeacherReferenceAnswer: "每杯10元",
		},
		Candidate:           ai.TutorTurn{Message: message, Action: tutor.StateHint},
		GeneratorResponseID: "resp-generator",
	}
}

func passingReviewer() *reviewerStub {
	return &reviewerStub{
		review: Review{Result: ReviewPass, NoAnswerLeak: true, ReasonCodes: []string{"NONE"}, Violations: []Violation{}},
		evidence: ReviewEvidence{
			Provider: "openai", Model: "reviewer-v1",
			RequestID: "review-request-1", ExpectedRequestID: "review-request-1",
		},
	}
}

func providerResponseError(t *testing.T, status int, code string) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(status)
		_, _ = fmt.Fprintf(writer, `{"error":{"code":%q,"message":"raw_provider_response_canary"}}`, code)
	}))
	defer server.Close()
	client, err := ai.NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "reviewer-model")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateStructured(context.Background(), ai.StructuredRequest{
		SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil {
		t.Fatal("provider failure returned no error")
	}
	return err
}

func TestServiceRequiresIndependentConfiguredReviewer(t *testing.T) {
	recorder := &recorderStub{}
	for _, test := range []struct {
		name, generator, reviewer string
		implementation            Reviewer
		want                      error
	}{
		{name: "same identity", generator: "openai:model", reviewer: "openai:model", implementation: passingReviewer(), want: ErrNonIndependent},
		{name: "missing reviewer", generator: "openai:model", reviewer: "openai:reviewer", implementation: nil, want: ErrReviewerUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewService(test.generator, test.reviewer, test.implementation, recorder); !errors.Is(err, test.want) {
				t.Fatalf("constructor error=%v want=%v", err, test.want)
			}
		})
	}
}

func TestServiceRunsBothGatesAndRejectsSelfReportedSafeAnswer(t *testing.T) {
	reviewer := passingReviewer()
	recorder := &recorderStub{}
	service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuditTutorOutput(context.Background(), validAuditRequest("原题答案是每杯10元。")); !errors.Is(err, ErrOutputRejected) {
		t.Fatalf("answer leak was not rejected: %v", err)
	}
	if reviewer.calls != 1 || len(recorder.records) != 1 {
		t.Fatalf("both gates did not run: reviewer=%d records=%d", reviewer.calls, len(recorder.records))
	}
	record := recorder.records[0]
	if record.DeterministicResult != "REJECT" || record.ReviewerResult != "PASS" || record.FinalResult != "REJECT" || record.ReasonCode != "DETERMINISTIC_ANSWER_MATCH" {
		t.Fatalf("unexpected audit record: %+v", record)
	}
}

func TestServiceChecksEveryCandidateSegment(t *testing.T) {
	request := validAuditRequest("先想一想。")
	request.Candidate.Segments = []ai.SpeechSegment{{ID: "safe", Text: "先看条件"}, {ID: "leak", Text: "每杯10元"}}
	result := (DeterministicChecker{}).Check(request)
	if result.Passed || result.ReasonCode != "DETERMINISTIC_ANSWER_MATCH" {
		t.Fatalf("segment answer leak passed: %+v", result)
	}
}

func TestServiceFailsClosedOnReviewerTimeoutAndKeepsMinimalAudit(t *testing.T) {
	reviewer := passingReviewer()
	reviewer.err = context.DeadlineExceeded
	recorder := &recorderStub{}
	service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.AuditTutorOutput(requestContext, validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrReviewerUnavailable) || !errors.Is(err, ai.ErrTutorOutputReviewUnavailable) {
		t.Fatalf("reviewer timeout did not fail closed: %v", err)
	}
	if len(recorder.records) != 1 || recorder.ctxErr != nil || recorder.records[0].ReviewerResult != "TIMEOUT" || recorder.records[0].FinalResult != "REJECT" {
		t.Fatalf("timeout audit record=%+v", recorder.records)
	}
	encoded, _ := json.Marshal(recorder.records[0])
	for _, forbidden := range []string{"每杯10元", "先找出固定费用", "student_answer", "correct_answer"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("minimal audit record contains private or candidate body %q: %s", forbidden, encoded)
		}
	}
}

func TestServiceDistinguishesRetryExhaustionFromImmediateReviewerFailure(t *testing.T) {
	for _, test := range []struct {
		name           string
		reviewErr      error
		reviewerResult string
		reasonCode     string
	}{
		{
			name: "retry exhausted",
			reviewErr: &retryExhaustedError{
				attempts: 3,
				err:      &ai.ResponsesAPIError{StatusCode: 429},
			},
			reviewerResult: "ERROR",
			reasonCode:     "RETRY_EXHAUSTED",
		},
		{name: "immediate failure", reviewErr: errors.New("price unavailable"), reviewerResult: "ERROR", reasonCode: "ERROR"},
		{
			name:           "inconsistent verdict",
			reviewErr:      fmt.Errorf("%w: inconsistent verdict", ErrInvalidReviewOutput),
			reviewerResult: "INVALID_SCHEMA",
			reasonCode:     "INVALID_SCHEMA",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reviewer := passingReviewer()
			reviewer.err = test.reviewErr
			recorder := &recorderStub{}
			service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
			if err != nil {
				t.Fatal(err)
			}
			err = service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
			if !errors.Is(err, ai.ErrTutorOutputReviewUnavailable) || !errors.Is(err, ErrReviewerUnavailable) {
				t.Fatalf("failure was not mapped to reviewer unavailability: %v", err)
			}
			if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != test.reviewerResult || recorder.records[0].FinalResult != "REJECT" || recorder.records[0].ReasonCode != test.reasonCode {
				t.Fatalf("audit record=%+v", recorder.records)
			}
		})
	}
}

func TestServicePreservesBoundedReviewerTransportDiagnostics(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		code           string
		wrap           func(error) error
		wantCategory   string
		wantResult     string
		wantReasonCode string
	}{
		{
			name: "invalid schema", status: http.StatusBadRequest, code: "invalid_json_schema",
			wrap:         func(err error) error { return err },
			wantCategory: ai.TutorReviewFailureInvalidSchema, wantResult: "INVALID_SCHEMA", wantReasonCode: "INVALID_SCHEMA",
		},
		{
			name: "retry exhausted server error", status: http.StatusInternalServerError, code: "server_error",
			wrap:         func(err error) error { return &retryExhaustedError{attempts: 3, err: err} },
			wantCategory: ai.TutorReviewFailureRetryExhausted, wantResult: "ERROR", wantReasonCode: "RETRY_EXHAUSTED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reviewer := passingReviewer()
			reviewer.err = test.wrap(providerResponseError(t, test.status, test.code))
			recorder := &recorderStub{}
			service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
			if err != nil {
				t.Fatal(err)
			}
			err = service.AuditTutorOutput(context.Background(), validAuditRequest("safe_candidate_canary"))
			if !errors.Is(err, ai.ErrTutorOutputReviewUnavailable) || !errors.Is(err, ErrReviewerUnavailable) {
				t.Fatalf("review failure did not remain fail closed")
			}
			details, ok := ai.TutorOutputReviewFailureDetails(err)
			if !ok || details.Category != test.wantCategory || details.HTTPStatus != test.status || details.ProviderCode != test.code || details.RequestID != reviewer.evidence.RequestID {
				t.Fatalf("details=%+v present=%v", details, ok)
			}
			if strings.Contains(err.Error(), "raw_provider_response_canary") || strings.Contains(err.Error(), "safe_candidate_canary") {
				t.Fatal("public review failure exposed provider or candidate content")
			}
			if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != test.wantResult || recorder.records[0].ReasonCode != test.wantReasonCode {
				t.Fatalf("audit record=%+v", recorder.records)
			}
		})
	}
}

func TestServiceRejectsInvalidReviewerProvenance(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ReviewEvidence)
	}{
		{name: "provider mismatch", mutate: func(evidence *ReviewEvidence) { evidence.Provider = "generator-provider" }},
		{name: "model mismatch", mutate: func(evidence *ReviewEvidence) { evidence.Model = "generator-v1" }},
		{name: "request ID mismatch", mutate: func(evidence *ReviewEvidence) { evidence.RequestID = "different-review-request" }},
		{name: "missing request ID", mutate: func(evidence *ReviewEvidence) { evidence.RequestID = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reviewer := passingReviewer()
			test.mutate(&reviewer.evidence)
			recorder := &recorderStub{}
			service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrInvalidProvenance) {
				t.Fatalf("invalid provenance did not fail closed: %v", err)
			}
			if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != "INVALID_PROVENANCE" || recorder.records[0].Violations != nil {
				t.Fatalf("invalid-provenance audit record=%+v", recorder.records)
			}
		})
	}
}

func TestServiceRejectsReviewerVerdictAndAuditPersistenceFailure(t *testing.T) {
	t.Run("review rejection", func(t *testing.T) {
		reviewer := passingReviewer()
		reviewer.review = Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"EQUIVALENT_ANSWER"}, Violations: []Violation{{ViolationType: "EQUIVALENT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: -1}}}
		recorder := &recorderStub{}
		service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrOutputRejected) {
			t.Fatalf("review rejection passed: %v", err)
		}
		if len(recorder.records) != 1 || recorder.records[0].ReasonCode != "REVIEWER_REJECTED" || len(recorder.records[0].Violations) != 1 {
			t.Fatalf("review rejection record=%+v", recorder.records)
		}
	})

	t.Run("inconsistent passing verdict", func(t *testing.T) {
		reviewer := passingReviewer()
		reviewer.review.ReasonCodes = []string{"DIRECT_ANSWER"}
		recorder := &recorderStub{}
		service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ai.ErrTutorOutputReviewUnavailable) {
			t.Fatalf("inconsistent passing verdict passed: %v", err)
		}
		if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != "INVALID_SCHEMA" || recorder.records[0].ReasonCode != "INVALID_SCHEMA" {
			t.Fatalf("inconsistent verdict audit=%+v", recorder.records)
		}
	})

	t.Run("audit persistence failure", func(t *testing.T) {
		recorder := &recorderStub{err: errors.New("database unavailable")}
		service, err := NewService("openai:generator-v1", "openai:reviewer-v1", passingReviewer(), recorder)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrAuditRecord) {
			t.Fatalf("audit persistence failure did not fail closed: %v", err)
		}
	})
}

func TestServiceRecordsEveryViolationInconsistencyAsFailClosed(t *testing.T) {
	validViolation := Violation{ViolationType: "DIRECT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: -1}
	tests := []struct {
		name        string
		review      Review
		withSegment bool
	}{
		{name: "PASS with violation", review: Review{Result: ReviewPass, NoAnswerLeak: true, ReasonCodes: []string{"NONE"}, Violations: []Violation{validViolation}}},
		{name: "REJECT without violation", review: Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"DIRECT_ANSWER"}, Violations: []Violation{}}},
		{name: "violation type mismatch", review: Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"DIRECT_ANSWER"}, Violations: []Violation{{ViolationType: "FULL_SOLUTION", PayloadKind: "MESSAGE", SegmentIndex: -1}}}},
		{name: "MESSAGE with nonnegative segment index", review: Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"DIRECT_ANSWER"}, Violations: []Violation{{ViolationType: "DIRECT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: 0}}}},
		{name: "SEGMENT index outside candidate", review: Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"SEGMENT_ANSWER_LEAK"}, Violations: []Violation{{ViolationType: "SEGMENT_ANSWER_LEAK", PayloadKind: "SEGMENT", SegmentIndex: 1}}}, withSegment: true},
		{name: "duplicate violation", review: Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"DIRECT_ANSWER"}, Violations: []Violation{validViolation, validViolation}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reviewer := passingReviewer()
			reviewer.review = test.review
			recorder := &recorderStub{}
			service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
			if err != nil {
				t.Fatal(err)
			}
			request := validAuditRequest("candidate_canary")
			if test.withSegment {
				request.Candidate.Segments = []ai.SpeechSegment{{ID: "s1", Text: "segment_canary"}}
			}
			err = service.AuditTutorOutput(context.Background(), request)
			if !errors.Is(err, ai.ErrTutorOutputReviewUnavailable) || !errors.Is(err, ErrInconsistentViolations) {
				t.Fatalf("inconsistent violations did not fail closed: %v", err)
			}
			if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != "INVALID_SCHEMA" || recorder.records[0].FinalResult != "REJECT" || recorder.records[0].ReasonCode != "INCONSISTENT_VIOLATIONS" || recorder.records[0].Violations != nil {
				t.Fatalf("inconsistent violation audit=%+v", recorder.records)
			}
		})
	}
}
