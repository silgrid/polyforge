package task

// dispatch.go: the streamed one-shot harness runner of the task lane
// (carried from #40). One Run is ONE fresh harness process in
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
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// RunSpec is one runner call: one candidate, one prompt, the remaining
// token budget of the task and the directory the harness runs in.
type RunSpec struct {
	Candidate harness.Candidate
	// Prompt is the harness's whole stdin: the task's prompt behind the
	// prohibited-actions section (Manager's dispatchedPrompt).
	Prompt string
	// MaxTokens is the task's REMAINING token budget for this attempt.
	MaxTokens int64
	WorkDir   string
	TaskID    string
	// ScratchTaskDir / ScratchRawDir are the task's scratch directories
	// the daemon created before this dispatch (empty when the caller wired
	// none): the harness's environment carries them as
	// workspace.EnvScratchDir / workspace.EnvScratchRawDir and the prompt's
	// closing # Scratch section names the same absolute paths
	// (polyforge#121, owner ruling Q3).
	ScratchTaskDir string
	ScratchRawDir  string
	// ReadOnlyTools selects the read/search-only tool profile.
	ReadOnlyTools bool
	// Limits are the stuck-detection bounds of this attempt (AC9: the
	// step lane's rule 0/2/3 thresholds, injected by the manager); a
	// zero Silence resolves per the harness's output granularity, a zero
	// Tool to the 30m default (supervise.Defaults.Fill / SilenceFor).
	Limits supervise.Defaults
	// OnContext (optional) is called with the attempt's context-window
	// usage whenever a stream record changes it (polyforge#230): the
	// manager stores it on the running record. The value is freshly built
	// per call and never mutated afterwards; nil drops it.
	OnContext func(*protocol.ContextUsage)
}

// StuckError is the runner's stuck verdict: the supervision rules that
// apply to a task (rule 0 harness_silent, 2 model_hung, 3 tool_hung)
// fired, the runner killed the candidate's process, and the manager
// switches to the next candidate inside the task's remaining budget.
// It is never a failure of the task itself: the run continues on the
// next candidate (AC9).
type StuckError struct {
	Cause    supervise.Cause
	Evidence string
}

func (e *StuckError) Error() string {
	return fmt.Sprintf("judged stuck (%s): %s", e.Cause, e.Evidence)
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

// resolveBin answers the executable path of a harness binary (
// code review): the configured name or path is resolved through exec.LookPath
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

// ReadOnlyClaudeTools / ReadOnlyPiTools are the read/search-only profile
// one spawn may be narrowed to (Spec.ReadOnlyTools; the message analyzer
// is its only user): no Bash, no web tool, no write tool. Like the
// standard lists, an allowlist is the lane's scope, not a sandbox.
const (
	ReadOnlyClaudeTools = "Read,Glob,Grep"
	ReadOnlyPiTools     = "read,grep,find,ls"
)

// innerArgv answers the harness argv (binary first). A claude task runs
// the step's profile in every auth mode, as the step does:
// runtime.ClaudeIsolationArgs (--safe-mode, only the
// harness's settings.json read, the built-in AGENTS.md loader off) and
// runtime.ClaudeToolArgs, under which Claude Code's `system/init.tools`
// equal the declared list (under --bare, 2.1.319 offered Bash and Read of
// the standard list and Read of the read-only one). The tool allowlist
// replaces --permission-mode bypassPermissions: only the listed tools
// exist (polyforge#40 code review). readOnlyTools picks the read/search-only
// pair instead of the standard one. The claude-config settings.json is
// still read: one that exists but is unusable refuses the run.
func (r *ExecRunner) innerArgv(c harness.Candidate, readOnlyTools bool) ([]string, error) {
	bin := r.bin(harness.Of(c.Harness))
	claudeTools, piTools := TaskClaudeTools, TaskPiTools
	if readOnlyTools {
		claudeTools, piTools = ReadOnlyClaudeTools, ReadOnlyPiTools
	}
	if harness.Of(c.Harness) == harness.Claude {
		if _, err := runtime.ReadClaudeSettingsEnv(r.ClaudeConfigDir); err != nil {
			return nil, err
		}
		args := append([]string{bin, "-p"}, runtime.ClaudeIsolationArgs()...)
		// --include-partial-messages (polyforge#72, ruling 14): token
		// deltas stream while the model thinks, exactly like a step's
		// dispatch, so the silence bound judges at token granularity.
		args = append(args, "--output-format", "stream-json", "--verbose", "--no-session-persistence",
			"--include-partial-messages", "--model", c.Model)
		args = append(args, c.EffortArgs()...)
		return append(args, runtime.ClaudeToolArgs(claudeTools)...), nil
	}
	args := []string{bin, "-p", "--mode", "json", "--no-session", "--tools", piTools, "--model", c.Model}
	return append(args, c.EffortArgs()...), nil
}

// taskEnv answers the child env: the inherited env minus every polyforge
// marker, aihub credential and harness-context variable, plus the task's
// own identity and the daemon-owned harness config dirs. No credential
// value is ever added: the harness reads its own from the config dir the
// daemon owns. A claude task also carries runtime.ClaudeIsolationEnv (the
// command adds it with runtime.WithEnv, so an inherited copy of one of its
// names never doubles it).
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

// command builds (without starting) one run's harness process: its argv,
// environment, working directory and the prompt on stdin.
func (r *ExecRunner) command(ctx context.Context, spec RunSpec) (*exec.Cmd, error) {
	argv, err := r.innerArgv(spec.Candidate, spec.ReadOnlyTools)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = spec.WorkDir
	cmd.Env = taskEnv(os.Environ(), spec.TaskID, r.DaemonID, r.SocketPath, r.ClaudeConfigDir, r.PiAgentDir)
	// the daemon-determined scratch directories (polyforge#121): set
	// after the inherited env is stripped of every PF_* name, so the
	// child never inherits a stale copy
	if spec.ScratchTaskDir != "" {
		cmd.Env = append(cmd.Env, workspace.EnvScratchDir+"="+spec.ScratchTaskDir)
	}
	if spec.ScratchRawDir != "" {
		cmd.Env = append(cmd.Env, workspace.EnvScratchRawDir+"="+spec.ScratchRawDir)
	}
	if harness.Of(spec.Candidate.Harness) == harness.Claude {
		cmd.Env = runtime.WithEnv(cmd.Env, runtime.ClaudeIsolationEnv()...)
	}
	cmd.Stdin = strings.NewReader(spec.Prompt)
	return cmd, nil
}

// Run runs one candidate to completion, a token cap, a stuck verdict, or
// ctx's end. The stdout loop feeds both consumers: the usage parser (the
// token cap) and the step lane's supervision translator + Tracker
// (rules 0/2/3, polyforge#72 AC9), whose JudgeTask ticker runs beside it;
// a verdict cancels the run (the process group dies) and answers
// *StuckError — the manager switches to the next candidate.
func (r *ExecRunner) Run(ctx context.Context, spec RunSpec, progress func(tokens int64)) (RunResult, error) {
	h := harness.Of(spec.Candidate.Harness)
	if !SupportsUsage(h) {
		return RunResult{}, fmt.Errorf("candidate %s cannot report token usage, so a task cannot start on it", spec.Candidate)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := r.command(cctx, spec)
	if err != nil {
		return RunResult{}, err
	}
	argv := cmd.Args
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
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
	// The context usage: the same fold a step session's transcript is read
	// with, fed the stream's transcript-equivalent records (polyforge#230).
	contexts := newContextTracker(h)
	// The stuck detection: the tracker the step lane's translator feeds,
	// and the judge beside it. The thresholds are the injected limits
	// (zero silence = the harness's own granularity default).
	limits := spec.Limits.Fill()
	link := supervise.NewLink("task:" + spec.TaskID)
	tracker := link.Tracker(1)
	var translator supervise.Translator
	if h == harness.Claude {
		translator = supervise.NewClaude(tracker)
	} else {
		translator = supervise.NewPi(tracker)
	}
	tracker.Begin(supervise.StepStarted{Step: "task", SpawnN: 1, Model: spec.Candidate.Model,
		Harness: string(h), StartedAt: time.Now(),
		Thresholds: supervise.Thresholds{Silence: limits.SilenceFor(translator.Granularity()), Tool: limits.Tool}})
	verdicts := make(chan supervise.StepDead, 1)
	judgeCtx, judgeStop := context.WithCancel(context.Background())
	go func() {
		defer judgeStop()
		tick := limits.SilenceFor(translator.Granularity()) / 4
		if tick > time.Second {
			tick = time.Second
		}
		if tick < 50*time.Millisecond {
			tick = 50 * time.Millisecond
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-judgeCtx.Done():
				return
			case <-t.C:
				if v := supervise.JudgeTask(tracker.Snapshot(time.Now()), time.Now()); v != nil {
					select {
					case verdicts <- *v:
					default:
					}
					cancel() // the verdict kills the candidate's process group
					return
				}
			}
		}
	}()
	capped := false
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, rerr := br.ReadBytes('\n')
		if len(line) > 0 {
			parser.Feed(line)
			translator.Feed(line)
			if _, changed := contexts.feed(line); changed && spec.OnContext != nil {
				spec.OnContext(contexts.usage(tracker.Snapshot(time.Now()).ContextWindows))
			}
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
	judgeStop()
	werr := cmd.Wait()
	if !capped && ctx.Err() == nil {
		select {
		case v := <-verdicts:
			return RunResult{Report: parser.Text(), Tokens: parser.Tokens()}, &StuckError{Cause: v.Cause, Evidence: v.Evidence}
		default:
		}
	}
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
