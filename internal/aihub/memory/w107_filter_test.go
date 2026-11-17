package memory

// carries: internal/aihub/memory/store.go, internal/aihub/memory/browse.go, internal/aihub/memory/w107_filter_test.go, docs/design/polyforge-vnext/06-memory-search.md

// w107_filter_test.go — polyforge#107 AC3 (the store's half): the
// writer_kind filter narrows both search modes — the keyword path's
// post-load filter (the same layer as the type filter) and the browse
// path's SQL narrowing (the page and the total share it, so the browse
// total counts only the matching entries) — and a value outside the
// session|step enum is MEM-400 on both paths. An empty filter keeps
// every entry, whatever its writer.

import (
	"testing"
)

// w107SeedWriters seeds one memory per writer class plus one without a
// writer, all sharing one findable token, and answers their ids.
func w107SeedWriters(t *testing.T, e *env) (step, session, none string) {
	t.Helper()
	mk := func(kind, details string) string {
		t.Helper()
		body := `{"project":"alpha","type":"fact.note","visibility":"project",
			"content":"w107filter quokka body ` + kind + `"`
		if kind != "" {
			body += `,"writer_kind":"` + kind + `"`
			if details != "" {
				body += `,"writer_details":` + details
			}
		}
		body += `}`
		served, err := w107Create(t, e, body)
		if err != nil {
			t.Fatalf("seed %s: %v", kind, err)
		}
		return served["id"].(string)
	}
	return mk("step", `{"work_item":"wi_107","step":"build","attempt":1}`),
		mk("session", ""), mk("", "")
}

func TestW107Filter(t *testing.T) {
	e := newEnv(t)
	step, session, none := w107SeedWriters(t, e)

	// Keyword mode: the filter narrows the hits like the type filter
	// does — only the step row answers, the session and writerless rows
	// stay out; an empty filter keeps all three.
	hits, err := e.store.SearchMode(MatchAll, nil, "quokka", "", "step", nil, 50, nil)
	if err != nil {
		t.Fatalf("keyword filter: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != step {
		t.Fatalf("keyword writer_kind=step = %d hits, want exactly the step row", len(hits))
	}
	hits, err = e.store.SearchMode(MatchAll, nil, "quokka", "", "session", nil, 50, nil)
	if err != nil {
		t.Fatalf("keyword filter session: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != session {
		t.Fatalf("keyword writer_kind=session = %d hits, want exactly the session row", len(hits))
	}
	hits, err = e.store.SearchMode(MatchAll, nil, "quokka", "", "", nil, 50, nil)
	if err != nil {
		t.Fatalf("keyword unfiltered: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("keyword without a writer filter = %d hits, want all three", len(hits))
	}
	seen := map[string]bool{}
	for _, h := range hits {
		seen[h.ID] = true
	}
	if !seen[none] {
		t.Fatal("the writerless row is missing from the unfiltered keyword answer")
	}

	// Any-term mode narrows under the same filter (the single entry
	// SearchMode applies it to every mode).
	hits, err = e.store.SearchMode(MatchAny, nil, "quokka w107filter", "", "step", nil, 50, nil)
	if err != nil {
		t.Fatalf("any-mode filter: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != step {
		t.Fatalf("any-mode writer_kind=step = %d hits, want exactly the step row", len(hits))
	}

	// Browse mode: the page and the total share the narrowing, so the
	// total counts only the matching entries (AC3).
	items, _, err := e.store.Browse(nil, "", "", "", "step", nil, 20, nil)
	if err != nil {
		t.Fatalf("browse filter: %v", err)
	}
	if len(items) != 1 || items[0].ID != step {
		t.Fatalf("browse writer_kind=step = %d items, want exactly the step row", len(items))
	}
	total, err := e.store.BrowseTotal(nil, "", "step", nil, nil)
	if err != nil {
		t.Fatalf("browse total: %v", err)
	}
	if total != 1 {
		t.Fatalf("browse writer_kind=step total = %d, want 1 (only the matching entries)", total)
	}
	items, _, err = e.store.Browse(nil, "", "", "", "session", nil, 20, nil)
	if err != nil {
		t.Fatalf("browse filter session: %v", err)
	}
	if len(items) != 1 || items[0].ID != session {
		t.Fatalf("browse writer_kind=session = %d items, want exactly the session row", len(items))
	}
	if items, _, _ := e.store.Browse(nil, "", "", "", "", nil, 20, nil); len(items) != 3 {
		t.Fatalf("browse without a writer filter = %d items, want all three", len(items))
	}
	// The writerless row is reachable only without a filter: the filter
	// values are the two enum kinds, and NULL matches neither.
	if total, _ := e.store.BrowseTotal(nil, "", "session", nil, nil); total != 1 {
		t.Fatalf("browse writer_kind=session total = %d, want 1", total)
	}

	// A value outside the enum is MEM-400 on both paths, never a silent
	// no-filter.
	for _, bad := range []string{"daemon", "Step", "*"} {
		if _, err := e.store.SearchMode(MatchAll, nil, "quokka", "", bad, nil, 50, nil); err == nil ||
			w107ErrCode(t, err) != "MEM-400" {
			t.Fatalf("keyword writer_kind=%s = %v, want MEM-400", bad, err)
		}
		if _, _, err := e.store.Browse(nil, "", "", "", bad, nil, 20, nil); err == nil ||
			w107ErrCode(t, err) != "MEM-400" {
			t.Fatalf("browse writer_kind=%s = %v, want MEM-400", bad, err)
		}
		if _, err := e.store.BrowseTotal(nil, "", bad, nil, nil); err == nil ||
			w107ErrCode(t, err) != "MEM-400" {
			t.Fatalf("browse total writer_kind=%s = %v, want MEM-400", bad, err)
		}
	}
}
