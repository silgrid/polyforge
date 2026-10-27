package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real install.
func TestMain(m *testing.M) { testenv.Main(m) }

// initLocal initializes a capsule worktree without a remote — a test
// fixture only: production never inits a capsule locally (aihub always
// supplies the remote at claim). Takes the per-WI lock like the
// production verbs.
func initLocal(t *testing.T, o Opts) error {
	t.Helper()
	release := Lock(o.WorkItemID)
	defer release()
	if err := os.MkdirAll(o.CapsuleDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.GitDir), 0o755); err != nil {
		return err
	}
	// the production layout (Clone's): an external git-dir behind a
	// one-line .git pointer, working branch main
	if _, err := git(nil, "", "init", "--initial-branch=main", "--separate-git-dir="+o.GitDir, o.CapsuleDir); err != nil {
		return fmt.Errorf("git init: %s", err.Error())
	}
	return nil
}
