package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

type OpenAIResponsesClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
	usage      UsageRecorder
	// Some OpenAI-compatible relays reject previous_response_id chaining.
	// Conversation context still travels in the request input (prior turns),
	// so chaining is dropped for the process lifetime once rejected.
	chainingUnsupported atomic.Bool
}

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
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultOpenAIBaseURL
	}
	return &OpenAIResponsesClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
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
	var schema any
	if err := json.Unmarshal(request.Schema, &schema); err != nil {
		return StructuredResult{}, fmt.Errorf("decode response schema: %w", err)
	}
	if guard, ok := client.usage.(PriceGuard); ok {
		if err := guard.EnsurePrice(ctx, "openai", client.model, startedAt); err != nil {
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
		return StructuredResult{}, err
	}
	result := StructuredResult{
		ResponseID: decoded.ID,
		Usage: ModelUsage{
			Provider: "openai", Model: decoded.Model, InputTokens: decoded.Usage.InputTokens,
			CachedInputTokens: decoded.Usage.InputTokenDetails.CachedTokens,
			OutputTokens:      decoded.Usage.OutputTokens,
		},
	}
	if client.usage != nil {
		requestID := request.RequestID
		if requestID == "" {
			requestID = decoded.ID
		}
		if err := client.usage.RecordAIUsage(ctx, UsageRecord{
			RequestID: requestID, StudentID: request.StudentID, SessionID: request.SessionID,
			Purpose: request.Purpose, Usage: result.Usage, Latency: time.Since(startedAt), CreatedAt: startedAt,
		}); err != nil {
			return StructuredResult{}, fmt.Errorf("record Responses API usage: %w", err)
		}
	}
	output, err := firstOutputText(decoded)
	if err != nil {
		return StructuredResult{}, err
	}
	result.OutputJSON = json.RawMessage(output)
	return result, nil
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
		return decoded, fmt.Errorf("Responses API status %d: %s", httpResponse.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return decoded, fmt.Errorf("decode Responses API response: %w", err)
	}
	return decoded, nil
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
