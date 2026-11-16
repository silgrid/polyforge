package workflow_test

// children_branch_test.go (polyforge#80 u1, AC1): a lane declares the
// branch-name template its sub-tickets work on — `children_branch`, a
// git branch name over the placeholders {seq} (required, the parent's
// seq) and {project} (optional, the parent's project name). The loader
// accepts a template whose substituted name is a legal branch ref and
// refuses everything else at the key (WFC-112): a template without
// {seq}, one whose substituted name is no legal git ref (the
// check-ref-format --branch rules), and an unknown placeholder.
//
// Expected values are derived from the requirement, not from the
// implementation: the accepted template is the ticket's own example
// (`epic/{seq}`), and each refused value names a rule of
// git-check-ref-format(1) the substituted name would break.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestW80ChildrenBranchLoads pins the load of a lane that declares
// children_branch: no defect, Lane.ChildrenBranch carries the template
// verbatim, and a lane that declares none keeps the zero value (the
// key is optional).
func TestW80ChildrenBranchLoads(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo",
		"children_branch: \"epic/{seq}\"\nsteps:\n  - { id: fix }\n")
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatalf("a lane declaring children_branch loads with no defect, got %v", errs)
	}
	if lane == nil || lane.ChildrenBranch != "epic/{seq}" {
		t.Fatalf("Lane.ChildrenBranch = %v, want the verbatim epic/{seq}", lane)
	}
	// the key is optional: a lane without it loads with the zero value
	plain, errs := workflow.Load(workflowtest.WriteLane(t, t.TempDir(), "demo",
		"steps:\n  - { id: fix }\n"))
	if len(errs) != 0 || plain == nil || plain.ChildrenBranch != "" {
		t.Fatalf("a lane declaring no children_branch loads with the zero value, got %q, %v", plain, errs)
	}
}

// TestW80ChildrenBranchRefused pins every children_branch defect as its
// own one-defect load, each WFC-112 at the key: a template without {seq},
// a template whose substituted name is not a legal git ref (a forbidden
// character, a component ending .lock, a component starting with a dot,
// a trailing slash), and an unknown placeholder.
func TestW80ChildrenBranchRefused(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"no seq placeholder", "epic/1", "{seq}"},
		{"a ref git refuses (a tilde)", "epic/{seq}~x", "ref"},
		{"a component ending .lock", "epic/{seq}.lock", "ref"},
		{"a component starting with a dot", ".{seq}", "ref"},
		{"a trailing slash", "epic/{seq}/", "ref"},
		{"an unknown placeholder", "epic/{n}", "{n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := workflowtest.WriteLaneUnchecked(t, t.TempDir(), "demo",
				"children_branch: \""+tc.value+"\"\nsteps:\n  - { id: fix }\n")
			_, errs := workflow.Load(dir)
			if len(errs) != 1 || errs[0].Code != workflow.CodeInvalidValue || errs[0].Path != "children_branch" {
				t.Fatalf("defects %v, want one %s at children_branch", errs, workflow.CodeInvalidValue)
			}
			if !strings.Contains(errs[0].Msg, tc.want) {
				t.Errorf("the defect %q does not name %q", errs[0].Msg, tc.want)
			}
		})
	}
}
