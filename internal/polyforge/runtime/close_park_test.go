package runtime

// A close whose old driver Run outlives the handle's bounded teardown must
// not write the capsule next to it (the double-writer class the recovery
// park closed for relaunches): the seal push, the commit of the dirty
// capsule, the task-repo WIP lane, the read-only seal and the SessionEnd
// all wait. The close parks in its persisted state, the operator gets a
// 409 that says so, and the close finishes by itself once that Run exits.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// closeParkRig is one job held by a driver Run that ignores its
// cancellation, over a git capsule whose bare remote shows any push.
type closeParkRig struct {
	r    *Runtime
	hub  *fakeHub
	toOB *supervise.Queue[protocol.RTMsg]
	j    *job
	old  *parkedRunHandle
	bare string

	mu     sync.Mutex
	parked []map[string]any
}

func newCloseParkRig(t *testing.T, wi string) *closeParkRig {
	t.Helper()
	origBound := cleanupBound
	cleanupBound = 50 * time.Millisecond
	t.Cleanup(func() { cleanupBound = origBound })

	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	k := &closeParkRig{r: r, hub: hub, toOB: toOB}
	r.cfg.EventTap = func(_ string, kind string, body map[string]any) {
		if kind != "close_parked" {
			return
		}
		k.mu.Lock()
		k.parked = append(k.parked, body)
		k.mu.Unlock()
	}
	k.j = seedJob(r, wi)
	k.bare = initGitCapsule(t, k.j)
	t.Cleanup(func() { unseal(t, k.j.capsuleDir) })
	hub.hold(wi, k.j.sessionID, api.StatusRunning)
	k.old = newParkedRunHandle()
	t.Cleanup(k.old.release)
	k.j.mu.Lock()
	k.j.agent = k.old
	k.j.agentPID = 0 // the driver bridge shape
	k.j.mu.Unlock()
	// the old Run's last, still uncommitted output
	if err := os.WriteFile(filepath.Join(k.j.capsuleDir, "late-work.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return k
}

func (k *closeParkRig) parkedEvents() []map[string]any {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]map[string]any(nil), k.parked...)
}

// assertNoCapsuleWrite: nothing reached the hub (no push, no SessionEnd),
// no commit landed on top of the seed, and the scene is still writable.
func (k *closeParkRig) assertNoCapsuleWrite(t *testing.T, what string) {
	t.Helper()
	if n := k.hub.endCount(); n != 0 {
		t.Fatalf("%s: %d SessionEnd call(s) while the old Run is alive (last %+v)", what, n, k.hub.lastEnd())
	}
	if bareHasMain(t, k.bare) {
		t.Fatalf("%s: the capsule was pushed while the old Run is alive", what)
	}
	if n := commitCount(t, k.j); n != 1 {
		t.Fatalf("%s: %d commits in the capsule (want only the seed): a commit ran next to the live Run", what, n)
	}
	if info, err := os.Stat(k.j.capsuleDir); err != nil || info.Mode().Perm()&0o200 == 0 {
		t.Fatalf("%s: the capsule was sealed read-only next to the live Run (%v, %v)", what, info, err)
	}
}

func commitCount(t *testing.T, j *job) int {
	t.Helper()
	out, err := capsuleGit(j, "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v: %s", err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("rev-list count %q: %v", out, err)
	}
	return n
}

// capsuleGit runs one git command in j's capsule (its separate git dir).
func capsuleGit(j *job, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = j.git.Env()
	cmd.Dir = j.capsuleDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func assertParked409(t *testing.T, err error, what string) {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "close is parked") {
		t.Fatalf("%s: want the DAEMON-409 naming the parked close, got %v", what, err)
	}
}

func waitRT(t *testing.T, q *supervise.Queue[protocol.RTMsg], reason string) {
	t.Helper()
	for {
		m := recvRT(t, q)
		if m.Kind == protocol.RTFinished {
			if m.Reason != reason {
				t.Fatalf("RTFinished(%s), want %s", m.Reason, reason)
			}
			return
		}
	}
}

// TestStopParksOnLiveRunThenSeals: `pf wrap` on a work item whose driver
// Run ignores the teardown parks the close in SEALING (nothing written)
// and seals once the Run exits.
func TestStopParksOnLiveRunThenSeals(t *testing.T) {
	k := newCloseParkRig(t, "wi_close_park_wrap")

	err := k.r.Stop(context.Background(), k.j.wi, protocol.FinishWrapped)
	assertParked409(t, err, "the close of a live Run")
	if st, _ := k.j.snapshot(); st != StateSealing {
		t.Fatalf("parked close state %q, want %q", st, StateSealing)
	}
	if recs := loadRecords(t, k.r); len(recs) != 1 || recs[0].State != string(StateSealing) || recs[0].StopReason != protocol.FinishWrapped {
		t.Fatalf("the parked close must stay persisted as a %s close in SEALING, got %+v", protocol.FinishWrapped, recs)
	}
	if ev := k.parkedEvents(); len(ev) != 1 || ev[0]["reason"] != protocol.FinishWrapped {
		t.Fatalf("the park must be named by one close_parked event, got %v", ev)
	}
	// a second close is answered with the same park, not a second attempt
	assertParked409(t, k.r.Stop(context.Background(), k.j.wi, protocol.FinishFailed), "a repeated close")
	time.Sleep(4 * cleanupBound)
	k.assertNoCapsuleWrite(t, "parked close")

	k.old.release()
	waitFor(t, "the parked close to finish after the Run exited", func() bool { return k.r.jobOf(k.j.wi) == nil })
	waitRT(t, k.toOB, protocol.FinishWrapped)
	if n := k.hub.endCount(); n != 1 || k.hub.lastEnd().Status != protocol.FinishWrapped {
		t.Fatalf("the resumed close must end the session once as %s: %d SessionEnd, last %+v", protocol.FinishWrapped, n, k.hub.lastEnd())
	}
	if !bareHasMain(t, k.bare) {
		t.Fatal("the resumed close never pushed the sealed capsule")
	}
	if n := commitCount(t, k.j); n < 2 {
		t.Fatalf("the resumed close never committed the dirty capsule (%d commits)", n)
	}
}

// TestPauseParksOnLiveRunThenPauses: `pf pause` keeps the PAUSED record
// but sends nothing (no seal push, no WIP, no SessionEnd) until the Run
// exits; `pf resume` on the parked record is refused the same way.
func TestPauseParksOnLiveRunThenPauses(t *testing.T) {
	k := newCloseParkRig(t, "wi_close_park_pause")

	err := k.r.Stop(context.Background(), k.j.wi, protocol.FinishPaused)
	assertParked409(t, err, "the pause of a live Run")
	if st, _ := k.j.snapshot(); st != StatePaused {
		t.Fatalf("parked pause state %q, want %q", st, StatePaused)
	}
	if _, rerr := k.r.ResumePaused(context.Background(), k.j.wi); rerr == nil {
		t.Fatal("a resume of the parked pause must be refused")
	} else {
		assertParked409(t, rerr, "the resume of a parked pause")
	}
	time.Sleep(4 * cleanupBound)
	k.assertNoCapsuleWrite(t, "parked pause")

	k.old.release()
	waitRT(t, k.toOB, protocol.FinishPaused)
	if n := k.hub.endCount(); n != 1 || k.hub.lastEnd().Status != api.StatusPaused {
		t.Fatalf("the resumed pause must end the session once as paused: %d SessionEnd, last %+v", n, k.hub.lastEnd())
	}
	if !bareHasMain(t, k.bare) {
		t.Fatal("the resumed pause never pushed the capsule")
	}
	if k.r.jobOf(k.j.wi) != k.j || k.j.closeParked() {
		t.Fatal("the paused record must stay retained with no park left")
	}
}

// TestBoundedFailParksOnLiveRunThenBlocks: the blocked verdict (budget
// exhaustion, a stall the driver ignores) commits, saves WIP and ends the
// session only after the Run exits.
func TestBoundedFailParksOnLiveRunThenBlocks(t *testing.T) {
	k := newCloseParkRig(t, "wi_close_park_blocked")
	if err := k.j.transition(StateRecovering); err != nil { // boundedFail's real entry state
		t.Fatal(err)
	}

	underLock(k.r, k.j.wi, func() { k.r.boundedFail(k.r.ctx, k.j, "recovery budget exhausted (test)") })
	if st, _ := k.j.snapshot(); st != StateRecovering {
		t.Fatalf("parked verdict state %q, want %q", st, StateRecovering)
	}
	if ev := k.parkedEvents(); len(ev) != 1 || ev[0]["reason"] != protocol.FinishBlocked {
		t.Fatalf("the park must be named by one close_parked event, got %v", ev)
	}
	assertParked409(t, k.r.Stop(context.Background(), k.j.wi, protocol.FinishWrapped), "a close over the parked verdict")
	time.Sleep(4 * cleanupBound)
	k.assertNoCapsuleWrite(t, "parked blocked verdict")

	k.old.release()
	waitRT(t, k.toOB, protocol.FinishBlocked)
	waitFor(t, "the parked verdict to settle", func() bool { return k.r.jobOf(k.j.wi) == nil })
	if n := k.hub.endCount(); n != 1 || k.hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("the resumed verdict must end the session once as blocked: %d SessionEnd, last %+v", n, k.hub.lastEnd())
	}
	head, _ := capsuleGit(k.j, "rev-parse", "HEAD")
	if got := k.hub.lastEnd().HeadSHA; got == "" || got != strings.TrimSpace(head) {
		t.Fatalf("the blocked verdict must carry the committed head %q, got %q", strings.TrimSpace(head), got)
	}
}
