package server

// carries: internal/aihub/server/*, internal/aihub/core/*, internal/common/api/lease.go

// landinglock_identity_test.go: the landing kind's server-side key
// (polyforge#245 已定 Q2, review r1#2). The daemon names the repository
// and the target branch; the server normalizes the repository through
// the trusted project declaration and the explicit alias table, so two
// daemons spelling one repository differently (ssh / https / a name)
// share one lock, a different branch is a different lock, a repository
// the ticket's project does not declare is refused, and an ambiguous
// alias pauses the lock instead of being taken as a new repository.

import (
	"net/http"
	"testing"
)

func TestLeaseLandingKey(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.db.Exec(`UPDATE projects SET repos=?1 WHERE name='alpha'`,
		`[{"name":"app","url":"git@github.com:o/app.git"}]`); err != nil {
		t.Fatalf("declare project repos: %v", err)
	}
	pub, priv := machineKey(t)
	e.registerSigned(t, e.key, "m-lease", pub, priv)
	wiA := createExecWIOverHTTP(t, e, "landing identity A")
	wiB := createExecWIOverHTTP(t, e, "landing identity B")
	leaseClaim(t, e, wiA, "m-lease", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", e.key)
	leaseClaim(t, e, wiB, "m-lease", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", e.key)

	body := func(repo, branch, requestID, wi string) map[string]any {
		return map[string]any{"repo": repo, "branch": branch, "request_id": requestID, "work_item": wi}
	}
	// The https spelling grants and answers the canonical key (repository
	// identity + target branch).
	code, held, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("https://github.com/o/app.git", "epic/245", "r-a", wiA))
	if code != http.StatusOK || held.Status != "granted" || held.Token == "" {
		t.Fatalf("first acquire = %d %+v %s", code, held, raw)
	}
	if held.Key != "github.com/o/app\nepic/245" {
		t.Fatalf("canonical key = %q, want the normalized repository + newline + branch", held.Key)
	}
	// The ssh spelling of the same repository is the same lock: one
	// holder, one queue.
	code, queued, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("git@github.com:o/app", "epic/245", "r-b", wiB))
	if code != http.StatusOK || queued.Status != "queued" || queued.Position != 1 {
		t.Fatalf("ssh spelling = %d %+v %s, want queued 1 behind the https holder", code, queued, raw)
	}
	// A declared repository name resolves to the same repository.
	code, named, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("app", "epic/245", "r-c", wiA))
	if code != http.StatusOK || named.Status != "queued" || named.Position != 2 {
		t.Fatalf("declared name = %d %+v %s, want queued 2", code, named, raw)
	}
	// A different target branch is a different lock.
	code, branch, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("https://github.com/o/app", "main", "r-d", wiA))
	if code != http.StatusOK || branch.Status != "granted" {
		t.Fatalf("other branch = %d %+v %s, want granted on its own key", code, branch, raw)
	}
	// A repository the ticket's project does not declare is refused.
	if code, _, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("https://github.com/o/other.git", "epic/245", "r-e", wiB)); code != http.StatusForbidden {
		t.Fatalf("undeclared repository = %d %s, want AUTH-403", code, raw)
	}
	// An alias naming two keys pauses the lock.
	if _, err := e.db.Exec(`INSERT INTO repo_aliases(alias, repo_key) VALUES
		('github.com/o/app','github.com/o/one'), ('github.com/o/app','github.com/o/two')`); err != nil {
		t.Fatalf("seed ambiguous alias: %v", err)
	}
	if code, _, raw := leasePost(t, e, leaseKindPath+"/acquire", e.key, "m-lease", priv, 0,
		body("https://github.com/o/app", "epic/245", "r-f", wiB)); code != http.StatusForbidden {
		t.Fatalf("ambiguous alias = %d %s, want AUTH-403 LEASE_REPO_AMBIGUOUS", code, raw)
	}
}
