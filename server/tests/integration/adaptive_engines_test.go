package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	parentrepo "github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/speech"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestPostgresVersionedUsageAccounting(t *testing.T) {
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	pool := isolatedPool(t, ctx, databaseURL)
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	oldID, currentID := uuid.New(), uuid.New()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	_, err := pool.Exec(ctx, `
INSERT INTO ai_price_catalog
 (id, provider, model, effective_from, effective_to, input_price_per_million_usd, cached_input_price_per_million_usd, output_price_per_million_usd)
VALUES ($1, 'openai', 'test-model', $3::timestamptz - interval '30 days', $3::timestamptz - interval '1 day', 99, 99, 99),
       ($2, 'openai', 'test-model', $3::timestamptz - interval '1 day', NULL, 2.5, 1, 10)`, oldID, currentID, now)
	if err != nil {
		t.Fatal(err)
	}
	recorder := usage.NewRecorder(pool)
	if err := recorder.EnsurePrice(ctx, "openai", "test-model", now); err != nil {
		t.Fatalf("current catalog should be effective: %v", err)
	}
	if err := recorder.EnsurePrice(ctx, "openai", "test-model", now.AddDate(-1, 0, 0)); !errors.Is(err, usage.ErrPriceNotFound) {
		t.Fatalf("price before first effective date = %v", err)
	}
	if err := recorder.EnsurePrice(ctx, "openai", "missing-model", now); !errors.Is(err, usage.ErrPriceNotFound) {
		t.Fatalf("missing model price = %v", err)
	}
	err = recorder.RecordAIUsage(ctx, ai.UsageRecord{RequestID: "req-1", Purpose: ai.PurposeSocraticTurn, CreatedAt: now, Usage: ai.ModelUsage{Provider: "openai", Model: "test-model", InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 200}})
	if err != nil {
		t.Fatalf("record usage: %v", err)
	}
	var catalogID uuid.UUID
	var cost string
	if err := pool.QueryRow(ctx, `SELECT price_catalog_id, estimated_cost_usd::text FROM ai_usage_records WHERE request_id = 'req-1'`).Scan(&catalogID, &cost); err != nil {
		t.Fatal(err)
	}
	if catalogID != currentID || cost != "0.003900000" {
		t.Fatalf("catalog/cost = %s/%s", catalogID, cost)
	}
	if err := recorder.RecordAIUsage(ctx, ai.UsageRecord{RequestID: "req-1", Purpose: ai.PurposeSocraticTurn, CreatedAt: now, Usage: ai.ModelUsage{Provider: "openai", Model: "test-model"}}); err != nil {
		t.Fatalf("idempotent insert: %v", err)
	}
	var count int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE request_id = 'req-1'`).Scan(&count)
	if count != 1 {
		t.Fatalf("usage request count = %d", count)
	}
}

type meteredVoice struct{}

func (meteredVoice) VoiceUsageIdentity() (string, string) { return "test", "tts-v1" }

func (meteredVoice) Explain(context.Context, string) (classroom.VoiceResult, error) {
	time.Sleep(2 * time.Millisecond)
	return classroom.VoiceResult{Provider: "test", Model: "tts-v1", RequestID: "tts-e2e-1", DurationSeconds: "6", AudioDataURL: "data:audio/wav;base64,UklGRg==", Segments: []speech.Segment{{ID: "seg-1", Text: "先分开固定量。", StartMS: 0, EndMS: 3000}, {ID: "seg-2", Text: "再回到原题。", StartMS: 3000, EndMS: 6000}}}, nil
}

type trackingVoice struct{ calls int }

func (voice *trackingVoice) VoiceUsageIdentity() (string, string) {
	return "test", "unpriced-tts"
}

func (voice *trackingVoice) Explain(context.Context, string) (classroom.VoiceResult, error) {
	voice.calls++
	return classroom.VoiceResult{Provider: "test", Model: "unpriced-tts", RequestID: "tts-unpriced-1", DurationSeconds: "3"}, nil
}

func TestVoiceExplanationDoesNotMutateClassroomWhenUsageCannotBePriced(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='ANALOGY',socratic_fail_count=3,version=9 WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	voice := &trackingVoice{}
	service := classroom.NewService(pool, nil, voice, usage.NewRecorder(pool))
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, studentUserID, fixture.sessionID, "wrong"); err == nil {
		t.Fatal("unpriced TTS response unexpectedly succeeded")
	}
	var state string
	var version int64
	var answers, outputs, usageRows int
	if err := pool.QueryRow(ctx, `SELECT current_state,version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speech_outputs WHERE session_id=$1`, fixture.sessionID).Scan(&outputs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE request_id='tts-unpriced-1'`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if voice.calls != 0 || state != "ANALOGY" || version != 9 || answers != 1 || outputs != 0 || usageRows != 0 {
		t.Fatalf("unpriced TTS calls=%d state=%s version=%d answers=%d outputs=%d usage=%d", voice.calls, state, version, answers, outputs, usageRows)
	}
}

type e2eTeachingAgent struct{}

func (e2eTeachingAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	return ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .97, ErrorType: "FIXED_COMPONENT_OMITTED", Misconceptions: []string{"FIXED_COST_IGNORED"}, EmotionSignal: "NEUTRAL", Engagement: "NORMAL", RecommendedAction: tutor.StateVoiceExplain}, nil
}
func e2eTurn(request ai.GenerateTurnRequest) ai.TutorTurn {
	return ai.TutorTurn{Message: "受限 TeachingAgent 教学动作：" + string(request.TutorDecision.NextState), Action: request.TutorDecision.NextState, ResponseID: uuid.NewString()}
}
func (e2eTeachingAgent) GenerateTurn(_ context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	return e2eTurn(request), nil
}
func (e2eTeachingAgent) GenerateAnalogy(_ context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return e2eTurn(ai.GenerateTurnRequest(request)), nil
}
func (e2eTeachingAgent) GenerateParallelExample(_ context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return e2eTurn(ai.GenerateTurnRequest(request)), nil
}
func (e2eTeachingAgent) GenerateExplanation(_ context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return e2eTurn(ai.GenerateTurnRequest(request)), nil
}

func TestFullStudentParentOwnerE2E(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET socratic_fail_count=0,current_state='ASK',assistance_level=0,evidence_form='REVIEW' WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT knowledge_point_id FROM questions WHERE id=$1`, fixture.releasedQuestionID).Scan(&knowledgePointID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_skill_states(student_id,knowledge_point_id,state,independent_successes,life_context_successes,variant_successes,textbook_successes,next_review_at) VALUES($1,$2,'REVIEW_DUE',3,1,1,1,now())`, fixture.studentID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	priceID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd,audio_output_price_per_minute_usd) VALUES($1,'test','tts-v1',now()-interval '1 day',0,0,0,0.12)`, priceID); err != nil {
		t.Fatal(err)
	}
	ownerToken := seedOwner(t, ctx, pool)
	hub := realtime.NewHub()
	parents := parentrepo.NewRepository(pool)
	recorder := usage.NewRecorder(pool)
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, hub, meteredVoice{}, recorder, plannerService).WithTeachingAgent(e2eTeachingAgent{})
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, PublicQuestions: content.NewRepository(pool), Parents: parents, Realtime: realtime.NewWebSocketHandler(hub, realtime.DatabaseAuthorizer(pool)), Classroom: classroom.NewHandler(service, pool, parents, plannerService)})
	studentEvents, stopStudent := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stopStudent()
	parentEvents, stopParent := hub.Subscribe(fixture.studentID.String(), auth.RoleParent)
	defer stopParent()
	actions := []string{"PROBE", "SCAFFOLD", "ANALOGY", "VOICE_EXPLAIN"}
	for index, want := range actions {
		response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "wrong"})
		if response.Code != 200 {
			t.Fatalf("wrong answer %d: %d %s", index+1, response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), fixture.privateCanary) {
			t.Fatalf("student HTTP leaked answer: %s", response.Body.String())
		}
		var result classroom.SubmitResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if string(result.Action) != want {
			t.Fatalf("action %d = %s want %s", index+1, result.Action, want)
		}
		if !strings.Contains(result.Message, "受限 TeachingAgent 教学动作") {
			t.Fatalf("runtime TeachingAgent message missing: %+v", result)
		}
		if want == "VOICE_EXPLAIN" && len(result.VoiceSegments) != 2 {
			t.Fatalf("voice segments = %+v", result.VoiceSegments)
		}
		studentEvent := awaitEventType(t, studentEvents, "TUTOR_ACTION_SELECTED")
		parentEvent := awaitEventType(t, parentEvents, "TUTOR_ACTION_SELECTED")
		if strings.Contains(string(studentEvent), fixture.privateCanary) || strings.Contains(string(studentEvent), "correct_answer") {
			t.Fatalf("student realtime leaked private answer: %s", studentEvent)
		}
		if !strings.Contains(string(parentEvent), fixture.privateCanary) {
			t.Fatalf("parent realtime missing answer: %s", parentEvent)
		}
	}
	recoveredVoice := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+fixture.sessionID.String(), fixture.studentToken, nil)
	if recoveredVoice.Code != http.StatusOK || strings.Contains(recoveredVoice.Body.String(), fixture.privateCanary) || !strings.Contains(recoveredVoice.Body.String(), `"voice_audio":"data:audio/wav;base64,UklGRg=="`) {
		t.Fatalf("recoverable Student voice session=%d %s", recoveredVoice.Code, recoveredVoice.Body.String())
	}
	var recoveredSession classroom.StudentSession
	if err := json.Unmarshal(recoveredVoice.Body.Bytes(), &recoveredSession); err != nil || len(recoveredSession.VoiceSegments) != 2 {
		t.Fatalf("recovered voice segments=%d err=%v", len(recoveredSession.VoiceSegments), err)
	}
	blockedAnswer := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": fixture.privateCanary})
	if blockedAnswer.Code != http.StatusConflict {
		t.Fatalf("answer bypassed voice RETURN: %d %s", blockedAnswer.Code, blockedAnswer.Body.String())
	}
	parentVoiceReturn := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/voice/complete", fixture.parentToken, nil)
	if parentVoiceReturn.Code != http.StatusForbidden {
		t.Fatalf("Parent used Student voice return endpoint: %d", parentVoiceReturn.Code)
	}
	voiceReturn := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/voice/complete", fixture.studentToken, nil)
	if voiceReturn.Code != http.StatusOK || !strings.Contains(voiceReturn.Body.String(), `"action":"RETURN"`) || strings.Contains(voiceReturn.Body.String(), fixture.privateCanary) {
		t.Fatalf("voice return=%d %s", voiceReturn.Code, voiceReturn.Body.String())
	}
	studentReturnEvent := awaitEventType(t, studentEvents, "VOICE_EXPLAIN_COMPLETED")
	parentReturnEvent := awaitEventType(t, parentEvents, "VOICE_EXPLAIN_COMPLETED")
	if !strings.Contains(string(studentReturnEvent), `"type":"VOICE_EXPLAIN_COMPLETED"`) || strings.Contains(string(studentReturnEvent), fixture.privateCanary) || strings.Contains(string(studentReturnEvent), "correct_answer") {
		t.Fatalf("unsafe Student voice return event: %s", studentReturnEvent)
	}
	if !strings.Contains(string(parentReturnEvent), `"type":"VOICE_EXPLAIN_COMPLETED"`) || !strings.Contains(string(parentReturnEvent), `"action":"RETURN"`) {
		t.Fatalf("incomplete Parent voice return event: %s", parentReturnEvent)
	}
	var returnedState string
	var returnedQuestion uuid.UUID
	var returnedRounds int
	if err := pool.QueryRow(ctx, `SELECT current_state,current_question_id,socratic_fail_count FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&returnedState, &returnedQuestion, &returnedRounds); err != nil {
		t.Fatal(err)
	}
	if returnedState != "RETURN" || returnedQuestion != fixture.releasedQuestionID || returnedRounds != 3 {
		t.Fatalf("voice return state=%s question=%s rounds=%d", returnedState, returnedQuestion, returnedRounds)
	}
	var misconceptionOccurrences, reviewCount int
	if err := pool.QueryRow(ctx, `SELECT occurrences FROM student_misconceptions sm JOIN misconceptions m ON m.id=sm.misconception_id WHERE sm.student_id=$1 AND sm.knowledge_point_id=$2 AND m.code='FIXED_COST_IGNORED'`, fixture.studentID, knowledgePointID).Scan(&misconceptionOccurrences); err != nil {
		t.Fatalf("misconception persistence: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MISCONCEPTION' AND status='PENDING'`, fixture.studentID, knowledgePointID).Scan(&reviewCount); err != nil {
		t.Fatal(err)
	}
	if misconceptionOccurrences != 4 || reviewCount != 1 {
		t.Fatalf("misconception occurrences/review count = %d/%d", misconceptionOccurrences, reviewCount)
	}
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": fixture.privateCanary})
	if response.Code != 200 {
		t.Fatalf("correct answer: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("correct Student response echoed private canary: %s", response.Body.String())
	}
	var completed classroom.SubmitResult
	if err := json.Unmarshal(response.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	if completed.MasteryState != "UNDERSTOOD" || completed.Energy != 2 || !completed.TomorrowChanged {
		t.Fatalf("completion = %+v", completed)
	}
	studentCompletionEvent := awaitEventType(t, studentEvents, "SESSION_COMPLETED")
	parentCompletionEvent := awaitEventType(t, parentEvents, "SESSION_COMPLETED")
	if strings.Contains(string(studentCompletionEvent), fixture.privateCanary) || strings.Contains(string(studentCompletionEvent), "correct_answer") {
		t.Fatalf("Student completion event leaked private answer: %s", studentCompletionEvent)
	}
	if !strings.Contains(string(parentCompletionEvent), `"action":"COMPLETE"`) || !strings.Contains(string(parentCompletionEvent), `"mastery_state":"UNDERSTOOD"`) || !strings.Contains(string(parentCompletionEvent), `"mastery_score":75`) {
		t.Fatalf("Parent completion event lacks mastery update: %s", parentCompletionEvent)
	}
	response = performJSON(router, http.MethodGet, "/api/v1/student/growth", fixture.studentToken, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"total_energy":2`) {
		t.Fatalf("growth: %d %s", response.Code, response.Body.String())
	}
	response = performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"plans"`) {
		t.Fatalf("tomorrow plan: %d %s", response.Code, response.Body.String())
	}
	var adaptiveBlocks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_plan_blocks b JOIN learning_plans p ON p.id=b.plan_id WHERE p.student_id=$1 AND p.based_on_session_id=$2 AND p.plan_date=(current_date+1) AND b.mode='REVIEW' AND b.reason='spaced_review_due'`, fixture.studentID, fixture.sessionID).Scan(&adaptiveBlocks); err != nil || adaptiveBlocks != 1 {
		t.Fatalf("adaptive tomorrow review blocks=%d err=%v", adaptiveBlocks, err)
	}
	response = performJSON(router, http.MethodPut, "/api/v1/parent/child/"+fixture.studentID.String()+"/preferences", fixture.parentToken, map[string]any{"daily_minutes": 25, "priority_subject_codes": []string{"MATH"}, "review_only": true})
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"answer_controls_available":false`) {
		t.Fatalf("parent preferences: %d %s", response.Code, response.Body.String())
	}
	response = performJSON(router, http.MethodGet, "/api/v1/owner/costs", ownerToken, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "tts-v1") || !strings.Contains(response.Body.String(), "0.012000000") {
		t.Fatalf("owner costs: %d %s", response.Code, response.Body.String())
	}
	var usageCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE request_id='tts-e2e-1' AND price_catalog_id=$1 AND latency_ms>0`, priceID).Scan(&usageCount); err != nil || usageCount != 1 {
		t.Fatalf("TTS usage count=%d err=%v", usageCount, err)
	}
	var segmentCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speech_segments sg JOIN speech_outputs so ON so.id=sg.speech_output_id WHERE so.session_id=$1`, fixture.sessionID).Scan(&segmentCount); err != nil || segmentCount != 2 {
		t.Fatalf("persisted TTS segment count=%d err=%v", segmentCount, err)
	}
	requiredEventTypes := []string{"ANSWER_SUBMITTED", "ANSWER_ANALYZED", "TUTOR_ACTION_SELECTED", "AI_TURN_COMPLETED", "VOICE_EXPLAIN_STARTED", "VOICE_EXPLAIN_COMPLETED", "MASTERY_UPDATED", "REWARD_GRANTED", "SESSION_COMPLETED", "PLAN_MODIFIED"}
	var eventTypeCount int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT type) FROM tutor_events WHERE session_id=$1 AND type=ANY($2)`, fixture.sessionID, requiredEventTypes).Scan(&eventTypeCount); err != nil || eventTypeCount != len(requiredEventTypes) {
		t.Fatalf("runtime lifecycle event types=%d/%d err=%v", eventTypeCount, len(requiredEventTypes), err)
	}
	var unsafeStudentEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_events WHERE session_id=$1 AND (student_payload_json::text ILIKE '%correct_answer%' OR student_payload_json::text ILIKE '%answer_correct%' OR student_payload_json::text ILIKE '%' || $2 || '%')`, fixture.sessionID, fixture.privateCanary).Scan(&unsafeStudentEvents); err != nil || unsafeStudentEvents != 0 {
		t.Fatalf("unsafe Student lifecycle events=%d err=%v", unsafeStudentEvents, err)
	}
}

func seedOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	userID, sessionID := uuid.New(), uuid.New()
	token := "owner-" + uuid.NewString()
	hash := sha256.Sum256([]byte(token))
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name)VALUES($1,'OWNER','测试 Owner')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(id,user_id,token_hash,expires_at)VALUES($1,$2,$3,now()+interval '1 hour')`, sessionID, userID, hash[:]); err != nil {
		t.Fatal(err)
	}
	return token
}
func performJSON(handler http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	var payload strings.Reader
	if body != nil {
		bytes, _ := json.Marshal(body)
		payload = *strings.NewReader(string(bytes))
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestPostgresDemoCurriculumAndReleaseGate(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := pool.Query(ctx, `
SELECT s.code, count(k.id)
FROM subjects s LEFT JOIN knowledge_points k ON k.subject_id=s.id AND k.id::text LIKE '30000000-%'
GROUP BY s.code ORDER BY s.code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var subject string
		var count int
		if err := rows.Scan(&subject, &count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("%s demo knowledge points = %d", subject, count)
		}
		seen++
	}
	if seen != 5 {
		t.Fatalf("subjects checked = %d", seen)
	}
	var incomplete int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT k.id FROM knowledge_points k LEFT JOIN world_connections w ON w.knowledge_point_id=k.id WHERE k.id::text LIKE '30000000-%' GROUP BY k.id HAVING count(w.id) <> 3) x`).Scan(&incomplete); err != nil {
		t.Fatal(err)
	}
	if incomplete != 0 {
		t.Fatalf("knowledge points without three world connections = %d", incomplete)
	}
	var releasedWithGate int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM questions q JOIN content_versions cv ON cv.question_id=q.id AND cv.version=q.content_version JOIN content_validations v ON v.question_id=q.id AND v.content_version=q.content_version AND v.status='PASS' JOIN content_reviews r ON r.question_id=q.id AND r.content_version=q.content_version AND r.schema_version=v.schema_version AND r.result='PASS' WHERE q.id::text LIKE '40000000-%' AND q.status='RELEASED'`).Scan(&releasedWithGate); err != nil {
		t.Fatal(err)
	}
	if releasedWithGate != 15 {
		t.Fatalf("released demo questions with full gate = %d", releasedWithGate)
	}

	questionID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO questions (id,knowledge_point_id,difficulty,question_type,prompt_public,input_schema_json,status,content_version) VALUES ($1,'30000000-0000-4000-8000-000000000001','L1','FREE_TEXT','gate bypass test','{}','DRAFT','bypass-v1')`, questionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE questions SET status='RELEASED' WHERE id=$1`, questionID); err == nil {
		t.Fatal("database allowed DRAFT -> RELEASED bypass")
	}

	releasedID := uuid.MustParse("40000000-0000-4000-8000-000000000001")
	if err := contentpipeline.NewRepository(pool).Quarantine(ctx, releasedID, uuid.Nil, "answer dispute"); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	if _, err := content.NewRepository(pool).ReleasedPublicQuestion(ctx, releasedID); !errors.Is(err, content.ErrQuestionNotFound) {
		t.Fatalf("quarantined question remained in classroom: %v", err)
	}
}

func TestPostgresPlannerUsesCrossSubjectDependency(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	source := uuid.MustParse("30000000-0000-4000-8000-000000000010")
	target := uuid.MustParse("30000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `INSERT INTO student_skill_states(student_id,knowledge_point_id,state,score_internal)VALUES($1,$2,'REGRESSED',20)`, fixture.studentID, source); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	plan, created, err := plannerService.EnsureWithStatus(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("first EnsureWithStatus did not report a created plan")
	}
	if _, createdAgain, err := plannerService.EnsureWithStatus(ctx, fixture.studentID, time.Now(), uuid.Nil); err != nil || createdAgain {
		t.Fatalf("existing plan reported changed=%v err=%v", createdAgain, err)
	}
	found := false
	for _, block := range plan.Blocks {
		if block.KnowledgePointID != nil && *block.KnowledgePointID == target {
			found = true
			if block.Mode != planner.ModeMicroBacktrack || block.OriginalTaskID == nil || *block.OriginalTaskID != source {
				t.Fatalf("cross-subject block=%+v", block)
			}
		}
	}
	if !found {
		t.Fatalf("cross-subject prerequisite was not planned: %+v", plan.Blocks)
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	return value
}

func isolatedPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["timezone"] = "Asia/Shanghai"
	if config.MaxConns < 4 {
		config.MaxConns = 4
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
