package classroom

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
)

func TestStageScorerIsDeterministicAndSubjectAgnostic(t *testing.T) {
	for _, code := range subject.All {
		task := scorerTestTask(t, code, studentinteraction.Scene{
			Version: studentinteraction.Version, Renderer: studentinteraction.RendererMultiChoice,
			AccessibleFallback: "请选择所有符合要求的项目。",
			Options:            []studentinteraction.Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}, {ID: "c", Label: "丙"}},
		}, stageScoringExactOptionSetV1, `{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a","c"],"allowed_option_ids":["a","b","c"]}`)
		if got := scoreStageTask(task, json.RawMessage(`{"selected_option_ids":["c","a"]}`)); got != StageScoreCorrect {
			t.Fatalf("subject=%s score=%s", code, got)
		}
		if got := scoreStageTask(task, json.RawMessage(`{"selected_option_ids":["b"]}`)); got != StageScoreIncorrect {
			t.Fatalf("subject=%s incorrect score=%s", code, got)
		}
	}
}

func TestStageScorerSupportsAllStudentInteractionV1Renderers(t *testing.T) {
	items := []studentinteraction.Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}}
	tests := []struct {
		name, version, rule, correct, incorrect string
		scene                                   studentinteraction.Scene
	}{
		{name: "single choice", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererSingleChoice, AccessibleFallback: "选择一项。", Options: items}, version: stageScoringExactOptionSetV1, rule: `{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a"],"allowed_option_ids":["a","b"]}`, correct: `{"selected_option_ids":["a"]}`, incorrect: `{"selected_option_ids":["b"]}`},
		{name: "multiple choice", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererMultiChoice, AccessibleFallback: "选择多项。", Options: items}, version: stageScoringExactOptionSetV1, rule: `{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a","b"],"allowed_option_ids":["a","b"]}`, correct: `{"selected_option_ids":["b","a"]}`, incorrect: `{"selected_option_ids":["a"]}`},
		{name: "ordering", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererOrdering, AccessibleFallback: "调整顺序。", Items: items}, version: stageScoringExactOrderV1, rule: `{"rule_type":"EXACT_ORDER","expected_item_ids":["b","a"],"allowed_item_ids":["a","b"]}`, correct: `{"ordered_item_ids":["b","a"]}`, incorrect: `{"ordered_item_ids":["a","b"]}`},
		{name: "matching", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererMatching, AccessibleFallback: "逐项配对。", Left: items, Right: []studentinteraction.Item{{ID: "x", Label: "一"}, {ID: "y", Label: "二"}}}, version: stageScoringExactPairsV1, rule: `{"rule_type":"EXACT_PAIRS","expected_pairs":[{"left_id":"a","right_id":"y"},{"left_id":"b","right_id":"x"}],"allowed_left_ids":["a","b"],"allowed_right_ids":["x","y"]}`, correct: `{"pairs":[{"left_id":"b","right_id":"x"},{"left_id":"a","right_id":"y"}]}`, incorrect: `{"pairs":[{"left_id":"a","right_id":"x"},{"left_id":"b","right_id":"y"}]}`},
		{name: "grouping", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererGrouping, AccessibleFallback: "逐项归类。", Items: items, Groups: []studentinteraction.Item{{ID: "x", Label: "第一组"}, {ID: "y", Label: "第二组"}}}, version: stageScoringExactGroupingV1, rule: `{"rule_type":"EXACT_GROUPING","expected_placements":[{"item_id":"a","group_id":"x"},{"item_id":"b","group_id":"y"}],"allowed_item_ids":["a","b"],"allowed_group_ids":["x","y"]}`, correct: `{"placements":[{"item_id":"b","group_id":"y"},{"item_id":"a","group_id":"x"}]}`, incorrect: `{"placements":[{"item_id":"a","group_id":"y"},{"item_id":"b","group_id":"x"}]}`},
		{name: "number line", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererNumberLine, AccessibleFallback: "输入数轴位置。", NumberLine: &studentinteraction.NumberLine{Label: "位置", Min: -1, Max: 1, Step: 0.5}}, version: stageScoringExactNumberV1, rule: `{"rule_type":"EXACT_NUMBER","expected_value":0.5,"min":-1,"max":1,"step":0.5}`, correct: `{"value":0.5}`, incorrect: `{"value":-0.5}`},
		{name: "fill blanks", scene: studentinteraction.Scene{Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks, AccessibleFallback: "填写两个空格。", Slots: items}, version: stageScoringExactFillV1, rule: `{"rule_type":"EXACT_FILL","expected_values":[{"slot_id":"a","accepted_values":["北京","北京市"]},{"slot_id":"b","accepted_values":["秋天"]}],"allowed_slot_ids":["a","b"]}`, correct: `{"values":[{"slot_id":"b","value":"秋 天"},{"slot_id":"a","value":"北京市"}]}`, incorrect: `{"values":[{"slot_id":"a","value":"上海"},{"slot_id":"b","value":"秋天"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := scorerTestTask(t, subject.Math, test.scene, test.version, test.rule)
			if got := scoreStageTask(task, json.RawMessage(test.correct)); got != StageScoreCorrect {
				t.Fatalf("correct score=%s", got)
			}
			if got := scoreStageTask(task, json.RawMessage(test.incorrect)); got != StageScoreIncorrect {
				t.Fatalf("incorrect score=%s", got)
			}
			if got := scoreStageTask(task, json.RawMessage(`{"unexpected":true}`)); got != StageScoreIndeterminate {
				t.Fatalf("invalid score=%s", got)
			}
		})
	}
}

func TestStageScorerFailsClosed(t *testing.T) {
	task := scorerTestTask(t, subject.Math, studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererSingleChoice,
		AccessibleFallback: "请选择一项。", Options: []studentinteraction.Item{{ID: "a", Label: "甲"}, {ID: "b", Label: "乙"}},
	}, stageScoringExactOptionSetV1, `{"rule_type":"EXACT_OPTION_SET","expected_option_ids":["a"],"allowed_option_ids":["a","b"]}`)
	for _, response := range []json.RawMessage{
		json.RawMessage(`{"selected_option_ids":`),
		json.RawMessage(`{"selected_option_ids":["unknown"]}`),
		json.RawMessage(`{"selected_option_ids":["a","a"]}`),
		json.RawMessage(`{"selected_option_ids":["a"],"model_correct":true}`),
	} {
		if got := scoreStageTask(task, response); got != StageScoreIndeterminate {
			t.Fatalf("response=%s score=%s", response, got)
		}
	}
	invalidScene := task
	invalidScene.Scene = json.RawMessage(`{"version":"student-interaction-v2","renderer":"SINGLE_CHOICE","accessible_fallback":"请选择一项。","options":[{"id":"a","label":"甲"},{"id":"b","label":"乙"}]}`)
	if got := scoreStageTask(invalidScene, json.RawMessage(`{"selected_option_ids":["a"]}`)); got != StageScoreIndeterminate {
		t.Fatalf("unknown material version score=%s", got)
	}
}

func TestStageFillScorerEnforcesPublishedRuneLimit(t *testing.T) {
	accepted := strings.Repeat("甲", 200)
	task := scorerTestTask(t, subject.Chinese, studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks,
		AccessibleFallback: "填写内容。", Slots: []studentinteraction.Item{{ID: "slot", Label: "内容"}},
	}, stageScoringExactFillV1, fmt.Sprintf(
		`{"rule_type":"EXACT_FILL","expected_values":[{"slot_id":"slot","accepted_values":[%q]}],"allowed_slot_ids":["slot"]}`,
		accepted,
	))
	response := func(value string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"values":[{"slot_id":"slot","value":%q}]}`, value))
	}
	if got := scoreStageTask(task, response(accepted)); got != StageScoreCorrect {
		t.Fatalf("200-rune response score=%s", got)
	}
	if got := scoreStageTask(task, response(accepted+"乙")); got != StageScoreIndeterminate {
		t.Fatalf("201-rune response score=%s", got)
	}
}

func TestStageFillSafetyClassificationScansOnlyChildAuthoredValues(t *testing.T) {
	task := scorerTestTask(t, subject.Chinese, studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererFillBlanks,
		AccessibleFallback: "填写内容。", Slots: []studentinteraction.Item{{ID: "slot", Label: "内容"}},
	}, stageScoringExactFillV1, `{"rule_type":"EXACT_FILL","expected_values":[{"slot_id":"slot","accepted_values":["普通内容"]}],"allowed_slot_ids":["slot"]}`)
	if classification := classifyStageResponse(task, json.RawMessage(`{"values":[{"slot_id":"unknown","value":"我不想活了"}]}`)); !classification.Matched {
		t.Fatal("child-authored value bypassed safety classification because its slot was invalid")
	}
	if classification := classifyStageResponse(task, json.RawMessage(`{"values":[{"slot_id":"我不想活了","value":"普通内容"}]}`)); classification.Matched {
		t.Fatal("slot identifier was incorrectly classified as child-authored text")
	}
}

func scorerTestTask(t *testing.T, code subject.Code, scene studentinteraction.Scene, ruleVersion, rule string) stageTask {
	t.Helper()
	rawScene, err := json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	rawSchema, ok := studentinteraction.AnswerSchema(scene)
	if !ok {
		t.Fatal("scene did not produce an answer schema")
	}
	return stageTask{
		SubjectCode: code, Stage: StageAbstract, Scene: rawScene, InputSchema: rawSchema,
		ScoringRuleVersion: ruleVersion, ScoringRule: json.RawMessage(rule),
	}
}
