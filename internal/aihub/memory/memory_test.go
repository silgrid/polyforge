package memory

// Memory tests: FTS round trip (create → search, redact removal, short
// CJK token widening), idempotent create and search paging.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

type env struct {
	db    *core.DB
	store *Store
	admin *core.AdminManager
	user  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := core.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	admin := core.NewAdminManager(db)
	u, err := admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := admin.CreateProject(core.CreateProjectRequest{Name: "alpha"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	return &env{db: db, store: NewStore(db, t.TempDir()), admin: admin, user: u.ID}
}

func (e *env) create(t *testing.T, content string) *Entry {
	t.Helper()
	entry, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "experience.debug",
		Visibility: "project", Content: content,
	}, e.user)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return entry
}

func TestFtsRoundTrip(t *testing.T) {
	e := newEnv(t)
	entry := e.create(t, "The trigram index finds arbitrary substrings like xyzzyplugh.")

	// ≥3-rune token via FTS MATCH.
	hits, err := e.store.Search("xyzzyplugh", "", nil, 10, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != entry.ID {
		t.Fatalf("round trip miss: %+v", hits)
	}
	// Arbitrary substring (mid-word) works too.
	hits, err = e.store.Search("zzyplu", "", nil, 10, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("substring miss: %v %+v", err, hits)
	}
	// A 2-rune token exercises the short-query widening.
	hits, err = e.store.Search("中", "", nil, 10, nil)
	if err != nil || len(hits) != 0 {
		// no CJK content yet: expect zero, not an error
		t.Fatalf("short token on empty: %v %+v", err, hits)
	}
	e.create(t, "中文检索必须覆盖两字词。")
	hits, err = e.store.Search("中文", "", nil, 10, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("CJK 2-rune widening miss: %v %+v", err, hits)
	}
	// Type filter.
	hits, err = e.store.Search("trigram", "experience.debug", nil, 10, nil)
	if err != nil || len(hits) != 1 {
		t.Fatalf("type filter: %v %+v", err, hits)
	}
	hits, err = e.store.Search("trigram", "fact.other", nil, 10, nil)
	if err != nil || len(hits) != 0 {
		t.Fatalf("type filter should exclude: %v", hits)
	}

	// The stored document matches what was written.
	content, err := e.store.Content(entry.ID)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if !strings.Contains(string(content), "xyzzyplugh") {
		t.Fatalf("stored document stale: %s", content)
	}
	if !strings.HasPrefix(string(content), "---\n") {
		t.Fatalf("stored document missing frontmatter: %q", string(content)[:40])
	}

	// Redact: gone from search and from Get.
	if err := e.store.Redact(entry.ID); err != nil {
		t.Fatalf("redact: %v", err)
	}
	if _, err := e.store.Get(entry.ID); err == nil || err.(*APIError).Code != "MEM-404" {
		t.Fatalf("redacted entry still served: %v", err)
	}
	if hits, _ = e.store.Search("xyzzyplugh", "", nil, 10, nil); len(hits) != 0 {
		t.Fatalf("redacted entry still searchable")
	}
}

// TestSearchTypePrefixFilter: the type filter's wildcard form — a
// trailing ".*" is a prefix match on "X." (experience.* matches
// experience.debug and experience.approach, not fact.note and not the
// bare type experience); a non-wildcard value keeps exact-match
// semantics (fact.* never matches experience.* rows).
func TestSearchTypePrefixFilter(t *testing.T) {
	e := newEnv(t)
	mk := func(memType string) {
		t.Helper()
		if _, err := e.store.Create(CreateRequest{
			Project: "alpha", Type: memType,
			Visibility: "project", Content: "the shared xylophone token",
		}, e.user); err != nil {
			t.Fatalf("create %s: %v", memType, err)
		}
	}
	mk("experience.debug")
	mk("experience.approach")
	mk("fact.note")
	mk("experience")

	types := func(hits []Entry) []string {
		out := make([]string, 0, len(hits))
		for _, h := range hits {
			out = append(out, h.Type)
		}
		return out
	}

	// experience.*: both experience.* rows, never fact.note and never
	// the bare type experience.
	hits, err := e.store.Search("xylophone", "experience.*", nil, 10, nil)
	if err != nil {
		t.Fatalf("search experience.*: %v", err)
	}
	got := types(hits)
	if len(got) != 2 {
		t.Fatalf("experience.* hits = %v, want the two experience.* types", got)
	}
	for _, ty := range got {
		if !strings.HasPrefix(ty, "experience.") {
			t.Fatalf("experience.* matched non-experience type %q", ty)
		}
	}

	// fact.*: only fact.note — an experience.* row never matches.
	hits, err = e.store.Search("xylophone", "fact.*", nil, 10, nil)
	if err != nil {
		t.Fatalf("search fact.*: %v", err)
	}
	if got = types(hits); len(got) != 1 || got[0] != "fact.note" {
		t.Fatalf("fact.* hits = %v, want only fact.note", got)
	}

	// Non-wildcard values keep exact-match semantics.
	hits, err = e.store.Search("xylophone", "experience.debug", nil, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].Type != "experience.debug" {
		t.Fatalf("exact hits = %+v err=%v, want only experience.debug", hits, err)
	}
	hits, err = e.store.Search("xylophone", "experience", nil, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].Type != "experience" {
		t.Fatalf("bare-type hits = %+v err=%v, want only the bare experience row", hits, err)
	}
}

func TestIdempotentCreate(t *testing.T) {
	e := newEnv(t)
	key := "wrap-summary-1"
	e1, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "experience.debug", Visibility: "project",
		Content: "first write", IdempotencyKey: &key,
	}, e.user)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Same key + same owner + same project returns the original entry.
	again, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "experience.debug", Visibility: "project",
		Content: "different content entirely", IdempotencyKey: &key,
	}, e.user)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if again.ID != e1.ID {
		t.Fatalf("idempotent replay created a new entry: %s vs %s", again.ID, e1.ID)
	}
	// Same key, different owner: rejected.
	other := "other-owner"
	if _, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "experience.debug", Visibility: "project",
		Content: "x", IdempotencyKey: &key,
	}, other); err == nil || err.(*APIError).Reason != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("cross-owner key reuse not rejected: %v", err)
	}
}

// TestSearchOperatorTokensQuoted: tokens carrying FTS5 operator characters
// are matched as literal phrases — they neither error nor widen to every
// memory.
func TestSearchOperatorTokensQuoted(t *testing.T) {
	e := newEnv(t)
	e.create(t, "Run the pre-commit hook before pushing.")
	e.create(t, "Unrelated note about trigram indexes.")
	e.create(t, `He said "quoted (paren) text" and a:b key.`)

	cases := []struct {
		q    string
		want int
	}{
		{"pre-commit", 1},
		{"a:b", 1},
		{"(paren)", 1},
		{`"quoted`, 1},
		{"unknown-token", 0},
		{`"(*)"`, 0},
		{"***", 0},
	}
	for _, c := range cases {
		hits, err := e.store.Search(c.q, "", nil, 10, nil)
		if err != nil {
			t.Fatalf("Search(%q): %v", c.q, err)
		}
		if len(hits) != c.want {
			t.Fatalf("Search(%q) = %d hits, want %d", c.q, len(hits), c.want)
		}
	}
}

// TestSearchFillsLimitPastFilteredCandidates: the post-MATCH filters
// (here the allowed predicate) must not shrink the answer below limit
// while matching entries remain — candidates are paged, not cut at one
// fixed window.
func TestSearchFillsLimitPastFilteredCandidates(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 12; i++ {
		e.create(t, "needle filler")
	}
	keep := e.create(t, "needle keep, a longer body so it ranks below every filler entry in the trigram index")
	allowed := func(en *Entry) bool { return en.ID == keep.ID }
	hits, err := e.store.Search("needle", "", nil, 1, allowed)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != keep.ID {
		t.Fatalf("hits = %d, want the one allowed entry past the first candidate window", len(hits))
	}
}
