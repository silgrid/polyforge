package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureBareConvergesAfterCrashMidInit (CR-8): the hub killed right
// after `git init --bare` leaves a repo with HEAD but without the push
// configuration. The next EnsureBare (a fresh process) must finish it:
// without receive.advertisePushOptions every daemon push is refused.
func TestEnsureBareConvergesAfterCrashMidInit(t *testing.T) {
	requireGitBin(t)
	mgr, err := NewManager(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	const wiID = "wi_cr8_half"
	dir := mgr.RepoDir(wiID)
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := mgr.EnsureBare(wiID); err != nil {
		t.Fatalf("EnsureBare: %v", err)
	}
	cfg := func(args ...string) string {
		out, _ := exec.Command("git", append([]string{"--git-dir", dir}, args...)...).Output()
		return strings.TrimSpace(string(out))
	}
	if got := cfg("config", "receive.advertisePushOptions"); got != "true" {
		t.Fatalf("receive.advertisePushOptions = %q after the converging EnsureBare", got)
	}
	if got := cfg("config", "core.hooksPath"); got != "hooks" {
		t.Fatalf("core.hooksPath = %q", got)
	}
	if got := cfg("symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatalf("HEAD = %q", got)
	}
	if st, err := os.Stat(filepath.Join(dir, "hooks", "pre-receive")); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("pre-receive hook missing or not executable: %v", err)
	}
}
