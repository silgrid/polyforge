package runtime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
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
	if err := r.CheckBundle("wi_s", "proj", "_shared"); err == nil || !strings.Contains(err.Error(), "not a workflow bundle") {
		t.Fatalf("CheckBundle(_shared) = %v, want a refusal", err)
	}
	if err := r.materializeBundle(&job{wi: "wi_s", bundleName: "_shared", capsuleDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "not a workflow bundle") {
		t.Fatalf("materializeBundle(_shared) = %v, want a refusal", err)
	}
}

// treeFiles answers every regular file under each of trees (relative to
// root), keyed by its slash path under root, with its bytes; a path under
// skip (slash, relative to root) is left out.
func treeFiles(t *testing.T, root string, trees []string, skip string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, tree := range trees {
		err := filepath.Walk(filepath.Join(root, tree), func(p string, info os.FileInfo, werr error) error {
			if werr != nil || !info.Mode().IsRegular() {
				return werr
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if skip != "" && strings.HasPrefix(rel, skip+"/") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = raw
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// sameFiles fails unless got holds exactly want's paths, each with
// want's bytes.
func sameFiles(t *testing.T, what string, want, got map[string][]byte) {
	t.Helper()
	var missing, extra, differ []string
	for rel, raw := range want {
		g, ok := got[rel]
		switch {
		case !ok:
			missing = append(missing, rel)
		case !bytes.Equal(g, raw):
			differ = append(differ, rel)
		}
	}
	for rel := range got {
		if _, ok := want[rel]; !ok {
			extra = append(extra, rel)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(differ)
	if len(missing)+len(extra)+len(differ) > 0 {
		t.Fatalf("%s: missing %v, extra %v, bytes differ %v", what, missing, extra, differ)
	}
}

// The claim leaves the shared scripts' own unit tests
// (_shared/scripts/tests/) out of the capsule — nothing at runtime reads
// them — and copies every other file of the lane and the shared tree
// byte for byte: a tests/ directory elsewhere (the shared tree's top, the
// lane's own scripts/) stays, and the marker vouches for exactly the
// files on disk (the copy and each step's rendered definition).
func TestMaterializeLeavesOutSharedScriptsTests(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	for rel, body := range map[string]string{
		"_shared/steps/deliver.md":              "deliver it\n",
		"_shared/scripts/run.py":                "print('run')\n",
		"_shared/scripts/pfstep/__init__.py":    "",
		"_shared/scripts/tests/test_x.py":       "import unittest\n",
		"_shared/scripts/tests/testdata/x.json": "{}\n",
		"_shared/tests/keep.md":                 "kept\n",
		"demo-bundle/scripts/tests/keep.py":     "kept = True\n",
	} {
		p := filepath.Join(wfDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir
	if err := r.Start(context.Background(), "wi_notests", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), "wi_notests", protocol.FinishWrapped) })
	capDir := testCapsuleDir(r, "wi_notests")

	if _, err := os.Stat(filepath.Join(capDir, "workflows", "_shared", "scripts", "tests")); !os.IsNotExist(err) {
		t.Fatalf("workflows/_shared/scripts/tests in the capsule: stat err %v, want not-exist", err)
	}
	want := map[string][]byte{}
	for rel, raw := range treeFiles(t, wfDir, []string{"demo-bundle", "_shared"}, "_shared/scripts/tests") {
		want["workflows/"+rel] = raw
	}
	sameFiles(t, "capsule snapshot vs the workflows dir minus _shared/scripts/tests",
		want, treeFiles(t, capDir, []string{"workflows/demo-bundle", "workflows/_shared"}, ""))
	onDisk := treeFiles(t, capDir, []string{"workflows/demo-bundle", "workflows/_shared", "workflows/_rendered"}, "")
	wantMarked := map[string][]byte{"workflows/_rendered/step.md": onDisk["workflows/_rendered/step.md"]}
	for rel, raw := range want {
		wantMarked[rel] = raw
	}
	marked := map[string][]byte{}
	for _, tgt := range readMarker(t, capDir).Targets {
		marked[tgt] = onDisk[tgt]
	}
	sameFiles(t, "marker targets", wantMarked, marked)
}

// The same against this checkout's own workflows/ (the tree a release
// ships): staging the develop lane writes the lane and the shared tree
// byte for byte, less _shared/scripts/tests/ and nothing else.
func TestStagedLaneFromTheReleaseTreeLeavesOutOnlyScriptsTests(t *testing.T) {
	builtin, err := filepath.Abs(filepath.Join("..", "..", "..", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(builtin, workflow.SharedDir, "scripts", "tests")); err != nil {
		t.Fatalf("the checkout's workflows/_shared/scripts/tests is the fixture this test needs: %v", err)
	}
	st, err := stageLane(workflow.LaneSet{Builtin: builtin}, "develop")
	if err != nil {
		t.Fatalf("stageLane: %v", err)
	}
	out := t.TempDir()
	if err := st.writeTo(out); err != nil {
		t.Fatalf("writeTo: %v", err)
	}
	sameFiles(t, "staged develop lane vs the checkout's workflows/ minus _shared/scripts/tests",
		treeFiles(t, builtin, []string{"develop", workflow.SharedDir}, workflow.SharedDir+"/scripts/tests"),
		treeFiles(t, out, []string{"develop", workflow.SharedDir}, ""))
}
