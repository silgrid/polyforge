package daemon

// resume_b6_repro_test.go: the B6 repro of wi_seWTgHeKtaa8 (audit
// 2026-10-13, polyforge#151). `pf resume` on a paused work item only
// PATCHes clean_pause=false on the hub (internal/polyforge/cli/
// verbs_daemon.go cmdResume, the "otherwise (paused)" branch); the
// daemon HOLDING the paused work item is never told, so nothing happens
// on it until that daemon's next start — whose restart reconciliation
// is the only path that attaches a paused+cleared session
// (runtime/restart.go). v2-converge §7 (the WI's goal) requires the
// ONLINE holder to continue in place.
//
// The repro drives the REAL verb end to end: the real cli.Run("resume")
// against an HTTP-fronted view of the fake hub and the real daemon
// socket under test, after the real `pf pause` (daemon route) parked the
// job. Before the fix this fails: no session-attach, no re-dispatch, the
// job stays paused locally, and the CLI itself tells the operator the
// live holder will not pick the resume up.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// hubOverHTTP fronts the fake hub's work-item read face for the REAL
// CLI: GET answers the WI (status paused), PATCH records the operator's
// clean_pause=false clear (the resume's hub-side effect). The daemon
// under test keeps talking to the fake hub directly, so both sides see
// one state.
func hubOverHTTP(t *testing.T, td *testDaemon, patches *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/work-items/wi_b6"):
			wi, err := td.hub.GetWorkItem("wi_b6")
			if err != nil {
				http.Error(w, `{"code":"CORE-500","detail":"fake"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(wi)
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/work-items/wi_b6"):
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"clean_pause":`) {
				http.Error(w, `{"code":"CORE-400","detail":"unexpected PATCH body"}`, http.StatusBadRequest)
				return
			}
			// the operator's resume landed on the hub: clean_pause is
			// cleared; the work item itself stays status=paused with its
			// session (the real hub's semantics for this PATCH).
			*patches++
			td.hub.mu.Lock()
			td.hub.cleanPauseCleared = true
			td.hub.mu.Unlock()
			wi, _ := td.hub.GetWorkItem("wi_b6")
			_ = json.NewEncoder(w).Encode(wi)
		default:
			http.Error(w, `{"code":"CORE-404","detail":"fake hub: route not scripted"}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runRealResume drives the real `pf resume` (cli.Run, the same entry
// cmd/pf dispatches to) pointed at the HTTP fake hub and the daemon
// socket under test.
func runRealResume(t *testing.T, hubURL, socket string) (int, string, string) {
	t.Helper()
	for _, k := range []string{"PF_SERVER", "PF_API_KEY", "PF_DAEMON_SOCKET", "PF_USER_ID", "PF_CHAT_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PF_SERVER", hubURL)
	t.Setenv("PF_DAEMON_SOCKET", socket)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"resume", "wi_b6"}, bytes.NewReader(nil), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// attachCount snapshots how many session-attach calls the fake hub saw.
func attachCount(h *fakeHub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.attaches)
}

// TestResumeOnlineHolderContinuesPausedWIInPlace is the B6 repro: pause
// a live job on this (online) daemon (the real pause route), then run
// the real `pf resume`. Expected (the WI's goal): the ONLINE holder
// re-attaches the paused session and continues the current step in
// place. Observed before the fix: the resume clears the hub flag and
// nothing else — no attach, no dispatch, the job stays paused until the
// daemon restarts.
func TestResumeOnlineHolderContinuesPausedWIInPlace(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		// the dispatch/exit loops stay live (in-place continuation must
		// be observable through them); no drain needed for one explicit
		// submit.
		c.DrainInterval = time.Hour
	})

	// 1. a running job on this (online) daemon.
	td.submitClaim(t, "wi_b6")
	spawnsAtPause := td.spawner.callCount()
	if spawnsAtPause == 0 {
		t.Fatalf("precondition: the first dispatch never happened")
	}

	// 2. the operator pauses it — the real pause route.
	if _, err := td.client.Pause("wi_b6"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitState(t, td, "wi_b6", "paused")
	if st := td.hub.lastEnd().Status; st != api.StatusPaused {
		t.Fatalf("precondition: hub pause end status = %q, want paused", st)
	}

	// 3. the operator runs the real `pf resume` (hub PATCH face).
	patches := 0
	hub := hubOverHTTP(t, td, &patches)
	code, stdout, stderr := runRealResume(t, hub.URL, td.sock)
	if code != 0 {
		t.Fatalf("pf resume exited %d (stderr %s)", code, stderr)
	}
	if patches == 0 {
		t.Fatalf("precondition broken: pf resume never PATCHed clean_pause=false (stdout %q)", stdout)
	}

	// 4. the reported failure, part one — the CLI's own guidance: a live
	// holder daemon is told to wait for its next start.
	if strings.Contains(stdout, "does NOT pick this up") {
		t.Errorf("REPORTED FAILURE (B6): pf resume tells the operator the live holder daemon will not pick the resume up until its next start: %q", strings.TrimSpace(stdout))
	}

	// 5. the reported failure, part two — the online holder does not
	// continue: no session-attach and no re-dispatch of the current
	// step within a window that spans hundreds of dispatch/exit ticks.
	deadline := time.Now().Add(3 * time.Second)
	for attachCount(td.hub) == 0 || td.spawner.callCount() <= spawnsAtPause {
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := attachCount(td.hub); got == 0 {
		t.Fatalf("REPORTED FAILURE (B6): 3s after the operator's pf resume (clean_pause cleared on the hub), the ONLINE holder daemon made no session-attach call for wi_b6 — only the startup reconciliation path (runtime/restart.go) ever attaches a paused session")
	}
	if got := td.spawner.callCount(); got <= spawnsAtPause {
		t.Fatalf("REPORTED FAILURE (B6): 3s after the operator's pf resume, the ONLINE holder daemon re-dispatched nothing (spawns before pause %d, after resume %d) — the paused work item stays parked until the daemon restarts", spawnsAtPause, got)
	}

	// 6. and the job is running again locally.
	waitState(t, td, "wi_b6", "running")
}
