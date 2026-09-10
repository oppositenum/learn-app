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
	} {
		if err := enforceTutorMaterialPolicy(question, TutorTurn{Action: tutor.StateProbe, Message: message}); err != nil {
			t.Fatalf("ordinary prose %q was rejected: %v", message, err)
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

func TestExplanationAlwaysReturnsToOriginalTask(t *testing.T) {
	turn := ensureOriginalTaskVerification(TutorTurn{Action: tutor.StateExplain, Message: "先看一个平行例子。"})
	if turn.Message != "先看一个平行例子。 现在回到原题，请你再独立试一次。" {
		t.Fatalf("message=%q", turn.Message)
	}
}
