package runtime

// w80_target_branch_fix_test.go (polyforge#80 u2, AC2): the fixed
// target-branch file — a sub-ticket whose parent's lane declares
// children_branch gets runtime-state/target-branch.json written at its
// first claim, BEFORE anything else depends on it; a re-claim keeps the
// fixed value even when the parent or its lane changed or went away; a
// work item without a parent, or one whose parent's lane declares no
// children_branch, gets no file and behaves exactly as before.
//
// Expected values are the ticket's own: the parent polyforge#1 on the
// epic lane's `epic/{seq}` fixes {"target":"epic/1",
// "parent":"polyforge#1"}.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w80StrPtr answers s as a *string (BundleName/ParentID in the fixtures).
func w80StrPtr(s string) *string { return &s }

// w80Lanes writes the two lanes the fixture needs — the child's own
// plain lane and the parent's epic lane over front — and installs them
// as the runtime's workflows dir.
func w80Lanes(t *testing.T, r *Runtime, epicFront string) {
	t.Helper()
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "child-lane", "steps:\n  - { id: a }\n")
	workflowtest.WriteLane(t, lanes, "epic-lane", epicFront+"steps:\n  - { id: a }\n")
	r.cfg.WorkflowsDir = lanes
}

// w80Claim materializes wi's capsule the way a claim does, with the
// work-item record item (the claim answer's snapshot) and no task repos.
func w80Claim(t *testing.T, r *Runtime, wi string, item *api.WorkItem) string {
	t.Helper()
	j := &job{
		wi:         wi,
		project:    "proj",
		bundleName: "child-lane",
		capsuleDir: testCapsuleDir(r, wi),
		item:       item,
	}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", wi, err)
	}
	return j.capsuleDir
}

// w80TargetBranch reads the capsule's fixed target branch (the file must
// exist).
func w80TargetBranch(t *testing.T, capDir string) (target, parent string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(capsule.TargetBranchJSON)))
	if err != nil {
		t.Fatalf("%s: %v", capsule.TargetBranchJSON, err)
	}
	var fixed struct {
		Target string `json:"target"`
		Parent string `json:"parent"`
	}
	if err := json.Unmarshal(raw, &fixed); err != nil {
		t.Fatalf("%s is not a JSON object: %v (%q)", capsule.TargetBranchJSON, err, raw)
	}
	return fixed.Target, fixed.Parent
}

// TestW80TargetBranchFixedAtFirstClaim pins the fix: the first claim of a
// sub-ticket whose parent's lane declares `epic/{seq}` writes
// {"target":"epic/1","parent":"polyforge#1"}, and the re-claim after the
// parent's lane changed — and after the hub stopped answering for the
// parent at all — keeps the fixed value as it is (the re-claim reads the
// file, never the parent).
func TestW80TargetBranchFixedAtFirstClaim(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	w80Lanes(t, r, "children_branch: \"epic/{seq}\"\n")
	const parent = "wi_w80epic"
	child := "wi_w80child"
	slug := "polyforge#1"
	epic := "epic-lane"
	hub.items = map[string]*api.WorkItem{parent: {
		ID: parent, Slug: slug, Seq: 1, ProjectName: "proj", BundleName: &epic,
	}}
	item := &api.WorkItem{ID: child, ProjectName: "proj", ParentID: w80StrPtr(parent), Slug: "polyforge#2"}
	capDir := w80Claim(t, r, child, item)
	if target, p := w80TargetBranch(t, capDir); target != "epic/1" || p != slug {
		t.Fatalf("first claim fixed {target:%q parent:%q}, want {epic/1 %s}", target, p, slug)
	}
	// The parent's lane changes its template and the hub goes quiet for
	// the parent: the re-claim keeps the first claim's fixed value.
	w80Lanes(t, r, "children_branch: \"moved/{seq}\"\n")
	hub.lookupErr[parent] = &api.Error{Code: api.ErrCoreNotFound, Detail: "work item not found"}
	w80Claim(t, r, child, item)
	if target, p := w80TargetBranch(t, capDir); target != "epic/1" || p != slug {
		t.Fatalf("a re-claim after the parent changed keeps the fixed value, got {target:%q parent:%q}", target, p)
	}
}

// TestW80TargetBranchNoDeclarationUnchanged pins the untouched half: a
// work item without a parent, and a sub-ticket whose parent's lane
// declares no children_branch, get no fixed file — the claim behaves as
// before.
func TestW80TargetBranchNoDeclarationUnchanged(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	w80Lanes(t, r, "") // the epic lane declares nothing
	const parent = "wi_w80epic2"
	hub.items = map[string]*api.WorkItem{parent: {
		ID: parent, Seq: 1, ProjectName: "proj",
		BundleName: w80StrPtr("epic-lane"),
	}}
	plain := w80Claim(t, r, "wi_w80plain", &api.WorkItem{ID: "wi_w80plain", ProjectName: "proj"})
	if _, err := os.Stat(filepath.Join(plain, filepath.FromSlash(capsule.TargetBranchJSON))); !os.IsNotExist(err) {
		t.Fatalf("a work item without a parent gets no target-branch.json: %v", err)
	}
	orphan := w80Claim(t, r, "wi_w80child2", &api.WorkItem{
		ID: "wi_w80child2", ProjectName: "proj", ParentID: w80StrPtr(parent),
	})
	if _, err := os.Stat(filepath.Join(orphan, filepath.FromSlash(capsule.TargetBranchJSON))); !os.IsNotExist(err) {
		t.Fatalf("a sub-ticket whose parent's lane declares no children_branch gets no target-branch.json: %v", err)
	}
}
