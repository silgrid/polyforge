package supervisor

// env.go: the daemon's environment allow-list (env hygiene). `pf daemon
// ensure` launches the daemon with EXACTLY these variables, taken from
// the ensure caller's environment — never the tmux server's (a tmux
// server keeps the environment of whichever shell first started it, e.g.
// a human's session carrying a third-party ANTHROPIC_BASE_URL/TOKEN or a
// stale API key, and every daemon it would host — and every step agent
// that daemon spawns — would inherit them). `pf doctor` reads the same
// list to flag a running daemon whose environment carries anything else
// that looks like a credential.
//
// Adding a variable is one line in daemonEnvNames (or daemonEnvPrefixes
// for a family). Never add a credential: step harnesses take theirs from
// their own files (claude: <harness-root>/claude-config/settings.json,
// pi: the step agent dir's auth.json), aihub's key is in cli.toml.

import (
	"regexp"
	"sort"
	"strings"
)

// daemonEnvNames are the variables a daemon started by ensure carries
// (when the caller has them set).
var daemonEnvNames = []string{
	// process basics
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TZ", "TMPDIR", "LANG",
	// the cwd, exported by the loop's /bin/sh itself
	"PWD",
	// network: proxies (both spellings; curl/git/node read either) and
	// the CA bundle overrides
	"http_proxy", "https_proxy", "no_proxy", "all_proxy",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
	// XDG base dirs: where cli.toml (XDG_CONFIG_HOME, read by the steps'
	// own `pf`), the watcher state (XDG_RUNTIME_DIR) and the harnesses'
	// files are looked up — dropping one would silently move them
	"XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
	// polyforge: the data dir default the daemon still reads (main.go;
	// ensure passes -data-dir as well) — the retired
	// POLYFORGE_HARNESS_ROOT / POLYFORGE_OPERATOR_AGENT_DIR are
	// deliberately NOT carried — and the supervision marker ensure sets
	// itself (step agents get no POLYFORGE_* / PF_* of the daemon's
	// either way: runtime/spawn.go strips them)
	"POLYFORGE_DATA_DIR", MarkerEnv,
	// Go toolchain: a Go step keeps its module cache, build cache,
	// install target, proxy/private-module settings and toolchain pin
	"GOPATH", "GOROOT", "GOPROXY", "GOPRIVATE", "GONOSUMDB", "GONOPROXY",
	"GOFLAGS", "GOTOOLCHAIN", "GO111MODULE", "GOCACHE", "GOMODCACHE",
	// Go coverage output dir of an instrumented test build (inert
	// otherwise)
	"GOCOVERDIR",
}

// daemonEnvPrefixes are the variable families the daemon carries.
var daemonEnvPrefixes = []string{"LC_"}

// envNameRe is a portable shell variable name. Anything else is never
// carried: ensure writes each entry as a `NAME='value'` assignment into
// the loop script, and a crafted name (e.g. `LC_$(cmd)`) would run.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DaemonEnvAllowed reports whether a daemon started by ensure may carry
// the variable name (an allow-listed, shell-safe name).
func DaemonEnvAllowed(name string) bool {
	if !envNameRe.MatchString(name) {
		return false
	}
	for _, n := range daemonEnvNames {
		if n == name {
			return true
		}
	}
	for _, p := range daemonEnvPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// DaemonEnv filters environ (KEY=VALUE entries) down to the allow-list,
// sorted by name. Empty values are kept (a set-but-empty variable stays
// set); entries without "=", repeated names (the first wins) and
// MarkerEnv (the caller sets it explicitly) are dropped.
func DaemonEnv(environ []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" || seen[name] || name == MarkerEnv || !DaemonEnvAllowed(name) {
			continue
		}
		seen[name] = true
		out = append(out, kv)
	}
	sort.Strings(out)
	return out
}

// EnvNames answers the sorted, deduplicated variable names of environ
// (KEY=VALUE entries) — names only, never a value.
func EnvNames(environ []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// SuspectDaemonEnv answers the names (never values) in names that a
// clean daemon environment must not carry: every ANTHROPIC_*, and every
// other non-allow-listed name that looks like a credential (ending in
// TOKEN, KEY or SECRET, or containing PASSWORD/PASSWD; case-insensitive). Sorted, deduplicated.
func SuspectDaemonEnv(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] || DaemonEnvAllowed(n) {
			continue
		}
		up := strings.ToUpper(n)
		if strings.HasPrefix(up, "ANTHROPIC_") || credentialLooking(up) {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func credentialLooking(up string) bool {
	for _, s := range []string{"TOKEN", "KEY", "SECRET"} {
		if strings.HasSuffix(up, s) {
			return true
		}
	}
	return strings.Contains(up, "PASSWORD") || strings.Contains(up, "PASSWD")
}
