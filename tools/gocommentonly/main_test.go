package main

// main_test.go (polyforge#219, u1): the comment-only judge's unit
// test. Every case is a source pair written from the settled rules
// (owner 2026-11-22: Q2 B directive sets compared first — //go:,
// // +build, //line, /*line, //export and import "C"; Q3 A both ends
// parsed by go/parser, then comment-free go/scanner token streams
// compared with positions and semicolon literals normalized), never
// from the implementation's own answers. The real pair is the two
// actual versions of internal/common/protocol/watch.go the fifteenth
// batch's green tag green/20261121-1 carries over the v0.2.3 release
// tag (Q7 C): four comment lines, no code change — the change this
// ticket unblocks, pinned by its sha256 so the fixtures cannot drift
// silently.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// w219Testdata reads one testdata fixture.
func w219Testdata(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGocommentonly(t *testing.T) {
	// pass cases: same Go code, comments (and comment-caused
	// whitespace) differ — the one exception the interface preflight
	// grants
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"identical sources pass",
			"package p\n\nfunc F() {}\n",
			"package p\n\nfunc F() {}\n"},
		{"comment added passes",
			"package p\n\nfunc F() {}\n",
			"package p\n\n// F does nothing.\nfunc F() {}\n"},
		{"comment modified passes",
			"package p\n\n// F does nothing.\nfunc F() {}\n",
			"package p\n\n// F still does nothing.\nfunc F() {}\n"},
		{"comment deleted passes",
			"package p\n\n// F does nothing.\nfunc F() {}\n",
			"package p\n\nfunc F() {}\n"},
		{"block comment passes",
			"package p\n\nfunc F() {}\n",
			"package p\n\n/* one\n   block */\nfunc F() {}\n"},
		{"blank-line and indentation reflow passes",
			"package p\n\nfunc F() {\n\tg()\n\th()\n}\n",
			"package p\n\n\nfunc F() {\n\n\t\tg()\n\n\t\th()\n\n}\n\n"},
		{"a comment moved between statements passes",
			"package p\n\nfunc F() {\n\tg()\n\t// note\n\th()\n}\n",
			"package p\n\n// note moved\nfunc F() {\n\tg()\n\th()\n}\n"},
		{"an unchanged directive set beside a changed comment passes",
			"//go:build linux\n\npackage p\n\nfunc F() {}\n",
			"//go:build linux\n\npackage p\n\n// F does nothing.\nfunc F() {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ok, why := judge(tc.old, tc.new); !ok {
				t.Fatalf("judge must pass a comment-only change (%s)", why)
			}
			if ok, why := judge(tc.new, tc.old); !ok {
				t.Fatalf("judge must be symmetric: the pair reversed must pass too (%s)", why)
			}
		})
	}

	// refuse cases: any code change, any directive-comment change, a
	// changed cgo presence or preamble, an unparseable version — fail
	// closed, exactly as before the exception existed. The reflow case
	// pins Q3 A's documented strictness: `{ g(); h() }` and the same
	// statements on separate lines are grammar-equivalent, but the
	// newline inserts a real semicolon token the written form lacks, and
	// the settled token-stream rule refuses (the owner-accepted cost of
	// the fail-closed bias).
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"a renamed field refuses",
			"package p\n\ntype T struct{ Reason string }\n",
			"package p\n\ntype T struct{ Why string }\n"},
		{"a changed string literal refuses",
			"package p\n\nconst M = \"parked\"\n",
			"package p\n\nconst M = \"parked!\"\n"},
		{"a changed struct tag refuses",
			"package p\n\ntype T struct{ Reason string `json:\"reason\"` }\n",
			"package p\n\ntype T struct{ Reason string `json:\"why\"` }\n"},
		{"a changed identifier refuses",
			"package p\n\nfunc F() { g() }\n",
			"package p\n\nfunc F() { h() }\n"},
		{"an added go directive refuses",
			"package p\n\nfunc F() {}\n",
			"//go:generate echo hi\n\npackage p\n\nfunc F() {}\n"},
		{"an added build constraint refuses",
			"package p\n\nfunc F() {}\n",
			"//go:build linux\n// +build linux\n\npackage p\n\nfunc F() {}\n"},
		{"a changed go directive refuses",
			"//go:build linux\n\npackage p\n\nfunc F() {}\n",
			"//go:build darwin\n\npackage p\n\nfunc F() {}\n"},
		{"an added line directive refuses",
			"package p\n\nfunc F() {}\n",
			"//line fake.go:1:1\npackage p\n\nfunc F() {}\n"},
		{"a changed cgo presence refuses",
			"package p\n\nfunc F() {}\n",
			"package p\n\nimport \"C\"\n\nfunc F() {}\n"},
		{"a changed cgo preamble refuses",
			"package p\n\n// int one;\nimport \"C\"\n\nfunc F() {}\n",
			"package p\n\n// int two;\nimport \"C\"\n\nfunc F() {}\n"},
		{"a reflow that adds a statement-list semicolon refuses",
			"package p\n\nfunc F() { g(); h() }\n",
			"package p\n\nfunc F() {\n\tg()\n\th()\n}\n"},
		{"an unparseable old version refuses",
			"this is not go source at all\n",
			"// still nothing but a comment\nthis is not go source at all\n"},
		{"an unparseable new version refuses",
			"package p\n\nfunc F() {}\n",
			"package p\n\nfunc F() {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ok, why := judge(tc.old, tc.new); ok {
				t.Fatal("judge must refuse: a comment-only verdict would pass a code or directive change")
			} else if why == "" {
				t.Fatal("a refusal carries a one-line reason")
			}
			if ok, _ := judge(tc.new, tc.old); ok {
				t.Fatal("judge must be symmetric: the pair reversed must refuse too")
			}
		})
	}

	// the real pair (Q7 C): v0.2.3's and green/20261121-1's
	// internal/common/protocol/watch.go — the fifteenth batch's four
	// comment lines. The sha256s pin the fixtures to the bytes the two
	// revisions actually carry, so a silent edit to the testdata
	// cannot silently change what is judged.
	t.Run("the real v0.2.3 vs green/20261121-1 watch.go passes", func(t *testing.T) {
		oldSrc := w219Testdata(t, "watch-v0.2.3.go")
		newSrc := w219Testdata(t, "watch-green-20261121-1.go")
		for name, src := range map[string]string{
			"watch-v0.2.3.go":           oldSrc,
			"watch-green-20261121-1.go": newSrc,
		} {
			sum := sha256.Sum256([]byte(src))
			want := map[string]string{
				"watch-v0.2.3.go":           "499708172c596dac055340f963469e3452819490b9b22fe86054ccb19555f6c2",
				"watch-green-20261121-1.go": "8de4aae49009b424c59a8a100d23ccfc92ccc9d509d99b4e7c243e59a7878adc",
			}[name]
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Fatalf("%s drifted: sha256 %s, want the bytes of the revision itself (%s)", name, got, want)
			}
		}
		if oldSrc == newSrc {
			t.Fatal("the two revisions of watch.go are byte-identical: the fixture would judge nothing")
		}
		if ok, why := judge(oldSrc, newSrc); !ok {
			t.Fatalf("the fifteenth batch's watch.go pair is the reported comment-only change and must pass (%s)", why)
		}
	})
}
