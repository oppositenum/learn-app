package integration

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestSessionLifecycleMigrationUpgradesPopulatedSchema(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, "000021_session_lifecycle.sql")); err != nil {
		t.Fatal(err)
	}
	userID, studentID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT','迁移测试学生')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,7)`, studentID, userID); err != nil {
		t.Fatal(err)
	}
	var subjectID, questionID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT kp.subject_id,q.id,kp.id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.status='RELEASED' ORDER BY q.id LIMIT 1`).Scan(&subjectID, &questionID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 2, 3, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	planID, blockID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES($1,$2,$3,20,'ACTIVE')`, planID, studentID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason) VALUES($1,$2,1,$3,$4,20,'CURRENT_GRADE','migration_fixture')`, blockID, planID, subjectID, knowledgePointID); err != nil {
		t.Fatal(err)
	}
	completedID, oldActiveID, keptPausedID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,started_at,ended_at,status,target_minutes,actual_seconds,current_state,socratic_fail_count)
VALUES
($1,$2,$3,$4,$5,$5::timestamptz+interval '123 seconds','COMPLETED',20,123,'COMPLETE',0),
($6,$2,$3,$4,$7,NULL,'ACTIVE',20,0,'PROBE',2),
($8,$2,$3,$4,$9,NULL,'PAUSED',20,45,'EXPLAIN',3)`, completedID, studentID, subjectID, questionID, now.AddDate(0, 0, -1), oldActiveID, now.Add(-2*time.Hour), keptPausedID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,action,message) VALUES($1,$2,1,'TUTOR','HINT','历史提示')`, uuid.New(), oldActiveID); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var completedAccumulated int
	if err := pool.QueryRow(ctx, `SELECT accumulated_seconds FROM learning_sessions WHERE id=$1`, completedID).Scan(&completedAccumulated); err != nil {
		t.Fatal(err)
	}
	var oldStatus string
	var oldAccumulated, oldAssistance int
	if err := pool.QueryRow(ctx, `SELECT status,accumulated_seconds,assistance_level FROM learning_sessions WHERE id=$1`, oldActiveID).Scan(&oldStatus, &oldAccumulated, &oldAssistance); err != nil {
		t.Fatal(err)
	}
	var keptStatus, blockStatus string
	var keptAccumulated, keptAssistance int
	var linkedBlockID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status,accumulated_seconds,assistance_level,plan_block_id FROM learning_sessions WHERE id=$1`, keptPausedID).Scan(&keptStatus, &keptAccumulated, &keptAssistance, &linkedBlockID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, blockID).Scan(&blockStatus); err != nil {
		t.Fatal(err)
	}
	if completedAccumulated != 123 || oldStatus != "ABANDONED" || oldAccumulated != 0 || oldAssistance < 1 || keptStatus != "PAUSED" || keptAccumulated != 45 || keptAssistance != 4 || linkedBlockID != blockID || blockStatus != "ACTIVE" {
		t.Fatalf("migration completed=%d old=%s/%d/%d kept=%s/%d/%d linked=%s block=%s", completedAccumulated, oldStatus, oldAccumulated, oldAssistance, keptStatus, keptAccumulated, keptAssistance, linkedBlockID, blockStatus)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,status,target_minutes,current_state) VALUES($1,$2,$3,$4,'ACTIVE',20,'ASK')`, uuid.New(), studentID, subjectID, questionID); err == nil {
		t.Fatal("open-session uniqueness was not enforced")
	}
}

func TestSessionLifecycleMigrationDoesNotGuessCrossSubjectPlanOrigin(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, "000021_session_lifecycle.sql")); err != nil {
		t.Fatal(err)
	}
	type releasedQuestion struct {
		subjectID, knowledgePointID, questionID uuid.UUID
	}
	loadQuestion := func(code string) releasedQuestion {
		t.Helper()
		var question releasedQuestion
		if err := pool.QueryRow(ctx, `
SELECT kp.subject_id,kp.id,q.id
FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
WHERE q.status='RELEASED' AND kp.code=$1
ORDER BY q.id LIMIT 1`, code).Scan(&question.subjectID, &question.knowledgePointID, &question.questionID); err != nil {
			t.Fatal(err)
		}
		return question
	}
	original := loadQuestion("PHYSICS-SPEED")
	prerequisite := loadQuestion("MATH-FRACTION-COMMON-DENOMINATOR")
	now := time.Date(2030, 2, 3, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	ambiguousUser, exactUser, dynamicUser := uuid.New(), uuid.New(), uuid.New()
	ambiguousStudent, exactStudent, dynamicStudent := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,role_code,display_name) VALUES($1,'STUDENT','歧义回填学生'),($2,'STUDENT','精确微回填学生'),($3,'STUDENT','精确动态回填学生')`, ambiguousUser, exactUser, dynamicUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO students(id,user_id,grade_level) VALUES($1,$2,7),($3,$4,7),($5,$6,7)`, ambiguousStudent, ambiguousUser, exactStudent, exactUser, dynamicStudent, dynamicUser); err != nil {
		t.Fatal(err)
	}
	ambiguousPlan, exactPlan, dynamicPlan := uuid.New(), uuid.New(), uuid.New()
	regularBlock, prerequisiteBlock, ambiguousMicroBlock, exactMicroBlock, exactDynamicBlock := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES
($1,$2,$7,20,'ACTIVE'),($3,$4,$7,20,'ACTIVE'),($5,$6,$7,20,'ACTIVE')`,
		ambiguousPlan, ambiguousStudent, exactPlan, exactStudent, dynamicPlan, dynamicStudent, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,original_task_id) VALUES
($1,$2,1,$8,$9,8,'CURRENT_GRADE','original',NULL),
($3,$2,2,$10,$11,6,'REVIEW','prerequisite',NULL),
($4,$2,3,$10,$11,6,'MICRO_BACKTRACK','ambiguous_micro',$9),
($5,$6,1,$10,$11,20,'MICRO_BACKTRACK','exact_micro',$9),
($7,$12,1,$8,$9,20,'CURRENT_GRADE','exact_dynamic',NULL)`,
		regularBlock, ambiguousPlan, prerequisiteBlock, ambiguousMicroBlock, exactMicroBlock, exactPlan, exactDynamicBlock,
		original.subjectID, original.knowledgePointID, prerequisite.subjectID, prerequisite.knowledgePointID, dynamicPlan); err != nil {
		t.Fatal(err)
	}
	dynamicBacktrack, returnedMicro, exactReturnedMicro, exactDynamicBacktrack := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,subject_id,current_question_id,started_at,ended_at,status,target_minutes,actual_seconds,current_state,original_task_id,active_task_id,evidence_form) VALUES
($1,$2,$9,$10,$8,$8,'ABANDONED',20,12,'BACKTRACK',$11,$12,'TEXTBOOK'),
($3,$2,$13,$14,$8,$8,'COMPLETED',20,18,'RETURN',$11,$11,'VARIANT'),
($4,$5,$13,$14,$8,$8,'COMPLETED',20,22,'RETURN',$11,$11,'VARIANT'),
($6,$7,$9,$10,$8,$8,'ABANDONED',20,10,'BACKTRACK',$11,$12,'TEXTBOOK')`,
		dynamicBacktrack, ambiguousStudent, returnedMicro, exactReturnedMicro, exactStudent, exactDynamicBacktrack, dynamicStudent,
		now, prerequisite.subjectID, prerequisite.questionID, original.knowledgePointID, prerequisite.knowledgePointID, original.subjectID, original.questionID); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []uuid.UUID{dynamicBacktrack, returnedMicro} {
		var linkedBlockID *uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT plan_block_id FROM learning_sessions WHERE id=$1`, sessionID).Scan(&linkedBlockID); err != nil {
			t.Fatal(err)
		}
		if linkedBlockID != nil {
			t.Fatalf("ambiguous cross-subject session %s linked to %s", sessionID, *linkedBlockID)
		}
	}
	var linkedBlockID uuid.UUID
	var blockStatus string
	if err := pool.QueryRow(ctx, `SELECT plan_block_id FROM learning_sessions WHERE id=$1`, exactReturnedMicro).Scan(&linkedBlockID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM learning_plan_blocks WHERE id=$1`, exactMicroBlock).Scan(&blockStatus); err != nil {
		t.Fatal(err)
	}
	if linkedBlockID != exactMicroBlock || blockStatus != "COMPLETED" {
		t.Fatalf("exact micro-backtrack link=%s status=%s", linkedBlockID, blockStatus)
	}
	if err := pool.QueryRow(ctx, `SELECT plan_block_id FROM learning_sessions WHERE id=$1`, exactDynamicBacktrack).Scan(&linkedBlockID); err != nil {
		t.Fatal(err)
	}
	if linkedBlockID != exactDynamicBlock {
		t.Fatalf("exact dynamic backtrack link=%s", linkedBlockID)
	}
}

func migrationsBefore(t *testing.T, cutoff string) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	result := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= cutoff {
			continue
		}
		contents, err := fs.ReadFile(migrations.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = &fstest.MapFile{Data: contents}
	}
	return result
}
