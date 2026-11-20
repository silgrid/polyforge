package cli

// verbs_components.go (polyforge#190, 84-inplace-upgrade): `pf
// components` — the workstation's in-container component upgrade, the
// CLI face of internal/polyforge/components' upgrade and rollback
// transactions. Inside the workstation image (judged by the one fact
// every consumer of the layout shares, the seed manifest at
// /opt/polyforge/seed) it answers:
//
//	pf components check                          # current, the channel's latest, whether an upgrade waits
//	pf components status                        # the current, previous and the volume's recorded upgrade status
//	pf components upgrade [--version V] [--force] [--seed-dir D]
//	pf components rollback [--force]
//	pf components cancel                         # stand a waiting run down
//
// On a host (no seed manifest) every subcommand refuses and points at
// tools/install.sh: the host installer owns a host. The verb is
// Host-level — the drain and the restart ask take no --profile.

import (
	"context"
	"errors"
	"flag"
)

// workstationBasePf is the bootstrap pf the image's entry point execs
// (polyforge#175's Q5): its version is the running base the install
// transaction's minimum base requirement judges. A package var so the
// tests inject a stub; production never changes it.
var workstationBasePf = "/usr/local/libexec/polyforge/pf"

// errComponentsNotImplemented is the zero-value stub's answer, so the
// first red is an assertion failure, never a compile error.
var errComponentsNotImplemented = errors.New("components: not implemented")

// componentsUsage is the `pf help components` text.
const componentsUsage = `pf components check|status|upgrade|rollback|cancel

The workstation's in-container component upgrade (polyforge#190):
pf itself (daemon and aihub included), the lanes and skills checkout
and Pi Web upgrade inside the container, on the components volume —
current is the single commit point, previous is kept, and the
container is never rebuilt. A harness upgrades only with its own
release (#86 pins them into the manifest first); the base — node,
python, git, podman, pf supervise and the image's own bootstrap pf —
upgrades only with a new image.

  check                 the current version, the channel's latest
                        release and whether an upgrade waits
  status                the current and previous versions and the
                        volume's recorded upgrade status
  upgrade [--version V] [--force] [--seed-dir D]
                        upgrade to V (default: the channel's latest
                        formal release): the release's assets are
                        SHA256SUMS-verified, the version directory
                        assembled, the Host drained (running work goes
                        on, new work is refused DAEMON-409 until the
                        daemon restarts; --force accepts in-flight
                        work), current switched and the daemon and Pi
                        Web relaunched on the new version. --seed-dir D
                        takes a complete local version directory as
                        the source instead of the channel (the operator
                        and smoke path).
  rollback [--force]    swap current and previous, through the same
                        drain and restarts; refused — changing nothing
                        — when there is no previous, when it is
                        corrupt, of another channel, of a manifest
                        schema this build cannot judge, or when the
                        running base cannot run it
  cancel                stand a waiting upgrade or rollback down: the
                        daemon is asked to restart (that restart clears
                        the drain) and nothing else changes

Every subcommand is Host-level: no --profile. On a host (no container
image seed) every subcommand refuses — tools/install.sh owns a host.
The upgrade's progress is on the volume (upgrade-status.json); Pi Web's
card reads it across its own restart.
`

// cmdComponents is the verb's registry entry.
var cmdComponents = &Command{
	Name:    "components",
	Group:   GroupProcess,
	Summary: "The workstation's in-container component upgrade (check, status, upgrade, rollback, cancel — inside the image only; install.sh owns a host)",
	Usage:   componentsUsage,
	Setup:   func(fs *flag.FlagSet) {},
	Run: func(ctx *Context, fs *flag.FlagSet, args []string) error {
		_ = ctx
		_ = args
		return errComponentsNotImplemented
	},
}

// componentsFace adapts the daemon client to the transactions' DaemonFace.
// Not implemented yet (the stub carries the signature alone).
type componentsFace struct {
	d *DaemonClient
}

func (componentsFace) Drain(ctx context.Context, force bool) ([]string, error) {
	_ = ctx
	_ = force
	return nil, errComponentsNotImplemented
}

func (componentsFace) Restart(ctx context.Context) error {
	_ = ctx
	return errComponentsNotImplemented
}
