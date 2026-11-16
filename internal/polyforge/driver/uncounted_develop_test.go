package driver

// uncounted_develop_test.go (polyforge#67): the shipped develop lane
// declares the uncounted class — the default-branch-moved merge FAIL its
// merge step reports, on its own cap — and the lanes with no such
// FAIL class (micro: its evidence has no base, so its merge never
// reports a moved default) declare none.

import (
	"path/filepath"
	"testing"
)

// TestDevelopLaneDeclaresTheUncountedClass: the develop lane declares
// `uncounted_rewinds` with the stable prefix of merge.py's
// default-branch-moved summary and its own cap; micro declares none.
func TestDevelopLaneDeclaresTheUncountedClass(t *testing.T) {
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "develop"))
	if err != nil {
		t.Fatalf("develop: %v", err)
	}
	c := wf.UncountedRewinds
	if c == nil {
		t.Fatal("develop declares no uncounted_rewinds class")
	}
	if c.ReasonContains != "the default branch moved since verification" {
		t.Fatalf("develop's declared reason: %q, want merge.py's default-branch-moved prefix", c.ReasonContains)
	}
	if c.Max != 2 {
		t.Fatalf("develop's declared cap: %d, want 2", c.Max)
	}
	wf, err = LoadWorkflow(filepath.Join(shippedWorkflowsDir, "micro"))
	if err != nil {
		t.Fatalf("micro: %v", err)
	}
	if wf.UncountedRewinds != nil {
		t.Fatalf("micro declares no uncounted class: %+v", wf.UncountedRewinds)
	}
}
