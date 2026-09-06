package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type responseQueueServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	outputs map[string][]string
	calls   map[string]int
}

func newResponseQueueServer(t *testing.T, outputs map[string][]string) *responseQueueServer {
	t.Helper()
	queue := &responseQueueServer{outputs: outputs, calls: map[string]int{}}
	queue.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/responses" {
			http.NotFound(writer, request)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		queue.mu.Lock()
		index := queue.calls[body.Model]
		queue.calls[body.Model]++
		modelOutputs := queue.outputs[body.Model]
		queue.mu.Unlock()
		if index >= len(modelOutputs) {
			t.Errorf("unexpected provider call model=%s index=%d", body.Model, index)
			http.Error(writer, "unexpected provider call", http.StatusInternalServerError)
			return
		}
		response := map[string]any{
			"id":    fmt.Sprintf("resp-%s-%d", body.Model, index+1),
			"model": body.Model,
			"output": []any{map[string]any{
				"type":    "message",
				"content": []any{map[string]any{"type": "output_text", "text": modelOutputs[index]}},
			}},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 25, "input_tokens_details": map[string]any{"cached_tokens": 20}},
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}))
	t.Cleanup(queue.server.Close)
	return queue
}

func (queue *responseQueueServer) callCount(model string) int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.calls[model]
}

type forbiddenAuditVoice struct{ calls int }

func (voice *forbiddenAuditVoice) VoiceUsageIdentity() (string, string) { return "test", "tts-v1" }
func (voice *forbiddenAuditVoice) Explain(context.Context, string) (classroom.VoiceResult, error) {
	voice.calls++
	return classroom.VoiceResult{}, errors.New("TTS must not run before Tutor output audit")
}

func configureAuditedCodexAgent(t *testing.T, pool *pgxpool.Pool, queue *responseQueueServer, tutorModel, reviewerModel string) ai.TeachingAgent {
	t.Helper()
	recorder := usage.NewRecorder(pool)
	tutorClient, err := ai.NewOpenAIResponsesClient(queue.server.Client(), queue.server.URL, "test-key", tutorModel)
	if err != nil {
		t.Fatal(err)
	}
	reviewerClient, err := ai.NewOpenAIResponsesClient(queue.server.Client(), queue.server.URL, "test-key", reviewerModel)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := tutoraudit.NewOpenAIReviewer(reviewerClient.WithUsageRecorder(recorder), "openai", reviewerModel)
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := tutoraudit.NewService(
		"openai:"+tutorModel,
		"openai:"+reviewerModel,
		reviewer,
		tutoraudit.NewPostgresRecorder(pool),
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(tutorClient.WithUsageRecorder(recorder), auditor)
	if err != nil {
		t.Fatal(err)
	}
	return agent
}

func insertAuditPrices(t *testing.T, ctx context.Context, pool *pgxpool.Pool, models ...string) {
	t.Helper()
	for _, model := range models {
		if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai',$2,now()-interval '1 day',1,0.5,2)`, uuid.New(), model); err != nil {
			t.Fatal(err)
		}
	}
}

type protectedClassroomSnapshot struct {
	state, responseID                                    string
	fails, assistance, version, studentAnswers, analyses int
	tutorTurns, aiEvents, speech, segments               int
	plans, reviews, skills                               string
}

func readProtectedClassroomSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture) protectedClassroomSnapshot {
	t.Helper()
	var snapshot protectedClassroomSnapshot
	if err := pool.QueryRow(ctx, `SELECT current_state,socratic_fail_count,COALESCE(teaching_response_id,''),assistance_level,version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(
		&snapshot.state, &snapshot.fails, &snapshot.responseID, &snapshot.assistance, &snapshot.version,
	); err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM student_answers WHERE session_id=$1`, &snapshot.studentAnswers},
		{`SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1`, &snapshot.analyses},
		{`SELECT count(*) FROM tutor_turns WHERE session_id=$1 AND actor='TUTOR'`, &snapshot.tutorTurns},
		{`SELECT count(*) FROM tutor_events WHERE session_id=$1 AND type='AI_TURN_COMPLETED'`, &snapshot.aiEvents},
		{`SELECT count(*) FROM speech_outputs WHERE session_id=$1`, &snapshot.speech},
		{`SELECT count(*) FROM speech_segments WHERE speech_output_id IN(SELECT id FROM speech_outputs WHERE session_id=$1)`, &snapshot.segments},
	}
	for _, item := range queries {
		if err := pool.QueryRow(ctx, item.query, fixture.sessionID).Scan(item.target); err != nil {
			t.Fatal(err)
		}
	}
	fingerprints := []struct {
		query  string
		target *string
	}{
		{`SELECT md5(COALESCE(string_agg(block::text,',' ORDER BY block.id::text),'')) FROM learning_plan_blocks block JOIN learning_plans plan ON plan.id=block.plan_id WHERE plan.student_id=$1`, &snapshot.plans},
		{`SELECT md5(COALESCE(string_agg(item::text,',' ORDER BY item.id::text),'')) FROM review_queue item WHERE item.student_id=$1`, &snapshot.reviews},
		{`SELECT md5(COALESCE(string_agg(skill::text,',' ORDER BY skill.knowledge_point_id::text),'')) FROM student_skill_states skill WHERE skill.student_id=$1`, &snapshot.skills},
	}
	for _, item := range fingerprints {
		if err := pool.QueryRow(ctx, item.query, fixture.studentID).Scan(item.target); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func TestB3BTutorOutputReviewRejectsBeforeClassroomRealtimeAndVoiceMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='ANALOGY',socratic_fail_count=3,assistance_level=2,version=9 WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tutorModel, reviewerModel := "b3b-tutor", "b3b-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newResponseQueueServer(t, map[string][]string{
		tutorModel: {
			`{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.98,"error_type":"FIXED_COST_IGNORED","misconceptions":["FIXED_COST_IGNORED"],"core_ability_signals":[],"emotion_signal":"NEUTRAL","engagement":"NORMAL","recommended_action":"VOICE_EXPLAIN","safe_to_increase_difficulty":false}`,
			`{"message":"先把固定费用和饮料费用分开。","action":"VOICE_EXPLAIN","answer_revealed":false,"segments":[{"id":"s1","text":"先分开两类费用。"}]}`,
		},
		reviewerModel: {`{"result":"REJECT","no_answer_leak":false,"reason_codes":["EQUIVALENT_ANSWER"]}`},
	})
	hub := realtime.NewHub()
	studentEvents, stop := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stop()
	voice := &forbiddenAuditVoice{}
	service := classroom.NewService(pool, hub, voice, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "wrong runtime answer"})
	if response.Code != http.StatusInternalServerError || response.Body.String() != "answer could not be processed\n" {
		t.Fatalf("rejected response=%d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), fixture.privateCanary) || strings.Contains(strings.ToLower(response.Body.String()), "correct_answer") {
		t.Fatalf("student-visible rejection leaked private answer: %s", response.Body.String())
	}
	after := readProtectedClassroomSnapshot(t, ctx, pool, fixture)
	if after != before {
		t.Fatalf("rejected output mutated protected classroom state:\nbefore=%+v\nafter=%+v", before, after)
	}
	if voice.calls != 0 {
		t.Fatalf("TTS calls=%d want=0", voice.calls)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("rejected output was published to Student WebSocket: %s", event)
	default:
	}

	var auditRows, usageRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='PASS' AND reviewer_result='REJECT' AND final_result='REJECT' AND reason_code='REVIEWER_REJECTED'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose IN('ANSWER_ANALYSIS','EXPLANATION','TUTOR_OUTPUT_REVIEW') AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 || usageRows != 3 {
		t.Fatalf("rejection persistence audits=%d priced_usage=%d", auditRows, usageRows)
	}
	var reviewerCost string
	if err := pool.QueryRow(ctx, `SELECT estimated_cost_usd::text FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewerCost); err != nil {
		t.Fatal(err)
	}
	if reviewerCost != "0.000140000" {
		t.Fatalf("reviewer cost=%s want=0.000140000", reviewerCost)
	}
}

func TestB3BInvalidReviewSchemaFailsClosedAfterPricedUsage(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3b-schema-tutor", "b3b-schema-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newResponseQueueServer(t, map[string][]string{
		tutorModel:    {`{"message":"先找出固定费用。","action":"HINT","answer_revealed":false,"segments":[]}`},
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"unexpected":true}`},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestSupport(ctx, studentUserID, fixture.sessionID, classroom.SupportHint); err == nil {
		t.Fatal("invalid review schema unexpectedly passed")
	}
	var usageRows, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW' AND estimated_cost_usd=0.000140000`, fixture.sessionID).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND reviewer_result='INVALID_SCHEMA' AND final_result='REJECT'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if usageRows != 1 || auditRows != 1 {
		t.Fatalf("invalid-schema evidence usage=%d audits=%d", usageRows, auditRows)
	}
}

func TestB3BDeterministicGateRejectsSelfReportedSafeAnswerAfterIndependentReview(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3b-leak-tutor", "b3b-leak-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newResponseQueueServer(t, map[string][]string{
		tutorModel:    {`{"message":"原题答案是` + fixture.privateCanary + `。","action":"HINT","answer_revealed":false,"segments":[]}`},
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"]}`},
	})
	hub := realtime.NewHub()
	studentEvents, stop := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stop()
	service := classroom.NewService(pool, hub, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("deterministic rejection response=%d %q", response.Code, response.Body.String())
	}
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("deterministic rejection mutated classroom:\nbefore=%+v\nafter=%+v", before, after)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("deterministically rejected output reached Student WebSocket: %s", event)
	default:
	}
	var auditRows, usageRows, forbiddenAuditColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='REJECT' AND reviewer_result='PASS' AND final_result='REJECT' AND reason_code='DETERMINISTIC_ANSWER_MATCH'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose IN('SOCRATIC_TURN','TUTOR_OUTPUT_REVIEW') AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tutor_output_audits' AND column_name IN('message','candidate','student_answer','answer_text','correct_answer','full_solution','teacher_reference_answer')`).Scan(&forbiddenAuditColumns); err != nil {
		t.Fatal(err)
	}
	if auditRows != 1 || usageRows != 2 || forbiddenAuditColumns != 0 || queue.callCount(reviewerModel) != 1 {
		t.Fatalf("dual-gate evidence audits=%d usage=%d forbidden_columns=%d reviewer_calls=%d", auditRows, usageRows, forbiddenAuditColumns, queue.callCount(reviewerModel))
	}
}

func TestB3BUnpricedReviewerIsBlockedBeforeNetworkAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3b-priced-tutor", "b3b-unpriced-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel)
	queue := newResponseQueueServer(t, map[string][]string{
		tutorModel:    {`{"message":"先找出固定费用。","action":"HINT","answer_revealed":false,"segments":[]}`},
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"]}`},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestSupport(ctx, studentUserID, fixture.sessionID, classroom.SupportHint); err == nil {
		t.Fatal("unpriced reviewer unexpectedly passed")
	}
	var reviewUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND reviewer_result='ERROR' AND final_result='REJECT'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(reviewerModel) != 0 || reviewUsage != 0 || auditRows != 1 {
		t.Fatalf("unpriced reviewer calls=%d usage=%d audits=%d", queue.callCount(reviewerModel), reviewUsage, auditRows)
	}
}
