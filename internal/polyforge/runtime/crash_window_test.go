package runtime

// crash_window_test.go: kill -9 windows of the runtime. Each test builds
// the on-disk state a daemon killed at that exact point leaves (or kills
// the in-process state there), restarts from disk against the fake hub
// and asserts convergence.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// TestRestartClearsIndexLockLeftByKilledCommit (CR-3): the daemon died
// inside a git commit, leaving a fresh index.lock. The restart reaps the
// WI's orphans, removes the lock and the recovery's commit-dirty push
// lands — the lock's age does not decide anything.
func TestRestartClearsIndexLockLeftByKilledCommit(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	wi := "wi_cr3_lock"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	r.mu.Lock()
	delete(r.jobs, wi) // the restart seeds from state.json
	r.mu.Unlock()

	rec := matrixRecord(r, wi, 0, "")
	rec.RemoteURL = bare
	writeStateFile(t, r, []*jobRecord{rec})
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	if err := os.WriteFile(filepath.Join(rec.CapsuleDir, "dirty.txt"), []byte("unflushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(rec.GitDir, "index.lock")
	if err := os.WriteFile(lock, []byte("torn index"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for sp.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sp.callCount() == 0 {
		t.Fatal("the restart did not recover the work item")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("the dead commit's index.lock must be gone after the reap: %v", err)
	}
	out, err := exec.Command("git", "--git-dir", bare, "log", "--name-only", "--format=%s", "main").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "dirty.txt") {
		t.Fatalf("the recovery's commit-dirty push must land; hub log:\n%s", out)
	}
}

// separateGitScene builds a worktree with an external git-dir, the
// production capsule layout.
func separateGitScene(t *testing.T) (capDir, gitDir string) {
	t.Helper()
	base := t.TempDir()
	capDir = filepath.Join(base, "capsules", "wi_q6")
	gitDir = filepath.Join(base, "git", "wi_q6.git")
	if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", "--separate-git-dir="+gitDir, capDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return capDir, gitDir
}

// gitDirOf answers the git-dir git resolves for a worktree.
func gitDirOf(t *testing.T, worktree string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--absolute-git-dir")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(worktree))
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	p, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	return p
}

func quarantinedSibling(t *testing.T, path string) string {
	t.Helper()
	m, _ := filepath.Glob(path + ".quarantined.*")
	if len(m) != 1 {
		t.Fatalf("want one quarantined sibling of %s, got %v", path, m)
	}
	p, _ := filepath.EvalSymlinks(m[0])
	return p
}

// TestQuarantinedWorktreePointsAtItsOwnGitDir (CR-6): after a quarantine
// and the fresh clone at the old git-dir path, git run in the
// quarantined tree must reach the quarantined git-dir, never the live one.
func TestQuarantinedWorktreePointsAtItsOwnGitDir(t *testing.T) {
	capDir, gitDir := separateGitScene(t)
	if err := quarantineCapsule(capDir, gitDir); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", "--separate-git-dir="+gitDir, capDir).CombinedOutput(); err != nil {
		t.Fatalf("fresh clone stand-in: %v: %s", err, out)
	}
	qTree := quarantinedSibling(t, capDir)
	if got, want := gitDirOf(t, qTree), quarantinedSibling(t, gitDir); got != want {
		t.Fatalf("the quarantined tree resolves git-dir %q, want its own %q", got, want)
	}
}

// TestCrashMidQuarantineKeepsPairing (CR-6): killed after the git-dir
// moved and the pointer followed it, before the worktree moved: the next
// claim's quarantine moves the worktree still paired with that git-dir.
func TestCrashMidQuarantineKeepsPairing(t *testing.T) {
	capDir, gitDir := separateGitScene(t)
	moved := gitDir + ".quarantined.crash"
	if err := os.Rename(gitDir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, ".git"), []byte("gitdir: "+moved+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := quarantineCapsule(capDir, gitDir); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", "--separate-git-dir="+gitDir, capDir).CombinedOutput(); err != nil {
		t.Fatalf("fresh clone stand-in: %v: %s", err, out)
	}
	want, _ := filepath.EvalSymlinks(moved)
	if got := gitDirOf(t, quarantinedSibling(t, capDir)); got != want {
		t.Fatalf("the quarantined tree resolves git-dir %q, want its pair %q", got, want)
	}
}

// TestCarrierFetchBlipDoesNotBlock (CR-10): a re-claim whose carrier read
// meets one hub blip retries it instead of failing the claim (which
// closed the work item blocked until a human resumed it).
func TestCarrierFetchBlipDoesNotBlock(t *testing.T) {
	origFetch, origWait := fetchTaskRefFn, carrierFetchBackoff
	defer func() { fetchTaskRefFn, carrierFetchBackoff = origFetch, origWait }()
	carrierFetchBackoff = time.Millisecond
	calls := 0
	var first error
	fetchTaskRefFn = func(context.Context, gitops.Opts, string, string) (string, error) {
		calls++
		if calls == 1 {
			return "", first
		}
		return "", nil
	}
	r := &Runtime{}
	j := &job{wi: "wi_cr10"}
	first = fmt.Errorf("git ls-remote: fatal: unable to access 'http://hub/wi.git/': Failed to connect to hub port 80: Connection refused")
	if _, err := r.restoreTaskRepoState(context.Background(), j, j.git, "app", t.TempDir()); err != nil {
		t.Fatalf("one hub blip failed the carrier restore: %v", err)
	}
	if calls != 2 {
		t.Fatalf("the blip must be retried once, got %d reads", calls)
	}
	// a refusal is not a blip: no retry, the claim fails at once
	calls = 0
	first = fmt.Errorf("git ls-remote: fatal: unable to access 'http://hub/wi.git/': The requested URL returned error: 403")
	if _, err := r.restoreTaskRepoState(context.Background(), j, j.git, "app", t.TempDir()); err == nil || calls != 1 {
		t.Fatalf("a permanent refusal must fail without a retry: err=%v reads=%d", err, calls)
	}
}

// TestCrashMidPauseReplaysThePause (CR-11): the operator pause persisted
// its Paused record, then the daemon died before SessionEnd(paused). The
// hub still says running; the restart finishes the pause instead of
// resuming the work item against the operator's intent.
func TestCrashMidPauseReplaysThePause(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	wi := "wi_cr11_pause"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.StopReason = protocol.FinishPaused
	rec.CleanPause = true
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if sp.callCount() != 0 {
		t.Fatal("the operator's pause must not be resumed by a restart")
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusPaused || !hub.lastEnd().CleanPause {
		t.Fatalf("the pending pause must land at the hub: %d ends, last %+v", hub.endCount(), hub.lastEnd())
	}
	if recs := loadRecords(t, r); len(recs) != 1 || recs[0].State != string(StatePaused) {
		t.Fatalf("the paused record must be retained: %+v", recs)
	}
}

// TestAttachClearsCleanPauseBeforeTheHubFlips (CR-11): the attach of a
// retained pause the operator resumed mirrors the cleared clean_pause on
// disk before SessionAttach runs, so a crash after the hub flipped to
// running resumes the work item instead of re-pausing it.
func TestAttachClearsCleanPauseBeforeTheHubFlips(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	wi := "wi_cr11_attach"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.StopReason = protocol.FinishPaused
	rec.CleanPause = true
	hub.hold(wi, rec.SessionID, api.StatusPaused) // the operator's pf resume cleared clean_pause
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, r, []*jobRecord{rec})
	gate := hub.hangCall(t, "attach", wi)
	done := make(chan error, 1)
	go func() { done <- r.Restart(context.Background()) }()
	gate.waitEntered(t, "the attach")
	recs := loadRecords(t, r)
	gate.open()
	if err := <-done; err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(recs) != 1 || recs[0].CleanPause || pausePending(recs[0]) {
		t.Fatalf("at the attach the record must no longer read as a pending pause: %+v", recs)
	}
	if j := r.jobOf(wi); j != nil {
		terminateJobAgent(r, wi)
	}
}

// TestPauseReplayFailureRetriesUntilItLands (CR-11): the replayed
// SessionEnd(paused) meets a hub blip (more than once): the record stays
// paused, nothing resumes, and the verify retry lands the pause once the
// hub takes it.
func TestPauseReplayFailureRetriesUntilItLands(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	r.cfg.EndRetryBase = 10 * time.Millisecond
	wi := "wi_cr11_blip"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.StopReason = protocol.FinishPaused
	rec.CleanPause = true
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	hub.end[wi] = fmt.Errorf("connection refused")
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // several failed retries
	if ji, ok := r.Info(wi); !ok || ji.State != StatePaused {
		t.Fatalf("a failed pause replay must keep the record paused: %+v (ok=%v)", ji, ok)
	}
	hub.mu.Lock()
	delete(hub.end, wi)
	hub.mu.Unlock()
	waitFor(t, "the pause to land", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.status[wi] == api.StatusPaused
	})
	if sp.callCount() != 0 {
		t.Fatal("the operator's pause must never be resumed")
	}
	if !hub.lastEnd().CleanPause {
		t.Fatalf("the landed pause must be clean: %+v", hub.lastEnd())
	}
}

// orphanWatchSpawner records whether a watched orphan step process was
// still alive when the runtime started a new step.
type orphanWatchSpawner struct {
	scriptedSpawner
	orphan       int
	aliveAtSpawn bool
}

func (s *orphanWatchSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	if procExists(s.orphan) {
		s.mu.Lock()
		s.aliveAtSpawn = true
		s.mu.Unlock()
	}
	return s.scriptedSpawner.Spawn(ctx, p)
}

// TestRestartReapsOrphanOfClaimingEntry: the daemon died between the
// starter launching a step process and the record's persist — only the
// claiming journal names the work item. The restart reaps that orphan
// before the claim replay starts a new step (single writer, #99).
func TestRestartReapsOrphanOfClaimingEntry(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &orphanWatchSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	wi := runMarker("wi_claiming_orphan")
	session := fakeSession(wi)
	if err := r.noteClaiming(wi, session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, session, api.StatusRunning)

	orphan := exec.Command("sleep", "300")
	orphan.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	orphan.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID="+r.cfg.MachineID)
	if err := orphan.Start(); err != nil {
		t.Fatal(err)
	}
	sp.orphan = orphan.Process.Pid
	reaped := make(chan struct{})
	go func() { _ = orphan.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = syscall.Kill(-sp.orphan, syscall.SIGKILL); <-reaped })

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	select {
	case <-reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("the crashed daemon's step process of a claiming work item survived the restart")
	}
	if sp.callCount() == 0 {
		t.Fatal("test premise: the claim replay must start a step")
	}
	sp.mu.Lock()
	alive := sp.aliveAtSpawn
	sp.mu.Unlock()
	if alive {
		t.Fatal("a new step started while the orphan step process was still running (two writers)")
	}
}
