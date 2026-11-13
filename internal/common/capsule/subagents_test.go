package capsule

import (
	"os"
	"path/filepath"
	"testing"
)

// a capsule session transcript's subagent transcripts live
// beside it — session/<stem>/subagents/ — Claude Code's own
// <session>.jsonl + <session>/subagents/ layout, for both harnesses.
func TestSubagentDir(t *testing.T) {
	got := SubagentDir("/c/session/session-0123456789abcdef#1.jsonl")
	if want := "/c/session/session-0123456789abcdef#1/subagents"; got != want {
		t.Fatalf("SubagentDir = %q, want %q", got, want)
	}
}

// The writable subagent directory of a transcript is created only inside
// the capsule's own session/ directory: a transcript elsewhere, a stem
// that is not a plain name, or a symlinked stem/subagents directory (the
// capsule is agent-writable) answers no directory, and nothing is created
// outside the capsule.
func TestEnsureSubagentDir(t *testing.T) {
	capDir := t.TempDir()
	sess := filepath.Join(capDir, DirSession)
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, ok := EnsureSubagentDir(capDir, filepath.Join(sess, "session-a.jsonl"))
	if !ok || dir != filepath.Join(sess, "session-a", DirSubagents) {
		t.Fatalf("EnsureSubagentDir = %q, %v", dir, ok)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("the directory was not created: %v", err)
	}
	if dir2, ok := EnsureSubagentDir(capDir, filepath.Join(sess, "session-a.jsonl")); !ok || dir2 != dir {
		t.Fatalf("an existing directory is answered again: %q, %v", dir2, ok)
	}

	outside := t.TempDir()
	for name, transcript := range map[string]string{
		"outside the capsule":        filepath.Join(outside, "session-x.jsonl"),
		"capsule root, not session/": filepath.Join(capDir, "session-x.jsonl"),
		"a nested session path":      filepath.Join(sess, "deep", "session-x.jsonl"),
		"not a jsonl":                filepath.Join(sess, "session-x.json"),
		"an empty stem":              filepath.Join(sess, ".jsonl"),
		"a relative path":            "session/session-x.jsonl",
	} {
		if dir, ok := EnsureSubagentDir(capDir, transcript); ok {
			t.Fatalf("%s: answered %q", name, dir)
		}
	}
	// a symlinked stem directory pointing outside the capsule is refused,
	// and nothing is created through it
	if err := os.Symlink(outside, filepath.Join(sess, "session-b")); err != nil {
		t.Fatal(err)
	}
	if dir, ok := EnsureSubagentDir(capDir, filepath.Join(sess, "session-b.jsonl")); ok {
		t.Fatalf("a symlinked stem was followed: %q", dir)
	}
	if _, err := os.Stat(filepath.Join(outside, DirSubagents)); err == nil {
		t.Fatal("a directory was created outside the capsule")
	}
	// a symlinked subagents directory is refused too
	if err := os.MkdirAll(filepath.Join(sess, "session-c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(sess, "session-c", DirSubagents)); err != nil {
		t.Fatal(err)
	}
	if dir, ok := EnsureSubagentDir(capDir, filepath.Join(sess, "session-c.jsonl")); ok {
		t.Fatalf("a symlinked subagents dir was followed: %q", dir)
	}
}
