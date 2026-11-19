package cli

// step_tiers.go: the Host-only [step_tiers] model-candidate catalog
// (polyforge#143, 85b) — the one machine-level list of the model
// candidates each step tier recommends, defined once where daemon,
// doctor and Pi Web all read it: the Host file. One table, one row per
// tier, the value a list of `<harness>:<model>[:<effort>]` candidates
// (stored per harness — a model's id differs between harnesses, so
// \"apply to every harness\" is Pi Web's to expand, 85b2). The tier
// names are not a fixed set: a name needs only the lane tier grammar,
// the conventional `default` tier carries the mainline models, and a
// catalog tier a lane does not declare is information, never a defect
// (Q1 A).

import (
	"fmt"
	"regexp"
)

// StepTiersSection is the catalog's section name: `[step_tiers]`, its
// keys `step_tiers.<tier>` (the interface's 步骤能力档 wording, Q2 A).
const StepTiersSection = "step_tiers"

// StepTiersLabel is the catalog's name in reload outcomes (the whole
// table is one family, like "[memory_policy]").
const StepTiersLabel = "[step_tiers]"

// StepTiersClass is the catalog's reload class: live — the catalog has no
// daemon consumer (Q5 B), so a reload only reports it applied; the
// readers (`pf doctor`, `pf config list`) take the new value with no
// restart.
const StepTiersClass = ConfigLive

// stepTierName is the grammar of a catalog tier name — the same one a
// lane's defaults.tiers tier names follow, so a catalog tier and a lane
// tier of the same name are the same tier.
var stepTierName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// parseStepTiersTable validates the [step_tiers] table (nil = absent; a
// table with no rows = an empty catalog, which doctor's catalog row
// skips): every row a tier name of the lane tier grammar with a
// non-empty array of unique `<harness>:<model>[:<effort>]` candidates,
// answered in canonical form. Every refusal names the file, the line,
// the table and the key.
func parseStepTiersTable(file string, t *tableBlock) (map[string][]string, error) {
	if t == nil {
		return nil, nil
	}
	const table = "[" + StepTiersSection + "]"
	out := map[string][]string{}
	seen := map[string]bool{}
	for _, kv := range t.rows {
		refuse := func(reason string) error {
			return &TableKeyError{File: file, Line: kv.line, Table: table, Key: kv.key, Reason: reason}
		}
		if !stepTierName.MatchString(kv.key) {
			return nil, refuse(fmt.Sprintf("%q is not a tier name (^[a-z][a-z0-9-]{0,39}$, the lane tier grammar)", kv.key))
		}
		if seen[kv.key] {
			return nil, refuse("duplicate key")
		}
		seen[kv.key] = true
		items, err := parseQuotedArray(kv.val, kv.line)
		if err != nil {
			return nil, refuse("must be a non-empty array of quoted `<harness>:<model>` candidates: " + err.Error())
		}
		if len(items) == 0 {
			return nil, refuse("is empty — name at least one candidate, or unset the tier")
		}
		// the same candidate bar a [summarize] models list takes
		list, reason := normalizeSummarizeModels(items)
		if reason != "" {
			return nil, refuse(reason)
		}
		out[kv.key] = list
	}
	return out, nil
}
