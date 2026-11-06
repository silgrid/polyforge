package watcher

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// `pf watch --once` shows the rewind tally once there is any:
// the automatic rewinds against their bound and the owner's (a hand-off
// citing the owner), charged to no budget, apart; none while both are zero.
func TestW420OnceRendersTheRewindTallyApart(t *testing.T) {
	s := &protocol.WatchSnapshot{WorkItem: "wi_1", Execution: &protocol.WatchExecution{WorkflowStatus: "running", CurrentStep: "plan",
		Rewinds: &protocol.WatchRewinds{Agent: 1, MaxRewinds: 4, Owner: 2}}}
	if out := FormatOnce(s, time.Now(), 5); !strings.Contains(out, "rewinds:    agent 1/4 (max_rewinds), owner-initiated 2 (charged to no budget)") {
		t.Fatalf("--once must show the two counts apart:\n%s", out)
	}
	s.Execution.Rewinds = &protocol.WatchRewinds{MaxRewinds: 4}
	if out := FormatOnce(s, time.Now(), 5); strings.Contains(out, "rewinds:") {
		t.Fatalf("no rewind yet, no line:\n%s", out)
	}
}
