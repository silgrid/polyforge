package runtime

// b8_claim_outside_opmu_test.go — repo review 2026-10-16 B-8: the claim's
// capsule clone and materialize run outside every runtime lock, so one
// slow or hung clone no longer freezes the other work items, and a
// stop, a pause or a shutdown during the claim cancels it cleanly.

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// hangingAccepted counts, per hanging remote URL, the connections it
// accepted: a clone of it is under way once the count is non-zero.
var hangingAccepted sync.Map // url → *atomic.Int32

// hangingRemote answers the URL of an http git remote that accepts every
// connection and never says a word: a clone of it hangs until killed.
func hangingRemote(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String() + "/app.git"
	accepted := &atomic.Int32{}
	hangingAccepted.Store(url, accepted)
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			accepted.Add(1)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	return url
}

// waitRemoteAccepted blocks until the clone pointed at url reaches it: the
// remote's accepted connection is the precondition that makes the clone
// really hang, which CLAIMING alone does not (the restart inserts its
// placeholder synchronously, before the replay runs —).
func waitRemoteAccepted(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if n, ok := hangingAccepted.Load(url); ok && n.(*atomic.Int32).Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the clone of %s never reached the hanging remote", url)
}

// waitClaiming blocks until wi sits CLAIMING (its clone under way).
func waitClaiming(t *testing.T, r *Runtime, wi string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if ji, ok := r.Info(wi); ok && ji.State == StateClaiming {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never reached CLAIMING", wi)
}

// within runs fn and fails the test when it does not return in time —
// the "promptly" of B-8 (a call that waited on a global lock behind the
// hung clone would never return).
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s (blocked behind the hung clone?)", what, d)
	}
}

func recvErr(t *testing.T, ch <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never returned", what)
		return nil
	}
}

func endsOf(h *fakeHub, wi string, sessionID string) []api.SessionEndRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []api.SessionEndRequest
	for _, e := range h.ends {
		if e.SessionID == sessionID {
			out = append(out, e)
		}
	}
	return out
}

func journaled(r *Runtime, wi string) *claimingRec { return r.loadClaiming()[wi] }

// A clone that hangs on one work item leaves the others fully
// operable: a second work item starts, pauses and resumes while the
// first is still cloning; the hung one refuses a duplicate start; and a
// wrap of the hung one cancels its clone and ends its session wrapped.
func TestB8HungCloneLeavesOtherWorkItemsResponsive(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const slow, fast = "wi_b8_slow", "wi_b8_fast"
	hub.reposFor = map[string][]api.ProjectRepo{slow: {{Name: "app", URL: hangingRemote(t)}}}
	ctx := context.Background()

	slowErr := make(chan error, 1)
	go func() { slowErr <- r.Start(ctx, slow, StartOpts{}) }()
	waitClaiming(t, r, slow)

	if err := r.Start(ctx, slow, StartOpts{}); err == nil {
		t.Fatal("a second start of a CLAIMING work item must be refused")
	}
	if n := hub.claimCount(); n != 1 {
		t.Fatalf("the CLAIMING work item was claimed %d times", n)
	}
	// state.json never carries the placeholder; the journal holds it.
	for _, rec := range loadRecords(t, r) {
		if rec.WorkItemID == slow {
			t.Fatalf("CLAIMING placeholder persisted: %+v", rec)
		}
	}
	if journaled(r, slow) == nil {
		t.Fatal("the in-flight claim lost its claiming-journal entry")
	}

	within(t, 20*time.Second, "Start of another work item", func() {
		if err := r.Start(ctx, fast, StartOpts{}); err != nil {
			t.Errorf("Start(%s): %v", fast, err)
		}
	})
	defer terminateJobAgent(r, fast)
	if m := recvRT(t, toOB); m.Kind != protocol.RTStarted || m.WorkItem != fast {
		t.Fatalf("want RTStarted(%s), got %+v", fast, m)
	}
	within(t, 20*time.Second, "pause of another work item", func() {
		if err := r.Stop(ctx, fast, protocol.FinishPaused); err != nil {
			t.Errorf("pause %s: %v", fast, err)
		}
	})
	if m := recvRT(t, toOB); m.Kind != protocol.RTFinished || m.Reason != protocol.FinishPaused {
		t.Fatalf("want RTFinished(paused), got %+v", m)
	}
	hub.mu.Lock()
	hub.cleanPause[fast] = false // the operator's pf resume PATCH
	hub.mu.Unlock()
	within(t, 20*time.Second, "resume of another work item", func() {
		out, err := r.ResumePaused(ctx, fast)
		if err != nil || out != ResumeAttached {
			t.Errorf("ResumePaused(%s) = %q, %v", fast, out, err)
		}
	})
	if ji, _ := r.Info(slow); ji.State != StateClaiming {
		t.Fatalf("the hung claim moved on its own: %s", ji.State)
	}

	// wrap of the hung work item: the clone is cancelled, the session
	// ends wrapped, nothing is left behind.
	sess := journaled(r, slow).SessionID
	within(t, 20*time.Second, "wrap of the CLAIMING work item", func() {
		if err := r.Stop(ctx, slow, protocol.FinishWrapped); err != nil {
			t.Errorf("wrap during CLAIMING: %v", err)
		}
	})
	if err := recvErr(t, slowErr, "the hung Start"); !errors.Is(err, ErrClaimClosed) {
		t.Fatalf("the hung Start answered %v, want ErrClaimClosed", err)
	}
	ends := endsOf(hub, slow, sess)
	if len(ends) != 1 || ends[0].Status != api.StatusWrapped {
		t.Fatalf("session ends of the closed claim = %+v, want one wrapped", ends)
	}
	if r.jobOf(slow) != nil || journaled(r, slow) != nil {
		t.Fatal("the closed claim left its placeholder or its journal entry behind")
	}
}

// A pause during CLAIMING has no capsule to retain a paused record for:
// the clone is cancelled and the session ends BLOCKED (cleared at the
// hub; pf resume re-claims it) — never a dangling session.
func TestB8PauseDuringClaimingEndsBlocked(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_b8_pause"
	hub.reposFor = map[string][]api.ProjectRepo{wi: {{Name: "app", URL: hangingRemote(t)}}}
	ctx := context.Background()
	startErr := make(chan error, 1)
	go func() { startErr <- r.Start(ctx, wi, StartOpts{}) }()
	waitClaiming(t, r, wi)
	sess := journaled(r, wi).SessionID
	within(t, 20*time.Second, "pause of the CLAIMING work item", func() {
		if err := r.Stop(ctx, wi, protocol.FinishPaused); err != nil {
			t.Errorf("pause during CLAIMING: %v", err)
		}
	})
	if err := recvErr(t, startErr, "Start"); !errors.Is(err, ErrClaimClosed) {
		t.Fatalf("Start answered %v, want ErrClaimClosed", err)
	}
	ends := endsOf(hub, wi, sess)
	if len(ends) != 1 || ends[0].Status != api.StatusBlocked {
		t.Fatalf("session ends = %+v, want one blocked", ends)
	}
	hub.mu.Lock()
	held := hub.held[wi]
	hub.mu.Unlock()
	if held != "" {
		t.Fatal("the paused claim left its session held at the hub")
	}
	if r.jobOf(wi) != nil || journaled(r, wi) != nil {
		t.Fatal("the closed claim left its placeholder or its journal entry behind")
	}
}

// A shutdown during the clone DEFERS the claim: the clone is killed, the
// work item is NOT turned blocked (no SessionEnd at all), the claiming
// journal keeps the entry, and the next daemon start replays the same
// claim to a running job.
func TestB8ShutdownDuringCloneDefersAndReplays(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_b8_shutdown"
	hub.reposFor = map[string][]api.ProjectRepo{wi: {{Name: "app", URL: hangingRemote(t)}}}
	startErr := make(chan error, 1)
	go func() { startErr <- r.Start(context.Background(), wi, StartOpts{}) }()
	waitClaiming(t, r, wi)
	rec := journaled(r, wi)

	r.Close() // the shutdown cancels the actors' context
	if err := recvErr(t, startErr, "Start"); !IsClaimDeferred(err) {
		t.Fatalf("Start answered %v, want the shutdown deferral", err)
	}
	if n := hub.endCount(); n != 0 {
		t.Fatalf("the shutdown ended %d session(s): %+v — a cancelled clone must not block the work item", n, hub.lastEnd())
	}
	if got := journaled(r, wi); got == nil || got.SessionID != rec.SessionID {
		t.Fatalf("claiming journal after the deferral = %+v, want the original entry", got)
	}
	if r.jobOf(wi) != nil {
		t.Fatal("the deferred claim left its placeholder in the table")
	}
	if err := r.Start(context.Background(), "wi_b8_other", StartOpts{}); !IsClaimDeferred(err) {
		t.Fatalf("a start after the shutdown answered %v, want the deferral", err)
	}

	// The next daemon start: same data dir, the remote reachable again.
	hub.mu.Lock()
	hub.reposFor[wi] = nil
	claimsBefore := len(hub.claims)
	hub.mu.Unlock()
	sp := &scriptedSpawner{}
	r2 := New(r.cfg, hub, sp, r.toOB, r.fromOB)
	t.Cleanup(r2.Close)
	if err := restartSettled(t, r2); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	settleReplays(t, r2) // the journal replays clone outside Restart
	defer terminateJobAgent(r2, wi)
	ji, ok := r2.Info(wi)
	if !ok || ji.State != StateRunning || ji.SessionID != rec.SessionID {
		t.Fatalf("after the replay: %+v (ok=%v), want RUNNING under the journaled session", ji, ok)
	}
	hub.mu.Lock()
	replays := hub.claims[claimsBefore:]
	hub.mu.Unlock()
	if len(replays) != 1 || replays[0].SessionID != rec.SessionID {
		t.Fatalf("restart claims = %+v, want one replay with the journaled session", replays)
	}
	if hub.endCount() != 0 {
		t.Fatalf("the replay ended a session: %+v", hub.lastEnd())
	}
	if journaled(r2, wi) != nil {
		t.Fatal("the replayed claim kept its journal entry")
	}
	if _, err := os.Stat(r.statePath); err != nil {
		t.Fatalf("state.json: %v", err)
	}
}

// The shutdown waits on no actor: a long close of another work item (its
// session end hung) is never waited on, and a Start after the shutdown
// defers without claiming.
func TestB8ShutdownDoesNotWaitOnALongClose(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const long, queued = "wi_b8_long", "wi_b8_queued"
	startRunning(t, r, long)
	g := hub.hangCall(t, "end", long)
	stopped := make(chan error, 1)
	go func() { stopped <- r.Stop(context.Background(), long, protocol.FinishWrapped) }()
	g.waitEntered(t, "the long close's session end")
	claims := hub.claimCount()
	r.Close()
	if err := r.Start(context.Background(), queued, StartOpts{}); !IsClaimDeferred(err) {
		t.Fatalf("a Start after the shutdown answered %v, want the deferral", err)
	}
	if n := hub.claimCount() - claims; n != 0 {
		t.Fatalf("the deferred Start claimed %d time(s)", n)
	}
	g.open()
	_ = recvErr(t, stopped, "the long close")
}

// review nit of B-8: a closed claim's partial capsule is quarantined, not
// left in place; a stale OB message never touches a CLAIMING placeholder.
func TestB8ClosedClaimQuarantinesCapsuleAndOBIgnoresPlaceholder(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_b8_quar"
	hub.reposFor = map[string][]api.ProjectRepo{wi: {{Name: "app", URL: hangingRemote(t)}}}
	startErr := make(chan error, 1)
	go func() { startErr <- r.Start(context.Background(), wi, StartOpts{}) }()
	waitClaiming(t, r, wi)
	j := r.jobOf(wi)
	// the fence itself (the message would otherwise queue behind the claim)
	r.applyOB(protocol.OBMsg{Kind: protocol.OBSuperseded, WorkItem: wi})
	if r.jobOf(wi) != j {
		t.Fatal("a stale OBSuperseded dropped the CLAIMING placeholder")
	}
	deadline := time.Now().Add(20 * time.Second)
	for !dirExists(filepath.Join(j.capsuleDir, "repos")) { // materialize under way
		if time.Now().After(deadline) {
			t.Fatal("the claim never reached its task-repo clone")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishFailed); err != nil {
		t.Fatalf("fail during CLAIMING: %v", err)
	}
	if err := recvErr(t, startErr, "Start"); !errors.Is(err, ErrClaimClosed) {
		t.Fatalf("Start answered %v", err)
	}
	if _, err := os.Stat(j.capsuleDir); !os.IsNotExist(err) {
		t.Fatalf("the closed claim's capsule was left in place (%v)", err)
	}
	matches, _ := filepath.Glob(j.capsuleDir + ".quarantined.*")
	if len(matches) == 0 {
		t.Fatal("the closed claim's capsule was not quarantined")
	}
}
