package summarize

// dispatch.go: the one-shot harness dispatch seam (polyforge#224). One
// summarize call is ONE one-shot harness dispatch: `pi -p --model <m>`
// / `claude -p <ctx> --model <m>`, the prompt on STDIN, never argv
// (polyforge#117: a transcript tail routinely exceeds the kernel's
// per-argument limit — caught live, "argument list too long"). The
// binary is the daemon's configured harness binary (--pi-bin /
// --claude-bin) — the same source the step spawn uses. The claude
// context flag follows the 2026-10-19 owner ruling: --bare in token
// auth mode, --safe-mode in OAuth mode, picked from the harness
// config's settings.json env block exactly the way the step spawn
// picks it (runtime.ReadClaudeSettingsEnv + runtime.ClaudeTokenMode —
// the same functions, no second implementation; the official URL is
// never contacted, never tested). One call is bounded by the 5-minute
// timeout (the old bound): a hung call times out into summary_error
// like any other failure; nothing waits on it. No credential value
// ever appears in the dispatch argv, the daemon's logs or any artifact:
// the settings env block is the single authority layer, applied by the
// harness itself (the dispatch hard-sets CLAUDE_CONFIG_DIR there like
// the step spawn does).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// Dispatcher is the one-shot dispatch seam: one call, one candidate, one
// prompt. The daemon binds the exec dispatcher in production; the tests
// bind a fake (the lane is table-testable by construction).
type Dispatcher interface {
	// Dispatch runs one summarize request on the candidate (a canonical
	// `<harness>:<model>` string) with the prompt on stdin and answers
	// the model's output text. A nonzero exit or a timeout is an error
	// whose text carries the harness's RAW stderr/exit wording — the
	// caller surfaces it verbatim in summary_error, never faked.
	Dispatch(candidate, prompt string) (string, error)
}

// ContextDispatcher is the optional context-aware dispatch seam: a caller
// that owns a single wall-clock deadline passes it down so the whole run
// (every candidate attempt) shares one budget and a cancel kills the
// in-flight harness process. A plain Dispatcher stays valid for callers
// that do not need it.
type ContextDispatcher interface {
	DispatchContext(ctx context.Context, candidate, prompt string) (string, error)
}

// dispatchTimeout bounds one summarize dispatch (the old bound): a hung
// model call must not occupy the lane forever — it times out into
// summary_error like any other failure.
const dispatchTimeout = 5 * time.Minute

// ExecDispatcher dispatches through the real harness binaries (the
// daemon's configured --pi-bin / claude_bin). ClaudeConfigDir is the
// harness root's claude-config dir: the settings.json env block the
// claude context flag is picked from — and the authority layer a real
// claude applies (the dispatch hard-sets CLAUDE_CONFIG_DIR there like
// the step spawn does; "" = no harness root, the operator's own
// config).
type ExecDispatcher struct {
	PiBin string // "" = "pi"
	// ClaudeBin answers the claude binary AT DISPATCH TIME ("" = the
	// built-in default). The daemon binds its live state's ClaudeBin, so
	// a reloaded cli.toml claude_bin reaches the lane without a restart
	// — one fact, one place, never a construction-time copy
	// ( F2).
	ClaudeBin       func() string
	ClaudeConfigDir string
	// Dir is the dispatch's working directory: the
	// daemon binds <harness-root>/summarize-cwd (created 0700 on use).
	// Never inherited: the daemon runs at $HOME, whose .claude/
	// settings.json Claude Code would read as the project settings of an
	// untrusted workspace. "" (a bare dispatcher: tests) = a private
	// temp dir per dispatch, removed afterwards.
	Dir     string
	Timeout time.Duration // <= 0 = dispatchTimeout
}

func (d *ExecDispatcher) piBin() string {
	if d.PiBin != "" {
		return d.PiBin
	}
	return harness.Pi
}

func (d *ExecDispatcher) claudeBin() string {
	if d.ClaudeBin != nil {
		if b := d.ClaudeBin(); b != "" {
			return b
		}
	}
	return harness.Claude
}

// claudeContextFlag answers the claude candidate's context flag per the
// 2026-10-19 ruling: --bare in token auth mode (a non-empty
// ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY in the settings env
// block), --safe-mode in OAuth mode (no token key, or no settings file).
// An unreadable/malformed settings.json is an error naming the file and
// never carrying a value from it.
func claudeContextFlag(ccDir string) (string, error) {
	env, err := runtime.ReadClaudeSettingsEnv(ccDir)
	if err != nil {
		return "", err
	}
	if runtime.ClaudeTokenMode(env) {
		return "--bare", nil
	}
	return "--safe-mode", nil
}

// dispatchArgs answers the one-shot argv for one parsed candidate:
// print mode first, then the claude context flag (per the auth-mode
// ruling), then exactly the candidate's model id. The PROMPT rides
// stdin, never argv. Exposed for the tests — the argv shape is the
// contract (the acceptance suite's persona discriminates on it).
func dispatchArgs(c harness.Candidate, claudeCtx string) []string {
	if harness.Of(c.Harness) == harness.Claude {
		return []string{"-p", claudeCtx, "--model", c.Model}
	}
	return []string{"-p", "--model", c.Model}
}

// Dispatch runs one one-shot harness dispatch: the configured binary,
// the spec'd argv, the prompt on stdin, bounded by the timeout. A
// nonzero exit or a timeout answers an error whose text carries the
// harness's RAW stderr (the exit wording beside it) — never faked,
// never collapsed to "no new output".
func (d *ExecDispatcher) Dispatch(candidate, prompt string) (string, error) {
	return d.DispatchContext(context.Background(), candidate, prompt)
}

// DispatchContext is Dispatch with a caller-owned deadline: the timeout is
// the smaller of d.Timeout and the caller's remaining budget, so a run
// that walks several candidates still ends inside one wall-clock cap.
func (d *ExecDispatcher) DispatchContext(parent context.Context, candidate, prompt string) (string, error) {
	c, err := harness.ParseCandidate(candidate)
	if err != nil {
		return "", err
	}
	claude := harness.Of(c.Harness) == harness.Claude
	bin := d.piBin()
	args := dispatchArgs(c, "")
	if claude {
		bin = d.claudeBin()
		ctx, cerr := claudeContextFlag(d.ClaudeConfigDir)
		if cerr != nil {
			return "", cerr
		}
		args = dispatchArgs(c, ctx)
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = dispatchTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	dir, release, err := d.workDir()
	if err != nil {
		return "", fmt.Errorf("%s -p dispatch: working directory: %w", bin, err)
	}
	defer release()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	// The dispatch is a DAEMON-side one-shot, never a step process: the
	// inherited daemon env is stripped of every step-identity and
	// harness-context marker (the same posture as the step spawn's claude
	// strip — a daemon started from inside a session inherits markers its
	// allow-list env would never carry, and none of them may shape the
	// dispatch): the PF_* step markers, the step agent's PI_CODING_AGENT_*
	// context (a real pi would otherwise use the step's agent dir and
	// write its session into the step's store), and the claude markers —
	// CLAUDE_CONFIG_DIR is hard-set below when a harness root exists, and
	// the settings.json env block stays the only credential/endpoint
	// source (the 2026-10-19 ruling). No credential value ever rides the
	// env, the argv or any log.
	cmd.Env = dispatchEnv(os.Environ())
	if claude && d.ClaudeConfigDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+d.ClaudeConfigDir)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errb.String())
		if detail == "" {
			detail = strings.TrimSpace(out.String())
		}
		if detail != "" {
			return "", fmt.Errorf("%s -p dispatch failed: %v: %s", bin, err, detail)
		}
		return "", fmt.Errorf("%s -p dispatch failed: %v", bin, err)
	}
	return out.String(), nil
}

// workDir answers the dispatch's working directory (see Dir) and its
// release: the configured dir, created 0700 when missing, or — with no
// Dir — a fresh private temp dir the release removes.
func (d *ExecDispatcher) workDir() (string, func(), error) {
	if d.Dir != "" {
		return d.Dir, func() {}, os.MkdirAll(d.Dir, 0o700)
	}
	tmp, err := os.MkdirTemp("", "pf-summarize-")
	if err != nil {
		return "", nil, err
	}
	return tmp, func() { _ = os.RemoveAll(tmp) }, nil
}

// dispatchEnv answers the inherited environment minus every step-identity
// and harness-context marker: the PF_* step markers (PF_WI_ID,
// PF_DAEMON_ID, PF_DAEMON_SOCKET), the step agent's PI_CODING_AGENT_*
// context, and the claude markers (CLAUDECODE, CLAUDE_CODE_*,
// CLAUDE_CONFIG_DIR, every inherited ANTHROPIC_*). The dispatch carries
// no step identity — the acceptance persona discriminates on PF_WI_ID —
// and the harness's own authority layers (the claude-config settings
// env block, the operator's pi agent dir) govern, never an inherited
// session's.
func dispatchEnv(inherited []string) []string {
	out := make([]string, 0, len(inherited))
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "PF_WI_ID" || name == "PF_DAEMON_ID" || name == "PF_DAEMON_SOCKET",
			name == "PI_CODING_AGENT_DIR" || name == "PI_CODING_AGENT_SESSION_DIR",
			name == "CLAUDECODE" || name == "CLAUDE_CONFIG_DIR",
			strings.HasPrefix(name, "CLAUDE_CODE_"), strings.HasPrefix(name, "ANTHROPIC_"):
			continue
		}
		out = append(out, kv)
	}
	return out
}
