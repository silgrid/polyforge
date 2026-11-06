package runtime

// w521_skip_freeze_test.go ( u3): the steps a claim skips
// are fixed in the capsule when the work item is first claimed —
// runtime-state/skipped-steps.json, the steps the lane snapshot declares
// skippable that the project's workspace yaml switches off. A re-claim
// materializes the same capsule chain again and keeps the file as it is,
// so a switch flipped while the work item runs does not reach it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w521SkippedStepsRel is the capsule-relative path of the claim's
// skipped-step set.
const w521SkippedStepsRel = "runtime-state/skipped-steps.json"

// w521Claim materializes wi's capsule the way a claim does (a re-claim
// materializes the same capsule dir, which its chain clone carries).
func w521Claim(t *testing.T, r *Runtime, wi string) string {
	t.Helper()
	j := &job{
		wi:         wi,
		project:    "proj",
		bundleName: "demo-skip",
		capsuleDir: testCapsuleDir(r, wi),
		item:       &api.WorkItem{ID: wi, ProjectName: "proj", Goal: "g"},
	}
	if err := r.materialize(context.Background(), j); err != nil {
		t.Fatalf("materialize %s: %v", wi, err)
	}
	return j.capsuleDir
}

// w521Skipped reads the capsule's skipped-step set (the file must exist).
func w521Skipped(t *testing.T, capDir string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(w521SkippedStepsRel)))
	if err != nil {
		t.Fatalf("%s: %v", w521SkippedStepsRel, err)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil || got == nil {
		t.Fatalf("%s is not a JSON list: %v (%q)", w521SkippedStepsRel, err, raw)
	}
	return got
}

func TestW521SkipFrozenAtFirstClaim(t *testing.T) {
	r, ws, _ := newWorkspaceTestRuntime(t, newFakeHub(), "proj")
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "demo-skip",
		"steps:\n  - { id: a }\n  - { id: b, skippable: true }\n  - { id: c }\n")
	r.cfg.WorkflowsDir = lanes

	// The project switches off b (declared skippable) and a (not
	// declared): only b is fixed in the capsule.
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    a: { skip: true }\n    b: { skip: true }\n")
	capOn := w521Claim(t, r, "wi_w521on")
	if got := w521Skipped(t, capOn); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("first claim with b switched off: skipped = %v, want [b]", got)
	}
	// The switch goes back on; the re-claim keeps the claim's set.
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    b: { skip: false }\n")
	w521Claim(t, r, "wi_w521on")
	if got := w521Skipped(t, capOn); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("a re-claim after the switch changed: skipped = %v, want [b] (kept)", got)
	}

	// Reverse: nothing switched off at the first claim is an empty list,
	// and switching b off afterwards does not reach the claimed capsule.
	capOff := w521Claim(t, r, "wi_w521off")
	if got := w521Skipped(t, capOff); len(got) != 0 {
		t.Fatalf("first claim with nothing switched off: skipped = %v, want []", got)
	}
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    b: { skip: true }\n")
	w521Claim(t, r, "wi_w521off")
	if got := w521Skipped(t, capOff); len(got) != 0 {
		t.Fatalf("a re-claim after b was switched off: skipped = %v, want [] (kept)", got)
	}
}
