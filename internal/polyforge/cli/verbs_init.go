package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// verbs_init.go: `pf init [<workspace-dir>]` — the local half of
// workspace init (polyforge#177). It gives an existing workspace its
// runtime-artifact layout: scratch/, the convention section of the
// notes file's polyforge-managed block (workspace.Notes: AGENTS.md, else
// CLAUDE.md; a fresh workspace gets AGENTS.md) and, for a mapped
// workspace, cli.toml's default [memory_policy."<path>".default] block
// (; idempotent, an existing entry is never rewritten). It
// never writes .polyforge.yaml and needs no aihub; mapping the workspace
// stays the operator's cli.toml edit (skills/polyforge/verbs/init.md).
// Machine install is tools/install.sh, not this verb.
var cmdInit = &Command{
	Name:         "init",
	Group:        GroupDiag,
	Summary:      "Give a workspace its local layout: scratch/, the layout section of AGENTS.md (else CLAUDE.md), the default memory_policy block in cli.toml",
	Usage:        "pf init [<workspace-dir>]   (default: the current directory)",
	NoConfigLoad: true,
	Setup:        func(*flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("init: at most one workspace directory, got %v", args)
		}
		dir := "."
		if len(args) == 1 {
			dir = args[0]
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return fmt.Errorf("init: workspace directory %s does not exist — create it first (pf init never creates the workspace itself)", abs)
		}
		res, err := workspace.Init(abs)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		cfgPath := configPath(ctx)
		policy, err := initMemoryPolicy(cfgPath, abs)
		if err != nil {
			return fmt.Errorf("init: %w", err)
		}
		hint := ""
		switch policy {
		case "unmapped":
			hint = fmt.Sprintf("%s is not mapped in %s: map it first with a [workspace.%q] section (`pf config set '[workspace.%q]' projects=<a,b>`), then re-run `pf init` to write its default memory_policy block", abs, cfgPath, abs, abs)
		case "inserted":
			if note := notifyPolicyReload(ctx); note != "" {
				hint = note
			}
		}
		if ctx.JSON {
			b, err := json.Marshal(struct {
				workspace.InitResult
				MemoryPolicy string `json:"memory_policy"`
				Hint         string `json:"hint,omitempty"`
			}{res, policy, hint})
			if err != nil {
				return err
			}
			ctx.Out(b)
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "workspace %s\n", res.Workspace)
		fmt.Fprintf(ctx.Stdout, "  %s/  %s\n", workspace.ScratchDir, res.Scratch)
		fmt.Fprintf(ctx.Stdout, "  %s  layout section %s\n", res.NotesPath, res.NotesFile)
		fmt.Fprintf(ctx.Stdout, "  %s  [memory_policy.%q.default] %s\n", cfgPath, abs, policy)
		if hint != "" {
			fmt.Fprintf(ctx.Stdout, "  note: %s\n", hint)
		}
		return nil
	},
}

// notifyPolicyReload asks a running daemon to reload after `pf init`
// wrote a policy block (the family is next-claim). No daemon is no note;
// a refused reload is reported, the written block stands.
func notifyPolicyReload(ctx *Context) string {
	if _, err := ctx.configDaemon().ConfigReload(); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			return "the running daemon refused the reload: " + err.Error()
		}
	}
	return ""
}
