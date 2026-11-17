package runtime

// epic_target_branch_test.go (polyforge#117 u3, AC1/Q4): the epic's own
// target-branch record — an item whose OWN lane declares `epic` over
// `children_branch` gets runtime-state/target-branch.json written at its
// first claim as {"checkout":"epic/<its own seq>"} with NO target key
// (the epic merges into the origin's default branch, which every reader
// takes as the change's base exactly as for a work item with no fixed
// target), the task repos check out that branch, and a re-claim keeps
// the fixed value. A lane that declares children_branch but not epic
// leaves its own items untouched (the template serves their sub-tickets
// only), and an epic lane that cannot be read refuses the claim naming
// why — never a silent fall-back to the default branch.
//
// Expected values are the owner-confirmed plan's own: the record's shape
// (checkout, no target), the branch of the epic's own seq, and the
// untouched non-epic claim.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// epicLanes installs the fixture's two lanes: the epic lane over front
// and a plain lane beside it.
func epicLanes(t *testing.T, r *Runtime, front string) {
	t.Helper()
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "plain-lane", "steps:\n  - { id: a }\n")
	workflowtest.WriteLane(t, lanes, "epic-lane", front+"steps:\n  - { id: a }\n")
	r.cfg.WorkflowsDir = lanes
}

// epicClaim materializes the epic item wi's capsule the way a claim does.
func epicClaim(t *testing.T, r *Runtime, wi string, item *api.WorkItem, repos []api.ProjectRepo) string {
	t.Helper()
	j := &job{
		wi:         wi,
		project:    "proj",
		bundleName: "epic-lane",
		capsuleDir: testCapsuleDir(r, wi),
		item:       item,
		repos:      repos,
	}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", wi, err)
	}
	return j.capsuleDir
}

// epicRecord reads the capsule's target-branch record.
func epicRecord(t *testing.T, capDir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(capsule.TargetBranchJSON)))
	if err != nil {
		t.Fatalf("%s: %v", capsule.TargetBranchJSON, err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("%s is not a JSON object: %v (%q)", capsule.TargetBranchJSON, err, raw)
	}
	return rec
}

// TestEpicClaimCheckoutRecord: the first claim of an epic (its own lane
// declares epic over children_branch, its own seq 7) writes
// {"checkout":"epic/7"} — no target key — and checks the task repo out
// on origin's epic/7 at its tip; the re-claim keeps the fixed value
// after the lane's template changed.
func TestEpicClaimCheckoutRecord(t *testing.T) {
	hub := newFakeHub()
	r, ws, _ := newWorkspaceTestRuntime(t, hub, "proj")
	epicLanes(t, r, "epic: true\nchildren_branch: \"epic/{seq}\"\nresume_reset: sync\n")
	// the remote: main at one commit, epic/7 at another (the children
	// landed on it)
	src := filepath.Join(ws, ".repo", "app")
	mainHead := w079GitRepo(t, src, "readme.txt", "main\n")
	carrierGit(t, src, "checkout", "-b", "epic/7")
	if err := os.WriteFile(filepath.Join(src, "epic.txt"), []byte("children's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, src, "add", "-A")
	carrierGit(t, src, "commit", "-m", "epic tip")
	branchHead := carrierGit(t, src, "rev-parse", "epic/7")
	carrierGit(t, src, "checkout", "main")
	hub.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + src}}

	const epic = "wi_epic7"
	item := &api.WorkItem{ID: epic, ProjectName: "proj", Slug: "polyforge#7", Seq: 7, BundleName: w80StrPtr("epic-lane")}
	capDir := epicClaim(t, r, epic, item, hub.repos)
	rec := epicRecord(t, capDir)
	if rec["checkout"] != "epic/7" {
		t.Fatalf("the epic record is {checkout:%v}, want epic/7 (%v)", rec["checkout"], rec)
	}
	if _, has := rec["target"]; has {
		t.Fatalf("the epic record carries a target key (%v): an epic merges into the default branch, so no reader may take it as the change's base", rec)
	}
	repoDir := filepath.Join(capDir, capsule.DirRepos, "app")
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != branchHead {
		t.Fatalf("the clone stands on %q, want the epic branch's tip %q (main is %q)", head, branchHead, mainHead)
	}
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/7" {
		t.Fatalf("HEAD names %q, want the epic branch epic/7", b)
	}
	// the re-claim keeps the fixed value after the lane's template moved
	// (the claim path quarantines the previous scene; the fixture removes
	// the materialized repos the same way)
	epicLanes(t, r, "epic: true\nchildren_branch: \"moved/{seq}\"\nresume_reset: sync\n")
	if err := os.RemoveAll(filepath.Join(capDir, capsule.DirRepos)); err != nil {
		t.Fatal(err)
	}
	epicClaim(t, r, epic, item, hub.repos)
	if rec := epicRecord(t, capDir); rec["checkout"] != "epic/7" {
		t.Fatalf("a re-claim keeps the fixed checkout, got %v", rec)
	}
}

// TestEpicClaimNoEpicKeyUnchanged: a lane that declares children_branch
// but not epic serves its SUB-tickets only — its own item gets no record
// and the claim behaves exactly as before.
func TestEpicClaimNoEpicKeyUnchanged(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	epicLanes(t, r, "children_branch: \"epic/{seq}\"\n")
	const epic = "wi_epic8"
	item := &api.WorkItem{ID: epic, ProjectName: "proj", Slug: "polyforge#8", Seq: 8, BundleName: w80StrPtr("epic-lane")}
	capDir := epicClaim(t, r, epic, item, nil)
	if _, err := os.Stat(filepath.Join(capDir, filepath.FromSlash(capsule.TargetBranchJSON))); !os.IsNotExist(err) {
		t.Fatalf("an item on a lane without the epic key gets no record: %v", err)
	}
}

// TestEpicClaimLaneUnreadableRefused: an epic lane the set carries but
// the loader refuses refuses the claim naming the lane — never a silent
// fall-back to the default branch.
func TestEpicClaimLaneUnreadableRefused(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
	epics := t.TempDir()
	workflowtest.WriteLane(t, epics, "plain-lane", "steps:\n  - { id: a }\n")
	// a lane the loader refuses: the epic key without children_branch
	workflowtest.WriteLaneUnchecked(t, epics, "epic-lane", "epic: true\nsteps:\n  - { id: a }\n")
	r.cfg.WorkflowsDir = epics
	const epic = "wi_epic9"
	item := &api.WorkItem{ID: epic, ProjectName: "proj", Slug: "polyforge#9", Seq: 9, BundleName: w80StrPtr("epic-lane")}
	j := &job{
		wi: epic, project: "proj", bundleName: "epic-lane",
		capsuleDir: testCapsuleDir(r, epic), item: item,
	}
	err := r.materialize(context.Background(), j)
	if err == nil || !strings.Contains(err.Error(), "epic-lane") {
		t.Fatalf("an epic lane the loader refuses names the lane in the refusal, got %v", err)
	}
}
