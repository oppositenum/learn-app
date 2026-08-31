package speech

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
)

type Handler struct {
	service *Service
	pool    *pgxpool.Pool
	usage   ai.UsageRecorder
}

func NewHandler(service *Service, pool *pgxpool.Pool, usage ai.UsageRecorder) *Handler {
	return &Handler{service: service, pool: pool, usage: usage}
}

func (handler *Handler) Transcribe(writer http.ResponseWriter, request *http.Request) {
	principal, _ := auth.PrincipalFromContext(request.Context())
	userID, err := uuid.Parse(principal.UserID)
	if err != nil {
		http.Error(writer, "invalid principal", 401)
		return
	}
	if err := request.ParseMultipartForm(12 << 20); err != nil {
		http.Error(writer, "invalid audio upload", 400)
		return
	}
	file, header, err := request.FormFile("audio")
	if err != nil {
		http.Error(writer, "audio is required", 400)
		return
	}
	defer file.Close()
	audioBytes, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		http.Error(writer, "audio unavailable", 400)
		return
	}
	duration, err := strconv.ParseFloat(request.FormValue("duration_seconds"), 64)
	if err != nil || duration <= 0 || duration > 300 {
		http.Error(writer, "invalid audio duration", 400)
		return
	}
	var studentID uuid.UUID
	if err := handler.pool.QueryRow(request.Context(), `SELECT id FROM students WHERE user_id=$1`, userID).Scan(&studentID); err != nil {
		http.Error(writer, "student unavailable", 404)
		return
	}
	var sessionID *uuid.UUID
	if value := request.FormValue("session_id"); value != "" {
		parsed, err := uuid.Parse(value)
		if err != nil {
			http.Error(writer, "invalid session id", 400)
			return
		}
		var belongs bool
		if err := handler.pool.QueryRow(request.Context(), `SELECT EXISTS(SELECT 1 FROM learning_sessions WHERE id=$1 AND student_id=$2)`, parsed, studentID).Scan(&belongs); err != nil {
			http.Error(writer, "session unavailable", 500)
			return
		}
		if !belongs {
			http.Error(writer, "session not found", 404)
			return
		}
		sessionID = &parsed
	}
	started := time.Now()
	provider, model := handler.service.STTUsageIdentity()
	if provider == "" || model == "" {
		http.Error(writer, "speech billing identity unavailable", http.StatusServiceUnavailable)
		return
	}
	guard, ok := handler.usage.(ai.PriceGuard)
	if !ok {
		http.Error(writer, "usage price guard unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := guard.EnsurePrice(request.Context(), provider, model, started); err != nil {
		http.Error(writer, "speech price unavailable", http.StatusServiceUnavailable)
		return
	}
	transcript, err := handler.service.Capture(request.Context(), Audio{Bytes: audioBytes, ContentType: header.Header.Get("Content-Type"), DurationSeconds: duration})
	if err != nil {
		http.Error(writer, "transcription failed", 502)
		return
	}
	if handler.usage == nil {
		http.Error(writer, "usage accounting unavailable", 500)
		return
	}
	sessionValue := ""
	if sessionID != nil {
		sessionValue = sessionID.String()
	}
	if err := handler.usage.RecordAIUsage(request.Context(), ai.UsageRecord{RequestID: transcript.RequestID, StudentID: studentID.String(), SessionID: sessionValue, Purpose: ai.PurposeSTTTranscription, Latency: time.Since(started), CreatedAt: started, Usage: ai.ModelUsage{Provider: transcript.Provider, Model: transcript.Model, AudioInputSeconds: strconv.FormatFloat(duration, 'f', 3, 64)}}); err != nil {
		http.Error(writer, "usage accounting failed", 500)
		return
	}
	_, err = handler.pool.Exec(request.Context(), `INSERT INTO speech_inputs(id,student_id,session_id,provider,model,transcript,duration_seconds,storage_status)VALUES($1,$2,$3,$4,$5,$6,$7,'DELETED')`, uuid.New(), studentID, sessionID, transcript.Provider, transcript.Model, transcript.Text, duration)
	if err != nil {
		http.Error(writer, "transcript persistence failed", 500)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{"transcript": transcript.Text, "requires_confirmation": true, "raw_audio_storage": "DELETED"})
}
