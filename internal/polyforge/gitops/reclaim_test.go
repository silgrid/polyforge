package gitops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Clone must refuse a pre-existing target git-dir (a stale one from a
// previous session): no silent reuse, no merge, nothing touched.
func TestCloneRefusesExistingGitDirTarget(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_stale_gd")
	mkBare(t, barePath, false)
	if err := os.RemoveAll(o.CapsuleDir); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []bool{true, false} {
		if err := os.MkdirAll(o.GitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(o.GitDir, "STALE")
		if stale {
			if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		err := Clone(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("want a loud refusal (non-empty=%v), got %v", stale, err)
		}
		if _, serr := os.Stat(o.CapsuleDir); !os.IsNotExist(serr) {
			t.Fatal("a refused clone must not create the worktree")
		}
		if stale {
			if _, serr := os.Stat(sentinel); serr != nil {
				t.Fatal("the existing target must be left untouched")
			}
			if err := os.Remove(sentinel); err != nil {
				t.Fatal(err)
			}
		}
	}
}
