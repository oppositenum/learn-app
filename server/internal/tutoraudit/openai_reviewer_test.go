package tutoraudit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

type structuredClientStub struct {
	request ai.StructuredRequest
	result  ai.StructuredResult
	err     error
}

func (stub *structuredClientStub) GenerateStructured(_ context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	stub.request = request
	result := stub.result
	if result.RequestID == "" {
		result.RequestID = request.RequestID
	}
	return result, stub.err
}

func TestOpenAIReviewerUsesDedicatedPurposeAndSeparatedPrivateInput(t *testing.T) {
	client := &structuredClientStub{result: ai.StructuredResult{Usage: ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"}, OutputJSON: json.RawMessage(`{
		"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]
	}`)}}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	request := validAuditRequest("先找出固定费用。")
	if _, evidence, err := reviewer.ReviewTutorOutput(context.Background(), request); err != nil {
		t.Fatal(err)
	} else if evidence.Provider != "openai" || evidence.Model != "reviewer-v1" || evidence.RequestID == "" {
		t.Fatalf("review provenance=%+v", evidence)
	}
	if client.request.Purpose != ai.PurposeTutorOutputReview || client.request.SchemaName != reviewSchemaFile {
		t.Fatalf("review request=%+v", client.request)
	}
	if client.request.Instructions != reviewerInstructions {
		t.Fatalf("review instructions=%q", client.request.Instructions)
	}
	for _, required := range []string{"REJECT only", "uniquely determines", "complete solution", "PASS when", "observation direction", "type of evidence", "method framework", "follow-up question", "reason_codes=[NONE]", "empty violations array", "violation_type set exactly matches reason_codes", "payload_kind=MESSAGE", "segment_index=-1", "zero-based segment_index", "never duplicate a violation", "never output a free-text reason", "Do not rewrite", "unrelated safety classification"} {
		if !strings.Contains(client.request.Instructions, required) {
			t.Fatalf("review instructions missing %q: %s", required, client.request.Instructions)
		}
	}
	if strings.Contains(strings.ToLower(client.request.Instructions), "derive") {
		t.Fatalf("review instructions retain overbroad derive language: %s", client.request.Instructions)
	}
	payload := string(client.request.Input)
	for _, expected := range []string{"question_prompt", "private_answer", "correct_answer", "full_solution", "teacher_reference_answer", "candidate", "segments"} {
		if !strings.Contains(payload, expected) {
			t.Fatalf("review input missing %q: %s", expected, payload)
		}
	}
	for _, forbidden := range []string{"student_answer", "answer_revealed", "scoring_key", "misconceptions", "hint_policy"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("review input contains unnecessary field %q: %s", forbidden, payload)
		}
	}
}

func TestOpenAIReviewerReportsActualProviderModelForProvenance(t *testing.T) {
	client := &structuredClientStub{result: ai.StructuredResult{
		Usage:      ai.ModelUsage{Provider: "openai", Model: "generator-v1"},
		OutputJSON: json.RawMessage(`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`),
	}}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-alias")
	if err != nil {
		t.Fatal(err)
	}
	_, evidence, err := reviewer.ReviewTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Provider != "openai" || evidence.Model != "generator-v1" {
		t.Fatalf("review provenance did not use provider response identity: %+v", evidence)
	}
}

func TestOpenAIReviewerAcceptsConsistentRejectVerdict(t *testing.T) {
	client := &structuredClientStub{result: ai.StructuredResult{
		Usage:      ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"},
		OutputJSON: json.RawMessage(`{"result":"REJECT","no_answer_leak":false,"reason_codes":["DIRECT_ANSWER","FULL_SOLUTION"],"violations":[{"violation_type":"DIRECT_ANSWER","payload_kind":"MESSAGE","segment_index":-1},{"violation_type":"FULL_SOLUTION","payload_kind":"MESSAGE","segment_index":-1}]}`),
	}}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	review, _, err := reviewer.ReviewTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
	if err != nil {
		t.Fatal(err)
	}
	if review.Result != ReviewReject || review.NoAnswerLeak || len(review.ReasonCodes) != 2 {
		t.Fatalf("reject verdict=%+v", review)
	}
}

func TestOpenAIReviewerActualGeneratorIdentityFailsServiceProvenance(t *testing.T) {
	client := &structuredClientStub{result: ai.StructuredResult{
		Usage:      ai.ModelUsage{Provider: "openai", Model: "generator-v1"},
		OutputJSON: json.RawMessage(`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`),
	}}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-alias")
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService("openai:generator-v1", "openai:reviewer-alias", reviewer, &recorderStub{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。")); !errors.Is(err, ErrInvalidProvenance) {
		t.Fatalf("provider response from generator identity did not fail closed: %v", err)
	}
}

func TestOpenAIReviewerSubstitutedServedModelFailsServiceProvenance(t *testing.T) {
	for _, test := range []struct {
		name          string
		reportedModel string
		want          error
	}{
		{name: "served model matches configured model", reportedModel: "reviewer-v1", want: nil},
		{name: "provider served a different model", reportedModel: "reviewer-v1-cheap-alias", want: ErrInvalidProvenance},
		{name: "provider reported no model", reportedModel: "", want: ErrInvalidProvenance},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &structuredClientStub{result: ai.StructuredResult{
				Usage:         ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"},
				ReportedModel: test.reportedModel,
				OutputJSON:    json.RawMessage(`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`),
			}}
			reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
			if err != nil {
				t.Fatal(err)
			}
			service, err := NewService("openai:generator-v1", "openai:reviewer-v1", reviewer, &recorderStub{})
			if err != nil {
				t.Fatal(err)
			}
			err = service.AuditTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
			if test.want == nil {
				if err != nil {
					t.Fatalf("matching served model did not pass provenance: %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("substituted served model did not fail closed: %v", err)
			}
		})
	}
}

func TestOpenAIReviewerRejectsInvalidSchemaWithRequestEvidence(t *testing.T) {
	client := &structuredClientStub{result: ai.StructuredResult{Usage: ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"}, OutputJSON: json.RawMessage(`{
		"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[],"forged":true
	}`)}}
	reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
	if err != nil {
		t.Fatal(err)
	}
	_, evidence, err := reviewer.ReviewTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
	if !errors.Is(err, ErrInvalidReviewOutput) || evidence.RequestID == "" {
		t.Fatalf("invalid schema err=%v evidence=%+v", err, evidence)
	}
}

func TestOpenAIReviewerRejectsInconsistentVerdictsInServerCode(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
	}{
		{name: "PASS contradiction", output: `{"result":"PASS","no_answer_leak":false,"reason_codes":["NONE"],"violations":[]}`},
		{name: "REJECT contradiction", output: `{"result":"REJECT","no_answer_leak":true,"reason_codes":["DIRECT_ANSWER"],"violations":[{"violation_type":"DIRECT_ANSWER","payload_kind":"MESSAGE","segment_index":-1}]}`},
		{name: "illegal reason code", output: `{"result":"REJECT","no_answer_leak":false,"reason_codes":["NONE"],"violations":[]}`},
		{name: "empty reason codes", output: `{"result":"REJECT","no_answer_leak":false,"reason_codes":[],"violations":[]}`},
		{name: "duplicate reason codes", output: `{"result":"REJECT","no_answer_leak":false,"reason_codes":["DIRECT_ANSWER","DIRECT_ANSWER"],"violations":[{"violation_type":"DIRECT_ANSWER","payload_kind":"MESSAGE","segment_index":-1}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &structuredClientStub{result: ai.StructuredResult{
				Usage:      ai.ModelUsage{Provider: "openai", Model: "reviewer-v1"},
				OutputJSON: json.RawMessage(test.output),
			}}
			reviewer, err := NewOpenAIReviewer(client, "openai", "reviewer-v1")
			if err != nil {
				t.Fatal(err)
			}
			_, evidence, err := reviewer.ReviewTutorOutput(context.Background(), validAuditRequest("先找出固定费用。"))
			if !errors.Is(err, ErrInvalidReviewOutput) || evidence.RequestID == "" || !strings.Contains(err.Error(), "inconsistent verdict") {
				t.Fatalf("inconsistent verdict err=%v evidence=%+v", err, evidence)
			}
		})
	}
}
