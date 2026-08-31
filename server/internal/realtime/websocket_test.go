package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

func TestWebSocketProjectsEventsByRole(t *testing.T) {
	hub := NewHub()
	handler := NewWebSocketHandler(hub, func(_ context.Context, _ auth.Principal, _ string) bool { return true })

	studentServer := websocketTestServer(handler, auth.Principal{UserID: "student-user", Role: auth.RoleStudent})
	defer studentServer.Close()
	parentServer := websocketTestServer(handler, auth.Principal{UserID: "parent-user", Role: auth.RoleParent})
	defer parentServer.Close()

	studentConnection := dialTestWebSocket(t, studentServer.URL, "stu_1")
	defer studentConnection.CloseNow()
	parentConnection := dialTestWebSocket(t, parentServer.URL, "stu_1")
	defer parentConnection.CloseNow()

	time.Sleep(10 * time.Millisecond)
	err := hub.Publish(Event{
		EventID: "evt_1", StudentID: "stu_1", SessionID: "ses_1", Sequence: 1,
		Type: EventAnswerAnalyzed, CreatedAt: time.Now(),
		StudentPayload: json.RawMessage(`{"message":"再想一小步"}`),
		ParentPayload:  json.RawMessage(`{"correct_answer":"10","error_type":"FIXED_COST_IGNORED"}`),
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	studentPayload := readTestWebSocket(t, studentConnection)
	parentPayload := readTestWebSocket(t, parentConnection)
	if strings.Contains(studentPayload, "correct_answer") {
		t.Fatalf("student WebSocket leaked private answer: %s", studentPayload)
	}
	if !strings.Contains(parentPayload, "correct_answer") {
		t.Fatalf("parent WebSocket did not receive supervision payload: %s", parentPayload)
	}
}

func websocketTestServer(handler http.Handler, principal auth.Principal) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.SetPathValue("student_id", strings.TrimPrefix(request.URL.Path, "/ws/"))
		handler.ServeHTTP(writer, request.WithContext(auth.WithPrincipal(request.Context(), principal)))
	}))
}

func dialTestWebSocket(t *testing.T, serverURL, studentID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(serverURL, "http")+"/ws/"+studentID, nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	return connection
}

func readTestWebSocket(t *testing.T, connection *websocket.Conn) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, payload, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read WebSocket: %v", err)
	}
	return string(payload)
}
