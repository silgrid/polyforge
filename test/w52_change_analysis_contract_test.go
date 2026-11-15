// carries: workflows/develop/*, workflows/_shared/skills/*, workflows/integrate/*, docs/concepts.md, docs/contributing.md

package test

// w52_change_analysis_contract_test.go (polyforge#52): the texts that
// state the change analysis and the fix-ticket marker carry what the code
// keeps — build writes the analysis at its close and its hand-off names
// it, review judges it for obviously missing tests, verification runs the
// union of the engine's selection and the analysis's listed tests, and
// an integration batch's fix tickets carry the integration_fix attr that
// switches their verification to the full bar. Whitespace-flattened, so
// a rewrap keeps them pinned.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w52Flat reads a repository file with its whitespace folded.
func w52Flat(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}

// w52Wants checks each want is stated (folded) in rel.
func w52Wants(t *testing.T, rel string, wants []string) {
	t.Helper()
	flat := w52Flat(t, rel)
	for _, w := range wants {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("%s must state %q", rel, w)
		}
	}
}

// TestW52FixTicketsCarryTheMarker (AC4, AC5): an integration batch's fix
// tickets are filed with the structured integration_fix attr — the
// full-gate's and collect's records JSON carry the attrs write beside
// each file, the lane's whitelist names the key, and the fix ticket's
// develop verification reads it.
func TestW52FixTicketsCarryTheMarker(t *testing.T) {
	w52Wants(t, "workflows/integrate/skills/full-gate.md", []string{
		`{"verb": "attrs", "target": "key:<this ticket's key>", "name": "integration_fix", "value": true}`,
		"the structured marker that makes the fix ticket's own develop verification run the full bar (a ticket without it runs the light tier)",
	})
	w52Wants(t, "workflows/integrate/skills/collect.md", []string{
		`{"verb": "attrs", "target": "key:<the ticket's key>", "name": "integration_fix", "value": true}`,
	})
	for _, step := range []string{"collect", "full-gate"} {
		raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "integrate", "workflow.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		yaml := string(raw)
		at := strings.Index(yaml, "id: "+step)
		if at < 0 {
			t.Fatalf("workflows/integrate/workflow.yaml names no step %s", step)
		}
		rest := yaml[at:]
		if end := strings.Index(rest, "\n  - id: "); end > 0 {
			rest = rest[:end]
		}
		if !strings.Contains(rest, "attrs: [integration, integration_fix]") {
			t.Errorf("workflows/integrate/workflow.yaml: the %s step must whitelist the integration_fix attrs key (hub.attrs; the attrs verb follows it)", step)
		}
	}
	w52Wants(t, "workflows/integrate/README.md", []string{
		"every fix ticket it files carries the integration_fix attr",
	})
}
