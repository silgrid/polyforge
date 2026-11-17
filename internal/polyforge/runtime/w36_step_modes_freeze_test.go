package runtime

// w36_step_modes_freeze_test.go (u3): the work item's step modes — the
// steps its project declares `by: human` in its workspace yaml — are
// fixed in the capsule when the work item is first claimed
// (capsule.StepModesJSON), exactly as the skipped-step set is: a re-claim
// keeps the file the capsule chain carries, so a declaration added or
// removed while the work item runs never reaches it. A claim of a lane
// that cannot honor a declared by — a step it does not carry, or one it
// does not declare human_mergeable — is refused naming it (micro never
// accepts the mode; the yaml is the project's, the lane's acceptance is
// the claim's to judge).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w36StepModesRel is the capsule-relative path of the claim's step modes.
const w36StepModesRel = "runtime-state/step-modes.json"

// w36Claim materializes wi's capsule the way a claim does, on the named
// bundle, and answers the capsule dir (or the error the claim refuses
// with).
func w36Claim(t *testing.T, r *Runtime, wi, bundle string) (string, error) {
	t.Helper()
	j := &job{
		wi:         wi,
		project:    "proj",
		bundleName: bundle,
		capsuleDir: testCapsuleDir(r, wi),
		item:       &api.WorkItem{ID: wi, ProjectName: "proj", Goal: "g"},
	}
	err := r.materialize(context.Background(), j)
	return j.capsuleDir, err
}

func TestW36StepModesFrozenAtFirstClaim(t *testing.T) {
	r, ws, _ := newWorkspaceTestRuntime(t, newFakeHub(), "proj")
	lanes := t.TempDir()
	workflowtest.WriteLane(t, lanes, "demo-human",
		"steps:\n  - { id: a }\n  - { id: b, human_mergeable: true }\n")
	r.cfg.WorkflowsDir = lanes

	// the declared by freezes as the step's mode
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    b: { by: human }\n")
	capOn, err := w36Claim(t, r, "wi_w36on", "demo-human")
	if err != nil {
		t.Fatalf("a claim of the lane that declares b human_mergeable: %v", err)
	}
	if got := w36Modes(t, capOn); !reflect.DeepEqual(got, map[string]string{"b": "human"}) {
		t.Fatalf("first claim with b declared by human: modes = %v, want {b: human}", got)
	}
	// the declaration goes away; the re-claim keeps the claim's modes
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    default: { models: [pi:a/one] }\n")
	if _, err := w36Claim(t, r, "wi_w36on", "demo-human"); err != nil {
		t.Fatalf("the re-claim: %v", err)
	}
	if got := w36Modes(t, capOn); !reflect.DeepEqual(got, map[string]string{"b": "human"}) {
		t.Fatalf("a re-claim after the declaration changed: modes = %v, want {b: human} (kept)", got)
	}

	// nothing declared is the empty object, fixed the same way
	capOff, err := w36Claim(t, r, "wi_w36off", "demo-human")
	if err != nil {
		t.Fatal(err)
	}
	if got := w36Modes(t, capOff); len(got) != 0 {
		t.Fatalf("first claim with nothing declared: modes = %v, want {}", got)
	}
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    b: { by: human }\n")
	if _, err := w36Claim(t, r, "wi_w36off", "demo-human"); err != nil {
		t.Fatal(err)
	}
	if got := w36Modes(t, capOff); len(got) != 0 {
		t.Fatalf("a re-claim after b was declared: modes = %v, want {} (kept)", got)
	}

	// a claim of a lane that cannot honor a declared by is refused
	// naming it — a step it does not declare human_mergeable, and a step
	// it does not carry at all
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    a: { by: human }\n")
	if _, err := w36Claim(t, r, "wi_w36refused", "demo-human"); err == nil ||
		!strings.Contains(err.Error(), "steps.proj.a.by") || !strings.Contains(err.Error(), "human_mergeable") {
		t.Fatalf("a by on a step the lane does not declare human_mergeable refuses the claim naming it: %v", err)
	}
	workflowtest.WriteLane(t, lanes, "demo-bare", "steps:\n  - { id: a }\n  - { id: b }\n")
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    b: { by: human }\n")
	if _, err := w36Claim(t, r, "wi_w36bare", "demo-bare"); err == nil ||
		!strings.Contains(err.Error(), "steps.proj.b.by") || !strings.Contains(err.Error(), "demo-bare") {
		t.Fatalf("a by on a step of a lane that declares no human_mergeable refuses the claim naming the lane: %v", err)
	}

	// a by on a step the lane does not carry never refuses the claim (a
	// skip's rule, polyforge#36 r1#2) — and since polyforge#98 a lane's
	// step may read another lane's step mode (the integration lane's
	// full gate routes the batch repair on the project's merge mode), the
	// declaration rides into the frozen modes unchanged: the claim
	// succeeds and the file carries it, the refusal still only for a
	// step the lane carries
	workflowtest.WriteLane(t, lanes, "demo-pool", "steps:\n  - { id: collect }\n  - { id: close }\n")
	capPool, err := w36Claim(t, r, "wi_w36pool", "demo-pool")
	if err != nil {
		t.Fatalf("a by on a step the lane does not carry must not refuse the claim: %v", err)
	}
	if got := w36Modes(t, capPool); !reflect.DeepEqual(got, map[string]string{"merge": "human"}) {
		t.Fatalf("a by naming a step the lane does not carry rides into the frozen modes as declared: modes = %v, want {merge: human}", got)
	}
}

// w36Modes reads the capsule's step modes (the file must exist and be a
// JSON object).
func w36Modes(t *testing.T, capDir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, filepath.FromSlash(w36StepModesRel)))
	if err != nil {
		t.Fatalf("%s: %v (every claimed capsule of a lane carries the file)", w36StepModesRel, err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil || got == nil {
		t.Fatalf("%s is not a JSON object: %v (%q)", w36StepModesRel, err, raw)
	}
	return got
}
