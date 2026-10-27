package test

// w21_treehash_fastpath_acceptance_test.go: polyforge workbench#21 — the
// driver's re-dispatch fast path (tree-hash resume), authored against
// the spec BEFORE the implementation lands. Every bar below is RED on
// the pre-implementation tree — an assertion-level red (the
// previous-attempt reference still orders a from-scratch start, and no
// failed attempt carries a tree_hash), never a build red: this file
// compiles, vets and gofmts against the tree as it stands today, and
// every surface it drives is the project's real e2e one (the real
// pf/aihub/fakepi binaries, a real daemon, the workspace
// .polyforge.yaml, the capsule execution.json / spawn journal and the
// fake agent's dispatched-prompt ledger). No implementation internal is
// read.
//
// Scenario map (artifacts/e2e-scenarios.md carries the GIVEN/WHEN/THEN
// forms, the criteria each scenario pins, and the baseline evidence):
//
//   - TestW21SecondFailureUnchangedTreeContinuesInPlace: the 2nd+
//     failure trigger, the fast-path prompt (keep the transcript path +
//     continue in place, no start-from-scratch wording) and the live
//     step_failed archive tree_hash.
//   - TestW21CandidateSwitchUnchangedTreeContinuesInPlace: the
//     candidate-switch trigger on an unchanged tree takes the fast path.
//   - TestW21DaemonRestartRecoveryArchivesTreeHash: the daemon-restart
//     recovery archive records the tree hash exactly as a live failure
//     archive does.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w21FastPathTokens are the semantic tokens the spec fixes for the
// continue-in-place instruction (requirement 4 sc. 1): the reference
// keeps its heading, states the tree is byte-identical to the moment of
// death, orders the new conversation to continue the previous attempt,
// tells it to verify the resume point from the transcript tail and not
// to redo completed environment setup. A revision may reword around
// them, never drop them.
var w21FastPathTokens = []string{
	"Previous attempt (read-only reference)",
	"byte-identical",
	"continue the previous attempt",
	"transcript tail",
	"environment setup",
}

// w21ColdStartSentence is today's from-scratch instruction; the fast path
// MUST NOT carry it (requirement 4 sc. 1).
const w21ColdStartSentence = "start this attempt's work from scratch"

// w21Prompt reads one pi dispatch's full prompt from the fake agent's
// ledger (logs/fakepi-prompt-<spawn>.txt).
func w21Prompt(t *testing.T, capDir string, spawnN int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(spawnN)+".txt"))
	if err != nil {
		t.Fatalf("the fake pi never persisted the prompt of spawn %d: %v", spawnN, err)
	}
	return string(raw)
}

// w21AssertContinueInPlace drives the THEN clause of the fast-path
// scenarios: every required token present, the from-scratch sentence
// absent, and a real transcript path named in the reference.
func w21AssertContinueInPlace(t *testing.T, prompt string) {
	t.Helper()
	for _, tok := range w21FastPathTokens {
		if !strings.Contains(prompt, tok) {
			t.Errorf("the dispatched prompt misses the fast-path token %q (the tree hash matched, so this must continue in place)", tok)
		}
	}
	if strings.Contains(prompt, w21ColdStartSentence) {
		t.Errorf("the fast-path prompt still carries the start-from-scratch wording %q", w21ColdStartSentence)
	}
	if !strings.Contains(prompt, ".jsonl") {
		t.Errorf("the fast-path reference names no transcript path (a .jsonl capsule path)")
	}
}

// w21Test2InfraAttemptHashes answers the tree_hash of every archived
// infra_failure attempt on one step (the live failure ledger).
func w21Test2InfraAttemptHashes(t *testing.T, capDir, step string) []string {
	t.Helper()
	x := readExecution(t, capDir)
	st := x.Steps[step]
	if st == nil {
		t.Fatalf("step %s missing from execution.json", step)
	}
	var out []string
	for _, a := range st.Attempts {
		if a["cause"] != "infra_failure" {
			continue
		}
		h, _ := a["tree_hash"].(string)
		out = append(out, h)
	}
	return out
}

// TestW21SecondFailureUnchangedTreeContinuesInPlace: GIVEN the fix step
// dies twice in a row with the capsule tree untouched, WHEN the driver
// re-dispatches it as a new conversation (the 2nd+ failure fresh
// threshold), THEN the previous-attempt reference keeps the transcript
// path but orders the new conversation to continue in place, AND both
// live failures archived the capsule tree hash.
func TestW21SecondFailureUnchangedTreeContinuesInPlace(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w21-a] }\n")
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "w21: second failure on an unchanged tree", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": w079ProviderErr},
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": w079ProviderErr},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	capDir := w079Run(t, d, inst, ws, wi)

	// requirement 1 sc. 1: every live step_failed archive carries the hash
	hashes := w21Test2InfraAttemptHashes(t, capDir, "fix")
	if len(hashes) != 2 {
		t.Fatalf("fix archived %d infra_failure attempts, want 2: %v", len(hashes), hashes)
	}
	for i, h := range hashes {
		if h == "" {
			t.Errorf("live failure attempt #%d records no tree_hash (the fast-path guard is absent)", i+1)
		}
	}
	// requirement 3 sc. 1 + requirement 4 sc. 1: the fresh retry (spawn 4)
	// continues in place on the matching hash.
	w21AssertContinueInPlace(t, w21Prompt(t, capDir, 4))
}

// TestW21CandidateSwitchUnchangedTreeContinuesInPlace: GIVEN a step whose
// first candidate dies model-class and the driver switches to the next
// candidate in a new conversation without touching the tree, WHEN that
// fresh dispatch is assembled, THEN it takes the fast path.
func TestW21CandidateSwitchUnchangedTreeContinuesInPlace(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w21-a, pi:w21-b] }\n")
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "w21: candidate switch on an unchanged tree", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": w079ProviderErr},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	capDir := w079Run(t, d, inst, ws, wi)

	if got := w079Model(t, capDir, "fix"); got != "w21-b" {
		t.Fatalf("the switch never reached the second candidate (fix model %q)", got)
	}
	// the cross-candidate dispatch (spawn 3) is a new conversation with the
	// previous transcript as reference; it must take the fast path.
	w21AssertContinueInPlace(t, w21Prompt(t, capDir, 3))
}

// TestW21DaemonRestartRecoveryArchivesTreeHash: GIVEN a step in flight
// when the daemon is SIGKILLed, WHEN the restarted daemon archives the
// interrupted dispatch as a recovery attempt, THEN that attempt carries
// the capsule tree hash exactly as a live failure archive does.
func TestW21DaemonRestartRecoveryArchivesTreeHash(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, "steps:\n  e2e:\n    fix: { models: [pi:w21-a] }\n")
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t)
	wi := w113NewWI(t, inst, "e2e", "w21: daemon restart records the tree hash", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "park": true}, // alive while the daemon dies hard
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)

	// the fix dispatch is in flight when the daemon is SIGKILLed
	w33WaitModels(t, capDir, 2, 60*time.Second)
	d.crash()
	d = rig.daemon(t)

	w113WaitCompleted(t, capDir, 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	x := readExecution(t, capDir)
	var found bool
	for _, a := range x.Steps["fix"].Attempts {
		if a["failure_cause"] != "daemon_restart" {
			continue
		}
		found = true
		if h, _ := a["tree_hash"].(string); h == "" {
			t.Errorf("the daemon_restart recovery attempt records no tree_hash: %v", a)
		}
	}
	if !found {
		t.Fatalf("the recovery archived no daemon_restart attempt for fix: %v", x.Steps["fix"].Attempts)
	}
}
