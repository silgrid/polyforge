package observe

// owner_wait_gap_test.go: a step's wait on the owner (step_owner_wait,
// workflow contract v2 §5.2) is an observation gap of its dispatch, anchored
// at the wait's START (PR 6d): when the owner's message resumes the
// dispatch, every stamp from before the wait moves forward by the wait —
// the dispatch's start (T counts no owner time; the driver's declaredPause
// reads exactly this) and the ask turn's end (rule 1b's clock restarts) —
// while a stamp the resumed turn wrote after the wait began stays put: the
// harness answers the message before the driver's fact is drained, and a
// cutoff of "now" would push that stamp into the future and blind the
// model-wait rule for the wait's length. Driven through the tick path on
// the injected clock (supervisor_pause_repro_test.go), ticking through the
// wait at the declared cadence so the supervisor declares no gap of its own.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestOwnerWaitIsAnObservationGapAnchoredAtItsStart(t *testing.T) {
	clk := newPauseClock(time.Unix(1_700_000_000, 0))
	m := newTickDrivenMonitor(t, clk, time.Minute)
	tr := dispatch(m, clk, supervise.Thresholds{
		Silence: 10 * time.Hour, Tool: 10 * time.Hour, StepTimeout: 10 * time.Hour,
	})
	tick(m)
	noVerdict(t, m, "the opening tick")
	started := tr.Snapshot(clk.Now()).StartedAt

	// the agent asks and ends its turn; the driver marks the dispatch
	// awaiting the owner
	clk.Advance(time.Minute)
	tr.Alive()
	tr.TurnEnded(true, "")
	turnEnded := tr.Snapshot(clk.Now()).TurnEndedAt
	m.link.Fact(supervise.StepOwnerWait{Step: "fix", SpawnN: 1, Waiting: true, AskAt: "ask-1"})
	tick(m)
	noVerdict(t, m, "the awaiting dispatch")
	waitStart := clk.Now()

	// the owner thinks for ten minutes; the supervisor ticks on its
	// cadence (no observation gap of its own) and judges nothing
	for i := 0; i < 10; i++ {
		clk.Advance(time.Minute)
		tick(m)
		noVerdict(t, m, "a tick while the owner is asked")
	}
	wait := clk.Now().Sub(waitStart)

	// the reply: the harness hands it to the model (the resumed turn's
	// model wait starts NOW) before the driver's fact is drained
	tr.WaitingModel()
	replied := clk.Now()
	m.link.Fact(supervise.StepOwnerWait{Step: "fix", SpawnN: 1, Waiting: false})
	tick(m)
	noVerdict(t, m, "the resumed dispatch")

	snap := tr.Snapshot(clk.Now())
	if !snap.StartedAt.Equal(started.Add(wait)) {
		t.Fatalf("the dispatch start moved by %v, want the whole wait %v (T must count no owner time)", snap.StartedAt.Sub(started), wait)
	}
	if !snap.TurnEndedAt.Equal(turnEnded.Add(wait)) {
		t.Fatalf("the ask turn's end moved by %v, want %v (rule 1b's clock restarts after the wait)", snap.TurnEndedAt.Sub(turnEnded), wait)
	}
	if !snap.WaitingSince.Equal(replied) {
		t.Fatalf("the resumed turn's model wait moved to %v, want %v: a stamp written after the wait began must not move", snap.WaitingSince, replied)
	}
	if snap.Waiting != true {
		t.Fatal("the resumed turn's model wait is lost")
	}
}
