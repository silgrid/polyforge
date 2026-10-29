package test

// w176_effort_chain_acceptance_test.go: polyforge#176 — a candidate is
// `<harness>:<model>[:<effort>]`. Through the real pf / aihub binaries
// and the fake harness (test/fakepi is both `pi` and `claude`; no real
// model is called, HOME is a scratch dir): one step's chain
// [claude:claude-opus-5-5:high, pi:example-gateway/model-a:low] — the claude
// candidate fails model-class and the step moves to the pi candidate —
// hands claude `--model claude-opus-5-5 --effort high` and pi
// `--model example-gateway/model-a --thinking low`; steps without an effort
// get no effort flag at all (the pre-#176 argv).

import (
	"strings"
	"testing"
)

func TestW176MixedEffortChain(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [claude:claude-opus-5-5:high, pi:example-gateway/model-a:low] }\n")
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "w176 mixed effort chain", nil)
	w159Plan(t, inst, wi, map[string]any{
		"bad_models": []string{"claude-opus-5-5"},
		"actions": []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true}, // the claude dispatch: 404 first
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}})
	capDir := w079Run(t, d, inst, ws, wi)

	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 1 || cl[0]["model"] != "claude-opus-5-5" {
		t.Fatalf("claude spawns %v, want exactly the fix step's head candidate", cl)
	}
	if argv := w159Argv(cl[0]); !strings.Contains(argv, "--model claude-opus-5-5 --effort high") || strings.Contains(argv, "--thinking") || strings.Contains(argv, "claude-opus-5-5:high") {
		t.Fatalf("claude argv %q, want --model claude-opus-5-5 --effort high", argv)
	}
	stepOf := map[float64]string{}
	for _, e := range w159SpawnJournal(t, capDir) {
		n, _ := e["spawn_n"].(float64)
		stepOf[n], _ = e["step_id"].(string)
	}
	var piFix map[string]any
	for _, e := range w159Spawns(t, capDir, "fakepi.jsonl") {
		argv := w159Argv(e)
		n, _ := e["spawn_n"].(float64)
		if stepOf[n] == "fix" {
			piFix = e
			if e["model"] != "example-gateway/model-a" || !strings.Contains(argv, "--model example-gateway/model-a --thinking low") || strings.Contains(argv, "--effort") {
				t.Fatalf("pi fix argv %q, want --model example-gateway/model-a --thinking low", argv)
			}
			continue
		}
		// the other steps' candidates carry no effort: no flag
		if strings.Contains(argv, "--thinking") || strings.Contains(argv, "--effort") {
			t.Fatalf("step %s: an effort-less candidate passed an effort flag: %q", stepOf[n], argv)
		}
	}
	if piFix == nil {
		t.Fatalf("the pi candidate never ran: %v", w159Spawns(t, capDir, "fakepi.jsonl"))
	}
	hs, _ := w175StepDispatches(t, capDir, "fix")
	if strings.Join(hs, ",") != "claude,pi" {
		t.Fatalf("fix dispatches %v, want claude then pi", hs)
	}
	// the step record names the candidate the step last ran on, effort
	// apart from the model id
	x := readExecution(t, capDir)
	if st := x.Steps["fix"]; st == nil || st.model() != "example-gateway/model-a" || st.effort() != "low" {
		t.Fatalf("fix step record %+v, want model example-gateway/model-a effort low", st)
	}
	if st := x.Steps["grill"]; st == nil || st.effort() != "" {
		t.Fatalf("grill step record %+v, want no effort", st)
	}
}
