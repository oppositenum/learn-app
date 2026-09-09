package integration

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/mastery"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

const provenanceMigration = "000027_answer_evaluation_provenance.sql"

type provenanceTeachingAgent struct {
	analysis      ai.AnalyzeAnswerResult
	analyzeCalls  int
	generateCalls int
}

func (agent *provenanceTeachingAgent) AnalyzeAnswer(context.Context, ai.AnalyzeAnswerRequest) (ai.AnalyzeAnswerResult, error) {
	agent.analyzeCalls++
	return agent.analysis, nil
}

func (agent *provenanceTeachingAgent) GenerateTurn(_ context.Context, request ai.GenerateTurnRequest) (ai.TutorTurn, error) {
	agent.generateCalls++
	return ai.TutorTurn{Action: request.TutorDecision.NextState, Message: "继续检查当前思路。"}, nil
}

func (agent *provenanceTeachingAgent) GenerateAnalogy(_ context.Context, request ai.AnalogyRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(context.Background(), ai.GenerateTurnRequest(request))
}

func (agent *provenanceTeachingAgent) GenerateParallelExample(_ context.Context, request ai.ExampleRequest) (ai.TutorTurn, error) {
	return agent.GenerateTurn(context.Background(), ai.GenerateTurnRequest(request))
}

func (agent *provenanceTeachingAgent) GenerateExplanation(_ context.Context, request ai.ExplainRequest) (ai.Explanation, error) {
	return agent.GenerateTurn(context.Background(), ai.GenerateTurnRequest(request))
}

func TestProvenanceMigrationRegistersPredicateCandidatesIdempotently(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, provenanceMigration)); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	legacySchema := schemaSignature(t, ctx, pool, []string{})

	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions
SET status='COMPLETED',current_state='COMPLETE',ended_at=now(),assistance_level=0,evidence_form='VARIANT'
WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	candidateAnswerID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO student_answers(id,session_id,question_id,answer_text)
VALUES($1,$2,$3,'semantically accepted legacy response')`,
		candidateAnswerID, fixture.sessionID, fixture.releasedQuestionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO answer_analyses(
    id,student_answer_id,answer_correct,reasoning_quality,confidence,error_type,
    misconceptions_private_json,emotion_signal,engagement,recommended_action
)
VALUES($1,$2,true,'STRONG',0.95,'NONE','[]','NEUTRAL','NORMAL','VARIANT')`,
		uuid.New(), candidateAnswerID); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	assertLegacySchemaUnchanged(t, ctx, pool, legacySchema)
	predicateCount, registeredCount := provenanceCandidateCounts(t, ctx, pool)
	if predicateCount == 0 {
		t.Fatal("fixture did not exercise the legacy provenance predicate")
	}
	if registeredCount != predicateCount {
		t.Fatalf("registered provenance=%d predicate candidates=%d", registeredCount, predicateCount)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, provenanceMigration); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("repeat provenance registration: %v", err)
	}
	repeatedPredicateCount, repeatedRegisteredCount := provenanceCandidateCounts(t, ctx, pool)
	if repeatedPredicateCount != predicateCount || repeatedRegisteredCount != registeredCount {
		t.Fatalf("repeat registration predicate=%d/%d registered=%d/%d", repeatedPredicateCount, predicateCount, repeatedRegisteredCount, registeredCount)
	}
	t.Logf("predicate_candidates=%d registered=%d registered_after_repeat=%d", predicateCount, registeredCount, repeatedRegisteredCount)
	if _, err := pool.Exec(ctx, `UPDATE mastery_evidence_provenance SET provenance_risk='NONE'`); err == nil {
		t.Fatal("append-only mastery evidence provenance accepted an update")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM mastery_evidence_provenance`); err == nil {
		t.Fatal("append-only mastery evidence provenance accepted a delete")
	}
	_, immutableCount := provenanceCandidateCounts(t, ctx, pool)
	if immutableCount != registeredCount {
		t.Fatalf("append-only checks changed registered rows=%d want=%d", immutableCount, registeredCount)
	}

	legacyAnswerID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO student_answers(id,session_id,question_id,answer_text) VALUES($1,$2,$3,'legacy write')`,
		legacyAnswerID, fixture.sessionID, fixture.releasedQuestionID); err != nil {
		t.Fatalf("legacy binary-shaped answer write after additive migration: %v", err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO answer_analyses(
    id,student_answer_id,answer_correct,reasoning_quality,confidence,error_type,
    misconceptions_private_json,emotion_signal,engagement,recommended_action
)
VALUES($1,$2,false,'WEAK',1,'NONE','[]','NEUTRAL','NORMAL','PROBE')`,
		uuid.New(), legacyAnswerID); err != nil {
		t.Fatalf("legacy binary-shaped analysis write after additive migration: %v", err)
	}
}

func TestProvenanceFreshDatabaseIsEmptyAndContentFree(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	predicateCount, registeredCount := provenanceCandidateCounts(t, ctx, pool)
	if predicateCount != 0 || registeredCount != 0 {
		t.Fatalf("fresh database predicate=%d registered=%d", predicateCount, registeredCount)
	}
	t.Logf("fresh_database_predicate_candidates=%d registered=%d", predicateCount, registeredCount)

	wantColumns := map[string][]string{
		"answer_evaluation_provenance": {
			"behavior_policy_version", "created_at", "deterministic_policy_version",
			"deterministic_result", "final_correct", "id", "legacy_resolution",
			"model_answer_correct", "model_confidence", "semantic_policy_version", "student_answer_id",
		},
		"mastery_evidence_provenance": {
			"answer_evaluation_provenance_id", "authorization_source", "created_at", "evidence_form",
			"id", "knowledge_point_id", "provenance_risk", "student_answer_id", "student_id",
		},
	}
	for table, want := range wantColumns {
		got := tableColumns(t, ctx, pool, table)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s columns=%v want=%v", table, got, want)
		}
		for _, column := range got {
			lower := strings.ToLower(column)
			for _, forbidden := range []string{"answer_text", "correct_answer", "full_solution", "model_reason", "provider_response", "hash", "excerpt"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("%s contains forbidden content-bearing column %s", table, column)
				}
			}
		}
	}
}

func TestProvenanceRecordsRawModelSeparatelyWithoutChangingAcceptedBehavior(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	studentUserID := fixtureStudentUserID(t, ctx, pool, fixture.studentID)
	now := time.Date(2031, 2, 3, 4, 5, 6, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions
SET current_state='ASK',socratic_fail_count=0,assistance_level=0,evidence_form='LIFE',
    started_at=$2::timestamptz-interval '1 minute',last_resumed_at=$2::timestamptz-interval '1 minute',
    last_activity_at=$2::timestamptz-interval '5 seconds'
WHERE id=$1`, fixture.sessionID, now); err != nil {
		t.Fatal(err)
	}
	startBusinessWriteTracking(t, ctx, pool)
	agent := &provenanceTeachingAgent{analysis: ai.AnalyzeAnswerResult{
		AnswerCorrect: true, ReasoningQuality: "STRONG", Confidence: 0.95,
		ErrorType: "NONE", EmotionSignal: "NEUTRAL", Engagement: "NORMAL",
		RecommendedAction: tutor.StateVariant,
	}}
	service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return now }).WithTeachingAgent(agent)
	result, err := service.Submit(ctx, studentUserID, fixture.sessionID, "semantically accepted current response")
	if err != nil {
		t.Fatal(err)
	}
	if agent.analyzeCalls != 1 || agent.generateCalls != 0 {
		t.Fatalf("AI calls analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	}
	t.Logf("accepted_path_ai_calls analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	if result.Status != "COMPLETED" || result.Action != tutor.StateComplete || result.MasteryState != mastery.Learning || result.Energy != 2 || result.TomorrowChanged {
		t.Fatalf("legacy accepted result changed: %+v", result)
	}

	wantWrites := map[string]int{
		"answer_analyses:INSERT":       1,
		"learning_sessions:UPDATE":     2,
		"reward_events:INSERT":         1,
		"student_activity_days:INSERT": 1,
		"student_answers:INSERT":       1,
		"student_growth:INSERT":        1,
		"student_growth:UPDATE":        2,
		"student_skill_states:INSERT":  1,
		"tutor_events:INSERT":          6,
		"tutor_turns:INSERT":           2,
	}
	assertBusinessWrites(t, ctx, pool, wantWrites)
	assertAcceptedLegacySnapshot(t, ctx, pool, fixture, now)

	var deterministicResult, deterministicVersion, legacyResolution, behaviorVersion string
	var modelCorrect, finalCorrect bool
	var confidence float64
	if err := pool.QueryRow(ctx, `
SELECT deterministic_result,deterministic_policy_version,model_answer_correct,
       model_confidence::float8,legacy_resolution,final_correct,behavior_policy_version
FROM answer_evaluation_provenance
ORDER BY created_at DESC LIMIT 1`).Scan(
		&deterministicResult, &deterministicVersion, &modelCorrect, &confidence,
		&legacyResolution, &finalCorrect, &behaviorVersion,
	); err != nil {
		t.Fatal(err)
	}
	if deterministicResult != "NO_MATCH" || deterministicVersion != "normalized-string-equality-v1" ||
		!modelCorrect || confidence != 0.95 || legacyResolution != "MODEL_MEDIATED_ACCEPTED" ||
		!finalCorrect || behaviorVersion != "legacy-model-override-v1" {
		t.Fatalf("separated evaluation provenance=%s/%s model=%t/%.2f resolution=%s final=%t behavior=%s",
			deterministicResult, deterministicVersion, modelCorrect, confidence, legacyResolution, finalCorrect, behaviorVersion)
	}
	var authorizationSource, risk string
	if err := pool.QueryRow(ctx, `SELECT authorization_source,provenance_risk FROM mastery_evidence_provenance`).Scan(&authorizationSource, &risk); err != nil {
		t.Fatal(err)
	}
	if authorizationSource != "LEGACY_MODEL_MEDIATED" || risk != "UNREVIEWED" {
		t.Fatalf("mastery evidence provenance=%s/%s", authorizationSource, risk)
	}
	if _, err := pool.Exec(ctx, `UPDATE answer_evaluation_provenance SET created_at=created_at`); err == nil {
		t.Fatal("append-only answer evaluation provenance accepted an update")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM answer_evaluation_provenance`); err == nil {
		t.Fatal("append-only answer evaluation provenance accepted a delete")
	}
	var evaluations, rawModelResults, finalResults, separatedModelMediated, evidenceSources int
	if err := pool.QueryRow(ctx, `
SELECT count(*)::integer,
       count(*) FILTER (WHERE model_answer_correct IS NOT NULL AND model_confidence IS NOT NULL)::integer,
       count(*) FILTER (WHERE final_correct)::integer,
       count(*) FILTER (
           WHERE deterministic_result='NO_MATCH'
             AND model_answer_correct
             AND final_correct
             AND legacy_resolution='MODEL_MEDIATED_ACCEPTED'
       )::integer
FROM answer_evaluation_provenance`).Scan(&evaluations, &rawModelResults, &finalResults, &separatedModelMediated); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM mastery_evidence_provenance
WHERE authorization_source='LEGACY_MODEL_MEDIATED' AND provenance_risk='UNREVIEWED'`).Scan(&evidenceSources); err != nil {
		t.Fatal(err)
	}
	t.Logf("evaluation_rows=%d raw_model_rows=%d final_correct_rows=%d separated_model_mediated_rows=%d evidence_source_rows=%d",
		evaluations, rawModelResults, finalResults, separatedModelMediated, evidenceSources)
}

func TestProvenancePreservesReviewFailureBehavior(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := prepareB2ReviewFixture(t, ctx, pool, 0)
	startBusinessWriteTracking(t, ctx, pool)
	agent := &provenanceTeachingAgent{analysis: ai.AnalyzeAnswerResult{
		AnswerCorrect: false, ReasoningQuality: "WEAK", Confidence: 0.97,
		ErrorType: "FIXED_COST_IGNORED", Misconceptions: []string{"FIXED_COST_IGNORED"},
		EmotionSignal: "NEUTRAL", Engagement: "NORMAL", RecommendedAction: tutor.StateProbe,
	}}
	service := classroom.NewService(pool, nil, nil, nil).WithClock(func() time.Time { return fixture.now }).WithTeachingAgent(agent)
	result, err := service.Submit(ctx, fixture.studentUserID, fixture.sessionID, "review response not accepted")
	if err != nil {
		t.Fatal(err)
	}
	if agent.analyzeCalls != 1 || agent.generateCalls != 1 {
		t.Fatalf("AI calls analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	}
	t.Logf("review_failure_ai_calls analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	if result.Status != "ACTIVE" || result.Action != tutor.StateProbe {
		t.Fatalf("wrong-review response changed: %+v", result)
	}
	wantWrites := map[string]int{
		"answer_analyses:INSERT":        1,
		"learning_sessions:UPDATE":      3,
		"review_queue:INSERT":           1,
		"student_answers:INSERT":        1,
		"student_misconceptions:INSERT": 1,
		"student_skill_states:UPDATE":   1,
		"tutor_events:INSERT":           4,
		"tutor_turns:INSERT":            2,
	}
	assertBusinessWrites(t, ctx, pool, wantWrites)

	var state, currentState string
	var failures, independent, assisted, evidenceRows int
	var failedAt *time.Time
	if err := pool.QueryRow(ctx, `
SELECT skill.state,skill.consecutive_review_failures,skill.independent_successes,
       skill.assisted_successes,session.current_state,session.review_attempt_failed_at
FROM student_skill_states skill
JOIN learning_sessions session ON session.student_id=skill.student_id
WHERE session.id=$1 AND skill.knowledge_point_id=$2`, fixture.sessionID, fixture.knowledgePointID).Scan(
		&state, &failures, &independent, &assisted, &currentState, &failedAt,
	); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM mastery_evidence_provenance`).Scan(&evidenceRows); err != nil {
		t.Fatal(err)
	}
	if state != "REGRESSED" || failures != 1 || independent != 3 || assisted != 0 ||
		currentState != "PROBE" || failedAt == nil || !failedAt.Equal(fixture.now) || evidenceRows != 0 {
		t.Fatalf("review failure snapshot state=%s failures=%d evidence=%d/%d current=%s failed_at=%v provenance=%d",
			state, failures, independent, assisted, currentState, failedAt, evidenceRows)
	}
	var deterministicResult, legacyResolution string
	var modelCorrect, finalCorrect bool
	if err := pool.QueryRow(ctx, `
SELECT deterministic_result,model_answer_correct,legacy_resolution,final_correct
FROM answer_evaluation_provenance`).Scan(&deterministicResult, &modelCorrect, &legacyResolution, &finalCorrect); err != nil {
		t.Fatal(err)
	}
	if deterministicResult != "NO_MATCH" || modelCorrect || legacyResolution != "NOT_ACCEPTED" || finalCorrect {
		t.Fatalf("review evaluation provenance=%s model=%t resolution=%s final=%t", deterministicResult, modelCorrect, legacyResolution, finalCorrect)
	}
	t.Logf("review_failure_state_rows=1 mastery_evidence_rows=%d", evidenceRows)
}

func provenanceCandidateCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int, int) {
	t.Helper()
	var predicateCount, registeredCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM legacy_model_mediated_evidence_candidates`).Scan(&predicateCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM mastery_evidence_provenance
WHERE authorization_source='LEGACY_MODEL_MEDIATED' AND provenance_risk='UNREVIEWED'`).Scan(&registeredCount); err != nil {
		t.Fatal(err)
	}
	return predicateCount, registeredCount
}

func tableColumns(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_schema=current_schema() AND table_name=$1 ORDER BY column_name`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}

func schemaSignature(t *testing.T, ctx context.Context, pool *pgxpool.Pool, excluded []string) string {
	t.Helper()
	var signature string
	if err := pool.QueryRow(ctx, `
SELECT COALESCE(string_agg(
    table_name || ':' || column_name || ':' || data_type || ':' || is_nullable || ':' || COALESCE(column_default,''),
    E'\n' ORDER BY table_name,ordinal_position
),'')
FROM information_schema.columns
WHERE table_schema=current_schema() AND NOT (table_name=ANY($1::text[]))`, excluded).Scan(&signature); err != nil {
		t.Fatal(err)
	}
	return signature
}

func assertLegacySchemaUnchanged(t *testing.T, ctx context.Context, pool *pgxpool.Pool, before string) {
	t.Helper()
	after := schemaSignature(t, ctx, pool, []string{
		"answer_evaluation_provenance",
		"mastery_evidence_provenance",
		"legacy_model_mediated_evidence_candidates",
		"minor_safety_incidents",
		"minor_safety_access_audits",
	})
	if after != before {
		t.Fatal("additive provenance migration changed a legacy table definition")
	}
}

func fixtureStudentUserID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID uuid.UUID) uuid.UUID {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, studentID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func startBusinessWriteTracking(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
CREATE TABLE provenance_test_write_observations (
    table_name text NOT NULL,
    operation text NOT NULL
);
CREATE FUNCTION provenance_test_observe_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO provenance_test_write_observations(table_name,operation) VALUES(TG_TABLE_NAME,TG_OP);
    RETURN COALESCE(NEW,OLD);
END $$`); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `
SELECT table_name FROM information_schema.tables
WHERE table_schema=current_schema() AND table_type='BASE TABLE'
  AND table_name NOT IN (
      'schema_migrations','answer_evaluation_provenance','mastery_evidence_provenance',
      'provenance_test_write_observations'
  )
ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	for _, table := range tables {
		statement := fmt.Sprintf(
			"CREATE TRIGGER provenance_test_observe_write AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION provenance_test_observe_write()",
			pgx.Identifier{table}.Sanitize(),
		)
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("track writes on %s: %v", table, err)
		}
	}
}

func assertBusinessWrites(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want map[string]int) {
	t.Helper()
	rows, err := pool.Query(ctx, `
SELECT table_name || ':' || operation,count(*)::integer
FROM provenance_test_write_observations
GROUP BY table_name,operation ORDER BY table_name,operation`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			t.Fatal(err)
		}
		got[key] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("business row writes=%v want=%v", got, want)
	}
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	counts := make([]string, 0, len(keys))
	for _, key := range keys {
		counts = append(counts, fmt.Sprintf("%s=%d", key, got[key]))
	}
	t.Logf("business_row_writes_provenance_excluded %s", strings.Join(counts, " "))
}

func assertAcceptedLegacySnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture securityFixture, now time.Time) {
	t.Helper()
	var sessionStatus, currentState, skillState string
	var independent, assisted, life, variant, textbook, review, score, energy int
	var endedAt time.Time
	if err := pool.QueryRow(ctx, `
SELECT session.status,session.current_state,session.ended_at,
       skill.state,skill.independent_successes,skill.assisted_successes,
       skill.life_context_successes,skill.variant_successes,skill.textbook_successes,
       skill.review_successes,skill.score_internal::integer,growth.total_energy
FROM learning_sessions session
JOIN questions question ON question.id=session.current_question_id
JOIN student_skill_states skill
  ON skill.student_id=session.student_id AND skill.knowledge_point_id=question.knowledge_point_id
JOIN student_growth growth ON growth.student_id=session.student_id
WHERE session.id=$1`, fixture.sessionID).Scan(
		&sessionStatus, &currentState, &endedAt, &skillState, &independent, &assisted,
		&life, &variant, &textbook, &review, &score, &energy,
	); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "COMPLETED" || currentState != "COMPLETE" || !endedAt.Equal(now) ||
		skillState != "LEARNING" || independent != 1 || assisted != 0 || life != 1 ||
		variant != 0 || textbook != 0 || review != 0 || score != 25 || energy != 2 {
		t.Fatalf("accepted legacy snapshot session=%s/%s ended=%s skill=%s evidence=%d/%d/%d/%d/%d/%d score=%d energy=%d",
			sessionStatus, currentState, endedAt, skillState, independent, assisted, life, variant, textbook, review, score, energy)
	}
	var rewardCount, activityCount, completedSessions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reward_events WHERE session_id=$1 AND type='EFFORT' AND points=2`, fixture.sessionID).Scan(&rewardCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(completed_sessions),0)::integer FROM student_activity_days WHERE student_id=$1`, fixture.studentID).Scan(&activityCount, &completedSessions); err != nil {
		t.Fatal(err)
	}
	if rewardCount != 1 || activityCount != 1 || completedSessions != 1 {
		t.Fatalf("accepted downstream reward=%d activity rows/sessions=%d/%d", rewardCount, activityCount, completedSessions)
	}
	rows, err := pool.Query(ctx, `SELECT type,student_payload_json::text,parent_payload_json::text FROM tutor_events WHERE session_id=$1 ORDER BY sequence`, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var eventTypes []string
	for rows.Next() {
		var eventType, studentPayload, parentPayload string
		if err := rows.Scan(&eventType, &studentPayload, &parentPayload); err != nil {
			t.Fatal(err)
		}
		combined := strings.ToLower(studentPayload + parentPayload)
		if strings.Contains(combined, "provenance") || strings.Contains(combined, "deterministic_result") || strings.Contains(combined, "legacy_resolution") {
			t.Fatalf("realtime payload changed with provenance fields for %s", eventType)
		}
		eventTypes = append(eventTypes, eventType)
	}
	wantEvents := []string{"ANSWER_SUBMITTED", "ANSWER_ANALYZED", "AI_TURN_COMPLETED", "MASTERY_UPDATED", "REWARD_GRANTED", "SESSION_COMPLETED"}
	if !reflect.DeepEqual(eventTypes, wantEvents) {
		t.Fatalf("realtime event order=%v want=%v", eventTypes, wantEvents)
	}
	t.Logf("accepted_state_snapshot session_completed=1 independent_successes=%d reward_rows=%d activity_rows=%d realtime_events=%d", independent, rewardCount, activityCount, len(eventTypes))
}
