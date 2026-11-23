package cli

// verbs_firstboot.go — polyforge#176 (84-components-C): `pf
// first-boot`, the workstation container's one-time initialization verb.
// The entrypoint (dist/workstation/entrypoint.sh) runs it before
// `pf supervise`; on a host it is never part of an install (the host
// installer is tools/install.sh).

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/silgrid/polyforge/internal/version"
)

// firstBootUsage is the verb's help text (`pf help first-boot`).
const firstBootUsage = `pf first-boot — the workstation container's first boot (polyforge#176)

Runs once per container start, before pf supervise: installs the image's
component seed into the components volume, establishes the AIHub
identity (the daily user, its key and the local project — through the
local init entry ` + "`pf aihub init`" + `, in process), writes cli.toml's default
Cell (server, api_key, user_id, container_engine, the /workspace
mapping), merges the harness references (the pi packages entry and the
claude skill symlinks, toward /components/current) and publishes
<data-dir>/workstation/first-boot.json — the phase record, never a
credential.

The gate is the settled Q6 with the owner's ruling (2026-11-20, review
r1#1) on the one shape Q6 left undecided — a default Cell with an
in-progress record (this boot's own interruption): a done record plus
the default Cell skips (the first choice — local or external — is made
exactly once); a cell with no record at all, or a done record without
the cell, is an inconsistency the boot refuses to start on, naming both
files; an in-progress record RESUMES — and the record, not the
environment, is the authority for the choice: environment inputs that
disagree with it refuse the start, and the external record's key input
must be set again (a record never carries a credential).

The mode: local by default (the AIHub volume, the identity
transaction, the default Cell pointing at 127.0.0.1:8420); external
when WORKSTATION_AIHUB_SERVER is set (with WORKSTATION_AIHUB_KEY; the
optional WORKSTATION_AIHUB_PROJECT names an already-existing project
for the /workspace mapping — nothing is ever created on the remote).

Flags (the container's canonical defaults; the tests inject their own):
  --data-dir D         the Host data dir (default ~/.local/share/polyforge)
  --aihub-data-dir D   the AIHub data dir (default /var/lib/polyforge/aihub)
  --components-root D  the components volume (default /components)
  --workspace-dir D    the workspace volume's mount (default /workspace)
  --seed-root D        the image's seed root (default /opt/polyforge/seed)
  --agent-dir D        the pi agent dir (default $PI_CODING_AGENT_DIR, else ~/.pi/agent)

Exit status: 0 the boot completed or was skipped (already done); 1 a
stage failed (the record names the phase and the reason) or the Q6
inconsistency refused the start; 2 usage.`

// cmdFirstBoot serves `pf first-boot`.
var cmdFirstBoot = &Command{
	Name:    "first-boot",
	Group:   GroupProcess,
	Summary: "Run the workstation container's first boot: the seed install, the AIHub identity, cli.toml's default Cell, the harness references and the phase record (84-components-C)",
	Usage:   firstBootUsage,
	// NoConfigLoad: the boot WRITES the config — the shared pre-load
	// would refuse on an absent or half-written file, which is exactly
	// the state the boot is called to fix; the Q6 gate reads the file
	// itself.
	NoConfigLoad: true,
	Setup: func(fs *flag.FlagSet) {
		fs.String("data-dir", "", "the Host data dir (default ~/.local/share/polyforge)")
		fs.String("aihub-data-dir", "/var/lib/polyforge/aihub", "the AIHub data dir")
		fs.String("components-root", "/components", "the components volume's root")
		fs.String("workspace-dir", "/workspace", "the workspace volume's mount")
		fs.String("seed-root", "/opt/polyforge/seed", "the image's seed root")
		fs.String("agent-dir", "", "the pi agent dir (default $PI_CODING_AGENT_DIR, else ~/.pi/agent)")
	},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("first-boot: unexpected arguments %v", args)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("first-boot: %w", err)
		}
		o := firstBootOpts{
			Home:        home,
			AgentDir:    os.Getenv("PI_CODING_AGENT_DIR"),
			RepoSlug:    "silgrid/polyforge",
			BaseVersion: version.Version,
			ExtServer:   os.Getenv(EnvWorkstationAihubServer),
			ExtKey:      os.Getenv(EnvWorkstationAihubKey),
			ExtProject:  os.Getenv(EnvWorkstationAihubProject),
		}
		if o.AgentDir == "" {
			o.AgentDir = filepath.Join(home, ".pi", "agent")
		}
		o.ConfigPath = ctx.ConfigPath
		if o.ConfigPath == "" {
			o.ConfigPath = DefaultConfigPath()
		}
		o.DataDir = stringFlag(fs, "data-dir")
		if o.DataDir == "" {
			o.DataDir = DataDir(home)
		}
		o.AihubDataDir = stringFlag(fs, "aihub-data-dir")
		o.ComponentsRoot = stringFlag(fs, "components-root")
		o.WorkspaceDir = stringFlag(fs, "workspace-dir")
		o.SeedRoot = stringFlag(fs, "seed-root")
		if dir := stringFlag(fs, "agent-dir"); dir != "" {
			o.AgentDir = dir
		}
		o.Platform = runtime.GOOS + "/" + runtime.GOARCH
		return runFirstBoot(o, ctx.Stdout)
	},
}
