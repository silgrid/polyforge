package runtime

// workspace_repos_test.go: shared git-fixture helpers for the workspace
// tests (the yaml no longer sources repos; claims clone registry URLs).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w079GitRepo creates a real git repo (one commit carrying markerName
// with markerContent) at dir and answers its HEAD SHA.
func w079GitRepo(t *testing.T, dir, markerName, markerContent string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=pf-test", "GIT_AUTHOR_EMAIL=pf-test@invalid",
			"GIT_COMMITTER_NAME=pf-test", "GIT_COMMITTER_EMAIL=pf-test@invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "w079 init")
	return strings.TrimSpace(git("rev-parse", "HEAD"))
}

// w079Remote is one local fake registry remote served over file://.
func w079Remote(t *testing.T, markerName, markerContent string) (url, head string) {
	t.Helper()
	dir := t.TempDir()
	return "file://" + dir, w079GitRepo(t, dir, markerName, markerContent)
}
