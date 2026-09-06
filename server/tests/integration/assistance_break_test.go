package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestB2BreakFollowedByCorrectAnswerRemainsIndependent(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT student.user_id,question.knowledge_point_id FROM learning_sessions session JOIN students student ON student.id=session.student_id JOIN questions question ON question.id=session.current_question_id WHERE session.id=$1`, fixture.sessionID).Scan(&studentUserID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET current_state='BREAK',socratic_fail_count=1,assistance_level=0,evidence_form='LIFE' WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	result, err := classroom.NewService(pool, nil, nil, nil).Submit(ctx, studentUserID, fixture.sessionID, fixture.privateCanary)
	if err != nil {
		t.Fatal(err)
	}
	var independent, assisted, life int
	if err := pool.QueryRow(ctx, `SELECT independent_successes,assisted_successes,life_context_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, knowledgePointID).Scan(&independent, &assisted, &life); err != nil {
		t.Fatal(err)
	}
	if independent != 1 || assisted != 0 || life != 1 {
		t.Fatalf("BREAK evidence independent=%d assisted=%d life=%d result=%+v", independent, assisted, life, result)
	}
}
