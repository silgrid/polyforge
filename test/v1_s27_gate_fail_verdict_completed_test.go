package test

// v1_s27: the polyforge#106 near-miss end-to-end. A gate-flagged step
// (the bug-fix lane's review) whose result envelope carries verdict
// "fail" in its OUTPUT JSON but outcome "completed" — the exact shape
// the #106 review step delivered. The driver never reads a verdict
// inside output, so the FAIL verdict advanced the workflow to completion
// instead of firing the on_fail rewind to fix.
//
// This scenario pins the work item's e2e acceptance (wi_7omkUJfGaJIS):
// the rewind MUST fire from review's fail verdict. It FAILS on the
// current code: the workflow completes with zero rewinds, review is
// archived as done with the fail verdict and the lane ends there —
// the near-miss without #106's lucky save (review is the lane's last
// step, so nothing downstream masks the symptom).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// TestV1S27GateFailVerdictInsideCompletedEnvelopeMustRewind runs the
// full bug-fix lane through the real daemon + driver with fakepi step
// agents scripted to deliver envelopes.
func TestV1S27GateFailVerdictInsideCompletedEnvelopeMustRewind(t *testing.T) {
	inst, _ := newV1InstallTaskRepo(t)

	// The bug-fix lane, scripted per spawn ordinal (the Nth envelope
	// action fires for spawn N — across fresh and re-dispatched spawns
	// alike, so the entries after #4 only ever fire when the rewind
	// machinery works; on the buggy driver the lane completes at #4).
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true,
			"files": map[string]string{"artifacts/repro.md": "# repro\n\nfake agent reproduction record\n"}},
		{"do": "envelope", "id": "root-cause", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		// #106's exact shape: the review gate's FAIL verdict (with the
		// fail_reason and blocking finding the review output schema
		// demands) rides the output JSON of a COMPLETED envelope.
		// (review is the first gate behind fix, so the shape
		// fires at spawn 4; the entries after it only ever run when the
		// rewind machinery works.)
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "the fix suppresses the symptom at one call site; the cause is still reachable",
			"findings":    []map[string]any{{"severity": "blocking", "detail": "root cause not removed"}},
		}},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}

	wi := newWIWithBundle(t, inst, "s27: review fail verdict rides a completed envelope (#106 shape)", "bug-fix", plan)
	d := newV1Daemon(t, inst)
	d.start()

	out := d.pf(t, "submit", wi)
	if !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}

	capDir := d.capsuleDir(wi)
	execPath := filepath.Join(capDir, "runtime-state", "execution.json")

	// Terminal status OR a fired rewind, whichever comes first: the
	// rewind is the expected outcome; a terminal status on this lane
	// before any rewind IS the symptom.
	waitUntil(t, 120*time.Second, "driver never reached a terminal status or fired a rewind", func() bool {
		x := loadExecRaw(t, execPath)
		if x == nil {
			return false
		}
		if x.WorkflowStatus != "running" && x.WorkflowStatus != "blocked" {
			return true
		}
		return x.RewindStats.Count >= 1
	})

	exec := loadExecRaw(t, execPath)
	if exec == nil {
		t.Fatalf("no execution.json at %s", execPath)
	}

	// The pin: the rewind fired FROM review's fail verdict.
	if exec.RewindStats.Count < 1 {
		t.Fatalf("SYMPTOM (#106): review's verdict:fail never fired the on_fail rewind — "+
			"workflow_status=%q, rewind_stats=%+v, review status=%q output=%s; the lane advanced "+
			"past a FAIL gate on the strength of a completed outcome",
			exec.WorkflowStatus, exec.RewindStats, stepStatus(exec, "review"), stepOutput(exec, "review"))
	}
	// The rewind must be review's (rewind_gate=review archived on fix).
	var reviewRewind bool
	if fx, ok := exec.Steps["fix"]; ok {
		for _, att := range fx.Attempts {
			if att["cause"] == "gate_fail_rewind" && att["rewind_gate"] == "review" {
				reviewRewind = true
			}
		}
	}
	if !reviewRewind {
		t.Fatalf("SYMPTOM (#106): no gate_fail_rewind attempt on fix with rewind_gate=review — "+
			"rewind_stats=%+v, fix attempts=%s", exec.RewindStats, stepAttempts(exec, "fix"))
	}
}

// execSnapshot is the execution.json shape this scenario inspects.
type execSnapshot struct {
	WorkflowStatus string `json:"workflow_status"`
	RewindStats    struct {
		Count  int64    `json:"count"`
		ToStep []string `json:"to_step"`
	} `json:"rewind_stats"`
	Steps map[string]struct {
		Status   string                   `json:"status"`
		Output   map[string]any           `json:"output"`
		Attempts []map[string]interface{} `json:"attempts"`
	} `json:"steps"`
}

func loadExecRaw(t *testing.T, path string) *execSnapshot {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var x execSnapshot
	if err := json.Unmarshal(raw, &x); err != nil {
		t.Fatalf("execution.json unparsable: %v", err)
	}
	return &x
}

func stepStatus(x *execSnapshot, id string) string {
	if st, ok := x.Steps[id]; ok {
		return st.Status
	}
	return "<missing>"
}

func stepOutput(x *execSnapshot, id string) string {
	if st, ok := x.Steps[id]; ok {
		if blob, err := json.Marshal(st.Output); err == nil {
			return string(blob)
		}
	}
	return "<none>"
}

func stepAttempts(x *execSnapshot, id string) string {
	if st, ok := x.Steps[id]; ok {
		if blob, err := json.Marshal(st.Attempts); err == nil {
			return string(blob)
		}
	}
	return "<none>"
}

// newWIWithBundle files one execution ticket on an arbitrary bundle
// (newWI hardcodes autopilot) and scripts its agent with the plan.
func newWIWithBundle(t *testing.T, inst *v1Install, goal, bundle string, actions []map[string]any) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    v1Project,
		Goal:       goal,
		BundleName: strPtr(bundle),
		Attrs:      nil,
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	inst.setPlanFull(t, wi.ID, actions, nil)
	inst.trackWI(wi.ID)
	return wi.ID
}
