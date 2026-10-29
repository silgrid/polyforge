package server

// memory_search_scope_test.go: the search face pushes the
// caller's readable-project set (key scope ∩ membership) into the
// candidate SQL — unreadable matches cost nothing per candidate (the
// counting driver pins the statement and membership-query counts), the
// results and their order are unchanged (the member's answer equals the
// admin's full answer restricted to the readable projects), the
// visibility tiers still hide private/admin memories, and a scoped
// admin key never leaks past its scope (the behaviour correction the
// pre- tree had wrong).

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// scopeMem seeds one project-visible memory and answers its id.
func scopeMem(t *testing.T, e *srvEnv, project, content string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": project, "type": "experience.debug", "visibility": "project", "content": content,
	}, &out); code != http.StatusCreated || out.ID == "" {
		t.Fatalf("create memory in %s: %d %s", project, code, raw)
	}
	return out.ID
}

// scopeSearch runs one search and answers status, item ids and project ids.
func scopeSearch(t *testing.T, e *srvEnv, key, params string) (int, []string, []string) {
	t.Helper()
	var r struct {
		Items []struct {
			ID        string `json:"id"`
			ProjectID string `json:"project_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	code, raw := e.do(t, "GET", "/api/v1/memories/search?"+params, key, nil, &r)
	ids := make([]string, 0, len(r.Items))
	projects := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		ids = append(ids, it.ID)
		projects = append(projects, it.ProjectID)
	}
	if r.Total != len(r.Items) {
		t.Fatalf("search %q: total %d != len(items) %d: %s", params, r.Total, len(r.Items), raw)
	}
	return code, ids, projects
}

// seedBeta creates project beta (alpha exists in every env) and answers
// its id.
func seedBeta(t *testing.T, e *srvEnv) string {
	t.Helper()
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	return projectIDOfName(t, e, "beta")
}

// TestSearchUnreadableMatchesCostNothingPerCandidate: the statement and
// membership-query counts of one search do not grow with the number of
// matches the caller cannot read (3 vs 300 unreadable matches — far past
// one 4×limit candidate window, so a per-window cost would show), and the
// membership query happens exactly once. Red on the pre-
// tree: every unreadable match cost one canRead membership lookup; red
// on the first tree too: the narrowing sat in ftsMeta only,
// so each window of 16 unreadable rowids cost one more statement.
func TestSearchUnreadableMatchesCostNothingPerCandidate(t *testing.T) {
	var perUnreadable [2]struct{ total, membership int }
	for i, unreadable := range []int{3, 300} {
		e := newCountingSrvEnv(t)
		seedBeta(t, e)
		_, member := memberUser(t, e, "member", "writer") // member of alpha only
		for j := 0; j < unreadable; j++ {
			scopeMem(t, e, "beta", fmt.Sprintf("w81needle beta match %d", j))
		}
		counts.reset()
		code, ids, projects := scopeSearch(t, e, member, "q=w81needle&limit=10")
		if code != http.StatusOK {
			t.Fatalf("member search: %d", code)
		}
		if len(ids) != 0 {
			t.Fatalf("member saw %d unreadable matches: %v %v", len(ids), ids, projects)
		}
		total, membership := counts.snapshot()
		perUnreadable[i].total, perUnreadable[i].membership = total, membership
		if membership != 1 {
			t.Fatalf("%d unreadable matches: %d project_members statements, want exactly 1", unreadable, membership)
		}
	}
	if perUnreadable[0] != perUnreadable[1] {
		t.Fatalf("statement count grew with unreadable matches: 3 → %+v, 300 → %+v (total/membership)", perUnreadable[0], perUnreadable[1])
	}
}

// TestSearchNoMembershipCallerGetsEmptyResult: a caller with no
// membership anywhere and no admin role gets the empty answer, with the
// membership query made exactly once and no candidate query at all.
func TestSearchNoMembershipCallerGetsEmptyResult(t *testing.T) {
	e := newCountingSrvEnv(t)
	seedBeta(t, e)
	_, outsider := memberUser(t, e, "outsider", "") // no project_members row at all
	for j := 0; j < 5; j++ {
		scopeMem(t, e, "alpha", fmt.Sprintf("w81needle alpha match %d", j))
	}
	counts.reset()
	code, ids, _ := scopeSearch(t, e, outsider, "q=w81needle&limit=10")
	if code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("outsider search: %d, %d hits, want 200 with none", code, len(ids))
	}
	total, membership := counts.snapshot()
	if membership != 1 {
		t.Fatalf("%d project_members statements, want exactly 1", membership)
	}
	if total != 2 { // authenticate + the one membership query; no candidate query
		t.Fatalf("%d statements, want 2 (auth + membership, no candidate query)", total)
	}
}

// TestSearchReadablePathBoundedToo: on the readable path the statement
// count stays constant far beyond one candidate window, with every
// bm25 rank tied — identical-length bodies, the shape that defeats any
// rank-based early stop. 6 vs 300 readable matches against limit=5
// (the second is 15 windows deep) assert equal total statement counts;
// the membership query happens exactly once, and the per-candidate
// entry loads are always exactly limit — never one per readable match.
// Red on the early-stopping ftsPage tree: among tied ranks its meta
// batches read every match (about 1+k·M/16 statements).
func TestSearchReadablePathBoundedToo(t *testing.T) {
	var perReadable [2]struct{ total, membership, loads int }
	for i, readable := range []int{6, 300} { // the second is far past one 4×limit=20 window
		e := newCountingSrvEnv(t)
		_, member := memberUser(t, e, "member", "writer")
		for j := 0; j < readable; j++ {
			// Identical-length bodies, one needle occurrence each: every
			// match carries the same bm25 rank, so no rank-order argument
			// can bound the page — only the statement shape can.
			scopeMem(t, e, "alpha", fmt.Sprintf("w81needle alpha match %03d", j))
		}
		counts.reset()
		code, ids, projects := scopeSearch(t, e, member, "q=w81needle&limit=5")
		if code != http.StatusOK {
			t.Fatalf("member search: %d", code)
		}
		want := min(5, readable)
		if len(ids) != want {
			t.Fatalf("member got %d hits, want min(5, %d) = %d: %v %v", len(ids), readable, want, ids, projects)
		}
		total, membership := counts.snapshot()
		perReadable[i].total, perReadable[i].membership, perReadable[i].loads = total, membership, counts.having("FROM memories WHERE id=")
		if membership != 1 {
			t.Fatalf("%d readable matches: %d membership statements, want exactly 1", readable, membership)
		}
		if perReadable[i].loads != want {
			t.Fatalf("%d readable matches: %d per-candidate loads, want exactly the answer size %d", readable, perReadable[i].loads, want)
		}
	}
	if perReadable[0] != perReadable[1] {
		t.Fatalf("statement count grew with readable matches: 6 → %+v, 300 → %+v (total/membership/loads)", perReadable[0], perReadable[1])
	}
}

// TestMemberSearchOrderMatchesAdminReadableSubsequence: a member's
// answer is exactly the admin's full answer restricted to the readable
// projects, in the same freshness-weighted order — the pushdown changed
// the cost, not the result.
func TestMemberSearchOrderMatchesAdminReadableSubsequence(t *testing.T) {
	e := newSrvEnv(t)
	seedBeta(t, e)
	_, member := memberUser(t, e, "member", "writer")
	// Interleave the projects' matches so no insertion-order accident can
	// explain the answer.
	for j := 0; j < 6; j++ {
		scopeMem(t, e, "alpha", fmt.Sprintf("w81order alpha needle %d with body filler to vary rank", j))
		scopeMem(t, e, "beta", fmt.Sprintf("w81order beta needle %d with body filler to vary rank", j))
	}
	_, adminAll, _ := scopeSearch(t, e, e.key, "q=w81order&limit=50")
	if len(adminAll) != 12 {
		t.Fatalf("admin sees %d matches, want 12", len(adminAll))
	}
	alphaID := projectIDOfName(t, e, "alpha")
	want := []string{}
	for _, id := range adminAll {
		if e.memProject(t, id) == alphaID {
			want = append(want, id)
		}
	}
	_ = alphaID
	if len(want) != 6 {
		t.Fatalf("admin answer holds %d alpha matches, want 6", len(want))
	}
	code, got, _ := scopeSearch(t, e, member, "q=w81order&limit=50")
	if code != http.StatusOK || len(got) != 6 {
		t.Fatalf("member full search: %d, %d hits, want 6", code, len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("member order diverges at %d: %v, want %v", i, got, want)
		}
	}
	// A smaller limit takes the same leading prefix.
	code, got, _ = scopeSearch(t, e, member, "q=w81order&limit=3")
	if code != http.StatusOK || len(got) != 3 {
		t.Fatalf("member limit=3: %d, %d hits, want 3", code, len(got))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("member limit=3 order diverges at %d: %v, want %v", i, got, want)
		}
	}
}

// TestScopedAdminSearchDoesNotLeak: an admin key scoped to one project
// never reads past its scope, with or without project=. Red on the
// pre- tree: the old search path skipped membership for
// admins and never applied the key scope, answering both projects.
func TestScopedAdminSearchDoesNotLeak(t *testing.T) {
	e := newSrvEnv(t)
	seedBeta(t, e)
	alphaID := projectIDOfName(t, e, "alpha")
	mine := scopeMem(t, e, "alpha", "w81leak: memory inside the scoped project")
	scopeMem(t, e, "beta", "w81leak: memory outside the scoped project")
	issued, err := e.admin.CreateAPIKey(e.adminID, "scoped-admin", alphaID)
	if err != nil {
		t.Fatalf("scoped admin key: %v", err)
	}
	code, got, _ := scopeSearch(t, e, issued.Plain, "q=w81leak&limit=50")
	if code != http.StatusOK || len(got) != 1 || got[0] != mine {
		t.Fatalf("scoped admin search = %v (code %d), want exactly the in-scope memory %s", got, code, mine)
	}
	// project= outside the scope stays refused by the read gate — the
	// pre-existing AUTH-403 shape (the scope check fires before the
	// not-found folding).
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=w81leak&project=beta", issued.Plain, nil, nil); code != http.StatusForbidden {
		t.Fatalf("scoped admin project=beta answered %d %s, want the out-of-scope 403 (the scope check fires before the not-found folding)", code, raw)
	}
}

// TestSearchVisibilityTiersStillHidden: the pushed-down membership set
// does not weaken the visibility tiers — a member sees its own private
// memory and its project's shared memory, but not another user's private
// memory nor an admin-tier memory of a project it reads; the admin sees
// all of them.
func TestSearchVisibilityTiersStillHidden(t *testing.T) {
	e := newSrvEnv(t)
	memberID, member := memberUser(t, e, "member", "writer")
	mk := func(visibility, content string) string {
		t.Helper()
		var out struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": "alpha", "type": "experience.debug", "visibility": visibility, "content": content,
		}, &out); code != http.StatusCreated {
			t.Fatalf("create %s memory: %d %s", visibility, code, raw)
		}
		return out.ID
	}
	shared := mk("project", "w81vis shared project memory")
	foreignPrivate := mk("private", "w81vis someone else's private memory")
	adminTier := mk("admin", "w81vis admin-tier memory")
	// The member's own private memory in the same project.
	var own struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", member, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "private", "content": "w81vis own private memory",
	}, &own); code != http.StatusCreated {
		t.Fatalf("member create private: %d %s", code, raw)
	}
	_ = memberID
	code, got, _ := scopeSearch(t, e, member, "q=w81vis&limit=50")
	if code != http.StatusOK {
		t.Fatalf("member search: %d", code)
	}
	for _, id := range got {
		if id == foreignPrivate || id == adminTier {
			t.Fatalf("member search leaked a hidden-tier memory %s: %v", id, got)
		}
	}
	if len(got) != 2 || !contains(got, shared) || !contains(got, own.ID) {
		t.Fatalf("member search = %v, want exactly [shared %s, own private %s]", got, shared, own.ID)
	}
	// The admin sees all four.
	_, adminGot, _ := scopeSearch(t, e, e.key, "q=w81vis&limit=50")
	if len(adminGot) != 4 {
		t.Fatalf("admin search = %v, want all four", adminGot)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// memProject answers a memory's project id straight from the store row.
func (e *srvEnv) memProject(t *testing.T, memID string) string {
	t.Helper()
	var p string
	if err := e.db.QueryRow(`SELECT project_id FROM memories WHERE id=?1`, memID).Scan(&p); err != nil {
		t.Fatalf("project of %s: %v", memID, err)
	}
	return p
}
