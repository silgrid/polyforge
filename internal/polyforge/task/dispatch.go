package task

// dispatch.go: the streamed one-shot harness runner of the task lane
// (workbench#53, carried from #40). One Run is ONE fresh harness process in
// the project's workspace directory: no session id, no resume, no
// `--models`; the prompt rides stdin, the stdout records are read line by
// line into the usage accumulator, and the process is killed the moment the
// token budget is crossed (or the caller's context — wall budget / cancel —
// ends).
//
// There is no OS-level boundary (owner ruling 2026-10-26): the harness runs
// with the daemon user's own filesystem and network trust, exactly like a
// session agent. The ONE capability gate is the write-verb guard — the
// child carries PF_TASK_ID + PF_DAEMON_ID, so the daemon refuses it every
// write at the UDS face and `pf` refuses it every write verb locally.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// RunSpec is one runner call: one candidate, one prompt, the remaining
// token budget of the task and the directory the harness runs in.
type RunSpec struct {
	Candidate harness.Candidate
	Prompt    string
	// MaxTokens is the task's REMAINING token budget for this attempt.
	MaxTokens int64
	WorkDir   string
	TaskID    string
}

// RunResult is what one run observed. TokenCapped is true when the runner
// itself killed the process for crossing MaxTokens.
type RunResult struct {
	Report      string
	Tokens      int64
	TokenCapped bool
}

// Runner is the dispatch seam: the daemon binds ExecRunner in production,
// tests bind a fake. Progress (optional) is called with the running token
// total and the captured text as records arrive.
type Runner interface {
	Run(ctx context.Context, spec RunSpec, progress func(tokens int64)) (RunResult, error)
}

// ExecRunner runs the real harness binaries.
type ExecRunner struct {
	PiBin           string
	ClaudeBin       func() string
	ClaudeConfigDir string
	PiAgentDir      string
	DaemonID        string
	SocketPath      string
}

// resolveBin answers the executable path of a harness binary (workbench#40
// review F2): the configured name or path is resolved through exec.LookPath
// — so a bare `claude` means the one on the daemon's PATH — and then its
// symlink target through filepath.EvalSymlinks, because a native-installer
// installs a versioned binary and links a stable name to it. Execing the
// resolved target keeps the child's own install root in place. An
// unresolvable answer is the configured name unchanged: the start error
// then names it, rather than a wrong path.
func resolveBin(bin string) string {
	if bin == "" {
		return bin
	}
	if p, err := exec.LookPath(bin); err == nil && p != "" {
		bin = p
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil && real != "" {
		return real
	}
	return bin
}

func (r *ExecRunner) bin(h string) string {
	if h == harness.Claude {
		if r.ClaudeBin != nil {
			if b := r.ClaudeBin(); b != "" {
				return resolveBin(b)
			}
		}
		return resolveBin(harness.Claude)
	}
	if r.PiBin != "" {
		return resolveBin(r.PiBin)
	}
	return resolveBin(harness.Pi)
}

// TaskClaudeTools is the task agent's whole claude tool surface: read/
// search, Bash and the web tools. There is no sub-agent tool and no
// scheduling/messaging tool of its own; the tool allowlist is the lane's
// own scope, not a security boundary (that is the write-verb guard).
const TaskClaudeTools = "Read,Glob,Grep,Bash,WebSearch,WebFetch"

// TaskPiTools is the pi equivalent (pi's built-in read/search tools plus
// bash; pi's web surface is an extension the task agent dir does not
// carry).
const TaskPiTools = "read,grep,find,ls,bash"

// innerArgv answers the harness argv (binary first). The claude context
// flag follows the dispatch ruling (--bare in token mode, --safe-mode in
// OAuth mode), the same functions the summarize dispatch uses. The tool
// allowlist replaces --permission-mode bypassPermissions: only the listed
// tools exist (polyforge#40 review B4).
func (r *ExecRunner) innerArgv(c harness.Candidate) ([]string, error) {
	bin := r.bin(harness.Of(c.Harness))
	var args []string
	if harness.Of(c.Harness) == harness.Claude {
		flag := "--safe-mode"
		env, err := runtime.ReadClaudeSettingsEnv(r.ClaudeConfigDir)
		if err != nil {
			return nil, err
		}
		if runtime.ClaudeTokenMode(env) {
			flag = "--bare"
		}
		args = []string{bin, "-p", flag, "--output-format", "stream-json", "--verbose",
			"--no-session-persistence",
			"--tools", TaskClaudeTools, "--allowedTools", TaskClaudeTools,
			"--permission-mode", "dontAsk", "--model", c.Model}
	} else {
		args = []string{bin, "-p", "--mode", "json", "--no-session",
			"--tools", TaskPiTools, "--model", c.Model}
	}
	return append(args, c.EffortArgs()...), nil
}

// taskEnv answers the child env: the inherited env minus every polyforge
// marker, aihub credential and harness-context variable, plus the task's
// own identity and the daemon-owned harness config dirs. No credential
// value is ever added: the harness reads its own from the config dir the
// daemon owns.
func taskEnv(inherited []string, taskID, daemonID, socket, claudeCfg, piAgentDir string) []string {
	out := make([]string, 0, len(inherited)+6)
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "PF_"), strings.HasPrefix(name, "POLYFORGE_"), strings.HasPrefix(name, "AIHUB_"),
			strings.HasPrefix(name, "PI_CODING_AGENT_"), strings.HasPrefix(name, "CLAUDE_CODE_"),
			strings.HasPrefix(name, "ANTHROPIC_"), name == "CLAUDECODE", name == "CLAUDE_CONFIG_DIR":
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "PF_TASK_ID="+taskID, "PF_DAEMON_ID="+daemonID)
	if socket != "" {
		out = append(out, "PF_DAEMON_SOCKET="+socket)
	}
	if claudeCfg != "" {
		out = append(out, "CLAUDE_CONFIG_DIR="+claudeCfg)
	}
	if piAgentDir != "" {
		out = append(out, "PI_CODING_AGENT_DIR="+piAgentDir)
	}
	return out
}

// Run runs one candidate to completion, a token cap, or ctx's end.
func (r *ExecRunner) Run(ctx context.Context, spec RunSpec, progress func(tokens int64)) (RunResult, error) {
	h := harness.Of(spec.Candidate.Harness)
	if !SupportsUsage(h) {
		return RunResult{}, fmt.Errorf("candidate %s cannot report token usage, so a task cannot start on it", spec.Candidate)
	}
	argv, err := r.innerArgv(spec.Candidate)
	if err != nil {
		return RunResult{}, err
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = spec.WorkDir
	cmd.Env = taskEnv(os.Environ(), spec.TaskID, r.DaemonID, r.SocketPath, r.ClaudeConfigDir, r.PiAgentDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = strings.NewReader(spec.Prompt)
	stderr := &boundedBuf{max: 8 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return RunResult{}, fmt.Errorf("start %s: %w", argv[0], err)
	}
	parser := NewStreamParser(h)
	capped := false
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			parser.Feed(line)
			if progress != nil {
				progress(parser.Tokens())
			}
			if !capped && spec.MaxTokens > 0 && parser.Tokens() >= spec.MaxTokens {
				capped = true
				cancel()
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) && !capped && ctx.Err() == nil {
				_ = cmd.Wait()
				return RunResult{}, rerr
			}
			break
		}
	}
	werr := cmd.Wait()
	res := RunResult{Report: parser.Text(), Tokens: parser.Tokens(), TokenCapped: capped}
	if capped || ctx.Err() != nil {
		return res, nil
	}
	if werr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return res, fmt.Errorf("%s failed: %v: %s", argv[0], werr, detail)
		}
		return res, fmt.Errorf("%s failed: %v", argv[0], werr)
	}
	if res.Tokens == 0 {
		return res, fmt.Errorf("%s reported no token usage, so the task cannot be measured", spec.Candidate)
	}
	return res, nil
}

// boundedBuf keeps the first max bytes written (stderr wording).
type boundedBuf struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (b *boundedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		b.b = append(b.b, p[:room]...)
	}
	return len(p), nil
}

func (b *boundedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.b)
}
