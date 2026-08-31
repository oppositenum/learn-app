package contentpipeline

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (handler *Handler) GenerationOptions(writer http.ResponseWriter, request *http.Request) {
	options, err := handler.service.GenerationOptions(request.Context())
	if err != nil {
		http.Error(writer, "content generation options unavailable", http.StatusInternalServerError)
		return
	}
	writePipelineJSON(writer, http.StatusOK, options)
}

func (handler *Handler) Generate(writer http.ResponseWriter, request *http.Request) {
	var body GenerateRequest
	if err := decodeStrict(writer, request, &body); err != nil {
		http.Error(writer, "invalid generation request: "+err.Error(), http.StatusBadRequest)
		return
	}
	assets, err := handler.service.GenerateDrafts(request.Context(), body)
	if err != nil {
		writePipelineError(writer, err)
		return
	}
	records := make([]map[string]any, 0, len(assets))
	for _, asset := range assets {
		records = append(records, map[string]any{
			"question_id": asset.QuestionID,
			"status":      asset.Status,
			"prompt":      asset.PromptPublic,
		})
	}
	writePipelineJSON(writer, http.StatusCreated, map[string]any{"records": records})
}

func (handler *Handler) ImportDraft(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Asset     Asset `json:"asset"`
		Generator struct {
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			RequestID string `json:"request_id"`
		} `json:"generator"`
	}
	if err := decodeStrict(writer, request, &body); err != nil {
		http.Error(writer, "invalid draft asset: "+err.Error(), http.StatusBadRequest)
		return
	}
	err := handler.service.ImportDraft(request.Context(), body.Asset, GenerationMetadata{
		Provider: body.Generator.Provider, Model: body.Generator.Model, RequestID: body.Generator.RequestID,
	})
	if err != nil {
		http.Error(writer, "draft import rejected: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writePipelineJSON(writer, http.StatusCreated, map[string]any{"question_id": body.Asset.QuestionID, "status": Draft})
}

func (handler *Handler) Validate(writer http.ResponseWriter, request *http.Request) {
	questionID, ok := pipelineQuestionID(writer, request)
	if !ok {
		return
	}
	validationID, status, validation, err := handler.service.Validate(request.Context(), questionID)
	if err != nil {
		writePipelineError(writer, err)
		return
	}
	writePipelineJSON(writer, http.StatusOK, map[string]any{"question_id": questionID, "validation_id": validationID, "status": status, "validation": validation})
}

func (handler *Handler) Review(writer http.ResponseWriter, request *http.Request) {
	questionID, ok := pipelineQuestionID(writer, request)
	if !ok {
		return
	}
	reviewID, status, review, err := handler.service.Review(request.Context(), questionID)
	if err != nil {
		writePipelineError(writer, err)
		return
	}
	writePipelineJSON(writer, http.StatusOK, map[string]any{"question_id": questionID, "review_id": reviewID, "status": status, "review": review})
}

func (handler *Handler) Release(writer http.ResponseWriter, request *http.Request) {
	handler.transition(writer, request, false)
}

func (handler *Handler) Quarantine(writer http.ResponseWriter, request *http.Request) {
	handler.transition(writer, request, true)
}

func (handler *Handler) transition(writer http.ResponseWriter, request *http.Request, quarantine bool) {
	questionID, ok := pipelineQuestionID(writer, request)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeStrict(writer, request, &body); err != nil || strings.TrimSpace(body.Reason) == "" {
		http.Error(writer, "a non-empty reason is required", http.StatusBadRequest)
		return
	}
	principal, _ := auth.PrincipalFromContext(request.Context())
	actorID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", http.StatusUnauthorized)
		return
	}
	status := Released
	if quarantine {
		status = Quarantined
		err = handler.service.Quarantine(request.Context(), questionID, actorID, body.Reason)
	} else {
		err = handler.service.Release(request.Context(), questionID, actorID, body.Reason)
	}
	if err != nil {
		writePipelineError(writer, err)
		return
	}
	writePipelineJSON(writer, http.StatusOK, map[string]any{"question_id": questionID, "status": status})
}

func pipelineQuestionID(writer http.ResponseWriter, request *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(request.PathValue("question_id"))
	if err != nil {
		http.Error(writer, "invalid question id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

func decodeStrict(writer http.ResponseWriter, request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func writePipelineError(writer http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, ErrInvalidGenerationRequest) {
		status = http.StatusBadRequest
	} else if errors.Is(err, ErrReviewerUnavailable) || errors.Is(err, ErrGeneratorUnavailable) {
		status = http.StatusServiceUnavailable
	}
	http.Error(writer, err.Error(), status)
}

func writePipelineJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
