package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

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
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET started_at=$2::timestamptz-interval '10 minutes',evidence_form='LIFE' WHERE id=$1`, fixture.sessionID, current); err != nil {
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
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state,started_at,evidence_form) VALUES($1,$2,$3,$4,'ACTIVE',10,'ASK',$5::timestamptz-interval '10 minutes','LIFE')`, sessionID, studentID, subjectID, questionID, now); err != nil {
		t.Fatal(err)
	}
	return sessionID
}
