package student

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
)

type questionResponse struct {
	Question content.QuestionPublic `json:"question"`
}

type QuestionHandler struct {
	questions content.PublicQuestionReader
}

func NewQuestionHandler(questions content.PublicQuestionReader) *QuestionHandler {
	return &QuestionHandler{questions: questions}
}

func (handler *QuestionHandler) Get(writer http.ResponseWriter, request *http.Request) {
	questionID, err := uuid.Parse(request.PathValue("id"))
	if err != nil {
		http.Error(writer, "invalid question id", http.StatusBadRequest)
		return
	}

	question, err := handler.questions.ReleasedPublicQuestion(request.Context(), questionID)
	if errors.Is(err, content.ErrQuestionNotFound) {
		http.Error(writer, "question not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(writer, "question unavailable", http.StatusInternalServerError)
		return
	}

	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(questionResponse{Question: question})
}
