package driver

// preflight_test.go: the executability preflight rejection of a completed
// e2e-author result (result.go rule 3d3, checkPreflight). Every case goes
// through ValidateStepResult only, so the file compiles against a driver
// without the check and its rejection cases are red there.

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	pfOK    = `{"verdict":"ok","evidence":"e2e/a.mjs: grep -c flag = 2"}`
	pfNA    = `{"verdict":"n/a","evidence":"CLI output, no render chain"}`
	pfFour  = `{"data_source":` + pfOK + `,"async_settle":` + pfOK + `,"timing_window":` + pfOK + `,"selector_unique":` + pfOK + `}`
	pfAllNA = `{"data_source":` + pfNA + `,"async_settle":` + pfNA + `,"timing_window":` + pfNA + `,"selector_unique":` + pfNA + `}`
)

// pfLayout is a healthy preflight shaped like a targeted run of the
// browser suite e2e/polyforge-config-layout.mjs, each evidence in its own
// fixed shape with values read from that suite: the mocked read and real
// grep -c counts, its bounded waitFor and timeout, n/a with the reason (the
// suite asserts a settled static layout, no transient window), and a
// runtime count of 0 pre-existing hits.
const pfLayout = `{` +
	`"data_source":{"verdict":"ok","evidence":"e2e/polyforge-config-layout.mjs: page.route(\"**/api/polyforge/config?*\") fulfils the CONFIG fixture, grep -c step_timeout = 3; components/PolyforgeConfig.tsx: grep -c polyforge-config-row = 5"},` +
	`"async_settle":{"verdict":"ok","evidence":"locator.waitFor({ state: \"attached\", timeout: 30_000 }) on the cli.toml zone before measuring; page.goto bounded by NAV_MS (180 s, e2e/timeouts.mjs)"},` +
	`"timing_window":{"verdict":"n/a","evidence":"no transient window: the suite measures the settled layout after the mocked read renders (grep -c toast|animation|setTimeout = 0)"},` +
	`"selector_unique":{"verdict":"ok","evidence":"section[aria-label=\"cli.toml\"] runtime count() = 0 pre-existing hits before the Polyforge pane opens; 1 after (asserted exactly one)"}` +
	`}`

// pfRow is one baseline row with the given status and raw preflight JSON
// (empty: no preflight key at all).
func pfRow(status, preflight string) string {
	row := `{"title":"A","status":"` + status + `","evidence":"want 1 got 0"`
	if preflight != "" {
		row += `,"preflight":` + preflight
	}
	return row + `}`
}

func pfOutput(rows ...string) string {
	return `{"scenarios_path":"artifacts/e2e-scenarios.md","scenario_titles":["A"],"baseline":[` + strings.Join(rows, ",") + `]}`
}

func pfValidate(step string, outcome Outcome, output string) error {
	res := &StepResult{WIID: "wi_pf", StepID: step, SpawnN: 1, Outcome: outcome, Output: json.RawMessage(output)}
	if outcome == OutcomeNeedsInput {
		res.QuestionID, res.Question = "q1", "which?"
	}
	return ValidateStepResult(res, ResultSpec{WIID: "wi_pf", StepID: step, SpawnN: 1})
}

// pfWith replaces one fixed key of the healthy four-key preflight.
func pfWith(key, value string) string {
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(pfFour), &m); err != nil {
		panic(err)
	}
	if value == "" {
		delete(m, key)
	} else {
		m[key] = json.RawMessage(value)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func TestPreflightRejectsBadRows(t *testing.T) {
	cases := []struct {
		name, output string
		want         []string
	}{
		{"missing preflight", pfOutput(pfRow("fail", "")), []string{"baseline[0]", "without a preflight object"}},
		{"all-green row without preflight", pfOutput(pfRow("pass", "")), []string{"without a preflight object"}},
		{"second row without preflight", pfOutput(pfRow("fail", pfFour), pfRow("fail", "")), []string{"baseline[1]", "preflight"}},
		{"preflight not an object", pfOutput(pfRow("fail", `"ok"`)), []string{"without a preflight object"}},
		{"row not an object", pfOutput(`"A"`), []string{"baseline[0] that is not an object"}},
		{"empty evidence", pfOutput(pfRow("fail", pfWith("data_source", `{"verdict":"ok","evidence":""}`))), []string{"preflight.data_source", "non-empty evidence"}},
		{"blank evidence", pfOutput(pfRow("fail", pfWith("async_settle", `{"verdict":"n/a","evidence":"  "}`))), []string{"preflight.async_settle", "non-empty evidence"}},
		{"missing evidence", pfOutput(pfRow("fail", pfWith("timing_window", `{"verdict":"ok"}`))), []string{"preflight.timing_window", "non-empty evidence"}},
		{"verdict structural", pfOutput(pfRow("fail", pfWith("timing_window", `{"verdict":"structural","evidence":"asserted after settle"}`))), []string{"timing_window", `"structural"`, "never a preflight verdict"}},
		{"verdict fail", pfOutput(pfRow("fail", pfWith("selector_unique", `{"verdict":"fail","evidence":"3 hits"}`))), []string{"selector_unique", `"fail"`}},
		{"verdict missing", pfOutput(pfRow("fail", pfWith("data_source", `{"evidence":"x"}`))), []string{"data_source", "not ok or n/a"}},
		{"check not an object", pfOutput(pfRow("fail", pfWith("async_settle", `"ok"`))), []string{"preflight without async_settle"}},
		{"unknown fifth key", pfOutput(pfRow("fail", pfWith("notes", pfOK))), []string{`unknown key "notes"`, "data_source, async_settle, timing_window, selector_unique"}},
	}
	for _, key := range []string{"data_source", "async_settle", "timing_window", "selector_unique"} {
		cases = append(cases, struct {
			name, output string
			want         []string
		}{"missing " + key, pfOutput(pfRow("fail", pfWith(key, ""))), []string{"preflight without " + key}})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := pfValidate(e2eAuthorStepID, OutcomeCompleted, c.output)
			if err == nil {
				t.Fatalf("accepted %s", c.output)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("rejection %q does not name %q", err, w)
				}
			}
		})
	}
}

func TestPreflightAcceptsHealthyShapes(t *testing.T) {
	cases := map[string]string{
		"four-key ok":              pfOutput(pfRow("fail", pfFour)),
		"config-layout run shapes": pfOutput(pfRow("fail", pfLayout)),
		"all n/a (non-UI stack)":   pfOutput(pfRow("fail", pfAllNA)),
		"all-green row":            pfOutput(pfRow("pass", pfFour)),
		"mixed ok and n/a rows":    pfOutput(pfRow("fail", pfFour), pfRow("fail", pfWith("selector_unique", pfNA))),
		"skipped mode, empty":      `{"e2e_author_mode":"skipped-with-rationale","skip_rationale":"internal","scenario_titles":[],"baseline":[]}`,
		"no baseline field":        `{"scenarios_path":"artifacts/e2e-scenarios.md","scenario_titles":["A"]}`,
	}
	for name, out := range cases {
		if err := pfValidate(e2eAuthorStepID, OutcomeCompleted, out); err != nil {
			t.Errorf("%s: rejected: %v", name, err)
		}
	}
}

// TestPreflightJudgesOnlyCompletedE2EAuthor: a non-completed e2e-author
// result and another step's baseline field are not judged.
func TestPreflightJudgesOnlyCompletedE2EAuthor(t *testing.T) {
	bad := pfOutput(pfRow("fail", ""))
	if err := pfValidate(e2eAuthorStepID, OutcomeNeedsInput, bad); err != nil {
		t.Errorf("needs_input judged: %v", err)
	}
	if err := pfValidate(e2eAuthorStepID, OutcomeIncomplete, bad); err != nil {
		t.Errorf("incomplete judged: %v", err)
	}
	if err := pfValidate("repro", OutcomeCompleted, bad); err != nil {
		t.Errorf("another step's baseline judged: %v", err)
	}
}
