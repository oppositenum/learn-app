package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/contentpipeline"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/planner"
	"github.com/oppositenum/ai-learning-tutor/server/internal/seedcontent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

func TestB7LinearEquationActivationCompletesFourStagesWithPipelineEvidence(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture, activation := seedLinearEquationActivation(t, ctx, pool)
	agent := &provenanceTeachingAgent{}
	router := stageRouter(pool, classroom.NewService(pool, nil, nil, nil, planner.NewService(pool)).WithTeachingAgent(agent))

	start := performJSON(router, http.MethodPost, "/api/v1/student/sessions", fixture.security.studentToken, map[string]any{"plan_block_id": fixture.planBlockID})
	if start.Code != http.StatusOK {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, start.Body.Bytes())
	var session classroom.StudentSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.StageFlow == nil || session.StageFlow.Stage != classroom.StageOriginal {
		t.Fatalf("start did not select ORIGINAL: %+v", session.StageFlow)
	}
	if session.Interaction.Version != studentinteraction.Version || session.Interaction.Fallback || session.Interaction.Scene == nil || session.Interaction.AccessibleFallback == "" {
		t.Fatalf("structured interaction missing: %+v", session.Interaction)
	}
	seenStages := make([]classroom.Stage, 0, 4)
	for len(seenStages) < 4 {
		seenStages = append(seenStages, session.StageFlow.Stage)
		response := linearEquationCorrectResponse(t, ctx, pool, session.QuestionID)
		submit := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
			"operation_id": uuid.New(), "stage": session.StageFlow.Stage, "task_id": session.QuestionID,
			"task_version": session.StageFlow.TaskVersion, "response": response,
		})
		if submit.Code != http.StatusOK {
			t.Fatalf("submit %s=%d %s", seenStages[len(seenStages)-1], submit.Code, submit.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, submit.Body.Bytes())
		read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
		if read.Code != http.StatusOK {
			t.Fatalf("read=%d %s", read.Code, read.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, read.Body.Bytes())
		if err := json.Unmarshal(read.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		if len(seenStages) < 4 && (session.StageFlow == nil || session.StageFlow.Stage == classroom.StageComplete) {
			t.Fatalf("stage completed too early after %s: %+v", seenStages[len(seenStages)-1], session.StageFlow)
		}
	}
	if session.Status != "COMPLETED" || session.State != string(classroom.StageComplete) {
		t.Fatalf("four-stage session not complete: %+v", session)
	}
	if agent.analyzeCalls != 0 || agent.generateCalls != 0 {
		t.Fatalf("all-correct deterministic path called AI analyze=%d generate=%d", agent.analyzeCalls, agent.generateCalls)
	}
	var attempts, completed, evidence, authorized int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int,count(*) FILTER (WHERE stage_completed)::int FROM classroom_stage_attempts WHERE session_id=$1`, session.ID).Scan(&attempts, &completed); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int,count(*) FILTER (WHERE authorized_for_mastery)::int FROM classroom_stage_evidence WHERE session_id=$1`, session.ID).Scan(&evidence, &authorized); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || completed != 4 || evidence != 4 || authorized != 4 {
		t.Fatalf("stage persistence attempts=%d completed=%d evidence=%d authorized=%d", attempts, completed, evidence, authorized)
	}
	var life, variant, textbook, independent int
	if err := pool.QueryRow(ctx, `SELECT life_context_successes,variant_successes,textbook_successes,independent_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, fixture.security.studentID, activation.KnowledgePointID).Scan(&life, &variant, &textbook, &independent); err != nil {
		t.Fatal(err)
	}
	if life != 1 || variant != 1 || textbook != 2 || independent != 4 {
		t.Fatalf("evidence counters=%d/%d/%d independent=%d", life, variant, textbook, independent)
	}

	var releasedCount, readyCount, taskCount int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM questions WHERE knowledge_point_id=$1 AND status='RELEASED'`, activation.KnowledgePointID).Scan(&releasedCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM classroom_task_lineages WHERE knowledge_point_id=$1 AND status='READY'`, activation.KnowledgePointID).Scan(&readyCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM classroom_stage_tasks WHERE lineage_id=$1`, activation.LineageID).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if releasedCount != 13 || readyCount != 1 || taskCount != 12 {
		t.Fatalf("activation inventory released=%d ready=%d tasks=%d", releasedCount, readyCount, taskCount)
	}
	var reviewFindings string
	if err := pool.QueryRow(ctx, `SELECT findings_json::text FROM content_reviews WHERE question_id=$1 AND content_version=(SELECT content_version FROM questions WHERE id=$1) ORDER BY created_at DESC LIMIT 1`, activation.QuestionIDs[0]).Scan(&reviewFindings); err != nil {
		t.Fatal(err)
	}
	for _, dimension := range []string{"age_appropriate=true", "factually_sound=true", "unambiguous=true", "no_answer_leak=true", "safe_values=true", "numeric_material_compatible=true"} {
		if !strings.Contains(reviewFindings, dimension) {
			t.Fatalf("secondary review did not record %s: %s", dimension, reviewFindings)
		}
	}
}

func TestB7LinearEquationAssistanceRequiresFreshIndependentTaskAndFailsClosedOnDrift(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture, activation := seedLinearEquationActivation(t, ctx, pool)
	agent := &provenanceTeachingAgent{}
	router := stageRouter(pool, classroom.NewService(pool, nil, nil, nil).WithTeachingAgent(agent))
	session := startStageSession(t, router, fixture)
	firstTask := session.QuestionID

	before := linearEquationSkillCounters(t, ctx, pool, fixture.security.studentID, activation.KnowledgePointID)
	help := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/support", fixture.security.studentToken, map[string]any{
		"type": "HINT", "operation_id": uuid.New(), "stage": session.StageFlow.Stage,
		"task_id": session.QuestionID, "task_version": session.StageFlow.TaskVersion,
	})
	if help.Code != http.StatusOK {
		t.Fatalf("help=%d %s", help.Code, help.Body.String())
	}
	assisted := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+session.ID.String()+"/answers", fixture.security.studentToken, map[string]any{
		"operation_id": uuid.New(), "stage": session.StageFlow.Stage, "task_id": session.QuestionID,
		"task_version": session.StageFlow.TaskVersion, "response": linearEquationCorrectResponse(t, ctx, pool, session.QuestionID),
	})
	if assisted.Code != http.StatusOK {
		t.Fatalf("assisted answer=%d %s", assisted.Code, assisted.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, assisted.Body.Bytes())
	var result classroom.StageSubmitResult
	if err := json.Unmarshal(assisted.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.EvidenceKind != classroom.StageEvidenceAssisted || result.StageCompleted || result.TaskID == nil || *result.TaskID == firstTask {
		t.Fatalf("assisted result did not require fresh task: %+v", result)
	}
	var presented int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT question_id)::int FROM classroom_stage_attempts WHERE session_id=$1`, session.ID).Scan(&presented); err != nil {
		t.Fatal(err)
	}
	if presented != 1 {
		t.Fatalf("presented task exclusion set=%d want the first task only before fresh task is answered", presented)
	}
	after := linearEquationSkillCounters(t, ctx, pool, fixture.security.studentID, activation.KnowledgePointID)
	if before != after {
		t.Fatalf("assisted success changed independent evidence counters before=%+v after=%+v", before, after)
	}
	if agent.generateCalls != 1 {
		t.Fatalf("hint generated %d tutor calls", agent.generateCalls)
	}

	driftPool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, driftPool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	driftFixture, _ := seedLinearEquationActivation(t, ctx, driftPool)
	driftRouter := stageRouter(driftPool, classroom.NewService(driftPool, nil, nil, nil))
	driftSession := startStageSession(t, driftRouter, driftFixture)
	if _, err := driftPool.Exec(ctx, `UPDATE questions SET scene_public_json=jsonb_set(scene_public_json,'{renderer}','"UNKNOWN"') WHERE id=$1`, driftSession.QuestionID); err != nil {
		t.Fatal(err)
	}
	read := performJSON(driftRouter, http.MethodGet, "/api/v1/student/sessions/"+driftSession.ID.String(), driftFixture.security.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("drift read=%d %s", read.Code, read.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, read.Body.Bytes())
	var drifted classroom.StudentSession
	if err := json.Unmarshal(read.Body.Bytes(), &drifted); err != nil {
		t.Fatal(err)
	}
	if !drifted.Interaction.Fallback || drifted.Interaction.Renderer != studentinteraction.RendererTextFallback || drifted.Interaction.Scene != nil {
		t.Fatalf("material drift did not fail closed: %+v", drifted.Interaction)
	}
}

func TestB7LinearEquationParentReceivesReadableAnswerButStudentDoesNot(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture, activation := seedLinearEquationActivation(t, ctx, pool)
	router := stageRouter(pool, classroom.NewService(pool, nil, nil, nil))
	session := startStageSession(t, router, fixture)
	parentResponse := performParentSessionRequest(router, fixture.security.parentToken, fixture.security.studentID, session.ID)
	if parentResponse.Code != http.StatusOK {
		t.Fatalf("parent live=%d %s", parentResponse.Code, parentResponse.Body.String())
	}
	if strings.Contains(parentResponse.Body.String(), "结构化回应已由规则核验") || strings.Contains(parentResponse.Body.String(), "scoring_rule_private_json") {
		t.Fatalf("parent response still contains placeholder/private scoring rule: %s", parentResponse.Body.String())
	}
	var parentPayload struct {
		CorrectAnswer json.RawMessage `json:"correct_answer"`
		FullSolution  string          `json:"full_solution"`
	}
	if err := json.Unmarshal(parentResponse.Body.Bytes(), &parentPayload); err != nil {
		t.Fatal(err)
	}
	if string(parentPayload.CorrectAnswer) == "null" || parentPayload.FullSolution == "" || !strings.Contains(parentPayload.FullSolution, "标准回应") {
		t.Fatalf("parent answer/solution not readable: %+v", parentPayload)
	}
	studentResponse := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+session.ID.String(), fixture.security.studentToken, nil)
	if studentResponse.Code != http.StatusOK {
		t.Fatalf("student session=%d %s", studentResponse.Code, studentResponse.Body.String())
	}
	assertStudentPayloadHasNoPrivateFields(t, studentResponse.Body.Bytes())
	if strings.Contains(studentResponse.Body.String(), parentPayload.FullSolution) || strings.Contains(studentResponse.Body.String(), activation.Tasks[0].ScoringRuleVersion) {
		t.Fatalf("student response exposed parent/private material: %s", studentResponse.Body.String())
	}
}

func TestB7LinearEquationMisconceptionTaxonomyRejectsOutOfScopeDraft(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	knowledgePointID, sourceID, err := seedcontent.LinearEquationKnowledgePoint(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	assets, _, err := seedcontent.LinearEquationAssets(knowledgePointID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	assets[0].TeacherPrivate.Misconceptions = []string{"LINEAR_RELATIONSHIP_NOT_IDENTIFIED"}
	pipeline := seedcontent.NewPipeline(pool)
	if err := pipeline.ImportDraft(ctx, assets[0], contentpipeline.GenerationMetadata{Provider: "test", Model: "taxonomy-test", RequestID: "taxonomy-test"}); err != nil {
		t.Fatal(err)
	}
	questionID, err := uuid.Parse(assets[0].QuestionID)
	if err != nil {
		t.Fatal(err)
	}
	_, status, validation, err := pipeline.Validate(ctx, questionID)
	if err != nil {
		t.Fatal(err)
	}
	if status != contentpipeline.RejectedAutomatic || validation.Passed {
		t.Fatalf("out-of-scope misconception was not rejected: status=%s validation=%+v", status, validation)
	}
	var released bool
	if err := pool.QueryRow(ctx, `SELECT status='RELEASED' FROM questions WHERE id=$1`, questionID).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if released {
		t.Fatal("out-of-scope misconception draft reached RELEASED")
	}
}

type linearEquationCounters struct {
	independent, life, variant, textbook int
}

func linearEquationSkillCounters(t *testing.T, ctx context.Context, pool *pgxpool.Pool, studentID, knowledgePointID uuid.UUID) linearEquationCounters {
	t.Helper()
	var result linearEquationCounters
	err := pool.QueryRow(ctx, `SELECT independent_successes,life_context_successes,variant_successes,textbook_successes FROM student_skill_states WHERE student_id=$1 AND knowledge_point_id=$2`, studentID, knowledgePointID).Scan(&result.independent, &result.life, &result.variant, &result.textbook)
	if errors.Is(err, pgx.ErrNoRows) {
		return result
	}
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func seedLinearEquationActivation(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (stageFixture, seedcontent.Activation) {
	t.Helper()
	security := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='ABANDONED',ended_at=now() WHERE id=$1`, security.sessionID); err != nil {
		t.Fatal(err)
	}
	activation, err := seedcontent.EnsureLinearEquation(ctx, pool, seedcontent.NewPipeline(pool), uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	var subjectID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT subject_id FROM knowledge_points WHERE id=$1`, activation.KnowledgePointID).Scan(&subjectID); err != nil {
		t.Fatal(err)
	}
	planID, blockID := uuid.New(), uuid.New()
	date := time.Now().In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02")
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES($1,$2,$3::date,20,'PROPOSED')`, planID, security.studentID, date); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status) VALUES($1,$2,1,$3,$4,20,'CURRENT_GRADE','B7-2 MATH-LINEAR-EQUATION activation','AVAILABLE')`, blockID, planID, subjectID, activation.KnowledgePointID); err != nil {
		t.Fatal(err)
	}
	return stageFixture{security: security, studentUserID: fixtureStudentUserID(t, ctx, pool, security.studentID), knowledgePointID: activation.KnowledgePointID, lineageID: activation.LineageID, planBlockID: blockID}, activation
}

func seedLinearEquationPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, security securityFixture, activation seedcontent.Activation) stageFixture {
	t.Helper()
	var subjectID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT subject_id FROM knowledge_points WHERE id=$1`, activation.KnowledgePointID).Scan(&subjectID); err != nil {
		t.Fatal(err)
	}
	planID, blockID := uuid.New(), uuid.New()
	date := time.Now().In(time.FixedZone("CST", 8*60*60)).Format("2006-01-02")
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plans(id,student_id,plan_date,target_minutes,status) VALUES($1,$2,$3::date,20,'PROPOSED')`, planID, security.studentID, date); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO learning_plan_blocks(id,plan_id,sequence,subject_id,knowledge_point_id,minutes,mode,reason,status) VALUES($1,$2,1,$3,$4,20,'CURRENT_GRADE','B7-2 MATH-LINEAR-EQUATION activation','AVAILABLE')`, blockID, planID, subjectID, activation.KnowledgePointID); err != nil {
		t.Fatal(err)
	}
	return stageFixture{security: security, studentUserID: fixtureStudentUserID(t, ctx, pool, security.studentID), knowledgePointID: activation.KnowledgePointID, lineageID: activation.LineageID, planBlockID: blockID}
}

func linearEquationCorrectResponse(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID uuid.UUID) map[string]any {
	t.Helper()
	var version string
	var rawScene, rawRule json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT scoring_rule_version,scene_public_json,scoring_rule_private_json FROM classroom_stage_tasks task JOIN questions question ON question.id=task.question_id WHERE task.question_id=$1`, questionID).Scan(&version, &rawScene, &rawRule); err != nil {
		t.Fatal(err)
	}
	var scene studentinteraction.Scene
	if err := json.Unmarshal(rawScene, &scene); err != nil {
		t.Fatal(err)
	}
	var rule struct {
		RuleType string   `json:"rule_type"`
		Options  []string `json:"expected_option_ids"`
		Items    []string `json:"expected_item_ids"`
		Value    float64  `json:"expected_value"`
		Values   []struct {
			SlotID   string   `json:"slot_id"`
			Accepted []string `json:"accepted_values"`
		} `json:"expected_values"`
	}
	if err := json.Unmarshal(rawRule, &rule); err != nil {
		t.Fatal(err)
	}
	switch version {
	case "exact-option-set-v1":
		return map[string]any{"selected_option_ids": rule.Options}
	case "exact-order-v1":
		return map[string]any{"ordered_item_ids": rule.Items}
	case "exact-number-v1":
		return map[string]any{"value": rule.Value}
	case "exact-fill-v1":
		values := make([]map[string]string, 0, len(rule.Values))
		for _, value := range rule.Values {
			values = append(values, map[string]string{"slot_id": value.SlotID, "value": value.Accepted[0]})
		}
		return map[string]any{"values": values}
	default:
		t.Fatalf("unsupported scoring version=%s renderer=%s", version, scene.Renderer)
		return nil
	}
}
