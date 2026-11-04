package workspace

import (
	"reflect"
	"strings"
	"testing"
)

// TestMemoryPolicyBuiltinDefault pins the built-in policy and that no
// declared entries answer it.
func TestMemoryPolicyBuiltinDefault(t *testing.T) {
	d := DefaultMemoryPolicy()
	if len(d.Record) != 3 || !strings.Contains(d.Record[0], "踩坑和根因") || len(d.Skip) != 4 || d.Skip[3] != "密钥和凭据的值" {
		t.Fatalf("built-in policy = %+v", d)
	}
	if got := MemoryPolicyFor(nil, "e2e"); !reflect.DeepEqual(got, d) {
		t.Fatalf("no entries = %+v", got)
	}
	// the caller's copy is its own
	d.Record[0] = "x"
	if DefaultMemoryPolicy().Record[0] == "x" {
		t.Fatal("DefaultMemoryPolicy shares its backing array")
	}
}

// TestMemoryPolicyMerge pins the merge: default's declared lists replace
// the built-in ones list by list, a project's lists are appended
// (duplicates dropped), a declared empty list governs.
func TestMemoryPolicyMerge(t *testing.T) {
	entries := map[string]PolicyEntry{
		DefaultKey: {Record: []string{"r1", "r2"}},
		"e2e":      {Record: []string{"r2", "r3"}, Skip: []string{"s1"}},
		"other":    {Skip: []string{}},
	}
	builtinSkip := DefaultMemoryPolicy().Skip
	if got := MemoryPolicyFor(entries, "e2e"); !reflect.DeepEqual(got.Record, []string{"r1", "r2", "r3"}) ||
		!reflect.DeepEqual(got.Skip, append(append([]string(nil), builtinSkip...), "s1")) {
		t.Fatalf("e2e policy = %+v", got)
	}
	if got := MemoryPolicyFor(entries, "other"); !reflect.DeepEqual(got.Record, []string{"r1", "r2"}) || !reflect.DeepEqual(got.Skip, builtinSkip) {
		t.Fatalf("other policy = %+v", got)
	}
	if got := MemoryPolicyFor(entries, "third"); !reflect.DeepEqual(got.Record, []string{"r1", "r2"}) {
		t.Fatalf("third policy = %+v", got)
	}
	empty := MemoryPolicyFor(map[string]PolicyEntry{DefaultKey: {Skip: []string{}}}, "e2e")
	if len(empty.Skip) != 0 || empty.Skip == nil {
		t.Fatalf("declared empty default skip: %+v", empty)
	}
	// a project-only block keeps the built-in base
	if got := MemoryPolicyFor(map[string]PolicyEntry{"e2e": {Record: []string{"extra"}}}, "e2e"); len(got.Record) != 4 || got.Record[3] != "extra" {
		t.Fatalf("project-only block: %+v", got)
	}
}

// TestParseTrailingContentReason pins the refusal of content after the
// top-level document: it names the parse failure, not a second document.
func TestParseTrailingContentReason(t *testing.T) {
	for _, raw := range []string{"{steps: {}}\nfoo:\n  default:\n    skip: [a]\n", "steps:\n...\nfoo: {}\n"} {
		_, reason := parse([]byte(raw), nil)
		if !strings.Contains(reason, "content after the top-level document does not parse") || strings.Contains(reason, "second `---`") {
			t.Fatalf("%q: reason %q", raw, reason)
		}
	}
	if _, reason := parse([]byte("a: 1\n---\nb: 2\n"), nil); !strings.Contains(reason, "found a second `---` document") {
		t.Fatalf("two documents: %q", reason)
	}
}
