package supervise

// w72_judgetask_test.go (polyforge#72, u3 — AC9): the task lane's rule
// subset. JudgeTask applies only rules 0, 2 and 3 (ruling 15); a turn
// that ended (the step rules 1/1b) or a cumulative-T overrun (rule 4)
// never kills a task — a task's one turn ends with its process, and its
// bound is the task's own wall/token budget. Expected verdicts are
// hand-built snapshots, the same shapes rules.go judges.

import (
	"testing"
	"time"
)

func w72Snapshot(th Thresholds, mutate func(*Snapshot)) Snapshot {
	now := time.Now()
	s := Snapshot{Begun: true, StartedAt: now, Thresholds: th}
	mutate(&s)
	return s
}

func TestW72JudgeTaskAppliesRules023(t *testing.T) {
	th := Thresholds{Silence: time.Minute, Tool: time.Minute}
	// rule 0: nothing since dispatch.
	s := w72Snapshot(th, func(s *Snapshot) {})
	if v := JudgeTask(s, s.StartedAt.Add(2*time.Minute)); v == nil || v.Cause != CauseHarnessSilent {
		t.Fatalf("rule 0 must fire on a silent dispatch: %+v", v)
	}
	// rule 2: waiting on the model, no output.
	s = w72Snapshot(th, func(s *Snapshot) {
		s.WorkSignals = 1
		s.Waiting = true
		s.WaitingSince = s.StartedAt
	})
	if v := JudgeTask(s, s.StartedAt.Add(2*time.Minute)); v == nil || v.Cause != CauseModelHung {
		t.Fatalf("rule 2 must fire on a hung model: %+v", v)
	}
	// rule 3: one tool running past the tool window.
	s = w72Snapshot(th, func(s *Snapshot) {
		s.WorkSignals = 1
		s.Tools = []ToolRun{{Name: "bash", Since: s.StartedAt}}
	})
	if v := JudgeTask(s, s.StartedAt.Add(2*time.Minute)); v == nil || v.Cause != CauseToolHung {
		t.Fatalf("rule 3 must fire on a hung tool: %+v", v)
	}
}

func TestW72JudgeTaskIgnoresStepOnlyRules(t *testing.T) {
	// An ended turn with no result and an error long past the grace, an
	// age far past a tiny StepTimeout: none of rules 1, 1b, 4 may fire —
	// the task's process owns its own ending.
	th := Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Minute}
	s := w72Snapshot(th, func(s *Snapshot) {
		s.WorkSignals = 1
		s.TurnEnded = true
		s.TurnEndedAt = s.StartedAt
		s.TurnError = true
		s.TurnErrAt = s.StartedAt
		s.TurnErrText = "boom"
	})
	if v := JudgeTask(s, s.StartedAt.Add(10*time.Minute)); v != nil {
		t.Fatalf("a task's ended turn is not a death (rules 1/1b are step-only): %+v", v)
	}
}

func TestW72JudgeSharesTheRules023(t *testing.T) {
	// Judge and JudgeTask answer the same verdict for the same rule-0/2/3
	// snapshot — one set of rule bodies, two entry points.
	th := Thresholds{Silence: time.Minute, Tool: time.Minute}
	s := w72Snapshot(th, func(s *Snapshot) {})
	now := s.StartedAt.Add(2 * time.Minute)
	vj, vt := Judge(s, now, DefaultModelErrorGrace), JudgeTask(s, now)
	if vj == nil || vt == nil || vj.Cause != vt.Cause || vj.Evidence != vt.Evidence {
		t.Fatalf("Judge and JudgeTask must agree on rules 0/2/3: %+v vs %+v", vj, vt)
	}
}
