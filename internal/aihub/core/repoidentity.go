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
	host, path, err := parseRepoURL(raw)
	if err != nil {
		return RepoIdentity{}, err
	}
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	if host == "" || strings.ContainsAny(host, " /\\") {
		return RepoIdentity{}, Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
			"repository %q carries no forge host", raw)
	}
	segs := strings.Split(path, "/")
	if len(segs) < 2 {
		return RepoIdentity{}, Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
			"repository %q carries no owner/name path", raw)
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return RepoIdentity{}, Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
				"repository %q carries an empty or relative path segment", raw)
		}
	}
	return RepoIdentity{
		Host:  host,
		Owner: strings.ToLower(strings.Join(segs[:len(segs)-1], "/")),
		Name:  strings.ToLower(segs[len(segs)-1]),
	}, nil
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
	id, err := NormalizeRepoURL(rawURL)
	if err != nil {
		return "", err
	}
	key := id.Key()
	rows, err := r.db.Query(`SELECT DISTINCT repo_key FROM repo_aliases WHERE alias=?1 ORDER BY repo_key`, key)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return "", err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(keys) {
	case 0:
		return key, nil
	case 1:
		return keys[0], nil
	default:
		return "", Fail(api.ErrAuthForbidden, "LEASE_REPO_AMBIGUOUS",
			"repository %q is aliased to %d keys (%s); the landing lock pauses until an operator keeps one",
			key, len(keys), strings.Join(keys, ", "))
	}
}

// parseRepoURL splits one remote into its forge host and repository path.
// It accepts the URL forms (ssh/git/http/https) and the SCP-style
// `[user@]host:path` remote git writes.
func parseRepoURL(raw string) (host, path string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID", "empty repository URL")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", "", Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
				"repository %q is not a URL with a host", raw)
		}
		switch strings.ToLower(u.Scheme) {
		case "ssh", "git", "http", "https":
		default:
			return "", "", Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
				"repository %q uses unsupported scheme %q", raw, u.Scheme)
		}
		return u.Hostname(), u.Path, nil
	}
	// SCP-style: [user@]host:path, the host carrying no slash.
	if i := strings.IndexByte(s, ':'); i > 0 && !strings.ContainsAny(s[:i], "/\\") {
		hostPart := s[:i]
		if j := strings.LastIndexByte(hostPart, '@'); j >= 0 {
			hostPart = hostPart[j+1:]
		}
		return hostPart, s[i+1:], nil
	}
	return "", "", Fail(api.ErrCoreBadInput, "LEASE_REPO_INVALID",
		"repository %q is neither a URL nor an SCP-style remote", raw)
}
