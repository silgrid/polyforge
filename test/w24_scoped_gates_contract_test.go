package test

// w24_scoped_gates_contract_test.go: text contract for.
//
// Work item #24 adds the scoped-gate discoverability surfaces: the filing
// skill fragments must teach the entry-point precondition before a ticket
// that opts into the scoped verification tier is filed, and the doctor verb's
// own authority (verbs/doctor.md) must document the new `scoped-gates
// readiness` row it now emits. The change is prompt material (skill
// fragments): no endpoint, verb, flag or protocol moves, so the only
// mechanical contract is the shipped text itself.
//
// This test pins the markers the two filing fragments must carry — the
// opt-in line `verification: scoped`, the release-shipped engine
// `tools/verify-scope` and its declarative `verify-scope.conf` override, the
// fall-back-to-full fact, and the one shared authority
// `docs/contributing.md`『Verification tiers』 — and the markers the doctor
// check table must carry for the new row. It is red on the base tree (no
// marker set existed in the skill fragments) and green only once the hint
// shipped.

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
			filepath.Join("skills", "polyforge", "verbs", "file.md"),
			[]string{
				"verification: scoped",
				"tools/verify-scope",
				"verify-scope.conf",
				"falls back to full",
				"docs/contributing.md『Verification tiers』",
				"scoped-gates readiness",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "execute-intent.md"),
			[]string{
				"verification: scoped",
				"tools/verify-scope",
				"verify-scope.conf",
				"falls back to full",
				"docs/contributing.md『Verification tiers』",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "doctor.md"),
			[]string{
				"scoped-gates readiness",
				"tools/verify-scope",
				"verify-scope.conf",
				"docs/contributing.md『Verification tiers』",
				"no local clone",
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
				t.Errorf("%s must carry the scoped-gate marker %q — before filing a ticket that carries the opt-in line `verification: scoped`, the skill must confirm the release tree carries `tools/verify-scope` and the repo is scoped-ready (a supported manifest or `.polyforge/verify-scope.conf`; the `pf doctor` `scoped-gates readiness` row, or `pf projects get` plus a look in the local clone); when not ready the marker is inert and the ticket falls back to full; the contract is docs/contributing.md『Verification tiers』", c.rel, want)
			}
		}
	}
}
