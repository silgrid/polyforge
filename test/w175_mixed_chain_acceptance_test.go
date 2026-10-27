package test

// w175_mixed_chain_acceptance_test.go: polyforge#175 — every model
// candidate is `<harness>:<model>`, so one step's chain may cross
// harnesses. Through the real pf / aihub binaries and the fake harness
// (test/fakepi is both `pi` and `claude`; no real model is called, HOME
// is a scratch dir): a claude candidate that fails model-class hands the
// step to the next, pi, candidate in a NEW session whose prompt carries
// the claude transcript as read-only reference — and the reverse, with
// the chain given by `pf submit --models`.

import (
	"strings"
	"testing"
	"time"
)

// w175PiPromptHasReference answers whether the fake pi's prompt journal
// records a read-only reference for derivation spawnN.
func w175PiPromptHasReference(t *testing.T, capDir string, spawnN float64) bool {
	t.Helper()
	for _, e := range w159Journal(t, capDir, "fakepi.jsonl") {
		if e["type"] == "prompt" && e["spawn_n"] == spawnN {
			return e["prompt_has_reference"] == true
		}
	}
	t.Fatalf("no fakepi prompt journal line for spawn %v", spawnN)
	return false
}

// w175StepDispatches answers the harness and the capsule transcript of
// each of the step's dispatches, in spawn-journal order.
func w175StepDispatches(t *testing.T, capDir, step string) (harnesses, transcripts []string) {
	t.Helper()
	for _, e := range w159SpawnJournal(t, capDir) {
		if e["step_id"] != step {
			continue
		}
		h, _ := e["harness"].(string)
		tr, _ := e["transcript"].(string)
		harnesses = append(harnesses, h)
		transcripts = append(transcripts, tr)
	}
	return harnesses, transcripts
}

// claude → pi: the yaml chain [claude:claude-opus-5-5, pi:w175/pi-b];
// the claude model is unknown (404), so the step switches to the pi
// candidate in a fresh session with the claude transcript as reference.
func TestW175MixedChainClaudeToPi(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [claude:claude-opus-5-5, pi:w175/pi-b] }\n")
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "w175 claude to pi", nil)
	w159Plan(t, inst, wi, map[string]any{
		"bad_models": []string{"claude-opus-5-5"},
		"actions": []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true}, // the claude dispatch: 404 first
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}})
	capDir := w079Run(t, d, inst, ws, wi)

	var claudeFail map[string]any
	for _, e := range w159Journal(t, capDir, "fakeclaude.jsonl") {
		if e["type"] == "model_fail" {
			claudeFail = e
		}
	}
	if claudeFail == nil || claudeFail["model"] != "claude-opus-5-5" {
		t.Fatalf("the claude candidate never ran / failed: %v", w159Journal(t, capDir, "fakeclaude.jsonl"))
	}
	var piFix map[string]any
	for _, e := range w159Spawns(t, capDir, "fakepi.jsonl") {
		if e["model"] == "w175/pi-b" {
			piFix = e
		}
	}
	if piFix == nil {
		t.Fatalf("the pi candidate never ran: %v", w159Spawns(t, capDir, "fakepi.jsonl"))
	}
	if piFix["resume"] == true {
		t.Fatalf("the cross-harness switch resumed: %v", piFix)
	}
	if !w175PiPromptHasReference(t, capDir, piFix["spawn_n"].(float64)) {
		t.Fatal("the pi dispatch after the claude candidate carries no read-only reference")
	}
	hs, trs := w175StepDispatches(t, capDir, "fix")
	if strings.Join(hs, ",") != "claude,pi" || trs[0] == trs[1] {
		t.Fatalf("fix dispatches %v / %v, want claude then pi on a new transcript", hs, trs)
	}
	if got := w079Model(t, capDir, "fix"); got != "w175/pi-b" {
		t.Fatalf("fix model %q, want the pi candidate", got)
	}
}

// pi → claude, the chain given by `pf submit --models` (every step): the
// pi candidate dies with a provider error, the claude candidate runs in
// a fresh session with the pi transcript as reference.
func TestW175MixedChainPiToClaude(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "w175 pi to claude", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "crash": "before", "crash_stderr": w079ProviderErr},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	d.pfWant(inst, -1, "submit", wi, "--models", "pi:w175/pi-a, claude:claude-opus-5-5")
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 240*time.Second)

	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 1 || cl[0]["step"] != "fix" || cl[0]["model"] != "claude-opus-5-5" {
		t.Fatalf("claude spawns %v, want exactly the fix step's second candidate", cl)
	}
	if cl[0]["resume"] == true || strings.Contains(w159Argv(cl[0]), "--resume") {
		t.Fatalf("the cross-harness switch resumed: %v", cl[0])
	}
	if cl[0]["prompt_has_reference"] != true {
		t.Fatalf("the claude dispatch after the pi candidate carries no read-only reference: %v", cl[0])
	}
	hs, trs := w175StepDispatches(t, capDir, "fix")
	if strings.Join(hs, ",") != "pi,claude" || trs[0] == trs[1] {
		t.Fatalf("fix dispatches %v / %v, want pi then claude on a new transcript", hs, trs)
	}
	// the manual chain's head governs the other steps (on pi)
	for _, step := range []string{"grill", "verification"} {
		if got := w079Model(t, capDir, step); got != "w175/pi-a" {
			t.Fatalf("step %s model %q, want the manual head", step, got)
		}
	}
}
