package realtime

import (
	"context"
	"net/http"

	"github.com/coder/websocket"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type AuthorizeSubscription func(ctx context.Context, principal auth.Principal, studentID string) bool

type WebSocketHandler struct {
	hub       *Hub
	authorize AuthorizeSubscription
}

func NewWebSocketHandler(hub *Hub, authorize AuthorizeSubscription) *WebSocketHandler {
	return &WebSocketHandler{hub: hub, authorize: authorize}
}

func (handler *WebSocketHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	studentID := request.PathValue("student_id")
	if !ok {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if handler.authorize == nil || !handler.authorize(request.Context(), principal, studentID) {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}

	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*", "127.0.0.1:*"},
	})
	if err != nil {
		return
	}
	defer connection.CloseNow()

	events, unsubscribe := handler.hub.Subscribe(studentID, principal.Role)
	defer unsubscribe()
	for {
		select {
		case <-request.Context().Done():
			_ = connection.Close(websocket.StatusNormalClosure, "request closed")
			return
		case payload, open := <-events:
			if !open {
				return
			}
			if err := connection.Write(request.Context(), websocket.MessageText, payload); err != nil {
				return
			}
		}
	}
}
