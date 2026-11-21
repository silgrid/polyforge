// carries: internal/polyforge/components/upgrade.go,
//
//	internal/polyforge/components/status.go
package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComponentsStageRefusesWhenTheStatusRecordCannotBeWritten pins
// AC2's fail-closed half: the status file IS the contract — an upgrade
// that cannot record on the volume refuses before anything runs, naming
// the record, and neither the pointers nor the daemon are touched.
func TestComponentsStageRefusesWhenTheStatusRecordCannotBeWritten(t *testing.T) {
	root, seed := upgradeFixture(t)
	// a directory where the status file must land: every write to it
	// fails, exactly a read-only or broken volume would.
	if err := os.Mkdir(filepath.Join(root, UpgradeStatusFilename), 0o755); err != nil {
		t.Fatal(err)
	}
	before := rootState(t, root)
	face := &fakeFace{}
	err := runUpgrade(t, root, seed, face, nil)
	if err == nil || !strings.Contains(err.Error(), UpgradeStatusFilename) {
		t.Fatalf("an unrecordable status file must refuse the upgrade naming the record: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused upgrade changes nothing:\n%s", got)
	}
	if len(face.calls) != 0 {
		t.Fatalf("a refused upgrade never reaches the daemon, got %v", face.callVerbs())
	}
}
