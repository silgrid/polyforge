package server

// Regression tests for the 2026-10-13 audit items B5 / B11 / B12
// (polyforge#146): memory search honors project=, non-member memory
// reads answer 404, and dependency reads surface DB errors as 5xx.

import (
	"net/http"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// projectIDOfName resolves a project name to its id via the DB.
func projectIDOfName(t *testing.T, e *srvEnv, name string) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name=?1`, name).Scan(&id); err != nil {
		t.Fatalf("resolve project %q: %v", name, err)
	}
	return id
}

// B5: memory search with project=<name> must only return memories of
// that project. Before the fix hSearchMemory dropped the client's
// project parameter, so results spanned every project the caller can read.
func TestReproB5MemorySearchHonorsProjectFilter(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	alphaID := projectIDOfName(t, e, "alpha")

	var alphaMem struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "project",
		"content": "unicorn sighting in alpha territory",
	}, &alphaMem); code != http.StatusCreated || alphaMem.ID == "" {
		t.Fatalf("create alpha memory: %d %s", code, raw)
	}
	var betaMem struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "beta", "type": "experience.debug", "visibility": "project",
		"content": "unicorn sighting in beta territory",
	}, &betaMem); code != http.StatusCreated || betaMem.ID == "" {
		t.Fatalf("create beta memory: %d %s", code, raw)
	}

	// The audit-reported symptom: project=alpha is sent and ignored.
	var search struct {
		Items []struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	code, raw := e.do(t, "GET", "/api/v1/memories/search?q=unicorn&project=alpha", e.key, nil, &search)
	if code != http.StatusOK {
		t.Fatalf("search: %d %s", code, raw)
	}
	if search.Total != 1 {
		t.Fatalf("B5: search with project=alpha returned %d hits, want 1 (only alpha's memory): %s",
			search.Total, raw)
	}
	for _, it := range search.Items {
		if it.ProjectID != alphaID {
			t.Fatalf("B5: hit %s belongs to project %s, want %s (alpha) — the project filter is dropped",
				it.ID, it.ProjectID, alphaID)
		}
	}

	// A caller with no membership in alpha asking for project=alpha gets
	// the same not-found as for an unknown project: neither the project
	// nor its memories are revealed.
	outsider, _ := e.admin.CreateUser("b5-stranger", "b5-stranger@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "o", "")
	for _, p := range []string{"alpha", "no-such-project"} {
		if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=unicorn&project="+p, okey.Plain, nil, nil); code != http.StatusNotFound {
			t.Fatalf("B5: non-member search with project=%s returned %d (%s), want 404", p, code, raw)
		}
	}
	// ... and an unscoped search by the same non-member answers none of
	// alpha's (or beta's) project-visibility memories. This server-side
	// gate is what the CLI's local mapping check on recall/remember (repo
	// review 2026-10-16 D-5) sits in front of, never instead of.
	var unscoped struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=unicorn", okey.Plain, nil, &unscoped); code != http.StatusOK || len(unscoped.Items) != 0 {
		t.Fatalf("non-member unscoped search: %d, %d hit(s) (%s), want 200 with none", code, len(unscoped.Items), raw)
	}
}

// B11: a non-member reading a memory must get 404, not 403 — the object
// must not be revealed to exist. Before the fix memReadAllowed answered
// MEM-403 for non-members.
func TestReproB11NonMemberMemoryReadIs404(t *testing.T) {
	e := newSrvEnv(t)

	var created struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "project",
		"content": "non member read repro",
	}, &created); code != http.StatusCreated || created.ID == "" {
		t.Fatalf("create memory: %d %s", code, raw)
	}

	// A writer with no membership in alpha.
	outsider, _ := e.admin.CreateUser("stranger", "stranger@example.com", "writer", "human")
	okeyIssued, _ := e.admin.CreateAPIKey(outsider.ID, "o", "")

	code, raw := e.do(t, "GET", "/api/v1/memories/"+created.ID, okeyIssued.Plain, nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("B11: non-member memory read returned %d (%s), want 404 (object existence must not leak)", code, raw)
	}
}

// B12: a database error while resolving a work item's dependencies must
// surface as a typed 5xx error, not an empty 200 list.
func TestReproB12DepsDBErrorReturns5xx(t *testing.T) {
	e := newSrvEnv(t)

	var created map[string]any
	code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": "b12 deps error repro"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create WI: %d %s", code, raw)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create WI: no id in %s", raw)
	}

	// Sanity: the healthy deps view answers 200.
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id+"/dependencies", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("healthy deps: %d %s", code, raw)
	}

	// Break exactly the dependency query: drop the table it reads.
	if _, err := e.db.Exec(`DROP TABLE wi_dependencies`); err != nil {
		t.Fatalf("drop wi_dependencies: %v", err)
	}

	code, raw = e.do(t, "GET", "/api/v1/work-items/"+id+"/dependencies", e.key, nil, nil)
	if code < 500 {
		t.Fatalf("B12: dependency query hit a database error but answered %d with body %s, want a typed 5xx error", code, raw)
	}
}
