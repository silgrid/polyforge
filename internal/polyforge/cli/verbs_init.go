package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// verbs_init.go: `pf init [<workspace-dir>]` — the local half of
// workspace init (polyforge#177). It gives an existing workspace its
// runtime-artifact layout: scratch/, the convention section of
// CLAUDE.md's polyforge-managed block and .polyforge.yaml's default
// memory_policy (workspace.Init: idempotent, the human part of CLAUDE.md
// is never touched). It needs no config, no
// aihub and no daemon; mapping the workspace stays the operator's
// cli.toml edit (skills/polyforge/verbs/init.md). Machine install is
// tools/install.sh, not this verb.
var cmdInit = &Command{
	Name:         "init",
	Group:        GroupDiag,
	Summary:      "Give a workspace its local layout: scratch/, the layout section of CLAUDE.md, the memory_policy of .polyforge.yaml",
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
		if ctx.JSON {
			b, err := json.Marshal(res)
			if err != nil {
				return err
			}
			ctx.Out(b)
			return nil
		}
		fmt.Fprintf(ctx.Stdout, "workspace %s\n", res.Workspace)
		fmt.Fprintf(ctx.Stdout, "  %s/  %s\n", workspace.ScratchDir, res.Scratch)
		fmt.Fprintf(ctx.Stdout, "  %s  layout section %s\n", workspace.NotesFile, res.NotesFile)
		fmt.Fprintf(ctx.Stdout, "  %s  %s section %s\n", workspace.FileName, workspace.MemoryPolicyKey, res.Yaml)
		if res.YamlWarning != "" {
			fmt.Fprintf(ctx.Stdout, "  warning: %s left untouched: %s\n", workspace.FileName, res.YamlWarning)
		}
		return nil
	},
}
