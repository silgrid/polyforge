package driver

// treehash_test.go: the capsule tree hash contract. Each
// requirement-2 scenario is one subtest over a real git capsule.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// treeHashGit runs one git command through the production helper (the
// same one capsuleTreeHash uses).
func treeHashGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := repoGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

func treeHashInitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	treeHashGit(t, dir, "init", "-q")
	treeHashGit(t, dir, "config", "user.email", "t@example.com")
	treeHashGit(t, dir, "config", "user.name", "t")
}

func treeHashCommit(t *testing.T, dir, msg string) {
	t.Helper()
	treeHashGit(t, dir, "add", "-A")
	treeHashGit(t, dir, "commit", "--no-gpg-sign", "-q", "-m", msg)
}

func treeHashWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func treeHashNow(t *testing.T, root string) string {
	t.Helper()
	h, err := capsuleTreeHash(root)
	if err != nil {
		t.Fatalf("capsuleTreeHash(%s): %v", root, err)
	}
	if h == "" {
		t.Fatalf("capsuleTreeHash(%s) returned an empty hash", root)
	}
	return h
}

// newTreeHashCapsule builds a capsule whose root is a git repo (the
// capsule .gitignore ignores /repos/ and /artifacts/release-*/), with a
// committed task repo repos/app.
func newTreeHashCapsule(t *testing.T) (root, repoDir string) {
	t.Helper()
	root = t.TempDir()
	treeHashInitRepo(t, root)
	treeHashWrite(t, filepath.Join(root, ".gitignore"), capsule.GitignoreContent)
	treeHashWrite(t, filepath.Join(root, capsule.DirArtifacts, "seed.txt"), "seed\n")
	treeHashCommit(t, root, "capsule init")

	repoDir = filepath.Join(root, capsule.DirRepos, "app")
	treeHashInitRepo(t, repoDir)
	treeHashWrite(t, filepath.Join(repoDir, ".gitignore"), "node_modules/\n")
	treeHashWrite(t, filepath.Join(repoDir, "tracked.txt"), "one\n")
	treeHashCommit(t, repoDir, "one")
	return root, repoDir
}

func TestCapsuleTreeHash(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		root, _ := newTreeHashCapsule(t)
		if a, b := treeHashNow(t, root), treeHashNow(t, root); a != b {
			t.Fatalf("the same tree hashed twice differs: %q vs %q", a, b)
		}
	})

	t.Run("second commit changes the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(repoDir, "tracked.txt"), "two\n")
		treeHashCommit(t, repoDir, "two")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a new commit left the hash unchanged: %q", after)
		}
	})

	t.Run("uncommitted tracked edit changes the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(repoDir, "tracked.txt"), "dirty\n")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("an uncommitted edit left the hash unchanged: %q", after)
		}
	})

	t.Run("staged addition changes the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(repoDir, "staged.txt"), "new\n")
		treeHashGit(t, repoDir, "add", "staged.txt")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a staged addition left the hash unchanged: %q", after)
		}
	})

	t.Run("non-ignored untracked file changes the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(repoDir, "untracked.txt"), "new\n")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a non-ignored untracked file left the hash unchanged: %q", after)
		}
	})

	t.Run("a symlink is hashed by its target string, not followed", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		link := filepath.Join(repoDir, "link.txt")
		if err := os.Symlink("/nonexistent/one", link); err != nil {
			t.Fatal(err)
		}
		base := treeHashNow(t, root)
		if after := treeHashNow(t, root); after != base {
			t.Fatalf("the same tree hashed twice differs: %q vs %q", after, base)
		}
		// Repointing the link changes its identity even though neither
		// target exists on disk (the old os.ReadFile read both as "gone").
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/nonexistent/two", link); err != nil {
			t.Fatal(err)
		}
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("repointing the symlink left the hash unchanged: %q", after)
		}
	})

	t.Run("a symlink to a non-regular file does not hang", func(t *testing.T) {
		if _, err := os.Stat("/dev/zero"); err != nil {
			t.Skip("/dev/zero not available")
		}
		root, repoDir := newTreeHashCapsule(t)
		if err := os.Symlink("/dev/zero", filepath.Join(repoDir, "zero.txt")); err != nil {
			t.Fatal(err)
		}
		// os.ReadFile on this link would read an infinite stream; the hash
		// must complete and identify the link by its target string.
		a, b := treeHashNow(t, root), treeHashNow(t, root)
		if a != b {
			t.Fatalf("nondeterministic: %q vs %q", a, b)
		}
	})

	t.Run("a tracked deletion changes the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		if err := os.Remove(filepath.Join(repoDir, "tracked.txt")); err != nil {
			t.Fatal(err)
		}
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a tracked deletion left the hash unchanged: %q", after)
		}
	})

	t.Run("ignored files do not change the hash", func(t *testing.T) {
		root, repoDir := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(repoDir, "node_modules", "pkg", "index.js"), "half installed\n")
		if after := treeHashNow(t, root); after != base {
			t.Fatalf("an ignored path moved the hash: %q vs %q", after, base)
		}
	})

	t.Run("driver-owned state does not change the hash", func(t *testing.T) {
		root, _ := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(root, capsule.DirRuntimeState, "execution.json"), "{}")
		treeHashWrite(t, filepath.Join(root, capsule.DirSession, "session-1.jsonl"), "{}\n")
		if after := treeHashNow(t, root); after != base {
			t.Fatalf("driver-owned state moved the hash: %q vs %q", after, base)
		}
	})

	t.Run("an artifacts change changes the hash", func(t *testing.T) {
		root, _ := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(root, capsule.DirArtifacts, "plan.md"), "# plan\n")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a new artifacts file left the hash unchanged: %q", after)
		}
	})

	t.Run("a release tree does not change the hash", func(t *testing.T) {
		root, _ := newTreeHashCapsule(t)
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(root, capsule.DirArtifacts, "release-1", "bin", "pf"), "binary\n")
		if after := treeHashNow(t, root); after != base {
			t.Fatalf("a capsule-gitignored release tree moved the hash: %q vs %q", after, base)
		}
	})

	t.Run("a non-git capsule returns an error", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, capsule.DirRepos, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := capsuleTreeHash(root); err == nil {
			t.Fatalf("a non-git task repo must return an error (the no-hash contract)")
		}
	})

	t.Run("a capsule with no task repos hashes its artifacts", func(t *testing.T) {
		root := t.TempDir()
		treeHashInitRepo(t, root)
		treeHashWrite(t, filepath.Join(root, ".gitignore"), capsule.GitignoreContent)
		treeHashWrite(t, filepath.Join(root, capsule.DirArtifacts, "seed.txt"), "seed\n")
		treeHashCommit(t, root, "capsule init")
		base := treeHashNow(t, root)
		treeHashWrite(t, filepath.Join(root, capsule.DirArtifacts, "plan.md"), "# plan\n")
		if after := treeHashNow(t, root); after == base {
			t.Fatalf("a repo-less capsule must still hash its artifacts: %q", after)
		}
	})
}
