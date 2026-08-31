package parent

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type LiveHandler struct {
	repository *Repository
}

func NewLiveHandler(repository *Repository) *LiveHandler {
	return &LiveHandler{repository: repository}
}

func (handler *LiveHandler) GetSession(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !ok || principal.Role != auth.RoleParent {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	parentID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	studentID, err := uuid.Parse(request.PathValue("student_id"))
	if err != nil {
		http.Error(writer, "invalid student id", http.StatusBadRequest)
		return
	}
	sessionID, err := uuid.Parse(request.PathValue("session_id"))
	if err != nil {
		http.Error(writer, "invalid session id", http.StatusBadRequest)
		return
	}

	allowed, err := handler.repository.CanSupervise(request.Context(), parentID, studentID)
	if err != nil {
		http.Error(writer, "supervision unavailable", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}

	live, err := handler.repository.LiveSession(request.Context(), studentID, sessionID)
	if errors.Is(err, ErrLiveSessionNotFound) {
		http.Error(writer, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "session unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(live)
}
