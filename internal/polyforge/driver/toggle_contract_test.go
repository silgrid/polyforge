package driver

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// TestPiCapabilityFactOnlyTargetsPi: the resume capability fact is one
// function over the VERIFIED capability (owner ruling Q3, polyforge#215):
// on names the daemon worker and one-task dispatch, off keeps the main path,
// and both say this dispatch replaces the earlier statement. The harness
// gate lives at the prompt level (a claude dispatch never receives it), in
// the resume matrix.
func TestPiCapabilityFactOnlyTargetsPi(t *testing.T) {
	on := piCapabilityFact(spawn.Capability{PiSubagents: true})
	for _, want := range []string{`agent: "worker"`, "one task at a time", "replaces the one stated in any earlier dispatch", "the capability the runtime verified"} {
		if !strings.Contains(on, want) {
			t.Errorf("an enabled resume fact must carry %q: %q", want, on)
		}
	}
	off := piCapabilityFact(spawn.Capability{})
	for _, want := range []string{"`implemented_by: main`", "do not dispatch a worker", "replaces the one stated in any earlier dispatch", "the capability the runtime verified"} {
		if !strings.Contains(off, want) {
			t.Errorf("a disabled resume fact must carry %q: %q", want, off)
		}
	}
	if strings.Contains(off, `agent: "worker"`) {
		t.Errorf("a disabled resume fact must not name a worker dispatch: %q", off)
	}
}