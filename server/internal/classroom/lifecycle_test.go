package classroom

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSessionTimingSeparatesCurrentRunFromAccumulatedTime(t *testing.T) {
	started := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)
	resumed := started.Add(2 * time.Hour)
	now := resumed.Add(15 * time.Second)
	row := lifecycleRow{
		sessionID:          uuid.New(),
		status:             "ACTIVE",
		startedAt:          started,
		accumulatedSeconds: 20,
		lastResumedAt:      &resumed,
	}
	timing := timingFromRow(row, now)
	if timing.ActiveSeconds != 35 || timing.CurrentActiveSeconds != 15 || timing.ActiveSince == nil || !timing.ActiveSince.Equal(resumed) {
		t.Fatalf("timing=%+v", timing)
	}
	if total := checkpointTotal(row, now); total != 35 {
		t.Fatalf("checkpoint=%d", total)
	}

	row.status = "PAUSED"
	timing = timingFromRow(row, now.Add(4*time.Hour))
	if timing.ActiveSeconds != 20 || timing.CurrentActiveSeconds != 0 || timing.ActiveSince != nil {
		t.Fatalf("paused timing=%+v", timing)
	}
}

func TestSessionTimingDoesNotCapLongActiveStudy(t *testing.T) {
	started := time.Date(2030, 1, 1, 8, 0, 0, 0, time.UTC)
	row := lifecycleRow{sessionID: uuid.New(), status: "ACTIVE", startedAt: started, accumulatedSeconds: 30, lastResumedAt: &started}
	timing := timingFromRow(row, started.Add(3*time.Hour))
	if timing.ActiveSeconds != 10830 || timing.CurrentActiveSeconds != 10800 {
		t.Fatalf("long-session timing=%+v", timing)
	}
}

func TestLatestTimeDoesNotMoveActivityBackwards(t *testing.T) {
	floor := time.Date(2030, 1, 1, 8, 0, 1, 0, time.UTC)
	if got := latestTime(floor.Add(-time.Second), floor); !got.Equal(floor) {
		t.Fatalf("latestTime moved backwards: %s", got)
	}
}
