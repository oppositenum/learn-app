package ai

import (
	"errors"
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

func TestTutorMaterialPolicyRejectsNewNumbersInBoundedActions(t *testing.T) {
	for _, action := range []tutor.State{tutor.StateProbe, tutor.StateHint, tutor.StateScaffold, tutor.StateAnalogy} {
		t.Run(string(action), func(t *testing.T) {
			err := enforceTutorMaterialPolicy("每盒有12支笔，共3盒。", TutorTurn{Action: action, Message: "先想想每盒有10支时会怎样。"})
			if !errors.Is(err, ErrTutorMaterialPolicyViolation) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestTutorMaterialPolicyAllowsOriginalNumbersAndExplanationExample(t *testing.T) {
	if err := enforceTutorMaterialPolicy("每盒有12支笔，共3盒。", TutorTurn{Action: tutor.StateHint, Message: "先看12和3表示什么。"}); err != nil {
		t.Fatal(err)
	}
	if err := enforceTutorMaterialPolicy("每盒有12支笔，共3盒。", TutorTurn{Action: tutor.StateExplain, Message: "用每盒10支、2盒做平行例子。"}); err != nil {
		t.Fatal(err)
	}
}

func TestExplanationAlwaysReturnsToOriginalTask(t *testing.T) {
	turn := ensureOriginalTaskVerification(TutorTurn{Action: tutor.StateExplain, Message: "先看一个平行例子。"})
	if turn.Message != "先看一个平行例子。 现在回到原题，请你再独立试一次。" {
		t.Fatalf("message=%q", turn.Message)
	}
}
