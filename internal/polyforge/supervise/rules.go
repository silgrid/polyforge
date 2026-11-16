package supervise

// rules.go: the supervisor's judging rules (v2-converge 2.3). Pure: a
// snapshot, a clock reading and the bounds in, at most one verdict out.
// When several rules fire at once the lowest-numbered one wins.

import (
	"fmt"
	"slices"
	"time"
)

// DefaultModelErrorGrace is rule 1's grace after an ended turn (error or
// ok) with no hand-off (`pf step handoff`).
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

	if v := judgeRule0(s, dead, age); v != nil {
		return v
	}
	// 1: model_error — the turn ended on an error and no result followed.
	if s.TurnError && now.Sub(s.TurnErrAt) >= grace {
		v := dead(CauseModelError, fmt.Sprintf(
			"turn ended with an error %s ago and no `pf step handoff` followed within the %s grace: %s",
			now.Sub(s.TurnErrAt).Round(time.Second), grace.Round(time.Second), s.TurnErrText))
		v.TurnError = s.TurnErrText
		return v
	}
	// 1b: ended_without_result — the turn ended normally and no result
	// file followed within the grace: the agent stopped without
	// delivering. Same-model retry (the retry prompt says so).
	if s.TurnEnded && !s.TurnError && !s.TurnEndedAt.IsZero() && now.Sub(s.TurnEndedAt) >= grace {
		return dead(CauseEndedWithoutResult, fmt.Sprintf(
			"turn ended normally %s ago and no `pf step handoff` followed within the %s grace",
			now.Sub(s.TurnEndedAt).Round(time.Second), grace.Round(time.Second)))
	}
	if v := judgeRules23(s, now, dead); v != nil {
		return v
	}
	// 4: step_timeout — CUMULATIVE step runtime over T.
	if th.StepTimeout > 0 {
		total := th.PriorRuntime + age
		if total >= th.StepTimeout {
			cause := CauseStepTimeoutLongTask
			if slowModelOwnsTimeout(age, s.ModelCallLatencies, th) {
				cause = CauseStepTimeoutSlowModel
			}
			med, p90 := slowLatencyThresholds(th)
			return dead(cause, fmt.Sprintf(
				"step ran %s cumulatively (%s before this dispatch + %s now), bound %s; this dispatch ran %d%% of the bound and completed %d model calls (median %s, p90 %s); the slow-model cause needs at least 50%% of the bound and, from at least %d completed calls, a median over %s or a p90 over %s",
				total.Round(time.Second), th.PriorRuntime.Round(time.Second), age.Round(time.Second),
				th.StepTimeout.Round(time.Second), percentOf(age, th.StepTimeout),
				len(s.ModelCallLatencies), medianDuration(s.ModelCallLatencies).Round(time.Millisecond), p90Duration(s.ModelCallLatencies).Round(time.Millisecond),
				MinSlowModelCalls, med, p90))
		}
	}
	return nil
}

// JudgeTask applies the task lane's subset of the rules (polyforge#72,
// AC9/ruling 15): 0 (harness_silent), 2 (model_hung), 3 (tool_hung)
// only. Rules 1/1b (a step's turn-end grace) and 4 (the cumulative step
// bound T) are step-only — a task's one turn ends with its process and
// its bound is its own wall/token budget. The rule bodies are Judge's,
// one definition each.
func JudgeTask(s Snapshot, now time.Time) *StepDead {
	if !s.Begun || s.Exit != nil {
		return nil
	}
	dead := func(c Cause, evidence string) *StepDead {
		return &StepDead{Step: s.Step, SpawnN: s.SpawnN, Cause: c, Evidence: evidence}
	}
	if v := judgeRule0(s, dead, now.Sub(s.StartedAt)); v != nil {
		return v
	}
	return judgeRules23(s, now, dead)
}

// judgeRule0 and judgeRules23 are the rule bodies a task and a step
// share, one definition each; each entry point calls them in its own
// lowest-numbered-first order (a step: 0, 1, 1b, 2, 3, 4; a task: 0, 2, 3).
func judgeRule0(s Snapshot, dead func(Cause, string) *StepDead, age time.Duration) *StepDead {
	th := s.Thresholds
	// 0: harness_silent — nothing but silence (or UI noise) since dispatch.
	if th.Silence > 0 && s.WorkSignals == 0 && age >= th.Silence {
		return dead(CauseHarnessSilent, fmt.Sprintf(
			"no work-class signal within %s of dispatch (%d non-work records seen: UI chatter and echoes never count as work)",
			th.Silence.Round(time.Second), s.NoiseCount))
	}
	return nil
}

func judgeRules23(s Snapshot, now time.Time, dead func(Cause, string) *StepDead) *StepDead {
	th := s.Thresholds
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
	// 3: tool_hung — one tool running longer than Y; a sub-agent call
	// (one reporting inner activity) silent for longer than Y since its
	// newest inner record, however long it ran in total.
	if tool, ok := s.StalestTool(); th.Tool > 0 && ok {
		if run := now.Sub(tool.ClockStart()); run >= th.Tool {
			if tool.LastActive.IsZero() {
				return dead(CauseToolHung, fmt.Sprintf(
					"tool %q still running after %s (bound %s); args: %s",
					tool.Name, run.Round(time.Second), th.Tool.Round(time.Second), tool.Args))
			}
			return dead(CauseToolHung, fmt.Sprintf(
				"sub-agent call %q silent for %s since its last inner activity, running %s in total (bound %s); args: %s",
				tool.Name, run.Round(time.Second), now.Sub(tool.Since).Round(time.Second), th.Tool.Round(time.Second), tool.Args))
		}
	}
	return nil
}

// slowModelOwnsTimeout is rule 4's attribution (D1 + polyforge#51): T is
// CUMULATIVE over the whole step, so the dispatch running when it runs out
// may have spent only a sliver of it. That dispatch is the slow-model cause
// (step_timeout_slow_model: a model-class death that switches the candidate)
// only when BOTH hold:
//
//   - it ran at least half of T itself (2·age ≥ T). Below half, the earlier
//     dispatches together spent more of the bound than this one did, so a
//     switch would charge the step's history to whichever model happened
//     to be running and throw away its conversation (switched
//     out after 12m38s of a 1h30m T, 1h17m22s of which came before it).
//     Half is the least share that makes this dispatch the largest single
//     consumer of the bound; "at least" keeps a dispatch that ran exactly
//     half on the blamed side;
//
//   - its COMPLETED model calls were individually slow: at least
//     MinSlowModelCalls of them, and their median over the median bound or
//     their p90 over the p90 bound (the [daemon] keys
//     slow_model_median_latency / slow_model_p90_latency, defaults 60s /
//     180s). The call in progress counts nowhere. This replaced the former
//     waiting-share condition (polyforge#51): a dispatch of many fast
//     calls — polyforge#25's 652 calls, median 6s, p90 15s — spent over
//     half its clock waiting on a perfectly normal model and was
//     misjudged slow; the share said how MUCH it waited, the latency says
//     whether the model was slow.
//
// Every other expiry is step_timeout_long_task: the step is long, so the
// same candidate retries under the doubled T (the first retry resumes its
// conversation) and a second long-task timeout blocks. A successor after
// a timeout relaxation starts with the previous bound already spent, so it
// holds about half of the doubled T and a later expiry is in practice
// judged long_task — but only in practice: a successor whose tick
// overshoot exceeds its predecessor's teardown reaches the half. The
// at-most-once rotation is therefore the driver's, not this rule's: a
// second slow-model timeout before the step's timeout ledger resets is
// archived as step_timeout_long_task (applyStepFailedLocked,
// S3).
func slowModelOwnsTimeout(age time.Duration, latencies []time.Duration, th Thresholds) bool {
	if age <= 0 || 2*age < th.StepTimeout {
		return false
	}
	if len(latencies) < MinSlowModelCalls {
		return false
	}
	med, p90 := slowLatencyThresholds(th)
	return medianDuration(latencies) > med || p90Duration(latencies) > p90
}

// slowLatencyThresholds answers the slow-model bounds in force, zero
// values taking the documented default (a dispatch started before the keys
// existed, or a test's bare Thresholds, judges by the defaults).
func slowLatencyThresholds(th Thresholds) (median, p90 time.Duration) {
	median, p90 = th.SlowModelMedianLatency, th.SlowModelP90Latency
	if median <= 0 {
		median = DefaultSlowModelMedianLatency
	}
	if p90 <= 0 {
		p90 = DefaultSlowModelP90Latency
	}
	return median, p90
}

// medianDuration answers the median of the completed calls' latencies
// (the mean of the two middle values for an even count; 0 for none).
func medianDuration(l []time.Duration) time.Duration {
	if len(l) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), l...)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// p90Duration answers the smallest value at or above the 90th percentile
// of the completed calls' latencies (0 for none).
func p90Duration(l []time.Duration) time.Duration {
	if len(l) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), l...)
	slices.Sort(s)
	idx := (len(s)*9+9)/10 - 1 // nearest rank: ceil(0.9·n) - 1, in ints
	return s[idx]
}

// percentOf answers part as an integer percentage of whole (0 when whole
// is not positive), for the human-facing evidence. Floating point, so a
// relaxed bound of many hours cannot overflow the nanosecond product.
func percentOf(part, whole time.Duration) int64 {
	if whole <= 0 {
		return 0
	}
	return int64(float64(part) / float64(whole) * 100)
}
