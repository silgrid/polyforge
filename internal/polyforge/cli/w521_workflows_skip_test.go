package cli

// w521_workflows_skip_test.go: the read face `pf
// workflows` reports, per step, whether its lane declares it `skippable`
// and — with --cwd mapping a workspace — each project whose yaml switches
// it off (`skip: {project: true}`), through the one judgment
// workspace.SkippedSteps. A step its lane does not declare skippable
// carries neither field, even when the yaml writes `skip: true` for it.

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestW521WorkflowsReportSkippableAndSkip(t *testing.T) {
	f := newWfFixture(t, "steps:\n  alpha:\n    ship: { skip: true }\n    build: { skip: true }\n  beta:\n    land: { skip: false }\n")
	writeLane(t, f.workflowsDir, "shipit", "steps:\n  - id: build\n  - id: ship\n    skippable: true\n  - id: land\n    skippable: true\n")

	code, stdout, stderr := runPf(t, f.env, "--json", "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("pf workflows exited %d: %s", code, stderr)
	}
	// decoded generically: the fields are the JSON document's, not a Go type's
	var doc struct {
		Bundles []struct {
			Name  string                       `json:"name"`
			Steps []map[string]json.RawMessage `json:"steps"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("pf workflows did not print one JSON document: %v\n%s", err, stdout)
	}
	steps := map[string]map[string]json.RawMessage{}
	for _, b := range doc.Bundles {
		if b.Name != "shipit" {
			continue
		}
		for _, s := range b.Steps {
			var id string
			_ = json.Unmarshal(s["id"], &id)
			steps[id] = s
		}
	}
	if len(steps) != 3 {
		t.Fatalf("pf workflows lists lane shipit with steps %v, want build, ship, land:\n%s", steps, stdout)
	}
	field := func(step, key string) string {
		raw, ok := steps[step][key]
		if !ok {
			return "<absent>"
		}
		var b bytes.Buffer // the document is indented; compare compact
		if err := json.Compact(&b, raw); err != nil {
			return string(raw)
		}
		return b.String()
	}
	cases := []struct{ step, key, want string }{
		{"ship", "skippable", "true"},
		{"ship", "skip", `{"alpha":true}`},
		{"land", "skippable", "true"},
		{"land", "skip", "<absent>"},
		{"build", "skippable", "<absent>"},
		{"build", "skip", "<absent>"},
	}
	for _, c := range cases {
		if got := field(c.step, c.key); got != c.want {
			t.Errorf("step %s: %s = %s, want %s\n%s", c.step, c.key, got, c.want, stdout)
		}
	}

	// without --cwd: skippable still reported, no project's skip
	code, stdout, stderr = runPf(t, f.env, "--json", "workflows")
	if code != 0 {
		t.Fatalf("pf workflows (no --cwd) exited %d: %s", code, stderr)
	}
	doc.Bundles = nil
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("pf workflows did not print one JSON document: %v\n%s", err, stdout)
	}
	for _, b := range doc.Bundles {
		for _, s := range b.Steps {
			if _, ok := s["skip"]; ok {
				t.Errorf("lane %s: a step carries skip with no --cwd workspace:\n%s", b.Name, stdout)
			}
		}
	}
}
