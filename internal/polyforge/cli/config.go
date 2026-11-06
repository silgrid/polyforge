// Package cli implements the `pf` client CLI of Polyforge.
//
// Routing split:
//
//	Direct to aihub HTTP (internal/common/api types, aihubclient client):
//	  file, queue, cancel, recall, remember, memory — plus the management
//	  face (users, api-keys, projects, backup, machines).
//
//	Local daemon UDS HTTP face (the client surface is pinned in
//	internal/common/protocol and verbs_daemon.go):
//	  submit, answer, pause, resume, wrap, ops, watch, daemon.
//
// Output: human-readable tables by default; --json emits the server's
// JSON response as-is. Errors are decoded from the typed errStatus
// envelope (common/api.Error: {code, detail, extra}) and rendered with
// the code prominent; exit codes follow the wire status class:
// 2xx → 0, 4xx → 1, 5xx → 2 (usage errors also 2).
//
// Configuration precedence: command-line flags > PF_* environment >
// ~/.config/polyforge/cli.toml (flat string keys, [workspace."<path>"]
// sections, the [daemon] and [summarize] tables and the
// [memory_policy."<path>".<entry>] family) > defaults. There is NO default project (polyforge#113): the
// [workspace] mapping decides capsule placement, and project scoping is
// per-invocation --project.
package cli

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// Default aihub listen address.
const defaultServer = "http://localhost:8420"

// DefaultMaxConcurrentJobs is the daemon's documented default job cap
// when [daemon] max_concurrent_jobs is unset (also what unsetting the key
// restores on a reload).
const DefaultMaxConcurrentJobs = 4

// DefaultMaxConcurrentBars is the daemon-wide heavy-run slot count when
// [daemon] max_concurrent_bars is unset: how many heavy
// runs (a dispatch declaring `admission: heavy`) run at
// once across every repo and work item; a further one queues.
const DefaultMaxConcurrentBars = 2

// DefaultBarLoadThreshold answers the load threshold when [daemon]
// bar_load_threshold is unset: the machine's cores less 4,
// at least 1. A heavy run is admitted only while the 1-minute load
// average is below it (and, so the queue always moves, whenever no
// admitted bar runs).
func DefaultBarLoadThreshold() float64 {
	return float64(max(goruntime.NumCPU()-4, 1))
}

// DataDir is the daemon data dir for the given home: $POLYFORGE_DATA_DIR,
// else <home>/.local/share/polyforge (tools/install.sh's).
func DataDir(home string) string {
	if d := os.Getenv("POLYFORGE_DATA_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".local", "share", "polyforge")
}

// defaultDaemonSocket is the daemon's unix domain socket when nothing
// names one: daemon.sock in DataDir — the same for pf and the daemon, so
// a daemon's -data-dir does not move it (reference.md §6.1: local
// machine only, no network face).
func defaultDaemonSocket() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(DataDir(home), "daemon.sock")
}

// Config holds the resolved CLI configuration.
type Config struct {
	Server       string        // aihub base URL
	APIKey       string        // aihub api key (Bearer)
	DaemonSocket string        // daemon unix socket path
	UserID       string        // caller identity for answer.answered_by
	Mapping      *mapping.Plan // the workspace→projects plan of record (non-nil; possibly empty)
	// ClaudeBin is the daemon-side Claude Code harness setting
	// (polyforge#159): the claude binary of claude steps. The token
	// authority layer is <harness-root>/claude-config/settings.json's
	// env block (polyforge#171, ruling 1) — there is NO config-file key
	// for credentials ever again (the former anthropic_auth_token is
	// hard-deleted, ruling 2: a residual key now fails the unknown-key
	// parse with the startup error it deserved.
	ClaudeBin string
	// Daemon is the [daemon] table: the production daemon knobs the
	// daemon reads from its --config file (decision 2026-10-16 G2).
	// Validated on load by every pf command and the daemon alike; the
	// zero value = every daemon default.
	Daemon DaemonSettings
	// Summarize is the machine-level [summarize] table, the summarize
	// lane's only source; nil = the table is absent and
	// the lane is off.
	Summarize *workspace.SummarizeSettings
	// MemoryPolicy is the [memory_policy."<path>".<entry>] family:
	// mapped workspace path → entry (`default` or a project of that path)
	// → its declared lists. A path without a block is the built-in
	// default.
	MemoryPolicy map[string]map[string]workspace.PolicyEntry
	// ModelSource is a named Cell's [model] source: the model-credential
	// declaration ("shared-default" or an absolute directory; "" = none,
	// which disables that Cell's execution plane with a visible reason).
	// The default Cell takes its credentials from [daemon]
	// operator_agent_dir and carries no [model] table.
	ModelSource string
}

// DaemonSettings is the cli.toml [daemon] table. A zero field is unset
// (the daemon's own default applies); an explicit daemon command-line
// flag (tests/dev) beats the table. Every value is validated on load.
type DaemonSettings struct {
	// StepTimeout is T, the cumulative step runtime bound (Go duration,
	// > 0; daemon default 90m); flag --step-timeout.
	StepTimeout time.Duration
	// StallWindow is N, the driver-stalled-between-steps bound (Go
	// duration, non-zero; negative disables; daemon default 10m); flag
	// --stall-window.
	StallWindow time.Duration
	// AskWarmWindow is how long a step process that asked the owner (`pf
	// ask`) stays resident for the reply before the daemon ends it (Go
	// duration > 0; 0 = unset, daemon default 15m); flag --ask-warm-window.
	// Restart-only.
	AskWarmWindow time.Duration
	// HarnessRoot is the parent of the step harness dirs (absolute path;
	// daemon default <data-dir>); flag --harness-root.
	HarnessRoot string
	// OperatorAgentDir is the source of the step agent dir's credential
	// and model files (absolute path; daemon default ~/.pi/agent; a set
	// value without a readable auth.json refuses the daemon start); flag
	// --operator-agent-dir.
	OperatorAgentDir string
	// Python is the interpreter the workflow step scripts run under
	// (absolute path; "" = unset, python3 on the daemon's PATH): the
	// daemon resolves it once at its start, refuses to start when it is
	// missing or older than MinPython, and hands it to every step as
	// PF_PYTHON (python.go). Host-only, restart-only.
	Python string
	// MaxConcurrentJobs is the configured job cap (>= 1; 0 = unset,
	// daemon default 4); flag --max-concurrent-jobs. `pf ops max N`
	// overrides it, and a reload that changes max_concurrent_jobs overrides
	// `pf ops max` (last action wins).
	MaxConcurrentJobs int
	// MaxConcurrentBars is the daemon-wide heavy-run slot count (>= 1;
	// 0 = unset, DefaultMaxConcurrentBars): how many heavy runs run at
	// once. Host-only, live.
	MaxConcurrentBars int
	// BarLoadThreshold is the 1-minute load average at or above which no
	// further heavy run is admitted (> 0; 0 = unset,
	// DefaultBarLoadThreshold). Host-only, live.
	BarLoadThreshold float64
	// CommandPath is the PATH every step process (and so every command a
	// step runs: a verify.py bar's commands and its scope engine) and the
	// commands the daemon runs in-process start with: every entry an
	// absolute directory; "" = unset, the daemon process's own PATH. Live.
	CommandPath string
	// CellMaxConcurrentJobs is the Cell's optional ceiling under the Host
	// ceiling (>= 1; 0 = unset: no extra limit).
	CellMaxConcurrentJobs int
	// MachineID seeds the Cell's cells/<profile>/daemon-id before the file
	// exists ("" = m-<short hostname>); the file wins once written.
	MachineID string
	// HeartbeatEvery is the Cell's heartbeat cadence (Go duration, > 0;
	// 0 = unset: the daemon default).
	HeartbeatEvery time.Duration
	// AgentLanguage is the language of the daemon's step agents ("en" |
	// "zh-CN"; "" = unset, "en"). Live; a dispatch keeps the language it
	// started with.
	AgentLanguage string
}

// DaemonKeyError is the typed refusal of one [daemon] entry: it names
// the file, the line and the key.
type DaemonKeyError struct {
	File   string
	Line   int
	Key    string
	Reason string
}

func (e *DaemonKeyError) Error() string {
	return fmt.Sprintf("config %s: line %d: [daemon] %s: %s", e.File, e.Line, e.Key, e.Reason)
}

// daemonKV is one raw `key = value` line of the [daemon] table.
type daemonKV struct {
	key, val string
	line     int
}

// parseDaemonTable validates the [daemon] entries into DaemonSettings.
func parseDaemonTable(file string, kvs []daemonKV) (DaemonSettings, error) {
	var s DaemonSettings
	seen := map[string]bool{}
	for _, kv := range kvs {
		refuse := func(reason string) error {
			return &DaemonKeyError{File: file, Line: kv.line, Key: kv.key, Reason: reason}
		}
		if seen[kv.key] {
			return s, refuse("duplicate key")
		}
		seen[kv.key] = true
		switch kv.key {
		case "heartbeat_every":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse(`must be a quoted Go duration, e.g. "60s"`)
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return s, refuse(fmt.Sprintf(`%q is not a positive Go duration (e.g. "60s")`, v))
			}
			s.HeartbeatEvery = d
		case "machine_id":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil || strings.TrimSpace(v) == "" {
				return s, refuse("must be a non-empty quoted string")
			}
			s.MachineID = v
		case "step_timeout", "stall_window":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse(`must be a quoted Go duration, e.g. "45m"`)
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return s, refuse(fmt.Sprintf(`%q is not a Go duration (e.g. "45m", "1h30m")`, v))
			}
			if kv.key == "step_timeout" {
				if d <= 0 {
					return s, refuse("must be > 0")
				}
				s.StepTimeout = d
			} else {
				if d == 0 {
					return s, refuse("must be non-zero (a negative value disables the stall check; remove the key for the default)")
				}
				s.StallWindow = d
			}
		case "harness_root", "operator_agent_dir":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse("must be a quoted absolute path")
			}
			if !filepath.IsAbs(v) {
				return s, refuse(fmt.Sprintf("%q is not an absolute path (no ~ or $HOME expansion)", v))
			}
			if kv.key == "harness_root" {
				s.HarnessRoot = v
			} else {
				s.OperatorAgentDir = v
			}
		case "python":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse("must be a quoted absolute path to a Python >= " + MinPythonString() + " interpreter")
			}
			if !filepath.IsAbs(v) {
				return s, refuse(fmt.Sprintf("%q is not an absolute path (no ~ or $HOME expansion; remove the key for python3 on the daemon's PATH)", v))
			}
			s.Python = v
		case "max_concurrent_jobs", "max_concurrent_bars", "cell_max_concurrent_jobs":
			n, err := strconv.Atoi(kv.val)
			if err != nil {
				return s, refuse(fmt.Sprintf("must be an unquoted integer >= 1, got %s", kv.val))
			}
			if n < 1 {
				return s, refuse("must be >= 1")
			}
			switch kv.key {
			case "max_concurrent_jobs":
				s.MaxConcurrentJobs = n
			case "cell_max_concurrent_jobs":
				s.CellMaxConcurrentJobs = n
			default:
				s.MaxConcurrentBars = n
			}
		case "bar_load_threshold":
			f, err := strconv.ParseFloat(kv.val, 64)
			if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
				return s, refuse(fmt.Sprintf("must be an unquoted number > 0 (a 1-minute load average, e.g. 12 or 7.5), got %s", kv.val))
			}
			s.BarLoadThreshold = f
		case "ask_warm_window":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse(`must be a quoted Go duration, e.g. "15m"`)
			}
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return s, refuse(fmt.Sprintf(`%q is not a positive Go duration (e.g. "15m", "1h")`, v))
			}
			s.AskWarmWindow = d
		case "command_path":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil {
				return s, refuse(`must be a quoted PATH value, e.g. "/usr/local/go/bin:/usr/bin:/bin"`)
			}
			if v == "" {
				return s, refuse("must be non-empty (remove the key for the daemon process's own PATH)")
			}
			for _, dir := range filepath.SplitList(v) {
				if !filepath.IsAbs(dir) {
					return s, refuse(fmt.Sprintf("entry %q is not an absolute directory (every entry is absolute; no empty entry, no ~ or $HOME expansion)", dir))
				}
			}
			s.CommandPath = v
		case "agent_language":
			v, err := parseQuotedValue(kv.val, kv.line)
			if err != nil || !stepcall.IsLanguage(v) {
				return s, refuse(fmt.Sprintf(`must be "%s" or "%s" (quoted), got %s`, stepcall.LangEN, stepcall.LangZhCN, kv.val))
			}
			s.AgentLanguage = v
		default:
			if coreFlatKeys[kv.key] {
				return s, refuse("a top-level key: move it above the [daemon] header (every key below the header belongs to the table)")
			}
			return s, refuse("unknown key (the [daemon] table carries only " + strings.Join(knownDaemonKeyNames(), ", ") + ")")
		}
	}
	return s, nil
}

// DefaultConfigPath returns the single documented config location:
// $XDG_CONFIG_HOME|~/.config / polyforge / cli.toml. One location, no
// secondary lookup — the legacy ~/.polyforge/config.toml fallback is
// dropped (polyforge#113): a file there is simply not read.
//
// The path is computed from $XDG_CONFIG_HOME|~/.config on EVERY OS,
// deliberately not via os.UserConfigDir: on darwin that helper answers
// ~/Library/Application Support (and ignores $XDG_CONFIG_HOME), a location
// tools/install.sh never writes and the docs never name. That split bare
// pf from the ~/.config/polyforge/cli.toml install.sh writes
// (polyforge#218, polyforge#235): a macOS work machine installed a config
// pf could never read. tools/install.sh's CFG is this same path, computed
// the same way. A relative XDG_CONFIG_HOME is ignored, per the XDG spec.
func DefaultConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "polyforge/cli.toml"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "polyforge", "cli.toml")
}

// LoadConfig reads the config file (a missing file is not an error: the
// defaults apply, with an EMPTY mapping) and applies environment
// overrides on top. An empty path means the documented default location,
// not "no file". PF_SERVER/PF_API_KEY are never applied: a Cell's
// connection comes only from its file (cli.go refuseConnectionOverride).
//
//	PF_DAEMON_SOCKET  -> DaemonSocket
//	PF_USER_ID        -> UserID
//
// A malformed file IS an error: silent misconfiguration is worse than a
// loud failure. So is a mapping that fails validation — it refuses with
// guidance for every pf command and the daemon alike (the shared loader is
// the single choke point).
func LoadConfig(path string) (Config, error) {
	cfg, err := LoadFileConfig(path)
	if err != nil {
		return cfg, err
	}
	applyEnv(&cfg)
	return cfg, nil
}

// applyConfigFile maps the documented flat keys onto the config.
func applyConfigFile(cfg *Config, kv map[string]string) {
	for k, v := range kv {
		switch k {
		case "server":
			cfg.Server = v
		case "api_key":
			cfg.APIKey = v
		case "daemon_socket":
			cfg.DaemonSocket = v
		case "user_id":
			cfg.UserID = v
		case "claude_bin":
			cfg.ClaudeBin = v
		}
	}
}

// applyEnv overlays environment overrides (env beats the config file).
// There is no project variable in the set: the ambient default-project
// selection is retired (polyforge#113) — the mapping in cli.toml is the
// only placement source and --project is per-invocation.
func applyEnv(cfg *Config) {
	if v := os.Getenv("PF_DAEMON_SOCKET"); v != "" {
		cfg.DaemonSocket = v
	}
	if v := os.Getenv("PF_USER_ID"); v != "" {
		cfg.UserID = v
	}
}

// workspaceSection is one parsed `[workspace."<key>"]` section.
type workspaceSection struct {
	key      string
	projects []string
	line     int
}

// The known flat keys: root-level wherever they appear in the file —
// polyforge#171 ruling 2: the former anthropic_auth_token is NOT one
// (hard-deleted, no deprecation path), so a residual key in an old
// cli.toml fails the unknown-key error at startup, never silent
// acceptance. Credentials live in <harness-root>/claude-config/settings.json.
var coreFlatKeys = map[string]bool{"server": true, "api_key": true, "daemon_socket": true, "user_id": true,
	"claude_bin": true}

// unknownKeyError is the refusal of a top-level key outside the known set.
type unknownKeyError struct{ key, file string }

func (e *unknownKeyError) Error() string {
	return fmt.Sprintf("unknown key %q in %s", e.key, e.file)
}

// parsedConfig is one parse of a cli.toml: every documented shape with
// the file line of each row, before any value validation.
type parsedConfig struct {
	flat     map[string]string
	flatLine map[string]int // the line of each flat key
	sections []workspaceSection
	daemon   []daemonKV
	// summarize is the [summarize] table (nil = absent).
	summarize *tableBlock
	// policy is the [memory_policy."<path>".<entry>] family, file order.
	policy []tableBlock
	// model is the [model] table (nil = absent).
	model *tableBlock
}

// tableBlock is one parsed [summarize] or [memory_policy...] table: its
// header line and its raw `key = value` rows (validated by
// config_tables.go). path/entry are set for a memory_policy table only.
type tableBlock struct {
	path, entry string
	line        int
	rows        []daemonKV
}

// tableKind names the section a header opens.
type tableKind int

const (
	tableWorkspace tableKind = iota
	tableDaemon
	tableSummarize
	tablePolicy
	tableModel
)

// parseConfigTOML parses the TOML subset documented for cli.toml:
//
//   - `key = "value"` flat string keys: the known set (server, api_key,
//     daemon_socket, user_id, claude_bin) is stored; the known keys are root-level
//     wherever they appear outside a table that owns its keys; any other
//     top-level key is an error — a residual anthropic_auth_token from a
//     pre-#171 deployment hits the unknown-key refusal (the credentials now
//     live in <harness-root>/claude-config/settings.json, ruling 1-2);
//   - one section family: `[workspace."<path>"]` (quoted key mandatory)
//     — inside, only `projects = ["a", "b"]`;
//   - one `[daemon]` table (at most once): its raw `key = value` lines
//     are answered as-is and validated by parseDaemonTable;
//   - one `[summarize]` table (at most once) and the path-keyed
//     `[memory_policy."<path>".<entry>]` family (quoted path mandatory,
//     the entry `default` or a project name, bare or quoted): their raw
//     rows are validated by config_tables.go. Every key after one of these
//     table headers belongs to the table (TOML semantics), so the flat keys
//     must come before it.
//
// An array value may span several lines until its closing `]`; inside it
// every item is a quoted string, a comma inside quotes is data, and one
// trailing comma is allowed. Other tables, unquoted section keys,
// non-array or wrongly-typed projects values, a bare `projects` key at the
// top level, inline tables, and duplicate keys/sections are line-named
// parse errors, never guesses. Deliberate narrowing: escape sequences are
// not supported — a double-quoted value containing a backslash is
// rejected — and values are taken literally between their quotes. A `#`
// inside a quoted value is data, not a comment.
func parseConfigTOML(file, text string) (*parsedConfig, error) {
	p := &parsedConfig{flat: map[string]string{}, flatLine: map[string]int{}}
	var (
		inWS       bool
		daemonSeen bool
		curWS      = -1 // index into sections while inside a [workspace] section
		curTable   *[]daemonKV
	)
	lines, err := logicalTOMLLines(text)
	if err != nil {
		return nil, err
	}
	for _, ll := range lines {
		ln, line := ll.line, ll.text
		if strings.HasPrefix(line, "[") {
			kind, key, entry, err := parseTableHeader(line, ln)
			if err != nil {
				return nil, err
			}
			inWS, curWS, curTable = false, -1, nil
			switch kind {
			case tableDaemon:
				if daemonSeen {
					return nil, fmt.Errorf("line %d: duplicate [daemon] table", ln)
				}
				daemonSeen = true
				curTable = &p.daemon
			case tableSummarize:
				if p.summarize != nil {
					return nil, fmt.Errorf("line %d: duplicate [summarize] table (first at line %d)", ln, p.summarize.line)
				}
				p.summarize = &tableBlock{line: ln}
				curTable = &p.summarize.rows
			case tableModel:
				if p.model != nil {
					return nil, fmt.Errorf("line %d: duplicate [model] table (first at line %d)", ln, p.model.line)
				}
				p.model = &tableBlock{line: ln}
				curTable = &p.model.rows
			case tablePolicy:
				for _, t := range p.policy {
					if t.path == key && t.entry == entry {
						return nil, fmt.Errorf("line %d: duplicate [memory_policy.%q.%s] table (first at line %d)", ln, key, entry, t.line)
					}
				}
				p.policy = append(p.policy, tableBlock{path: key, entry: entry, line: ln})
				curTable = &p.policy[len(p.policy)-1].rows
			default:
				for _, s := range p.sections {
					if s.key == key {
						return nil, fmt.Errorf("line %d: duplicate [workspace] section for %s", ln, key)
					}
				}
				p.sections = append(p.sections, workspaceSection{key: key, line: ln})
				curWS = len(p.sections) - 1
				inWS = true
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: expected `key = \"value\"`, got %q", ln, redactConfigLine(line))
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", ln)
		}
		if strings.HasPrefix(val, "{") {
			return nil, fmt.Errorf("line %d: inline tables are not supported (%s)", ln, key)
		}
		if curTable != nil {
			*curTable = append(*curTable, daemonKV{key: key, val: val, line: ln})
			continue
		}
		if coreFlatKeys[key] {
			// Known flat key: root-level wherever it appears.
			inner, err := parseQuotedValue(val, ln)
			if err != nil {
				if key == "api_key" {
					return nil, fmt.Errorf("line %d: api_key value must be a quoted string (value redacted)", ln)
				}
				return nil, err
			}
			if _, dup := p.flat[key]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", ln, key)
			}
			p.flat[key] = inner
			p.flatLine[key] = ln
			continue
		}
		if inWS {
			// Inside a workspace section: only the projects array.
			if key != "projects" {
				return nil, fmt.Errorf("line %d: unknown key %q in section [workspace.%q] (a workspace section carries only `projects`)", ln, key, p.sections[curWS].key)
			}
			if len(p.sections[curWS].projects) > 0 {
				return nil, fmt.Errorf("line %d: duplicate projects array in the [workspace] section of %s", ln, p.sections[curWS].key)
			}
			list, err := parseStringArray(val, ln)
			if err != nil {
				return nil, err
			}
			p.sections[curWS].projects = append(p.sections[curWS].projects, list...)
			continue
		}
		if key == "projects" {
			return nil, fmt.Errorf("line %d: `projects` belongs inside a [workspace.\"<path>\"] section, not the top level", ln)
		}
		return nil, &unknownKeyError{key: key, file: file}
	}
	return p, nil
}

// logicalLine is one comment-stripped, trimmed, non-empty statement of the
// file, carrying the line number it starts on.
type logicalLine struct {
	line int
	text string
}

// logicalTOMLLines splits text into statements: one per physical line,
// except an array value that does not close on its own line, which runs
// on (each continuation comment-stripped) until its closing `]`.
func logicalTOMLLines(text string) ([]logicalLine, error) {
	var out []logicalLine
	phys := strings.Split(text, "\n")
	for i := 0; i < len(phys); i++ {
		line := strings.TrimSpace(stripTOMLComment(phys[i]))
		if line == "" {
			continue
		}
		start := i + 1
		if !strings.HasPrefix(line, "[") {
			if eq := strings.IndexByte(line, '='); eq >= 0 {
				val := strings.TrimSpace(line[eq+1:])
				for strings.HasPrefix(val, "[") && !arrayClosed(val) {
					i++
					if i >= len(phys) {
						return nil, fmt.Errorf("line %d: unterminated array (no closing `]`)", start)
					}
					more := strings.TrimSpace(stripTOMLComment(phys[i]))
					if more == "" {
						continue
					}
					val += " " + more
					line += " " + more
				}
			}
		}
		out = append(out, logicalLine{line: start, text: line})
	}
	return out, nil
}

// arrayClosed reports whether the bracket opened at val[0] closes,
// counting brackets outside quoted strings only.
func arrayClosed(val string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(val); i++ {
		c := val[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	return false
}

// parseTableHeader validates one `[...]` line: `[daemon]`, `[summarize]`,
// `[workspace."<path>"]` or `[memory_policy."<path>".<entry>]`. It answers
// the kind, the quoted path key (workspace/memory_policy) and the entry.
func parseTableHeader(line string, ln int) (tableKind, string, string, error) {
	if !strings.HasSuffix(line, "]") {
		return 0, "", "", fmt.Errorf("line %d: malformed section header %q", ln, line)
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	switch inner {
	case "daemon":
		return tableDaemon, "", "", nil
	case "summarize":
		return tableSummarize, "", "", nil
	case "model":
		return tableModel, "", "", nil
	}
	const policyPrefix = "memory_policy."
	if strings.HasPrefix(inner, policyPrefix) {
		path, rest, err := scanQuotedKey(strings.TrimSpace(inner[len(policyPrefix):]), ln, `[memory_policy."<path>".<entry>]`)
		if err != nil {
			return 0, "", "", err
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, ".") {
			return 0, "", "", fmt.Errorf("line %d: a memory_policy table names the workspace path and the entry: [memory_policy.\"<path>\".<default|project>]", ln)
		}
		entry, err := parseTableEntryKey(strings.TrimSpace(rest[1:]), ln)
		if err != nil {
			return 0, "", "", err
		}
		return tablePolicy, path, entry, nil
	}
	key, err := parseSectionHeader(line, ln)
	if err != nil {
		return 0, "", "", err
	}
	return tableWorkspace, key, "", nil
}

// scanQuotedKey reads one leading quoted key (no escapes) and answers it
// and the rest of s. shape names the header form for the refusal.
func scanQuotedKey(s string, ln int, shape string) (string, string, error) {
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') {
		return "", "", fmt.Errorf("line %d: section key must be a QUOTED path: %s", ln, shape)
	}
	end := strings.IndexByte(s[1:], s[0])
	if end < 0 {
		return "", "", fmt.Errorf("line %d: unterminated quoted key in %s", ln, shape)
	}
	key := s[1 : 1+end]
	if s[0] == '"' && strings.ContainsAny(key, "\\\n") {
		return "", "", fmt.Errorf("line %d: escapes are not supported in section keys", ln)
	}
	if key == "" {
		return "", "", fmt.Errorf("line %d: empty workspace key", ln)
	}
	return key, s[2+end:], nil
}

// parseTableEntryKey reads the entry segment of a memory_policy header:
// a bare name ([A-Za-z0-9_-]+) or one quoted key, and nothing after it.
func parseTableEntryKey(s string, ln int) (string, error) {
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		key, rest, err := scanQuotedKey(s, ln, `[memory_policy."<path>"."<entry>"]`)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(rest) != "" {
			return "", fmt.Errorf("line %d: a memory_policy table has exactly two key segments: [memory_policy.\"<path>\".<entry>]", ln)
		}
		return key, nil
	}
	if s == "" {
		return "", fmt.Errorf("line %d: a memory_policy table names an entry: [memory_policy.\"<path>\".<default|project>]", ln)
	}
	for _, c := range s {
		if !(c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return "", fmt.Errorf("line %d: a memory_policy table has exactly two key segments and a bare entry name is [A-Za-z0-9_-]: [memory_policy.\"<path>\".<entry>] (got %q)", ln, s)
		}
	}
	return s, nil
}

// parseSectionHeader validates one `[workspace."<key>"]` header (quoted
// key mandatory) — the mapping plan. It answers the workspace key; any
// other header is the unknown-section refusal.
func parseSectionHeader(line string, ln int) (string, error) {
	if !strings.HasSuffix(line, "]") {
		return "", fmt.Errorf("line %d: malformed section header %q", ln, line)
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	const wsPrefix = `workspace.`
	if !strings.HasPrefix(inner, wsPrefix) {
		return "", fmt.Errorf("line %d: unknown section %q — only [workspace.\"<path>\"] sections, [memory_policy.\"<path>\".<entry>] tables and the [daemon], [summarize] and [model] tables are supported", ln, line)
	}
	keyExpr := strings.TrimSpace(inner[len(wsPrefix):])
	if len(keyExpr) < 2 {
		return "", fmt.Errorf("line %d: section key must be a quoted path: [workspace.\"<path>\"]", ln)
	}
	quote := keyExpr[0]
	if (quote != '"' && quote != '\'') || keyExpr[len(keyExpr)-1] != quote {
		return "", fmt.Errorf("line %d: section key must be a QUOTED path: [workspace.\"<path>\"]", ln)
	}
	key := keyExpr[1 : len(keyExpr)-1]
	if quote == '"' && strings.ContainsAny(key, "\\\n") {
		return "", fmt.Errorf("line %d: escapes are not supported in section keys", ln)
	}
	if key == "" {
		return "", fmt.Errorf("line %d: empty workspace key", ln)
	}
	return key, nil
}

// parseStringArray parses `["a", "b"]` — the projects array of a
// workspace section.
func parseStringArray(val string, ln int) ([]string, error) {
	out, err := parseQuotedArray(val, ln)
	if err != nil {
		return nil, fmt.Errorf("line %d: projects must be an array of quoted strings: projects = [\"a\", \"b\"]: %w", ln, err)
	}
	return out, nil
}

// parseQuotedArray parses one array of quoted strings: items separated by
// commas outside quotes (a comma inside quotes is data), one trailing
// comma allowed, no nested arrays. The error carries no line prefix (the
// caller names the line and the key).
func parseQuotedArray(val string, ln int) ([]string, error) {
	if len(val) < 2 || val[0] != '[' || val[len(val)-1] != ']' {
		return nil, fmt.Errorf("must be an array of quoted strings, got %q", val)
	}
	body := strings.TrimSpace(val[1 : len(val)-1])
	out := []string{}
	if body == "" {
		return out, nil
	}
	var items []string
	var quote byte
	last := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case ',':
			items = append(items, body[last:i])
			last = i + 1
		}
	}
	tail := strings.TrimSpace(body[last:])
	if tail != "" || len(items) == 0 {
		items = append(items, tail)
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, fmt.Errorf("an empty array item (two commas in a row)")
		}
		s, err := parseQuotedValue(item, ln)
		if err != nil {
			return nil, fmt.Errorf("items must be quoted strings: %v", err)
		}
		out = append(out, s)
	}
	return out, nil
}

// parseQuotedValue parses one `"value"` (or single-quoted literal).
func parseQuotedValue(val string, ln int) (string, error) {
	if len(val) < 2 {
		return "", fmt.Errorf("line %d: value must be a quoted string", ln)
	}
	quote := val[0]
	if (quote != '"' && quote != '\'') || val[len(val)-1] != quote {
		return "", fmt.Errorf("line %d: value must be a quoted string, got %q", ln, val)
	}
	inner := val[1 : len(val)-1]
	if strings.IndexByte(inner, quote) >= 0 {
		return "", fmt.Errorf("line %d: one quoted string expected, got %q", ln, val)
	}
	if quote == '"' && strings.ContainsAny(inner, "\\\n") {
		return "", fmt.Errorf("line %d: escapes and multi-line strings are not supported", ln)
	}
	return inner, nil
}

// redactConfigLine replaces the value side of an api_key line with
// <redacted> so a parser error can never echo the secret (R6/O3).
func redactConfigLine(line string) string {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "api_key") {
		return line
	}
	if i := strings.IndexByte(t, '='); i >= 0 {
		return strings.TrimSpace(t[:i]) + " = <redacted>"
	}
	return "api_key <redacted>"
}

// stripTOMLComment cuts a line at the first `#` OUTSIDE any quoted string.
func stripTOMLComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote != 0 {
			switch {
			case quote == '"' && c == '\\':
				i++ // skip the escaped character
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '#':
			return line[:i]
		}
	}
	return line
}

// ---------------------------------------------------------------------------
// The Host set: cli.toml (Host + default Cell) and every cli-<profile>.toml.
// ---------------------------------------------------------------------------

// HostSet is one configuration directory's Host file and named Cell files,
// loaded file by file: a bad named file isolates that Cell only, while a bad
// Host file is the loader's error (it blocks the Host start).
type HostSet struct {
	// HostPath is the Host file (cli.toml); Dir is its directory, where the
	// named Cell files live.
	HostPath, Dir string
	// Host is the Host file's config: the Host keys and the default Cell's.
	Host Config
	// Cells are the named Cells in profile order.
	Cells []CellConfig
}

// CellConfig is one named Cell as loaded from its file.
type CellConfig struct {
	Profile string
	Path    string
	// Cfg is the Cell's resolved config: its own file's Cell keys over the
	// Host keys inherited from the Host file. Zero when Invalid is set by a
	// parse failure.
	Cfg Config
	// Invalid is the reason the Cell cannot be served ("" = valid): a name
	// or parse refusal, an incomplete connection, an unusable workspace, or
	// a conflict with the default Cell or another named Cell.
	Invalid string
}

// LoadHostSet loads the Host file at hostPath ("" = the documented default)
// and every named Cell file next to it. Only file contents are read: no
// environment override applies (a Cell's connection lives in its file).
func LoadHostSet(hostPath string) (*HostSet, error) {
	if hostPath == "" {
		hostPath = DefaultConfigPath()
	}
	host, err := LoadFileConfig(hostPath)
	if err != nil {
		return nil, err
	}
	set := &HostSet{HostPath: hostPath, Dir: filepath.Dir(hostPath), Host: host}
	files, err := cellcfg.Discover(set.Dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		c := CellConfig{Profile: f.Profile, Path: f.Path}
		switch {
		case f.NameErr != nil:
			c.Invalid = fmt.Sprintf("%s: %v", f.Path, f.NameErr)
		default:
			cfg, err := LoadCellFile(host, f.Path)
			if err != nil {
				c.Invalid = err.Error()
			} else {
				c.Cfg = cfg
				c.Invalid = cellCompleteness(cfg, f.Path)
			}
		}
		set.Cells = append(set.Cells, c)
	}
	set.applyConflicts()
	return set, nil
}

// LoadCellFile reads one named Cell file over the Host file's Host keys:
// the Cell inherits the Host-only values (socket, [daemon] Host knobs) and
// takes every Cell key from its own file only — nothing of the default
// Cell's connection, workspaces or execution settings leaks into it.
func LoadCellFile(host Config, path string) (Config, error) {
	cfg := Config{
		Server:       "",
		DaemonSocket: host.DaemonSocket,
		Mapping:      &mapping.Plan{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if err := applyConfigBytes(&cfg, path, data); err != nil {
		return cfg, err
	}
	cfg.DaemonSocket = host.DaemonSocket
	cfg.Daemon.MaxConcurrentJobs = host.Daemon.MaxConcurrentJobs
	cfg.Daemon.MaxConcurrentBars = host.Daemon.MaxConcurrentBars
	cfg.Daemon.BarLoadThreshold = host.Daemon.BarLoadThreshold
	cfg.Daemon.HarnessRoot = host.Daemon.HarnessRoot
	cfg.Daemon.OperatorAgentDir = host.Daemon.OperatorAgentDir
	return cfg, nil
}

// cellCompleteness answers why a parsed named Cell cannot load ("" = it
// can): its connection must be complete, and a mapped workspace must be
// usable. An empty mapping is valid (the Cell serves the data plane and its
// execution plane is disabled until a mapping exists).
func cellCompleteness(cfg Config, path string) string {
	if strings.TrimSpace(cfg.Server) == "" {
		return fmt.Sprintf("config %s: server is not set (a Cell's connection needs server and api_key)", path)
	}
	if _, err := cellcfg.NormalizeServer(cfg.Server); err != nil {
		return fmt.Sprintf("config %s: %v", path, err)
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return fmt.Sprintf("config %s: api_key is not set (a Cell's connection needs server and api_key)", path)
	}
	if cfg.Mapping != nil && !cfg.Mapping.Empty() {
		if err := ValidateMappingForDaemon(cfg); err != nil {
			return fmt.Sprintf("config %s: %v", path, err)
		}
	}
	return ""
}

// applyConflicts marks every named Cell that conflicts with the default
// Cell or with another named Cell invalid (cellcfg.Conflicts: load-order
// independent, every party of a named/named conflict).
func (s *HostSet) applyConflicts() {
	def := &cellcfg.Party{Profile: cellcfg.DefaultProfile, Server: s.Host.Server, Workspaces: s.Host.Mapping.Workspaces()}
	var named []cellcfg.Party
	for _, c := range s.Cells {
		if c.Invalid != "" {
			continue
		}
		named = append(named, cellcfg.Party{Profile: c.Profile, Server: c.Cfg.Server, Workspaces: c.Cfg.Mapping.Workspaces()})
	}
	bad := cellcfg.Conflicts(def, named)
	for i := range s.Cells {
		if why, ok := bad[s.Cells[i].Profile]; ok {
			s.Cells[i].Invalid = why
		}
	}
}

// Cell answers a profile's config from the set: the Host file's for
// default, the named file's otherwise. ok=false: no such Cell file;
// invalid carries a configured Cell's refusal reason.
func (s *HostSet) Cell(profile string) (cfg Config, invalid string, ok bool) {
	if profile == cellcfg.DefaultProfile {
		return s.Host, "", true
	}
	for _, c := range s.Cells {
		if c.Profile == profile {
			return c.Cfg, c.Invalid, true
		}
	}
	return Config{}, "", false
}

// CellPath answers the file a profile's keys live in.
func (s *HostSet) CellPath(profile string) string {
	return filepath.Join(s.Dir, cellcfg.CellFileName(profile))
}

// DiskIndex is the workspace index built from the files on disk (the local
// verbs' fallback when no Host answers; business never routes from it).
func (s *HostSet) DiskIndex() []cellcfg.IndexEntry {
	var out []cellcfg.IndexEntry
	for _, ws := range s.Host.Mapping.Workspaces() {
		out = append(out, cellcfg.IndexEntry{Profile: cellcfg.DefaultProfile, Workspace: ws})
	}
	for _, c := range s.Cells {
		if c.Invalid != "" {
			continue
		}
		for _, ws := range c.Cfg.Mapping.Workspaces() {
			out = append(out, cellcfg.IndexEntry{Profile: c.Profile, Workspace: ws})
		}
	}
	return out
}
