package runtime

// endpending_push_test.go — repo review 2026-10-16 B-2 stage 1: an
// end_pending close pushes the local chain BEFORE its SessionEnd, on the
// same retry. boundedFail commits the dirty capsule first; when that
// push failed (the outage that parked the close), the commits used to
// stay local forever — the retry only re-sent SessionEnd.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestEndPendingPushesTheChainBeforeTheClose(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
	t.Cleanup(r.Close)
	const wi = "wi_b2push"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	hub.hold(wi, j.sessionID, api.StatusRunning)
	local := gitHead(t, j.git.GitDir)

	// the git face is unreachable while the API answers: the close must
	// NOT be sent before the chain is pushed
	good := j.git.RemoteURL
	j.mu.Lock()
	j.git.RemoteURL = filepath.Join(t.TempDir(), "unreachable.git")
	j.state = StateEndPending
	j.endNote = "bounded fail"
	j.mu.Unlock()
	underLock(r, wi, func() { r.retry(j) })
	time.Sleep(80 * time.Millisecond)
	if n := hub.endCount(); n != 0 {
		t.Fatalf("the blocked close went out before the chain push landed (%d ends)", n)
	}
	if _, ok := r.Info(wi); !ok {
		t.Fatal("the end_pending record must be kept while the push fails")
	}

	// the git face is back: push, then close
	j.mu.Lock()
	j.git.RemoteURL = good
	j.mu.Unlock()
	waitFor(t, "the end_pending close to settle", func() bool {
		_, ok := r.Info(wi)
		return !ok
	})
	waitRetryIdle(t, r)
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "refs/heads/main").Output()
	if err != nil || strings.TrimSpace(string(out)) != local {
		t.Fatalf("the hub chain = %q (%v), want the local commit %s", strings.TrimSpace(string(out)), err, local)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("the blocked close must land once, got %d ends (%+v)", hub.endCount(), hub.lastEnd())
	}
}

// A push that fails because the session no longer holds the WI (its
// credential is dead) cannot ever land: the close is settled without it.
func TestEndPendingPushFailureWithSessionGoneSettles(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_b2gone"
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	hub.hold(wi, "another-session", api.StatusRunning)
	j.mu.Lock()
	j.git.RemoteURL = filepath.Join(t.TempDir(), "unreachable.git")
	j.state = StateEndPending
	j.mu.Unlock()
	if !endPendingAttempt(r, j) {
		t.Fatal("a session that no longer holds the WI must settle the end_pending close")
	}
	if _, ok := r.Info(wi); ok {
		t.Fatal("the settled record must be dropped")
	}
}

// waitRetryIdle waits until no actor has work (the settling attempt's
// trailing persist included), so the test's temp dirs are quiet.
func waitRetryIdle(t *testing.T, r *Runtime) {
	t.Helper()
	waitFor(t, "the actors to finish", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.boxes) == 0
	})
}

// endPendingAttempt runs one attempt of an end_pending job's close on its
// actor (reconcile's end_pending step); true = settled (record dropped).
func endPendingAttempt(r *Runtime, j *job) (settled bool) {
	underLock(r, j.wi, func() {
		if settled = r.closeBlocked(j, true); settled {
			r.settleEndPending(j)
		}
	})
	return settled
}
