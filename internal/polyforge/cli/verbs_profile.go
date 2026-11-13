package cli

// verbs_profile.go: `pf profile` — print the profile this invocation
// resolves to and where it came from (flag|env|cwd|disk|default). Local:
// with the daemon down it still answers from the files on disk. Refused in
// a step or task: an agent's Cell is the Host's ownership record, never a
// resolution of its own.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

var cmdProfile = &Command{
	Name:         "profile",
	Group:        GroupDiag,
	Summary:      "Print the Cell (profile) this invocation resolves to and its source",
	Usage:        "pf [--profile NAME] [--json] profile",
	NoConfigLoad: true,
	Setup:        func(*flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("profile: unexpected arguments %v", args)
		}
		res, err := ctx.resolveProfile(true)
		if err != nil {
			return err
		}
		file := filepath.Join(filepath.Dir(DefaultConfigPath()), cellcfg.CellFileName(res.Profile))
		_, statErr := os.Stat(file)
		configured := statErr == nil
		raw, _ := json.Marshal(map[string]any{"profile": res.Profile, "source": res.Source, "file": file, "configured": configured})
		ctx.finish(raw, func() {
			note := ""
			if !configured {
				note = " (not configured: " + file + " does not exist)"
			}
			fmt.Fprintf(ctx.Stdout, "%s (source: %s)%s\n", res.Profile, res.Source, note)
		})
		return nil
	},
}
