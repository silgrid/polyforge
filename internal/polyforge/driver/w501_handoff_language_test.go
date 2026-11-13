package driver

// w501_handoff_language_test.go (AC3/AC5): the daemon's
// opening and event sections of artifacts/handoff.md are stamped in the
// live agent language at the moment they are written — under en no CJK
// character in the daemon's text (the work item's data is ASCII), under
// zh-CN none of the English labels.

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w501EnFragments answers every literal fragment of the en labels (the
// text between format verbs) that carries a letter and differs from the
// zh-CN label.
func w501EnFragments() []string {
	verb := regexp.MustCompile(`%[a-z]`)
	en, zh := reflect.ValueOf(stepcall.LabelsOf(stepcall.LangEN)), reflect.ValueOf(stepcall.LabelsOf(stepcall.LangZhCN))
	var out []string
	for i := 0; i < en.NumField(); i++ {
		if en.Field(i).String() == zh.Field(i).String() {
			continue
		}
		for _, f := range verb.Split(en.Field(i).String(), -1) {
			if strings.IndexFunc(f, unicode.IsLetter) >= 0 {
				out = append(out, f)
			}
		}
	}
	return out
}

// w501RunLane runs build → check (a gate rewinding to build): check fails
// once, then the lane completes — the opening, a rewind event and the
// completion event — with the live language lang answers; act (nil =
// nothing) runs while check's failing dispatch runs.
func w501RunLane(t *testing.T, lang func() string, act func()) string {
	t.Helper()
	dir := t.TempDir()
	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_ho", CapsuleDir: dir, BundleName: "b"},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: testLane(4, spec("build"), gateSpec("check", "build")),
		Spawner:  sp,
		Log:      &eventLog{},
		Language: lang,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_ho","slug":"demo-7","project":"proj","goal":"make the demo green","content":"Some background.\n\n## Acceptance\n- AC1 the demo is green\n"}`)
	sp.script = []fakeEntry{
		done(`{}`),
		handedOff(&stepcall.Handoff{Status: stepcall.StatusFail, Reason: "the demo is red"}, act),
		done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if x := d.Snapshot(); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q", x.WorkflowStatus)
	}
	return handoffDocOf(t, dir)
}

func TestW501OpeningAndEventsLanguage(t *testing.T) {
	frags := w501EnFragments()
	if len(frags) < 30 {
		t.Fatalf("the en label table carries %d labels differing from zh-CN's: every stamped label is in the table", len(frags))
	}

	en := w501RunLane(t, func() string { return stepcall.LangEN }, nil)
	for _, r := range en {
		if unicode.Is(unicode.Han, r) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			t.Fatalf("en: the daemon's text carries the CJK character %q:\n%s", string(r), en)
		}
	}
	for _, w := range []string{"# demo-7 · Hand-off document", "## Event · Rewind · ", "## Event · Close · ", "the demo is red", "`build` → `check`"} {
		if !strings.Contains(en, w) {
			t.Fatalf("en: the document lacks %q:\n%s", w, en)
		}
	}

	zh := w501RunLane(t, func() string { return stepcall.LangZhCN }, nil)
	for _, f := range frags {
		if strings.Contains(zh, f) {
			t.Fatalf("zh-CN: the document carries the en label %q:\n%s", f, zh)
		}
	}
	for _, w := range []string{"# demo-7 · 交接文档", "## 事件 · 回退 · ", "## 事件 · 收尾 · ", "要重做的步骤（按顺序）：`build` → `check`"} {
		if !strings.Contains(zh, w) {
			t.Fatalf("zh-CN: the document lacks %q:\n%s", w, zh)
		}
	}

	// no live language (nil) is the default en
	if doc := w501RunLane(t, nil, nil); !strings.Contains(doc, "# demo-7 · Hand-off document") {
		t.Fatalf("a driver without a live language stamps en:\n%s", doc)
	}

	// the live value at the moment of writing (AC5): the opening in en,
	// the events after the owner switched to zh-CN mid-lane in zh-CN
	var mu sync.Mutex
	lang := stepcall.LangEN
	mixed := w501RunLane(t, func() string {
		mu.Lock()
		defer mu.Unlock()
		return lang
	}, func() {
		mu.Lock()
		defer mu.Unlock()
		lang = stepcall.LangZhCN
	})
	if !strings.Contains(mixed, "# demo-7 · Hand-off document") || !strings.Contains(mixed, "## 事件 · 回退 · ") {
		t.Fatalf("each stamp takes the live value when written:\n%s", mixed)
	}
}
