// carries: workflows/develop/steps/build.md, workflows/_shared/skills/build.md, workflows/develop/workflow.yaml, internal/contracttest/w241_handoff_gate_contract_test.go

package test

// w241_handoff_gate_contract_test.go (polyforge#241, AC1): the two texts
// that state the build close's mechanical facts wiring agree with the
// lane's declaration — build's 3c and the shared build skill run
// `facts.py handoff` before the hand-off, once per touched repo, writing
// the result the develop lane's `facts_proof` names. The command is the
// lane's, {repo} its only substitution; the daemon holds no repository's
// command. Whitespace-flattened, so a rewrap keeps the line pinned; the
// wiring's behaviour on real fixtures is pinned by the Python tests under
// workflows/_shared/scripts/tests/, not here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW241HandoffGateContract(t *testing.T) {
	// the lane's declaration, {repo} shown as <repo> (the step texts'
	// spelling of the placeholder)
	const command = `"$PF_PYTHON" "$PF_SHARED_DIR/scripts/facts.py" handoff --json --out artifacts/build/handoff-facts/<repo>.json`
	const result = `artifacts/build/handoff-facts/<repo>.json`

	for _, rel := range []string{"workflows/develop/steps/build.md", "workflows/_shared/skills/build.md"} {
		flat := w68Flat(t, rel)
		if !strings.Contains(flat, command) {
			t.Errorf("%s must state the mechanical facts line %q", rel, command)
		}
		// the close names the gate the line satisfies
		if !strings.Contains(flat, "facts.py handoff") {
			t.Errorf("%s must name the facts.py handoff gate", rel)
		}
	}

	raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "develop", "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(raw)
	declared := strings.ReplaceAll(command, "<repo>", "{repo}")
	if !strings.Contains(yaml, "facts_proof:") {
		t.Fatalf("develop's build declares no facts_proof")
	}
	if !strings.Contains(yaml, "command: '"+declared+"'") {
		t.Errorf("the lane's facts_proof command is not the step's line %q:\n%s", declared, yaml)
	}
	if !strings.Contains(yaml, "result: "+strings.ReplaceAll(result, "<repo>", "{repo}")) {
		t.Errorf("the lane's facts_proof result is not %q", result)
	}
}
