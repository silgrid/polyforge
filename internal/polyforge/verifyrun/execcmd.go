package verifyrun

// execcmd.go: running one command — `bash -c <cmd>` for plan commands,
// argv as given for git and the engine — with its combined output in its
// own log file (runtime-state/verify/<n>/<repo>/<k>.log), a private
// TMPDIR, the resource knobs, a per-command timeout that kills the whole
// process group, and one command_evidence entry per run.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Evidence is one command_evidence entry, in the envelope's shape.
type Evidence struct {
	Argv       []string  `json:"argv"`
	Cwd        string    `json:"cwd"`
	ExitCode   int       `json:"exit_code"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	LogPath    string    `json:"log_path"`
}

// timeoutExit is the exit code recorded for a command killed at its
// timeout (timeout(1)'s convention).
const timeoutExit = 124

// gitStepTimeout bounds one recorded git command (fetch, merge, abort,
// worktree add/remove).
const gitStepTimeout = 10 * time.Minute

// execResult is one finished command.
type execResult struct {
	Exit     int
	TimedOut bool
	// DeadlineHit: the command was killed at the run's deadline, which cut
	// its own timeout short.
	DeadlineHit bool
	// NotStarted: the run's deadline had passed; nothing ran, no log,
	// no evidence.
	NotStarted bool
	// TmpDir is the command's private TMPDIR (removed after it ran):
	// output naming it is normalized to a token before parsing.
	TmpDir   string
	LogPath  string // absolute
	Evidence Evidence
	Err      error // the command could not be started at all
}

// execSpec describes one command to run.
type execSpec struct {
	Argv    []string
	Dir     string // absolute working directory
	Cwd     string // the evidence cwd (capsule-relative when inside)
	Timeout time.Duration
	// PrivateTmp gives the command its own TMPDIR (removed afterwards).
	PrivateTmp bool
	// Capture, when set, also receives the command's stdout (the engine's
	// report); the log keeps both streams.
	Capture io.Writer
}

// bashArgv is how every plan, re-run and base command runs.
func bashArgv(cmd string) []string { return []string{"bash", "-c", cmd} }

// exec runs one command for repo rr, logging it as rr's next log and
// recording its evidence on rr.
func (r *runner) exec(ctx context.Context, rr *repoRun, spec execSpec) execResult {
	rr.mu.Lock()
	rr.k++
	k := rr.k
	rr.mu.Unlock()
	if err := os.MkdirAll(rr.logDir, 0o755); err != nil {
		return execResult{Exit: -1, Err: err}
	}
	logPath := filepath.Join(rr.logDir, strconv.Itoa(k)+".log")
	res := r.execTo(ctx, spec, logPath)
	if len(res.Evidence.Argv) > 0 { // a run that never got a log has no evidence
		rr.mu.Lock()
		rr.evidence = append(rr.evidence, res.Evidence)
		rr.mu.Unlock()
	}
	return res
}

// execTo runs spec with its combined output in logPath.
func (r *runner) execTo(ctx context.Context, spec execSpec, logPath string) execResult {
	res := execResult{LogPath: logPath}
	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = r.o.CommandTimeout
	}
	capped := false
	if rem := r.remaining(); rem < time.Second {
		res.Exit, res.NotStarted = -1, true
		return res
	} else if rem < timeout {
		timeout, capped = rem, true
	}
	logf, err := os.Create(logPath)
	if err != nil {
		res.Exit, res.Err = -1, err
		return res
	}
	defer func() { _ = logf.Close() }()
	// a log write that fails costs only the log, never the command's run
	_, _ = fmt.Fprintf(logf, "$ %s\n# cwd: %s\n", strings.Join(spec.Argv, " "), spec.Cwd)

	env := r.commandEnv()
	var tmp string
	if spec.PrivateTmp {
		if tmp, err = os.MkdirTemp("", "pf-verify-tmp-"); err == nil {
			res.TmpDir = tmp
			env = setEnv(env, "TMPDIR", tmp)
			defer func() { _ = os.RemoveAll(tmp) }()
		}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = env
	cmd.Stdin = nil
	cmd.Stdout = logf
	if spec.Capture != nil {
		cmd.Stdout = io.MultiWriter(spec.Capture, logf)
	}
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// the whole group: a test binary's children die with it
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)
	if cmd.Process != nil {
		// whatever the command left running in its group (a server a test
		// forgot, a background job) dies with it
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case cctx.Err() == context.DeadlineExceeded:
		res.Exit, res.TimedOut, res.DeadlineHit = timeoutExit, true, capped
		if capped {
			_, _ = fmt.Fprintf(logf, "\n# pf verify-run: killed at %s\n", r.deadlineNote())
		} else {
			_, _ = fmt.Fprintf(logf, "\n# pf verify-run: killed at its %s timeout\n", timeout)
		}
	case runErr == nil:
		res.Exit = 0
	default:
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.Exit = ee.ExitCode()
			if res.Exit < 0 {
				res.Exit = 128 + int(syscall.SIGKILL)
			}
		} else {
			res.Exit, res.Err = 127, runErr
			_, _ = fmt.Fprintf(logf, "\n# pf verify-run: could not start: %v\n", runErr)
		}
	}
	ms := dur.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	res.Evidence = Evidence{
		Argv:       append([]string(nil), spec.Argv...),
		Cwd:        spec.Cwd,
		ExitCode:   res.Exit,
		StartedAt:  start.UTC().Truncate(time.Millisecond),
		DurationMs: ms,
		LogPath:    r.rel(logPath),
	}
	return res
}

// commandEnv is the environment every command starts from: the
// invocation's own, plus the explicit resource knobs.
func (r *runner) commandEnv() []string {
	env := append([]string(nil), r.o.Env...)
	if r.o.E2EJobs > 0 {
		env = setEnv(env, "E2E_JOBS", strconv.Itoa(r.o.E2EJobs))
	}
	if r.o.GoMaxProcs > 0 {
		env = setEnv(env, "GOMAXPROCS", strconv.Itoa(r.o.GoMaxProcs))
	}
	return env
}

// lookupEnv answers a variable of the command environment.
func (r *runner) lookupEnv(name string) (string, bool) {
	env := r.commandEnv()
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// lookPath resolves a command: a name containing `/` against dir (the
// repo the command runs in; an absolute path as it is), any other name
// on the command environment's PATH.
func (r *runner) lookPath(bin, dir string) bool {
	if strings.Contains(bin, "/") {
		if !filepath.IsAbs(bin) {
			bin = filepath.Join(dir, bin)
		}
		st, err := os.Stat(bin)
		return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
	}
	pathVar, _ := r.lookupEnv("PATH")
	for _, d := range filepath.SplitList(pathVar) {
		if d == "" {
			continue
		}
		p := filepath.Join(d, bin)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// setEnv replaces (or appends) NAME=value in env.
func setEnv(env []string, name, value string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == name {
			continue
		}
		out = append(out, kv)
	}
	return append(out, name+"="+value)
}

// readLines reads a log's lines (each capped at maxLineBytes; the rest of
// an overlong line is dropped, never a parse failure).
func readLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	return scanLines(f)
}

const maxLineBytes = 64 << 10

// normalizeOutput rewrites the paths that differ between two runs of
// the same test — the command's private TMPDIR, the tree it ran in (the
// task repo, or the base worktree) and the system temp dir — to tokens,
// so a failure signature compares the failure, not where it ran.
func normalizeOutput(lines []string, root, tmp string) []string {
	var pairs []string
	for _, p := range [][2]string{{tmp, "<tmp>"}, {root, "<root>"}, {os.TempDir(), "<tmp>"}} {
		if p[0] != "" && p[0] != "/" {
			pairs = append(pairs, p[0], p[1])
		}
	}
	if len(pairs) == 0 {
		return lines
	}
	r := strings.NewReplacer(pairs...)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = r.Replace(l)
	}
	return out
}

// outputLines answers a command log's output lines (the two header lines
// execTo writes skipped).
func outputLines(path string) []string {
	lines := readLines(path)
	if len(lines) < 2 {
		return nil
	}
	return lines[2:]
}

func scanLines(rd io.Reader) []string {
	br := bufio.NewReaderSize(rd, 64<<10)
	var out []string
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if len(line) > maxLineBytes {
				line = line[:maxLineBytes]
			}
			out = append(out, line)
		}
		if err != nil {
			return out
		}
	}
}

// gitQuiet runs one read-only git command (no evidence, no log).
func gitQuiet(ctx context.Context, dir string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, gitStepTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return string(out), fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out), nil
}
