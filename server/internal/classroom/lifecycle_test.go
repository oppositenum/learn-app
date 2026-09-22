package classroom

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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

func TestClassroomSubmitBudgetMatchesFrontendInFlightContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate lifecycle test source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../..", "apps", "web", "src", "lib", "studentInteraction.ts"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`studentSubmitBudgetMS\s*=\s*([0-9_]+)`).FindSubmatch(source)
	if len(match) != 2 {
		t.Fatal("frontend submit budget mirror is missing")
	}
	frontendMS, err := strconv.ParseInt(strings.ReplaceAll(string(match[1]), "_", ""), 10, 64)
	if err != nil {
		t.Fatalf("invalid frontend submit budget: %v", err)
	}
	if frontendMS != SubmitOverallTimeout.Milliseconds() {
		t.Fatalf("submit budget drift: frontend=%dms server=%dms", frontendMS, SubmitOverallTimeout.Milliseconds())
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

func TestAbandonPausedSessionInTxSQLKeepsPlanCardAvailable(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate lifecycle test source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "lifecycle.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(source)
	if !strings.Contains(body, "func abandonPausedSessionInTx") {
		t.Fatal("starting a new card from a paused classroom needs abandonPausedSessionInTx")
	}
	if !strings.Contains(body, `UPDATE learning_plan_blocks SET status='AVAILABLE' WHERE id=$1 AND status='ACTIVE'`) {
		t.Fatal("pause and abandon must return today's plan card to AVAILABLE")
	}
}

func TestLatestTimeDoesNotMoveActivityBackwards(t *testing.T) {
	floor := time.Date(2030, 1, 1, 8, 0, 1, 0, time.UTC)
	if got := latestTime(floor.Add(-time.Second), floor); !got.Equal(floor) {
		t.Fatalf("latestTime moved backwards: %s", got)
	}
}
