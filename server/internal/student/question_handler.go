package student

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
)

type questionResponse struct {
	Question    content.QuestionPublic      `json:"question"`
	Interaction studentinteraction.Material `json:"interaction"`
}

type QuestionHandler struct {
	questions content.PublicQuestionReader
}

func NewQuestionHandler(questions content.PublicQuestionReader) *QuestionHandler {
	return &QuestionHandler{questions: questions}
}

func (handler *QuestionHandler) Get(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !ok {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	studentUserID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	questionID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		http.Error(writer, "invalid question id", http.StatusBadRequest)
		return
	}

	question, err := handler.questions.ReleasedPublicQuestionForStudent(request.Context(), questionID, studentUserID)
	if errors.Is(err, content.ErrQuestionNotFound) {
		http.Error(writer, "question not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "question unavailable", http.StatusInternalServerError)
		return
	}

	interaction := studentinteraction.Resolve(question.Prompt, question.Scene, question.InputSchema)
	if interaction.Fallback {
		question.Scene = json.RawMessage(`{}`)
		question.InputSchema = interaction.AnswerSchema
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(questionResponse{Question: question, Interaction: interaction})
}
