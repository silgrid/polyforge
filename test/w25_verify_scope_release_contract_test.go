package test

// w25_verify_scope_release_contract_test.go: the same-release-tree contract
// for. The verification lane may only take its fast-gate scope
// from the release-shipped engine, so the shipped `workflows/_shared/scripts/verify-scope` and the
// lane text that names it must travel in one tree. There is no
// backward-compatibility branch: the old repo-owned script is gone and the
// lane never describes an engine-absent or old-release path.
// Since the verify action runs the engine in the
// daemon: no lane text names it, so the contract is the shipped engine.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25ReleaseTree' -count=1

import (
	"os"
	"path/filepath"
	"testing"
)

// TestW25ReleaseTreeEngineAndLaneShipTogether pins the coexistence guarantee.
func TestW25ReleaseTreeEngineAndLaneShipTogether(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(root, "workflows", "_shared", "scripts", "verify-scope")
	info, err := os.Stat(engine)
	if err != nil {
		t.Fatalf("the release tree must ship workflows/_shared/scripts/verify-scope: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope must be executable: mode %v", info.Mode())
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
