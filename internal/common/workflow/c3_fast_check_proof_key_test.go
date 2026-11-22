package workflow_test

// c3_fast_check_proof_key_test.go: a step's `fast_check_proof` — the result
// file and the command that writes it, both carrying {repo} — loads in its
// shape and is refused outside it (polyforge#211). The shape is the lane's
// declaration: a path relative to the capsule with no `..` and a
// single-line command, each carrying exactly the substitution token the
// daemon later fills with the task repo's name.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

const fastCheckProofLane = `steps:
  - id: work
    fast_check_proof:
      result: artifacts/x/{repo}.json
      command: run-fast-check --repo {repo} --out artifacts/x/{repo}.json
`

func TestFastCheckProofKey(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "demo", fastCheckProofLane)
	lane, errs := workflow.Load(dir)
	if len(errs) != 0 {
		t.Fatal(workflow.AsLoadError(dir, errs))
	}
	work := lane.Step("work")
	if work == nil || work.FastCheckProof == nil {
		t.Fatalf("work carries no fast_check_proof: %+v", work)
	}
	if work.FastCheckProof.Result != "artifacts/x/{repo}.json" ||
		work.FastCheckProof.Command != "run-fast-check --repo {repo} --out artifacts/x/{repo}.json" {
		t.Fatalf("fast_check_proof = %+v, want the declared templates", work.FastCheckProof)
	}

	for _, c := range []struct{ name, front, code, at, phrase string }{
		{"an absolute result", "steps:\n  - id: work\n    fast_check_proof:\n      result: /tmp/{repo}.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.result", "relative"},
		{"a result leaving the capsule", "steps:\n  - id: work\n    fast_check_proof:\n      result: ../x/{repo}.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.result", ".."},
		{"a result without {repo}", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/result.json\n      command: c --repo {repo}\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.result", "{repo}"},
		{"an empty command", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/{repo}.json\n      command: \"\"\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.command", "one line"},
		{"a command over two lines", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/{repo}.json\n      command: \"c --repo {repo}\\nd --out {repo}\"\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.command", "one line"},
		{"a command without {repo}", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/{repo}.json\n      command: c --out artifacts/x.json\n",
			workflow.CodeInvalidValue, "steps[0](work).fast_check_proof.command", "{repo}"},
		{"no result", "steps:\n  - id: work\n    fast_check_proof:\n      command: c --repo {repo}\n",
			workflow.CodeMissingKey, "steps[0](work).fast_check_proof.result", "no result"},
		{"no command", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/{repo}.json\n",
			workflow.CodeMissingKey, "steps[0](work).fast_check_proof.command", "no command"},
		{"an unknown subkey", "steps:\n  - id: work\n    fast_check_proof:\n      result: artifacts/x/{repo}.json\n      command: c --repo {repo}\n      foo: bar\n",
			workflow.CodeUnknownKey, "steps[0](work).fast_check_proof.foo", "foo"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := refusedOnce(t, c.front, c.code, c.at)
			if !strings.Contains(e.Msg, c.phrase) {
				t.Errorf("the defect %q does not carry %q", e.Msg, c.phrase)
			}
		})
	}
}
