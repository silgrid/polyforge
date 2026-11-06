package stepcall

// stepcall_test.go: the ledger's append/read (a seq per
// line, a torn line skipped, the next line intact), the body's shape
// check and the key references of the pending writes.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func body(extra ...string) string {
	var b strings.Builder
	for _, s := range append(append([]string{}, BodySectionsOf(LangZhCN)...), extra...) {
		b.WriteString("## " + s + "\n\ntext\n\n")
	}
	return b.String()
}

func TestLedgerAppendReadSkipsATornLine(t *testing.T) {
	dir := t.TempDir()
	first, err := Append(dir, Line{Kind: KindCall, Step: "build", Spawn: 1, Verb: VerbHandoff})
	if err != nil || first.Seq != 1 || first.At == "" {
		t.Fatalf("first append = %+v, %v", first, err)
	}
	f, err := os.OpenFile(Path(dir), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":2,"kind":"res`) // a crash mid-append
	f.Close()
	second, err := Append(dir, Line{Kind: KindResult, Step: "build", Spawn: 1, Verb: VerbHandoff, Call: 1, Outcome: OutcomeOK, Handoff: &Handoff{Status: StatusDone}})
	if err != nil || second.Seq != 2 {
		t.Fatalf("the append after a torn line = %+v, %v", second, err)
	}
	lines, err := Read(dir)
	if err != nil || len(lines) != 2 || lines[1].Handoff == nil {
		t.Fatalf("read = %+v, %v", lines, err)
	}
	if h := HandoffOf(lines, "build", 1); h == nil || h.Handoff.Status != StatusDone {
		t.Fatalf("HandoffOf = %+v", h)
	}
	if HandoffOf(lines, "build", 2) != nil || len(StepHandoffs(lines, "review")) != 0 {
		t.Fatal("another dispatch's hand-off was answered")
	}
}

func TestCheckBody(t *testing.T) {
	if err := CheckBody(body(), false, LangZhCN); err != nil {
		t.Fatalf("the template = %v", err)
	}
	if err := CheckBody(strings.Replace(body(), "## 结论", "### 结论：通过", 1), false, LangZhCN); err != nil {
		t.Fatalf("a heading carrying more after the name = %v", err)
	}
	for name, c := range map[string]struct {
		body    string
		reentry bool
		want    string
	}{
		"a section missing":       {strings.Replace(body(), "## 验证\n", "验证\n", 1), false, "验证"},
		"a name only prefixed":    {strings.Replace(body(), "## 验证\n", "## 验证码\n", 1), false, "验证"},
		"a re-entry without it":   {body(), true, ReentrySectionOf(LangZhCN)},
		"over the bound":          {body() + strings.Repeat("字", MaxBodyChars), false, "over the 3000 bound"},
		"empty":                   {"  \n", false, "empty"},
		"not UTF-8":               {body() + "\xff", false, "UTF-8"},
		"a section only in prose": {strings.Replace(body(), "## 做了什么", "做了什么", 1), false, "做了什么"},
	} {
		if err := CheckBody(c.body, c.reentry, LangZhCN); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v, want %q", name, err, c.want)
		}
	}
	if err := CheckBody(body(ReentrySectionOf(LangZhCN)), true, LangZhCN); err != nil {
		t.Fatalf("a re-entry with it = %v", err)
	}
	// the bound counts characters, not bytes: 3000 CJK characters pass
	exact := body()
	exact += strings.Repeat("字", MaxBodyChars-len([]rune(exact)))
	if err := CheckBody(exact, false, LangZhCN); err != nil {
		t.Fatalf("exactly %d characters = %v", MaxBodyChars, err)
	}
}

func TestRenderSectionQuotesTheOwnerVerbatim(t *testing.T) {
	msg := capsule.AskLine{At: "2026-11-11T10:00:00Z", Kind: capsule.AskKindMessage, By: "owner", Text: "line one\n~~~ a fence\n| a pipe"}
	s := RenderSection(Header{Call: 3, Step: "review", Round: 2, Spawn: 4, Status: StatusRewind, To: "build", PerOwner: &msg, Language: LangZhCN,
		OwnerMessages: []capsule.AskLine{msg}, Repos: []RepoChange{{Repo: "app", End: "abc", Stat: "起点未知"}}}, body())
	for _, w := range []string{"## review · 第 2 轮 · 派发 4 · rewind → build", "~~~~\nline one\n~~~ a fence\n| a pipe\n~~~~", "`app`: `（起点未知）` → `abc`", "seq 3"} {
		if !strings.Contains(s, w) {
			t.Fatalf("the section lacks %q:\n%s", w, s)
		}
	}
}

func TestOpRefs(t *testing.T) {
	op := Op{Verb: HubAttrs, Target: "key:A", Name: "integration", Value: json.RawMessage(`{"state":"landed","fix_tickets":["key:B","wi_x","key:A"]}`)}
	if got := strings.Join(KeyRefs(op), ","); got != "A,B" {
		t.Fatalf("KeyRefs = %s", got)
	}
	r, err := ResolveRefs(op, map[string]string{"A": "p#1", "B": "p#2"})
	if err != nil || r.Target != "p#1" || string(r.Value) != `{"fix_tickets":["p#2","wi_x","p#1"],"state":"landed"}` {
		t.Fatalf("ResolveRefs = %+v (%s), %v", r, r.Value, err)
	}
	if !strings.Contains(string(op.Value), "key:B") {
		t.Fatal("ResolveRefs changed the op it was given")
	}
	if _, err := ResolveRefs(op, map[string]string{"A": "p#1"}); err == nil || !strings.Contains(err.Error(), "B") {
		t.Fatalf("an unresolved reference = %v", err)
	}
	// a wrap attr's value is the wrap's, never resolved
	wrap := Op{Verb: HubWrapAttrs, Name: "landing", Value: json.RawMessage(`{"t":"key:A"}`)}
	if len(KeyRefs(wrap)) != 0 {
		t.Fatalf("a wrap attr's value carries no reference: %v", KeyRefs(wrap))
	}
	for _, good := range []Op{
		{Verb: HubAttrs, Target: "x", Name: "integration", Value: []byte(`null`)},
		{Verb: HubAttrs, Target: "x", Name: "integration", Value: []byte(`{}`), If: map[string]any{"integration.state": "landed", "a_1.0": nil}},
	} {
		if err := CheckOp(good); err != nil {
			t.Fatalf("CheckOp refused %+v: %v", good, err)
		}
	}
	for _, bad := range []Op{
		{Verb: HubFile, Lane: "l", Goal: "g", Content: "c"},
		{Verb: HubAttrs, Name: "integration", Value: []byte(`{}`)},                                                     // no target
		{Verb: HubAttrs, Target: "x", Value: []byte(`{}`)},                                                             // no key
		{Verb: HubAttrs, Target: "x", Name: "Integration", Value: []byte(`{}`)},                                        // a malformed key
		{Verb: HubAttrs, Target: "x", Name: "integration"},                                                             // no value
		{Verb: HubAttrs, Target: "x", Name: "integration", Value: []byte(`{`)},                                         // not JSON
		{Verb: HubAttrs, Target: "x", Name: "integration", Value: []byte(`{}`), If: map[string]any{"a..b": "x"}},       // a malformed path
		{Verb: HubAttrs, Target: "x", Name: "integration", Value: []byte(`{}`), If: map[string]any{"State": "landed"}}, // upper case
		{Verb: HubWrapAttrs, Name: "Bad Name", Value: []byte(`{}`)},
		{Verb: "move", Target: "x"},
		{Verb: "clear-hold", Target: "x"},
		{Verb: HubGet},
	} {
		if CheckOp(bad) == nil {
			t.Fatalf("CheckOp accepted %+v", bad)
		}
	}
}

func stringsOf(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
