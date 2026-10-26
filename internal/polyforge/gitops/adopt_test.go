package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AdoptCommit takes the sealed head from a quarantined git-dir as data:
// nothing configured inside that git-dir — hooks, a hooks path, an
// fsmonitor, a filter driver — runs, and the fresh capsule ends on the
// head with its files checked out.
func TestAdoptCommitRunsNothingFromTheSource(t *testing.T) {
	setIdentity(t)
	o, _ := testOpts(t, "wi_adopt_source")
	mkCapsule(t, o)
	gitIn := func(args ...string) string {
		t.Helper()
		out, err := git(o.Env(), o.CapsuleDir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "mark.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDirty(t, o, ".gitattributes", "*.txt filter=mark\n")
	writeDirty(t, o, "sealed.txt", "sealed work\n")
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "sealed")
	head := gitIn("rev-parse", "HEAD")
	hooks := filepath.Join(o.GitDir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"post-checkout", "reference-transaction", "post-merge", "post-rewrite", "pre-auto-gc", "post-index-change"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, kv := range [][2]string{
		{"core.hooksPath", hooks},
		{"core.fsmonitor", script},
		{"filter.mark.smudge", script},
		{"filter.mark.clean", script},
		{"uploadpack.packObjectsHook", script},
	} {
		gitIn("config", kv[0], kv[1])
	}
	_ = os.Remove(marker) // the setup's own commit may have run a hook

	src := o.GitDir + ".quarantined.x"
	if err := os.Rename(o.GitDir, src); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(o.CapsuleDir); err != nil {
		t.Fatal(err)
	}
	mkCapsule(t, o)
	if !HasCommit(o, src, head) {
		t.Fatal("HasCommit must find the sealed head in the source")
	}
	if err := AdoptCommit(context.Background(), o, head, src); err != nil {
		t.Fatalf("AdoptCommit: %v", err)
	}
	if got, err := Head(o); err != nil || got != head {
		t.Fatalf("adopted head = %q (%v), want %s", got, err, head)
	}
	if b, err := os.ReadFile(filepath.Join(o.CapsuleDir, "sealed.txt")); err != nil || string(b) != "sealed work\n" {
		t.Fatalf("sealed.txt = %q (%v)", b, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("something configured in the quarantined git-dir ran during the adopt (%v)", err)
	}
}
