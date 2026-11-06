package supervise

import "time"

// Defaults are the global supervision bounds (daemon config / flags,
// v2-converge 2.4). The only per-step override is workflow.yaml
// budget.wall_minutes (an estimate: T = max(3 x wall_minutes, StepTimeout));
// X and Y are global (polyforge#147 deleted silence_minutes/tool_minutes).
// Zero fields take the documented default in Fill.
type Defaults struct {
	// Silence is X. Zero = by the harness's declared output granularity
	// (DefaultSilence: 10m token-level, 30m message-level); a non-zero
	// value applies to every harness.
	Silence time.Duration
	// Tool is Y (default 30m, the #83 lesson).
	Tool time.Duration
	// StepTimeout is T, cumulative per step (default 90m, the #117
	// lesson).
	StepTimeout time.Duration
	// DriverStall is N: the driver between steps (not waiting on a
	// human) for longer than this is stalled (default 10m).
	DriverStall time.Duration
	// ModelErrorGrace is rule 1's grace after an error-ended turn
	// (default 1m).
	ModelErrorGrace time.Duration
	// AskWarmWindow is how long a step process that asked the owner (`pf
	// ask`) is kept resident for the reply before the daemon ends it; the
	// step stays awaiting the owner and a later reply resumes the session
	// (workflow contract v2 §5.2; default DefaultAskWarmWindow; [daemon]
	// ask_warm_window).
	AskWarmWindow time.Duration
}

// DefaultAskWarmWindow is the documented warm window of a step awaiting
// the owner: ~15 minutes, then the idle process exits (owner ruling Q9).
const DefaultAskWarmWindow = 15 * time.Minute

// Fill answers the defaults with every zero field replaced (Silence
// stays zero: it is resolved per harness by SilenceFor).
func (d Defaults) Fill() Defaults {
	if d.Tool <= 0 {
		d.Tool = 30 * time.Minute
	}
	if d.StepTimeout <= 0 {
		d.StepTimeout = 90 * time.Minute
	}
	if d.DriverStall <= 0 {
		d.DriverStall = 10 * time.Minute
	}
	if d.ModelErrorGrace <= 0 {
		d.ModelErrorGrace = DefaultModelErrorGrace
	}
	if d.AskWarmWindow <= 0 {
		d.AskWarmWindow = DefaultAskWarmWindow
	}
	return d
}

// SilenceFor answers X for a harness granularity.
func (d Defaults) SilenceFor(g Granularity) time.Duration {
	if d.Silence > 0 {
		return d.Silence
	}
	return DefaultSilence(g)
}
