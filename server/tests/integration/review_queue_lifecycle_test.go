package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type b2ReviewFixture struct {
	securityFixture
	studentUserID, subjectID, knowledgePointID uuid.UUID
	queueID, otherQueueID, planID, blockID     uuid.UUID
	now                                        time.Time
}

func prepareB2ReviewFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, assistance int) b2ReviewFixture {
	t.Helper()
	fixture := seedSecurityFixture(t, ctx, pool)
	var studentUserID, subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `
SELECT student.user_id,session.subject_id,question.knowledge_point_id
FROM learning_sessions session
JOIN students student ON student.id=session.student_id
JOIN questions question ON question.id=session.current_question_id
WHERE session.id=$1`, fixture.sessionID).Scan(&studentUserID, &subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	now := time.Date(2032, 3, 4, 10, 30, 0, 0, location)
	queueID, otherQueueID, planID, blockID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority)
VALUES($1,$2,$3,'MASTERY',$5::timestamptz-interval '1 hour',50),
      ($4,$2,$3,'MISCONCEPTION',$5::timestamptz+interval '72 hours',80)`, queueID, fixture.studentID, knowledgePointID, otherQueueID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,$3,20,'ACTIVE')`, planID, fixture.studentID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status,review_queue_id)
VALUES($1,$2,1,$3,$4,20,'REVIEW','spaced_review_due','ACTIVE',$5)`, blockID, planID, subjectID, knowledgePointID, queueID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE learning_sessions
SET plan_block_id=$2,review_queue_id=$3,evidence_form='REVIEW',current_state='ASK',
    socratic_fail_count=0,assistance_level=$4,started_at=$5::timestamptz-interval '1 minute',
    last_resumed_at=$5::timestamptz-interval '1 minute',last_activity_at=$5::timestamptz-interval '5 seconds'
WHERE id=$1`, fixture.sessionID, blockID, queueID, assistance, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO student_skill_states(
    student_id,knowledge_point_id,state,score_internal,independent_successes,
    life_context_successes,variant_successes,textbook_successes,next_review_at)
VALUES($1,$2,'UNDERSTOOD',75,3,1,1,1,$3::timestamptz-interval '1 hour')`, fixture.studentID, knowledgePointID, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b2ReviewFixture{
		securityFixture:  fixture,
		studentUserID:    studentUserID,
		subjectID:        subjectID,
		knowledgePointID: knowledgePointID,
		queueID:          queueID,
		otherQueueID:     otherQueueID,
		planID:           planID,
		blockID:          blockID,
		now:              now,
	}
}

func TestB2ReviewQueueAssistedThenIndependentLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := prepareB2ReviewFixture(t, ctx, pool, 0)
	service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return fixture.now })
	if _, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, "wrong"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, fixture.privateCanary)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "COMPLETED" || result.MasteryState != mastery.Understood {
		t.Fatalf("assisted completion=%+v", result)
	}

	var status, reviewResult string
	var attempts int
	var resolvedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT status,result,attempts,resolved_at FROM review_queue WHERE id=$1`, fixture.queueID).Scan(&status, &reviewResult, &attempts, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	if status != "COMPLETED" || reviewResult != "ASSISTED_SUCCESS" || attempts != 1 || !resolvedAt.Equal(fixture.now) {
		t.Fatalf("assisted queue=%s/%s attempts=%d resolved=%s", status, reviewResult, attempts, resolvedAt)
	}
	var otherStatus string
	var otherAttempts int
	if err := pool.QueryRow(ctx, `SELECT status,attempts FROM review_queue WHERE id=$1`, fixture.otherQueueID).Scan(&otherStatus, &otherAttempts); err != nil {
		t.Fatal(err)
	}
	if otherStatus != "PENDING" || otherAttempts != 0 {
		t.Fatalf("unbound same-knowledge queue changed: %s/%d", otherStatus, otherAttempts)
	}

	var nextQueueID uuid.UUID
	var nextDue time.Time
	if err := pool.QueryRow(ctx, `SELECT id,due_at FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MASTERY' AND status='PENDING'`, fixture.studentID, fixture.knowledgePointID).Scan(&nextQueueID, &nextDue); err != nil {
		t.Fatal(err)
	}
	wantNext := fixture.now.Add(24 * time.Hour)
	if !nextDue.Equal(wantNext) {
		t.Fatalf("next review due=%s want=%s", nextDue, wantNext)
	}
	var independent, assisted, life, variant, textbook, review int
	if err := pool.QueryRow(ctx, `SELECT independent_successes,assisted_successes,life_context_successes,variant_successes,textbook_successes,review_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, fixture.knowledgePointID).Scan(&independent, &assisted, &life, &variant, &textbook, &review); err != nil {
		t.Fatal(err)
	}
	if independent != 3 || assisted != 1 || life != 1 || variant != 1 || textbook != 1 || review != 0 {
		t.Fatalf("assisted evidence independent=%d assisted=%d forms=%d/%d/%d review=%d", independent, assisted, life, variant, textbook, review)
	}

	if _, err := pool.Exec(ctx, `UPDATE learning_plans SET status='COMPLETED' WHERE id=$1`, fixture.planID); err != nil {
		t.Fatal(err)
	}
	plannerService := planner.NewService(pool)
	beforePlan, _, err := plannerService.EnsureWithStatus(ctx, fixture.studentID, wantNext.Add(-time.Second), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range beforePlan.Blocks {
		if block.KnowledgePointID != nil && *block.KnowledgePointID == fixture.knowledgePointID && block.Mode == planner.ModeReview {
			t.Fatalf("review was due before NextReviewAt: %+v", block)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_plans SET status='REPLACED' WHERE id=$1`, beforePlan.ID); err != nil {
		t.Fatal(err)
	}
	afterPlan, _, err := plannerService.EnsureWithStatus(ctx, fixture.studentID, wantNext, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	var reviewBlockID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM learning_plan_blocks WHERE plan_id=$1 AND knowledge_point_id=$2 AND mode='REVIEW' AND review_queue_id=$3`, afterPlan.ID, fixture.knowledgePointID, nextQueueID).Scan(&reviewBlockID); err != nil {
		t.Fatalf("due queue was not bound into the plan: %v", err)
	}

	secondSessionID := uuid.New()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET status='ACTIVE' WHERE id=$1`, reviewBlockID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE learning_plans SET status='ACTIVE' WHERE id=$1`, afterPlan.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,plan_block_id,review_queue_id,subject_id,current_question_id,status,target_minutes,current_state,evidence_form,started_at,last_resumed_at,last_activity_at)
VALUES($1,$2,$3,$4,$5,$6,'ACTIVE',20,'ASK','REVIEW',$7,$7,$7)`,
			secondSessionID, fixture.studentID, reviewBlockID, nextQueueID, fixture.subjectID, fixture.releasedQuestionID, wantNext)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service = classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return wantNext.Add(time.Minute) })
	result, err = service.Submit(ctx, fixture.studentUserID, secondSessionID, fixture.privateCanary)
	if err != nil {
		t.Fatal(err)
	}
	if result.MasteryState != mastery.Mastered {
		t.Fatalf("independent review state=%s", result.MasteryState)
	}
	if err := pool.QueryRow(ctx, `SELECT result,attempts FROM review_queue WHERE id=$1`, nextQueueID).Scan(&reviewResult, &attempts); err != nil {
		t.Fatal(err)
	}
	if reviewResult != "INDEPENDENT_SUCCESS" || attempts != 1 {
		t.Fatalf("independent queue=%s/%d", reviewResult, attempts)
	}
	if err := pool.QueryRow(ctx, `SELECT independent_successes,assisted_successes,review_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.studentID, fixture.knowledgePointID).Scan(&independent, &assisted, &review); err != nil {
		t.Fatal(err)
	}
	if independent != 4 || assisted != 1 || review != 1 {
		t.Fatalf("independent evidence=%d assisted=%d review=%d", independent, assisted, review)
	}
}

func TestB2ReviewQueueFailureRollbackAndConcurrentCompletion(t *testing.T) {
	t.Run("wrong_then_abandon_resolves_failed", func(t *testing.T) {
		ctx := context.Background()
		pool := isolatedPool(t, ctx, testDatabaseURL(t))
		if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
			t.Fatal(err)
		}
		fixture := prepareB2ReviewFixture(t, ctx, pool, 0)
		current := fixture.now
		service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return current })
		if _, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, "wrong"); err != nil {
			t.Fatal(err)
		}
		var state string
		var failures int
		var failedAt *time.Time
		if err := pool.QueryRow(ctx, `SELECT skill.state,skill.consecutive_review_failures,session.review_attempt_failed_at FROM student_skill_states skill JOIN learning_sessions session ON session.student_id=skill.student_id WHERE session.id=$1 AND skill.knowledge_point_id=$2`, fixture.sessionID, fixture.knowledgePointID).Scan(&state, &failures, &failedAt); err != nil {
			t.Fatal(err)
		}
		if state != "REGRESSED" || failures != 1 || failedAt == nil || !failedAt.Equal(fixture.now) {
			t.Fatalf("wrong review state=%s failures=%d failed_at=%v", state, failures, failedAt)
		}
		current = current.Add(time.Minute)
		if _, err := service.AbandonSession(ctx, fixture.studentUserID, fixture.sessionID); err != nil {
			t.Fatal(err)
		}
		var result, queueStatus, blockStatus string
		var resolvedAt, dueAt time.Time
		if err := pool.QueryRow(ctx, `SELECT status,result,resolved_at FROM review_queue WHERE id=$1`, fixture.queueID).Scan(&queueStatus, &result, &resolvedAt); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, fixture.blockID).Scan(&blockStatus); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT due_at FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MASTERY' AND status='PENDING'`, fixture.studentID, fixture.knowledgePointID).Scan(&dueAt); err != nil {
			t.Fatal(err)
		}
		if queueStatus != "COMPLETED" || result != "FAILED" || !resolvedAt.Equal(current) || blockStatus != "COMPLETED" || !dueAt.Equal(fixture.now.Add(24*time.Hour)) {
			t.Fatalf("abandon queue=%s/%s resolved=%s block=%s next=%s", queueStatus, result, resolvedAt, blockStatus, dueAt)
		}
	})

	t.Run("completion_rolls_back_atomically", func(t *testing.T) {
		ctx := context.Background()
		pool := isolatedPool(t, ctx, testDatabaseURL(t))
		if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
			t.Fatal(err)
		}
		fixture := prepareB2ReviewFixture(t, ctx, pool, 2)
		if _, err := pool.Exec(ctx, `
CREATE FUNCTION reject_b2_skill_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'forced skill rollback'; END $$;
CREATE TRIGGER reject_b2_skill_write BEFORE UPDATE ON student_skill_states
FOR EACH ROW EXECUTE FUNCTION reject_b2_skill_write()`); err != nil {
			t.Fatal(err)
		}
		service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return fixture.now })
		if _, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, fixture.privateCanary); err == nil {
			t.Fatal("forced skill failure unexpectedly committed")
		}
		var pending, future int
		var sessionStatus string
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE id=$1 AND status='PENDING' AND result IS NULL AND resolved_at IS NULL`, fixture.queueID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MASTERY' AND id<>$3`, fixture.studentID, fixture.knowledgePointID, fixture.queueID).Scan(&future); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT status FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&sessionStatus); err != nil {
			t.Fatal(err)
		}
		if pending != 1 || future != 0 || sessionStatus != "ACTIVE" {
			t.Fatalf("partial commit pending=%d future=%d session=%s", pending, future, sessionStatus)
		}
	})

	t.Run("concurrent_completion_creates_one_next_item", func(t *testing.T) {
		ctx := context.Background()
		pool := isolatedPool(t, ctx, testDatabaseURL(t))
		if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
			t.Fatal(err)
		}
		fixture := prepareB2ReviewFixture(t, ctx, pool, 2)
		service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return fixture.now })
		start := make(chan struct{})
		errs := make(chan error, 2)
		var ready sync.WaitGroup
		ready.Add(2)
		for range 2 {
			go func() {
				ready.Done()
				<-start
				_, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, fixture.privateCanary)
				errs <- err
			}()
		}
		ready.Wait()
		close(start)
		successes := 0
		for range 2 {
			if err := <-errs; err == nil {
				successes++
			}
		}
		var pending int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM review_queue WHERE student_id=$1 AND knowledge_point_id=$2 AND source='MASTERY' AND status='PENDING'`, fixture.studentID, fixture.knowledgePointID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if successes != 1 || pending != 1 {
			t.Fatalf("concurrent successes=%d future pending=%d", successes, pending)
		}
	})
}

func TestB2ReviewSessionStartRequiresBoundPendingItem(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='COMPLETED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT kp.subject_id,kp.id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$1`, fixture.releasedQuestionID).Scan(&subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	planID, blockID, queueID := uuid.New(), uuid.New(), uuid.New()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES($1,$2,current_date,20,'PROPOSED')`, planID, fixture.studentID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason) VALUES($1,$2,1,$3,$4,20,'REVIEW','forged_review')`, blockID, planID, subjectID, knowledgePointID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return now })
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, nil),
	})
	start := func() int {
		return performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.studentToken, map[string]any{"plan_block_id": blockID}).Code
	}
	if status := start(); status != http.StatusNotFound {
		t.Fatalf("unbound REVIEW start status=%d", status)
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at) VALUES($1,$2,$3,'MASTERY',$4::timestamptz+interval '1 hour')`, queueID, fixture.studentID, knowledgePointID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE learning_plan_blocks SET review_queue_id=$1 WHERE id=$2`, queueID, blockID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if status := start(); status != http.StatusNotFound {
		t.Fatalf("not-due REVIEW start status=%d", status)
	}
	if _, err := pool.Exec(ctx, `UPDATE review_queue SET due_at=$2::timestamptz-interval '1 second' WHERE id=$1`, queueID, now); err != nil {
		t.Fatal(err)
	}
	if status := start(); status != http.StatusOK {
		t.Fatalf("bound due REVIEW start status=%d", status)
	}
	var boundQueueID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT review_queue_id FROM learning_sessions WHERE student_id=$1 AND status='ACTIVE'`, fixture.studentID).Scan(&boundQueueID); err != nil {
		t.Fatal(err)
	}
	if boundQueueID != queueID {
		t.Fatalf("started queue=%s want=%s", boundQueueID, queueID)
	}
}

func TestB2ReviewQueueMigrationDeduplicatesAndBindsDeterministically(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, "000023_review_queue_lifecycle.sql")); err != nil {
		t.Fatal(err)
	}
	userID, studentID := uuid.New(), uuid.New()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT','复习迁移学生')`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,7)`, studentID, userID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT subject_id,id FROM knowledge_points WHERE status='RELEASED' ORDER BY id LIMIT 1`).Scan(&subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	keptID, duplicateA, duplicateB := uuid.New(), uuid.New(), uuid.New()
	planID, blockID := uuid.New(), uuid.New()
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority,created_at)
VALUES($1,$2,$3,'MASTERY',current_date-interval '3 hours',20,current_date-interval '3 days'),
      ($4,$2,$3,'MASTERY',current_date-interval '2 hours',90,current_date-interval '2 days'),
      ($5,$2,$3,'MASTERY',current_date-interval '1 hour',100,current_date-interval '1 day')`, keptID, studentID, knowledgePointID, duplicateA, duplicateB); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status)
VALUES($1,$2,current_date,20,'PROPOSED')`, planID, studentID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason)
VALUES($1,$2,1,$3,$4,20,'REVIEW','spaced_review_due')`, blockID, planID, subjectID, knowledgePointID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var keptStatus, duplicateStatusA, duplicateStatusB, resultA, resultB string
	if err := pool.QueryRow(ctx, `SELECT status FROM review_queue WHERE id=$1`, keptID).Scan(&keptStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,result FROM review_queue WHERE id=$1`, duplicateA).Scan(&duplicateStatusA, &resultA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status,result FROM review_queue WHERE id=$1`, duplicateB).Scan(&duplicateStatusB, &resultB); err != nil {
		t.Fatal(err)
	}
	var boundQueueID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT review_queue_id FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&boundQueueID); err != nil {
		t.Fatal(err)
	}
	if keptStatus != "PENDING" || duplicateStatusA != "CANCELLED" || duplicateStatusB != "CANCELLED" || resultA != "CANCELLED" || resultB != "CANCELLED" || boundQueueID != keptID {
		t.Fatalf("migration kept=%s duplicate=%s/%s results=%s/%s bound=%s", keptStatus, duplicateStatusA, duplicateStatusB, resultA, resultB, boundQueueID)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at) VALUES($1,$2,$3,'MASTERY',now())`, uuid.New(), studentID, knowledgePointID); err == nil {
		t.Fatal("pending review uniqueness was not enforced")
	}
	var indexExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='review_queue_one_pending_source')`).Scan(&indexExists); err != nil || !indexExists {
		t.Fatalf("partial unique index exists=%v err=%v", indexExists, err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("review migration is not idempotent: %v", err)
	}
}
