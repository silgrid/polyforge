package observe

// terminal_auth_repro_test.go — the form-① reproduction
// (the pi#126 incident, observation mem_plyQWWjvmaMl's escalated
// recurrence): a job's TERMINAL snapshot push was refused by the aihub
// git push-proxy as an authentication failure with the job's own
// UNCHANGED credential — same job, the five pushes before it (v23–v27)
// all succeeded, only the terminal one (v28) failed, and the daemon
// recorded it GIT-500 "Authentication failed for http://…/git/<wi>.git/"
// with the published record stopping one version short.
//
// The cause: the close (stopOp → sealNetwork) sent its SessionEnd to
// aihub while the observe monitor was still alive, so the monitor's
// flush of the terminal execution.json version could run only after the
// session was dead server-side. aihub's core clears the session row at
// SessionEnd (M2), the git face then refuses the unchanged credential
// (GIT-401, git's "Authentication failed"), and gitops wraps the chain
// push's failure as GIT-500 — the recorded incident shape.
//
// The fix (runtime Config.ReleaseMonitor → Observe.Release) makes every
// close stop the monitor and wait for it to exit — its in-flight push
// drained, queued ones dropped — BEFORE its seal push and SessionEnd.
// This test plays exactly that enforced ordering (owner ruling on
// gmi32-terminal-auth-repro-test, answer A: the first version played
// SessionEnd before a free-running monitor's flush, an ordering the
// fixed close can no longer produce): stop the monitor and wait for its
// exit, THEN SessionEnd, THEN the terminal version lands on disk. The
// assertion is unchanged.
//
// Everything below is real: the real aihub git push-proxy (core session
// model + git http-backend) serving a real bare repo, the real
// gitops.CommitPush, the real monitor machinery — no recording doubles.
//
// Reported failure: at the terminal boundary the snapshot push fails
// "Authentication failed" with the unchanged credential and lands in the
// events log as a GIT-500 push_failed.
// Expected behavior: the terminal boundary produces no
// credential-authentication push failure for the job's own credential.
//
// Regression test: were the monitor still running at SessionEnd (the
// pre-fix close), the terminal flush would be attempted against the
// ended session and its push_failed would carry the GIT-500
// authentication refusal, failing the assertion with the reported
// symptom.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	aihubgit "github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// pushProxy is one real aihub git push-proxy: the core session model
// (users/projects/work items/claims) plus the smart-HTTP git proxy
// serving a real bare repo, on an httptest server.
type pushProxy struct {
	db       *core.DB
	sessions *core.SessionManager
	wis      *core.WIManager
	admin    *core.AdminManager
	mgr      *aihubgit.Manager
	ts       *httptest.Server
	userID   string
}

// newPushProxy builds the proxy and its backing store.
func newPushProxy(t *testing.T) *pushProxy {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not found: %v", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not found: %v", err)
	}
	dataDir := t.TempDir()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open core db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p := &pushProxy{
		db:       db,
		sessions: core.NewSessionManager(db),
		wis:      core.NewWIManager(db),
		admin:    core.NewAdminManager(db),
	}
	u, err := p.admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	p.userID = u.ID
	if _, err := p.admin.CreateProject(core.CreateProjectRequest{Name: "alpha"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	mgr, err := aihubgit.NewManager(filepath.Join(dataDir, "git"), core.NewOwnerManager(db))
	if err != nil {
		t.Fatalf("git manager: %v", err)
	}
	p.mgr = mgr
	mux := http.NewServeMux()
	mux.Handle("/git/", http.HandlerFunc(mgr.HandleGit))
	p.ts = httptest.NewServer(mux)
	t.Cleanup(p.ts.Close)
	return p
}

// claim creates + claims one work item and answers its id and the
// session secret (the push credential), with the bare repo installed.
func (p *pushProxy) claim(t *testing.T) (wiID, secret string) {
	t.Helper()
	bundle := "bug-fix"
	wi, err := p.wis.Create(core.CreateRequest{
		Project: "alpha", Goal: "terminal auth repro", Reporter: p.userID,
		BundleName: &bundle,
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	secret = "git-session-secret-" + wi.ID
	if _, err := p.sessions.Claim(wi.ID, api.SessionClaimRequest{
		SessionID: secret, MachineID: "m1",
	}, p.userID); err != nil {
		t.Fatalf("session claim: %v", err)
	}
	if err := p.mgr.EnsureBare(wi.ID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	return wi.ID, secret
}

// endSession plays the close's SessionEnd on the hub: the fenced
// terminal verdict that clears the session row (M2), exactly what
// runtime.sealNetwork sends after its seal push and WIP lane.
func (p *pushProxy) endSession(t *testing.T, wiID, secret string) {
	t.Helper()
	if err := p.sessions.End(wiID, api.SessionEndRequest{
		SessionID: secret, Status: api.StatusWrapped, Note: "terminal close",
	}, p.userID); err != nil {
		t.Fatalf("session end: %v", err)
	}
}

// chainExecVersion answers the execution.json the proxy's bare repo
// carries on its main chain (-1 when the chain has none).
func (p *pushProxy) chainExecVersion(t *testing.T, wiID string) int64 {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", p.mgr.RepoDir(wiID), "show",
		"refs/heads/main:runtime-state/execution.json").Output()
	if err != nil {
		return -1
	}
	var e capsule.Execution
	if json.Unmarshal(out, &e) != nil {
		return -1
	}
	return e.Version
}

// pushFailedAuthRefusals answers the push_failed event bodies whose
// error carries the git face's authentication refusal.
func pushFailedAuthRefusals(m *Monitor) []string {
	var out []string
	for _, e := range eventsOf(m, EvPushFailed) {
		var b struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(e.Body, &b) == nil &&
			strings.Contains(b.Error, "Authentication failed") {
			out = append(out, string(e.Body))
		}
	}
	return out
}

// TestTerminalFlushAfterSessionEndIsNotAnAuthFailure pins the incident
// under the close's enforced ordering: the monitor is released (stopped,
// exited) before SessionEnd clears the session server-side, so the
// terminal-carrying execution version that lands afterwards must not end
// as a credential-authentication push failure of the job's own,
// unchanged credential.
func TestTerminalFlushAfterSessionEndIsNotAnAuthFailure(t *testing.T) {
	p := newPushProxy(t)
	wiID, secret := p.claim(t)
	remote := p.ts.URL + "/git/" + wiID + ".git"

	// --- the capsule: a real git worktree + external git-dir, the real
	// commit+push lane against the real proxy ---
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	gitDir := filepath.Join(dir, "git", wiID+".git")
	for _, d := range []string{filepath.Dir(gitDir), filepath.Join(capsuleDir, "runtime-state")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "--initial-branch=main",
		"--separate-git-dir="+gitDir, capsuleDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(capsuleDir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "commit", "--allow-empty", "-m", "init")
	commit.Dir = capsuleDir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("initial commit: %v: %s", err, out)
	}
	git := gitops.Opts{
		WorkItemID: wiID, CapsuleDir: capsuleDir, GitDir: gitDir,
		RemoteURL: remote, Username: wiID, Secret: secret,
	}

	// --- the monitor: the real push, real backoff ---
	log, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	cfg := testCfg()
	cfg.push = gitops.CommitPush
	cfg.PushBound = 30 * time.Second
	// A coarser retry cadence than the unit default: the terminal flush
	// fails fast and the test only needs its first result — a tighter
	// cadence only multiplies real git processes at teardown.
	cfg.PushRetryBackoff = 100 * time.Millisecond
	cfg.PushBackoffMax = 400 * time.Millisecond
	h := newFakeHandle("att_1")
	m := &Monitor{
		wi:  WIInfo{WorkItem: wiID, CapsuleDir: capsuleDir, Git: git},
		cfg: cfg, log: log, ob: supervise.NewQueue[protocol.OBMsg](),
		link:       supervise.NewLink(wiID),
		rtQ:        supervise.NewQueue[Attach](),
		stopCh:     make(chan struct{}),
		gate:       make(chan struct{}, 1),
		pushDone:   make(chan pushResult, 1),
		verifyDone: make(chan verifyResult, 1),
	}
	m.attach(Attach{
		Msg:    protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: wiID, SpawnN: 1},
		Handle: h,
	})
	m.phase = phaseBetween
	m.phaseSince = m.now()
	m.announceStart()
	runDone := make(chan struct{})
	go func() { m.run(); close(runDone) }()
	// Stop the monitor and let its last in-flight push drain before the
	// events log closes and the temp dirs are removed (LIFO: this runs
	// before the log cleanup registered above): a push sub-goroutine
	// still running at teardown is what leaves temp entries behind, and
	// the private-TMPDIR leak check fails the whole package on them.
	t.Cleanup(func() {
		m.requestStop()
		<-runDone
	})

	writeExecVersion := func(version int64, status string) {
		t.Helper()
		exec := capsule.Execution{
			CapsuleID: "cap_" + wiID, WorkItemID: wiID,
			CurrentStep: "review", WorkflowStatus: status,
			Steps: map[string]*capsule.Step{
				"review": {Status: capsule.StepInProgress},
			},
			ReviewRounds: map[string]int64{"review": 1},
			UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
			Version:      version,
		}
		blob, err := json.Marshal(exec)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(capsuleDir, "runtime-state", "execution.json")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	// --- mid-run: the versions before the terminal one publish with
	// the SAME credential (the incident's v23–v27 all succeeded) ---
	writeExecVersion(1, capsule.WorkflowStatusRunning)
	m.link.Fact(supervise.ExecCommitted{Version: 1, Step: "review"})
	soon(t, 15*time.Second, "the first mid-run version published", func() bool {
		return publishedVersion(m, 1)
	})
	writeExecVersion(2, capsule.WorkflowStatusRunning)
	m.link.Fact(supervise.ExecCommitted{Version: 2, Step: "review"})
	soon(t, 15*time.Second, "the second mid-run version published", func() bool {
		return publishedVersion(m, 2)
	})
	if v := p.chainExecVersion(t, wiID); v != 2 {
		t.Fatalf("the proxy's chain carries version %d, want 2 — the mid-run publishes did not land", v)
	}

	// --- terminal: the close releases the monitor first — stopped and
	// exited, its in-flight push drained (Observe.Release) — and only
	// then sends SessionEnd ---
	m.requestStop()
	select {
	case <-runDone:
	case <-time.After(cfg.PushBound + 10*time.Second):
		t.Fatal("the released monitor never exited")
	}
	p.endSession(t, wiID, secret)

	// The terminal-carrying version lands after the session ended (the
	// driver's last write; the close's seal push is its carrier). The
	// released monitor must not flush it with the dead credential.
	writeExecVersion(3, capsule.WorkflowStatusCompleted)
	m.link.Fact(supervise.ExecCommitted{Version: 3, Step: "review"})

	// --- the claimed end state, asserted literally ---
	// Several of the monitor's push-retry periods: a monitor still alive
	// would have flushed (and failed) by now.
	time.Sleep(4 * cfg.PushRetryBackoff)
	if auth := pushFailedAuthRefusals(m); len(auth) > 0 {
		t.Fatalf("SYMPTOM ( form \u2460, pi#126): the terminal snapshot "+
			"push was refused as an authentication failure with the job's own unchanged "+
			"credential and recorded as a GIT-500 push_failed: %s\n(events dump:\n%s)",
			strings.Join(auth, "\n"), testEventsDump(m))
	}
	if v := p.chainExecVersion(t, wiID); v != 2 {
		t.Fatalf("the proxy's chain carries version %d, want 2 — the mid-run publishes stand", v)
	}
}
