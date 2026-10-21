package test

// w114 (polyforge#114, root-cause lane step "repro"): the silent
// step-process wedge — a step process whose next provider call never
// returns parks alive with an open turn, no envelope, no exit, no
// transcript growth (the #113 hand-found shape). The old probe lane acked
// "driver-alive" for it forever; v2-converge 2.3 detects it directly.
//
// Two scenarios against the REAL `pf daemon`, both scripted through the
// fakepi envelope vocabulary's "park" action (test scaffolding only — it
// manufactures the #113 wedge shape deterministically; nothing in
// production imports fakepi):
//
//   - TestW114StepWedgeDetectedWithoutOperator: the first step parks
//     silently with its turn open (waiting on a model that never
//     answers). The supervisor judges rule 2 model_hung within the
//     silence window and sends ONE step_dead verdict; the driver kills
//     the process, records ONE failure, advances the model candidate
//     (model-class cause) and re-dispatches — the workflow wraps WITHOUT
//     operator intervention.
//   - TestW114StepWedgeOperatorKillRecovers pins the OTHER half of the
//     report: the identical wedge, then the operator's kill (the #113
//     intervention). A kill is an exit WITHOUT a result: the driver
//     classifies it itself (no model-class sign: same-model retry, the
//     first retry resumes the transcript) — no supervisor verdict and no
//     recovery engine involved (the old probe/exit race that made this
//     test flaky is gone with the probe).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// w114Plan parks the first dispatch of grill and scripts the healthy
// continuation for every later ordinal, so a daemon that DOES detect the
// wedge kills it, re-dispatches in place under the next ordinal, and the
// workflow completes and wraps.
func w114Plan() []map[string]any {
	return []map[string]any{
		// Ordinal 1: grill parks silently — open turn, then nothing:
		// no envelope, no exit, no further record, indefinitely.
		{"do": "envelope", "id": "grill", "park": true},
		// Ordinals 2..5: the recovery continuation (grill resume) and
		// the remaining steps, all healthy.
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}
}

// w114WaitParked waits until the first step process has parked (the
// wedge is in place: park journaled, process alive, no envelope).
func w114WaitParked(t *testing.T, d *v1Daemon, wi string) {
	t.Helper()
	capDir := d.capsuleDir(wi)
	waitUntil(t, 30*time.Second, "the wedge never engaged (no park journal record for grill)", func() bool {
		for _, rec := range fakeJournal(t, capDir) {
			if rec["type"] == "park" && rec["step"] == "grill" {
				return true
			}
		}
		return false
	})
	waitUntil(t, 15*time.Second, "the parked step process is not alive", func() bool {
		return len(orphanPIDs(wi)) > 0
	})
}

// w114WedgeDiagnostic is the repro's evidence snapshot: everything the
// supervision surface shows while the step process sits wedged.
func w114WedgeDiagnostic(t *testing.T, d *v1Daemon, inst *v1Install, wi string) string {
	t.Helper()
	capDir := d.capsuleDir(wi)
	var journalKinds []string
	for _, rec := range fakeJournal(t, capDir) {
		if k, _ := rec["type"].(string); k != "" {
			journalKinds = append(journalKinds, k)
		}
	}
	envelope := ""
	if raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "pending-results", "1.json")); err == nil {
		envelope = fmt.Sprintf("%d bytes", len(raw))
	} else if os.IsNotExist(err) {
		envelope = "absent (the wedge never wrote it)"
	} else {
		envelope = "unreadable: " + err.Error()
	}
	return fmt.Sprintf(
		"wi status=%q (want eventual wrapped)\n"+
			"event kinds: %v\n"+
			"fakepi journal: %v\n"+
			"step envelope drop (ordinal 1): %s\n"+
			"parked step processes alive: %v\n",
		inst.wiStatus(t, wi), d.eventKinds(t, wi),
		journalKinds, envelope, orphanPIDs(wi))
}

func TestW114StepWedgeDetectedWithoutOperator(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "w114: step process parks silently mid-run (the #113 wedge)",
		w114Plan(), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{silence: 2 * time.Second}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The wedge engages: grill's first dispatch parks (open turn, no
	// envelope, no exit, no transcript growth).
	w114WaitParked(t, d, wi)
	wedged := orphanPIDs(wi)

	// The acceptance (polyforge#114): a step process that parks silently
	// past the window is detected WITHOUT operator intervention — the
	// supervisor's verdict names rule 2, with evidence.
	rec := waitForEvent(t, d, wi, "step_dead", 30*time.Second)
	var v struct {
		Step     string `json:"step"`
		Cause    string `json:"cause"`
		Evidence string `json:"evidence"`
	}
	eventBody(t, rec, &v)
	if v.Cause != "model_hung" || v.Step != "grill" || v.Evidence == "" {
		t.Fatalf("verdict %+v, want step_dead model_hung for grill with evidence.\nobserved state:\n%s",
			v, w114WedgeDiagnostic(t, d, inst, wi))
	}
	// The driver's handling: one failure record, candidate switched (a
	// model that never answers is a model-class death), fresh dispatch.
	fail := waitForEvent(t, d, wi, "step_failed", 30*time.Second)
	var f struct {
		Cause       string `json:"cause"`
		Disposition string `json:"disposition"`
		From        string `json:"from_model"`
		To          string `json:"to_model"`
		Count       int    `json:"failure_count"`
	}
	eventBody(t, fail, &f)
	if f.Cause != "model_hung" || f.Disposition != "switch_model" || f.From == "" || f.To == "" || f.From == f.To || f.Count != 0 {
		t.Fatalf("step_failed = %+v, want model_hung/switch_model with distinct models, counter reset to 0", f)
	}

	// Detected and recovered, still with no operator action.
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	for _, pid := range wedged {
		if syscall.Kill(pid, 0) == nil {
			t.Fatalf("the wedged step process %d survived its verdict", pid)
		}
	}
	// The dispatches ran under the chain's first two candidates, in order.
	// The failure record names candidates (`<harness>:<model>`,
	// polyforge#175); the harness journals the bare model its --model got.
	_, fromModel, _ := strings.Cut(f.From, ":")
	_, toModel, _ := strings.Cut(f.To, ":")
	var models []string
	for _, rec := range fakeJournal(t, d.capsuleDir(wi)) {
		if rec["type"] == "spawn" {
			if m, _ := rec["model"].(string); m != "" {
				models = append(models, m)
			}
		}
	}
	if !strings.HasPrefix(f.From, "pi:") || !strings.HasPrefix(f.To, "pi:") || len(models) < 2 || models[0] != fromModel || models[1] != toModel {
		t.Fatalf("dispatch models %v, want [%s %s ...] (from %s → %s)", models, fromModel, toModel, f.From, f.To)
	}
	// ONE verdict, ONE failure record for the wedged dispatch.
	kinds := d.eventKinds(t, wi)
	if n := countOf(kinds, "step_dead"); n != 1 {
		t.Fatalf("step_dead fired %d times, want 1: %v", n, kinds)
	}
	if n := countOf(kinds, "step_failed"); n != 1 {
		t.Fatalf("step_failed recorded %d times, want 1: %v", n, kinds)
	}
}

func TestW114StepWedgeOperatorKillRecovers(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "w114 control: the #113 operator kill against the identical wedge",
		w114Plan(), nil)
	// default bounds: detection stays out of this control's way
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The identical wedge engages.
	w114WaitParked(t, d, wi)

	// Let the wedge sit a while first — the exact state the operator
	// found in #113.
	time.Sleep(3 * time.Second)

	// The operator intervention (#113: found by hand, killed by hand).
	pids := orphanPIDs(wi)
	if len(pids) == 0 {
		t.Fatalf("no live step process to kill — the wedge never engaged")
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}

	// A kill is an exit WITHOUT a result: the driver classifies it
	// itself — exit_other, same-model retry, resume shape — and the
	// workflow wraps. Deterministic: no supervisor verdict can race it.
	fail := waitForEvent(t, d, wi, "step_failed", 60*time.Second)
	var f struct {
		Cause       string `json:"cause"`
		Disposition string `json:"disposition"`
		Next        string `json:"next_dispatch"`
		Count       int    `json:"failure_count"`
	}
	eventBody(t, fail, &f)
	if f.Cause != "exit_other" || f.Disposition != "retry_same_model" || f.Next != "resume" || f.Count != 1 {
		t.Fatalf("step_failed = %+v, want exit_other/retry_same_model/resume/1", f)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	spawns := waitSpawnCount(t, d, wi, 4, 60*time.Second)
	if len(spawns) != 4 || spawns[1]["resume"] != true {
		t.Fatalf("the retry was not an in-place resume: %+v", spawns)
	}
	if n := countOf(d.eventKinds(t, wi), "step_dead"); n != 0 {
		t.Fatalf("a supervisor verdict raced the operator kill: %v", d.eventKinds(t, wi))
	}
}
