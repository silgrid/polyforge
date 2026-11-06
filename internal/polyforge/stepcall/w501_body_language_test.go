package stepcall

// w501_body_language_test.go (AC4): `pf step handoff`'s
// body check takes the dispatch's language — its seven headings, and on
// a re-run its re-entry heading — and the shared body template and step
// header give both languages.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w501Headings are the body headings per language, as the template gives
// them: the seven in order, then the re-entry heading.
var w501Headings = map[string]struct {
	seven   []string
	reentry string
}{
	LangEN:   {[]string{"Conclusion", "The question this step answers", "What was done", "Decisions and why", "Verification", "Problems and risks found", "For downstream, and what is open"}, "Reply to the previous round"},
	LangZhCN: {[]string{"结论", "这一步要回答什么", "做了什么", "关键决定和理由", "验证", "发现的问题和风险", "给下游与未决"}, "回应上一轮"},
}

func w501Body(headings ...string) string {
	var b strings.Builder
	for _, h := range headings {
		b.WriteString("### " + h + "\n\ntext\n\n")
	}
	return b.String()
}

func TestW501CheckBodyPerLanguage(t *testing.T) {
	for lang, want := range w501Headings {
		other := LangZhCN
		if lang == LangZhCN {
			other = LangEN
		}
		if got := BodySectionsOf(lang); strings.Join(got, "|") != strings.Join(want.seven, "|") {
			t.Fatalf("%s: BodySectionsOf = %q, want %q", lang, got, want.seven)
		}
		if got := ReentrySectionOf(lang); got != want.reentry {
			t.Fatalf("%s: ReentrySectionOf = %q, want %q", lang, got, want.reentry)
		}
		if err := CheckBody(w501Body(want.seven...), false, lang); err != nil {
			t.Fatalf("%s: its seven headings = %v", lang, err)
		}
		for i, h := range want.seven {
			rest := append(append([]string{}, want.seven[:i]...), want.seven[i+1:]...)
			err := CheckBody(w501Body(rest...), false, lang)
			if err == nil || !strings.Contains(err.Error(), h) {
				t.Fatalf("%s: a body without %q = %v, want a refusal naming it", lang, h, err)
			}
		}
		theirs := w501Headings[other]
		err := CheckBody(w501Body(theirs.seven...), false, lang)
		if err == nil || !strings.Contains(err.Error(), want.seven[0]) {
			t.Fatalf("%s: the %s body = %v, want a refusal naming %q", lang, other, err, want.seven[0])
		}
		err = CheckBody(w501Body(append([]string{theirs.reentry}, want.seven...)...), true, lang)
		if err == nil || !strings.Contains(err.Error(), want.reentry) {
			t.Fatalf("%s: a re-entry under the %s heading = %v, want a refusal naming %q", lang, other, err, want.reentry)
		}
		if err := CheckBody(w501Body(append([]string{want.reentry}, want.seven...)...), true, lang); err != nil {
			t.Fatalf("%s: a re-entry under its own heading = %v", lang, err)
		}
	}
	// an absent or unknown language is the default's
	if err := CheckBody(w501Body(w501Headings[DefaultLanguage].seven...), false, ""); err != nil {
		t.Fatalf("no language = %v, want the %s headings accepted", err, DefaultLanguage)
	}
}

func TestW501TemplateCarriesBothLanguages(t *testing.T) {
	shared := filepath.Join("..", "..", "..", "workflows", "_shared")
	raw, err := os.ReadFile(filepath.Join(shared, "handoff.md"))
	if err != nil {
		t.Fatal(err)
	}
	headings := map[string]bool{}
	for _, h := range headingNames(string(raw)) {
		headings[h] = true
	}
	for lang, want := range w501Headings {
		for _, h := range append([]string{want.reentry}, want.seven...) {
			if !headings[h] {
				t.Fatalf("_shared/handoff.md: no heading line %q (%s)", h, lang)
			}
		}
	}
	raw, err = os.ReadFile(filepath.Join(shared, "step-header.md"))
	if err != nil {
		t.Fatal(err)
	}
	header := string(raw)
	for _, lang := range []string{LangEN, LangZhCN} {
		l := LabelsOf(lang)
		body, _, _ := strings.Cut(l.Body, " ")
		body, _, _ = strings.Cut(body, "（")
		for _, marker := range []string{body, l.Rewind, w501Headings[lang].reentry} {
			if !strings.Contains(header, marker) {
				t.Fatalf("_shared/step-header.md: does not name the %s stamp marker %q", lang, marker)
			}
		}
	}
}
