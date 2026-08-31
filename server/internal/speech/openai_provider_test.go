package speech

import (
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProviderUsesServerSideAudioEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing server authorization")
		}
		switch request.URL.Path {
		case "/audio/transcriptions":
			if err := request.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			if request.FormValue("model") != "stt-model" {
				t.Fatalf("STT model=%s", request.FormValue("model"))
			}
			writer.Header().Set("x-request-id", "stt-req")
			_, _ = writer.Write([]byte(`{"text":"识别后的回答"}`))
		case "/audio/speech":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["model"] != "tts-model" || body["voice"] != "cedar" || body["response_format"] != "wav" {
				t.Fatalf("TTS body=%v", body)
			}
			writer.Header().Set("x-request-id", "tts-req")
			_, _ = writer.Write(testWAV(48000))
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(server.Client(), server.URL, "secret", "stt-model", "tts-model", "cedar")
	if err != nil {
		t.Fatal(err)
	}
	transcript, err := provider.Transcribe(t.Context(), Audio{Bytes: []byte("audio"), DurationSeconds: 1})
	if err != nil || transcript.Text != "识别后的回答" || transcript.RequestID != "stt-req" {
		t.Fatalf("transcript=%+v err=%v", transcript, err)
	}
	synthesis, err := provider.Synthesize(t.Context(), "第一句。第二句。", []Segment{{ID: "1", Text: "第一句。"}, {ID: "2", Text: "第二句。"}})
	if err != nil {
		t.Fatal(err)
	}
	if synthesis.Audio.DurationSeconds != 1 || len(synthesis.Segments) != 2 || synthesis.Segments[1].EndMS != 1000 {
		t.Fatalf("synthesis=%+v", synthesis)
	}
}

func testWAV(dataBytes int) []byte {
	result := make([]byte, 44+dataBytes)
	copy(result[0:4], "RIFF")
	copy(result[8:12], "WAVE")
	binary.LittleEndian.PutUint32(result[28:32], 48000)
	return result
}
