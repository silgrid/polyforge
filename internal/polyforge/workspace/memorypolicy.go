package workspace

// memorypolicy.go: the `memory_policy:` section of `.polyforge.yaml` —
// what the workflow's last step puts into the work item's experience.wrap
// summary (`record`) and what never goes in (`skip`):
//
//	memory_policy:
//	  default: { record: [...], skip: [...] }      # optional, every project
//	  <project-name>: { record: [...], skip: [...] }
//
// Merge: a list `default` declares replaces the built-in one; a project's
// lists are appended to that base (duplicates dropped). A declared empty
// list governs as empty. A missing section is the built-in default, never
// a refusal; a declared one parses strictly (invalid → DAEMON-414).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MemoryPolicyKey is the section's top-level key.
const MemoryPolicyKey = "memory_policy"

// PolicyEntry is one declared entry; a nil list is not declared.
type PolicyEntry struct{ Record, Skip []string }

// MemoryPolicy is the effective policy of one project.
type MemoryPolicy struct {
	Record []string `json:"record"`
	Skip   []string `json:"skip"`
}

// DefaultMemoryPolicySection is the built-in default, as the text `pf
// init` appends and as the source the built-in policy is parsed from.
const DefaultMemoryPolicySection = `# memory_policy: what the workflow's last step puts into the work item's
# experience.wrap summary memory. record = worth remembering, skip = never
# in it. A project entry's lists are appended to default's:
#   <project>: { record: [...], skip: [...] }
memory_policy:
  default:
    record:
      - 踩坑和根因（现象→原因→解法）
      - 代码/git 里看不出来的环境与外部系统事实
      - 所有者的裁决和约定
    skip:
      - 过程流水、step 输出、完整 spec/plan（已在票和 capsule 里）
      - 代码/git 能直接看出来的东西
      - 临时状态
      - 密钥和凭据的值
`

var builtinPolicy = func() PolicyEntry {
	cfg, reason := parse([]byte(DefaultMemoryPolicySection), nil)
	if reason != "" {
		panic("workspace: the built-in memory_policy does not parse: " + reason)
	}
	return cfg.MemoryPolicy[DefaultKey]
}()

// DefaultMemoryPolicy answers the built-in policy (a fresh copy).
func DefaultMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{Record: clone(builtinPolicy.Record), Skip: clone(builtinPolicy.Skip)}
}

// MemoryPolicyFor answers project's effective policy (nil config or no
// section: the built-in default).
func (c *Config) MemoryPolicyFor(project string) MemoryPolicy {
	p := DefaultMemoryPolicy()
	if c == nil {
		return p
	}
	if d, ok := c.MemoryPolicy[DefaultKey]; ok {
		if d.Record != nil {
			p.Record = clone(d.Record)
		}
		if d.Skip != nil {
			p.Skip = clone(d.Skip)
		}
	}
	if e, ok := c.MemoryPolicy[project]; ok && project != DefaultKey {
		p.Record, p.Skip = appendNew(p.Record, e.Record), appendNew(p.Skip, e.Skip)
	}
	return p
}

// clone copies a list, keeping a non-nil empty list non-nil.
func clone(l []string) []string { return append(make([]string, 0, len(l)), l...) }

func appendNew(base, more []string) []string {
	for _, s := range more {
		if !contains(base, s) {
			base = append(base, s)
		}
	}
	return base
}

// parseMemoryPolicy reads the section: DefaultKey or a mapped project →
// { record, skip }, each a list of unique non-empty plain strings.
func parseMemoryPolicy(n *yaml.Node, projects []string) (map[string]PolicyEntry, string) {
	if isNull(n) || (n.Kind == yaml.MappingNode && len(n.Content) == 0) {
		return nil, MemoryPolicyKey + ": section is empty — declare `default` or a project entry, or remove it"
	}
	if n.Kind != yaml.MappingNode {
		return nil, MemoryPolicyKey + ": must be a mapping of `default` / project → { record: [...], skip: [...] }"
	}
	entries, reason := mapping(n, MemoryPolicyKey)
	if reason != "" {
		return nil, reason
	}
	out := make(map[string]PolicyEntry, len(entries))
	var unmapped []string
	for _, kv := range entries {
		where := MemoryPolicyKey + "." + kv.key
		if kv.key != DefaultKey && projects != nil && !contains(projects, kv.key) {
			unmapped = append(unmapped, kv.key)
		}
		if isNull(kv.val) || (kv.val.Kind == yaml.MappingNode && len(kv.val.Content) == 0) {
			return nil, where + ": entry is empty — declare `record` and/or `skip`, or remove it"
		}
		if kv.val.Kind != yaml.MappingNode {
			return nil, where + ": must be `{ record: [...], skip: [...] }`"
		}
		keys, reason := mapping(kv.val, where)
		if reason != "" {
			return nil, reason
		}
		var e PolicyEntry
		for _, lk := range keys {
			if lk.key != "record" && lk.key != "skip" {
				return nil, fmt.Sprintf("%s.%s: not allowed (an entry's keys are `record` and `skip`)", where, lk.key)
			}
			list, reason := policyList(lk.val, where+"."+lk.key)
			if reason != "" {
				return nil, reason
			}
			if lk.key == "record" {
				e.Record = list
			} else {
				e.Skip = list
			}
		}
		out[kv.key] = e
	}
	if len(unmapped) > 0 {
		return nil, fmt.Sprintf("project(s) %s are not mapped to this workspace in cli.toml (it maps: %s)", strings.Join(unmapped, ", "), strings.Join(projects, ", "))
	}
	return out, ""
}

func policyList(n *yaml.Node, where string) ([]string, string) {
	if n.Kind != yaml.SequenceNode {
		return nil, where + ": must be a list of strings"
	}
	list := make([]string, 0, len(n.Content))
	for i, item := range n.Content {
		item = deref(item)
		s := strings.TrimSpace(item.Value)
		switch {
		case item.Kind != yaml.ScalarNode || item.Tag != "!!str":
			return nil, fmt.Sprintf("%s[%d]: must be a plain string", where, i)
		case s == "":
			return nil, fmt.Sprintf("%s[%d]: is empty", where, i)
		case contains(list, s):
			return nil, fmt.Sprintf("%s[%d]: repeats %q", where, i, s)
		}
		list = append(list, s)
	}
	return list, ""
}

// planMemoryPolicy plans (writing nothing) giving the workspace's yaml
// the default section when it declares none: an absent file is created
// with it, an existing one gets it APPENDED as text (its bytes kept
// exactly, never re-marshalled). A file that does not load, or would not
// with the section appended, answers an error. how: created | appended |
// unchanged; data nil = nothing to write.
func planMemoryPolicy(root string) (path string, data []byte, mode fs.FileMode, how string, err error) {
	path, mode = PathFor(root), 0o644
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, []byte(DefaultMemoryPolicySection), mode, "created", nil
	}
	if err != nil {
		return path, nil, mode, "", err
	}
	if fi, serr := os.Stat(path); serr == nil {
		mode = fi.Mode().Perm()
	}
	cfg, reason := parse(raw, nil)
	switch {
	case reason != "":
		return path, nil, mode, "", fmt.Errorf("%s: %s — fix it first (`pf doctor` names the defect)", path, reason)
	case cfg != nil && cfg.MemoryPolicy != nil:
		return path, nil, mode, "unchanged", nil
	}
	out := append([]byte(nil), raw...)
	if len(out) > 0 {
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	out = append(out, DefaultMemoryPolicySection...)
	if _, reason := parse(out, nil); reason != "" {
		return path, nil, mode, "", fmt.Errorf("%s: appending the memory_policy section would not load (%s) — add it by hand", path, reason)
	}
	return path, out, mode, "appended", nil
}
