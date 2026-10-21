package test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// v1_t7: pre-claim crash — the B1 journal replay (reference.md §3.5),
// e2e, driver/envelope edition. The crash window is the v0 known gap
// "#Start-crash" (D5): the daemon dies between the aihub session claim
// and the local job registration, orphaning a RUNNING session nobody can
// name. B1 closes it from both sides with claiming.json, written durably
// BEFORE the claim call, and the startup replay:
//
//   - journal entry exists + hub session matches → adopt and finish the
//     start (the SAME session secret replays the SAME claim answer, D4);
//   - anything else → drop.
//
// The window is deterministic here through a claim-hold proxy (the s17
// seal-crash mechanism, re-aimed): the proxy fronts the daemon's aihub
// API face, FORWARDS the claim (so it lands server-side) and then holds
// the RESPONSE until the test releases it. The daemon is SIGKILLed
// while the claim is landed-but-unseen — the exact ambiguous-claim
// shape ZooKeeper's GUID lesson names — and the restarted daemon must
// adopt the journaled session and finish the start with no second
// claim and no blocked compensation: the adopted start dispatches the
// driver's ordinal-1 step process, the workflow completes and the
// terminal wiring wraps it. The in-process halves (journal write,
// replay refusal paths) are pinned by runtime/v15_session_test.go; this
// scenario pins the real daemon's restart wiring end to end.

// claimHoldProxy fronts aihub: every request is forwarded untouched,
// but the work item's session CLAIM response is captured and held — the
// test releases it only after the doomed daemon is dead. Unlike s17's
// hold-proxy (which never forwards the held call), the claim must LAND
// at aihub: the ambiguous-claim shape is "server heard it, client
// never saw the answer".
type claimHoldProxy struct {
	proxy    *httputil.ReverseProxy
	landed   chan struct{} // closed once the claim response reached the proxy
	landOnce sync.Once
	release  chan struct{} // closed to unblock the held response
	relOnce  sync.Once
	srv      *http.Server
	ln       net.Listener
}

func newClaimHoldProxy(t *testing.T, target string) *claimHoldProxy {
	t.Helper()
	tu, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	p := &claimHoldProxy{
		landed:  make(chan struct{}),
		release: make(chan struct{}),
	}
	p.proxy = &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(tu)
	}}
	// The claim lands at the backend, its answer reaches the proxy, and
	// only then does the test get its kill signal — the response is
	// withheld from the daemon until release.
	p.proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == http.StatusOK && isSessionEndpoint(resp.Request) {
			p.landOnce.Do(func() { close(p.landed) })
			select {
			case <-p.release:
			case <-resp.Request.Context().Done():
			}
		}
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p.ln = ln
	p.srv = &http.Server{Handler: p}
	go func() { _ = p.srv.Serve(ln) }()
	t.Cleanup(func() {
		p.drop()
		_ = ln.Close()
		_ = p.srv.Close()
	})
	return p
}

// isSessionEndpoint reports whether the response belongs to the WI's
// claim endpoint (the path the B1 window straddles).
func isSessionEndpoint(req *http.Request) bool {
	return req != nil && req.Method == http.MethodPost && lastSegment(req.URL.Path) == "claim"
}

func lastSegment(path string) string {
	trimmed := path
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] == '/' {
			return trimmed[i+1:]
		}
	}
	return trimmed
}

// ServeHTTP forwards everything (the holding happens in ModifyResponse).
func (p *claimHoldProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.proxy.ServeHTTP(w, r)
}

// url answers the proxy's own base URL.
func (p *claimHoldProxy) url() string {
	return "http://" + p.ln.Addr().String()
}

// waitForLanded blocks the test until the claim answer reached the
// proxy (bounded — the claim must happen at all for the window to
// exist).
func (p *claimHoldProxy) waitForLanded(t *testing.T, bound time.Duration) {
	t.Helper()
	select {
	case <-p.landed:
	case <-time.After(bound):
		t.Fatalf("the daemon's claim never reached aihub through the proxy (bound %v)", bound)
	}
}

// drop releases the held response (the doomed daemon is already dead by
// then; the write lands on a closed connection, which is the point).
func (p *claimHoldProxy) drop() {
	p.relOnce.Do(func() { close(p.release) })
}

// claimingEntries reads <dataDir>/claiming.json — the B1 journal.
func claimingEntries(t *testing.T, dataDir string) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "claiming.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("claiming.json: %v", err)
	}
	var cf struct {
		Items map[string]map[string]any `json:"items"`
	}
	if json.Unmarshal(raw, &cf) != nil || cf.Items == nil {
		return nil
	}
	return cf.Items
}

func TestV1T7PreClaimCrashJournalReplay(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "t7: pre-claim crash, journal replay adopts",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	// The proxy fronts the daemon's aihub API face; git pushes are pinned
	// direct by optServerURL.
	proxy := newClaimHoldProxy(t, inst.url)
	d := newV1Daemon(t, inst, optServerURL(proxy.url()))
	d.start()
	d.pf(t, "submit", wi)

	// The ambiguous claim: the claim LANDED server-side, the daemon never
	// saw the answer, and the process is killed in that exact window.
	proxy.waitForLanded(t, 60*time.Second)

	// B1: the journal entry was written BEFORE the call and survived the
	// crash — it names the minted SECRET (the replay anchor, D4).
	entries := claimingEntries(t, d.dataDir)
	if len(entries) == 0 {
		t.Fatalf("no claiming.json entry survived the pre-claim crash")
	}
	rec, ok := entries[wi]
	if !ok {
		t.Fatalf("claiming.json has no entry for %s: %+v", wi, entries)
	}
	secret, _ := rec["session_id"].(string)
	if secret == "" {
		t.Fatalf("claiming entry incomplete: %+v", rec)
	}

	// The hub already holds the claimed session: the PUBLIC fingerprint
	// of the journaled secret is the WI's holder on the audit block —
	// exactly what the replay's SessionVerify will learn.
	waitUntil(t, 15*time.Second, "the claim never landed at aihub", func() bool {
		return inst.sessionOf(t, wi).Fingerprint == api.SessionFingerprint(secret)
	})

	// SIGKILL the doomed daemon mid-claim, then release the held answer
	// (it writes to a dead connection — the point of the shape).
	d.crash()
	proxy.drop()

	// Restart the SAME daemon (same socket/dataDir/workspace), direct to
	// aihub: the startup replay must verify the journaled session
	// (matches), re-claim with the SAME secret (D4: the same answer comes
	// back — no second session, no ALREADY_HELD refusal) and finish the
	// start: the adopted driver dispatches its first step process, then
	// the workflow runs to its terminal wrap.
	d2 := &v1Daemon{
		t: t, inst: inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, extraArgs: d.extraArgs,
		logPath: filepath.Join(t.TempDir(), "daemon2.log"),
	}
	t.Cleanup(func() { d2.stop() })
	d2.start()
	waitUntil(t, 60*time.Second, "the restarted daemon never adopted the journaled start", func() bool {
		return len(spawnJournal(t, d2.capsuleDir(wi))) > 0
	})

	// The adopted session is the JOURNALED one — not a fresh claim: the
	// holder fingerprint never moved.
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("the adopted start changed the session: %q, want the journaled %q",
			sess.Fingerprint, api.SessionFingerprint(secret))
	}

	// The journal entry settled: cleared once the start finished.
	waitUntil(t, 30*time.Second, "the claiming entry never settled", func() bool {
		return len(claimingEntries(t, d2.dataDir)) == 0
	})

	// The adopted derivation finished the work item — no blocked
	// compensation, no second session.
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)

	// One derivation per workflow step — the adopted start's own
	// dispatches, no re-derivations — and the wrap cleared the session
	// row.
	spawns := spawnJournal(t, d2.capsuleDir(wi))
	if len(spawns) != 3 {
		t.Fatalf("spawn journal has %d entries, want 3 (the adopted start's single pass): %+v", len(spawns), spawns)
	}
	for _, s := range spawns {
		if s["resume"] != false {
			t.Fatalf("no dispatch of the adopted start may be a resume: %+v", spawns)
		}
	}
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}
}
