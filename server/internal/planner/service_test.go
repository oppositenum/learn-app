package planner

import (
	"os"
	"strings"
	"testing"
)

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

func TestSessionLocksTodayCardOnlyActiveOrPaused(t *testing.T) {
	paused, active, abandoned, completed := "PAUSED", "ACTIVE", "ABANDONED", "COMPLETED"
	tests := []struct {
		name   string
		status *string
		want   bool
	}{
		{name: "no classroom"},
		{name: "paused classroom", status: &paused, want: true},
		{name: "active classroom", status: &active, want: true},
		{name: "abandoned classroom", status: &abandoned},
		{name: "completed classroom", status: &completed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionLocksTodayCard(test.status); got != test.want {
				t.Fatalf("sessionLocksTodayCard(%v)=%v want %v", statusValue(test.status), got, test.want)
			}
		})
	}
}

func TestRefreshUnusedBlocksSQLKeepsAvailableGuard(t *testing.T) {
	source, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "WHERE id=$1 AND status='AVAILABLE'") {
		t.Fatal("card swap UPDATE dropped the AVAILABLE status guard")
	}
}

func statusValue(status *string) string {
	if status == nil {
		return "<nil>"
	}
	return *status
}
