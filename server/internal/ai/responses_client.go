package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	aioutputs "github.com/oppositenum/ai-learning-tutor/schemas/ai_outputs"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

type ResponsesAPIError struct {
	StatusCode    int
	RetryAfter    time.Duration
	RetryAfterSet bool
	body          string
	providerCode  string
}

var knownResponsesProviderErrorCodes = map[string]struct{}{
	"gateway_concurrency_limit": {},
	"invalid_json_schema":       {},
	"invalid_request_error":     {},
	"rate_limit_exceeded":       {},
	"server_error":              {},
	"service_unavailable":       {},
}

func (err *ResponsesAPIError) Error() string {
	if err == nil {
		return "Responses API request failed"
	}
	return fmt.Sprintf("Responses API status %d: %s", err.StatusCode, err.body)
}

func IsRetryableResponsesError(err error) bool {
	var responseErr *ResponsesAPIError
	return errors.As(err, &responseErr) && (responseErr.StatusCode == http.StatusTooManyRequests ||
		(responseErr.StatusCode >= http.StatusInternalServerError && responseErr.StatusCode <= 599))
}

func ResponsesRetryAfter(err error) (time.Duration, bool) {
	var responseErr *ResponsesAPIError
	if !errors.As(err, &responseErr) || !responseErr.RetryAfterSet {
		return 0, false
	}
	return responseErr.RetryAfter, true
}

func ResponsesErrorDiagnostics(err error) (httpStatus int, providerCode string, ok bool) {
	var responseErr *ResponsesAPIError
	if !errors.As(err, &responseErr) {
		return 0, TutorReviewDiagnosticUnavailable, false
	}
	return responseErr.StatusCode, sanitizeResponsesProviderErrorCode(responseErr.providerCode), true
}

type OpenAIResponsesClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	provider   string
	model      string
	usage      UsageRecorder
	// Some OpenAI-compatible relays reject previous_response_id chaining.
	// Conversation context still travels in the request input (prior turns),
	// so chaining is dropped for the process lifetime once rejected.
	chainingUnsupported atomic.Bool
}

const accountingWriteTimeout = 5 * time.Second

func (client *OpenAIResponsesClient) WithUsageRecorder(recorder UsageRecorder) *OpenAIResponsesClient {
	client.usage = recorder
	return client
}

func NewOpenAIResponsesClient(httpClient *http.Client, baseURL, apiKey, model string) (*OpenAIResponsesClient, error) {
	if httpClient == nil {
		return nil, errors.New("http client is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("OpenAI API key is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("OpenAI Tutor model is required")
	}
	return NewProviderResponsesClient(httpClient, baseURL, apiKey, "openai", model)
}

func NewProviderResponsesClient(httpClient *http.Client, baseURL, apiKey, provider, model string) (*OpenAIResponsesClient, error) {
	if httpClient == nil {
		return nil, errors.New("http client is required")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, errors.New("structured output provider is required")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("structured output API key is required")
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("structured output model is required")
	}
	if strings.TrimSpace(baseURL) == "" {
		if provider != "openai" {
			return nil, errors.New("structured output base URL is required for non-OpenAI providers")
		}
		baseURL = defaultOpenAIBaseURL
	}
	return &OpenAIResponsesClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		provider:   provider,
		model:      model,
	}, nil
}

type responsesRequest struct {
	Model              string        `json:"model"`
	Instructions       string        `json:"instructions"`
	Input              string        `json:"input"`
	PreviousResponseID string        `json:"previous_response_id,omitempty"`
	Text               responsesText `json:"text"`
}

type responsesText struct {
	Format responsesTextFormat `json:"format"`
}

type responsesTextFormat struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

type responsesResponse struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens       int64 `json:"input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
		InputTokenDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	} `json:"usage"`
}

func (client *OpenAIResponsesClient) GenerateStructured(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	startedAt := time.Now()
	if err := aioutputs.ValidateProviderSchemaCompatibility(request.Schema); err != nil {
		return StructuredResult{}, fmt.Errorf("check response schema provider compatibility: %w", err)
	}
	var schema any
	if err := json.Unmarshal(request.Schema, &schema); err != nil {
		return StructuredResult{}, fmt.Errorf("decode response schema: %w", err)
	}
	if guard, ok := client.usage.(PriceGuard); ok {
		if err := guard.EnsurePrice(ctx, client.provider, client.model, startedAt); err != nil {
			return StructuredResult{}, fmt.Errorf("check Responses API price: %w", err)
		}
	}

	previousResponseID := request.PreviousResponseID
	if client.chainingUnsupported.Load() {
		previousResponseID = ""
	}
	decoded, err := client.callResponses(ctx, request, schema, previousResponseID)
	if err != nil && previousResponseID != "" && isChainingRejected(err) {
		client.chainingUnsupported.Store(true)
		decoded, err = client.callResponses(ctx, request, schema, "")
	}
	if err != nil {
		outcome := RequestTransportError
		var status *int
		var responseErr *ResponsesAPIError
		if errors.As(err, &responseErr) {
			outcome = RequestProviderError
			value := responseErr.StatusCode
			status = &value
		}
		client.recordRequestOutcome(ctx, request, outcome, status, startedAt)
		return StructuredResult{}, err
	}
	result := StructuredResult{
		ResponseID: decoded.ID,
		Usage: ModelUsage{
			Provider: client.provider, Model: client.model, InputTokens: decoded.Usage.InputTokens,
			CachedInputTokens: decoded.Usage.InputTokenDetails.CachedTokens,
			OutputTokens:      decoded.Usage.OutputTokens,
		},
	}
	if client.usage != nil {
		requestID := request.RequestID
		if requestID == "" {
			requestID = decoded.ID
		}
		accountingCtx, cancelAccounting := context.WithTimeout(context.WithoutCancel(ctx), accountingWriteTimeout)
		defer cancelAccounting()
		if err := client.usage.RecordAIUsage(accountingCtx, UsageRecord{
			RequestID: requestID, StudentID: request.StudentID, SessionID: request.SessionID,
			Purpose: request.Purpose, Usage: result.Usage, Latency: time.Since(startedAt), CreatedAt: startedAt,
		}); err != nil {
			client.recordRequestOutcome(ctx, request, RequestAccountingError, nil, startedAt)
			return StructuredResult{}, fmt.Errorf("record Responses API usage: %w", err)
		}
	}
	output, err := firstOutputText(decoded)
	if err != nil {
		client.recordRequestOutcome(ctx, request, RequestInvalidResponse, nil, startedAt)
		return StructuredResult{}, err
	}
	result.OutputJSON = json.RawMessage(output)
	client.recordRequestOutcome(ctx, request, RequestSucceeded, nil, startedAt)
	return result, nil
}

func (client *OpenAIResponsesClient) recordRequestOutcome(ctx context.Context, request StructuredRequest, outcome RequestOutcome, status *int, startedAt time.Time) {
	recorder, ok := client.usage.(RequestOutcomeRecorder)
	if !ok || request.RequestID == "" {
		return
	}
	accountingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingWriteTimeout)
	defer cancel()
	_ = recorder.RecordAIRequestOutcome(accountingCtx, RequestOutcomeRecord{
		RequestID: request.RequestID, StudentID: request.StudentID, SessionID: request.SessionID,
		Provider: client.provider, Model: client.model, Purpose: request.Purpose, Outcome: outcome,
		HTTPStatus: status, Latency: time.Since(startedAt), CreatedAt: startedAt,
	})
}

func (client *OpenAIResponsesClient) callResponses(ctx context.Context, request StructuredRequest, schema any, previousResponseID string) (responsesResponse, error) {
	var decoded responsesResponse
	payload, err := json.Marshal(responsesRequest{
		Model: client.model, Instructions: request.Instructions, Input: string(request.Input),
		PreviousResponseID: previousResponseID,
		Text: responsesText{Format: responsesTextFormat{
			Type: "json_schema", Name: strings.TrimSuffix(request.SchemaName, ".schema.json"),
			Strict: true, Schema: schema,
		}},
	})
	if err != nil {
		return decoded, fmt.Errorf("encode Responses request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return decoded, fmt.Errorf("create Responses request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")

	httpResponse, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return decoded, fmt.Errorf("call Responses API: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, 4<<20))
	if err != nil {
		return decoded, fmt.Errorf("read Responses API response: %w", err)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		retryAfter, retryAfterSet := parseRetryAfter(httpResponse.Header.Get("Retry-After"), time.Now())
		return decoded, &ResponsesAPIError{
			StatusCode: httpResponse.StatusCode, RetryAfter: retryAfter,
			RetryAfterSet: retryAfterSet, body: strings.TrimSpace(string(body)),
			providerCode: extractResponsesProviderErrorCode(body),
		}
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return decoded, fmt.Errorf("decode Responses API response: %w", err)
	}
	return decoded, nil
}

func extractResponsesProviderErrorCode(body []byte) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return TutorReviewDiagnosticUnavailable
	}
	return sanitizeResponsesProviderErrorCode(envelope.Error.Code)
}

func sanitizeResponsesProviderErrorCode(code string) string {
	if len(code) == 0 || len(code) > 64 {
		return TutorReviewDiagnosticUnavailable
	}
	if _, known := knownResponsesProviderErrorCodes[code]; !known {
		return TutorReviewDiagnosticUnavailable
	}
	return code
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		const maxRetryAfterSeconds = int64((time.Duration(1<<63 - 1)) / time.Second)
		if seconds <= maxRetryAfterSeconds {
			return time.Duration(seconds) * time.Second, true
		}
		return 0, false
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func isChainingRejected(err error) bool {
	message := err.Error()
	return strings.Contains(message, "status 400") && strings.Contains(message, "previous_response_id")
}

func firstOutputText(response responsesResponse) (string, error) {
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && content.Text != "" {
				return content.Text, nil
			}
		}
	}
	return "", errors.New("Responses API returned no output_text")
}
