package integration

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestFullV1CurriculumCatalogIsReleasedAndTraceable(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}

	expected := map[string]int{
		"CHEMISTRY/JUNIOR_SECONDARY": 21,
		"CHINESE/JUNIOR_SECONDARY":   15,
		"CHINESE/PRIMARY":            12,
		"ENGLISH/JUNIOR_SECONDARY":   13,
		"ENGLISH/PRIMARY":            7,
		"MATH/JUNIOR_SECONDARY":      29,
		"MATH/PRIMARY":               22,
		"PHYSICS/JUNIOR_SECONDARY":   23,
	}
	rows, err := pool.Query(ctx, `
SELECT s.code || '/' || kp.grade_band_code,count(*)
FROM knowledge_points kp JOIN subjects s ON s.id=kp.subject_id
WHERE kp.status='RELEASED'
GROUP BY s.code,kp.grade_band_code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			t.Fatal(err)
		}
		actual[key] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("catalog groups=%v, want %v", actual, expected)
	}
	for key, want := range expected {
		if actual[key] != want {
			t.Fatalf("catalog %s=%d, want %d; all=%v", key, actual[key], want, actual)
		}
	}

	var released, skeletonRefs, placeholder, invalidHierarchy, withoutSource int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_points WHERE status='RELEASED'`).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT source_ref) FROM knowledge_point_sources WHERE basis_kind='V1_SKELETON'`).Scan(&skeletonRefs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_points WHERE name='初三模块预留'`).Scan(&placeholder); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM knowledge_points kp
JOIN domains d ON d.id=kp.domain_id
JOIN units u ON u.id=kp.unit_id
WHERE kp.status='RELEASED'
  AND (d.subject_id<>kp.subject_id OR u.domain_id<>kp.domain_id OR u.grade_band_code<>kp.grade_band_code)`).Scan(&invalidHierarchy); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM knowledge_points kp
WHERE kp.status='RELEASED' AND NOT EXISTS (
    SELECT 1 FROM knowledge_point_sources kps
    JOIN curriculum_sources cs ON cs.id=kps.curriculum_source_id AND cs.status='RELEASED'
    WHERE kps.knowledge_point_id=kp.id
)`).Scan(&withoutSource); err != nil {
		t.Fatal(err)
	}
	if released != 142 || skeletonRefs != 132 || placeholder != 0 || invalidHierarchy != 0 || withoutSource != 0 {
		t.Fatalf("catalog released=%d skeleton=%d placeholder=%d invalid_hierarchy=%d without_source=%d", released, skeletonRefs, placeholder, invalidHierarchy, withoutSource)
	}
}

func TestFullV1CurriculumPreservesDemoKnowledgePointIdentifiers(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}

	var name, domain, unit string
	if err := pool.QueryRow(ctx, `
SELECT kp.name,d.name,u.name
FROM knowledge_points kp JOIN domains d ON d.id=kp.domain_id JOIN units u ON u.id=kp.unit_id
WHERE kp.id='30000000-0000-4000-8000-000000000002' AND kp.code='MATH-FRACTION-COMMON-DENOMINATOR'`).Scan(&name, &domain, &unit); err != nil {
		t.Fatal(err)
	}
	if name != "分数通分" || domain != "代数式与整式" || unit != "代数运算进阶" {
		t.Fatalf("preserved knowledge point name=%q domain=%q unit=%q", name, domain, unit)
	}
}

func TestCurriculumDomainsAreUsefulAcrossEverySupportedSubjectAndGradeBand(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}

	expected := map[string]int{
		"CHEMISTRY/JUNIOR_SECONDARY": 7,
		"CHINESE/JUNIOR_SECONDARY":   5,
		"CHINESE/PRIMARY":            4,
		"ENGLISH/JUNIOR_SECONDARY":   5,
		"ENGLISH/PRIMARY":            3,
		"MATH/JUNIOR_SECONDARY":      6,
		"MATH/PRIMARY":               6,
		"PHYSICS/JUNIOR_SECONDARY":   8,
	}
	rows, err := pool.Query(ctx, `
SELECT subject.code || '/' || knowledge_point.grade_band_code,count(DISTINCT domain.id)
FROM knowledge_points knowledge_point
JOIN subjects subject ON subject.id=knowledge_point.subject_id
JOIN domains domain ON domain.id=knowledge_point.domain_id
JOIN units unit ON unit.id=knowledge_point.unit_id
WHERE knowledge_point.status='RELEASED'
  AND unit.domain_id=domain.id
  AND unit.grade_band_code=knowledge_point.grade_band_code
GROUP BY subject.code,knowledge_point.grade_band_code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			t.Fatal(err)
		}
		actual[key] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("domain groups=%v, want %v", actual, expected)
	}
	for key, want := range expected {
		if actual[key] != want {
			t.Fatalf("domain count %s=%d, want %d; all=%v", key, actual[key], want, actual)
		}
	}

	var juniorMathDomains []string
	rows, err = pool.Query(ctx, `
SELECT DISTINCT domain.name,domain.sort_order
FROM knowledge_points knowledge_point
JOIN subjects subject ON subject.id=knowledge_point.subject_id
JOIN domains domain ON domain.id=knowledge_point.domain_id
WHERE subject.code='MATH' AND knowledge_point.grade_band_code='JUNIOR_SECONDARY'
ORDER BY domain.sort_order`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var sortOrder int
		if err := rows.Scan(&name, &sortOrder); err != nil {
			t.Fatal(err)
		}
		juniorMathDomains = append(juniorMathDomains, name)
	}
	wantJuniorMath := []string{"有理数与数轴", "代数式与整式", "方程与不等式", "坐标与几何", "函数", "数据分析"}
	if len(juniorMathDomains) != len(wantJuniorMath) {
		t.Fatalf("junior math domains=%v, want %v", juniorMathDomains, wantJuniorMath)
	}
	for index := range wantJuniorMath {
		if juniorMathDomains[index] != wantJuniorMath[index] {
			t.Fatalf("junior math domains=%v, want %v", juniorMathDomains, wantJuniorMath)
		}
	}
}

func TestCurriculumRefinementPreservesOwnerDefinedEmptyTaxonomy(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	beforeRefinement := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "000019_refine_curriculum_taxonomy.sql" {
			continue
		}
		data, err := fs.ReadFile(migrations.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		beforeRefinement[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if err := database.Migrate(ctx, pool, beforeRefinement); err != nil {
		t.Fatal(err)
	}

	domainID, unitID := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO domains(id,subject_id,code,name,description,sort_order)
SELECT $1,id,'OWNER_DRAFT_DOMAIN','Owner draft domain','Pending sourced curriculum',999
FROM subjects WHERE code='MATH'`, domainID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO units(id,domain_id,code,name,grade_band_code,sort_order)
VALUES($2,$1,'OWNER_DRAFT_UNIT','Owner draft unit','JUNIOR_SECONDARY',999)`, domainID, unitID); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var preserved int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM domains domain
JOIN units unit ON unit.domain_id=domain.id
WHERE domain.id=$1 AND unit.id=$2`, domainID, unitID).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatal("curriculum refinement removed an Owner-defined empty domain or unit")
	}
}

func TestPlannerExcludesReleasedKnowledgePointsWithoutReleasedQuestions(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}

	plan, err := planner.NewService(pool).Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocks) == 0 {
		t.Fatal("planner returned no executable blocks")
	}
	var unexecutable int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM learning_plan_blocks block
WHERE block.plan_id=$1 AND NOT EXISTS (
    SELECT 1 FROM questions question
    WHERE question.knowledge_point_id=block.knowledge_point_id AND question.status='RELEASED'
)`, plan.ID).Scan(&unexecutable); err != nil {
		t.Fatal(err)
	}
	if unexecutable != 0 {
		t.Fatalf("planner created %d blocks without released questions", unexecutable)
	}
}

func TestPlannerSwapsUnusedTodayCardsPerOwnerFreeze(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	prepareGradeNineMathPlan(t, ctx, pool, fixture)

	plannerService := planner.NewService(pool)
	plan, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("6 unpracticed foundation beats overdue ticket review", func(t *testing.T) {
		assertMathCodeOnPlan(t, ctx, pool, plan.ID, "MATH-JUN-REMOVE-PARENTHESES")
	})

	t.Run("1 available without classroom swaps", func(t *testing.T) {
		forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
		refreshed, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		if refreshed.ID != plan.ID {
			t.Fatalf("unused today plan was replaced: %s -> %s", plan.ID, refreshed.ID)
		}
		assertMathCodeOnPlan(t, ctx, pool, refreshed.ID, "MATH-JUN-REMOVE-PARENTHESES")
		assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, refreshed.ID)
	})

	t.Run("2 available with paused classroom holds", func(t *testing.T) {
		blockID, subjectID := forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
		insertMathClassroom(t, ctx, pool, fixture.studentID, blockID, subjectID, "PAUSED")
		held, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		if held.ID != plan.ID {
			t.Fatalf("paused today plan was replaced: %s -> %s", plan.ID, held.ID)
		}
		assertMathCodeOnPlan(t, ctx, pool, held.ID, "MATH-LINEAR-EQUATION")
		assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, held.ID)
	})

	t.Run("3 available with active classroom holds", func(t *testing.T) {
		clearMathClassrooms(t, ctx, pool, plan.ID)
		blockID, subjectID := forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
		insertMathClassroom(t, ctx, pool, fixture.studentID, blockID, subjectID, "ACTIVE")
		held, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		if held.ID != plan.ID {
			t.Fatalf("active today plan was replaced: %s -> %s", plan.ID, held.ID)
		}
		assertMathCodeOnPlan(t, ctx, pool, held.ID, "MATH-LINEAR-EQUATION")
		assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, held.ID)
	})

	t.Run("4 completed card holds", func(t *testing.T) {
		clearMathClassrooms(t, ctx, pool, plan.ID)
		forceMathTicketCard(t, ctx, pool, plan.ID, "COMPLETED")
		held, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		if held.ID != plan.ID {
			t.Fatalf("completed today plan was replaced: %s -> %s", plan.ID, held.ID)
		}
		assertMathCodeOnPlan(t, ctx, pool, held.ID, "MATH-LINEAR-EQUATION")
		assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, held.ID)
	})

	t.Run("5 abandoned classroom on available card swaps", func(t *testing.T) {
		clearMathClassrooms(t, ctx, pool, plan.ID)
		blockID, subjectID := forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
		insertMathClassroom(t, ctx, pool, fixture.studentID, blockID, subjectID, "ABANDONED")
		refreshed, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
		if err != nil {
			t.Fatal(err)
		}
		if refreshed.ID != plan.ID {
			t.Fatalf("abandoned today plan was replaced: %s -> %s", plan.ID, refreshed.ID)
		}
		assertMathCodeOnPlan(t, ctx, pool, refreshed.ID, "MATH-JUN-REMOVE-PARENTHESES")
		assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, refreshed.ID)
	})
}

func TestTicketMisconceptionYieldsToUnpracticedParentheses(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	prepareGradeNineMathPlan(t, ctx, pool, fixture)
	plannerService := planner.NewService(pool)
	plan, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO student_misconceptions(student_id,knowledge_point_id,misconception_id,first_seen_at,last_seen_at,status)
SELECT $1,kp.id,link.misconception_id,now(),now(),'ACTIVE'
FROM knowledge_points kp
JOIN knowledge_misconception_links link ON link.knowledge_point_id=kp.id
WHERE kp.code='MATH-LINEAR-EQUATION'
ORDER BY link.misconception_id
LIMIT 1
ON CONFLICT (student_id,knowledge_point_id,misconception_id) DO UPDATE SET status='ACTIVE'`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
	refreshed, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	assertMathCodeOnPlan(t, ctx, pool, refreshed.ID, "MATH-JUN-REMOVE-PARENTHESES")
	assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, refreshed.ID)

	if _, err := pool.Exec(ctx, `
INSERT INTO student_skill_states(student_id,knowledge_point_id,state,score_internal)
SELECT $1,id,'LEARNING',20 FROM knowledge_points WHERE code='MATH-JUN-REMOVE-PARENTHESES'
ON CONFLICT (student_id,knowledge_point_id) DO UPDATE SET state='LEARNING',score_internal=20`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	forceMathTicketCard(t, ctx, pool, plan.ID, "AVAILABLE")
	returned, err := plannerService.Ensure(ctx, fixture.studentID, time.Now(), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	assertMathCodeOnPlan(t, ctx, pool, returned.ID, "MATH-LINEAR-EQUATION")
	assertPlanSubjectMatchesKnowledgePoint(t, ctx, pool, returned.ID)
}

func prepareGradeNineMathPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE students SET grade_level=9 WHERE id=$1`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO parent_preferences(parent_user_id,student_id,daily_minutes,review_only,reduce_intensity,priority_subject_codes,enabled_subject_codes)
VALUES ($1,$2,30,false,true,'{MATH}','{MATH,ENGLISH,CHEMISTRY,PHYSICS,CHINESE}')`, fixture.parentUserID, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO student_skill_states(student_id,knowledge_point_id,state,score_internal)
SELECT $1,id,'REGRESSED',40 FROM knowledge_points WHERE code='MATH-LINEAR-EQUATION'`, fixture.studentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority,status)
SELECT $1,$2,id,'MISCONCEPTION',now()-interval '1 day',80,'PENDING'
FROM knowledge_points WHERE code='MATH-LINEAR-EQUATION'`, uuid.New(), fixture.studentID); err != nil {
		t.Fatal(err)
	}
}

func forceMathTicketCard(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID, status string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var blockID, subjectID uuid.UUID
	if err := pool.QueryRow(ctx, `
UPDATE learning_plan_blocks block
SET knowledge_point_id=(SELECT id FROM knowledge_points WHERE code='MATH-LINEAR-EQUATION'),
    reason='spaced_review_due',
    status=$2
FROM subjects s
WHERE block.plan_id=$1 AND block.subject_id=s.id AND s.code='MATH'
RETURNING block.id, block.subject_id`, planID, status).Scan(&blockID, &subjectID); err != nil {
		t.Fatalf("force ticket card: %v", err)
	}
	return blockID, subjectID
}

func insertMathClassroom(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID, blockID, subjectID uuid.UUID, status string) {
	t.Helper()
	if status == "ACTIVE" || status == "PAUSED" {
		if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE student_id=$1 AND status IN ('ACTIVE','PAUSED')`, studentID); err != nil {
			t.Fatalf("close open classrooms: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(id,student_id,plan_block_id,subject_id,current_question_id,status,target_minutes,current_state,started_at,last_activity_at)
SELECT $1,$2,$3,$4,id,$5,20,'PROBE',now(),now()
FROM questions WHERE id='40000000-0000-4000-8000-000000000001'`, uuid.New(), studentID, blockID, subjectID, status); err != nil {
		t.Fatalf("insert %s classroom: %v", status, err)
	}
}

func clearMathClassrooms(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions
SET status=CASE WHEN status IN ('ACTIVE','PAUSED') THEN 'ABANDONED' ELSE status END,
    ended_at=COALESCE(ended_at, now()),
    plan_block_id=NULL
WHERE plan_block_id IN (
	SELECT block.id FROM learning_plan_blocks block
	JOIN subjects s ON s.id=block.subject_id
	WHERE block.plan_id=$1 AND s.code='MATH'
)`, planID); err != nil {
		t.Fatalf("clear math classrooms: %v", err)
	}
}

func assertPlanSubjectMatchesKnowledgePoint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID) {
	t.Helper()
	var mismatches int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM learning_plan_blocks block
JOIN knowledge_points kp ON kp.id=block.knowledge_point_id
WHERE block.plan_id=$1 AND block.subject_id<>kp.subject_id`, planID).Scan(&mismatches); err != nil {
		t.Fatal(err)
	}
	if mismatches != 0 {
		t.Fatalf("plan %s has %d blocks whose subject_id disagrees with the knowledge point", planID, mismatches)
	}
}

func assertMathCodeOnPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID, want string) {
	t.Helper()
	got := knowledgePointCodeOnPlan(t, ctx, pool, planID, "MATH")
	if got != want {
		t.Fatalf("math card=%s want %s", got, want)
	}
}

func knowledgePointCodeOnPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, planID uuid.UUID, subjectCode string) string {
	t.Helper()
	var code string
	if err := pool.QueryRow(ctx, `
SELECT kp.code
FROM learning_plan_blocks block
JOIN subjects s ON s.id=block.subject_id
JOIN knowledge_points kp ON kp.id=block.knowledge_point_id
WHERE block.plan_id=$1 AND s.code=$2`, planID, subjectCode).Scan(&code); err != nil {
		t.Fatalf("load %s block: %v", subjectCode, err)
	}
	return code
}
