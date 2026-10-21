package workspace

// compose.go: the model-candidate precedence chain as a PURE function
// over the bundle frontmatter, the parsed workspace yaml and the claimed
// project — no I/O, no state, fully table-testable. The runtime computes
// it once per claim and snapshots the result into the daemon job record
// (runtime-state.json, runtime/state.go StepCandidates); recovery
// rehydrates that snapshot, never a second composition.
//
// The chain, outermost first (five levels):
//
//	manual `pf submit --models` > yaml steps.<project>.<step-id>.models >
//	bundle step-level model_candidates > yaml steps.<project>.default.models >
//	bundle defaults.model_candidates
//
// A step's own list outranks the project yaml `default` (polyforge#164,
// H10): the yaml default is the project's fallback for steps that make
// no choice of their own, so a bundle step that pins its model (e.g. an
// Opus-first root-cause, a flash-first verification) keeps it. A yaml
// step-id list still overrides the step — it is the project's explicit
// per-step decision.
//
// The manual list is the per-work-item level: when present it is the
// candidate chain of EVERY step of that work item.
//
// The key is the step id: a `fix` list governs the `fix` step of every
// bundle that has one. Resolution is non-merge: the first
// PRESENT level wholly replaces the others. Presence is "declared": a
// declared-but-empty list is a PRESENT level that governs — it yields no
// switchable chain (the harness adapter's default model stays in
// effect) and NEVER a silent fall-through. A project the yaml does not
// list leaves the bundle alone.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// SummarizeModels answers the summarize-lane candidate list of a claim
// of ANY project of the workspace — the root summarize.models key
// (polyforge#224, polyforge#242): the parsed list when the yaml declares
// one (a declared-empty list answers empty, non-nil — present-empty
// governs, and an empty candidate list dispatches nothing, so an empty
// list is the lane off), nil when the key or the whole yaml is absent
// (the lane is silently off — "off" is a legitimate state in the
// demand-gate era, never a typed failure). It is NOT part of the
// per-step precedence chain; the claim snapshots it into the job record
// like the step lists (an edit governs the next claim).
func SummarizeModels(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Summarize
}

// DefaultSummarizeLanguage is the summarize lane's narration language
// when the root summarize entry declares no `language` (polyforge#243),
// and the reading of a job record written without one.
const DefaultSummarizeLanguage = "中文"

// SummarizeLanguage answers the summarize lane's narration language of a
// claim: the declared root summarize.language (trimmed), or
// DefaultSummarizeLanguage when the key or the whole yaml is absent. The
// claim snapshots it beside SummarizeModels (an edit governs the next
// claim).
func SummarizeLanguage(cfg *Config) string {
	if cfg == nil || cfg.SummarizeLanguage == "" {
		return DefaultSummarizeLanguage
	}
	return cfg.SummarizeLanguage
}

// The summarize lane's per-WI call-start spacing bounds and default, in
// seconds (polyforge#250): a declared summarize.interval_sec must lie in
// MinSummarizeIntervalSec..MaxSummarizeIntervalSec inclusive;
// DefaultSummarizeIntervalSec governs when the key is absent and is the
// reading of a job record written without one.
const (
	MinSummarizeIntervalSec     = 10
	MaxSummarizeIntervalSec     = 3600
	DefaultSummarizeIntervalSec = 60
)

// SummarizeIntervalSec answers the summarize lane's per-WI call-start
// spacing of a claim, in seconds: the declared root
// summarize.interval_sec, or DefaultSummarizeIntervalSec when the key or
// the whole yaml is absent. The claim snapshots it beside
// SummarizeModels (an edit governs the next claim).
func SummarizeIntervalSec(cfg *Config) int {
	if cfg == nil || cfg.SummarizeIntervalSec == 0 {
		return DefaultSummarizeIntervalSec
	}
	return cfg.SummarizeIntervalSec
}

// ComposeCandidates answers the per-step effective candidate lists of
// one claim of project, keyed by step id. An entry exists for every step
// whose composed list is non-nil (including present-empty lists); a step
// absent from the map (or a nil map) means no list applies anywhere. fm
// may be nil (no bundle frontmatter — no steps to key on); cfg may be
// nil (no yaml — the bundle alone).
// manual is the work item's `pf submit --models` list (nil/empty = none).
func ComposeCandidates(fm *workflow.Frontmatter, cfg *Config, project string, manual []string) map[string][]string {
	if fm == nil || len(fm.Steps) == 0 {
		return nil
	}
	var pm map[string][]string
	if cfg != nil {
		pm = cfg.Models[project]
	}
	out := make(map[string][]string, len(fm.Steps))
	for _, s := range fm.Steps {
		if len(manual) > 0 {
			out[s.ID] = append([]string(nil), manual...)
		} else if list := composeStep(fm, pm, s); list != nil {
			out[s.ID] = list
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// composeStep resolves one step's effective list through the chain; pm
// is the claimed project's yaml entry (nil when it has none).
func composeStep(fm *workflow.Frontmatter, pm map[string][]string, s workflow.FrontmatterStep) []string {
	list, _ := composeStepFrom(fm, pm, s, "", "")
	return list
}

// composeStepFrom is composeStep that also names the level the list came
// from ("" when none applies); project/bundle only label the source.
func composeStepFrom(fm *workflow.Frontmatter, pm map[string][]string, s workflow.FrontmatterStep, project, bundle string) ([]string, string) {
	if list, ok := pm[s.ID]; ok {
		return list, fmt.Sprintf("steps.%s.%s.models", project, s.ID)
	}
	if s.ModelCandidates != nil {
		return s.ModelCandidates, fmt.Sprintf("bundle %s step %s model_candidates", bundle, s.ID)
	}
	if list, ok := pm[DefaultKey]; ok {
		return list, fmt.Sprintf("steps.%s.%s.models", project, DefaultKey)
	}
	if fm.Defaults != nil && fm.Defaults.ModelCandidates != nil {
		return fm.Defaults.ModelCandidates, fmt.Sprintf("bundle %s defaults.model_candidates", bundle)
	}
	return nil, ""
}

// StepResolution is one step's resolved candidate list with the level
// it came from (the doctor's resolution table). Each candidate carries
// its harness as its `<harness>:` prefix (polyforge#175).
type StepResolution struct {
	Project    string   `json:"project"`
	Bundle     string   `json:"bundle"`
	Step       string   `json:"step"`
	Models     []string `json:"models,omitempty"`
	ModelsFrom string   `json:"models_from,omitempty"`
}

// Harnesses answers the distinct harnesses of the resolved candidates,
// in candidate order. A step with no (or an empty) list runs on pi's
// default model, so it answers [pi]. Every list reaching here was
// validated at its load (yaml, bundle, --models), so an unparseable
// candidate is an internal inconsistency: it is answered as an error,
// never guessed onto a harness.
func (r StepResolution) Harnesses() ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for i, m := range r.Models {
		c, err := harness.ParseCandidate(m)
		if err != nil {
			return nil, fmt.Errorf("%s/%s/%s: candidate %d (%s): %w", r.Project, r.Bundle, r.Step, i, r.ModelsFrom, err)
		}
		if !seen[c.Harness] {
			seen[c.Harness] = true
			out = append(out, c.Harness)
		}
	}
	if len(out) == 0 {
		out = []string{harness.Pi}
	}
	return out, nil
}

// ResolveSteps answers the resolution of every step of one bundle for
// one project (manual = the work item's `pf submit --models` list).
func ResolveSteps(bundle string, fm *workflow.Frontmatter, cfg *Config, project string, manual []string) []StepResolution {
	if fm == nil {
		return nil
	}
	var pm map[string][]string
	if cfg != nil {
		pm = cfg.Models[project]
	}
	out := make([]StepResolution, 0, len(fm.Steps))
	for _, s := range fm.Steps {
		r := StepResolution{Project: project, Bundle: bundle, Step: s.ID}
		if len(manual) > 0 {
			r.Models, r.ModelsFrom = append([]string(nil), manual...), "pf submit --models"
		} else {
			r.Models, r.ModelsFrom = composeStepFrom(fm, pm, s, project, bundle)
		}
		out = append(out, r)
	}
	return out
}

// Bundles answers the installed bundles under workflowsDir (name →
// frontmatter, nil frontmatter skipped) — or only the named one when
// bundle is non-empty. A torn or refused frontmatter (an unprefixed
// candidate, a retired `harness` key — polyforge#175) is the bundle's
// own defect: it is skipped from the map and its error is answered in
// bad (name → load error) for the doctor to report; its claim surfaces
// it too.
func Bundles(workflowsDir, bundle string) (map[string]*workflow.Frontmatter, map[string]error, error) {
	out := map[string]*workflow.Frontmatter{}
	bad := map[string]error{}
	if workflowsDir == "" {
		return out, bad, nil
	}
	names := []string{bundle}
	if bundle == "" {
		ents, err := os.ReadDir(workflowsDir)
		if err != nil {
			return nil, nil, err
		}
		names = names[:0]
		for _, e := range ents {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	for _, n := range names {
		fm, err := workflow.ReadFrontmatter(filepath.Join(workflowsDir, n, "workflow.md"))
		if err != nil {
			bad[n] = err
			continue
		}
		if fm != nil {
			out[n] = fm
		}
	}
	return out, bad, nil
}

// NormalizeModels validates a manual `--models` list: non-empty, every
// entry a `<harness>:<model>` candidate (harness.ParseCandidate — the
// prefix is mandatory, polyforge#175), no duplicates. It returns the
// canonical list. There is no model registry to check ids against.
func NormalizeModels(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("models list is empty")
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, m := range in {
		if strings.TrimSpace(m) == "" {
			return nil, fmt.Errorf("models list has an empty candidate")
		}
		c, err := harness.Normalize(m)
		if err != nil {
			return nil, err
		}
		if seen[c] {
			return nil, fmt.Errorf("models list repeats %q", c)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}
