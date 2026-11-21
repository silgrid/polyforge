// carries: internal/polyforge/daemon/routes.go,
//
//	internal/polyforge/daemon/cells.go,
//	internal/polyforge/daemon/main.go
package daemon

// opsrestart_test.go — polyforge#190 (84-inplace-upgrade), u4: the
// restart-for-an-upgrade's trigger — the one half #175 left to this
// ticket (the supervisor already relaunches the daemon AT ONCE on
// exit 75). POST /ops/restart answers at once, the Host's graceful
// shutdown runs, and Run ends in the exit the supervisor knows: 75 —
// never 0 (which would end the whole container) and never 1. A second
// ask while one is already in flight is the same answer, and the flag
// lives in the process alone: a fresh daemon never carries it over.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// opsRestartPost posts one empty /ops/restart body over the Host's
// socket, answering the HTTP status and the decoded answer.
func opsRestartPost(t *testing.T, sock string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	resp, err := c.Post("http://daemon/ops/restart", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestOpsRestartRouteAnswersThenRunEndsIn75 pins the trigger's whole
// contract: the route answers 200 at once (the shutdown it starts is
// the same graceful one /shutdown runs), the Host's Run ends in the
// restart-for-an-upgrade error, and the exit mapping answers 75 — the
// supervisor's AT-ONCE relaunch, never the container's end.
func TestOpsRestartRouteAnswersThenRunEndsIn75(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})

	code, raw := opsRestartPost(t, th.sock)
	if code != http.StatusOK {
		t.Fatalf("the restart ask must answer 200 at once, got %d %s", code, raw)
	}
	if !strings.Contains(raw, "restarting_for_upgrade") {
		t.Fatalf("the answer must name the restart-for-an-upgrade: %s", raw)
	}

	select {
	case err := <-th.done:
		if err == nil {
			t.Fatal("a restart-for-an-upgrade must not end Run in nil (exit 0 ends the whole container)")
		}
		if !errors.Is(err, errRestartForUpgrade) {
			t.Fatalf("Run must end in the restart-for-an-upgrade sentinel, got: %v", err)
		}
		if got := exitOfRun(err); got != ExitUpgradeRestart {
			t.Fatalf("the exit mapping must answer %d, got %d", ExitUpgradeRestart, got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the Host never ended its run after the restart ask")
	}
}

// TestOpsRestartRouteRefusesAProfile pins the Host-level shape the
// drain established: the restart is every Cell's, it takes no profile.
func TestOpsRestartRouteRefusesAProfile(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	defer th.Shutdown(context.Background(), "test done")

	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", th.sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", "http://daemon/ops/restart", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PF-Profile", "alpha")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a profiled restart ask must be refused 400, got %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "Host-level") {
		t.Fatalf("the refusal must name the Host-level shape: %s", raw)
	}
}

// TestOpsRestartFlagIsProcessLocal pins the #175 review's N8 concern:
// the restart flag lives in the process alone — a daemon that relaunches
// starts with no flag, so a supervisor relaunch can never loop on a
// persisted restart ask.
func TestOpsRestartFlagIsProcessLocal(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	defer th.Shutdown(context.Background(), "test done")

	if th.restartForUpgrade.Load() {
		t.Fatal("a fresh Host carries no restart flag")
	}
	th.restartForUpgrade.Store(true)
	if !th.restartForUpgrade.Load() {
		t.Fatal("the flag is the process's own record of the ask")
	}
	fresh, err := NewHost(th.cfg)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	if fresh.restartForUpgrade.Load() {
		t.Fatal("a fresh Host must never inherit the restart flag")
	}
}

// TestOpsRestartRouteRefusesStepAndUncredentialedPeers pins the guard
// review r2#3/#4 found missing: POST /ops/restart is a Host-level
// write — a step agent's process tree is refused DAEMON-403 (and Run
// never ends: no restart was asked), and so is a peer whose UDS
// credential is unavailable (fail closed, exactly like /shutdown and
// the drain). The docs' claim that a step agent cannot reach it only
// becomes true with this.
func TestOpsRestartRouteRefusesStepAndUncredentialedPeers(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	defer th.Shutdown(context.Background(), "test done")

	// a step agent's process tree: the runtime probe names this very
	// process a step of the Host's machine id.
	restore := runtimeStepOrigin
	runtimeStepOrigin = func(pid int, id string) (string, bool) { return "wi_step", id == "m-def" }
	t.Cleanup(func() { runtimeStepOrigin = restore })

	code, raw := opsRestartPost(t, th.sock)
	if code != http.StatusForbidden {
		t.Fatalf("a step agent's restart ask must be refused DAEMON-403, got %d %s", code, raw)
	}
	if !strings.Contains(raw, "DAEMON-403") || !strings.Contains(raw, "step process tree") {
		t.Fatalf("the refusal must name the step origin: %s", raw)
	}
	if th.restartForUpgrade.Load() {
		t.Fatal("a refused ask must never set the restart flag")
	}
	select {
	case err := <-th.done:
		t.Fatalf("a refused ask must not end Run: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	_ = th.Shutdown(context.Background(), "the step-origin half done")

	// a peer without a UDS credential: the guarded handler answers the
	// same fail-closed refusal as every other Host-level write.
	th2 := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	defer th2.Shutdown(context.Background(), "test done")
	req := httptest.NewRequest(http.MethodPost, "/ops/restart", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	th2.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an uncredentialed restart ask must be refused DAEMON-403, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "identity") {
		t.Fatalf("the refusal must name the unavailable identity: %s", rec.Body.String())
	}
	if th2.restartForUpgrade.Load() {
		t.Fatal("a refused ask must never set the restart flag")
	}
}
