package tutoraudit

import (
	"context"
	"encoding/json"
	"errors"
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
		review:   Review{Result: ReviewPass, NoAnswerLeak: true, ReasonCodes: []string{"NONE"}},
		evidence: ReviewEvidence{Provider: "openai", Model: "reviewer-v1", RequestID: "review-request-1"},
	}
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
	if err := service.AuditTutorOutput(requestContext, validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrReviewerUnavailable) {
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

func TestServiceRejectsInvalidReviewerProvenance(t *testing.T) {
	reviewer := passingReviewer()
	reviewer.evidence.Model = "generator-v1"
	recorder := &recorderStub{}
	service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrInvalidProvenance) {
		t.Fatalf("invalid provenance did not fail closed: %v", err)
	}
	if len(recorder.records) != 1 || recorder.records[0].ReviewerResult != "INVALID_PROVENANCE" {
		t.Fatalf("invalid-provenance audit record=%+v", recorder.records)
	}
}

func TestServiceRejectsReviewerVerdictAndAuditPersistenceFailure(t *testing.T) {
	t.Run("review rejection", func(t *testing.T) {
		reviewer := passingReviewer()
		reviewer.review = Review{Result: ReviewReject, NoAnswerLeak: false, ReasonCodes: []string{"EQUIVALENT_ANSWER"}}
		recorder := &recorderStub{}
		service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, recorder)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrOutputRejected) {
			t.Fatalf("review rejection passed: %v", err)
		}
		if len(recorder.records) != 1 || recorder.records[0].ReasonCode != "REVIEWER_REJECTED" {
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
		if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrOutputRejected) {
			t.Fatalf("inconsistent passing verdict passed: %v", err)
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
