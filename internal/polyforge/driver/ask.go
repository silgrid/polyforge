package driver

// ask.go: the step's explicit "I need the owner" signal and the
// AWAITING-OWNER state it parks the step in (workflow contract v2, design
// §2.2 and §5.2, owner ruling Q9; PR 6c).
//
// The agent runs `pf ask "<question>"` — a local command that appends
// `{at, step, spawn, kind: "ask", text}` to the capsule's
// runtime-state/asks.jsonl (capsule.AppendAsk) and tells the agent to end
// its turn. The driver watches that file while a dispatch runs
// (askPollInterval, at most 2 s late) and the supervisor re-checks it at
// a turn end before rule 1b fires (observe.Monitor.judge): an ask of the
// CURRENT dispatch that no reply closes parks the step awaiting the owner:
//
//   - the event `awaiting_owner` carries the question verbatim into the
//     events log; the decision feed classes it as a question (the
//     attention channel Pi Web's orange button and card read), and the
//     watch snapshot projects it as the step's pending question (id = the
//     ask's `at`);
//   - the running slot is released (Config.OwnerWait: the runtime's job
//     flag the daemon's slot accounting reads); a step declaring
//     resources keeps no warm window, so what it held is released with
//     the process (resources.go, stepDeclaresResources);
//   - supervision stops counting the dispatch (StepOwnerWait: silence, the
//     tool window and T are not judged while the owner is asked), and the
//     wall booked into T ends where the wait began;
//   - the process stays resident for the warm window
//     (supervise.Defaults.AskWarmWindow, [daemon] ask_warm_window, default
//     15 min), then the driver ends it — stdin closed, then the signal,
//     the same teardown an accepted envelope gets — and the step STAYS
//     awaiting; a process that exits by itself while waiting is treated
//     the same (no death, no retry: the question stands);
//   - the driver's plan pass parks on the open ask — no dispatch, no
//     stall clock — and a restart
//     finds the open ask in the file and parks again without a new
//     dispatch (lifecycle (i)); the owner's reply (PR 6d) is appended as
//     `{at, kind: "reply", ask_at, text, by}` and resumes the session.
//
// A turn that ends with neither a result nor an open ask is NOT waiting:
// rule 1b (ended_without_result) applies unchanged, so an accidental stop
// is never mistaken for a question. A result that lands while an ask is
// open wins: the envelope is the step's only completion signal.
//
// An ask is keyed by its own `at` and belongs to one dispatch (step,
// spawn): a later dispatch of the step never inherits it, and a reply
// names the ask it answers by that stamp — never by a harness message id.
// The daemon reads the file's shape only (is there an ask of this
// dispatch without a reply?); the question's words are shown, never read.
// (The v1 needs_input question, mirrored here as an ask line while the
// driver still accepted it, left with workflow contract v2 change 19.)

import (
	"context"
	"fmt"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// EvAwaitingOwner is the driver event of a step parked on an open ask:
// `{step, spawn_n, ask_at, question_id, question, resumed?}` — the
// question verbatim, `question_id` = `ask_at` (the ask's own stamp, the
// id every face shows it under), `resumed: true` when a restarted driver
// found the ask still open.
const EvAwaitingOwner = "awaiting_owner"

// DefaultAskPoll is how often a running dispatch's asks.jsonl is re-read
// (the owner ruling: at most 2 s between `pf ask` and the daemon noticing).
const DefaultAskPoll = time.Second

// awaitingOwnerEnd is awaitDispatch's answer when the dispatch ended in the
// awaiting-owner state: its process is gone (the warm window ran out, or
// it exited while idle) with the ask still open. It is no failure — the
// Run loop books the wall up to Since, runs the step-end hook and parks on
// the ask. It unwraps to nothing on purpose: no classifier reads it.
type awaitingOwnerEnd struct {
	Ask   capsule.AskLine
	Since time.Time // when the dispatch was marked awaiting
}

func (e *awaitingOwnerEnd) Error() string {
	return fmt.Sprintf("driver: step %s spawn %d awaits the owner (ask %s)", e.Ask.Step, e.Ask.Spawn, e.Ask.At)
}

// askPoll answers the dispatch poll interval of asks.jsonl.
func (d *Driver) askPoll() time.Duration {
	if d.cfg.AskPoll > 0 {
		return d.cfg.AskPoll
	}
	return DefaultAskPoll
}

// askWarmWindow answers the warm window a waiting process is kept for.
func (d *Driver) askWarmWindow() time.Duration {
	return d.limits().AskWarmWindow
}

// openAsk answers the open ask of dispatch (stepID, n) in the capsule's
// record, nil when none or unreadable (the next poll reads again).
func (d *Driver) openAsk(stepID string, n int64) *capsule.AskLine {
	if d.wi.CapsuleDir == "" {
		return nil
	}
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return nil
	}
	return capsule.OpenAsk(lines, stepID, n)
}

// parkedAsk answers the open ask of the CURRENT dispatch — the current
// step in_progress under its dispatch ordinal (capsule.CurrentDispatch)
// and an ask of exactly that dispatch no reply closes — or nil. The state is read under the lock, the file outside it.
func (d *Driver) parkedAsk() *capsule.AskLine {
	d.mu.Lock()
	step, n, ok := capsule.CurrentDispatch(d.exec)
	d.mu.Unlock()
	if !ok {
		return nil
	}
	return d.openAsk(step, n)
}

// refreshParkedAsk reads the current dispatch's open ask into d.parked
// (the lock-free fact unsettledStepLocked consults): a Run start reads it
// before it decides whether the dispatch it found needs an interruption
// archive or a claim re-entry — a dispatch awaiting the owner needs
// neither, it is settled by its ask.
func (d *Driver) refreshParkedAsk() {
	ask := d.parkedAsk()
	d.mu.Lock()
	d.parked = ask
	d.mu.Unlock()
}

// parkedAskLocked reports whether st (the current step) is parked on an
// open ask the last refresh read: the ask names its step and dispatch.
func (d *Driver) parkedAskLocked(st *capsule.Step) bool {
	return d.parked != nil && d.parked.Step == d.exec.CurrentStep && d.parked.Spawn == st.Dispatch
}

// ownerWait tells the runtime whether this work item's step is waiting on
// the owner (the daemon's slot accounting reads the flag): true releases
// the running slot, false takes it back before the next dispatch.
func (d *Driver) ownerWait(waiting bool) {
	if d.cfg.OwnerWait != nil {
		d.cfg.OwnerWait(waiting)
	}
}

// markAwaitingOwner records that the running dispatch (stepID, n) asked
// the owner: the slot release, the supervisor's StepOwnerWait (nothing
// judged from here on) and the awaiting_owner event (the question
// verbatim) — in that order, so whoever reads the queue once it sees the
// event finds the slot already released. Called once per dispatch, from
// the dispatch wait.
func (d *Driver) markAwaitingOwner(stepID string, n int64, ask *capsule.AskLine) {
	d.mu.Lock()
	d.parked = ask
	d.inputEventFor = ask.At
	d.mu.Unlock()
	d.ownerWait(true)
	d.fact(supervise.StepOwnerWait{Step: stepID, SpawnN: n, Waiting: true, AskAt: ask.At})
	d.emit(EvAwaitingOwner, map[string]any{
		"step": stepID, "spawn_n": n, "ask_at": ask.At, "question_id": ask.At, "question": ask.Text,
	})
}

// announceParkedAsk is the plan pass's announcement of a park on an open
// ask: no step process
// runs, so the supervisor runs no dispatch timer and no stall clock
// (StepWaitingHuman keyed by the ask's stamp), the slot stays released,
// and a driver that finds the ask already open — a restart — re-announces
// the event once per life with `resumed: true`.
func (d *Driver) announceParkedAsk(ask *capsule.AskLine) {
	d.mu.Lock()
	step, n := d.exec.CurrentStep, d.spawnN
	announced := d.waitingAnnounced == ask.At
	d.waitingAnnounced = ask.At
	reannounce := d.inputEventFor != ask.At
	d.inputEventFor = ask.At
	d.parked = ask
	d.mu.Unlock()
	d.ownerWait(true)
	if reannounce {
		d.emit(EvAwaitingOwner, map[string]any{
			"step": step, "spawn_n": n, "ask_at": ask.At, "question_id": ask.At, "question": ask.Text, "resumed": true,
		})
	}
	if announced || d.cfg.Link == nil {
		return
	}
	d.cfg.Link.Fact(supervise.StepWaitingHuman{Step: step, SpawnN: n, QuestionID: ask.At})
}

// awaitParkedAsk parks the loop while the current dispatch's ask stays
// open: it re-reads the record every poll (a reply line is appended by
// another process — the relay, PR 6d — so no wake announces it) and
// re-plans when the ask is closed, the state moved on (a rewind, a
// terminal, a cancel), the driver failed, or ctx ended.
func (d *Driver) awaitParkedAsk(ctx context.Context, ask *capsule.AskLine) error {
	for {
		d.mu.Lock()
		failed := d.failed != nil
		d.mu.Unlock()
		cur := d.parkedAsk()
		if failed || cur == nil || cur.At != ask.At {
			d.mu.Lock()
			d.parked = cur
			d.mu.Unlock()
			return nil // plan re-reads the state
		}
		t := time.NewTimer(d.askPoll())
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-d.wake:
			t.Stop()
		case <-t.C:
		}
	}
}
