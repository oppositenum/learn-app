package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type countingVoiceProvider struct{ calls int }

func (*countingVoiceProvider) VoiceUsageIdentity() (string, string) { return "fixture", "fixture-tts" }
func (provider *countingVoiceProvider) Explain(context.Context, string) (classroom.VoiceResult, error) {
	provider.calls++
	return classroom.VoiceResult{}, nil
}

type materialMutationSnapshot struct {
	answers, analyses, turns, events, speechOutputs, speechSegments int
	version                                                         int64
}

func TestTutorNumberMaterialRejectionPrecedesClassroomRealtimeAndTTS(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','mock-tutor',now()-interval '1 day',1,0.5,2)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	unsafeTurn, _ := json.Marshal(map[string]any{
		"message": "把每杯改成99元再比较。", "action": "ANALOGY", "answer_revealed": false, "segments": []any{},
	})
	providerServer := structuredResponseServer(t, []string{validAnalysisJSON(), string(unsafeTurn)})
	defer providerServer.Close()
	recorder := usage.NewRecorder(pool)
	client, err := ai.NewOpenAIResponsesClient(providerServer.Client(), providerServer.URL, "test-key", "mock-tutor")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(client.WithUsageRecorder(recorder), allowTutorOutputAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	hub := realtime.NewHub()
	studentEvents, stop := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stop()
	voice := &countingVoiceProvider{}
	service := classroom.NewService(pool, hub, voice, recorder).WithTeachingAgent(agent)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	before := readMaterialMutationSnapshot(t, ctx, pool, fixture.sessionID)

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "需要继续分析"})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), ai.TutorOutputRephraseRequiredCode) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	after := readMaterialMutationSnapshot(t, ctx, pool, fixture.sessionID)
	if before != after {
		t.Fatalf("rejected material mutated classroom: before=%+v after=%+v", before, after)
	}
	if voice.calls != 0 {
		t.Fatalf("rejected material reached TTS: calls=%d", voice.calls)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("rejected material published realtime event: %s", event)
	case <-time.After(30 * time.Millisecond):
	}
	var generationUsage, reviewUsage, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose IN('ANSWER_ANALYSIS','SOCRATIC_TURN')`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, fixture.sessionID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if generationUsage != 2 || reviewUsage != 0 || audits != 0 {
		// The integration fixture uses an in-process allow auditor, so the
		// existing gate is crossed without an external review usage or record.
		t.Fatalf("generation_usage=%d review_usage=%d audits=%d", generationUsage, reviewUsage, audits)
	}
}

func readMaterialMutationSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) materialMutationSnapshot {
	t.Helper()
	var snapshot materialMutationSnapshot
	err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM student_answers WHERE session_id=$1),
  (SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1),
  (SELECT count(*) FROM tutor_turns WHERE session_id=$1),
  (SELECT count(*) FROM tutor_events WHERE session_id=$1),
  (SELECT count(*) FROM speech_outputs WHERE session_id=$1),
  (SELECT count(*) FROM speech_segments segment JOIN speech_outputs output ON output.id=segment.speech_output_id WHERE output.session_id=$1),
  (SELECT version FROM learning_sessions WHERE id=$1)`, sessionID).Scan(
		&snapshot.answers, &snapshot.analyses, &snapshot.turns, &snapshot.events,
		&snapshot.speechOutputs, &snapshot.speechSegments, &snapshot.version,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
