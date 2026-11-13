package workflow_test

// laneset_test.go (workflow contract "User lanes"): a
// workspace's lane set is the built-in lanes plus its own user lanes under
// .polyforge/workflows/<lane>/. A user lane's step definitions are its own
// steps/ first, then the built-in _shared/steps/; its attr fragments may
// sit in its own attrs/; fragments come from the built-in _shared/ alone
// (a workspace has none of its own). A user lane named like a built-in one
// is refused with WFC-122 and never shadows it.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// userSet lays out a built-in lanes root (one lane `develop-ish`, the
// shared tree with a shared step definition `review`, the `principles`
// fragment and an attr fragment) and a workspace, and answers the
// workspace's lane set.
func userSet(t *testing.T) (workflow.LaneSet, string) {
	t.Helper()
	builtin := filepath.Join(t.TempDir(), "workflows")
	write(t, filepath.Join(builtin, workflow.SharedDir, "principles.md"), "Built-in principles.\n")
	write(t, filepath.Join(builtin, workflow.SharedDir, "steps", "review.md"), workflowtest.Definition("review")+"Shared review.\n")
	workflowtest.WriteLane(t, builtin, "builtin-lane", "steps:\n  - { id: fix }\n")
	ws := t.TempDir()
	return workflow.ForWorkspace(builtin, ws), ws
}

// writeUserLane writes a user lane's workflow.yaml (the header plus front)
// and the given files under its directory.
func writeUserLane(t *testing.T, set workflow.LaneSet, name, front string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(set.User, name)
	write(t, filepath.Join(dir, workflow.WorkflowFile), workflowtest.Header(name)+front)
	for rel, body := range files {
		write(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	return dir
}

func TestUserLaneResolvesAndFallsBackOnTheBuiltinSharedTree(t *testing.T) {
	set, ws := userSet(t)
	if set.User != filepath.Join(ws, ".polyforge", "workflows") {
		t.Fatalf("the user lane root = %s", set.User)
	}
	dir := writeUserLane(t, set, "notes", "steps:\n  - id: draft\n  - id: review\n", map[string]string{
		"steps/draft.md": workflowtest.Definition("draft") + "{{principles}}\n",
	})
	gotDir, source, ok := set.Resolve("notes")
	if !ok || gotDir != dir || source != workflow.SourceWorkspace {
		t.Fatalf("Resolve(notes) = %s %s %t", gotDir, source, ok)
	}
	if d, src, ok := set.Resolve("builtin-lane"); !ok || src != workflow.SourceBuiltin || d != filepath.Join(set.Builtin, "builtin-lane") {
		t.Fatalf("Resolve(builtin-lane) = %s %s %t", d, src, ok)
	}
	for _, name := range []string{"nowhere", workflow.SharedDir, ""} {
		if _, _, ok := set.Resolve(name); ok {
			t.Fatalf("Resolve(%q) resolved", name)
		}
	}
	lane, src, err := set.Load("notes")
	if err != nil || src != workflow.SourceWorkspace {
		t.Fatalf("the user lane must load over the built-in shared tree: %v (%s)", err, src)
	}
	if lane.Shared != filepath.Join(set.Builtin, workflow.SharedDir) {
		t.Fatalf("the user lane falls back on %s, want the built-in shared tree", lane.Shared)
	}
	// its own steps/ first: draft is the lane's; review is the shared one
	if p := filepath.Join(dir, "steps", "draft.md"); !fileIs(t, p) {
		t.Fatal("draft's own definition is gone")
	}
	review, err := workflow.ReadDefinitionIn(lane, "review")
	if err != nil || !strings.Contains(review.Body, "Shared review.") || review.Path != filepath.Join(set.Builtin, workflow.SharedDir, "steps", "review.md") {
		t.Fatalf("review must fall back on the built-in _shared/steps/review.md: %+v %v", review, err)
	}
	draft, err := workflow.ReadDefinitionIn(lane, "draft")
	if err != nil || !strings.Contains(draft.Body, "Built-in principles.") {
		t.Fatalf("draft's {{principles}} must render the built-in fragment: %+v %v", draft, err)
	}
	// a lane's own definition wins over the shared one of the same id
	write(t, filepath.Join(dir, "steps", "review.md"), workflowtest.Definition("review")+"Own review.\n")
	if lane, _, err = set.Load("notes"); err != nil {
		t.Fatal(err)
	}
	if review, err = workflow.ReadDefinitionIn(lane, "review"); err != nil || !strings.Contains(review.Body, "Own review.") {
		t.Fatalf("the lane's own steps/review.md must win: %+v %v", review, err)
	}
}

// A workspace has no shared tree of its own: a `_shared` under its user
// lane root is no lane and none of its fragments or definitions resolve.
func TestWorkspaceHasNoSharedTree(t *testing.T) {
	set, _ := userSet(t)
	write(t, filepath.Join(set.User, workflow.SharedDir, "house_style.md"), "Workspace style.\n")
	write(t, filepath.Join(set.User, workflow.SharedDir, "steps", "polish.md"), workflowtest.Definition("polish"))
	writeUserLane(t, set, "notes", "steps:\n  - id: draft\n  - id: polish\n", map[string]string{
		"steps/draft.md": workflowtest.Definition("draft") + "{{house_style}}\n",
	})
	_, _, err := set.Load("notes")
	le, ok := err.(*workflow.LoadError)
	if !ok || !le.HasCode(workflow.CodePlaceholder) || !le.HasCode(workflow.CodeDefinitionMissing) {
		t.Fatalf("a workspace _shared must resolve nothing (WFC-195 for its fragment, WFC-190 for its step): %v", err)
	}
	lanes, err := set.Lanes()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lanes {
		if l.Name == workflow.SharedDir {
			t.Fatalf("the workspace's _shared is listed as a lane: %+v", l)
		}
	}
}

// WFC-122: a user lane named like a built-in lane is refused and never
// shadows it — the name resolves to the built-in lane.
func TestUserLaneNamedLikeABuiltinIsRefused(t *testing.T) {
	set, _ := userSet(t)
	dir := writeUserLane(t, set, "builtin-lane", "steps:\n  - id: other\n", map[string]string{"steps/other.md": workflowtest.Definition("other")})
	writeUserLane(t, set, "notes", "steps:\n  - id: draft\n", map[string]string{"steps/draft.md": workflowtest.Definition("draft")})
	if d, src, ok := set.Resolve("builtin-lane"); !ok || src != workflow.SourceBuiltin || d == dir {
		t.Fatalf("the built-in lane must win: %s %s %t", d, src, ok)
	}
	lanes, err := set.Lanes()
	if err != nil {
		t.Fatal(err)
	}
	var shadowed, notes *workflow.SetLane
	for i := range lanes {
		switch {
		case lanes[i].Dir == dir:
			shadowed = &lanes[i]
		case lanes[i].Name == "notes":
			notes = &lanes[i]
		}
	}
	if shadowed == nil || !shadowed.Shadowed || shadowed.Source != workflow.SourceWorkspace || len(shadowed.Errors) != 1 {
		t.Fatalf("the shadowing user lane: %+v", shadowed)
	}
	e := shadowed.Errors[0]
	if e.Code != workflow.CodeShadowsBuiltin || e.File != filepath.Join(dir, workflow.WorkflowFile) || e.Line != 2 || e.Path != "name" {
		t.Fatalf("WFC-122 defect = %+v, want WFC-122 at workflow.yaml:2 name", e)
	}
	if notes == nil || len(notes.Errors) != 0 || notes.Shadowed {
		t.Fatalf("an unrelated user lane stays good: %+v", notes)
	}
	ids, err := set.StepIDs()
	if err != nil {
		t.Fatal(err)
	}
	if !ids["draft"] || !ids["fix"] || ids["other"] {
		t.Fatalf("step ids %v: the user lane's and the built-in lane's, never the shadowed one's", keys(ids))
	}
}

func fileIs(t *testing.T, p string) bool {
	t.Helper()
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// An unreadable user lane root (a file where the directory belongs)
// disables only the user lanes: StepIDs still answers every built-in
// lane's steps, beside a *UserLanesError naming the root — never an empty
// answer that would make every key of the workspace yaml stale.
func TestUnreadableUserLaneRootKeepsTheBuiltinLanes(t *testing.T) {
	set, _ := userSet(t)
	workflowtest.WriteLane(t, set.Builtin, "attr-lane", "steps:\n  - id: draft\n")
	write(t, set.User, "not a directory\n")
	ids, err := set.StepIDs()
	var ule *workflow.UserLanesError
	if !errors.As(err, &ule) || ule.Root != set.User || !ids["fix"] || !ids["draft"] {
		t.Fatalf("StepIDs = %v, %v; want the built-in steps fix and draft and a *UserLanesError naming %s", ids, err, set.User)
	}
	// an unreadable built-in lanes root is still an error answering nothing
	if ids, err := (workflow.LaneSet{Builtin: filepath.Join(set.Builtin, "missing"), User: set.User}).StepIDs(); err == nil || ids != nil || errors.As(err, &ule) {
		t.Fatalf("a missing built-in lanes root: %v, %v", ids, err)
	}
}
