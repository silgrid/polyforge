// carries: docs/reference/config.md, docs/bundles.md, docs/reference/uds-routes.md, docs/reference/commands.md, docs/reference/workflow-contract.md, skills/polyforge/verbs/override.md, skills/polyforge/SKILL.md, docs/design/polyforge-vnext/01-workflow-skills.md, internal/contracttest/w78_params_texts_contract_test.go
package test

// w78_params_texts_contract_test.go (polyforge#78): a text contract. A
// lane step may declare `params` — a name to a lane-default string — and
// the value a dispatch takes is composed at every dispatch over three
// layers: the lane default, the project's
// `steps.<project>.<step-id>.params.<name>` in the workspace yaml (only on
// a lane that declares the param on the step), and the work item's
// `pf override <id> --step <step-id> --param <name>=<value>`. The final
// value fills the definition's placeholder and reaches the step's scripts
// as `PF_PARAM_<NAME>`. Unlike `vars` — fixed text of the lane — a param
// is the one per-project, per-ticket value of a step.
//
// The acceptance (AC5) is the documentation and the routing skill
// themselves, so the shipped text is the behaviour pinned here: the
// config reference (the yaml key, the `pf config set` form, the workspace
// read's entry shape, the precedence and the params section), the
// bundle-authoring chapter (vars against params, the rendering rule), the
// UDS route table (the override action set), the command reference (the
// override summary and the workflows read face), the override skill
// fragment, the routing skill's rule 5 and the design doc's retirement of
// step attrs as a current mechanism. Every marker is lane-neutral: no
// lane, step or param name is required text.
//
// Red on the base tree: none of these texts names params yet — the first
// must-contain marker (the config reference's params section) fails.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -run '^TestW78ParamsDocsContract$' -count=1

import (
	"strings"
	"testing"
)

// w78Has asserts every marker is in the flattened text of rel.
func w78Has(t *testing.T, rel, text string, markers ...string) {
	t.Helper()
	for _, m := range markers {
		if !strings.Contains(text, m) {
			t.Errorf("%s must carry the step-params marker %q", rel, m)
		}
	}
}

// w78Lacks asserts no retired marker is in the flattened text of rel.
func w78Lacks(t *testing.T, rel, text string, retired ...string) {
	t.Helper()
	for _, m := range retired {
		if strings.Contains(text, m) {
			t.Errorf("%s still carries %q — step attrs were removed and are no current mechanism", rel, m)
		}
	}
}

// w78Between answers the part of text from the first start marker up to
// the first end marker after it (to the end when none follows); "" when
// the start marker is absent.
func w78Between(text, start, end string) string {
	i := strings.Index(text, start)
	if i < 0 {
		return ""
	}
	rest := text[i:]
	if j := strings.Index(rest[len(start):], end); j >= 0 {
		return rest[:len(start)+j]
	}
	return rest
}

func TestW78ParamsDocsContract(t *testing.T) {
	t.Run("config reference", func(t *testing.T) {
		const rel = "docs/reference/config.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text,
			// the params section of the yaml reference
			"### `steps.<project>.<step-id>.params` — a step's parameter values",
			// the step entry's key set
			"A step entry's keys are `models`, `skip`, `by` and `params`",
			// the three layers, highest first, as the yaml block states them
			"# Precedence (params): pf override --param > steps.<project>.<step-id>.params.<name> > the lane's default",
			// a project value governs only where the lane declares the param
			"only on a lane that declares the param on the step",
			// the write and its refusal
			"pf config set steps.<project>.<step-id>.params.<name> <value>",
			"pf config unset steps.<project>.<step-id>.params.<name>",
			"refused when no lane of the workspace's lane set declares the param on the step",
			// the workspace read's entry shape and the key's class
			"the entry also carries `params` (each declared name to its value)",
			"every `steps.<project>.<step-id>.params.<name>` key is `live`",
			// the value rule and the script face
			"one line of at most 256 bytes",
			"`PF_PARAM_<NAME>`",
			// the difference from vars
			"unlike a lane's `vars`",
		)
	})

	t.Run("workflow contract", func(t *testing.T) {
		const rel = "docs/reference/workflow-contract.md"
		text := w68Flat(t, rel)
		// the snapshot marker's hash is the claim's rendering; a dispatch
		// rewrites the rendered file at its own final values
		w78Has(t, rel, text,
			"the snapshot marker's content hash vouches for the claim's rendering only",
		)
	})

	t.Run("bundle chapter", func(t *testing.T) {
		const rel = "docs/bundles.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text,
			// the step key
			"- `params:` —",
			// vars against params
			"a var is fixed text of the lane",
			"a param's value is taken at every dispatch",
			"lane default < project < work item",
			// the rendering rule
			"a placeholder naming a param renders with the param's final value of the dispatch",
			"`PF_PARAM_<NAME>`",
		)
	})

	t.Run("uds routes", func(t *testing.T) {
		const rel = "docs/reference/uds-routes.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text,
			// the write's action set
			"`param` `{name, value}` set one step param",
			"`clear_param` clear one",
			"a param the lane snapshot does not declare on the step",
			// the view
			"`params`: the step's param overrides",
		)
	})

	t.Run("command reference", func(t *testing.T) {
		const rel = "docs/reference/commands.md"
		text := w68Flat(t, rel)
		override := w78Between(text, "`override` (a running ticket's per-step overlay", "), `")
		if override == "" {
			t.Fatalf("%s: the execution-plane summary's `override` entry is missing", rel)
		}
		w78Has(t, rel+" (the override summary)", override,
			"`--param <name>=<value>`",
			"`--clear-param <name>`",
		)
		// the workflows read face: each step param's final value and level
		w78Has(t, rel, text,
			"`params.<project>.<name>`",
		)
	})

	t.Run("override skill fragment", func(t *testing.T) {
		const rel = "skills/polyforge/verbs/override.md"
		text := w68Flat(t, rel)
		w78Has(t, rel, text,
			"pf override <id> --step <step-id> --param <name>=<value>",
			"pf override <id> --step <step-id> --clear-param <name>",
			"## Params",
			"the overlay > `steps.<project>.<step-id>.params.<name>` > the lane default",
			"the param overrides only, never the final value",
			"`PF_PARAM_<NAME>`",
			"one action per call",
		)
	})

	t.Run("routing skill rule 5", func(t *testing.T) {
		const rel = "skills/polyforge/SKILL.md"
		text := w68Flat(t, rel)
		rule := w78Between(text, " 5. **", " 6. **")
		if rule == "" {
			t.Fatalf("%s: session rule 5 is missing", rel)
		}
		w78Has(t, rel+" (rule 5)", rule,
			// the declaration
			"`params: { <name>: <default> }`",
			// the three layers
			"the lane's default < the project's `steps.<project>.<step-id>.params.<name>` < the work item's `pf override <id> --step <step-id> --param <name>=<value>`",
			"only on a lane that declares",
			// the dispatch-time value
			"composed at every dispatch",
			// the difference from vars
			"unlike `vars`",
		)
	})

	t.Run("design doc", func(t *testing.T) {
		const rel = "docs/design/polyforge-vnext/01-workflow-skills.md"
		text := w68Flat(t, rel)
		w78Lacks(t, rel, text,
			"| `attrs` | map[string]{type, default}",
			"**Step attribute sections**",
			"attr_<name>.md",
			"Config.Attrs",
			"workspace.ComposeAttrs",
		)
		w78Has(t, rel, text,
			"| `params` |",
			"step `attrs` were removed",
		)
	})
}
