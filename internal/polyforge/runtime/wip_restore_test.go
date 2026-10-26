package runtime

// wip_restore_test: a task repo's UNCOMMITTED work survives blocked →
// resume (polyforge#167). The bounded-fail close snapshots it to
// refs/pf-wip/<name> on the WI's hub bare (after mirroring HEAD to
// refs/pf-task/<name>); the re-claim restores the carrier head and checks
// the WIP tree out on top of it — uncommitted, HEAD and index untouched.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// wipRig is carrierRig's first half with the close left to the test: a
// live job on repos/app with one work commit on top of the clone.
type wipRig struct {
	r        *Runtime
	hub      *fakeHub
	hubBare  string
	wi       string
	repoDir  string
	workHead string
	mu       sync.Mutex
	events   []map[string]any
}

func newWIPRig(t *testing.T, wi string) *wipRig {
	t.Helper()
	isolateGitConfig(t)
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	source := filepath.Join(ws, ".repo", "app")
	w079GitRepo(t, source, "local.txt", "local source\n")
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + source}}
	hubRoot := t.TempDir()
	hubBare := filepath.Join(hubRoot, wi+".git")
	for _, args := range [][]string{
		{"init", "--bare", "-b", "main", hubBare},
		{"--git-dir", hubBare, "config", "receive.advertisePushOptions", "true"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	r.cfg.GitRemoteBase = hubRoot
	g := &wipRig{r: r, hub: hub, hubBare: hubBare, wi: wi}
	r.cfg.EventTap = func(_, kind string, body map[string]any) {
		g.mu.Lock()
		defer g.mu.Unlock()
		b := map[string]any{"kind": kind}
		for k, v := range body {
			b[k] = v
		}
		g.events = append(g.events, b)
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	g.repoDir = filepath.Join(testCapsuleDir(r, wi), capsule.DirRepos, "app")
	writeFile(t, filepath.Join(g.repoDir, "work.txt"), "committed work\n")
	carrierGit(t, g.repoDir, "add", "-A")
	carrierGit(t, g.repoDir, "commit", "-m", "repo work")
	g.workHead = carrierGit(t, g.repoDir, "rev-parse", "HEAD")
	return g
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirty leaves the step's uncommitted work: a tracked edit (staged, so the
// agent's index differs from HEAD), an untracked file, a deleted tracked
// file and an ignored file.
func (g *wipRig) dirty(t *testing.T) {
	t.Helper()
	writeFile(t, filepath.Join(g.repoDir, "work.txt"), "committed work\nplus uncommitted edit\n")
	carrierGit(t, g.repoDir, "add", "work.txt")
	writeFile(t, filepath.Join(g.repoDir, "e2e_test.go"), "package app // authored, never committed\n")
	if err := os.Remove(filepath.Join(g.repoDir, "local.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(g.repoDir, ".git", "info", "exclude"), "*.log\n")
	writeFile(t, filepath.Join(g.repoDir, "debug.log"), "ignored noise\n")
}

// block runs the bounded-fail close (kill, commit-dirty, WIP lane,
// SessionEnd(blocked)), then the owner's resume returns the WI to the
// claim gate.
func (g *wipRig) block(t *testing.T) {
	t.Helper()
	j := g.r.jobOf(g.wi)
	if j == nil {
		t.Fatal("no live job to block")
	}
	underLock(g.r, j.wi, func() { g.r.boundedFail(context.Background(), j, "test: blocked") })
	if last := g.hub.lastEnd(); last.Status != api.StatusBlocked {
		t.Fatalf("close = %+v, want blocked", last)
	}
	g.hub.forceEnd(g.wi)
}

func (g *wipRig) hubRef(t *testing.T, ref string) (string, bool) {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", g.hubBare, "rev-parse", "--verify", "-q", ref).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func (g *wipRig) eventsOf(kind string) []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []map[string]any
	for _, e := range g.events {
		if e["kind"] == kind {
			out = append(out, e)
		}
	}
	return out
}

// Acceptance: uncommitted changes → blocked → resume: the changes are in
// the re-claimed capsule's working tree, HEAD is the carried head, the
// repo's index is clean (nothing committed or staged by the restore).
func TestBlockedResumeRestoresUncommittedTaskWork(t *testing.T) {
	g := newWIPRig(t, "wi_wip_restore")
	g.dirty(t)
	g.block(t)

	if got, ok := g.hubRef(t, gitops.TaskRefName("app")); !ok || got != g.workHead {
		t.Fatalf("carrier = %q (%v), want %q", got, ok, g.workHead)
	}
	wip, ok := g.hubRef(t, gitops.TaskWIPRefName("app"))
	if !ok {
		t.Fatal("the blocked close left no refs/pf-wip/app for a dirty task repo")
	}
	if parent, _ := g.hubRef(t, wip+"^"); parent != g.workHead {
		t.Fatalf("WIP parent = %q, want the carried head %q", parent, g.workHead)
	}
	// the dead session's repo itself was not touched: HEAD, index
	if got := carrierGit(t, g.repoDir, "rev-parse", "HEAD"); got != g.workHead {
		t.Fatalf("the WIP lane moved the agent's HEAD to %q", got)
	}
	if staged := carrierGit(t, g.repoDir, "diff", "--cached", "--name-only"); staged != "work.txt" {
		t.Fatalf("the agent's index changed: staged %q, want work.txt", staged)
	}

	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	repoB := filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")
	if got := carrierGit(t, repoB, "rev-parse", "HEAD"); got != g.workHead {
		t.Fatalf("re-claimed HEAD = %q, want the carried head %q", got, g.workHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "committed work\nplus uncommitted edit\n" {
		t.Fatalf("the uncommitted edit is missing: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "e2e_test.go")); err != nil || !strings.Contains(string(b), "never committed") {
		t.Fatalf("the untracked file is missing: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(repoB, "local.txt")); !os.IsNotExist(err) {
		t.Fatalf("the deleted file came back (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(repoB, "debug.log")); !os.IsNotExist(err) {
		t.Fatalf("an ignored file was carried (%v)", err)
	}
	if staged := carrierGit(t, repoB, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("the restore staged %q (it must leave the index on HEAD)", staged)
	}
	st := carrierGit(t, repoB, "status", "--porcelain")
	// (carrierGit trims the output: compare each line trimmed; the
	// empty diff --cached above already proves nothing is staged)
	lines := map[string]bool{}
	for _, l := range strings.Split(st, "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	for _, want := range []string{"M work.txt", "D local.txt", "?? e2e_test.go"} {
		if !lines[want] {
			t.Fatalf("status %q misses %q", st, want)
		}
	}
	if got := capsuleReposOf(t, testCapsuleDir(g.r, g.wi))["app"]; got != g.workHead {
		t.Fatalf("baseline = %q, want the carried head %q", got, g.workHead)
	}
	if ev := g.eventsOf(gitops.EventTaskWIPNotApplied); len(ev) != 0 {
		t.Fatalf("a matching WIP raised %v", ev)
	}
}

// A WIP whose parent is not the restored head (the carrier moved past
// it) is NOT applied; task_wip_not_applied names both SHAs.
func TestReclaimSkipsWIPOnParentMismatch(t *testing.T) {
	g := newWIPRig(t, "wi_wip_mismatch")
	g.dirty(t)
	g.block(t)
	wip, ok := g.hubRef(t, gitops.TaskWIPRefName("app"))
	if !ok {
		t.Fatal("no WIP ref to mismatch")
	}
	// The carrier moves on (a later session committed and published).
	carrierGit(t, g.repoDir, "commit", "-q", "-m", "later commit")
	later := carrierGit(t, g.repoDir, "rev-parse", "HEAD")
	carrierGit(t, g.repoDir, "push", "-q", g.hubBare, "HEAD:"+gitops.TaskRefName("app"))

	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	repoB := filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")
	if got := carrierGit(t, repoB, "rev-parse", "HEAD"); got != later {
		t.Fatalf("re-claimed HEAD = %q, want the moved carrier %q", got, later)
	}
	if st := carrierGit(t, repoB, "status", "--porcelain"); st != "" {
		t.Fatalf("a mismatched WIP was applied: status %q", st)
	}
	ev := g.eventsOf(gitops.EventTaskWIPNotApplied)
	if len(ev) != 1 || ev[0]["wip"] != wip || ev[0]["wip_parent"] != g.workHead || ev[0]["head"] != later || ev[0]["repo"] != "app" {
		t.Fatalf("task_wip_not_applied events = %v, want one naming wip %s, parent %s, head %s", ev, wip, g.workHead, later)
	}
}

// No uncommitted changes: the close produces no WIP ref, and the
// re-claim restores a clean tree.
func TestCleanTaskRepoProducesNoWIPRef(t *testing.T) {
	g := newWIPRig(t, "wi_wip_clean")
	g.block(t)
	if got, ok := g.hubRef(t, gitops.TaskRefName("app")); !ok || got != g.workHead {
		t.Fatalf("carrier = %q (%v), want %q", got, ok, g.workHead)
	}
	if wip, ok := g.hubRef(t, gitops.TaskWIPRefName("app")); ok {
		t.Fatalf("a clean task repo produced refs/pf-wip/app = %s", wip)
	}
	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	repoB := filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")
	if st := carrierGit(t, repoB, "status", "--porcelain"); st != "" {
		t.Fatalf("clean restore is dirty: %q", st)
	}
}

// The wrapped close never produces a WIP ref, even over a dirty tree,
// and drops a stale one.
func TestWrappedCloseLeavesNoWIPRef(t *testing.T) {
	g := newWIPRig(t, "wi_wip_wrapped")
	g.dirty(t)
	j := g.r.jobOf(g.wi)
	// a stale snapshot from an earlier close
	gitops.PushTaskWIP(j.git, true, true)
	if _, ok := g.hubRef(t, gitops.TaskWIPRefName("app")); !ok {
		t.Fatal("setup: no stale WIP ref")
	}
	var err error
	underLock(g.r, j.wi, func() { err = g.r.stopOp(context.Background(), j, "wrapped", false, "") })
	if err != nil {
		t.Fatalf("wrapped close: %v", err)
	}
	if wip, ok := g.hubRef(t, gitops.TaskWIPRefName("app")); ok {
		t.Fatalf("the wrapped close left refs/pf-wip/app = %s", wip)
	}
}

// B1 regression: a WIP that turned a tracked directory into a symlink out
// of the repo (HEAD tracks a/b.txt; the step ran `mv a ../shared && ln -s
// ../shared a`) must be applied by git itself — the deleted a/b.txt is
// never removed THROUGH the new symlink, so the file outside the repo
// survives. HEAD and the repo's index stay on head.
func TestWIPCheckoutNeverFollowsASymlinkOutOfTheRepo(t *testing.T) {
	isolateGitConfig(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, repo, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(repo, "a", "b.txt"), "tracked\n")
	writeFile(t, filepath.Join(repo, "top.txt"), "top\n")
	carrierGit(t, repo, "add", "-A")
	carrierGit(t, repo, "commit", "-q", "-m", "base")
	head := carrierGit(t, repo, "rev-parse", "HEAD")

	// the step's work, committed elsewhere as the WIP
	work := filepath.Join(root, "work")
	carrierGit(t, root, "clone", "-q", repo, work)
	if err := os.RemoveAll(filepath.Join(work, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared", filepath.Join(work, "a")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "top.txt"), "top, edited\n")
	carrierGit(t, work, "add", "-A")
	carrierGit(t, work, "commit", "-q", "-m", "pf-wip")
	wip := carrierGit(t, work, "rev-parse", "HEAD")
	carrierGit(t, repo, "fetch", "-q", work, "HEAD")

	// the file outside the repo the symlink points at
	outside := filepath.Join(root, "shared", "b.txt")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, outside, "not the repo's\n")

	if err := checkoutWIPTree(context.Background(), repo, head, wip); err != nil {
		t.Fatalf("checkoutWIPTree: %v", err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "not the repo's\n" {
		t.Fatalf("the restore touched a file outside the repo: %q, %v", b, err)
	}
	if st, err := os.Lstat(filepath.Join(repo, "a")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("a is not the WIP's symlink (%v)", err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "top.txt")); string(b) != "top, edited\n" {
		t.Fatalf("top.txt = %q", b)
	}
	if got := carrierGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if staged := carrierGit(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("the restore staged %q", staged)
	}
}

// isolateGitConfig points git at an empty throwaway global config (and
// XDG dir) for the whole test: never the operator's real ~/.gitconfig.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}
