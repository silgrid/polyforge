package runtime

// claudespawn.go: the step spawner for the Claude Code harness
// (polyforge#159). One step = one headless `claude -p` process:
//
//	<claude_bin> -p --bare | --safe-mode
//	  --output-format stream-json --verbose --include-partial-messages
//	  --session-id <uuid> | --resume <uuid>
//	  [--model <claude id>]
//	  --tools <ClaudeStepTools> --allowedTools <ClaudeStepTools>
//	  --permission-mode dontAsk --permission-prompts none
//	  --strict-mcp-config --disable-slash-commands
//
// cwd = the capsule root, own process group (as pi); stdin = the
// assembled step prompt (the SAME text pi gets), closed once written.
//
// The step's Claude Code context is daemon-owned: <harness-root>/
// claude-config is the config dir (its settings.json env block is the
// single credential authority layer, polyforge#159 closeout #171), so
// the daemon hard-sets CLAUDE_CONFIG_DIR there, and switches off
// nonessential traffic (CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1).
// It also sets the Bash tool's per-call timeouts (BASH_DEFAULT_TIMEOUT_MS,
// BASH_MAX_TIMEOUT_MS) from the supervision tool window, each only when
// that settings.json env block does not carry the key itself
// (claudeBashTimeoutEnv).
// The daemon never injects a credential: none ride the environment or
// argv — the harness's own settings.json provides them.
//
// Context flag by auth mode (polyforge#223), decided at every spawn
// (fresh and --resume) from that settings.json env block:
//   - token mode — a non-empty ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY:
//     --bare only. --bare skips hooks, plugins, LSP, auto-memory,
//     CLAUDE.md discovery and the keychain.
//   - OAuth mode — no non-empty token key, a missing settings.json or no
//     harness root: --safe-mode only. --bare would skip the keychain the
//     OAuth login lives in; --safe-mode starts with every customization
//     off (CLAUDE.md, skills, plugins, hooks, MCP servers, custom
//     commands and agents, output styles, workflows, …).
// A settings.json that exists but is unreadable, is not JSON or has no
// `env` string map refuses the spawn. Either flag skips hooks.
//
// Clean step context (a functional requirement, not isolation): the
// mode's flag above; --strict-mcp-config with no --mcp-config loads no
// MCP server; --disable-slash-commands keeps the human's skills out. The
// --tools allowlist is the whole tool surface: no background, scheduling
// or messaging tool exists in the step (background tasks are also off
// through CLAUDE_CODE_DISABLE_BACKGROUND_TASKS). No --fallback-model:
// switching models is D7's alone.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// ClaudeStepTools is the step's whole tool surface: pre-approved, and
// nothing else exists. The built-in Agent tool is the fresh-context
// sub-agent (foreground only); WebFetch/WebSearch stay out (pi parity).
const ClaudeStepTools = "Bash,Read,Edit,Write,Glob,Grep,TodoWrite,Agent"

// ClaudeBackgroundTools are tools a step must never have (asserted by
// the argv regression): background shells, monitors, schedulers,
// cross-session messaging.
var ClaudeBackgroundTools = []string{
	"Monitor", "TaskOutput", "TaskStop", "KillShell", "BashOutput",
	"CronCreate", "CronDelete", "CronList", "ScheduleWakeup",
	"RemoteTrigger", "PushNotification", "SendMessage",
}

// ReadClaudeSettingsEnv reads the env block of <ccDir>/settings.json —
// the one read shared by the claude spawner (auth mode) and the daemon's
// doctor facts. ccDir "" (no harness root) or an absent file: nil, nil.
// A file that exists but is unreadable, is not JSON or does not decode
// into an `env` string map is an error naming the file; the error never
// carries a value from the file.
func ReadClaudeSettingsEnv(ccDir string) (map[string]string, error) {
	if ccDir == "" {
		return nil, nil
	}
	path := filepath.Join(ccDir, "settings.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the claude-config settings.json is unreadable (%v)", err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return nil, fmt.Errorf("the claude-config settings.json %s does not parse as JSON (offset %d)", path, syn.Offset)
		}
		return nil, fmt.Errorf("the claude-config settings.json %s does not parse into an `env` string map", path)
	}
	if doc.Env == nil {
		return nil, fmt.Errorf("the claude-config settings.json %s does not parse into an `env` block (no `env` key; `\"env\": {}` makes it tokenless)", path)
	}
	return doc.Env, nil
}

// ClaudeTokenMode reports whether a settings.json env block carries a
// credential: a non-empty ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY.
func ClaudeTokenMode(env map[string]string) bool {
	return env["ANTHROPIC_AUTH_TOKEN"] != "" || env["ANTHROPIC_API_KEY"] != ""
}

// The Bash tool timeout variables of Claude Code: the per-call timeout a
// command gets when the model names none, and the largest one it may
// name. Unset, claude's own defaults are 2m and 10m, so a step's
// `make test` was killed at 2m and the agent fell back to polling a
// backgrounded run.
const (
	envBashDefaultTimeout = "BASH_DEFAULT_TIMEOUT_MS"
	envBashMaxTimeout     = "BASH_MAX_TIMEOUT_MS"
)

const (
	// claudeBashDefaultTimeout is a claude step's Bash default timeout
	// (clamped to the max).
	claudeBashDefaultTimeout = 10 * time.Minute
	// claudeBashToolMargin keeps the Bash max timeout this far under the
	// supervision tool window, so a command claude lets run to its max
	// ends before the supervisor judges the tool hung.
	claudeBashToolMargin = time.Minute
)

// claudeBashTimeouts answers a claude step's Bash default and max timeout
// for the supervision tool window Y (<= 0 resolves to the supervision
// default, 30m, through the same supervise.Defaults.Fill the daemon's
// supervisor uses): max = Y - 1m, default = 10m clamped to max (so a
// window of 11m or less gives default = max). A window of 2m or less
// (test/dev-only) keeps half of itself as the margin instead of 1m, and
// the max never drops below 1ms.
func claudeBashTimeouts(toolWindow time.Duration) (defTimeout, maxTimeout time.Duration) {
	window := supervise.Defaults{Tool: toolWindow}.Fill().Tool
	maxTimeout = max(window-min(claudeBashToolMargin, window/2), time.Millisecond)
	return min(claudeBashDefaultTimeout, maxTimeout), maxTimeout
}

// claudeBashTimeoutEnv answers the Bash timeout variables the adapter
// adds to a claude step's environment: each of the two only when the
// claude-config settings.json env block does not carry that key (claude
// applies that block itself, so the operator's value wins and the
// daemon never writes the file). Claude raises its max to the default
// when the default is larger, so a default injected next to an
// operator's own max is clamped to that max (when it is a positive
// integer) — the operator's max stays the max.
func claudeBashTimeoutEnv(settings map[string]string, toolWindow time.Duration) []string {
	defTimeout, maxTimeout := claudeBashTimeouts(toolWindow)
	var out []string
	if v, ok := settings[envBashMaxTimeout]; ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
			defTimeout = min(defTimeout, time.Duration(n)*time.Millisecond)
		}
	} else {
		out = append(out, envBashMaxTimeout+"="+strconv.FormatInt(maxTimeout.Milliseconds(), 10))
	}
	if _, ok := settings[envBashDefaultTimeout]; !ok {
		out = append(out, envBashDefaultTimeout+"="+strconv.FormatInt(defTimeout.Milliseconds(), 10))
	}
	return out
}

// claudeSpawner derives Claude Code step processes. Like piSpawner it
// reads the shared config by pointer at spawn time.
type claudeSpawner struct {
	cfg *Config
}

func (s *claudeSpawner) bin() string {
	if b := s.cfg.liveClaudeBin(); b != "" {
		return b
	}
	return "claude"
}

// buildCmd constructs (without starting) one derivation's argv/env/cwd.
func (s *claudeSpawner) buildCmd(p SpawnParams) (*exec.Cmd, error) {
	if !harness.ValidSessionID(p.HarnessSession) {
		return nil, fmt.Errorf("runtime: claude spawn without a valid session id (%q)", p.HarnessSession)
	}
	settings, err := ReadClaudeSettingsEnv(s.cfg.ClaudeConfigDir())
	if err != nil {
		return nil, fmt.Errorf("runtime: claude spawn refused: %w", err)
	}
	contextFlag := "--safe-mode"
	if ClaudeTokenMode(settings) {
		contextFlag = "--bare"
	}
	args := []string{"-p", contextFlag,
		"--output-format", "stream-json", "--verbose", "--include-partial-messages"}
	if p.Resume {
		args = append(args, "--resume", p.HarnessSession)
	} else {
		args = append(args, "--session-id", p.HarnessSession)
	}
	if p.Model != "" {
		args = append(args, "--model", p.Model)
	}
	// polyforge#176: the candidate's effort rides --effort
	args = append(args, harness.Candidate{Harness: harness.Claude, Effort: p.Effort}.EffortArgs()...)
	args = append(args,
		"--tools", ClaudeStepTools,
		"--allowedTools", ClaudeStepTools,
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--strict-mcp-config",
		"--disable-slash-commands",
	)
	cmd := exec.Command(s.bin(), args...)
	cmd.Dir = p.CapsuleDir
	additions := []string{
		"PF_WI_ID=" + p.WorkItemID,
		"PF_DAEMON_ID=" + s.cfg.MachineID,
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1",
		"DISABLE_AUTOUPDATER=1",
		"CLAUDE_CODE_STARTUP_FAILURE_RESULTS=1",
	}
	if s.cfg.DaemonSocket != "" {
		additions = append(additions, "PF_DAEMON_SOCKET="+s.cfg.DaemonSocket)
	}
	// polyforge#171: the step's config dir is where its credentials live
	// (claude-config/settings.json's env block — the single authority
	// layer); the adapter hard-sets it and never injects a credential of
	// its own. Only a real root redirects (a bare Config has no claude
	// config dir; the step runs on whatever its own HOME resolves to).
	if ccDir := s.cfg.ClaudeConfigDir(); ccDir != "" {
		additions = append(additions, "CLAUDE_CONFIG_DIR="+ccDir)
	}
	// the "0" value is OFF for everything CLAUDE_CONFIG_DIR-side, so the
	// existence is the semantic (see the WI ruling 5)
	additions = append(additions, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	// the Bash tool's timeouts follow the supervision tool window, unless
	// the settings.json env block carries the key (claudeBashTimeoutEnv)
	additions = append(additions, claudeBashTimeoutEnv(settings, s.cfg.ToolWindow)...)
	// the harness-neutral tool window binding every step carries; the Bash
	// max is one minute lower, so verify-run's min() sees the same bound
	additions = append(additions, toolWindowEnv(s.cfg.ToolWindow))
	// Zero credential injection (polyforge#171, closing out #159's three-layer blind spot):
	// the adapter NEVER injects ANTHROPIC_AUTH_TOKEN or any other
	// credential — the settings.json env block under claude-config is
	// the single authority layer, applied by claude itself.
	// A daemon started from inside a Claude Code session inherits that
	// session's CLAUDECODE / CLAUDE_CODE_* markers and switches; none of
	// them may shape the step (clean context) — only the switches the
	// adapter sets above. CLAUDE_CONFIG_DIR is stripped the same way: it
	// is hard-set by the adapter (an operator env value would hijack the
	// step's config dir and would duplicate the env entry).
	//
	// ANTHROPIC_*: EVERY inherited ANTHROPIC_* is stripped too, so the
	// `env` block of <claude-config>/settings.json is the ONLY source of
	// a claude step's credential, endpoint and model defaults. Whatever
	// the daemon inherited (e.g. the tmux server's ANTHROPIC_BASE_URL /
	// AUTH_TOKEN / MODEL of a human's session) must not reach a step, and
	// a variable in the process env could otherwise override or add to
	// settings.json. Set every ANTHROPIC_* a step needs in settings.json
	// (`pf daemon ensure` hands the daemon no ANTHROPIC_* either: see
	// supervisor/env.go). pi steps strip nothing extra here, but they too
	// see only the daemon's environment — for an ensure-started daemon
	// the allow-list — so a pi provider key must be in the step agent
	// dir's auth.json (copied from the operator agent dir) or a literal
	// in its models.json; a key that lives only in the daemon's
	// environment (a provider *_API_KEY, a models.json `$NAME`) does not
	// reach pi steps.
	//
	// Any inherited copy of a name the adapter sets itself (additions) is
	// dropped as well, so every adapter name appears exactly once with the
	// adapter's value (polyforge#238: an inherited DISABLE_AUTOUPDATER,
	// e.g. from an outer claude step, matched no prefix rule above and
	// was duplicated). The two Bash timeout names are the adapter's even
	// when settings.json carries them and the adapter adds neither: an
	// inherited value (an outer claude step's) never reaches the step, so
	// the step's timeouts come from settings.json or the adapter only.
	own := make(map[string]bool, len(additions)+2)
	for _, kv := range additions {
		name, _, _ := strings.Cut(kv, "=")
		own[name] = true
	}
	own[envBashDefaultTimeout], own[envBashMaxTimeout] = true, true
	inherited := newEnvSanitizer(s.cfg.EnvSecrets...).childEnv()
	env := make([]string, 0, len(inherited)+len(additions))
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if own[name] || name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") || name == "CLAUDE_CONFIG_DIR" ||
			strings.HasPrefix(name, "ANTHROPIC_") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, additions...)
	return cmd, nil
}

// Spawn derives one Claude Code step process: process group, identity
// journal before the prompt, then the prompt on stdin and stdin closed.
func (s *claudeSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	if p.InitialPrompt == "" {
		return nil, fmt.Errorf("runtime: spawn without an initial prompt")
	}
	cmd, err := s.buildCmd(p)
	if err != nil {
		return nil, err
	}
	proc, stdin, err := startHarness(cmd, p, false)
	if err != nil {
		return nil, err
	}
	_, werr := stdin.Write([]byte(p.InitialPrompt))
	cerr := stdin.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, injectionFailed(proc, werr)
	}
	return proc, nil
}

// harnessSpawner is the production spawner: it dispatches each
// derivation to its step harness's spawner (polyforge#159).
type harnessSpawner struct {
	pi     *piSpawner
	claude *claudeSpawner
}

// Spawn implements Spawner.
func (s *harnessSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	switch harness.Of(p.Harness) {
	case harness.Pi:
		return s.pi.Spawn(ctx, p)
	case harness.Claude:
		return s.claude.Spawn(ctx, p)
	}
	return nil, fmt.Errorf("runtime: unknown step harness %q", p.Harness)
}
