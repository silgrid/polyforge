package workspace

// w433_lane_set_test.go (workflow contract "User lanes"):
// the workspace yaml's per-step keys are judged over the lane set of the
// workspace the yaml belongs to — the built-in lanes and that workspace's
// user lanes — so a user lane's step key is not stale. An unreadable user
// lane root disables only the user lanes: the yaml still loads, and no
// key is called stale.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// w433Workspace writes yaml into a fresh workspace carrying the user lane
// notes (one step draft) and answers the built-in lanes root (stepsDir)
// and the workspace.
func w433Workspace(t *testing.T, yaml string) (string, string) {
	t.Helper()
	builtin := stepsDir(t)
	ws := writeYaml(t, yaml)
	dir := filepath.Join(workflow.UserLanesRoot(ws), "notes")
	files := map[string]string{
		workflow.WorkflowFile: "contract: 2\nname: notes\ndescription: a user lane\nsteps:\n  - id: draft\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return builtin, ws
}

func TestW433UnreadableUserLaneRootKeepsTheBuiltinLanes(t *testing.T) {
	builtin, ws := w433Workspace(t, "steps:\n  p:\n    fix: { models: [pi:f] }\n    draft: { models: [pi:d] }\n")
	root := workflow.UserLanesRoot(ws)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(ws, nil)
	if err != nil {
		t.Fatalf("an unreadable user lane root disables only the user lanes, never the yaml: %v", err)
	}
	stale, err := StaleKeys(cfg, workflow.ForWorkspace(builtin, ws))
	var ule *workflow.UserLanesError
	if !errors.As(err, &ule) || ule.Root != root || len(stale) != 0 {
		t.Fatalf("no key is judged stale while the user lanes are unknown, and the error names the root: %v, %v", stale, err)
	}
}
