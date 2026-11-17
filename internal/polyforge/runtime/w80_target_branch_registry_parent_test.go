package runtime

// w80_target_branch_registry_parent_test.go (polyforge#114, AC1): a
// sub-ticket whose parent is bound to no lane — a registry ticket used
// as the parent, the only parent shape polyforge#77 allowed — claims
// exactly as before: no runtime-state/target-branch.json is written and
// the task repo is checked out on the origin's default branch. Both
// shapes of "bound to no lane" (BundleName nil, and BundleName the empty
// string) are pinned, so the case can never again be merged into the
// refusal branch (the v0.2.2 regression: DAEMON-507 "names no lane").
//
// The origin's default branch is trunk, NOT main: a claim that "keeps
// the default branch" cannot pass by hard-coding main.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w114GitRepo creates a real git repo at dir whose default branch is
// trunk (one commit) and answers its HEAD SHA.
func w114GitRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, dir, "init", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("local source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, dir, "add", "-A")
	carrierGit(t, dir, "commit", "-m", "w114 init")
	return carrierGit(t, dir, "rev-parse", "HEAD")
}

// TestW80TargetBranchRegistryParentUnchanged pins AC1: the claim of a
// sub-ticket whose parent binds no lane succeeds, writes no fixed
// target-branch file, and leaves the clone on the origin's default
// branch (trunk, at the fixture's tip) — the origin gains no branch.
func TestW80TargetBranchRegistryParentUnchanged(t *testing.T) {
	const parent = "wi_w80reg"
	const child = "wi_w114child"
	for _, tc := range []struct {
		name   string
		parent *api.WorkItem
	}{
		{"the parent's BundleName is nil", &api.WorkItem{
			ID: parent, Slug: "polyforge#9", Seq: 9, ProjectName: "proj",
		}},
		{"the parent's BundleName is the empty string", &api.WorkItem{
			ID: parent, Slug: "polyforge#9", Seq: 9, ProjectName: "proj", BundleName: w80StrPtr(""),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub()
			r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
			w80Lanes(t, r, "children_branch: \"epic/{seq}\"\n")
			src := t.TempDir()
			tip := w114GitRepo(t, src)
			hub.items = map[string]*api.WorkItem{parent: tc.parent}
			j := &job{
				wi:         child,
				project:    "proj",
				bundleName: "child-lane",
				capsuleDir: testCapsuleDir(r, child),
				item:       &api.WorkItem{ID: child, ProjectName: "proj", ParentID: w80StrPtr(parent)},
				repos:      []api.ProjectRepo{{Name: "app", URL: "file://" + src}},
			}
			if err := r.materialize(context.Background(), j); err != nil {
				t.Fatalf("a sub-ticket whose parent binds no lane claims as before, got the refusal: %v", err)
			}
			if _, err := os.Stat(filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.TargetBranchJSON))); !os.IsNotExist(err) {
				t.Fatalf("no %s may be written: %v", capsule.TargetBranchJSON, err)
			}
			repoDir := filepath.Join(j.capsuleDir, capsule.DirRepos, "app")
			if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != tip {
				t.Fatalf("the clone stands on %q, want the origin's default tip %q", head, tip)
			}
			if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "trunk" {
				t.Fatalf("HEAD names %q, want the origin's default branch trunk", b)
			}
			if out := strings.TrimSpace(carrierGit(t, repoDir, "ls-remote", "--heads", "origin")); out != tip+"\trefs/heads/trunk" {
				t.Fatalf("the origin must carry only trunk at %s, got %q", tip, out)
			}
		})
	}
}
