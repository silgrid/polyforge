// carries: .polyforge/verify-scope.conf, test/testdata/verify-scope/verify-scope-go.sh, internal/contract/test/pf66_one_place_contract_test.go
package test

// pf66_one_place_contract_test.go: the one-place convention's own
// example (polyforge#66). This file joined the contract-scan fast check
// by landing in this package — no conf line, no frozen-seed byte, no
// registration anywhere else changed for it — and what it pins keeps
// that true for the next test: the conf's contract-scan command names
// no test (a per-test -run list is the two-places maintenance this
// change removed, so a new contract test never edits the conf), and the
// frozen Go seed's verbatim copy of that command stays byte-identical
// to the conf's (the polyforge#62 owner ruling: every verbatim copy
// syncs — the seed drifted once and cost that lane a stop).
//
// Run (targeted): GOWORK=off go test ./internal/contract/test/ -run '^TestPF66OnePlaceContract$' -count=1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPF66OnePlaceContract: adding a whole-repo contract test is one
// change — the conf's contract-scan command selects by package and
// names no test, and the frozen seed's copy of it is byte-identical.
func TestPF66OnePlaceContract(t *testing.T) {
	root := v1Root(t)
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	var decl []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "check=contract-scan|") {
			decl = append(decl, ln)
		}
	}
	if len(decl) != 1 {
		t.Fatalf("the conf must declare the contract-scan check exactly once; got %d declaration(s)", len(decl))
	}
	if !strings.HasPrefix(decl[0], "check=contract-scan|fast|") {
		t.Fatalf("the contract-scan check must stay a fast-phase check, got %q", decl[0])
	}
	cmd := strings.TrimPrefix(decl[0], "check=contract-scan|fast|")
	if strings.Contains(cmd, " -run ") {
		t.Errorf("the conf's contract-scan command names tests by hand (%q): a whole-repo contract test joins the check by landing in this package, so neither this conf nor the frozen seed changes with one", cmd)
	}

	seed, err := os.ReadFile(filepath.Join(root, "test", "testdata", "verify-scope", "verify-scope-go.sh"))
	if err != nil {
		t.Fatalf("the frozen Go seed must exist: %v", err)
	}
	if !strings.Contains(string(seed), "add_cmd \""+cmd+"\"") {
		t.Errorf("the frozen Go seed's verbatim copy of the contract-scan command is not byte-identical to the conf's: the seed must carry add_cmd %q in the same change that edits the conf (polyforge#62's owner ruling: every verbatim copy syncs)", cmd)
	}
}
