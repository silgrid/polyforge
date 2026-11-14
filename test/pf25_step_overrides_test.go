package test

// pf25_step_overrides_test.go (u5, polyforge#25): the step overlay, end
// to end through the real pf + daemon + Go driver + fake harness on the
// micro vehicle lane (whose merge this copy declares skippable):
//
//   - the overlay chain governs the step's next dispatch over the manual
//     list and the workspace yaml alike, and a model-class failure
//     rotates along the overlay chain (AC1);
//   - the overlay survives a gate FAIL's rewind (the re-dispatched step
//     still runs it) and a clear restores the source below (AC3);
//   - the skip switch (a skippable step only) skips the step with the
//     event visible in `pf watch`, and a non-skippable step's request is
//     refused naming the waive (AC5);
//   - a write while the work item is PARKED BLOCKED rides the next
//     claim's fresh capsule (the owner's change-before-wake window,
//     AC7 + the owner ruling of 2026-11-14);
//   - a write while the work item is PAUSED lands in the kept scene and
//     governs the resumed dispatch — including the frozen current step's
//     model chain.
//
// Every overlay model is declared in the test's operator agent
// models.json (the write validates pi models against the Cell's
// step-agent copy of it, AC4).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pf25ModelsJSON is the operator agent source's models.json: one
// provider whose ids are every model the scenarios overlay.
const pf25ModelsJSON = `{
  "providers": {
    "pf25": {
      "baseUrl": "http://127.0.0.1:1",
      "api": "openai-completions",
      "models": [ {"id": "m1"}, {"id": "m2"}, {"id": "r1"}, {"id": "p1"}, {"id": "p2"}, {"id": "b1"} ]
    }
  }
}
`

// pf25ProviderErr is the provider-class death that rotates the chain.
const pf25ProviderErr = `upstream provider error: model "fake/pf25-missing" not found (fake harness injected)`

// pf25Setup answers the install, a workspace with the given yaml, and a
// started daemon whose micro copy declares merge skippable and whose
// step-agent models.json is the test's own.
func pf25Setup(t *testing.T, yaml string) (*v1Install, *w113Daemon, string) {
	t.Helper()
	inst := newV1Install(t)
	ws := t.TempDir()
	if yaml != "" {
		w079Yaml(t, ws, yaml)
	}
	// the operator agent source: auth.json (the explicit-source door) and
	// the models.json the overlay validation reads
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "auth.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "models.json"), []byte(pf25ModelsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"), inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	d := w113NewDaemon(t, inst,
		w113DOptConfig(cfg),
		w113DOptArgs("--operator-agent-dir", src),
		w113DOptArgs("--workflows-dir", pf25WorkflowsDir(t)))
	d.start()
	t.Cleanup(d.stop)
	return inst, d, ws
}

// pf25WorkflowsDir is the shipped workflows tree with micro's merge step
// declared skippable (the one declaration this feature needs a lane to
// carry; the shipped micro does not, so the test-local copy does).
func pf25WorkflowsDir(t *testing.T) string {
	t.Helper()
	return copyWorkflowsTrees(t, func(raw []byte) []byte {
		if !strings.Contains(string(raw), "name: micro") {
			return raw
		}
		return []byte(strings.Replace(string(raw), "  - id: merge\n",
			"  - id: merge\n    skippable: true\n", 1))
	}, filepath.Join(v1Root(t), "workflows"))
}

// pf25Override runs `pf override` asserting the exit code.
func pf25Override(t *testing.T, d *w113Daemon, inst *v1Install, wantExit int, args ...string) string {
	t.Helper()
	return d.pfWant(inst, wantExit, append([]string{"override"}, args...)...)
}

// TestPF25OverrideChainGovernsDispatchAndRotates: the overlay chain
// replaces the step's whole effective list — over the manual list (fix
// keeps it) and the workspace yaml — its head is the next dispatch's
// model, and a model-class death rotates along it (AC1, AC2's control:
// steps without an overlay keep the manual list).
func TestPF25OverrideChainGovernsDispatchAndRotates(t *testing.T) {
	inst, d, ws := pf25Setup(t, "steps:\n  e2e:\n    review: { models: [pi:pf25/yaml-r] }\n")
	wi := w113NewWI(t, inst, "e2e", "pf25 override chain governs and rotates",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 6000},
			{"do": "envelope", "id": "review", "outcome": "completed", "crash": "before", "crash_stderr": pf25ProviderErr},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, 0, "submit", wi, "--models", "pi:pf25/m1,pi:pf25/m2")
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 90*time.Second)
	// the overlay on the still-pending review, over manual and yaml alike
	out := pf25Override(t, d, inst, 0, wi, "--step", "review", "--models", "pi:pf25/m1,pi:pf25/m2")
	for _, want := range []string{"overlay models=pi:pf25/m1,pi:pf25/m2", "published: the capsule chain carries it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the write's rendering lacks %q:\n%s", want, out)
		}
	}
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	// fix ran the manual list (no overlay of its own); review ran the
	// overlay head and rotated to the overlay's second candidate
	if got := w079Model(t, capDir, "fix"); got != "pf25/m1" {
		t.Fatalf("fix model %q, want the manual list's head pf25/m1 (no overlay on fix)", got)
	}
	if got := w079Model(t, capDir, "review"); got != "pf25/m2" {
		t.Fatalf("review model %q, want the overlay's second candidate pf25/m2 (the provider death rotated along the overlay)", got)
	}
	x := readExecution(t, capDir)
	var reviewCandidates []string
	for _, a := range x.Steps["review"].Attempts {
		if c, _ := a["candidate"].(string); c != "" {
			reviewCandidates = append(reviewCandidates, c)
		}
	}
	if len(reviewCandidates) < 2 || reviewCandidates[0] != "pi:pf25/m1" || reviewCandidates[len(reviewCandidates)-1] != "pi:pf25/m2" {
		t.Fatalf("review attempt candidates %v, want the overlay chain [pi:pf25/m1 → pi:pf25/m2]", reviewCandidates)
	}
	// a pi model outside the step-agent models.json is refused and writes
	// nothing (AC4, through the CLI)
	out = pf25Override(t, d, inst, 1, wi, "--step", "sync", "--models", "pi:pf25/absent")
	if !strings.Contains(out, "models.json") {
		t.Fatalf("the refusal must name the step-agent models.json:\n%s", out)
	}
}

// TestPF25OverrideSurvivesRewindAndClearRestoresSource: a gate FAIL
// rewinds to fix and the re-dispatched review still runs the overlay
// chain; the clear restores the source below it (AC3).
func TestPF25OverrideSurvivesRewindAndClearRestoresSource(t *testing.T) {
	inst, d, ws := pf25Setup(t, "steps:\n  e2e:\n    review: { models: [pi:pf25/yaml-r] }\n")
	wi := w113NewWI(t, inst, "e2e", "pf25 overlay survives the rewind",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 6000},
			{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "fail"}},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, 0, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 90*time.Second)
	pf25Override(t, d, inst, 0, wi, "--step", "review", "--models", "pi:pf25/r1")
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)

	// both review dispatches (before and after the rewind) ran the overlay
	x := readExecution(t, capDir)
	var reviewCandidates []string
	for _, a := range x.Steps["review"].Attempts {
		if c, _ := a["candidate"].(string); c != "" {
			reviewCandidates = append(reviewCandidates, c)
		}
	}
	if len(reviewCandidates) != 2 || reviewCandidates[0] != "pi:pf25/r1" || reviewCandidates[1] != "pi:pf25/r1" {
		t.Fatalf("review attempt candidates %v, want the overlay chain on both sides of the rewind", reviewCandidates)
	}
	// the clear restores the source below: the view answers the yaml entry
	out := pf25Override(t, d, inst, 0, wi, "--step", "review", "--clear-models")
	if !strings.Contains(out, "overlay cleared") {
		t.Fatalf("the clear's rendering:\n%s", out)
	}
	view := pf25Override(t, d, inst, 0, wi)
	if !strings.Contains(view, "steps.e2e.review.models") {
		t.Fatalf("after the clear the view must compose from the yaml again:\n%s", view)
	}
	if strings.Contains(view, "overlay-models=pi:pf25/r1") {
		t.Fatalf("the cleared overlay must be gone from the view:\n%s", view)
	}
}

// TestPF25SkipToggleSkipsStepAndRefusesNonSkippable: the skip switch of
// the skippable tail is accepted while the run goes, the step is never
// dispatched and records skipped with the event visible in `pf watch`;
// a non-skippable step's request is refused naming the waive (AC5, AC6).
func TestPF25SkipToggleSkipsStepAndRefusesNonSkippable(t *testing.T) {
	inst, d, ws := pf25Setup(t, "")
	wi := w113NewWI(t, inst, "e2e", "pf25 skip toggle",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 6000},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, 0, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 90*time.Second)

	// a non-skippable step's skip is refused naming the waive door
	out := pf25Override(t, d, inst, 1, wi, "--step", "review", "--skip")
	if !strings.Contains(out, "waive") {
		t.Fatalf("the refusal must point at the waive door:\n%s", out)
	}
	pf25Override(t, d, inst, 0, wi, "--step", "merge", "--skip")

	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	x := readExecution(t, capDir)
	if st := x.Steps["merge"]; st == nil || st.Status != "skipped" || len(st.Attempts) != 0 {
		t.Fatalf("merge must record skipped and never dispatch: %+v", x.Steps["merge"])
	}
	// the change is visible in `pf watch --once` (AC6)
	watch := d.pfWant(inst, 0, "watch", wi, "--once", "--events", "200")
	if !strings.Contains(watch, "override_set") {
		t.Fatalf("`pf watch --once` must show the override_set event:\n%s", watch)
	}
	// the un-skip of the skipped step is accepted (a later rewind would
	// dispatch it again) and idempotent when repeated
	out = pf25Override(t, d, inst, 0, wi, "--step", "merge", "--unskip")
	if !strings.Contains(out, "nothing to change") && !strings.Contains(out, "overlay skip=false") {
		t.Fatalf("the un-skip's rendering:\n%s", out)
	}
}

// TestPF25OverrideWhileBlockedAppliesAfterResumeClaim: a write while the
// work item is PARKED BLOCKED (the owner's change-before-wake window)
// rides the daemon-local pending record into the next claim's fresh
// capsule, so the woken step's first dispatch runs it (the owner ruling
// of 2026-11-14; the sealed scene of the blocked close is not written).
func TestPF25OverrideWhileBlockedAppliesAfterResumeClaim(t *testing.T) {
	inst, d, ws := pf25Setup(t, "")
	wi := w113NewWI(t, inst, "e2e", "pf25 blocked-window overlay",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			{"do": "envelope", "id": "review", "outcome": "incomplete", "blocker": map[string]any{"reason": "pf25: blocked for the overlay window"}},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, 0, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "review", "in_progress", 120*time.Second)
	inst.waitWIStatus(t, wi, "blocked", 120*time.Second)

	// the write while blocked: the frozen current step (review) accepts a
	// model chain, into the pending record (the sealed scene stays
	// untouched)
	out := pf25Override(t, d, inst, 0, wi, "--step", "review", "--models", "pi:pf25/b1")
	if !strings.Contains(out, "recorded locally") {
		t.Fatalf("the blocked write's rendering must name the pending record:\n%s", out)
	}
	if raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "step-overrides.json")); err == nil {
		t.Fatalf("the sealed scene must not carry the overlay: %s", raw)
	}
	pend := filepath.Join(d.dataDir, "cells", "default", "pending-step-overrides.json")
	raw, err := os.ReadFile(pend)
	if err != nil || !strings.Contains(string(raw), "pi:pf25/b1") {
		t.Fatalf("the daemon-local pending record is missing: %v %s", err, raw)
	}

	// the resume's fresh claim carries it: review's first dispatch runs it
	d.pfWant(inst, 0, "resume", wi)
	inst.waitWIStatus(t, wi, "wrapped", 300*time.Second)
	x := readExecution(t, capDir)
	if st := x.Steps["review"]; st == nil || st.Status != "done" || w079Model(t, capDir, "review") != "b1" {
		t.Fatalf("the re-claimed review must run the recorded overlay: %+v", st)
	}
	// the claim consumed the record
	if raw, err := os.ReadFile(pend); err == nil && strings.Contains(string(raw), wi) {
		t.Fatalf("the claim must consume the pending record: %s", raw)
	}
}

// TestPF25OverrideWhilePausedAppliesAfterInPlaceResume: a write while the
// work item is PAUSED lands in the kept scene and governs the resumed
// dispatch — including the frozen current step's model chain (the
// change-before-wake window of the owner ruling of 2026-11-14).
func TestPF25OverrideWhilePausedAppliesAfterInPlaceResume(t *testing.T) {
	inst, d, ws := pf25Setup(t, "")
	wi := w113NewWI(t, inst, "e2e", "pf25 paused-window overlay",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 30000},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, 0, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 120*time.Second)
	d.pfWant(inst, 0, "pause", wi)
	inst.waitWIStatus(t, wi, "paused", 60*time.Second)

	// the frozen current step (fix) and the pending review both accept a
	// model chain while paused
	pf25Override(t, d, inst, 0, wi, "--step", "fix", "--models", "pi:pf25/p2")
	pf25Override(t, d, inst, 0, wi, "--step", "review", "--models", "pi:pf25/p1")
	ovRaw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "step-overrides.json"))
	if err != nil || !strings.Contains(string(ovRaw), "pi:pf25/p2") {
		t.Fatalf("the paused write must land in the kept scene: %v %s", err, ovRaw)
	}

	d.pfWant(inst, 0, "resume", wi)
	w113WaitCompleted(t, capDir, 300*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	if got := w079Model(t, capDir, "fix"); got != "pf25/p2" {
		t.Fatalf("the resumed fix must run the overlay set while paused: %q", got)
	}
	if got := w079Model(t, capDir, "review"); got != "pf25/p1" {
		t.Fatalf("review must run its overlay: %q", got)
	}
}
