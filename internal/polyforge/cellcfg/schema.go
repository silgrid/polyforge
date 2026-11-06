package cellcfg

import "sort"

// Scope is the one owner of a configuration key: the Host or a Cell. It is
// fixed by the key, never by the file the key happens to sit in.
type Scope string

const (
	ScopeHost Scope = "host"
	ScopeCell Scope = "cell"
)

// Class is how a running Host applies a changed value.
type Class string

const (
	ClassLive        Class = "live"              // applied at once
	ClassNextClaim   Class = "next-claim"        // applied at the next claim
	ClassRestartOnly Class = "restart-only"      // applied by a Host restart
	ClassConnection  Class = "connection-change" // drains the Cell, then switches (same identity only)
	ClassCLIOnly     Class = "cli-only"          // read by the CLI, never by the daemon
	ClassReadOnly    Class = "read-only"         // reported, never settable
)

// Key is one row of the Host/Cell partition.
type Key struct {
	Name    string // the key as written ("server", "max_concurrent_jobs", "source", "workspace")
	Section string // "" = flat; "daemon", "model", "summarize", "workspace", "memory_policy"
	Scope   Scope
	Class   Class
}

// Keys is the one Host/Cell key table (docs/reference/config.md mirrors it).
var Keys = []Key{
	{"daemon_socket", "", ScopeHost, ClassRestartOnly},
	{"max_concurrent_jobs", "daemon", ScopeHost, ClassLive},
	{"max_concurrent_bars", "daemon", ScopeHost, ClassLive},
	{"bar_load_threshold", "daemon", ScopeHost, ClassLive},
	{"harness_root", "daemon", ScopeHost, ClassRestartOnly},
	{"operator_agent_dir", "daemon", ScopeHost, ClassRestartOnly},
	{"python", "daemon", ScopeHost, ClassRestartOnly},
	{"layout_version", "", ScopeHost, ClassReadOnly},
	{"server", "", ScopeCell, ClassConnection},
	{"api_key", "", ScopeCell, ClassConnection},
	{"user_id", "", ScopeCell, ClassCLIOnly},
	{"claude_bin", "", ScopeCell, ClassLive},
	{"step_timeout", "daemon", ScopeCell, ClassLive},
	{"stall_window", "daemon", ScopeCell, ClassLive},
	{"ask_warm_window", "daemon", ScopeCell, ClassRestartOnly},
	{"command_path", "daemon", ScopeCell, ClassLive},
	{"cell_max_concurrent_jobs", "daemon", ScopeCell, ClassLive},
	{"machine_id", "daemon", ScopeCell, ClassRestartOnly},
	{"heartbeat_every", "daemon", ScopeCell, ClassLive},
	{"agent_language", "daemon", ScopeCell, ClassLive},
	{"source", "model", ScopeCell, ClassRestartOnly},
	{"workspace", "workspace", ScopeCell, ClassNextClaim},
	{"models", "summarize", ScopeCell, ClassLive},
	{"language", "summarize", ScopeCell, ClassLive},
	{"interval_sec", "summarize", ScopeCell, ClassLive},
	{"memory_policy", "memory_policy", ScopeCell, ClassNextClaim},
}

// Lookup answers the row of (section, name); ok=false for an unknown key.
// A [workspace] or [memory_policy] table row is looked up by its section.
func Lookup(section, name string) (Key, bool) {
	for _, k := range Keys {
		if k.Section != section {
			continue
		}
		if k.Name == name || section == "workspace" || section == "memory_policy" {
			return k, true
		}
	}
	return Key{}, false
}

// HostOnly reports whether (section, name) is a Host-only key.
func HostOnly(section, name string) bool {
	k, ok := Lookup(section, name)
	return ok && k.Scope == ScopeHost
}

// HostOnlyKeyNames lists the Host-only key names, sorted (refusal texts).
func HostOnlyKeyNames() []string {
	var out []string
	for _, k := range Keys {
		if k.Scope == ScopeHost && k.Class != ClassReadOnly {
			out = append(out, k.Name)
		}
	}
	sort.Strings(out)
	return out
}
