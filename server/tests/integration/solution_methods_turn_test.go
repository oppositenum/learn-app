package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// linearEquationTextQuestion is the released free-text question of
// MATH-LINEAR-EQUATION served by the ordinary text classroom.
const linearEquationTextQuestion = "40000000-0000-4000-8000-000000000001"

const solutionMethodsMove = "Ask the student one question that lets them choose between the methods in solution_methods, naming them by method_name. Use only those methods and do not invent another one. Do not give the answer to this question."

const plainStrategyChoiceMove = "Offer two ways to approach the question and let the student choose one."

// The Tutor's next sentence may name the methods; it never carries the table.
const methodChoiceProbe = "你想先用假设法，还是先用列方程法来想这道题？"

// acceptedAnswers reads the accepted answers of a question so the test can
// check none of them reaches the student, without writing them in the test.
func acceptedAnswers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, questionID string) []string {
	t.Helper()
	var value, reference string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(correct_answer_json->>'value',''),teacher_reference_answer FROM question_private_answers WHERE question_id=$1`, questionID).Scan(&value, &reference); err != nil {
		t.Fatal(err)
	}
	answers := []string{}
	for _, answer := range []string{value, reference} {
		if answer != "" {
			answers = append(answers, answer)
		}
	}
	if len(answers) == 0 {
		t.Fatal("question has no accepted answer to guard")
	}
	return answers
}

type solutionMethodsTurn struct {
	instructions string
	input        map[string]any
	submitBody   string
	readBody     string
}

// submitWrongAnswerAtLayer answers the question wrongly, has the analysis name
// layer, and returns the Tutor generation request with both student bodies.
func submitWrongAnswerAtLayer(t *testing.T, question layerQuestion, name string) solutionMethodsTurn {
	t.Helper()
	ctx := context.Background()
	tutorModel, reviewerModel := "methods-tutor-"+name, "methods-reviewer-"+name
	pool, fixture, router, queue := startLayerClassroom(t, ctx, question, tutorModel, reviewerModel, map[string][]queuedResponse{
		tutorModel:    {{status: http.StatusOK, output: layerAnalysisJSON(false, question.layer)}, {status: http.StatusOK, output: probeTurnJSON(question.probe)}},
		reviewerModel: {{status: http.StatusOK, output: passingReview}},
	})
	if question.answers == nil {
		question.answers = acceptedAnswers(t, ctx, pool, question.questionID)
	}
	code, body := submitLayerAnswer(router, fixture, question.wrongAnswer)
	if code != http.StatusOK || !strings.Contains(body, `"action":"PROBE"`) {
		t.Fatalf("submit=%d %s", code, body)
	}
	var stored *string
	if err := pool.QueryRow(ctx, `SELECT aa.weakness_layer FROM answer_analyses aa JOIN student_answers sa ON sa.id=aa.student_answer_id WHERE sa.session_id=$1 AND sa.question_id=$2`, fixture.sessionID, question.questionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == nil || *stored != question.layer {
		t.Fatalf("stored weakness_layer=%v want %s", stored, question.layer)
	}
	read := performJSON(router, http.MethodGet, "/api/v1/student/sessions/"+fixture.sessionID.String(), fixture.studentToken, nil)
	if read.Code != http.StatusOK {
		t.Fatalf("read=%d %s", read.Code, read.Body.String())
	}
	for _, studentBody := range []string{body, read.Body.String()} {
		assertNoAnswerOrLayerForStudent(t, question, studentBody)
		assertNoSolutionMethodsForStudent(t, studentBody)
	}
	requests := queue.requestsFor(tutorModel)
	if len(requests) != 2 {
		t.Fatalf("tutor requests=%d want analysis and one turn", len(requests))
	}
	instructions, _ := requests[1]["instructions"].(string)
	rawInput, _ := requests[1]["input"].(string)
	var input map[string]any
	if err := json.Unmarshal([]byte(rawInput), &input); err != nil {
		t.Fatal(err)
	}
	return solutionMethodsTurn{instructions: instructions, input: input, submitBody: body, readBody: read.Body.String()}
}

func assertNoSolutionMethodsForStudent(t *testing.T, body string) {
	t.Helper()
	for _, key := range []string{"solution_methods", "method_name", "first_look", "why_this_method", "method_path", "check_where", "more_direct"} {
		if strings.Contains(body, `"`+key+`"`) {
			t.Fatalf("student body carried %s", key)
		}
	}
	for _, method := range linearEquationSolutionMethods {
		for index, sentence := range method.sentences() {
			if strings.Contains(body, sentence) {
				t.Fatalf("student body carried sentence %d of %s", index+1, method.MethodName)
			}
		}
	}
}

func inputSolutionMethodNames(t *testing.T, input map[string]any) []string {
	t.Helper()
	raw, present := input["solution_methods"]
	if !present {
		return nil
	}
	methods, ok := raw.([]any)
	if !ok {
		t.Fatalf("solution_methods=%v", raw)
	}
	names := []string{}
	for _, method := range methods {
		fields, _ := method.(map[string]any)
		name, _ := fields["method_name"].(string)
		names = append(names, name)
	}
	return names
}

func TestStrategyChoiceTurnCarriesLinearEquationSolutionMethods(t *testing.T) {
	turn := submitWrongAnswerAtLayer(t, layerQuestion{
		name: "linear equation L4", questionID: linearEquationTextQuestion, wrongAnswer: "36÷3", layer: "L4", probe: methodChoiceProbe,
	}, "l4")
	if !strings.Contains(turn.instructions, "weakness_layer is L4: "+solutionMethodsMove) {
		t.Fatalf("L4 turn instructions lack the solution-methods move: %q", turn.instructions)
	}
	if strings.Contains(turn.instructions, plainStrategyChoiceMove) {
		t.Fatal("L4 turn with prepared methods still asked the model to invent two ways")
	}
	if names := inputSolutionMethodNames(t, turn.input); fmt.Sprint(names) != fmt.Sprint([]string{"假设法", "列方程法"}) {
		t.Fatalf("L4 turn input solution methods=%v", names)
	}
	methods, _ := turn.input["solution_methods"].([]any)
	for index, method := range methods {
		fields, _ := method.(map[string]any)
		want := linearEquationSolutionMethods[index]
		if fields["first_look"] != want.FirstLook || fields["why_this_method"] != want.WhyThisMethod || fields["method_path"] != want.MethodPath || fields["check_where"] != want.CheckWhere || fields["more_direct"] != want.MoreDirect {
			t.Fatalf("L4 turn input method %d does not carry its five answers", index)
		}
	}
	if !strings.Contains(turn.submitBody, methodChoiceProbe) {
		t.Fatal("the Tutor's method choice question did not reach the student")
	}
}

func TestInformationExtractionTurnOnLinearEquationCarriesNoSolutionMethods(t *testing.T) {
	turn := submitWrongAnswerAtLayer(t, layerQuestion{
		name: "linear equation L3", questionID: linearEquationTextQuestion, wrongAnswer: "36÷3", layer: "L3",
		probe: "题目里给了哪几个条件，你用到了哪几个？",
	}, "l3")
	if !strings.Contains(turn.instructions, "weakness_layer is L3: ") || strings.Contains(turn.instructions, solutionMethodsMove) {
		t.Fatalf("L3 turn instructions=%q", turn.instructions)
	}
	if names := inputSolutionMethodNames(t, turn.input); names != nil {
		t.Fatalf("L3 turn input carried solution methods %v", names)
	}
}

func TestStrategyChoiceTurnWithoutSolutionMethodsStillGenerates(t *testing.T) {
	question := layerQuestions[0]
	question.name, question.layer = question.name+" L4", "L4"
	turn := submitWrongAnswerAtLayer(t, question, "plain-l4")
	if !strings.Contains(turn.instructions, "weakness_layer is L4: "+plainStrategyChoiceMove) || strings.Contains(turn.instructions, solutionMethodsMove) {
		t.Fatalf("L4 turn without methods instructions=%q", turn.instructions)
	}
	if names := inputSolutionMethodNames(t, turn.input); names != nil {
		t.Fatalf("L4 turn without methods carried %v", names)
	}
}
