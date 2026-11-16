package runtime

// w80_target_branch_remote_test.go (polyforge#80 u4, AC3): the remote
// half of the fixed target branch — the daemon creates a branch the
// origin does not carry from the default branch tip with a non-forced
// push (its one outward write to the task repos' origin), a push another
// claim won loses to the remote's copy, and two sub-tickets of the one
// parent claimed at once never end up on two different branches.
//
// Expected values are the ticket's own: "daemon 从默认分支顶端非强制推送
// 创建；已存在时直接使用；两张子票同时首次领取不会建出两条不同的分支"
// — pinned on real git remotes, never a mock's word.

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
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w80RemoteHub builds the rig a target-branch claim needs: the hub
// serving the parent epic-lane fixture and the child item, and the job
// of one sub-ticket claim.
func w80RemoteHub(t *testing.T, wi string, seq int64) (r *Runtime, j *job) {
	t.Helper()
	hub := newFakeHub()
	r, _, _ = newWorkspaceTestRuntime(t, hub, "proj")
	w80Lanes(t, r, "children_branch: \"epic/{seq}\"\n")
	parent := "wi_w80epic" + wi
	epic := "epic-lane"
	hub.items = map[string]*api.WorkItem{parent: {
		ID: parent, Slug: "polyforge#" + wi, Seq: seq, ProjectName: "proj",
		BundleName: w80StrPtr(epic),
	}}
	j = &job{
		wi:         wi,
		project:    "proj",
		bundleName: "child-lane",
		capsuleDir: testCapsuleDir(r, wi),
		item:       &api.WorkItem{ID: wi, ProjectName: "proj", ParentID: w80StrPtr(parent)},
	}
	return r, j
}

// TestW80TargetBranchCreatesFromDefaultTip: the origin carries no branch
// — the claim creates it from the default branch tip and pushes it
// non-forced, and the clone is checked out on it.
func TestW80TargetBranchCreatesFromDefaultTip(t *testing.T) {
	r, j := w80RemoteHub(t, "wi_w80create", 8)
	src := t.TempDir()
	tip := w079GitRepo(t, src, "readme.txt", "main\n")
	j.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + src}}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", j.wi, err)
	}
	repoDir := filepath.Join(j.capsuleDir, capsule.DirRepos, "app")
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/8" {
		t.Fatalf("HEAD names %q, want the created branch epic/8", b)
	}
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("the created branch stands on %q, want the default branch tip %q", head, tip)
	}
	if out := strings.TrimSpace(carrierGit(t, repoDir, "ls-remote", "--heads", "origin")); out != tip+"\trefs/heads/epic/8\n"+tip+"\trefs/heads/main" {
		t.Fatalf("the origin must carry exactly main and the created epic/8 at the default tip, got %q", out)
	}
}

// TestW80TargetBranchPushRejectedUsesRemote: both of two claims' pushes
// are parked at the seam until both clones are about to push; the test
// then puts an unrelated tip on the origin's branch, so both pushes are
// rejected — each claim must fall back to the remote's copy, and the
// origin keeps exactly the one branch.
func TestW80TargetBranchPushRejectedUsesRemote(t *testing.T) {
	r, j := w80RemoteHub(t, "wi_w80race1", 7)
	src := t.TempDir()
	tip := w079GitRepo(t, src, "readme.txt", "main\n")
	// the remote copy another claim will have created and pushed by the
	// time both of these pushes run: an unrelated tip — the create-only
	// lease rejects both pushes because the branch exists, whatever the
	// tips' relation (pushTargetBranch, r1#6)
	other := t.TempDir()
	if out, err := exec.Command("git", "clone", "--quiet", "--", src, other).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(other, "winner.txt"), []byte("another claim's branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, other, "add", "-A")
	carrierGit(t, other, "commit", "-m", "the remote's copy")
	winner := carrierGit(t, other, "rev-parse", "HEAD")

	// park both pushes: neither runs until the test has pushed the
	// remote copy, so both are rejected and both fall back to it.
	orig := pushTargetBranchFn
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unpark := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { pushTargetBranchFn = orig; unpark() })
	pushTargetBranchFn = func(ctx context.Context, dir, name string) error {
		arrived <- struct{}{}
		<-release
		return orig(ctx, dir, name)
	}

	claims := []string{"wi_w80race1", "wi_w80race2"}
	parent := j.item.ParentID
	var wg sync.WaitGroup
	errs := make([]error, len(claims))
	for i, wi := range claims {
		ji := &job{
			wi:         wi,
			project:    "proj",
			bundleName: "child-lane",
			capsuleDir: testCapsuleDir(r, wi),
			item:       &api.WorkItem{ID: wi, ProjectName: "proj", ParentID: parent},
			repos:      []api.ProjectRepo{{Name: "app", URL: "file://" + src}},
		}
		wg.Add(1)
		go func(i int, jj *job) {
			defer wg.Done()
			errs[i] = r.materialize(context.Background(), jj)
		}(i, ji)
	}
	deadline := time.Now().Add(30 * time.Second)
	parked := 0
	for parked < 2 {
		select {
		case <-arrived:
			parked++
		case <-time.After(time.Until(deadline)):
			t.Fatalf("both claims never reached the branch push (30s; %d parked)", parked)
		}
	}
	carrierGit(t, other, "push", src, "HEAD:refs/heads/epic/7") // the origin gains the other copy
	unpark()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("claim %s: %v", claims[i], err)
		}
	}
	for _, wi := range claims {
		repoDir := filepath.Join(testCapsuleDir(r, wi), capsule.DirRepos, "app")
		if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != winner {
			t.Fatalf("claim %s stands on %q, want the remote's copy %q", wi, head, winner)
		}
		if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/7" {
			t.Fatalf("claim %s: HEAD names %q, want epic/7", wi, b)
		}
	}
	if out := strings.TrimSpace(carrierGit(t, other, "ls-remote", "--heads", "origin")); out != winner+"\trefs/heads/epic/7\n"+tip+"\trefs/heads/main" {
		t.Fatalf("the origin must carry exactly main and the one epic/7, got %q", out)
	}
}
