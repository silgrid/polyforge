package test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// v1_s10_spawn_fence_test.go: the spawn fence, step-call ledger edition.
//
// RETIRED SUB-ASSERTIONS (Phase-4 ticket polyforge#106): the legacy
// scenario's core was the agent→daemon flush(wrap) STALE_SPAWN fence — a
// zombie survivor (or a stale flush body) carrying an old/absent
// derivation counter answered DAEMON-409 STALE_SPAWN and never reached
// the monitor. That fence lives on the WI-Agent RPC flush proxy, a
// legacy-only face with no driver counterpart, and both the route and
// protocol.FlushRequest are deleted; its sub-assertions are retired with
// this comment, not silently dropped.
//
// LIVE REMAINDER, re-expressed on the driver's step-call ledger
// contract: a dispatch settles only its OWN hand-off. HandoffOf keys an
// accepted hand-off by its dispatch — the step AND the ordinal — so a
// hand-off recorded under another ordinal never settles the live one
// (pinned at the unit layer beside this by
// TestStepRunWaitIgnoresAnotherDispatchsHandoff and
// TestLedgerAppendReadSkipsATornLine; this scenario is the one layer
// that goes through the real daemon and driver). The scenario forges an
// accepted `done` hand-off of the SAME step under ANOTHER ordinal
// (spawn 999 — no dispatch of this work item ever holds it) directly
// into the call ledger while the step awaits the owner on its open
// `pf step ask`, and pins: the forged hand-off is fenced out (the open
// ask and the durable state are undisturbed while the live dispatch
// keeps waiting for its own), and the hand-off finally accepted for
// fix's dispatch 1 is the dispatch's own — never the forged line.
func TestV1S10LedgerSpawnFence(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s10: the ledger spawn fence",
		[]map[string]any{
			askAction("fix", "s10: parked while the fence is probed.", map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"}),
			{"do": "envelope", "id": "review", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	askAt := waitAsk(t, d, wi, 30*time.Second)
	capDir := d.capsuleDir(wi)

	// Wait for the ledger to settle the ask itself (its call and result
	// pair): the daemon writes both under the step-call lock, while the
	// forged lines below are appended from THIS test process, which holds
	// no such lock. After the ask's pair the ledger is quiet — the next
	// daemon append is the owner's reply or the step's own hand-off — so
	// the two processes cannot pick the same seq.
	waitUntil(t, 30*time.Second, "the open ask never reached the call ledger", func() bool {
		for _, l := range ledgerLines(t, capDir) {
			if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbAsk && l.Step == "fix" && l.Spawn == 1 && l.Call != 0 {
				return true
			}
		}
		return false
	})

	// The fence target: an accepted hand-off of the SAME step under
	// ANOTHER ordinal, staged the way the daemon records one — a call
	// line, then its ok result line.
	forged, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindCall, Step: "fix", Spawn: 999, Verb: stepcall.VerbHandoff,
		Args: json.RawMessage(`{"status":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindResult, Step: "fix", Spawn: 999, Verb: stepcall.VerbHandoff,
		Call: forged.Seq, Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: stepcall.StatusDone}}); err != nil {
		t.Fatal(err)
	}

	// The forged hand-off is fenced out: HandoffOf keys by (step, spawn),
	// so the line under spawn 999 never settles fix's live dispatch, and
	// the driver keeps waiting for the dispatch's own hand-off — the open
	// ask and the durable state are undisturbed (the window below is the
	// driver's own poll interval plus margin: a driver that DID settle the
	// forged line would have disturbed both long before the checks).
	time.Sleep(1500 * time.Millisecond)
	if questionIDOf(statusQuiet(d), wi) != askAt {
		t.Fatal("the forged ledger hand-off disturbed the open ask")
	}
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "running" || x.Steps["fix"].Status != "in_progress" {
		t.Fatalf("the forged ledger hand-off disturbed the durable state: %+v", x)
	}

	// The fence held while the live dispatch answered for itself: the
	// owner's reply reaches the resident process, whose own hand-off is
	// the one the dispatch settles on.
	replyOwner(t, d, wi, "The fence held.")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The accepted hand-off is the real one, exactly once: fix's dispatch
	// 1 settled on its OWN call — never the forged line — and the
	// workflow closed cleanly on it.
	x = readExecution(t, capDir)
	for _, step := range []string{"fix", "review"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	if h := acceptedHandoff(t, capDir, "fix", 1); h.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("fix's real hand-off is %s, want done", h.Handoff.Status)
	} else if h.Call == forged.Seq {
		t.Fatal("the forged ledger hand-off was accepted — the spawn fence is broken")
	}
	// One awaiting_owner for the open ask, one clean terminal close.
	if n := countOf(d.eventKinds(t, wi), "awaiting_owner"); n != 1 {
		t.Fatalf("awaiting_owner fired %d times, want 1: %v", n, d.eventKinds(t, wi))
	}
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}
}
