package test

// pf245_w113_daemon_git_config_test.go (polyforge#245, verification r1#1):
// a w113 daemon runs under its own HOME, yet the project repos the
// harness registers are forge-shaped URLs (leaseableProjectRepos) that
// only the test run's hermetic global git config rewrites back to the
// local bare repository. The daemon's git must read that same config,
// or its claim clone dials https://forge.invalid and the workflow never
// starts (TestW079AbsentAndEmptyYamlRunExactlyAsHead's timeout).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestPF245W113DaemonGitSeesLeaseableURLs resolves a leaseable repo URL
// with the w113 daemon's own process environment.
func TestPF245W113DaemonGitSeesLeaseableURLs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	bare := filepath.Join(t.TempDir(), "demo.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	got := leaseableProjectRepos(t, []api.ProjectRepo{{Name: "demo", URL: "file://" + bare}})
	home := t.TempDir()
	d := &w113Daemon{t: t, home: home, xdg: filepath.Join(home, ".config")}
	if err := os.MkdirAll(d.xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "ls-remote", "--get-url", got[0].URL)
	cmd.Env = d.processEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-remote --get-url %s under the daemon env: %v", got[0].URL, err)
	}
	if want := "file://" + bare; strings.TrimSpace(string(out)) != want {
		t.Fatalf("under the w113 daemon's env %s resolves to %q, want %q: the daemon's git does not read the run's insteadOf mapping", got[0].URL, strings.TrimSpace(string(out)), want)
	}
}
