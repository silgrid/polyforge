package driver

// uncounted_rewinds_test.go: the lane-declared uncounted rewind class
// (polyforge#67) — a gate FAIL whose one-line reason falls in the lane's
// declared class lands as the ledger's uncounted record: it spends no
// max_rewinds, counts on its own, and the budget a FAIL spends routes by
// its reason (the class's own cap, else max_rewinds).

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// movedReason is the failing step's script summary: the stable prefix the
// lane declares, with the varying tail (the two commit ids) the substring
// match must ignore.
const movedReason = "the default branch moved since verification (verified aaa1111, now bbb2222); sync, verify and publish again"

// uncountedLaneOf answers the test lane declaring the uncounted class.
func uncountedLaneOf(max int) *workflow.UncountedRewinds {
	return &workflow.UncountedRewinds{ReasonContains: "the default branch moved since verification", Max: max}
}

// TestUncountedRewindClassKeepsItsOwnTally: a gate FAIL whose reason
// falls in the lane's declared uncounted class rewinds charged to nothing
// and counted apart (the ledger's class, the agent tally unchanged, the
// 回退 event's own count segment); a FAIL with any other reason is
// charged to max_rewinds as before; the budget a FAIL spends routes by
// its reason — the class's own cap for a matching FAIL (which a spent
// max_rewinds never refuses), the lane's max_rewinds for every other.
func TestUncountedRewindClassKeepsItsOwnTally(t *testing.T) {
	t.Run("a matching FAIL is uncounted, kept apart", func(t *testing.T) {
		d, sp, _, dir := handoffEnv(t, 2, spec("build"), gateSpec("check", "build"))
		d.workflow.UncountedRewinds = uncountedLaneOf(2)
		sp.script = []fakeEntry{
			done(`{}`),
			handedOff(&stepcall.Handoff{Status: stepcall.StatusFail, Reason: movedReason}, nil),
			done(`{}`), done(`{}`),
		}
		if err := d.Run(t.Context()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		x := d.Snapshot()
		r := x.RewindStats.Rewinds
		if len(r) != 1 || r[0].Class != capsule.RewindUncounted || r[0].Gate != "check" || r[0].ToStep != "build" {
			t.Fatalf("the matching FAIL lands one uncounted rewind of its edge: %+v", r)
		}
		if n := AgentRewindCount(r); n != 0 {
			t.Fatalf("the matching FAIL spends no max_rewinds: agent rewinds %d", n)
		}
		if n := UncountedRewindCount(r); n != 1 {
			t.Fatalf("the matching FAIL counts on its own: uncounted rewinds %d", n)
		}
		doc := handoffDocOf(t, dir)
		for _, want := range []string{"回退次数：0/2", "另有不计数类回退 1/2 次"} {
			if !strings.Contains(doc, want) {
				t.Fatalf("the rewind event shows the class's own tally (%q missing):\n%s", want, doc)
			}
		}
	})
	t.Run("any other reason is charged as before", func(t *testing.T) {
		d, sp, _, dir := handoffEnv(t, 2, spec("build"), gateSpec("check", "build"))
		d.workflow.UncountedRewinds = uncountedLaneOf(2)
		sp.script = []fakeEntry{
			done(`{}`),
			handedOff(&stepcall.Handoff{Status: stepcall.StatusFail, Reason: "the demo is red"}, nil),
			done(`{}`), done(`{}`),
		}
		if err := d.Run(t.Context()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		r := d.Snapshot().RewindStats.Rewinds
		if len(r) != 1 || r[0].Class != capsule.RewindCharged {
			t.Fatalf("a FAIL outside the class is charged: %+v", r)
		}
		if AgentRewindCount(r) != 1 || UncountedRewindCount(r) != 0 {
			t.Fatalf("the charged FAIL spends max_rewinds alone: agent %d uncounted %d", AgentRewindCount(r), UncountedRewindCount(r))
		}
		if doc := handoffDocOf(t, dir); strings.Contains(doc, "不计数类回退") {
			t.Fatalf("no uncounted rewind, no own-tally segment:\n%s", doc)
		}
	})
	t.Run("the budget routes by the FAIL's reason", func(t *testing.T) {
		wf := &Workflow{MaxRewinds: 2, UncountedRewinds: uncountedLaneOf(1)}
		uncounted := capsule.RewindStats{Rewinds: []capsule.RewindRecord{
			{Gate: "check", ToStep: "build", Class: capsule.RewindUncounted},
			{Gate: "check", ToStep: "build", Class: capsule.RewindUncounted},
		}}
		if why := FailRewindBudgetSpent(wf, &capsule.Execution{RewindStats: uncounted}, movedReason); why == "" || !strings.Contains(why, "uncounted") {
			t.Fatalf("a matching FAIL over the class's own cap is refused: %q", why)
		}
		if why := FailRewindBudgetSpent(wf, &capsule.Execution{RewindStats: uncounted}, "the demo is red"); why != "" {
			t.Fatalf("a FAIL outside the class spends max_rewinds, not the class's cap: %q", why)
		}
		spent := capsule.RewindStats{Rewinds: []capsule.RewindRecord{
			{Gate: "check", ToStep: "build", Class: capsule.RewindCharged},
			{Gate: "check", ToStep: "build", Class: capsule.RewindCharged},
		}}
		if why := FailRewindBudgetSpent(wf, &capsule.Execution{RewindStats: spent}, movedReason); why != "" {
			t.Fatalf("a matching FAIL rewinds past a spent max_rewinds (the class's own budget is the one it spends): %q", why)
		}
		if why := FailRewindBudgetSpent(wf, &capsule.Execution{RewindStats: spent}, "the demo is red"); why == "" || !strings.Contains(why, "max_rewinds 2") {
			t.Fatalf("any other FAIL on a spent max_rewinds is refused: %q", why)
		}
	})
}
