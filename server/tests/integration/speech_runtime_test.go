package integration

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type runtimeSpeechProvider struct{}

func (runtimeSpeechProvider) STTUsageIdentity() (string, string) {
	return "test", "stt-v1"
}

func (runtimeSpeechProvider) TTSUsageIdentity() (string, string) {
	return "test", "tts-v1"
}

func (runtimeSpeechProvider) Transcribe(context.Context, speech.Audio) (speech.Transcript, error) {
	return speech.Transcript{Text: "我先减去固定费用", Provider: "test", Model: "stt-v1", RequestID: "stt-runtime-1"}, nil
}

func (runtimeSpeechProvider) Synthesize(context.Context, string, []speech.Segment) (speech.Synthesis, error) {
	return speech.Synthesis{}, nil
}

type guardedSpeechProvider struct{ calls int }

func (provider *guardedSpeechProvider) STTUsageIdentity() (string, string) {
	return "test", "unpriced-stt"
}

func (provider *guardedSpeechProvider) TTSUsageIdentity() (string, string) {
	return "test", "unpriced-tts"
}

func (provider *guardedSpeechProvider) Transcribe(context.Context, speech.Audio) (speech.Transcript, error) {
	provider.calls++
	return speech.Transcript{Text: "不应调用", Provider: "test", Model: "unpriced-stt", RequestID: "unpriced-stt-1"}, nil
}

func (provider *guardedSpeechProvider) Synthesize(context.Context, string, []speech.Segment) (speech.Synthesis, error) {
	return speech.Synthesis{}, nil
}

func TestStudentSTTPriceGuardBlocksProviderBeforeRequest(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	provider := &guardedSpeechProvider{}
	service, err := speech.NewService(provider, provider)
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Speech:       speech.NewHandler(service, pool, usage.NewRecorder(pool)),
	})
	response := performAudioUpload(t, router, fixture.studentToken, fixture.sessionID, "UNPRICED_AUDIO")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unpriced STT status=%d body=%s", response.Code, response.Body.String())
	}
	var usageRows, speechRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speech_inputs`).Scan(&speechRows); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 || usageRows != 0 || speechRows != 0 {
		t.Fatalf("unpriced STT calls=%d usage=%d speech=%d", provider.calls, usageRows, speechRows)
	}
}

func TestStudentSTTMetersUsageDeletesRawAudioAndChecksSessionOwnership(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd,audio_input_price_per_minute_usd) VALUES($1,'test','stt-v1',now()-interval '1 day',0,0,0,0.06)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	provider := runtimeSpeechProvider{}
	service, err := speech.NewService(provider, provider)
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Speech:       speech.NewHandler(service, pool, usage.NewRecorder(pool)),
	})
	rawCanary := "RAW_CHILD_AUDIO_MUST_NOT_BE_STORED"
	response := performAudioUpload(t, router, fixture.studentToken, fixture.sessionID, rawCanary)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"requires_confirmation":true`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"raw_audio_storage":"DELETED"`)) {
		t.Fatalf("STT response=%d %s", response.Code, response.Body.String())
	}

	var storage, transcript, duration string
	if err := pool.QueryRow(ctx, `SELECT storage_status,transcript,duration_seconds::text FROM speech_inputs WHERE student_id=$1 AND session_id=$2`, fixture.studentID, fixture.sessionID).Scan(&storage, &transcript, &duration); err != nil {
		t.Fatal(err)
	}
	if storage != "DELETED" || transcript != "我先减去固定费用" || duration != "2.500" || transcript == rawCanary {
		t.Fatalf("speech input storage=%s transcript=%q duration=%s", storage, transcript, duration)
	}
	var usageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE request_id='stt-runtime-1' AND student_id=$1 AND session_id=$2 AND purpose='STT_TRANSCRIPTION' AND audio_input_seconds=2.500 AND price_catalog_id IS NOT NULL`, fixture.studentID, fixture.sessionID).Scan(&usageCount); err != nil || usageCount != 1 {
		t.Fatalf("STT usage=%d err=%v", usageCount, err)
	}
	var binaryColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='speech_inputs' AND data_type='bytea'`).Scan(&binaryColumns); err != nil || binaryColumns != 0 {
		t.Fatalf("speech_inputs raw binary columns=%d err=%v", binaryColumns, err)
	}

	foreignSession := uuid.New()
	otherStudent := uuid.New()
	otherUser := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name)VALUES($1,'STUDENT','另一个孩子')`, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level)VALUES($1,$2,7)`, otherStudent, otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state) VALUES($1,$2,'00000000-0000-4000-8000-000000000001',$3,'ACTIVE',20,'ASK')`, foreignSession, otherStudent, fixture.releasedQuestionID); err != nil {
		t.Fatal(err)
	}
	response = performAudioUpload(t, router, fixture.studentToken, foreignSession, rawCanary)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign session STT status=%d body=%s", response.Code, response.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE request_id='stt-runtime-1'`).Scan(&usageCount); err != nil || usageCount != 1 {
		t.Fatalf("foreign session reached provider, usage=%d err=%v", usageCount, err)
	}
}

func performAudioUpload(t *testing.T, handler http.Handler, token string, sessionID uuid.UUID, raw string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("audio", "answer.webm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	_ = writer.WriteField("duration_seconds", "2.5")
	_ = writer.WriteField("session_id", sessionID.String())
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/student/speech/transcriptions", &body)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
