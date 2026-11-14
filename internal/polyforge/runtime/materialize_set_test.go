package runtime

// materialize_set_test.go: the F7 lane — the bundle copy with its
// completion marker: the full materialization shape (workflows/<bundle>
// + marker targets), the recovery trust gate (marker present, its
// bundle, every target on disk) and a rebuild over a torn snapshot.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// startedWithBundle runs one full Start against the pristine demo bundle
// and answers the capsule dir + job of the live run.
func startedWithBundle(t *testing.T, wi string) (*Runtime, string, *job) {
	t.Helper()
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Stop(context.Background(), wi, protocol.FinishWrapped)
	})
	j := r.jobOf(wi)
	if j == nil {
		t.Fatalf("no live job for %s", wi)
	}
	return r, testCapsuleDir(r, wi), j
}

func readMarker(t *testing.T, capDir string) materializeMarkerRecord {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(RelMaterializeMarker)))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var m materializeMarkerRecord
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("marker unparsable: %v", err)
	}
	return m
}

// The full materialization: workflows/<bundle>/ tree (workflow.yaml, its
// README.md, its fragments/ and the step definitions; no second agents/
// copy), each step's rendered definition under workflows/_rendered/, and
// a marker whose targets list covers every file on disk.
func TestMaterializeSetCompleteShape(t *testing.T) {
	_, capDir, _ := startedWithBundle(t, "wi_f7a")

	for _, rel := range []string{
		"workflows/demo-bundle/workflow.yaml",
		"workflows/demo-bundle/README.md",
		"workflows/demo-bundle/fragments/note.md",
		"workflows/demo-bundle/steps/step.md",
		"workflows/_rendered/step.md",
	} {
		if _, err := os.Stat(filepath.Join(capDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("materialized target missing: %s: %v", rel, err)
		}
	}
	// No harness discovery copy any more.
	if _, err := os.Stat(filepath.Join(capDir, ".pi", "agents")); !os.IsNotExist(err) {
		t.Fatalf(".pi/agents discovery copy must not be materialized: %v", err)
	}
	// No second copy of the step definitions.
	if _, err := os.Stat(filepath.Join(capDir, "agents")); !os.IsNotExist(err) {
		t.Fatalf("agents/ step-definition copy must not be materialized: %v", err)
	}
	m := readMarker(t, capDir)
	if m.BundleName != "demo-bundle" {
		t.Fatalf("marker identity = %+v", m)
	}
	// workflow contract (AC7): the marker records the
	// snapshot's contract version, its source and its content hash.
	hash, err := workflow.ContentHash(capDir, m.Targets)
	if err != nil {
		t.Fatal(err)
	}
	if m.Contract != workflow.ContractVersion || m.Source != workflow.SourceBuiltin || m.ContentHash != hash || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("marker contract/source/content_hash = %d %q %q, want %d %q %q", m.Contract, m.Source, m.ContentHash, workflow.ContractVersion, workflow.SourceBuiltin, hash)
	}
	want := map[string]bool{
		"workflows/demo-bundle/workflow.yaml":     false,
		"workflows/demo-bundle/README.md":         false,
		"workflows/demo-bundle/fragments/note.md": false,
		"workflows/demo-bundle/steps/step.md":     false,
		"workflows/_rendered/step.md":             false,
	}
	if len(m.Targets) != len(want) {
		t.Fatalf("marker targets = %v", m.Targets)
	}
	for _, rel := range m.Targets {
		if _, ok := want[rel]; !ok {
			t.Fatalf("unexpected marker target %q", rel)
		}
		want[rel] = true
		if _, err := os.Stat(filepath.Join(capDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("marker target not on disk: %s: %v", rel, err)
		}
	}
	for rel, seen := range want {
		if !seen {
			t.Fatalf("marker target missing from the list: %s", rel)
		}
	}
	// sync/ is daemon-private: the capsule .gitignore excludes it.
	gi, err := os.ReadFile(filepath.Join(capDir, ".gitignore"))
	if err != nil || !containsLine(string(gi), "/sync/") {
		t.Fatalf(".gitignore must exclude /sync/: %v (%q)", err, gi)
	}
}

func containsLine(s, line string) bool {
	for _, l := range splitLines(s) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// A torn snapshot (the marker removed, a target gone) fails the gate
// and a materialization rebuilds it and rewrites the marker; a target
// lost after the marker (the gate's missing-file hole) fails the gate
// too.
func TestMaterializeSetTornSnapshotRebuilt(t *testing.T) {
	r, capDir, j := startedWithBundle(t, "wi_f7c")

	// (a) torn: marker gone, one staged copy vandalized.
	os.Remove(filepath.Join(capDir, filepath.FromSlash(RelMaterializeMarker)))
	os.Remove(filepath.Join(capDir, "workflows", "demo-bundle", workflow.WorkflowFile))

	if err := r.materializeBundle(j); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(capDir, "workflows", "demo-bundle", workflow.WorkflowFile)); err != nil {
		t.Fatalf("torn snapshot not rebuilt: %v", err)
	}
	m := readMarker(t, capDir)
	if m.BundleName != "demo-bundle" || len(m.Targets) == 0 {
		t.Fatalf("marker not rewritten after the rebuild: %+v", m)
	}
	if !r.materializationComplete(j) {
		t.Fatal("gate must validate the rebuilt snapshot")
	}

	// (b) missing target with the marker intact: the gate fails on the
	// missing file and the rebuild restores it.
	os.Remove(filepath.Join(capDir, "workflows", "demo-bundle", "steps", "step.md"))
	if r.materializationComplete(j) {
		t.Fatal("gate validated a snapshot with a missing target")
	}
	if err := r.materializeBundle(j); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(capDir, "workflows", "demo-bundle", "steps", "step.md")); err != nil {
		t.Fatalf("missing target not restored: %v", err)
	}
	if !r.materializationComplete(j) {
		t.Fatal("gate must validate the rebuilt snapshot (missing-target case)")
	}
}

// A capsule recovered from an older daemon passes the recovery gate: its
// marker still carries the retired "adapters" key and lists the retired
// agents/ copy as a target, and the gate validates it (identity +
// targets on disk). A marker for another bundle fails it.
func TestMaterializeSetAdoptsPreG4Capsule(t *testing.T) {
	r, capDir, j := startedWithBundle(t, "wi_f7d")

	if err := os.MkdirAll(filepath.Join(capDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, "agents", "step.md"), []byte("old copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(capDir, filepath.FromSlash(RelMaterializeMarker))
	os.Remove(markerPath)
	old, _ := json.Marshal(map[string]any{
		"bundle_name":  "demo-bundle",
		"adapters":     []string{"pi"},
		"targets":      []string{"agents/step.md", "workflows/demo-bundle/workflow.yaml"},
		"completed_at": "2026-10-01T00:00:00Z",
	})
	if err := os.WriteFile(markerPath, old, 0o444); err != nil {
		t.Fatal(err)
	}
	if !r.materializationComplete(j) {
		t.Fatal("a pre-G4 marker with its targets on disk must validate")
	}

	// A marker for a different bundle fails the gate; a materialization
	// rewrites it for the job's bundle.
	os.Remove(markerPath)
	other, _ := json.Marshal(map[string]any{
		"bundle_name": "other-bundle", "targets": []string{"agents/step.md"},
	})
	if err := os.WriteFile(markerPath, other, 0o444); err != nil {
		t.Fatal(err)
	}
	if r.materializationComplete(j) {
		t.Fatal("a marker for another bundle must not validate")
	}
	if err := r.materializeBundle(j); err != nil {
		t.Fatal(err)
	}
	if m := readMarker(t, capDir); m.BundleName != "demo-bundle" || !r.materializationComplete(j) {
		t.Fatalf("foreign marker not rebuilt: %+v", m)
	}
}

// A lane without steps/ of its own (its one step's definition the shared
// one) still gets its workflows/<bundle> tree copied and a valid
// completion marker (the latent bug: the workflow tree copy used to hang
// off the presence of a step directory).
func TestMaterializeBundleWithoutStepsStillCopiesWorkflowTree(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	// the lane's one step falls back on the shared definition: its own
	// directory holds the workflow.yaml alone (workflow contract 3 has no
	// daemon step, the steps-less shape of contract 1/2)
	if err := os.MkdirAll(filepath.Join(dir, workflow.SharedDir, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, workflow.SharedDir, "steps", "close.md"), []byte(workflowtest.Definition("close")), 0o644); err != nil {
		t.Fatal(err)
	}
	workflowtest.WriteLane(t, dir, "demo-bundle", "steps:\n  - { id: close }\n")
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = dir
	if err := r.Start(context.Background(), "wi_f7nos", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), "wi_f7nos", protocol.FinishWrapped) })
	capDir := testCapsuleDir(r, "wi_f7nos")
	if _, err := os.Stat(filepath.Join(capDir, "workflows", "demo-bundle", workflow.WorkflowFile)); err != nil {
		t.Fatalf("workflow tree not materialized: %v", err)
	}
	m := readMarker(t, capDir)
	if len(m.Targets) != 3 || m.Targets[0] != "workflows/_rendered/close.md" || m.Targets[1] != "workflows/_shared/steps/close.md" || m.Targets[2] != "workflows/demo-bundle/workflow.yaml" {
		t.Fatalf("marker targets = %v", m.Targets)
	}
	if !r.materializationComplete(r.jobOf("wi_f7nos")) {
		t.Fatal("gate must validate a steps-less bundle's snapshot")
	}
}

// Recovery never re-materializes a moved source: a workflows-dir source
// that evolved after the claim leaves the capsule snapshot untouched
// (the relaunch uses the capsule's own copy), and a missing snapshot is
// a blocked finish, not a silent rebuild.
func TestRecoveryUsesClaimSnapshotNeverRepins(t *testing.T) {
	r, capDir, j := startedWithBundle(t, "wi_norepin")
	src := filepath.Join(r.cfg.WorkflowsDir, "demo-bundle", workflow.WorkflowFile)
	if err := os.WriteFile(src, []byte("# evolved after claim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	got, _ := os.ReadFile(filepath.Join(capDir, "workflows", "demo-bundle", workflow.WorkflowFile))
	if string(got) == "# evolved after claim\n" {
		t.Fatal("recovery re-materialized from the moved source")
	}

	// A missing snapshot is not rebuilt: the WI blocks.
	if err := os.Remove(filepath.Join(capDir, filepath.FromSlash(RelMaterializeMarker))); err != nil {
		t.Fatal(err)
	}
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if _, err := os.Stat(filepath.Join(capDir, filepath.FromSlash(RelMaterializeMarker))); err == nil {
		t.Fatal("recovery rebuilt the snapshot instead of blocking")
	}
	if st, _ := j.snapshot(); st == StateRunning || st == StateRecovering {
		t.Fatalf("a missing snapshot must end the run, state %s", st)
	}
}
