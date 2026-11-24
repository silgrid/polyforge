package runtime

// stepoverrides_future_dispatch_test.go (polyforge#270 u2): a running
// step's models/tier overlay is accepted for its NEXT dispatch — the write
// touches the capsule's overlay record only, never the live dispatch's
// execution state — while the skip switch keeps its old "a step that has
// started refuses it" rule (the owner's Q2 B).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// writeLiveExecution plants the execution record of a running job whose
// current step (fix) is mid-dispatch on candidate pi:prov/old.
func writeLiveExecution(t *testing.T, capDir, wi string) {
	t.Helper()
	ex := capsule.Execution{
		WorkItemID:     wi,
		WorkflowStatus: capsule.WorkflowStatusRunning,
		CurrentStep:    "fix",
		Steps: map[string]*capsule.Step{
			"grill":   {Status: capsule.StepDone, Round: 1},
			"fix":     {Status: capsule.StepInProgress, Round: 1, Candidate: "pi:prov/old"},
			"release": {Status: capsule.StepPending},
		},
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOverrideLiveStepNextDispatch: the live step's model chain is written
// and published while its dispatch keeps running; the execution record's
// candidate and round are untouched, and the skip switch is still refused.
func TestOverrideLiveStepNextDispatch(t *testing.T) {
	r, _ := pendingEnv(t)
	const wi = "wi_ov270"
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	writeLiveExecution(t, j.capsuleDir, wi)

	out, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
		Step: "fix", Operator: "usr_270", Models: []string{"claude:strong"},
	})
	if err != nil {
		t.Fatalf("the live step's model write must be accepted: %v", err)
	}
	if !out.Changed || out.Posture != OverridePostureRunning {
		t.Fatalf("the live write's outcome: %+v", out)
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatal(err)
	}
	e := ov.Steps["fix"]
	if e == nil || len(e.Models) != 1 || e.Models[0] != "claude:strong" || e.ModelsBy != "usr_270" || e.ModelsAt == "" {
		t.Fatalf("the live step's overlay after the write: %+v", e)
	}
	ex := capsuleExecution(j.capsuleDir)
	if st := ex.Steps["fix"]; st == nil || st.Status != capsule.StepInProgress || st.Candidate != "pi:prov/old" || st.Round != 1 {
		t.Fatalf("the live dispatch's execution state changed: %+v", st)
	}
	// the skip switch keeps the old rule: a step that has started refuses it
	yes := true
	if _, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
		Step: "fix", Operator: "usr_270", Skip: &yes,
	}); err == nil {
		t.Fatal("the live step accepted the skip switch")
	} else if ae, ok := err.(*api.Error); !ok || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("the skip refusal must be DAEMON-409: %v", err)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
