package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

type structuredClientStub struct {
	result  StructuredResult
	err     error
	request StructuredRequest
}

func (stub *structuredClientStub) GenerateStructured(_ context.Context, request StructuredRequest) (StructuredResult, error) {
	stub.request = request
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
	provider, err := NewCodexProvider(client)
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

func TestCodexProviderBlocksUnauthorizedAnswerReveal(t *testing.T) {
	client := &structuredClientStub{result: StructuredResult{OutputJSON: json.RawMessage(`{
        "message":"原题答案是10。",
        "action":"EXPLAIN",
        "answer_revealed":true,
        "segments":[]
    }`)}}
	provider, err := NewCodexProvider(client)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateExplanation(context.Background(), ExplainRequest{
		TutorDecision: tutor.Decision{NextState: tutor.StateExplain, AnswerRevealAllowed: false},
	})
	if !errors.Is(err, ErrAnswerRevealViolation) {
		t.Fatalf("expected ErrAnswerRevealViolation, got %v", err)
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
	provider, err := NewCodexProvider(client)
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
	provider, err := NewCodexProvider(client)
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
	provider, err := NewCodexProvider(client)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	_, err = provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Question: content.QuestionPublic{
			Prompt:      "公开题面",
			Scene:       json.RawMessage(`{"kind":"NUMBER_LINE"}`),
			InputSchema: json.RawMessage(`{"type":"string"}`),
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
}
