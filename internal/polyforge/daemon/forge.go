package daemon

// forge.go: the production driver.Forge (polyforge#245, 245-lane). It
// answers the daemon's own command environment for git and gh, and takes a
// step's single-dispatch merge lock through the landing-lease manager
// (landing.go): the same server key and the same before-merge check as the
// lanes' declared interval (decided Q4). The retired in-memory mergeLocks
// is gone; a restart leaves no grant behind (the private journal and its
// recovery own that).
//
// Reachability: daemon code only — the drivers this daemon runs call it
// in-process through Config.Forge; no route serves it. The command
// environment is the daemon's own (the machine's gh account), never a
// step's.

import (
	"context"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// cellForge is the production driver.Forge: one work item's daemon binding.
type cellForge struct {
	d          *Daemon
	wi         string
	capsuleDir string
}

var _ driver.Forge = cellForge{}

// CommandEnv implements driver.Forge.
func (f cellForge) CommandEnv() []string { return f.d.commandEnv() }

// LockRepo implements driver.Forge: the repository's landing lease for one
// dispatch. The driver's key is the normalized origin identity
// (`host/owner/name`, mergeLockKey); the server normalizes a full remote,
// so the identity is handed over as its https form.
func (f cellForge) LockRepo(ctx context.Context, repo string) (func(), error) {
	return f.d.leases.HoldRepo(ctx, f.wi, f.capsuleDir, leaseRepoArg(repo))
}

// leaseRepoArg spells a merge-lock key as a remote the lease server can
// normalize. A key that already carries a scheme is passed through.
func leaseRepoArg(key string) string {
	if strings.Contains(key, "://") {
		return key
	}
	return "https://" + key
}

// leaseHubOf answers the lease client face of hub (nil when the hub is
// not the production aihub client: tests bind none).
func leaseHubOf(hub Hub) leaseHub {
	if c, ok := hub.(*aihubclient.Client); ok {
		return c
	}
	return nil
}
