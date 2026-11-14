package test

// w24_scoped_gates_contract_test.go: a text contract.
//
// Work item #24 added the scoped-gate discoverability surfaces. The doctor
// verb's own authority (verbs/doctor.md) documents the readiness check —
// a doctor row until the engine moved into the workflow, the workflow's
// scope_ready.py; this test pins the markers it
// carries. (The filing skill's per-work-item scoped-tier hint left with
// the scoped tier, a lane runs the tier its
// verification step declares.)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW24ScopedGatesFilingHintContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("skills", "polyforge", "verbs", "doctor.md"),
			[]string{
				"Scoped-gates readiness is the workflow's own check",
				"workflows/_shared/scripts/scope_ready.py",
				"workflows/_shared/scripts/verify-scope",
				"verify-scope.conf",
				"docs/contributing.md『Verification tiers』",
				// a projection-stable marker, not a ticket tag: the desensitize
				// REWRITE rules wash workbench#NN literals to nothing, which
				// would empty this check in the projected copy.
				"never faked green",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("%s is scoped-gate text the change must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("%s must carry the scoped-gate marker %q — before filing a ticket that carries the opt-in line `verification: scoped`, the skill must confirm the release tree carries `workflows/_shared/scripts/verify-scope` and the repo is scoped-ready (a supported manifest or `.polyforge/verify-scope.conf`; the workflow's `scope_ready.py --repo <clone>`, or `pf projects get` plus a look in the local clone); when not ready the marker is inert and the ticket falls back to full; the contract is docs/contributing.md『Verification tiers』", c.rel, want)
			}
		}
	}
}
