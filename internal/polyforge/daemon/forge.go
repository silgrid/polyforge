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
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
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
// dispatch. The driver hands a normalized origin identity
// (`host/owner/name`, mergeLockKey); the lease cycle is keyed by the CLAIM
// REPO NAME, because `pf step lock-check --repo <name>` is what every merge
// script calls before its real merge (decided Q6). The key is resolved back
// to the name through the capsule's repos and each clone's origin.
func (f cellForge) LockRepo(ctx context.Context, repo string) (func(), error) {
	return f.d.leases.HoldRepo(ctx, f.wi, f.capsuleDir, claimRepoName(f.capsuleDir, repo))
}

// claimRepoName maps a merge-lock key to the claim repo whose clone carries
// that origin. A key no clone matches falls back to its last path element
// (a repo named by its own name), so a check by name still finds it.
func claimRepoName(capsuleDir, key string) string {
	for _, name := range claimRepoNames(capsuleDir) {
		if originIdentity(repoOriginURL(capsuleDir, name)) == originIdentity(key) {
			return name
		}
	}
	if i := strings.LastIndexAny(key, "/:"); i >= 0 && i+1 < len(key) {
		return strings.TrimSuffix(key[i+1:], ".git")
	}
	return key
}

// claimRepoNames answers the capsule's task repo names (capsule.json).
func claimRepoNames(capsuleDir string) []string {
	var id capsule.Identity
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil || json.Unmarshal(raw, &id) != nil {
		return nil
	}
	out := make([]string, 0, len(id.Repos))
	for name := range id.Repos {
		out = append(out, name)
	}
	return out
}

// repoOriginURL answers the origin remote of a claim repo's clone ("" when
// unreadable).
func repoOriginURL(capsuleDir, name string) string {
	dir := filepath.Join(capsuleDir, "repos", name)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := taskrepo.Command(ctx, nil, dir, "remote", "get-url", "origin")
	var out strings.Builder
	c.Stdout = &out
	if err := c.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// originIdentity normalizes a remote to the identity the driver's
// mergeLockKey answers: a URL or an SCP-style remote to `host/path` (no
// scheme, user, port, trailing `/` or `.git`, lower-cased); a local path
// to the path cleaned.
func originIdentity(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	host, path := "", u
	if scheme, rest, ok := strings.Cut(u, "://"); ok {
		parsed, err := url.Parse(u)
		switch {
		case err == nil && parsed.Host != "":
			host, path = parsed.Hostname(), parsed.Path
		case err == nil:
			path = parsed.Path
		case strings.EqualFold(scheme, "file"):
			path = rest
		}
	} else if i := strings.Index(u, ":"); i > 0 && !strings.ContainsRune(u[:i], '/') {
		host, path = u[:i], u[i+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	path = strings.TrimSuffix(strings.TrimRight(path, "/"), ".git")
	if host == "" {
		return filepath.Clean(path)
	}
	return strings.ToLower(host + "/" + strings.Trim(path, "/"))
}

// leaseHubOf answers the lease client face of hub (nil when the hub is not
// the production aihub client: tests bind none).
func leaseHubOf(hub Hub) leaseHub {
	if c, ok := hub.(*aihubclient.Client); ok {
		return c
	}
	return nil
}
