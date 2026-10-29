package driver

// w115_prompt_contract_test.go: the verification step's mechanical/
// judgment split, pinned on the rendered prompts (whitespace-normalized,
// so a re-wrap never breaks a pin).
//
//  1. TestW115VerificationCallsVerifyRunOnce — every delivery lane's
//     verification prompt runs the mechanical half as ONE foreground
//     `pf verify-run` call with the tier the prompt names and the
//     release-shipped engine, never backgrounded, never polled, and states
//     the judgment duties left to the agent.
//  2. TestW115FeatureFullScenariosFromBindings — the authored lane takes a
//     bound scenario's outcome from the run and judges only unbound titles.
//  3. TestW115E2EAuthorDeclaresBindings — e2e-author's schema and text
//     carry the optional bindings and their rules.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW115VerificationCallsVerifyRunOnce(t *testing.T) {
	for _, lane := range []string{"feature-full", "bug-fix", "chore", "autopilot"} {
		prompt := spawnStepPrompt(t, lane, verificationStepID)
		flat := w109Flat(prompt)
		for _, want := range []string{
			"`pf verify-run --tier <full|scoped> --engine <data-dir>/release/current/tools/verify-scope --deadline <D>`",
			"## Run `pf verify-run` exactly once",
			"One blocking call: give it an explicit timeout as long as your shell tool allows, pass `--deadline` about 2 minutes under that timeout, and wait for it.",
			"Never background it, never poll for its completion, never split its work into calls of your own.",
			"a command that can no longer start is recorded `fail`",
			"`--tier` is what the `# Verification tier` section names",
			"never guess it",
			"Exit 0 means a result was produced — a red bar is data, read it.",
			"Exit 1 with `incomplete:` means no merged-tree proof could be made: return outcome `incomplete` with that reason verbatim.",
			"never run the bar by hand instead",
			"does NOT call it",
			// the judgment duties
			"Start `checks` with `result.json`'s `checks`, verbatim and in order",
			"carry its `pre_existing_failures`, `test_bar` and `command_evidence` verbatim",
			"Never change a status it recorded.",
			"**Unplanned repos**",
			"Run the lane checks below, each its own named check.",
			"declare `scope` only when `result.json`'s `tier.effective` is `scoped`",
			"`declared_paths` verbatim from `steps.review.output.scope`",
			"start from `result.json`'s `fail_reason`",
			"`report-mechanical.md` in whole",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s: the verification prompt lost %q", lane, want)
			}
		}
		if strings.Contains(strings.ToLower(prompt), "sleep") {
			t.Errorf("%s: the verification prompt names a sleep — waiting is never a call of its own", lane)
		}
		// the retired hand-run bar is gone
		for _, gone := range []string{
			"Identify the project's standard build/lint/test commands (inspect for a Makefile",
			"Run each command via `bash`, working inside the relevant `repos/<name>/` worktree(s).",
		} {
			if strings.Contains(flat, gone) {
				t.Errorf("%s: the verification prompt still runs the bar by hand: %q", lane, gone)
			}
		}
	}
}

func TestW115FeatureFullScenariosFromBindings(t *testing.T) {
	flat := w109Flat(spawnStepPrompt(t, "feature-full", verificationStepID))
	for _, want := range []string{
		"take its `bound_scenarios` entry's status and detail as they are",
		"A title without a binding (`unbound_titles`) is yours to judge.",
		"Copy `steps.e2e-author.output.scenario_titles` verbatim into `authored_titles`",
		"Record the suite as its own named check (`authored-scenarios`)",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the feature-full verification prompt lost %q", want)
		}
	}
}

func TestW115E2EAuthorDeclaresBindings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, "feature-full", "steps", "e2e-author.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "required: [title, repo, command, unit, marker]") {
		t.Error("e2e-author's schema must declare bindings items {title, repo, command, unit, marker}")
	}
	flat := w109Flat(text)
	for _, want := range []string{
		"## Bindings (how verification reads each scenario's result)",
		"a regular expression one output line matches only when THIS scenario passed",
		"bind each title to its own marker",
		"At the baseline run no marker may match",
		"A scenario without a binding is document-only",
		"The driver rejects a binding whose title is not in `scenario_titles`, whose fields are empty, or whose marker does not compile.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("e2e-author.md lost %q", want)
		}
	}
	if strings.Contains(strings.ToLower(text), "sleep") {
		t.Error("e2e-author.md names a sleep")
	}
}

// TestW115ScopedDispatchCallsVerifyRunToo: the scoped-tier material a
// SCOPED dispatch renders runs the scoped tier through the same one call —
// no hand-run engine, no "floor" of extra hand-run commands — and keys the
// scope declaration on the tier the run proved.
func TestW115ScopedDispatchCallsVerifyRunToo(t *testing.T) {
	for _, lane := range []string{"feature-full", "bug-fix", "chore"} {
		prompt := spawnStepPromptWith(t, lane, StepSpawn{StepID: verificationStepID, SpawnN: 1, ScopeTier: tierScoped})
		flat := w109Flat(prompt)
		for _, want := range []string{
			"Call `pf verify-run --tier scoped` (the step definition's one call; never run the bar or the engine by hand).",
			"Declare the optional `scope` object only when `result.json`'s `tier.effective` is `scoped`",
			"`fast_gates_ref` copied from `result.json`'s `scope.fast_gates_ref`",
			"the result MUST NOT be scoped — carry no `scope`",
			"`pf verify-run` ran NOTHING there, and its bar is yours to run, at full",
			"the run covers the whole diff again: `pf verify-run` always scopes `merge-base HEAD origin/<default>`, never only the fix increment",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("%s: the scoped verification prompt lost %q", lane, want)
			}
		}
		for _, gone := range []string{
			"Invoke the engine at",
			"a floor to include",
			"run the project's full build/lint/test commands as well",
			"that run proved the full tier for those repos",
			"you MAY verify scoped over the fix increment",
		} {
			if strings.Contains(flat, gone) {
				t.Errorf("%s: the scoped verification prompt still runs the engine or the bar by hand: %q", lane, gone)
			}
		}
		if strings.Contains(strings.ToLower(prompt), "sleep") {
			t.Errorf("%s: the scoped verification prompt names a sleep", lane)
		}
	}
}
