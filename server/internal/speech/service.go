package speech

import (
	"context"
	"errors"
)

type Audio struct {
	Bytes           []byte
	ContentType     string
	DurationSeconds float64
}

type Transcript struct {
	Text      string
	Provider  string
	Model     string
	RequestID string
}

type Segment struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	StartMS int    `json:"start_ms"`
	EndMS   int    `json:"end_ms"`
}

type Synthesis struct {
	Audio     Audio
	Segments  []Segment
	Provider  string
	Model     string
	RequestID string
}

type STTProvider interface {
	STTUsageIdentity() (provider, model string)
	Transcribe(ctx context.Context, audio Audio) (Transcript, error)
}

type TTSProvider interface {
	TTSUsageIdentity() (provider, model string)
	Synthesize(ctx context.Context, text string, segments []Segment) (Synthesis, error)
}

type Service struct {
	stt STTProvider
	tts TTSProvider
}

func NewService(stt STTProvider, tts TTSProvider) (*Service, error) {
	if stt == nil || tts == nil {
		return nil, errors.New("STT and TTS providers are required")
	}
	return &Service{stt: stt, tts: tts}, nil
}

func (service *Service) STTUsageIdentity() (provider, model string) {
	return service.stt.STTUsageIdentity()
}

func (service *Service) Capture(ctx context.Context, audio Audio) (Transcript, error) {
	if len(audio.Bytes) == 0 || audio.DurationSeconds <= 0 {
		return Transcript{}, errors.New("non-empty audio with positive duration is required")
	}
	result, err := service.stt.Transcribe(ctx, audio)
	if err != nil {
		return Transcript{}, err
	}
	if result.Text == "" {
		return Transcript{}, errors.New("STT returned an empty transcript")
	}
	return result, nil
}

func (service *Service) Explain(ctx context.Context, text string, segments []Segment) (Synthesis, error) {
	if text == "" || len(segments) == 0 {
		return Synthesis{}, errors.New("text and semantic segments are required")
	}
	for index, segment := range segments {
		if segment.ID == "" || segment.Text == "" || segment.StartMS < 0 || segment.EndMS <= segment.StartMS {
			return Synthesis{}, errors.New("invalid speech segment timing")
		}
		if index > 0 && segment.StartMS < segments[index-1].EndMS {
			return Synthesis{}, errors.New("speech segments overlap")
		}
	}
	return service.tts.Synthesize(ctx, text, segments)
}

func ActiveSegment(segments []Segment, positionMS int) int {
	for index, segment := range segments {
		if positionMS >= segment.StartMS && positionMS < segment.EndMS {
			return index
		}
	}
	return -1
}
