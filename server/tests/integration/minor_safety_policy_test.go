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
	"github.com/oppositenum/ai-learning-tutor/server/internal/safety"
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
  (SELECT count(*) FROM learning_plan_blocks block JOIN learning_plans plan ON plan.id=block.plan_id WHERE plan.student_id=$2)`, fixture.sessionID, fixture.studentID).Scan(
		&snapshot.answers, &snapshot.analyses, &snapshot.turns, &snapshot.skillStates,
		&snapshot.rewards, &snapshot.reviews, &snapshot.planBlocks,
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
