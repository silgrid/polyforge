package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/silgrid/polyforge/internal/aihub"
)

// aihubServerUsage is the `pf help aihub` text: the server entry's
// launch surface, with the flags rendered from the one flag definition
// (aihub.DefineFlags) the real entry parses.
var aihubServerUsage = `pf aihub [--flag value] ...

pf aihub runs the v1 aihub server in the foreground on the service host:
single-active (an exclusive lock on <data-dir>/service.lock), SQLite-backed,
one HTTP port for the business face, the admin face and the git proxy. A
fresh data dir seeds one bootstrap admin whose api key is written to
<data-dir>/bootstrap-admin.key (0600). SIGINT/SIGTERM drains and stops.
The real entry is assembled: cmd/pf/main.go intercepts ` + "`pf aihub`" + ` argv
before CLI dispatch and calls internal/aihub.Main. This CLI registration
exists only to serve ` + "`pf help aihub`" + ` presentation. It needs no
cli.toml and no aihub api key.

pf aihub init [--data-dir D] --key-out F

The local first-boot initialization entry (polyforge#176,
84-components-C): it opens the core database IN PROCESS — no network
listener, no HTTP route — and establishes the workstation's daily
identity (the user, its API key and the local project) in one
transaction, before any server starts on the data dir (a server
started afterwards finds the users table non-empty and its own
fresh-install bootstrap never fires). It refuses to run inside a step
or task environment and beside a running server (the single-active
lock), and the issued key's plaintext lands only in the 0600 file
--key-out names — never on stdout. On an initialized store the
identity is verified, never duplicated, and the same-named key is
revoked and reissued (the first boot's retry discipline). The
workstation's entrypoint reaches it through pf first-boot; a human
never needs it by hand.

Flags:
` + aihubServerFlagsHelp()

// aihubServerFlagsHelp renders the server's flags (aihub.DefineFlags).
func aihubServerFlagsHelp() string {
	fs := flag.NewFlagSet("pf aihub", flag.ContinueOnError)
	aihub.DefineFlags(fs)
	var b strings.Builder
	fs.SetOutput(&b)
	fs.PrintDefaults()
	return b.String()
}

var cmdAihubServer = &Command{
	Name:    "aihub",
	Group:   GroupProcess,
	Summary: "Run the aihub server on the service host (main.go intercepts `pf aihub` before CLI dispatch; this verb serves `pf help aihub` only)",
	Usage:   aihubServerUsage,
	Setup:   func(fs *flag.FlagSet) { aihub.DefineFlags(fs) },
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		// In a correct build this Run is unreachable: cmd/pf/main.go
		// intercepts `pf aihub` argv and calls aihub.Main. If it is ever
		// reached, the only honest answer is the launch-surface help.
		fmt.Fprint(ctx.Stdout, aihubServerUsage)
		return nil
	},
}
