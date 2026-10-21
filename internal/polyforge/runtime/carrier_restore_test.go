package runtime

// carrier_restore_test: the re-claim carrier restore never lets a WI
// continue on a task repo that lacks its own commits (production
// 2026-10-12, polyforge#132: a locally-sourced clone of a DETACHED-HEAD
// workspace checkout made the restore "skip" and the WI ran on a fresh
// clone while its commits sat safe in refs/pf-task/<name> on aihub).

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

func carrierGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// carrierRig: a workspace runtime whose "app" repo is cloned from a local
// checkout (served as the registry URL), a WI hub bare repo for the carrier lane, and the first claim
// done with one work commit published to the carrier. Answers the pieces
// the re-claim scenarios need.
type carrierRig struct {
	r        *Runtime
	hub      *fakeHub
	source   string // the workspace's local checkout
	hubBare  string
	wi       string
	capDir   string
	workHead string
	srcHead  string
}

func newCarrierRig(t *testing.T, wi string, publishCarrier bool) *carrierRig {
	t.Helper()
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	source := filepath.Join(ws, ".repo", "app")
	srcHead := w079GitRepo(t, source, "local.txt", "local source\n")
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + source}}

	hubRoot := t.TempDir()
	hubBare := filepath.Join(hubRoot, wi+".git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", hubBare).CombinedOutput(); err != nil {
		t.Fatalf("hub bare init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	r.cfg.GitRemoteBase = hubRoot

	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	capDir := testCapsuleDir(r, wi)
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("durable work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, repoDir, "add", "-A")
	carrierGit(t, repoDir, "commit", "-m", "repo work")
	workHead := carrierGit(t, repoDir, "rev-parse", "HEAD")
	if publishCarrier {
		carrierGit(t, repoDir, "push", hubBare, "HEAD:refs/pf-task/app")
	}

	// Session A dies; the hub returns the WI to queued.
	j := r.jobOf(wi)
	r.killAgentTree(j)
	r.dropJob(j)
	writeStateFile(t, r, nil)
	hub.forceEnd(wi)
	return &carrierRig{r: r, hub: hub, source: source, hubBare: hubBare, wi: wi,
		capDir: capDir, workHead: workHead, srcHead: srcHead}
}

func (g *carrierRig) repoHead(t *testing.T) string {
	t.Helper()
	return carrierGit(t, filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app"), "rev-parse", "HEAD")
}

// (a) The workspace checkout is on a DETACHED HEAD at re-claim: the fresh
// clone has no symbolic HEAD, yet the carrier is restored — repo HEAD is
// the carrier tip, the WI's commit is back, the baseline names it.
func TestReclaimRestoresCarrierFromDetachedHeadSource(t *testing.T) {
	g := newCarrierRig(t, "wi_detached", true)
	// Detached at a commit NO branch points to (git clone would otherwise
	// guess a matching branch for the clone): move main on, then detach
	// at the old tip — the production shape.
	if err := os.WriteFile(filepath.Join(g.source, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, g.source, "add", "-A")
	carrierGit(t, g.source, "commit", "-m", "later")
	carrierGit(t, g.source, "checkout", "--detach", "HEAD~1")
	if _, err := exec.Command("git", "-C", g.source, "symbolic-ref", "HEAD").Output(); err == nil {
		t.Fatal("test setup: the source checkout must be detached")
	}

	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	if got := g.repoHead(t); got != g.workHead {
		t.Fatalf("re-claimed repos/app HEAD = %q, want the carrier tip %q", got, g.workHead)
	}
	repoB := filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "durable work\n" {
		t.Fatalf("the WI's own commit is missing after the re-claim: %q, %v", b, err)
	}
	if st := carrierGit(t, repoB, "status", "--porcelain"); st != "" {
		t.Fatalf("restored worktree is dirty: %q", st)
	}
	if got := capsuleReposOf(t, testCapsuleDir(g.r, g.wi))["app"]; got != g.workHead {
		t.Fatalf("baseline = %q, want the carrier tip %q", got, g.workHead)
	}
}

// (b) A carrier exists but cannot be restored (its commit is not
// readable): the WI must NOT run on a fresh clone. The claim fails with
// the typed DAEMON-507 naming the repo and the carrier ref, the session
// closes blocked (the carrier stays on aihub for repair), and no
// repos/app is left behind for a retry to mistake for a recovery husk.
func TestReclaimRefusesWhenCarrierCannotBeRestored(t *testing.T) {
	g := newCarrierRig(t, "wi_unrestorable", true)
	// Corrupt the carrier: point it at an object the hub does not have.
	bogus := strings.Repeat("ab", 20)
	if err := os.WriteFile(filepath.Join(g.hubBare, "refs", "pf-task", "app"), []byte(bogus+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	endsBefore := len(g.hub.ends)

	err := g.r.Start(context.Background(), g.wi, StartOpts{})
	if err == nil {
		t.Fatal("a WI whose carrier cannot be restored must not start")
	}
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonSpawnFail {
		t.Fatalf("want typed DAEMON-507, got %v (%T)", err, err)
	}
	if !strings.Contains(ae.Detail, "task_repo_restore_failed") || !strings.Contains(ae.Detail, "app") || !strings.Contains(ae.Detail, "refs/pf-task/app") {
		t.Fatalf("the refusal must name the repo and the carrier ref, got %q", ae.Detail)
	}
	if len(g.hub.ends) != endsBefore+1 || g.hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("the claim must close blocked, got %+v", g.hub.lastEnd())
	}
	if g.r.jobOf(g.wi) != nil {
		t.Fatal("an unrestorable carrier left a live job")
	}
	if _, serr := os.Stat(filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")); !os.IsNotExist(serr) {
		t.Fatalf("a fresh clone without the WI's commits was left in place (%v)", serr)
	}
}

// (c) No carrier ref (fresh WI): the plain clone path is unchanged — the
// baseline is the local checkout's HEAD, on its branch.
func TestFreshClaimWithoutCarrierClonesNormally(t *testing.T) {
	g := newCarrierRig(t, "wi_nocarrier", false)
	carrierGit(t, g.source, "checkout", "--detach", "HEAD") // the detached source alone changes nothing
	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start without a carrier: %v", err)
	}
	if got := g.repoHead(t); got != g.srcHead {
		t.Fatalf("repos/app HEAD = %q, want the plain clone's %q", got, g.srcHead)
	}
	if got := capsuleReposOf(t, testCapsuleDir(g.r, g.wi))["app"]; got != g.srcHead {
		t.Fatalf("baseline = %q, want %q", got, g.srcHead)
	}
}

// (d) M8b: the required task-repo set is the claim inventory UNION the
// prior capsule.json Repos. An empty inventory must not drop a recorded
// repo: a recorded repo with no URL to clone from is a typed refusal,
// not a silent continue.
func TestRecordedReposSurviveAnEmptyInventory(t *testing.T) {
	g := newCarrierRig(t, "wi_union", true)
	r := g.r
	capDir := testCapsuleDir(r, g.wi)
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")
	raw, err := json.Marshal(capsule.Identity{WorkItemID: g.wi, Repos: map[string]string{"app": g.workHead}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, capsule.CapsuleJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	j := &job{wi: g.wi, capsuleDir: capDir} // empty inventory
	j.git = r.gitOptsForTest(t, g)

	if err := os.RemoveAll(repoDir); err != nil {
		t.Fatal(err)
	}
	_, err = r.cloneTaskRepos(context.Background(), j)
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonSpawnFail || !strings.Contains(ae.Detail, "app") {
		t.Fatalf("a recorded repo missing with no inventory URL must be a typed DAEMON-507, got %v", err)
	}
}

// gitOptsForTest builds the capsule gitops.Opts the rig's claim would
// (RemoteURL = the WI's hub bare repo).
func (r *Runtime) gitOptsForTest(t *testing.T, g *carrierRig) gitops.Opts {
	t.Helper()
	return gitops.Opts{WorkItemID: g.wi, Username: g.wi, Secret: "s", CapsuleDir: testCapsuleDir(r, g.wi), RemoteURL: g.hubBare}
}
