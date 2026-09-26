package parent

import (
	"testing"

	"github.com/google/uuid"
)

func TestBlockProgressFollowsStoredStatusAndTheOpenClassroom(t *testing.T) {
	open := uuid.New()
	other := uuid.New()
	paused := "PAUSED"
	abandoned := "ABANDONED"
	active := &OverviewSessionDTO{SessionID: open, Status: "ACTIVE"}
	quiet := &OverviewSessionDTO{SessionID: open, Status: "PAUSED"}
	cases := []struct {
		name          string
		block         OverviewBlockDTO
		sessionStatus *string
		session       *OverviewSessionDTO
		want          string
	}{
		{"untouched block", OverviewBlockDTO{Status: "AVAILABLE"}, nil, nil, ProgressNotStarted},
		{"abandoned attempt starts over", OverviewBlockDTO{Status: "AVAILABLE", SessionID: &other}, &abandoned, nil, ProgressNotStarted},
		{"completed block", OverviewBlockDTO{Status: "COMPLETED", SessionID: &open}, nil, active, ProgressCompleted},
		{"block of the working classroom", OverviewBlockDTO{Status: "ACTIVE", SessionID: &open}, nil, active, ProgressInProgress},
		{"block of the paused classroom", OverviewBlockDTO{Status: "AVAILABLE", SessionID: &open}, &paused, quiet, ProgressPaused},
		{"block of a classroom gone quiet", OverviewBlockDTO{Status: "ACTIVE", SessionID: &open}, nil, quiet, ProgressPaused},
		{"active block without an open classroom", OverviewBlockDTO{Status: "ACTIVE"}, nil, nil, ProgressInProgress},
		{"paused classroom not yet the open one", OverviewBlockDTO{Status: "AVAILABLE", SessionID: &other}, &paused, active, ProgressPaused},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := blockProgress(testCase.block, testCase.sessionStatus, testCase.session); got != testCase.want {
				t.Fatalf("progress=%s want %s", got, testCase.want)
			}
		})
	}
}
