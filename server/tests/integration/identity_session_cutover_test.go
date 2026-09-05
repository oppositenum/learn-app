package integration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	parentrepo "github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/realtime"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestB1BLogoutPausesSessionAndPublishesImmediately(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	studentUserID := b1bStudentUserID(t, ctx, pool, fixture.studentID)
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions
SET started_at=$2::timestamptz-interval '15 minutes',accumulated_seconds=30,actual_seconds=30,
    last_resumed_at=$2::timestamptz-interval '10 minutes',last_activity_at=$2::timestamptz-interval '2 minutes'
WHERE id=$1`, fixture.sessionID, now); err != nil {
		t.Fatal(err)
	}

	hub := realtime.NewHub()
	parents := parentrepo.NewRepository(pool)
	service := classroom.NewService(pool, hub, nil, nil).WithClock(func() time.Time { return now })
	classrooms := classroom.NewHandler(service, pool, parents)
	authenticator := auth.NewSessionAuthenticator(pool)
	router := api.NewRouter(api.Dependencies{
		Authenticate: authenticator.Middleware,
		Identity:     auth.NewHandler(pool, service),
		Classroom:    classrooms,
	})
	parentEvents, unsubscribe := hub.Subscribe(fixture.studentID.String(), auth.RoleParent)
	defer unsubscribe()

	logout := performJSON(router, http.MethodPost, "/api/v1/auth/logout", fixture.studentToken, nil)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout=%d %s", logout.Code, logout.Body.String())
	}

	var status string
	var accumulated, actual int
	var lastResumedAt *time.Time
	var processingToken *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status,accumulated_seconds,actual_seconds,last_resumed_at,processing_token FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&status, &accumulated, &actual, &lastResumedAt, &processingToken); err != nil {
		t.Fatal(err)
	}
	const expectedActiveSeconds = 30 + 8*60
	if status != "PAUSED" || accumulated != expectedActiveSeconds || actual != expectedActiveSeconds || lastResumedAt != nil || processingToken != nil {
		t.Fatalf("closed session status=%s accumulated=%d actual=%d last_resumed=%v processing=%v", status, accumulated, actual, lastResumedAt, processingToken)
	}

	studentHash := sha256.Sum256([]byte(fixture.studentToken))
	var revoked bool
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id=$1 AND token_hash=$2`, studentUserID, studentHash[:]).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("authentication revocation revoked=%v err=%v", revoked, err)
	}

	select {
	case raw := <-parentEvents:
		var event realtime.ParentEventDTO
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Status        string `json:"status"`
			ActiveSeconds int    `json:"active_seconds"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if event.Type != realtime.EventSessionPaused || event.SessionID != fixture.sessionID.String() || payload.Status != "PAUSED" || payload.ActiveSeconds != expectedActiveSeconds {
			t.Fatalf("logout realtime event=%+v payload=%+v", event, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logout did not publish SESSION_PAUSED")
	}

	var eventType string
	var studentPayload, parentPayload json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT type,student_payload_json,parent_payload_json FROM tutor_events WHERE session_id=$1 ORDER BY sequence DESC LIMIT 1`, fixture.sessionID).Scan(&eventType, &studentPayload, &parentPayload); err != nil {
		t.Fatal(err)
	}
	var persistedStudent, persistedParent struct {
		Status        string `json:"status"`
		ActiveSeconds int    `json:"active_seconds"`
	}
	studentErr := json.Unmarshal(studentPayload, &persistedStudent)
	parentErr := json.Unmarshal(parentPayload, &persistedParent)
	if eventType != string(realtime.EventSessionPaused) || studentErr != nil || parentErr != nil || persistedStudent.Status != "PAUSED" || persistedStudent.ActiveSeconds != expectedActiveSeconds || persistedParent != persistedStudent {
		t.Fatalf("persisted logout event type=%s student=%s parent=%s", eventType, studentPayload, parentPayload)
	}

	for _, request := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/heartbeat", nil},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/pause", nil},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/resume", nil},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/abandon", nil},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/answers", map[string]any{"answer": "stale"}},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/support", map[string]any{"type": "HINT"}},
		{http.MethodPost, "/api/v1/student/sessions/" + fixture.sessionID.String() + "/voice/complete", nil},
		{http.MethodPost, "/api/v1/student/sessions", map[string]any{"plan_block_id": uuid.New()}},
	} {
		response := performJSON(router, request.method, request.path, fixture.studentToken, request.body)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("revoked token %s %s=%d %s", request.method, request.path, response.Code, response.Body.String())
		}
	}

	children := performJSON(router, http.MethodGet, "/api/v1/parent/children", fixture.parentToken, nil)
	if children.Code != http.StatusOK {
		t.Fatalf("parent children=%d %s", children.Code, children.Body.String())
	}
	var childrenPayload struct {
		Children []struct {
			StudentID       uuid.UUID  `json:"student_id"`
			ActiveSessionID *uuid.UUID `json:"active_session_id"`
		} `json:"children"`
	}
	if err := json.Unmarshal(children.Body.Bytes(), &childrenPayload); err != nil || len(childrenPayload.Children) != 1 || childrenPayload.Children[0].StudentID != fixture.studentID || childrenPayload.Children[0].ActiveSessionID != nil {
		t.Fatalf("parent active classroom after logout=%s err=%v", children.Body.String(), err)
	}
}

func TestB1BLogoutCutoverIsAtomic(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	studentUserID := b1bStudentUserID(t, ctx, pool, fixture.studentID)
	if _, err := pool.Exec(ctx, `
CREATE FUNCTION reject_b1b_pause_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.type='SESSION_PAUSED' THEN
        RAISE EXCEPTION 'forced B1B event failure';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER reject_b1b_pause_event BEFORE INSERT ON tutor_events
FOR EACH ROW EXECUTE FUNCTION reject_b1b_pause_event()`); err != nil {
		t.Fatal(err)
	}

	hub := realtime.NewHub()
	service := classroom.NewService(pool, hub, nil, nil)
	authenticator := auth.NewSessionAuthenticator(pool)
	router := api.NewRouter(api.Dependencies{
		Authenticate: authenticator.Middleware,
		Identity:     auth.NewHandler(pool, service),
	})

	logout := performJSON(router, http.MethodPost, "/api/v1/auth/logout", fixture.studentToken, nil)
	if logout.Code != http.StatusInternalServerError {
		t.Fatalf("forced failure logout=%d %s", logout.Code, logout.Body.String())
	}
	studentHash := sha256.Sum256([]byte(fixture.studentToken))
	var revoked bool
	var status string
	if err := pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id=$1 AND token_hash=$2`, studentUserID, studentHash[:]).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if revoked || status != "ACTIVE" {
		t.Fatalf("failed cutover was not rolled back: revoked=%v status=%s", revoked, status)
	}
}

func TestB1BLogoutWinsResumeRaceInBothCommitOrders(t *testing.T) {
	t.Run("resume commits before logout", func(t *testing.T) {
		ctx, pool, fixture, service, authenticator, identity, classrooms := b1bRaceFixture(t)
		studentUserID := b1bStudentUserID(t, ctx, pool, fixture.studentID)
		if _, err := service.PauseSession(ctx, studentUserID, fixture.sessionID); err != nil {
			t.Fatal(err)
		}

		blockedLogout, authenticated, release := b1bBlockAfterAuthentication(authenticator, http.HandlerFunc(identity.Logout))
		logoutDone := b1bServeAsync(blockedLogout, b1bRequest(http.MethodPost, "/api/v1/auth/logout", fixture.studentToken, uuid.Nil))
		<-authenticated

		resumeHandler := authenticator.Middleware(auth.RequireRole(auth.RoleStudent, http.HandlerFunc(classrooms.ResumeSession)))
		resume := httptest.NewRecorder()
		resumeHandler.ServeHTTP(resume, b1bRequest(http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/resume", fixture.studentToken, fixture.sessionID))
		if resume.Code != http.StatusOK {
			t.Fatalf("resume first=%d %s", resume.Code, resume.Body.String())
		}
		close(release)
		logout := <-logoutDone
		if logout.Code != http.StatusNoContent {
			t.Fatalf("logout second=%d %s", logout.Code, logout.Body.String())
		}
		b1bAssertPaused(t, ctx, pool, fixture.sessionID)
	})

	t.Run("logout commits before stale authenticated resume", func(t *testing.T) {
		ctx, pool, fixture, service, authenticator, identity, classrooms := b1bRaceFixture(t)
		_ = service
		resumeTarget := auth.RequireRole(auth.RoleStudent, http.HandlerFunc(classrooms.ResumeSession))
		blockedResume, authenticated, release := b1bBlockAfterAuthentication(authenticator, resumeTarget)
		resumeDone := b1bServeAsync(blockedResume, b1bRequest(http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/resume", fixture.studentToken, fixture.sessionID))
		<-authenticated

		logoutHandler := authenticator.Middleware(http.HandlerFunc(identity.Logout))
		logout := httptest.NewRecorder()
		logoutHandler.ServeHTTP(logout, b1bRequest(http.MethodPost, "/api/v1/auth/logout", fixture.studentToken, uuid.Nil))
		if logout.Code != http.StatusNoContent {
			t.Fatalf("logout first=%d %s", logout.Code, logout.Body.String())
		}
		close(release)
		resume := <-resumeDone
		if resume.Code != http.StatusUnauthorized {
			t.Fatalf("stale resume second=%d %s", resume.Code, resume.Body.String())
		}
		b1bAssertPaused(t, ctx, pool, fixture.sessionID)
	})
}

func b1bRaceFixture(t *testing.T) (context.Context, *pgxpool.Pool, securityFixture, *classroom.Service, *auth.SessionAuthenticator, *auth.Handler, *classroom.Handler) {
	t.Helper()
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	service := classroom.NewService(pool, realtime.NewHub(), nil, nil)
	authenticator := auth.NewSessionAuthenticator(pool)
	return ctx, pool, fixture, service, authenticator, auth.NewHandler(pool, service), classroom.NewHandler(service, pool, parentrepo.NewRepository(pool))
}

func b1bBlockAfterAuthentication(authenticator *auth.SessionAuthenticator, next http.Handler) (http.Handler, <-chan struct{}, chan struct{}) {
	authenticated := make(chan struct{})
	release := make(chan struct{})
	blocked := authenticator.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(authenticated)
		<-release
		next.ServeHTTP(writer, request)
	}))
	return blocked, authenticated, release
}

func b1bServeAsync(handler http.Handler, request *http.Request) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		done <- response
	}()
	return done
}

func b1bRequest(method, path, token string, sessionID uuid.UUID) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if sessionID != uuid.Nil {
		request.SetPathValue("session_id", sessionID.String())
	}
	return request
}

func b1bStudentUserID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, studentID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func b1bAssertPaused(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID uuid.UUID) {
	t.Helper()
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_sessions WHERE id=$1`, sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PAUSED" {
		t.Fatalf("final learning session status=%s, want PAUSED", status)
	}
}
