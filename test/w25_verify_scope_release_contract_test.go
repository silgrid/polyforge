package test

// w25_verify_scope_release_contract_test.go: the same-release-tree contract
// for workbench#25. The verification lane may only take its fast-gate scope
// from the release-shipped engine, so the shipped `tools/verify-scope` and the
// lane text that names it must travel in one tree. There is no
// backward-compatibility branch: the old repo-owned script is gone and the
// lane never describes an engine-absent or old-release path.
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
	raw, err := os.ReadFile(filepath.Join(root, "workflows", "_shared", "steps", "verification.md"))
	if err != nil {
		t.Fatal(err)
	}
	lane := string(raw)
	for _, want := range []string{
		"release/current/tools/verify-scope",
		"~/.local/share/polyforge",
		"$POLYFORGE_DATA_DIR",
		"fast_gates_ref",
		"verification: scoped",
		"mode: full",
	} {
		if !strings.Contains(lane, want) {
			t.Fatalf("the verification lane must carry %q", want)
		}
	}
	if strings.Contains(lane, "Invoke\n`tools/verify-scope`") || strings.Contains(lane, "Invoke `tools/verify-scope`") {
		t.Fatalf("the verification lane must name the release-tree path, not a clone-relative tools/verify-scope")
	}
	for _, forbidden := range []string{
		".polyforge/verify-scope.sh",
		"engine absent",
		"engine-absent",
		"old release",
		"legacy release",
	} {
		if strings.Contains(lane, forbidden) {
			t.Fatalf("the verification lane carries the forbidden compatibility wording %q", forbidden)
		}
	}
	for _, marker := range []string{"GMIS" + "WE", "ieops" + "-polyforge", "pi-web" + "-gmi"} {
		if strings.Contains(lane, marker) {
			t.Fatalf("the verification lane hard-codes the repository identifier %q", marker)
		}
	}
}
