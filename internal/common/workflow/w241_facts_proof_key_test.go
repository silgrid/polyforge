package workflow_test

// w241_facts_proof_key_test.go (polyforge#241, AC1/AC2): a step's
// `facts_proof` — the result file facts.py handoff writes and the one
// command that writes it, both carrying {repo} — loads in its shape and is
// refused outside it. The key is `fast_check_proof`'s twin: the daemon
// reads the same `pf-step-result/1` shape (verdict pass, heads carrying
// the repo at its current HEAD), so the loader judges it by the same
// rules, and the contract registry carries it beside the fast-check key.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

const factsProofLane = `steps:
  - id: work
    facts_proof:
      result: artifacts/x/handoff-facts/{repo}.json
      command: run-handoff-facts --repo {repo} --out artifacts/x/handoff-facts/{repo}.json
`

func TestW241FactsProofKey(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo", factsProofLane)
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	work := lane.Step("work")
	if work == nil || work.FactsProof == nil {
		t.Fatalf("work carries no facts_proof: %+v", work)
	}
	if work.FactsProof.Result != "artifacts/x/handoff-facts/{repo}.json" ||
		work.FactsProof.Command != "run-handoff-facts --repo {repo} --out artifacts/x/handoff-facts/{repo}.json" {
		t.Fatalf("facts_proof = %+v, want the declared templates", work.FactsProof)
	}
	// the two proof keys are independent: declaring one declares no other
	if work.FastCheckProof != nil {
		t.Fatalf("facts_proof alone must declare no fast_check_proof: %+v", work.FastCheckProof)
	}

	for _, c := range []struct{ name, front, code, at, phrase string }{
		{"an absolute result", "steps:\n  - id: work\n    facts_proof:\n      result: /tmp/{repo}.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.result", "relative"},
		{"a result leaving the capsule", "steps:\n  - id: work\n    facts_proof:\n      result: ../x/{repo}.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.result", ".."},
		{"a result without {repo}", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/result.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.result", "{repo}"},
		{"an empty command", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/{repo}.json\n      command: \"\"\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.command", "one line"},
		{"a command over two lines", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/{repo}.json\n      command: \"c --repo {repo}\\nd --out {repo}\"\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.command", "one line"},
		{"a command without {repo}", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/{repo}.json\n      command: c --out artifacts/x.json\n",
			workflow.CodeInvalidValue, "steps[0](work).facts_proof.command", "{repo}"},
		{"no result", "steps:\n  - id: work\n    facts_proof:\n      command: c --repo {repo}\n",
			workflow.CodeMissingKey, "steps[0](work).facts_proof.result", "no result"},
		{"no command", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/{repo}.json\n",
			workflow.CodeMissingKey, "steps[0](work).facts_proof.command", "no command"},
		{"an unknown subkey", "steps:\n  - id: work\n    facts_proof:\n      result: artifacts/x/{repo}.json\n      command: c --repo {repo}\n      foo: bar\n",
			workflow.CodeUnknownKey, "steps[0](work).facts_proof.foo", "foo"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := refusedOnce(t, c.front, c.code, c.at)
			if !strings.Contains(e.Msg, c.phrase) {
				t.Errorf("the defect %q does not carry %q", e.Msg, c.phrase)
			}
		})
	}
}

// TestW241FactsProofRegistry pins the key in the contract registry and the
// lane schema: the reference's key tables are generated from it, and a
// missing entry is a lane file no editor completes.
func TestW241FactsProofRegistry(t *testing.T) {
	dump := workflow.RegistryDump()
	for _, want := range []string{
		"[facts_proof keys]",
		"facts_proof.result: string, required",
		"facts_proof.command: string, required",
		"[required]\nfacts_proof.command\nfacts_proof.result",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("the contract registry carries no %q", want)
		}
	}
	raw, err := workflow.LaneSchema()
	if err != nil {
		t.Fatal(err)
	}
	schema := string(raw)
	for _, want := range []string{`"facts_proof"`, `"artifacts`} {
		if !strings.Contains(schema, want) {
			t.Errorf("the lane schema carries no %q", want)
		}
	}
}
