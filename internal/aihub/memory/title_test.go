package memory

// title_test.go: the derived row title — the derivation itself and
// create filling a blank title while keeping an explicit one.

import (
	"strings"
	"testing"
)

func TestDeriveTitle(t *testing.T) {
	long := strings.Repeat("abcdefghij", 10) // 100 runes
	cjkLong := strings.Repeat("记忆标题派生", 20)  // 120 runes
	cases := []struct {
		name, body string
		want       *string
	}{
		{"heading", "\n\n# Retry ladder lesson\nbody", sp("Retry ladder lesson")},
		{"deep heading", "### Deep heading ###", sp("Deep heading ###")},
		{"list", "- first item\n- second", sp("first item")},
		{"star list", "*   starred", sp("starred")},
		{"quote list", "> - quoted item", sp("quoted item")},
		{"numbered", "12. numbered step", sp("numbered step")},
		{"numbered paren", "3) paren step", sp("paren step")},
		{"task", "- [ ] make the backoff exponential", sp("make the backoff exponential")},
		{"hashtag kept", "#hashtag is text", sp("#hashtag is text")},
		{"emphasis kept", "**Bold** start", sp("**Bold** start")},
		{"fence skipped", "```go\nfunc main() {}\n```", sp("func main() {}")},
		{"rule skipped", "---\n***\nafter rules", sp("after rules")},
		{"empty markers skipped", "#\n- \n>\nreal line", sp("real line")},
		{"cjk", "## 重试阶梯必须指数退避", sp("重试阶梯必须指数退避")},
		{"long", long, sp(long[:79] + "…")},
		{"cjk long", cjkLong, sp(string([]rune(cjkLong)[:79]) + "…")},
		{"exactly max", long[:80], sp(long[:80])},
		{"empty", "", nil},
		{"blank", " \n\t\n", nil},
		{"markers only", "#\n- \n---", nil},
		{"bare numbered marker", "1.\n2)\n- [ ]\nthe real step", sp("the real step")},
		{"decimal kept", "3.14 is pi", sp("3.14 is pi")},
	}
	for _, c := range cases {
		got := deriveTitle(c.body)
		if !strPtrEq(got, c.want) {
			t.Errorf("%s: deriveTitle = %v, want %v", c.name, deref(got), deref(c.want))
		}
		if got != nil && len([]rune(*got)) > titleMaxRunes {
			t.Errorf("%s: %d runes > %d", c.name, len([]rune(*got)), titleMaxRunes)
		}
	}
}

func TestCreateDerivesBlankTitle(t *testing.T) {
	e := newEnv(t)
	for _, title := range []*string{nil, sp(""), sp("  \t")} {
		got, err := e.store.Create(CreateRequest{
			Project: "alpha", Type: "experience.debug", Visibility: "project",
			Title: title, Content: "# Provider retry ladder\nback off exponentially",
		}, e.user)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title == nil || *got.Title != "Provider retry ladder" {
			t.Fatalf("title %q: row title = %v, want derived", deref(title), deref(got.Title))
		}
	}
	got, err := e.store.Create(CreateRequest{
		Project: "alpha", Type: "experience.debug", Visibility: "project",
		Title: sp(" Explicit "), Content: "# Heading",
	}, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title == nil || *got.Title != " Explicit " {
		t.Fatalf("explicit title = %v, want kept verbatim", deref(got.Title))
	}
}

func sp(s string) *string { return &s }

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
