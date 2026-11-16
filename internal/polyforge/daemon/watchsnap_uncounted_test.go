package daemon

// watchsnap_uncounted_test.go (polyforge#67): the watch snapshot's
// rewind tally carries the lane-declared uncounted class apart — its own
// count from the ledger and its cap from the lane's declaration, beside
// the automatic rewinds and the owner's.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// TestWatchSnapshotCarriesTheUncountedTally: one uncounted rewind on the
// ledger of a lane declaring the class (the fixture lane's cap 1)
// reaches the watch wire shape as the class's own count against its own
// cap, spending neither max_rewinds nor the owner's tally.
func TestWatchSnapshotCarriesTheUncountedTally(t *testing.T) {
	f := newC4Fixture(t, "wi_pf67w")
	f.writeExecRecord(t, map[string]*capsule.Step{"verification": {Status: capsule.StepInProgress, Round: 2}}, []capsule.RewindRecord{
		{Gate: "verification", ToStep: "build", Class: capsule.RewindUncounted},
	})
	we := f.td.readExecution(f.wi, nil)
	if we == nil || we.Rewinds == nil {
		t.Fatalf("the watch snapshot carries the rewind tally: %+v", we)
	}
	rw := we.Rewinds
	if rw.Agent != 0 || rw.Owner != 0 || rw.Uncounted != 1 || rw.MaxRewinds != 3 || rw.MaxUncountedRewinds != 1 {
		t.Fatalf("the uncounted class keeps its own tally and cap (0/3 automatic, 0 owner, 1/1 uncounted): %+v", *rw)
	}
}
