package watcher

// uncounted_rewinds_render_test.go (polyforge#67): `pf watch --once`
// shows the lane-declared uncounted class's tally apart — beside the
// automatic rewinds against max_rewinds and the owner's charged to no
// budget, the class's own count against its own cap, and nothing of it
// while the class has yet to rewind.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// TestUncountedRewindsRenderApartInWatch: the rewind tally line carries
// the lane-declared uncounted class's own count and cap as soon as the
// class has one rewind on record, and drops the segment while it has
// none — a lane without the declaration never shows it.
func TestUncountedRewindsRenderApartInWatch(t *testing.T) {
	s := &protocol.WatchSnapshot{WorkItem: "wi_1", Execution: &protocol.WatchExecution{WorkflowStatus: "running", CurrentStep: "merge",
		Rewinds: &protocol.WatchRewinds{Agent: 1, MaxRewinds: 4, Owner: 2, Uncounted: 1, MaxUncountedRewinds: 2}}}
	out := FormatOnce(s, time.Now(), 5)
	if !strings.Contains(out, "rewinds:    agent 1/4 (max_rewinds), owner-initiated 2 (charged to no budget), lane-declared uncounted 1/2") {
		t.Fatalf("--once must show the class's own tally apart:\n%s", out)
	}
	s.Execution.Rewinds = &protocol.WatchRewinds{MaxRewinds: 4, Uncounted: 0, MaxUncountedRewinds: 2}
	if out = FormatOnce(s, time.Now(), 5); strings.Contains(out, "uncounted") {
		t.Fatalf("no uncounted rewind yet, no segment:\n%s", out)
	}
	if out = FormatOnce(s, time.Now(), 5); strings.Contains(out, "rewinds:") {
		t.Fatalf("no rewind at all, no line:\n%s", out)
	}
}
