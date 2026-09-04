package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestGrowthAndParentReportDeriveTheSameLiveStreak(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	current := time.Date(2030, 1, 10, 16, 1, 0, 0, time.UTC) // 2030-01-11 00:01 in Shanghai.
	if _, err := pool.Exec(ctx, `INSERT INTO student_growth(student_id,streak_days,buildings_json) VALUES($1,99,'{}') ON CONFLICT(student_id) DO UPDATE SET streak_days=99`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	for _, date := range []string{"2030-01-09", "2030-01-10", "2030-01-11"} {
		if _, err := pool.Exec(ctx, `INSERT INTO student_activity_days(student_id,activity_date,completed_sessions,active_seconds,first_completed_at,last_completed_at) VALUES($1,$2::date,1,60,$3,$3)`, fixture.studentID, date, current); err != nil {
			t.Fatal(err)
		}
	}
	service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return current })
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})
	assertStreak := func(want int) {
		t.Helper()
		growth := performJSON(router, http.MethodGet, "/api/v1/student/growth", fixture.studentToken, nil)
		var growthPayload struct {
			Streak int `json:"streak_days"`
		}
		if err := json.Unmarshal(growth.Body.Bytes(), &growthPayload); err != nil || growth.Code != http.StatusOK || growthPayload.Streak != want {
			t.Fatalf("growth streak=%d code=%d body=%s err=%v", growthPayload.Streak, growth.Code, growth.Body.String(), err)
		}
		report := performJSON(router, http.MethodGet, "/api/v1/parent/child/"+fixture.studentID.String()+"/report", fixture.parentToken, nil)
		var reportPayload struct {
			Summary struct {
				Streak int `json:"streak_days"`
			} `json:"summary"`
		}
		if err := json.Unmarshal(report.Body.Bytes(), &reportPayload); err != nil || report.Code != http.StatusOK || reportPayload.Summary.Streak != want {
			t.Fatalf("report streak=%d code=%d body=%s err=%v", reportPayload.Summary.Streak, report.Code, report.Body.String(), err)
		}
	}
	assertStreak(3)
	if _, err := pool.Exec(ctx, `DELETE FROM student_activity_days WHERE student_id=$1 AND activity_date='2030-01-11'`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	assertStreak(2)
	if _, err := pool.Exec(ctx, `DELETE FROM student_activity_days WHERE student_id=$1 AND activity_date='2030-01-10'`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	assertStreak(0)
}

func TestCompletedSessionsMaintainRealConsecutiveDayStreakAndGrowthSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT subject_id,knowledge_point_id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$1`, fixture.releasedQuestionID).Scan(&subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	current := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	service := classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithClock(func() time.Time { return current })

	complete := func(sessionID uuid.UUID) {
		t.Helper()
		result, err := service.Submit(ctx, studentUserID, sessionID, fixture.privateCanary)
		if err != nil {
			t.Fatal(err)
		}
		if result.Action != "COMPLETE" {
			t.Fatalf("session %s action=%s", sessionID, result.Action)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET started_at=$2::timestamptz-interval '10 minutes',last_resumed_at=$2::timestamptz-interval '10 minutes',last_activity_at=$2,assistance_level=0,evidence_form='LIFE' WHERE id=$1`, fixture.sessionID, current); err != nil {
		t.Fatal(err)
	}
	complete(fixture.sessionID)

	for day := 1; day < 7; day++ {
		current = current.AddDate(0, 0, 1)
		sessionID := seedGrowthSession(t, ctx, pool, fixture.studentID, subjectID, fixture.releasedQuestionID, current)
		complete(sessionID)
	}
	var streak, activityDays, sessions, seconds int
	if err := pool.QueryRow(ctx, `SELECT streak_days FROM student_growth WHERE student_id=$1`, fixture.studentID).Scan(&streak); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),sum(completed_sessions),sum(active_seconds) FROM student_activity_days WHERE student_id=$1`, fixture.studentID).Scan(&activityDays, &sessions, &seconds); err != nil {
		t.Fatal(err)
	}
	if streak != 7 || activityDays != 7 || sessions != 7 || seconds != 4200 {
		t.Fatalf("seven-day activity streak=%d days=%d sessions=%d seconds=%d", streak, activityDays, sessions, seconds)
	}

	if _, err := pool.Exec(ctx, `UPDATE student_skill_states SET state='MASTERED',life_context_successes=1,variant_successes=1,textbook_successes=1,review_successes=1 WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	current = current.AddDate(0, 0, 2)
	gapSession := seedGrowthSession(t, ctx, pool, fixture.studentID, subjectID, fixture.releasedQuestionID, current)
	complete(gapSession)
	var buildings []byte
	if err := pool.QueryRow(ctx, `SELECT streak_days,buildings_json FROM student_growth WHERE student_id=$1`, fixture.studentID).Scan(&streak, &buildings); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		CompletedDays           int            `json:"completed_days"`
		CompletedSessions       int            `json:"completed_sessions"`
		MasteredKnowledgePoints int            `json:"mastered_knowledge_points"`
		MasteredBySubject       map[string]int `json:"mastered_by_subject"`
	}
	if err := json.Unmarshal(buildings, &snapshot); err != nil {
		t.Fatal(err)
	}
	if streak != 1 || snapshot.CompletedDays != 8 || snapshot.CompletedSessions != 8 || snapshot.MasteredKnowledgePoints != 1 || snapshot.MasteredBySubject["MATH"] != 1 {
		t.Fatalf("gap/snapshot streak=%d snapshot=%+v", streak, snapshot)
	}
}

func seedGrowthSession(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID, subjectID, questionID uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,started_at,last_resumed_at,last_activity_at,evidence_form) VALUES($1,$2,$3,$4,'ACTIVE',10,'ASK',$5::timestamptz-interval '10 minutes',$5::timestamptz-interval '10 minutes',$5,'LIFE')`, sessionID, studentID, subjectID, questionID, now); err != nil {
		t.Fatal(err)
	}
	return sessionID
}
