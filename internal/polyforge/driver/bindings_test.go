package driver

// bindings_test.go: e2e-author's optional bindings contract — a binding
// names one of the result's own scenario titles, carries every field and
// a marker that compiles; titles need no binding (document-only).

import (
	"encoding/json"
	"strings"
	"testing"
)

func authorResult(t *testing.T, output string) (*StepResult, ResultSpec) {
	t.Helper()
	if !json.Valid([]byte(output)) {
		t.Fatalf("fixture output is not JSON: %s", output)
	}
	return &StepResult{WIID: "wi_1", StepID: e2eAuthorStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(output)},
		ResultSpec{WIID: "wi_1", StepID: e2eAuthorStepID, SpawnN: 1}
}

const bindingTitles = `"scenario_titles":["S1 lists","S2 pages","S3 documents"]`

func TestBindingsAccepted(t *testing.T) {
	for name, out := range map[string]string{
		"absent": `{` + bindingTitles + `}`,
		"null":   `{` + bindingTitles + `,"bindings":null}`,
		"several titles share one test, one title unbound": `{` + bindingTitles + `,"bindings":[` +
			`{"title":"S1 lists","repo":"app","command":"go test ./test/ -run '^TestW1$' -count=1 -v","unit":"TestW1","marker":"^PASS S1$"},` +
			`{"title":"S2 pages","repo":"app","command":"go test ./test/ -run '^TestW1$' -count=1 -v","unit":"TestW1","marker":"^PASS S2$"}]}`,
		"two bindings on one title": `{` + bindingTitles + `,"bindings":[` +
			`{"title":"S1 lists","repo":"app","command":"node e2e/a.mjs","unit":"MP1","marker":"^PASS: MP1"},` +
			`{"title":"S1 lists","repo":"web","command":"node e2e/b.mjs","unit":"MP2","marker":"^PASS: MP2"}]}`,
	} {
		res, spec := authorResult(t, out)
		if err := ValidateStepResult(res, spec); err != nil {
			t.Errorf("%s: must be accepted: %v", name, err)
		}
	}
}

func TestBindingsRejected(t *testing.T) {
	ok := `"repo":"app","command":"go test ./...","unit":"TestA"`
	for name, c := range map[string]struct{ out, want string }{
		"not a list":       {`{` + bindingTitles + `,"bindings":{"title":"S1 lists"}}`, "not a list"},
		"not an object":    {`{` + bindingTitles + `,"bindings":["S1 lists"]}`, "not an object"},
		"unknown title":    {`{` + bindingTitles + `,"bindings":[{"title":"S9 nowhere",` + ok + `,"marker":"^PASS$"}]}`, `"S9 nowhere"`},
		"empty unit":       {`{` + bindingTitles + `,"bindings":[{"title":"S1 lists","repo":"app","command":"go test","unit":" ","marker":"x"}]}`, "non-empty unit"},
		"missing marker":   {`{` + bindingTitles + `,"bindings":[{"title":"S1 lists",` + ok + `}]}`, "non-empty marker"},
		"marker no regexp": {`{` + bindingTitles + `,"bindings":[{"title":"S1 lists",` + ok + `,"marker":"^PASS (S1$"}]}`, "does not compile"},
	} {
		res, spec := authorResult(t, c.out)
		err := ValidateStepResult(res, spec)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "bindings") {
			t.Errorf("%s: err = %v, want a rejection naming %q", name, err, c.want)
		}
	}
}

// Only e2e-author's completed result owes the contract.
func TestBindingsOnlyOnCompletedAuthor(t *testing.T) {
	bad := `{` + bindingTitles + `,"bindings":[{"title":"S9 nowhere"}]}`
	res, spec := authorResult(t, bad)
	res.Outcome, res.QuestionID, res.Question = OutcomeNeedsInput, "q1", "which?"
	if err := ValidateStepResult(res, spec); err != nil {
		t.Errorf("a needs_input result is not judged: %v", err)
	}
	res, spec = authorResult(t, bad)
	res.StepID, spec.StepID = "code", "code"
	if err := ValidateStepResult(res, spec); err != nil {
		t.Errorf("another step's bindings field is not judged: %v", err)
	}
}
