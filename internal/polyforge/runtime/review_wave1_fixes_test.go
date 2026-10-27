package runtime

// review_wave1_fixes_test.go — the review of the 2026-10-16 repo-review
// fixes: the first blocked close pushes before it closes, Restart never
// blocks on an end_pending close, a permanent push rejection stops the
// push and closes, a restart after the shutdown starts nothing, a record
// whose session another daemon holds is released, and a refused
// replay of an open session ends it instead of dropping its only secret.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func fastRetry(r *Runtime) {
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
}

// Item 1: boundedFail's own push failed (hub blip on the git face) — the
// FIRST blocked close must not go out before the chain lands.
func TestFirstBlockedCloseWaitsForTheChainPush(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	fastRetry(r)
	t.Cleanup(r.Close)
	const wi = "wi_rf_first"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	hub.hold(wi, j.sessionID, api.StatusRunning)
	if err := os.WriteFile(filepath.Join(j.capsuleDir, "work.txt"), []byte("late work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := j.git.RemoteURL
	j.mu.Lock()
	j.git.RemoteURL = filepath.Join(t.TempDir(), "unreachable.git")
	j.mu.Unlock()

	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	if n := hub.endCount(); n != 0 {
		t.Fatalf("SessionEnd(blocked) landed with the chain unpushed (%d ends)", n)
	}
	if ji, ok := r.Info(wi); !ok || ji.State != StateEndPending {
		t.Fatalf("job = %+v (ok=%v), want end_pending", ji, ok)
	}
	local := gitHead(t, j.git.GitDir)

	j.mu.Lock()
	j.git.RemoteURL = good
	j.mu.Unlock()
	waitFor(t, "the blocked close to settle", func() bool { _, ok := r.Info(wi); return !ok })
	waitRetryIdle(t, r)
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "refs/heads/main").Output()
	if err != nil || strings.TrimSpace(string(out)) != local {
		t.Fatalf("hub chain = %q (%v), want %s", strings.TrimSpace(string(out)), err, local)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("ends = %d (%+v), want one blocked close", hub.endCount(), hub.lastEnd())
	}
}

// Item 2: Restart runs before the socket serves — an end_pending
// record must only schedule its retry, so a hung hub cannot hang startup.
func TestRestartDoesNotBlockOnEndPendingClose(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	fastRetry(r)
	const wi = "wi_rf_hang"
	block := make(chan struct{})
	hub.endBlock = block
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StateEndPending)
	rec.EndNote = "boom"
	writeStateFile(t, r, []*jobRecord{rec})
	t.Cleanup(func() {
		close(block)
		r.Close()
		waitRetryIdle(t, r)
	})

	done := make(chan error, 1)
	go func() { done <- r.Restart(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Restart: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Restart blocked on the end_pending close against a hung hub")
	}
}

// Item 3: a PERMANENT chain push rejection (the hook refuses) stops the
// push, closes without it and raises push_failed with the local head.
func TestBlockedClosePermanentPushRejectionClosesWithoutIt(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	var mu sync.Mutex
	var events []map[string]any
	r.cfg.EventTap = func(_ string, kind string, body map[string]any) {
		if kind == "push_failed" {
			mu.Lock()
			events = append(events, body)
			mu.Unlock()
		}
	}
	const wi = "wi_rf_perm"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'aihub: commit is missing an integer Polyforge-Seq trailer' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, j.sessionID, api.StatusRunning)
	j.mu.Lock()
	j.state = StateEndPending
	j.endNote = "boom"
	j.mu.Unlock()
	local := gitHead(t, j.git.GitDir)

	if !endPendingAttempt(r, j) {
		t.Fatal("a permanent push rejection must not keep the close pending")
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("ends = %d (%+v), want the blocked close", hub.endCount(), hub.lastEnd())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 || events[0]["local_head"] != local || events[0]["permanent"] != true {
		t.Fatalf("push_failed events = %v, want one permanent with local_head %s", events, local)
	}
	if out, err := exec.Command("git", "--git-dir", j.git.GitDir, "cat-file", "-e", local).CombinedOutput(); err != nil {
		t.Fatalf("the local commit must stay in the capsule: %v %s", err, out)
	}
}

// Item 3, transient side: an unreachable remote keeps the close pending.
func TestBlockedCloseTransientPushFailureKeepsRetrying(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_rf_trans"
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	hub.hold(wi, j.sessionID, api.StatusRunning)
	j.mu.Lock()
	j.git.RemoteURL = filepath.Join(t.TempDir(), "unreachable.git")
	j.state = StateEndPending
	j.mu.Unlock()
	for i := 0; i < 3; i++ {
		if endPendingAttempt(r, j) {
			t.Fatal("a transient push failure must keep the close pending")
		}
	}
	if hub.endCount() != 0 {
		t.Fatalf("the close went out without the chain (%d ends)", hub.endCount())
	}
}

// Item 4: a restart after the shutdown began recovers and spawns
// nothing; the record persists for the next start.
func TestRestartAfterShutdownStartsNothing(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	fastRetry(r)
	const wi = "wi_rf_shutdown"
	rec := matrixRecord(r, wi, 0, "")
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	writeStateFile(t, r, []*jobRecord{rec})
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r.Close() // shutdown
	if err := restartSettled(t, r); err != nil {
		t.Fatal(err)
	}
	waitRetryIdle(t, r)
	if sp.callCount() != 0 {
		t.Fatalf("nothing may spawn during shutdown, got %d spawns", sp.callCount())
	}
	if err := r.persist(); err != nil {
		t.Fatal(err)
	}
	if recs := loadRecords(t, r); len(recs) != 1 || recs[0].SessionID != rec.SessionID {
		t.Fatalf("the record must persist for the next restart, got %+v", recs)
	}
}

// Item 11: a running record whose session another daemon holds is
// released at restart (quarantine, record dropped, session_released) —
// no spawn, no push, no hub transition.
func TestRestartNonMatchingReleases(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	fastRetry(r)
	t.Cleanup(r.Close)
	var mu sync.Mutex
	var kinds []string
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		mu.Lock()
		kinds = append(kinds, kind)
		mu.Unlock()
	}
	const wi = "wi_rf_taken"
	rec := matrixRecord(r, wi, 0, "")
	hub.hold(wi, fakeSession("another-daemon"), api.StatusRunning)
	bare := initGitCapsule(t, r.restoredJob(rec))
	rec.RemoteURL = bare
	writeStateFile(t, r, []*jobRecord{rec})
	if err := restartSettled(t, r); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the verify to release the job", func() bool { _, ok := r.Info(wi); return !ok })
	waitRetryIdle(t, r)
	if sp.callCount() != 0 {
		t.Fatalf("a WI held elsewhere must not spawn here, got %d", sp.callCount())
	}
	if head := gitRefOrEmpty(bare); head != "" {
		t.Fatalf("a WI held elsewhere must not push here, the hub chain moved to %s", head)
	}
	if m, _ := filepath.Glob(rec.CapsuleDir + ".quarantined.*"); len(m) != 1 {
		t.Fatalf("the foreign scene must be quarantined, got %v", m)
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("the record must be dropped, got %+v", recs)
	}
	if hub.endCount() != 0 {
		t.Fatal("no hub transition on release")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(kinds, ","), "session_released") {
		t.Fatalf("events = %v, want session_released", kinds)
	}
}

// Item 5: the retry of a lost claim answer finds the session open (verify
// matches) but the replayed claim is REFUSED — the open session is ended
// blocked with the secret we hold, never left behind by dropping it.
func TestStartRefusedReplayOfOpenSessionEndsIt(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_rf_replay"
	session := fakeSession(wi)
	if err := r.noteClaiming(wi, session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, session, api.StatusRunning)
	refusal := &api.Error{Code: api.ErrCoreConflict, Detail: "NOT_CLAIMABLE"}
	hub.claimFaults = []claimFault{{err: refusal}}

	if err := r.Start(context.Background(), wi, StartOpts{}); err == nil {
		t.Fatal("the refused replay must surface")
	}
	if hub.endCount() != 1 || hub.lastEnd().SessionID != session || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("the open session must be ended blocked with our secret, got %d ends (%+v)", hub.endCount(), hub.lastEnd())
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the journal settles once the close landed, got %+v", items)
	}
}

func TestRestartRefusedReplayOfOpenSessionEndsIt(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_rf_replay2"
	session := fakeSession(wi)
	if err := r.noteClaiming(wi, session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, session, api.StatusRunning)
	hub.claimFaults = []claimFault{{err: &api.Error{Code: api.ErrCoreConflict, Detail: "NOT_CLAIMABLE"}}}

	replayClaims(t, r)
	if hub.endCount() != 1 || hub.lastEnd().SessionID != session || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("the open session must be ended blocked with our secret, got %d ends (%+v)", hub.endCount(), hub.lastEnd())
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the journal settles once the close landed, got %+v", items)
	}
}
