package workspace

// compose.go: the model-candidate precedence chain as a PURE function
// over the bundle's workflow.yaml, the parsed workspace yaml and the claimed
// project — no I/O, no state, fully table-testable. The runtime computes
// it at EVERY dispatch (polyforge#33: the driver's compose closure re-reads
// the yaml and bundle each time), so an edit governs the next dispatch;
// nothing is snapshotted at claim or persisted in the daemon's record.
//
// The chain, outermost first (six levels):
//
//	manual `pf submit --models` > yaml steps.<project>.<step-id>.models >
//	yaml tiers.<project>.<tier>.models (a step naming model_tier <tier>) >
//	bundle step-level model_candidates, or the bundle's defaults.tiers list
//	of the step's model_tier > yaml steps.<project>.default.models >
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
// bundle that has one (no key is a sub-definition's own —
// the daemon actions that dispatched them left with workflow contract v2
// change 16). Resolution is non-merge: the first
// PRESENT level wholly replaces the others. Presence is "declared": a
// declared-but-empty list is a PRESENT level that governs — it yields no
// switchable chain (the harness adapter's default model stays in
// effect) and NEVER a silent fall-through. A project the yaml does not
// list leaves the bundle alone.

import (
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// TaskModels answers the one-shot task lane's effective candidate list
// of project: the bundle-less `steps.<project>.task.models`
// list when declared, else `steps.<project>.default.models`. It is PURE
// over the parsed workspace yaml and is read live by the daemon at every
// spawn (the runtime accessor, runtime.TaskModels — no snapshot).
//
// list is non-nil exactly when a level governs (a declared-but-empty
// list answers empty, non-nil — present-empty governs and dispatches
// nothing); from names the level it came from ("" when neither is
// declared). The named refusal for the neither case is
// MissingTaskModelsError.
func TaskModels(cfg *Config, project string) (list []string, from string) {
	if cfg == nil {
		return nil, ""
	}
	pm := cfg.Models[project]
	if l, ok := pm[TaskKey]; ok {
		return l, fmt.Sprintf("steps.%s.%s.models", project, TaskKey)
	}
	if l, ok := pm[DefaultKey]; ok {
		return l, fmt.Sprintf("steps.%s.%s.models", project, DefaultKey)
	}
	return nil, ""
}

// MissingTaskModelsError names both configuration keys a task spawn of
// project needs one of: the task list is absent and so is the project
// default. There is no hardcoded model — the refusal is the whole
// answer.
func MissingTaskModelsError(project string) error {
	return fmt.Errorf("task model candidates for project %q are not configured: set steps.%s.%s.models or fall back to steps.%s.%s.models (there is no default model)",
		project, project, TaskKey, project, DefaultKey)
}

// DefaultSummarizeLanguage is the summarize lane's narration language
// when cli.toml's [summarize] table declares no `language` (polyforge#243).
const DefaultSummarizeLanguage = "中文"

// The summarize lane's per-WI call-start spacing bounds and default, in
// seconds (polyforge#250): a declared [summarize] interval_sec must lie in
// MinSummarizeIntervalSec..MaxSummarizeIntervalSec inclusive;
// DefaultSummarizeIntervalSec governs when the key is absent and is the
// reading of a job record written without one.
const (
	MinSummarizeIntervalSec     = 10
	MaxSummarizeIntervalSec     = 3600
	DefaultSummarizeIntervalSec = 60
)

// SummarizeSettings is the machine-level summarize lane configuration —
// cli.toml's [summarize] table: Models is mandatory in a
// declared table (canonical `<harness>:<model>` candidates); an empty
// Language or a zero IntervalSec is an absent optional key (Lane resolves
// the documented defaults).
type SummarizeSettings struct {
	Models      []string
	Language    string
	IntervalSec int
}

// Lane answers the lane's effective configuration: nil models when the
// table is absent (the lane is off), else the declared models and the
// language/interval with the documented defaults for an absent key.
func (s *SummarizeSettings) Lane() (models []string, language string, intervalSec int) {
	if s == nil {
		return nil, DefaultSummarizeLanguage, DefaultSummarizeIntervalSec
	}
	language, intervalSec = s.Language, s.IntervalSec
	if language == "" {
		language = DefaultSummarizeLanguage
	}
	if intervalSec == 0 {
		intervalSec = DefaultSummarizeIntervalSec
	}
	return append([]string{}, s.Models...), language, intervalSec
}

// ComposeCandidates answers the per-step effective candidate lists of
// one claim of project, keyed by step id. An entry exists for every step
// whose composed list is non-nil (including present-empty lists); a step
// absent from the map (or a nil map) means no list applies anywhere. fm
// may be nil (no bundle workflow.yaml — no steps to key on); cfg may be
// nil (no yaml — the bundle alone).
// manual is the work item's `pf submit --models` list (nil/empty = none).
func ComposeCandidates(fm *workflow.Lane, cfg *Config, project string, manual []string) map[string][]string {
	if fm == nil || len(fm.Steps) == 0 {
		return nil
	}
	pm, pt := projectLists(cfg, project)
	out := make(map[string][]string, len(fm.Steps))
	for _, s := range fm.Steps {
		if len(manual) > 0 {
			out[s.ID] = append([]string(nil), manual...)
		} else if list := composeStep(fm, pm, pt, s); list != nil {
			out[s.ID] = list
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// projectLists answers the claimed project's yaml lists: its step entries
// (steps.<project>) and its tier lists (tiers.<project>); nil when the
// yaml declares none.
func projectLists(cfg *Config, project string) (pm, pt map[string][]string) {
	if cfg == nil {
		return nil, nil
	}
	return cfg.Models[project], cfg.Tiers[project]
}

// composeStep resolves one step's effective list through the chain; pm
// is the claimed project's yaml step entries and pt its yaml tier lists
// (nil when it has none).
func composeStep(fm *workflow.Lane, pm, pt map[string][]string, s workflow.Step) []string {
	list, _ := composeStepFrom(fm, pm, pt, s, "", "")
	return list
}

// composeStepFrom is composeStep that also names the level the list came
// from ("" when none applies); project/bundle only label the source. A
// step naming a `model_tier` takes, below its own yaml key, the project's
// yaml list of that tier (tiers.<project>.<tier>.models: the workspace's
// tier→model table), then the lane's own (`defaults.tiers`); the yaml
// project default ranks below both. The bundle step level is the step's
// own `model_candidates`, or that lane tier list. A bundle list that is
// placeholders only (every candidate `<harness>:example-…`)
// is undeclared: a shipped placeholder never outranks the project default.
func composeStepFrom(fm *workflow.Lane, pm, pt map[string][]string, s workflow.Step, project, bundle string) ([]string, string) {
	if list, ok := pm[s.ID]; ok {
		return list, fmt.Sprintf("steps.%s.%s.models", project, s.ID)
	}
	if s.ModelTier != "" {
		if list, ok := pt[s.ModelTier]; ok {
			return list, fmt.Sprintf("%s.%s.%s.models", TiersKey, project, s.ModelTier)
		}
	}
	if list := fm.TierCandidates(&s); list != nil && !placeholdersOnly(list) {
		return list, fmt.Sprintf("bundle %s step %s model_tier %s (defaults.tiers)", bundle, s.ID, s.ModelTier)
	}
	if s.ModelCandidates != nil && !placeholdersOnly(s.ModelCandidates) {
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

// PlaceholderPrefix opens a placeholder model name: `example-` after the
// harness prefix (the shipped lanes' `pi:example-gateway/model-a`), a name
// no gateway serves.
const PlaceholderPrefix = "example-"

// IsPlaceholder reports whether a candidate names a placeholder model
// (`<harness>:example-…`).
func IsPlaceholder(candidate string) bool {
	c, err := harness.ParseCandidate(candidate)
	if err != nil {
		return false
	}
	return strings.HasPrefix(c.Model, PlaceholderPrefix)
}

// placeholdersOnly reports whether a non-empty list is placeholders only.
func placeholdersOnly(list []string) bool {
	if len(list) == 0 {
		return false
	}
	for _, c := range list {
		if !IsPlaceholder(c) {
			return false
		}
	}
	return true
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
func ResolveSteps(bundle string, fm *workflow.Lane, cfg *Config, project string, manual []string) []StepResolution {
	if fm == nil {
		return nil
	}
	pm, pt := projectLists(cfg, project)
	out := make([]StepResolution, 0, len(fm.Steps))
	for _, s := range fm.Steps {
		r := StepResolution{Project: project, Bundle: bundle, Step: s.ID}
		if len(manual) > 0 {
			r.Models, r.ModelsFrom = append([]string(nil), manual...), "pf submit --models"
		} else {
			r.Models, r.ModelsFrom = composeStepFrom(fm, pm, pt, s, project, bundle)
		}
		out = append(out, r)
	}
	return out
}

// Bundles answers the lanes of the lane set (name → loaded lane) — or
// only the named one when bundle is non-empty — each read by the one lane
// loader. A lane the loader refuses is skipped from the map and its
// *workflow.LoadError is answered in bad (keyed by its name; a user lane a
// built-in lane shadows, WFC-122, by its directory) for the doctor and
// the listing to report; its claim refuses it too. A name the set does
// not carry is in neither map.
func Bundles(set workflow.LaneSet, bundle string) (map[string]*workflow.Lane, map[string]error, error) {
	out := map[string]*workflow.Lane{}
	bad := map[string]error{}
	if bundle != "" {
		lane, _, err := set.Load(bundle)
		switch {
		case err != nil:
			bad[bundle] = err
		case lane != nil:
			out[bundle] = lane
		}
		return out, bad, nil
	}
	lanes, err := set.Lanes()
	if err != nil {
		return nil, nil, err
	}
	for _, sl := range lanes {
		key := sl.Name
		if sl.Shadowed {
			key = sl.Dir
		}
		if err := workflow.AsLoadError(sl.Dir, sl.Errors); err != nil {
			bad[key] = err
			continue
		}
		out[key] = sl.Lane
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
