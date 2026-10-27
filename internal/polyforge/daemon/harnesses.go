package daemon

// harnesses.go: the daemon's view of the step harnesses, reported on
// GET /status?harnesses=1 so `pf doctor` — which runs CLI-side and cannot
// see the daemon's environment — judges what the daemon will actually
// run: whether each binary resolves, claude's version (cached per binary
// path + mtime), whether the credential variables are SET, the three
// harness dirs' existence, and the claude-config settings.json env
// block's key facts with its pairing warnings. Values are read in-memory
// only and never reported; no login is attempted, no model is called.

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// minClaudeVersion is the oldest Claude Code version the adapter's
// argv/stream contract was verified against. The CLI auto-updates, so a
// newer version passes; an older or unparsable one is a doctor warning,
// never a dispatch refusal (decision 2026-10-16 G15).
const minClaudeVersion = "2.1.296"

// claudeVersionAtLeast reports whether the dotted numeric version v is
// >= min. An unparsable v is false.
func claudeVersionAtLeast(v, min string) bool {
	a, okA := parseDotted(v)
	b, okB := parseDotted(min)
	if !okA || !okB {
		return false
	}
	for len(a) < len(b) {
		a = append(a, 0)
	}
	for len(b) < len(a) {
		b = append(b, 0)
	}
	return slices.Compare(a, b) >= 0
}

// parseDotted parses "1.2.3" into its numeric parts; anything else fails.
func parseDotted(v string) ([]int, bool) {
	var out []int
	for _, p := range strings.Split(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// The /status harness entry is the CLI's wire shape.
type (
	harnessStatus    = cli.HarnessStatus
	harnessDirStatus = cli.HarnessDir
)

// settingsEnvKeys are the env block keys the doctor reports facts about.
var settingsEnvKeys = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "CLAUDE_CONFIG_DIR"}

// settingsTokenPrefix is the OAuth setup-token prefix Claude Code mints
// (`claude setup-token`).
const settingsTokenPrefix = "sk-ant-oat01-"

// versionCache memoizes `claude --version` per binary path + mtime.
type versionCache struct {
	mu    sync.Mutex
	key   string
	ver   string
	error string
}

func setOrUnset(v string) string {
	if v != "" {
		return "set"
	}
	return "unset"
}

// harnessStatuses answers the /status harnesses section.
func (d *Daemon) harnessStatuses() map[string]harnessStatus {
	pi := harnessStatus{Bin: cmp.Or(d.cfg.PiBin, "pi")}
	if p, err := exec.LookPath(pi.Bin); err == nil {
		pi.Found, pi.Path = true, p
	}
	cl := harnessStatus{
		// the live value, never the startup copy: a reloaded claude_bin is
		// what the daemon spawns with and what doctor/ops status judge
		// (workbench#41 F2)
		Bin:             cmp.Or(d.live.ClaudeBin(), "claude"),
		AuthTokenEnv:    setOrUnset(os.Getenv("ANTHROPIC_AUTH_TOKEN")),
		APIKeyEnv:       setOrUnset(os.Getenv("ANTHROPIC_API_KEY")),
		BaseURLEnv:      setOrUnset(os.Getenv("ANTHROPIC_BASE_URL")),
		BackgroundTasks: "disabled by the adapter (CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1, --tools allowlist)",
	}
	cl.SettingsEnv, cl.HarnessDirs, cl.SettingsWarning = d.claudeConfigFacts()
	if p, err := exec.LookPath(cl.Bin); err == nil {
		cl.Found, cl.Path = true, p
		cl.Version, cl.VersionError = d.claudeVersion(p)
		cl.MinVersion = minClaudeVersion
		cl.Verified = claudeVersionAtLeast(cl.Version, minClaudeVersion)
	}
	return map[string]harnessStatus{"pi": pi, "claude": cl}
}

// claudeConfigFacts reads the claude-config settings.json in-memory
// through runtime.ReadClaudeSettingsEnv, the claude spawner's own read
// (polyforge#223). It never carries a value. Absent: all-unset facts, no
// warning (nothing configured is exactly the doctor's "carries nothing"
// verdict, and the in-repo fake-cfg tests below pin the un-configured
// default shape). Unusable: no facts, the spawner's refusal as the
// warning.
func (d *Daemon) claudeConfigFacts() (map[string]string, map[string]harnessDirStatus, string) {
	root := d.cfg.HarnessRoot
	ccDir := filepath.Join(root, "claude-config")
	dirs := map[string]harnessDirStatus{
		"step_agent":          {Path: filepath.Join(root, "step-agent")},
		"step_agent_sessions": {Path: filepath.Join(root, "step-agent-sessions")},
		"claude_config":       {Path: ccDir},
	}
	for k, s := range dirs {
		st, err := os.Stat(s.Path)
		dirs[k] = harnessDirStatus{Path: s.Path, Exists: err == nil && st.IsDir()}
	}
	env, err := runtime.ReadClaudeSettingsEnv(ccDir)
	if err != nil {
		return nil, dirs, err.Error() + " — claude steps are refused until it is fixed"
	}
	if env == nil {
		return nil, dirs, ""
	}
	facts := map[string]string{}
	for _, k := range settingsEnvKeys {
		facts[k] = setOrUnset(env[k])
	}
	var warns []string
	token, baseUrl := env["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_BASE_URL"]
	if strings.HasPrefix(token, settingsTokenPrefix) && baseUrl != "" {
		warns = append(warns, "OAuth token (sk-ant-oat01-, a `claude setup-token` mint) paired with a gateway BASE_URL — that combination is rejected by Claude Code: use either the official API with no BASE_URL, or a gateway token; this is the doctor's pairing clause")
	}
	if env["CLAUDE_CONFIG_DIR"] != "" {
		warns = append(warns, "the claude-config settings.json env block sets CLAUDE_CONFIG_DIR: remove it — the adapter hard-sets the step's config dir and the settings value overrides the adapter's")
	}
	return facts, dirs, strings.Join(warns, "; ")
}

// modelsEnvRefRe matches pi's models.json environment interpolation:
// `$NAME` or `${NAME}`.
var modelsEnvRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// piModelsEnvRefs (env hygiene) answers the variable NAMES the step agent
// dir's models.json references through `$NAME` / `${NAME}` interpolation
// in an apiKey or header value — pi resolves them from the step's
// environment, which an ensure-started daemon limits to the allow-list.
// Sorted, deduplicated; never a value (a literal key or a `!command`
// value contributes nothing). Absent or unparsable file: none.
func (d *Daemon) piModelsEnvRefs() []string {
	raw, err := os.ReadFile(filepath.Join(d.cfg.HarnessRoot, "step-agent", "models.json"))
	if err != nil {
		return nil
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(v any, key string, inHeaders bool)
	walk = func(v any, key string, inHeaders bool) {
		switch t := v.(type) {
		case map[string]any:
			for k, c := range t {
				walk(c, k, inHeaders || k == "headers")
			}
		case []any:
			for _, c := range t {
				walk(c, key, inHeaders)
			}
		case string:
			if (key != "apiKey" && !inHeaders) || strings.HasPrefix(t, "!") {
				return
			}
			for _, m := range modelsEnvRefRe.FindAllStringSubmatch(t, -1) {
				seen[m[1]+m[2]] = true
			}
		}
	}
	walk(doc, "", false)
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// claudeVersion runs `<path> --version` (bounded), cached per
// path+mtime. The probe runs outside the cache lock.
func (d *Daemon) claudeVersion(path string) (string, string) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err.Error()
	}
	key := path + "@" + st.ModTime().UTC().Format(time.RFC3339Nano)
	c := &d.claudeVer
	c.mu.Lock()
	if c.key == key {
		defer c.mu.Unlock()
		return c.ver, c.error
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var ver, verr string
	if out, err := exec.CommandContext(ctx, path, "--version").Output(); err != nil {
		verr = "claude --version: " + err.Error()
	} else if f := strings.Fields(string(out)); len(f) > 0 {
		ver = f[0]
	}
	c.mu.Lock()
	c.key, c.ver, c.error = key, ver, verr
	c.mu.Unlock()
	return ver, verr
}
