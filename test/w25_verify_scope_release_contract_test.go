package test

// w25_verify_scope_release_contract_test.go: the same-release-tree contract
// for. The verification lane may only take its fast-gate scope
// from the release-shipped engine, so the shipped `tools/verify-scope` and the
// lane text that names it must travel in one tree. There is no
// backward-compatibility branch: the old repo-owned script is gone and the
// lane never describes an engine-absent or old-release path.
// Since the lane text is split: the shared step definition
// (its output schema names the engine invocation) and the scoped-tier
// section the driver renders on a scoped dispatch
// (`workflows/_shared/scoped_verification.md`) — both halves are the lane
// text of this contract.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25ReleaseTree' -count=1

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW25ReleaseTreeEngineAndLaneShipTogether pins the coexistence guarantee.
func TestW25ReleaseTreeEngineAndLaneShipTogether(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(root, "tools", "verify-scope")
	info, err := os.Stat(engine)
	if err != nil {
		t.Fatalf("the release tree must ship tools/verify-scope: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("tools/verify-scope must be executable: mode %v", info.Mode())
	}
	step := w25ReadLane(t, root, filepath.Join("workflows", "_shared", "steps", "verification.md"))
	scoped := w25ReadLane(t, root, filepath.Join("workflows", "_shared", "scoped_verification.md"))
	for _, want := range []string{
		"release/current/tools/verify-scope",
		"~/.local/share/polyforge",
		"fast_gates_ref",
		"verification: scoped",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("the verification step text must carry %q", want)
		}
	}
	for _, want := range []string{
		"release/current/tools/verify-scope",
		"~/.local/share/polyforge",
		"$POLYFORGE_DATA_DIR",
		"mode: full",
	} {
		if !strings.Contains(scoped, want) {
			t.Errorf("the scoped-tier section must carry %q", want)
		}
	}
	for name, lane := range map[string]string{
		"the verification step text": step,
		"the scoped-tier section":    scoped,
	} {
		if strings.Contains(lane, "Invoke\n`tools/verify-scope`") || strings.Contains(lane, "Invoke `tools/verify-scope`") {
			t.Errorf("%s must name the release-tree path, not a clone-relative tools/verify-scope", name)
		}
		for _, forbidden := range []string{
			".polyforge/verify-scope.sh",
			"engine absent",
			"engine-absent",
			"old release",
			"legacy release",
		} {
			if strings.Contains(lane, forbidden) {
				t.Errorf("%s carries the forbidden compatibility wording %q", name, forbidden)
			}
		}
		for _, marker := range []string{"GMIS" + "WE", "ieops" + "-polyforge", "pi-web" + "-gmi"} {
			if strings.Contains(lane, marker) {
				t.Errorf("%s hard-codes the repository identifier %q", name, marker)
			}
		}
	}
}

// w25ReadLane reads one lane text file of the verification lane.
func w25ReadLane(t *testing.T, root string, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
