// carries: .polyforge/verify-scope.conf, workflows/_shared/scripts/verify-scope

package test

// w528_mypy_gate_test.go: the workflow step scripts' type
// check — pf CI's python-types job, mypy --strict — is one of this
// repository's verification commands, so a type error is red before a
// merge instead of on main afterwards.
//
//   - The develop lane's light verification of a change to a step script
//     (workflows/**/*.py) selects it.
//   - The full gate's plan carries it.
//
// The expected command is read from the python-types job itself: the conf
// runs the very command CI runs, with the same strictness and scope.

import (
	"strings"
	"testing"
)

// w528MypyCommand is the python-types job's mypy line, verbatim.
func w528MypyCommand(t *testing.T) string {
	t.Helper()
	wf := w375ReadWorkflow(t, e2e344Root(t), w375Workflow)
	job, ok := wf.Jobs["python-types"]
	if !ok {
		t.Fatalf(".github/workflows/%s has no python-types job", w375Workflow)
	}
	for _, l := range w375Lines(job) {
		if strings.Contains(l, "python3 -m mypy ") {
			return l
		}
	}
	t.Fatalf("the python-types job runs no `python3 -m mypy` line: %q", w375Lines(job))
	return ""
}

// TestW528MypyGateLightTierSelectsIt: a light report on a change to a step
// script, under this repository's shipped conf, selects CI's mypy command.
func TestW528MypyGateLightTierSelectsIt(t *testing.T) {
	want := w528MypyCommand(t)
	dir, base := w25e2eFixture(t, map[string]string{
		"go.mod":                         "module fixture.test/w528\n\ngo 1.21\n",
		"main.go":                        "package main\n\nfunc main() {}\n",
		".polyforge/verify-scope.conf":   w25engIeopsConf(t),
		"workflows/_shared/scripts/x.py": "X = 1\n",
	})
	w25e2eWrite(t, dir, "workflows/_shared/scripts/x.py", "X = 2\n")
	w25e2eCommit(t, dir, "change a step script")

	r := w403Run(t, dir, "--light", "--base", base)
	if r.Light == nil || !*r.Light {
		t.Fatalf("--light must report light: true; got %v", r.Light)
	}
	if len(r.Paths) != 1 || r.Paths[0] != "workflows/_shared/scripts/x.py" {
		t.Fatalf("the change is the one step script; got paths %q", r.Paths)
	}
	for _, c := range r.Commands {
		if c.Cmd == want {
			return
		}
	}
	t.Fatalf("the light report of a step-script change does not select CI's mypy command %q; commands: %+v", want, r.Commands)
}

// TestW528MypyGateFullTierPlanCarriesIt: the full gate's plan for this
// repository carries CI's mypy command.
func TestW528MypyGateFullTierPlanCarriesIt(t *testing.T) {
	want := w528MypyCommand(t)
	r := w375FullTier(t, e2e344Root(t))
	for _, c := range r.Commands {
		if c.Cmd == want {
			return
		}
	}
	t.Fatalf("the full-tier plan does not carry CI's mypy command %q; commands: %+v", want, r.Commands)
}
