package modelhealth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

var w14Now = time.Date(2026, 10, 25, 3, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time { return w14Now.Add(offset) }

func entry(wi, kind string, seq int64, when time.Time, body map[string]any) protocol.EventEntry {
	raw, _ := json.Marshal(body)
	return protocol.EventEntry{At: when.Format(time.RFC3339Nano), Kind: kind, WI: wi, Seq: seq, Body: raw}
}

func startBody(model, effort, step string, spawn int64) map[string]any {
	b := map[string]any{"step": step, "model": model, "spawn_n": spawn}
	if effort != "" {
		b["effort"] = effort
	}
	return b
}

func startEvent(wi string, seq int64, when time.Time, model, effort, step string, spawn int64) protocol.EventEntry {
	return entry(wi, kindStepStarted, seq, when, startBody(model, effort, step, spawn))
}

func failEvent(wi string, seq int64, when time.Time, step, cause string, spawn int64) protocol.EventEntry {
	return entry(wi, kindStepFailed, seq, when, map[string]any{"step": step, "cause": cause, "spawn_n": spawn})
}

func closeEvent(wi string, seq int64, when time.Time, kind, step string, spawn int64) protocol.EventEntry {
	return entry(wi, kind, seq, when, map[string]any{"step": step, "spawn_n": spawn})
}

func cellFor(t *testing.T, p Profile, model, effort, step string) Cell {
	t.Helper()
	for _, c := range p.Cells {
		if c.Model == model && c.Effort == effort && c.Step == step {
			return c
		}
	}
	t.Fatalf("no cell %s/%s/%s in %+v", model, effort, step, p.Cells)
	return Cell{}
}

func TestThrottledDeathIsNotAModelDeath(t *testing.T) {
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-2*time.Hour), "m1", "", "code", 1),
		entry("wi", "step_dead", 2, at(-2*time.Hour+time.Minute), map[string]any{"step": "code", "spawn_n": 1, "cause": "model_error"}),
		failEvent("wi", 3, at(-2*time.Hour+2*time.Minute), "code", driver.FailRateLimited, 1),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	c := cellFor(t, p, "m1", "", "code")
	if c.ModelDeaths != 0 || c.ExcludedDeaths != 1 || c.Rate != 0 {
		t.Fatalf("rate_limited must be excluded, not a model death: %+v", c)
	}
	if len(p.Warnings) != 0 {
		t.Fatalf("one excluded death must warn nothing: %+v", p.Warnings)
	}
}

func TestExitOnlyDeathIsCountedOnce(t *testing.T) {
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-time.Hour), "m1", "", "code", 1),
		failEvent("wi", 2, at(-time.Hour+time.Minute), "code", driver.FailExitModel, 1),
	}
	p, alerts := Build(es, w14Now, 30*24*time.Hour)
	c := cellFor(t, p, "m1", "", "code")
	if c.ModelDeaths != 1 || c.Dispatches != 1 || c.LastCause != driver.FailExitModel {
		t.Fatalf("an exit-only death must count once: %+v", c)
	}
	if len(alerts) != 0 {
		t.Fatalf("one death must not cross the 3-in-24h threshold: %+v", alerts)
	}
}

func TestAttributionTableIsClosedAndTotal(t *testing.T) {
	check := func(cause string, want Tier) {
		if got := TierOf(cause); got != want {
			t.Errorf("TierOf(%q) = %v, want %v", cause, got, want)
		}
	}
	for _, c := range []string{driver.FailModelError, driver.FailModelHung, driver.FailStepTimeoutSlowModel, driver.FailExitModel, driver.FailEndedWithoutResult} {
		check(c, TierModel)
	}
	for _, c := range []string{driver.FailToolHung, driver.FailHarnessSilent, driver.FailStepTimeoutLongTask, driver.FailExitOther, driver.FailResultInvalid} {
		check(c, TierNonModel)
	}
	for _, c := range []string{driver.FailRateLimited, driver.FailUpstreamTransient, driver.FailExitHost, driver.FailDaemonRestart, driver.FailDriverRelaunch} {
		check(c, TierExcluded)
	}
	check("driver_stalled", TierExcluded)
	check("a_cause_we_never_classified", TierNonModel)
	// Cross-check the literal wire causes against the producers so drift
	// fails here instead of silently mis-attributing.
	if driver.FailModelError != string(supervise.CauseModelError) ||
		driver.FailEndedWithoutResult != string(supervise.CauseEndedWithoutResult) ||
		driver.FailToolHung != string(supervise.CauseToolHung) {
		t.Fatalf("driver/supervise cause spellings drifted")
	}
}

func TestHarnessAndOldNewIdentityJoinToOneCell(t *testing.T) {
	es := []protocol.EventEntry{
		entry("wi", kindStepStarted, 1, at(-time.Hour), map[string]any{"step": "fix", "model": "m1", "effort": "high", "candidate": "pi:m1:high", "spawn_n": 1}),
		failEvent("wi", 2, at(-time.Hour+time.Minute), "fix", driver.FailModelError, 1),
		entry("wi", kindStepStarted, 3, at(-time.Hour+2*time.Minute), map[string]any{"step": "fix", "model": "m1", "effort": "high", "candidate": "claude:m1:high", "spawn_n": 2}),
		failEvent("wi", 4, at(-time.Hour+3*time.Minute), "fix", driver.FailModelError, 2),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	if len(p.Cells) != 1 {
		t.Fatalf("two harness spellings must land in one cell: %+v", p.Cells)
	}
	c := cellFor(t, p, "m1", "high", "fix")
	if c.Dispatches != 2 || c.ModelDeaths != 2 || c.Rate != 1 {
		t.Fatalf("old+new shape join: %+v", c)
	}
}

func TestFailureWithoutPrecedingStartIsNotAttributed(t *testing.T) {
	es := []protocol.EventEntry{
		entry("wi", kindStepFailed, 1, at(-time.Hour), map[string]any{"step": "fix", "cause": driver.FailModelError, "spawn_n": 1, "model": "guessed"}),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	if len(p.Cells) != 0 {
		t.Fatalf("an orphan failure must not be attributed: %+v", p.Cells)
	}
}

func TestResumesAreDispatchesAndSuccessEnvelopesCount(t *testing.T) {
	es := []protocol.EventEntry{
		entry("wi", kindStepStarted, 1, at(-3*time.Hour), map[string]any{"step": "code", "model": "m1", "spawn_n": 1, "resume": false}),
		closeEvent("wi", 2, at(-3*time.Hour+time.Minute), kindStepDone, "code", 1),
		entry("wi", kindStepStarted, 3, at(-2*time.Hour), map[string]any{"step": "code", "model": "m1", "spawn_n": 2, "resume": true}),
		closeEvent("wi", 4, at(-2*time.Hour+time.Minute), kindNeedsInput, "code", 2),
		entry("wi", kindStepStarted, 5, at(-time.Hour), map[string]any{"step": "code", "model": "m1", "spawn_n": 3, "resume": true}),
		closeEvent("wi", 6, at(-time.Hour+time.Minute), kindGateVerdict, "code", 3),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	c := cellFor(t, p, "m1", "", "code")
	if c.Dispatches != 3 || c.Successes != 3 || c.ModelDeaths != 0 {
		t.Fatalf("resumes count as dispatches, all three envelopes are successes: %+v", c)
	}
}

func TestIncompleteIsNotASuccessButClosesWall(t *testing.T) {
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-time.Hour), "m1", "", "code", 1),
		closeEvent("wi", 2, at(-time.Hour+10*time.Minute), kindIncomplete, "code", 1),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	c := cellFor(t, p, "m1", "", "code")
	if c.Successes != 0 || c.MeanWallMs != 600000 {
		t.Fatalf("incomplete is a close but not a success: %+v", c)
	}
}

func TestWallTimeComesFromEventsOnlyAndInFlightHasNoSample(t *testing.T) {
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-2*time.Hour), "m1", "", "code", 1),
		closeEvent("wi", 2, at(-time.Hour), kindStepDone, "code", 1),
		startEvent("wi", 3, at(-30*time.Minute), "m1", "", "code", 2),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	c := cellFor(t, p, "m1", "", "code")
	if c.Dispatches != 2 || c.MeanWallMs != 3600000 {
		t.Fatalf("mean wall must use only the closed dispatch: %+v", c)
	}
	if c.Successes != 1 {
		t.Fatalf("the in-flight dispatch is no success: %+v", c)
	}
}

func TestThresholdsWarnAndSmallSamplesStayQuiet(t *testing.T) {
	// glm: three model deaths in 24h across two steps -> 24h warning.
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-5*time.Hour), "glm", "", "verification", 1),
		failEvent("wi", 2, at(-5*time.Hour+time.Minute), "verification", driver.FailModelError, 1),
		startEvent("wi", 3, at(-4*time.Hour), "glm", "", "verification", 2),
		failEvent("wi", 4, at(-4*time.Hour+time.Minute), "verification", driver.FailEndedWithoutResult, 2),
		startEvent("wi", 5, at(-3*time.Hour), "glm", "", "grill", 3),
		failEvent("wi", 6, at(-3*time.Hour+time.Minute), "grill", driver.FailModelError, 3),
		// tiny: 4 dispatches, 1 death = 25% and only 4 dispatches -> quiet.
		startEvent("wi2", 1, at(-2*time.Hour), "tiny", "", "code", 1),
		failEvent("wi2", 2, at(-2*time.Hour+time.Minute), "code", driver.FailModelError, 1),
		startEvent("wi2", 3, at(-time.Hour), "tiny", "", "code", 2),
		closeEvent("wi2", 4, at(-time.Hour+time.Minute), kindStepDone, "code", 2),
		startEvent("wi2", 5, at(-50*time.Minute), "tiny", "", "code", 3),
		closeEvent("wi2", 6, at(-49*time.Minute), kindStepDone, "code", 3),
		startEvent("wi2", 7, at(-40*time.Minute), "tiny", "", "code", 4),
		closeEvent("wi2", 8, at(-39*time.Minute), kindStepDone, "code", 4),
		// busy: 5 dispatches, 2 deaths = 40% -> 7-day warning.
		startEvent("wi3", 1, at(-30*time.Hour), "busy", "", "fix", 1),
		closeEvent("wi3", 2, at(-30*time.Hour+time.Minute), kindStepDone, "fix", 1),
		startEvent("wi3", 3, at(-29*time.Hour), "busy", "", "fix", 2),
		failEvent("wi3", 4, at(-29*time.Hour+time.Minute), "fix", driver.FailModelError, 2),
		startEvent("wi3", 5, at(-28*time.Hour), "busy", "", "fix", 3),
		failEvent("wi3", 6, at(-28*time.Hour+time.Minute), "fix", driver.FailEndedWithoutResult, 3),
		startEvent("wi3", 7, at(-27*time.Hour), "busy", "", "fix", 4),
		closeEvent("wi3", 8, at(-27*time.Hour+time.Minute), kindStepDone, "fix", 4),
		startEvent("wi3", 9, at(-26*time.Hour), "busy", "", "fix", 5),
		closeEvent("wi3", 10, at(-26*time.Hour+time.Minute), kindStepDone, "fix", 5),
	}
	p, alerts := Build(es, w14Now, 30*24*time.Hour)
	var glm, tiny, busy bool
	for _, w := range p.Warnings {
		switch w.Model {
		case "glm":
			glm = true
		case "tiny":
			tiny = true
		case "busy":
			busy = true
		}
	}
	if !glm || !busy || tiny {
		t.Fatalf("glm(3/24h)+busy(2/5=40%%) warn, tiny(1/4) must not: %+v", p.Warnings)
	}
	if len(alerts) != 2 {
		t.Fatalf("one alert per model+effort episode (glm, busy): %+v", alerts)
	}
}

func TestBoundaryAndUnparseableTimestamps(t *testing.T) {
	retention := 30 * 24 * time.Hour
	es := []protocol.EventEntry{
		// exactly on the retention boundary: outside (half-open)
		startEvent("wi", 1, at(-retention), "boundary", "", "code", 1),
		failEvent("wi", 2, at(-retention+time.Minute), "code", driver.FailModelError, 1),
		// just inside: the newer half owns a boundary failure
		startEvent("wi", 3, at(-retention+2*time.Minute), "inside", "", "code", 2),
		failEvent("wi", 4, at(-retention+3*time.Minute), "code", driver.FailModelError, 2),
		{At: "not-a-time", Kind: kindStepStarted, WI: "wi", Seq: 5, Body: mustJSON(map[string]any{"step": "code", "model": "badtime", "spawn_n": 3})},
	}
	p, _ := Build(es, w14Now, retention)
	if _, err := time.Parse(time.RFC3339Nano, p.Window.From); err != nil {
		t.Fatalf("window from unparseable: %v", err)
	}
	for _, c := range p.Cells {
		if c.Model == "badtime" || c.Model == "boundary" {
			t.Fatalf("out-of-window identity leaked into the profile: %+v", c)
		}
	}
	if _, ok := func() (Cell, bool) {
		for _, c := range p.Cells {
			if c.Model == "inside" {
				return c, true
			}
		}
		return Cell{}, false
	}(); !ok {
		t.Fatalf("a timestamp just inside the window must be counted: %+v", p.Cells)
	}
}

func TestRetentionPrunes(t *testing.T) {
	es := []protocol.EventEntry{
		startEvent("wi", 1, at(-31*24*time.Hour), "expired", "", "code", 1),
		failEvent("wi", 2, at(-31*24*time.Hour+time.Minute), "code", driver.FailModelError, 1),
	}
	p, _ := Build(es, w14Now, 30*24*time.Hour)
	if len(p.Cells) != 0 {
		t.Fatalf("events older than retention must not survive: %+v", p.Cells)
	}
}

func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
