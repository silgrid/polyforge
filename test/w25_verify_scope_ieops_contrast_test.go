package test

// w25_verify_scope_ieops_contrast_test.go: the real-history byte-identical
// contrast for workbench#25. The frozen seed script and the release-shipped
// engine (driven by this repository's `.polyforge/verify-scope.conf`) must
// print the same report for the same range, including the last ten real
// first-parent merge ranges of this repository.
//
// Both sides fall back to `full` identically when `go list` cannot run in a
// throwaway worktree; the stdout comparison is the oracle either way.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25Ieops' -count=1

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w25ioMerges lists the last n first-parent merge commits, newest first.
func w25ioMerges(t *testing.T, root string, n int) []string {
	t.Helper()
	c := exec.Command("git", "-C", root, "log", "--first-parent", "--merges", "--format=%H", "-n", "10")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git log merges: %v", err)
	}
	var merges []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			merges = append(merges, line)
		}
	}
	if len(merges) > n {
		merges = merges[:n]
	}
	return merges
}

// w25ioWorktree adds a detached throwaway worktree at sha and answers its dir.
func w25ioWorktree(t *testing.T, root, sha string) (dir string, cleanup func()) {
	t.Helper()
	base := t.TempDir()
	dir = filepath.Join(base, "wt")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", dir, sha).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", sha, err, out)
	}
	cleanup = func() {
		_, _ = exec.Command("git", "-C", root, "worktree", "remove", "--force", dir).CombinedOutput()
		_, _ = exec.Command("git", "-C", root, "worktree", "prune").CombinedOutput()
	}
	return dir, cleanup
}

// TestW25IeopsFrozenContrastOnRealRanges replays the last ten merge ranges in
// throwaway worktrees and requires byte-identical stdout.
func TestW25IeopsFrozenContrastOnRealRanges(t *testing.T) {
	root := w25engRoot(t)
	frozen := filepath.Join(root, "test", "testdata", "verify-scope", "verify-scope-go.sh")
	conf := filepath.Join(root, ".polyforge", "verify-scope.conf")
	if _, err := os.Stat(frozen); err != nil {
		t.Fatalf("the frozen seed must exist: %v", err)
	}
	if _, err := os.Stat(conf); err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	merges := w25ioMerges(t, root, 10)
	if len(merges) == 0 {
		t.Skip("no merge history in this clone (shallow checkout)")
	}
	for _, merge := range merges {
		merge := merge
		t.Run(merge[:12], func(t *testing.T) {
			base := strings.TrimSpace(w25engGit(t, root, "rev-parse", merge+"^1"))
			dir, cleanup := w25ioWorktree(t, root, merge)
			defer cleanup()
			w25engCopy(t, frozen, filepath.Join(dir, ".polyforge", "verify-scope.sh"), 0o755)
			w25engCopy(t, conf, filepath.Join(dir, ".polyforge", "verify-scope.conf"), 0o644)
			// The engine is new in this change, so an older range's worktree does
			// not carry it; install the current release tree's engine there.
			w25engCopy(t, filepath.Join(root, "tools", "verify-scope"), filepath.Join(dir, "tools", "verify-scope"), 0o755)
			frozenOut, frozenErr, frozenCode := w25engRun(t, dir, filepath.Join(dir, ".polyforge", "verify-scope.sh"), nil, "--base", base)
			if frozenCode != 0 {
				t.Fatalf("frozen seed exited %d: %s", frozenCode, frozenErr)
			}
			engineOut, engineErr, engineCode := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--base", base)
			if engineCode != 0 {
				t.Fatalf("engine exited %d: %s", engineCode, engineErr)
			}
			if frozenOut != engineOut {
				t.Fatalf("engine stdout differs from the frozen seed\n--- frozen ---\n%s--- engine ---\n%s", frozenOut, engineOut)
			}
			_ = w25engParse(t, engineOut)
		})
	}
}
