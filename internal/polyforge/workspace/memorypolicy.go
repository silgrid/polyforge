package workspace

// memorypolicy.go: the memory policy types and merge rule — what the
// workflow's last step puts into the work item's experience.wrap summary
// (`record`) and what never goes in (`skip`). The declared entries are
// cli.toml's [memory_policy."<path>".<default|project>] family
// (parsed in internal/polyforge/cli); a path without a
// block is the built-in default, never a refusal.
//
// Merge: a list `default` declares replaces the built-in one; a project's
// lists are appended to that base (duplicates dropped). A declared empty
// list governs as empty.

// PolicyEntry is one declared entry; a nil list is not declared.
type PolicyEntry struct{ Record, Skip []string }

// MemoryPolicy is the effective policy of one project.
type MemoryPolicy struct {
	Record []string `json:"record"`
	Skip   []string `json:"skip"`
}

// builtinPolicy is the built-in default policy: what governs a workspace
// path whose cli.toml carries no [memory_policy."<path>".default] block,
// and the block `pf init` writes as an editable copy.
var builtinPolicy = PolicyEntry{
	Record: []string{
		"踩坑和根因（现象→原因→解法）",
		"代码/git 里看不出来的环境与外部系统事实",
		"所有者的裁决和约定",
	},
	Skip: []string{
		"过程流水、step 输出、完整 spec/plan（已在票和 capsule 里）",
		"代码/git 能直接看出来的东西",
		"临时状态",
		"密钥和凭据的值",
	},
}

// DefaultMemoryPolicy answers the built-in policy (a fresh copy).
func DefaultMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{Record: clone(builtinPolicy.Record), Skip: clone(builtinPolicy.Skip)}
}

// MemoryPolicyFor answers project's effective policy from one workspace's
// declared entries (nil = none declared: the built-in default): a list
// `default` declares replaces the built-in one, the project's lists are
// appended to that base with duplicates dropped.
func MemoryPolicyFor(entries map[string]PolicyEntry, project string) MemoryPolicy {
	p := DefaultMemoryPolicy()
	if d, ok := entries[DefaultKey]; ok {
		if d.Record != nil {
			p.Record = clone(d.Record)
		}
		if d.Skip != nil {
			p.Skip = clone(d.Skip)
		}
	}
	if e, ok := entries[project]; ok && project != DefaultKey {
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
