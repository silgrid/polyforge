package runtime

// materialize_test: the helpers for the F7 bundle materialization tests.
// The digest chain (the pinned-digest verification and the name-derived
// stand-in) is deleted (polyforge#149): materialization lays out the
// bundle by NAME and never verifies content digests.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// pristineBundle writes the demo bundle into a daemon-checkout workflows
// dir and answers the dir (the bundle materializes by name).
func pristineBundle(t *testing.T) (workflowsDir string) {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "demo-bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// CRLF on disk: the copy is byte-faithful, normalization is gone
	// with the digest chain.
	if err := os.WriteFile(filepath.Join(bundle, "steps", "step.md"), []byte("do the thing\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Materialization proceeds against ANY checkout content: no digest is
// pinned or verified anywhere on the path.
func TestMaterializeNeverVerifiesContent(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	// tampered content — content is never checked
	if err := os.WriteFile(filepath.Join(wfDir, "demo-bundle", "workflow.md"),
		[]byte("# tampered too\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir

	if err := r.Start(context.Background(), "wi_dig2", StartOpts{}); err != nil {
		t.Fatalf("Start must materialize without any verification: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testCapsuleDir(r, "wi_dig2"), capsule.DirWorkflows, "demo-bundle", "workflow.md")); err != nil {
		t.Fatalf("bundle must be materialized: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_dig2", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
