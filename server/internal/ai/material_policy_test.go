package ai

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/content"
	"github.com/oppositenum/ai-learning-tutor/server/internal/studentinteraction"
	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

func materialPolicyQuestion(t *testing.T, prompt string, scene *studentinteraction.Scene) content.QuestionPublic {
	t.Helper()
	question := content.QuestionPublic{Prompt: prompt}
	if scene == nil {
		return question
	}
	var err error
	question.Scene, err = json.Marshal(scene)
	if err != nil {
		t.Fatal(err)
	}
	question.InputSchema, _ = studentinteraction.AnswerSchema(*scene)
	return question
}

func TestTutorMaterialPolicyRejectsNewNumbersInBoundedActions(t *testing.T) {
	for _, action := range []tutor.State{tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy} {
		t.Run(string(action), func(t *testing.T) {
			err := enforceTutorMaterialPolicy(materialPolicyQuestion(t, "每盒有12支笔，共3盒。", nil), TutorTurn{Action: action, Message: "先想想每盒有10支时会怎样。"})
			if !errors.Is(err, ErrTutorMaterialPolicyViolation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestTutorMaterialPolicyAllowsOriginalNumbersAndExplanationExample(t *testing.T) {
	question := materialPolicyQuestion(t, "每盒有12支笔，共3盒。", nil)
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: "先看12和3表示什么。"}); err != nil {
		t.Fatal(err)
	}
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateExplain, Message: "用每盒10支、2盒做平行例子。"}); err != nil {
		t.Fatal(err)
	}
}

func TestTutorMaterialPolicyUsesStructuredPublicMaterialWithoutIdentifiers(t *testing.T) {
	scene := studentinteraction.Scene{
		Version: studentinteraction.Version, Renderer: studentinteraction.RendererNumberLine,
		AccessibleFallback: "在数轴上标出三。",
		NumberLine:         &studentinteraction.NumberLine{Label: "目标位置", Min: 0, Max: 5, Step: 1},
	}
	question := materialPolicyQuestion(t, "选择合适的位置。", &scene)
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: "先找到三和5的位置。"}); err != nil {
		t.Fatal(err)
	}
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: "先找到四的位置。"}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
		t.Fatalf("error=%v", err)
	}
}

func TestTutorMaterialPolicyChecksSingleChineseNumeralsAndExemptsOrdinals(t *testing.T) {
	question := materialPolicyQuestion(t, "把三个物品分组，第一步先观察。", nil)
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: "先找三个物品。"}); err != nil {
		t.Fatal(err)
	}
	if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: "先找五个物品。"}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
		t.Fatalf("error=%v", err)
	}
	if err := enforceTutorMaterialPolicy(materialPolicyQuestion(t, "第一步先观察。", nil), TutorTurn{Action: tutor.StateProbe, Message: "第二步再分类。"}); err != nil {
		t.Fatal(err)
	}
}

func TestTutorMaterialPolicyAllowsOrdinaryChineseProse(t *testing.T) {
	question := materialPolicyQuestion(t, "请根据题目继续思考。", nil)
	for _, message := range []string{
		"我们一起检查已知条件。",
		"先看这个知识点和题目的关系。",
		"想一想哪个条件还没用到。",
		"下一步先检查已知条件。",
		"再试一次，看看思路是否完整。",
		"一步一步检查推理过程。",
		"这个思路十分清楚。",
		"第2步再检查结论。",
		"先换一个角度想想。",
		"你能举一个例子吗？",
		"把这一步重复一次。",
		"万一还没有思路，先看已知条件。",
		"千万不要着急，先检查这一步。",
	} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: message}); err != nil {
			t.Fatalf("ordinary prose %q was rejected: %v", message, err)
		}
	}
}

func TestTutorMaterialPolicyStillRejectsNewChineseQuantitiesAroundOrdinaryProse(t *testing.T) {
	question := materialPolicyQuestion(t, "请根据题目继续思考。", nil)
	for _, message := range []string{
		"再拿一个苹果试试。",
		"计算万一+2的结果。",
		"把数量改成千万个。",
		"先计算三万一千个时的结果。",
	} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: message}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
			t.Fatalf("new quantity %q error=%v", message, err)
		}
	}
}

func TestTutorMaterialPolicyTreatsEquivalentChineseAndArabicNumbersAsSameMaterial(t *testing.T) {
	for _, test := range []struct {
		prompt, message string
	}{
		{"用三个容器装水。", "先找3个容器。"},
		{"Use 3 containers.", "先找三个容器。"},
		{"把一半区域涂色。", "先找到0.5的区域。"},
	} {
		if err := enforceTutorMaterialPolicy(materialPolicyQuestion(t, test.prompt, nil), TutorTurn{Action: tutor.StateHint, Message: test.message}); err != nil {
			t.Fatalf("equivalent restatement %q -> %q was rejected: %v", test.prompt, test.message, err)
		}
	}
}

func TestTutorMaterialPolicyStillRejectsChangedChineseTaskQuantities(t *testing.T) {
	question := materialPolicyQuestion(t, "这段路需要走三步，每步一米。", nil)
	for _, message := range []string{"改成走五步试试。", "每步两米时怎样？"} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: message}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
			t.Fatalf("changed task quantity %q error=%v", message, err)
		}
	}
}

func TestTutorMaterialPolicyRecognizesChineseQuantitiesAndDecimals(t *testing.T) {
	question := materialPolicyQuestion(t, "用三个容器装三点五升水。", nil)
	for _, message := range []string{"先找三个容器。", "先看三点五升表示什么。"} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: message}); err != nil {
			t.Fatalf("original Chinese quantity %q was rejected: %v", message, err)
		}
	}
	for _, message := range []string{"先找五个容器。", "先看三点六升表示什么。"} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: message}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
			t.Fatalf("new Chinese quantity %q error=%v", message, err)
		}
	}
}

func TestTutorMaterialPolicyRejectsChangedDiscountsAndFractionsInBoundedActions(t *testing.T) {
	tests := []struct {
		name     string
		question string
		message  string
	}{
		{name: "discount", question: "商品打八折。", message: "如果改成九折呢？"},
		{name: "fraction without unit", question: "三分之一是多少？", message: "三分之二是多少？"},
		{name: "fraction with unit", question: "喝三分之一杯橙汁。", message: "喝三分之二杯橙汁。"},
		{name: "ascii negative", question: "温度是-5度。", message: "温度是5度。"},
		{name: "unicode negative", question: "温度是−5度。", message: "温度是5度。"},
	}
	for _, test := range tests {
		for _, action := range []tutor.State{tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy} {
			t.Run(test.name+"/"+string(action), func(t *testing.T) {
				question := materialPolicyQuestion(t, test.question, nil)
				if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: action, Message: test.message}); !errors.Is(err, ErrTutorMaterialPolicyViolation) {
					t.Fatalf("changed material was accepted: %v", err)
				}
			})
		}
	}
}

func TestTutorMaterialPolicyAllowsUnchangedCompoundMaterialAndBoundedScope(t *testing.T) {
	tests := []struct {
		name     string
		question string
		message  string
	}{
		{name: "discount", question: "商品打八折。", message: "先想想八折代表什么。"},
		{name: "fraction without unit", question: "三分之一是多少？", message: "先看三分之一。"},
		{name: "fraction with unit", question: "喝三分之一杯橙汁。", message: "先看三分之一杯橙汁。"},
		{name: "negative", question: "温度是−5度。", message: "先看−5度。"},
	}
	for _, test := range tests {
		for _, action := range []tutor.State{tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy} {
			t.Run(test.name+"/"+string(action), func(t *testing.T) {
				question := materialPolicyQuestion(t, test.question, nil)
				if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: action, Message: test.message}); err != nil {
					t.Fatalf("unchanged material was rejected: %v", err)
				}
			})
		}
	}
}

func TestTutorMaterialPolicyPreservesTeachingProseExemptions(t *testing.T) {
	question := materialPolicyQuestion(t, "请根据题目继续思考。", nil)
	for _, phrase := range []string{
		"一个角度", "一个例子", "一个思路", "一个方法", "一个办法", "一个方式",
		"万一", "千万", "第3步",
	} {
		t.Run(phrase, func(t *testing.T) {
			if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateHint, Message: phrase}); err != nil {
				t.Fatalf("teaching prose %q was rejected: %v", phrase, err)
			}
		})
	}
}

func TestExplanationAlwaysReturnsToOriginalTask(t *testing.T) {
	turn := ensureOriginalTaskVerification(TutorTurn{Action: tutor.StateExplain, Message: "先看一个平行例子。"})
	if turn.Message != "先看一个平行例子。 现在回到原题，请你再独立试一次。" {
		t.Fatalf("message=%q", turn.Message)
	}
}
