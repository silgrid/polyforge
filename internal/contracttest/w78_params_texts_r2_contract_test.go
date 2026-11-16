// carries: skills/polyforge/verbs/doctor.md, docs/reference/commands.md, docs/reference/workflow-contract.md, internal/contracttest/w78_params_texts_r2_contract_test.go
package test

// w78_params_texts_r2_contract_test.go (polyforge#78, review round 1
// findings r1#1, r1#2 and r1#4): the step-params texts the first round
// left out. The doctor skill teaches the `workspace yaml` row's warn on a
// project param value no lane declares for its step (named apart from a
// stale step key) and the step entry's `params` key; the command
// reference's `pf config` paragraph lists the workspace read's `params`
// and the `steps.<project>.<step-id>.params.<name>` write key with its
// refusal and class; the workflow contract keeps the script environment
// row `PF_PARAM_<NAME>` and no longer calls placeholders static text
// substitution at load. The acceptance (AC5) is these texts themselves.
//
// Red on the round-1 tree: doctor.md names no param value warn — its
// first must-contain marker fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW78ParamsDocsRound2$' -count=1

import (
	"strings"
	"testing"
)

func TestW78ParamsDocsRound2(t *testing.T) {
	t.Run("doctor skill", func(t *testing.T) {
		const rel = "skills/polyforge/verbs/doctor.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text,
			// the row's warn, worded apart from a stale step key
			"`warn` naming each project param value no lane of the workspace's set declares for its step",
			"`steps.<project>.<step-id>.params.<name>`",
			"declare the param in the lane's step entry (`params`) or delete the value",
			// the step entry's params key and its value rule
			"a step's entry may also carry `params: { <name>: <value> }`",
			"one line of at most 256 bytes",
		)
	})

	t.Run("command reference", func(t *testing.T) {
		const rel = "docs/reference/commands.md"
		text := w68Flat(t, rel)
		cfg := w78Between(text, "`pf config workspace --cwd <dir>` reads", "`pf config migrate`")
		if cfg == "" {
			t.Fatalf("%s lost its `pf config workspace --cwd <dir>` paragraph", rel)
		}
		w78Has(t, rel, cfg,
			"its `params` when the file sets any",
			"`steps.<project>.<step-id>.params.<name>`",
			"refused when no lane of the workspace's lane set declares the param on the step",
			"class `live`",
		)
	})

	t.Run("workflow contract", func(t *testing.T) {
		const rel = "docs/reference/workflow-contract.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text, "| `PF_PARAM_<NAME>` |")
		if old := "static text substitution at load"; strings.Contains(text, old) {
			t.Errorf("%s still calls placeholders %q: a param takes its value at dispatch time", rel, old)
		}
	})
}
