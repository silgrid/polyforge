package daemon

// stephandoff_uncounted_test.go (polyforge#67): the lane-declared
// uncounted rewind class's own cap at the door — a gate FAIL whose
// reason falls in the declared class spends that cap, not max_rewinds:
// over it the fail without the owner's word is refused (the gate asks
// the owner first, exactly like a spent max_rewinds), under it the fail
// rewinds even past a spent max_rewinds, and every FAIL outside the class
// keeps spending max_rewinds as before.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// pf67MovedReason is the failing step's script summary the lane's
// declared class matches (the stable prefix; the ids vary).
const pf67MovedReason = "the default branch moved since verification (verified aaa1111, now bbb2222); sync, verify and publish again"

// TestStepHandoffFailOverTheUncountedBudgetAsksTheOwner: with the
// fixture lane's declared class (cap 1) on record, a FAIL whose reason
// falls in the class spends the class's own budget — refused without the
// owner's word once its cap is reached, accepted past a spent
// max_rewinds while it is not; a FAIL with any other reason spends
// max_rewinds alone.
func TestStepHandoffFailOverTheUncountedBudgetAsksTheOwner(t *testing.T) {
	f := newC4Fixture(t, "wi_pf67u")
	steps := map[string]*capsule.Step{"verification": {Status: capsule.StepInProgress, Round: 4}}
	oneUncounted := []capsule.RewindRecord{{Gate: "verification", ToStep: "build", Class: capsule.RewindUncounted}}
	threeCharged := []capsule.RewindRecord{
		{Gate: "review", ToStep: "build", Class: capsule.RewindCharged},
		{Gate: "verification", ToStep: "build", Class: capsule.RewindCharged},
		{Gate: "review", ToStep: "build", Class: capsule.RewindCharged},
	}

	// over the class's own cap: the matching FAIL without the owner's
	// word is refused, naming the class's budget and `pf step ask`
	f.writeExecRecord(t, steps, oneUncounted)
	f.as("verification", 1)
	before := f.handoffDoc(t)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "reason": pf67MovedReason, "body": c4Body()})
	if code != 400 || !strings.Contains(body, "uncounted rewind budget is spent") || !strings.Contains(body, "1 of uncounted_rewinds.max 1") ||
		!strings.Contains(body, "pf step ask") || !strings.Contains(body, "--per-owner") {
		t.Fatalf("a matching FAIL over the class's cap = %d %s, want 400 naming the class's budget and pf step ask", code, body)
	}
	if f.handoffDoc(t) != before {
		t.Fatal("the refused fail changed handoff.md")
	}
	// the same FAIL citing the owner passes
	f.ownerSays(t, "verification", "2026-11-16T05:00:00.000000001Z", "再给一轮。")
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "reason": pf67MovedReason, "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN)), "per_owner": "2026-11-16T05:00:00.000000001Z"}); code != 200 {
		t.Fatalf("a matching FAIL citing the owner over the class's cap = %d %s", code, body)
	}

	// under the class's cap: the matching FAIL rewinds past a spent
	// max_rewinds (the uncounted class is the budget it spends)
	f.writeExecRecord(t, steps, threeCharged)
	f.as("verification", 2)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "reason": pf67MovedReason, "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN))}); code != 200 {
		t.Fatalf("a matching FAIL past a spent max_rewinds = %d %s, want 200 (the class's cap is the budget it spends)", code, body)
	}

	// any other reason on the same spent max_rewinds is still refused
	f.writeExecRecord(t, steps, threeCharged)
	f.as("verification", 3)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "reason": "the demo is red", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN))}); code != 400 ||
		!strings.Contains(body, "3 of max_rewinds 3") {
		t.Fatalf("a FAIL outside the class on a spent max_rewinds = %d %s, want 400 naming max_rewinds", code, body)
	}
}
