package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type lifecycleTestClock struct {
	nanoseconds atomic.Int64
}

func newLifecycleTestClock(now time.Time) *lifecycleTestClock {
	clock := &lifecycleTestClock{}
	clock.nanoseconds.Store(now.UnixNano())
	return clock
}

func (clock *lifecycleTestClock) Now() time.Time {
	return time.Unix(0, clock.nanoseconds.Load()).UTC()
}

func (clock *lifecycleTestClock) Add(duration time.Duration) {
	clock.nanoseconds.Add(int64(duration))
}

type blockingLifecycleAgent struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	correct bool
}

func newBlockingLifecycleAgent(correct bool) *blockingLifecycleAgent {
	return &blockingLifecycleAgent{started: make(chan struct{}), release: make(chan struct{}), correct: correct}
}

func (agent *blockingLifecycleAgent) AnalyzeAnswer(ctx context.Context, _ ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.once.Do(func() { close(agent.started) })
	select {
	case <-ctx.Done():
		return ai.AnalyzeAnswerResult{}, ctx.Err()
	case <-agent.release:
	}
	return ai.AnalyzeAnswerResult{AnswerCorrect: agent.correct, ReasoningQuality: "STRONG", Confidence: .99, ErrorType: "NONE", EmotionSignal: "NEUTRAL", Engagement: "NORMAL", RecommendedAction: tutor.StateProbe}, nil
}

func lifecycleTurn(request ai.GenerateTurnRequest) ai.TutorTurn {
	return ai.TutorTurn{Message: "只推进一个思考台阶", Action: request.TutorDecision.NextState, ResponseID: uuid.NewString()}
}

func (agent *blockingLifecycleAgent) GenerateTurn(_ context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	return lifecycleTurn(request), nil
}

func (agent *blockingLifecycleAgent) GenerateAnalogy(_ context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return lifecycleTurn(ai.GenerateTurnRequest(request)), nil
}

func (agent *blockingLifecycleAgent) GenerateParallelExample(_ context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return lifecycleTurn(ai.GenerateTurnRequest(request)), nil
}

func (agent *blockingLifecycleAgent) GenerateExplanation(_ context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return lifecycleTurn(ai.GenerateTurnRequest(request)), nil
}

func TestSessionLifecycleCountsOnlyActiveTimeAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2030, 1, 10, 2, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions
SET current_state='ASK',socratic_fail_count=0,assistance_level=0,started_at=$2,last_resumed_at=$2,last_activity_at=$2,accumulated_seconds=0,actual_seconds=0
WHERE id=$1`, fixture.sessionID, current); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithClock(func() time.Time { return current })

	current = current.Add(20 * time.Second)
	paused, err := service.PauseSession(ctx, studentUserID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != "PAUSED" || paused.ActiveSeconds != 20 || paused.CurrentActiveSeconds != 0 || paused.TimingVersion != 1 {
		t.Fatalf("pause timing=%+v", paused)
	}
	current = current.Add(2 * time.Hour)
	pausedAgain, err := service.PauseSession(ctx, studentUserID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if pausedAgain.ActiveSeconds != 20 || pausedAgain.TimingVersion != paused.TimingVersion {
		t.Fatalf("paused wall time was counted: %+v", pausedAgain)
	}
	resumed, err := service.ResumeSession(ctx, studentUserID, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != "ACTIVE" || resumed.ActiveSeconds != 20 || resumed.CurrentActiveSeconds != 0 || resumed.TimingVersion != 2 {
		t.Fatalf("resume timing=%+v", resumed)
	}
	current = current.Add(15 * time.Second)
	completed, err := service.Submit(ctx, studentUserID, fixture.sessionID, fixture.privateCanary)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "COMPLETED" || completed.ActiveSeconds != 35 || completed.TimingVersion != 3 {
		t.Fatalf("completion timing=%+v", completed)
	}

	var actualSeconds, activitySeconds, pausedEvents, resumedEvents int
	if err := pool.QueryRow(ctx, `SELECT actual_seconds FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&actualSeconds); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT active_seconds FROM student_activity_days WHERE student_id=$1 AND activity_date=$2::date`, fixture.studentID, "2030-01-10").Scan(&activitySeconds); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_events WHERE session_id=$1 AND type='SESSION_PAUSED'`, fixture.sessionID).Scan(&pausedEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_events WHERE session_id=$1 AND type='SESSION_RESUMED'`, fixture.sessionID).Scan(&resumedEvents); err != nil {
		t.Fatal(err)
	}
	if actualSeconds != 35 || activitySeconds != 35 || pausedEvents != 1 || resumedEvents != 1 {
		t.Fatalf("duration/events actual=%d activity=%d paused=%d resumed=%d", actualSeconds, activitySeconds, pausedEvents, resumedEvents)
	}
}

func TestWrongAnswerAndHintAreRecordedAsAssistedEvidence(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT knowledge_point_id FROM questions WHERE id=$1`, fixture.releasedQuestionID).Scan(&knowledgePointID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='ASK',socratic_fail_count=0,assistance_level=0,last_resumed_at=$2,last_activity_at=$2 WHERE id=$1`, fixture.sessionID, now); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithClock(func() time.Time { return now })
	wrong, err := service.Submit(ctx, studentUserID, fixture.sessionID, "not correct")
	if err != nil || wrong.Action != "PROBE" {
		t.Fatalf("wrong answer result=%+v err=%v", wrong, err)
	}
	if _, err := service.PauseSession(ctx, studentUserID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResumeSession(ctx, studentUserID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestSupport(ctx, studentUserID, fixture.sessionID, classroom.SupportHint); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Submit(ctx, studentUserID, fixture.sessionID, fixture.privateCanary)
	if err != nil {
		t.Fatal(err)
	}
	if completed.MasteryState != "ASSISTED" || completed.Energy != 2 {
		t.Fatalf("assisted completion=%+v", completed)
	}

	var independent, assisted, life, variant, textbook, review, masteryRewards int
	if err := pool.QueryRow(ctx, `SELECT independent_successes,assisted_successes,life_context_successes,variant_successes,textbook_successes,review_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, knowledgePointID).Scan(&independent, &assisted, &life, &variant, &textbook, &review); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reward_events WHERE student_id=$1 AND type='MASTERY'`, fixture.studentID).Scan(&masteryRewards); err != nil {
		t.Fatal(err)
	}
	if independent != 0 || assisted != 1 || life != 0 || variant != 0 || textbook != 0 || review != 0 || masteryRewards != 0 {
		t.Fatalf("assistance evidence independent=%d assisted=%d forms=%d/%d/%d/%d mastery_rewards=%d", independent, assisted, life, variant, textbook, review, masteryRewards)
	}
	rows, err := pool.Query(ctx, `SELECT student_payload_json FROM tutor_events WHERE session_id=$1 ORDER BY sequence`, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		assertStudentPayloadHasNoPrivateFields(t, payload)
		if strings.Contains(string(payload), fixture.privateCanary) {
			t.Fatalf("Student event leaked private answer: %s", payload)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestStaleSessionIsAbandonedAndReleasesItsPlanBlock(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now(),last_resumed_at=NULL WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	current := time.Now()
	plannerService := planner.NewService(pool)
	service := classroom.NewService(pool, nil, nil, nil, plannerService).WithClock(func() time.Time { return current })
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool), plannerService),
	})
	today := performJSON(router, http.MethodGet, "/api/v1/student/today", fixture.studentToken, nil)
	var planPayload struct {
		Plans []struct {
			Blocks []struct {
				ID uuid.UUID `json:"id"`
			} `json:"blocks"`
		} `json:"plans"`
	}
	if err := json.Unmarshal(today.Body.Bytes(), &planPayload); err != nil || len(planPayload.Plans) == 0 || len(planPayload.Plans[0].Blocks) == 0 {
		t.Fatalf("today=%d %s err=%v", today.Code, today.Body.String(), err)
	}
	blockID := planPayload.Plans[0].Blocks[0].ID
	started := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	var session classroom.StudentSession
	if err := json.Unmarshal(started.Body.Bytes(), &session); err != nil || started.Code != http.StatusOK {
		t.Fatalf("start=%d %s err=%v", started.Code, started.Body.String(), err)
	}
	staleAt := current.Add(-44 * time.Hour)
	leaseToken := uuid.New()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET started_at=$2,last_resumed_at=$2,last_activity_at=$2,accumulated_seconds=12,actual_seconds=12,processing_token=$3,processing_until=$4 WHERE id=$1`, session.ID, staleAt, leaseToken, current.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverAllStaleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	var leasedStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_sessions WHERE id=$1`, session.ID).Scan(&leasedStatus); err != nil || leasedStatus != "ACTIVE" {
		t.Fatalf("active processing lease was reclaimed status=%s err=%v", leasedStatus, err)
	}
	current = current.Add(6 * time.Minute)
	currentSession := performJSON(router, http.MethodGet, "/api/v1/student/sessions/current", fixture.studentToken, nil)
	if currentSession.Code != http.StatusNoContent || currentSession.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stale current=%d cache=%q body=%s", currentSession.Code, currentSession.Header().Get("Cache-Control"), currentSession.Body.String())
	}
	var status, blockStatus string
	var actualSeconds int
	if err := pool.QueryRow(ctx, `SELECT status,actual_seconds FROM learning_sessions WHERE id=$1`, session.ID).Scan(&status, &actualSeconds); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&blockStatus); err != nil {
		t.Fatal(err)
	}
	if status != "ABANDONED" || actualSeconds != 12 || blockStatus != "AVAILABLE" {
		t.Fatalf("stale recovery status=%s seconds=%d block=%s", status, actualSeconds, blockStatus)
	}
	restarted := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID})
	if restarted.Code != http.StatusOK || strings.Contains(restarted.Body.String(), session.ID.String()) {
		t.Fatalf("released block did not restart: %d %s", restarted.Code, restarted.Body.String())
	}
	forbidden := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/resume", fixture.parentToken, nil)
	if forbidden.Code != http.StatusForbidden || forbidden.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Parent lifecycle access=%d cache=%q", forbidden.Code, forbidden.Header().Get("Cache-Control"))
	}
}

type lifecycleSubmitOutcome struct {
	result classroom.SubmitResult
	err    error
}

func prepareLifecycleOperationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture, clock *lifecycleTestClock) (uuid.UUID, int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ACTIVE',current_state='ASK',socratic_fail_count=0,assistance_level=0,started_at=$2,last_resumed_at=$2,last_activity_at=$2,accumulated_seconds=0,actual_seconds=0,processing_token=NULL,processing_until=NULL WHERE id=$1`, fixture.sessionID, clock.Now()); err != nil {
		t.Fatal(err)
	}
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	var answers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answers); err != nil {
		t.Fatal(err)
	}
	return studentUserID, answers
}

func awaitAgentStart(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("teaching agent did not start")
	}
}

func awaitSubmit(t *testing.T, outcome <-chan lifecycleSubmitOutcome) lifecycleSubmitOutcome {
	t.Helper()
	select {
	case result := <-outcome:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("classroom submit did not finish")
		return lifecycleSubmitOutcome{}
	}
}

func TestTeachingResultCanCommitAfterVisibilityPause(t *testing.T) {
	for _, test := range []struct {
		name       string
		wantStatus string
		wantAction tutor.State
	}{
		{name: "wrong answer remains paused", wantStatus: "PAUSED", wantAction: tutor.StateProbe},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := isolatedPool(t, ctx, testDatabaseURL(t))
			if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
				t.Fatal(err)
			}
			fixture := seedSecurityFixture(t, ctx, pool)
			clock := newLifecycleTestClock(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC))
			studentUserID, answersBefore := prepareLifecycleOperationFixture(t, ctx, pool, fixture, clock)
			agent := newBlockingLifecycleAgent(false)
			service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithClock(clock.Now).WithTeachingAgent(agent)
			outcome := make(chan lifecycleSubmitOutcome, 1)
			answer := "等待分析的回答"
			go func() {
				result, err := service.Submit(ctx, studentUserID, fixture.sessionID, answer)
				outcome <- lifecycleSubmitOutcome{result: result, err: err}
			}()
			awaitAgentStart(t, agent.started)

			clock.Add(20 * time.Second)
			pausedAt := clock.Now()
			paused, err := service.PauseSession(ctx, studentUserID, fixture.sessionID)
			if err != nil || paused.Status != "PAUSED" || paused.ActiveSeconds != 20 {
				t.Fatalf("pause during analysis=%+v err=%v", paused, err)
			}
			clock.Add(2 * time.Minute)
			close(agent.release)
			submitted := awaitSubmit(t, outcome)
			if submitted.err != nil || submitted.result.Status != test.wantStatus || submitted.result.Action != test.wantAction || submitted.result.ActiveSeconds != 20 {
				t.Fatalf("submit after pause=%+v err=%v", submitted.result, submitted.err)
			}

			var status string
			var actualSeconds, answersAfter int
			var lastActivityAt time.Time
			var processingToken *uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT status,actual_seconds,last_activity_at,processing_token FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&status, &actualSeconds, &lastActivityAt, &processingToken); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
				t.Fatal(err)
			}
			if status != test.wantStatus || actualSeconds != 20 || !lastActivityAt.Equal(pausedAt) || processingToken != nil || answersAfter != answersBefore+1 {
				t.Fatalf("persisted status=%s seconds=%d last_activity=%s token=%v answers=%d/%d", status, actualSeconds, lastActivityAt, processingToken, answersAfter, answersBefore)
			}
		})
	}
}

func TestTeachingResultCannotCommitAfterPausedOperationLeaseExpires(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	clock := newLifecycleTestClock(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC))
	studentUserID, answersBefore := prepareLifecycleOperationFixture(t, ctx, pool, fixture, clock)
	agent := newBlockingLifecycleAgent(false)
	service := classroom.NewService(pool, nil, nil, nil).WithClock(clock.Now).WithTeachingAgent(agent)
	outcome := make(chan lifecycleSubmitOutcome, 1)
	go func() {
		result, err := service.Submit(ctx, studentUserID, fixture.sessionID, "租约过期的回答")
		outcome <- lifecycleSubmitOutcome{result: result, err: err}
	}()
	awaitAgentStart(t, agent.started)

	clock.Add(20 * time.Second)
	if _, err := service.PauseSession(ctx, studentUserID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	clock.Add(6 * time.Minute)
	close(agent.release)
	submitted := awaitSubmit(t, outcome)
	if !errors.Is(submitted.err, classroom.ErrClassroomChanged) {
		t.Fatalf("expired paused operation err=%v", submitted.err)
	}

	var status string
	var answersAfter int
	var processingToken *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status,processing_token FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&status, &processingToken); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" || processingToken != nil || answersAfter != answersBefore {
		t.Fatalf("expired paused operation status=%s token=%v answers=%d/%d", status, processingToken, answersAfter, answersBefore)
	}
}

func TestAbandonInvalidatesInFlightTeachingOperation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	clock := newLifecycleTestClock(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC))
	studentUserID, answersBefore := prepareLifecycleOperationFixture(t, ctx, pool, fixture, clock)
	agent := newBlockingLifecycleAgent(false)
	service := classroom.NewService(pool, nil, nil, nil).WithClock(clock.Now).WithTeachingAgent(agent)
	outcome := make(chan lifecycleSubmitOutcome, 1)
	go func() {
		result, err := service.Submit(ctx, studentUserID, fixture.sessionID, "不会被保存的回答")
		outcome <- lifecycleSubmitOutcome{result: result, err: err}
	}()
	awaitAgentStart(t, agent.started)
	clock.Add(10 * time.Second)
	if _, err := service.AbandonSession(ctx, studentUserID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	close(agent.release)
	submitted := awaitSubmit(t, outcome)
	if !errors.Is(submitted.err, classroom.ErrSessionNotActive) {
		t.Fatalf("abandoned operation err=%v", submitted.err)
	}
	var answersAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
		t.Fatal(err)
	}
	if answersAfter != answersBefore {
		t.Fatalf("abandoned operation wrote answer: %d/%d", answersAfter, answersBefore)
	}
}

func TestExpiredOperationCannotCommitAfterAutomaticPause(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	clock := newLifecycleTestClock(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC))
	studentUserID, answersBefore := prepareLifecycleOperationFixture(t, ctx, pool, fixture, clock)
	agent := newBlockingLifecycleAgent(false)
	service := classroom.NewService(pool, nil, nil, nil).WithClock(clock.Now).WithTeachingAgent(agent)
	outcome := make(chan lifecycleSubmitOutcome, 1)
	go func() {
		result, err := service.Submit(ctx, studentUserID, fixture.sessionID, "超时回答")
		outcome <- lifecycleSubmitOutcome{result: result, err: err}
	}()
	awaitAgentStart(t, agent.started)

	clock.Add(6 * time.Minute)
	if err := service.RecoverStaleSessions(ctx, studentUserID); err != nil {
		t.Fatal(err)
	}
	close(agent.release)
	submitted := awaitSubmit(t, outcome)
	if !errors.Is(submitted.err, classroom.ErrClassroomChanged) {
		t.Fatalf("expired operation err=%v", submitted.err)
	}
	var status string
	var answersAfter int
	var processingToken *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status,processing_token FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&status, &processingToken); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" || processingToken != nil || answersAfter != answersBefore {
		t.Fatalf("expired operation status=%s token=%v answers=%d/%d", status, processingToken, answersAfter, answersBefore)
	}
}

func TestConcurrentSubmitIsRejectedWhileTeachingOperationOwnsLease(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	clock := newLifecycleTestClock(time.Date(2030, 1, 10, 1, 0, 0, 0, time.UTC))
	studentUserID, answersBefore := prepareLifecycleOperationFixture(t, ctx, pool, fixture, clock)
	agent := newBlockingLifecycleAgent(false)
	service := classroom.NewService(pool, nil, nil, nil).WithClock(clock.Now).WithTeachingAgent(agent)
	firstOutcome := make(chan lifecycleSubmitOutcome, 1)
	go func() {
		result, err := service.Submit(ctx, studentUserID, fixture.sessionID, "第一份回答")
		firstOutcome <- lifecycleSubmitOutcome{result: result, err: err}
	}()
	awaitAgentStart(t, agent.started)
	if _, err := service.Submit(ctx, studentUserID, fixture.sessionID, "重复回答"); !errors.Is(err, classroom.ErrClassroomChanged) {
		t.Fatalf("concurrent submit err=%v", err)
	}
	close(agent.release)
	if submitted := awaitSubmit(t, firstOutcome); submitted.err != nil {
		t.Fatal(submitted.err)
	}
	var answersAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
		t.Fatal(err)
	}
	if answersAfter != answersBefore+1 {
		t.Fatalf("concurrent submit answer count=%d/%d", answersAfter, answersBefore)
	}
}
