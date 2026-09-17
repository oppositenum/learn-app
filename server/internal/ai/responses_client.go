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

// ErrInvalidProviderOutput marks a response the provider returned successfully
// but that this service refused: unparseable JSON, or JSON that fails local
// schema validation. Providers do not enforce every schema control — nested
// enums and array item types have both been observed unenforced — so a single
// malformed field would otherwise end the turn with no second attempt.
var ErrInvalidProviderOutput = errors.New("provider returned output this service cannot accept")

// IsRetryableGenerationError reports whether another identical attempt could
// plausibly succeed. It covers provider-side congestion and provider output
// this service rejected. It deliberately excludes failures in building our own
// request, where retrying would only repeat the same mistake.
//
// Retrying rejected output does not weaken any gate: the output was already
// discarded, and the replacement passes through the same local validation,
// the deterministic answer check, and the independent review.
func IsRetryableGenerationError(err error) bool {
	return IsRetryableResponsesError(err) || errors.Is(err, ErrInvalidProviderOutput)
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

const (
	// ShapeResponses is the OpenAI Responses wire format: a json_schema under
	// text.format, conversation chaining by previous_response_id.
	ShapeResponses = "responses"
	// ShapeChatCompletions is the OpenAI Chat Completions wire format: a
	// json_schema under response_format, no server-side conversation chaining.
	ShapeChatCompletions = "chat_completions"
)

// StructuredProviderClient calls one provider for strict structured output.
// Price preflight, usage accounting, request-outcome recording, and error
// classification are shared across wire formats on purpose: those are the
// safety and cost boundaries, and a second copy of them would be free to drift.
// Only the request body and the response decoding differ per shape.
type StructuredProviderClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	provider   string
	model      string
	shape      string
	// requestOverlay is merged into the top level of every request body. It
	// carries provider-specific controls such as disabling reasoning mode; it
	// may not touch the structured-output contract.
	requestOverlay map[string]any
	usage          UsageRecorder
	// Some OpenAI-compatible relays reject previous_response_id chaining.
	// Conversation context still travels in the request input (prior turns),
	// so chaining is dropped for the process lifetime once rejected.
	chainingUnsupported atomic.Bool
}

// OpenAIResponsesClient is the original name of the Responses-shaped client.
type OpenAIResponsesClient = StructuredProviderClient

const accountingWriteTimeout = 5 * time.Second

// reservedRequestFields may never come from a request overlay: they are the
// structured-output contract and the identity the call is accounted under.
var reservedRequestFields = []string{"model", "messages", "input", "instructions", "response_format", "text", "previous_response_id"}

func ValidateRequestOverlay(overlay map[string]any) error {
	for _, reserved := range reservedRequestFields {
		if _, present := overlay[reserved]; present {
			return fmt.Errorf("request overlay must not override %q", reserved)
		}
	}
	return nil
}

func (client *StructuredProviderClient) WithUsageRecorder(recorder UsageRecorder) *StructuredProviderClient {
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

func NewProviderResponsesClient(httpClient *http.Client, baseURL, apiKey, provider, model string) (*StructuredProviderClient, error) {
	return NewStructuredProviderClient(httpClient, baseURL, apiKey, provider, model, ShapeResponses, nil)
}

func NewStructuredProviderClient(httpClient *http.Client, baseURL, apiKey, provider, model, shape string, overlay map[string]any) (*StructuredProviderClient, error) {
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
	switch shape {
	case ShapeResponses, ShapeChatCompletions:
	default:
		return nil, fmt.Errorf("unsupported structured output shape %q", shape)
	}
	if err := ValidateRequestOverlay(overlay); err != nil {
		return nil, err
	}
	if strings.TrimSpace(baseURL) == "" {
		if provider != "openai" {
			return nil, errors.New("structured output base URL is required for non-OpenAI providers")
		}
		baseURL = defaultOpenAIBaseURL
	}
	return &StructuredProviderClient{
		httpClient:     httpClient,
		baseURL:        strings.TrimRight(baseURL, "/"),
		apiKey:         apiKey,
		provider:       provider,
		model:          model,
		shape:          shape,
		requestOverlay: overlay,
	}, nil
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesOutputItem struct {
	Type    string                   `json:"type"`
	Content []responsesOutputContent `json:"content"`
}

type responsesResponse struct {
	ID     string                `json:"id"`
	Model  string                `json:"model"`
	Output []responsesOutputItem `json:"output"`
	Usage  struct {
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
		RequestID: request.RequestID, ResponseID: decoded.ID, ReportedModel: decoded.Model,
		Usage: ModelUsage{
			Provider: client.provider, Model: client.model, InputTokens: decoded.Usage.InputTokens,
			CachedInputTokens: decoded.Usage.InputTokenDetails.CachedTokens,
			OutputTokens:      decoded.Usage.OutputTokens,
		},
	}
	if result.RequestID == "" {
		result.RequestID = decoded.ID
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
	payload, endpoint, err := client.buildRequest(request, schema, previousResponseID)
	if err != nil {
		return decoded, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+endpoint, bytes.NewReader(payload))
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
	if err := client.decodeResponse(body, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

// buildRequest renders the same StructuredRequest into whichever wire format
// the configured provider actually enforces the schema on.
func (client *StructuredProviderClient) buildRequest(request StructuredRequest, schema any, previousResponseID string) ([]byte, string, error) {
	schemaName := strings.TrimSuffix(request.SchemaName, ".schema.json")
	var payload map[string]any
	endpoint := "/responses"
	if client.shape == ShapeChatCompletions {
		endpoint = "/chat/completions"
		messages := []map[string]string{}
		if request.Instructions != "" {
			messages = append(messages, map[string]string{"role": "system", "content": request.Instructions})
		}
		messages = append(messages, map[string]string{"role": "user", "content": string(request.Input)})
		payload = map[string]any{
			"model": client.model, "messages": messages,
			"response_format": map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name": schemaName, "strict": true, "schema": schema,
				},
			},
		}
	} else {
		payload = map[string]any{
			"model": client.model, "instructions": request.Instructions, "input": string(request.Input),
			"text": map[string]any{"format": map[string]any{
				"type": "json_schema", "name": schemaName, "strict": true, "schema": schema,
			}},
		}
		if previousResponseID != "" {
			payload["previous_response_id"] = previousResponseID
		}
	}
	for key, value := range client.requestOverlay {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("encode %s request: %w", client.shape, err)
	}
	return encoded, endpoint, nil
}

type chatCompletionsResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// decodeResponse normalizes either wire format into the internal shape, so the
// accounting and output-extraction paths below stay identical.
func (client *StructuredProviderClient) decodeResponse(body []byte, decoded *responsesResponse) error {
	if client.shape != ShapeChatCompletions {
		if err := json.Unmarshal(body, decoded); err != nil {
			return fmt.Errorf("decode Responses API response: %w", err)
		}
		return nil
	}
	var chat chatCompletionsResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return fmt.Errorf("decode Chat Completions response: %w", err)
	}
	decoded.ID, decoded.Model = chat.ID, chat.Model
	decoded.Usage.InputTokens = chat.Usage.PromptTokens
	decoded.Usage.OutputTokens = chat.Usage.CompletionTokens
	decoded.Usage.InputTokenDetails.CachedTokens = chat.Usage.PromptTokensDetails.CachedTokens
	for _, choice := range chat.Choices {
		if choice.Message.Content == "" {
			continue
		}
		decoded.Output = append(decoded.Output, responsesOutputItem{
			Type:    "message",
			Content: []responsesOutputContent{{Type: "output_text", Text: choice.Message.Content}},
		})
		break
	}
	return nil
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
