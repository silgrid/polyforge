package daemon

// w501_handoff_language_test.go (AC3/AC5): `pf step
// handoff` stamps a section's header in the language its dispatch's
// spawn-journal line records, not the live value — a running step is
// never stamped in a language it was not told.

import (
	"net/http"
	"strings"
	"testing"
	"unicode"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w501HeadingsEN are the English body headings (U5): with
// the Chinese ones, a body passes the shape check under either language.
var w501HeadingsEN = []string{"Conclusion", "The question this step answers", "What was done", "Decisions and why", "Verification", "Problems and risks found", "For downstream, and what is open"}

func TestW501HandoffStampUsesTheDispatchLanguage(t *testing.T) {
	f := newC4Fixture(t, "wi_w501")
	body := c4Body(w501HeadingsEN...)
	for _, c := range []struct {
		step, recorded, live string
		want                 string
	}{
		{"build", stepcall.LangEN, stepcall.LangZhCN, "## build · round 1 · dispatch 1 · done"},
		{"sync", stepcall.LangZhCN, stepcall.LangEN, "## sync · 第 1 轮 · 派发 1 · done"},
	} {
		f.td.live.SetAgentLanguage(c.live)
		step := c.step
		f.td.stepOriginOf = func(*http.Request) (stepJob, string) {
			return stepJob{wi: f.wi, step: step, attempt: 1}, ""
		}
		f.journalLanguage(c.step, 1, c.recorded)
		before := f.handoffDoc(t)
		if code, out := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": body}); code != 200 {
			t.Fatalf("%s: %d %s", c.step, code, out)
		}
		section := strings.TrimPrefix(f.handoffDoc(t), before)
		// the section carries the body verbatim, its trailing blank lines trimmed
		header, _, ok := strings.Cut(section, strings.TrimRight(body, "\n"))
		if !ok {
			t.Fatalf("%s: the body is not verbatim:\n%s", c.step, section)
		}
		if !strings.Contains(header, c.want) {
			t.Fatalf("%s (recorded %s, live %s): the header is not in the recorded language, want %q:\n%s", c.step, c.recorded, c.live, c.want, header)
		}
		hasHan := strings.IndexFunc(header, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
		if c.recorded == stepcall.LangEN && hasHan {
			t.Fatalf("%s: an en dispatch's header carries Chinese:\n%s", c.step, header)
		}
		if c.recorded == stepcall.LangZhCN && (strings.Contains(header, "Task repo commits") || strings.Contains(header, "| Step |")) {
			t.Fatalf("%s: a zh-CN dispatch's header carries English labels:\n%s", c.step, header)
		}
	}
}

// TestW501HandoffCheckUsesTheDispatchLanguage (AC4/AC5):
// the body check takes the headings of the language the dispatch was
// told (its spawn-journal line), not the live value.
func TestW501HandoffCheckUsesTheDispatchLanguage(t *testing.T) {
	f := newC4Fixture(t, "wi_w501_check")
	bodyOf := func(lang string) string {
		var b strings.Builder
		for _, s := range stepcall.BodySectionsOf(lang) {
			b.WriteString("### " + s + "\n\ntext\n\n")
		}
		return b.String()
	}
	for _, c := range []struct {
		step, recorded, live string
		missing              []string
	}{
		{"build", stepcall.LangEN, stepcall.LangZhCN, w501HeadingsEN},
		{"sync", stepcall.LangZhCN, stepcall.LangEN, []string{"结论", "这一步要回答什么", "做了什么", "关键决定和理由", "验证", "发现的问题和风险", "给下游与未决"}},
	} {
		f.td.live.SetAgentLanguage(c.live)
		step := c.step
		f.td.stepOriginOf = func(*http.Request) (stepJob, string) {
			return stepJob{wi: f.wi, step: step, attempt: 1}, ""
		}
		f.journalLanguage(c.step, 1, c.recorded)
		before := f.handoffDoc(t)
		code, out := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": bodyOf(c.live)})
		if code != 400 {
			t.Fatalf("%s (recorded %s, live %s): a %s-only body = %d %s, want refused", c.step, c.recorded, c.live, c.live, code, out)
		}
		for _, h := range c.missing {
			if !strings.Contains(out, h) {
				t.Fatalf("%s: the refusal does not name the %s heading %q: %s", c.step, c.recorded, h, out)
			}
		}
		if f.handoffDoc(t) != before {
			t.Fatalf("%s: the refused hand-off changed handoff.md", c.step)
		}
		body := ""
		for _, h := range c.missing {
			body += "### " + h + "\n\ntext\n\n"
		}
		if code, out := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": body}); code != 200 {
			t.Fatalf("%s (recorded %s, live %s): a %s body = %d %s, want accepted", c.step, c.recorded, c.live, c.recorded, code, out)
		}
	}
}
