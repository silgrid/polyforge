package core

// repoidentity_test.go: the repository identity the lease key is built
// from (polyforge#245 Q2/Q4): ssh/https/SCP spellings, a port, a trailing
// slash and a `.git` suffix normalize to one key; a different host or
// path stays a different key; the explicit alias table maps an observed
// spelling to another key; and an alias naming more than one key is
// fail-closed (CORE-403 LEASE_REPO_AMBIGUOUS), never silently picked.

import (
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestRepoIdentityNormalize(t *testing.T) {
	const want = "github.com/silgrid/polyforge"
	for _, raw := range []string{
		"git@github.com:silgrid/polyforge.git",
		"git@github.com:silgrid/polyforge",
		"ssh://git@github.com/silgrid/polyforge.git",
		"ssh://git@github.com:22/silgrid/polyforge",
		"https://github.com/silgrid/polyforge.git",
		"https://github.com/Silgrid/PolyForge.git/",
		"https://user:pass@github.com/silgrid/polyforge",
		"  git@github.com:silgrid/polyforge.git  ",
	} {
		id, err := NormalizeRepoURL(raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", raw, err)
		}
		if got := id.Key(); got != want {
			t.Fatalf("normalize %q = %q, want %q", raw, got, want)
		}
	}
	// A different host or repository path is a different lock.
	for raw, key := range map[string]string{
		"https://github.com/silgrid/other":             "github.com/silgrid/other",
		"https://gitlab.example.com/silgrid/polyforge": "gitlab.example.com/silgrid/polyforge",
		"https://github.com/other/polyforge":           "github.com/other/polyforge",
	} {
		id, err := NormalizeRepoURL(raw)
		if err != nil {
			t.Fatalf("normalize %q: %v", raw, err)
		}
		if id.Key() != key {
			t.Fatalf("normalize %q = %q, want %q", raw, id.Key(), key)
		}
	}
	// Nested groups keep every path segment.
	id, err := NormalizeRepoURL("https://gitlab.example.com/group/sub/repo.git")
	if err != nil || id.Key() != "gitlab.example.com/group/sub/repo" {
		t.Fatalf("nested group = %+v, %v", id, err)
	}
	// A local origin (a development workspace) is its own stable identity.
	for raw, key := range map[string]string{
		"file:///tmp/x/y": "local/path/tmp/x/y",
		"/srv/repos/app":  "local/path/srv/repos/app",
	} {
		id, err := NormalizeRepoURL(raw)
		if err != nil || id.Key() != key {
			t.Fatalf("normalize %q = %+v, %v, want %q", raw, id, err, key)
		}
	}
	for _, raw := range []string{"", "   ", "https://github.com/onlyowner", "not a url", "git@github.com:"} {
		if _, err := NormalizeRepoURL(raw); err == nil {
			t.Fatalf("normalize %q succeeded, want a CORE-400 refusal", raw)
		}
	}
}

func TestRepoIdentityAliases(t *testing.T) {
	_, db, _ := newLeaseEnv(t)
	ids := NewRepoIdentities(db)

	// No alias: the normalized key answers.
	if got, err := ids.Key("git@github.com:silgrid/polyforge.git"); err != nil || got != "github.com/silgrid/polyforge" {
		t.Fatalf("plain key = %q, %v", got, err)
	}
	// One explicit alias maps the normalized spelling to the canonical key.
	if _, err := db.Exec(`INSERT INTO repo_aliases(alias, repo_key) VALUES (?1,?2)`,
		"github.com/silgrid/polyforge", "github.com/silgrid/polyforge-monorepo"); err != nil {
		t.Fatalf("alias insert: %v", err)
	}
	if got, err := ids.Key("https://github.com/silgrid/polyforge.git"); err != nil || got != "github.com/silgrid/polyforge-monorepo" {
		t.Fatalf("aliased key = %q, %v", got, err)
	}
	if got, err := ids.Key("https://github.com/silgrid/polyforge-monorepo"); err != nil || got != "github.com/silgrid/polyforge-monorepo" {
		t.Fatalf("canonical key = %q, %v", got, err)
	}
	// An alias naming two keys pauses the lock instead of picking one.
	if _, err := db.Exec(`INSERT INTO repo_aliases(alias, repo_key) VALUES (?1,?2)`,
		"github.com/silgrid/polyforge", "github.com/silgrid/polyforge-fork"); err != nil {
		t.Fatalf("second alias insert: %v", err)
	}
	_, err := ids.Key("git@github.com:silgrid/polyforge.git")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrAuthForbidden {
		t.Fatalf("ambiguous alias = %v, want CORE-403", err)
	}
	leaseWantErr(t, err, api.ErrAuthForbidden, "LEASE_REPO_AMBIGUOUS")
}
