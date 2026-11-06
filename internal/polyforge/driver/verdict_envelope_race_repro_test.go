package driver

// verdict_envelope_race_repro_test.go: the verdict / recorded-hand-off race
//. A supervisor StepDead and the dispatch's own hand-off
// can land in the same tick. When the hand-off is already on the call
// ledger but the stepRun has not collected it yet, awaitDispatch's verdict
// path must not cancel the wait and return the verdict — the recorded
// hand-off wins.
//
// The probe holds the envelope poll far longer than the verdict path and
// signals the stepRun's first (empty) collect through the agent handle's
// Done method, so the sequence is controlled: the first collect finds
// nothing, then the envelope lands, then the verdict arrives before the
// next poll. Expected: the dispatch delivered (the result that raced the
// verdict wins, as awaitDispatch's contract states).

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// firstDoneAgent is a live handle that closes firstDone on the first Done
// call. A stepRun asks Done right after each envelope collect, so the
// signal marks the deterministic "window is open" edge: every envelope
// written from now on sits on disk before the next (hour-away) poll.
type firstDoneAgent struct {
	*fakeAgent
	once      sync.Once
	firstDone chan struct{}
}

func (h *firstDoneAgent) Done() bool {
	h.once.Do(func() { close(h.firstDone) })
	return false
}

type hookedSpawner struct {
	handle *firstDoneAgent
}

func (s *hookedSpawner) Spawn(ctx context.Context, p spawn.Params) (spawn.Handle, error) {
	return s.handle, nil
}

func TestVerdictDoesNotSwallowAnEnvelopeAlreadyWritten(t *testing.T) {
	capDir := t.TempDir()
	writeExtLane(t, capDir, map[string]string{"verification": "verification: build, lint, test"})

	handle := &firstDoneAgent{fakeAgent: &fakeAgent{}, firstDone: make(chan struct{})}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_sp", CapsuleDir: capDir, BundleName: "autopilot-x", SessionHandle: "ab12cd34"},
		Spawner:      &hookedSpawner{handle: handle},
		PollInterval: time.Hour, // the only collect before the cancel is the initial one
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 1})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}

	link := supervise.NewLink("wi_sp")
	d := &Driver{cfg: Config{Link: link}}
	type waitOutcome struct {
		res  *StepResult
		err  error
		dead *supervise.StepDead
	}
	settled := make(chan waitOutcome, 1)
	go func() {
		res, werr, dead := d.awaitDispatch(context.Background(), run, "verification", 1)
		settled <- waitOutcome{res, werr, dead}
	}()

	<-handle.firstDone // the stepRun has polled once and is parked on the timer
	appendLedgerHandoff(t, capDir, "verification", 1, stepcall.StatusDone)
	link.Verdict(supervise.StepDead{
		Step: "verification", SpawnN: 1,
		Cause: supervise.CauseEndedWithoutResult, Evidence: "turn ended ok",
	})

	var got waitOutcome
	select {
	case got = <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("awaitDispatch never settled")
	}
	if got.dead != nil || got.res == nil {
		t.Fatalf("the verdict swallowed a hand-off already on the ledger: res=%+v err=%v dead=%+v", got.res, got.err, got.dead)
	}
	if got.res.Handoff == nil || got.res.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("collected hand-off %+v, want done", got.res.Handoff)
	}
}
