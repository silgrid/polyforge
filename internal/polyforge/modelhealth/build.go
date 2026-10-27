package modelhealth

// build.go: the pure aggregation core. It reads the durable event entries
// (protocol.EventEntry) and answers the profile + warnings; it touches no
// file and imports no dispatch-facing package.
//
// Identity rule (workbench#14 spec, assumption 8): a failure's model is
// ALWAYS the most recent preceding step_started of the same work item and
// step in seq order. step_failed.candidate is self-description only; the
// candidate body field is deliberately not read here.

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

const (
	kindStepStarted = "step_started"
	kindStepFailed  = "step_failed"
	kindStepDone    = "step_done"
	kindGateVerdict = "gate_verdict"
	kindNeedsInput  = "needs_input"
	kindIncomplete  = "incomplete"
	kindUnhealthy   = "model_unhealthy"
)

type eventBody struct {
	Step   string `json:"step"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
	SpawnN int64  `json:"spawn_n"`
	Cause  string `json:"cause"`
}

type event struct {
	entry protocol.EventEntry
	at    time.Time
	body  eventBody
	seq   int64
}

type dispatchRec struct {
	model   string
	effort  string
	step    string
	at      time.Time
	seq     int64
	wi      string
	spawnN  int64
	closed  bool
	success bool
	wallMs  int64
}

type deathRec struct {
	model  string
	effort string
	step   string
	cause  string
	at     time.Time
	seq    int64
	tier   Tier
}

type built struct {
	profile  Profile
	alerts   []Alert
	reported map[string]bool
}

// Build answers the profile and the currently firing per-episode alerts.
func Build(entries []protocol.EventEntry, now time.Time, retention time.Duration) (Profile, []Alert) {
	r := build(entries, now, retention)
	return r.profile, r.alerts
}

// build is Build plus the alert seed read from model_unhealthy entries
// (self-description only; never a counting input).
func build(entries []protocol.EventEntry, now time.Time, retention time.Duration) built {
	windowStart := now.Add(-retention)
	events := windowEvents(entries, windowStart, now)

	// Group by work item and read in file/seq order: the start join must not
	// jump across a neighbouring work item or a neighbouring step.
	byWI := map[string][]event{}
	var wiOrder []string
	for _, e := range events {
		if _, ok := byWI[e.entry.WI]; !ok {
			wiOrder = append(wiOrder, e.entry.WI)
		}
		byWI[e.entry.WI] = append(byWI[e.entry.WI], e)
	}
	sort.SliceStable(wiOrder, func(i, j int) bool { return wiOrder[i] < wiOrder[j] })
	for _, wi := range wiOrder {
		es := byWI[wi]
		sort.SliceStable(es, func(i, j int) bool { return es[i].seq < es[j].seq })
		byWI[wi] = es
	}

	dispatches := map[string]*dispatchRec{}
	var dispatchList []*dispatchRec
	lastStart := map[string]*dispatchRec{} // wi\x00step -> latest preceding start
	var deaths []deathRec
	countedFailure := map[string]bool{}

	for _, wi := range wiOrder {
		for _, e := range byWI[wi] {
			key := dispatchKey(wi, e.body.Step, e.body.SpawnN)
			switch e.entry.Kind {
			case kindStepStarted:
				if e.body.Model == "" {
					// A start with no model identity cannot key a model+effort
					// cell; it is not a dispatch of any profile row.
					delete(lastStart, joinKey(wi, e.body.Step))
					continue
				}
				d := &dispatchRec{model: e.body.Model, effort: e.body.Effort, step: e.body.Step,
					at: e.at, seq: e.seq, wi: wi, spawnN: e.body.SpawnN}
				dispatches[key] = d
				dispatchList = append(dispatchList, d)
				lastStart[joinKey(wi, e.body.Step)] = d
			case kindStepFailed:
				start := lastStart[joinKey(wi, e.body.Step)]
				if start == nil {
					// No preceding start: never guess the identity from the
					// failure body, a neighbour or the current candidate list.
					continue
				}
				failKey := key
				if failKey == "" {
					failKey = joinKey(wi, e.body.Step) + "\x00" + e.entry.At
				}
				if countedFailure[failKey] {
					continue
				}
				countedFailure[failKey] = true
				deaths = append(deaths, deathRec{model: start.model, effort: start.effort,
					step: start.step, cause: e.body.Cause, at: e.at, seq: e.seq, tier: TierOf(e.body.Cause)})
				if !start.closed {
					start.closed = true // a failure closes the dispatch for wall time; it is not a success
					if wall := e.at.Sub(start.at).Milliseconds(); wall >= 0 {
						start.wallMs = wall
					}
				}
			case kindStepDone, kindGateVerdict, kindNeedsInput, kindIncomplete:
				if d := dispatches[key]; d != nil && !d.closed {
					d.closed = true
					d.success = e.entry.Kind != kindIncomplete
					if wall := e.at.Sub(d.at).Milliseconds(); wall >= 0 {
						d.wallMs = wall
					}
				}
			}
		}
	}

	// One cell per model+effort × step.
	type acc struct {
		cell         Cell
		wallSum      int64
		wallN        int
		lastDeathAt  time.Time
		lastDeathSeq int64
	}
	cells := map[string]*acc{}
	order := []string{}
	cellOf := func(model, effort, step string) *acc {
		k := joinKey(Key(model, effort), step)
		a := cells[k]
		if a == nil {
			a = &acc{cell: Cell{Model: model, Effort: effort, Step: step}}
			cells[k] = a
			order = append(order, k)
		}
		return a
	}
	for _, d := range dispatchList {
		a := cellOf(d.model, d.effort, d.step)
		a.cell.Dispatches++
		if d.success {
			a.cell.Successes++
		}
		if d.closed {
			a.wallSum += d.wallMs
			a.wallN++
		}
	}
	for _, d := range deaths {
		a := cellOf(d.model, d.effort, d.step)
		switch d.tier {
		case TierModel:
			a.cell.ModelDeaths++
		case TierNonModel:
			a.cell.NonModelDeaths++
		default:
			a.cell.ExcludedDeaths++
		}
		if d.tier != TierExcluded && (a.lastDeathAt.IsZero() || d.at.After(a.lastDeathAt) ||
			(d.at.Equal(a.lastDeathAt) && d.seq > a.lastDeathSeq)) {
			a.lastDeathAt, a.lastDeathSeq = d.at, d.seq
			a.cell.LastCause = d.cause
			a.cell.LastDeathAt = d.at.UTC().Format(time.RFC3339Nano)
		}
	}
	sort.Strings(order)
	profileCells := make([]Cell, 0, len(cells))
	for _, k := range order {
		a := cells[k]
		if a.cell.Dispatches > 0 {
			a.cell.Rate = float64(a.cell.ModelDeaths) / float64(a.cell.Dispatches)
		}
		if a.wallN > 0 {
			a.cell.MeanWallMs = a.wallSum / int64(a.wallN)
		}
		profileCells = append(profileCells, a.cell)
	}

	warnings := currentWarnings(dispatchList, deaths, now)
	profile := Profile{
		Window:   Window{Duration: retention.String(), From: windowStart.UTC().Format(time.RFC3339Nano), To: now.UTC().Format(time.RFC3339Nano)},
		Cells:    profileCells,
		Warnings: warnings,
	}
	if profile.Cells == nil {
		profile.Cells = []Cell{}
	}
	if profile.Warnings == nil {
		profile.Warnings = []Warning{}
	}

	// Seed "already reported" from the daemon-scope model_unhealthy events.
	reported := map[string]bool{}
	for _, e := range entries {
		if e.Kind != kindUnhealthy {
			continue
		}
		var a Alert
		if json.Unmarshal(e.Body, &a) != nil || a.Model == "" {
			continue
		}
		reported[alertKey(a.Model, a.Effort)] = true
	}
	return built{profile: profile, alerts: episodeAlerts(warnings), reported: reported}
}

// windowEvents keeps parseable entries strictly inside (start, now].
func windowEvents(entries []protocol.EventEntry, start, now time.Time) []event {
	out := make([]event, 0, len(entries))
	for _, e := range entries {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || !at.After(start) || at.After(now) {
			continue
		}
		var b eventBody
		if len(e.Body) > 0 {
			_ = json.Unmarshal(e.Body, &b)
		}
		out = append(out, event{entry: e, at: at, body: b, seq: e.Seq})
	}
	return out
}

func joinKey(a, b string) string { return a + "\x00" + b }

func dispatchKey(wi, step string, spawn int64) string {
	if spawn <= 0 {
		return ""
	}
	return wi + "\x00" + step + "\x00" + itoa(spawn)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// currentWarnings applies the two constant rules:
//   - >= 3 model deaths on one model+effort inside (now-24h, now];
//   - > 20% model deaths with >= 5 dispatches on one model+effort × step
//     inside (now-7d, now].
func currentWarnings(dispatches []*dispatchRec, deaths []deathRec, now time.Time) []Warning {
	w24, w7 := now.Add(-Window24h), now.Add(-Window7d)
	deaths24 := map[string]int{}
	deaths7 := map[string]int{}
	disp7 := map[string]int{}
	model7 := map[string]string{} // cell key -> model
	effort7 := map[string]string{}
	step7 := map[string]string{}
	for _, d := range deaths {
		if d.tier != TierModel {
			continue
		}
		if d.at.After(w24) {
			deaths24[alertKey(d.model, d.effort)]++
		}
		if d.at.After(w7) {
			k := joinKey(Key(d.model, d.effort), d.step)
			deaths7[k]++
			model7[k], effort7[k], step7[k] = d.model, d.effort, d.step
		}
	}
	for _, d := range dispatches {
		if !d.at.After(w7) {
			continue
		}
		k := joinKey(Key(d.model, d.effort), d.step)
		disp7[k]++
		model7[k], effort7[k], step7[k] = d.model, d.effort, d.step
	}
	var out []Warning
	for k, n := range deaths24 {
		if n < WarnDeaths24h {
			continue
		}
		model, effort := splitAlertKey(k)
		out = append(out, Warning{Model: model, Effort: effort, Rule: RuleDeaths24h, ModelDeaths: n})
	}
	for k, n := range deaths7 {
		disp := disp7[k]
		if disp < WarnRateMinDispatches {
			continue
		}
		rate := float64(n) / float64(disp)
		if rate <= WarnRate {
			continue
		}
		out = append(out, Warning{Model: model7[k], Effort: effort7[k], Step: step7[k],
			Rule: RuleRate7d, ModelDeaths: n, Dispatches: disp, Rate: rate})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		if a.Effort != b.Effort {
			return a.Effort < b.Effort
		}
		if a.Step != b.Step {
			return a.Step < b.Step
		}
		return a.Rule < b.Rule
	})
	return out
}

// episodeAlerts folds the current warnings into one alert per model+effort
// episode: the event is edge-triggered per episode, not per row.
func episodeAlerts(warnings []Warning) []Alert {
	by := map[string]*Alert{}
	var order []string
	for _, w := range warnings {
		k := alertKey(w.Model, w.Effort)
		a := by[k]
		if a == nil {
			a = &Alert{Model: w.Model, Effort: w.Effort}
			by[k] = a
			order = append(order, k)
		}
		if a.Rule == "" {
			a.Rule = w.Rule
		}
		if !contains(a.Rules, w.Rule) {
			a.Rules = append(a.Rules, w.Rule)
		}
		if w.ModelDeaths > a.ModelDeaths {
			a.ModelDeaths = w.ModelDeaths
		}
		if w.Dispatches > a.Dispatches {
			a.Dispatches = w.Dispatches
		}
		if w.Rate > a.Rate {
			a.Rate = w.Rate
		}
	}
	sort.Strings(order)
	out := make([]Alert, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

func alertKey(model, effort string) string { return Key(model, effort) }

func splitAlertKey(k string) (string, string) {
	for i := 0; i < len(k); i++ {
		if k[i] == ':' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
