package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/oppositenum/ai-learning-tutor/server/internal/safety"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type countingSafetyAgent struct{ calls int }

func (agent *countingSafetyAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.calls++
	return ai.AnalyzeAnswerResult{}, nil
}
func (agent *countingSafetyAgent) GenerateTurn(context.Context, ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, nil
}
func (agent *countingSafetyAgent) GenerateAnalogy(context.Context, ai.AnalogyRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, nil
}
func (agent *countingSafetyAgent) GenerateParallelExample(context.Context, ai.ExampleRequest) (ai.TutorTurn, error) {
	agent.calls++
	return ai.TutorTurn{}, nil
}
func (agent *countingSafetyAgent) GenerateExplanation(context.Context, ai.ExplainRequest) (ai.Explanation, error) {
	agent.calls++
	return ai.Explanation{}, nil
}

type safetyBusinessSnapshot struct {
	answers, analyses, turns, skillStates, rewards, reviews, planBlocks int
	stageAttempts, stageEvidence, activityDays, energy                  int
}

func TestMinorSafetyGatePreventsAgentAndLearningMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	agent := &countingSafetyAgent{}
	hub := realtime.NewHub()
	studentEvents, stopStudent := hub.Subscribe(fixture.studentID.String(), auth.RoleStudent)
	defer stopStudent()
	parentEvents, stopParent := hub.Subscribe(fixture.studentID.String(), auth.RoleParent)
	defer stopParent()
	service := classroom.NewService(pool, hub, nil, nil).WithTeachingAgent(agent)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	before := readSafetyBusinessSnapshot(t, ctx, pool, fixture)
	privateInput := "我不想活了，PRIVATE_SAFETY_INPUT_CANARY"

	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": privateInput})
	if response.Code != http.StatusOK {
		t.Fatalf("safety response=%d %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	safetyPayload, ok := payload["safety"].(map[string]any)
	if !ok || safetyPayload["policy_version"] != safety.PolicyVersion || safetyPayload["category"] != "SELF_HARM" || safetyPayload["parent_notified"] != true {
		t.Fatalf("safety payload=%v", payload)
	}
	if strings.Contains(response.Body.String(), "PRIVATE_SAFETY_INPUT_CANARY") || agent.calls != 0 {
		t.Fatalf("private input exposed or agent called: calls=%d body=%s", agent.calls, response.Body.String())
	}
	after := readSafetyBusinessSnapshot(t, ctx, pool, fixture)
	if before != after {
		t.Fatalf("learning state changed: before=%+v after=%+v", before, after)
	}

	studentEvent := awaitEventType(t, studentEvents, "SAFETY_INTERVENTION")
	parentEvent := awaitEventType(t, parentEvents, "SAFETY_INTERVENTION")
	if strings.Contains(string(studentEvent), "PRIVATE_SAFETY_INPUT_CANARY") || strings.Contains(string(parentEvent), "PRIVATE_SAFETY_INPUT_CANARY") {
		t.Fatalf("safety realtime exposed input: student=%s parent=%s", studentEvent, parentEvent)
	}
	for _, forbidden := range []string{"message", "answer", "reason", "response"} {
		if strings.Contains(strings.ToLower(string(parentEvent)), `"`+forbidden+`"`) {
			t.Fatalf("parent safety event includes non-minimal field %q: %s", forbidden, parentEvent)
		}
	}

	var incidents, leakedBodies, forbiddenColumns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM minor_safety_incidents WHERE session_id=$1 AND category='SELF_HARM' AND severity='CRITICAL' AND parent_escalated`, fixture.sessionID).Scan(&incidents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1 AND message LIKE '%PRIVATE_SAFETY_INPUT_CANARY%'`, fixture.sessionID).Scan(&leakedBodies); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name LIKE 'minor_safety_%' AND column_name IN('answer','answer_text','input','input_text','reason','model_output','provider_response','content_hash')`).Scan(&forbiddenColumns); err != nil {
		t.Fatal(err)
	}
	if incidents != 1 || leakedBodies != 0 || forbiddenColumns != 0 {
		t.Fatalf("incidents=%d leaked_bodies=%d forbidden_columns=%d", incidents, leakedBodies, forbiddenColumns)
	}

	for attempt := 0; attempt < 2; attempt++ {
		summary := performJSON(router, http.MethodGet, "/api/v1/parent/child/"+fixture.studentID.String()+"/safety-events", fixture.parentToken, nil)
		if summary.Code != http.StatusOK || strings.Contains(summary.Body.String(), "PRIVATE_SAFETY_INPUT_CANARY") || !strings.Contains(summary.Body.String(), `"category":"SELF_HARM"`) {
			t.Fatalf("parent summary=%d %s", summary.Code, summary.Body.String())
		}
	}
	var accessAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM minor_safety_access_audits`).Scan(&accessAudits); err != nil || accessAudits != 2 {
		t.Fatalf("access audits=%d err=%v", accessAudits, err)
	}
}

func TestNonEscalatedSafetyCategoryIsNotPublishedToParent(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	hub := realtime.NewHub()
	parentEvents, stopParent := hub.Subscribe(fixture.studentID.String(), auth.RoleParent)
	defer stopParent()
	var studentUserID string
	if err := pool.QueryRow(ctx, `SELECT user_id::text FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, hub, nil, nil)
	result, err := service.Submit(ctx, uuid.MustParse(studentUserID), fixture.sessionID, "我的手机号是 13812345678")
	if err != nil || result.Safety == nil || result.Safety.ParentNotified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	select {
	case event := <-parentEvents:
		t.Fatalf("parent received non-escalated safety event: %s", event)
	case <-time.After(30 * time.Millisecond):
	}
	var incidents, parentVisible int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE parent_escalated) FROM minor_safety_incidents WHERE session_id=$1`, fixture.sessionID).Scan(&incidents, &parentVisible); err != nil || incidents != 1 || parentVisible != 0 {
		t.Fatalf("incidents=%d visible=%d err=%v", incidents, parentVisible, err)
	}
}

func TestStructuredFillSafetyGatePreventsPersistenceAgentAndLearningMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	var questionID uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT question_id FROM classroom_stage_tasks
WHERE lineage_id=$1 AND stage_role='ORIGINAL' AND selection_order=1`, fixture.lineageID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	scene := studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks,
		AccessibleFallback: "填写内容。", Slots: []studentinteraction.Item{{ID: "slot-1", Label: "内容"}},
	}
	rawScene, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := studentinteraction.AnswerSchema(scene)
	if !ok {
		t.Fatal("fill scene did not produce answer schema")
	}
	rule := json.RawMessage(`{"rule_type":"EXACT_FILL","expected_values":[{"slot_id":"slot-1","accepted_values":["已发布值"]}],"allowed_slot_ids":["slot-1"]}`)
	if _, err := pool.Exec(ctx, `UPDATE questions SET question_type='FILL_BLANKS',scene_public_json=$2,input_schema_json=$3 WHERE id=$1`,
		questionID, rawScene, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE classroom_stage_tasks SET scoring_rule_version='exact-fill-v1',scoring_rule_private_json=$2 WHERE question_id=$1`,
		questionID, rule); err != nil {
		t.Fatal(err)
	}
	agent := &countingSafetyAgent{}
	hub := realtime.NewHub()
	studentEvents, stopStudent := hub.Subscribe(fixture.security.studentID.String(), auth.RoleStudent)
	defer stopStudent()
	parentEvents, stopParent := hub.Subscribe(fixture.security.studentID.String(), auth.RoleParent)
	defer stopParent()
	service := classroom.NewService(pool, hub, nil, nil).WithTeachingAgent(agent)
	router := stageRouter(pool, service)
	session := startStageSession(t, router, fixture)
	stageSecurity := fixture.security
	stageSecurity.sessionID = session.ID
	before := readSafetyBusinessSnapshot(t, ctx, pool, stageSecurity)

	escalatedInput := "我不想活了，PRIVATE_STRUCTURED_SAFETY_CANARY"
	requestBody := func(operationID, taskID uuid.UUID, value string) map[string]any {
		return map[string]any{
			"operation_id": operationID, "stage": session.StageFlow.Stage,
			"task_id": taskID, "task_version": session.StageFlow.TaskVersion,
			"response": map[string]any{"values": []map[string]string{{"slot_id": "slot-1", "value": value}}},
		}
	}
	request := func(body map[string]any) *httptest.ResponseRecorder {
		return performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
			"operation_id": body["operation_id"], "stage": body["stage"],
			"task_id": body["task_id"], "task_version": body["task_version"],
			"response": body["response"],
		})
	}
	assertNoRealtime := func() {
		t.Helper()
		select {
		case event := <-studentEvents:
			t.Fatalf("idempotent safety replay republished Student event: %s", event)
		case <-time.After(30 * time.Millisecond):
		}
		select {
		case event := <-parentEvents:
			t.Fatalf("idempotent safety replay republished Parent event: %s", event)
		case <-time.After(30 * time.Millisecond):
		}
	}
	escalatedOperation := uuid.New()
	escalatedBody := requestBody(escalatedOperation, session.QuestionID, escalatedInput)
	escalated := request(escalatedBody)
	if escalated.Code != http.StatusOK || strings.Contains(escalated.Body.String(), escalatedInput) || !strings.Contains(escalated.Body.String(), `"category":"SELF_HARM"`) {
		t.Fatalf("structured escalated safety=%d %s", escalated.Code, escalated.Body.String())
	}
	studentEvent := awaitEventType(t, studentEvents, string(realtime.EventSafetyIntervention))
	parentEvent := awaitEventType(t, parentEvents, string(realtime.EventSafetyIntervention))
	if strings.Contains(string(studentEvent), escalatedInput) || strings.Contains(string(parentEvent), escalatedInput) {
		t.Fatalf("structured safety realtime exposed child text: student=%s parent=%s", studentEvent, parentEvent)
	}
	replayed := request(escalatedBody)
	if replayed.Code != http.StatusOK || replayed.Body.String() != escalated.Body.String() {
		t.Fatalf("structured safety replay first=%d/%s replay=%d/%s", escalated.Code, escalated.Body.String(), replayed.Code, replayed.Body.String())
	}
	assertNoRealtime()

	changedBodyReplay := request(requestBody(escalatedOperation, session.QuestionID, "我的手机号是 13812345678"))
	if changedBodyReplay.Code != http.StatusOK || changedBodyReplay.Body.String() != escalated.Body.String() {
		t.Fatalf("privacy-preserving first-write replay=%d/%s", changedBodyReplay.Code, changedBodyReplay.Body.String())
	}
	assertNoRealtime()
	changedIdentity := request(requestBody(escalatedOperation, uuid.New(), escalatedInput))
	if changedIdentity.Code != http.StatusConflict {
		t.Fatalf("changed public safety identity=%d %s", changedIdentity.Code, changedIdentity.Body.String())
	}
	assertNoRealtime()

	nonEscalatedInput := "我的手机号是 13812345678，PRIVATE_STRUCTURED_PHONE_CANARY"
	nonEscalated := request(requestBody(uuid.New(), session.QuestionID, nonEscalatedInput))
	if nonEscalated.Code != http.StatusOK || strings.Contains(nonEscalated.Body.String(), nonEscalatedInput) || !strings.Contains(nonEscalated.Body.String(), `"parent_notified":false`) {
		t.Fatalf("structured non-escalated safety=%d %s", nonEscalated.Code, nonEscalated.Body.String())
	}
	studentEvent = awaitEventType(t, studentEvents, string(realtime.EventSafetyIntervention))
	if strings.Contains(string(studentEvent), nonEscalatedInput) {
		t.Fatalf("structured safety Student realtime exposed child text: %s", studentEvent)
	}
	select {
	case event := <-parentEvents:
		t.Fatalf("Parent received non-escalated structured safety event: %s", event)
	case <-time.After(30 * time.Millisecond):
	}

	after := readSafetyBusinessSnapshot(t, ctx, pool, stageSecurity)
	if before != after || agent.calls != 0 {
		t.Fatalf("structured safety changed learning state or called model: before=%+v after=%+v calls=%d", before, after, agent.calls)
	}
	var incidents, safetyOperations, safetyEvents, leakedTurns, forbiddenColumns int
	if err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*)::int FROM minor_safety_incidents WHERE session_id=$1),
	  (SELECT count(*)::int FROM classroom_stage_safety_operations WHERE session_id=$1),
	  (SELECT count(*)::int FROM tutor_events WHERE session_id=$1 AND type='SAFETY_INTERVENTION'),
	  (SELECT count(*)::int FROM tutor_turns WHERE session_id=$1 AND (message LIKE '%PRIVATE_STRUCTURED_SAFETY_CANARY%' OR message LIKE '%PRIVATE_STRUCTURED_PHONE_CANARY%')),
	  (SELECT count(*)::int FROM information_schema.columns
	   WHERE table_schema=current_schema() AND table_name='classroom_stage_safety_operations'
	     AND (column_name ILIKE '%answer%' OR column_name ILIKE '%digest%' OR column_name ILIKE '%hash%'
	       OR column_name ILIKE '%body%' OR column_name ILIKE '%reason%' OR column_name ILIKE '%excerpt%'
	       OR column_name ILIKE '%input%' OR column_name ILIKE '%text%' OR column_name ILIKE '%content%'
	       OR column_name ILIKE '%value%' OR column_name ILIKE '%model%' OR column_name ILIKE '%provider%'))`,
		session.ID).Scan(&incidents, &safetyOperations, &safetyEvents, &leakedTurns, &forbiddenColumns); err != nil {
		t.Fatal(err)
	}
	if incidents != 2 || safetyOperations != 2 || safetyEvents != 2 || leakedTurns != 0 || forbiddenColumns != 0 {
		t.Fatalf("structured safety incidents=%d operations=%d events=%d leaked_turns=%d forbidden_columns=%d",
			incidents, safetyOperations, safetyEvents, leakedTurns, forbiddenColumns)
	}
}

func readSafetyBusinessSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture) safetyBusinessSnapshot {
	t.Helper()
	var snapshot safetyBusinessSnapshot
	err := pool.QueryRow(ctx, `
SELECT
  (SELECT count(*) FROM student_answers WHERE session_id=$1),
  (SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1),
  (SELECT count(*) FROM tutor_turns WHERE session_id=$1),
  (SELECT count(*) FROM student_skill_states WHERE student_id=$2),
  (SELECT count(*) FROM reward_events WHERE student_id=$2),
  (SELECT count(*) FROM review_queue WHERE student_id=$2),
	  (SELECT count(*) FROM learning_plan_blocks block JOIN learning_plans plan ON plan.id=block.plan_id WHERE plan.student_id=$2),
	  (SELECT count(*) FROM classroom_stage_attempts WHERE session_id=$1),
	  (SELECT count(*) FROM classroom_stage_evidence WHERE session_id=$1),
	  (SELECT count(*) FROM student_activity_days WHERE student_id=$2),
	  (SELECT COALESCE(max(total_energy),0) FROM student_growth WHERE student_id=$2)`, fixture.sessionID, fixture.studentID).Scan(
		&snapshot.answers, &snapshot.analyses, &snapshot.turns, &snapshot.skillStates,
		&snapshot.rewards, &snapshot.reviews, &snapshot.planBlocks, &snapshot.stageAttempts,
		&snapshot.stageEvidence, &snapshot.activityDays, &snapshot.energy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
