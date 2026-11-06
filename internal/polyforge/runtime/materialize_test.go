package runtime

// materialize_test: the helpers for the F7 bundle materialization tests.
// The digest chain (the pinned-digest verification and the name-derived
// stand-in) is deleted (polyforge#149): materialization lays out the
// bundle by NAME and never verifies content digests.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// pristineBundle writes the demo bundle into a daemon-checkout workflows
// dir and answers the dir (the bundle materializes by name).
func pristineBundle(t *testing.T) (workflowsDir string) {
	t.Helper()
	dir := t.TempDir()
	// the lane's prose and a var file of the lane ride the snapshot too
	// (workflow contract 5: README.md, fragments/)
	for rel, body := range map[string]string{"README.md": "# demo-bundle\n", "fragments/note.md": "a long note\n"} {
		p := filepath.Join(dir, "demo-bundle", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bundle := workflowtest.WriteLane(t, dir, "demo-bundle", "steps:\n  - { id: step, vars_files: { note: fragments/note.md } }\n")
	// CRLF on disk: the copy is byte-faithful, normalization is gone
	// with the digest chain.
	crlf := strings.ReplaceAll(workflowtest.Definition("step", "note"), "\n", "\r\n")
	if err := os.WriteFile(filepath.Join(bundle, "steps", "step.md"), []byte(crlf), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Materialization proceeds against ANY checkout content: no digest is
// pinned or verified anywhere on the path.
func TestMaterializeNeverVerifiesContent(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	// tampered content — no digest is checked (the lane still loads: a
	// claim refuses a lane the contract refuses, not a changed one)
	p := filepath.Join(wfDir, "demo-bundle", workflow.WorkflowFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(raw, "# tampered too\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir

	if err := r.Start(context.Background(), "wi_dig2", StartOpts{}); err != nil {
		t.Fatalf("Start must materialize without any verification: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testCapsuleDir(r, "wi_dig2"), capsule.DirWorkflows, "demo-bundle", workflow.WorkflowFile)); err != nil {
		t.Fatalf("bundle must be materialized: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_dig2", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
