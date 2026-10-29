package memory

// freshness_test.go: freshness-weighted search ordering under a pinned
// clock — the fresher of two equal matches first (both match modes and
// the short-token scan), the non-decaying kinds (rule.*, doc.design,
// doc.decision), a much better old match staying ahead of a weak fresh
// one, the weight's edge cases (unparseable / future updated_at) and
// ftsPage's page matching a full sort of every match.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

var freshNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

// pinClock fixes the search clock.
func pinClock(e *env) { e.store.now = func() time.Time { return freshNow } }

// age sets an entry's updated_at to days before freshNow.
func age(t *testing.T, e *env, id string, days int) {
	t.Helper()
	ts := freshNow.Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := e.db.Exec(`UPDATE memories SET updated_at=?1 WHERE id=?2`, ts, id); err != nil {
		t.Fatal(err)
	}
}

func createTyped(t *testing.T, e *env, typ, content string) *Entry {
	t.Helper()
	got, err := e.store.Create(CreateRequest{Project: "alpha", Type: typ, Visibility: "project", Content: content}, e.user)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func ids(hits []Entry) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestSearchFresherWinsEqualMatch(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	const body = "quokka retry ladder 重试 lesson"
	// The stale one is created last so insertion order cannot explain
	// the answer.
	fresh := e.create(t, body)
	stale := e.create(t, body)
	age(t, e, fresh.ID, 2)
	age(t, e, stale.ID, 400)
	for _, m := range []struct{ mode, q string }{{MatchAll, "quokka"}, {MatchAny, "quokka ladder"}, {MatchAll, "重试"}} {
		hits, err := e.store.SearchMode(m.mode, nil, m.q, "", nil, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 || hits[0].ID != fresh.ID {
			t.Fatalf("%s %q: order %v, want fresh %s first", m.mode, m.q, ids(hits), fresh.ID)
		}
	}
}

func TestSearchRuleNeverDecays(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	const body = "wombat deploy freeze rule"
	exp := createTyped(t, e, "experience.deploy", body)
	rule := createTyped(t, e, "rule.deploy", body)
	doc := createTyped(t, e, "doc.design", body)
	age(t, e, exp.ID, 30)
	age(t, e, rule.ID, 3*365)
	age(t, e, doc.ID, 3*365+1)
	hits, err := e.store.Search("wombat", "", nil, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := ids(hits)
	if len(got) != 3 || got[0] != rule.ID || got[1] != doc.ID || got[2] != exp.ID {
		t.Fatalf("order %v, want [rule %s, doc %s, experience %s]: rule.*/doc.* never decay", got, rule.ID, doc.ID, exp.ID)
	}
}

func TestSearchMuchBetterOldMatchBeatsWeakFresh(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	strong := e.create(t, "numbat numbat numbat numbat numbat")
	weak := e.create(t, "numbat "+strings.Repeat("unrelated filler words about other topics ", 30))
	age(t, e, strong.ID, 5*365)
	age(t, e, weak.ID, 0)
	// Precondition: the old match is far more than freshnessBoost better.
	var rs, rw float64
	q := `SELECT rank FROM memory_fts WHERE memory_fts MATCH 'body:"numbat"' AND mem_id=?1`
	if err := e.db.QueryRow(q, strong.ID).Scan(&rs); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(q, weak.ID).Scan(&rw); err != nil {
		t.Fatal(err)
	}
	if rs/rw <= 1+freshnessBoost {
		t.Fatalf("fixture too weak: bm25 strong %f vs weak %f", rs, rw)
	}
	for _, mode := range []string{MatchAll, MatchAny} {
		hits, err := e.store.SearchMode(mode, nil, "numbat", "", nil, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 || hits[0].ID != strong.ID {
			t.Fatalf("%s: order %v, want the much better old match %s first", mode, ids(hits), strong.ID)
		}
	}
}

func TestSearchFresherWinsNearTie(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	// Filler keeps the term rare: bm25's idf clamps to ~0 for a term in
	// most of the corpus, which would make every match a tie.
	for i := 0; i < 6; i++ {
		e.create(t, "unrelated filler about other topics")
	}
	stale := e.create(t, "quokka ladder note")
	fresh := e.create(t, "quokka ladder note, slightly longer")
	age(t, e, stale.ID, 365)
	age(t, e, fresh.ID, 1)
	// Precondition: on text alone the stale entry ranks (slightly) first.
	var rs, rf float64
	q := `SELECT rank FROM memory_fts WHERE memory_fts MATCH 'body:"quokka"' AND mem_id=?1`
	if err := e.db.QueryRow(q, stale.ID).Scan(&rs); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(q, fresh.ID).Scan(&rf); err != nil {
		t.Fatal(err)
	}
	if !(rs < rf && rs/rf < 1.4) {
		t.Fatalf("fixture is not a near-tie favouring the stale entry: bm25 stale %f fresh %f", rs, rf)
	}
	for _, mode := range []string{MatchAll, MatchAny} {
		hits, err := e.store.SearchMode(mode, nil, "quokka", "", nil, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 || hits[0].ID != fresh.ID {
			t.Fatalf("%s: order %v, want fresh %s first on a near-tie", mode, ids(hits), fresh.ID)
		}
	}
}

func TestSearchDocKindsDecay(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	const body = "dingo snapshot document"
	review := createTyped(t, e, "doc.review", body)
	decision := createTyped(t, e, "doc.decision", body)
	audit := createTyped(t, e, "doc.audit", body)
	design := createTyped(t, e, "doc.design", body)
	age(t, e, review.ID, 20)
	age(t, e, audit.ID, 10)
	age(t, e, decision.ID, 3*365)
	age(t, e, design.ID, 3*365+1)
	hits, err := e.store.Search("dingo", "", nil, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{decision.ID, design.ID, audit.ID, review.ID}
	if got := ids(hits); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v: doc.design/doc.decision never decay, doc.review/doc.audit do", got, want)
	}
}

func TestFreshnessWeight(t *testing.T) {
	ts := func(d float64) string {
		return freshNow.Add(-time.Duration(d * 24 * float64(time.Hour))).Format(time.RFC3339Nano)
	}
	e1 := math.Exp(-1)
	cases := []struct {
		typ, updated string
		want         float64
	}{
		{"experience.debug", ts(0), 1},
		{"experience.debug", ts(90), e1},
		{"fact.note", ts(180), e1},
		{"methodology.x", ts(180), e1},
		{"doc.review", ts(180), e1},
		{"doc.audit", ts(180), e1},
		{"doc", ts(180), e1},
		{"doc.design", ts(5000), 1},
		{"doc.decision", ts(5000), 1},
		{"rule.ops", ts(5000), 1},
		{"rule", ts(5000), 1},
		{"other.kind", ts(90), e1},
		{"docs.x", ts(90), e1}, // not the doc family
		{"experience.debug", "", 0},
		{"experience.debug", "not a time", 0},
		{"experience.debug", "2026-10-01 00:00:00", 0},
		{"experience.debug", ts(-30), 1}, // future: clamped to age 0
		{"rule.ops", "not a time", 1},
	}
	for _, c := range cases {
		got := freshness(c.typ, parseUpdatedAt(c.updated), freshNow)
		if math.Abs(got-c.want) > 1e-9 || got > 1 || got < 0 {
			t.Errorf("freshness(%q, %q) = %v, want %v", c.typ, c.updated, got, c.want)
		}
	}
}

// TestSearchUnparseableAndFutureUpdatedAt: through the search, an
// unparseable updated_at ranks as fully stale and a future one as fresh
// (never above a fresh entry's weight).
func TestSearchUnparseableAndFutureUpdatedAt(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	const body = "bilby clock skew"
	bad := e.create(t, body)
	old := e.create(t, body)
	future := e.create(t, body)
	now := e.create(t, body)
	if _, err := e.db.Exec(`UPDATE memories SET updated_at='garbage' WHERE id=?1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	age(t, e, old.ID, 365)
	age(t, e, future.ID, -400)
	age(t, e, now.ID, 0)
	hits, err := e.store.Search("bilby", "", nil, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	// future and now tie at w = 1 (updated_at DESC puts future first);
	// the year-old entry keeps a little weight; garbage has none.
	want := []string{future.ID, now.ID, old.ID, bad.ID}
	if got := ids(hits); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// refCand is one brute-force reference candidate.
type refCand struct {
	id      string
	score   float64
	updated time.Time
}

// refSort is the documented order, written independently of
// sortScored: score ascending, then updated_at DESC as instants, then id.
func refSort(c []refCand) {
	sort.Slice(c, func(i, j int) bool {
		a, b := c[i], c[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if ua, ub := a.updated.UnixNano(), b.updated.UnixNano(); ua != ub {
			return ua > ub
		}
		return a.id < b.id
	})
}

// refOrder ranks every active match of the given projects (nil = all)
// by brute force: every rank read, every score computed, one full sort.
func refOrder(t *testing.T, db *core.DB, match string, projects []string, now time.Time, keep func(id string) bool) []refCand {
	t.Helper()
	clause, pargs := sqlProjectSet(projects, 2)
	rows, err := db.Query(`SELECT m.id, f.rank, m.type, m.updated_at
		FROM memory_fts f JOIN memories m ON m.id = f.mem_id
		WHERE memory_fts MATCH ?1 AND m.status='active'`+clause, append([]any{match}, pargs...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ref []refCand
	for rows.Next() {
		var c refCand
		var rank float64
		var typ, ua string
		if err := rows.Scan(&c.id, &rank, &typ, &ua); err != nil {
			t.Fatal(err)
		}
		if keep != nil && !keep(c.id) {
			continue
		}
		c.updated = parseUpdatedAt(ua)
		c.score = rank * (1 + freshnessBoost*freshness(typ, c.updated, now))
		ref = append(ref, c)
	}
	refSort(ref)
	return ref
}

// TestFTSPageExactOrder: ftsPage's page equals a full
// sort of every match by the documented score, on every page — with a
// project filter, inactive rows still carrying an FTS row, exact score
// ties and mixed whole-second / fractional timestamps.
func TestFTSPageExactOrder(t *testing.T) {
	s, anyMatch := benchCorpus(t, 400)
	now := time.Now().UTC()
	admin := core.NewAdminManager(s.db)
	beta, err := admin.CreateProject(core.CreateProjectRequest{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	var alpha, owner string
	if err := s.db.QueryRow(`SELECT project_id, owner_user_id FROM memories LIMIT 1`).Scan(&alpha, &owner); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE memories SET project_id=?1 WHERE CAST(substr(id, 10) AS INTEGER) % 3 = 0`,
		`UPDATE memories SET status='redacted' WHERE CAST(substr(id, 10) AS INTEGER) % 11 = 0`,
		// whole-second stamps for a share of the rows
		`UPDATE memories SET updated_at = substr(updated_at, 1, 19) || 'Z' WHERE CAST(substr(id, 10) AS INTEGER) % 2 = 0`,
	} {
		if _, err := s.db.Exec(q, beta.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Exact score ties: identical bodies of a non-decaying type, so
	// score is equal and only updated_at / id order them — including two
	// stamps in the same second, whole vs fractional (string order would
	// put the whole-second one first; as instants it is older).
	tieStamps := []string{"2026-01-01T00:00:05Z", "2026-01-01T00:00:05.123Z", "2026-01-01T00:00:05.123Z", "2025-06-01T00:00:00Z"}
	for i, ts := range tieStamps {
		id := fmt.Sprintf("mem_tie%02d", i)
		if _, err := s.db.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, visibility,
			authority_path, content_sha, content_len, created_at, updated_at, content)
			VALUES (?1, ?2, ?3, 'rule.ops', 'project', '', 'sha', 1, ?4, ?4, 'x')`, id, alpha, owner, ts); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO memory_fts(mem_id, body) VALUES (?1, 'gowork tie body')`, id); err != nil {
			t.Fatal(err)
		}
	}
	rare := ftsQuery([]string{"gowork"})
	for _, match := range []string{rare, anyMatch} {
		for _, projects := range [][]string{nil, {alpha}, {beta.ID}} {
			ref := refOrder(t, s.db, match, projects, now, nil)
			for _, pg := range []struct{ limit, offset int }{{10, 0}, {10, 10}, {25, 30}, {50, 0}, {7, max(0, len(ref)-3)}, {5, len(ref) + 5}} {
				got, err := s.ftsPage(match, projects, now, pg.limit, pg.offset)
				if err != nil {
					t.Fatal(err)
				}
				lo, hi := min(pg.offset, len(ref)), min(pg.offset+pg.limit, len(ref))
				if len(got) != hi-lo {
					t.Fatalf("projects %v page %+v: %d rows, want %d", projects, pg, len(got), hi-lo)
				}
				for i, c := range got {
					if c.id != ref[lo+i].id || c.body == "" {
						t.Fatalf("projects %v page %+v row %d = %s, want %s", projects, pg, i, c.id, ref[lo+i].id)
					}
				}
			}
		}
		// The tie group, as instants: the two fractional stamps (id
		// order between them) ahead of the whole second, then the older.
		got, err := s.ftsPage(match, []string{alpha}, now, 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
		var ties []string
		for _, c := range got {
			if strings.HasPrefix(c.id, "mem_tie") {
				ties = append(ties, c.id)
			}
		}
		if match == rare {
			if want := "mem_tie01,mem_tie02,mem_tie00,mem_tie03"; strings.Join(ties, ",") != want {
				t.Fatalf("tie order %v, want %s", ties, want)
			}
		}
	}
}

// TestSearchModeExactOrder: the full search path (collect, entry loads,
// type / tag / project filters, limit) answers the brute-force order in
// both match modes, over real entries with redacted ones among them.
func TestSearchModeExactOrder(t *testing.T) {
	e := newEnv(t)
	pinClock(e)
	beta, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	var alpha string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='alpha'`).Scan(&alpha); err != nil {
		t.Fatal(err)
	}
	types := []string{"experience.debug", "fact.note", "rule.ops", "doc.review", "doc.design", "methodology.review"}
	words := []string{"wallaby", "lantern", "harbour", "cobalt"}
	tagged := map[string]bool{}
	typeOf := map[string]string{}
	for i := 0; i < 48; i++ {
		proj := "alpha"
		if i%4 == 0 {
			proj = "beta"
		}
		var tags []string
		if i%3 == 0 {
			tags = []string{"ops"}
		}
		body := strings.Repeat(words[i%len(words)]+" ", 1+i%5) + "wallaby " + strings.Repeat("filler ", i%7)
		got, err := e.store.Create(CreateRequest{Project: proj, Type: types[i%len(types)], Visibility: "project",
			Content: body, Tags: tags}, e.user)
		if err != nil {
			t.Fatal(err)
		}
		typeOf[got.ID] = got.Type
		tagged[got.ID] = len(tags) > 0
		ts := freshNow.Add(-time.Duration(i*17) * 24 * time.Hour)
		stamp := ts.Format(time.RFC3339Nano)
		if i%2 == 0 {
			stamp = ts.Add(123 * time.Millisecond).Format(time.RFC3339Nano)
		}
		if _, err := e.db.Exec(`UPDATE memories SET updated_at=?1 WHERE id=?2`, stamp, got.ID); err != nil {
			t.Fatal(err)
		}
		if i%9 == 0 {
			if err := e.store.Redact(got.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		mode, q, typ string
		projects     []string
		tags         []string
		limit        int
	}{
		{MatchAll, "wallaby", "", nil, nil, 50},
		{MatchAll, "wallaby", "", []string{alpha}, nil, 7},
		{MatchAll, "wallaby", "", []string{beta.ID}, nil, 50},
		{MatchAll, "wallaby", "", []string{alpha, beta.ID}, nil, 50},
		{MatchAll, "wallaby", "doc.*", nil, nil, 50},
		{MatchAll, "wallaby", "", nil, []string{"ops"}, 5},
		{MatchAny, "wallaby lantern harbour", "", nil, nil, 10},
		{MatchAny, "wallaby lantern harbour", "experience.*", []string{alpha}, nil, 50},
		{MatchAny, "cobalt lantern", "", nil, []string{"ops"}, 50},
	}
	for _, c := range cases {
		match := ftsQuery([]string{c.q})
		if c.mode == MatchAny {
			match = ftsJoin(anyTerms(c.q), " OR ")
		}
		ref := refOrder(t, e.db, match, c.projects, freshNow, func(id string) bool {
			return typeMatches(typeOf[id], c.typ) && (len(c.tags) == 0 || tagged[id])
		})
		want := make([]string, 0, c.limit)
		for _, r := range ref[:min(c.limit, len(ref))] {
			want = append(want, r.id)
		}
		hits, err := e.store.SearchMode(c.mode, c.projects, c.q, c.typ, c.tags, c.limit, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(hits); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%+v:\n got  %v\n want %v", c, got, want)
		}
		if len(want) == 0 {
			t.Fatalf("%+v: empty reference — the case tests nothing", c)
		}
	}
}
