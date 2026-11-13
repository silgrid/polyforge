package driver

// owner_intent_test.go: the helpers of the tests that drive a hand-off
// citing the owner straight through the transition (the owner's message
// recorded in asks.jsonl, the routed intent, settleResult).

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// waivable declares `waivable: true` on gate s.
func waivable(s StepSpec) StepSpec {
	s.Waivable = true
	return s
}

// ownerReplied records an owner message to dispatch (step, spawn) in the
// capsule's asks.jsonl — the step's ask and the owner's reply to it, in
// the exact shapes `pf step ask` and the relay (relay.go, PR 6d) write:
// the reply carries its own step and spawn — and answers the reply's
// stamp (what a hand-off's --per-owner names). The relay itself is driven in
// ownermsg_test.go; this writes its line where a test's state has no
// dispatch the relay would talk to.
func ownerReplied(t *testing.T, d *Driver, step string, spawn int64, text string) string {
	t.Helper()
	// the driver's own clock stamps the lines (as the daemon's writes do),
	// so the round boundary and the messages are ordered alike
	askAt := d.stamp()
	if err := capsule.AppendAsk(d.wi.CapsuleDir, capsule.AskLine{At: askAt, Step: step, Spawn: spawn, Kind: capsule.AskKindAsk, Text: "which?"}); err != nil {
		t.Fatal(err)
	}
	replyAt := d.stamp()
	if err := capsule.AppendAsk(d.wi.CapsuleDir, capsule.AskLine{At: replyAt, Step: step, Spawn: spawn, Kind: capsule.AskKindReply, AskAt: askAt, Text: text, By: "alice"}); err != nil {
		t.Fatal(err)
	}
	return replyAt
}

// intentResult is a cancel or rewind hand-off citing the owner, as
// handoffShape routes it (the intent: cancel, retry, rewind).
func intentResult(step string, n int64, kind, replyAt, to, reason string) *StepResult {
	return &StepResult{WIID: "wi_t1", StepID: step, SpawnN: n, Outcome: OutcomeIntent, Intent: &StepIntent{Kind: kind, ReplyAt: replyAt, To: to, Reason: reason}}
}

// secondRound drives build done and starts the gate for the round after
// the budget was spent — where the gate's agent asks and delivers an
// intent — answering the gate's dispatch ordinal.
func secondRound(t *testing.T, d *Driver, gate string, n int64) int64 {
	t.Helper()
	mustStart(t, d, "build", n)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: n, Result: completedResult("build", n, `{}`)}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, gate, n+1)
	return n + 1
}

// waiveResult is a gate's waive hand-off on the owner message replyAt, as
// handoffShape routes it (gate_failed carrying the waive intent).
func waiveResult(step string, n int64, replyAt, reason string) *StepResult {
	return &StepResult{WIID: "wi_t1", StepID: step, SpawnN: n, Outcome: OutcomeGateFailed,
		Output: json.RawMessage(`{"verdict":"fail","fail_reason":"r"}`), Intent: &StepIntent{Kind: IntentWaive, ReplyAt: replyAt, Reason: reason}}
}

// settleResult settles a routed result like settleHandoff does: the owner
// message an intent cites, looked up in the round, then the transition.
func settleResult(t *testing.T, d *Driver, res *StepResult) error {
	t.Helper()
	var msg *capsule.AskLine
	if res.Intent != nil {
		if msg = d.ownerMessageAt(res.StepID, res.Intent.ReplyAt); msg == nil {
			t.Fatalf("the owner message %s is no message of step %s's round", res.Intent.ReplyAt, res.StepID)
		}
	}
	return d.transition(TransitionRequest{Kind: TransStepResult, StepID: res.StepID, SpawnN: res.SpawnN, Result: res, OwnerMessage: msg})
}
