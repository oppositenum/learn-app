package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestStudentSupportIsBoundedRoleSeparatedAndMetered(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','mock-tutor',now()-interval '1 day',1,0.5,2)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	provider := structuredResponseServer(t, []string{validTurnJSON("HINT", false), validTurnJSON("EXPLAIN", false)})
	defer provider.Close()
	recorder := usage.NewRecorder(pool)
	client, err := ai.NewOpenAIResponsesClient(provider.Client(), provider.URL, "test-key", "mock-tutor")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(client.WithUsageRecorder(recorder), allowTutorOutputAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	service := classroom.NewService(pool, nil, nil, recorder).WithTeachingAgent(agent)
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Classroom:    classroom.NewHandler(service, pool, parent.NewRepository(pool)),
	})

	var answersBefore, analysesBefore, turnsBefore, failedRoundsBefore int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1`, fixture.sessionID).Scan(&analysesBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT socratic_fail_count FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&failedRoundsBefore); err != nil {
		t.Fatal(err)
	}

	for _, supportType := range []string{"HINT", "EXPLAIN"} {
		response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.studentToken, map[string]any{"type": supportType})
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"action":"`+supportType+`"`) {
			t.Fatalf("%s support=%d %s", supportType, response.Code, response.Body.String())
		}
		assertStudentPayloadHasNoPrivateFields(t, response.Body.Bytes())
		if strings.Contains(response.Body.String(), fixture.privateCanary) {
			t.Fatalf("%s support leaked private answer: %s", supportType, response.Body.String())
		}
	}

	parentResponse := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", fixture.parentToken, map[string]any{"type": "HINT"})
	if parentResponse.Code != http.StatusForbidden {
		t.Fatalf("parent used Student support endpoint: %d", parentResponse.Code)
	}
	for name, rawBody := range map[string]string{
		"unknown field": `{"type":"HINT","answer":"10"}`,
		"unknown type":  `{"type":"ANSWER"}`,
		"trailing JSON": `{"type":"HINT"}{"type":"EXPLAIN"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/support", strings.NewReader(rawBody))
		request.Header.Set("Authorization", "Bearer "+fixture.studentToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d %s", name, response.Code, response.Body.String())
		}
	}

	var answersAfter, analysesAfter, turnsAfter, failedRoundsAfter, usageCount, assistanceLevel int
	var state, responseID string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1`, fixture.sessionID).Scan(&analysesAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT current_state,socratic_fail_count,teaching_response_id,assistance_level FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&state, &failedRoundsAfter, &responseID, &assistanceLevel); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND price_catalog_id IS NOT NULL AND purpose IN('SOCRATIC_TURN','EXPLANATION')`, fixture.sessionID).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if answersAfter != answersBefore || analysesAfter != analysesBefore || turnsAfter != turnsBefore+2 || failedRoundsAfter != failedRoundsBefore || state != "EXPLAIN" || assistanceLevel != 4 || responseID != "resp-runtime-2" || usageCount != 2 {
		t.Fatalf("support side effects answers=%d/%d analyses=%d/%d turns=%d/%d rounds=%d/%d state=%s response=%s usage=%d", answersAfter, answersBefore, analysesAfter, analysesBefore, turnsAfter, turnsBefore, failedRoundsAfter, failedRoundsBefore, state, responseID, usageCount)
	}

	rows, err := pool.Query(ctx, `SELECT student_payload_json,parent_payload_json FROM tutor_events WHERE session_id=$1 AND type IN('HINT_REQUESTED','AI_TURN_COMPLETED') ORDER BY sequence`, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	events := 0
	for rows.Next() {
		var studentPayload, parentPayload []byte
		if err := rows.Scan(&studentPayload, &parentPayload); err != nil {
			t.Fatal(err)
		}
		assertStudentPayloadHasNoPrivateFields(t, studentPayload)
		if strings.Contains(string(studentPayload), fixture.privateCanary) || strings.Contains(strings.ToLower(string(studentPayload)), "reason") {
			t.Fatalf("Student event leaked private data: %s", studentPayload)
		}
		var parentEvent map[string]any
		if err := json.Unmarshal(parentPayload, &parentEvent); err != nil || parentEvent["reason"] == "" {
			t.Fatalf("Parent event lacks Tutor reason: %s err=%v", parentPayload, err)
		}
		events++
	}
	if events != 2 || rows.Err() != nil {
		t.Fatalf("support events=%d err=%v", events, rows.Err())
	}
}

func TestStudentSupportRejectsAgentActionMismatchWithoutClassroomMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','mock-tutor',now()-interval '1 day',1,0.5,2)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	provider := structuredResponseServer(t, []string{validTurnJSON("EXPLAIN", false)})
	defer provider.Close()
	recorder := usage.NewRecorder(pool)
	client, err := ai.NewOpenAIResponsesClient(provider.Client(), provider.URL, "test-key", "mock-tutor")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ai.NewCodexProvider(client.WithUsageRecorder(recorder), allowTutorOutputAuditor{})
	if err != nil {
		t.Fatal(err)
	}
	var studentUserID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
		t.Fatal(err)
	}
	var versionBefore int64
	var turnsBefore int
	if err := pool.QueryRow(ctx, `SELECT version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := classroom.NewService(pool, nil, nil, recorder).WithTeachingAgent(agent).RequestSupport(ctx, studentUserID, fixture.sessionID, classroom.SupportHint); err == nil {
		t.Fatal("agent action mismatch unexpectedly succeeded")
	}
	var versionAfter int64
	var turnsAfter, usageCount int
	if err := pool.QueryRow(ctx, `SELECT version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1 AND purpose='SOCRATIC_TURN' AND price_catalog_id IS NOT NULL`, fixture.sessionID).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if versionAfter != versionBefore || turnsAfter != turnsBefore || usageCount != 1 {
		t.Fatalf("mismatch mutated classroom version=%d/%d turns=%d/%d usage=%d", versionAfter, versionBefore, turnsAfter, turnsBefore, usageCount)
	}
}
