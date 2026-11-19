package cli

// verbs_supervise.go (polyforge#160, 84-supervise): `pf supervise`, the
// container process supervisor's entry point. The workstation container's
// entry point (84-image) runs it as PID 1's child; it starts and watches
// the daemon, Pi Web and (with --with-aihub) the aihub server through
// supervisor.RunSupervise (the watch loop, the signal forwarding and the
// exit-code rules live there). On a host the daemon's supervision stays
// the tmux loop (`pf daemon ensure`) — this verb replaces it only inside
// the container.
//
// The children are plain subprocesses, never in-process calls, so the
// supervisor owns their whole lifecycle: the daemon argv is the canonical
// launch (`--config`, `-data-dir`, `-workflows-dir`; the socket and the
// machine id resolve inside the daemon exactly as `pf daemon ensure`'s
// own launch would), Pi Web runs `npm run start:lan` in its own checkout
// (a container must bind 0.0.0.0 to be reachable), and the aihub server
// runs `pf aihub` with its own defaults. The daemon child's environment
// is the same allow-list `pf daemon ensure` launches it with (the
// container's credentials never reach it); Pi Web and the aihub server
// see the container's environment. Every child carries
// POLYFORGE_SUPERVISOR=container:pf-supervise — the one fact `pf daemon
// ensure` (no-op) and `pf doctor` (pass) judge the container supervision
// by.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

// superviseUsage is the verb's help text (`pf help supervise`).
const superviseUsage = `pf supervise — the container process supervisor (polyforge#160)

Runs as the workstation container's entry point (84-image): it starts and
watches the daemon, Pi Web and — with --with-aihub — the aihub server as
its own children. A child that dies on it (a crash, a kill -9) is
restarted with a growing delay; the daemon exiting 0 or 78 ends the
whole run with that code (the container stops with it; the 78 reason is
in <data-dir>/daemon.log); a stop signal (SIGTERM/SIGINT, what
docker/podman stop sends) is forwarded ONCE to every child and the daemon
gets the --grace to finish its shutdown (its WIP pushes can take up to
30s) before anything still running is SIGKILLed — the outer container's
stop timeout must be the grace plus a margin.

Flags:
  --config P          shared config file (default ~/.config/polyforge/cli.toml)
  --data-dir D        the Host data dir (default ~/.local/share/polyforge)
  --workflows-dir D   the daemon's workflows dir (default <data-dir>/release/current/workflows)
  --pi-web-dir D      the Pi Web checkout (default <data-dir>/release/current/pi-web);
                      absent → Pi Web is not started (a line says so)
  --grace D           the stop grace, >= 45s (default 60s)
  --with-aihub        start and watch the aihub server too ("pf aihub")

On a host the daemon's supervision stays the tmux loop this verb never
replaces: "pf daemon ensure" there behaves exactly as before.

Exit status: the daemon's own exit code when it ended the run (0 or 78);
0 when a forwarded stop let the daemon finish inside the grace; 1 when
the daemon had to be SIGKILLed at the grace's end; 2 usage.`

// cmdSupervise serves `pf help supervise` (and the command registry's
// Process group): cmd/pf/main.go intercepts `pf supervise` before CLI
// dispatch and calls SuperviseMain, the real entry point.
var cmdSupervise = &Command{
	Name:    "supervise",
	Group:   GroupProcess,
	Summary: "Run the container process supervisor: daemon, Pi Web and (with --with-aihub) the aihub server as watched children (main.go intercepts `pf supervise` before CLI dispatch; this verb serves `pf help supervise` only)",
	Usage:   superviseUsage,
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		// In a correct build this Run is unreachable: cmd/pf/main.go
		// intercepts `pf supervise` argv and calls SuperviseMain. If it
		// is ever reached, the only honest answer is the launch-surface
		// help.
		fmt.Fprint(ctx.Stdout, superviseUsage)
		return nil
	},
}

// superviseOpts is everything planSupervise resolves the children from.
type superviseOpts struct {
	pfBin                                             string
	home, configPath, dataDir, workflowsDir, piWebDir string
	withAIHub                                         bool
	grace                                             time.Duration
	environ                                           []string // supervise's own environment
}

// planSupervise answers the resolved run: the daemon child (the
// canonical launch argv, the ensure allow-list environment, the
// supervisor marker), the Pi Web child when its checkout exists, and
// the aihub child when --with-aihub asked for it.
func planSupervise(o superviseOpts) (supervisor.RunSpec, error) {
	if o.grace < supervisor.MinStopGrace {
		return supervisor.RunSpec{}, fmt.Errorf("the stop grace %s is below the %s minimum — the daemon's shutdown can take up to 30s of WIP pushes, a shorter grace would cut a checkpoint in half", o.grace, supervisor.MinStopGrace)
	}
	daemon := supervisor.ChildSpec{
		Name: "daemon",
		// the canonical launch: --config, -data-dir, -workflows-dir. The
		// socket and the machine id resolve inside the daemon exactly as
		// `pf daemon ensure`'s own launch would (the recorded daemon-id and
		// the Host file's daemon_socket).
		Argv: []string{o.pfBin, "daemon", "--config", o.configPath, "-data-dir", o.dataDir, "-workflows-dir", o.workflowsDir},
		Dir:  o.home,
		Env:  supervisor.DaemonLaunchEnv(o.home, o.environ, supervisor.MarkerContainerValue),
		Log:  filepath.Join(o.dataDir, "daemon.log"),
	}
	rs := supervisor.RunSpec{Daemon: daemon, Grace: o.grace}
	// Pi Web is started from its checkout when there is one: a container
	// must bind 0.0.0.0 to be reachable, so start:lan, not the loopback
	// form. An image that ships no Pi Web simply runs without it.
	if st, err := os.Stat(o.piWebDir); err == nil && st.IsDir() {
		rs.Others = append(rs.Others, supervisor.ChildSpec{
			Name: "pi-web", Argv: []string{"npm", "run", "start:lan"}, Dir: o.piWebDir,
			Env: superviseChildEnv(o.environ), Log: filepath.Join(o.dataDir, "pi-web.log"),
		})
	}
	if o.withAIHub {
		rs.Others = append(rs.Others, supervisor.ChildSpec{
			Name: "aihub", Argv: []string{o.pfBin, "aihub"}, Dir: o.home,
			Env: superviseChildEnv(o.environ), Log: filepath.Join(o.dataDir, "aihub.log"),
		})
	}
	return rs, nil
}

// SuperviseMain executes `pf supervise <flags>` and answers the process
// exit code the container stops with (the daemon's own 0/78, a clean
// forwarded stop's 0, a grace-exceeded SIGKILL's 1, usage's 2).
func SuperviseMain(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pf supervise", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "shared config file (default ~/.config/polyforge/cli.toml)")
	dataDir := fs.String("data-dir", "", "the Host data dir (default ~/.local/share/polyforge)")
	wfDir := fs.String("workflows-dir", "", "the daemon's workflows dir (default <data-dir>/release/current/workflows)")
	piWebDir := fs.String("pi-web-dir", "", "the Pi Web checkout (default <data-dir>/release/current/pi-web)")
	grace := fs.Duration("grace", 60*time.Second, "the stop grace for the daemon's shutdown (>= 45s)")
	withAIHub := fs.Bool("with-aihub", false, "start and watch the aihub server too")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pf supervise: unexpected arguments %v\n", fs.Args())
		return 2
	}
	home, _ := os.UserHomeDir()
	if *cfgPath == "" {
		*cfgPath = DefaultConfigPath()
	}
	if *dataDir == "" {
		*dataDir = DataDir(home)
	}
	if *wfDir == "" {
		*wfDir = filepath.Join(*dataDir, "release", "current", "workflows")
	}
	if *piWebDir == "" {
		*piWebDir = filepath.Join(*dataDir, "release", "current", "pi-web")
	}
	pfBin, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "pf supervise: cannot locate the pf binary: %v\n", err)
		return 1
	}
	rs, err := planSupervise(superviseOpts{
		pfBin: pfBin, home: home, configPath: *cfgPath, dataDir: *dataDir,
		workflowsDir: *wfDir, piWebDir: *piWebDir, withAIHub: *withAIHub,
		grace: *grace, environ: os.Environ(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pf supervise: %v\n", err)
		return 2
	}
	// the children's log files live under the data dir
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "pf supervise: %v\n", err)
		return 1
	}
	if superviseChild(rs, "pi-web") == nil {
		fmt.Fprintf(stderr, "pf supervise: the pi-web dir %s is missing: running without Pi Web\n", *piWebDir)
	}
	return supervisor.RunSupervise(rs)
}

// superviseChild answers rs's child by name (nil when absent).
func superviseChild(rs supervisor.RunSpec, name string) *supervisor.ChildSpec {
	if rs.Daemon.Name == name {
		return &rs.Daemon
	}
	for i := range rs.Others {
		if rs.Others[i].Name == name {
			return &rs.Others[i]
		}
	}
	return nil
}

// superviseChildEnv answers the environment a non-daemon child (Pi Web,
// the aihub server) runs with: the caller's environment with the
// supervision marker REPLACED by the container supervisor's own (these
// children read their own credentials from their own files; only the
// daemon sits behind the environment allow-list).
func superviseChildEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); name == supervisor.MarkerEnv {
			continue
		}
		out = append(out, kv)
	}
	return append(out, supervisor.MarkerEnv+"="+supervisor.MarkerContainerValue)
}
