package runtime

// actor_fence_test.go — the doors and retries of the per-work-item actor
// keep the session fence and the claiming journal's durability: a close
// of a restored record verifies first, a start queued behind a restart's
// journal replay is not lost, a failed state write keeps the journal
// entry, a sealing retry that finds the session gone finishes the seal
// locally, and nothing recovers after the shutdown.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// A close of a record the restart could not verify yet pushes and ends
// nothing: it answers DAEMON-503 while the hub is unreachable, and once
// the hub answers that another session holds the work item it releases
// the record (DAEMON-409) — never a push or a SessionEnd under the dead
// session.
func TestCloseOfAnUnverifiedRecordVerifiesFirst(t *testing.T) {
	for _, reason := range []string{protocol.FinishWrapped, protocol.FinishPaused} {
		t.Run(reason, func(t *testing.T) {
			hub := newFakeHub()
			r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			r.cfg.EndRetryBase = time.Hour // the restart's own retry stays out of the way
			t.Cleanup(r.Close)
			const wi = "wi_unverified"
			rec := matrixRecord(r, wi, 0, "")
			bare := initGitCapsule(t, r.restoredJob(rec))
			rec.RemoteURL = bare
			pushes := filepath.Join(t.TempDir(), "pushes")
			if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte("#!/bin/sh\necho push >> "+pushes+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeStateFile(t, r, []*jobRecord{rec})
			hub.hold(wi, rec.SessionID, api.StatusRunning)
			hub.lookupErr[wi] = errLookupBoom
			if err := restartSettled(t, r); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			if err := r.Stop(context.Background(), wi, reason); !hubUnavailable(err) {
				t.Fatalf("a close before the verify answered = %v, want DAEMON-503", err)
			}
			hub.mu.Lock()
			delete(hub.lookupErr, wi)
			hub.mu.Unlock()
			hub.hold(wi, fakeSession("another-daemon:"+wi), api.StatusRunning)
			if err := r.Stop(context.Background(), wi, reason); !isConflictErr(err) {
				t.Fatalf("a close of a session the hub no longer holds = %v, want DAEMON-409", err)
			}
			if r.jobOf(wi) != nil {
				t.Fatal("the record must be released")
			}
			if n := countLines(t, pushes); n != 0 {
				t.Fatalf("%d push(es) under a session that was never verified", n)
			}
			if n := hub.endCount(); n != 0 {
				t.Fatalf("%d SessionEnd(s) under a session that was never verified: %+v", n, hub.lastEnd())
			}
		})
	}
}

// A start whose claim never reached the hub leaves a journal entry; the
// restart's replay finds it stale and drops it — a Start of the same work
// item queued behind that replay (the submit queue's) claims afresh
// instead of being refused as already active.
func TestStartBehindAStaleJournalReplayClaims(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_stale_replay"
	stale := fakeSession("never-landed:" + wi)
	if err := r.noteClaiming(wi, stale, nil); err != nil {
		t.Fatal(err)
	}
	hub.status[wi] = api.StatusQueued
	if err := r.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start behind the stale replay: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	ji, ok := r.Info(wi)
	if !ok || ji.State != StateRunning || ji.SessionID == stale {
		t.Fatalf("job = %+v (ok=%v), want a fresh running claim", ji, ok)
	}
	if n := hub.claimCount(); n != 1 {
		t.Fatalf("claims = %d, want the one fresh claim", n)
	}
}

// The claiming journal holds the only copy of a minted secret until the
// start's record is durable: a state write that fails keeps the entry.
func TestClaimKeepsJournalWhenStatePersistFails(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_persist_fails"
	hub.status[wi] = api.StatusQueued
	orig := writeStateFn
	writeStateFn = func(string, []byte, os.FileMode) error { return errors.New("disk full (scripted)") }
	t.Cleanup(func() { writeStateFn = orig })
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	e := r.loadClaiming()[wi]
	if ji, _ := r.Info(wi); e == nil || e.SessionID != ji.SessionID {
		t.Fatalf("journal entry = %+v, want it kept with the running session's secret", e)
	}
}

// A sealing record whose session the hub no longer holds finishes its
// seal locally (event seal_finished_locally, observe released) — it
// neither pushes nor ends under the dead session.
func TestSealingRecordOfAGoneSessionFinishesLocally(t *testing.T) {
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	kinds := tapEvents(r)
	const wi = "wi_seal_gone"
	rec := matrixRecord(r, wi, 0, "")
	rec.State, rec.StopReason = string(StateSealing), protocol.FinishWrapped
	initGitCapsule(t, r.restoredJob(rec))
	writeStateFile(t, r, []*jobRecord{rec})
	hub.forceEnd(wi)
	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitFor(t, "the seal to finish", func() bool { return r.jobOf(wi) == nil })
	if !slices.Contains(*kinds, "seal_finished_locally") {
		t.Fatalf("events %v, want seal_finished_locally", *kinds)
	}
	if m := recvRT(t, toOB); m.Kind != protocol.RTFinished || m.WorkItem != wi {
		t.Fatalf("observe must be released, got %+v", m)
	}
	if n := hub.endCount(); n != 0 {
		t.Fatalf("%d SessionEnd(s) under the gone session", n)
	}
	unseal(t, testWorkspace(r))
}

// After the shutdown a death signal recovers nothing: no push, no
// relaunch, no close — the record waits for the next start.
func TestNothingRecoversAfterTheShutdown(t *testing.T) {
	sp := &scriptedSpawner{}
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, sp)
	const wi = "wi_after_close"
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	installOldAgent(t, sp, j)
	before := sp.callCount()
	r.Close()
	r.handleOB(context.Background(), protocol.OBMsg{Kind: protocol.OBNeedsRecovery, SpawnN: j.spawnN, WorkItem: wi, Reason: protocol.RecoverDeath})
	if n := sp.callCount(); n != before {
		t.Fatalf("a recovery after the shutdown spawned %d agent(s)", n-before)
	}
	if st, _ := j.snapshot(); st != StateRunning || hub.endCount() != 0 {
		t.Fatalf("state %s, %d ends: the record must stay as it was", st, hub.endCount())
	}
}
