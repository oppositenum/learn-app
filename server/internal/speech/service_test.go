package speech

import (
	"context"
	"testing"
)

type fakeSTT struct{}

func (fakeSTT) STTUsageIdentity() (string, string) { return "test", "stt-test" }

func (fakeSTT) Transcribe(context.Context, Audio) (Transcript, error) {
	return Transcript{Text: "我先减去配送费"}, nil
}

type fakeTTS struct{}

func (fakeTTS) TTSUsageIdentity() (string, string) { return "test", "tts-test" }

func (fakeTTS) Synthesize(_ context.Context, _ string, segments []Segment) (Synthesis, error) {
	return Synthesis{Segments: segments}, nil
}

func TestSpeechShellRequiresConfirmationReadyTranscriptAndTimedSegments(t *testing.T) {
	service, err := NewService(fakeSTT{}, fakeTTS{})
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := service.Capture(context.Background(), Audio{Bytes: []byte("audio"), DurationSeconds: 1.2})
	if err != nil || transcript.Text == "" {
		t.Fatalf("capture = %+v, %v", transcript, err)
	}
	segments := []Segment{{ID: "seg-1", Text: "先看总价。", StartMS: 0, EndMS: 1200}, {ID: "seg-2", Text: "再减配送费。", StartMS: 1200, EndMS: 2600}}
	result, err := service.Explain(context.Background(), "先看总价。再减配送费。", segments)
	if err != nil || len(result.Segments) != 2 || ActiveSegment(result.Segments, 1500) != 1 {
		t.Fatalf("explain = %+v, %v", result, err)
	}
}

func TestSpeechShellRejectsOverlappingTimings(t *testing.T) {
	service, _ := NewService(fakeSTT{}, fakeTTS{})
	_, err := service.Explain(context.Background(), "text", []Segment{{ID: "a", Text: "a", StartMS: 0, EndMS: 100}, {ID: "b", Text: "b", StartMS: 99, EndMS: 200}})
	if err == nil {
		t.Fatal("overlapping segments were accepted")
	}
}
