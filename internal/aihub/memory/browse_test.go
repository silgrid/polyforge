package memory

// browse_test.go: the store's browse mode — descending
// updated (default) / created order, unknown sort refused naming both
// values, the 45/20 walk covering every entry exactly once, keyset
// behaviour across a between-pages write, past-end and malformed /
// sort-mismatched cursors, the type/tag/visibility/project filters and
// the empty-readable-set short-circuit.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// browseAll is the permissive visibility predicate (an unscoped admin).
func browseAll(*Entry) bool { return true }

func (e *env) browseEntry(t *testing.T, typ, title, content string, tags []string, visibility string) *Entry {
	t.Helper()
	entry, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: typ, Title: &title, Visibility: visibility,
		Content: content, Tags: tags,
	}, e.user)
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return entry
}

func browseIDs(items []Entry) string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return strings.Join(ids, ",")
}

func TestBrowseDefaultOrderIsUpdatedDescending(t *testing.T) {
	e := newEnv(t)
	m1 := e.browseEntry(t, "fact.note", "one", "browse body", nil, "project")
	m2 := e.browseEntry(t, "fact.note", "two", "browse body", nil, "project")
	m3 := e.browseEntry(t, "fact.note", "three", "browse body", nil, "project")

	items, next, err := e.store.Browse(nil, "", "", "", nil, 20, browseAll)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if got := browseIDs(items); got != strings.Join([]string{m3.ID, m2.ID, m1.ID}, ",") {
		t.Fatalf("default order = %s, want newest-updated first", got)
	}
	if next != "" {
		t.Fatalf("single page carries a next cursor: %q", next)
	}
}

func TestBrowseSortCreatedAndUnknownSort(t *testing.T) {
	e := newEnv(t)
	m1 := e.browseEntry(t, "fact.note", "one", "browse body", nil, "project")
	m2 := e.browseEntry(t, "fact.note", "two", "browse body", nil, "project")
	m3 := e.browseEntry(t, "fact.note", "three", "browse body", nil, "project")
	// m3 was created last but updated before m2 (a direct backdate of
	// created_at, the shape a between-pages write leaves behind).
	if _, err := e.db.Exec(`UPDATE memories SET created_at=?1 WHERE id=?2`,
		"2020-01-01T00:00:00Z", m3.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	items, _, err := e.store.Browse(nil, "created", "", "", nil, 20, browseAll)
	if err != nil {
		t.Fatalf("browse created: %v", err)
	}
	if got := browseIDs(items); got != strings.Join([]string{m2.ID, m1.ID, m3.ID}, ",") {
		t.Fatalf("sort=created order = %s, want creation-descending with the backdated entry last", got)
	}
	for _, bad := range []string{"asc", "created_at", "UPDATED"} {
		_, _, err := e.store.Browse(nil, bad, "", "", nil, 20, browseAll)
		var ae *APIError
		if !asAPIError(err, &ae) || ae.Code != api.ErrMemBadInput {
			t.Fatalf("sort=%s = %v, want MEM-400", bad, err)
		}
		if !strings.Contains(ae.Detail, "updated") || !strings.Contains(ae.Detail, "created") {
			t.Fatalf("sort=%s refusal does not name both values: %s", bad, ae.Detail)
		}
	}
}

func asAPIError(err error, out **APIError) bool {
	if err == nil {
		return false
	}
	if ae, ok := err.(*APIError); ok {
		*out = ae
		return true
	}
	return false
}

func TestBrowseWalks45InPagesOf20(t *testing.T) {
	e := newEnv(t)
	all := make([]string, 45)
	for i := range all {
		all[i] = e.browseEntry(t, "fact.note", "walk", "walk body", nil, "project").ID
	}
	seen := map[string]bool{}
	sizes := []int{}
	after := ""
	for {
		items, next, err := e.store.Browse(nil, "", after, "", nil, 20, browseAll)
		if err != nil {
			t.Fatalf("browse after %q: %v", after, err)
		}
		sizes = append(sizes, len(items))
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("entry %s repeats in the walk", it.ID)
			}
			seen[it.ID] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if sizes[len(sizes)-1] != 5 || len(sizes) != 3 {
		t.Fatalf("page sizes = %v, want three pages ending in 5", sizes)
	}
	if len(seen) != 45 {
		t.Fatalf("the walk covered %d of 45 entries", len(seen))
	}
}

func TestBrowseEntryMovedBetweenPages(t *testing.T) {
	e := newEnv(t)
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = e.browseEntry(t, "fact.note", "moved", "moved body", nil, "project").ID
	}
	first, next, err := e.store.Browse(nil, "", "", "", nil, 2, browseAll)
	if err != nil || len(first) != 2 || next == "" {
		t.Fatalf("first page = %d items cursor %q err %v", len(first), next, err)
	}
	// Move the oldest entry (below the boundary) to the top; the walk
	// must neither repeat it nor skip an unmoved one.
	if _, err := e.db.Exec(`UPDATE memories SET updated_at=?1 WHERE id=?2`,
		time.Now().UTC().Format(time.RFC3339Nano), ids[0]); err != nil {
		t.Fatalf("move: %v", err)
	}
	second, next2, err := e.store.Browse(nil, "", next, "", nil, 2, browseAll)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	got := map[string]bool{}
	for _, it := range second {
		if it.ID == ids[0] {
			t.Fatalf("the moved entry reappeared below the boundary: %s", browseIDs(second))
		}
		got[it.ID] = true
	}
	if !got[ids[1]] || !got[ids[2]] {
		t.Fatalf("the second page skipped unmoved entries: %s", browseIDs(second))
	}
	if len(got) != 2 {
		t.Fatalf("second page = %s, want the two unmoved entries", browseIDs(second))
	}
	if next2 != "" {
		t.Fatalf("second page carries a cursor although nothing remains below the boundary: %q", next2)
	}
}

func TestBrowseCursorEdgeCases(t *testing.T) {
	e := newEnv(t)
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = e.browseEntry(t, "fact.note", "cursor", "cursor body", nil, "project").ID
	}
	_, next, err := e.store.Browse(nil, "", "", "", nil, 1, browseAll)
	if err != nil || next == "" {
		t.Fatalf("setup page: %d items, cursor %q, err %v", 1, next, err)
	}

	// A malformed cursor is refused, never restarted.
	if _, _, err := e.store.Browse(nil, "", "not-a-cursor", "", nil, 2, browseAll); err == nil {
		t.Fatal("malformed cursor was accepted")
	} else {
		var ae *APIError
		if !asAPIError(err, &ae) || ae.Code != api.ErrMemBadInput {
			t.Fatalf("malformed cursor = %v, want MEM-400", err)
		}
	}
	// A cursor issued under another sort is refused the same way.
	_, createdCursor, err := e.store.Browse(nil, "created", "", "", nil, 2, browseAll)
	if err != nil || createdCursor == "" {
		t.Fatalf("created-sort page: cursor %q, err %v", createdCursor, err)
	}
	if _, _, err := e.store.Browse(nil, "", createdCursor, "", nil, 2, browseAll); err == nil {
		t.Fatal("sort-mismatched cursor was accepted")
	}

	// A valid cursor positioned past the last entry answers an empty
	// page with no cursor: redact everything it still points at.
	pastEnd := next
	for _, id := range ids {
		if err := e.store.Redact(id); err != nil {
			t.Fatalf("redact %s: %v", id, err)
		}
	}
	items, next2, err := e.store.Browse(nil, "", pastEnd, "", nil, 2, browseAll)
	if err != nil {
		t.Fatalf("past-end browse: %v", err)
	}
	if len(items) != 0 || next2 != "" {
		t.Fatalf("past-end page = %d items cursor %q, want an empty page with no cursor", len(items), next2)
	}
}

func TestBrowseFiltersTypeTagsVisibilityAndProjects(t *testing.T) {
	e := newEnv(t)
	beta := seedBeta(t, e)
	kept := e.browseEntry(t, "experience.wrap", "kept", "gate body", []string{"a", "b"}, "project")
	e.browseEntry(t, "experience.debug", "wrong-tag", "gate body", []string{"a"}, "project")
	e.browseEntry(t, "fact.decision", "wrong-type", "gate body", []string{"a", "b"}, "project")
	private := e.browseEntry(t, "experience.wrap", "private", "gate body", []string{"a", "b"}, "private")
	otherOwner := e.browseEntry(t, "experience.wrap", "foreign", "gate body", []string{"a", "b"}, "project")
	if _, err := e.db.Exec(`UPDATE memories SET project_id=?1 WHERE id=?2`,
		beta, otherOwner.ID); err != nil {
		t.Fatalf("foreign project: %v", err)
	}

	// type prefix + every-tag AND + the readable-set narrowing.
	items, _, err := e.store.Browse([]string{alphaID(t, e)}, "", "", "experience.*", []string{"a", "b"}, 20, browseAll)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(items) != 2 || !containsIDs(items, kept.ID, private.ID) {
		t.Fatalf("type+tags browse = %s, want %s and %s (the private one keeps its visibility tier for the predicate)",
			browseIDs(items), kept.ID, private.ID)
	}
	// The visibility predicate (the server layer's memVisible) drops the
	// private entry for a non-owner non-admin caller.
	visible := func(en *Entry) bool { return en.Visibility != "private" }
	items, _, err = e.store.Browse([]string{alphaID(t, e)}, "", "", "experience.*", []string{"a", "b"}, 20, visible)
	if err != nil {
		t.Fatalf("browse: %v", err)
	}
	if len(items) != 1 || items[0].ID != kept.ID {
		t.Fatalf("visibility browse = %s, want %s only", browseIDs(items), kept.ID)
	}
	// An exact type match and the * wildcard.
	if items, _, _ = e.store.Browse(nil, "", "", "*", nil, 20, browseAll); len(items) != 5 {
		t.Fatalf("type=* browse = %d items, want every type (the foreign entry included: nil projects = all)", len(items))
	}
	// An unreadable project narrows to nothing: the empty readable set
	// answers an empty page without a candidate query.
	items, next, err := e.store.Browse([]string{}, "", "", "", nil, 20, browseAll)
	if err != nil || len(items) != 0 || next != "" {
		t.Fatalf("empty readable set = %d items cursor %q err %v, want an empty page", len(items), next, err)
	}
}

func containsIDs(items []Entry, want ...string) bool {
	set := map[string]bool{}
	for _, it := range items {
		set[it.ID] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// seedBeta creates project beta and answers its id (foreign-project
// candidates).
func seedBeta(t *testing.T, e *env) string {
	t.Helper()
	p, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}
	return p.ID
}

func alphaID(t *testing.T, e *env) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='alpha'`).Scan(&id); err != nil {
		t.Fatalf("alpha id: %v", err)
	}
	return id
}
