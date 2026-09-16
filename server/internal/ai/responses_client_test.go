package ai

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
)

type collectingUsageRecorder struct {
	records     []UsageRecord
	outcomes    []RequestOutcomeRecord
	priceChecks []ModelUsage
	err         error
	priceErr    error
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func (recorder *collectingUsageRecorder) RecordAIUsage(_ context.Context, record UsageRecord) error {
	recorder.records = append(recorder.records, record)
	return recorder.err
}

func (recorder *collectingUsageRecorder) RecordAIRequestOutcome(_ context.Context, record RequestOutcomeRecord) error {
	recorder.outcomes = append(recorder.outcomes, record)
	return nil
}

func (recorder *collectingUsageRecorder) EnsurePrice(_ context.Context, provider, model string, _ time.Time) error {
	recorder.priceChecks = append(recorder.priceChecks, ModelUsage{Provider: provider, Model: model})
	return recorder.priceErr
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
	if len(recorder.outcomes) != 1 || recorder.outcomes[0].Outcome != RequestInvalidResponse || recorder.outcomes[0].HTTPStatus != nil {
		t.Fatalf("invalid response outcome=%+v", recorder.outcomes)
	}
}

func TestProviderResponsesClientUsesConfiguredIdentityForPriceUsageAndOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{
            "id":"provider-response-id",
            "model":"vendor-reported-model-name",
            "output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}],
            "usage":{"input_tokens":9,"output_tokens":4,"input_tokens_details":{"cached_tokens":3}}
        }`))
	}))
	defer server.Close()

	recorder := &collectingUsageRecorder{}
	client, err := NewProviderResponsesClient(server.Client(), server.URL, "test-key", "configured-provider", "configured-endpoint-id")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	result, err := client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "canonical-identity", Purpose: PurposeSocraticTurn,
		Input: json.RawMessage(`{}`), SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.Provider != "configured-provider" || result.Usage.Model != "configured-endpoint-id" {
		t.Fatalf("result usage identity=%s:%s", result.Usage.Provider, result.Usage.Model)
	}
	if len(recorder.priceChecks) != 1 || recorder.priceChecks[0].Provider != result.Usage.Provider || recorder.priceChecks[0].Model != result.Usage.Model {
		t.Fatalf("price checks=%+v usage=%+v", recorder.priceChecks, result.Usage)
	}
	if len(recorder.records) != 1 || recorder.records[0].Usage.Provider != result.Usage.Provider || recorder.records[0].Usage.Model != result.Usage.Model {
		t.Fatalf("usage records=%+v usage=%+v", recorder.records, result.Usage)
	}
	if len(recorder.outcomes) != 1 || recorder.outcomes[0].Provider != result.Usage.Provider || recorder.outcomes[0].Model != result.Usage.Model {
		t.Fatalf("outcomes=%+v usage=%+v", recorder.outcomes, result.Usage)
	}
}

func TestOpenAIResponsesClientPreservesOpenAIIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"id":"response-id","model":"response-alias","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}],"usage":{}}`))
	}))
	defer server.Close()
	recorder := &collectingUsageRecorder{}
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "configured-model")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	result, err := client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "openai-identity", Purpose: PurposeSocraticTurn,
		SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.Provider != "openai" || result.Usage.Model != "configured-model" {
		t.Fatalf("OpenAI compatibility identity=%s:%s", result.Usage.Provider, result.Usage.Model)
	}
}

func TestProviderResponsesClientMissingPriceMakesNoNetworkRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()
	recorder := &collectingUsageRecorder{priceErr: errors.New("price missing")}
	client, err := NewProviderResponsesClient(server.Client(), server.URL, "test-key", "configured-provider", "configured-model")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	_, err = client.GenerateStructured(context.Background(), StructuredRequest{
		SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "price missing") {
		t.Fatalf("price preflight error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("provider received %d request(s) without a price", calls)
	}
}

func TestOpenAIResponsesClientRecordsBoundedRequestOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		recorderErr error
		want        RequestOutcome
		wantStatus  *int
	}{
		{
			name: "success",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(`{"id":"ok","model":"model","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}],"usage":{}}`))
			},
			want: RequestSucceeded,
		},
		{
			name: "provider error",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"error":{"message":"provider body must not be recorded"}}`))
			},
			want: RequestProviderError, wantStatus: intPointer(http.StatusServiceUnavailable),
		},
		{
			name: "accounting error",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte(`{"id":"usage-failed","model":"model","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}],"usage":{}}`))
			},
			recorderErr: errors.New("usage unavailable"), want: RequestAccountingError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			recorder := &collectingUsageRecorder{err: test.recorderErr}
			client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "model")
			if err != nil {
				t.Fatal(err)
			}
			client.WithUsageRecorder(recorder)
			_, _ = client.GenerateStructured(context.Background(), StructuredRequest{
				RequestID: "bounded-outcome", StudentID: "student", SessionID: "session",
				Purpose: PurposeAnswerAnalysis, SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
			})
			if len(recorder.outcomes) != 1 || recorder.outcomes[0].Outcome != test.want {
				t.Fatalf("outcomes=%+v want=%s", recorder.outcomes, test.want)
			}
			gotStatus := recorder.outcomes[0].HTTPStatus
			if (gotStatus == nil) != (test.wantStatus == nil) || gotStatus != nil && *gotStatus != *test.wantStatus {
				t.Fatalf("status=%v want=%v", gotStatus, test.wantStatus)
			}
		})
	}
}

func TestOpenAIResponsesClientRecordsTransportErrorWithoutHTTPStatus(t *testing.T) {
	recorder := &collectingUsageRecorder{}
	client, err := NewOpenAIResponsesClient(&http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		},
	)}, "https://provider.invalid", "test-key", "model")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	_, err = client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "transport-error", StudentID: "student", SessionID: "session",
		Purpose: PurposeAnswerAnalysis, SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err == nil {
		t.Fatal("transport failure was accepted")
	}
	if len(recorder.outcomes) != 1 || recorder.outcomes[0].Outcome != RequestTransportError {
		t.Fatalf("outcomes=%+v want=%s", recorder.outcomes, RequestTransportError)
	}
	if recorder.outcomes[0].HTTPStatus != nil {
		t.Fatalf("transport error unexpectedly recorded HTTP status %d", *recorder.outcomes[0].HTTPStatus)
	}
}

func TestProviderResponsesClientPropagatesInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	recorder := &collectingUsageRecorder{}
	client, err := NewProviderResponsesClient(server.Client(), server.URL, "test-key", "provider-a", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, callErr := client.GenerateStructured(ctx, StructuredRequest{
			RequestID: "cancel-in-flight", Purpose: PurposeSocraticTurn,
			SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
		})
		result <- callErr
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	select {
	case callErr := <-result:
		close(release)
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("canceled request error=%v", callErr)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("provider request did not return promptly after cancellation")
	}
	if len(recorder.records) != 0 {
		t.Fatalf("canceled request recorded successful usage: %+v", recorder.records)
	}
	if len(recorder.outcomes) != 1 || recorder.outcomes[0].Outcome != RequestTransportError {
		t.Fatalf("canceled request outcomes=%+v", recorder.outcomes)
	}
}

func intPointer(value int) *int { return &value }

func TestOpenAIResponsesClientRejectsIncompatibleSchemaBeforeNetwork(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "reviewer-model")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateStructured(context.Background(), StructuredRequest{
		SchemaName: "incompatible.schema.json",
		Schema:     json.RawMessage(`{"type":"object","oneOf":[]}`),
	})
	if err == nil || !strings.Contains(err.Error(), `keyword "oneOf"`) {
		t.Fatalf("incompatible schema error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("provider received %d request(s) for an incompatible schema", calls)
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

func TestOpenAIResponsesClientClassifiesRetryableStatuses(t *testing.T) {
	for _, test := range []struct {
		status    int
		retryable bool
	}{
		{status: http.StatusBadRequest},
		{status: http.StatusUnauthorized},
		{status: http.StatusTooManyRequests, retryable: true},
		{status: http.StatusInternalServerError, retryable: true},
		{status: http.StatusServiceUnavailable, retryable: true},
	} {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(`{"error":{"code":"test_failure"}}`))
			}))
			defer server.Close()
			client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "reviewer-model")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GenerateStructured(context.Background(), StructuredRequest{
				SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
			})
			var responseErr *ResponsesAPIError
			if !errors.As(err, &responseErr) || responseErr.StatusCode != test.status {
				t.Fatalf("response error=%v", err)
			}
			if got := IsRetryableResponsesError(err); got != test.retryable {
				t.Fatalf("retryable=%v want=%v", got, test.retryable)
			}
		})
	}
}

func TestOpenAIResponsesClientExtractsOnlyAllowlistedProviderDiagnostics(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantCode     string
		wantHTTPCode int
	}{
		{
			name:         "known code",
			body:         `{"error":{"code":"invalid_json_schema","message":"raw_provider_response_canary"}}`,
			wantCode:     "invalid_json_schema",
			wantHTTPCode: http.StatusBadRequest,
		},
		{
			name:         "unknown code",
			body:         `{"error":{"code":"unrecognized_provider_failure","message":"raw_provider_response_canary"}}`,
			wantCode:     TutorReviewDiagnosticUnavailable,
			wantHTTPCode: http.StatusInternalServerError,
		},
		{
			name:         "overlong code",
			body:         `{"error":{"code":"` + strings.Repeat("x", 65) + `","message":"raw_provider_response_canary"}}`,
			wantCode:     TutorReviewDiagnosticUnavailable,
			wantHTTPCode: http.StatusInternalServerError,
		},
		{
			name:         "non-string code",
			body:         `{"error":{"code":500,"message":"raw_provider_response_canary"}}`,
			wantCode:     TutorReviewDiagnosticUnavailable,
			wantHTTPCode: http.StatusInternalServerError,
		},
		{
			name:         "malformed body",
			body:         `raw_provider_response_canary`,
			wantCode:     TutorReviewDiagnosticUnavailable,
			wantHTTPCode: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.wantHTTPCode)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "reviewer-model")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GenerateStructured(context.Background(), StructuredRequest{
				SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
			})
			httpStatus, providerCode, ok := ResponsesErrorDiagnostics(err)
			if !ok || httpStatus != test.wantHTTPCode || providerCode != test.wantCode {
				t.Fatalf("diagnostics=(%d,%q,%v) want=(%d,%q,true)", httpStatus, providerCode, ok, test.wantHTTPCode, test.wantCode)
			}
			for _, forbidden := range []string{"raw_provider_response_canary", "message"} {
				if strings.Contains(providerCode, forbidden) {
					t.Fatalf("diagnostics exposed provider response content")
				}
			}
		})
	}
}

func TestOpenAIResponsesClientCapturesRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "7")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "reviewer-model")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateStructured(context.Background(), StructuredRequest{
		SchemaName: "test", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if retryAfter, ok := ResponsesRetryAfter(err); !ok || retryAfter != 7*time.Second {
		t.Fatalf("Retry-After=%v present=%v", retryAfter, ok)
	}
}

func TestParseRetryAfterHTTPDateAndInvalidValues(t *testing.T) {
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	when := now.Add(9 * time.Second).Format(http.TimeFormat)
	if delay, ok := parseRetryAfter(when, now); !ok || delay != 9*time.Second {
		t.Fatalf("HTTP-date delay=%v present=%v", delay, ok)
	}
	if delay, ok := parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now); !ok || delay != 0 {
		t.Fatalf("past HTTP-date delay=%v present=%v", delay, ok)
	}
	for _, value := range []string{"", "not-a-date", "-1", "9223372036854775807"} {
		if delay, ok := parseRetryAfter(value, now); ok || delay != 0 {
			t.Fatalf("invalid %q delay=%v present=%v", value, delay, ok)
		}
	}
}

func TestChatCompletionsShapeBuildsContractAndMapsUsage(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.Path
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &seenBody)
		_, _ = writer.Write([]byte(`{
            "id":"chat-1","model":"served-alias",
            "choices":[{"message":{"content":"{\"ok\":true}"}}],
            "usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":11}}
        }`))
	}))
	defer server.Close()

	recorder := &collectingUsageRecorder{}
	client, err := NewStructuredProviderClient(server.Client(), server.URL, "key", "qwen", "configured-model",
		ShapeChatCompletions, map[string]any{"enable_thinking": false})
	if err != nil {
		t.Fatal(err)
	}
	client.WithUsageRecorder(recorder)
	result, err := client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "chat-request", Purpose: PurposeSocraticTurn,
		Instructions: "system rule", Input: json.RawMessage(`{"a":1}`),
		SchemaName: "tutor_turn.schema.json", Schema: json.RawMessage(`{"type":"object"}`),
		PreviousResponseID: "must-not-be-sent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/chat/completions" {
		t.Fatalf("endpoint=%s", seenPath)
	}
	format, _ := seenBody["response_format"].(map[string]any)
	schema, _ := format["json_schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["strict"] != true || schema["name"] != "tutor_turn" {
		t.Fatalf("structured-output contract=%v", seenBody["response_format"])
	}
	if seenBody["enable_thinking"] != false {
		t.Fatalf("request overlay was not applied: %v", seenBody)
	}
	if _, chained := seenBody["previous_response_id"]; chained {
		t.Fatalf("chat completions must not carry previous_response_id: %v", seenBody)
	}
	messages, _ := seenBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages=%v", messages)
	}
	if string(result.OutputJSON) != `{"ok":true}` {
		t.Fatalf("output=%s", result.OutputJSON)
	}
	if result.ReportedModel != "served-alias" {
		t.Fatalf("reported model=%q", result.ReportedModel)
	}
	if result.Usage.Provider != "qwen" || result.Usage.Model != "configured-model" {
		t.Fatalf("identity=%s:%s", result.Usage.Provider, result.Usage.Model)
	}
	if result.Usage.InputTokens != 30 || result.Usage.OutputTokens != 7 || result.Usage.CachedInputTokens != 11 {
		t.Fatalf("usage mapping=%+v", result.Usage)
	}
	if len(recorder.priceChecks) != 1 || recorder.priceChecks[0].Provider != "qwen" || recorder.priceChecks[0].Model != "configured-model" {
		t.Fatalf("price preflight=%+v", recorder.priceChecks)
	}
}

func TestStructuredProviderClientRejectsBadShapeAndContractOverlay(t *testing.T) {
	for _, test := range []struct {
		name    string
		shape   string
		overlay map[string]any
	}{
		{name: "unknown shape", shape: "graphql", overlay: nil},
		{name: "overlay rewrites model", shape: ShapeChatCompletions, overlay: map[string]any{"model": "cheaper"}},
		{name: "overlay rewrites response_format", shape: ShapeChatCompletions, overlay: map[string]any{"response_format": map[string]any{"type": "text"}}},
		{name: "overlay rewrites messages", shape: ShapeChatCompletions, overlay: map[string]any{"messages": []any{}}},
		{name: "overlay rewrites text", shape: ShapeResponses, overlay: map[string]any{"text": map[string]any{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewStructuredProviderClient(http.DefaultClient, "https://example.invalid", "k", "p", "m", test.shape, test.overlay); err == nil {
				t.Fatal("client was constructed with an unsafe configuration")
			}
		})
	}
}

func TestResponsesShapeIsUnchangedByTheSharedBuilder(t *testing.T) {
	var seenPath string
	var seenBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seenPath = request.URL.Path
		body, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(body, &seenBody)
		_, _ = writer.Write([]byte(`{"id":"r","model":"m","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}],"usage":{"input_tokens":2,"output_tokens":1}}`))
	}))
	defer server.Close()
	client, err := NewOpenAIResponsesClient(server.Client(), server.URL, "key", "configured-model")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GenerateStructured(context.Background(), StructuredRequest{
		RequestID: "r1", Purpose: PurposeSocraticTurn, Instructions: "inst",
		Input: json.RawMessage(`{"a":1}`), SchemaName: "tutor_turn.schema.json",
		Schema: json.RawMessage(`{"type":"object"}`), PreviousResponseID: "prev-1",
	}); err != nil {
		t.Fatal(err)
	}
	if seenPath != "/responses" || seenBody["previous_response_id"] != "prev-1" || seenBody["instructions"] != "inst" {
		t.Fatalf("responses contract changed: path=%s body=%v", seenPath, seenBody)
	}
	text, _ := seenBody["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "tutor_turn" {
		t.Fatalf("responses json_schema contract=%v", text)
	}
}
