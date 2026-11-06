package driver

// w536_build_history_rules_test.go: the daemon backs every
// commit a step makes up to aihub, and refuses to back up a history that
// no longer contains the earlier backup. The build skill therefore never
// rewrites a commit it has made (a fix is a new commit) and checks an
// earlier commit's red in a separate worktree, never by switching its
// working directory to that commit.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW536BuildSkillKeepsCommittedHistory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, "_shared", "skills", "build.md"))
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(string(raw)), " ")
	for _, w := range []string{
		"Never amend (`git commit --amend`) or otherwise rewrite a commit you have made — no rebase, no reset past it: the daemon backs every commit up to aihub and cannot back up a rewritten history over it; a fix is a new commit.",
		"To check an earlier commit's red again, check it out in a separate directory (`git worktree add <dir> <sha>`, then `git worktree remove <dir>`), never by switching your working directory to that commit.",
	} {
		if !strings.Contains(flat, w) {
			t.Errorf("workflows/_shared/skills/build.md does not say %q", w)
		}
	}
}
