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
//	  submit, answer, pause, resume, wrap, ops, watch, chat, daemon.
//
// Output: human-readable tables by default; --json emits the server's
// JSON response as-is. Errors are decoded from the typed errStatus
// envelope (common/api.Error: {code, detail, extra}) and rendered with
// the code prominent; exit codes follow the wire status class:
// 2xx → 0, 4xx → 1, 5xx → 2 (usage errors also 2).
//
// Configuration precedence: command-line flags > PF_* environment >
// ~/.config/polyforge/cli.toml (flat string keys, [workspace."<path>"]
// sections and the [daemon] table) > defaults. There is NO default project (polyforge#113): the
// [workspace] mapping decides capsule placement, and project scoping is
// per-invocation --project.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// Default aihub listen address.
const defaultServer = "http://localhost:8420"

// DefaultMaxConcurrentJobs is the daemon's documented default job cap
// when [daemon] max_concurrent_jobs is unset (also what unsetting the key
// restores on a reload).
const DefaultMaxConcurrentJobs = 4

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
	ChatID       string        // PF_CHAT_ID: the harness chat this process runs in (label, not a credential)
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
	// HarnessRoot is the parent of the step harness dirs (absolute path;
	// daemon default <data-dir>); flag --harness-root.
	HarnessRoot string
	// OperatorAgentDir is the source of the step agent dir's credential
	// and model files (absolute path; daemon default ~/.pi/agent; a set
	// value without a readable auth.json refuses the daemon start); flag
	// --operator-agent-dir.
	OperatorAgentDir string
	// MaxConcurrentJobs is the configured job cap (>= 1; 0 = unset,
	// daemon default 4); flag --max-concurrent-jobs. `pf ops max N`
	// overrides it, and a reload that changes max_concurrent_jobs overrides
	// `pf ops max` (last action wins).
	MaxConcurrentJobs int
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
		case "max_concurrent_jobs":
			n, err := strconv.Atoi(kv.val)
			if err != nil {
				return s, refuse(fmt.Sprintf("must be an unquoted integer >= 1, got %s", kv.val))
			}
			if n < 1 {
				return s, refuse("must be >= 1")
			}
			s.MaxConcurrentJobs = n
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
// not "no file".
//
//	PF_SERVER         -> Server
//	PF_API_KEY        -> APIKey
//	PF_DAEMON_SOCKET  -> DaemonSocket
//	PF_USER_ID        -> UserID
//	PF_CHAT_ID        -> ChatID
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
	if v := os.Getenv("PF_SERVER"); v != "" {
		cfg.Server = v
	}
	if v := os.Getenv("PF_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("PF_DAEMON_SOCKET"); v != "" {
		cfg.DaemonSocket = v
	}
	if v := os.Getenv("PF_USER_ID"); v != "" {
		cfg.UserID = v
	}
	if v := os.Getenv("PF_CHAT_ID"); v != "" {
		cfg.ChatID = v
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

// parseConfigTOML parses the TOML subset documented for cli.toml:
//
//   - `key = "value"` flat string keys: the known set (server, api_key,
//     daemon_socket, user_id, claude_bin) is stored; the known keys are root-level
//     wherever they appear; any other top-level key is an error — a
//     residual anthropic_auth_token from a pre-#171 deployment hits the
//     unknown-key refusal (the credentials now live in
//     <harness-root>/claude-config/settings.json, ruling 1-2);
//   - one section family: `[workspace."<path>"]` (quoted key mandatory)
//     — inside, only `projects = ["a", "b"]`;
//   - one `[daemon]` table (at most once): its raw `key = value` lines
//     are answered as-is and validated by parseDaemonTable. Every key
//     after the header belongs to the table (TOML semantics), so the
//     flat keys must come before it.
//
// Other tables, unquoted section keys, non-array or wrongly-typed
// projects values, a bare `projects` key at the top level, and duplicate
// keys/sections are line-named parse errors, never guesses. Deliberate
// narrowing: escape sequences are not supported — a double-quoted value
// containing a backslash is rejected — and values are taken literally
// between their quotes. A `#` inside a quoted value is data, not a
// comment.
func parseConfigTOML(file, text string) (map[string]string, []workspaceSection, []daemonKV, error) {
	flat := map[string]string{}
	var sections []workspaceSection
	var daemon []daemonKV
	var (
		inWS       bool
		inDaemon   bool
		daemonSeen bool
		curWS      = -1 // index into sections while inside a [workspace] section
	)
	for ln, line := range strings.Split(text, "\n") {
		line = stripTOMLComment(line)
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if strings.HasSuffix(line, "]") && strings.TrimSpace(line[1:len(line)-1]) == "daemon" {
				if daemonSeen {
					return nil, nil, nil, fmt.Errorf("line %d: duplicate [daemon] table", ln+1)
				}
				daemonSeen, inDaemon, inWS, curWS = true, true, false, -1
				continue
			}
			key, err := parseSectionHeader(line, ln+1)
			if err != nil {
				return nil, nil, nil, err
			}
			for _, s := range sections {
				if s.key == key {
					return nil, nil, nil, fmt.Errorf("line %d: duplicate [workspace] section for %s", ln+1, key)
				}
			}
			sections = append(sections, workspaceSection{key: key, line: ln + 1})
			curWS = len(sections) - 1
			inWS, inDaemon = true, false
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, nil, nil, fmt.Errorf("line %d: expected `key = \"value\"`, got %q", ln+1, redactConfigLine(line))
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "" {
			return nil, nil, nil, fmt.Errorf("line %d: empty key", ln+1)
		}
		if inDaemon {
			daemon = append(daemon, daemonKV{key: key, val: val, line: ln + 1})
			continue
		}
		if coreFlatKeys[key] {
			// Known flat key: root-level wherever it appears.
			inner, err := parseQuotedValue(val, ln+1)
			if err != nil {
				if key == "api_key" {
					return nil, nil, nil, fmt.Errorf("line %d: api_key value must be a quoted string (value redacted)", ln+1)
				}
				return nil, nil, nil, err
			}
			if _, dup := flat[key]; dup {
				return nil, nil, nil, fmt.Errorf("line %d: duplicate key %q", ln+1, key)
			}
			flat[key] = inner
			continue
		}
		if inWS {
			// Inside a workspace section: only the projects array.
			if key != "projects" {
				return nil, nil, nil, fmt.Errorf("line %d: unknown key %q in section [workspace.%q] (a workspace section carries only `projects`)", ln+1, key, sections[curWS].key)
			}
			if len(sections[curWS].projects) > 0 {
				return nil, nil, nil, fmt.Errorf("line %d: duplicate projects array in the [workspace] section of %s", ln+1, sections[curWS].key)
			}
			list, err := parseStringArray(val, ln+1)
			if err != nil {
				return nil, nil, nil, err
			}
			sections[curWS].projects = append(sections[curWS].projects, list...)
			continue
		}
		if key == "projects" {
			return nil, nil, nil, fmt.Errorf("line %d: `projects` belongs inside a [workspace.\"<path>\"] section, not the top level", ln+1)
		}
		return nil, nil, nil, &unknownKeyError{key: key, file: file}
	}
	return flat, sections, daemon, nil
}

// parseSectionHeader validates one `[...]` line: the only legal section
// is `[workspace."<key>"]` (quoted key mandatory) — the mapping plan.
// It answers the workspace key.
func parseSectionHeader(line string, ln int) (string, error) {
	if !strings.HasSuffix(line, "]") {
		return "", fmt.Errorf("line %d: malformed section header %q", ln, line)
	}
	inner := strings.TrimSpace(line[1 : len(line)-1])
	const wsPrefix = `workspace.`
	if !strings.HasPrefix(inner, wsPrefix) {
		return "", fmt.Errorf("line %d: unknown section %q — only [workspace.\"<path>\"] sections and the [daemon] table are supported", ln, line)
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

// parseStringArray parses `["a", "b"]` — an array of quoted strings
// only (the one value shape a workspace section may carry).
func parseStringArray(val string, ln int) ([]string, error) {
	if len(val) < 2 || val[0] != '[' || val[len(val)-1] != ']' {
		return nil, fmt.Errorf("line %d: projects must be an array of quoted strings: projects = [\"a\", \"b\"], got %q", ln, val)
	}
	body := strings.TrimSpace(val[1 : len(val)-1])
	if body == "" {
		return []string{}, nil
	}
	var out []string
	for _, item := range strings.Split(body, ",") {
		item = strings.TrimSpace(item)
		s, err := parseQuotedValue(item, ln)
		if err != nil {
			return nil, fmt.Errorf("line %d: projects entries must be quoted strings: %w", ln, err)
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
