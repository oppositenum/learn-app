package main

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

type probeAccountingStub struct {
	priceErr error
	usageErr error
	prices   []ai.ModelUsage
	usage    []ai.UsageRecord
	outcomes []ai.RequestOutcomeRecord
}

func (stub *probeAccountingStub) EnsurePrice(_ context.Context, provider, model string, _ time.Time) error {
	stub.prices = append(stub.prices, ai.ModelUsage{Provider: provider, Model: model})
	return stub.priceErr
}

func (stub *probeAccountingStub) RecordAIUsage(_ context.Context, record ai.UsageRecord) error {
	stub.usage = append(stub.usage, record)
	return stub.usageErr
}

func (stub *probeAccountingStub) RecordAIRequestOutcome(_ context.Context, record ai.RequestOutcomeRecord) error {
	stub.outcomes = append(stub.outcomes, record)
	return nil
}

func TestRequestForShapeBuildsResponsesAndChatContracts(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
	for _, test := range []struct {
		shape    string
		endpoint string
		marker   string
	}{
		{shape: "responses", endpoint: "/responses", marker: `"text"`},
		{shape: "chat_completions", endpoint: "/chat/completions", marker: `"response_format"`},
	} {
		t.Run(test.shape, func(t *testing.T) {
			payload, endpoint, err := requestForShape(test.shape, "endpoint-model", schema, "probe", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if endpoint != test.endpoint || !strings.Contains(string(payload), test.marker) || !strings.Contains(string(payload), `"strict":true`) {
				t.Fatalf("endpoint=%q payload=%s", endpoint, payload)
			}
		})
	}
}

func TestRequestForShapeMergesReasoningControlWithoutTouchingContract(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
	for _, test := range []struct {
		shape     string
		reasoning map[string]any
		marker    string
	}{
		{shape: "responses", reasoning: map[string]any{"thinking": map[string]any{"type": "disabled"}}, marker: `"thinking":{"type":"disabled"}`},
		{shape: "chat_completions", reasoning: map[string]any{"enable_thinking": false}, marker: `"enable_thinking":false`},
	} {
		t.Run(test.shape, func(t *testing.T) {
			payload, _, err := requestForShape(test.shape, "endpoint-model", schema, "probe", "", test.reasoning)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(payload), test.marker) {
				t.Fatalf("reasoning control missing: %s", payload)
			}
			if !strings.Contains(string(payload), `"strict":true`) || !strings.Contains(string(payload), `"endpoint-model"`) {
				t.Fatalf("reasoning control damaged the structured-output contract: %s", payload)
			}
		})
	}
}

func TestReasoningOverlayRejectsNonObjectAndContractOverrides(t *testing.T) {
	for _, test := range []struct {
		name    string
		control string
		wantErr bool
	}{
		{name: "empty means no control", control: "", wantErr: false},
		{name: "valid object", control: `{"enable_thinking":false}`, wantErr: false},
		{name: "not an object", control: `"disabled"`, wantErr: true},
		{name: "malformed", control: `{`, wantErr: true},
		{name: "overrides model", control: `{"model":"other"}`, wantErr: true},
		{name: "overrides response_format", control: `{"response_format":{}}`, wantErr: true},
		{name: "overrides text", control: `{"text":{}}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := providerProbeConfig{Name: "probe", ReasoningControl: test.control}
			_, err := config.reasoningOverlay()
			if (err != nil) != test.wantErr {
				t.Fatalf("control=%q err=%v wantErr=%v", test.control, err, test.wantErr)
			}
		})
	}
}

func TestProbeRecordsAppliedReasoningControlAsEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if !strings.Contains(string(body), `"enable_thinking":false`) {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{"id":"r","model":"m","choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`))
	}))
	defer server.Close()
	runner := newProbeRunner(server.Client(), &probeAccountingStub{}, 1)
	config := providerProbeConfig{
		Name: "probe", Provider: "p", Model: "m", BaseURL: server.URL,
		APIKey: "k", AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		ReasoningControl: `{"enable_thinking":false}`,
	}
	observation, _ := runner.call(context.Background(), config, "chat_completions", json.RawMessage(`{"type":"object"}`), "probe", ai.PurposeSocraticTurn, "")
	if observation.ReasoningControl != `{"enable_thinking":false}` {
		t.Fatalf("applied reasoning control was not recorded: %+v", observation)
	}
	if observation.Classification != "ACCEPTED_ACCOUNTED" {
		t.Fatalf("reasoning control was not sent to the provider: %+v", observation)
	}
}

func TestProbePriceFailurePreventsNetworkCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	accounting := &probeAccountingStub{priceErr: errors.New("price missing")}
	runner := newProbeRunner(server.Client(), accounting, 1)
	observation, _ := runner.call(context.Background(), testProbeConfig(server.URL), "responses", json.RawMessage(`{"type":"object"}`), "probe", ai.PurposeSocraticTurn, "")
	if observation.Classification != "PRICE_PREFLIGHT_FAILED" || calls != 0 {
		t.Fatalf("observation=%+v calls=%d", observation, calls)
	}
}

func TestProbeAccountsConfiguredIdentityAndCapturesResponseMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" {
			t.Fatalf("path=%s", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{
          "id":"response-id",
          "model":"vendor-model-alias",
          "output":[{"content":[{"text":"{}"}]}],
          "usage":{"input_tokens":12,"output_tokens":4,"input_tokens_details":{"cached_tokens":2}}
        }`))
	}))
	defer server.Close()
	accounting := &probeAccountingStub{}
	runner := newProbeRunner(server.Client(), accounting, 1)
	observation, output := runner.call(context.Background(), testProbeConfig(server.URL), "responses", json.RawMessage(`{"type":"object"}`), "probe", ai.PurposeSocraticTurn, "")
	if observation.Classification != "ACCEPTED_ACCOUNTED" || !observation.AccountingWritten || string(output) != "{}" {
		t.Fatalf("observation=%+v output=%s", observation, output)
	}
	if observation.ResponseModel != "vendor-model-alias" || observation.ModelMatches {
		t.Fatalf("response model evidence=%+v", observation)
	}
	if len(accounting.usage) != 1 || accounting.usage[0].Usage.Provider != "probe-provider" || accounting.usage[0].Usage.Model != "configured-model" || accounting.usage[0].Usage.CachedInputTokens != 2 {
		t.Fatalf("usage=%+v", accounting.usage)
	}
	if len(accounting.outcomes) != 1 || accounting.outcomes[0].Outcome != ai.RequestSucceeded {
		t.Fatalf("outcomes=%+v", accounting.outcomes)
	}
}

func TestProbeAccountingFailureNeverClassifiesRequestAsAccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"id":"response-id","model":"configured-model","output":[{"content":[{"text":"{}"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()
	accounting := &probeAccountingStub{usageErr: errors.New("database unavailable")}
	runner := newProbeRunner(server.Client(), accounting, 1)
	observation, _ := runner.call(context.Background(), testProbeConfig(server.URL), "responses", json.RawMessage(`{"type":"object"}`), "probe", ai.PurposeSocraticTurn, "")
	if observation.Classification != "ACCOUNTING_FAILED" || observation.AccountingWritten {
		t.Fatalf("observation=%+v", observation)
	}
	if len(accounting.outcomes) != 1 || accounting.outcomes[0].Outcome != ai.RequestAccountingError {
		t.Fatalf("outcomes=%+v", accounting.outcomes)
	}
}

func TestPreferredShapePicksEnforcementNotAcceptedStatus(t *testing.T) {
	accounted := map[string]probeObservation{
		"responses":        {Classification: "ACCEPTED_ACCOUNTED"},
		"chat_completions": {Classification: "ACCEPTED_ACCOUNTED"},
	}
	constraints := func(responsesResult, chatResult string) map[string]constraintFinding {
		found := make(map[string]constraintFinding)
		for _, keyword := range constraintKeywords {
			found["responses:"+keyword] = constraintFinding{Classification: responsesResult}
			found["chat_completions:"+keyword] = constraintFinding{Classification: chatResult}
		}
		return found
	}
	for _, test := range []struct {
		name        string
		observed    map[string]probeObservation
		constraints map[string]constraintFinding
		want        string
	}{
		{
			name:     "both accounted but only chat enforces",
			observed: accounted, constraints: constraints("IGNORED", "ENFORCED"),
			want: "chat_completions",
		},
		{
			name:     "both accounted and both enforce keeps declaration order",
			observed: accounted, constraints: constraints("ENFORCED", "ENFORCED"),
			want: "responses",
		},
		{
			name:     "rejection loses to a merely ignored shape",
			observed: accounted, constraints: constraints("REJECTED", "IGNORED"),
			want: "chat_completions",
		},
		{
			name: "only one shape accounted",
			observed: map[string]probeObservation{
				"responses":        {Classification: "PROVIDER_REJECTED"},
				"chat_completions": {Classification: "ACCEPTED_ACCOUNTED"},
			},
			constraints: constraints("ENFORCED", "IGNORED"),
			want:        "chat_completions",
		},
		{
			name: "nothing accounted",
			observed: map[string]probeObservation{
				"responses":        {Classification: "PROVIDER_REJECTED"},
				"chat_completions": {Classification: "TRANSPORT_ERROR"},
			},
			constraints: constraints("ENFORCED", "ENFORCED"),
			want:        "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			shape, reason := preferredShape(test.observed, test.constraints)
			if shape != test.want {
				t.Fatalf("preferred shape=%q want %q (reason %q)", shape, test.want, reason)
			}
			if reason == "" {
				t.Fatal("preferred shape was chosen without a recorded reason")
			}
		})
	}
}

func TestConstraintClassificationDistinguishesEnforcedAndIgnored(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["value"],"properties":{"value":{"enum":["ALLOWED"]}}}`)
	accepted := probeObservation{Classification: "ACCEPTED_ACCOUNTED"}
	if got := classifyConstraint("responses", accepted, schema, json.RawMessage(`{"value":"ALLOWED"}`)); got.Classification != "ENFORCED" {
		t.Fatalf("valid output classification=%+v", got)
	}
	if got := classifyConstraint("responses", accepted, schema, json.RawMessage(`{"value":"BLOCKED"}`)); got.Classification != "IGNORED" {
		t.Fatalf("invalid output classification=%+v", got)
	}
}

func testProbeConfig(baseURL string) providerProbeConfig {
	return providerProbeConfig{
		Name: "test", Provider: "probe-provider", BaseURL: baseURL, APIKey: "test-key",
		Model: "configured-model", Region: "test-region", AuthHeader: "Authorization", AuthPrefix: "Bearer ",
	}
}

func TestConstraintProbeNeedsEveryAttemptToClaimEnforced(t *testing.T) {
	for _, test := range []struct {
		name         string
		violateOn    int // attempt number that returns schema-violating output, 0 = never
		want         string
		wantAttempts int
	}{
		{name: "all attempts comply", violateOn: 0, want: "ENFORCED", wantAttempts: constraintAttempts},
		{name: "first attempt violates", violateOn: 1, want: "IGNORED", wantAttempts: 1},
		{name: "last attempt violates", violateOn: constraintAttempts, want: "IGNORED", wantAttempts: constraintAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls++
				// constraintProbe("maxLength") allows a one-character string.
				value := "a"
				if calls == test.violateOn {
					value = "far too long"
				}
				inner, _ := json.Marshal(map[string]string{"value": value})
				envelope, _ := json.Marshal(map[string]any{
					"id": "r", "model": "configured-model",
					"output": []any{map[string]any{"content": []any{map[string]any{"text": string(inner)}}}},
					"usage":  map[string]any{"input_tokens": 1, "output_tokens": 1},
				})
				_, _ = writer.Write(envelope)
			}))
			defer server.Close()
			runner := newProbeRunner(server.Client(), &probeAccountingStub{}, 1)
			finding := runner.probeConstraint(context.Background(), testProbeConfig(server.URL), "responses", "maxLength")
			if finding.Classification != test.want || finding.Attempts != test.wantAttempts {
				t.Fatalf("finding=%+v want=%s attempts=%d (calls=%d)", finding, test.want, test.wantAttempts, calls)
			}
		})
	}
}
