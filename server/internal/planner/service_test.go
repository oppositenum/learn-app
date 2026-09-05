package planner

import "testing"

func TestShouldPreserveToday(t *testing.T) {
	tests := []struct {
		name              string
		openTodaySessions int
		todayStarted      bool
		want              bool
	}{
		{name: "nothing started", want: false},
		{name: "today session open", openTodaySessions: 1, want: true},
		{name: "today block already started", todayStarted: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldPreserveToday(test.openTodaySessions, test.todayStarted); got != test.want {
				t.Fatalf("shouldPreserveToday(%d,%v)=%v want %v", test.openTodaySessions, test.todayStarted, got, test.want)
			}
		})
	}
}
