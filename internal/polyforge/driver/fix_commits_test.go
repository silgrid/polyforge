package driver

// fix_commits_test.go: a completed fix may report an empty commits list
// only with root_cause_addressed the literal false; a true fix with no
// commit is an invalid completion.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixRes(out string) *StepResult {
	return &StepResult{WIID: "wi_fx", StepID: fixStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(out)}
}

func TestFixEmptyCommitsOnlyWhenRootCauseNotAddressed(t *testing.T) {
	spec := ResultSpec{WIID: "wi_fx", StepID: fixStepID, SpawnN: 1}
	for _, c := range []struct {
		out    string
		reject bool
	}{
		{`{"commits":[],"build_status":"green","root_cause_addressed":true,"regression_test":"t"}`, true},
		{`{"build_status":"green","root_cause_addressed":true,"regression_test":"t"}`, true},
		{`{"commits":[],"build_status":"green","root_cause_addressed":"false","regression_test":"t"}`, true},
		{`{"commits":[],"build_status":"green","root_cause_addressed":false,"regression_test":"t","summary":"the diagnosis did not hold"}`, false},
		{`{"commits":[{"sha":"a","message":"m"}],"build_status":"green","root_cause_addressed":true,"regression_test":"t"}`, false},
		{`{"commits":[],"build_status":"green"}`, false}, // a fix schema without the field is not judged here
	} {
		err := ValidateStepResult(fixRes(c.out), spec)
		if c.reject != (err != nil && strings.Contains(err.Error(), "root_cause_addressed")) {
			t.Errorf("%s: reject=%v, got %v", c.out, c.reject, err)
		}
	}
}

func TestFixFalseEmptyCommitsTakesTheFirstFalseRewind(t *testing.T) {
	d, execPath := bugfixLane(t)
	mustStart(t, d, reproStepID, 1)
	resultAt(t, d, reproStepID, 1, OutcomeCompleted, `{"reproduces":true}`, nil)
	mustStart(t, d, rootCauseStepID, 2)
	resultAt(t, d, rootCauseStepID, 2, OutcomeCompleted, `{"verdict":"pass"}`, nil)
	mustStart(t, d, fixStepID, 3)
	resultAt(t, d, fixStepID, 3, OutcomeCompleted, `{"commits":[],"root_cause_addressed":false,"summary":"the cause is elsewhere"}`, nil)
	x := readExec(t, execPath)
	if x.CurrentStep != rootCauseStepID || x.Steps[rootCauseStepID].PendingRewindInput == nil ||
		!strings.Contains(x.Steps[rootCauseStepID].PendingRewindInput.Reason, "the cause is elsewhere") {
		t.Fatalf("false + empty commits rewinds to root-cause with the summary: %s %+v", x.CurrentStep, x.Steps[rootCauseStepID].PendingRewindInput)
	}
}

// The shipped bug-fix fix contract states the conditional exemption and
// forbids re-listing an earlier attempt's commits.
func TestFixContractStatesConditionalEmptyCommits(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, "bug-fix", "steps", "fix.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{
		"root_cause_addressed false with nothing changed may report an empty list",
		"never re-list an earlier attempt's commits",
		"the one legal empty list",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the fix contract must state %q", want)
		}
	}
	if strings.Contains(text, "minItems: 1") {
		t.Error("the commit minimum is the driver's conditional rule, not an unconditional minItems")
	}
}
