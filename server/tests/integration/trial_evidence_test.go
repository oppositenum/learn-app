package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/trial"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestSevenDayTrialRequiresChildSubmittedWillingnessAndRealActivity(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	ownerToken := seedOwner(t, ctx, pool)
	now := time.Now()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='COMPLETED',current_state='COMPLETE',ended_at=$2,actual_seconds=600 WHERE id=$1`, fixture.sessionID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_activity_days(student_id,activity_date,completed_sessions,active_seconds,first_completed_at,last_completed_at) VALUES($1,current_date,1,600,$2,$2)`, fixture.studentID, now); err != nil {
		t.Fatal(err)
	}
	for offset := 1; offset <= 6; offset++ {
		sessionID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,started_at,ended_at,status,target_minutes,actual_seconds,current_state) VALUES($1,$2,'00000000-0000-4000-8000-000000000001',$3,current_date-$4::integer,(current_date-$4::integer)+interval '10 minutes','COMPLETED',20,600,'COMPLETE')`, sessionID, fixture.studentID, fixture.releasedQuestionID, offset); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO student_activity_days(student_id,activity_date,completed_sessions,active_seconds,first_completed_at,last_completed_at) VALUES($1,current_date-$2::integer,1,600,current_date-$2::integer,(current_date-$2::integer)+interval '10 minutes')`, fixture.studentID, offset); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO student_session_reflections(session_id,student_id,reflection_date,willingness) VALUES($1,$2,current_date-$3::integer,'CONTINUE_TOMORROW')`, sessionID, fixture.studentID, offset); err != nil {
			t.Fatal(err)
		}
	}

	handler := trial.NewHandler(pool)
	router := api.NewRouter(api.Dependencies{Authenticate: auth.NewSessionAuthenticator(pool).Middleware, Trial: handler})
	path := "/api/v1/student/sessions/" + fixture.sessionID.String() + "/reflection"
	unknown := performJSON(router, http.MethodPost, path, fixture.studentToken, map[string]any{"willingness": "CONTINUE_TOMORROW", "answer": "not allowed"})
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("reflection accepted unknown field: %d %s", unknown.Code, unknown.Body.String())
	}
	parentAttempt := performJSON(router, http.MethodPost, path, fixture.parentToken, map[string]any{"willingness": "CONTINUE_TOMORROW"})
	if parentAttempt.Code != http.StatusForbidden {
		t.Fatalf("Parent submitted child willingness: %d", parentAttempt.Code)
	}
	saved := performJSON(router, http.MethodPost, path, fixture.studentToken, map[string]any{"willingness": "CONTINUE_TOMORROW"})
	if saved.Code != http.StatusOK || !strings.Contains(saved.Body.String(), `"saved":true`) {
		t.Fatalf("Student reflection=%d %s", saved.Code, saved.Body.String())
	}
	studentReport := performJSON(router, http.MethodGet, "/api/v1/owner/trials", fixture.studentToken, nil)
	if studentReport.Code != http.StatusForbidden {
		t.Fatalf("Student accessed Owner trial report: %d", studentReport.Code)
	}
	report := performJSON(router, http.MethodGet, "/api/v1/owner/trials", ownerToken, nil)
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), `"longest_willing_streak_days":7`) || !strings.Contains(report.Body.String(), `"current_willing_streak_days":7`) || !strings.Contains(report.Body.String(), `"seven_day_core_complete":true`) {
		t.Fatalf("qualified trial report=%d %s", report.Code, report.Body.String())
	}

	paused := performJSON(router, http.MethodPost, path, fixture.studentToken, map[string]any{"willingness": "PAUSE"})
	if paused.Code != http.StatusOK {
		t.Fatalf("pause reflection=%d %s", paused.Code, paused.Body.String())
	}
	report = performJSON(router, http.MethodGet, "/api/v1/owner/trials", ownerToken, nil)
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), `"longest_willing_streak_days":6`) || !strings.Contains(report.Body.String(), `"current_willing_streak_days":0`) || !strings.Contains(report.Body.String(), `"seven_day_core_complete":false`) {
		t.Fatalf("paused trial report=%d %s", report.Code, report.Body.String())
	}

	historicalSessionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,started_at,ended_at,status,target_minutes,actual_seconds,current_state) VALUES($1,$2,'00000000-0000-4000-8000-000000000001',$3,current_date-7,(current_date-7)+interval '10 minutes','COMPLETED',20,600,'COMPLETE')`, historicalSessionID, fixture.studentID, fixture.releasedQuestionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_activity_days(student_id,activity_date,completed_sessions,active_seconds,first_completed_at,last_completed_at) VALUES($1,current_date-7,1,600,current_date-7,(current_date-7)+interval '10 minutes')`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO student_session_reflections(session_id,student_id,reflection_date,willingness) VALUES($1,$2,current_date-7,'CONTINUE_TOMORROW')`, historicalSessionID, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	report = performJSON(router, http.MethodGet, "/api/v1/owner/trials", ownerToken, nil)
	if report.Code != http.StatusOK || !strings.Contains(report.Body.String(), `"longest_willing_streak_days":7`) || !strings.Contains(report.Body.String(), `"current_willing_streak_days":0`) || !strings.Contains(report.Body.String(), `"seven_day_core_complete":true`) {
		t.Fatalf("historical seven-day evidence was lost: %d %s", report.Code, report.Body.String())
	}
}

func TestTrialDatabaseRejectsReflectionForActiveSession(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	_, err := pool.Exec(ctx, `INSERT INTO student_session_reflections(session_id,student_id,reflection_date,willingness) VALUES($1,$2,current_date,'CONTINUE_TOMORROW')`, fixture.sessionID, fixture.studentID)
	if err == nil || !strings.Contains(err.Error(), "reflection requires a completed session") {
		t.Fatalf("active session reflection error=%v", err)
	}
}
