package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func b8TextFixture(t *testing.T, ctx context.Context, timeout time.Duration) (*classroom.Service, *pgxpool.Pool, uuid.UUID, uuid.UUID, func()) {
	t.Helper()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	userID := fixtureStudentUserID(t, ctx, pool, fixture.studentID)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ACTIVE',current_state='ASK',socratic_fail_count=0,processing_token=NULL,processing_until=NULL WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, nil).WithSubmitTimeout(timeout)
	return service, pool, userID, fixture.sessionID, func() { pool.Close() }
}

func b8StageFixture(t *testing.T, ctx context.Context, timeout time.Duration) (*classroom.Service, *pgxpool.Pool, stageFixture, classroom.StudentSession, func()) {
	t.Helper()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedCompleteStageFixture(t, ctx, pool)
	service := classroom.NewService(pool, nil, nil, nil).WithSubmitTimeout(timeout)
	session := startStageSession(t, stageRouter(pool, service), fixture)
	return service, pool, fixture, session, func() { pool.Close() }
}

func b8WaitForLease(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) time.Time {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var lease *time.Time
		if err := pool.QueryRow(ctx, `SELECT processing_until FROM learning_sessions WHERE id=$1`, sessionID).Scan(&lease); err != nil {
			t.Fatal(err)
		}
		if lease != nil {
			return *lease
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("submission did not acquire an operation lease")
	return time.Time{}
}

func b8SessionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) (string, time.Time, *uuid.UUID) {
	t.Helper()
	var status string
	var lastActivity time.Time
	var token *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status,last_activity_at,processing_token FROM learning_sessions WHERE id=$1`, sessionID).Scan(&status, &lastActivity, &token); err != nil {
		t.Fatal(err)
	}
	return status, lastActivity, token
}

func TestB8TextSubmitInFlightFreshnessDoesNotPauseAndHeartbeatDoesNotCountProviderWait(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 2*time.Second)
	defer cleanup()
	agent := &slowSubmitAgent{mode: "generate", delay: 250 * time.Millisecond}
	service.WithTeachingAgent(agent)
	resultCh := make(chan error, 1)
	go func() {
		_, err := service.Submit(ctx, userID, sessionID, "提交在途")
		resultCh <- err
	}()
	b8WaitForLease(t, ctx, pool, sessionID)
	_, before, _ := b8SessionState(t, ctx, pool, sessionID)
	if err := service.RecoverAllStaleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	timing, err := service.HeartbeatSession(ctx, userID, sessionID)
	if err != nil || timing.Status != "ACTIVE" {
		t.Fatalf("in-flight heartbeat timing=%+v err=%v", timing, err)
	}
	status, after, _ := b8SessionState(t, ctx, pool, sessionID)
	if status != "ACTIVE" || !after.Equal(before) {
		t.Fatalf("in-flight submit state status=%s before=%s after=%s", status, before, after)
	}
	if err := <-resultCh; err != nil {
		t.Fatal(err)
	}
	status, _, token := b8SessionState(t, ctx, pool, sessionID)
	if status != "ACTIVE" || token != nil {
		t.Fatalf("completed submit state status=%s token=%v", status, token)
	}
}

func TestB8StructuredSubmitInFlightFreshnessDoesNotPause(t *testing.T) {
	ctx := context.Background()
	service, pool, fixture, session, cleanup := b8StageFixture(t, ctx, 2*time.Second)
	defer cleanup()
	agent := newBlockingStageAgent()
	service.WithTeachingAgent(agent)
	response, err := json.Marshal(incorrectStageResponse(t, ctx, pool, session.QuestionID))
	if err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, submitErr := service.SubmitStage(ctx, fixture.studentUserID, classroom.StageSubmitRequest{
			SessionID: session.ID, OperationID: uuid.New(), Stage: session.StageFlow.Stage,
			TaskID: session.QuestionID, TaskVersion: session.StageFlow.TaskVersion,
			Kind: classroom.StageAttemptAnswer, Response: response,
		})
		resultCh <- submitErr
	}()
	<-agent.started
	status, before, _ := b8SessionState(t, ctx, pool, session.ID)
	if status != "ACTIVE" {
		t.Fatalf("structured in-flight status=%s", status)
	}
	if err := service.RecoverAllStaleSessions(ctx); err != nil {
		t.Fatal(err)
	}
	timing, err := service.HeartbeatSession(ctx, fixture.studentUserID, session.ID)
	if err != nil || timing.Status != "ACTIVE" {
		t.Fatalf("structured heartbeat timing=%+v err=%v", timing, err)
	}
	status, after, _ := b8SessionState(t, ctx, pool, session.ID)
	if status != "ACTIVE" || !after.Equal(before) {
		t.Fatalf("structured in-flight state status=%s before=%s after=%s", status, before, after)
	}
	close(agent.release)
	if err := <-resultCh; err != nil {
		t.Fatal(err)
	}
}

func TestB8VisibilityPauseWithInFlightTextSubmitResumeClearsLease(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 2*time.Second)
	defer cleanup()
	agent := &slowSubmitAgent{mode: "generate", delay: 250 * time.Millisecond}
	service.WithTeachingAgent(agent)
	resultCh := make(chan error, 1)
	go func() {
		_, err := service.Submit(ctx, userID, sessionID, "后台暂停")
		resultCh <- err
	}()
	b8WaitForLease(t, ctx, pool, sessionID)
	paused, err := service.PauseSession(ctx, userID, sessionID)
	if err != nil || paused.Status != "PAUSED" {
		t.Fatalf("visibility pause=%+v err=%v", paused, err)
	}
	resumed, err := service.ResumeSession(ctx, userID, sessionID)
	if err != nil || resumed.Status != "ACTIVE" {
		t.Fatalf("resume after visibility pause=%+v err=%v", resumed, err)
	}
	if err := <-resultCh; !errors.Is(err, classroom.ErrClassroomChanged) {
		t.Fatalf("in-flight submit after lease-owning resume err=%v", err)
	}
	status, _, token := b8SessionState(t, ctx, pool, sessionID)
	if status != "ACTIVE" || token != nil {
		t.Fatalf("resumed text state status=%s token=%v", status, token)
	}
}

func TestB8VisibilityPauseWithInFlightStructuredSubmitResumeClearsLease(t *testing.T) {
	ctx := context.Background()
	service, pool, fixture, session, cleanup := b8StageFixture(t, ctx, 2*time.Second)
	defer cleanup()
	agent := newBlockingStageAgent()
	service.WithTeachingAgent(agent)
	response, err := json.Marshal(incorrectStageResponse(t, ctx, pool, session.QuestionID))
	if err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan error, 1)
	go func() {
		_, submitErr := service.SubmitStage(ctx, fixture.studentUserID, classroom.StageSubmitRequest{
			SessionID: session.ID, OperationID: uuid.New(), Stage: session.StageFlow.Stage,
			TaskID: session.QuestionID, TaskVersion: session.StageFlow.TaskVersion,
			Kind: classroom.StageAttemptAnswer, Response: response,
		})
		resultCh <- submitErr
	}()
	<-agent.started
	if _, err := service.PauseSession(ctx, fixture.studentUserID, session.ID); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.ResumeSession(ctx, fixture.studentUserID, session.ID)
	if err != nil || resumed.Status != "ACTIVE" {
		t.Fatalf("structured resume=%+v err=%v", resumed, err)
	}
	close(agent.release)
	if err := <-resultCh; !errors.Is(err, classroom.ErrClassroomChanged) {
		t.Fatalf("structured submit after resume err=%v", err)
	}
	status, _, token := b8SessionState(t, ctx, pool, session.ID)
	if status != "ACTIVE" || token != nil {
		t.Fatalf("structured resumed state status=%s token=%v", status, token)
	}
}

func TestB8CancelledSubmitThenResumeIsNotDeadlocked(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 2*time.Second)
	defer cleanup()
	agent := &slowSubmitAgent{mode: "analyze", delay: 250 * time.Millisecond}
	service.WithTeachingAgent(agent)
	requestCtx, cancel := context.WithCancel(ctx)
	resultCh := make(chan error, 1)
	go func() {
		_, err := service.Submit(requestCtx, userID, sessionID, "取消后恢复")
		resultCh <- err
	}()
	b8WaitForLease(t, ctx, pool, sessionID)
	if _, err := service.PauseSession(ctx, userID, sessionID); err != nil {
		t.Fatal(err)
	}
	cancel()
	resumed, err := service.ResumeSession(ctx, userID, sessionID)
	if err != nil || resumed.Status != "ACTIVE" {
		t.Fatalf("cancelled resume=%+v err=%v", resumed, err)
	}
	<-resultCh
	status, _, token := b8SessionState(t, ctx, pool, sessionID)
	if status != "ACTIVE" || token != nil {
		t.Fatalf("cancelled resumed state status=%s token=%v", status, token)
	}
}

func TestB8ServerTimeoutThenResumeIsNotDeadlocked(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 30*time.Millisecond)
	defer cleanup()
	service.WithTeachingAgent(&slowSubmitAgent{mode: "analyze", delay: 200 * time.Millisecond})
	if _, err := service.Submit(ctx, userID, sessionID, "超时后恢复"); !errors.Is(err, classroom.ErrSubmitTimedOut) {
		t.Fatalf("timeout err=%v", err)
	}
	if _, err := service.PauseSession(ctx, userID, sessionID); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.ResumeSession(ctx, userID, sessionID)
	if err != nil || resumed.Status != "ACTIVE" {
		t.Fatalf("timeout resume=%+v err=%v", resumed, err)
	}
	status, _, token := b8SessionState(t, ctx, pool, sessionID)
	if status != "ACTIVE" || token != nil {
		t.Fatalf("timeout resumed state status=%s token=%v", status, token)
	}
}

func TestB8StructuredCancellationAndTimeoutThenResumeAreNotDeadlocked(t *testing.T) {
	ctx := context.Background()
	t.Run("cancelled", func(t *testing.T) {
		service, pool, fixture, session, cleanup := b8StageFixture(t, ctx, 2*time.Second)
		defer cleanup()
		agent := newBlockingStageAgent()
		service.WithTeachingAgent(agent)
		response, err := json.Marshal(incorrectStageResponse(t, ctx, pool, session.QuestionID))
		if err != nil {
			t.Fatal(err)
		}
		requestCtx, cancel := context.WithCancel(ctx)
		resultCh := make(chan error, 1)
		go func() {
			_, submitErr := service.SubmitStage(requestCtx, fixture.studentUserID, classroom.StageSubmitRequest{
				SessionID: session.ID, OperationID: uuid.New(), Stage: session.StageFlow.Stage,
				TaskID: session.QuestionID, TaskVersion: session.StageFlow.TaskVersion,
				Kind: classroom.StageAttemptAnswer, Response: response,
			})
			resultCh <- submitErr
		}()
		<-agent.started
		if _, err := service.PauseSession(ctx, fixture.studentUserID, session.ID); err != nil {
			t.Fatal(err)
		}
		cancel()
		resumed, err := service.ResumeSession(ctx, fixture.studentUserID, session.ID)
		if err != nil || resumed.Status != "ACTIVE" {
			t.Fatalf("cancelled structured resume=%+v err=%v", resumed, err)
		}
		close(agent.release)
		<-resultCh
		status, _, token := b8SessionState(t, ctx, pool, session.ID)
		if status != "ACTIVE" || token != nil {
			t.Fatalf("cancelled structured state status=%s token=%v", status, token)
		}
	})

	t.Run("timed-out", func(t *testing.T) {
		service, pool, fixture, session, cleanup := b8StageFixture(t, ctx, 30*time.Millisecond)
		defer cleanup()
		service.WithTeachingAgent(&slowSubmitAgent{mode: "generate", delay: 200 * time.Millisecond})
		response, err := json.Marshal(incorrectStageResponse(t, ctx, pool, session.QuestionID))
		if err != nil {
			t.Fatal(err)
		}
		_, submitErr := service.SubmitStage(ctx, fixture.studentUserID, classroom.StageSubmitRequest{
			SessionID: session.ID, OperationID: uuid.New(), Stage: session.StageFlow.Stage,
			TaskID: session.QuestionID, TaskVersion: session.StageFlow.TaskVersion,
			Kind: classroom.StageAttemptAnswer, Response: response,
		})
		if !errors.Is(submitErr, classroom.ErrSubmitTimedOut) {
			t.Fatalf("structured timeout err=%v", submitErr)
		}
		if _, err := service.PauseSession(ctx, fixture.studentUserID, session.ID); err != nil {
			t.Fatal(err)
		}
		resumed, err := service.ResumeSession(ctx, fixture.studentUserID, session.ID)
		if err != nil || resumed.Status != "ACTIVE" {
			t.Fatalf("timed-out structured resume=%+v err=%v", resumed, err)
		}
		status, _, token := b8SessionState(t, ctx, pool, session.ID)
		if status != "ACTIVE" || token != nil {
			t.Fatalf("timed-out structured state status=%s token=%v", status, token)
		}
	})
}

func TestB8ResumeAndSubmitCloseInEitherOrderLeaveOneActiveLeaseFreeSession(t *testing.T) {
	ctx := context.Background()
	for _, order := range []string{"submit-then-resume", "resume-then-submit"} {
		t.Run(order, func(t *testing.T) {
			service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 30*time.Millisecond)
			defer cleanup()
			service.WithTeachingAgent(&slowSubmitAgent{mode: "analyze", delay: 200 * time.Millisecond})
			if order == "submit-then-resume" {
				if _, err := service.Submit(ctx, userID, sessionID, "先提交"); !errors.Is(err, classroom.ErrSubmitTimedOut) {
					t.Fatalf("submit first err=%v", err)
				}
			}
			if _, err := service.PauseSession(ctx, userID, sessionID); err != nil {
				t.Fatal(err)
			}
			if resumed, err := service.ResumeSession(ctx, userID, sessionID); err != nil || resumed.Status != "ACTIVE" {
				t.Fatalf("resume order=%s result=%+v err=%v", order, resumed, err)
			}
			if order == "resume-then-submit" {
				if _, err := service.Submit(ctx, userID, sessionID, "恢复后提交"); !errors.Is(err, classroom.ErrSubmitTimedOut) {
					t.Fatalf("submit second err=%v", err)
				}
			}
			status, _, token := b8SessionState(t, ctx, pool, sessionID)
			if status != "ACTIVE" || token != nil {
				t.Fatalf("order=%s final status=%s token=%v", order, status, token)
			}
		})
	}
}

func TestB8StaleRecoveryStillPausesStudentWhoActuallyLeft(t *testing.T) {
	ctx := context.Background()
	service, pool, userID, sessionID, cleanup := b8TextFixture(t, ctx, 2*time.Second)
	defer cleanup()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET last_activity_at=now()-interval '2 minutes',processing_token=NULL,processing_until=NULL WHERE id=$1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverStaleSessions(ctx, userID); err != nil {
		t.Fatal(err)
	}
	status, _, token := b8SessionState(t, ctx, pool, sessionID)
	if status != "PAUSED" || token != nil {
		t.Fatalf("real idle recovery status=%s token=%v", status, token)
	}
}
