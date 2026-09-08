package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type structuredClientStub struct {
	result      StructuredResult
	err         error
	request     StructuredRequest
	calls       int
	deadline    time.Time
	hasDeadline bool
}

type tutorOutputAuditorStub struct {
	requests    []TutorOutputAuditRequest
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (stub *tutorOutputAuditorStub) AuditTutorOutput(ctx context.Context, request TutorOutputAuditRequest) error {
	stub.requests = append(stub.requests, request)
	stub.deadline, stub.hasDeadline = ctx.Deadline()
	return stub.err
}

func passingTutorOutputAuditor() *tutorOutputAuditorStub { return &tutorOutputAuditorStub{} }

func (stub *structuredClientStub) GenerateStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	stub.request = request
	stub.calls++
	stub.deadline, stub.hasDeadline = ctx.Deadline()
	return stub.result, stub.err
}

func TestCodexProviderRejectsSchemaViolation(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"先看固定费用。",
        "action":"PROBE",
        "answer_revealed":false,
        "segments":[],
        "unexpected":"must fail"
    }`)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateProbe},
	})
	if err == nil {
		t.Fatal("expected additional property to violate strict schema")
	}
}

func TestCodexProviderDoesNotTrustAnswerRevealedWhenAuditorRejectsBody(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"原题答案是10。",
        "action":"EXPLAIN",
		"answer_revealed":false,
        "segments":[]
    }`)}}
	auditErr := errors.New("deterministic answer match")
	auditor := &tutorOutputAuditorStub{err: auditErr}
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateExplanation(context.Background(), ExplainRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateExplain, AnswerRevealAllowed: false},
	})
	if !errors.Is(err, auditErr) || len(auditor.requests) != 1 || auditor.requests[0].Candidate.Message != "原题答案是10。" {
		t.Fatalf("auditor did not reject self-reported-safe answer: err=%v requests=%+v", err, auditor.requests)
	}
}

func TestCodexProviderFailsClosedWithoutTutorOutputAuditor(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"先看数量关系。","action":"PROBE","answer_revealed":false,"segments":[]
	}`)}}
	provider, err := NewCodexProvider(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{TutorDecision: tutor.Decision{NextState: tutor.StateProbe}}); !errors.Is(err, ErrTutorOutputAuditorUnavailable) {
		t.Fatalf("missing auditor did not fail closed: %v", err)
	}
	if client.request.SchemaName != "" {
		t.Fatal("Tutor provider was called before the missing-auditor failure")
	}
}

func TestCodexProviderCarriesResponseContext(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{
		ResponseID: "resp_next",
		OutputJSON: json.RawMessage(`{
            "message":"配送费是哪一部分？",
            "action":"PROBE",
            "answer_revealed":false,
            "segments":[]
        }`),
	}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	turn, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		PreviousResponseID: "resp_previous",
		TutorDecision:      tutor.Decision{NextState: tutor.StateProbe},
	})
	if err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	if client.request.PreviousResponseID != "resp_previous" || turn.ResponseID != "resp_next" {
		t.Fatalf("response context was not preserved: request=%q result=%q", client.request.PreviousResponseID, turn.ResponseID)
	}
	if len(client.request.Schema) == 0 {
		t.Fatal("provider did not send a strict output schema")
	}
}

func TestCodexProviderConstrainsActionEnumToServerDecision(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"给你一个方向。",
        "action":"HINT",
        "answer_revealed":false,
        "segments":[]
    }`)}}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}); err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	var outgoing map[string]any
	if err := json.Unmarshal(client.request.Schema, &outgoing); err != nil {
		t.Fatalf("decode outgoing schema: %v", err)
	}
	action := outgoing["properties"].(map[string]any)["action"].(map[string]any)
	enum, ok := action["enum"].([]any)
	if !ok || len(enum) != 1 || enum[0] != "HINT" {
		t.Fatalf("outgoing action enum was not constrained to the server decision: %v", action["enum"])
	}
}

func TestCodexProviderSendsOnlyPublicQuestionDataToTutorGeneration(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"先观察题目中的数量关系。",
        "action":"PROBE",
        "answer_revealed":false,
        "segments":[]
    }`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Question: content.QuestionPublic{
			Prompt:      "公开题面",
			Scene:       json.RawMessage(`{"kind":"NUMBER_LINE"}`),
			InputSchema: json.RawMessage(`{"type":"string"}`),
		},
		AuditPrivateAnswer: content.QuestionPrivateAnswer{
			CorrectAnswer: json.RawMessage(`{"value":"private_answer_canary"}`),
			FullSolution:  "private_full_solution_canary", TeacherReferenceAnswer: "private_teacher_reference_canary",
		},
		TutorDecision: tutor.Decision{NextState: tutor.StateProbe},
	})
	if err != nil {
		t.Fatalf("generate turn: %v", err)
	}
	payload := strings.ToLower(string(client.request.Input))
	if !strings.Contains(payload, "公开题面") {
		t.Fatalf("public prompt missing from teaching request: %s", client.request.Input)
	}
	for _, forbidden := range []string{
		"correct_answer",
		"full_solution",
		"teacher_reference_answer",
		"scoring_key",
		"misconceptions",
		"hint_policy",
		"private_answer_canary",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("teaching request contains private answer field %q: %s", forbidden, client.request.Input)
		}
	}
	if len(auditor.requests) != 1 || !strings.Contains(string(auditor.requests[0].PrivateAnswer.CorrectAnswer), "private_answer_canary") {
		t.Fatalf("auditor did not receive private server context: %+v", auditor.requests)
	}
}

func TestCodexProviderAuditsEveryStudentVisibleGenerationEntryPoint(t *testing.T) {
	for _, test := range []struct {
		name   string
		action tutor.State
		call   func(*CodexProvider, GenerateTurnRequest) error
	}{
		{name: "turn", action: tutor.StateHint, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateTurn(context.Background(), request)
			return err
		}},
		{name: "analogy", action: tutor.StateAnalogy, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateAnalogy(context.Background(), AnalogyRequest(request))
			return err
		}},
		{name: "parallel example", action: tutor.StateExplain, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateParallelExample(context.Background(), ExampleRequest(request))
			return err
		}},
		{name: "explanation", action: tutor.StateVoiceExplain, call: func(provider *CodexProvider, request GenerateTurnRequest) error {
			_, err := provider.GenerateExplanation(context.Background(), ExplainRequest(request))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &structuredClientStub{result: StructuredResult{ResponseID: "resp-audited", OutputJSON: json.RawMessage(`{
				"message":"先找题目条件。","action":"` + string(test.action) + `","answer_revealed":false,
				"segments":[{"id":"s1","text":"先找条件。"}]
			}`)}}
			auditor := passingTutorOutputAuditor()
			provider, err := NewCodexProvider(client, auditor)
			if err != nil {
				t.Fatal(err)
			}
			request := GenerateTurnRequest{StudentID: "student", SessionID: "session", TutorDecision: tutor.Decision{NextState: test.action}}
			if err := test.call(provider, request); err != nil {
				t.Fatal(err)
			}
			if len(auditor.requests) != 1 || len(auditor.requests[0].Candidate.Segments) != 1 || auditor.requests[0].GeneratorResponseID != "resp-audited" {
				t.Fatalf("entry point did not cross audit gate: %+v", auditor.requests)
			}
			for _, required := range []string{"Never directly quote, repeat, or equivalently paraphrase", "Do not locate evidence for the student", "Never provide the answer or a complete solution"} {
				if !strings.Contains(client.request.Instructions, required) {
					t.Fatalf("%s instructions missing shared disclosure constraint %q: %s", test.name, required, client.request.Instructions)
				}
			}
			if test.action == tutor.StateHint {
				if !strings.Contains(client.request.Instructions, hintInstructions) {
					t.Fatalf("HINT instructions missing dedicated constraint: %s", client.request.Instructions)
				}
			} else if strings.Contains(client.request.Instructions, hintInstructions) {
				t.Fatalf("%s unexpectedly received HINT-only instructions: %s", test.name, client.request.Instructions)
			}
			if test.name == "parallel example" {
				const unchanged = "Explain with a parallel example using different values. Do not solve the original question."
				if !strings.HasPrefix(client.request.Instructions, unchanged+" ") {
					t.Fatalf("parallel-example instruction changed: %s", client.request.Instructions)
				}
			}
		})
	}
}

func TestCodexProviderBoundsTutorGenerationAndAuditWithOneOperationDeadline(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
		"message":"先找题目条件。","action":"HINT","answer_revealed":false,"segments":[]
	}`)}}
	auditor := passingTutorOutputAuditor()
	provider, err := NewCodexProvider(client, auditor)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}); err != nil {
		t.Fatal(err)
	}
	remaining := client.deadline.Sub(started)
	if !client.hasDeadline || !auditor.hasDeadline || !client.deadline.Equal(auditor.deadline) {
		t.Fatalf("generation deadline=%v/%v audit deadline=%v/%v", client.deadline, client.hasDeadline, auditor.deadline, auditor.hasDeadline)
	}
	if remaining <= 84*time.Second || remaining > tutorOutputOperationTimeout+100*time.Millisecond {
		t.Fatalf("operation deadline remaining=%v", remaining)
	}
	if client.calls != 1 {
		t.Fatalf("Tutor generation calls=%d want=1", client.calls)
	}
}
