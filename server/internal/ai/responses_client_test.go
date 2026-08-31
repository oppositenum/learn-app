package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type collectingUsageRecorder struct{ records []UsageRecord }

func (recorder *collectingUsageRecorder) RecordAIUsage(_ context.Context, record UsageRecord) error {
	recorder.records = append(recorder.records, record)
	return nil
}

func TestOpenAIResponsesClientUsesStrictSchemaAndContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" || request.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["previous_response_id"] != "resp_previous" {
			t.Fatalf("previous response ID not sent: %v", body)
		}
		if _, exists := body["tools"]; exists {
			t.Fatalf("Teaching Agent request must not expose tools: %v", body["tools"])
		}
		format := body["text"].(map[string]any)["format"].(map[string]any)
		if format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("strict JSON schema was not requested: %v", format)
		}

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
            "id":"resp_next",
            "model":"configured-tutor-model",
            "output":[{"type":"message","content":[{"type":"output_text","text":"{\"message\":\"再看一步\",\"action\":\"PROBE\",\"answer_revealed\":false,\"segments\":[]}"}]}],
            "usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":40}}
        }`))
	}))
	defer server.Close()

	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "configured-tutor-model")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	result, err := client.GenerateStructured(context.Background(), StructuredRequest{
		Instructions: "test", Input: json.RawMessage(`{"answer":"12"}`),
		SchemaName: "tutor_turn.schema.json", Schema: json.RawMessage(`{"type":"object"}`),
		PreviousResponseID: "resp_previous",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.ResponseID != "resp_next" || result.Usage.CachedInputTokens != 40 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestOpenAIResponsesClientRecordsUsageBeforeOutputExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"resp_metered","model":"metered-model","output":[],"usage":{"input_tokens":9,"output_tokens":4,"input_tokens_details":{"cached_tokens":3}}}`))
	}))
	defer server.Close()
	recorder := &collectingUsageRecorder{}
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "metered-model")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	_, err = client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "req-metered", Purpose: PurposeSocraticTurn,
		Input: json.RawMessage(`{}`), SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil {
		t.Fatal("missing output_text was accepted")
	}
	if len(recorder.records) != 1 || recorder.records[0].RequestID != "req-metered" || recorder.records[0].Usage.CachedInputTokens != 3 {
		t.Fatalf("usage was not recorded before output parsing: %+v", recorder.records)
	}
	if recorder.records[0].Latency < 0 || recorder.records[0].CreatedAt.After(time.Now()) {
		t.Fatalf("invalid usage timing: %+v", recorder.records[0])
	}
}

func TestOpenAIResponsesClientFallsBackWhenChainingIsRejected(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if _, chained := body["previous_response_id"]; chained {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"error":{"message":"previous_response_id requires an OpenAI API-key account for HTTP requests","type":"invalid_request_error"}}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"id": "resp_fallback", "model": "mock-tutor",
			"output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": `{"ok":true}`}}}},
			"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "mock-tutor")
	if err != nil {
		t.Fatal(err)
	}
	request := StructuredRequest{RequestID: "req-1", Schema: json.RawMessage(`{"type":"object"}`), SchemaName: "tutor_turn.schema.json", PreviousResponseID: "resp_previous"}
	result, err := client.GenerateStructured(context.Background(), request)
	if err != nil {
		t.Fatalf("fallback generate: %v", err)
	}
	if result.ResponseID != "resp_fallback" || calls != 2 {
		t.Fatalf("expected one rejected call plus one fallback call, got calls=%d result=%+v", calls, result)
	}
	request.RequestID = "req-2"
	if _, err := client.GenerateStructured(context.Background(), request); err != nil {
		t.Fatalf("second generate: %v", err)
	}
	if calls != 3 {
		t.Fatalf("chaining rejection should be remembered, got calls=%d", calls)
	}
}
