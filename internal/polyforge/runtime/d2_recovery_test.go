package runtime

// D2 recovery-closure regressions (v2-converge §6 bugs 1/4/5, quarantine
// atomicity, §7 cross-daemon continue guard).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Bug 4: the consecutive-failures bound (and every other boundedFail
// caller) kills the live agent tree and commits+pushes the last local work
// BEFORE the verdict, and the blocked verdict carries the pushed head.
func TestBoundedFailKillsAgentAndPushesDirtyWork(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	j := seedJob(r, "wi_d2_commit")
	bare := initGitCapsule(t, j)
	writeExecutionState(t, j.capsuleDir, "running")
	h := installOldAgent(t, sp, j)
	pid := h.PID()
	// Arm the single recovery ledger (C7) so this recovery re-entry
	// bounded-fails: the counter is pre-spent to the bound.
	j.mu.Lock()
	j.recoverFails = r.cfg.MaxRecoveries
	j.mu.Unlock()
	if err := os.WriteFile(filepath.Join(j.capsuleDir, "last-work.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.recoverOp(context.Background(), j, "agent_death", "x")
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("expected RTFinished(blocked), got %+v", msg)
	}
	waitDead(t, pid)
	if !bareHasMain(t, bare) {
		t.Fatal("the dirty capsule must be pushed to aihub before the WI is blocked")
	}
	head, err := gitops.Head(j.git)
	if err != nil || head == "" {
		t.Fatalf("head: %q %v", head, err)
	}
	if got := hub.lastEnd().HeadSHA; got != head {
		t.Fatalf("the blocked verdict must carry the sealed head %s, got %q", head, got)
	}
}

// Bug 4, missing-capsule branch: no live driver may survive the verdict
// (there is nothing to commit, and the commit step must not choke).
func TestBoundedFailMissingCapsuleKillsAgent(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	j := seedJob(r, "wi_d2_missing")
	h := installOldAgent(t, sp, j)
	pid := h.PID()
	if err := os.RemoveAll(j.capsuleDir); err != nil {
		t.Fatal(err)
	}
	r.recoverOp(context.Background(), j, "daemon_restart", "x")
	if msg := recvRT(t, toOB); msg.Reason != protocol.FinishBlocked {
		t.Fatalf("expected blocked, got %+v", msg)
	}
	waitDead(t, pid)
	if hub.lastEnd().HeadSHA != "" {
		t.Fatalf("no capsule, no head: %+v", hub.lastEnd())
	}
}

func endPendingRuntime(t *testing.T, hub *fakeHub) (*Runtime, *job, *supervise.Queue[protocol.RTMsg]) {
	t.Helper()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
	t.Cleanup(r.Close)
	j := seedJob(r, "wi_d2_end")
	hub.hold(j.wi, j.sessionID, api.StatusRunning)
	if err := j.transition(StateRecovering); err != nil { // boundedFail's real entry state
		t.Fatal(err)
	}
	return r, j, toOB
}

// Bug 5: a SessionEnd failure keeps the record end_pending (persisted with
// the secret) and retries with backoff until the hub accepts; only then is
// the record dropped.
func TestBoundedFailSessionEndFailureKeepsRecordAndRetries(t *testing.T) {
	hub := newFakeHub()
	r, j, toOB := endPendingRuntime(t, hub)
	hub.end[j.wi] = fmt.Errorf("aihub unreachable (scripted)")

	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("observe must be released blocked, got %+v", msg)
	}
	if r.jobOf(j.wi) == nil {
		t.Fatal("an unacknowledged blocked close must keep the job record")
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StateEndPending) || recs[0].SessionID != j.sessionID || recs[0].EndNote != "boom" {
		t.Fatalf("the durable record must be end_pending carrying secret+note, got %+v", recs)
	}
	time.Sleep(60 * time.Millisecond) // a few failed retries
	if r.jobOf(j.wi) == nil {
		t.Fatal("record dropped while the hub is still unreachable")
	}

	hub.mu.Lock()
	delete(hub.end, j.wi)
	hub.mu.Unlock()
	waitFor(t, "the retry to land the blocked close", func() bool { return r.jobOf(j.wi) == nil })
	if hub.lastEnd().Status != api.StatusBlocked || hub.lastEnd().Note != "boom" {
		t.Fatalf("retry must send the recorded verdict, got %+v", hub.lastEnd())
	}
	waitFor(t, "state.json to drop the record", func() bool { return len(loadRecords(t, r)) == 0 })
}

// Bug 5, daemon-start half: a restarted daemon retries an end_pending
// record's close (on the background lane).
func TestRestartRetriesEndPendingRecord(t *testing.T) {
	hub := newFakeHub()
	r, j, _ := endPendingRuntime(t, hub)
	hub.end[j.wi] = fmt.Errorf("aihub unreachable (scripted)")
	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	r.Close()

	// fresh daemon over the same DataDir, hub reachable again
	r2, toOB2, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r2.cfg = r.cfg
	r2.statePath = r.statePath
	r2.cfg.EndRetryBase = 10 * time.Millisecond
	t.Cleanup(r2.Close)
	hub.mu.Lock()
	delete(hub.end, j.wi)
	hub.mu.Unlock()
	if err := restartSettled(t, r2); err != nil {
		t.Fatal(err)
	}
	_ = toOB2
	// the restart only schedules the retry (never a synchronous push
	// before the socket serves): wait for the lane
	waitFor(t, "the end_pending close to be replayed", func() bool { return hub.endCount() > 0 })
	waitRetryIdle(t, r2)
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("restart must replay the blocked close, got %+v", hub.lastEnd())
	}
	if r2.jobOf(j.wi) != nil || len(loadRecords(t, r2)) != 0 {
		t.Fatal("the settled end_pending record must be dropped")
	}
}

// A close whose answer was lost (the hub already ended our session) is
// settled by the verify fallback — never retried forever.
func TestEndPendingSettlesWhenSessionNoLongerHoldsWI(t *testing.T) {
	hub := newFakeHub()
	r, j, _ := endPendingRuntime(t, hub)
	hub.end[j.wi] = fmt.Errorf("answer lost (scripted)")
	hub.mu.Lock()
	hub.held[j.wi] = "" // the close landed server-side
	hub.status[j.wi] = api.StatusBlocked
	hub.mu.Unlock()
	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	if r.jobOf(j.wi) != nil {
		t.Fatal("a session that no longer holds the WI settles the close")
	}
}

// Bug 1: the clone-failure compensation is not one-shot — the session is
// kept as an end_pending job (persisted before the journal entry goes),
// and the SessionEnd(blocked) is retried until it lands.
func TestAbandonClaimRetriesSessionEnd(t *testing.T) {
	hub := newFakeHub()
	r, j, _ := endPendingRuntime(t, hub)
	if err := r.noteClaiming(j.wi, j.sessionID, nil); err != nil {
		t.Fatal(err)
	}
	hub.end[j.wi] = fmt.Errorf("aihub unreachable (scripted)")
	orig := fmt.Errorf("capsule clone failed")
	var got error
	underLock(r, j.wi, func() { got = r.abandonClaim(j, orig, "clone") })
	if got != orig {
		t.Fatalf("the original error must be returned, got %v", got)
	}
	if recs := loadRecords(t, r); len(recs) != 1 || recs[0].State != string(StateEndPending) || recs[0].SessionID != j.sessionID {
		t.Fatalf("the unacknowledged close must be kept end_pending in state.json, got %+v", recs)
	}
	hub.mu.Lock()
	delete(hub.end, j.wi)
	hub.mu.Unlock()
	waitFor(t, "the end_pending job to settle", func() bool { return r.jobOf(j.wi) == nil })
	if _, ok := r.loadClaiming()[j.wi]; ok {
		t.Fatal("the journal entry outlived the compensation")
	}
	if hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("expected the blocked close, got %+v", hub.lastEnd())
	}
}

// Quarantine is all-or-nothing: a failed worktree move puts the git-dir back.
func TestQuarantineCapsuleRollsBackOnSecondMoveFailure(t *testing.T) {
	base := t.TempDir()
	capDir := filepath.Join(base, "capsules", "wi_q")
	gitDir := filepath.Join(base, "git", "wi_q.git")
	for _, d := range []string{capDir, gitDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	orig := renameFn
	defer func() { renameFn = orig }()
	renameFn = func(from, to string) error {
		if from == capDir {
			return fmt.Errorf("injected EXDEV")
		}
		return os.Rename(from, to)
	}
	err := quarantineCapsule(capDir, gitDir)
	if err == nil || !strings.Contains(err.Error(), "git-dir restored") {
		t.Fatalf("expected a restored-failure error, got %v", err)
	}
	if !dirExists(gitDir) || !dirExists(capDir) {
		t.Fatal("the scene must be whole again (git-dir back, worktree untouched)")
	}
	ents, _ := os.ReadDir(filepath.Dir(gitDir))
	if len(ents) != 1 {
		t.Fatalf("no quarantined leftovers expected, got %v", ents)
	}

	// and the success path still moves both under one timestamp
	renameFn = os.Rename
	if err := quarantineCapsule(capDir, gitDir); err != nil {
		t.Fatal(err)
	}
	if dirExists(gitDir) || dirExists(capDir) {
		t.Fatal("both must have moved aside")
	}
}

// The restore itself failing is reported (split scene named, never silent).
func TestQuarantineCapsuleReportsFailedRestore(t *testing.T) {
	base := t.TempDir()
	capDir := filepath.Join(base, "c")
	gitDir := filepath.Join(base, "g")
	_ = os.MkdirAll(capDir, 0o755)
	_ = os.MkdirAll(gitDir, 0o755)
	orig := renameFn
	defer func() { renameFn = orig }()
	renameFn = func(from, to string) error {
		if from == capDir || strings.Contains(from, ".quarantined.") {
			return fmt.Errorf("injected")
		}
		return os.Rename(from, to)
	}
	err := quarantineCapsule(capDir, gitDir)
	if err == nil || !strings.Contains(err.Error(), "split the scene") {
		t.Fatalf("expected the split-scene report, got %v", err)
	}
}

// §7: a continue refuses a clone whose head is not the sealing daemon's
// recorded head unless the sealing machine finds it in a quarantined
// scene; a match proceeds.
func TestVerifySealedHead(t *testing.T) {
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	_ = toOB
	j := seedJob(r, "wi_d2_head")
	initGitCapsule(t, j)
	head, err := gitops.Head(j.git)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		c       api.SessionClaim
		wantErr bool
	}{
		{"no seal recorded", api.SessionClaim{}, false},
		{"same machine, head matches", api.SessionClaim{SealedHead: head, SealedMachine: "m-test"}, false},
		{"same machine, head nowhere on this machine", api.SessionClaim{SealedHead: "deadbeef", SealedMachine: "m-test"}, true},
		{"other machine, head matches", api.SessionClaim{SealedHead: head, SealedMachine: "m-other"}, false},
		{"other machine, stale clone", api.SessionClaim{SealedHead: "deadbeef", SealedMachine: "m-other"}, true},
	}
	for _, tc := range cases {
		err := r.verifySealedHead(context.Background(), j, tc.c)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), api.ErrDaemonSealedHeadMismatch) {
			t.Fatalf("%s: the refusal must be typed %s, got %v", tc.name, api.ErrDaemonSealedHeadMismatch, err)
		}
	}
}

// An end_pending record whose capsule is gone still retries its SessionEnd at
// restart (the close needs no capsule — it is never re-blocked with a
// missing-capsule reason of its own), and the retry settles the close.
func TestEndPendingWithGoneCapsuleStillRetries(t *testing.T) {
	hub := newFakeHub()
	r, j, _ := endPendingRuntime(t, hub)
	hub.end[j.wi] = fmt.Errorf("aihub unreachable (scripted)")
	underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "boom") })
	r.Close()
	if j.capsuleDir != "" {
		_ = os.RemoveAll(j.capsuleDir)
	}

	r2, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r2.cfg = r.cfg
	r2.statePath = r.statePath
	r2.cfg.EndRetryBase = 10 * time.Millisecond
	t.Cleanup(r2.Close)
	hub.mu.Lock()
	delete(hub.end, j.wi)
	hub.mu.Unlock()
	if err := restartSettled(t, r2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the end_pending close to be replayed", func() bool { return hub.endCount() > 0 })
	waitRetryIdle(t, r2)
	if hub.endCount() < 1 || hub.lastEnd().Status != api.StatusBlocked || hub.lastEnd().Note != "boom" {
		t.Fatalf("the blocked close must still be retried, got %+v", hub.lastEnd())
	}
}

// writeExecutionState seeds a minimal in-flight execution.json in capDir.
func writeExecutionState(t *testing.T, capDir, workflowStatus string) {
	t.Helper()
	dir := filepath.Join(capDir, "runtime-state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := `{"work_item_id":"wi","current_step":"grill","workflow_status":"` +
		workflowStatus + `","bundle_digest":"d0","steps":{"grill":{"status":"in_progress"}}}`
	if err := os.WriteFile(filepath.Join(dir, "execution.json"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A close whose seal push the hub permanently refuses degrades to
// blocked; that blocked verdict carries the local chain head, so the hub
// records the blocked seal and a continue on another machine — whose
// clone lacks the unpushed commit — is refused instead of silently
// dropping this machine's work.
func TestDegradedSealCarriesHeadAndGuardsOtherMachine(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	a, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_seal_degraded_head"
	startRunning(t, a, wi)
	j := a.jobOf(wi)
	if j == nil {
		t.Fatal("no job after Start")
	}
	base := hub.capsuleRepoBase
	bare := filepath.Join(base, wi+".git")
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho refused >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.capsuleDir, "unpushed.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := a.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	head, err := gitops.Head(j.git)
	if err != nil || head == "" {
		t.Fatalf("local head: %q %v", head, err)
	}
	end := hub.lastEnd()
	if end.Status != api.StatusBlocked || end.HeadSHA != head {
		t.Fatalf("the degraded close = %+v, want blocked carrying the local head %s", end, head)
	}

	b, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	b.cfg.MachineID = "m-other"
	hub.mu.Lock()
	hub.capsuleRepoBase = base // the same hub's capsule repos
	hub.mu.Unlock()
	b.cfg.GitRemoteBase = base
	t.Cleanup(func() { terminateJobAgent(b, wi) })
	err = b.Start(context.Background(), wi, StartOpts{})
	if err == nil || !strings.Contains(err.Error(), api.ErrDaemonSealedHeadMismatch) {
		t.Fatalf("a continue on another machine from the stale hub head = %v, want %s", err, api.ErrDaemonSealedHeadMismatch)
	}
}
