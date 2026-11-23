package daemon

// upgradedrain_test.go (polyforge#160, AC3): the Host-level upgrade
// drain — `pf ops drain --upgrade` sets it, every Cell's drainRefusal
// then refuses new work (DAEMON-409), a Host still holding work refuses
// the upgrade (409, the drain stays set) until --force, and the route
// is Host-level and human-only.

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// upgradeDrainPost posts one /ops/drain body over the Host's socket,
// answering the HTTP status and the decoded answer.
func upgradeDrainPost(t *testing.T, sock, body string, header map[string]string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	c := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", "http://daemon/ops/drain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestUpgradeDrainRefusesNewWork409: once the upgrade drain is set,
// every Cell's drainRefusal — the one gate every admission of new work
// passes — answers DAEMON-409 naming the upgrade; before it, nil.
func TestUpgradeDrainRefusesNewWork409(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {})
	<-td.ready
	td.upgradeDrain = &upgradeDrain{}
	if err := td.drainRefusal(); err != nil {
		t.Fatalf("an unset upgrade drain must not refuse: %v", err)
	}
	td.upgradeDrain.setUpgrade()
	e := td.drainRefusal()
	if e == nil {
		t.Fatal("the set upgrade drain must refuse new work")
	}
	ae, ok := e.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("the upgrade drain refusal must be DAEMON-409, got %+v", e)
	}
	if !strings.Contains(ae.Detail, "upgrade") || !strings.Contains(ae.Detail, "daemon restart") {
		t.Fatalf("the refusal must name the upgrade and the restart that ends it: %s", ae.Detail)
	}
}

// TestUpgradeDrainRouteIdleBusyForce: the Host route sets the drain
// first, then judges: an idle Host answers 200 (safe to upgrade); a
// Host still holding work answers 409 naming the busy Cell and KEEPS
// the drain; --force answers 200 accepting the in-flight work. A
// profile is refused — the drain is Host-level.
func TestUpgradeDrainRouteIdleBusyForce(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})

	// an idle Host: the drain is set, the answer says so
	code, raw := upgradeDrainPost(t, th.sock, `{"upgrade":true}`, nil)
	if code != http.StatusOK || !strings.Contains(raw, "idle") {
		t.Fatalf("an idle Host must answer 200 naming its idleness, got %d %s", code, raw)
	}
	// new work is refused on EVERY Cell from here on
	for _, p := range []string{"default", "alpha"} {
		d := th.cell(p).d
		if d == nil || d.drainRefusal() == nil {
			t.Fatalf("Cell %s must refuse new work once the drain is set", p)
		}
	}

	// a Cell holding work refuses the upgrade (409, the busy Cell named)
	def := th.cell("default").d
	def.q.mu.Lock()
	def.q.inflight["wi_busy"] = qEntry{}
	def.q.mu.Unlock()
	code, raw = upgradeDrainPost(t, th.sock, `{"upgrade":true}`, nil)
	if code != http.StatusConflict || !strings.Contains(raw, "default") {
		t.Fatalf("a busy Host must refuse the upgrade with 409 naming the busy Cell, got %d %s", code, raw)
	}
	// the busy refusal did NOT lift the drain: new work is still refused
	if def.drainRefusal() == nil {
		t.Fatal("the drain must stay set after a busy refusal")
	}
	// --force accepts the in-flight work
	code, raw = upgradeDrainPost(t, th.sock, `{"upgrade":true,"force":true}`, nil)
	if code != http.StatusOK || !strings.Contains(raw, "default") || !strings.Contains(raw, "force") {
		t.Fatalf("--force must accept the in-flight work naming it, got %d %s", code, raw)
	}

	// a profile is refused: the drain is Host-level
	code, raw = upgradeDrainPost(t, th.sock, `{"upgrade":true}`, map[string]string{protocol.ProfileHeader: "default"})
	if code != http.StatusBadRequest || !strings.Contains(raw, "Host-level") {
		t.Fatalf("a profiled drain request must be refused 400, got %d %s", code, raw)
	}
}

// TestUpgradeDrainRouteRefusesAgents: the route mutates the Host, so a
// step agent's process tree is refused 403 (the same guard POST
// /shutdown and a profile-less /ops/config carry).
func TestUpgradeDrainRouteRefusesAgents(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"})
	old := runtimeStepOrigin
	runtimeStepOrigin = func(pid int, daemonID string) (string, bool) { return "wi_agent", true }
	t.Cleanup(func() { runtimeStepOrigin = old })
	code, raw := upgradeDrainPost(t, th.sock, `{"upgrade":true}`, nil)
	if code != http.StatusForbidden || !strings.Contains(raw, "DAEMON-403") {
		t.Fatalf("an agent origin must be refused 403, got %d %s", code, raw)
	}
}

// TestUpgradeDrainStopsTheAutoWave (r1#3): with the Host-wide upgrade
// drain set, the auto-pull wave enqueues NOTHING — the automatic-claim
// half of AC3 rides the one drainRefusal gate (daemon.go's wave check);
// reverting that check to the rotation-only pendingChange() turns this
// test red (that run is recorded in build's round-2 self-review).
func TestUpgradeDrainStopsTheAutoWave(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.MaxConcurrent = 2
		// the loops under test are driven by hand; keep them out
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	td.upgradeDrain = &upgradeDrain{}
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	seedReadyB3(t, td, 3)
	td.upgradeDrain.setUpgrade()
	td.fetchAutoWave()
	if got := td.q.LenPending(); got != 0 {
		t.Fatalf("the auto wave under the upgrade drain enqueued %d entries, want 0 — automatic claims are refused like every other new work", got)
	}
}
