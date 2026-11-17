package runtime

// w80_target_branch_refusal_test.go (polyforge#80, AC4): a claim that
// must compute the target branch and cannot read the parent, or the
// parent's lane, is refused naming why — never a silent fall-back to the
// default branch. Each refusal is the typed DAEMON-507 shape the
// claim-lane failures use, so the detail becomes the blocked note.
//
// The refusal logic is one function with the fix (u2's implementation
// commit, targetbranch.go); this file is its coverage, riding u2's
// recorded run (the same -run 'TestW80TargetBranch' command).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestW80TargetBranchRefusesUnreadableParent pins every refusal case,
// each with the reason in the typed error's detail: a hub that cannot
// read the parent, a parent's lane that is not in this daemon's lane
// set, and a parent's lane the loader refuses. A parent whose lane loads
// and declares no children_branch stays green (covered by
// TestW80TargetBranchNoDeclarationUnchanged), and a parent bound to no
// lane (a registry ticket) stays green too — covered by
// TestW80TargetBranchRegistryParentUnchanged (polyforge#114: it was
// wrongly refused as "names no lane" in v0.2.2).
func TestW80TargetBranchRefusesUnreadableParent(t *testing.T) {
	const parent = "wi_w80epic11"
	const child = "wi_w80child11"
	cases := []struct {
		name string
		set  func(t *testing.T, r *Runtime, hub *fakeHub)
		want string
	}{
		{"the hub cannot read the parent", func(t *testing.T, r *Runtime, hub *fakeHub) {
			hub.lookupErr[parent] = errors.New("connection refused")
		}, "the parent " + parent + " cannot be read"},
		{"the parent's lane is not in the lane set", func(t *testing.T, r *Runtime, hub *fakeHub) {
			hub.items[parent] = &api.WorkItem{
				ID: parent, Slug: "polyforge#11", Seq: 11, ProjectName: "proj", BundleName: w80StrPtr("no-such-lane"),
			}
		}, "is not in this daemon's lane set"},
		{"the parent's lane does not load", func(t *testing.T, r *Runtime, hub *fakeHub) {
			workflowtest.WriteLaneUnchecked(t, r.cfg.WorkflowsDir, "epic-lane",
				"children_branch: \"epic/{seq}\"\nbad_key: true\nsteps:\n  - { id: a }\n")
			hub.items[parent] = &api.WorkItem{
				ID: parent, Slug: "polyforge#11", Seq: 11, ProjectName: "proj", BundleName: w80StrPtr("epic-lane"),
			}
		}, "does not load"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub()
			r, _, _ := newWorkspaceTestRuntime(t, hub, "proj")
			w80Lanes(t, r, "children_branch: \"epic/{seq}\"\n")
			hub.items = map[string]*api.WorkItem{}
			tc.set(t, r, hub)
			j := &job{
				wi:         child,
				project:    "proj",
				bundleName: "child-lane",
				capsuleDir: testCapsuleDir(r, child),
				item:       &api.WorkItem{ID: child, ProjectName: "proj", ParentID: w80StrPtr(parent)},
			}
			err := r.materialize(context.Background(), j)
			if err == nil {
				t.Fatalf("the claim is not refused: materialize %s succeeded", child)
			}
			var ae *api.Error
			if !errors.As(err, &ae) || ae.Code != api.ErrDaemonSpawnFail {
				t.Fatalf("the refusal is the typed DAEMON-507 claim-lane failure, got %v", err)
			}
			if !strings.Contains(ae.Detail, tc.want) {
				t.Fatalf("the refusal names %q, want it to carry %q", ae.Detail, tc.want)
			}
		})
	}
}
