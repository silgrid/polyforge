package gitops

// empty_config_test.go: the daemon's hub and carrier git calls read their
// global config from a daemon-owned empty file next to the external
// git-dirs, never from the null device. A null device replaced by a
// regular file holding text makes git fail on every call that reads it as
// config ("bad config line 1"); the daemon-owned file is checked and
// rebuilt before each use, so such text there never reaches git either.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const brokenNullText = "bash: line 1: x: command not found\n"

// plantBrokenConfig writes the broken null device's bytes at path as a
// regular 0644 file.
func plantBrokenConfig(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte(brokenNullText), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertEmptyConfig requires path to be a regular, empty, read-only file.
func assertEmptyConfig(t *testing.T, path, when string) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: the daemon-owned empty config %s: %v", when, path, err)
	}
	if !st.Mode().IsRegular() || st.Size() != 0 || st.Mode().Perm() != 0o444 {
		t.Fatalf("%s: %s is mode %v, size %d; want a regular empty file, mode 0444", when, path, st.Mode(), st.Size())
	}
}

func TestCapsuleGitSurvivesBrokenNullDevice(t *testing.T) {
	setIdentity(t)

	// 1–2. A stand-in for a null device turned into a regular file holding
	// shell error text reproduces the failure: git refuses it as config.
	standIn := filepath.Join(t.TempDir(), "null")
	plantBrokenConfig(t, standIn)
	cmd := exec.Command("git", "config", "--list")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+standIn, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	cmd.Dir = t.TempDir()
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "bad config line 1") {
		t.Fatalf("the stand-in must reproduce the failure: err=%v out=%s", err, out)
	}

	// 3–5. The capsule clone succeeds with those bytes at the daemon-owned
	// path, and the file is an empty read-only regular file afterwards.
	o, barePath := testOpts(t, "wi_nullclone")
	mkBare(t, barePath, false)
	if out, err := run("git", "--git-dir", barePath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		t.Fatalf("bare HEAD: %v: %s", err, out)
	}
	seed, _ := testOpts(t, "wi_nullclone_seed")
	seed.RemoteURL = barePath
	mkCapsule(t, seed)
	writeDirty(t, seed, "seed.txt", "seed\n")
	if _, pushed, _, err := CommitPush(context.Background(), seed, "seed"); err != nil || !pushed {
		t.Fatalf("seed push: %v", err)
	}
	if err := os.RemoveAll(o.CapsuleDir); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(filepath.Dir(o.GitDir), "empty.config")
	plantBrokenConfig(t, cfg)
	if err := Clone(context.Background(), o); err != nil {
		t.Fatalf("Clone with a broken config at the daemon-owned path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(o.CapsuleDir, "seed.txt")); err != nil {
		t.Fatalf("the clone did not check the history out: %v", err)
	}
	assertEmptyConfig(t, cfg, "after Clone")

	// 6. The carrier lane (a publish with a task repo) lands its ref with
	// the same bytes planted, and leaves the file empty again.
	to, base := taskOpts(t, "wi_nullcarrier")
	mkBare(t, to.RemoteURL, false)
	mkTaskCapsule(t, to)
	workHead := mkTaskRepoWithWork(t, to.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	tcfg := filepath.Join(filepath.Dir(to.GitDir), "empty.config")
	plantBrokenConfig(t, tcfg)
	if _, _, _, err := CommitPush(context.Background(), to, "boundary"); err != nil {
		t.Fatalf("CommitPush with a broken config at the daemon-owned path: %v", err)
	}
	if got, ok := bareRefSHA(t, to.RemoteURL, TaskRefName("demo-src")); !ok || got != workHead {
		t.Fatalf("carrier ref = %q (%v), want the task work head %q", got, ok, workHead)
	}
	assertEmptyConfig(t, tcfg, "after CommitPush")
}
