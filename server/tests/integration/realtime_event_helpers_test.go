package integration

import (
	"encoding/json"
	"testing"
	"time"
)

func awaitEventType(t *testing.T, events <-chan []byte, want string) []byte {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed while waiting for %s", want)
			}
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(event, &envelope); err != nil {
				t.Fatalf("decode realtime event: %v", err)
			}
			if envelope.Type == want {
				return event
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for realtime event %s", want)
		}
	}
}
