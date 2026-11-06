package watcher

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

func running(wi, step string) *protocol.WatchSnapshot {
	return &protocol.WatchSnapshot{WorkItem: wi, Known: true,
		Execution: &protocol.WatchExecution{WorkflowStatus: "running", CurrentStep: step,
			Steps: map[string]protocol.WatchStep{step: {Status: "in_progress", StartedAt: "2026-10-12T10:00:00Z"}}}}
}

func ev(seq int64, kind string) protocol.EventEntry {
	return protocol.EventEntry{Seq: seq, Kind: kind, At: "2026-10-12T10:00:00Z"}
}

func TestOnceNamesTheWIBySlug(t *testing.T) {
	now := time.Date(2026, 10, 12, 10, 4, 12, 0, time.UTC)
	snap := running("wi_LRLfXwxPqtEw", "grill")
	snap.Slug = "polyforge#140"
	if once := FormatOnce(snap, now, 8); !strings.HasPrefix(once, "work item:  polyforge#140 (wi_LRLfXwxPqtEw)\n") {
		t.Fatalf("--once header: %q", once)
	}
	snap.Slug = ""
	if once := FormatOnce(snap, now, 8); !strings.HasPrefix(once, "work item:  wi_LRLfXwxPqtEw\n") {
		t.Fatalf("id fallback: %q", once)
	}
}

func TestSubmittedButUnregisteredWIShowsQueuedNotAnError(t *testing.T) {
	snap := &protocol.WatchSnapshot{WorkItem: "wi_1"} // nothing known yet
	once := FormatOnce(snap, time.Now(), 8)
	if !strings.Contains(once, "queued/preparing") || strings.Contains(strings.ToLower(once), "error") {
		t.Fatalf("once = %q", once)
	}
	// and a registered job with no execution.json yet is the same state
	snap.Known, snap.Job = true, &protocol.WatchJob{Status: "running"}
	if StateLabel(snap) != StateQueued {
		t.Fatalf("state = %q", StateLabel(snap))
	}
}

func TestOnceShowsQuestionTextOnExplicitRequest(t *testing.T) {
	snap := running("wi_1", "s1")
	snap.Execution.PendingQuestion = &protocol.WatchPendingQuestion{ID: "q-1", Step: "s1", Question: "Which database?"}
	snap.Events = []protocol.EventEntry{ev(3, "step_started")}
	out := FormatOnce(snap, time.Now(), 8)
	for _, want := range []string{"needs input", "q-1", "Which database?", "step_started"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestOnceStripsControlCharsFromQuestionText(t *testing.T) {
	snap := running("wi_1", "s1")
	snap.Execution.PendingQuestion = &protocol.WatchPendingQuestion{ID: "q-1", Step: "s1",
		Question: "Which\x1b[2J database?\r\nsecond\tline\x07\u009b31m"}
	out := FormatOnce(snap, time.Now(), 8)
	if strings.ContainsAny(out, "\x1b\x07\r\u009b") {
		t.Fatalf("control characters leaked into the --once output: %q", out)
	}
	if !strings.Contains(out, "Which[2J database?\n  second line31m") {
		t.Fatalf("question text must keep its line structure, got %q", out)
	}
}

// TestHubStatusOutranksAStaleExecution: a manual close leaves
// execution.json at running; the hub status decides the --once state.
func TestHubStatusOutranksAStaleExecution(t *testing.T) {
	snap := running("wi_1", "s1")
	snap.HubStatus = "canceled"
	if got := StateLabel(snap); got != "canceled" {
		t.Fatalf("state = %q", got)
	}
}
