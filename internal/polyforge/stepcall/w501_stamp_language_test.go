package stepcall

// w501_stamp_language_test.go (AC3): a section's header is
// stamped in the dispatch's language — under en no CJK character outside
// the body, under zh-CN none of the English labels.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w501CJK reports the first CJK character of s (Han, or CJK / fullwidth
// punctuation), "" when it has none.
func w501CJK(s string) string {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return string(r)
		}
	}
	return ""
}

// w501LabelFragments answers every literal fragment of lang's labels (the
// text between format verbs) that carries a letter and differs from the
// other language's same label.
func w501LabelFragments(t *testing.T, lang, other string) []string {
	t.Helper()
	verb := regexp.MustCompile(`%[a-z]`)
	a, b := reflect.ValueOf(LabelsOf(lang)), reflect.ValueOf(LabelsOf(other))
	var out []string
	for i := 0; i < a.NumField(); i++ {
		v := a.Field(i).String()
		if v == b.Field(i).String() {
			continue
		}
		for _, f := range verb.Split(v, -1) {
			if strings.IndexFunc(f, unicode.IsLetter) >= 0 {
				out = append(out, f)
			}
		}
	}
	return out
}

func TestW501SectionStampLanguage(t *testing.T) {
	msg := capsule.AskLine{At: "2026-11-11T10:00:00Z", Kind: capsule.AskKindMessage, By: "owner", Text: "go ahead"}
	h := Header{Call: 7, Step: "review", Round: 2, Spawn: 3, StartedAt: "2026-11-11T09:00:00Z", EndedAt: "2026-11-11T09:30:00Z",
		Duration: "30m0s", Model: "pi:test/m1", Status: StatusRewind, To: "build", Reason: "the gate is red",
		Events:        []string{"2026-11-11T09:10:00Z infra_failure/model_error (pi:old): boom"},
		OwnerMessages: []capsule.AskLine{msg}, PerOwner: &msg,
		Repos:    []RepoChange{{Repo: "app", End: "abc"}, {Repo: "lib", Start: "aaa", End: "bbb", Stat: "1 file changed"}},
		Evidence: []string{"artifacts/test-result.json"}}
	const body = "## Body text\n\nwritten by the step.\n"

	if n := len(w501LabelFragments(t, LangEN, LangZhCN)); n < 30 {
		t.Fatalf("the en label table carries %d labels differing from zh-CN's: every stamped label is in the table", n)
	}

	h.Language = LangEN
	en := RenderSection(h, body)
	header, _, ok := strings.Cut(en, body)
	if !ok {
		t.Fatalf("en: the body is not verbatim:\n%s", en)
	}
	if c := w501CJK(header); c != "" {
		t.Fatalf("en: the header carries the CJK character %q:\n%s", c, header)
	}
	for _, w := range []string{"## review · round 2 · dispatch 3 · rewind → build", "### Body (written by the step)", "seq 7"} {
		if !strings.Contains(en, w) {
			t.Fatalf("en: the section lacks %q:\n%s", w, en)
		}
	}

	h.Language = LangZhCN
	zh := RenderSection(h, body)
	for _, f := range w501LabelFragments(t, LangEN, LangZhCN) {
		if strings.Contains(zh, f) {
			t.Fatalf("zh-CN: the section carries the en label %q:\n%s", f, zh)
		}
	}
	for _, w := range []string{"## review · 第 2 轮 · 派发 3 · rewind → build", "### 正文（step 撰写）"} {
		if !strings.Contains(zh, w) {
			t.Fatalf("zh-CN: the section lacks %q:\n%s", w, zh)
		}
	}

	// an unknown or absent language reads en
	h.Language = ""
	if got := RenderSection(h, body); got != en {
		t.Fatalf("an absent language is not en:\n%s", got)
	}
}
