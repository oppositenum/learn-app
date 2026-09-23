package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oppositenum/ai-learning-tutor/server/internal/ai"
	"github.com/oppositenum/ai-learning-tutor/server/internal/api"
	"github.com/oppositenum/ai-learning-tutor/server/internal/auth"
	"github.com/oppositenum/ai-learning-tutor/server/internal/classroom"
	"github.com/oppositenum/ai-learning-tutor/server/internal/database"
	"github.com/oppositenum/ai-learning-tutor/server/internal/parent"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutoraudit"
	"github.com/oppositenum/ai-learning-tutor/server/internal/usage"
	"github.com/oppositenum/ai-learning-tutor/server/migrations"
)

// layerQuestion is one of the child's released life questions, with a wrong
// answer the child could plausibly give and the layer that answer is stuck at.
type layerQuestion struct {
	name        string
	questionID  string
	wrongAnswer string
	layer       string
	// move is the teaching move the order table gives that layer.
	move string
	// probe is the Tutor's next sentence. It names no number the question
	// does not contain and does not give the answer.
	probe string
	// answers are the accepted answers; none may reach the student.
	answers []string
}

var layerQuestions = []layerQuestion{
	{
		name:        "remove parentheses",
		questionID:  "40000000-0000-4000-8000-000000000017",
		wrongAnswer: "3a-2",
		layer:       "L5",
		move:        "Break out only the next single step; move on to the step after only once that one is done.",
		probe:       "先只看括号外的 3，它要和括号里的哪几项分别相乘？",
		answers:     []string{"3a-6", "3a－6", "3×a-6"},
	},
	{
		name:        "leftovers",
		questionID:  "40000000-0000-4000-8000-000000000016",
		wrongAnswer: "左边的东西",
		layer:       "L1",
		move:        "Go back to the prior knowledge the question depends on, then return to the original question.",
		probe:       "纸条说要把 leftovers 放进 fridge，冰箱里一般放什么呢？",
		answers:     []string{"吃剩的食物", "剩菜", "剩饭", "剩余的食物", "剩下的饭菜"},
	},
}

func layerAnalysisJSON(correct bool, layer string) string {
	document := map[string]any{
		"answer_correct": correct, "reasoning_quality": "WEAK", "confidence": 0.95,
		"error_type": "LAYER_FIXTURE", "misconceptions": []string{}, "core_ability_signals": []any{},
		"emotion_signal": "NEUTRAL", "engagement": "NORMAL",
		"recommended_action": "PROBE", "safe_to_increase_difficulty": false,
	}
	if layer != "" {
		document["weakness_layer"] = layer
	}
	encoded, _ := json.Marshal(document)
	return string(encoded)
}

func probeTurnJSON(message string) string {
	encoded, _ := json.Marshal(map[string]any{"message": message, "action": "PROBE", "answer_revealed": false, "segments": []any{}})
	return string(encoded)
}

const passingReview = `{"result":"PASS","no_answer_leak":true,"reason_codes":["NONE"],"violations":[]}`

// startLayerClassroom points the security fixture's session at one of the
// child's questions, on its first attempt, and returns a router whose Tutor
// talks to queue through the independent output audit.
func startLayerClassroom(t *testing.T, ctx context.Context, question layerQuestion, tutorModel, reviewerModel string, queued map[string][]queuedResponse) (*pgxpool.Pool, securityFixture, http.Handler, *responseQueueServer) {
	t.Helper()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
UPDATE learning_sessions ls
SET current_question_id=q.id, subject_id=kp.subject_id, current_state='ASK', socratic_fail_count=0
FROM questions q JOIN knowledge_points kp ON kp.id=q.knowledge_point_id
WHERE ls.id=$1 AND q.id=$2 AND q.status='RELEASED'`, fixture.sessionID, question.questionID); err != nil {
		t.Fatal(err)
	}
	var current string
	if err := pool.QueryRow(ctx, `SELECT current_question_id::text FROM learning_sessions WHERE id=$1`, fixture.sessionID).Scan(&current); err != nil || current != question.questionID {
		t.Fatalf("session is not on %s: current=%s err=%v", question.name, current, err)
	}
	insertAuditPrices(t, ctx, pool, tutorModel, reviewerModel)
	queue := newScriptedResponseQueueServer(t, queued)
	parents := parent.NewRepository(pool)
	service := classroom.NewService(pool, nil, nil, usage.NewRecorder(pool)).WithTeachingAgent(configureAuditedCodexAgent(t, pool, queue, tutorModel, reviewerModel))
	router := api.NewRouter(api.Dependencies{
		Authenticate: auth.NewSessionAuthenticator(pool).Middleware,
		Parents:      parents,
		Classroom:    classroom.NewHandler(service, pool, parents),
	})
	return pool, fixture, router, queue
}

func submitLayerAnswer(router http.Handler, fixture securityFixture, answer string) (int, string) {
	response := performJSON(router, http.MethodPost, "/api/v1/student/sessions/"+fixture.sessionID.String()+"/answers", fixture.studentToken, map[string]any{"answer": answer})
	return response.Code, response.Body.String()
}

func assertNoAnswerOrLayerForStudent(t *testing.T, question layerQuestion, body string) {
	t.Helper()
	assertStudentPayloadHasNoPrivateFields(t, []byte(body))
	if strings.Contains(body, "weakness_layer") {
		t.Fatalf("%s: student body carried weakness_layer: %s", question.name, body)
	}
	for _, answer := range question.answers {
		if strings.Contains(body, answer) {
			t.Fatalf("%s: student body carried an accepted answer", question.name)
		}
	}
}

func TestWrongAnswerLayerSteersOnlyTheNextProbe(t *testing.T) {
	for index, question := range layerQuestions {
		t.Run(question.name, func(t *testing.T) {
			ctx := context.Background()
			tutorModel, reviewerModel := fmt.Sprintf("layer-tutor-%d", index), fmt.Sprintf("layer-reviewer-%d", index)
			pool, fixture, router, queue := startLayerClassroom(t, ctx, question, tutorModel, reviewerModel, map[string][]queuedResponse{
				tutorModel:    {{status: http.StatusOK, output: layerAnalysisJSON(false, question.layer)}, {status: http.StatusOK, output: probeTurnJSON(question.probe)}},
				reviewerModel: {{status: http.StatusOK, output: passingReview}},
			})

			code, body := submitLayerAnswer(router, fixture, question.wrongAnswer)
			if code != http.StatusOK || !strings.Contains(body, `"action":"PROBE"`) {
				t.Fatalf("submit=%d %s", code, body)
			}
			assertNoAnswerOrLayerForStudent(t, question, body)

			// The explanation model is told the layer and only that layer's move.
			requests := queue.requestsFor(tutorModel)
			if len(requests) != 2 {
				t.Fatalf("tutor requests=%d want analysis and one turn", len(requests))
			}
			instructions, _ := requests[1]["instructions"].(string)
			input, _ := requests[1]["input"].(string)
			if !strings.Contains(instructions, "weakness_layer is "+question.layer+": "+question.move) {
				t.Fatalf("turn instructions lack the %s move: %q", question.layer, instructions)
			}
			for _, other := range []string{"L1", "L2", "L3", "L4", "L5", "L6"} {
				if other != question.layer && strings.Contains(instructions, "weakness_layer is "+other) {
					t.Fatalf("turn instructions carried %s as well", other)
				}
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(input), &payload); err != nil || payload["weakness_layer"] != question.layer {
				t.Fatalf("turn input weakness_layer=%v err=%v", payload["weakness_layer"], err)
			}
			analysisInstructions, _ := requests[0]["instructions"].(string)
			for _, definition := range []string{"L1 knowledge gap", "L2 concept understanding", "L3 information extraction", "L4 strategy choice", "L5 reasoning chain", "L6 metacognition"} {
				if !strings.Contains(analysisInstructions, definition) {
					t.Fatalf("analysis instructions do not define %q", definition)
				}
			}

			var stored *string
			if err := pool.QueryRow(ctx, `SELECT aa.weakness_layer FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1 AND sa.question_id=$2`, fixture.sessionID, question.questionID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored == nil || *stored != question.layer {
				t.Fatalf("stored weakness_layer=%v want %s", stored, question.layer)
			}

			// Student events never carry the layer; the parent analysis event does.
			rows, err := pool.Query(ctx, `SELECT type,student_payload_json::text,parent_payload_json::text FROM tutor_events WHERE session_id=$1 ORDER BY sequence`, fixture.sessionID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			parentLayer := ""
			for rows.Next() {
				var eventType, studentPayload, parentPayload string
				if err := rows.Scan(&eventType, &studentPayload, &parentPayload); err != nil {
					t.Fatal(err)
				}
				assertNoAnswerOrLayerForStudent(t, question, studentPayload)
				if eventType == "ANSWER_ANALYZED" {
					var parentEvent map[string]any
					if err := json.Unmarshal([]byte(parentPayload), &parentEvent); err != nil {
						t.Fatal(err)
					}
					parentLayer, _ = parentEvent["weakness_layer"].(string)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if parentLayer != question.layer {
				t.Fatalf("parent analysis event weakness_layer=%q want %s", parentLayer, question.layer)
			}

			// The parent live view shows the layer, and nothing the Tutor was
			// told about how to teach it.
			parentResponse := performParentSessionRequest(router, fixture.parentToken, fixture.studentID, fixture.sessionID)
			if parentResponse.Code != http.StatusOK {
				t.Fatalf("parent live=%d %s", parentResponse.Code, parentResponse.Body.String())
			}
			var live map[string]any
			if err := json.Unmarshal(parentResponse.Body.Bytes(), &live); err != nil {
				t.Fatal(err)
			}
			if live["weakness_layer"] != question.layer {
				t.Fatalf("parent live weakness_layer=%v want %s", live["weakness_layer"], question.layer)
			}
			if strings.Contains(parentResponse.Body.String(), "weakness_layer is") {
				t.Fatal("parent live view carried the Tutor's layer instruction")
			}
		})
	}
}

func TestCorrectAnalysisCarryingALayerFailsClosed(t *testing.T) {
	for index, question := range layerQuestions {
		t.Run(question.name, func(t *testing.T) {
			ctx := context.Background()
			tutorModel, reviewerModel := fmt.Sprintf("layer-correct-tutor-%d", index), fmt.Sprintf("layer-correct-reviewer-%d", index)
			bad := queuedResponse{status: http.StatusOK, output: layerAnalysisJSON(true, "L2")}
			pool, fixture, router, queue := startLayerClassroom(t, ctx, question, tutorModel, reviewerModel, map[string][]queuedResponse{
				tutorModel: {bad, bad, bad},
			})
			before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)
			code, body := submitLayerAnswer(router, fixture, question.wrongAnswer)
			assertLayerRejectedLikeAnyBusyAnalysis(t, code, body)
			if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
				t.Fatalf("rejected analysis mutated the classroom:\nbefore=%+v\nafter=%+v", before, after)
			}
			if queue.callCount(tutorModel) != ai.TutorRetryMaxAttempts || queue.callCount(reviewerModel) != 0 {
				t.Fatalf("calls tutor=%d reviewer=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel))
			}
		})
	}
}

// TestWrongAnalysisWithoutAValidLayerFailsClosed covers the value the schema
// alone lets through: NONE is a legal enum member, so only the layer check
// stops a wrong answer from arriving without a layer.
func TestWrongAnalysisWithoutAValidLayerFailsClosed(t *testing.T) {
	for index, question := range layerQuestions {
		t.Run(question.name, func(t *testing.T) {
			ctx := context.Background()
			tutorModel, reviewerModel := fmt.Sprintf("layer-missing-tutor-%d", index), fmt.Sprintf("layer-missing-reviewer-%d", index)
			// The first analysis is the one under test. The later entries are a
			// valid turn, so if the layer check were gone the classroom would
			// visibly carry on instead of failing for an unrelated reason; with
			// the check in place they are rejected as analyses and the retry
			// budget runs out.
			bad := queuedResponse{status: http.StatusOK, output: layerAnalysisJSON(false, ai.WeaknessLayerNone)}
			turn := queuedResponse{status: http.StatusOK, output: probeTurnJSON(question.probe)}
			pool, fixture, router, queue := startLayerClassroom(t, ctx, question, tutorModel, reviewerModel, map[string][]queuedResponse{
				tutorModel:    {bad, turn, turn},
				reviewerModel: {{status: http.StatusOK, output: passingReview}},
			})
			before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)
			code, body := submitLayerAnswer(router, fixture, question.wrongAnswer)
			assertLayerRejectedLikeAnyBusyAnalysis(t, code, body)
			if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
				t.Fatalf("rejected analysis mutated the classroom:\nbefore=%+v\nafter=%+v", before, after)
			}
			var defaulted int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1 AND aa.weakness_layer IS NOT NULL`, fixture.sessionID).Scan(&defaulted); err != nil || defaulted != 0 {
				t.Fatalf("a layer was filled in for a rejected analysis: rows=%d err=%v", defaulted, err)
			}
			if queue.callCount(tutorModel) != ai.TutorRetryMaxAttempts || queue.callCount(reviewerModel) != 0 {
				t.Fatalf("calls tutor=%d reviewer=%d", queue.callCount(tutorModel), queue.callCount(reviewerModel))
			}
			retry, _ := queue.requestsFor(tutorModel)[1]["instructions"].(string)
			if !strings.Contains(retry, "rejected by local schema validation at /weakness_layer") {
				t.Fatalf("the first analysis was not rejected for its layer: %q", retry)
			}
		})
	}
}

func assertLayerRejectedLikeAnyBusyAnalysis(t *testing.T, code int, body string) {
	t.Helper()
	if code != http.StatusServiceUnavailable {
		t.Fatalf("analysis with an invalid layer was accepted: %d %s", code, body)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(body), &payload); err != nil || len(payload) != 1 || payload["code"] != ai.TutorGenerationBusyCode {
		t.Fatalf("busy payload=%q err=%v", body, err)
	}
}

// TestLeftoversLayeredTurnStillPassesTheAnswerAudit keeps the layer from
// becoming a way around the independent review: a layered turn that gives the
// meaning away is still blocked as DIRECT_ANSWER.
func TestLeftoversLayeredTurnStillPassesTheAnswerAudit(t *testing.T) {
	ctx := context.Background()
	question := layerQuestions[1]
	tutorModel, reviewerModel := "layer-audit-tutor", "layer-audit-reviewer"
	// A paraphrase rather than an accepted answer string, so the turn gets
	// past the deterministic check and reaches the independent reviewer.
	leak := "leftovers 就是上一顿没吃完、留着下一顿再吃的那些。"
	pool, fixture, router, queue := startLayerClassroom(t, ctx, question, tutorModel, reviewerModel, map[string][]queuedResponse{
		tutorModel:    {{status: http.StatusOK, output: layerAnalysisJSON(false, question.layer)}, {status: http.StatusOK, output: probeTurnJSON(leak)}},
		reviewerModel: {{status: http.StatusOK, output: `{"result":"REJECT","no_answer_leak":false,"reason_codes":["DIRECT_ANSWER"],"violations":[{"violation_type":"DIRECT_ANSWER","payload_kind":"MESSAGE","segment_index":-1}]}`}},
	})
	before := readProtectedClassroomSnapshot(t, ctx, pool, fixture)
	code, body := submitLayerAnswer(router, fixture, question.wrongAnswer)
	if code == http.StatusOK {
		t.Fatalf("a layered turn that gave the answer away reached the student: %s", body)
	}
	assertNoAnswerOrLayerForStudent(t, question, body)
	if after := readProtectedClassroomSnapshot(t, ctx, pool, fixture); after != before {
		t.Fatalf("audit rejection mutated the classroom:\nbefore=%+v\nafter=%+v", before, after)
	}
	requests := queue.requestsFor(tutorModel)
	if len(requests) != 2 {
		t.Fatalf("tutor requests=%d", len(requests))
	}
	if instructions, _ := requests[1]["instructions"].(string); !strings.Contains(instructions, "weakness_layer is "+question.layer+": ") {
		t.Fatalf("the audited turn was not the layered one: %q", instructions)
	}
	if queue.callCount(reviewerModel) != 1 {
		t.Fatalf("reviewer calls=%d want 1", queue.callCount(reviewerModel))
	}
	var violationsJSON string
	if err := pool.QueryRow(ctx, `SELECT violations_json::text FROM tutor_output_audits WHERE session_id=$1 AND reviewer_result='REJECT' AND final_result='REJECT'`, fixture.sessionID).Scan(&violationsJSON); err != nil {
		t.Fatal(err)
	}
	assertMinimalViolationJSON(t, violationsJSON, []tutoraudit.Violation{{ViolationType: "DIRECT_ANSWER", PayloadKind: "MESSAGE", SegmentIndex: -1}}, question.answers[0], leak)
}

func TestAnswerAnalysisWeaknessLayerColumnAllowsOnlyL1ToL6OnWrongAnswers(t *testing.T) {
	ctx := context.Background()
	pool := isolatedPool(t, ctx, testDatabaseURL(t))
	if err := database.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatal(err)
	}
	fixture := seedSecurityFixture(t, ctx, pool)
	// The fixture writes its analysis without naming the column, the way rows
	// written before the migration were; it stays NULL rather than defaulted.
	var existing *string
	if err := pool.QueryRow(ctx, `SELECT aa.weakness_layer FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1`, fixture.sessionID).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != nil {
		t.Fatalf("an analysis written without a layer got %q", *existing)
	}
	insert := func(correct bool, layer any) error {
		answerID := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO student_answers (id,session_id,question_id,answer_text) VALUES ($1,$2,$3,'x')`, answerID, fixture.sessionID, fixture.releasedQuestionID); err != nil {
			t.Fatal(err)
		}
		_, err := pool.Exec(ctx, `INSERT INTO answer_analyses (id,student_answer_id,answer_correct,reasoning_quality,confidence,error_type,emotion_signal,engagement,recommended_action,weakness_layer) VALUES ($1,$2,$3,'WEAK',0.9,'X','NEUTRAL','NORMAL','PROBE',$4)`, uuid.New(), answerID, correct, layer)
		return err
	}
	for _, layer := range []string{"L1", "L2", "L3", "L4", "L5", "L6"} {
		if err := insert(false, layer); err != nil {
			t.Fatalf("wrong answer with %s refused: %v", layer, err)
		}
	}
	if err := insert(false, nil); err != nil {
		t.Fatalf("NULL layer refused: %v", err)
	}
	for _, layer := range []string{"NONE", "L0", "L7", "l1", ""} {
		if err := insert(false, layer); err == nil {
			t.Fatalf("layer %q was stored", layer)
		}
	}
	if err := insert(true, "L2"); err == nil {
		t.Fatal("a correct answer was stored with a layer")
	}
	if err := insert(true, nil); err != nil {
		t.Fatalf("a correct answer without a layer refused: %v", err)
	}
}
