package driver

// verification_attempt_ledger_test.go: a gate dispatch whose FAIL verdict
// rewound the workflow keeps its own audit-only record in the gate's
// attempts[] (cause gate_fail_verdict: the verdict output and the window
// it consumed), while the gate stays in_progress and every rewind-ledger
// fact (the target's gate_fail_rewind archive, the rewind stats, the
// gate's last-verdict time) reads exactly as before.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestRewoundGateDispatchKeepsItsOwnAttempt(t *testing.T) {
	for _, gate := range []string{verificationStepID, reviewStepID} {
		d, _, _, execPath := testEnv(t, spec("code"), gateSpec(gate, "code"))
		mustInit(t, d)
		mustStart(t, d, "code", 1)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 1,
			Result: completedResult("code", 1, `{"ok":true}`)}); err != nil {
			t.Fatalf("%s: code result: %v", gate, err)
		}
		mustStart(t, d, gate, 2)
		verdict := `{"verdict":"fail","fail_reason":"tests red"}`
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: gate, SpawnN: 2, WallMs: 7000,
			Result: &StepResult{WIID: "wi_t1", StepID: gate, SpawnN: 2, Outcome: OutcomeGateFailed,
				Output: json.RawMessage(verdict)}}); err != nil {
			t.Fatalf("%s: gate_fail: %v", gate, err)
		}
		exec := readExec(t, execPath)
		gst := exec.Steps[gate]
		if gst.Status != capsule.StepInProgress || len(gst.Attempts) != 1 {
			t.Fatalf("%s: the gate stays in_progress with one audit record: %+v", gate, gst)
		}
		att := gst.Attempts[0]
		if att.Cause != CauseGateFailVerdict || att.RewindGate != "" || att.AttemptSeq != 1 ||
			att.StartedAt != gst.StepStartedAt || att.ArchivedAt == "" {
			t.Fatalf("%s: the rewound dispatch's record: %+v", gate, att)
		}
		var got, want map[string]any
		_ = json.Unmarshal(att.Result, &got)
		_ = json.Unmarshal([]byte(verdict), &want)
		if got["fail_reason"] != want["fail_reason"] || got["verdict"] != want["verdict"] {
			t.Fatalf("%s: the record must carry the verdict output: %s", gate, att.Result)
		}
		if att.ConsumedBudget == nil || att.ConsumedBudget.WallMs != 7000 || gst.BudgetConsumed.WallMs != 0 {
			t.Fatalf("%s: the record keeps the consumed window (%+v) the gate's reset cleared (%+v)", gate, att.ConsumedBudget, gst.BudgetConsumed)
		}
		// the rewind ledger is unchanged
		cst := exec.Steps["code"]
		tgt := cst.Attempts[len(cst.Attempts)-1]
		if tgt.Cause != CauseGateFailRewind || tgt.RewindGate != gate || exec.RewindStats.Count != 1 {
			t.Fatalf("%s: target archive %+v, stats %+v", gate, tgt, exec.RewindStats)
		}
		d.mu.Lock()
		at, ok := d.gateLastVerdictAtLocked(gate)
		d.mu.Unlock()
		archived, _ := time.Parse(time.RFC3339Nano, tgt.ArchivedAt)
		if !ok || !at.Equal(archived) {
			t.Fatalf("%s: the gate's last verdict time = %v (%v), want the target archive's %v", gate, at, ok, archived)
		}
	}
}
