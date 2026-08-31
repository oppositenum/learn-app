package curriculum

import (
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/subject"
)

func TestFiveSubjectSkeletonIsStable(t *testing.T) {
	want := []subject.Code{subject.Math, subject.Chinese, subject.English, subject.Physics, subject.Chemistry}
	if len(subject.All) != len(want) {
		t.Fatalf("expected %d subjects, got %d", len(want), len(subject.All))
	}
	for index, code := range want {
		if subject.All[index] != code {
			t.Fatalf("subject %d: expected %s, got %s", index, code, subject.All[index])
		}
	}
}
