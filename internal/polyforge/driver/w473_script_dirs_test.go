package driver

// w473_script_dirs_test.go: a dispatch names the lane
// snapshot its step runs — the capsule's workflows/<lane> — so the step
// process gets PF_LANE_DIR (and PF_SHARED_DIR beside it) and PF_CAPSULE
// (runtime/stepenv.go adds the variables from these params).

import (
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestDispatchNamesTheLaneSnapshot(t *testing.T) {
	p, capDir, _ := driftSpawn(t)
	if p.CapsuleDir != capDir {
		t.Fatalf("CapsuleDir %q, want the capsule %q", p.CapsuleDir, capDir)
	}
	if want := filepath.Join(capDir, capsule.DirWorkflows, "develop"); p.LaneDir != want {
		t.Fatalf("LaneDir %q, want the materialized lane %q", p.LaneDir, want)
	}
}
