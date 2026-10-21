package cli

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

// DaemonFlags are the `pf daemon` launch flags. DefineDaemonFlags is the
// one definition: daemon.Main parses with it and `pf help daemon` renders
// it, so the help cannot drift from what the daemon accepts.
type DaemonFlags struct {
	Config, Socket, Server, APIKey, MachineID, DisplayName, DataDir *string
	WorkflowsDir, PiBin, ClaudeBin, HarnessRoot, OperatorAgentDir   *string
	GitRemoteBase                                                   *string
	MaxConcurrent, RecallTopK                                       *int
	Auto                                                            *bool
	SilenceWindow, ToolWindow, StepTimeout, StallWindow             *time.Duration
	ModelErrorGrace, RevalidateEvery, HeartbeatEvery                *time.Duration
}

// DefineDaemonFlags defines the daemon launch flags on fs. Production
// sets only what `pf daemon ensure` passes plus the config's [daemon]
// table; a flag marked test/dev-only is never set in production. The
// supervision bounds are Go durations where omitted or 0 keeps the
// default.
func DefineDaemonFlags(fs *flag.FlagSet) *DaemonFlags {
	return &DaemonFlags{
		Config:           fs.String("config", "", "shared config file: its [workspace] mapping is the capsule placement source (default ~/.config/polyforge/cli.toml)"),
		Socket:           fs.String("socket", "", "unix socket path to listen on (env PF_DAEMON_SOCKET; default daemon.sock in $POLYFORGE_DATA_DIR, else ~/.local/share/polyforge — the CLI's default; -data-dir does not move it)"),
		Server:           fs.String("server", "", "aihub base URL (env PF_SERVER)"),
		APIKey:           fs.String("api-key", "", "machine aihub api key (env PF_API_KEY)"),
		MachineID:        fs.String("machine-id", "", "daemon id (default m-<short hostname>); recorded in <data-dir>/daemon-id on first start, which then wins — a differing value refuses to start"),
		DisplayName:      fs.String("display-name", "", "test/dev-only: daemon display name shown by pf ops status (default hostname)"),
		DataDir:          fs.String("data-dir", "", "daemon data dir (env POLYFORGE_DATA_DIR; default ~/.local/share/polyforge)"),
		WorkflowsDir:     fs.String("workflows-dir", "", "workflows/ checkout dir (default ./workflows)"),
		PiBin:            fs.String("pi-bin", "", "test/dev-only: harness binary (default pi)"),
		ClaudeBin:        fs.String("claude-bin", "", "Claude Code binary (tests/dev; production source cli.toml claude_bin) for the steps whose harness is claude, run as claude -p --bare when the claude-config settings.json env block carries a non-empty ANTHROPIC_AUTH_TOKEN/ANTHROPIC_API_KEY, else claude -p --safe-mode (default claude); the daemon never manages its credentials — the claude-config settings.json env block is the authority layer"),
		HarnessRoot:      fs.String("harness-root", "", "parent root of the step harness state dirs ([daemon] harness_root; default <data-dir>): <harness-root>/step-agent (the dedicated step agent dir), /step-agent-sessions (the pi steps' session store) and /claude-config (claude's config dir; its settings.json env block is the single credential authority layer); it replaces --harness-sessions-root, which is refused"),
		OperatorAgentDir: fs.String("operator-agent-dir", "", "SOURCE of the dedicated step agent dir ([daemon] operator_agent_dir; default ~/.pi/agent): the daemon copies the minimal set (auth.json, models.json, agents/, extensions/subagent/) from here into <harness-root>/step-agent and every step agent runs against that copy, so the operator's own harness settings never reach a step. A configured source without a readable auth.json refuses the start"),
		GitRemoteBase:    fs.String("git-remote-base", "", "test/dev-only: aihub bare-repo base URL (default <server>/git)"),
		MaxConcurrent:    fs.Int("max-concurrent-jobs", 0, "configured max concurrent jobs ([daemon] max_concurrent_jobs; default 4); pf ops max N overrides it until the daemon restarts"),
		RecallTopK:       fs.Int("recall-topk", 0, "test/dev-only: claim-time memory snapshot hit count (default 10, max 50)"),
		Auto:             fs.Bool("auto", false, "test/dev-only (production: pf ops auto on|off): auto-pull — the daemon claims ready-queue work itself (default off; unset keeps the persisted switch; --auto=false forces off)"),
		SilenceWindow:    fs.Duration("silence-window", 0, "test/dev-only: X: no real work signal after dispatch, or no model output while waiting for the model, before the step is judged dead (default 10m for token-level harnesses, 30m for message-level)"),
		ToolWindow:       fs.Duration("tool-window", 0, "test/dev-only: Y: one tool running longer than this is judged hung (default 30m)"),
		StepTimeout:      fs.Duration("step-timeout", 0, "T: cumulative step runtime across its dispatches ([daemon] step_timeout; default 90m); a step's budget.wall_minutes is an estimate, not an override: that step's T = max(3 x wall_minutes, this value)"),
		StallWindow:      fs.Duration("stall-window", 0, "N: the driver between steps, not waiting on a human, for longer than this is judged stalled ([daemon] stall_window; default 10m; negative disables)"),
		ModelErrorGrace:  fs.Duration("model-error-grace", 0, "test/dev-only: grace after a turn ends on a model error before the step is judged dead (default 1m)"),
		RevalidateEvery:  fs.Duration("revalidate-every", 0, "test/dev-only: periodic session revalidation cadence for held records — running jobs and retained paused records (default 30s; negative disables)"),
		HeartbeatEvery:   fs.Duration("heartbeat-every", 60*time.Second, "test/dev-only: machines heartbeat cadence (0 disables)"),
	}
}

// daemonFlagsHelp renders the launch flags for `pf help daemon`.
func daemonFlagsHelp() string {
	fs := flag.NewFlagSet("pf daemon", flag.ContinueOnError)
	DefineDaemonFlags(fs)
	var b strings.Builder
	fs.VisitAll(func(f *flag.Flag) {
		typ, usage := flag.UnquoteUsage(f)
		if f.DefValue != "" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "false" {
			usage += " (default " + f.DefValue + ")"
		}
		fmt.Fprintf(&b, "\n  %s", strings.TrimSpace("--"+f.Name+" "+typ))
		line := ""
		for _, w := range strings.Fields(usage) {
			if line != "" && len(line)+1+len(w) > 66 {
				fmt.Fprintf(&b, "\n      %s", line)
				line = ""
			}
			line = strings.TrimSpace(line + " " + w)
		}
		fmt.Fprintf(&b, "\n      %s", line)
	})
	return b.String()
}
