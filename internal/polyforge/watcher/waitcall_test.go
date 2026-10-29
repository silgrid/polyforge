package watcher

// waitcall_test.go: `pf watch <wi> --once` shows the current step's wait
// calls on one line, and only when there are some.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestOnceShowsTheStepsWaitCalls(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 4, 12, 0, time.UTC)
	snap := running("wi_1", "verification")
	tools, waits, secs := int64(12), int64(3), int64(240)
	snap.Progress = &protocol.WatchProgress{ToolCalls: &tools, WaitCalls: &waits, WaitSeconds: &secs}
	out := FormatOnce(snap, now, 0)
	if !strings.Contains(out, "tool calls: 12\nwait calls: 3 (240s)\n") {
		t.Fatalf("the step view must carry one wait-call line after the tool calls:\n%s", out)
	}
	snap.Progress = &protocol.WatchProgress{ToolCalls: &tools}
	if out := FormatOnce(snap, now, 0); strings.Contains(out, "wait calls") {
		t.Fatalf("no wait call, no line:\n%s", out)
	}
}
