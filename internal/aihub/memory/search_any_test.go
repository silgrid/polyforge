package memory

// search_any_test.go: the ranked any-term mode (match=any) — a long task
// description finds the memories that share its terms (where the
// default AND mode finds none), bm25 ranks the stronger overlap first,
// the type/tag/allowed filters and the limit still hold, FTS5 operator
// characters stay inert, and a query with no usable term answers no
// hits without an error.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// claimQuery is a realistic claim-time query: a work item's goal plus
// the opening of its markdown content.
const claimQuery = `Fix the flaky provider retry ladder in the daemon

## Context
The step agent hits a 429 from the provider and the retry ladder gives up
after two attempts; see https://example.com/issues/171 for the trace.
- [ ] make the backoff exponential
- [ ] log every attempt`

func TestSearchAnyLongQueryFindsRelevantMemory(t *testing.T) {
	e := newEnv(t)
	strong := e.create(t, "Lesson: the provider retry ladder must back off exponentially; a flat ladder gives up on 429.")
	weak := e.create(t, "Unrelated note that merely mentions a daemon restart.")
	e.create(t, "Totally different subject: CSS grid layout quirks.")

	// The default AND mode answers nothing for a whole task description.
	if hits, err := e.store.Search(claimQuery, "", nil, 10, nil); err != nil || len(hits) != 0 {
		t.Fatalf("AND mode on the long query: %v, %d hits (want 0 — the bug's shape)", err, len(hits))
	}
	hits, err := e.store.SearchAny(claimQuery, "", nil, 10, nil)
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("SearchAny hits = %d, want 2 (the retry lesson and the daemon note): %+v", len(hits), hits)
	}
	if hits[0].ID != strong.ID || hits[1].ID != weak.ID {
		t.Fatalf("rank order = [%s %s], want the stronger overlap (%s) first", hits[0].ID, hits[1].ID, strong.ID)
	}
}

func TestSearchAnyFiltersAndLimit(t *testing.T) {
	e := newEnv(t)
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, e.create(t, fmt.Sprintf("retry ladder lesson number %d", i)).ID)
	}
	other, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "fact.note", Visibility: "project",
		Content: "retry ladder fact", Tags: []string{"ops"},
	}, e.user)
	if err != nil {
		t.Fatalf("create fact: %v", err)
	}

	// Limit honoured.
	if hits, err := e.store.SearchAny("retry ladder", "", nil, 2, nil); err != nil || len(hits) != 2 {
		t.Fatalf("limit 2: %v, %d hits", err, len(hits))
	}
	// Type filter.
	hits, err := e.store.SearchAny("retry ladder", "fact.*", nil, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != other.ID {
		t.Fatalf("type filter: %v %+v", err, hits)
	}
	// Tag filter.
	hits, err = e.store.SearchAny("retry ladder", "", []string{"ops"}, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != other.ID {
		t.Fatalf("tag filter: %v %+v", err, hits)
	}
	// The allowed predicate (membership / visibility / project — the
	// server layer's filters) is still applied to every candidate, and
	// paging fills the limit past refused candidates.
	allowed := func(en *Entry) bool { return en.ID == ids[4] }
	hits, err = e.store.SearchAny("retry ladder", "", nil, 1, allowed)
	if err != nil || len(hits) != 1 || hits[0].ID != ids[4] {
		t.Fatalf("allowed filter: %v %+v", err, hits)
	}
	if hits, err = e.store.SearchAny("retry ladder", "", nil, 10, func(*Entry) bool { return false }); err != nil || len(hits) != 0 {
		t.Fatalf("refusing allowed must answer none: %v %+v", err, hits)
	}
}

func TestSearchAnyOperatorCharactersInert(t *testing.T) {
	e := newEnv(t)
	target := e.create(t, "The trigram index finds xyzzyplugh.")
	e.create(t, "Another memory about something else entirely.")
	e.create(t, "A third one, unrelated as well.")

	for _, q := range []string{
		`"xyzzyplugh" OR (NOT) -- body:* ^near NEAR(a b)`,
		`xyzzyplugh*`,
		`{xyzzyplugh} AND`,
	} {
		hits, err := e.store.SearchAny(q, "", nil, 10, nil)
		if err != nil {
			t.Fatalf("SearchAny(%q): %v", q, err)
		}
		if len(hits) != 1 || hits[0].ID != target.ID {
			t.Fatalf("SearchAny(%q) = %d hits, want only the xyzzyplugh memory", q, len(hits))
		}
	}
	// Pure operator noise yields no term at all: no hits, no error, and
	// certainly not every memory.
	for _, q := range []string{`*** "(" ) : ^ -`, `"(*)"`} {
		hits, err := e.store.SearchAny(q, "", nil, 10, nil)
		if err != nil || len(hits) != 0 {
			t.Fatalf("SearchAny(%q): %v, %d hits (want none)", q, err, len(hits))
		}
	}
}

func TestSearchAnyNoUsableTerm(t *testing.T) {
	e := newEnv(t)
	e.create(t, "the and of to a with from this that")
	hits, err := e.store.SearchAny("the and of to a with from this that", "", nil, 10, nil)
	if err != nil || len(hits) != 0 {
		t.Fatalf("stopword-only query: %v, %d hits (want none, no error)", err, len(hits))
	}
	if _, err := e.store.SearchAny("   ", "", nil, 10, nil); err == nil || err.(*APIError).Code != "MEM-400" {
		t.Fatalf("blank query must be MEM-400 like Search: %v", err)
	}
}

func TestAnyTerms(t *testing.T) {
	got := anyTerms("Fix the Retry-Ladder: retry ladder, https://x.io/a_b__c 42 ok 中文 trigram_index")
	// Words come longest first (ties keep first appearance).
	want := []string{"trigram_index", "ladder", "a_b__c", "retry", "fix"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("anyTerms = %v, want %v", got, want)
	}
	// The cap keeps the longest maxAnyTerms terms.
	var words []string
	for i := 0; i < 50; i++ {
		words = append(words, fmt.Sprintf("w%03d", i)) // 4 runes
	}
	words = append(words, "longestterm")
	got = anyTerms(strings.Join(words, " "))
	if len(got) != maxAnyTerms || got[0] != "longestterm" {
		t.Fatalf("cap: %d terms, first %q", len(got), got[0])
	}
}

// CJK clauses have no word boundaries: anyTerms shingles them into the
// trigram index's own 3-rune pieces, so a Chinese task description
// finds a memory sharing a stretch of it (where AND, and a whole-clause
// phrase, find nothing).
func TestSearchAnyChineseQuery(t *testing.T) {
	e := newEnv(t)
	hit := e.create(t, "教训：重试阶梯必须指数退避，否则遇到限流会直接放弃。")
	e.create(t, "前端样式布局说明。")
	goal := "修复守护进程中的重试阶梯问题"
	if hits, err := e.store.Search(goal, "", nil, 10, nil); err != nil || len(hits) != 0 {
		t.Fatalf("AND mode on the clause: %v, %d hits (want 0)", err, len(hits))
	}
	hits, err := e.store.SearchAny(goal, "", nil, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != hit.ID {
		t.Fatalf("SearchAny(%q): %v %+v, want only the 重试阶梯 memory", goal, err, hits)
	}
}

// A mixed Chinese + English query finds both the Chinese and the English
// memory.
func TestSearchAnyMixedScripts(t *testing.T) {
	e := newEnv(t)
	zh := e.create(t, "重试阶梯必须指数退避。")
	en := e.create(t, "The retry ladder must back off exponentially.")
	e.create(t, "前端样式布局说明 and CSS grid quirks.")
	hits, err := e.store.SearchAny("修复守护进程中的重试阶梯问题 (retry ladder in the daemon)", "", nil, 10, nil)
	if err != nil {
		t.Fatalf("SearchAny: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.ID] = true
	}
	if len(hits) != 2 || !got[zh.ID] || !got[en.ID] {
		t.Fatalf("mixed query hits = %+v, want exactly the Chinese and the English memory", hits)
	}
}

// Devanagari vowel signs and viramas are combining marks: they stay in
// the word instead of cutting it into sub-3-rune pieces.
func TestSearchAnyDevanagariWordKept(t *testing.T) {
	terms := anyTerms("हिन्दी परीक्षण")
	if strings.Join(terms, " ") != "परीक्षण हिन्दी" {
		t.Fatalf("anyTerms = %q, want both whole words", terms)
	}
	e := newEnv(t)
	hit := e.create(t, "यह हिन्दी नोट है")
	e.create(t, "an English note")
	hits, err := e.store.SearchAny("हिन्दी", "", nil, 10, nil)
	if err != nil || len(hits) != 1 || hits[0].ID != hit.ID {
		t.Fatalf("SearchAny(हिन्दी): %v %+v", err, hits)
	}
}

func TestAnyTermsCJKShingles(t *testing.T) {
	got := anyTerms("修复重试阶梯 retry 重试阶梯 中文")
	// One clause → its shingles once (the repeated clause adds nothing);
	// a 2-rune run yields nothing; script switches split runs.
	want := []string{"retry", "修复重", "复重试", "重试阶", "试阶梯"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("anyTerms = %v, want %v", got, want)
	}
	if got := anyTerms("修复retry阶梯问题"); strings.Join(got, " ") != "retry 阶梯问 梯问题" {
		t.Fatalf("script switch: %v", got)
	}
	// Neither script starves the other under the cap: a long English
	// text and a long Chinese clause share the 32 slots, and two CJK
	// clauses share the shingle slots.
	var words []string
	for i := 0; i < 40; i++ {
		words = append(words, fmt.Sprintf("word%03d", i))
	}
	var han []rune
	for i := 0; i < 40; i++ {
		han = append(han, rune(0x4E00+i*7)) // 40 distinct Han runes
	}
	long := string(han)
	got = anyTerms(strings.Join(words, " ") + " " + long + "，子丑寅卯")
	var nWords, nShingles int
	hasSecondClause := false
	for _, t := range got {
		if strings.HasPrefix(t, "word") {
			nWords++
		} else {
			nShingles++
		}
		if t == "子丑寅" || t == "丑寅卯" {
			hasSecondClause = true
		}
	}
	if len(got) != maxAnyTerms || nWords != maxAnyTerms/2 || nShingles != maxAnyTerms/2 || !hasSecondClause {
		t.Fatalf("cap split: %d terms, %d words, %d shingles, second clause kept=%v", len(got), nWords, nShingles, hasSecondClause)
	}
	// Operator characters never survive into a CJK shingle either.
	for _, term := range anyTerms(`重试"阶梯*（OR）:NEAR`) {
		if strings.ContainsAny(term, `"*():^-{}`) {
			t.Fatalf("operator character in term %q", term)
		}
	}
}

// Particle-heavy shingles do not take term slots; content shingles that
// touch one particle stay, content characters that also serve as
// function words (中 上 下 对 将 一 等) are not particles, a run the
// filter would empty keeps its shingles, and Latin words are unaffected.
func TestAnyTermsCJKParticles(t *testing.T) {
	join := func(ts []string) string { return strings.Join(ts, " ") }
	got := anyTerms("修复的是在重试时的问题")
	want := []string{"修复的", "在重试", "重试时", "试时的", "时的问", "的问题"}
	if join(got) != join(want) {
		t.Fatalf("anyTerms = %v, want %v (复的是 / 的是在 / 是在重 dropped)", got, want)
	}
	// A multi-rune function word counts as particles: 进行 + 了.
	if got := anyTerms("进行了重试"); join(got) != "了重试" {
		t.Fatalf("function word: %v, want [了重试]", got)
	}
	// Real terms built on 中 上 下 对 将 一 等 survive.
	for q, keys := range map[string][]string{
		"修复守护进程中的重试阶梯问题": {"守护进", "重试阶", "试阶梯"},
		"上下文":         {"上下文"},
		"中文上下文":       {"中文上", "上下文"},
		"下载中":         {"下载中"},
		"等待中的任务":      {"等待中", "的任务"},
		"将来为了支持对象上传":  {"将来为", "支持对", "对象上", "象上传"},
		"一致性检查与对账":    {"一致性", "性检查", "与对账"},
		"修复上下文压缩后的问题": {"修复上", "上下文", "文压缩"},
	} {
		got := " " + join(anyTerms(q)) + " "
		for _, k := range keys {
			if !strings.Contains(got, " "+k+" ") {
				t.Fatalf("anyTerms(%q) = %q lacks %q", q, got, k)
			}
		}
	}
	// A clause made only of particles keeps its shingles rather than
	// yielding nothing.
	if got := anyTerms("就是这个"); join(got) != "就是这 是这个" {
		t.Fatalf("all-particle run: %v, want its unfiltered shingles", got)
	}
	// Latin is untouched by the CJK filter (the particle runes never
	// occur in a non-CJK run).
	if got := anyTerms("retry ladder 的了是"); join(got) != "ladder 的了是 retry" {
		t.Fatalf("latin: %v", got)
	}
}

// The project scope narrows the FTS candidates inside the SQL: many
// better-ranked matches of another project neither crowd the scoped
// answer out of the any-term candidate window nor cost a load each.
func TestSearchModeProjectNarrowsCandidates(t *testing.T) {
	e := newEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("project beta: %v", err)
	}
	for i := 0; i < anyCandidateFloor+20; i++ {
		if _, err := e.store.Create(CreateRequest{
			Project: "beta", Type: "experience.debug", Visibility: "project", Content: "needle",
		}, e.user); err != nil {
			t.Fatalf("create beta: %v", err)
		}
	}
	keep := e.create(t, "needle in alpha, a longer body so it ranks below every short beta entry in the trigram index")
	scoped := func(en *Entry) bool { return en.ProjectID == keep.ProjectID }
	for _, mode := range []string{MatchAll, MatchAny} {
		hits, err := e.store.SearchMode(mode, []string{keep.ProjectID}, "needle", "", nil, 1, scoped)
		if err != nil || len(hits) != 1 || hits[0].ID != keep.ID {
			t.Fatalf("%s scoped search: %v %+v, want the alpha memory", mode, err, hits)
		}
	}
	// Unscoped with the same predicate: the AND mode pages to it; the
	// any-term mode examines only its bounded best-ranked window, which
	// the beta entries fill — the bound holds.
	if hits, err := e.store.SearchMode(MatchAll, nil, "needle", "", nil, 1, scoped); err != nil || len(hits) != 1 {
		t.Fatalf("AND unscoped: %v %+v", err, hits)
	}
	if hits, err := e.store.SearchMode(MatchAny, nil, "needle", "", nil, 1, scoped); err != nil || len(hits) != 0 {
		t.Fatalf("any-term unscoped must stop at its candidate bound: %v %+v", err, hits)
	}
	if _, err := e.store.SearchMode("some", nil, "needle", "", nil, 1, nil); err == nil || err.(*APIError).Code != "MEM-400" {
		t.Fatalf("unknown mode must be MEM-400: %v", err)
	}
}
