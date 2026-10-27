package daemon

// ensure.go: `pf daemon ensure`, the only way the daemon is started on a
// work machine (by tools/install.sh and by the harness adapters at session
// start, which brings it back after a machine restart tmux does not
// survive). A live daemon on the socket means nothing to do; otherwise a
// tmux session runs the restart loop of ensure_loop.sh.tmpl (exit 0 and
// ExitNeedsOperator end it, so a graceful stop stays stopped and an
// operator-only failure does not spin). An flock on <data-dir>/ensure.lock
// serializes concurrent calls: a pi-web restart resumes many sessions at
// once, and tools/install.sh holds the lock across an upgrade. The
// production knobs live in the config's [daemon] table, which the daemon
// reads itself, so the argv stays fixed.

import (
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

const (
	ensureLockFile = "ensure.lock"
	daemonLogFile  = "daemon.log"
	logRotateBytes = 10 << 20
	restartDelay   = 5 // seconds
)

type ensurePlan struct {
	pfBin, configPath, dataDir, socket, workflowsDir, machineID, home string
	launcher                                                          string
	timeout                                                           time.Duration
	// refuse is checked after the socket probe: a live daemon wins
	refuse error
}

// daemonArgv is the canonical `pf daemon` argv (without the binary).
func (p *ensurePlan) daemonArgv() []string {
	argv := []string{"daemon", "--config", p.configPath, "-machine-id", p.machineID,
		"-data-dir", p.dataDir, "-socket", p.socket, "-workflows-dir", p.workflowsDir}
	if p.launcher != "" {
		argv = append(argv, "-launcher", p.launcher)
	}
	return argv
}

// launcherFromEnv recognises the invoking environment: a Claude Code
// session (CLAUDECODE / CLAUDE_CODE_*), a pi-web session
// (PI_CODING_AGENT_*), or a plain shell (non-interactive).
func launcherFromEnv(environ []string) string {
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_"):
			return "claude"
		case strings.HasPrefix(name, "PI_CODING_AGENT_"):
			return "pi-web"
		}
	}
	return "non-interactive"
}

func (p *ensurePlan) logPath() string { return filepath.Join(p.dataDir, daemonLogFile) }

// Ensure executes `pf daemon ensure [--config P] [--workflows-dir D]
// [--timeout D]` and returns the exit code: 0 the daemon answers on its
// socket, 1 it does not (or ensure refused to start it), 2 usage.
func Ensure(argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pf daemon ensure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "shared config file (default ~/.config/polyforge/cli.toml)")
	workflowsDir := fs.String("workflows-dir", "", "workflows dir the daemon runs (default <data-dir>/release/current/workflows)")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for the daemon socket to answer")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pf daemon ensure: unexpected arguments %v\n", fs.Args())
		return 2
	}
	if os.Getenv("PF_WI_ID") != "" {
		fmt.Fprintln(stderr, "pf daemon ensure: refused inside a step agent (PF_WI_ID is set): only a human's session starts the daemon")
		return 1
	}
	plan, err := planEnsure(*cfgPath, *workflowsDir, *timeout)
	if err == nil {
		err = plan.run(stdout, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pf daemon ensure: %v\n", err)
		return 1
	}
	return 0
}

// planEnsure resolves the canonical launch. A config without a workspace
// mapping is refused: the daemon would refuse it and the loop would spin.
func planEnsure(cfgPath, workflowsDir string, timeout time.Duration) (*ensurePlan, error) {
	if cfgPath == "" {
		cfgPath = cli.DefaultConfigPath()
	}
	cfgPath = absOr(cfgPath)
	shared, err := cli.LoadConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the pf binary: %w", err)
	}
	home, _ := os.UserHomeDir()
	dataDir := cli.DataDir(home)
	if workflowsDir == "" {
		workflowsDir = filepath.Join(dataDir, "release", "current", "workflows")
	}
	p := &ensurePlan{pfBin: exe, configPath: cfgPath, dataDir: dataDir, socket: shared.DaemonSocket,
		workflowsDir: absOr(workflowsDir), machineID: ensureMachineID(dataDir), home: home, timeout: timeout,
		launcher: launcherFromEnv(os.Environ())}
	if shared.Mapping.Empty() {
		p.refuse = fmt.Errorf("not starting the daemon: %s has no [workspace.\"<path>\"] mapping, so the daemon would refuse to start — add one, check it with `pf doctor`, then run `pf daemon ensure` again", cfgPath)
	}
	return p, nil
}

func absOr(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

var hostSanitizeRe = regexp.MustCompile(`[^a-z0-9-]`)

// ensureMachineID answers the recorded daemon id, else hostMachineID.
func ensureMachineID(dataDir string) string {
	if raw, err := os.ReadFile(filepath.Join(dataDir, daemonIDFile)); err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id
		}
	}
	return hostMachineID()
}

// hostMachineID is the daemon id a first start derives: m-<short host>,
// sanitized exactly as tools/install.sh does.
func hostMachineID() string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	return "m-" + hostSanitizeRe.ReplaceAllString(strings.ToLower(host), "-")
}

func (p *ensurePlan) run(stdout, stderr io.Writer) error {
	if p.answers(stdout) {
		return nil
	}
	if p.refuse != nil {
		return p.refuse
	}
	if err := os.MkdirAll(p.dataDir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(p.dataDir, ensureLockFile), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close() // closing the fd drops the flock
	tryLock := func() bool { return syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil }
	deadline := time.Now().Add(p.timeout)
	if !tryLock() {
		// Another ensure is starting the daemon: wait for it, then answer
		// by the socket — never start a second loop.
		fmt.Fprintln(stdout, "another `pf daemon ensure` is starting the daemon; waiting for it")
		for !tryLock() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		return p.await(time.Now(), stdout)
	}
	// The daemon may have come up while we waited for the lock.
	if p.answers(stdout) {
		return nil
	}
	if st, err := os.Stat(p.workflowsDir); err != nil || !st.IsDir() {
		return fmt.Errorf("not starting the daemon: the workflows dir %s is missing (run tools/install.sh, or pass --workflows-dir)", p.workflowsDir)
	}
	if err := p.start(stdout, stderr); err != nil {
		return err
	}
	return p.await(deadline, stdout)
}

// answers probes GET /status (short bound) and says so when it answers.
func (p *ensurePlan) answers(stdout io.Writer) bool {
	c := cli.NewDaemonClient(p.socket)
	c.HTTP.Timeout = 3 * time.Second
	raw, err := c.Status()
	if err != nil {
		return false
	}
	var st cli.StatusResponse
	_ = json.Unmarshal(raw, &st)
	fmt.Fprintf(stdout, "daemon %s answers on %s\n", st.MachineID, p.socket)
	return true
}

// await probes the socket at least once, polling until the deadline.
func (p *ensurePlan) await(deadline time.Time, stdout io.Writer) error {
	for !p.answers(stdout) {
		if time.Now().After(deadline) {
			return fmt.Errorf("the daemon does not answer on %s after %s; check %s (`pf doctor` names config problems)%s",
				p.socket, p.timeout, p.logPath(), logTail(p.logPath(), 10))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// start launches the tmux loop, or without tmux one unsupervised daemon.
func (p *ensurePlan) start(stdout, stderr io.Writer) error {
	running, err := supervisor.LoopRunning()
	if errors.Is(err, supervisor.ErrNoTmux) {
		fmt.Fprintf(stderr, "WARNING: tmux is not installed: starting the daemon WITHOUT supervision — a crash is not restarted until the next `pf daemon ensure`; install tmux and run `pf ops shutdown`, then `pf daemon ensure`\n")
		return p.startDetached()
	}
	if running {
		// between two iterations or still booting: the loop starts it
		fmt.Fprintf(stdout, "supervision loop (tmux session %s) present; waiting for its daemon\n", supervisor.SessionName)
		return nil
	}
	out, err := supervisor.Tmux("new-session", "-d", "-s", supervisor.SessionName, "-c", p.home, "sh", "-c", p.loopScript())
	if err != nil {
		return fmt.Errorf("tmux new-session: %v: %s", err, strings.TrimSpace(out))
	}
	fmt.Fprintf(stdout, "started the daemon supervision loop (tmux session %s; log %s)\n", supervisor.SessionName, p.logPath())
	return nil
}

// startDetached starts one daemon in its own session (no tmux).
func (p *ensurePlan) startDetached() error {
	logf, err := os.OpenFile(p.logPath(), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(p.pfBin, p.daemonArgv()...)
	cmd.Dir = p.home
	cmd.Env = p.daemonEnv(false)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// daemonEnv is the daemon's whole environment: the supervisor allow-list
// filtered from the ENSURE CALLER's environment (never the tmux server's),
// HOME pinned, plus the supervision marker when the loop starts it.
func (p *ensurePlan) daemonEnv(marker bool) []string {
	env := []string{"HOME=" + p.home}
	for _, kv := range supervisor.DaemonEnv(os.Environ()) {
		// PWD is the loop shell's own cwd, never the caller's
		if !strings.HasPrefix(kv, "HOME=") && !strings.HasPrefix(kv, "PWD=") {
			env = append(env, kv)
		}
	}
	if marker {
		env = append(env, supervisor.MarkerEnv+"=tmux:"+supervisor.SessionName)
	}
	return env
}

// loopTmpl renders what tmux runs ("script") and the restart loop
// ("body"); every value goes through q (shell single-quoting). A tmux
// session inherits the TMUX SERVER's environment (captured when the server
// first started), so "script" re-execs through `env -i` with exactly
// daemonEnv. The daemon runs under nohup: killing the session never kills it.
//
//go:embed ensure_loop.sh.tmpl
var loopTmplText string
var loopTmpl = template.Must(template.New("").Funcs(template.FuncMap{"q": shQuote}).Parse(loopTmplText))

func renderLoop(name string, data any) string {
	var b strings.Builder
	if err := loopTmpl.ExecuteTemplate(&b, name, data); err != nil {
		panic(err) // fixed template, fixed data shape
	}
	return b.String()
}

func (p *ensurePlan) loopScript() string {
	var assign []string
	for _, kv := range p.daemonEnv(true) {
		name, val, _ := strings.Cut(kv, "=") // names are shell-safe (supervisor.DaemonEnvAllowed)
		assign = append(assign, name+"="+shQuote(val))
	}
	return renderLoop("script", map[string]any{"Home": p.home, "Env": assign, "Body": p.loopBody()})
}

func (p *ensurePlan) loopBody() string {
	return renderLoop("body", map[string]any{
		"Log": p.logPath(), "Argv": append([]string{p.pfBin}, p.daemonArgv()...),
		"RotateBytes": logRotateBytes, "NeedsOperator": ExitNeedsOperator, "Delay": restartDelay,
	})
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// logTail answers the last n lines of the log (empty when unreadable).
func logTail(path string, n int) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return ""
	}
	raw = raw[max(0, len(raw)-64<<10):] // the log may be up to 10 MiB
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return "\n  " + strings.Join(lines, "\n  ")
}
