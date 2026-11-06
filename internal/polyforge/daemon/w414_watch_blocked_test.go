package daemon

// w414_watch_blocked_test.go: the watch snapshot shows a
// blocked work item as blocked, never a pending question. (The environment
// kind and its log tail — a run request the daemon could not run — left
// with the notary, workflow contract v2 change 18.)

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

func TestW414WatchBlockedKinds(t *testing.T) {
	// a step's own `blocked` hand-off: its reason and its section
	reason := driver.StepBlockedReason("verification", map[string]any{"reason": "could not resolve host",
		"evidence": "artifacts/handoff.md, the section of step verification's call 3"})
	env := &capsule.Execution{WorkflowStatus: driver.StatusBlocked, CurrentStep: "verification", BlockedReason: reason,
		Steps: map[string]*capsule.Step{"verification": {Status: capsule.StepInProgress}}}
	got := watchBlocked(env)
	want := &protocol.WatchBlocked{Kind: "incomplete", Step: "verification", Reason: "could not resolve host",
		RequiredAction: driver.StepBlockedRequiredAction, Evidence: "artifacts/handoff.md, the section of step verification's call 3"}
	if got == nil || *got != *want {
		t.Fatalf("a step's blocked hand-off is the incomplete kind with its reason and section: %+v", got)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != `{"kind":"incomplete","step":"verification","reason":"could not resolve host","required_action":"`+driver.StepBlockedRequiredAction+`","evidence":"artifacts/handoff.md, the section of step verification's call 3"}` {
		t.Fatalf("the wire shape: %s", raw)
	}

	drv := &capsule.Execution{WorkflowStatus: driver.StatusBlocked, CurrentStep: "review", BlockedReason: "supervision: 3 consecutive failures", Steps: map[string]*capsule.Step{"review": {}}}
	if b := watchBlocked(drv); b == nil || b.Kind != "driver" || b.Reason != "supervision: 3 consecutive failures" {
		t.Fatalf("driver: %+v", b)
	}

	env.WorkflowStatus = driver.StatusRunning
	if b := watchBlocked(env); b != nil {
		t.Fatalf("a running workflow is not blocked, whatever its last reason: %+v", b)
	}
}

// Contract 3's parks have no kind of their own any more:
// a rewind-budget or effect park recorded before the upgrade projects as
// a driver park.
func TestW414NewBlockedKinds(t *testing.T) {
	for _, reason := range []string{"rewind budget spent: `merge` FAILed", "effect failed: the hub refused the move"} {
		x := &capsule.Execution{WorkflowStatus: driver.StatusBlocked, CurrentStep: "merge", BlockedReason: reason, Steps: map[string]*capsule.Step{"merge": {}}}
		if b := watchBlocked(x); b == nil || b.Kind != "driver" || b.Reason != reason {
			t.Fatalf("%q: %+v", reason, b)
		}
	}
}

// AC7: a capsule parked on its lane snapshot's contract
// version projects kind contract, its reason (both versions, the upgrade
// section) and the cancel-and-re-file action.
func TestWatchBlockedContract(t *testing.T) {
	reason := "contract: the capsule's lane snapshot targets workflow contract 0, this daemon implements contract 1 (WFC-100); it cannot run here — see docs/upgrade.md, section \"x\""
	ex := &capsule.Execution{WorkflowStatus: driver.StatusBlocked, CurrentStep: "fix", BlockedReason: reason}
	b := watchBlocked(ex)
	if b == nil || b.Kind != driver.BlockerKindContract || b.Reason != reason || b.RequiredAction != driver.ContractRequiredAction || b.Step != "fix" {
		t.Fatalf("contract block projection = %+v", b)
	}
}
