package driver

// ownermsg.go: the owner's messages to a step, as the daemon's own record
// holds them (workflow contract v2 §2.2, PR 8): the lines of the
// capsule's runtime-state/asks.jsonl — the step's asks (`pf ask`), the
// owner's replies to them and the
// owner's messages sent while no ask was open (the relay, PR 6d). They
// replace the archived answers as what a step's `<id>.answers` input
// names, what P6 (`on_fail.charge: unless_owner_answered`) counts for the
// gate's current round, and what an intent's `reply_at` must name (B-6):
// one of this step's owner messages of this round, by its own stamp.
//
// A round is the step's own (capsule.Step.Round, the PF_ROUND every
// dispatch of it carries). The record's lines carry their dispatch, not
// their round, so the round's opening is read off the step's attempts
// ledger: every archive that advances Round closes the round before it —
// an accepted result, a FAIL verdict that rewound, a rewind archive — and
// every message stamped after the newest such archive belongs to the
// current round. The archives that stay in the round are
// skipped: an infra failure (a crash re-dispatch) and an intent retry
// (§5.3: the same round, a new conversation). A step never archived is in
// its first round: every message counts. The daemon reads the file's
// lines and stamps only; the words reach the agent verbatim.
//
// A reply or message belongs to the step its OWN `step` names: the relay
// (driver/relay.go) writes on both the step the owner addressed and the
// dispatch current when the owner wrote (`spawn`). The relay's `delivered` lines are
// delivery receipts, never owner messages. The record alone is read, not
// the relay's `owner_message` event: the relay appends the line FIRST and
// the event after it (the record is the authority, relay.go), and an
// event is news, never state — best effort, kept in the daemon's data
// dir rather than the capsule, gone past the events retention — so a
// cross-check against it would refuse a real owner message whose event
// append failed or which a restore on another machine never carried.

import (
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// stepAskLines answers every line of the record that belongs to step: its
// asks, the replies to them and the messages addressed to it, in order
// (nil when the capsule has no record, or it is unreadable: nothing
// counts). An ask, a reply and a message belong to the step their own
// `step` names.
func (d *Driver) stepAskLines(step string) []capsule.AskLine {
	if d.wi.CapsuleDir == "" {
		return nil
	}
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return nil
	}
	return askLinesOf(lines, step)
}

// askLinesOf filters lines to those of step (stepAskLines' rule).
func askLinesOf(lines []capsule.AskLine, step string) []capsule.AskLine {
	var out []capsule.AskLine
	for _, l := range lines {
		switch l.Kind {
		case capsule.AskKindAsk, capsule.AskKindReply, capsule.AskKindMessage:
		default:
			continue // a delivered line: a receipt, not a message
		}
		if l.Step != "" && l.Step == step {
			out = append(out, l)
		}
	}
	return out
}

// ownerMessagesOf answers the owner's messages among lines (the reply and
// message lines) stamped after since ("" = all).
func ownerMessagesOf(lines []capsule.AskLine, since string) []capsule.AskLine {
	var out []capsule.AskLine
	for _, l := range lines {
		if l.Kind != capsule.AskKindReply && l.Kind != capsule.AskKindMessage {
			continue
		}
		if since != "" && !stampAfter(l.At, since) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// roundStart answers the stamp the step's current round (Step.Round)
// opened at: the archive stamp of its newest attempt that closed a round —
// any but an infra failure and an intent retry, which stay in it; "" for a
// step in its first round.
func roundStart(st *capsule.Step) string {
	if st == nil {
		return ""
	}
	for i := len(st.Attempts) - 1; i >= 0; i-- {
		a := st.Attempts[i]
		if !closesRound(a.Cause) {
			continue
		}
		if a.ArchivedAt != "" {
			return a.ArchivedAt
		}
		return a.InterruptedAt
	}
	return ""
}

// closesRound reports whether an attempt archived with cause ended the
// step's round (Step.Round advances after it): every cause but the two a
// round survives — a crash re-dispatch (infra_failure) and an intent
// retry (intent_retry).
func closesRound(cause string) bool {
	return cause != CauseInfraFailure && cause != CauseIntentRetry
}

// ownerMessagesOfRoundLocked answers the owner's messages to step in its
// current round. The caller holds d.mu; the record is read from the
// capsule (the driver's writes already happen under its lock).
func (d *Driver) ownerMessagesOfRoundLocked(step string) []capsule.AskLine {
	return ownerMessagesOf(d.stepAskLines(step), roundStart(d.exec.Steps[step]))
}

// ownerMessageAt answers the owner message of step's current round
// stamped at (nil when none: an intent naming it is refused, B-6).
func (d *Driver) ownerMessageAt(step, at string) *capsule.AskLine {
	d.mu.Lock()
	since := roundStart(d.exec.Steps[step])
	d.mu.Unlock()
	for _, l := range ownerMessagesOf(d.stepAskLines(step), since) {
		if l.At == at {
			cp := l
			return &cp
		}
	}
	return nil
}

// OwnerMessagesOfRound answers, from the record's lines, the owner's
// messages to step in the round st records (nil st: its first round) —
// the daemon's `pf step handoff` stamp and its --per-owner check
// (workflow contract 4,), by the rules above.
func OwnerMessagesOfRound(lines []capsule.AskLine, step string, st *capsule.Step) []capsule.AskLine {
	return ownerMessagesOf(askLinesOf(lines, step), roundStart(st))
}

// RoundAttempts answers the attempts st archived in its current round —
// those after the newest one that closed a round (nil st: none).
func RoundAttempts(st *capsule.Step) []capsule.Attempt {
	if st == nil {
		return nil
	}
	from := 0
	for i := len(st.Attempts) - 1; i >= 0; i-- {
		if closesRound(st.Attempts[i].Cause) {
			from = i + 1
			break
		}
	}
	return st.Attempts[from:]
}
