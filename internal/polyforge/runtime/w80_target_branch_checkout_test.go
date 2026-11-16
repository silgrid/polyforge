package runtime

// w80_target_branch_checkout_test.go (polyforge#80 u3, AC2): the fixed
// value's checkout half — a fresh clone of a sub-ticket's task repo is
// moved onto the fixed target branch before anything reads the tree, the
// carrier restore lands on the fixed branch name (not the clone's
// default branch), and a work item with no target branch keeps the
// claim exactly as before.
//
// Expected values are the ticket's own: the checkout happens "每次
// （重新）领取都按固定值检出" and "carrier 恢复也挂在该分支上" — pinned
// on a real git remote that carries the branch and a real carrier ref.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestW80TargetBranchChecksOutExistingBranch: the origin carries the
// branch at a tip the default branch does not — the fresh clone is
// checked out on it (HEAD is the branch tip, HEAD names the branch), and
// the claim-time baseline is that tip.
func TestW80TargetBranchChecksOutExistingBranch(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	w80Lanes(t, r, "children_branch: \"epic/{seq}\"\n")
	// the remote: main at one commit, epic/1 at another
	src := t.TempDir()
	mainHead := w079GitRepo(t, src, "readme.txt", "main\n")
	carrierGit(t, src, "checkout", "-b", "epic/1")
	if err := os.WriteFile(filepath.Join(src, "epic.txt"), []byte("epic work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, src, "add", "-A")
	carrierGit(t, src, "commit", "-m", "epic tip")
	branchHead := carrierGit(t, src, "rev-parse", "epic/1")
	carrierGit(t, src, "checkout", "main")

	const parent = "wi_w80epic5"
	const child = "wi_w80co"
	hub.items = map[string]*api.WorkItem{parent: {
		ID: parent, Slug: "polyforge#5", Seq: 5, ProjectName: "proj",
		BundleName: w80StrPtr("epic-lane"),
	}}
	j := &job{
		wi:         child,
		project:    "proj",
		bundleName: "child-lane",
		capsuleDir: testCapsuleDir(r, child),
		item:       &api.WorkItem{ID: child, ProjectName: "proj", ParentID: w80StrPtr(parent)},
		repos:      []api.ProjectRepo{{Name: "app", URL: "file://" + src}},
	}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", child, err)
	}
	repoDir := filepath.Join(j.capsuleDir, capsule.DirRepos, "app")
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != branchHead {
		t.Fatalf("the clone stands on %q, want the fixed branch's tip %q (main is %q)", head, branchHead, mainHead)
	}
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/1" {
		t.Fatalf("HEAD names %q, want the fixed branch epic/1", b)
	}
}

// TestW80TargetBranchCarrierOnFixedBranch: a re-claim restores the
// previous session's carrier head ONTO the fixed branch — HEAD is the
// carried commit and names epic/1, not the clone's default branch.
func TestW80TargetBranchCarrierOnFixedBranch(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "demo-bundle", "steps:\n  - { id: a }\n")
	workflowtest.WriteLane(t, lanes, "epic-lane", "children_branch: \"epic/{seq}\"\nsteps:\n  - { id: a }\n")
	r.cfg.WorkflowsDir = lanes
	const parent = "wi_w80epic6"
	const child = "wi_w80carrier"
	epic := "epic-lane"
	hub.items = map[string]*api.WorkItem{
		parent: {ID: parent, Slug: "polyforge#6", Seq: 6, ProjectName: "proj", BundleName: w80StrPtr(epic)},
		child:  {ID: child, ProjectName: "proj", ParentID: w80StrPtr(parent)},
	}
	// the task repo and the capsule carrier store
	source := filepath.Join(ws, ".repo", "app")
	w079GitRepo(t, source, "local.txt", "local source\n")
	carrierGit(t, source, "branch", "epic/6") // the origin carries the fixed branch
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + source}}
	hubRoot := t.TempDir()
	hubBare := filepath.Join(hubRoot, child+".git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", hubBare).CombinedOutput(); err != nil {
		t.Fatalf("hub bare init: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", hubBare, "config", "receive.shallowUpdate", "true").CombinedOutput(); err != nil {
		t.Fatalf("hub bare config: %v: %s", err, out)
	}
	r.cfg.GitRemoteBase = hubRoot

	if err := r.Start(context.Background(), child, StartOpts{}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	repoDir := filepath.Join(testCapsuleDir(r, child), capsule.DirRepos, "app")
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("durable work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, repoDir, "add", "-A")
	carrierGit(t, repoDir, "commit", "-m", "repo work")
	workHead := carrierGit(t, repoDir, "rev-parse", "HEAD")
	carrierGit(t, repoDir, "push", hubBare, "HEAD:refs/pf-task/app")

	// session A dies; the hub returns the WI to queued; the re-claim
	// restores the carried head onto the fixed branch.
	j := r.jobOf(child)
	r.killAgentTree(j)
	r.dropJob(j)
	writeStateFile(t, r, nil)
	hub.forceEnd(child)
	if err := r.Start(context.Background(), child, StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	repoDir = filepath.Join(testCapsuleDir(r, child), capsule.DirRepos, "app")
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != workHead {
		t.Fatalf("the re-claim's repo stands on %q, want the carried head %q", head, workHead)
	}
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/6" {
		t.Fatalf("HEAD names %q, want the fixed branch epic/6", b)
	}
}

// TestW80TargetBranchWithoutTargetUnchanged: a work item with no fixed
// target branch keeps the claim as it was — the clone stays on the
// origin's default branch and the origin gains no branch.
func TestW80TargetBranchWithoutTargetUnchanged(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	source := filepath.Join(ws, ".repo", "app")
	mainHead := w079GitRepo(t, source, "local.txt", "local source\n")
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + source}}
	const child = "wi_w80plainrepos"
	if err := r.Start(context.Background(), child, StartOpts{}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	repoDir := filepath.Join(testCapsuleDir(r, child), capsule.DirRepos, "app")
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != mainHead {
		t.Fatalf("the clone stands on %q, want the origin's default tip %q", head, mainHead)
	}
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "main" {
		t.Fatalf("HEAD names %q, want the origin's default branch main", b)
	}
	out := strings.TrimSpace(carrierGit(t, repoDir, "ls-remote", "--heads", "origin"))
	if out != mainHead+"\trefs/heads/main" {
		t.Fatalf("the origin must carry only main at %s, got %q", mainHead, out)
	}
}
