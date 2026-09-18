package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
)

// contentPriceRecorder stands in for the usage recorder so these tests can
// assert the two behaviours the content pipeline shares with the Tutor path:
// price preflight happens before any network call, and generation and review
// are accounted separately under their own identities.
type contentPriceRecorder struct {
	priceErr error
	prices   []string
	usage    []string
	records  []ai.UsageRecord
}

func (recorder *contentPriceRecorder) EnsurePrice(_ context.Context, provider, model string, _ time.Time) error {
	recorder.prices = append(recorder.prices, provider+":"+model)
	return recorder.priceErr
}

func (recorder *contentPriceRecorder) RecordAIUsage(_ context.Context, record ai.UsageRecord) error {
	recorder.usage = append(recorder.usage, record.Usage.Provider+":"+record.Usage.Model+"/"+string(record.Purpose))
	recorder.records = append(recorder.records, record)
	return nil
}

func contentAsset() Asset {
	return Asset{
		KnowledgePointID: "00000000-0000-4000-8000-000000000001",
		Difficulty:       "L2",
		PromptPublic:     "三张同价门票加6元服务费共36元。",
		TeacherPrivate:   PrivateAnswer{Answer: "10", Solution: "先减服务费再平均分。", Misconceptions: []string{}},
	}
}

func TestContentChannelsUseChatCompletionsWithoutChaining(t *testing.T) {
	for _, test := range []struct {
		name     string
		provider string
		model    string
		output   string
		call     func(t *testing.T, client ai.StructuredClient, provider, model string)
	}{
		{
			name: "generator", provider: "doubao", model: "doubao-seed-2-1-pro-260628",
			output: `{"questions":[{"prompt_public":"改写后的题干","answer":"10","numeric_value":10,"unit":"元","solution":"先减服务费再平均分。","solution_result":10,"misconceptions":[],"choices":[]}]}`,
			call: func(t *testing.T, client ai.StructuredClient, provider, model string) {
				generator, err := NewOpenAIGenerator(client, provider, model)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := generator.Generate(context.Background(), GenerationContext{
					KnowledgePointID: contentAsset().KnowledgePointID, Difficulty: "L2",
				}); err != nil {
					t.Fatalf("generate: %v", err)
				}
			},
		},
		{
			name: "reviewer", provider: "qwen", model: "qwen3.7-plus-2026-05-26",
			output: `{"result":"PASS","age_appropriate":true,"factually_sound":true,"unambiguous":true,"no_answer_leak":true,"safe_values":true,"findings":[]}`,
			call: func(t *testing.T, client ai.StructuredClient, provider, model string) {
				reviewer, err := NewOpenAIReviewer(client, provider, model)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := reviewer.Review(context.Background(), contentAsset()); err != nil {
					t.Fatalf("review: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var seenPath string
			var seenBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				seenPath = request.URL.Path
				raw, _ := io.ReadAll(request.Body)
				_ = json.Unmarshal(raw, &seenBody)
				_, _ = writer.Write([]byte(`{"id":"content-1","model":"served-alias","choices":[{"message":{"content":` +
					strconvQuote(test.output) + `}}],"usage":{"prompt_tokens":40,"completion_tokens":12}}`))
			}))
			defer server.Close()

			recorder := &contentPriceRecorder{}
			client, err := ai.NewStructuredProviderClient(server.Client(), server.URL, "key",
				test.provider, test.model, ai.ShapeChatCompletions, map[string]any{"enable_thinking": false})
			if err != nil {
				t.Fatal(err)
			}
			test.call(t, client.WithUsageRecorder(recorder), test.provider, test.model)

			if seenPath != "/chat/completions" {
				t.Fatalf("endpoint=%s", seenPath)
			}
			if _, chained := seenBody["previous_response_id"]; chained {
				t.Fatal("content request carried previous_response_id")
			}
			format, _ := seenBody["response_format"].(map[string]any)
			schema, _ := format["json_schema"].(map[string]any)
			if format["type"] != "json_schema" || schema["strict"] != true {
				t.Fatalf("structured-output contract=%v", seenBody["response_format"])
			}
			if seenBody["enable_thinking"] != false {
				t.Fatalf("request overlay was not applied: %v", seenBody)
			}
			want := test.provider + ":" + test.model
			if len(recorder.prices) != 1 || recorder.prices[0] != want {
				t.Fatalf("price preflight=%v want %s", recorder.prices, want)
			}
			// The served alias must not become the accounted identity.
			if len(recorder.usage) != 1 || !strings.HasPrefix(recorder.usage[0], want+"/") {
				t.Fatalf("usage=%v want prefix %s", recorder.usage, want)
			}
		})
	}
}

// TestRefusedContentGenerationIsStillAccountedForEveryAttempt closes the gap the
// scripted client in generation_retry_test.go cannot reach: that client stands in
// for ai.StructuredProviderClient, so it never proves an attempt was priced and
// recorded. A refused output arrives in a 2xx reply the provider will bill for,
// so the resample must add a second usage record rather than overwrite the first.
func TestRefusedContentGenerationIsStillAccountedForEveryAttempt(t *testing.T) {
	const provider, model = "doubao", "doubao-seed-2-1-pro-260628"
	replies := []string{rawNewlineGeneration, validGeneration}
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if served >= len(replies) {
			t.Errorf("provider called %d times, script allows %d", served+1, len(replies))
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		body := replies[served]
		served++
		// Both attempts bill tokens and both carry their own provider-side id.
		_, _ = writer.Write([]byte(`{"id":"content-attempt-` + strconv.Itoa(served) +
			`","model":"served-alias","choices":[{"message":{"content":` + strconvQuote(body) +
			`}}],"usage":{"prompt_tokens":40,"completion_tokens":12}}`))
	}))
	defer server.Close()

	recorder := &contentPriceRecorder{}
	client, err := ai.NewStructuredProviderClient(server.Client(), server.URL, "key",
		provider, model, ai.ShapeChatCompletions, nil)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewOpenAIGenerator(client.WithUsageRecorder(recorder), provider, model)
	if err != nil {
		t.Fatal(err)
	}
	result, metadata, err := generator.Generate(context.Background(), liveLikeContext())
	if err != nil {
		t.Fatalf("refused output was not resampled through the real client: %v", err)
	}
	if len(result.Questions) != 1 || metadata.RequestID == "" {
		t.Fatalf("second attempt did not reach the caller: %+v %+v", result, metadata)
	}
	if served != 2 {
		t.Fatalf("provider served %d replies, want 2", served)
	}

	// Price preflight is per attempt, not per Generate call: a resample that
	// skipped it could spend against a price the catalogue no longer carries.
	want := provider + ":" + model
	if len(recorder.prices) != 2 || recorder.prices[0] != want || recorder.prices[1] != want {
		t.Fatalf("price preflight=%v want %s twice", recorder.prices, want)
	}
	if len(recorder.records) != 2 {
		t.Fatalf("usage records=%d want 2: the refused attempt was billed but not accounted", len(recorder.records))
	}
	for index, record := range recorder.records {
		if record.Usage.Provider != provider || record.Usage.Model != model {
			t.Fatalf("usage[%d] identity=%s:%s want %s", index, record.Usage.Provider, record.Usage.Model, want)
		}
		if record.Purpose != ai.PurposeContentGeneration {
			t.Fatalf("usage[%d] purpose=%s want %s", index, record.Purpose, ai.PurposeContentGeneration)
		}
		if record.RequestID == "" {
			t.Fatalf("usage[%d] carries no request id", index)
		}
		if record.Usage.InputTokens == 0 || record.Usage.OutputTokens == 0 {
			t.Fatalf("usage[%d] recorded no tokens: %+v", index, record.Usage)
		}
	}
	// Distinct request IDs are what keeps the two priced calls separable in the
	// ledger; a shared ID would read as one call charged twice.
	if recorder.records[0].RequestID == recorder.records[1].RequestID {
		t.Fatalf("both attempts accounted under request id %q", recorder.records[0].RequestID)
	}
	// Provenance quotes the provider-side response id, accounting quotes the id
	// this service generated — different namespaces on purpose. What matters is
	// that provenance names the accepted attempt, not the one that was refused.
	if metadata.RequestID != "content-attempt-2" {
		t.Fatalf("provenance request id=%q, want the accepted attempt content-attempt-2", metadata.RequestID)
	}
}

func TestContentGenerationMakesNoNetworkCallWithoutAPrice(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()

	recorder := &contentPriceRecorder{priceErr: errors.New("price missing")}
	client, err := ai.NewStructuredProviderClient(server.Client(), server.URL, "key",
		"doubao", "doubao-seed-2-1-pro-260628", ai.ShapeChatCompletions, nil)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := NewOpenAIGenerator(client.WithUsageRecorder(recorder), "doubao", "doubao-seed-2-1-pro-260628")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := generator.Generate(context.Background(), GenerationContext{
		KnowledgePointID: contentAsset().KnowledgePointID, Difficulty: "L2",
	}); err == nil {
		t.Fatal("content generation proceeded without an effective price")
	}
	if calls != 0 {
		t.Fatalf("provider received %d request(s) without a price", calls)
	}
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
