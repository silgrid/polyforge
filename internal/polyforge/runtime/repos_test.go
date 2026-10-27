package runtime

// repos_test: the claim→capsule task-repo lane. A claim carrying the
// project's repo inventory clones each repo into repos/<name> with real
// git (a local file:// fake remote — no network), records the claim-time
// HEAD SHA as the capsule.json baseline, leaves the clone outside the
// capsule's own git (gitignored, independent .git), keeps an existing
// worktree's uncommitted scene untouched on a re-materialize (the
// crashed-Start replay shape), and fails a bad clone as the typed
// DAEMON-507 error with the ticket closed blocked — never a live job.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// fakeTaskRemote builds a real local git repo (one commit on main) to
// serve as the task repo's remote. Answers the remote's directory, its
// file:// URL and the initial HEAD SHA.
func fakeTaskRemote(t *testing.T) (dir, url, head string) {
	t.Helper()
	setGitIdentity(t)
	dir = t.TempDir()
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return dir, "file://" + dir, strings.TrimSpace(string(out))
}

func capsuleReposOf(t *testing.T, capDir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, capsule.CapsuleJSON))
	if err != nil {
		t.Fatalf("capsule.json: %v", err)
	}
	var idn capsule.Identity
	if err := json.Unmarshal(raw, &idn); err != nil {
		t.Fatalf("capsule.json: %v", err)
	}
	return idn.Repos
}

// A claim with a repo inventory clones each repo into repos/<name> as a
// real independent worktree, records its HEAD as the capsule baseline,
// and keeps it out of the capsule's own git.
func TestStartClonesTaskRepos(t *testing.T) {
	setGitIdentity(t)
	_, url, head := fakeTaskRemote(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "app", URL: url}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})

	if err := r.Start(context.Background(), "wi_repo1", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	capDir := testCapsuleDir(r, "wi_repo1")
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")

	// A real clone: its own .git and the remote's content.
	if !dirExists(filepath.Join(repoDir, ".git")) {
		t.Fatal("repos/app is not a git clone (no .git)")
	}
	if b, err := os.ReadFile(filepath.Join(repoDir, "hello.txt")); err != nil || string(b) != "hello\n" {
		t.Fatalf("cloned content wrong: %q, %v", b, err)
	}
	// The claim-time HEAD is recorded as the baseline.
	if got := capsuleReposOf(t, capDir)["app"]; got != head {
		t.Fatalf("capsule baseline = %q, want remote HEAD %q", got, head)
	}
	// The clone stays out of the capsule's own git: repos/ is ignored
	// and nothing under it is tracked or listed as untracked.
	gi, _ := os.ReadFile(filepath.Join(capDir, ".gitignore"))
	if !strings.Contains(string(gi), "/repos/") {
		t.Fatalf("capsule .gitignore must exclude repos/: %q", gi)
	}
	out, err := exec.Command("git", "-C", capDir, "status", "--porcelain", "-uall").CombinedOutput()
	if err != nil {
		t.Fatalf("git status in capsule: %v: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "repos/") {
			t.Fatalf("capsule git sees repos/: %s", line)
		}
	}
	if err := r.Stop(context.Background(), "wi_repo1", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	unseal(t, capDir)
	unseal(t, repoDir)
}

// A journal replay of the SAME session's crashed start (the claim
// answered, the record never landed) still starts from a fresh clone:
// the husk — with whatever the reaped step left uncommitted in a task
// repo — is quarantined beside the capsule, bytes intact, and the
// replayed claim clones the task repo afresh.
func TestClaimReplayQuarantinesHusk(t *testing.T) {
	setGitIdentity(t)
	_, url, _ := fakeTaskRemote(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "app", URL: url}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})

	if err := r.Start(context.Background(), "wi_repo2", StartOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	capDir := testCapsuleDir(r, "wi_repo2")
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")

	// Simulate a daemon crash between spawn and the record's persist: the
	// agent tree dies, the job leaves the map, the bookkeeping is wiped
	// and the journal still names the unsettled start of the SAME session.
	j := r.jobOf("wi_repo2")
	j.mu.Lock()
	session := j.sessionID
	j.mu.Unlock()
	r.killAgentTree(j)
	r.mu.Lock()
	delete(r.jobs, "wi_repo2")
	r.mu.Unlock()
	writeStateFile(t, r, nil)
	if err := r.noteClaiming("wi_repo2", session, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart (journal replay): %v", err)
	}
	if r.jobOf("wi_repo2") == nil {
		t.Fatal("the replayed claim must start the work item")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "dirty.txt")); !os.IsNotExist(err) {
		t.Fatalf("the replayed claim reused the husk's task repo (stat err %v)", err)
	}
	husks, _ := filepath.Glob(capDir + ".quarantined.*")
	if len(husks) != 1 {
		t.Fatalf("the husk must be quarantined beside the capsule, got %v", husks)
	}
	kept := filepath.Join(husks[0], capsule.DirRepos, "app", "dirty.txt")
	if b, err := os.ReadFile(kept); err != nil || string(b) != "uncommitted\n" {
		t.Fatalf("the husk's uncommitted bytes must survive the quarantine: %q, %v", b, err)
	}
	if err := r.Stop(context.Background(), "wi_repo2", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	unseal(t, capDir)
	unseal(t, repoDir)
}

// A clone failure is the typed DAEMON-507 error; the fresh attempt is
// closed blocked on the spot (never an orphaned running generation, never
// a live job), and an unsafe inventory name fails the same way.
func TestCloneFailureBlocksTicket(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file:///nonexistent/nope.git"}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})

	err := r.Start(context.Background(), "wi_repo3", StartOpts{})
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonSpawnFail {
		t.Fatalf("expected typed %s, got %v", api.ErrDaemonSpawnFail, err)
	}
	if !strings.Contains(ae.Detail, "app") {
		t.Fatalf("error detail must name the repo: %q", ae.Detail)
	}
	if r.jobOf("wi_repo3") != nil {
		t.Fatal("a blocked clone must leave no live job")
	}
	if n := hub.endCount(); n != 1 {
		t.Fatalf("end-generation calls = %d, want 1", n)
	}
	end := hub.lastEnd()
	if end.Status != api.StatusBlocked || end.SessionID == "" || end.Note == "" {
		t.Fatalf("blocked close wrong: %+v", end)
	}
	if dirExists(filepath.Join(testCapsuleDir(r, "wi_repo3"), capsule.DirRepos, "app")) {
		t.Fatal("failed clone left a worktree behind")
	}

	// A repo name that would escape repos/ is refused before any clone.
	hub2 := newFakeHub()
	hub2.repos = []api.ProjectRepo{{Name: "../escape", URL: "file:///nonexistent/x.git"}}
	r2, _, _ := newTestRuntime(t, hub2, &scriptedSpawner{})
	if err := r2.Start(context.Background(), "wi_repo4", StartOpts{}); err == nil {
		t.Fatal("unsafe repo name must fail Start")
	} else if ae, ok := err.(*api.Error); !ok || ae.Code != api.ErrDaemonSpawnFail {
		t.Fatalf("unsafe name must be typed %s, got %v", api.ErrDaemonSpawnFail, err)
	} else if hub2.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("unsafe name must close blocked, got %+v", hub2.lastEnd())
	}
	if r2.jobOf("wi_repo4") != nil {
		t.Fatal("unsafe-name start left a live job")
	}
}

// The durability half of the claim lane (polyforge#108): a bounded-fail
// re-claim on a NEW session quarantines the old capsule and re-clones
// repos/<name> from its bare origin — which knows nothing of the dead
// session's commits. When the dead session published its head to the
// WI's carrier ref (refs/pf-task/<name>, written by every capsule
// publish; the gitops half is pinned in its own package), the fresh
// clone is moved there: the worktree stands at the carried head, the
// work files are back, and capsule.json records the carried head as the
// claim-time baseline. The task origin itself never moves.
func TestReclaimRestoresTaskRepoFromCarrier(t *testing.T) {
	setGitIdentity(t)
	remoteDir, url, head := fakeTaskRemote(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "app", URL: url}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})

	// The WI's bare hub — the capsule-chain remote the claim clones from
	// and the carrier lane publishes to. Pre-created and pinned to main
	// the way aihub's EnsureBare does, so Start #1 can clone it (empty)
	// and the carrier ref has a place to land.
	hubRoot := t.TempDir()
	hubBare := filepath.Join(hubRoot, "wi_durab.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", hubBare).CombinedOutput(); err != nil {
		t.Fatalf("hub bare init: %v: %s", err, out)
	}
	// aihub's EnsureBare advertises push options (every pf push carries
	// one); mirror it so the session's own publishes land cleanly.
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.shallowUpdate", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	r.cfg.GitRemoteBase = hubRoot

	wi := "wi_durab"
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	capDir := testCapsuleDir(r, wi)
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")
	if got := capsuleReposOf(t, capDir)["app"]; got != head {
		t.Fatalf("first-claim baseline = %q, want the origin head %q", got, head)
	}

	// The agent's work: one local commit on top of the claim-time clone,
	// pushed NOWHERE near the task origin.
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("durable work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@invalid", "commit", "-m", "repo work"}} {
		if out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	workHead := strings.TrimSpace(string(out))
	if workHead == head {
		t.Fatal("the work commit must differ from the origin head")
	}

	// The publish lane's carrier write (what gitops.pushTaskRefsLocked
	// does on every capsule publish): mirror the work head into the WI
	// bare repo under the carrier namespace.
	if out, err := exec.Command("git", "-C", repoDir, "push", hubBare, "HEAD:refs/pf-task/app").CombinedOutput(); err != nil {
		t.Fatalf("carrier write: %v: %s", err, out)
	}

	// Session A dies mid-flight; session B re-claims (the hub still
	// holds the dead session until the operator's force-end-session
	// returns the WI to queued).
	j := r.jobOf(wi)
	r.killAgentTree(j)
	r.dropJob(j)
	writeStateFile(t, r, nil)
	hub.forceEnd(wi)
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}

	// The old scene was quarantined (renamed, never deleted).
	var quarantined bool
	ents, err := os.ReadDir(filepath.Dir(capDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), wi+".quarantined.") {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatalf("no %s.quarantined.<ts> sibling: %v", wi, ents)
	}

	// The oracle: the re-claimed worktree stands at the carried head with
	// the work files back, and the baseline names it.
	repoB := filepath.Join(testCapsuleDir(r, wi), capsule.DirRepos, "app")
	got, err := exec.Command("git", "-C", repoB, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse (re-claimed): %v", err)
	}
	if strings.TrimSpace(string(got)) != workHead {
		t.Fatalf("re-claimed repos/app HEAD = %q, want the carried head %q", strings.TrimSpace(string(got)), workHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "durable work\n" {
		t.Fatalf("re-claimed work.txt wrong: %q, %v", b, err)
	}
	if got := capsuleReposOf(t, testCapsuleDir(r, wi))["app"]; got != workHead {
		t.Fatalf("re-claim baseline = %q, want the carried head %q", got, workHead)
	}
	// The task origin never moved: the workflows' deliver step is the only
	// push path, and nothing here pushed to it.
	originHead, err := exec.Command("git", "-C", remoteDir, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(originHead)) != head {
		t.Fatalf("task origin moved: %q (%v), want %q", strings.TrimSpace(string(originHead)), err, head)
	}

	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	unseal(t, testCapsuleDir(r, wi))
	unseal(t, repoB)
}

// polyforge#158 SHOULD-2: the once-per-repo carrier_push_failed state
// belongs to the job. Within one job a persisting carrier failure alerts
// once; after a re-claim in the same daemon (a new job) the same failure
// alerts again.
func TestCarrierPushFailedAlertStateIsPerJob(t *testing.T) {
	setGitIdentity(t)
	_, url, _ := fakeTaskRemote(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "app", URL: url}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	var mu sync.Mutex
	var alerts int
	r.cfg.EventTap = func(_, kind string, _ map[string]any) {
		if kind == gitops.EventCarrierPushFailed {
			mu.Lock()
			alerts++
			mu.Unlock()
		}
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return alerts }

	// The WI's hub bare refuses every carrier ref (the chain lands).
	hubRoot := t.TempDir()
	hubBare := filepath.Join(hubRoot, "wi_alert.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", hubBare).CombinedOutput(); err != nil {
		t.Fatalf("hub bare init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.shallowUpdate", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	hook := "#!/bin/sh\nwhile read old new ref; do case \"$ref\" in refs/pf-task/*) echo 'carrier refused' >&2; exit 1;; esac; done; exit 0\n"
	if err := os.WriteFile(filepath.Join(hubBare, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	r.cfg.GitRemoteBase = hubRoot

	wi := "wi_alert"
	publish := func(label string) {
		t.Helper()
		if _, _, _, err := gitops.CommitPush(context.Background(), r.jobOf(wi).git, label); err != nil {
			t.Fatalf("CommitPush(%s): %v", label, err)
		}
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	publish("a1")
	publish("a2")
	if got := count(); got != 1 {
		t.Fatalf("job A: %d carrier alerts, want exactly 1 for a persisting failure", got)
	}

	// Re-claim in the same daemon: a new job, a fresh alert state.
	j := r.jobOf(wi)
	r.killAgentTree(j)
	r.dropJob(j)
	writeStateFile(t, r, nil)
	hub.forceEnd(wi)
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	publish("b1")
	publish("b2")
	if got := count(); got != 2 {
		t.Fatalf("after the re-claim: %d carrier alerts in total, want 2 (one per job)", got)
	}

	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	unseal(t, testCapsuleDir(r, wi))
	unseal(t, filepath.Join(testCapsuleDir(r, wi), capsule.DirRepos, "app"))
}
