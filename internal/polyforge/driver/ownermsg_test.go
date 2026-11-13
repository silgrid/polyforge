package driver

// ownermsg_test.go: the owner messages a hand-off's --per-owner (and an
// attrs write's) must name (ownermsg.go) are the lines the relay (relay.go,
// PR 6d) writes. The owner's words go through Driver.OwnerMessage — the
// relay's own record line, `{at, step, spawn, kind: reply|message,
// ask_at?, text, by}`, then the `owner_message` event — and count for the
// step the line's own `step` names, in the step's current round. A line
// naming another step (even a reply whose ask_at names this step's ask),
// a `delivered` receipt, a reply naming no step and a line of a closed
// round do not. The record is read, not the event (ownermsg.go's header
// says why).

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// relayOwner sends text through the relay to the current step and checks
// the line it recorded is 6d's shape for (step, spawn).
func relayOwner(t *testing.T, d *Driver, step string, spawn int64, kind, text string) string {
	t.Helper()
	ack, err := d.OwnerMessage(context.Background(), text, "alice")
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if ack.Kind != kind || ack.Step != step || ack.SpawnN != spawn || ack.Disposition != dispositionResume {
		t.Fatalf("relay ack: want %s to %s/%d with no live process, got %+v", kind, step, spawn, ack)
	}
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil || len(lines) == 0 {
		t.Fatalf("asks.jsonl: %v %d", err, len(lines))
	}
	if l := lines[len(lines)-1]; l.At != ack.At || l.Kind != kind || l.Step != step || l.Spawn != spawn || l.Text != text || l.By != "alice" {
		t.Fatalf("the relay's line carries its own step and spawn: %+v", l)
	}
	return ack.At
}

// appendLine writes one line to the capsule's record, stamped by the
// driver's clock (as the daemon's writes are).
func appendLine(t *testing.T, d *Driver, l capsule.AskLine) string {
	t.Helper()
	l.At = d.stamp()
	if err := capsule.AppendAsk(d.wi.CapsuleDir, l); err != nil {
		t.Fatal(err)
	}
	return l.At
}

// failDeliver runs build (spawn n) and starts deliver (n+1), lets lines
// write the round's record, answers whether the round then holds an owner
// message of deliver, and FAILs deliver (closing its round).
func failDeliver(t *testing.T, d *Driver, _ string, n int64, lines func(gateSpawn int64)) bool {
	t.Helper()
	secondRound(t, d, deliverStepID, n)
	lines(n + 1)
	d.mu.Lock()
	owned := len(d.ownerMessagesOfRoundLocked(deliverStepID)) > 0
	d.mu.Unlock()
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: deliverStepID, SpawnN: n + 1,
		Result: &StepResult{StepID: deliverStepID, SpawnN: n + 1, Outcome: OutcomeGateFailed}}); err != nil {
		t.Fatalf("deliver fail: %v", err)
	}
	return owned
}

// A round's owner messages are the relay's reply and message lines of the
// gate's current round; another step's lines, a delivered receipt and a
// reply naming no step are not.
func TestOwnerMessagesOfTheRoundAreTheRelaysLines(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("build"), gateSpec(deliverStepID, "build"))
	d.workflow.MaxRewinds = 5
	mustInit(t, d)

	// round 1: the gate asks (`pf ask`), the owner replies through the relay
	if c := failDeliver(t, d, execPath, 1, func(n int64) {
		appendLine(t, d, capsule.AskLine{Step: deliverStepID, Spawn: n, Kind: capsule.AskKindAsk, Text: "merge or back to code?"})
		relayOwner(t, d, deliverStepID, n, capsule.AskKindReply, "back to code")
	}); !c {
		t.Fatalf("a relayed reply is an owner message of the round")
	}
	if log.count(EvOwnerMessage) != 1 {
		t.Fatalf("the relay emitted its owner_message event: %v", log.kinds())
	}

	// round 2: a live interjection (no ask open) through the relay
	var msgAt string
	if c := failDeliver(t, d, execPath, 3, func(n int64) {
		msgAt = relayOwner(t, d, deliverStepID, n, capsule.AskKindMessage, "the fixture is wrong, go back")
	}); !c {
		t.Fatalf("a relayed message is an owner message of the round")
	}

	// round 3: lines that are no owner message to this gate in this round
	if c := failDeliver(t, d, execPath, 5, func(n int64) {
		// the delivered receipt of round 2's message, under this round's dispatch
		appendLine(t, d, capsule.AskLine{Step: deliverStepID, Spawn: n, Kind: capsule.AskKindDelivered, MsgAt: msgAt})
		// a message the owner wrote to another step
		appendLine(t, d, capsule.AskLine{Step: "build", Spawn: n - 1, Kind: capsule.AskKindMessage, Text: "x", By: "alice"})
		// a reply whose own step is another step, though it names this gate's ask
		askAt := appendLine(t, d, capsule.AskLine{Step: deliverStepID, Spawn: n, Kind: capsule.AskKindAsk, Text: "which?"})
		appendLine(t, d, capsule.AskLine{Step: "build", Spawn: n - 1, Kind: capsule.AskKindReply, AskAt: askAt, Text: "y", By: "alice"})
	}); c {
		t.Fatalf("SYMPTOM: a receipt, another step's message or a reply naming another step counted as the round's owner message")
	}

	// round 4: a reply naming no step (the mirrored v1 answer's shape, gone
	// with workflow contract v2 change 19) is no owner message to the gate
	if c := failDeliver(t, d, execPath, 7, func(n int64) {
		askAt := appendLine(t, d, capsule.AskLine{Step: deliverStepID, Spawn: n, Kind: capsule.AskKindAsk, Text: "merge?"})
		appendLine(t, d, capsule.AskLine{Kind: capsule.AskKindReply, AskAt: askAt, Text: "back to code", By: "alice"})
	}); c {
		t.Fatalf("a reply naming no step counted as the round's owner message")
	}
}

// A hand-off's --per-owner names a line the relay recorded for this step
// in this round; another step's message and a delivered receipt's stamp
// are no owner message of it.
func TestPerOwnerNamesTheRelaysLine(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("build"), gateSpec("check", "build"), spec("after"))
	mustInit(t, d)
	n := secondRound(t, d, "check", 1)
	appendLine(t, d, capsule.AskLine{Step: "check", Spawn: n, Kind: capsule.AskKindAsk, Text: "another round, or cancel?"})
	replyAt := relayOwner(t, d, "check", n, capsule.AskKindReply, "try the fixture once more")
	foreign := appendLine(t, d, capsule.AskLine{Step: "build", Spawn: n - 1, Kind: capsule.AskKindMessage, Text: "go", By: "alice"})
	receipt := appendLine(t, d, capsule.AskLine{Step: "check", Spawn: n, Kind: capsule.AskKindDelivered, MsgAt: replyAt})
	for name, at := range map[string]string{"another step's message": foreign, "a delivered receipt": receipt} {
		if d.ownerMessageAt("check", at) != nil {
			t.Fatalf("SYMPTOM: %s was taken as the owner's message to check", name)
		}
	}
	if msg := d.ownerMessageAt("check", replyAt); msg == nil || msg.Text != "try the fixture once more" {
		t.Fatalf("the relayed reply is the round's owner message: %+v", msg)
	}
}

// A re-run on the owner's word (a rewind to the step itself) stays in the
// step's round (§5.3; Step.Round does not advance): the owner's reply
// before it is still an owner message of the round — the re-run dispatch
// may cite it with --per-owner (red before: the retry's archive closed the
// round).
func TestOwnerMessageOutlivesAnIntentRetry(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec(deliverStepID, "build"))
	d.workflow.MaxRewinds = 3
	mustInit(t, d)
	n := secondRound(t, d, deliverStepID, 1)
	appendLine(t, d, capsule.AskLine{Step: deliverStepID, Spawn: n, Kind: capsule.AskKindAsk, Text: "merge as is?"})
	replyAt := relayOwner(t, d, deliverStepID, n, capsule.AskKindReply, "no, take it again from a clean conversation")
	round := readExec(t, execPath).Steps[deliverStepID].Round
	if err := settleResult(t, d, intentResult(deliverStepID, n, IntentRetry, replyAt, "", "the owner wants a fresh take")); err != nil {
		t.Fatalf("retry: %v", err)
	}
	mustStart(t, d, deliverStepID, n+1)
	if r := readExec(t, execPath).Steps[deliverStepID].Round; r != round {
		t.Fatalf("a retry stays in the round: %d -> %d", round, r)
	}
	if d.ownerMessageAt(deliverStepID, replyAt) == nil {
		t.Fatalf("SYMPTOM: the re-run dispatch cannot cite the round's reply")
	}
}
