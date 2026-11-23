package cli

// step_tiers.go: the Host-only [step_tiers] model-candidate catalog
// (polyforge#143, 85b) — the one machine-level list of the model
// candidates each step tier recommends, defined once where daemon,
// doctor and Pi Web all read it: the Host file. One table, one row per
// tier, the value a list of `<harness>:<model>[:<effort>]` candidates
// (stored per harness — a model's id differs between harnesses, so
// "apply to every harness" is Pi Web's to expand, 85b2). The tier
// names are not a fixed set: a name needs only the lane tier grammar,
// the conventional `default` tier carries the mainline models, and a
// catalog tier a lane does not declare is information, never a defect
// (Q1 A).

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
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

// stepTierCatalogCovers reports whether candidate is in the catalog,
// by Q3's strength rule: a catalog entry without an effort covers the
// candidate's model at any effort; an entry with an effort requires the
// candidate's to be the same. The catalog spans every tier (the
// candidate's own tier is a lane concern, not a catalog one).
func stepTierCatalogCovers(catalog map[string][]string, candidate string) bool {
	c, err := harness.ParseCandidate(candidate)
	if err != nil {
		return true // a malformed candidate is the load-time validators' refusal, never this row's
	}
	for _, list := range catalog {
		for _, entry := range list {
			e, err := harness.ParseCandidate(entry)
			if err != nil {
				continue
			}
			if e.Harness == c.Harness && e.Model == c.Model && (e.Effort == "" || e.Effort == c.Effort) {
				return true
			}
		}
	}
	return false
}

// stepTiersCatalogCheck is doctor's `step tiers catalog` row
// (polyforge#143, 85b): every candidate the user wrote — the workspace
// yaml's steps.<project>.<step>.models and tiers.<project>.<tier>.models
// lists, and the [summarize] table's models (Q3's scope; a resolved
// chain's lane defaults are not the user's writing) — is named in the
// Host's catalog. A candidate outside it warns, never fails: the catalog
// is a recommendation, not an admission rule. An empty catalog skips the
// row; a placeholder candidate does not count (the workspace yaml row's
// finding); a catalog tier no installed lane declares is information
// only (Q1 A), judged when the daemon reports its workflows dir.
func stepTiersCatalogCheck(r *doctorRun) (string, string) {
	catalog := r.cfg.StepTiers
	if len(catalog) == 0 {
		return "skip", "not run: the [step_tiers] catalog is empty — fill it with `pf config set step_tiers.<tier> '[\"pi:…\"]' --host` (it is a recommendation; this row only checks candidates against it)"
	}
	checked := 0
	var warns []string
	note := func(where, candidate string) {
		if workspace.IsPlaceholder(candidate) {
			return // a shipped placeholder is the workspace yaml row's finding, never counted here
		}
		checked++
		if !stepTierCatalogCovers(catalog, candidate) {
			warns = append(warns, fmt.Sprintf("%s → %s", where, candidate))
		}
	}
	for _, ws := range r.cfg.Mapping.Workspaces() {
		cfg, err := workspace.Load(ws, r.cfg.Mapping.ProjectsOf(ws))
		if err != nil || cfg == nil {
			continue // the workspace yaml row fails on it already
		}
		for project, keys := range cfg.Models {
			for step, list := range keys {
				for _, cand := range list {
					note(fmt.Sprintf("steps.%s.%s", project, step), cand)
				}
			}
		}
		for project, tiers := range cfg.Tiers {
			for tier, list := range tiers {
				for _, cand := range list {
					note(fmt.Sprintf("tiers.%s.%s", project, tier), cand)
				}
			}
		}
	}
	if r.cfg.Summarize != nil {
		for _, cand := range r.cfg.Summarize.Models {
			note("summarize.models", cand)
		}
	}
	// a catalog tier no installed lane declares is information only
	info := ""
	if r.status.WorkflowsDir != "" {
		var laneTiers []string
		for _, ws := range r.cfg.Mapping.Workspaces() {
			set := workflow.ForWorkspace(r.status.WorkflowsDir, ws)
			bundles, _, _ := workspace.Bundles(set, "")
			for _, lane := range bundles {
				if lane.Defaults == nil {
					continue
				}
				for tier := range lane.Defaults.Tiers {
					laneTiers = append(laneTiers, tier)
				}
			}
		}
		var extra []string
		for tier := range catalog {
			if !slices.Contains(laneTiers, tier) {
				extra = append(extra, tier)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			info = fmt.Sprintf("; catalog tier(s) no installed lane declares (information only, never a warn): %s", strings.Join(extra, ", "))
		}
	}
	if len(warns) > 0 {
		sort.Strings(warns)
		return "warn", fmt.Sprintf("%d of %d user-written candidate(s) are not in the [step_tiers] catalog: %s — a warn, never a fail (the catalog is a recommendation, not an admission rule; `pf override` does not read it): add each with `pf config set step_tiers.<tier> '[\"…\"]' --host` or drop the yaml entry%s",
			len(warns), checked, strings.Join(capRows(warns), ", "), info)
	}
	return "pass", fmt.Sprintf("all %d user-written candidate(s) are in the [step_tiers] catalog's %d tier(s)%s", checked, len(catalog), info)
}
