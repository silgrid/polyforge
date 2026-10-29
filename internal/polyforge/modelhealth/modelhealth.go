// Package modelhealth aggregates the daemon's durable events stream into a
// per-model+effort × step profile and the constant-threshold warnings the
// operator faces read.
//
// The package is a pure read-side consumer: Build has no I/O and no
// dispatch-facing import, the tracker (tracker.go) persists the aggregate to
// <DataDir>/model-health.json through atomicfile and rebuilds it from
// events/*.jsonl, and nothing here ever edits a candidate list or a queue.
package modelhealth

import "time"

// The profile's window is the events-retention window (one rolling
// duration, stated in every output). The thresholds are compiled constants:
// there is deliberately no config key for any of them.
const (
	// WarnDeaths24h is the model+effort death count that warns inside 24h.
	WarnDeaths24h = 3
	// WarnRate is the 7-day model-death rate that warns (strictly above).
	WarnRate = 0.20
	// WarnRateMinDispatches is the sample floor of the 7-day rate rule.
	WarnRateMinDispatches = 5
	// Window24h and Window7d are the two rolling alert windows.
	Window24h = 24 * time.Hour
	Window7d  = 7 * 24 * time.Hour
)

// Rule names carried by Warning / Alert.
const (
	RuleDeaths24h = "3 model deaths in 24h"
	RuleRate7d    = "7-day model death rate"
)

// Window states the rolling interval a profile covers.
type Window struct {
	Duration string `json:"duration"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// Cell is one model+effort × step row of the profile.
type Cell struct {
	Model          string  `json:"model"`
	Effort         string  `json:"effort,omitempty"`
	Step           string  `json:"step"`
	Dispatches     int     `json:"dispatches"`
	Successes      int     `json:"successes"`
	ModelDeaths    int     `json:"model_deaths"`
	NonModelDeaths int     `json:"non_model_deaths"`
	ExcludedDeaths int     `json:"excluded_deaths"`
	Rate           float64 `json:"rate"`
	MeanWallMs     int64   `json:"mean_wall_ms"`
	LastCause      string  `json:"last_cause,omitempty"`
	LastDeathAt    string  `json:"last_death_at,omitempty"`
}

// Warning is one current threshold breach, as displayed by `pf ops status`
// and `pf doctor`. Model+effort identifies the episode; a 24h warning spans
// steps (Step empty) while a 7-day rate warning is per cell.
type Warning struct {
	Model       string  `json:"model"`
	Effort      string  `json:"effort,omitempty"`
	Step        string  `json:"step,omitempty"`
	Rule        string  `json:"rule"`
	ModelDeaths int     `json:"model_deaths"`
	Dispatches  int     `json:"dispatches,omitempty"`
	Rate        float64 `json:"rate,omitempty"`
}

// Alert is one unhealthy episode's event body (daemon-scope
// model_unhealthy). Rules names every threshold that fired for the episode.
type Alert struct {
	Model       string   `json:"model"`
	Effort      string   `json:"effort,omitempty"`
	Rule        string   `json:"rule"`
	Rules       []string `json:"rules,omitempty"`
	ModelDeaths int      `json:"model_deaths"`
	Dispatches  int      `json:"dispatches,omitempty"`
	Rate        float64  `json:"rate,omitempty"`
}

// Profile is the read face: the window, one row per model+effort × step and
// the currently firing warnings.
type Profile struct {
	Window   Window    `json:"window"`
	Cells    []Cell    `json:"cells"`
	Warnings []Warning `json:"warnings"`
}

// Summary is the additive `model_health` object of GET /status.
type Summary struct {
	Window   Window    `json:"window"`
	Warnings []Warning `json:"warnings"`
}

// Key renders a model+effort key: the model alone when the effort is empty,
// never a trailing ':' or a placeholder.
func Key(model, effort string) string {
	if effort == "" {
		return model
	}
	return model + ":" + effort
}

// Tier is a death's attribution class.
type Tier int

const (
	// TierExcluded: neither the model nor the step is blamed.
	TierExcluded Tier = iota
	// TierModel: counted as a model death.
	TierModel
	// TierNonModel: displayed apart, never in the model rate.
	TierNonModel
)

// TierOf is the closed attribution table. An unclassified cause is
// non-model, never a model death.
func TierOf(cause string) Tier {
	switch cause {
	case "model_error", "model_hung", "step_timeout_slow_model", "exit_model_class", "ended_without_result":
		return TierModel
	case "rate_limited", "upstream_transient", "exit_host_class", "driver_stalled", "daemon_restart", "driver_relaunch":
		return TierExcluded
	case "tool_hung", "harness_silent", "step_timeout_long_task", "exit_other", "result_invalid":
		return TierNonModel
	default:
		return TierNonModel
	}
}
