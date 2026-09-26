package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

func analysisWithLayer(correct bool, layer string) StructuredResult {
	document := map[string]any{
		"answer_correct": correct, "reasoning_quality": "WEAK", "confidence": 0.8,
		"error_type": "X", "misconceptions": []string{}, "core_ability_signals": []any{},
		"emotion_signal": "NEUTRAL", "engagement": "NORMAL",
		"recommended_action": "PROBE", "safe_to_increase_difficulty": false,
	}
	if layer != "" {
		document["weakness_layer"] = layer
	}
	encoded, _ := json.Marshal(document)
	return StructuredResult{OutputJSON: encoded}
}

func analyzeWith(t *testing.T, results ...StructuredResult) (AnalyzeAnswerResult, *structuredClientStub, error) {
	t.Helper()
	client := &structuredClientStub{results: results}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	configureImmediateGenerationRetries(provider, &waits)
	result, err := provider.AnalyzeAnswer(context.Background(), AnalyzeAnswerRequest{Question: content.QuestionForTeaching{Teaching: teachingFixture()}})
	return result, client, err
}

func TestWrongAnswerCarriesItsWeaknessLayer(t *testing.T) {
	result, _, err := analyzeWith(t, analysisWithLayer(false, "L3"))
	if err != nil {
		t.Fatal(err)
	}
	if result.WeaknessLayer != "L3" {
		t.Fatalf("weakness_layer=%q want L3", result.WeaknessLayer)
	}
}

func TestWrongAnswerWithoutALayerIsRejectedNotDefaulted(t *testing.T) {
	for name, layer := range map[string]string{"missing": "", "none": "NONE", "unknown": "L7", "lowercase": "l1"} {
		t.Run(name, func(t *testing.T) {
			bad := analysisWithLayer(false, layer)
			result, client, err := analyzeWith(t, bad, bad, bad, bad)
			if !errors.Is(err, ErrTutorGenerationBusy) {
				t.Fatalf("a wrong answer with weakness_layer %q was accepted: %+v err=%v", layer, result, err)
			}
			if client.calls != TutorRetryMaxAttempts {
				t.Fatalf("calls=%d want %d; a rejected layer must be retried like any rejected sample", client.calls, TutorRetryMaxAttempts)
			}
			details, ok := TutorGenerationBusyFailureDetails(err)
			if !ok || details.Category != TutorReviewFailureInvalidSchema {
				t.Fatalf("details=%+v want an invalid-schema failure", details)
			}
			// A missing required field is reported at the object root by the
			// schema validator; every present-but-wrong value names the field.
			if layer != "" && !strings.Contains(details.RejectedPaths, "/weakness_layer") {
				t.Fatalf("rejected paths=%q want /weakness_layer", details.RejectedPaths)
			}
		})
	}
}

func TestCorrectAnswerCarryingALayerIsRejected(t *testing.T) {
	bad := analysisWithLayer(true, "L2")
	if _, _, err := analyzeWith(t, bad, bad, bad, bad); !errors.Is(err, ErrTutorGenerationBusy) {
		t.Fatalf("a correct analysis carrying a layer was accepted: %v", err)
	}
	if _, _, err := analyzeWith(t, analysisWithLayer(true, WeaknessLayerNone)); err != nil {
		t.Fatalf("a correct analysis with NONE was rejected: %v", err)
	}
}

func TestRejectedLayerRetryNamesTheLayerField(t *testing.T) {
	_, client, err := analyzeWith(t, analysisWithLayer(true, "L4"), analysisWithLayer(false, "L5"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.requests[1].Instructions, "/weakness_layer") {
		t.Fatalf("retry did not name the rejected layer field: %q", client.requests[1].Instructions)
	}
}

func TestAnalysisInstructionsDefineAllSixLayers(t *testing.T) {
	_, client, err := analyzeWith(t, analysisWithLayer(false, "L1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range []string{"L1 knowledge gap", "L2 concept understanding", "L3 information extraction", "L4 strategy choice", "L5 reasoning chain", "L6 metacognition"} {
		if !strings.Contains(client.requests[0].Instructions, layer) {
			t.Fatalf("analysis instructions do not define %q", layer)
		}
	}
}

func TestTurnFollowsOnlyTheDiagnosedLayerMove(t *testing.T) {
	for layer, move := range weaknessLayerMoves {
		t.Run(layer, func(t *testing.T) {
			client := &structuredClientStub{result: validGeneratedTurn(tutor.StateProbe)}
			provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
				Teaching: teachingFixture(), TutorDecision: tutor.Decision{NextState: tutor.StateProbe}, WeaknessLayer: layer,
			}); err != nil {
				t.Fatal(err)
			}
			instructions := client.request.Instructions
			if !strings.Contains(instructions, "weakness_layer is "+layer+": "+move) {
				t.Fatalf("turn instructions lack the %s move: %q", layer, instructions)
			}
			for other, otherMove := range weaknessLayerMoves {
				if other != layer && strings.Contains(instructions, otherMove) {
					t.Fatalf("turn for %s also carried the %s move", layer, other)
				}
			}
			var payload map[string]any
			if err := json.Unmarshal(client.request.Input, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["weakness_layer"] != layer {
				t.Fatalf("turn input weakness_layer=%v want %s", payload["weakness_layer"], layer)
			}
		})
	}
}

func TestTurnWithoutALayerCarriesNoLayerMove(t *testing.T) {
	client := &structuredClientStub{result: validGeneratedTurn(tutor.StateHint)}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Teaching: teachingFixture(), TutorDecision: tutor.Decision{NextState: tutor.StateHint},
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.request.Instructions, "weakness_layer") || strings.Contains(string(client.request.Input), "weakness_layer") {
		t.Fatal("a turn without a diagnosed layer still mentioned weakness_layer")
	}
}

var preparedSolutionMethods = []SolutionMethod{
	{MethodName: "假设法", FirstLook: "看一", WhyThisMethod: "为何一", MethodPath: "路径一", CheckWhere: "检查一", MoreDirect: "更直接一"},
	{MethodName: "列方程法", FirstLook: "看二", WhyThisMethod: "为何二", MethodPath: "路径二", CheckWhere: "检查二", MoreDirect: "更直接二"},
}

func generateLayerTurn(t *testing.T, layer string, methods []SolutionMethod) (string, map[string]any) {
	t.Helper()
	client := &structuredClientStub{result: validGeneratedTurn(tutor.StateProbe)}
	provider, err := NewCodexProvider(client, passingTutorOutputAuditor())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateTurn(context.Background(), GenerateTurnRequest{
		Teaching: teachingFixture(), TutorDecision: tutor.Decision{NextState: tutor.StateProbe}, WeaknessLayer: layer, SolutionMethods: methods,
	}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(client.request.Input, &payload); err != nil {
		t.Fatal(err)
	}
	return client.request.Instructions, payload
}

func TestStrategyChoiceTurnOffersThePreparedSolutionMethods(t *testing.T) {
	instructions, payload := generateLayerTurn(t, "L4", preparedSolutionMethods)
	if !strings.Contains(instructions, "weakness_layer is L4: "+weaknessLayerSolutionMethodsMove) {
		t.Fatalf("L4 turn with methods lacks the solution-methods move: %q", instructions)
	}
	if strings.Contains(instructions, weaknessLayerMoves["L4"]) {
		t.Fatal("L4 turn with methods still carried the move that invents two ways")
	}
	methods, _ := payload["solution_methods"].([]any)
	if len(methods) != len(preparedSolutionMethods) {
		t.Fatalf("turn input solution_methods=%v", payload["solution_methods"])
	}
	for index, method := range methods {
		fields, _ := method.(map[string]any)
		if fields["method_name"] != preparedSolutionMethods[index].MethodName || fields["more_direct"] != preparedSolutionMethods[index].MoreDirect {
			t.Fatalf("turn input method %d=%v", index, fields)
		}
	}
}

func TestStrategyChoiceTurnWithoutMethodsKeepsItsOwnMove(t *testing.T) {
	instructions, payload := generateLayerTurn(t, "L4", nil)
	if !strings.Contains(instructions, "weakness_layer is L4: "+weaknessLayerMoves["L4"]) || strings.Contains(instructions, weaknessLayerSolutionMethodsMove) {
		t.Fatalf("L4 turn without methods did not keep its move: %q", instructions)
	}
	if _, present := payload["solution_methods"]; present {
		t.Fatal("L4 turn without methods still sent solution_methods")
	}
}

func TestSolutionMethodsNeverReachAnotherLayer(t *testing.T) {
	for layer := range weaknessLayerMoves {
		if layer == "L4" {
			continue
		}
		instructions, payload := generateLayerTurn(t, layer, preparedSolutionMethods)
		if _, present := payload["solution_methods"]; present || strings.Contains(instructions, weaknessLayerSolutionMethodsMove) {
			t.Fatalf("%s turn carried the solution methods", layer)
		}
	}
}
