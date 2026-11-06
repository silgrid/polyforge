package test

import (
	"testing"
	"time"
)

// v1_s21: the F15 progress-semantics regression and the #99 parked-
// question stillness, v2 supervisor edition (v2-converge 2.3 "real
// output": only work-class harness signals count as life). The pilot's
// double-blind wedge (#46) was a turn stuck on an in-flight model call
// while an extension's UI chatter kept streaming records — under the old
// "any record refreshes the heartbeat" rule the stream looked forever
// fresh. Two shapes on the driver's step processes, whose stdout reaches
// the supervisor through the harness translator:
//
//   - shape A: a step process emitting ONLY extension_ui_request records
//     (UI noise — never work) is silence: rule 0 judges it harness_silent
//     within the silence window although records keep flowing, the ops
//     status shows a step age with NO last-work age meanwhile, and the
//     driver's same-model retry then proceeds to an open ask;
//   - shape B (legal slow work): an open turn whose model keeps
//     streaming deltas stays alive past many silence windows — LAST-WORK
//     stays fresh, no verdict fires.
//
// Both shapes then park on an open `pf ask` for several windows with the
// supervision bounds shrunk to seconds: NO dispatch timer and NO stall
// clock may run while the owner is asked (#99; the awaiting dispatch is
// not judged), and the owner's reply completes the run.
//
// s21Window is that shrunk silence and stall bound. Besides firing where
// the scenario wants a verdict, it must NOT fire on the legitimate work in
// between: the driver's step-end → next step-start path (a few ms idle, a
// handful of fsynced writes) and a fresh dispatch's first output (~0.4s
// idle). At 2s a loaded gate exceeded it once: the stall rule judged the
// driver stalled between dispatch 1's verdict and dispatch 2's start, the
// recovery relaunch cancelled dispatch 2 (the asking dispatch) and the
// plan's next ordinal completed the workflow, so the ask never showed.
// Every wait and park below is expressed in windows of it.
const s21Window = 5 * time.Second

func TestV1S21F15ProgressSemantics(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst, optSupervision(supBounds{
		silence: s21Window, stall: s21Window, tool: 30 * time.Second,
	}))
	d.start()

	// ------------------------------------------------------------------
	// shape A: UI noise is not work — silence, judged harness_silent.

	wiA := inst.newWI(t, "s21-A: UI noise never proves progress (rule 0)",
		[]map[string]any{
			// Ordinal 1: the zero-work harness streaming UI chatter.
			{"do": "envelope", "id": "fix", "silent": true, "noise": "ui", "noise_ms": 50},
			// Ordinal 2: the same-model retry asks the owner and parks.
			askAction("fix", "s21-A: park here (the test replies after the stillness pin).",
				map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"}),
			{"do": "envelope", "id": "review", "outcome": "completed"},
		}, nil)
	d.pf(t, "submit", wiA)
	// While the chattering dispatch is alive its step age grows and its
	// last-work age stays absent: the owner face tells "records flowing"
	// from "work happening".
	waitUntil(t, 15*time.Second, "shape A: STEP-AGE never showed without LAST-WORK through the UI noise", func() bool {
		for _, j := range d.status(t).Jobs {
			if j.WorkItemID == wiA && j.StepAgeSec != nil && *j.StepAgeSec >= 1 && j.LastWorkAgoSec == nil {
				return true
			}
		}
		return false
	})
	rec := waitForEvent(t, d, wiA, "step_dead", 30*time.Second)
	var v struct {
		Cause    string `json:"cause"`
		Evidence string `json:"evidence"`
	}
	eventBody(t, rec, &v)
	if v.Cause != "harness_silent" {
		t.Fatalf("shape A: verdict %+v, want step_dead harness_silent (UI noise is silence)", v)
	}
	waitAsk(t, d, wiA, 30*time.Second)

	// #99: parked on the owner for several windows of every bound —
	// nothing may fire (the awaiting dispatch is not judged; the stall
	// clock is exempt).
	time.Sleep(2*s21Window + time.Second)
	assertQuietWhileParked(t, d, wiA, 1)
	replyOwner(t, d, wiA, "Shape A done.")
	waitWorkflowStatus(t, d, wiA, "completed", 120*time.Second)
	inst.waitWIStatus(t, wiA, "wrapped", 60*time.Second)
	killOrphans(wiA)

	// ------------------------------------------------------------------
	// shape B: legal slow work — a streaming model is alive.

	wiB := inst.newWI(t, "s21-B: model deltas prove progress (alive stays alive)",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed",
				"open_turn": true, "noise": "progress", "noise_ms": 50, "ms": int((3*s21Window + time.Second) / time.Millisecond)},
			askAction("review", "s21-B: park here.", map[string]any{"do": "envelope", "id": "review", "outcome": "completed"}),
		}, nil)
	capB := d.capsuleDir(wiB)
	d.pf(t, "submit", wiB)
	t.Cleanup(func() { killOrphans(wiB) })

	waitUntil(t, 15*time.Second, "shape B: the progress stream never started", func() bool {
		for _, rec := range fakeJournal(t, capB) {
			if rec["type"] == "ui_noise" && rec["kind"] == "progress" {
				return true
			}
		}
		return false
	})
	// Three silence windows of streaming: LAST-WORK stays fresh, nothing
	// fires.
	deadline := time.Now().Add(3 * s21Window)
	for time.Now().Before(deadline) {
		for _, j := range d.status(t).Jobs {
			if j.WorkItemID == wiB && j.LastWorkAgoSec != nil && float64(*j.LastWorkAgoSec) >= s21Window.Seconds() {
				t.Fatalf("shape B: LAST-WORK stale (%vs) while the model streams deltas", *j.LastWorkAgoSec)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if n := countOf(d.eventKinds(t, wiB), "step_dead"); n != 0 {
		t.Fatalf("shape B: the supervisor judged a streaming model dead: %v", d.eventKinds(t, wiB))
	}
	waitAsk(t, d, wiB, 30*time.Second)
	time.Sleep(2*s21Window + time.Second)
	assertQuietWhileParked(t, d, wiB, 0)
	replyOwner(t, d, wiB, "Shape B done.")
	waitWorkflowStatus(t, d, wiB, "completed", 120*time.Second)
}

// assertQuietWhileParked pins the #99 stillness: with an ask open for
// several windows of every bound, the supervisor issued no more than the
// expected number of verdicts (wantVerdicts: the earlier legitimate
// ones), and neither a driver stall nor a recovery fired.
func assertQuietWhileParked(t *testing.T, d *v1Daemon, wi string, wantVerdicts int) {
	t.Helper()
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "step_dead"); n != wantVerdicts {
		t.Fatalf("supervisor verdicts while parked: %d, want %d: %v", n, wantVerdicts, kinds)
	}
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "step_dead" && wantVerdicts == 0 {
			t.Fatalf("unexpected verdict: %s", e.Body)
		}
	}
	if n := countOf(kinds, "needs_recovery"); n != 0 {
		t.Fatalf("a recovery fired while a human was asked: %v", kinds)
	}
}
