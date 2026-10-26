package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// The checkout's shared step definitions (workflows/_shared/) are
// materialized beside the bundle and recorded in the marker, so the
// driver's fallback resolves them inside the capsule (decision 2026-10-16
// G1). Symlinks are not an option: copyTree skips them.
func TestMaterializeSetCarriesSharedSteps(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	shared := filepath.Join(wfDir, "_shared", "steps")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "deliver.md"), []byte("deliver it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir
	if err := r.Start(context.Background(), "wi_shared", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), "wi_shared", protocol.FinishWrapped) })
	capDir := testCapsuleDir(r, "wi_shared")

	const rel = "workflows/_shared/steps/deliver.md"
	got, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(rel)))
	if err != nil || string(got) != "deliver it\n" {
		t.Fatalf("shared step not materialized: %q, %v", got, err)
	}
	found := false
	for _, tgt := range readMarker(t, capDir).Targets {
		found = found || tgt == rel
	}
	if !found {
		t.Fatalf("marker does not record %s", rel)
	}
}

// "_shared" is the shared-steps directory, never a bundle: the pre-claim
// check and the materialization both refuse it by name.
func TestSharedDirIsNotABundle(t *testing.T) {
	wfDir := pristineBundle(t)
	if err := os.MkdirAll(filepath.Join(wfDir, "_shared", "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir
	if err := r.CheckBundle("wi_s", "_shared"); err == nil || !strings.Contains(err.Error(), "not a workflow bundle") {
		t.Fatalf("CheckBundle(_shared) = %v, want a refusal", err)
	}
	if err := r.materializeBundle(&job{wi: "wi_s", bundleName: "_shared", capsuleDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not a workflow bundle") {
		t.Fatalf("materializeBundle(_shared) = %v, want a refusal", err)
	}
}
