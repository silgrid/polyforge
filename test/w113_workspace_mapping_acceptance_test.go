package test

// w113_workspace_mapping_acceptance_test.go: the polyforge#113
// acceptance suite — authored against the SPEC (the capsule's
// artifacts/e2e-scenarios.md, which mirrors spec.md's Acceptance
// criteria and Scenario blocks), BEFORE the implementation lands. It
// pins the workspace→projects mapping contract through the project's
// real e2e surfaces only: the pf/aihub/fakepi binaries, the `pf` CLI,
// the daemon's UDS face, the config file, tools/install.sh, real git
// and the real filesystem — never implementation internals.
//
// Suite posture (see artifacts/e2e-scenarios.md for the full mapping):
//   - New-behavior bars are RED on the pre-implementation tree
//     (e2e-author's baseline run) — assertion-level reds, never build reds: this
//     file compiles and vets against the tree as it stands today;
//     every failure is an exit-code/output/filesystem/process
//     assertion, not a compile error. They must go green when the
//     change lands the spec.
//   - Kept-face guards (read-only `--project` pass-through on
//     `pf queue`, usage errors exiting 2) are green BOTH before and
//     after the change where the surrounding config surface allows.
//
// Daemon argv discipline: every `pf daemon` this suite launches is
// driven through the surviving flag surface only (--socket,
// --api-key, --machine-id, --data-dir, --workflows-dir, --pi-bin,
// --config) — never through the mapping-retired workspace/project
// flags. The only `--project` bytes in this file are LIVE CLI surface
// (queue / file create / remember / recall), which the
// change keeps.
//
// Self-policing: the retired env/flag tokens this suite polices are
// assembled at RUNTIME from fragments (w113Tok) so that this file's
// own bytes never carry them contiguously — mirroring the v3/#106
// convention.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// ---------------------------------------------------------------------------
// Retired-face tokens, assembled from fragments at runtime.
// ---------------------------------------------------------------------------

// w113Tok joins parts into one retired-face token. The fragmentation is
// deliberate: the greppable-floor scenario scans daemon source bytes for
// these tokens, and this file's own assertions must stay honest about
// its bytes (the same discipline as the v3 suite).
func w113Tok(parts ...string) string {
	return strings.Join(parts, "")
}

// w113EnvProjectTok is the retired default-project environment variable.
func w113EnvProjectTok() string { return w113Tok("PF_", "PROJECT") }

// w113EnvWorkspaceTok is the retired daemon workspace env mirror.
func w113EnvWorkspaceTok() string { return w113Tok("POLYFORGE_", "WORKSPACE") }

// w113FlagWorkspaceTok is the retired daemon workspace flag, either
// dash spelling (the single-dash form is a substring of the double-dash
// one, so one needle catches both).
func w113FlagWorkspaceTok() string { return w113Tok("-", "work", "space") }

// w113FlagProjectTok is the retired daemon project flag, either dash
// spelling (the single-dash form is a substring of the double-dash one,
// so one needle catches both).
func w113FlagProjectTok() string { return w113Tok("-", "proj", "ect") }

// ---------------------------------------------------------------------------
// Config fixtures: the cli.toml shapes the spec pins.
// ---------------------------------------------------------------------------

// w113WS is one `[workspace."<key>"]` section of a cli.toml fixture.
type w113WS struct {
	key      string // section key exactly as written (~/… or absolute)
	projects []string
}

// w113ConfigFile writes a cli.toml fixture: the flat connection keys
// plus one section per workspace, and returns the path. Empty
// server/apiKey/socket values skip their lines (dummies are fine —
// the fixture is about the mapping, not the connection).
func w113ConfigFile(t *testing.T, path, server, apiKey, socket string, ws []w113WS) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if server != "" {
		fmt.Fprintf(&b, "server = %q\n", server)
	}
	if apiKey != "" {
		fmt.Fprintf(&b, "api_key = %q\n", apiKey)
	}
	if socket != "" {
		fmt.Fprintf(&b, "daemon_socket = %q\n", socket)
	}
	b.WriteString("user_id = \"usr_root\"\n")
	for _, w := range ws {
		fmt.Fprintf(&b, "\n[workspace.%q]\n", w.key)
		// w113ProjectList renders the COMPLETE array (["a", "b"]) —
		// the writer adds no brackets of its own.
		fmt.Fprintf(&b, "projects = %s\n", w113ProjectList(w.projects))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// w113ProjectList renders the projects array body: ["a", "b"].
func w113ProjectList(projects []string) string {
	parts := make([]string, len(projects))
	for i, p := range projects {
		parts[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// ---------------------------------------------------------------------------
// The pf CLI as a real process, on a controlled hermetic environment.
// ---------------------------------------------------------------------------

// w113Env describes the environment one w113 CLI run gets: a scratch
// HOME and XDG_CONFIG_HOME (so no ambient config leaks in), optional
// connection env (PF_SERVER/PF_API_KEY/PF_USER_ID — the surviving env
// face), an optional --config global flag, an optional daemon socket
// env, and extra vars (the retired ones under test).
type w113Env struct {
	home   string
	config string // --config global flag; "" = none
	socket string // PF_DAEMON_SOCKET; "" = none
	json   bool   // --json global flag before the verb
	noConn bool   // omit PF_SERVER/PF_API_KEY/PF_USER_ID
	extra  []string
}

// w113Pf runs the real pf binary once and returns (combined output,
// exit code) with NO assertion on the code.
func w113Pf(t *testing.T, inst *v1Install, e w113Env, args ...string) (string, int) {
	t.Helper()
	pfPath := filepath.Join(v1Bins(t), "pf")
	if inst != nil && inst.pfPath != "" {
		pfPath = inst.pfPath
	}
	if e.home == "" {
		e.home = t.TempDir()
	}
	xdg := filepath.Join(e.home, ".config")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"XDG_CONFIG_HOME=" + xdg,
	}
	if !e.noConn && inst != nil {
		env = append(env,
			"PF_SERVER="+inst.url,
			"PF_API_KEY="+inst.adminKey,
			"PF_USER_ID=usr_root",
		)
	}
	if e.socket != "" {
		env = append(env, "PF_DAEMON_SOCKET="+e.socket)
	}
	env = append(env, e.extra...)
	argv := make([]string, 0, len(args)+4)
	if e.config != "" {
		argv = append(argv, "--config", e.config)
	}
	if e.json {
		argv = append(argv, "--json")
	}
	argv = append(argv, args...)
	cmd := exec.Command(pfPath, argv...)
	cmd.Env = env
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", argv, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// w113PfWant asserts one w113 CLI run's exit code and returns the
// combined output (wantExit < 0 = must succeed).
func w113PfWant(t *testing.T, inst *v1Install, wantExit int, e w113Env, args ...string) string {
	t.Helper()
	out, code := w113Pf(t, inst, e, args...)
	if wantExit < 0 && code != 0 {
		t.Fatalf("pf %v: exit %d, want success (output %q)", args, code, out)
	}
	if wantExit >= 0 && code != wantExit {
		t.Fatalf("pf %v: exit %d, want %d (output %q)", args, code, wantExit, out)
	}
	return out
}

// ---------------------------------------------------------------------------
// The daemon as a real process on the post-change flag surface.
// ---------------------------------------------------------------------------

// w113Daemon is one `pf daemon` process driven only through surviving
// flags: --socket, --api-key, --machine-id, --data-dir,
// --workflows-dir, --pi-bin and (optionally) --config. The aihub
// server comes from the config file the spec makes the shared source.
type w113Daemon struct {
	t         *testing.T
	pfPath    string
	fakepi    string
	root      string
	sock      string
	dataDir   string
	machineID string
	apiKey    string
	config    string // --config value; "" = the standard XDG location
	home      string
	xdg       string
	planDir   string // FAKEPI_PLANS ("" = none; refusal paths never spawn)
	extraArgs []string
	extraEnv  []string
	cmd       *exec.Cmd
	logPath   string
	logFile   *os.File
	waitExit  chan error
}

type w113DOpt func(*w113Daemon)

func w113DOptSock(s string) w113DOpt    { return func(d *w113Daemon) { d.sock = s } }
func w113DOptDataDir(p string) w113DOpt { return func(d *w113Daemon) { d.dataDir = p } }
func w113DOptConfig(p string) w113DOpt  { return func(d *w113Daemon) { d.config = p } }
func w113DOptHome(h string) w113DOpt    { return func(d *w113Daemon) { d.home = h } }

func w113DOptArgs(a ...string) w113DOpt {
	return func(d *w113Daemon) { d.extraArgs = append(d.extraArgs, a...) }
}

// w113NewDaemon prepares (without starting) a daemon on the surviving
// flag surface. inst may be nil for refusal paths that never reach the
// wire (the binary cache is shared either way).
func w113NewDaemon(t *testing.T, inst *v1Install, opts ...w113DOpt) *w113Daemon {
	t.Helper()
	d := &w113Daemon{
		t:         t,
		pfPath:    filepath.Join(v1Bins(t), "pf"),
		fakepi:    filepath.Join(v1Bins(t), "fakepi"),
		root:      v1Root(t),
		sock:      filepath.Join(shortTmp(t, "w113sock"), "d.sock"),
		dataDir:   t.TempDir(),
		machineID: "m-w113",
		apiKey:    "w113key",
		home:      t.TempDir(),
		logPath:   filepath.Join(t.TempDir(), "w113daemon.log"),
	}
	if inst != nil {
		d.apiKey = inst.adminKey
		d.planDir = inst.planDir
	}
	for _, o := range opts {
		o(d)
	}
	d.xdg = filepath.Join(d.home, ".config")
	if err := os.MkdirAll(d.xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.stop() })
	return d
}

// start launches the daemon and requires its UDS face to answer: the
// expect-serve shape (the new-behavior bars). A daemon that exits
// during startup fails HERE — an assertion-level red on the
// pre-implementation tree (it exits on the retired workspace
// requirement or the config parse long before anything serves).
func (d *w113Daemon) start() {
	d.t.Helper()
	served, output, code := d.boot(30 * time.Second)
	if !served {
		d.t.Fatalf("w113 daemon exited during startup: exit %d (log tail: %s)", code, output)
	}
}

// boot launches the daemon process and waits up to bound for EITHER its
// UDS face to answer (served=true; the process keeps running under the
// registered cleanup) or the process to exit (served=false, with the
// combined log output and exit code). A daemon that neither serves nor
// exits inside the bound fails the test.
func (d *w113Daemon) boot(bound time.Duration) (served bool, output string, code int) {
	d.t.Helper()
	logf, err := os.Create(d.logPath)
	if err != nil {
		d.t.Fatal(err)
	}
	d.logFile = logf
	args := []string{
		"daemon",
		"--socket", d.sock,
		"--api-key", d.apiKey,
		"--machine-id", d.machineID,
		"--data-dir", d.dataDir,
		"--workflows-dir", strippedWorkflowsDir(d.t, filepath.Join(d.root, "workflows")),
		"--pi-bin", d.fakepi,
		// never the host's real claude; a test opts in through extraArgs
		// (a later --claude-bin wins)
		"--claude-bin", "/nonexistent/claude-test-guard",
	}
	if d.config != "" {
		args = append(args, "--config", d.config)
	}
	args = append(args, d.extraArgs...)
	cmd := exec.Command(d.pfPath, args...)
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + d.home,
		"XDG_CONFIG_HOME=" + d.xdg,
		// the fakepi orphan watchdog's pid (see proc_hygiene_test.go)
		fakepiTestPIDEnv + "=" + os.Getenv(fakepiTestPIDEnv),
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	}
	if d.planDir != "" {
		env = append(env, "FAKEPI_PLANS="+d.planDir)
	}
	env = append(env, d.extraEnv...)
	cmd.Env = env
	cmd.Dir = d.dataDir
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		d.t.Fatal(err)
	}
	d.cmd = cmd
	d.waitExit = make(chan error, 1)
	go func() { d.waitExit <- cmd.Wait() }()

	deadline := time.Now().Add(bound)
	for {
		conn, derr := net.Dial("unix", d.sock)
		if derr == nil {
			conn.Close()
			return true, d.logTail(), 0
		}
		select {
		case werr := <-d.waitExit:
			d.cmd = nil
			c := -1
			if ee, ok := werr.(*exec.ExitError); ok {
				c = ee.ExitCode()
			}
			return false, d.logTail(), c
		default:
		}
		if time.Now().After(deadline) {
			d.stop()
			d.t.Fatalf("w113 daemon neither served nor exited within %v (log tail: %s)", bound, d.logTail())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// logTail returns the last ~4KB of the daemon log.
func (d *w113Daemon) logTail() string {
	raw, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("(unreadable log: %v)", err)
	}
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	return string(raw)
}

// stop terminates the daemon (SIGTERM, group SIGKILL fallback, then a
// group kill for stragglers). Idempotent.
func (d *w113Daemon) stop() {
	if d.logFile != nil {
		_ = d.logFile.Close()
	}
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.waitExit:
	case <-time.After(10 * time.Second):
		killGroup(d.cmd)
		<-d.waitExit
	}
	killGroup(d.cmd)
	d.cmd = nil
}

// crash kills the daemon hard (the crash-restart lever).
func (d *w113Daemon) crash() {
	d.t.Helper()
	if d.cmd == nil || d.cmd.Process == nil {
		d.t.Fatal("w113 crash: daemon not running")
	}
	// drain a push the daemon had in flight (see v1Daemon.crash)
	gits := gitDescendants(d.cmd.Process.Pid)
	_ = d.cmd.Process.Kill()
	<-d.waitExit
	d.cmd = nil
	waitPIDsGone(gits, 10*time.Second)
}

// socketExists reports whether the UDS socket file is on disk.
func (d *w113Daemon) socketExists() bool {
	_, err := os.Stat(d.sock)
	return err == nil
}

// pf runs the real pf CLI against THIS daemon's socket with no exit
// assertion, returning (combined output, exit code).
func (d *w113Daemon) pf(inst *v1Install, args ...string) (string, int) {
	d.t.Helper()
	return w113Pf(d.t, inst, w113Env{socket: d.sock}, args...)
}

// pfWant runs the CLI against this daemon's socket asserting the exit
// code and returning the combined output.
func (d *w113Daemon) pfWant(inst *v1Install, wantExit int, args ...string) string {
	d.t.Helper()
	return w113PfWant(d.t, inst, wantExit, w113Env{socket: d.sock}, args...)
}

// ---------------------------------------------------------------------------
// Work items in arbitrary projects (the harness's newWI is pinned to
// the e2e project; the mapping scenarios need more).
// ---------------------------------------------------------------------------

// w113NewWI files one autopilot execution ticket in the given project
// and scripts its agent with the given plan actions.
func w113NewWI(t *testing.T, inst *v1Install, project, goal string, actions []map[string]any) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    project,
		Goal:       goal,
		BundleName: strPtr("autopilot"),
		Attrs:      nil,
	})
	if err != nil {
		t.Fatalf("create work item in %s: %v", project, err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	inst.setPlan(t, wi.ID, actions)
	inst.trackWI(wi.ID)
	return wi.ID
}

// w113EnsureProject creates the project if it is not there yet.
func w113EnsureProject(t *testing.T, inst *v1Install, name string) {
	t.Helper()
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: name, Description: "w113 scratch project",
	}); err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
}

// w113Capsule is the capsule dir of wi under workspace ws.
func w113Capsule(t *testing.T, inst *v1Install, ws, wi string) string {
	return filepath.Join(ws, "capsules", wiPathName(t, inst, wi))
}

// w113TopLevel lists the entry names of dir.
func w113TopLevel(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// w113StepStatus polls the capsule's execution.json until one step's
// status reaches want.
func w113StepStatus(t *testing.T, capDir, step, want string, bound time.Duration) {
	t.Helper()
	waitExecution(t, capDir, bound,
		fmt.Sprintf("step %s never reached %q", step, want),
		func(x *executionView) bool {
			st := x.Steps[step]
			return st != nil && st.Status == want
		})
}

// w113WaitCompleted polls the capsule's execution.json until the
// workflow completes.
func w113WaitCompleted(t *testing.T, capDir string, bound time.Duration) {
	t.Helper()
	waitExecution(t, capDir, bound, "workflow never completed",
		func(x *executionView) bool { return x.WorkflowStatus == "completed" })
}

// w113WaitCapsule polls until the capsule dir exists.
func w113WaitCapsule(t *testing.T, capDir string, bound time.Duration) {
	t.Helper()
	waitUntil(t, bound, fmt.Sprintf("capsule never materialized at %s", capDir), func() bool {
		_, err := os.Stat(capDir)
		return err == nil
	})
}

// ---------------------------------------------------------------------------
// Doctor --json decoding (the minimum shape the spec pins).
// ---------------------------------------------------------------------------

// w113DoctorCheck is one check record of `pf --json doctor`.
type w113DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// w113DoctorDoc is the minimum --json document shape.
type w113DoctorDoc struct {
	OK     bool              `json:"ok"`
	Checks []w113DoctorCheck `json:"checks"`
}

// w113DoctorJSON extracts and decodes the doctor JSON document from the
// CLI's combined output (tolerating surrounding text), failing the test
// when no document decodes.
func w113DoctorJSON(t *testing.T, out string) *w113DoctorDoc {
	t.Helper()
	s := w113JSONDocOf(out)
	var doc w113DoctorDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("doctor --json output is not the pinned {ok, checks} shape: %v (output %q)", err, out)
	}
	return &doc
}

// w113FailingCheck returns the first failing check whose name+detail
// contains any of the needles (case-insensitive); nil when none does.
func w113FailingCheck(doc *w113DoctorDoc, needles ...string) *w113DoctorCheck {
	for i := range doc.Checks {
		c := &doc.Checks[i]
		if c.Status != "fail" {
			continue
		}
		hay := strings.ToLower(c.Name + " " + c.Detail)
		for _, n := range needles {
			if strings.Contains(hay, strings.ToLower(n)) {
				return c
			}
		}
	}
	return nil
}

// w113JSONDocOf trims surrounding text off a CLI --json output.
func w113JSONDocOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	return s
}

// ---------------------------------------------------------------------------
// /proc argv scanning (the installer-launched daemon).
// ---------------------------------------------------------------------------

// w113ProcArgv lists live processes whose argv contains match and the
// literal element "daemon" (self excluded).
func w113ProcArgv(match string) [][]string {
	var out [][]string
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := os.Getpid()
	for _, e := range ents {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil || pid == self {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if rerr != nil {
			continue
		}
		argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if !contains(argv, "daemon") {
			continue
		}
		if strings.Contains(strings.Join(argv, " "), match) {
			out = append(out, argv)
		}
	}
	return out
}

// w113KillArgv SIGKILLs every live daemon process matching match
// (installer hygiene: a launched daemon never outlives the scenario).
func w113KillArgv(match string) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, e := range ents {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil || pid == self {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if rerr != nil {
			continue
		}
		argv := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if !contains(argv, "daemon") {
			continue
		}
		if strings.Contains(strings.Join(argv, " "), match) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// w113FindDaemonArgv polls /proc until a daemon process matching match
// appears (the installer's nohup launch), returning its argv.
func w113FindDaemonArgv(t *testing.T, match string, bound time.Duration) []string {
	t.Helper()
	var found []string
	waitUntil(t, bound, "no daemon process matching "+match+" ever appeared", func() bool {
		got := w113ProcArgv(match)
		if len(got) > 0 {
			found = got[0]
			return true
		}
		return false
	})
	return found
}

// ---------------------------------------------------------------------------
// Hermetic tools/install.sh runs: stubbed gh/pi on PATH, the real built
// pf served as the release download, a scratch HOME with the package
// checkout stub the installer expects.
// ---------------------------------------------------------------------------

// w113InstallCtx is one hermetic installer fixture.
type w113InstallCtx struct {
	stubBin string // gh/pi stubs (put FIRST on PATH)
	home    string // scratch HOME (config, package checkout, daemon data)
	rel     string // the fake release dir the stub gh serves
	tmuxDir string // private tmux server (polyforge#174: install.sh starts the daemon loop in tmux)
}

// w113InstallPrep builds the hermetic installer fixture: a stub bin dir
// (gh serving a local fake release with a correct SHA256SUMS, pi a
// no-op), a scratch HOME carrying the pre-created package checkout
// (package.json + skills/polyforge/SKILL.md + workflows/), and the
// /usr/local/bin write gate with pf-binary snapshot/restore.
func w113InstallPrep(t *testing.T) *w113InstallCtx {
	t.Helper()
	bins := v1Bins(t)
	// the fake release dir must exist before w113CopyFile writes into it
	// (the baseline run's fixture defect — repaired per its verdict)
	relDir := filepath.Join(t.TempDir(), "rel")
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ic := &w113InstallCtx{
		rel:     relDir,
		home:    t.TempDir(),
		tmuxDir: t.TempDir(),
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "kill-server")
		kill.Env = []string{"PATH=" + os.Getenv("PATH"), "TMUX_TMPDIR=" + ic.tmuxDir}
		_ = kill.Run()
	})
	pfBin := filepath.Join(ic.rel, "pf-linux-amd64")
	if err := w113CopyFile(filepath.Join(bins, "pf"), pfBin); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pfBin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	sums := fmt.Sprintf("%s  pf-linux-amd64\n", hex.EncodeToString(sum[:]))
	if err := os.WriteFile(filepath.Join(ic.rel, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}

	// the stub bin dir must exist before the gh stub is written into it
	// (same fixture class as the rel dir — repaired together)
	ic.stubBin = filepath.Join(t.TempDir(), "stubbin")
	if err := os.MkdirAll(ic.stubBin, 0o755); err != nil {
		t.Fatal(err)
	}
	gh := "#!/bin/sh\n" +
		"# w113 stub gh: serves the local fake release (hermetic installer run).\n" +
		"case \"$1\" in\n" +
		"release)\n" +
		"  case \"$2\" in\n" +
		"  list) echo '[{\"tagName\":\"v0.0.0-w113\"}]' ;;\n" +
		"  download)\n" +
		"    dir=; prev=\n" +
		"    for a in \"$@\"; do\n" +
		"      if [ \"$prev\" = \"--dir\" ]; then dir=$a; fi\n" +
		"      prev=$a\n" +
		"    done\n" +
		"    if [ -n \"$dir\" ] && [ -n \"$W113_REL\" ]; then\n" +
		"      cp \"$W113_REL/pf-linux-amd64\" \"$dir/\" 2>/dev/null\n" +
		"      cp \"$W113_REL/SHA256SUMS\" \"$dir/\" 2>/dev/null\n" +
		"    fi\n" +
		"    ;;\n" +
		"  esac\n" +
		"  ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(ic.stubBin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	pi := "#!/bin/sh\n# w113 stub pi: no-op (the package checkout is pre-created).\nexit 0\n"
	if err := os.WriteFile(filepath.Join(ic.stubBin, "pi"), []byte(pi), 0o755); err != nil {
		t.Fatal(err)
	}

	// The scratch HOME with the pre-created package checkout.
	pkg := filepath.Join(ic.home, ".pi", "agent", "git", "github.com", "silgrid", "polyforge")
	pkgJSON := "{\n  \"keywords\": [\"pi-package\"],\n  \"pi\": {\"skills\": [\"./skills/polyforge\"]}\n}\n"
	files := map[string]string{
		filepath.Join(pkg, "package.json"):                             pkgJSON,
		filepath.Join(pkg, "skills", "polyforge", "SKILL.md"):          "# polyforge routing skill (w113 stub)\n",
		filepath.Join(pkg, "workflows", "feature-full", "workflow.md"): "# stub workflow\n",
	}
	for dir, content := range files {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The /usr/local/bin write gate (the installer hard-codes it), with
	// snapshot/restore of any pre-existing pf binary. Opt-in only: the
	// restore runs in t.Cleanup, so a test process killed mid-run (a
	// -timeout panic, a stopped CI job) leaves the test build installed
	// as the host's real pf. Never touch a host binary by default.
	if os.Getenv("POLYFORGE_TEST_SYSTEM_INSTALL") != "1" {
		t.Skip("installer scenarios write the host's /usr/local/bin/pf; set POLYFORGE_TEST_SYSTEM_INSTALL=1 to run them on a disposable host")
	}
	probe := "/usr/local/bin/.w113probe"
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Skipf("installer scenarios need write access to /usr/local/bin: %v", err)
	}
	_ = os.Remove(probe)
	binPath := "/usr/local/bin/pf"
	prev, hadErr := os.ReadFile(binPath)
	had := hadErr == nil
	t.Cleanup(func() {
		if had {
			_ = os.WriteFile(binPath, prev, 0o755)
		} else {
			_ = os.Remove(binPath)
		}
	})
	return ic
}

// w113RunInstall runs tools/install.sh once hermetically and returns
// (combined output, exit code).
func w113RunInstall(t *testing.T, ic *w113InstallCtx) (string, int) {
	t.Helper()
	script := filepath.Join(v1Root(t), "tools", "install.sh")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", script, "w113key", "127.0.0.1")
	cmd.Env = []string{
		"PATH=" + ic.stubBin + ":" + os.Getenv("PATH"),
		"HOME=" + ic.home,
		"W113_REL=" + ic.rel,
		// never the operator's tmux server (TMUX is not passed either)
		"TMUX_TMPDIR=" + ic.tmuxDir,
	}
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("install.sh: %v (output %q)", err, out.String())
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// w113CopyFile copies src to dst (mode 0755).
func w113CopyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o755)
}

// ---------------------------------------------------------------------------
// S01 — Mapping loads for CLI and daemon: ~ expanded, absolute keys.
// ---------------------------------------------------------------------------

// TestW113MappingTildeExpansionAndRouting pins acceptance criterion #1:
// a cli.toml with two `[workspace."<path>"]` sections (one `~/…` key,
// one absolute) loads for the daemon, `~` is expanded to the home
// directory, the absolute key is used literally, and per-claim capsule
// routing lands each project's work item in its own workspace. RED
// pre-change: the daemon exits at startup on the (then unparsable)
// mapping-bearing config.
func TestW113MappingTildeExpansionAndRouting(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")

	home := t.TempDir()
	tildeWS := filepath.Join(home, "w113home")
	if err := os.MkdirAll(tildeWS, 0o755); err != nil {
		t.Fatal(err)
	}
	absWS := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{
			{key: "~/w113home", projects: []string{"e2e"}},
			{key: absWS, projects: []string{"acme-ops"}},
		})

	d := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptHome(home))
	d.start()
	defer d.stop()

	wiA := w113NewWI(t, inst, "e2e", "w113 tilde-keyed workspace claim",
		envelopeAutopilot())
	wiB := w113NewWI(t, inst, "acme-ops", "w113 absolute-keyed workspace claim",
		envelopeAutopilot())
	d.pfWant(inst, -1, "submit", wiA)
	d.pfWant(inst, -1, "submit", wiB)

	capA := w113Capsule(t, inst, tildeWS, wiA)
	capB := w113Capsule(t, inst, absWS, wiB)
	w113WaitCapsule(t, capA, 60*time.Second)
	w113WaitCapsule(t, capB, 60*time.Second)
	w113WaitCompleted(t, capA, 120*time.Second)
	w113WaitCompleted(t, capB, 120*time.Second)
	inst.waitWIStatus(t, wiA, "wrapped", 60*time.Second)
	inst.waitWIStatus(t, wiB, "wrapped", 60*time.Second)

	// The #103 contract, extended to N workspaces: the daemon writes
	// only capsules/ inside each mapped workspace.
	if got := w113TopLevel(t, tildeWS); len(got) != 1 || got[0] != "capsules" {
		t.Fatalf("workspace %s top level = %v, want only [capsules]", tildeWS, got)
	}
	if got := w113TopLevel(t, absWS); len(got) != 1 || got[0] != "capsules" {
		t.Fatalf("workspace %s top level = %v, want only [capsules]", absWS, got)
	}
}

// ---------------------------------------------------------------------------
// S02 — Relative and trailing-separator workspace keys are load errors.
// ---------------------------------------------------------------------------

// TestW113MappingInvalidKeysRejected pins the key-normalization half of
// criterion #1 (relative keys and trailing path separators are load
// errors, not silently normalized) for BOTH readers: the CLI and the
// daemon. Each fixture carries one VALID section first so a pre-change
// flat-parse error names the first section line, never the offending
// key — the red is genuinely about the new key validation.
func TestW113MappingInvalidKeysRejected(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	goodWS := t.TempDir()
	cases := []struct {
		name string
		key  string
		want string // the offending-key text the error must name
	}{
		{"relative key", "rel/w113rel", "w113rel"},
		{"trailing separator", t.TempDir() + "/w113trail/", "w113trail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
				"http://127.0.0.1:1", "w113key", "",
				[]w113WS{
					{key: goodWS, projects: []string{"e2e"}},
					{key: c.key, projects: []string{"acme-ops"}},
				})
			// CLI reader.
			out, code := w113Pf(t, inst, w113Env{config: cfg, noConn: true}, "queue")
			if code != 1 {
				t.Fatalf("pf queue on invalid key %q: exit %d, want 1 (output %q)", c.key, code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("pf queue on invalid key %q: error does not name the offending key %q (output %q)", c.key, c.want, out)
			}
			// Daemon reader.
			d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
			served, doutput, dcode := d.boot(30 * time.Second)
			if served {
				d.stop()
				t.Fatalf("daemon started on invalid workspace key %q", c.key)
			}
			if dcode != w113ExitNeedsOperator {
				t.Fatalf("daemon on invalid key %q: exit %d, want 78 (output %q)", c.key, dcode, doutput)
			}
			if !strings.Contains(doutput, c.want) {
				t.Fatalf("daemon on invalid key %q: error does not name the offending key %q (output %q)", c.key, c.want, doutput)
			}
			if d.socketExists() {
				t.Fatalf("daemon on invalid key %q still created its socket", c.key)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// S03 — Old-shape config (flat project key) refused everywhere.
// ---------------------------------------------------------------------------

// TestW113OldShapeConfigRefusedEverywhere pins criterion #2: a config
// carrying the flat `project` key is refused by every pf command and
// the daemon as an unknown top-level key (naming the key and the file).
func TestW113OldShapeConfigRefusedEverywhere(t *testing.T) {
	inst := newV1Install(t)
	oldShape := "server = " + strconv.Quote(inst.url) + "\n" +
		"api_key = \"w113key\"\n" +
		"user_id = \"usr_root\"\n" +
		"project = \"e2e\"\n"
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	if err := os.WriteFile(cfgPath, []byte(oldShape), 0o600); err != nil {
		t.Fatal(err)
	}
	assertRefused := func(what, out string, code, want int) {
		t.Helper()
		if code != want {
			t.Fatalf("%s on old-shape config: exit %d, want %d (output %q)", what, code, want, out)
		}
		for _, tok := range []string{`unknown key "project" in ` + cfgPath} {
			if !strings.Contains(out, tok) {
				t.Fatalf("%s on old-shape config: error misses the %q guidance token (output %q)", what, tok, out)
			}
		}
	}
	// The CLI readers.
	out, code := w113Pf(t, inst, w113Env{config: cfgPath}, "queue")
	assertRefused("pf queue", out, code, 1)
	out, code = w113Pf(t, inst, w113Env{config: cfgPath},
		"remember", "--type", "experience.debug", "w113 old-shape memory")
	assertRefused("pf remember", out, code, 1)
	out, code = w113Pf(t, inst, w113Env{config: cfgPath}, "recall", "w113 query")
	assertRefused("pf recall", out, code, 1)
	// The daemon reader.
	d := w113NewDaemon(t, nil, w113DOptConfig(cfgPath))
	served, doutput, dcode := d.boot(30 * time.Second)
	if served {
		d.stop()
		t.Fatal("daemon started on an old-shape config")
	}
	assertRefused("pf daemon", doutput, dcode, w113ExitNeedsOperator)
	if d.socketExists() {
		t.Fatal("daemon on an old-shape config still created its socket")
	}
}

// ---------------------------------------------------------------------------
// S04 — Project listed under two workspaces fails loading naming both.
// ---------------------------------------------------------------------------

// TestW113AmbiguousInverseRejected pins the first half of criterion #3:
// a project in two workspace sections is a load error naming the
// project and BOTH workspaces, for both readers.
func TestW113AmbiguousInverseRejected(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	wsA := filepath.Join(t.TempDir(), "w113dupA")
	wsB := filepath.Join(t.TempDir(), "w113dupB")
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		"http://127.0.0.1:1", "w113key", "",
		[]w113WS{
			{key: wsA, projects: []string{"acme-ops"}},
			{key: wsB, projects: []string{"acme-ops"}},
		})
	out, code := w113Pf(t, inst, w113Env{config: cfg, noConn: true}, "queue")
	if code != 1 {
		t.Fatalf("pf queue on ambiguous inverse: exit %d, want 1 (output %q)", code, out)
	}
	for _, tok := range []string{"acme-ops", "w113dupA", "w113dupB"} {
		if !strings.Contains(out, tok) {
			t.Fatalf("pf queue on ambiguous inverse: error misses %q (output %q)", tok, out)
		}
	}
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	served, doutput, dcode := d.boot(30 * time.Second)
	if served {
		d.stop()
		t.Fatal("daemon started on an ambiguous mapping")
	}
	if dcode != w113ExitNeedsOperator {
		t.Fatalf("daemon on ambiguous inverse: exit %d, want 78 (output %q)", dcode, doutput)
	}
	for _, tok := range []string{"acme-ops", "w113dupA", "w113dupB"} {
		if !strings.Contains(doutput, tok) {
			t.Fatalf("daemon on ambiguous inverse: error misses %q (output %q)", tok, doutput)
		}
	}
}

// ---------------------------------------------------------------------------
// S05 — Project duplicated within one workspace's list fails loading.
// ---------------------------------------------------------------------------

// TestW113DuplicateProjectInOneListRejected pins the second half of
// criterion #3: a project twice in one workspace's projects array is a
// load error naming the duplicate project and its workspace, for both
// readers.
func TestW113DuplicateProjectInOneListRejected(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	ws := filepath.Join(t.TempDir(), "w113dup")
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		"http://127.0.0.1:1", "w113key", "",
		[]w113WS{{key: ws, projects: []string{"e2e", "e2e"}}})
	out, code := w113Pf(t, inst, w113Env{config: cfg, noConn: true}, "queue")
	if code != 1 {
		t.Fatalf("pf queue on duplicate project: exit %d, want 1 (output %q)", code, out)
	}
	for _, tok := range []string{"e2e", "w113dup"} {
		if !strings.Contains(out, tok) {
			t.Fatalf("pf queue on duplicate project: error misses %q (output %q)", tok, out)
		}
	}
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	served, doutput, dcode := d.boot(30 * time.Second)
	if served {
		d.stop()
		t.Fatal("daemon started on a duplicate project list")
	}
	if dcode != w113ExitNeedsOperator {
		t.Fatalf("daemon on duplicate project: exit %d, want 78 (output %q)", dcode, doutput)
	}
	for _, tok := range []string{"e2e", "w113dup"} {
		if !strings.Contains(doutput, tok) {
			t.Fatalf("daemon on duplicate project: error misses %q (output %q)", tok, doutput)
		}
	}
}

// ---------------------------------------------------------------------------
// S07 — the retired default-project env var has no effect on the CLI.
// ---------------------------------------------------------------------------

// TestW113CLIIgnoresRetiredEnv pins the CLI half of criterion #4: with
// the retired env var set to an UNMAPPED project, `pf queue` without
// --project behaves exactly as with the variable unset — both runs
// exit 0 with byte-identical output covering all mapped projects.
func TestW113CLIIgnoresRetiredEnv(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e", "acme-ops"}}})
	w113NewWI(t, inst, "e2e", "w113 env-indifference alpha", nil)
	w113NewWI(t, inst, "acme-ops", "w113 env-indifference beta", nil)

	withVar, codeSet := w113Pf(t, inst, w113Env{config: cfg, extra: []string{w113EnvProjectTok() + "=w113unmapped"}}, "queue")
	withoutVar, codeUnset := w113Pf(t, inst, w113Env{config: cfg}, "queue")
	if codeSet != 0 || codeUnset != 0 {
		t.Fatalf("pf queue exit codes: var set %d, unset %d, want 0/0 (outputs %q / %q)",
			codeSet, codeUnset, withVar, withoutVar)
	}
	if withVar != withoutVar {
		t.Fatalf("pf queue output differs under the retired env var:\nset:   %q\nunset: %q", withVar, withoutVar)
	}
	for _, tok := range []string{"w113 env-indifference alpha", "w113 env-indifference beta", "e2e", "acme-ops"} {
		if !strings.Contains(withVar, tok) {
			t.Fatalf("pf queue output does not cover the mapped projects (%q missing): %q", tok, withVar)
		}
	}
}

// ---------------------------------------------------------------------------
// S08 — the retired daemon flags fail flag parsing.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// S09 — daemon --config default = standard cli.toml location; explicit
// --config overrides.
// ---------------------------------------------------------------------------

// TestW113DaemonDefaultConfigLocation pins criterion #6: the daemon
// with no --config loads the mapping from the standard XDG cli.toml
// location (and routes claims by it), and an explicit --config naming a
// different mapping overrides the default.
func TestW113DaemonDefaultConfigLocation(t *testing.T) {
	inst := newV1Install(t)
	home := t.TempDir()
	defaultWS := filepath.Join(home, "w113default")
	if err := os.MkdirAll(defaultWS, 0o755); err != nil {
		t.Fatal(err)
	}
	// The standard location: $XDG_CONFIG_HOME/polyforge/cli.toml.
	std := filepath.Join(home, ".config", "polyforge", "cli.toml")
	w113ConfigFile(t, std, inst.url, inst.adminKey, "",
		[]w113WS{{key: defaultWS, projects: []string{"e2e"}}})

	wiA := w113NewWI(t, inst, "e2e", "w113 default-config-location claim", envelopeAutopilot())
	d := w113NewDaemon(t, inst, w113DOptHome(home)) // NO --config
	d.start()
	defer d.stop()
	d.pfWant(inst, -1, "submit", wiA)
	capA := w113Capsule(t, inst, defaultWS, wiA)
	w113WaitCapsule(t, capA, 60*time.Second)
	w113WaitCompleted(t, capA, 120*time.Second)

	// The override: an explicit --config with a different mapping wins
	// over the standard location.
	otherWS := t.TempDir()
	cfg2 := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: otherWS, projects: []string{"e2e"}}})
	wiB := w113NewWI(t, inst, "e2e", "w113 explicit-config-override claim", envelopeAutopilot())
	d2 := w113NewDaemon(t, inst, w113DOptConfig(cfg2))
	d2.start()
	defer d2.stop()
	d2.pfWant(inst, -1, "submit", wiB)
	capB := w113Capsule(t, inst, otherWS, wiB)
	w113WaitCapsule(t, capB, 60*time.Second)
	w113WaitCompleted(t, capB, 120*time.Second)
}

// ---------------------------------------------------------------------------
// S10 — zero [workspace] sections: daemon refuses startup with
// guidance, before listening.
// ---------------------------------------------------------------------------

// TestW113DaemonNoMappingRefusesStartup pins criterion #7: a config
// with zero `[workspace]` sections (the fresh-install templated shape)
// makes the daemon refuse to start with guidance pointing at the
// `[workspace]` shape and `pf doctor`, BEFORE the socket exists.
func TestW113DaemonNoMappingRefusesStartup(t *testing.T) {
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		"http://127.0.0.1:1", "w113key", "", nil)
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	served, output, code := d.boot(30 * time.Second)
	if served {
		d.stop()
		t.Fatal("daemon started serving with zero [workspace] sections")
	}
	if code != w113ExitNeedsOperator {
		t.Fatalf("daemon with no mapping: exit %d, want 78 (output %q)", code, output)
	}
	for _, tok := range []string{"[workspace", "doctor"} {
		if !strings.Contains(output, tok) {
			t.Fatalf("daemon with no mapping: refusal misses the %q guidance token (output %q)", tok, output)
		}
	}
	if d.socketExists() {
		t.Fatal("daemon with no mapping still created its socket before refusing")
	}
}

// ---------------------------------------------------------------------------
// S11 — every mapped workspace validated at launch.
// ---------------------------------------------------------------------------

// TestW113DaemonValidatesAllMappedWorkspaces pins the first half of
// criterion #8: startup fails naming a mapped workspace whose directory
// is missing on disk. (The "daemon writes only capsules/ inside each"
// half is asserted live in S01/S12 after real claims.)
func TestW113DaemonValidatesAllMappedWorkspaces(t *testing.T) {
	existing := t.TempDir()
	missing := filepath.Join(t.TempDir(), "w113gone")
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		"http://127.0.0.1:1", "w113key", "",
		[]w113WS{
			{key: existing, projects: []string{"e2e"}},
			{key: missing, projects: []string{"acme-ops"}},
		})
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	served, output, code := d.boot(30 * time.Second)
	if served {
		d.stop()
		t.Fatalf("daemon started with a missing mapped workspace %s", missing)
	}
	if code != w113ExitNeedsOperator {
		t.Fatalf("daemon with missing workspace: exit %d, want 78 (output %q)", code, output)
	}
	if !strings.Contains(output, "w113gone") {
		t.Fatalf("daemon with missing workspace: refusal does not name it (output %q)", output)
	}
	if d.socketExists() {
		t.Fatal("daemon with a missing workspace still created its socket")
	}
}

// ---------------------------------------------------------------------------
// S12/S16 — cross-project claims route per workspace; projections tell
// the multi-workspace truth.
// ---------------------------------------------------------------------------

// TestW113CrossProjectClaimsRoutePerWorkspace pins criteria #9 and #12
// (and the live halves of #8 and #14): one daemon, workspace A mapping
// `e2e` and workspace B mapping `acme-ops`, claims land the capsules at
// A/capsules/<project>-<seq> and B/capsules/<project>-<seq>, each workspace's top level
// holds only capsules/, `pf ops status` names BOTH workspaces, and the
// WI-anchored `pf watch --once` works with no project anywhere (unchanged
// face).
func TestW113CrossProjectClaimsRoutePerWorkspace(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")
	wsA := filepath.Join(t.TempDir(), "w113wsA")
	wsB := filepath.Join(t.TempDir(), "w113wsB")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{
			{key: wsA, projects: []string{"e2e"}},
			{key: wsB, projects: []string{"acme-ops"}},
		})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	defer d.stop()

	wiA := w113NewWI(t, inst, "e2e", "w113 neighborhood A claim", envelopeAutopilot())
	wiB := w113NewWI(t, inst, "acme-ops", "w113 neighborhood B claim", envelopeAutopilot())
	d.pfWant(inst, -1, "submit", wiA)
	d.pfWant(inst, -1, "submit", wiB)

	capA := w113Capsule(t, inst, wsA, wiA)
	capB := w113Capsule(t, inst, wsB, wiB)
	w113WaitCapsule(t, capA, 60*time.Second)
	w113WaitCapsule(t, capB, 60*time.Second)
	w113WaitCompleted(t, capA, 120*time.Second)
	w113WaitCompleted(t, capB, 120*time.Second)
	inst.waitWIStatus(t, wiA, "wrapped", 60*time.Second)
	inst.waitWIStatus(t, wiB, "wrapped", 60*time.Second)

	// The capsules live in their own neighborhoods, and each workspace
	// holds ONLY the capsules/ the daemon created.
	if got := w113TopLevel(t, wsA); len(got) != 1 || got[0] != "capsules" {
		t.Fatalf("workspace A top level = %v, want only [capsules]", got)
	}
	if got := w113TopLevel(t, wsB); len(got) != 1 || got[0] != "capsules" {
		t.Fatalf("workspace B top level = %v, want only [capsules]", got)
	}

	// Projections tell the multi-workspace truth: `pf ops status` names
	// BOTH mapped workspaces — no single-workspace lie.
	opsOut := d.pfWant(inst, -1, "ops", "status")
	for _, ws := range []string{wsA, wsB} {
		if !strings.Contains(opsOut, ws) {
			t.Fatalf("pf ops status names only one workspace (%s missing): %q", ws, opsOut)
		}
	}

	// WI-anchored verbs need no project: watch --once works exactly as
	// before, deriving the project from the WI via the hub.
	d.pfWant(inst, -1, "watch", wiA, "--once")
}

// ---------------------------------------------------------------------------
// S13 — unmapped project claim refused loudly, before any side effect.
// ---------------------------------------------------------------------------

// TestW113UnmappedProjectClaimRefused pins criterion #10: a claim for a work item whose project exists on aihub but is NOT in
// the machine's mapping is rejected with an error naming the project
// and pointing at the mapping / `pf doctor`, BEFORE any capsule is
// created and before the aihub-side claim state changes.
func TestW113UnmappedProjectClaimRefused(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "ghostproj") // exists on aihub, deliberately unmapped
	wsA := filepath.Join(t.TempDir(), "w113wsA")
	if err := os.MkdirAll(wsA, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: wsA, projects: []string{"e2e"}}})
	d := w113NewDaemon(t, inst, w113DOptConfig(cfg))
	d.start()
	defer d.stop()

	wi := w113NewWI(t, inst, "ghostproj", "w113 unmapped-project claim refusal", envelopeAutopilot())
	before := inst.wiStatus(t, wi)
	for _, argv := range [][]string{
		{"submit", wi},
	} {
		out, code := d.pf(inst, argv...)
		if code != 1 {
			t.Fatalf("pf %v: exit %d, want 1 (output %q)", argv, code, out)
		}
		if !strings.Contains(out, "ghostproj") {
			t.Fatalf("pf %v: rejection does not name the unmapped project (output %q)", argv, out)
		}
		if !strings.Contains(out, "doctor") && !strings.Contains(out, "[workspace") && !strings.Contains(out, "mapping") {
			t.Fatalf("pf %v: rejection does not point at the mapping (output %q)", argv, out)
		}
	}
	// No capsule was created for the unmapped project anywhere.
	if _, err := os.Stat(w113Capsule(t, inst, wsA, wi)); err == nil {
		t.Fatalf("a capsule was created for unmapped project ghostproj at %s", w113Capsule(t, inst, wsA, wi))
	}
	// The server-side claim state is unchanged.
	if after := inst.wiStatus(t, wi); after != before {
		t.Fatalf("aihub-side status of %s changed under the refused claim: %q -> %q", wi, before, after)
	}
}

// ---------------------------------------------------------------------------
// S14 — in-flight capsules keep their homes across a crash-restart.
// ---------------------------------------------------------------------------

// TestW113InflightCapsulesKeepHomesAcrossRestart pins criterion #11's
// cutover story: a mid-flight capsule under A/capsules/<project>-<seq> survives a
// daemon crash and restart on the SAME mapping — the restarted daemon
// reconciles in place (marker file intact, no second capsule anywhere)
// and the work item wraps. No migration path is needed or shipped.
func TestW113InflightCapsulesKeepHomesAcrossRestart(t *testing.T) {
	inst := newV1Install(t)
	wsA := filepath.Join(t.TempDir(), "w113wsA")
	if err := os.MkdirAll(wsA, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: wsA, projects: []string{"e2e"}}})
	sock := filepath.Join(shortTmp(t, "w113sock"), "d.sock")
	dataDir := t.TempDir()

	wi := inst.newWI(t, "w113 in-flight capsule keeps its home",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 4000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := w113NewDaemon(t, inst,
		w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d.start()
	d.pfWant(inst, -1, "submit", wi)

	capDir := w113Capsule(t, inst, wsA, wi)
	w113WaitCapsule(t, capDir, 60*time.Second)
	w113StepStatus(t, capDir, "fix", "in_progress", 60*time.Second)
	marker := filepath.Join(capDir, "w113-marker.txt")
	if err := os.WriteFile(marker, []byte("in flight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d.crash()

	// Restart on the SAME socket/data/mapping/machine: recovery must
	// find the capsule in place and finish the workflow.
	d2 := w113NewDaemon(t, inst,
		w113DOptConfig(cfg), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d2.start()
	defer d2.stop()
	w113WaitCompleted(t, capDir, 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)

	// The capsule never moved and no second one appeared.
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "in flight\n" {
		t.Fatalf("marker file lost or changed: %q, %v", raw, err)
	}
	if got := w113TopLevel(t, filepath.Join(wsA, "capsules")); len(got) != 1 || got[0] != wiPathName(t, inst, wi) {
		t.Fatalf("capsules dir of %s holds %v, want exactly [%s]", wsA, got, wi)
	}
}

// ---------------------------------------------------------------------------
// S15 — unmapped mid-flight WI fails loudly; the capsule is not
// relocated.
// ---------------------------------------------------------------------------

// TestW113UnmappedMidflightWIBlocksThenResumes pins criterion #11's failure
// posture: a mid-flight capsule for a workspace the operator failed to
// re-add to the mapping does not stop the daemon — that work item alone is
// set to the ordinary aihub `blocked` status with a reason naming the
// project, nothing stays parked locally, and the capsule is NOT relocated to
// another workspace. Once the mapping is restored, `pf resume` re-claims it
// and the workflow finishes.
func TestW113UnmappedMidflightWIBlocksThenResumes(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")
	wsA := filepath.Join(t.TempDir(), "w113wsA")
	wsX := filepath.Join(t.TempDir(), "w113wsX")
	for _, ws := range []string{wsA, wsX} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fullCfgPath := filepath.Join(t.TempDir(), "cli.toml")
	w113ConfigFile(t, fullCfgPath, inst.url, inst.adminKey, "",
		[]w113WS{
			{key: wsA, projects: []string{"e2e"}},
			{key: wsX, projects: []string{"acme-ops"}},
		})
	sock := filepath.Join(shortTmp(t, "w113sock"), "d.sock")
	dataDir := t.TempDir()

	wi := w113NewWI(t, inst, "acme-ops", "w113 unmapped mid-flight work item",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 4000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		})
	d := w113NewDaemon(t, inst,
		w113DOptConfig(fullCfgPath), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d.start()
	d.pfWant(inst, -1, "submit", wi)
	capX := w113Capsule(t, inst, wsX, wi)
	w113WaitCapsule(t, capX, 60*time.Second)
	w113StepStatus(t, capX, "fix", "in_progress", 60*time.Second)
	d.crash()

	// The operator rewrote the config WITHOUT wsX's mapping (the failed
	// re-add): the daemon still starts and blocks that work item alone.
	w113ConfigFile(t, fullCfgPath, inst.url, inst.adminKey, "",
		[]w113WS{{key: wsA, projects: []string{"e2e"}}})
	d2 := w113NewDaemon(t, inst,
		w113DOptConfig(fullCfgPath), w113DOptSock(sock), w113DOptDataDir(dataDir))
	served, output, _ := d2.boot(30 * time.Second)
	if !served {
		t.Fatalf("an unmapped mid-flight project must not stop the daemon: %s", output)
	}
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	if raw := wiRawWithEvents(t, inst, wi); !strings.Contains(raw, "acme-ops") || !strings.Contains(raw, "[workspace] mapping") {
		t.Fatalf("the blocked reason does not name the project: %s", raw)
	}
	slug := wiSlug(t, inst, wi) // ops status names the WI by its slug
	waitUntil(t, 20*time.Second, "the unmapped WI is still held by the daemon", func() bool {
		snap, _ := d2.pf(inst, "ops", "status")
		return !strings.Contains(snap, slug)
	})

	// No relocation: the capsule stays under wsX, nothing appears under
	// any other workspace.
	if _, err := os.Stat(capX); err != nil {
		t.Fatalf("the original capsule disappeared from %s: %v", capX, err)
	}
	if _, err := os.Stat(w113Capsule(t, inst, wsA, wi)); err == nil {
		t.Fatalf("the mid-flight capsule was relocated to %s", w113Capsule(t, inst, wsA, wi))
	}

	// The operator restores the mapping and the human resumes: the work
	// item is re-claimed under its project's workspace and finishes.
	d2.stop()
	w113ConfigFile(t, fullCfgPath, inst.url, inst.adminKey, "",
		[]w113WS{
			{key: wsA, projects: []string{"e2e"}},
			{key: wsX, projects: []string{"acme-ops"}},
		})
	d3 := w113NewDaemon(t, inst,
		w113DOptConfig(fullCfgPath), w113DOptSock(sock), w113DOptDataDir(dataDir))
	d3.start()
	defer d3.stop()
	d3.pfWant(inst, 0, "resume", wi)
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)
	if _, err := os.Stat(w113Capsule(t, inst, wsA, wi)); err == nil {
		t.Fatalf("the resumed work item was placed under %s", w113Capsule(t, inst, wsA, wi))
	}
}

// ---------------------------------------------------------------------------
// S17 — placement verbs without --project fail with mapping guidance.
// ---------------------------------------------------------------------------

// TestW113PlacementVerbsRequireProject pins criterion #13: every
// placement verb without --project fails stating --project is required.
// `pf file create` also points at the mapping (`[workspace]` sections /
// `pf doctor`); the remember / recall required-error carries no mapping
// mention (the mapping check runs on a given --project, S18).
func TestW113PlacementVerbsRequireProject(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	out, code := w113Pf(t, inst, w113Env{config: cfg}, "file", "create", "--goal", "w113 no project")
	if code != 1 {
		t.Fatalf("pf file create: exit %d, want 1 (output %q)", code, out)
	}
	if !strings.Contains(out, "project") || !strings.Contains(out, "required") {
		t.Fatalf("pf file create: error does not state --project is required (output %q)", out)
	}
	if !strings.Contains(out, "[workspace") && !strings.Contains(out, "doctor") && !strings.Contains(out, "mapping") {
		t.Fatalf("pf file create: error does not point at the mapping (output %q)", out)
	}
	for _, argv := range [][]string{
		{"remember", "--type", "experience.debug", "w113 no project memory"},
		{"recall", "w113 no project query"},
	} {
		out, code := w113Pf(t, inst, w113Env{config: cfg}, argv...)
		if code != 1 {
			t.Fatalf("pf %v: exit %d, want 1 (output %q)", argv, code, out)
		}
		if !strings.Contains(out, "project") || !strings.Contains(out, "required") {
			t.Fatalf("pf %v: error does not state --project is required (output %q)", argv, out)
		}
	}
}

// ---------------------------------------------------------------------------
// S18 — placement verbs validate --project against the mapping;
// WI-anchored verbs unchanged.
// ---------------------------------------------------------------------------

// TestW113PlacementVerbsValidateProject pins criterion #14: placement
// verbs with an unmapped --project fail with a naming error; with a
// mapped --project they succeed. (The WI-anchored-verbs half lives in
// S12's observe check.)
func TestW113PlacementVerbsValidateProject(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})

	// Unmapped: every placement verb (file create, remember, recall —
	// repo review 2026-10-16 D-5; the owner superseded the 2026-10-13
	// C15 "not mapping-restricted" ruling on 2026-10-16) fails naming the
	// project and the mapping requirement, even for an aihub-known project.
	w113EnsureProject(t, inst, "w113offmap")
	for _, argv := range [][]string{
		{"file", "create", "--project", "w113offmap", "--goal", "w113 unmapped placement"},
		{"remember", "--project", "w113offmap", "--type", "experience.debug", "w113 unmapped memory"},
		{"recall", "--project", "w113offmap", "w113 unmapped query"},
	} {
		out, code := w113Pf(t, inst, w113Env{config: cfg}, argv...)
		if code != 1 {
			t.Fatalf("pf %v (unmapped): exit %d, want 1 (output %q)", argv, code, out)
		}
		if !strings.Contains(out, "w113offmap") {
			t.Fatalf("pf %v (unmapped): error does not name the unmapped project (output %q)", argv, out)
		}
		if !strings.Contains(out, "[workspace") && !strings.Contains(out, "doctor") && !strings.Contains(out, "mapping") {
			t.Fatalf("pf %v (unmapped): error does not point at the mapping (output %q)", argv, out)
		}
	}

	// Mapped: each verb succeeds.
	out := w113PfWant(t, inst, -1, w113Env{config: cfg},
		"file", "create", "--project", "e2e", "--goal", "w113 mapped placement goal")
	if !strings.Contains(out, "w113 mapped placement goal") {
		t.Fatalf("pf file create (mapped): output does not carry the created item (output %q)", out)
	}
	w113PfWant(t, inst, -1, w113Env{config: cfg},
		"remember", "--project", "e2e", "--type", "experience.debug", "w113 mapped memory")
	w113PfWant(t, inst, -1, w113Env{config: cfg},
		"recall", "--project", "e2e", "w113 mapped query")
}

// ---------------------------------------------------------------------------
// S19 — queue without --project: one merged presentation, per-row
// project identity.
// ---------------------------------------------------------------------------

// TestW113QueueMergedAllMapped pins the first half of criterion #15:
// `pf queue` without --project lists work items from ALL mapped
// projects in one merged presentation with each row's project
// identifiable, and `pf --json queue` emits one merged items array in
// which every record carries its project.
func TestW113QueueMergedAllMapped(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e", "acme-ops"}}})
	goalA := "w113 merged queue presentation alpha"
	goalB := "w113 merged queue presentation beta"
	w113NewWI(t, inst, "e2e", goalA, nil)
	w113NewWI(t, inst, "acme-ops", goalB, nil)

	out := w113PfWant(t, inst, -1, w113Env{config: cfg}, "queue")
	for _, tok := range []string{goalA, goalB, "e2e", "acme-ops"} {
		if !strings.Contains(out, tok) {
			t.Fatalf("pf queue (all mapped) output misses %q — the merged presentation must identify each row's project: %q", tok, out)
		}
	}

	jout := w113PfWant(t, inst, -1, w113Env{config: cfg, json: true}, "queue")
	type w113Item struct {
		Project string `json:"project"`
	}
	var items []w113Item
	var wrapper struct {
		Items []w113Item `json:"items"`
	}
	if err := json.Unmarshal([]byte(w113JSONDocOf(jout)), &wrapper); err == nil && len(wrapper.Items) > 0 {
		items = wrapper.Items
	} else {
		var bare []w113Item
		if err := json.Unmarshal([]byte(w113JSONDocOf(jout)), &bare); err == nil && len(bare) > 0 {
			items = bare
		}
	}
	if len(items) < 2 {
		t.Fatalf("pf --json queue (all mapped) emitted %d item(s), want the merged items of both projects (output %q)", len(items), jout)
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Project == "" {
			t.Fatalf("a merged queue item record carries no project identity (output %q)", jout)
		}
		seen[it.Project] = true
	}
	if !seen["e2e"] || !seen["acme-ops"] {
		t.Fatalf("the merged queue items do not cover all mapped projects (seen %v, output %q)", seen, jout)
	}
}

// ---------------------------------------------------------------------------
// S20 — queue --project passes through unvalidated.
// ---------------------------------------------------------------------------

// TestW113QueueProjectPassThrough pins the second half of criterion
// #15: `pf queue --project X` passes X to aihub unvalidated (mapped or
// not) — the mapping governs placement, not knowledge. The flag-only
// half (no config in play) is the kept face, green before AND after;
// the config-bearing half is the new bar.
func TestW113QueueProjectPassThrough(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "extraproj")
	goal := "w113 pass-through extraproj item"
	w113NewWI(t, inst, "extraproj", goal, nil)

	// Kept face: no config, pure flag+env — works before and after.
	out := w113PfWant(t, inst, -1, w113Env{}, "queue", "--project", "extraproj")
	if !strings.Contains(out, goal) {
		t.Fatalf("pf queue --project extraproj (no config): the project's items are not shown (output %q)", out)
	}
	// A project aihub itself does not know: aihub's own answer is
	// surfaced — never a local mapping rejection.
	out2, code2 := w113Pf(t, inst, w113Env{}, "queue", "--project", "w113nothere")
	if code2 != 0 && code2 != 1 {
		t.Fatalf("pf queue --project w113nothere: exit %d, want aihub's own answer (0 or 1; output %q)", code2, out2)
	}
	for _, tok := range []string{"[workspace", "doctor", "mapping"} {
		if strings.Contains(out2, tok) {
			t.Fatalf("pf queue --project w113nothere: local mapping rejection surfaced for a read-only query (token %q, output %q)", tok, out2)
		}
	}

	// New bar: with a mapping-bearing config, an unmapped --project
	// still passes through unvalidated.
	ws := t.TempDir()
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	out3 := w113PfWant(t, inst, -1, w113Env{config: cfg}, "queue", "--project", "extraproj")
	if !strings.Contains(out3, goal) {
		t.Fatalf("pf queue --project extraproj (mapping-bearing config): pass-through did not show the project's items (output %q)", out3)
	}
}

// ---------------------------------------------------------------------------
// S21 — queue without --project and without mapping fails loudly.
// ---------------------------------------------------------------------------

// TestW113QueueNoMappingFailsLoudly pins criterion #16: `pf queue`
// without --project and without any mapping fails with guidance to add
// a mapping or pass --project — it does not silently list nothing.
func TestW113QueueNoMappingFailsLoudly(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		"http://127.0.0.1:1", "w113key", "", nil)
	out, code := w113Pf(t, inst, w113Env{config: cfg, noConn: true}, "queue")
	if code != 1 {
		t.Fatalf("pf queue with no mapping: exit %d, want 1 (output %q)", code, out)
	}
	if !strings.Contains(out, "mapping") && !strings.Contains(out, "[workspace") && !strings.Contains(out, "doctor") {
		t.Fatalf("pf queue with no mapping: no guidance to add a mapping or pass --project (output %q)", out)
	}
}

// ---------------------------------------------------------------------------
// S23 — the auto-pull iterates all mapped projects.
// ---------------------------------------------------------------------------

// TestW113DrainIteratesAllMappedProjects pins criterion #17's daemon
// half: a daemon with auto on fetches the ready queue and iterates ALL
// mapped projects — no daemon-level project scope exists anymore.
func TestW113DrainIteratesAllMappedProjects(t *testing.T) {
	inst := newV1Install(t)
	w113EnsureProject(t, inst, "acme-ops")
	wsA := filepath.Join(t.TempDir(), "w113wsA")
	wsB := filepath.Join(t.TempDir(), "w113wsB")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
		inst.url, inst.adminKey, "",
		[]w113WS{
			{key: wsA, projects: []string{"e2e"}},
			{key: wsB, projects: []string{"acme-ops"}},
		})
	wiA := w113NewWI(t, inst, "e2e", "w113 drain across workspaces alpha", envelopeAutopilot())
	wiB := w113NewWI(t, inst, "acme-ops", "w113 drain across workspaces beta", envelopeAutopilot())

	d := w113NewDaemon(t, inst, w113DOptConfig(cfg), w113DOptArgs("--auto"))
	d.start()
	defer d.stop()

	capA := w113Capsule(t, inst, wsA, wiA)
	capB := w113Capsule(t, inst, wsB, wiB)
	w113WaitCapsule(t, capA, 90*time.Second)
	w113WaitCapsule(t, capB, 90*time.Second)
	w113WaitCompleted(t, capA, 240*time.Second)
	w113WaitCompleted(t, capB, 240*time.Second)
	inst.waitWIStatus(t, wiA, "wrapped", 90*time.Second)
	inst.waitWIStatus(t, wiB, "wrapped", 90*time.Second)
}

// ---------------------------------------------------------------------------
// S24 — doctor: healthy machine.
// ---------------------------------------------------------------------------

// TestW113DoctorHealthyMachine pins criteria #18/#19's healthy face:
// with a valid invertible mapping, a reachable aihub, a responding
// daemon socket and working auth, every check passes and the exit code
// is 0 (human and --json; the JSON emits ok=true with no failing
// check).
func TestW113DoctorHealthyMachine(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	d := w113NewDaemon(t, inst, w113DOptConfig(cfgPath))
	w113ConfigFile(t, cfgPath, inst.url, inst.adminKey, d.sock,
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	d.start()
	defer d.stop()

	out := w113PfWant(t, inst, -1, w113Env{config: cfgPath}, "doctor")
	if out == "" {
		t.Fatal("pf doctor (healthy, human): no output")
	}
	jout := w113PfWant(t, inst, -1, w113Env{config: cfgPath, json: true}, "doctor")
	doc := w113DoctorJSON(t, jout)
	if !doc.OK {
		t.Fatalf("pf doctor --json (healthy): ok is false (checks %+v, output %q)", doc.Checks, jout)
	}
	for _, c := range doc.Checks {
		switch c.Name {
		case "claude progress display", "pi progress display",
			"claude install layout", "pi install layout", "v1 harness leftovers":
			continue // the harness family (polyforge#142, #174): no harness in the scratch HOME
		case "daemon supervision":
			// polyforge#174: this test runs its daemon directly, not under
			// the tmux restart loop — a warn, never a fail
			if c.Status == "fail" {
				t.Fatalf("daemon supervision failed: %s", c.Detail)
			}
			continue
		}
		if c.Status != "pass" {
			t.Fatalf("pf doctor --json (healthy): check %q is %q, want pass (output %q)", c.Name, c.Status, jout)
		}
	}
}

// ---------------------------------------------------------------------------
// S25 — doctor: the four mapping checks fail distinctly.
// ---------------------------------------------------------------------------

// TestW113DoctorMappingChecks pins criterion #18's mapping half: each
// of the four mapping failure shapes fails its check distinctly —
// mapping absent (with guidance), workspace dir missing (named),
// project unknown to aihub (named with its workspace), ambiguous
// inverse (both workspaces named) — and the exit code is non-zero.
func TestW113DoctorMappingChecks(t *testing.T) {
	inst := newV1Install(t) // (c) needs a real aihub with its project set
	cases := []struct {
		name  string
		ws    []w113WS
		need  []string // tokens the output must carry
		loose []string
	}{
		{
			name:  "mapping absent",
			ws:    nil,
			loose: []string{"workspace", "mapping"},
		},
		{
			name: "workspace dir missing",
			ws: []w113WS{
				{key: t.TempDir(), projects: []string{"e2e"}},
				{key: filepath.Join(t.TempDir(), "w113missing"), projects: []string{"acme-ops"}},
			},
			need: []string{"w113missing"},
		},
		{
			name: "project unknown to aihub",
			ws: []w113WS{
				{key: filepath.Join(t.TempDir(), "w113unk"), projects: []string{"w113notonhub"}},
			},
			need: []string{"w113notonhub", "w113unk"},
		},
		{
			name: "ambiguous inverse",
			ws: []w113WS{
				{key: filepath.Join(t.TempDir(), "w113ambA"), projects: []string{"acme-ops"}},
				{key: filepath.Join(t.TempDir(), "w113ambB"), projects: []string{"acme-ops"}},
			},
			need: []string{"w113ambA", "w113ambB"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
				inst.url, inst.adminKey, filepath.Join(t.TempDir(), "dead.sock"), c.ws)
			jout, code := w113Pf(t, inst, w113Env{config: cfg, json: true}, "doctor")
			if code != 1 {
				t.Fatalf("pf doctor (%s): exit %d, want 1 (output %q)", c.name, code, jout)
			}
			doc := w113DoctorJSON(t, jout)
			if doc.OK {
				t.Fatalf("pf doctor (%s): ok is true with a failing mapping (output %q)", c.name, jout)
			}
			// The named tokens ride the checks' name+detail text (the
			// spec pins the shape, not the names — see the doc's
			// under-specification #3).
			for _, tok := range c.need {
				if !strings.Contains(jout, tok) {
					t.Fatalf("pf doctor (%s): output misses the named token %q (output %q)", c.name, tok, jout)
				}
			}
			if len(c.loose) > 0 {
				found := false
				for _, tok := range c.loose {
					if strings.Contains(jout, tok) {
						found = true
					}
				}
				if !found {
					t.Fatalf("pf doctor (%s): no guidance token %v in output %q", c.name, c.loose, jout)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// S26 — doctor: connectivity checks fail distinctly.
// ---------------------------------------------------------------------------

// TestW113DoctorConnectivityChecks pins criterion #18's connectivity
// half: server reach, daemon socket and auth each fail their check in
// isolation, and the exit code is non-zero.
func TestW113DoctorConnectivityChecks(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	cases := []struct {
		name   string
		server string
		apiKey string
		socket string
		needle []string // identifies the distinct failing check
	}{
		{
			name:   "server unreachable",
			server: "http://127.0.0.1:59998",
			apiKey: inst.adminKey,
			socket: filepath.Join(t.TempDir(), "dead.sock"),
			needle: []string{"server", "aihub", "reach", "59998"},
		},
		{
			name:   "daemon socket dead",
			server: inst.url,
			apiKey: inst.adminKey,
			socket: filepath.Join(t.TempDir(), "w113deadsock"),
			needle: []string{"sock"},
		},
		{
			name:   "api key rejected",
			server: inst.url,
			apiKey: "w113badkey",
			socket: filepath.Join(t.TempDir(), "dead.sock"),
			needle: []string{"auth", "401", "unauthor", "api"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
				c.server, c.apiKey, c.socket,
				[]w113WS{{key: ws, projects: []string{"e2e"}}})
			// noConn: the config's own server/key are the values under
			// test (env would override them).
			jout, code := w113Pf(t, inst, w113Env{config: cfg, json: true, noConn: true}, "doctor")
			if code != 1 {
				t.Fatalf("pf doctor (%s): exit %d, want 1 (output %q)", c.name, code, jout)
			}
			doc := w113DoctorJSON(t, jout)
			if doc.OK {
				t.Fatalf("pf doctor (%s): ok is true (output %q)", c.name, jout)
			}
			if w113FailingCheck(doc, c.needle...) == nil {
				t.Fatalf("pf doctor (%s): no failing check identifies %v (checks %+v, output %q)",
					c.name, c.needle, doc.Checks, jout)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// S27 — doctor on an unloadable config does not fake green.
// ---------------------------------------------------------------------------

// TestW113DoctorUnloadableConfig pins criterion #20: an old-shape
// (flat project key) config makes `pf doctor` report the config failure
// and migration guidance (the retired key named), exit 1, and report
// the config-dependent checks as not-run (skip) rather than silently
// passing.
func TestW113DoctorUnloadableConfig(t *testing.T) {
	inst := newV1Install(t)
	oldShape := "server = " + strconv.Quote(inst.url) + "\n" +
		"api_key = \"w113key\"\n" +
		"user_id = \"usr_root\"\n" +
		"project = \"e2e\"\n"
	cfgPath := filepath.Join(t.TempDir(), "cli.toml")
	if err := os.WriteFile(cfgPath, []byte(oldShape), 0o600); err != nil {
		t.Fatal(err)
	}
	jout, code := w113Pf(t, inst, w113Env{config: cfgPath, json: true, noConn: true}, "doctor")
	if code != 1 {
		t.Fatalf("pf doctor (old-shape config): exit %d, want 1 (output %q)", code, jout)
	}
	if !strings.Contains(jout, "project") {
		t.Fatalf("pf doctor (old-shape config): the retired project key is not named in the failure (output %q)", jout)
	}
	doc := w113DoctorJSON(t, jout)
	if doc.OK {
		t.Fatalf("pf doctor (old-shape config): ok is true (output %q)", jout)
	}
	skipped := 0
	for _, c := range doc.Checks {
		if c.Status == "skip" {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatalf("pf doctor (old-shape config): no check reported as skip — config-dependent checks must not fake green (checks %+v, output %q)", doc.Checks, jout)
	}
}

// ---------------------------------------------------------------------------
// S28 — doctor usage errors exit 2.
// ---------------------------------------------------------------------------

// TestW113DoctorUsageErrors pins the usage-error half of criterion #19
// (exit 2) — a kept face: an unknown verb exits 2 today, and an unknown
// doctor flag exits 2 after the change. Green before and after.
func TestW113DoctorUsageErrors(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	out, code := w113Pf(t, inst, w113Env{}, "doctor", "--bogus")
	if code != 2 {
		t.Fatalf("pf doctor --bogus: exit %d, want 2 (usage error; output %q)", code, out)
	}
}

// ---------------------------------------------------------------------------
// S29 — the legacy config fallback location is not read.
// ---------------------------------------------------------------------------

// TestW113LegacyConfigLocationDropped pins criterion #21: with
// ~/.polyforge/config.toml present and the XDG cli.toml absent, the
// legacy file is not read — the command proceeds on the documented
// default server (its diagnostics name the default, never the legacy
// URL).
func TestW113LegacyConfigLocationDropped(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	home := t.TempDir()
	legacyDir := filepath.Join(home, ".polyforge")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "server = \"http://127.0.0.1:59999\"\n" +
		"api_key = \"w113key\"\n" +
		"project = \"e2e\"\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "config.toml"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	// The XDG config dir exists but carries NO polyforge/cli.toml.
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, code := w113Pf(t, inst, w113Env{home: home, noConn: true}, "queue", "--project", "e2e")
	if code == 0 {
		t.Fatalf("pf queue against an unreachable-by-design server exited 0 (output %q)", out)
	}
	if strings.Contains(out, "59999") {
		t.Fatalf("the legacy config location was read: diagnostics name the legacy URL (output %q)", out)
	}
	if !strings.Contains(out, "8420") {
		t.Fatalf("diagnostics do not name the documented default server (output %q)", out)
	}
}

// ---------------------------------------------------------------------------
// S30 — install.sh leaves an old-shape config untouched.
// ---------------------------------------------------------------------------

// TestW113InstallOldShapeUntouched pins criterion #22: with an existing
// old-shape cli.toml (flat project key), install.sh never rewrites the
// file (bytes identical) and prints the migration steps instead.
func TestW113InstallOldShapeUntouched(t *testing.T) {
	ic := w113InstallPrep(t)
	cfgPath := filepath.Join(ic.home, ".config", "polyforge", "cli.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	oldShape := "server = \"http://127.0.0.1:8080\"\n" +
		"api_key = \"w113key\"\n" +
		"daemon_socket = \"" + filepath.Join(ic.home, ".local", "share", "polyforge", "daemon.sock") + "\"\n" +
		"project = \"e2e\"\n"
	if err := os.WriteFile(cfgPath, []byte(oldShape), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w113KillArgv(ic.home) })

	out, code := w113RunInstall(t, ic)
	if code != 0 {
		t.Fatalf("install.sh (existing old-shape config): exit %d (output %q)", code, out)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config disappeared under install.sh: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("install.sh rewrote the operator's old-shape config:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !strings.Contains(out, "workspace") && !strings.Contains(out, "mapping") {
		t.Fatalf("install.sh printed no migration steps for the old-shape config (output %q)", out)
	}
}

// ---------------------------------------------------------------------------
// S31 — fresh install writes a commented templated [workspace] section.
// ---------------------------------------------------------------------------

// TestW113InstallFreshTemplate pins criterion #23: a fresh install
// writes a cli.toml carrying the flat connection keys plus a COMMENTED
// templated [workspace] section (no real mapping fabricated, no
// uncommented section), tells the operator the daemon refuses to start
// until a real mapping exists, and leaves no serving daemon behind.
func TestW113InstallFreshTemplate(t *testing.T) {
	ic := w113InstallPrep(t)
	t.Cleanup(func() { w113KillArgv(ic.home) })

	out, code := w113RunInstall(t, ic)
	if code != 0 {
		t.Fatalf("install.sh (fresh): exit %d (output %q)", code, out)
	}
	cfgPath := filepath.Join(ic.home, ".config", "polyforge", "cli.toml")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("fresh install wrote no config: %v", err)
	}
	cfgText := string(raw)
	if !strings.Contains(cfgText, "api_key") {
		t.Fatalf("the fresh config carries no flat connection keys (config %q)", cfgText)
	}
	commented, uncommented := false, false
	for _, line := range strings.Split(cfgText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[workspace") {
			uncommented = true
		}
		if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "[workspace") {
			commented = true
		}
	}
	if !commented {
		t.Fatalf("the fresh config carries no commented templated [workspace] section (config %q)", cfgText)
	}
	if uncommented {
		t.Fatalf("the fresh install fabricated a real (uncommented) mapping (config %q)", cfgText)
	}
	combined := out + "\n" + cfgText
	if !strings.Contains(combined, "daemon") || !(strings.Contains(combined, "refus") ||
		strings.Contains(combined, "mapping") || strings.Contains(combined, "workspace")) {
		t.Fatalf("the fresh install does not tell the operator the daemon refuses to start until a real mapping exists (output %q, config %q)", out, cfgText)
	}
	// No daemon left serving without a real mapping.
	if procs := w113ProcArgv(ic.home); len(procs) > 0 {
		t.Fatalf("a daemon process survived a fresh install with no real mapping: %v", procs)
	}
}

// ---------------------------------------------------------------------------
// S32 — install.sh's daemon invocation uses the new flag surface.
// ---------------------------------------------------------------------------

// TestW113InstallDaemonFlagSurface pins criterion #24: when the
// installer launches the daemon, its real argv (read from
// /proc/<pid>/cmdline) carries NEITHER a workspace NOR a project flag —
// the mapping-bearing config is the only workspace source. The daemon
// is torn down by the test.
func TestW113InstallDaemonFlagSurface(t *testing.T) {
	ic := w113InstallPrep(t)
	ws := filepath.Join(ic.home, "w113ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(ic.home, ".config", "polyforge", "cli.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	w113ConfigFile(t, cfgPath, "http://127.0.0.1:8080", "w113key",
		filepath.Join(ic.home, ".local", "share", "polyforge", "daemon.sock"),
		[]w113WS{{key: ws, projects: []string{"e2e"}}})
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w113KillArgv(ic.home) })

	out, code := w113RunInstall(t, ic)
	if code != 0 {
		t.Fatalf("install.sh (real mapping present): exit %d (output %q)", code, out)
	}
	// The mapping-bearing config was not rewritten either.
	after, err := os.ReadFile(cfgPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("install.sh rewrote the mapping-bearing config (err %v)", err)
	}

	argv := w113FindDaemonArgv(t, ic.home, 15*time.Second)
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, w113FlagWorkspaceTok()) {
		t.Fatalf("the installer-launched daemon still carries the retired workspace flag (argv %v)", argv)
	}
	if strings.Contains(joined, w113FlagProjectTok()) {
		t.Fatalf("the installer-launched daemon still carries the retired project flag (argv %v)", argv)
	}
}

// ---------------------------------------------------------------------------
// S33 — the vNext design docs describe the mapping as the current
// contract.
// ---------------------------------------------------------------------------

// TestW113DesignDocsTeachMappingContract pins criterion #25's automated
// floor: the live design docs — reference.md (the current-system
// reference) and 08-capsule-base-migration.md — describe the
// `[workspace."<path>"]` mapping shape. (The current-surface-vs-history
// judgment is the manual document-only review in
// artifacts/e2e-scenarios.md.)
func TestW113DesignDocsTeachMappingContract(t *testing.T) {
	root := v1Root(t)
	docs := []string{
		filepath.Join(root, "docs", "design", "polyforge-vnext", "reference.md"),
		filepath.Join(root, "docs", "design", "polyforge-vnext", "08-capsule-base-migration.md"),
	}
	for _, doc := range docs {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("design doc %s: %v", doc, err)
		}
		if !strings.Contains(string(raw), "[workspace") {
			t.Fatalf("design doc %s does not teach the [workspace.\"<path>\"] mapping shape", doc)
		}
	}
}

// ---------------------------------------------------------------------------
// S34 — SKILL.md and the five verb fragments teach the new contract.
// ---------------------------------------------------------------------------

// TestW113SkillsTeachMappingContract pins criterion #26's automated
// floor: SKILL.md's config-contract section and the file, queue,
// remember and recall fragments all teach the
// `[workspace` mapping shape. (The teaching-quality judgment is the
// manual document-only review in artifacts/e2e-scenarios.md.)
func TestW113SkillsTeachMappingContract(t *testing.T) {
	root := v1Root(t)
	files := []string{
		filepath.Join(root, "skills", "polyforge", "SKILL.md"),
		filepath.Join(root, "skills", "polyforge", "verbs", "file.md"),
		filepath.Join(root, "skills", "polyforge", "verbs", "queue.md"),
		filepath.Join(root, "skills", "polyforge", "verbs", "remember.md"),
		filepath.Join(root, "skills", "polyforge", "verbs", "recall.md"),
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("skill file %s: %v", f, err)
		}
		if !strings.Contains(string(raw), "[workspace") {
			t.Fatalf("skill file %s does not teach the [workspace.\"<path>\"] mapping shape", f)
		}
	}
}

// ---------------------------------------------------------------------------
// S35 — `pf help daemon` teaches the new contract.
// ---------------------------------------------------------------------------

// TestW113HelpDaemonTeachesNewContract pins criterion #26's help face:
// `pf help daemon` documents --config and the mapping requirement
// (mentions workspace) and no longer lists the retired workspace flag,
// project flag, or either retired env variable.
func TestW113HelpDaemonTeachesNewContract(t *testing.T) {
	inst := &v1Install{pfPath: filepath.Join(v1Bins(t), "pf")}
	out, code := w113Pf(t, inst, w113Env{}, "help", "daemon")
	if code != 0 {
		t.Fatalf("pf help daemon: exit %d (output %q)", code, out)
	}
	if !strings.Contains(out, "--config") {
		t.Fatalf("pf help daemon does not document --config (output %q)", out)
	}
	if !strings.Contains(out, "workspace") {
		t.Fatalf("pf help daemon does not mention the workspace mapping requirement (output %q)", out)
	}
	for _, tok := range []string{
		w113FlagWorkspaceTok(),
		w113FlagProjectTok(),
		w113EnvWorkspaceTok(),
		w113EnvProjectTok(),
	} {
		if strings.Contains(out, tok) {
			t.Fatalf("pf help daemon still lists the retired surface token %q (output %q)", tok, out)
		}
	}
}

// ---------------------------------------------------------------------------
// S36 — greppable floor: daemon sources carry no retired env tokens.
// ---------------------------------------------------------------------------

// TestW113DaemonSourcesNoRetiredEnvTokens pins the zero-compat source
// posture: neither retired env variable appears contiguously in the
// daemon's Go source bytes — the refusal copy names them at runtime
// only (assembled from fragments, the #103/#106 greppable-floor
// convention).
func TestW113DaemonSourcesNoRetiredEnvTokens(t *testing.T) {
	root := v1Root(t)
	dir := filepath.Join(root, "internal", "polyforge", "daemon")
	tokens := []string{w113EnvProjectTok(), w113EnvWorkspaceTok()}
	var hits []string
	err := filepath.WalkDir(dir, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, tok := range tokens {
			if strings.Contains(string(raw), tok) {
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel+": "+tok)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk daemon sources: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("retired env tokens still present in daemon sources (%d hits):\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}

// w113ExitNeedsOperator mirrors daemon.ExitNeedsOperator (78): a daemon that
// refuses to start for an operator-fixable reason (config, workspace,
// mapping) exits 78 so its supervision loop stops instead of restarting it
// every 5s (repo review 2026-10-16 B-14). The pf CLI verbs still exit 1.
const w113ExitNeedsOperator = 78
