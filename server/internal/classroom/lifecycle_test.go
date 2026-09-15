package classroom

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestClassroomTimeoutOrderingKeepsRequestsAheadOfProxyAndStaleRecovery(t *testing.T) {
	if !timeoutOrderingValid() {
		t.Fatalf("invalid timeout ordering: submit=%s proxy=%s stale=%s", SubmitOverallTimeout, proxyReadTimeout, stalePauseAfter)
	}
	if sessionLeaseTime <= SubmitOverallTimeout || sessionLeaseTime <= stalePauseAfter {
		t.Fatalf("operation lease must cover the request and stale recovery window: lease=%s submit=%s stale=%s", sessionLeaseTime, SubmitOverallTimeout, stalePauseAfter)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate lifecycle test source")
	}
	nginx, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../..", "deploy", "nginx.conf"))
	if err != nil {
		t.Fatal(err)
	}
	proxySeconds := fmt.Sprintf("%ds", int(proxyReadTimeout/time.Second))
	wantProxy := "proxy_read_timeout " + proxySeconds + ";"
	if !strings.Contains(string(nginx), wantProxy) || !strings.Contains(string(nginx), "proxy_send_timeout "+proxySeconds+";") {
		t.Fatalf("deploy proxy timeout does not match ordering source %s", proxyReadTimeout)
	}
}

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
