package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type slowSubmitAgent struct {
	mode          string
	delay         time.Duration
	analyzeCalls  atomic.Int32
	generateCalls atomic.Int32
}

func (agent *slowSubmitAgent) wait(ctx context.Context) error {
	timer := time.NewTimer(agent.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (agent *slowSubmitAgent) AnalyzeAnswer(ctx context.Context, _ ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.analyzeCalls.Add(1)
	if agent.mode == "analyze" {
		if err := agent.wait(ctx); err != nil {
			return ai.AnalyzeAnswerResult{}, err
		}
	}
	return ai.AnalyzeAnswerResult{AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: .5, ErrorType: "UNKNOWN", EmotionSignal: "NEUTRAL", Engagement: "NORMAL", RecommendedAction: tutor.StateProbe, WeaknessLayer: "L5"}, nil
}

func (agent *slowSubmitAgent) GenerateTurn(ctx context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.generateCalls.Add(1)
	if agent.mode == "generate" {
		if err := agent.wait(ctx); err != nil {
			return ai.TutorTurn{}, err
		}
	}
	return ai.TutorTurn{Action: request.TutorDecision.NextState, Message: "先检查这一步。"}, nil
}

func (agent *slowSubmitAgent) GenerateAnalogy(ctx context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func (agent *slowSubmitAgent) GenerateParallelExample(ctx context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func (agent *slowSubmitAgent) GenerateExplanation(ctx context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return agent.GenerateTurn(ctx, ai.GenerateTurnRequest(request))
}

func timeoutFixture(t *testing.T, ctx context.Context) (*classroom.Service, *pgxpool.Pool, uuid.UUID, uuid.UUID, func()) {
	t.Helper()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ACTIVE',current_state='ASK',socratic_fail_count=0,processing_token=NULL,processing_until=NULL WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	return classroom.NewService(pool, nil, nil, nil).WithSubmitTimeout(30 * time.Millisecond), pool, userID, fixture.sessionID, func() { pool.Close() }
}

func TestB7SubmitAnalyzeTimeoutReturnsRetryableWithoutMutation(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	agent := &slowSubmitAgent{mode: "analyze", delay: 200 * time.Millisecond}
	service.WithTeachingAgent(agent)
	result, err := service.Submit(ctx, userID, sessionID, "还没想完")
	if !errors.Is(err, classroom.ErrSubmitTimedOut) || result.SessionID != "" {
		t.Fatalf("analyze timeout result=%+v err=%v", result, err)
	}
	assertTimeoutState(t, ctx, pool, sessionID, 1, 1, 1)
}

func TestB7SubmitGenerateTimeoutReturnsRetryableWithoutMutation(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	agent := &slowSubmitAgent{mode: "generate", delay: 200 * time.Millisecond}
	service.WithTeachingAgent(agent)
	result, err := service.Submit(ctx, userID, sessionID, "这一步我不确定")
	if !errors.Is(err, classroom.ErrSubmitTimedOut) || result.SessionID != "" {
		t.Fatalf("generation timeout result=%+v err=%v", result, err)
	}
	if agent.analyzeCalls.Load() != 1 || agent.generateCalls.Load() != 1 {
		t.Fatalf("unexpected provider calls analyze=%d generate=%d", agent.analyzeCalls.Load(), agent.generateCalls.Load())
	}
	assertTimeoutState(t, ctx, pool, sessionID, 1, 1, 1)
}

type slowAudit struct{ calls atomic.Int32 }

func (audit *slowAudit) AuditTutorOutput(ctx context.Context, _ ai.TutorOutputAuditRequest) error {
	audit.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

type timeoutStructuredClient struct{}

func (timeoutStructuredClient) GenerateStructured(_ context.Context, request ai.StructuredRequest) (ai.StructuredResult, error) {
	output := validAnalysisJSON()
	if request.Purpose == ai.PurposeSocraticTurn {
		output = validTurnJSON("PROBE", false)
	}
	return ai.StructuredResult{ResponseID: uuid.NewString(), OutputJSON: json.RawMessage(output)}, nil
}

func TestB7SubmitOutputAuditTimeoutFailsClosedWithoutMutation(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	audit := &slowAudit{}
	agent, err := ai.NewCodexProvider(timeoutStructuredClient{}, audit)
	if err != nil {
		t.Fatal(err)
	}
	service.WithTeachingAgent(agent)
	result, err := service.Submit(ctx, userID, sessionID, "需要老师再提示一下")
	if !errors.Is(err, classroom.ErrSubmitTimedOut) || result.SessionID != "" {
		t.Fatalf("audit timeout result=%+v err=%v", result, err)
	}
	if audit.calls.Load() != 1 {
		t.Fatalf("audit calls=%d want 1", audit.calls.Load())
	}
	assertTimeoutState(t, ctx, pool, sessionID, 1, 1, 1)
}

func TestB7SubmitTimeoutRecordsAIRequestOutcome(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','slow-timeout-model',now()-interval '1 minute',1,0.5,2)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(250 * time.Millisecond):
		}
	}))
	defer provider.CloseClientConnections()
	defer provider.Close()
	client, err := ai.NewOpenAIResponsesClient(provider.Client(), provider.URL, "test-key", "slow-timeout-model")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(client.WithUsageRecorder(usage.NewRecorder(pool)))
	if err != nil {
		t.Fatal(err)
	}
	service.WithTeachingAgent(agent)
	if _, err := service.Submit(ctx, userID, sessionID, "等待外部服务的回答"); !errors.Is(err, classroom.ErrSubmitTimedOut) {
		t.Fatalf("submit error=%v", err)
	}
	var count int
	var purpose, outcome string
	if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(min(purpose),''),COALESCE(min(outcome),'') FROM ai_request_outcomes WHERE session_id=$1`, sessionID).Scan(&count, &purpose, &outcome); err != nil {
		t.Fatal(err)
	}
	if count != 1 || purpose != string(ai.PurposeAnswerAnalysis) || outcome != string(ai.RequestTransportError) {
		t.Fatalf("timeout outcome count=%d purpose=%s outcome=%s", count, purpose, outcome)
	}
	t.Logf("ai_request_outcomes: count=%d purpose=%s outcome=%s", count, purpose, outcome)
}

func TestB7SubmitTimeoutReleasesLeaseAndRetryIsNotDeadlocked(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	service.WithSubmitTimeout(2 * time.Second)
	agent := &slowSubmitAgent{mode: "analyze", delay: 5 * time.Second}
	service.WithTeachingAgent(agent)
	requestCtx, cancel := context.WithCancel(ctx)
	resultCh := make(chan error, 1)
	go func() {
		_, err := service.Submit(requestCtx, userID, sessionID, "客户端断开")
		resultCh <- err
	}()
	waitForTimeoutLeaseState(t, ctx, pool, sessionID, true)
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled submit error_type=%T error=%v want=%v", err, err, context.Canceled)
	}
	waitForTimeoutLeaseState(t, ctx, pool, sessionID, false)
	// A second request must be able to acquire the lease after cancellation.
	agent.mode = "none"
	retryResult, retryErr := service.Submit(ctx, userID, sessionID, "重试同一回答")
	switch {
	case retryErr == nil:
		t.Logf("retry submit outcome=success result=%+v", retryResult)
	case errors.Is(retryErr, classroom.ErrClassroomChanged):
		t.Fatalf("retry submit outcome=classroom_changed error_type=%T error=%v", retryErr, retryErr)
	default:
		t.Fatalf("retry submit outcome=unexpected_error error_type=%T error=%v result=%+v", retryErr, retryErr, retryResult)
	}
	assertTimeoutState(t, ctx, pool, sessionID, 2, 2, 3)
}

func waitForTimeoutLeaseState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID, wantHeld bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var token *uuid.UUID
	var until *time.Time
	for {
		if err := pool.QueryRow(ctx, `SELECT processing_token,processing_until FROM learning_sessions WHERE id=$1`, sessionID).Scan(&token, &until); err != nil {
			t.Fatal(err)
		}
		held := token != nil || until != nil
		if held == wantHeld && ((token == nil) == (until == nil)) {
			t.Logf("operation_lease_state: held=%t processing_token=%v processing_until=%v", held, token, until)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation lease wait timed out: want_held=%t actual_token=%v actual_until=%v", wantHeld, token, until)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestB7InFlightSubmitIsExcludedFromStaleRecovery(t *testing.T) {
	ctx := context.Background()
	service, pool, _, sessionID, cleanup := timeoutFixture(t, ctx)
	defer cleanup()
	future := time.Now().UTC().Add(30 * time.Second)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ACTIVE',last_activity_at=$2,processing_token=$3,processing_until=$4 WHERE id=$1`, sessionID, time.Now().UTC().Add(-2*time.Minute), uuid.New(), future); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverAllStaleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_sessions WHERE id=$1`, sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" {
		t.Fatalf("in-flight stale session was recovered as %s", status)
	}
	t.Logf("in_flight_session_state: status=%s processing_until=%s", status, future.Format(time.RFC3339))
}

func assertTimeoutState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID, wantAnswers, wantAnalyses, wantTurns int) {
	t.Helper()
	var status string
	var token *uuid.UUID
	var answers, analyses, turns int
	if err := pool.QueryRow(ctx, `SELECT status,processing_token FROM learning_sessions WHERE id=$1`, sessionID).Scan(&status, &token); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, sessionID).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM answer_analyses WHERE student_answer_id IN (SELECT id FROM student_answers WHERE session_id=$1)`, sessionID).Scan(&analyses); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, sessionID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if status != "ACTIVE" || token != nil || answers != wantAnswers || analyses != wantAnalyses || turns != wantTurns {
		t.Fatalf("classroom state mismatch: status actual=%s expected=ACTIVE; processing_token actual=%v expected=<nil>; answers actual=%d expected=%d; analyses actual=%d expected=%d; tutor_turns actual=%d expected=%d", status, token, answers, wantAnswers, analyses, wantAnalyses, turns, wantTurns)
	}
	t.Logf("timeout_session_state: status=%s processing_token=%v answers=%d analyses=%d tutor_turns=%d", status, token, answers, analyses, turns)
}
