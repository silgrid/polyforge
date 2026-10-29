package server

// memory_browse_test.go: the browse mode of
// GET /api/v1/memories/search — an empty q lists, the readable set
// narrows, sort/limit/cursor behave per the ruling, unreadable and
// unknown projects answer the same non-leaking CORE-404, nothing
// readable is an empty page, the keyword answer stays cursor-free and
// byte-identical in shape, q cannot be mixed with after/sort, and an
// unreadable project costs no per-candidate membership query (the
// guarantee, pinned with the counting driver).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// browseMems seeds n memories of typ in project and answers their ids.
func browseMems(t *testing.T, e *srvEnv, project, typ, prefix string, n int, tags []string, visibility string) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("%s %02d", prefix, i)
		var out struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": project, "type": typ, "title": title, "visibility": visibility,
			"content": fmt.Sprintf("w103 browse body %d", i), "tags": tags,
		}, &out); code != http.StatusCreated || out.ID == "" {
			t.Fatalf("create memory %s: %d %s", title, code, raw)
		}
		ids = append(ids, out.ID)
	}
	return ids
}

// browseGet runs one browse (empty-q) request and answers status, items
// and cursor.
func browseGet(t *testing.T, e *srvEnv, key, params string) (int, []string, string, string) {
	t.Helper()
	var r struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total      int    `json:"total"`
		NextCursor string `json:"next_cursor"`
	}
	code, raw := e.do(t, "GET", "/api/v1/memories/search?"+params, key, nil, &r)
	ids := make([]string, len(r.Items))
	for i, it := range r.Items {
		ids[i] = it.ID
	}
	if code == http.StatusOK && r.Total != len(r.Items) {
		t.Fatalf("browse %q: total %d != len(items) %d: %s", params, r.Total, len(r.Items), raw)
	}
	return code, ids, r.NextCursor, string(raw)
}

func TestBrowseEmptyQListsReadableSet(t *testing.T) {
	e := newSrvEnv(t)
	seedBeta(t, e)
	inAlpha := browseMems(t, e, "alpha", "experience.wrap", "W103 A", 2, nil, "project")
	inBeta := browseMems(t, e, "beta", "fact.note", "W103 B", 1, nil, "project")

	// An unscoped admin browses every project.
	code, ids, _, raw := browseGet(t, e, e.key, "limit=20")
	if code != http.StatusOK {
		t.Fatalf("empty q = %d, want 200: %s", code, raw)
	}
	if len(ids) != 3 {
		t.Fatalf("empty q = %d items, want the 3 readable ones: %s", len(ids), raw)
	}
	for _, want := range append(inAlpha, inBeta...) {
		if !containsStr(ids, want) {
			t.Fatalf("empty q misses %s: %s", want, raw)
		}
	}

	// A member of alpha only (key unscoped) browses its readable set.
	_, memberKey := memberUser(t, e, "w103-browse-member", "viewer")
	code, ids, _, raw = browseGet(t, e, memberKey, "limit=20")
	if code != http.StatusOK {
		t.Fatalf("member empty q = %d: %s", code, raw)
	}
	if len(ids) != 2 || !containsStr(ids, inAlpha[0]) {
		t.Fatalf("member browse = %v, want its readable project's memories only: %s", ids, raw)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestBrowseSortAndUnknownSort(t *testing.T) {
	e := newSrvEnv(t)
	ids := browseMems(t, e, "alpha", "fact.note", "W103 sort", 3, nil, "project")

	code, got, _, raw := browseGet(t, e, e.key, "limit=10")
	if code != http.StatusOK {
		t.Fatalf("default browse = %d: %s", code, raw)
	}
	if strings.Join(got, ",") != strings.Join([]string{ids[2], ids[1], ids[0]}, ",") {
		t.Fatalf("default order = %v, want newest-updated first", got)
	}
	// Backdate the newest entry's created_at: sort=created must put it last.
	if _, err := e.db.Exec(`UPDATE memories SET created_at=?1 WHERE id=?2`,
		"2020-01-01T00:00:00Z", ids[2]); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	_, got, _, _ = browseGet(t, e, e.key, "sort=created&limit=10")
	if strings.Join(got, ",") != strings.Join([]string{ids[1], ids[0], ids[2]}, ",") {
		t.Fatalf("sort=created order = %v, want creation-descending with the backdated entry last", got)
	}
	for _, bad := range []string{"asc", "UPDATED", "created_at"} {
		code, _, _, raw = browseGet(t, e, e.key, "sort="+bad)
		if code != http.StatusBadRequest || !strings.Contains(raw, api.ErrMemBadInput) {
			t.Fatalf("sort=%s = %d %s, want MEM-400", bad, code, raw)
		}
		if !strings.Contains(raw, "updated") || !strings.Contains(raw, "created") {
			t.Fatalf("sort=%s refusal does not name both values: %s", bad, raw)
		}
	}
}

func TestBrowseStrictLimit(t *testing.T) {
	e := newSrvEnv(t)
	browseMems(t, e, "alpha", "fact.note", "W103 limit", 25, nil, "project")

	for _, params := range []string{"limit=1", "limit=200", ""} {
		code, ids, _, raw := browseGet(t, e, e.key, params)
		if code != http.StatusOK {
			t.Fatalf("%q = %d: %s", params, code, raw)
		}
		want := 20
		switch params {
		case "limit=1":
			want = 1
		case "limit=200":
			want = 25
		}
		if len(ids) != want {
			t.Fatalf("%q = %d items, want %d", params, len(ids), want)
		}
	}
	for _, bad := range []string{"limit=0", "limit=201", "limit=ten", "limit=-1"} {
		code, _, _, raw := browseGet(t, e, e.key, bad)
		if code != http.StatusBadRequest || !strings.Contains(raw, api.ErrMemBadInput) {
			t.Fatalf("%s = %d %s, want MEM-400 (no silent substitution)", bad, code, raw)
		}
	}
}

func TestBrowseCursorWalkAndEdges(t *testing.T) {
	e := newSrvEnv(t)
	browseMems(t, e, "alpha", "fact.note", "W103 walk", 45, nil, "project")

	seen := map[string]bool{}
	sizes := []int{}
	after := ""
	for {
		params := "limit=20"
		if after != "" {
			params += "&after=" + after
		}
		code, page, next, raw := browseGet(t, e, e.key, params)
		if code != http.StatusOK {
			t.Fatalf("walk page = %d: %s", code, raw)
		}
		sizes = append(sizes, len(page))
		for _, id := range page {
			if seen[id] {
				t.Fatalf("entry %s repeats in the walk", id)
			}
			seen[id] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if fmt.Sprint(sizes) != "[20 20 5]" || len(seen) != 45 {
		t.Fatalf("walk sizes = %v over %d entries, want [20 20 5] over 45", sizes, len(seen))
	}

	// A malformed cursor is refused, never restarted.
	code, _, _, raw := browseGet(t, e, e.key, "limit=2&after=not-a-cursor")
	if code != http.StatusBadRequest || !strings.Contains(raw, api.ErrMemBadInput) {
		t.Fatalf("malformed cursor = %d %s, want MEM-400", code, raw)
	}
	// A cursor issued under sort=created does not serve sort=updated.
	_, _, createdCursor, _ := browseGet(t, e, e.key, "sort=created&limit=2")
	if createdCursor == "" {
		t.Fatal("created-sort page carries no cursor")
	}
	code, _, _, raw = browseGet(t, e, e.key, "limit=2&sort=updated&after="+createdCursor)
	if code != http.StatusBadRequest || !strings.Contains(raw, api.ErrMemBadInput) {
		t.Fatalf("sort-mismatched cursor = %d %s, want MEM-400", code, raw)
	}
	// A valid cursor past the end answers an empty page with no cursor.
	_, _, lastCursor, _ := browseGet(t, e, e.key, "limit=44")
	code, page, next, raw := browseGet(t, e, e.key, "limit=20&after="+lastCursor)
	if code != http.StatusOK || len(page) != 1 || next != "" {
		t.Fatalf("cursor near the end = %d %d items cursor %q, want the one last entry and no cursor: %s",
			code, len(page), next, raw)
	}
}

func TestBrowseFiltersTypeTagsVisibility(t *testing.T) {
	e := newSrvEnv(t)
	browseMems(t, e, "alpha", "experience.wrap", "W103 kept", 1, []string{"a", "b"}, "project")
	browseMems(t, e, "alpha", "experience.debug", "W103 wrong tag", 1, []string{"a"}, "project")
	browseMems(t, e, "alpha", "fact.decision", "W103 wrong type", 1, []string{"a", "b"}, "project")
	browseMems(t, e, "alpha", "experience.wrap", "W103 private", 1, []string{"a", "b"}, "private")
	browseMems(t, e, "alpha", "experience.wrap", "W103 admin tier", 1, []string{"a", "b"}, "admin")

	code, ids, _, raw := browseGet(t, e, e.key, "type=experience.*&tags=a,b&limit=50")
	if code != http.StatusOK {
		t.Fatalf("filtered browse = %d: %s", code, raw)
	}
	// The admin passes every visibility tier: the wrong-type and
	// wrong-tag rows are gone, the tiered ones stay.
	if len(ids) != 3 || !strings.Contains(raw, "W103 kept") {
		t.Fatalf("type+tags browse = %d items, want the three tagged experience rows (admin sees every tier): %s", len(ids), raw)
	}

	// A non-admin member sees the project tiers only.
	_, memberKey := memberUser(t, e, "w103-filter-member", "viewer")
	_, ids, _, _ = browseGet(t, e, memberKey, "limit=50")
	if len(ids) != 3 {
		t.Fatalf("member browse = %d items, want the three project-tier rows", len(ids))
	}
	// The same filter through the member's eyes: the private/admin
	// tiers drop out, the one kept row stays.
	_, ids, _, _ = browseGet(t, e, memberKey, "type=experience.*&tags=a,b&limit=50")
	if len(ids) != 1 {
		t.Fatalf("member filtered browse = %d items, want the one kept row", len(ids))
	}
}

func TestBrowseProjectGateAndNonLeak(t *testing.T) {
	e := newSrvEnv(t)
	seedBeta(t, e)
	browseMems(t, e, "beta", "fact.note", "W103 hidden", 1, nil, "project")
	_, memberKey := memberUser(t, e, "w103-gate-member", "viewer") // member of alpha only

	unreadable, rawUnreadable := e.do(t, "GET", "/api/v1/memories/search?project=beta", memberKey, nil, nil)
	unknown, rawUnknown := e.do(t, "GET", "/api/v1/memories/search?project=nosuch", memberKey, nil, nil)
	if unreadable != http.StatusNotFound || unknown != http.StatusNotFound ||
		!strings.Contains(string(rawUnreadable), api.ErrCoreNotFound) || !strings.Contains(string(rawUnknown), api.ErrCoreNotFound) {
		t.Fatalf("unreadable project = %d %s, unknown = %d %s; want the same CORE-404",
			unreadable, rawUnreadable, unknown, rawUnknown)
	}
	if !strings.Contains(string(rawUnreadable), "project") {
		t.Fatalf("the refusal lost its caller-supplied detail: %s", rawUnreadable)
	}

	// A caller with nothing readable gets an empty page, not an error.
	_, nobody := memberUser(t, e, "w103-nobody", "") // no membership at all
	code, ids, next, raw := browseGet(t, e, nobody, "limit=20")
	if code != http.StatusOK || len(ids) != 0 || next != "" {
		t.Fatalf("nothing-readable browse = %d %d items, want an empty 200 page: %s", code, len(ids), raw)
	}
}

func TestBrowseKeywordPathStaysCursorFreeAndMixedParametersRefused(t *testing.T) {
	e := newSrvEnv(t)
	browseMems(t, e, "alpha", "fact.note", "W103 keyword", 1, nil, "project")

	// The keyword answer keeps the plain {items,total} shape: no cursor.
	code, raw := e.do(t, "GET", "/api/v1/memories/search?q=browse&type=fact.*&tags=a&limit=5", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("keyword search = %d: %s", code, raw)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("keyword answer: %v", err)
	}
	if _, has := fields["next_cursor"]; has {
		t.Fatalf("the keyword answer carries a browse cursor: %s", raw)
	}
	if _, has := fields["after"]; has {
		t.Fatalf("the keyword answer carries an after field: %s", raw)
	}

	for _, params := range []string{
		"q=browse&after=not-a-cursor",
		"q=browse&sort=created",
		"q=browse&after=&sort=",
	} {
		code, raw = e.do(t, "GET", "/api/v1/memories/search?"+params, e.key, nil, nil)
		if code != http.StatusBadRequest || !strings.Contains(string(raw), api.ErrMemBadInput) {
			t.Fatalf("%s = %d %s, want MEM-400 (never a relevance page that ignores the extras)", params, code, raw)
		}
	}
}

// TestBrowseUnreadableProjectsCostNoMembershipQueryPerCandidate pins
// the guarantee on the browse path: the statement count
// and the project_members query count of one page do not grow with the
// number of unreadable candidates (3 vs 300), and the membership query
// runs exactly once.
func TestBrowseUnreadableProjectsCostNoMembershipQueryPerCandidate(t *testing.T) {
	var perUnreadable [2]struct{ total, membership int }
	for i, unreadable := range []int{3, 300} {
		e := newCountingSrvEnv(t)
		seedBeta(t, e)
		_, member := memberUser(t, e, "member", "viewer") // member of alpha only
		for j := 0; j < unreadable; j++ {
			scopeMem(t, e, "beta", fmt.Sprintf("w103browse unreadable match %d", j))
		}
		counts.reset()
		code, ids, _, raw := browseGet(t, e, member, "limit=10")
		if code != http.StatusOK || len(ids) != 0 {
			t.Fatalf("member browse: %d, %d items, want 200 with none: %s", code, len(ids), raw)
		}
		total, membership := counts.snapshot()
		perUnreadable[i].total, perUnreadable[i].membership = total, membership
		if membership != 1 {
			t.Fatalf("%d unreadable candidates: %d project_members statements, want exactly 1", unreadable, membership)
		}
	}
	if perUnreadable[0] != perUnreadable[1] {
		t.Fatalf("statement count grew with unreadable candidates: 3 → %+v, 300 → %+v",
			perUnreadable[0], perUnreadable[1])
	}
}

// TestBrowseUpdatedBetweenPagesDoesNotDuplicateOrSkip drives the
// moved-entry criterion through the API: the entry the page boundary
// already passed does not reappear, the unmoved remainder does not skip.
func TestBrowseUpdatedBetweenPagesDoesNotDuplicateOrSkip(t *testing.T) {
	e := newSrvEnv(t)
	ids := browseMems(t, e, "alpha", "fact.note", "W103 moved", 5, nil, "project")
	code, first, next, raw := browseGet(t, e, e.key, "limit=2")
	if code != http.StatusOK || len(first) != 2 || next == "" {
		t.Fatalf("first page = %d %d items cursor %q: %s", code, len(first), next, raw)
	}
	if _, err := e.db.Exec(`UPDATE memories SET updated_at=?1 WHERE id=?2`,
		time.Now().UTC().Format(time.RFC3339Nano), ids[0]); err != nil {
		t.Fatalf("move: %v", err)
	}
	code, second, _, raw := browseGet(t, e, e.key, "limit=2&after="+next)
	if code != http.StatusOK {
		t.Fatalf("second page = %d: %s", code, raw)
	}
	for _, id := range second {
		if containsStr(first, id) {
			t.Fatalf("second page repeats first-page entry %s: %s", id, raw)
		}
		if id == ids[0] {
			t.Fatalf("the entry moved to the top reappeared below the boundary: %s", raw)
		}
	}
	for _, id := range ids[1:3] {
		if !containsStr(second, id) {
			t.Fatalf("second page skipped unmoved entry %s: %s", id, raw)
		}
	}
}
