package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	server    *httptest.Server
	mu        sync.Mutex
	responses map[string][]queuedResponse
	calls     map[string]int
	// requests keeps each decoded request body per model, so a test can
	// check what context the provider was actually given.
	requests map[string][]map[string]any
}

type queuedResponse struct {
	status     int
	output     string
	body       string
	retryAfter string
}

func newResponseQueueServer(t *testing.T, outputs map[string][]string) *responseQueueServer {
	t.Helper()
	responses := make(map[string][]queuedResponse, len(outputs))
	for model, modelOutputs := range outputs {
		for _, output := range modelOutputs {
			responses[model] = append(responses[model], queuedResponse{status: http.StatusOK, output: output})
		}
	}
	return newScriptedResponseQueueServer(t, responses)
}

func newScriptedResponseQueueServer(t *testing.T, responses map[string][]queuedResponse) *responseQueueServer {
	t.Helper()
	queue := &responseQueueServer{responses: responses, calls: map[string]int{}, requests: map[string][]map[string]any{}}
	queue.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/responses" {
			http.NotFound(writer, request)
			return
		}
		var decoded map[string]any
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		var body struct{ Model string }
		body.Model, _ = decoded["model"].(string)
		queue.mu.Lock()
		index := queue.calls[body.Model]
		queue.calls[body.Model]++
		queue.requests[body.Model] = append(queue.requests[body.Model], decoded)
		modelResponses := queue.responses[body.Model]
		queue.mu.Unlock()
		if index >= len(modelResponses) {
			t.Errorf("unexpected provider call model=%s index=%d", body.Model, index)
			http.Error(writer, "unexpected provider call", http.StatusInternalServerError)
			return
		}
		queued := modelResponses[index]
		if queued.status != 0 && (queued.status < 200 || queued.status >= 300) {
			if queued.retryAfter != "" {
				writer.Header().Set("Retry-After", queued.retryAfter)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(queued.status)
			_, _ = writer.Write([]byte(queued.body))
			return
		}
		response := map[string]any{
			"id":    fmt.Sprintf("resp-%s-%d", body.Model, index+1),
			"model": body.Model,
			"output": []any{map[string]any{
				"type":    "message",
				"content": []any{map[string]any{"type": "output_text", "text": queued.output}},
			}},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 25, "input_tokens_details": map[string]any{"cached_tokens": 20}},
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}))
	t.Cleanup(queue.server.Close)
	return queue
}

func (queue *responseQueueServer) requestsFor(model string) []map[string]any {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return append([]map[string]any(nil), queue.requests[model]...)
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
	retryingReviewer, err := tutoraudit.NewRetryingReviewer(reviewer)
	if err != nil {
		t.Fatal(err)
	}
	auditor, err := tutoraudit.NewService(
		"openai:"+tutorModel,
		"openai:"+reviewerModel,
		retryingReviewer,
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

func assertMinimalViolationJSON(t *testing.T, raw string, expected []tutoraudit.Violation, forbidden ...string) {
	t.Helper()
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("decode violations_json: %v", err)
	}
	if len(decoded) != len(expected) {
		t.Fatalf("violations_json entries=%d want=%d", len(decoded), len(expected))
	}
	allowedTypes := map[string]bool{"DIRECT_ANSWER": true, "EQUIVALENT_ANSWER": true, "FULL_SOLUTION": true, "SEGMENT_ANSWER_LEAK": true}
	allowedKinds := map[string]bool{"MESSAGE": true, "SEGMENT": true}
	for index, violation := range decoded {
		if len(violation) != 3 {
			t.Fatalf("violation %d keys=%v want exactly three allowlisted keys", index, violation)
		}
		violationType, typeOK := violation["violation_type"].(string)
		payloadKind, kindOK := violation["payload_kind"].(string)
		segmentIndex, indexOK := violation["segment_index"].(float64)
		if !typeOK || !kindOK || !indexOK || !allowedTypes[violationType] || !allowedKinds[payloadKind] {
			t.Fatalf("violation %d contains a non-enum or non-integer value: %v", index, violation)
		}
		if _, ok := violation["violation_type"]; !ok {
			t.Fatalf("violation %d missing violation_type", index)
		}
		if _, ok := violation["payload_kind"]; !ok {
			t.Fatalf("violation %d missing payload_kind", index)
		}
		if _, ok := violation["segment_index"]; !ok {
			t.Fatalf("violation %d missing segment_index", index)
		}
		want := expected[index]
		if violationType != want.ViolationType || payloadKind != want.PayloadKind || int(segmentIndex) != want.SegmentIndex || segmentIndex != float64(int(segmentIndex)) {
			t.Fatalf("violation %d=%v want=%+v", index, violation, want)
		}
	}
	for _, value := range forbidden {
		if strings.Contains(raw, value) {
			t.Fatalf("violations_json contains forbidden free text")
		}
	}
}

func TestB3BTutorOutputReviewRejectsBeforeClassroomRealtimeAndVoiceMutation(t *testing.T) {
	ctx := context.Background()
	var logOutput bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logOutput)
	defer log.SetOutput(previousLogOutput)
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
			`{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.98,"error_type":"FIXED_COST_IGNORED","misconceptions":["FIXED_COST_IGNORED"],"core_ability_signals":[],"emotion_signal":"NEUTRAL","engagement":"NORMAL","recommended_action":"VOICE_EXPLAIN","safe_to_increase_difficulty":false,"weakness_layer":"L2"}`,
			`{"message":"先把固定费用和饮料费用分开。","action":"VOICE_EXPLAIN","answer_revealed":false,"segments":[{"id":"s1","text":"先分开两类费用。"}]}`,
		},
		reviewerModel: {`{"result":"REJECT","no_answer_leak":false,"reason_codes":["EQUIVALENT_ANSWER"],"violations":[{"violation_type":"EQUIVALENT_ANSWER","payload_kind":"MESSAGE","segment_index":-1}]}`},
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
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("rejected response=%d %q", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorOutputRephraseRequiredCode {
		t.Fatalf("rephrase payload=%q decode_error=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{fixture.privateCanary, "wrong runtime answer", "先把固定费用和饮料费用分开", "EQUIVALENT_ANSWER", "correct_answer"} {
		if strings.Contains(response.Body.String(), forbidden) || strings.Contains(logOutput.String(), forbidden) {
			t.Fatalf("rejection response or log contains forbidden content")
		}
	}
	if !strings.Contains(logOutput.String(), "classroom submit Tutor output requires rephrasing") {
		t.Fatalf("rejection did not emit the fixed content-free log message")
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
	var violationsJSON string
	if err := pool.QueryRow(ctx, `SELECT violations_json::text FROM tutor_output_audits WHERE session_id=$1`, fixture.sessionID).Scan(&violationsJSON); err != nil {
		t.Fatal(err)
	}
	assertMinimalViolationJSON(t, violationsJSON, []tutoraudit.Violation{{ViolationType: "EQUIVALENT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: -1}}, fixture.privateCanary, "wrong runtime answer", "先把固定费用和饮料费用分开")
}

func TestB3CReviewerRejectsSupportWithMinimal422AndNoClassroomMutation(t *testing.T) {
	ctx := context.Background()
	var logOutput bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logOutput)
	defer log.SetOutput(previousLogOutput)
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3c-support-tutor", "b3c-support-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newResponseQueueServer(t, map[string][]string{
		tutorModel:    {`{"message":"先观察题目里的关系。","action":"HINT","answer_revealed":false,"segments":[]}`},
		reviewerModel: {`{"result":"REJECT","no_answer_leak":false,"reason_codes":["DIRECT_ANSWER"],"violations":[{"violation_type":"DIRECT_ANSWER","payload_kind":"MESSAGE","segment_index":-1}]}`},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("support rejection response=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorOutputRephraseRequiredCode {
		t.Fatalf("support rephrase payload=%q decode_error=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{fixture.privateCanary, "先观察题目里的关系", "DIRECT_ANSWER", "correct_answer"} {
		if strings.Contains(response.Body.String(), forbidden) || strings.Contains(logOutput.String(), forbidden) {
			t.Fatalf("support rejection response or log contains forbidden content")
		}
	}
	if !strings.Contains(logOutput.String(), "classroom support Tutor output requires rephrasing") {
		t.Fatalf("support rejection did not emit the fixed content-free log message")
	}
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("support rejection mutated classroom state:\nbefore=%+v\nafter=%+v", before, after)
	}
	var violationsJSON string
	if err := pool.QueryRow(ctx, `SELECT violations_json::text FROM tutor_output_audits WHERE session_id=$1 AND reviewer_result='REJECT' AND final_result='REJECT' AND reason_code='REVIEWER_REJECTED'`, fixture.sessionID).Scan(&violationsJSON); err != nil {
		t.Fatal(err)
	}
	assertMinimalViolationJSON(t, violationsJSON, []tutoraudit.Violation{{ViolationType: "DIRECT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: -1}}, fixture.privateCanary, "先观察题目里的关系")
	if queue.callCount(tutorModel) != 1 || queue.callCount(reviewerModel) != 1 {
		t.Fatalf("support rejection provider calls tutor=%d reviewer=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel))
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
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[],"unexpected":true}`},
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
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`},
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
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Type") != "application/json" || strings.Contains(response.Body.String(), fixture.privateCanary) {
		t.Fatalf("deterministic rejection response=%d %q", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorOutputRephraseRequiredCode {
		t.Fatalf("deterministic rephrase payload=%q decode_error=%v", response.Body.String(), err)
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
		reviewerModel: {`{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`},
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

func TestB3BReviewer429RetryRecoversWithSinglePricedUsage(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3b-retry-tutor", "b3b-retry-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {{status: http.StatusOK, output: `{"message":"先找出固定费用。","action":"HINT","answer_revealed":false,"segments":[]}`}},
		reviewerModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`},
		},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusOK {
		t.Fatalf("recovered response=%d %q", response.Code, response.Body.String())
	}
	if queue.callCount(tutorModel) != 1 || queue.callCount(reviewerModel) != 2 {
		t.Fatalf("provider calls tutor=%d reviewer=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel))
	}
	var reviewerUsage, generationUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW' AND price_catalog_id IS NOT NULL AND estimated_cost_usd=0.000140000`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='PASS' AND reviewer_result='PASS' AND final_result='PASS' AND reason_code='APPROVED'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if reviewerUsage != 1 || generationUsage != 1 || auditRows != 1 {
		t.Fatalf("recovery evidence reviewer_usage=%d generation_usage=%d audits=%d", reviewerUsage, generationUsage, auditRows)
	}
}

func TestB3BReviewerRetryExhaustionReturnsChildSafe503WithoutMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3b-exhaust-tutor", "b3b-exhaust-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {{status: http.StatusOK, output: `{"message":"先找出固定费用。","action":"HINT","answer_revealed":false,"segments":[]}`}},
		reviewerModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
		},
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

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("retry exhaustion response=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorOutputReviewUnavailableCode {
		t.Fatalf("child-safe payload=%q decode_error=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{fixture.privateCanary, tutorModel, reviewerModel, "gateway", "concurrency"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("child-safe response contains internal value %q: %s", forbidden, response.Body.String())
		}
	}
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("retry exhaustion mutated classroom:\nbefore=%+v\nafter=%+v", before, after)
	}
	if voice.calls != 0 {
		t.Fatalf("TTS calls=%d want=0", voice.calls)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("retry exhaustion reached Student WebSocket: %s", event)
	default:
	}
	var reviewerUsage, generationUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='PASS' AND reviewer_result='ERROR' AND final_result='REJECT' AND reason_code='RETRY_EXHAUSTED'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 1 || queue.callCount(reviewerModel) != 3 || reviewerUsage != 0 || generationUsage != 1 || auditRows != 1 {
		t.Fatalf("exhaustion evidence calls=%d/%d usage=%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), reviewerUsage, generationUsage, auditRows)
	}
}

func TestB3DGeneration429RetryRecoversWithSinglePricedUsageAndAudit(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3d-retry-tutor", "b3d-retry-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusOK, output: `{"message":"先观察题目中的关系。","action":"HINT","answer_revealed":false,"segments":[]}`},
		},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`}},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": "HINT"})
	if response.Code != http.StatusOK {
		t.Fatalf("recovered generation response=%d %q", response.Code, response.Body.String())
	}
	var generationUsage, reviewerUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='PASS' AND reviewer_result='PASS' AND final_result='PASS' AND reason_code='APPROVED'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 2 || queue.callCount(reviewerModel) != 1 || generationUsage != 1 || reviewerUsage != 1 || auditRows != 1 {
		t.Fatalf("generation retry evidence calls=%d/%d usage=%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), generationUsage, reviewerUsage, auditRows)
	}
	t.Logf("generation_429_recovered tutor_calls=%d reviewer_calls=%d generation_usage=%d reviewer_usage=%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), generationUsage, reviewerUsage, auditRows)
}

func TestB3DGeneration429RetryExhaustionReturnsMinimal503WithoutMutation(t *testing.T) {
	ctx := context.Background()
	var logOutput bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logOutput)
	defer log.SetOutput(previousLogOutput)
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3d-exhaust-tutor", "b3d-exhaust-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
		},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`}},
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
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("generation exhaustion response=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorGenerationBusyCode {
		t.Fatalf("generation busy payload=%q decode_error=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{fixture.privateCanary, tutorModel, reviewerModel, "raw_provider_response_canary"} {
		if strings.Contains(response.Body.String(), forbidden) || strings.Contains(logOutput.String(), forbidden) {
			t.Fatalf("generation failure response or log contains forbidden content")
		}
	}
	for _, required := range []string{
		"classroom tutor_generation_busy operation=support",
		"failure_category=RETRY_EXHAUSTED",
		"http_status=429",
		"provider_error_code=gateway_concurrency_limit",
		"generation_request_id=",
	} {
		if !strings.Contains(logOutput.String(), required) {
			t.Fatalf("generation failure log=%q missing %q", logOutput.String(), required)
		}
	}
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("generation retry exhaustion mutated classroom:\nbefore=%+v\nafter=%+v", before, after)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("generation retry exhaustion reached Student WebSocket: %s", event)
	default:
	}
	var generationUsage, reviewerUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN'`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 3 || queue.callCount(reviewerModel) != 0 || generationUsage != 0 || reviewerUsage != 0 || auditRows != 0 {
		t.Fatalf("generation exhaustion evidence calls=%d/%d usage=%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), generationUsage, reviewerUsage, auditRows)
	}
	t.Log(strings.TrimSpace(logOutput.String()))
	t.Logf("generation_429_exhausted tutor_calls=%d reviewer_calls=%d generation_usage=%d reviewer_usage=%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), generationUsage, reviewerUsage, auditRows)
}

func TestB3DSubmitGeneration429RetryExhaustionReturnsSameMinimal503(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3d-submit-tutor", "b3d-submit-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {
			{status: http.StatusOK, output: `{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.8,"error_type":"NEEDS_MORE_REASONING","misconceptions":[],"core_ability_signals":[],"emotion_signal":"NEUTRAL","engagement":"NORMAL","recommended_action":"PROBE","safe_to_increase_difficulty":false,"weakness_layer":"L5"}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
		},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`}},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "未提交的测试输入"})
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("submit generation exhaustion response=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorGenerationBusyCode {
		t.Fatalf("submit generation busy payload=%q decode_error=%v", response.Body.String(), err)
	}
	var analysisUsage, generationUsage, reviewerUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='ANSWER_ANALYSIS' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&analysisUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose IN('SOCRATIC_TURN','EXPLANATION')`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 4 || queue.callCount(reviewerModel) != 0 || analysisUsage != 1 || generationUsage != 0 || reviewerUsage != 0 || auditRows != 0 {
		t.Fatalf("submit generation exhaustion evidence calls=%d/%d usage=%d/%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
	}
	t.Logf("submit_generation_429_exhausted tutor_calls=%d analysis_usage=%d generation_usage=%d reviewer_usage=%d audits=%d", queue.callCount(tutorModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
}

func TestB3EAnalysis429RetryRecoversWithSinglePricedUsageAndFullAudit(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3e-retry-tutor", "b3e-retry-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit"}}`},
			{status: http.StatusOK, output: `{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.8,"error_type":"NEEDS_MORE_REASONING","misconceptions":[],"core_ability_signals":[],"emotion_signal":"NEUTRAL","engagement":"NORMAL","recommended_action":"PROBE","safe_to_increase_difficulty":false,"weakness_layer":"L5"}`},
			{status: http.StatusOK, output: `{"message":"先检查题目中的条件。","action":"ANALOGY","answer_revealed":false,"segments":[]}`},
		},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`}},
	})
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "集成测试输入"})
	if response.Code != http.StatusOK {
		t.Fatalf("recovered analysis response=%d %q", response.Code, response.Body.String())
	}
	var analysisUsage, generationUsage, reviewerUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='ANSWER_ANALYSIS' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&analysisUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1 AND deterministic_result='PASS' AND reviewer_result='PASS' AND final_result='PASS' AND reason_code='APPROVED'`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 3 || queue.callCount(reviewerModel) != 1 || analysisUsage != 1 || generationUsage != 1 || reviewerUsage != 1 || auditRows != 1 {
		t.Fatalf("analysis retry evidence calls=%d/%d usage=%d/%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
	}
	t.Logf("analysis_429_recovered tutor_calls=%d reviewer_calls=%d analysis_usage=%d generation_usage=%d reviewer_usage=%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
}

func TestB3EAnalysis429RetryExhaustionReturnsExistingMinimal503WithoutMutation(t *testing.T) {
	ctx := context.Background()
	var logOutput bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logOutput)
	defer log.SetOutput(previousLogOutput)
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	tutorModel, reviewerModel := "b3e-exhaust-tutor", "b3e-exhaust-reviewer"
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, map[string][]queuedResponse{
		tutorModel: {
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
			{status: http.StatusTooManyRequests, body: `{"error":{"code":"gateway_concurrency_limit","message":"raw_provider_response_canary"}}`},
		},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`}},
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

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": "未持久化的集成测试输入"})
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("analysis exhaustion response=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorGenerationBusyCode {
		t.Fatalf("analysis busy payload=%q decode_error=%v", response.Body.String(), err)
	}
	for _, forbidden := range []string{fixture.privateCanary, tutorModel, reviewerModel, "raw_provider_response_canary"} {
		if strings.Contains(response.Body.String(), forbidden) || strings.Contains(logOutput.String(), forbidden) {
			t.Fatalf("analysis failure response or log contains forbidden content")
		}
	}
	for _, required := range []string{
		"classroom tutor_generation_busy operation=submit",
		"failure_category=RETRY_EXHAUSTED",
		"http_status=429",
		"provider_error_code=gateway_concurrency_limit",
		"generation_request_id=",
	} {
		if !strings.Contains(logOutput.String(), required) {
			t.Fatalf("analysis failure log=%q missing %q", logOutput.String(), required)
		}
	}
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("analysis retry exhaustion mutated classroom:\nbefore=%+v\nafter=%+v", before, after)
	}
	select {
	case event := <-studentEvents:
		t.Fatalf("analysis retry exhaustion reached Student WebSocket: %s", event)
	default:
	}
	var analysisUsage, generationUsage, reviewerUsage, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='ANSWER_ANALYSIS'`, fixture.sessionID).Scan(&analysisUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose IN('SOCRATIC_TURN','EXPLANATION')`, fixture.sessionID).Scan(&generationUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='TUTOR_OUTPUT_REVIEW'`, fixture.sessionID).Scan(&reviewerUsage); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_output_audits WHERE session_id=$1`, fixture.sessionID).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if queue.callCount(tutorModel) != 3 || queue.callCount(reviewerModel) != 0 || analysisUsage != 0 || generationUsage != 0 || reviewerUsage != 0 || auditRows != 0 {
		t.Fatalf("analysis exhaustion evidence calls=%d/%d usage=%d/%d/%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
	}
	t.Log(strings.TrimSpace(logOutput.String()))
	t.Logf("analysis_429_exhausted tutor_calls=%d reviewer_calls=%d analysis_usage=%d generation_usage=%d reviewer_usage=%d audits=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel), analysisUsage, generationUsage, reviewerUsage, auditRows)
}
