package test

// w29_review_reentry_acceptance_test.go: — the review
// re-entry mode, end to end through the real daemon + driver and the
// fake harness.
//
// The shared re-entry material (`workflows/_shared/review_reentry.md`,
// which the driver renders as its own prompt section only on a review
// dispatch with a prior verdict-bearing round on record — the w80 prompt
// trim; the shared review body keeps a one-line pointer) carries the
// re-entry contract: on a re-entry the review still runs with a
// FRESH conversation (fresh is the base — the old reviewer's blind spots
// are never inherited), and additionally reads the latest prior round's
// structured findings list from its archive
// (`artifacts/steps/review/s<n>/result.json`). Two additive faces run:
// the checklist face verifies each prior finding's disposition on the
// current tree (a still-present prior blocking finding is a residual,
// referenced by its position in the prior round and reported without
// re-deriving it), and the fresh face is the existing full judgment,
// now including the fixes' new surface. With no archive the behaviour is
// the first round: exactly the full review.
//
// DETERMINISM BOUNDARY: the reviewer's residual-vs-new *judgment* is an
// LLM call and is not machine-observable here. This test pins the INPUTS
// the re-entry contract guarantees: (a) the first-round verdict is
// archived and readable as the structured findings list the checklist
// face consumes; (b) the second review dispatch's persisted prompt
// carries the re-entry material and names the archive path; (c) that
// second dispatch is fresh (`resume` false — conversation is never
// inherited); and (d) a one-round scenario has no prior review archive
// when its only review dispatches, and its prompt carries none of the
// re-entry material. The judgment itself is not
// machine-verified anywhere in the suite; it is observed on real work
// items during the contributing guide's discipline ③ observation window
// for workflow/prompt changes.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// w29ReentryPhrases are the stable, lane-neutral phrases the shared
// review body pins for the re-entry contract. Every one is absent from
// the base review text, so the assertions below fail before the edit.
var w29ReentryPhrases = []string{
	"`artifacts/steps/review/`",
	"checklist face",
	"fresh face",
	"prior round",
	"residual",
	"without re-deriving",
	"first round",
}

// w80FirstRoundAbsent are the w29ReentryPhrases only the re-entry
// section carries ("prior round", "residual" and "first round" also
// appear in the always-rendered review body's pointer and output duty).
var w80FirstRoundAbsent = []string{
	"`artifacts/steps/review/`",
	"checklist face",
	"fresh face",
	"without re-deriving",
	"## The round contract",
	"BLOCKING mis-marking",
}

// w29Prompt reads the fake pi's persisted prompt of one derivation
// ordinal (`logs/fakepi-prompt-<N>.txt`).
func w29Prompt(t *testing.T, capDir string, spawnN int) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(spawnN)+".txt"))
	if err != nil {
		t.Fatalf("fakepi prompt of spawn %d: %v", spawnN, err)
	}
	return string(raw)
}

// w29ReviewArchiveDirs answers the review step's archive directory names
// (`s<N>`) under `artifacts/steps/review/`, sorted.
func w29ReviewArchiveDirs(t *testing.T, capDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(capDir, "artifacts", "steps", "review", "s*"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && fi.IsDir() {
			out = append(out, filepath.Base(m))
		}
	}
	return out
}

// w29FindingsOf reads one archived review result's output.findings list.
func w29FindingsOf(t *testing.T, env map[string]any) []any {
	t.Helper()
	out, ok := env["output"].(map[string]any)
	if !ok {
		t.Fatalf("archived review has no output object: %v", env)
	}
	findings, ok := out["findings"].([]any)
	if !ok {
		t.Fatalf("archived review output has no findings array: %v", out)
	}
	return findings
}

// TestW29ReviewReentryTwoRound drives the bug-fix lane through a review
// FAIL, its fix pass and verification, then a second review dispatch,
// and asserts the re-entry inputs: the first verdict is archived and
// readable, the second dispatch is fresh and carries the re-entry
// material.
func TestW29ReviewReentryTwoRound(t *testing.T) {
	plan := append(w222BugFixHead(),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "the new branch has no test",
			"findings": []map[string]any{
				{"severity": "blocking", "repair_target": "code", "detail": "the new branch has no test"},
				{"severity": "non-blocking", "detail": "nit: the helper name is terse"},
			},
		}},
		w67ChangedRound("fix", "demo-src", 1), // the fix pass adds the missing test
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w29: review re-entry reads the prior archived findings", plan)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	// One review FAIL rewind to fix, then the gate tail re-runs unchanged
	// (the tail is a fresh review, then verification).
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,review,fix,review,verification" {
		t.Errorf("dispatch order %s, want the two-round review tail", got)
	}

	// (a) The first-round verdict is archived and readable as the
	// structured findings list the checklist face consumes: the first
	// review is derivation 4 (repro 1, root-cause 2, fix 3, review 4).
	env := archivedEnvelope(t, capDir, "review", 4)
	first := w29FindingsOf(t, env)
	if len(first) != 2 {
		t.Fatalf("first-round archived findings %v, want the two scripted findings", first)
	}
	if sev, _ := first[0].(map[string]any)["severity"].(string); sev != "blocking" {
		t.Errorf("first-round finding #0 severity %q, want blocking: %v", sev, first[0])
	}
	if sev, _ := first[1].(map[string]any)["severity"].(string); sev != "non-blocking" {
		t.Errorf("first-round finding #1 severity %q, want non-blocking: %v", sev, first[1])
	}

	// (b) The second review dispatch's persisted prompt carries the
	// re-entry material and names the archive path. The second review is
	// derivation 6 (fix 5, review 6; verification 7 follows it).
	prompt := w29Prompt(t, capDir, 6)
	for _, want := range w29ReentryPhrases {
		if !strings.Contains(prompt, want) {
			t.Errorf("second review prompt (spawn 6) lost re-entry material %q", want)
		}
	}

	// (c) The second review dispatch is fresh: conversation is never
	// inherited across review rounds.
	rv := w222Steps(t, capDir, "review")
	if len(rv) != 2 {
		t.Fatalf("review dispatches %d, want 2: %v", len(rv), rv)
	}
	if rv[1]["resume"] != false {
		t.Errorf("the review re-entry must be fresh, got resume=%v (%v)", rv[1]["resume"], rv[1])
	}
}

// TestW29ReviewReentrySingleRound pins the first-round path: with no
// prior review archive the lane completes exactly as before, and only
// the single review dispatch's own archive is present.
func TestW29ReviewReentrySingleRound(t *testing.T) {
	plan := append(w222BugFixHead(),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w29: a single review round has no prior archive", plan)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	rv := w222Steps(t, capDir, "review")
	if len(rv) != 1 {
		t.Fatalf("review dispatches %d, want 1: %v", len(rv), rv)
	}
	// (d) No prior archive existed when the only review dispatched: the
	// sole archive is its own derivation (spawn 4 — review
	// runs right after fix).
	if dirs := w29ReviewArchiveDirs(t, capDir); len(dirs) != 1 || dirs[0] != "s4" {
		t.Errorf("review archives %v, want exactly [s4] — a first round has no prior round", dirs)
	}
	// (d, w80) The first round's persisted prompt carries none of the
	// re-entry material: it rides only a re-entry dispatch.
	prompt := w29Prompt(t, capDir, 4)
	for _, absent := range w80FirstRoundAbsent {
		if strings.Contains(prompt, absent) {
			t.Errorf("first-round review prompt (spawn 4) carries re-entry material %q", absent)
		}
	}
}
