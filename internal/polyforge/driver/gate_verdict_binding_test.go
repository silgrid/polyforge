package driver

// gate_verdict_binding_test.go: the fix for the polyforge#106
// near-miss — the verdict binding. A workflow-declared gate step's
// business verdict (the output's top-level "verdict" string) and the
// envelope's routing outcome must agree in the fail direction: a
// completed envelope carrying a fail verdict is re-bound to
// gate_failed at envelope acceptance (result.go's rule 3b), so the
// gate's on_fail policy fires and the verdict can never advance a
// workflow. The instruction half lives in the delivery contract
// (spawn.go): gate steps are TOLD the binding in the prompt.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// gateRes builds a minimal valid envelope for one outcome/output pair.
func gateRes(outcome Outcome, output string) *StepResult {
	return &StepResult{
		WIID:    "wi_run",
		StepID:  "review",
		SpawnN:  1,
		Outcome: outcome,
		Output:  json.RawMessage(output),
	}
}

func TestValidateStepResultGateVerdictBinding(t *testing.T) {
	failOutput := `{"verdict":"fail","fail_reason":"the fix suppresses the symptom at one call site","findings":[{"severity":"blocking","detail":"root cause not removed"}]}`
	cases := []struct {
		name       string
		spec       ResultSpec
		res        *StepResult
		wantBound  bool
		wantErr    bool
		wantErrSub string
	}{
		{
			name:      "gate completed fail verdict re-binds to gate_failed",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, failOutput),
			wantBound: true,
		},
		{
			name:      "gate completed prose-decorated FAIL verdict re-binds",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"verdict":"FAIL: tests red"}`),
			wantBound: true,
		},
		{
			name:      "gate completed failed verdict re-binds (prefix match)",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"verdict":"failed"}`),
			wantBound: true,
		},
		{
			name:      "gate completed padded verdict re-binds",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"verdict":"  Fail  "}`),
			wantBound: true,
		},
		{
			name:      "gate completed pass verdict stays completed",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"verdict":"pass"}`),
			wantBound: false,
		},
		{
			name:      "gate completed non-string verdict stays completed",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"verdict":{"result":"fail"}}`),
			wantBound: false,
		},
		{
			name:      "gate completed no verdict stays completed",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeCompleted, `{"summary":"looked fine"}`),
			wantBound: false,
		},
		{
			name:      "non-gate completed fail verdict is NOT re-bound",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1},
			res:       gateRes(OutcomeCompleted, failOutput),
			wantBound: false,
		},
		{
			name:      "gate gate_failed envelope is untouched",
			spec:      ResultSpec{WIID: "wi_run", StepID: "review", SpawnN: 1, Gate: true},
			res:       gateRes(OutcomeGateFailed, `{"verdict":"pass"}`),
			wantBound: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delivered := tc.res.Outcome
			err := ValidateStepResult(tc.res, tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil (outcome now %q)", tc.res.Outcome)
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error %q missing %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateStepResult: %v", err)
			}
			want := delivered
			if tc.wantBound {
				want = OutcomeGateFailed
			}
			if tc.res.Outcome != want {
				t.Fatalf("outcome binding: delivered %q, want %q", delivered, want)
			}
		})
	}
}

// TestRunGateVerdictBindingRewindsAndLogs drives the binding through
// the Run loop: the #106 envelope (completed + verdict:fail) must
// archive the rewind, leave the gate in_progress, hand the verdict to
// the rewind target as its pending_rewind_input — and log the bind.
func TestRunGateVerdictBindingRewindsAndLogs(t *testing.T) {
	verdictJSON := `{"verdict":"fail","fail_reason":"the fix suppresses the symptom at one call site; the cause is still reachable","findings":[{"severity":"blocking","detail":"root cause not removed"}]}`
	d, sp, execPath := runEnv(t, spec("fix"), gateSpec("review", "fix"))
	log := &eventLog{}
	d.log = log
	sp.script = []fakeEntry{
		done(`{"summary":"fix applied"}`),
		fakeEntry{res: &StepResult{
			Outcome: OutcomeCompleted,
			Output:  json.RawMessage(verdictJSON),
		}},
		// The rewind re-dispatches fix (spawn 3) — this entry lets the
		// ledger record it with the rewind input aboard. The loop then
		// re-dispatches the gate itself (resume, INV-1g), which parks
		// (held) until the test cancels: the durable state is the
		// assertion.
		done(`{"summary":"fix round 2"}`),
		{hold: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(sp.dispatched()) < 4 {
		if time.Now().After(deadline) {
			t.Fatalf("the gate re-dispatch never happened: %v", sp.dispatched())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if runErr := <-runDone; runErr == nil || !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run: want the cancellation stop after the rewind re-dispatch, got %v", runErr)
	}

	if got := sp.dispatched(); fmt.Sprint(got) != fmt.Sprint([]string{
		"fix@1(resume=false)", "review@2(resume=false)", "fix@3(resume=false)", "review@4(resume=false)",
	}) {
		t.Fatalf("dispatch ledger: %v", got)
	}
	// The re-dispatched fix consumes the verdict as its rewind input.
	if ri := sp.spawnAt(2).RewindInput; ri == nil || ri.ToStep != "fix" || ri.Reason != verdictJSON {
		t.Fatalf("rewind input on the re-dispatched fix: %+v", ri)
	}

	exec := readExec(t, execPath)
	if st := exec.Steps["review"]; st.Status != capsule.StepInProgress {
		t.Fatalf("the gate must stay in_progress holding its verdict, got %q", st.Status)
	} else if st.Output["verdict"] != "fail" {
		t.Fatalf("the verdict must stay on the gate, got %+v", st.Output)
	}
	if exec.WorkflowStatus != StatusRunning {
		t.Fatalf("the workflow must stay running on a bound gate fail, got %q", exec.WorkflowStatus)
	}
	if exec.RewindStats.Count != 1 || len(exec.RewindStats.ToStep) != 1 || exec.RewindStats.ToStep[0] != "fix" {
		t.Fatalf("rewind stats: %+v", exec.RewindStats)
	}
	fx := exec.Steps["fix"]
	var rewound bool
	for _, at := range fx.Attempts {
		if at.Cause == CauseGateFailRewind && at.RewindGate == "review" {
			rewound = true
		}
	}
	if !rewound {
		t.Fatalf("no gate_fail_rewind attempt on fix: %+v", fx.Attempts)
	}
	// fix's persisted pending_rewind_input is already consumed: the
	// spawn-3 START cleared it in the same write (D8) — its delivery
	// is pinned above via the re-dispatch's RewindInput.
	if exec.ReviewRounds["review"] != 1 {
		t.Fatalf("review rounds: %+v", exec.ReviewRounds)
	}

	// The bind is news: one gate_verdict_bound event, delivered ->
	// bound outcome named for the operator.
	var bound bool
	for _, e := range log.entries {
		if e.kind != EvGateVerdictBound {
			continue
		}
		bound = true
		if e.body["step"] != "review" || e.body["delivered_outcome"] != OutcomeCompleted ||
			e.body["bound_outcome"] != OutcomeGateFailed || e.body["verdict"] != "fail" {
			t.Fatalf("bind event body: %+v", e.body)
		}
	}
	if !bound {
		t.Fatalf("no %s event recorded; log=%+v", EvGateVerdictBound, log.entries)
	}
}
