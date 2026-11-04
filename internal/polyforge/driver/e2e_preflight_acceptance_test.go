// carries: internal/polyforge/driver/*

package driver

// e2e_preflight_acceptance_test.go: authored acceptance scenarios for the
// executability-preflight contract of a completed e2e-author result
// (artifacts/spec.md, "## Acceptance criteria"). Authored against the spec
// BEFORE the driver check exists: the rejection scenarios below are red on
// the pre-implementation tree because ValidateStepResult has no preflight
// gate, and turn green only on the delivered check.
//
// Run (targeted): GOWORK=off go test ./internal/polyforge/driver -run '^TestW276E2E' -count=1 -v
//
// Every scenario prints one `PASS S<n>` marker line when it passes, so the
// verification step can bind each scenario to its own output line.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func w276AuthorEnvelope(t *testing.T, output string) (*StepResult, ResultSpec) {
	t.Helper()
	if !json.Valid([]byte(output)) {
		t.Fatalf("fixture output is not JSON: %s", output)
	}
	return &StepResult{WIID: "wi_w276", StepID: e2eAuthorStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(output)},
		ResultSpec{WIID: "wi_w276", StepID: e2eAuthorStepID, SpawnN: 1}
}

// w276Rejected drives the completed envelope through the driver's result
// validation and requires a rejection whose message names every want token.
func w276Rejected(t *testing.T, output string, want ...string) {
	t.Helper()
	res, spec := w276AuthorEnvelope(t, output)
	err := ValidateStepResult(res, spec)
	if err == nil {
		t.Fatalf("the completed envelope was accepted; want a rejection naming %s", strings.Join(want, ", "))
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("the rejection %q must name %q", err, w)
		}
	}
}

func TestW276E2EPreflightMissingRejected(t *testing.T) {
	w276Rejected(t,
		`{"scenario_titles":["S1"],"baseline":[{"title":"S1","status":"fail","evidence":"expected the new flag, got absent"}]}`,
		"preflight")
	fmt.Println("PASS S1")
}

func TestW276E2EPreflightMissingKeyRejected(t *testing.T) {
	w276Rejected(t,
		`{"scenario_titles":["S2"],"baseline":[{"title":"S2","status":"fail","evidence":"baseline red",`+
			`"preflight":{"data_source":{"verdict":"ok","evidence":"e2e/suite.mjs: grep -c target = 1"},`+
			`"async_settle":{"verdict":"ok","evidence":"bounded wait 10000ms"},`+
			`"selector_unique":{"verdict":"ok","evidence":"runtime count 0 pre-existing hits"}}}]}`,
		"preflight", "timing_window")
	fmt.Println("PASS S2")
}

func TestW276E2EPreflightEmptyEvidenceRejected(t *testing.T) {
	w276Rejected(t,
		`{"scenario_titles":["S3"],"baseline":[{"title":"S3","status":"fail","evidence":"baseline red",`+
			`"preflight":{"data_source":{"verdict":"ok","evidence":""},`+
			`"async_settle":{"verdict":"ok","evidence":"bounded wait 10000ms"},`+
			`"timing_window":{"verdict":"ok","evidence":"window 2000ms; assertion at t=0"},`+
			`"selector_unique":{"verdict":"ok","evidence":"runtime count 0 pre-existing hits"}}}]}`,
		"data_source", "evidence")
	fmt.Println("PASS S3")
}

func TestW276E2EPreflightIllegalVerdictRejected(t *testing.T) {
	for _, verdict := range []string{"structural", "pass"} {
		w276Rejected(t,
			`{"scenario_titles":["S4"],"baseline":[{"title":"S4","status":"fail","evidence":"baseline red",`+
				`"preflight":{"data_source":{"verdict":"ok","evidence":"e2e/suite.mjs: grep -c target = 1"},`+
				`"async_settle":{"verdict":"ok","evidence":"bounded wait 10000ms"},`+
				`"timing_window":{"verdict":"`+verdict+`","evidence":"window 2000ms; assertion at t=0"},`+
				`"selector_unique":{"verdict":"ok","evidence":"runtime count 0 pre-existing hits"}}}]}`,
			"timing_window", verdict)
	}
	fmt.Println("PASS S4")
}

func TestW276E2EAllGreenWithoutPreflightRejected(t *testing.T) {
	w276Rejected(t,
		`{"scenario_titles":["S5"],"baseline":[{"title":"S5","status":"pass","evidence":"every scenario green at baseline"}]}`,
		"preflight")
	fmt.Println("PASS S5")
}
