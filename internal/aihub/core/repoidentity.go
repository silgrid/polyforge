package core

// repoidentity.go: repository identity normalization for the lease base
// (polyforge#245 Q2/Q4). The lease key is the normalized repository
// identity — forge host plus the stable repository path — so ssh, https,
// a trailing `.git`, a port or a differently cased spelling of the same
// repository all name one lock. The `repo_aliases` table carries the
// explicit aliases an operator maintains by hand; an alias that names
// more than one key is ambiguous and pauses the lock (fail-closed)
// instead of being taken as a new repository.
//
// Reachability: the core layer and the lease caller. This unit ships no
// write route for the alias table (decided Q4 C); an operator fills it
// directly.

import (
	"errors"
	"net/url"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// RepoIdentity is one normalized repository identity.
type RepoIdentity struct {
	Host  string
	Owner string
	Name  string
}

// Key answers the lease key: "host/owner/name", all lowercase.
func (r RepoIdentity) Key() string {
	return r.Host + "/" + r.Owner + "/" + r.Name
}

// NormalizeRepoURL normalizes one repository URL or SCP-style remote to
// its identity. Schemes (ssh, git, http, https), user info, ports, a
// trailing `/` and a trailing `.git` are dropped; host and path are
// lowercased. A value that names no repository is CORE-400.
func NormalizeRepoURL(raw string) (RepoIdentity, error) {
	return RepoIdentity{}, errors.New("repoidentity: not implemented")
}

// RepoIdentities resolves repository URLs to lease keys over one store,
// applying the explicit alias table.
type RepoIdentities struct {
	db *DB
}

// NewRepoIdentities assembles the resolver.
func NewRepoIdentities(db *DB) *RepoIdentities {
	return &RepoIdentities{db: db}
}

// Key answers the lease key of one repository URL after the alias table.
// An alias that names more than one key is CORE-403 LEASE_REPO_AMBIGUOUS
// and pauses the lock; it is never silently resolved to one of them.
func (r *RepoIdentities) Key(rawURL string) (string, error) {
	return "", errors.New("repoidentity: not implemented")
}

// parseRepoURL is the scheme/SCP split NormalizeRepoURL builds on.
func parseRepoURL(raw string) (host, path string, err error) {
	return "", "", errors.New("repoidentity: not implemented")
}

var _ = url.Parse
var _ = strings.TrimSpace
var _ = api.ErrCoreBadInput
