package supervise

// rules.go: the supervisor's judging rules (v2-converge 2.3). Pure: a
// snapshot, a clock reading and the bounds in, at most one verdict out.
// When several rules fire at once the lowest-numbered one wins.

import (
	"fmt"
	"time"
)

// DefaultModelErrorGrace is rule 1's grace after an ended turn (error or
// ok) with no result file.
const DefaultModelErrorGrace = time.Minute

// Judge applies the rules in priority order and answers the verdict of
// the first that fires; nil = the dispatch is healthy (or not judgeable:
// not begun, or its process already exited — the driver owns exits).
func Judge(s Snapshot, now time.Time, grace time.Duration) *StepDead {
	if !s.Begun || s.Exit != nil {
		return nil
	}
	if grace <= 0 {
		grace = DefaultModelErrorGrace
	}
	th := s.Thresholds
	dead := func(c Cause, evidence string) *StepDead {
		return &StepDead{Step: s.Step, SpawnN: s.SpawnN, Cause: c, Evidence: evidence}
	}
	age := now.Sub(s.StartedAt)

	// 0: harness_silent — nothing but silence (or UI noise) since dispatch.
	if th.Silence > 0 && s.WorkSignals == 0 && age >= th.Silence {
		return dead(CauseHarnessSilent, fmt.Sprintf(
			"no work-class signal within %s of dispatch (%d non-work records seen: UI chatter and echoes never count as work)",
			th.Silence.Round(time.Second), s.NoiseCount))
	}
	// 1: model_error — the turn ended on an error and no result followed.
	if s.TurnError && now.Sub(s.TurnErrAt) >= grace {
		v := dead(CauseModelError, fmt.Sprintf(
			"turn ended with an error %s ago and no result file followed within the %s grace: %s",
			now.Sub(s.TurnErrAt).Round(time.Second), grace.Round(time.Second), s.TurnErrText))
		v.TurnError = s.TurnErrText
		return v
	}
	// 1b: ended_without_result — the turn ended normally and no result
	// file followed within the grace: the agent stopped without
	// delivering. Same-model retry (the retry prompt says so).
	if s.TurnEnded && !s.TurnError && !s.TurnEndedAt.IsZero() && now.Sub(s.TurnEndedAt) >= grace {
		return dead(CauseEndedWithoutResult, fmt.Sprintf(
			"turn ended normally %s ago and no result file followed within the %s grace",
			now.Sub(s.TurnEndedAt).Round(time.Second), grace.Round(time.Second)))
	}
	// 2: model_hung — waiting for the model, no output for X.
	if th.Silence > 0 && s.Waiting {
		since := s.WaitingSince
		if s.LastOutput.After(since) {
			since = s.LastOutput
		}
		if now.Sub(since) >= th.Silence {
			return dead(CauseModelHung, fmt.Sprintf(
				"waiting for the model for %s with no output (bound %s)",
				now.Sub(since).Round(time.Second), th.Silence.Round(time.Second)))
		}
	}
	// 3: tool_hung — one tool running longer than Y.
	if th.Tool > 0 && len(s.Tools) > 0 {
		oldest := s.Tools[0]
		if run := now.Sub(oldest.Since); run >= th.Tool {
			return dead(CauseToolHung, fmt.Sprintf(
				"tool %q still running after %s (bound %s); args: %s",
				oldest.Name, run.Round(time.Second), th.Tool.Round(time.Second), oldest.Args))
		}
	}
	// 4: step_timeout — CUMULATIVE step runtime over T.
	if th.StepTimeout > 0 {
		total := th.PriorRuntime + age
		if total >= th.StepTimeout {
			cause := CauseStepTimeoutLongTask
			if s.ModelTime > s.ToolTime {
				cause = CauseStepTimeoutSlowModel
			}
			return dead(cause, fmt.Sprintf(
				"step ran %s cumulatively (%s before this dispatch + %s now), bound %s; time went to tools %s, waiting on the model %s",
				total.Round(time.Second), th.PriorRuntime.Round(time.Second), age.Round(time.Second),
				th.StepTimeout.Round(time.Second), s.ToolTime.Round(time.Second), s.ModelTime.Round(time.Second)))
		}
	}
	return nil
}
