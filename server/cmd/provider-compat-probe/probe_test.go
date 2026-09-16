package main

import (
	"context"
	"encoding/json"
	"errors"
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
			payload, endpoint, err := requestForShape(test.shape, "endpoint-model", schema, "probe", "")
			if err != nil {
				t.Fatal(err)
			}
			if endpoint != test.endpoint || !strings.Contains(string(payload), test.marker) || !strings.Contains(string(payload), `"strict":true`) {
				t.Fatalf("endpoint=%q payload=%s", endpoint, payload)
			}
		})
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
