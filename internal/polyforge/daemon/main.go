package daemon

// main.go: the `pf daemon` process entry. Assembles the daemon Config
// from flags, the shared CLI config chain (PF_SERVER / PF_API_KEY /
// PF_DAEMON_SOCKET, claude_bin), POLYFORGE_DATA_DIR and the documented
// defaults, then runs the daemon with SIGTERM/SIGINT wired to the
// shutdown.
//
// Production starts the daemon only through `pf daemon ensure`, whose
// argv carries only the fixed launch flags. The five production knobs
// (step timeout, stall window, harness root, operator agent dir, job cap)
// come from the --config file's [daemon] table (decision 2026-10-16 G2):
// an explicit command-line flag (tests/dev) > the table > the built-in
// default (applyDaemonTable). Every other flag (cli.DefineDaemonFlags) is test/dev-only:
// nothing in production sets it.
//
// polyforge#113: there is NO launch-time workspace and NO project scope
// anymore. The [workspace."<path>"] mapping in the shared config (the
// --config file, defaulting to the standard cli.toml location) is the
// only workspace source — the daemon refuses to start without a non-empty
// mapping, validates every mapped workspace, routes capsules per project
// through the inverse mapping, and iterates all mapped projects when
// auto-pulling. The retired selection surfaces (the -workspace flag, the
// --project flag and their env mirrors) are refused loudly at boot.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// Main executes `pf daemon <flags>` and returns the process exit code.
func Main(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// a daemon reads cli.toml's api key and talks to aihub with it: never
	// from inside a workflow step (the step markers are set) — neither run
	// nor ensure
	if os.Getenv("PF_WI_ID") != "" && os.Getenv("PF_DAEMON_ID") != "" {
		fmt.Fprintln(stderr, "pf daemon: refused inside a workflow step (PF_WI_ID and PF_DAEMON_ID are set): only a human's session runs the daemon")
		return 1
	}
	// `pf daemon ensure` (polyforge#174): start the daemon under
	// supervision unless it already answers — never runs the daemon
	// in this process.
	if len(argv) > 0 && argv[0] == "ensure" {
		return Ensure(argv[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("pf daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := cli.DefineDaemonFlags(fs)
	cfgPath, socket, server, apiKey, machineID, displayName, dataDir := f.Config, f.Socket, f.Server, f.APIKey, f.MachineID, f.DisplayName, f.DataDir
	workflowsDir, piBin, claudeBin, harnessRoot, operatorAgentDir, gitRemoteBase := f.WorkflowsDir, f.PiBin, f.ClaudeBin, f.HarnessRoot, f.OperatorAgentDir, f.GitRemoteBase
	launcher := f.Launcher
	maxConcurrent, recallTopK, auto := f.MaxConcurrent, f.RecallTopK, f.Auto
	silenceWindow, toolWindow, stepTimeout, stallWindow := f.SilenceWindow, f.ToolWindow, f.StepTimeout, f.StallWindow
	modelErrorGrace, revalidateEvery, heartbeatEvery := f.ModelErrorGrace, f.RevalidateEvery, f.HeartbeatEvery
	askWarmWindow := f.AskWarmWindow
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pf daemon: unexpected arguments %v\n", fs.Args())
		return 2
	}
	autoCfg := resolveAuto(fs, *auto)
	// The Host set: cli.toml (the Host keys and the default Cell) and every
	// cli-<profile>.toml next to it, file contents only (a Cell's connection
	// lives in its file; no PF_* environment override reaches the daemon).
	// An invalid Host file refuses the start naming the file; an invalid
	// named Cell file is isolated.
	set, err := cli.LoadHostSet(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pf daemon: refusing to start: %v\n", err)
		return ExitNeedsOperator
	}
	host := set.Host
	// The step scripts' interpreter (the workflow contract's "Scripts"):
	// resolved once, here, and checked before any machine state is
	// touched — a missing or too old one refuses the start, naming it.
	python, err := cli.ResolvePython(host.Daemon.Python, exec.LookPath, cli.ProbePython)
	if err != nil {
		fmt.Fprintf(stderr, "pf daemon: refusing to start: %v (config %s)\n", err, set.HostPath)
		return ExitNeedsOperator
	}
	if *socket == "" {
		*socket = host.DaemonSocket
	}
	if v := os.Getenv("PF_DAEMON_SOCKET"); v != "" && !flagWasSet(fs, "socket") {
		*socket = v
	}
	// The default Cell's mapping, when present, must be usable (the SAME
	// shared validator `pf config` and `pf doctor` use) before any machine
	// state is touched; an empty one loads with the execution plane closed.
	if !host.Mapping.Empty() {
		if err := cli.ValidateMappingForDaemon(host); err != nil {
			fmt.Fprintf(stderr, "pf daemon: refusing to start: %v (config %s; the daemon places capsules at <workspace>/capsules/<project>-<seq> per the claim's project, and `pf doctor` checks the mapping)\n", err, set.HostPath)
			return ExitNeedsOperator
		}
	}
	if *dataDir == "" {
		home, _ := os.UserHomeDir()
		*dataDir = cli.DataDir(home)
	}
	if abs, aerr := filepath.Abs(*dataDir); aerr == nil {
		*dataDir = abs
	}
	// The layout gate (cellcfg/layout.go): a data dir on the per-Cell
	// layout starts; a fresh one is initialized; a pre-Cell one is refused
	// naming `pf migrate cell-layout` — never migrated here, nothing written.
	fresh, lerr := cellcfg.CheckLayout(*dataDir)
	if lerr != nil {
		fmt.Fprintf(stderr, "pf daemon: refusing to start: %v\n", lerr)
		return ExitNeedsOperator
	}
	if fresh {
		if err := cellcfg.InitLayout(*dataDir); err != nil {
			fmt.Fprintf(stderr, "pf daemon: refusing to start: initialize the data dir layout: %v\n", err)
			return ExitNeedsOperator
		}
	}
	if *displayName == "" {
		*displayName, _ = os.Hostname()
	}
	if *workflowsDir == "" {
		*workflowsDir = "workflows"
	}
	if abs, err := filepath.Abs(*workflowsDir); err == nil {
		*workflowsDir = abs // GET /status reports it; pf doctor reads by it
	}
	// The Host-only [daemon] knobs: an explicit flag (tests/dev) beats the
	// table.
	applyDaemonTable(fs, host.Daemon, stepTimeout, stallWindow, askWarmWindow, harnessRoot, operatorAgentDir, maxConcurrent)
	envWarnings := retiredEnvWarnings(os.Getenv, *harnessRoot, *dataDir, *operatorAgentDir)
	for _, w := range envWarnings {
		fmt.Fprintln(stderr, "pf daemon: WARNING: "+w)
	}
	pinned := flagPinned(fs)
	base := Config{
		DisplayName:  *displayName,
		SocketPath:   *socket,
		FlagPinned:   pinned,
		WorkflowsDir: *workflowsDir,
		PiBin:        *piBin,
		// An operator-named source is EXPLICIT: a missing auth.json
		// under it refuses the start rather than running step agents
		// without credentials (addendum 2).
		OperatorAgentDir:         *operatorAgentDir,
		OperatorAgentDirExplicit: *operatorAgentDir != "",
		Python:                   python.Path,
		MaxConcurrent:            *maxConcurrent,
		RecallTopK:               *recallTopK,
		Auto:                     autoCfg,
		HeartbeatEvery:           *heartbeatEvery,
		Launcher:                 *launcher,
		EnvWarnings:              envWarnings,
		Hosted:                   true,
		// Zero fields fall through to the documented defaults.
		Supervision: SupervisionConfig{
			SilenceWindow:          *silenceWindow,
			ToolWindow:             *toolWindow,
			StepTimeout:            *stepTimeout,
			StallWindow:            *stallWindow,
			ModelErrorGrace:        *modelErrorGrace,
			AskWarmWindow:          *askWarmWindow,
			SessionRevalidateEvery: *revalidateEvery,
		},
	}
	build := func(profile string, c cli.Config) (Config, error) {
		return cellConfig(base, cellBuildInput{
			profile: profile, file: c, hostRoot: *dataDir, configPath: cellFilePath(set, profile),
			harnessRoot: *harnessRoot, gitRemoteBase: *gitRemoteBase,
			flagServer: *server, flagAPIKey: *apiKey, flagMachineID: *machineID, flagClaudeBin: *claudeBin,
			flagStepTimeout: flagWasSet(fs, "step-timeout"), flagStallWindow: flagWasSet(fs, "stall-window"),
			flagHeartbeat: flagWasSet(fs, "heartbeat-every"),
		})
	}
	h, err := NewHost(HostConfig{SocketPath: *socket, DataDir: *dataDir, Set: set, Build: build, MaxConcurrent: *maxConcurrent})
	if err != nil {
		// every assembly failure is configuration or on-disk state (a missing
		// workspace, an unreadable state file, a missing agent dir)
		fmt.Fprintf(stderr, "pf daemon: refusing to start: %v\n", err)
		return ExitNeedsOperator
	}

	// SIGTERM/SIGINT run the same shutdown the
	// POST /shutdown route triggers. The signal VALUE is observed via a
	// Notify channel (polyforge#110): a NotifyContext cancellation
	// reports only "context canceled", so the cause record could never
	// name which signal fired. One shot: after the first delivery
	// signal.Stop restores the default disposition, so a second signal
	// aborts waits hard — the same semantics NotifyContext's stop gave.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		sig := <-sigCh
		signal.Stop(sigCh) // one shot: a second signal aborts waits hard
		cause := fmt.Sprintf("signal: %v", sig)
		if s, ok := sig.(syscall.Signal); ok {
			cause = fmt.Sprintf("signal: %s (signal %d)", s, int(s))
		}
		_ = h.Shutdown(context.Background(), cause)
	}()

	if err := h.Run(context.Background()); err != nil {
		fmt.Fprintf(stderr, "pf daemon: %v\n", err)
		var no *needsOperatorError
		if errors.As(err, &no) {
			return ExitNeedsOperator
		}
		return 1
	}
	return 0
}

// ExitNeedsOperator is the daemon's exit status for a startup failure
// only an operator can fix — configuration, identity, workspace, on-disk
// state, a failed restart reconciliation (EX_CONFIG). The ensure
// supervision loop ends on it instead of restarting into the same
// failure every few seconds; the next
// `pf daemon ensure` starts a fresh loop once the cause is fixed.
const ExitNeedsOperator = 78

// needsOperatorError marks a Run error as ExitNeedsOperator-class.
type needsOperatorError struct{ err error }

func (e *needsOperatorError) Error() string { return e.err.Error() }
func (e *needsOperatorError) Unwrap() error { return e.err }

// retiredEnvWarnings names each retired harness variable (decision
// 2026-10-16 G2: no longer read) that is set to a value differing from
// what the daemon resolved. polyforge#171 ruled out harness state moving
// silently; an operator who relied on the variable must move it into the
// [daemon] table. harnessRoot/operatorAgentDir are the resolved flag or
// table values ("" = the defaults <data-dir> and ~/.pi/agent).
func retiredEnvWarnings(getenv func(string) string, harnessRoot, dataDir, operatorAgentDir string) []string {
	if harnessRoot == "" {
		harnessRoot = dataDir
	}
	if operatorAgentDir == "" {
		operatorAgentDir = runtime.OperatorAgentDirDefault()
	}
	var out []string
	for _, v := range []struct{ env, key, resolved string }{
		{"POLYFORGE_HARNESS_ROOT", "harness_root", harnessRoot},
		{"POLYFORGE_OPERATOR_AGENT_DIR", "operator_agent_dir", operatorAgentDir},
	} {
		val := getenv(v.env)
		if val == "" || filepath.Clean(val) == filepath.Clean(v.resolved) {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s is set but no longer read (decision 2026-10-16 G2); the daemon uses %s — if the harness state lives at %s, set [daemon] %s = %q in cli.toml and restart the daemon", v.env, val, v.resolved, val, v.key, val))
	}
	return out
}

// flagPinned names the [daemon]/live keys an explicit command-line flag
// fixed at startup. A reload reports those overridden and never applies
// them (an explicit daemon flag beats a reloaded value).
func flagPinned(fs *flag.FlagSet) map[string]bool {
	pinned := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-concurrent-jobs":
			pinned["max_concurrent_jobs"] = true
		case "step-timeout":
			pinned["step_timeout"] = true
		case "stall-window":
			pinned["stall_window"] = true
		case "claude-bin":
			pinned["claude_bin"] = true
		case "harness-root":
			pinned["harness_root"] = true
		case "operator-agent-dir":
			pinned["operator_agent_dir"] = true
		case "api-key":
			pinned["api_key"] = true
		case "server":
			pinned["server"] = true
		case "socket":
			pinned["daemon_socket"] = true
		}
	})
	return pinned
}

// applyDaemonTable fills each knob the command line did not set
// explicitly from the config's [daemon] table (already validated by
// cli.LoadConfig); an unset table key leaves the built-in default.
func applyDaemonTable(fs *flag.FlagSet, t cli.DaemonSettings, stepTimeout, stallWindow, askWarmWindow *time.Duration, harnessRoot, operatorAgentDir *string, maxConcurrent *int) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["step-timeout"] && t.StepTimeout != 0 {
		*stepTimeout = t.StepTimeout
	}
	if !set["stall-window"] && t.StallWindow != 0 {
		*stallWindow = t.StallWindow
	}
	if !set["ask-warm-window"] && t.AskWarmWindow != 0 {
		*askWarmWindow = t.AskWarmWindow
	}
	if !set["harness-root"] && t.HarnessRoot != "" {
		*harnessRoot = t.HarnessRoot
	}
	if !set["operator-agent-dir"] && t.OperatorAgentDir != "" {
		*operatorAgentDir = t.OperatorAgentDir
	}
	if !set["max-concurrent-jobs"] && t.MaxConcurrentJobs > 0 {
		*maxConcurrent = t.MaxConcurrentJobs
	}
}

// resolveAuto maps -auto to the initial auto switch (nil = unspecified:
// the persisted switch wins).
func resolveAuto(fs *flag.FlagSet, auto bool) *bool {
	autoSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "auto" {
			autoSet = true
		}
	})
	if !autoSet {
		return nil
	}
	return &auto
}

// flagWasSet reports whether the named flag was given on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// cellBuildInput is what one Cell's daemon Config is assembled from.
type cellBuildInput struct {
	profile    string
	file       cli.Config // the Cell's file config (the Host file for default)
	hostRoot   string     // the Host data dir
	configPath string     // the Cell's file
	// harnessRoot is the Host-only [daemon] harness_root ("" = the Cell dir).
	harnessRoot, gitRemoteBase string
	// The test/dev launch flags; they address the default Cell only.
	flagServer, flagAPIKey, flagMachineID, flagClaudeBin string
	flagStepTimeout, flagStallWindow, flagHeartbeat      bool
}

// cellConfig assembles one Cell's daemon Config: the Host-wide base, the
// Cell's own state directory, hub client, machine identity, mapping and
// execution settings. Every Cell, the default one included, keeps its state
// in cells/<profile> under the Host data dir.
func cellConfig(base Config, in cellBuildInput) (Config, error) {
	c := in.file
	cfg := base
	cfg.ConfigPath = in.configPath
	cfg.StartupFile = c
	cfg.Mapping = c.Mapping
	cfg.Server, cfg.APIKey = c.Server, c.APIKey
	cfg.ClaudeBin = c.ClaudeBin
	named := in.profile != cellcfg.DefaultProfile
	// Every Cell, default included, lives under cells/<profile> (the
	// layout gate in Main refused a pre-Cell data dir); its harness root is
	// the Cell dir, or <harness_root>/cells/<profile>.
	cfg.DataDir = cellcfg.CellDir(in.hostRoot, in.profile)
	cfg.HarnessRoot = ""
	if in.harnessRoot != "" {
		cfg.HarnessRoot = filepath.Join(in.harnessRoot, cellcfg.CellsDirName, in.profile)
	}
	if named {
		// A named Cell's knobs come from its own file only.
		cfg.Supervision.StepTimeout = c.Daemon.StepTimeout
		cfg.Supervision.StallWindow = c.Daemon.StallWindow
		cfg.Supervision.AskWarmWindow = c.Daemon.AskWarmWindow
		cfg.Auto = nil
		cfg.EnvWarnings = nil
		switch c.ModelSource {
		case "":
			cfg.ExecutionDisabled = execDisabledNoModelSource
			cfg.OperatorAgentDir = filepath.Join(cfg.DataDir, "no-model-source")
			cfg.OperatorAgentDirExplicit = false
		case cli.ModelSourceShared:
			// shares the default Cell's source (base carries it): the pi
			// minimal set is synced from it like the default's, and only the
			// env block of the default Cell's claude settings is copied
			if err := shareDefaultClaudeEnv(in, cfg); err != nil {
				cfg.ExecutionDisabled = "the shared model-credential source could not be copied: " + err.Error()
			}
		default:
			if why := unusableModelSource(c.ModelSource); why != "" {
				cfg.ExecutionDisabled = why
				cfg.OperatorAgentDir = filepath.Join(cfg.DataDir, "no-model-source")
				cfg.OperatorAgentDirExplicit = false
			} else {
				cfg.OperatorAgentDir, cfg.OperatorAgentDirExplicit = c.ModelSource, true
			}
		}
	} else {
		if in.flagServer != "" {
			cfg.Server = in.flagServer
		}
		if in.flagAPIKey != "" {
			cfg.APIKey = in.flagAPIKey
		}
		if in.flagClaudeBin != "" {
			cfg.ClaudeBin = in.flagClaudeBin
		}
	}
	if !named || !in.flagHeartbeat {
		if c.Daemon.HeartbeatEvery > 0 && !in.flagHeartbeat {
			cfg.HeartbeatEvery = c.Daemon.HeartbeatEvery
		}
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = cli.DefaultMaxConcurrentJobs
	}
	if c.Daemon.CellMaxConcurrentJobs > 0 && c.Daemon.CellMaxConcurrentJobs < cfg.MaxConcurrent {
		cfg.MaxConcurrent = c.Daemon.CellMaxConcurrentJobs
	}
	cfg.CellMaxConcurrentJobs = c.Daemon.CellMaxConcurrentJobs
	// The Cell's machine identity: <cell-dir>/daemon-id is THE id once
	// written; a configured value (the launch flag for default, [daemon]
	// machine_id) that disagrees refuses this Cell — never a silent switch.
	seed, explicit := c.Daemon.MachineID, c.Daemon.MachineID != ""
	if !named && in.flagMachineID != "" {
		seed, explicit = in.flagMachineID, true
	}
	if seed == "" {
		// every Cell's machine id is its own: the step and task process
		// markers (PF_DAEMON_ID) then name exactly one Cell
		seed = hostMachineID()
		if named {
			seed += "-" + in.profile
		}
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return cfg, err
	}
	id, err := ResolveDaemonID(cfg.DataDir, seed, explicit)
	if err != nil {
		return cfg, err
	}
	cfg.MachineID = id
	// Identity is stable at every assembly (Host start, add, repair): a
	// Cell file whose normalized server differs from the one recorded in
	// the Cell's identity.json is another identity domain — refused with
	// the new-profile route, never adopted silently.
	if why := identityMismatch(in.profile, cfg.DataDir, cfg.Server); why != "" {
		return cfg, errors.New(why)
	}
	cfg.GitRemoteBase = in.gitRemoteBase
	if cfg.GitRemoteBase == "" || named {
		cfg.GitRemoteBase = strings.TrimSuffix(cfg.Server, "/") + "/git"
	}
	// The effective credential is persisted from the first start on; a
	// Host restart restores it and a pending rotation first (drain.go). A
	// file credential that differs from it is a pending change: the Cell
	// starts on its effective credential and the file's goes through the
	// identity check (adoptFileKey) — a newer file value can neither
	// switch the Cell's user nor bypass the idle gate. An explicit launch
	// flag (the default Cell's test/dev seam) pins the key instead.
	if named || in.flagAPIKey == "" {
		if cfg.APIKey == "" {
			// the file holds no api_key: the persisted copy never stands in
			// for it — the record is deleted and the Cell holds no
			// credential (it reads degraded and refuses AIHub business
			// naming the missing key; drain.go missingCredential)
			if err := os.Remove(filepath.Join(cfg.DataDir, effectiveFile)); err != nil && !os.IsNotExist(err) {
				return cfg, fmt.Errorf("drop the effective connection of a Cell whose file has no api_key: %w", err)
			}
		} else if rec, ok := readEffective(cfg.DataDir); ok {
			if cfg.APIKey != rec.APIKey && cfg.APIKey != rec.PendingAPIKey {
				cfg.FileAPIKey = cfg.APIKey
			}
			cfg.APIKey, cfg.PendingAPIKey = rec.APIKey, rec.PendingAPIKey
		} else if cfg.APIKey != "" {
			if err := writeEffective(cfg.DataDir, effectiveConnection{APIKey: cfg.APIKey}); err != nil {
				return cfg, fmt.Errorf("persist the effective connection: %w", err)
			}
		}
	}
	cfg.Hub = aihubclient.New(cfg.Server, cfg.APIKey)
	return cfg, nil
}

// unusableModelSource answers why a declared model-credential source
// directory cannot serve a Cell ("" = usable): it must be absolute and
// carry a readable auth.json.
func unusableModelSource(dir string) string {
	if !filepath.IsAbs(dir) {
		return fmt.Sprintf("the declared model-credential source %q is not an absolute directory — fix [model] source in this Cell's file", dir)
	}
	f, err := os.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		return fmt.Sprintf("the declared model-credential source %s is unusable (no readable auth.json: %v) — fix [model] source in this Cell's file", dir, err)
	}
	_ = f.Close()
	return ""
}

// shareDefaultClaudeEnv copies the env block of the default Cell's claude
// settings into a sharing named Cell's own claude config dir.
func shareDefaultClaudeEnv(in cellBuildInput, cfg Config) error {
	defRoot := cellcfg.CellDir(in.hostRoot, cellcfg.DefaultProfile)
	if in.harnessRoot != "" {
		defRoot = filepath.Join(in.harnessRoot, cellcfg.CellsDirName, cellcfg.DefaultProfile)
	}
	root := cfg.HarnessRoot
	if root == "" {
		root = cfg.DataDir
	}
	return runtime.ShareClaudeEnv(filepath.Join(defRoot, runtime.ClaudeConfigDirName, "settings.json"), filepath.Join(root, runtime.ClaudeConfigDirName))
}

// cellFilePath answers the file a Cell's keys were loaded from: the Host
// file itself (whatever its name, --config included) for default, the
// cli-<profile>.toml next to it for a named Cell.
func cellFilePath(set *cli.HostSet, profile string) string {
	if profile == cellcfg.DefaultProfile {
		return set.HostPath
	}
	return set.CellPath(profile)
}
