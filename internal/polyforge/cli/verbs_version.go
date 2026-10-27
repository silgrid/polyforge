package cli

import (
	"flag"
	"fmt"

	"github.com/silgrid/polyforge/internal/version"
)

// verbs_version.go: `pf version` (polyforge#196) — the build string on
// the verb dispatch, so scripts and the routing skill can query it
// without knowing the global `--version` flag. It answers from the same
// source (internal/version, link-time injection) and prints the same
// bytes as the flag path in Run(): the human line, or under the global
// --json flag the machine line {"version":"<v>"} (`pf --json version`,
// the chat usage-form convention), exit 0. NoConfigLoad: the verb must
// answer with no or broken config, the same reachability the
// pre-dispatch flag has. No flags, no Setup; extra positionals are an
// arity error (the doctor convention, exit 1). It touches no face —
// no aihub, no daemon.
var cmdVersion = &Command{
	Name:         "version",
	Group:        GroupDiag,
	Summary:      "Print the build version of this pf binary",
	Usage:        "pf [--json] version",
	NoConfigLoad: true,
	Setup:        func(*flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("version: unexpected arguments %v", args)
		}
		if ctx.JSON {
			fmt.Fprintf(ctx.Stdout, "{\"version\":%q}\n", version.Version)
			return nil
		}
		fmt.Fprintln(ctx.Stdout, version.Version)
		return nil
	},
}
