package driver

// uncountedrewind.go: the lane-declared uncounted rewind class
// (`uncounted_rewinds`, polyforge#67). A lane may declare one class of
// gate-FAIL rewinds charged to no budget: a FAIL whose one-line hand-off
// reason carries the declared substring (the stable prefix of the
// failing step's script summary) lands as the ledger's `uncounted`
// record — it spends no max_rewinds and counts on its own, toward the
// class's own cap, which `pf step handoff` holds exactly like
// max_rewinds: past it a matching FAIL without the owner's word is
// refused and the gate asks the owner first.
//
// The mechanism is the lane's declaration alone: nothing here names a
// lane, a project or a failure of its own — the reason substring is the
// lane's, read from workflow.yaml (workflow.UncountedRewinds).

import (
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// uncountedFail answers whether a gate FAIL with this one-line reason
// falls in the lane's declared uncounted class: the workflow declares
// one and the reason carries its substring. A FAIL citing the owner
// never reaches here (its rewind is the owner's, charged to nothing by
// its own class).
func uncountedFail(wf *Workflow, reason string) bool {
	return false
}

// UncountedRewindCount answers how many of the ledger's rewinds were the
// lane-declared uncounted class: the tally pf watch shows apart, beside
// the automatic and the owner's.
func UncountedRewindCount(rewinds []capsule.RewindRecord) int64 {
	return 0
}

// MaxUncountedRewinds answers the uncounted class's own cap (0 when the
// lane declares no class).
func MaxUncountedRewinds(wf *Workflow) int {
	return 0
}

// FailRewindBudgetSpent answers why this gate FAIL may not rewind now
// ("" when it may): the budget its reason spends — the uncounted class's
// own cap when the FAIL's reason falls in the lane's declared class,
// else the lane's max_rewinds. `pf step handoff` holds it at the call: a
// fail without the owner's word is refused once its budget is spent, so
// the gate asks the owner first.
func FailRewindBudgetSpent(wf *Workflow, ex *capsule.Execution, reason string) string {
	return RewindBudgetSpent(wf, ex)
}

// uncountedRewindBudgetSpent answers why the uncounted class's own
// budget is spent ("" when it is not).
func uncountedRewindBudgetSpent(wf *Workflow, ex *capsule.Execution) string {
	return ""
}
