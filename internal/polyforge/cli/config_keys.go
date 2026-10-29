package cli

// config_keys.go: the canonical key model `pf config` edits and the
// reload class table the daemon and the CLI report. One
// fact, one place: the key name, its class and how it is addressed live
// here.

import (
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ConfigClass is the reload classification of one key: exactly one of
// live, next-claim or restart-only (user_id is CLI-only and never
// reaches the daemon).
type ConfigClass string

const (
	// ConfigLive: the running daemon applies the new value on reload.
	ConfigLive ConfigClass = "live"
	// ConfigNextClaim: the value governs the next claim/dispatch (the
	// [workspace] mapping).
	ConfigNextClaim ConfigClass = "next-claim"
	// ConfigRestartOnly: the running daemon keeps its baseline; the new
	// value applies at the next daemon start.
	ConfigRestartOnly ConfigClass = "restart-only"
	// ConfigCLIOnly: a client-side key the daemon never reads.
	ConfigCLIOnly ConfigClass = "cli-only"
)

// configKeyDef is one entry of THE canonical key model: its name, how a
// `pf config` argument addresses it, and its reload class.
type configKeyDef struct {
	name  string
	kind  configKeyKind
	class ConfigClass
}

// configKeyDefs is the one ordered key model: the flat keys first, then
// the [daemon] table keys in the docs' order. Everything else (the CLI's
// argument parser, its rendered lists and the daemon's reload) derives
// from this list.
var configKeyDefs = []configKeyDef{
	{"server", keyFlat, ConfigRestartOnly},
	{"api_key", keyFlat, ConfigRestartOnly},
	{"daemon_socket", keyFlat, ConfigRestartOnly},
	{"user_id", keyFlat, ConfigCLIOnly},
	{"claude_bin", keyFlat, ConfigLive},
	{"step_timeout", keyDaemon, ConfigLive},
	{"stall_window", keyDaemon, ConfigLive},
	{"harness_root", keyDaemon, ConfigRestartOnly},
	{"operator_agent_dir", keyDaemon, ConfigRestartOnly},
	{"max_concurrent_jobs", keyDaemon, ConfigLive},
}

// ConfigSchema answers one key's machine-readable value schema: its kind
// plus ONLY the constraints the shared validator actually enforces. It is
// derived from the same key model the CLI validates with; no bound is
// invented here (notably `language` has no enum).
func ConfigSchema(name string) map[string]any {
	switch name {
	case "server", "user_id", "claude_bin":
		return map[string]any{"kind": "string"}
	case "api_key":
		return map[string]any{"kind": "secret"}
	case "daemon_socket", "harness_root", "operator_agent_dir":
		return map[string]any{"kind": "path"}
	case "step_timeout":
		return map[string]any{"kind": "duration", "min_exclusive": 0}
	case "stall_window":
		return map[string]any{"kind": "duration", "nonzero": true, "allow_negative": true}
	case "max_concurrent_jobs":
		return map[string]any{"kind": "int", "min": 1}
	}
	return nil
}

// WorkspaceConfigSchema answers the value schema of one workspace
// `.polyforge.yaml` summarize key. The bounds are the workspace package's
// existing validator constants (never a UI copy).
func WorkspaceConfigSchema(name string) map[string]any {
	switch name {
	case "summarize.models":
		return map[string]any{"kind": "model-list"}
	case "summarize.language":
		return map[string]any{"kind": "string"}
	case "summarize.interval_sec":
		return map[string]any{"kind": "int", "min": workspace.MinSummarizeIntervalSec, "max": workspace.MaxSummarizeIntervalSec}
	}
	return nil
}

// WorkspaceConfigClass answers a workspace summarize key's reload class. The
// summarize lane reads the yaml live at every request, so every summarize key
// is live — the same class table the cli.toml keys use, never a UI copy.
func WorkspaceConfigClass(name string) ConfigClass {
	switch name {
	case "summarize.models", "summarize.language", "summarize.interval_sec":
		return ConfigLive
	}
	return ""
}

// ConfigKeys lists every cli.toml key in canonical order — the one key
// list the CLI's key model and the daemon's reload share.
func ConfigKeys() []string {
	out := make([]string, 0, len(configKeyDefs))
	for _, d := range configKeyDefs {
		out = append(out, d.name)
	}
	return out
}

// ConfigClassOf answers a key name's reload class. An unknown name is
// not a config key; a whole [workspace] section uses ConfigSectionClass.
func ConfigClassOf(name string) (ConfigClass, bool) {
	_, class, ok := lookupConfigKey(name)
	return class, ok
}

// ConfigSectionClass is the class of a whole [workspace."<path>"]
// section (the mapping), the one next-claim outcome of a reload.
const ConfigSectionClass = ConfigNextClaim

func lookupConfigKey(name string) (configKeyKind, ConfigClass, bool) {
	for _, d := range configKeyDefs {
		if d.name == name {
			return d.kind, d.class, true
		}
	}
	return 0, "", false
}

// configKeyKind is how a `pf config` key argument addresses the file.
type configKeyKind int

const (
	keyFlat configKeyKind = iota
	keyDaemon
	keyWorkspace
)

// configKey is one parsed key argument.
type configKey struct {
	kind configKeyKind
	name string      // flat/daemon key name, or the workspace path as spelled
	raw  string      // the argument as given (for error messages)
	cls  ConfigClass // zero for a workspace section
}

func (k configKey) isWorkspace() bool { return k.kind == keyWorkspace }

// parseConfigKey resolves one key argument: a flat key name, a [daemon]
// key (bare or the tolerated "[daemon] <key>" spelling) or a whole
// [workspace."<path>"] section. An unknown name is a typed refusal.
func parseConfigKey(arg string) (configKey, error) {
	raw := arg
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return configKey{}, fmt.Errorf("config: a key is required (a flat key, a [daemon] key or [workspace.\"<path>\"]); see `pf help config`")
	}
	// whole workspace section
	if strings.HasPrefix(arg, "[workspace.") {
		if !strings.HasSuffix(arg, "]") {
			return configKey{}, fmt.Errorf("config: malformed workspace section key %q (want [workspace.\"<path>\"])", raw)
		}
		key, err := parseSectionHeader(arg, 0)
		if err != nil {
			return configKey{}, fmt.Errorf("config: %v", err)
		}
		return configKey{kind: keyWorkspace, name: key, raw: raw}, nil
	}
	if strings.HasPrefix(arg, "[daemon]") {
		rest := strings.TrimSpace(strings.TrimPrefix(arg, "[daemon]"))
		if rest == "" {
			return configKey{}, fmt.Errorf("config: [daemon] names a table, not a key — say `[daemon] <key>` or the bare key name")
		}
		target := rest
		kind, cls, ok := lookupConfigKey(target)
		if !ok || kind != keyDaemon {
			return configKey{}, unknownConfigKey(target)
		}
		return configKey{kind: keyDaemon, name: target, raw: raw, cls: cls}, nil
	}
	if kind, cls, ok := lookupConfigKey(arg); ok {
		return configKey{kind: kind, name: arg, raw: raw, cls: cls}, nil
	}
	return configKey{}, unknownConfigKey(arg)
}

func unknownConfigKey(name string) error {
	return fmt.Errorf("config: unknown key %q — the known keys are the flat keys (%s), the [daemon] keys (%s) and whole [workspace.\"<path>\"] sections",
		name, strings.Join(knownFlatKeyNames(), ", "), strings.Join(knownDaemonKeyNames(), ", "))
}

func knownFlatKeyNames() []string {
	return configKeyNamesOfKind(keyFlat)
}

func knownDaemonKeyNames() []string {
	return configKeyNamesOfKind(keyDaemon)
}

func configKeyNamesOfKind(kind configKeyKind) []string {
	var out []string
	for _, d := range configKeyDefs {
		if d.kind == kind {
			out = append(out, d.name)
		}
	}
	return out
}

func mustConfigClass(name string) ConfigClass {
	class, _ := ConfigClassOf(name)
	return class
}

// classOfKey answers a key argument's class (workspace sections are
// next-claim).
func classOfKey(k configKey) ConfigClass {
	if k.isWorkspace() {
		return ConfigSectionClass
	}
	return k.cls
}

// ReloadStatus is the reload summary GET /status carries: the keys
// whose new value waits for the next daemon start, the keys an explicit
// daemon flag overrides, and the last reload's per-key outcomes.
type ReloadStatus struct {
	Pending    []string           `json:"pending,omitempty"`
	Overridden []string           `json:"overridden,omitempty"`
	Outcomes   []ConfigKeyOutcome `json:"outcomes,omitempty"`
}
