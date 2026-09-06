package classroom

import (
	"testing"

	"github.com/oppositenum/ai-learning-tutor/server/internal/tutor"
)

func TestB2AssistanceLevelsKeepBreakNonCognitive(t *testing.T) {
	tests := []struct {
		state tutor.State
		want  int
	}{
		{state: tutor.StateBreak, want: 0},
		{state: tutor.StateProbe, want: 1},
		{state: tutor.StateHint, want: 1},
		{state: tutor.StateScaffold, want: 2},
		{state: tutor.StateAnalogy, want: 3},
		{state: tutor.StateBacktrack, want: 3},
		{state: tutor.StateExplain, want: 4},
		{state: tutor.StateVoiceExplain, want: 4},
	}
	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			if got := assistanceForState(test.state); got != test.want {
				t.Fatalf("assistanceForState(%s)=%d want=%d", test.state, got, test.want)
			}
		})
	}
}
