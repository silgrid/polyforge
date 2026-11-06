package core

// workitem_web_test.go: the console list read — the
// fixed 20-row page with the keyset walk, the readable-set narrowing
// inside the SQL (before the page cut), the status filter, the
// case-insensitive goal/content substring search, equal-timestamp
// cursor edges, the same-millisecond insertion order
// and the typed refusals.

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// webCreate creates one work item in project alpha and returns its id.
func webCreate(t *testing.T, env *testEnv, goal, content string) string {
	t.Helper()
	req := CreateRequest{Project: "alpha", Goal: goal, Reporter: env.UserID}
	if content != "" {
		req.Content = strPtrTest(content)
	}
	wi, err := env.WIs.Create(req)
	if err != nil {
		t.Fatalf("create %q: %v", goal, err)
	}
	return wi.ID
}

func TestWebListPagesExactlyOnce(t *testing.T) {
	env := newEnv(t)
	ids := make([]string, 0, 45)
	for i := 0; i < 45; i++ {
		ids = append(ids, webCreate(t, env, "web page item "+string(rune('a'+i%26))+strings.Repeat("x", i%3), ""))
	}
	seen := map[string]int{}
	sizes := []int{}
	cursor := ""
	for page := 1; page <= 6; page++ {
		items, next, err := env.WIs.ListWeb(WebListFilters{
			ProjectID: env.Project, After: cursor,
			ReadableProjects: []string{env.Project},
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		sizes = append(sizes, len(items))
		for _, it := range items {
			seen[it.ID]++
			if seen[it.ID] > 1 {
				t.Fatalf("item %s appears on two pages", it.ID)
			}
			if it.Slug == "" || it.CreatedAt == "" {
				t.Fatalf("list row misses fields: %#v", it)
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 45 {
		t.Fatalf("the walk covered %d distinct items, want 45", len(seen))
	}
	if len(sizes) != 3 || sizes[0] != 20 || sizes[1] != 20 || sizes[2] != 5 {
		t.Fatalf("page sizes = %v, want [20 20 5]", sizes)
	}
	// Newest first: the first page's first row is the newest creation.
	first, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, ReadableProjects: []string{env.Project}})
	if err != nil || len(first) == 0 {
		t.Fatalf("list: %v", err)
	}
	if first[0].ID != ids[44] {
		t.Fatalf("first row = %s, want the newest item %s", first[0].ID, ids[44])
	}
}

func TestWebListReadableSetNarrowsBeforeThePageCut(t *testing.T) {
	env := newEnv(t)
	secret, err := env.Admin.CreateProject(CreateProjectRequest{Name: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.Create(CreateRequest{Project: "secret", Goal: "hidden web item", Reporter: env.UserID}); err != nil {
		t.Fatal(err)
	}
	own := webCreate(t, env, "readable web item", "")
	// A readable set that excludes nothing still finds only the own
	// project's rows; an empty readable set answers empty without a
	// query.
	items, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, ReadableProjects: []string{env.Project}})
	if err != nil || len(items) != 1 || items[0].ID != own {
		t.Fatalf("project list = %v (err %v), want only %s", items, err, own)
	}
	items, _, err = env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, ReadableProjects: []string{}})
	if err != nil || len(items) != 0 {
		t.Fatalf("empty readable set = %v (err %v), want the empty page", items, err)
	}
	// A readable set narrowed to another project never leaks this one.
	items, _, err = env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, ReadableProjects: []string{secret.ID}})
	if err != nil || len(items) != 0 {
		t.Fatalf("foreign readable set = %v (err %v), want empty", items, err)
	}
}

func TestWebListStatusFilterAndSearch(t *testing.T) {
	env := newEnv(t)
	alpha := webCreate(t, env, "WEB Alpha Needle goal", "")
	beta := webCreate(t, env, "WEB beta title", "body with NEEDLE inside")
	webCreate(t, env, "WEB gamma title", "")
	webCreate(t, env, "WEB delta title", "")
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.Create(CreateRequest{Project: "elsewhere", Goal: "WEB foreign needle", Reporter: env.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.Cancel(beta, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	f := WebListFilters{ProjectID: env.Project, ReadableProjects: []string{env.Project}}

	// Status filter.
	items, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, Status: api.StatusCanceled, ReadableProjects: []string{env.Project}})
	if err != nil || len(items) != 1 || items[0].ID != beta {
		t.Fatalf("status filter = %v (err %v), want only %s", items, err, beta)
	}

	// Case-insensitive substring over goal AND content, never labels,
	// never another project.
	for _, q := range []string{"needle", "NEEDLE", "nEeDlE"} {
		items, _, err = env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, Search: q, ReadableProjects: []string{env.Project}})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		got := map[string]bool{}
		for _, it := range items {
			got[it.ID] = true
		}
		if !got[alpha] || !got[beta] {
			t.Fatalf("search %q = %v, want %s and %s", q, got, alpha, beta)
		}
		if len(items) != 2 {
			t.Fatalf("search %q matched %d rows, want exactly 2", q, len(items))
		}
	}

	// Composition: status + search.
	items, _, err = env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, Status: api.StatusCanceled, Search: "needle", ReadableProjects: []string{env.Project}})
	if err != nil || len(items) != 1 || items[0].ID != beta {
		t.Fatalf("composed filter = %v (err %v), want only the canceled item", items, err)
	}
	_ = f
}

func TestWebListCursorEdgesAndEqualTimestamps(t *testing.T) {
	env := newEnv(t)
	// Seed rows with EXACTLY equal created_at values: the seq tiebreak
	// keeps the walk exact (the string-ordering hazard this read is
	// built against).
	const same = "2026-01-02T03:04:05.000000000Z"
	ids := []string{}
	for i := 0; i < 25; i++ {
		wi, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "same-ms item", Reporter: env.UserID})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, wi.ID)
		if _, err := env.DB.Exec(`UPDATE work_items SET created_at=?1 WHERE id=?2`, same, wi.ID); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 4 {
			t.Fatal("the equal-timestamp walk did not end in 2 pages")
		}
		items, next, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, After: cursor, ReadableProjects: []string{env.Project}})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("item %s appears on two pages", it.ID)
			}
			seen[it.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 25 || pages != 2 {
		t.Fatalf("walk covered %d items in %d pages, want 25 in 2 (20+5)", len(seen), pages)
	}

	// A cursor this code did not issue is refused.
	if _, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, After: "not-a-cursor", ReadableProjects: []string{env.Project}}); err == nil {
		t.Fatal("a malformed cursor was accepted")
	}
	// An unknown status is refused.
	if _, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, Status: "nope", ReadableProjects: []string{env.Project}}); err == nil {
		t.Fatal("an unknown status filter was accepted")
	}
}

// TestWebListSameMillisecondNewestCreatedFirst: "newest
// first" holds inside one millisecond too. julianday() rounds a
// created_at to the nearest millisecond, so two creations less than a
// millisecond apart tie on the primary key; the tiebreak must be the
// project's insertion order (seq), never the random id — the random id
// made TestWebListPagesExactlyOnce's "first row is the newest" flaky on
// a 2-core runner.
func TestWebListSameMillisecondNewestCreatedFirst(t *testing.T) {
	env := newEnv(t)
	const same = "2026-01-02T03:04:05.000000000Z"
	ids := []string{}
	for i := 0; i < 25; i++ {
		id := webCreate(t, env, "same-ms ordered item", "")
		ids = append(ids, id)
		if _, err := env.DB.Exec(`UPDATE work_items SET created_at=?1 WHERE id=?2`, same, id); err != nil {
			t.Fatal(err)
		}
	}
	// The walk (20 + 5 across the cursor) is exactly the reverse
	// creation order: no duplicate, no gap, the newest first.
	walk := []string{}
	sizes := []int{}
	cursor := ""
	for page := 1; page <= 4; page++ {
		items, next, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, After: cursor, ReadableProjects: []string{env.Project}})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		sizes = append(sizes, len(items))
		for _, it := range items {
			walk = append(walk, it.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(sizes) != 2 || sizes[0] != 20 || sizes[1] != 5 {
		t.Fatalf("page sizes = %v, want [20 5]", sizes)
	}
	for i, id := range walk {
		if want := ids[len(ids)-1-i]; id != want {
			t.Fatalf("walk[%d] = %s, want %s (reverse creation order)", i, id, want)
		}
	}

	// Two distinct timestamps that julianday() rounds into one
	// millisecond (a pair a reproduction run produced): the later
	// creation still comes first.
	older := webCreate(t, env, "sub-ms older", "")
	newer := webCreate(t, env, "sub-ms newer", "")
	for id, ts := range map[string]string{
		older: "2026-11-12T16:25:09.311538009Z",
		newer: "2026-11-12T16:25:09.312489656Z",
	} {
		if _, err := env.DB.Exec(`UPDATE work_items SET created_at=?1 WHERE id=?2`, ts, id); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, ReadableProjects: []string{env.Project}})
	if err != nil || len(items) < 2 {
		t.Fatalf("list: %v (%d rows)", err, len(items))
	}
	if items[0].ID != newer || items[1].ID != older {
		t.Fatalf("first rows = %s, %s; want the newer %s then the older %s", items[0].ID, items[1].ID, newer, older)
	}
}

// TestWebListRefusesVersion1Cursor: the cursor moved to
// version 2 (it carries seq, not the id); a version-1 cursor is refused
// as bad input, never misread (no compatibility path).
func TestWebListRefusesVersion1Cursor(t *testing.T) {
	env := newEnv(t)
	id := webCreate(t, env, "v1 cursor item", "")
	webCreate(t, env, "v1 cursor older item", "")
	var createdAt string
	if err := env.DB.QueryRow(`SELECT created_at FROM work_items WHERE id=?1`, id).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	v1 := base64.RawURLEncoding.EncodeToString([]byte("1|" + createdAt + "|" + id))
	_, _, err := env.WIs.ListWeb(WebListFilters{ProjectID: env.Project, After: v1, ReadableProjects: []string{env.Project}})
	if !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("a version-1 cursor answered err %v, want %s", err, api.ErrCoreBadInput)
	}
}
