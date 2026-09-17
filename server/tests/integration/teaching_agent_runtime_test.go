package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

type allowTutorOutputAuditor struct{}

func (allowTutorOutputAuditor) AuditTutorOutput(context.Context, ai.TutorOutputAuditRequest) error {
	return nil
}

type rejectTutorOutputAuditor struct{}

func (rejectTutorOutputAuditor) AuditTutorOutput(context.Context, ai.TutorOutputAuditRequest) error {
	return errors.New("Tutor output rejected")
}

func TestClassroomMetersRealResponsesClientBeforeStateMutation(t *testing.T) {
	tests := []struct {
		name              string
		outputs           []string
		wantUsage         int
		wantPurposeCounts map[string]int
		auditor           ai.TutorOutputAuditor
	}{
		{
			// Output the provider returns but this service rejects is retried
			// within the generation budget, so a provider that keeps answering
			// malformed JSON is metered once per accounted attempt. The
			// invariant under test is unchanged: every attempt is metered, and
			// none of them mutates classroom state.
			name: "malformed analysis",
			outputs: []string{
				`{"answer_correct":false}`,
				`{"answer_correct":false}`,
				`{"answer_correct":false}`,
			},
			wantUsage:         ai.TutorRetryMaxAttempts,
			wantPurposeCounts: map[string]int{"ANSWER_ANALYSIS": ai.TutorRetryMaxAttempts},
		},
		{
			name:      "agent action mismatch",
			outputs:   []string{validAnalysisJSON(), validTurnJSON("HINT", false)},
			wantUsage: 2, wantPurposeCounts: map[string]int{"ANSWER_ANALYSIS": 1, "SOCRATIC_TURN": 1},
		},
		{
			name:      "independent output audit rejection",
			outputs:   []string{validAnalysisJSON(), validTurnJSON("PROBE", false)},
			wantUsage: 2, wantPurposeCounts: map[string]int{"ANSWER_ANALYSIS": 1, "SOCRATIC_TURN": 1},
			auditor: rejectTutorOutputAuditor{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := isolatedPool(t, ctx, testDatabaseURL(t))
			if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
				t.Fatal(err)
			}
			fixture := seedSecurityFixture(t, ctx, pool)
			if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET socratic_fail_count=0,current_state='ASK',version=7 WHERE id=$1`, fixture.sessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','mock-tutor',now()-interval '1 day',1,0.5,2)`, uuid.New()); err != nil {
				t.Fatal(err)
			}
			server := structuredResponseServer(t, test.outputs)
			defer server.Close()
			recorder := usage.NewRecorder(pool)
			client, err := ai.NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "mock-tutor")
			if err != nil {
				t.Fatal(err)
			}
			auditor := test.auditor
			if auditor == nil {
				auditor = allowTutorOutputAuditor{}
			}
			agent, err := ai.NewCodexProvider(client.WithUsageRecorder(recorder), auditor)
			if err != nil {
				t.Fatal(err)
			}
			service := classroom.NewService(pool, nil, nil, recorder).WithTeachingAgent(agent)
			var studentUserID uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT user_id FROM students WHERE id=$1`, fixture.studentID).Scan(&studentUserID); err != nil {
				t.Fatal(err)
			}
			var answersBefore, turnsBefore int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersBefore); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsBefore); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Submit(ctx, studentUserID, fixture.sessionID, "wrong runtime answer"); err == nil {
				t.Fatal("unsafe provider output unexpectedly mutated classroom")
			}

			var version int64
			var state string
			var answersAfter, turnsAfter int
			if err := pool.QueryRow(ctx, `SELECT version,current_state FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&version, &state); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM student_answers WHERE session_id=$1`, fixture.sessionID).Scan(&answersAfter); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM tutor_turns WHERE session_id=$1`, fixture.sessionID).Scan(&turnsAfter); err != nil {
				t.Fatal(err)
			}
			if version != 7 || state != "ASK" || answersAfter != answersBefore || turnsAfter != turnsBefore {
				t.Fatalf("classroom mutated: version=%d state=%s answers=%d/%d turns=%d/%d", version, state, answersAfter, answersBefore, turnsAfter, turnsBefore)
			}

			rows, err := pool.Query(ctx, `SELECT purpose,student_id,session_id,model,price_catalog_id IS NOT NULL FROM ai_usage_records ORDER BY created_at`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			purposeCounts := map[string]int{}
			usageCount := 0
			for rows.Next() {
				var purpose, model string
				var studentID, sessionID uuid.UUID
				var priced bool
				if err := rows.Scan(&purpose, &studentID, &sessionID, &model, &priced); err != nil {
					t.Fatal(err)
				}
				if studentID != fixture.studentID || sessionID != fixture.sessionID || model != "mock-tutor" || !priced {
					t.Fatalf("usage attribution invalid: student=%s session=%s model=%s priced=%v", studentID, sessionID, model, priced)
				}
				purposeCounts[purpose]++
				usageCount++
			}
			if usageCount != test.wantUsage || fmt.Sprint(purposeCounts) != fmt.Sprint(test.wantPurposeCounts) {
				t.Fatalf("usage=%d purposes=%v want=%d/%v", usageCount, purposeCounts, test.wantUsage, test.wantPurposeCounts)
			}
		})
	}
}

func TestResponsesPriceGuardBlocksProviderRequestBeforeClassroomMutation(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		providerCalls++
	}))
	defer server.Close()
	recorder := usage.NewRecorder(pool)
	client, err := ai.NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "unpriced-tutor")
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
	if err := pool.QueryRow(ctx, `SELECT version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := classroom.NewService(pool, nil, nil, recorder).WithTeachingAgent(agent).Submit(ctx, studentUserID, fixture.sessionID, "wrong"); err == nil {
		t.Fatal("unpriced Responses request unexpectedly succeeded")
	}
	var versionAfter int64
	var usageRows int
	if err := pool.QueryRow(ctx, `SELECT version FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&versionAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records`).Scan(&usageRows); err != nil {
		t.Fatal(err)
	}
	if providerCalls != 0 || versionAfter != versionBefore || usageRows != 0 {
		t.Fatalf("unpriced Responses calls=%d version=%d/%d usage=%d", providerCalls, versionAfter, versionBefore, usageRows)
	}
}

func TestClassroomPersistsEmotionDeescalationWithoutConsumingSocraticRound(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `UPDATE learning_sessions SET socratic_fail_count=0,current_state='ASK',engagement_state='NORMAL' WHERE id=$1`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_price_catalog(id,provider,model,effective_from,input_price_per_million_usd,cached_input_price_per_million_usd,output_price_per_million_usd) VALUES($1,'openai','mock-tutor',now()-interval '1 day',1,0.5,2)`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	analysis := `{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.96,"error_type":"FRUSTRATED_GUESS","misconceptions":["FIXED_COST_IGNORED"],"core_ability_signals":[],"emotion_signal":"BORED","engagement":"LOW","recommended_action":"BREAK","safe_to_increase_difficulty":false}`
	server := structuredResponseServer(t, []string{analysis, validTurnJSON("BREAK", false)})
	defer server.Close()
	recorder := usage.NewRecorder(pool)
	client, err := ai.NewOpenAIResponsesClient(server.Client(), server.URL, "test-key", "mock-tutor")
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
	result, err := classroom.NewService(pool, nil, nil, recorder).WithTeachingAgent(agent).Submit(ctx, studentUserID, fixture.sessionID, "我烦了，不想算")
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "BREAK" || result.SocraticRound != 0 {
		t.Fatalf("deescalation result=%+v", result)
	}
	var state, engagement, emotion string
	var failedRounds, usageCount int
	if err := pool.QueryRow(ctx, `SELECT current_state,engagement_state,socratic_fail_count FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&state, &engagement, &failedRounds); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT emotion_signal FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1 ORDER BY aa.created_at DESC LIMIT 1`, fixture.sessionID).Scan(&emotion); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_usage_records WHERE session_id=$1`, fixture.sessionID).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if state != "BREAK" || engagement != "LOW" || failedRounds != 0 || emotion != "BORED" || usageCount != 2 {
		t.Fatalf("state=%s engagement=%s rounds=%d emotion=%s usage=%d", state, engagement, failedRounds, emotion, usageCount)
	}
}

func structuredResponseServer(t *testing.T, outputs []string) *httptest.Server {
	t.Helper()
	index := 0
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/responses" {
			t.Errorf("unexpected provider request: %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		if index >= len(outputs) {
			t.Errorf("unexpected extra provider request")
			http.Error(writer, "extra request", http.StatusInternalServerError)
			return
		}
		output := outputs[index]
		output = strings.ReplaceAll(output, `\"`, `"`)
		index++
		response := map[string]any{
			"id":    fmt.Sprintf("resp-runtime-%d", index),
			"model": "mock-tutor",
			"output": []any{
				map[string]any{
					"type": "message",
					"content": []any{
						map[string]any{"type": "output_text", "text": output},
					},
				},
			},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 25, "input_tokens_details": map[string]any{"cached_tokens": 20}},
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}))
}

func validAnalysisJSON() string {
	return `{"answer_correct":false,"reasoning_quality":"WEAK","confidence":0.98,"error_type":"FIXED_COST_IGNORED","misconceptions":["FIXED_COST_IGNORED"],"core_ability_signals":[],"emotion_signal":"NEUTRAL","engagement":"NORMAL","recommended_action":"PROBE","safe_to_increase_difficulty":false}`
}

func validTurnJSON(action string, answerRevealed bool) string {
	value, _ := json.Marshal(map[string]any{"message": "先找出固定费用。", "action": action, "answer_revealed": answerRevealed, "segments": []any{}})
	return string(value)
}
