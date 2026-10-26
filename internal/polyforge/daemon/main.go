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
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
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
	maxConcurrent, recallTopK, auto := f.MaxConcurrent, f.RecallTopK, f.Auto
	silenceWindow, toolWindow, stepTimeout, stallWindow := f.SilenceWindow, f.ToolWindow, f.StepTimeout, f.StallWindow
	modelErrorGrace, revalidateEvery, heartbeatEvery := f.ModelErrorGrace, f.RevalidateEvery, f.HeartbeatEvery
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "pf daemon: unexpected arguments %v\n", fs.Args())
		return 2
	}
	autoCfg := resolveAuto(fs, *auto)
	shared, err := cli.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pf daemon: %v\n", err)
		return ExitNeedsOperator
	}
	applyDaemonTable(fs, shared.Daemon, stepTimeout, stallWindow, harnessRoot, operatorAgentDir, maxConcurrent)
	if *socket == "" {
		*socket = shared.DaemonSocket
	}
	if *server == "" {
		*server = shared.Server
	}
	if *apiKey == "" {
		*apiKey = shared.APIKey
	}
	if *claudeBin == "" {
		*claudeBin = shared.ClaudeBin
	}

	// The mapping is the ONLY workspace source (polyforge#113): a config
	// with zero [workspace] sections — the fresh-install template shape —
	// refuses startup here, before the socket exists, with the shape
	// guidance and the doctor pointer.
	if shared.Mapping.Empty() {
		where := *cfgPath
		if where == "" {
			where = cli.DefaultConfigPath()
		}
		fmt.Fprintf(stderr, "pf daemon: refusing to start: no workspace mapping is configured — add at least one [workspace.\"<path>\"] section with a projects = [\"...\"] list to %s; the daemon places capsules at <workspace>/capsules/<project>-<seq> per the claim's project (`pf doctor` checks the mapping)\n", where)
		return ExitNeedsOperator
	}

	if *dataDir == "" {
		home, _ := os.UserHomeDir()
		*dataDir = cli.DataDir(home)
	}
	// v2 identity: <data-dir>/daemon-id is THE id once written; a flag
	// value that disagrees refuses to start (never silently switch).
	explicitID := *machineID != ""
	if *machineID == "" {
		*machineID = hostMachineID()
	}
	id, err := ResolveDaemonID(*dataDir, *machineID, explicitID)
	if err != nil {
		fmt.Fprintf(stderr, "pf daemon: refusing to start: %v\n", err)
		return ExitNeedsOperator
	}
	*machineID = id
	if *displayName == "" {
		*displayName, _ = os.Hostname()
	}
	if *workflowsDir == "" {
		*workflowsDir = "workflows"
	}
	if abs, err := filepath.Abs(*workflowsDir); err == nil {
		*workflowsDir = abs // GET /status reports it; pf doctor reads by it
	}
	if *gitRemoteBase == "" {
		*gitRemoteBase = strings.TrimSuffix(*server, "/") + "/git"
	}
	envWarnings := retiredEnvWarnings(os.Getenv, *harnessRoot, *dataDir, *operatorAgentDir)
	for _, w := range envWarnings {
		fmt.Fprintln(stderr, "pf daemon: WARNING: "+w)
	}

	cfg := Config{
		MachineID:     *machineID,
		DisplayName:   *displayName,
		SocketPath:    *socket,
		Mapping:       shared.Mapping,
		DataDir:       *dataDir,
		GitRemoteBase: *gitRemoteBase,
		WorkflowsDir:  *workflowsDir,
		PiBin:         *piBin,
		ClaudeBin:     *claudeBin,
		HarnessRoot:   *harnessRoot,
		// An operator-named source is EXPLICIT: a missing auth.json
		// under it refuses the start rather than running step agents
		// without credentials (addendum 2).
		OperatorAgentDir:         *operatorAgentDir,
		OperatorAgentDirExplicit: *operatorAgentDir != "",
		APIKey:                   *apiKey,
		MaxConcurrent:            *maxConcurrent,
		RecallTopK:               *recallTopK,
		Auto:                     autoCfg,
		HeartbeatEvery:           *heartbeatEvery,
		EnvWarnings:              envWarnings,
		Hub:                      aihubclient.New(*server, *apiKey),
		// Zero fields fall through to the documented defaults.
		Supervision: SupervisionConfig{
			SilenceWindow:          *silenceWindow,
			ToolWindow:             *toolWindow,
			StepTimeout:            *stepTimeout,
			StallWindow:            *stallWindow,
			ModelErrorGrace:        *modelErrorGrace,
			SessionRevalidateEvery: *revalidateEvery,
		},
	}
	d, err := New(cfg)
	if err != nil {
		// every New failure is configuration or on-disk state (a missing
		// workspace, an unreadable state file, a missing agent dir)
		fmt.Fprintf(stderr, "pf daemon: %v\n", err)
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
		_ = d.Shutdown(context.Background(), cause)
	}()

	if err := d.Run(context.Background()); err != nil {
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

// applyDaemonTable fills each knob the command line did not set
// explicitly from the config's [daemon] table (already validated by
// cli.LoadConfig); an unset table key leaves the built-in default.
func applyDaemonTable(fs *flag.FlagSet, t cli.DaemonSettings, stepTimeout, stallWindow *time.Duration, harnessRoot, operatorAgentDir *string, maxConcurrent *int) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["step-timeout"] && t.StepTimeout != 0 {
		*stepTimeout = t.StepTimeout
	}
	if !set["stall-window"] && t.StallWindow != 0 {
		*stallWindow = t.StallWindow
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
