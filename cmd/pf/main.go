// Command pf is the one polyforge binary. On a work machine it is the v1
// client: 13 user verbs plus the M1-M5 management face, routed as
// described in docs/design/polyforge-vnext/reference.md §1 and §11:
//
//   - direct-to-aihub verbs speak the shared HTTP contract
//     (internal/common/api) through internal/polyforge/aihubclient;
//   - execution-plane verbs speak the daemon's UDS HTTP face
//     (internal/common/protocol);
//   - `pf daemon` runs the local daemon: the phase-3 assembly of
//     Runtime (Start/Stop/Recover/Restart) and Observe (per-WI monitor
//     goroutines) over the UDS surface this CLI speaks
//     (internal/polyforge/daemon).
//
// On the service host it is the server: `pf aihub` runs the v1 aihub
// server (internal/aihub).
//
// Both `pf daemon` and `pf aihub` are intercepted here before the CLI
// dispatch, so each process's flag chain stays its own package's concern
// (the daemon config chain: flags + POLYFORGE_* + the shared PF_* config).
// The binary answers the same whatever name it is started under: the
// commands a workflow step runs are the workflow's own scripts
// (workflows/_shared/scripts), never pf.
package main

import (
	"os"

	"github.com/silgrid/polyforge/internal/aihub"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/daemon"
)

func main() {
	argv := os.Args[1:]
	if len(argv) > 0 && argv[0] == "daemon" {
		os.Exit(daemon.Main(argv[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(argv) > 0 && argv[0] == "aihub" {
		os.Exit(aihub.Main(argv[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(cli.Run(argv, os.Stdin, os.Stdout, os.Stderr))
}
