package classroom

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
)

type SpeechVoiceAdapter struct{ provider speech.TTSProvider }

func NewSpeechVoiceAdapter(provider speech.TTSProvider) *SpeechVoiceAdapter {
	return &SpeechVoiceAdapter{provider: provider}
}

func (adapter *SpeechVoiceAdapter) VoiceUsageIdentity() (string, string) {
	return adapter.provider.TTSUsageIdentity()
}

func (adapter *SpeechVoiceAdapter) Explain(ctx context.Context, message string) (VoiceResult, error) {
	segments := []speech.Segment{{ID: "voice-1", Text: "先看一个不同数字的例子。"}, {ID: "voice-2", Text: message}, {ID: "voice-3", Text: "现在回到原题，再自己试一次。"}}
	result, err := adapter.provider.Synthesize(ctx, message, segments)
	if err != nil {
		return VoiceResult{}, err
	}
	return VoiceResult{Provider: result.Provider, Model: result.Model, RequestID: result.RequestID, DurationSeconds: fmt.Sprintf("%.3f", result.Audio.DurationSeconds), Segments: result.Segments, AudioDataURL: "data:" + result.Audio.ContentType + ";base64," + base64.StdEncoding.EncodeToString(result.Audio.Bytes)}, nil
}
