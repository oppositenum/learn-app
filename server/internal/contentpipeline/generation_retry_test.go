package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

// scriptedGenerationClient replays a fixed sequence of provider replies and
// counts calls, so these tests can distinguish "resampled once" from "sent the
// same request twice for no reason".
type scriptedGenerationClient struct {
	outputs  []string
	errs     []error
	calls    int
	requests []ai.StructuredRequest
	waits    []time.Time
}

func (client *scriptedGenerationClient) GenerateStructured(ctx context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	index := client.calls
	client.calls++
	client.requests = append(client.requests, request)
	client.waits = append(client.waits, time.Now())
	if index < len(client.errs) && client.errs[index] != nil {
		return ai.StructuredResult{}, client.errs[index]
	}
	if index >= len(client.outputs) {
		return ai.StructuredResult{}, errors.New("provider called more times than the script allows")
	}
	return ai.StructuredResult{
		RequestID: request.RequestID, ResponseID: "provider-response",
		OutputJSON: json.RawMessage(client.outputs[index]),
		Usage:      ai.ModelUsage{Provider: "doubao", Model: "doubao-seed-2-1-pro-260628"},
	}, nil
}

const validGeneration = `{"questions":[{"prompt_public":"一次买三张同价门票另加服务费，一共花了多少？","answer":"10 元","numeric_value":10,"unit":"元","solution":"先减去服务费，再平均分成三份。","solution_result":10,"misconceptions":[],"choices":[]}]}`

// The provider failure observed live: a raw newline inside a JSON string.
const rawNewlineGeneration = "{\"questions\":[{\"prompt_public\":\"第一行\n第二行\"}]}"

const schemaInvalidGeneration = `{"questions":[]}`

func liveLikeContext() GenerationContext {
	return GenerationContext{
		KnowledgePointID: "00000000-0000-4000-8000-000000000001",
		SubjectCode:      "MATH", Difficulty: "L2", QuestionType: "FREE_TEXT", Count: 1,
	}
}

func newScriptedGenerator(t *testing.T, client *scriptedGenerationClient) *OpenAIGenerator {
	t.Helper()
	generator, err := NewOpenAIGenerator(client, "doubao", "doubao-seed-2-1-pro-260628")
	if err != nil {
		t.Fatal(err)
	}
	return generator
}

func TestContentGenerationResamplesOutputThisServiceRefused(t *testing.T) {
	for _, test := range []struct {
		name  string
		first string
	}{
		{name: "invalid JSON then valid", first: rawNewlineGeneration},
		{name: "schema rejected then valid", first: schemaInvalidGeneration},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedGenerationClient{outputs: []string{test.first, validGeneration}}
			started := time.Now()
			result, metadata, err := newScriptedGenerator(t, client).Generate(context.Background(), liveLikeContext())
			if err != nil {
				t.Fatalf("refused output was not resampled: %v", err)
			}
			if client.calls != 2 {
				t.Fatalf("provider calls=%d want 2", client.calls)
			}
			if len(result.Questions) != 1 || result.Questions[0].Answer != "10 元" {
				t.Fatalf("the second reply did not reach the caller: %+v", result)
			}
			if metadata.Provider != "doubao" {
				t.Fatalf("metadata=%+v", metadata)
			}
			// Each attempt carries its own request ID so the two priced calls
			// stay separable in accounting.
			if client.requests[0].RequestID == "" || client.requests[0].RequestID == client.requests[1].RequestID {
				t.Fatalf("request IDs=%q %q", client.requests[0].RequestID, client.requests[1].RequestID)
			}
			// No backoff: an offline pipeline gains nothing from sleeping, and
			// a long fixed wait would hide the failure rather than handle it.
			if gap := client.waits[1].Sub(client.waits[0]); gap > 250*time.Millisecond {
				t.Fatalf("retry waited %s before resampling", gap)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("generation took %s for two scripted calls", elapsed)
			}
		})
	}
}

func TestContentGenerationFailsClosedAfterTwoRefusedOutputs(t *testing.T) {
	client := &scriptedGenerationClient{outputs: []string{rawNewlineGeneration, rawNewlineGeneration, validGeneration}}
	result, metadata, err := newScriptedGenerator(t, client).Generate(context.Background(), liveLikeContext())
	if err == nil {
		t.Fatal("two refused outputs did not fail closed")
	}
	if !errors.Is(err, ai.ErrInvalidProviderOutput) {
		t.Fatalf("error was not classified as refused provider output: %v", err)
	}
	if client.calls != contentGenerationMaxAttempts {
		t.Fatalf("provider calls=%d want %d", client.calls, contentGenerationMaxAttempts)
	}
	if len(result.Questions) != 0 || metadata.RequestID != "" {
		t.Fatalf("a failed generation returned usable content: %+v %+v", result, metadata)
	}
}

func TestContentGenerationDoesNotRetryNonProviderOutputFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "provider transport failure", err: errors.New("call provider: connection reset")},
		{name: "provider rejected the request", err: errors.New("provider status 400: bad request")},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &scriptedGenerationClient{errs: []error{test.err}, outputs: []string{validGeneration, validGeneration}}
			if _, _, err := newScriptedGenerator(t, client).Generate(context.Background(), liveLikeContext()); err == nil {
				t.Fatal("a non-output failure was swallowed")
			}
			if client.calls != 1 {
				t.Fatalf("provider calls=%d want 1: only refused output is resampled", client.calls)
			}
		})
	}
}

func TestContentGenerationStopsWhenTheCallerCancels(t *testing.T) {
	client := &scriptedGenerationClient{outputs: []string{rawNewlineGeneration, validGeneration}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := newScriptedGenerator(t, client).Generate(ctx, liveLikeContext()); err == nil {
		t.Fatal("a cancelled context still produced a generation")
	}
	if client.calls != 0 {
		t.Fatalf("provider calls=%d want 0 after cancellation", client.calls)
	}
}

func TestContentReviewerStillFailsClosedWithoutRetrying(t *testing.T) {
	// The reviewer is the independent gate for content. Asking it again after
	// it failed to return a usable verdict is not equivalent to resampling a
	// generator, and this round deliberately leaves that boundary alone.
	client := &scriptedGenerationClient{outputs: []string{`{"result":"NOT_AN_ENUM"}`, `{"result":"PASS","age_appropriate":true,"factually_sound":true,"unambiguous":true,"no_answer_leak":true,"safe_values":true,"findings":[]}`}}
	reviewer, err := NewOpenAIReviewer(client, "qwen", "qwen3.7-plus-2026-05-26")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := reviewer.Review(context.Background(), contentAsset()); err == nil {
		t.Fatal("an unusable review verdict was accepted")
	}
	if client.calls != 1 {
		t.Fatalf("reviewer calls=%d want 1: the reviewer must not retry", client.calls)
	}
}

func TestRefusedGenerationErrorKeepsItsContext(t *testing.T) {
	client := &scriptedGenerationClient{outputs: []string{schemaInvalidGeneration, schemaInvalidGeneration}}
	_, _, err := newScriptedGenerator(t, client).Generate(context.Background(), liveLikeContext())
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "validate content generation output") {
		t.Fatalf("error lost its original context: %v", err)
	}
}
