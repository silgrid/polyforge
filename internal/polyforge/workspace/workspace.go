// Package workspace implements the workspace-level per-step settings
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
//	    <step-id>: { models: [...] }     # optional: the steps with this id,
//	                                     # in every bundle (fix, code, review, ...)
//	summarize:                           # optional: the summarize lane, one
//	  models: [...]                      # entry for every project of this
//	                                     # workspace: its model candidates
//	                                     # (polyforge#224; mandatory in the entry)
//	  language: <text>                   # optional: the narration language
//	                                     # (polyforge#243; absent = 中文)
//	  interval_sec: <int>                # optional: the minimum seconds between
//	                                     # call starts per WI (polyforge#250;
//	                                     # 10-3600, absent = 60)
//	memory_policy: ...                   # optional section (memorypolicy.go)
//
// A step entry has one key, `models`: a list of model candidates, each
// `<harness>:<model>` (polyforge#175 — harness ∈ pi | claude, split on
// the first `:`; the prefix is mandatory and is the whole truth of where
// the candidate runs). The root `summarize` entry has the same `models`
// plus the optional `language`: a plain string, trimmed, non-empty and
// single-line, passed verbatim into the summarize prompt (no allowlist,
// no length cap), and the optional `interval_sec`: a YAML integer in
// 10-3600 inclusive (never clamped; no 0-means-default). Any key not shown
// here — at any level — is a load error, as are duplicate keys, type
// mismatches, an empty project entry, an unprefixed / unknown-prefix /
// empty-model candidate, the retired `harness` key (refused with a
// rewrite hint) and a per-step key that is no step id of the installed
// workflows (a typo would otherwise silently govern nothing). The
// retired top-level `models:` section (the pre-#169 shape) refuses with
// a migration message; `projects:` / `workflow_defaults:` are unknown
// keys like any other.
//
// Presence semantics: an ABSENT file and an EMPTY or comments-only file
// both answer (nil, nil) — absent-equivalent, never an error. A file
// that declares anything must parse strictly.
//
// List presence: a declared-but-empty list is a PRESENT level that
// wholly governs (yielding no switchable chain rather than a
// fall-through). Absent keys fall through per the precedence chain
// (compose.go). The model chain has five levels, highest first: manual
// `pf submit --models` (per work item, every step) >
// steps.<project>.<step-id>.models > bundle step model_candidates >
// steps.<project>.default.models > bundle defaults.model_candidates.
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

// SummarizeKey is the top-level key whose `models` list names the
// demand-gated summarize lane's model candidates (polyforge#224,
// polyforge#242): summarize.models, one list for every project of the
// workspace. It is NEVER a per-step precedence level (compose.go
// SummarizeModels). A file that declares no such key has the lane
// silently off ("off" is a legitimate state in the demand-gate era,
// never a typed failure).
const SummarizeKey = "summarize"

// Config is the parsed `.polyforge.yaml`.
type Config struct {
	// Models is project name → (DefaultKey or a step id → candidate
	// list): steps.<project>.<key>.models, flattened for the compose.
	// Every candidate is in canonical `<harness>:<model>` form.
	Models map[string]map[string][]string
	// MemoryPolicy is the declared memory_policy section (nil = absent).
	MemoryPolicy map[string]PolicyEntry
	// Summarize is the root summarize.models list in canonical form
	// (nil = absent; a declared-empty list is empty, non-nil).
	Summarize []string
	// SummarizeLanguage is the root summarize.language value, trimmed
	// ("" = absent; compose.go SummarizeLanguage resolves the default).
	SummarizeLanguage string
	// SummarizeIntervalSec is the root summarize.interval_sec value
	// (0 = absent; compose.go SummarizeIntervalSec resolves the default).
	SummarizeIntervalSec int
}

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
// Every per-step key must be a step id of the bundles under workflowsDir
// (workflow.StepIDs; "" skips that one check), and every project key one
// of projects — the projects cli.toml maps to this workspace (nil skips
// that check).
func Load(workspaceRoot, workflowsDir string, projects []string) (*Config, error) {
	path := PathFor(workspaceRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &Error{Path: path, Reason: fmt.Sprintf("unreadable: %v", err)}
	}
	cfg, reason := parse(raw, projects)
	if reason != "" {
		return nil, &Error{Path: path, Reason: reason}
	}
	if cfg == nil || workflowsDir == "" {
		return cfg, nil
	}
	steps, err := workflow.StepIDs(workflowsDir)
	if err != nil {
		return nil, &Error{Path: path, Reason: fmt.Sprintf("the installed workflows are unreadable, so the step-id keys cannot be checked: %v", err)}
	}
	if reason := checkStepIDs(cfg, steps, workflowsDir); reason != "" {
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
		return nil, "the file must be a mapping with `steps:`, `summarize:` and/or `memory_policy:` sections"
	}
	top, reason := mapping(root, "")
	if reason != "" {
		return nil, reason
	}
	var stepsNode *yaml.Node
	cfg := &Config{}
	for _, kv := range top {
		switch kv.key {
		case "steps":
			stepsNode = kv.val
		case SummarizeKey:
			if reason = summarizeEntry(kv.val, cfg); reason != "" {
				return nil, reason
			}
		case MemoryPolicyKey:
			if cfg.MemoryPolicy, reason = parseMemoryPolicy(kv.val, projects); reason != "" {
				return nil, reason
			}
		default:
			return nil, fmt.Sprintf("unknown top-level key %q (only `steps`, `summarize` and `memory_policy`)", kv.key)
		}
	}
	if stepsNode == nil || isNull(stepsNode) {
		if cfg.MemoryPolicy != nil || cfg.Summarize != nil {
			return cfg, ""
		}
		return nil, "" // a file declaring nothing governs nothing
	}
	if stepsNode.Kind != yaml.MappingNode {
		return nil, "steps: must be a mapping of project → step entries"
	}
	byProject, reason := mapping(stepsNode, "steps")
	if reason != "" {
		return nil, reason
	}
	cfg.Models = make(map[string]map[string][]string, len(byProject))
	var unmapped []string
	for _, pkv := range byProject {
		project, pn := pkv.key, pkv.val
		if projects != nil && !contains(projects, project) {
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
			list, reason := stepEntry(ekv.val, where+"."+ekv.key)
			if reason != "" {
				return nil, reason
			}
			cfg.Models[project][ekv.key] = list
		}
	}
	if len(unmapped) > 0 {
		return nil, fmt.Sprintf("project(s) %s are not mapped to this workspace in cli.toml (it maps: %s)", strings.Join(unmapped, ", "), strings.Join(projects, ", "))
	}
	return cfg, ""
}

// stepEntry reads one `{ models: [...] }` step entry at path where:
// `models` is the only key. The retired `harness` key refuses with the
// rewrite hint.
func stepEntry(en *yaml.Node, where string) ([]string, string) {
	if en.Kind != yaml.MappingNode {
		return nil, where + ": must be `{ models: [...] }`"
	}
	keys, reason := mapping(en, where)
	if reason != "" {
		return nil, reason
	}
	var mn *yaml.Node
	for _, kv := range keys {
		switch kv.key {
		case "models":
			mn = kv.val
		case "harness":
			return nil, where + ".harness: " + workflow.RetiredHarnessHint
		default:
			return nil, fmt.Sprintf("%s.%s: not allowed (a step entry's only key is `models`)", where, kv.key)
		}
	}
	return modelsList(mn, where)
}

// summarizeEntry reads the root `{ models: [...], language: <text>,
// interval_sec: <int> }` summarize entry into cfg: `models` as strict as
// a step entry's and mandatory (an entry without it governs nothing and
// refuses like an empty one); `language` optional (polyforge#243) — a
// plain string, trimmed, non-empty and single-line, no allowlist and no
// length cap; `interval_sec` optional (polyforge#250) — a YAML integer
// in MinSummarizeIntervalSec..MaxSummarizeIntervalSec inclusive, never
// clamped. An absent key leaves its field zero (compose.go resolves the
// defaults).
func summarizeEntry(en *yaml.Node, cfg *Config) string {
	where := SummarizeKey
	if en.Kind != yaml.MappingNode {
		return where + ": must be `{ models: [...] }`"
	}
	keys, reason := mapping(en, where)
	if reason != "" {
		return reason
	}
	var mn, ln, in *yaml.Node
	for _, kv := range keys {
		switch kv.key {
		case "models":
			mn = kv.val
		case "language":
			ln = kv.val
		case "interval_sec":
			in = kv.val
		case "harness":
			return where + ".harness: " + workflow.RetiredHarnessHint
		default:
			return fmt.Sprintf("%s.%s: not allowed (the summarize entry's keys are `models`, `language` and `interval_sec`)", where, kv.key)
		}
	}
	list, reason := modelsList(mn, where)
	if reason != "" {
		return reason
	}
	cfg.Summarize = list
	if ln != nil {
		if ln.Kind != yaml.ScalarNode || ln.Tag != "!!str" {
			return where + ".language: must be a plain string (e.g. `English`)"
		}
		lang := strings.TrimSpace(ln.Value)
		if lang == "" {
			return where + ".language: is empty — name a language, or remove the key (absent = " + DefaultSummarizeLanguage + ")"
		}
		if strings.ContainsAny(lang, "\r\n") {
			return where + ".language: must be a single line"
		}
		cfg.SummarizeLanguage = lang
	}
	if in != nil {
		rangeReason := fmt.Sprintf("%s.interval_sec: must be an integer in %d-%d (seconds; absent = %d)",
			where, MinSummarizeIntervalSec, MaxSummarizeIntervalSec, DefaultSummarizeIntervalSec)
		var sec int
		if in.Kind != yaml.ScalarNode || in.Tag != "!!int" || in.Decode(&sec) != nil {
			return rangeReason
		}
		if sec < MinSummarizeIntervalSec || sec > MaxSummarizeIntervalSec {
			return rangeReason
		}
		cfg.SummarizeIntervalSec = sec
	}
	return ""
}

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

// checkStepIDs refuses a per-step key that is no step id in steps (every
// unknown key named, sorted — or, when the workflows dir holds no
// readable bundle at all, that fact). The task id is bundle-less by
// design and is recognized like DefaultKey.
func checkStepIDs(cfg *Config, steps map[string]bool, workflowsDir string) string {
	seen := map[string]bool{}
	var unknown []string
	note := func(project, key string) {
		name := fmt.Sprintf("steps.%s.%s", project, key)
		if key != DefaultKey && key != TaskKey && !steps[key] && !seen[name] {
			seen[name] = true
			unknown = append(unknown, name)
		}
	}
	for project, keys := range cfg.Models {
		for key := range keys {
			note(project, key)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	if len(steps) == 0 {
		return fmt.Sprintf("no workflows found in %s, so no per-step key can be checked", workflowsDir)
	}
	sort.Strings(unknown)
	return fmt.Sprintf("unknown key(s) %s: a key is %q, %q or a step id of the installed workflows", strings.Join(unknown, ", "), DefaultKey, TaskKey)
}
