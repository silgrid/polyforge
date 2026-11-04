package memory

// revision_order_test.go: a module map's current revision (latest
// updated_at for its title key) ranks before its older revisions in
// both keyword modes and the short-token scan, from row metadata only;
// distinct keys, legacy titles and other types keep today's order;
// browse's default sort already lists the current revision first; an
// older revision stays readable and findable; doc.module-map keeps the
// doc stability family.

import (
	"slices"
	"strings"
	"testing"
)

// rev1Strong is a much better "sprocket" match than revWeak.
const (
	rev1Strong = "baseline @abc1234 2026-10-01\n\nsprocket sprocket sprocket sprocket sprocket sprocket\n"
	revWeak    = "baseline @def5678 2026-10-10\n\nsprocket\n\nalpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango\n"
)

// revEntry creates one entry of typ titled title and ages it.
func revEntry(t *testing.T, e *env, typ, title, body string, tags []string, days int) *Entry {
	t.Helper()
	got, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: typ, Title: &title, Visibility: "project",
		Content: body, Tags: tags,
	}, e.user)
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	age(t, e, got.ID, days)
	return got
}

func entryIDs(items []Entry) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func wantOrder(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: order %v, want %v", what, got, want)
	}
}

var widgetTags = []string{"demo", "widget"}

// The control: the same two bodies and ages under a type that has no
// module key keep bm25 order (rev1 first) — so the module-map case
// below is the rule at work, not the corpus.
func TestRevisionOrderControlKeepsRelevance(t *testing.T) {
	for _, typ := range []string{"doc.track"} {
		e := newEnv(t)
		pinClock(e)
		r1 := revEntry(t, e, typ, "demo/widget rev1", rev1Strong, widgetTags, 5)
		r2 := revEntry(t, e, typ, "demo/widget rev2", revWeak, widgetTags, 1)
		for _, match := range []string{MatchAll, MatchAny} {
			got, err := e.store.SearchMode(match, nil, "sprocket", "", nil, 50, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantOrder(t, typ+" "+match, entryIDs(got), r1.ID, r2.ID)
		}
	}
}

func TestRevisionOrderCurrentFirstBothModes(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	r1 := revEntry(t, e, moduleMapType, "demo/widget rev1", rev1Strong, widgetTags, 5)
	r2 := revEntry(t, e, moduleMapType, "demo/widget rev2", revWeak, widgetTags, 1)
	for _, match := range []string{MatchAll, MatchAny} {
		got, err := e.store.SearchMode(match, nil, "sprocket", "", nil, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder(t, match, entryIDs(got), r2.ID, r1.ID)
		// A query only rev1 answers still returns it.
		got, err = e.store.SearchMode(match, nil, "abc1234", "", nil, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder(t, match+" rev1-only", entryIDs(got), r1.ID)
	}
	// The key is the title's: rev01 and rev2 of demo/widget are one key,
	// and the latest updated_at is current whatever the revision number.
	r01 := revEntry(t, e, moduleMapType, "demo/widget rev01", rev1Strong, widgetTags, 0)
	got, err := e.store.SearchMode(MatchAll, nil, "sprocket", "", nil, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder(t, "rev01 newest", entryIDs(got), r01.ID, r2.ID, r1.ID)
}

// Limit and page cuts are exact prefixes/slices of the full order.
func TestRevisionOrderCutsArePrefixes(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	revEntry(t, e, moduleMapType, "demo/widget rev1", rev1Strong, widgetTags, 6)
	revEntry(t, e, moduleMapType, "demo/widget rev2", revWeak, widgetTags, 4)
	revEntry(t, e, moduleMapType, "demo/widget rev3", revWeak+" sprocket x", widgetTags, 2)
	revEntry(t, e, "fact.note", "other", "sprocket sprocket noise", nil, 3)
	revEntry(t, e, moduleMapType, "demo/gear rev1", "baseline @abc1234 2026-10-01\nsprocket sprocket\n", []string{"demo", "gear"}, 3)
	for _, match := range []string{MatchAll, MatchAny} {
		full, err := e.store.SearchMode(match, nil, "sprocket", "", nil, 50, nil)
		if err != nil {
			t.Fatal(err)
		}
		all := entryIDs(full)
		if len(all) != 5 {
			t.Fatalf("%s: %d hits, want 5", match, len(all))
		}
		for k := 1; k <= len(all); k++ {
			cut, err := e.store.SearchMode(match, nil, "sprocket", "", nil, k, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantOrder(t, match+" limit", entryIDs(cut), all[:k]...)
		}
	}
	var paged []string
	for off := 0; ; off++ {
		page, err := e.store.ftsPage(ftsQuery([]string{"sprocket"}), nil, freshNow, 1, off)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		paged = append(paged, page[0].id)
	}
	full, _ := e.store.SearchMode(MatchAll, nil, "sprocket", "", nil, 50, nil)
	wantOrder(t, "ftsPage pages of 1", paged, entryIDs(full)...)
}

// Distinct keys keep relevance order; a legacy (unparseable) title and
// a module-shaped title on another type get no key and are not demoted.
func TestRevisionOrderOnlyKeyedRowsMove(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	strong := revEntry(t, e, moduleMapType, "demo/gear rev1", rev1Strong, []string{"demo", "gear"}, 5)
	weak := revEntry(t, e, moduleMapType, "demo/widget rev1", revWeak, widgetTags, 1)
	got, err := e.store.SearchMode(MatchAll, nil, "sprocket", "", nil, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder(t, "distinct keys", entryIDs(got), strong.ID, weak.ID)

	e = newEnv(t)
	pinClock(e)
	legacy := revEntry(t, e, moduleMapType, "demo/widget rev1", rev1Strong, widgetTags, 5)
	if _, err := e.db.Exec(`UPDATE memories SET title='widget module map (legacy)' WHERE id=?1`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	cur := revEntry(t, e, moduleMapType, "demo/widget rev2", revWeak, widgetTags, 1)
	got, err = e.store.SearchMode(MatchAny, nil, "sprocket", "", nil, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder(t, "legacy title", entryIDs(got), legacy.ID, cur.ID)
}

// The short-token scan (all mode, no FTS step) follows the same rule:
// its score is freshness alone, so a key's revisions already come
// newest first; the rule keeps them together at the current one's
// place, ahead of an unkeyed entry fresher than the older revision.
func TestRevisionOrderShortTokenScan(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	r1 := revEntry(t, e, moduleMapType, "demo/widget rev1", rev1Strong+" zq", widgetTags, 5)
	other := revEntry(t, e, "doc.track", "ledger", "ledger zq", nil, 3)
	r2 := revEntry(t, e, moduleMapType, "demo/widget rev2", revWeak+" zq", widgetTags, 1)
	got, err := e.store.SearchMode(MatchAll, nil, "zq", "", nil, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder(t, "short-token scan", entryIDs(got), r2.ID, r1.ID, other.ID)
}

// Browse (default updated sort) already lists the current revision
// first across a page cut, and nothing is hidden or deleted.
func TestRevisionOrderBrowseAndVisibility(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	r1 := revEntry(t, e, moduleMapType, "demo/widget rev1", rev1Strong, widgetTags, 5)
	r2 := revEntry(t, e, moduleMapType, "demo/widget rev2", revWeak, widgetTags, 1)
	var walked []string
	after := ""
	for {
		items, next, err := e.store.Browse(nil, "", after, "", nil, 1, browseAll)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, entryIDs(items)...)
		if next == "" {
			break
		}
		after = next
	}
	wantOrder(t, "browse walk", walked, r2.ID, r1.ID)
	if _, _, err := e.store.Browse(nil, "", "not-a-cursor", "", nil, 1, browseAll); err == nil ||
		err.(*APIError).Code != "MEM-400" {
		t.Fatalf("bad cursor: %v, want MEM-400", err)
	}
	if _, created, err := e.store.Browse(nil, "created", "", "", nil, 1, browseAll); err != nil {
		t.Fatal(err)
	} else if _, _, err := e.store.Browse(nil, "", created, "", nil, 1, browseAll); err == nil ||
		err.(*APIError).Code != "MEM-400" {
		t.Fatalf("wrong-sort cursor: %v, want MEM-400", err)
	}
	// rev1 is still readable by id and found by a tag search.
	got, err := e.store.Get(r1.ID)
	if err != nil || got.Status != "active" {
		t.Fatalf("rev1 by id: %v %v", got, err)
	}
	body, err := e.store.Content(r1.ID)
	if err != nil || !strings.Contains(string(body), "sprocket sprocket") {
		t.Fatalf("rev1 content: %v", err)
	}
	hits, err := e.store.SearchMode(MatchAll, nil, "sprocket", "", []string{"widget"}, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder(t, "tag search", entryIDs(hits), r2.ID, r1.ID)
}

func TestModuleMapKeepsDocStability(t *testing.T) {
	if d := stabilityDays(moduleMapType); d != 180 {
		t.Fatalf("stabilityDays(%s) = %v, want the doc family's 180", moduleMapType, d)
	}
	for _, ts := range typeStability {
		if ts.family == moduleMapType {
			t.Fatalf("typeStability carries its own %s row", moduleMapType)
		}
	}
}
