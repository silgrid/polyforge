// Package workspace implements the workspace-level per-project settings
// file — `.polyforge.yaml` at a workspace root — as ONE strict-schema
// loader shared by every consumer: the daemon's pre-claim read, pf doctor
// and the candidate-precedence compose. There is no second parser and no
// second strictness implementation.
//
// The grammar is closed:
//
//	steps:                               # optional section
//	  <project-name>:                    # one entry per project of this workspace
//	    default: { models: [...] }       # optional: every step of this project
//	    <step-id>:                       # optional: the steps with this id,
//	      models: [...]                  # in every bundle (fix, code, review, ...)
//	      skip: true|false               # optional: switch the step off
//	tiers:                               # optional section (workflow contract 2)
//	  <project-name>:                    # one entry per project of this workspace
//	    <tier>: { models: [...] }        # the list of a step's model_tier
//	                                     # (cheap, mid, ...) in every bundle
//
// The summarize lane and the memory policy are no part of this file: they
// are cli.toml's [summarize] table and [memory_policy."<path>".<entry>]
// family. A file still carrying a root `summarize:` or
// `memory_policy:` section is refused whole — the refusal names the line
// and `pf config migrate` (legacy.go); nothing reads either value here.
// The removed root `integrate:` section is refused whole with its rewrite
// hint, IntegrateRemovedHint: the daemon's automatic batch trigger is
// gone, a person files a batch.
//
// A step entry has two keys, at least one declared: `models`, a list of
// model candidates, each `<harness>:<model>` (polyforge#175 — harness ∈
// pi | claude, split on the first `:`; the prefix is mandatory and is the
// whole truth of where the candidate runs), and `skip`, a bool
// `skip: true` switches the step off for the project —
// only where its lane declares the step `skippable: true` (SkippedSteps;
// on any other step it governs nothing, and `pf config set` refuses to
// write it), fixed when a work item is claimed. A skip-only entry is no
// level of the model chain. `default` and `task` take no `skip`. Any key not shown here — at any level — is a load
// error, as are duplicate keys, type mismatches, an empty project entry,
// an unprefixed / unknown-prefix / empty-model candidate, and the retired
// `harness` key and the removed `attrs` key (each refused with a rewrite
// hint: RetiredHarnessHint, AttrsRemovedHint). A per-step key that is no step id of the installed
// workflows is stale, not refused: it governs
// nothing, `pf doctor` warns naming it (StaleKeys) and `pf config set`
// refuses to write one. The retired top-level `models:` section (the
// pre-#169 shape) refuses with a migration message; `projects:` /
// `workflow_defaults:` are unknown keys like any other.
//
// Presence semantics: an ABSENT file and an EMPTY or comments-only file
// both answer (nil, nil) — absent-equivalent, never an error. A file
// that declares anything must parse strictly.
//
// List presence: a declared-but-empty list is a PRESENT level that
// wholly governs (yielding no switchable chain rather than a
// fall-through). Absent keys fall through per the precedence chain
// (compose.go). The model chain, highest first: manual `pf submit
// --models` (per work item, every step) > steps.<project>.<step-id>.models
// > tiers.<project>.<tier>.models (a step naming that model_tier) > bundle
// step model_candidates or the bundle's tier list (defaults.tiers) >
// steps.<project>.default.models > bundle defaults.model_candidates.
//
// A tier entry has one key, `models` (the list's rules are a step
// entry's). A tier name no bundle step names governs nothing.
package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"gopkg.in/yaml.v3"
)

// FileName is the workspace-level defaults file every consumer reads.
const FileName = ".polyforge.yaml"

// DefaultKey is the per-project key whose list governs every step of
// the project that has neither a list of its own step id nor a bundle
// step-level model_candidates list.
const DefaultKey = "default"

// TaskKey is the recognized, bundle-less step id of the one-shot task
// agent. It belongs to no bundle; its only key is
// `models`, the task lane's candidate list. When absent the task lane
// falls back to DefaultKey; when neither is present the spawn refuses
// naming both keys. It is never part of a bundle's precedence chain.
const TaskKey = "task"

// BundlelessKey reports whether a per-step key belongs to no bundle step:
// the project default and the task lane.
func BundlelessKey(key string) bool {
	return key == DefaultKey || key == TaskKey
}

// SummarizeKey and MemoryPolicyKey are the retired root sections (moved
// to cli.toml): a file carrying either is refused whole.
const (
	SummarizeKey    = "summarize"
	MemoryPolicyKey = "memory_policy"
)

// Config is the parsed `.polyforge.yaml`.
type Config struct {
	// Models is project name → (DefaultKey or a step id → candidate
	// list): steps.<project>.<key>.models, flattened for the compose.
	// Every candidate is in canonical `<harness>:<model>` form.
	Models map[string]map[string][]string
	// Skip is project name → step id → bool: steps.<project>.<step>.skip.
	// A step is skipped only where its lane declares it
	// skippable (SkippedSteps). nil when no entry declares skip.
	Skip map[string]map[string]bool
	// Tiers is project name → tier name → candidate list:
	// tiers.<project>.<tier>.models (workflow contract 2), the list of
	// every bundle step whose model_tier names that tier — above the
	// bundle's own tier table, below the step's own yaml key. nil when the
	// section is absent.
	Tiers map[string]map[string][]string
}

// TiersKey is the optional top-level section of the per-project tier
// lists (workflow contract 2): tiers.<project>.<tier>.models.
const TiersKey = "tiers"

// IntegrateKey is the removed top-level section of the daemon's automatic
// batch trigger: a file still carrying it is refused whole with
// IntegrateRemovedHint.
const IntegrateKey = "integrate"

// IntegrateRemovedHint is the rewrite of a refused `integrate:` section.
const IntegrateRemovedHint = "removed: the daemon no longer files integration batches on its own — a person files a batch with `pf file create --bundle integrate --project <p>` and `pf submit`, so `auto` and `quiet` govern nothing; delete the whole `integrate:` section (docs/upgrade.md)"

// Error is the typed load failure: the file that refused and the
// reason. Every consumer surfaces it verbatim — the file name and the
// offending key/reason are the contract.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string { return e.Path + ": " + e.Reason }

// PathFor answers the yaml path of one workspace root.
func PathFor(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, FileName)
}

// Load reads and strictly validates `<workspaceRoot>/.polyforge.yaml`.
// An absent file answers (nil, nil); an empty or comments-only file
// answers (nil, nil) too (absent-equivalent). Anything else parses
// strictly or returns a typed *Error naming the file and the reason.
// A per-step key that is no step id of the workspace's lane set (nor
// `default` or `task`) is stale: it governs nothing and is not refused —
// StaleKeys names it, and `pf doctor` warns. Every
// project key must be one of projects — the projects cli.toml maps to
// this workspace (nil skips that check).
func Load(workspaceRoot string, projects []string) (*Config, error) {
	path := PathFor(workspaceRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &Error{Path: path, Reason: fmt.Sprintf("unreadable: %v", err)}
	}
	return LoadBytes(raw, path, projects)
}

// LoadBytes is Load over raw, the proposed bytes of the yaml at path
// (`pf config set` judges a write before it lands): the same strict parse.
func LoadBytes(raw []byte, path string, projects []string) (*Config, error) {
	cfg, reason := parse(raw, projects)
	if reason != "" {
		return nil, &Error{Path: path, Reason: reason}
	}
	return cfg, nil
}

// parse walks one yaml document node by node (so every refusal names its
// yaml path, never a Go type) and answers the flattened config or the
// refusal reason. (nil, "") is absent-equivalent.
func parse(raw []byte, projects []string) (*Config, string) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, "" // empty or comments-only
		}
		return nil, strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	var second yaml.Node
	if err := dec.Decode(&second); err == nil {
		return nil, "the file must hold exactly one YAML document (found a second `---` document)"
	} else if !errors.Is(err, io.EOF) {
		return nil, "content after the top-level document does not parse (no keys may follow a flow-style `{...}` top level or a `...` end marker): " + strings.ReplaceAll(err.Error(), "\n", "; ")
	}
	if len(doc.Content) == 0 {
		return nil, ""
	}
	root := deref(doc.Content[0])
	if isNull(root) {
		return nil, ""
	}
	if root.Kind != yaml.MappingNode {
		return nil, "the file must be a mapping with a `steps:` section"
	}
	top, reason := mapping(root, "")
	if reason != "" {
		return nil, reason
	}
	var stepsNode, tiersNode *yaml.Node
	cfg := &Config{}
	for _, kv := range top {
		switch kv.key {
		case "steps":
			stepsNode = kv.val
		case IntegrateKey:
			return nil, "`" + IntegrateKey + ":` " + IntegrateRemovedHint
		case TiersKey:
			tiersNode = kv.val
		case SummarizeKey, MemoryPolicyKey:
			secs, err := LegacySections(raw)
			if err != nil || len(secs) == 0 {
				return nil, fmt.Sprintf("`%s:` moved to cli.toml and is no longer read from the workspace yaml — %s", kv.key, MigrateHint)
			}
			return nil, LegacyReason(secs)
		default:
			return nil, fmt.Sprintf("unknown top-level key %q (only `steps` and `tiers`)", kv.key)
		}
	}
	var unmapped []string
	if tiersNode != nil && !isNull(tiersNode) {
		tiers, more, reason := tiersSection(tiersNode, projects)
		if reason != "" {
			return nil, reason
		}
		cfg.Tiers = tiers
		unmapped = more
	}
	if stepsNode == nil || isNull(stepsNode) {
		if len(unmapped) > 0 {
			return nil, unmappedReason(unmapped, projects)
		}
		if cfg.Tiers == nil {
			return nil, "" // a file declaring nothing governs nothing
		}
		return cfg, ""
	}
	if stepsNode.Kind != yaml.MappingNode {
		return nil, "steps: must be a mapping of project → step entries"
	}
	byProject, reason := mapping(stepsNode, "steps")
	if reason != "" {
		return nil, reason
	}
	cfg.Models = make(map[string]map[string][]string, len(byProject))
	for _, pkv := range byProject {
		project, pn := pkv.key, pkv.val
		if projects != nil && !contains(projects, project) && !contains(unmapped, project) {
			unmapped = append(unmapped, project)
		}
		where := "steps." + project
		if isNull(pn) || (pn.Kind == yaml.MappingNode && len(pn.Content) == 0) {
			return nil, fmt.Sprintf("%s: entry is empty — declare %s or a step id, or remove it", where, DefaultKey)
		}
		if pn.Kind != yaml.MappingNode {
			return nil, where + ": must be a mapping of step entries (`default` or a step id)"
		}
		entries, reason := mapping(pn, where)
		if reason != "" {
			return nil, reason
		}
		cfg.Models[project] = make(map[string][]string, len(entries))
		for _, ekv := range entries {
			e, reason := stepEntry(ekv.val, where+"."+ekv.key)
			if reason != "" {
				return nil, reason
			}
			if e.skip != nil && BundlelessKey(ekv.key) {
				return nil, fmt.Sprintf("%s.%s.skip: not allowed (`%s` is no step: skip switches off one step a lane declares skippable)", where, ekv.key, ekv.key)
			}
			if e.hasModels {
				cfg.Models[project][ekv.key] = e.models
			}
			if e.skip != nil {
				if cfg.Skip == nil {
					cfg.Skip = map[string]map[string]bool{}
				}
				if cfg.Skip[project] == nil {
					cfg.Skip[project] = map[string]bool{}
				}
				cfg.Skip[project][ekv.key] = *e.skip
			}
		}
	}
	if len(unmapped) > 0 {
		return nil, unmappedReason(unmapped, projects)
	}
	return cfg, ""
}

func unmappedReason(unmapped, projects []string) string {
	return fmt.Sprintf("project(s) %s are not mapped to this workspace in cli.toml (it maps: %s)", strings.Join(unmapped, ", "), strings.Join(projects, ", "))
}

// tiersSection reads the `tiers:` section (workflow contract 2): a mapping
// of project → tier name → `{ models: [...] }`, at least one tier per
// project, `models` the only key of a tier entry (a declared-but-empty
// list governs, like a step entry's). It answers the lists and the
// project keys not among projects (nil skips that check).
func tiersSection(n *yaml.Node, projects []string) (map[string]map[string][]string, []string, string) {
	if n.Kind != yaml.MappingNode {
		return nil, nil, TiersKey + ": must be a mapping of project → `{ <tier>: { models: [...] } }`"
	}
	byProject, reason := mapping(n, TiersKey)
	if reason != "" {
		return nil, nil, reason
	}
	out := make(map[string]map[string][]string, len(byProject))
	var unmapped []string
	for _, pkv := range byProject {
		project, pn := pkv.key, pkv.val
		where := TiersKey + "." + project
		if projects != nil && !contains(projects, project) {
			unmapped = append(unmapped, project)
		}
		if isNull(pn) || (pn.Kind == yaml.MappingNode && len(pn.Content) == 0) {
			return nil, nil, where + ": entry is empty — declare a tier (`cheap: { models: [...] }`), or remove it"
		}
		if pn.Kind != yaml.MappingNode {
			return nil, nil, where + ": must be a mapping of tier → `{ models: [...] }`"
		}
		tiers, reason := mapping(pn, where)
		if reason != "" {
			return nil, nil, reason
		}
		out[project] = make(map[string][]string, len(tiers))
		for _, tkv := range tiers {
			tw := where + "." + tkv.key
			if tkv.val.Kind != yaml.MappingNode {
				return nil, nil, tw + ": must be `{ models: [...] }`"
			}
			keys, reason := mapping(tkv.val, tw)
			if reason != "" {
				return nil, nil, reason
			}
			var mn *yaml.Node
			for _, kv := range keys {
				if kv.key != "models" {
					return nil, nil, fmt.Sprintf("%s.%s: not allowed (a tier entry's one key is `models`)", tw, kv.key)
				}
				mn = kv.val
			}
			list, reason := modelsList(mn, tw)
			if reason != "" {
				return nil, nil, reason
			}
			out[project][tkv.key] = list
		}
	}
	return out, unmapped, ""
}

// stepEntryVal is one decoded step entry: its models list when declared
// (hasModels; a declared empty list governs) and its skip when declared.
type stepEntryVal struct {
	models    []string
	hasModels bool
	skip      *bool
}

// stepEntry reads one `{ models: [...], skip: <bool> }` step entry at path
// where: `models` and `skip` are its keys, at least one declared. The
// retired `harness` key and the removed `attrs` key refuse with their
// rewrite hints.
func stepEntry(en *yaml.Node, where string) (stepEntryVal, string) {
	var e stepEntryVal
	if en.Kind != yaml.MappingNode {
		return e, where + ": must be `{ models: [...] }` or `{ skip: true }`"
	}
	keys, reason := mapping(en, where)
	if reason != "" {
		return e, reason
	}
	var mn *yaml.Node
	for _, kv := range keys {
		switch kv.key {
		case "models":
			mn = kv.val
		case "skip":
			var b bool
			if kv.val.Kind != yaml.ScalarNode || kv.val.Tag != "!!bool" || kv.val.Decode(&b) != nil {
				return e, where + ".skip: must be true or false"
			}
			e.skip = &b
		case "attrs":
			return e, where + ".attrs: " + AttrsRemovedHint
		case "harness":
			return e, where + ".harness: " + workflow.RetiredHarnessHint
		default:
			return e, fmt.Sprintf("%s.%s: not allowed (a step entry's one key is `models`, beside its `skip` switch)", where, kv.key)
		}
	}
	if mn == nil {
		if e.skip == nil {
			return e, where + ": entry is empty — declare `models` or `skip`, or remove it"
		}
		return e, ""
	}
	e.models, reason = modelsList(mn, where)
	e.hasModels = reason == ""
	return e, reason
}

// AttrsRemovedHint is the rewrite of a step entry's `attrs` key: step
// attributes are no part of workflow contract 4 (the lane's `attrs` key
// is refused by the loader alike).
const AttrsRemovedHint = "removed in workflow contract 4: step attrs are gone — delete the key (a step that should behave differently says so in its definition or a `vars` entry of its lane; docs/upgrade.md)"

// modelsList reads an entry's `models` value at path where. The items
// are unique `<harness>:<model>` candidate strings (harness.ParseCandidate
// — the same rules as a `pf submit --models` list), answered in
// canonical form; a declared empty list answers an empty, non-nil list.
// An absent value (mn nil) is the empty-entry refusal.
func modelsList(mn *yaml.Node, where string) ([]string, string) {
	if mn == nil {
		return nil, where + ": entry is empty — declare `models`, or remove it"
	}
	if mn.Kind != yaml.SequenceNode {
		return nil, where + ".models: must be a list of `<harness>:<model>` candidates"
	}
	list := make([]string, 0, len(mn.Content))
	seen := map[string]bool{}
	for i, item := range mn.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, fmt.Sprintf("%s.models[%d]: must be a plain `<harness>:<model>` string", where, i)
		}
		c, err := harness.Normalize(item.Value)
		if err != nil {
			return nil, fmt.Sprintf("%s.models[%d]: %v", where, i, err)
		}
		if seen[c] {
			return nil, fmt.Sprintf("%s.models[%d]: repeats %q", where, i, c)
		}
		seen[c] = true
		list = append(list, c)
	}
	return list, ""
}

// keyVal is one mapping entry, kept in file order (refusals are then
// deterministic: the first defect in the file wins).
type keyVal struct {
	key string
	val *yaml.Node
}

// MergeKeyReason is the refusal suffix for a YAML merge key (`<<`):
// merge semantics are not implemented; anchors on lists are.
const MergeKeyReason = "YAML merge keys (`<<`) are not supported — use anchors on lists (`models: &x [...]` / `{ models: *x }`)"

// mapping answers a mapping node's entries in file order (values with
// aliases followed), refusing a merge key, a null, non-scalar or
// duplicate key (where names the mapping's yaml path).
func mapping(n *yaml.Node, where string) ([]keyVal, string) {
	out := make([]keyVal, 0, len(n.Content)/2)
	seen := map[string]bool{}
	prefix := where
	if prefix != "" {
		prefix += "."
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		switch {
		case k.Kind == yaml.ScalarNode && (k.Tag == "!!merge" || k.Value == "<<"):
			return nil, orTop(where) + ": " + MergeKeyReason
		case k.Kind != yaml.ScalarNode:
			return nil, fmt.Sprintf("%s: keys must be plain names", orTop(where))
		case k.Tag == "!!null":
			return nil, fmt.Sprintf("%s: a null key (`~` / `null`) is not a name", orTop(where))
		case seen[k.Value]:
			return nil, fmt.Sprintf("%s%s: duplicate key", prefix, k.Value)
		}
		seen[k.Value] = true
		out = append(out, keyVal{k.Value, deref(n.Content[i+1])})
	}
	return out, ""
}

func orTop(where string) string {
	if where == "" {
		return "top level"
	}
	return where
}

// deref follows a yaml alias to its anchored node.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// StaleKeys answers the stale per-step keys of cfg against the lane set
// of its workspace (the built-in lanes and the workspace's user lanes),
// sorted, each as `steps.<project>.<key>`: a key that is no step id of a
// lane of the set, nor `default` or `task` — the retired `verify-judge`
// among them (a sub-definition takes the candidates of the step that
// dispatches it). A stale key governs nothing; `pf doctor` warns about
// each. An unreadable built-in lanes root is an error; one holding no lane
// makes every step key stale. An unreadable user lane root is the
// *workflow.UserLanesError and names no key: a key no built-in lane
// carries may be a step of a user lane nobody can read, so none is judged.
func StaleKeys(cfg *Config, set workflow.LaneSet) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	steps, err := set.StepIDs()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var stale []string
	note := func(project, key string) {
		name := fmt.Sprintf("steps.%s.%s", project, key)
		if !BundlelessKey(key) && !steps[key] && !seen[name] {
			seen[name] = true
			stale = append(stale, name)
		}
	}
	for project, keys := range cfg.Models {
		for key := range keys {
			note(project, key)
		}
	}
	for project, keys := range cfg.Skip {
		for key := range keys {
			note(project, key)
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// SkippedSteps answers the steps of lane that project switches off, in the
// lane's order: each step the lane declares `skippable: true` whose
// steps.<project>.<step>.skip is true in cfg. A skip on a
// step the lane does not declare skippable governs nothing. The claim fixes
// this set into the work item's capsule; `pf workflows` reports it.
func SkippedSteps(cfg *Config, lane *workflow.Lane, project string) []string {
	if cfg == nil || lane == nil {
		return nil
	}
	var out []string
	for _, st := range lane.Steps {
		if st.Skippable && cfg.Skip[project][st.ID] {
			out = append(out, st.ID)
		}
	}
	return out
}

// KeyRenameSection is the docs/upgrade.md section that explains the
// per-step key renames a stale key most often needs — the retired
// `verify-judge` key to `verification`, a `full-gate` key meant for
// collect's ruling copied to `collect` — the section
// StaleKeyHint names. It is a section of its own, not the current
// contract's UpgradeSection: the renames do not move with the contract.
const KeyRenameSection = "Workflow contract 4: the integration lane is a declaration; a sub-definition takes its step's models"

// StaleKeyHint is the rewrite a stale key's warning carries: what a key
// names, and the upgrade section of the retired sub-definition key.
const StaleKeyHint = "a per-step key is `default`, `task` or the id of a step of an installed lane — a sub-definition takes the models of the step that dispatches it (the verify-judge: `verification`'s), see docs/upgrade.md, section \"" + KeyRenameSection + "\""
