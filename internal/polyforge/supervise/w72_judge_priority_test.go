// carries: internal/polyforge/supervise/rules.go

package supervise

// w72_judge_priority_test.go (polyforge#72, u14 — review r1#1): splitting
// out the task lane's rules 0/2/3 must not reorder the STEP lane's rules.
// rules.go's own contract: when several rules fire at once the
// lowest-numbered one wins — 0 → 1 → 1b → 2 → 3 → 4 (the ticket's
// non-goal: the step supervision rules are unchanged). Expected causes
// follow from that order, on hand-built snapshots where two rules fire.

import (
	"testing"
	"time"
)

func TestW72JudgeLowestRuleWins(t *testing.T) {
	th := Thresholds{Silence: time.Minute, Tool: time.Minute}
	start := time.Unix(10000, 0)
	now := start.Add(time.Hour)
	grace := time.Minute
	// 1b and 3 at once: the turn ended normally past the grace, and a tool
	// (as replayed on a re-attach) still runs past Y.
	s := Snapshot{Begun: true, StartedAt: start, Thresholds: th, WorkSignals: 3,
		TurnEnded: true, TurnEndedAt: start.Add(time.Minute),
		Tools: []ToolRun{{ID: "t1", Name: "Bash", Since: start}}}
	if v := Judge(s, now, grace); v == nil || v.Cause != CauseEndedWithoutResult {
		t.Fatalf("1b and 3 both fire: verdict %+v, want ended_without_result (rule 1b outranks 3)", v)
	}
	// 1 and 2 at once: the turn ended on an error past the grace while the
	// tracker still reads as waiting on the model.
	s = Snapshot{Begun: true, StartedAt: start, Thresholds: th, WorkSignals: 3,
		TurnError: true, TurnErrAt: start.Add(time.Minute), TurnErrText: "boom",
		Waiting: true, WaitingSince: start}
	if v := Judge(s, now, grace); v == nil || v.Cause != CauseModelError {
		t.Fatalf("1 and 2 both fire: verdict %+v, want model_error (rule 1 outranks 2)", v)
	}
	// the task lane keeps its subset: the same 1b+3 snapshot is a tool hang
	// for a task (1b is step-only)
	s = Snapshot{Begun: true, StartedAt: start, Thresholds: th, WorkSignals: 3,
		TurnEnded: true, TurnEndedAt: start.Add(time.Minute),
		Tools: []ToolRun{{ID: "t1", Name: "Bash", Since: start}}}
	if v := JudgeTask(s, now); v == nil || v.Cause != CauseToolHung {
		t.Fatalf("JudgeTask: verdict %+v, want tool_hung", v)
	}
}
