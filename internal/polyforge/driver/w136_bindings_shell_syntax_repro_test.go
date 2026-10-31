package driver

// w136_bindings_shell_syntax_repro_test.go: the repro of's
// first reported failure — the driver's accept gate takes an e2e-author
// binding whose `command` is not a parseable shell command.
//
// The field shape (yingfang#37): the command opens a double quote before
// `$(...)` and closes it with a SINGLE quote after the closing paren:
//
//	YINGFANG_TEST_DSN="$(grep ... 'regex' tests/e2e_s37/main_test.go)' go test ...
//
// `bash -n` on that string exits 2 ("unexpected EOF while looking for
// matching `\"'"), so the canonical record's binding can never pass
// verification; the reported failure is that nothing rejects it at
// delivery time, and the malformed string becomes canonical.
//
// The test encodes the required contract at the driver's accept gate:
// a completed e2e-author result whose binding command does not parse as
// a shell command is an envelope rejection naming the binding index and
// the offending character; a correctly quoted command is accepted. The
// control passes before and after, so a red run fails for exactly the
// reported reason.

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	w136Titles = `"scenario_titles":["S1 lists","S2 captures"]`

	// the exact malformed command from yingfang#37 (code-report.md, round 2/3)
	w136BadCommand = `YINGFANG_TEST_DSN="$(grep -m1 -o 'postgres://[a-zA-Z0-9_:@./?=-]*' tests/e2e_s37/main_test.go)' go test ./tests/e2e_cp37/ -count=1 -v`

	// the same command with the trailing quote made matching — the only
	// byte that differs in the WI's one-time correction
	w136GoodCommand = `YINGFANG_TEST_DSN="$(grep -m1 -o 'postgres://[a-zA-Z0-9_:@./?=-]*' tests/e2e_s37/main_test.go)" go test ./tests/e2e_cp37/ -count=1 -v`
)

// w136AuthorResult builds a completed e2e-author envelope with two
// bindings: the first well-quoted, the second the given command. The
// malformed binding sits at index 1 so a rejection can name it.
func w136AuthorResult(t *testing.T, second string) (*StepResult, ResultSpec) {
	t.Helper()
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	out := `{` + w136Titles + `,"bindings":[` +
		`{"title":"S1 lists","repo":"yingfang","command":"go test ./tests/e2e_cp37/ -count=1 -v","unit":"TestE2EList","marker":"^PASS S1$"},` +
		`{"title":"S2 captures","repo":"yingfang","command":` + q(second) + `,"unit":"TestE2ECapture","marker":"^PASS S2$"}]}`
	if !json.Valid([]byte(out)) {
		t.Fatalf("fixture output is not JSON: %s", out)
	}
	return &StepResult{WIID: "wi_1", StepID: e2eAuthorStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(out)},
		ResultSpec{WIID: "wi_1", StepID: e2eAuthorStepID, SpawnN: 1}
}

// TestW136MalformedBindingCommandIsRejected: the reported failure. The
// malformed command must be an envelope rejection naming bindings[1]
// and the quote that does not pair; today the driver accepts it, so the
// string would become the canonical record verification later executes.
func TestW136MalformedBindingCommandIsRejected(t *testing.T) {
	res, spec := w136AuthorResult(t, w136BadCommand)
	err := ValidateStepResult(res, spec)
	if err == nil {
		t.Fatalf("REPORTED FAILURE: a completed e2e-author result whose bindings[1].command is not a parseable shell command was ACCEPTED:\n  %s\n"+
			"bash -n on that command exits 2 (unexpected EOF while looking for matching `\"'), so once accepted it is canonical and verify-run can only exit 2",
			w136BadCommand)
	}
	msg := err.Error()
	if !strings.Contains(msg, "bindings[1]") {
		t.Errorf("the rejection must name the offending command's index (bindings[1]); got: %v", err)
	}
	if !strings.Contains(msg, `"`) && !strings.Contains(strings.ToLower(msg), "quote") {
		t.Errorf("the rejection must name the quote that does not pair; got: %v", err)
	}
}

// TestW136WellQuotedBindingCommandIsAccepted: the control — the corrected
// form of the same command parses and passes the gate, before and after
// the fix. It keeps the failure above attributable to the malformed
// quote, not to a gate that rejects every binding.
func TestW136WellQuotedBindingCommandIsAccepted(t *testing.T) {
	res, spec := w136AuthorResult(t, w136GoodCommand)
	if err := ValidateStepResult(res, spec); err != nil {
		t.Fatalf("control: the correctly quoted command must be accepted: %v", err)
	}
}
