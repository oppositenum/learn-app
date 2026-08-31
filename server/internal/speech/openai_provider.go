package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type OpenAIProvider struct {
	client                                     *http.Client
	baseURL, apiKey, sttModel, ttsModel, voice string
}

func NewOpenAIProvider(client *http.Client, baseURL, apiKey, sttModel, ttsModel, voice string) (*OpenAIProvider, error) {
	if client == nil || strings.TrimSpace(apiKey) == "" || strings.TrimSpace(sttModel) == "" || strings.TrimSpace(ttsModel) == "" || strings.TrimSpace(voice) == "" {
		return nil, errors.New("HTTP client, API key, STT model, TTS model, and voice are required")
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{client: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, sttModel: sttModel, ttsModel: ttsModel, voice: voice}, nil
}

func (provider *OpenAIProvider) STTUsageIdentity() (string, string) {
	return "openai", provider.sttModel
}

func (provider *OpenAIProvider) TTSUsageIdentity() (string, string) {
	return "openai", provider.ttsModel
}

func (provider *OpenAIProvider) Transcribe(ctx context.Context, audio Audio) (Transcript, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "student-answer.audio")
	if err != nil {
		return Transcript{}, err
	}
	if _, err = part.Write(audio.Bytes); err != nil {
		return Transcript{}, err
	}
	if err = writer.WriteField("model", provider.sttModel); err != nil {
		return Transcript{}, err
	}
	if err = writer.Close(); err != nil {
		return Transcript{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.baseURL+"/audio/transcriptions", &body)
	if err != nil {
		return Transcript{}, err
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := provider.client.Do(request)
	if err != nil {
		return Transcript{}, fmt.Errorf("call transcription API: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return Transcript{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Transcript{}, fmt.Errorf("transcription API status %d", response.StatusCode)
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(payload, &decoded); err != nil {
		return Transcript{}, err
	}
	if strings.TrimSpace(decoded.Text) == "" {
		return Transcript{}, errors.New("transcription API returned empty text")
	}
	return Transcript{Text: decoded.Text, Provider: "openai", Model: provider.sttModel, RequestID: requestID(response)}, nil
}

func (provider *OpenAIProvider) Synthesize(ctx context.Context, text string, segments []Segment) (Synthesis, error) {
	payload, err := json.Marshal(map[string]any{"model": provider.ttsModel, "voice": provider.voice, "input": text, "instructions": "Speak warmly and clearly for a school-age learner. Do not add words.", "response_format": "wav"})
	if err != nil {
		return Synthesis{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.baseURL+"/audio/speech", bytes.NewReader(payload))
	if err != nil {
		return Synthesis{}, err
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return Synthesis{}, fmt.Errorf("call speech API: %w", err)
	}
	defer response.Body.Close()
	audioBytes, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return Synthesis{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Synthesis{}, fmt.Errorf("speech API status %d", response.StatusCode)
	}
	duration, err := wavDuration(audioBytes)
	if err != nil {
		return Synthesis{}, err
	}
	timed := spreadSegments(segments, int(duration*1000))
	return Synthesis{Audio: Audio{Bytes: audioBytes, ContentType: "audio/wav", DurationSeconds: duration}, Segments: timed, Provider: "openai", Model: provider.ttsModel, RequestID: requestID(response)}, nil
}

func requestID(response *http.Response) string {
	if value := response.Header.Get("x-request-id"); value != "" {
		return value
	}
	return uuid.NewString()
}

func wavDuration(data []byte) (float64, error) {
	if len(data) < 44 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, errors.New("speech API did not return a valid WAV")
	}
	byteRate := binary.LittleEndian.Uint32(data[28:32])
	if byteRate == 0 {
		return 0, errors.New("WAV byte rate is zero")
	}
	return float64(len(data)-44) / float64(byteRate), nil
}
func spreadSegments(segments []Segment, totalMS int) []Segment {
	if len(segments) == 0 {
		return nil
	}
	weight := 0
	for _, segment := range segments {
		weight += max(1, len([]rune(segment.Text)))
	}
	result := make([]Segment, len(segments))
	position := 0
	for index, segment := range segments {
		duration := totalMS * max(1, len([]rune(segment.Text))) / weight
		if index == len(segments)-1 {
			duration = totalMS - position
		}
		segment.StartMS = position
		segment.EndMS = max(position+1, position+duration)
		position = segment.EndMS
		result[index] = segment
	}
	return result
}
