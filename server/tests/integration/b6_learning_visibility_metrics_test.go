package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

const learningEffectMigration = "000030_learning_effect_metrics.sql"

func TestB6LearningMetricsMigrationIsBodyFreeAppendOnlyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrationsBefore(t, learningEffectMigration)); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_effect_events`).Scan(&before); err != nil || before == 0 {
		t.Fatalf("backfilled learning events=%d err=%v", before, err)
	}
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM learning_effect_events`).Scan(&after); err != nil || after != before {
		t.Fatalf("repeat migration events=%d want=%d err=%v", after, before, err)
	}

	wantColumns := map[string][]string{
		"learning_effect_events": {
			"assistance_level", "classification_version", "classroom_state", "created_at", "effective_elapsed_ms",
			"event_type", "evidence_form", "id", "knowledge_point_id", "occurred_at", "outcome", "question_id",
			"review_interval_days", "session_id", "source_id", "source_kind", "student_id", "subject_id", "support_type",
		},
		"ai_request_outcomes": {
			"created_at", "http_status", "latency_ms", "model", "occurred_at", "outcome", "provider", "purpose",
			"request_id", "session_id", "student_id",
		},
	}
	for table, want := range wantColumns {
		got := tableColumns(t, ctx, pool, table)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s columns=%v want=%v", table, got, want)
		}
		for _, column := range got {
			for _, forbidden := range []string{"answer_text", "correct_answer", "full_solution", "model_reason", "provider_response", "prompt", "transcript", "audio", "hash", "excerpt"} {
				if strings.Contains(strings.ToLower(column), forbidden) {
					t.Fatalf("%s contains prohibited body column %s", table, column)
				}
			}
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_effect_events SET outcome=outcome`); err == nil {
		t.Fatal("learning effect event accepted update")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM learning_effect_events`); err == nil {
		t.Fatal("learning effect event accepted delete")
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ai_request_outcomes(request_id,student_id,session_id,provider,model,purpose,outcome,latency_ms,occurred_at)
VALUES('append-only-check',$1,$2,'openai','test-model','ANSWER_ANALYSIS','SUCCEEDED',1,now())`, fixture.studentID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM ai_request_outcomes WHERE request_id='append-only-check'`); err == nil {
		t.Fatal("AI request outcome accepted delete")
	}
}

func TestB6ParentLivePreviewAndCompletedReportEnforceVisibilityBoundary(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Parents:      parent.NewRepository(pool),
	})
	assertVisibility := func(answer, visibility string, preview bool) map[string]any {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE student_answers SET answer_text=$2 WHERE session_id=$1`, fixture.sessionID, answer); err != nil {
			t.Fatal(err)
		}
		response := performParentSessionRequest(router, fixture.parentToken, fixture.studentID, fixture.sessionID)
		if response.Code != http.StatusOK {
			t.Fatalf("Parent live=%d %s", response.Code, response.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["student_answer_visibility"] != visibility {
			t.Fatalf("visibility=%v want=%s", payload["student_answer_visibility"], visibility)
		}
		_, hasPreview := payload["student_answer_preview"]
		if hasPreview != preview {
			t.Fatalf("preview present=%t want=%t payload=%v", hasPreview, preview, payload)
		}
		if _, legacyField := payload["student_answer"]; legacyField {
			t.Fatal("legacy student_answer field remains in Parent DTO")
		}
		return payload
	}
	assertVisibility("", "NONE", false)
	eighty := strings.Repeat("界", 80)
	payload := assertVisibility(eighty, "SHORT_CURRENT", true)
	if payload["student_answer_preview"] != eighty {
		t.Fatal("80-character answer preview was altered")
	}
	assertVisibility(strings.Repeat("界", 81), "WITHHELD_LONG", false)
	assertVisibility("first line\nsecond line", "WITHHELD_LONG", false)

	completedCanary := "COMPLETED_STUDENT_DIALOGUE_CANARY"
	if _, err := pool.Exec(ctx, `UPDATE student_answers SET answer_text=$2 WHERE session_id=$1`, fixture.sessionID, completedCanary); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tutor_turns(id,session_id,sequence,actor,message) VALUES($1,$2,5,'STUDENT',$3)`, uuid.New(), fixture.sessionID, completedCanary); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET status='COMPLETED',current_state='COMPLETE',ended_at=now() WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	payload = assertVisibility(completedCanary, "WITHHELD_NOT_ACTIVE", false)
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), completedCanary) {
		t.Fatal("completed Parent report exposed Student answer or dialogue")
	}
	if payload["detail_mode"] != "REPORT" {
		t.Fatalf("completed detail_mode=%v", payload["detail_mode"])
	}
}

func TestB6ParentRealtimePayloadNeverPersistsStudentAnswerBody(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	studentUserID := fixtureStudentUserID(t, ctx, pool, fixture.studentID)
	const answerCanary = "PARENT_REALTIME_STUDENT_ANSWER_CANARY"
	if _, err := classroom.NewService(pool, nil, nil, nil).Submit(ctx, studentUserID, fixture.sessionID, answerCanary); err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err := pool.QueryRow(ctx, `
SELECT count(*) FROM tutor_events
WHERE session_id=$1 AND parent_payload_json::text LIKE '%' || $2 || '%'`, fixture.sessionID, answerCanary).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("Parent realtime persisted %d Student answer bodies", leaked)
	}
}

func TestB6GrowthIndicatorsExposeDeterministicEventProvenanceWithoutBodies(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	var knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT knowledge_point_id FROM questions WHERE id=$1`, fixture.releasedQuestionID).Scan(&knowledgePointID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO mastery_evidence_provenance(
    id,student_answer_id,student_id,knowledge_point_id,evidence_form,authorization_source,provenance_risk
) SELECT $1,id,$2,$3,'VARIANT','DETERMINISTIC_RULE','NONE'
  FROM student_answers WHERE session_id=$4 LIMIT 1`, uuid.New(), fixture.studentID, knowledgePointID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	insertEvaluation := func(answerBody, deterministic, resolution string, final bool, submittedAt time.Time) {
		t.Helper()
		answerID, analysisID := uuid.New(), uuid.New()
		if _, err := pool.Exec(ctx, `
INSERT INTO student_answers(id,session_id,question_id,answer_text,submitted_at) VALUES($1,$2,$3,$4,$5)`,
			answerID, fixture.sessionID, fixture.releasedQuestionID, answerBody, submittedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO answer_analyses(id,student_answer_id,answer_correct,reasoning_quality,confidence,error_type,misconceptions_private_json,emotion_signal,engagement,recommended_action,created_at)
VALUES($1,$2,$3,'UNKNOWN',0,'NONE','[]','NEUTRAL','NORMAL','PROBE',$4)`,
			analysisID, answerID, final, submittedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO answer_evaluation_provenance(
    id,student_answer_id,deterministic_result,deterministic_policy_version,
    legacy_resolution,final_correct,behavior_policy_version,created_at
) VALUES($1,$2,$3,'normalized-string-equality-v1',$4,$5,'deterministic-evidence-authorization-v1',$6)`,
			uuid.New(), answerID, deterministic, resolution, final, submittedAt); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2041, 1, 1, 0, 0, 0, 0, time.UTC)
	insertEvaluation("ASSISTED_BODY_CANARY", "MATCH", "DETERMINISTIC_ACCEPTED", true, base)
	insertEvaluation("WRONG_BODY_CANARY", "NO_MATCH", "NOT_ACCEPTED", false, base.Add(time.Minute))
	insertEvaluation("CORRECTED_BODY_CANARY", "MATCH", "DETERMINISTIC_ACCEPTED", true, base.Add(2*time.Minute))
	if _, err := pool.Exec(ctx, `
INSERT INTO review_queue(id,student_id,knowledge_point_id,source,due_at,priority,attempts,status,resolved_at,result,created_at)
VALUES($1,$2,$3,'MASTERY',$4,50,1,'COMPLETED',$5,'INDEPENDENT_SUCCESS',$6)`,
		uuid.New(), fixture.studentID, knowledgePointID, base, base.Add(24*time.Hour), base.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(classroom.NewService(pool, nil, nil, nil), pool, parent.NewRepository(pool)),
	})
	response := performJSON(router, http.MethodGet, "/api/v1/student/growth", fixture.studentToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("growth=%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		GrowthEvidence struct {
			PolicyVersion string `json:"policy_version"`
			Indicators    []struct {
				Code   string `json:"code"`
				Count  int    `json:"count"`
				Events []struct {
					SourceKind string `json:"source_kind"`
					SourceID   string `json:"source_id"`
				} `json:"events"`
			} `json:"indicators"`
		} `json:"growth_evidence"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.GrowthEvidence.PolicyVersion != "growth-evidence-v1" || len(payload.GrowthEvidence.Indicators) != 5 {
		t.Fatalf("growth evidence contract=%+v", payload.GrowthEvidence)
	}
	counts := map[string]int{}
	for _, indicator := range payload.GrowthEvidence.Indicators {
		counts[indicator.Code] = indicator.Count
		for _, event := range indicator.Events {
			if event.SourceKind == "" || event.SourceID == "" {
				t.Fatalf("event lacks provenance: %+v", event)
			}
		}
	}
	for _, code := range []string{"INDEPENDENT_SOLVING", "UNDERSTANDING_AFTER_HELP", "SELF_CORRECTION", "TRANSFER_SUCCESS", "DELAYED_REVIEW"} {
		if counts[code] == 0 {
			t.Fatalf("indicator %s count=0 all=%v", code, counts)
		}
	}
	for _, forbidden := range []string{"ASSISTED_BODY_CANARY", "WRONG_BODY_CANARY", "CORRECTED_BODY_CANARY", "answer_text", "full_solution"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatalf("growth payload exposed prohibited content %q", forbidden)
		}
	}
}

func TestB6OwnerLearningEffectsComputeExplicitRatesAndUnknownLatencyCoverage(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	ownerToken := seedOwner(t, ctx, pool)
	var subjectID, knowledgePointID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT subject_id,knowledge_point_id FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id WHERE q.id=$1`, fixture.releasedQuestionID).Scan(&subjectID, &knowledgePointID); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2042, 2, 2, 10, 0, 0, 0, time.UTC)
	unknownSessionID := uuid.New()
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_sessions(
    id,student_id,subject_id,current_question_id,started_at,ended_at,status,target_minutes,
    actual_seconds,current_state,evidence_form
) VALUES($1,$2,$3,$4,$5,$5,'COMPLETED',10,0,'COMPLETE','LIFE')`,
		unknownSessionID, fixture.studentID, subjectID, fixture.releasedQuestionID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO learning_effect_events(
    id,student_id,session_id,subject_id,knowledge_point_id,question_id,event_type,source_kind,
    source_id,outcome,classroom_state,evidence_form,assistance_level,effective_elapsed_ms,occurred_at
) VALUES($1,$2,$3,$4,$5,$6,'TASK_ATTEMPT','ANSWER_ANALYSIS',$7,'CORRECT','ASK','LIFE',0,NULL,$8)`,
		uuid.New(), fixture.studentID, unknownSessionID, subjectID, knowledgePointID,
		fixture.releasedQuestionID, uuid.New(), base.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	types := []struct {
		eventType, sourceKind, outcome, support, state, form string
		elapsed, interval                                    any
	}{
		{"TASK_ATTEMPT", "ANSWER_ANALYSIS", "INCORRECT", "", "VARIANT", "VARIANT", int64(5000), nil},
		{"TUTOR_OUTPUT", "TUTOR_TURN", "DELIVERED", "", "PROBE", "VARIANT", nil, nil},
		{"SUPPORT_REQUESTED", "TUTOR_TURN", "HELP_REQUESTED", "EXPLAIN", "EXPLAIN", "VARIANT", nil, nil},
		{"TASK_ATTEMPT", "ANSWER_ANALYSIS", "CORRECT", "", "VARIANT", "VARIANT", int64(9000), int16(1)},
		{"TUTOR_OUTPUT", "TUTOR_TURN", "DELIVERED", "", "COMPLETE", "VARIANT", nil, nil},
		{"SESSION_EXIT", "LEARNING_SESSION", "COMPLETED", "", "COMPLETE", "VARIANT", int64(12000), nil},
	}
	for index, item := range types {
		var support any
		if item.support != "" {
			support = item.support
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO learning_effect_events(
    id,student_id,session_id,subject_id,knowledge_point_id,question_id,event_type,source_kind,
    source_id,outcome,support_type,classroom_state,evidence_form,assistance_level,
    effective_elapsed_ms,review_interval_days,occurred_at
) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,0,$14,$15,$16)`,
			uuid.New(), fixture.studentID, fixture.sessionID, subjectID, knowledgePointID,
			fixture.releasedQuestionID, item.eventType, item.sourceKind, uuid.New(), item.outcome,
			support, item.state, item.form, item.elapsed, item.interval, base.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ai_request_outcomes(request_id,student_id,session_id,provider,model,purpose,outcome,http_status,latency_ms,occurred_at)
VALUES('metric-success',$1,$2,'openai','test-model','ANSWER_ANALYSIS','SUCCEEDED',NULL,20,$3),
      ('metric-invalid',$1,$2,'openai','test-model','ANSWER_ANALYSIS','INVALID_RESPONSE',NULL,30,$3)`,
		fixture.studentID, fixture.sessionID, base); err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(classroom.NewService(pool, nil, nil, nil), pool, parent.NewRepository(pool)),
	})
	response := performJSON(router, http.MethodGet,
		"/api/v1/owner/learning-effects?student_id="+fixture.studentID.String()+"&subject=MATH&date_from=2042-02-02&date_to=2042-02-02",
		ownerToken, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("learning effects=%d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Definitions map[string]string `json:"definitions"`
		First       struct {
			Sessions         int      `json:"sessions"`
			MeasuredSessions int      `json:"measured_sessions"`
			AverageMS        *float64 `json:"average_ms"`
		} `json:"first_answer_effective_latency"`
		Share struct {
			StudentEvents int      `json:"student_events"`
			TutorEvents   int      `json:"tutor_events"`
			Rate          *float64 `json:"rate"`
		} `json:"student_interaction_share"`
		Transfer struct {
			Correct, Attempts int
			Rate              *float64 `json:"rate"`
		} `json:"transfer_accuracy"`
		Reengagement struct {
			Requests, Reengaged int
			Rate                *float64 `json:"rate"`
		} `json:"dont_know_reengagement"`
		AI struct {
			Requests, Anomalies int
			Rate                *float64 `json:"rate"`
		} `json:"ai_anomaly"`
		Completion []struct {
			AssistanceLevel  int `json:"assistance_level"`
			Completed, Exits int
			Rate             *float64 `json:"rate"`
		} `json:"completion_by_assistance"`
		Retention []struct {
			Days, Correct, Attempts int
			Rate                    *float64 `json:"rate"`
		} `json:"retention"`
		ExitStages []struct {
			Stage, Outcome string
			Count          int
		} `json:"exit_stages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Definitions["student_interaction_share"] == "" || payload.First.Sessions != 2 || payload.First.MeasuredSessions != 1 || payload.First.AverageMS == nil || *payload.First.AverageMS != 5000 ||
		payload.Share.StudentEvents != 4 || payload.Share.TutorEvents != 2 || payload.Share.Rate == nil || *payload.Share.Rate != float64(4)/6 ||
		payload.Transfer.Correct != 1 || payload.Transfer.Attempts != 2 || payload.Transfer.Rate == nil || *payload.Transfer.Rate != 0.5 ||
		payload.Reengagement.Requests != 1 || payload.Reengagement.Reengaged != 1 || payload.Reengagement.Rate == nil || *payload.Reengagement.Rate != 1 ||
		payload.AI.Requests != 2 || payload.AI.Anomalies != 1 || payload.AI.Rate == nil || *payload.AI.Rate != 0.5 {
		t.Fatalf("unexpected learning effects: %s", response.Body.String())
	}
	if len(payload.Completion) != 5 || payload.Completion[0].AssistanceLevel != 0 || payload.Completion[0].Completed != 1 || payload.Completion[0].Exits != 1 || payload.Completion[0].Rate == nil || *payload.Completion[0].Rate != 1 {
		t.Fatalf("completion metrics=%+v", payload.Completion)
	}
	if len(payload.Retention) != 2 || payload.Retention[0].Days != 1 || payload.Retention[0].Correct != 1 || payload.Retention[0].Attempts != 1 || payload.Retention[0].Rate == nil || *payload.Retention[0].Rate != 1 || payload.Retention[1].Days != 7 || payload.Retention[1].Rate != nil {
		t.Fatalf("retention metrics=%+v", payload.Retention)
	}
	if len(payload.ExitStages) != 1 || payload.ExitStages[0].Stage != "COMPLETE" || payload.ExitStages[0].Outcome != "COMPLETED" || payload.ExitStages[0].Count != 1 {
		t.Fatalf("exit metrics=%+v", payload.ExitStages)
	}
	for _, forbidden := range []string{"answer_text", "student_answer", "full_solution", "provider_response", "audio"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("learning effects payload contains prohibited field %q", forbidden)
		}
	}
}

func TestB6AIRequestOutcomeRecorderStoresOnlyBoundedClassifications(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	status := http.StatusTooManyRequests
	recorder := usage.NewRecorder(pool)
	record := ai.RequestOutcomeRecord{
		RequestID: "bounded-provider-failure", StudentID: fixture.studentID.String(),
		SessionID: fixture.sessionID.String(), Provider: "openai", Model: "test-model",
		Purpose: ai.PurposeAnswerAnalysis, Outcome: ai.RequestProviderError,
		HTTPStatus: &status, Latency: 125 * time.Millisecond,
		CreatedAt: time.Date(2043, 3, 3, 3, 3, 3, 0, time.UTC),
	}
	if err := recorder.RecordAIRequestOutcome(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAIRequestOutcome(ctx, record); err != nil {
		t.Fatalf("idempotent outcome record: %v", err)
	}
	var rows, storedStatus, latency int
	var outcome string
	if err := pool.QueryRow(ctx, `
SELECT count(*)::int,min(outcome),min(http_status),min(latency_ms)::int
FROM ai_request_outcomes WHERE request_id=$1`, record.RequestID).Scan(
		&rows, &outcome, &storedStatus, &latency,
	); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || outcome != "PROVIDER_ERROR" || storedStatus != status || latency != 125 {
		t.Fatalf("stored outcome=%d/%s/%d/%d", rows, outcome, storedStatus, latency)
	}
	invalid := record
	invalid.RequestID = "invalid-outcome"
	invalid.Outcome = ai.RequestOutcome("RAW_PROVIDER_BODY")
	if err := recorder.RecordAIRequestOutcome(ctx, invalid); err == nil {
		t.Fatal("database accepted an unbounded AI outcome")
	}
}
