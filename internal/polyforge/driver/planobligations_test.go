package driver

// planobligations_test.go: the plan's coupled-obligation contract
// a declared write without an obligation entry is an
// envelope rejection; the new shape and an explicit none-with-reason are
// accepted; read entries owe nothing.

import (
	"encoding/json"
	"strings"
	"testing"
)

func planResult(t *testing.T, output string) (*StepResult, ResultSpec) {
	t.Helper()
	if !json.Valid([]byte(output)) {
		t.Fatalf("fixture output is not JSON: %s", output)
	}
	return &StepResult{WIID: "wi_1", StepID: planStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(output)},
		ResultSpec{WIID: "wi_1", StepID: planStepID, SpawnN: 1}
}

func TestPlanObligationsMissingEntryRejected(t *testing.T) {
	for name, out := range map[string]string{
		"old shape":      `{"touched_files":[{"path":"a.go","intent":"read"},{"path":"b.go","intent":"write"}]}`,
		"empty list":     `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[]}]}`,
		"none no reason": `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[{"none":true}]}]}`,
		"none beside":    `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[{"none":true,"reason":"r"},{"path":"d.md"}]}]}`,
		"no path":        `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[{"note":"a row"}]}]}`,
		"not object":     `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":["d.md"]}]}`,
	} {
		res, spec := planResult(t, out)
		err := ValidateStepResult(res, spec)
		if err == nil {
			t.Fatalf("%s: a write without a well-formed coupled-obligation entry must be rejected", name)
		}
		if !strings.Contains(err.Error(), `"b.go"`) {
			t.Fatalf("%s: the rejection must name the offending write: %v", name, err)
		}
	}
}

func TestPlanObligationsNewShapeAccepted(t *testing.T) {
	for name, out := range map[string]string{
		"paths":     `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[{"path":"docs/b.md","note":"table row"},{"path":"docs/list.md"}]}]}`,
		"none":      `{"touched_files":[{"path":"b.go","intent":"write","coupled_obligations":[{"none":true,"reason":"no doc enumerates it"}]}]}`,
		"read only": `{"touched_files":[{"path":"a.go","intent":"read"}]}`,
		"no list":   `{}`,
	} {
		res, spec := planResult(t, out)
		if err := ValidateStepResult(res, spec); err != nil {
			t.Fatalf("%s: must be accepted: %v", name, err)
		}
	}
}

// Only the plan step owes the contract, and only on a completed result.
func TestPlanObligationsOnlyOnCompletedPlan(t *testing.T) {
	const old = `{"touched_files":[{"path":"b.go","intent":"write"}]}`
	res, spec := planResult(t, old)
	res.StepID, spec.StepID = "code", "code"
	if err := ValidateStepResult(res, spec); err != nil {
		t.Fatalf("a non-plan step owes no coupled obligations: %v", err)
	}
	res, spec = planResult(t, old)
	res.Outcome = OutcomeIncomplete
	if err := ValidateStepResult(res, spec); err != nil {
		t.Fatalf("an incomplete plan owes no coupled obligations: %v", err)
	}
}
