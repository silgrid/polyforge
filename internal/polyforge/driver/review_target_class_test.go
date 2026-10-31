package driver

// review_target_class_test.go: every blocking review finding carries a
// machine-readable repair_target class. A blocking finding with no class
// (or an unknown one) is an unfilled review result — an envelope
// rejection — and a code-class finding is never off-graph.

import (
	"encoding/json"
	"strings"
	"testing"
)

func repairTargetRes(outcome Outcome, findings string) *StepResult {
	return &StepResult{
		WIID: "wi_rt", StepID: "review", SpawnN: 1, Outcome: outcome,
		Output: json.RawMessage(`{"verdict":"fail","fail_reason":"r","findings":` + findings + `}`),
	}
}

func TestReviewBlockingFindingWithoutClassIsUnfilled(t *testing.T) {
	spec := ResultSpec{WIID: "wi_rt", StepID: "review", SpawnN: 1, Gate: true}
	for _, c := range []struct {
		name     string
		outcome  Outcome
		findings string
	}{
		{"completed fail, no class", OutcomeCompleted, `[{"severity":"blocking","detail":"d"}]`},
		{"gate_failed, no class", OutcomeGateFailed, `[{"severity":"blocking","detail":"d"}]`},
		{"unknown class", OutcomeGateFailed, `[{"severity":"blocking","detail":"d","repair_target":"elsewhere"}]`},
		{"second finding unclassified", OutcomeGateFailed, `[{"severity":"blocking","detail":"a","repair_target":"code"},{"severity":"blocking","detail":"b"}]`},
	} {
		err := ValidateStepResult(repairTargetRes(c.outcome, c.findings), spec)
		if err == nil || !strings.Contains(err.Error(), "repair_target") {
			t.Errorf("%s: want an unfilled-result rejection naming repair_target, got %v", c.name, err)
		}
	}
}

func TestReviewEveryClassIsAccepted(t *testing.T) {
	spec := ResultSpec{WIID: "wi_rt", StepID: "review", SpawnN: 1, Gate: true}
	for _, class := range []string{"code", "authored-suite", "spec", "declared-obligation", "instruction"} {
		res := repairTargetRes(OutcomeCompleted, `[{"severity":"blocking","detail":"d","repair_target":"`+class+`"},{"severity":"non-blocking","detail":"nit"}]`)
		if err := ValidateStepResult(res, spec); err != nil {
			t.Errorf("class %q: a classified blocking finding must be accepted, got %v", class, err)
		}
	}
}

func TestReviewCodeFindingIsNeverOffGraph(t *testing.T) {
	parse := func(s string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	cases := []struct {
		findings string
		want     bool
	}{
		{`[{"severity":"blocking","repair_target":"code"}]`, false},
		{`[{"severity":"blocking","repair_target":"spec"},{"severity":"blocking","repair_target":"code"}]`, false},
		{`[{"severity":"blocking"}]`, false},
		{`[{"severity":"non-blocking","repair_target":"spec"}]`, false},
		{`[{"severity":"blocking","repair_target":"spec"},{"severity":"blocking","repair_target":"authored-suite"},{"severity":"non-blocking","repair_target":"code"}]`, true},
		{`[{"severity":"blocking","repair_target":"declared-obligation"},{"severity":"blocking","repair_target":"instruction"}]`, true},
	}
	for _, c := range cases {
		if got := reviewAllOffGraph(parse(`{"findings":` + c.findings + `}`)); got != c.want {
			t.Errorf("reviewAllOffGraph(%s) = %v, want %v", c.findings, got, c.want)
		}
	}
	// a mixed round's convergence count holds the code findings only
	blocking, _, _ := reviewRoundFacts(parse(`{"findings":[{"severity":"blocking","repair_target":"spec"},{"severity":"blocking","repair_target":"code"}]}`))
	if blocking != 1 {
		t.Errorf("the convergence count of a mixed round = %d, want 1 (off-graph findings ride along uncounted)", blocking)
	}
}
