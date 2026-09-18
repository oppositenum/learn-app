package contentpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
}

func (recorder *contentPriceRecorder) EnsurePrice(_ context.Context, provider, model string, _ time.Time) error {
	recorder.prices = append(recorder.prices, provider+":"+model)
	return recorder.priceErr
}

func (recorder *contentPriceRecorder) RecordAIUsage(_ context.Context, record ai.UsageRecord) error {
	recorder.usage = append(recorder.usage, record.Usage.Provider+":"+record.Usage.Model+"/"+string(record.Purpose))
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
