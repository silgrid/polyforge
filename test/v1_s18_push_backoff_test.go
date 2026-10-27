package test

// v1_s18: the push backoff's degradation event. The real daemon backs a
// failed push off from 10s to 5m, which no e2e can wait out — but the
// inproc rig assembles the SAME production components (runtime, observe,
// DefaultSpawner, the real UDS routes) with an injectable observe
// config, so the ladder shrinks to 200ms→1s here. The pushes themselves
// stay REAL: GitRemoteBase points at the REAL aihub process's git face,
// and the outage is that process being genuinely stopped mid-run.
//
// The version stream is the driver's own transition writes, one per
// step boundary, with each envelope action holding its step open long
// enough that one Version keeps failing until the delay reaches its
// cap: the push_backpressure event lands; the ENGINE survives the whole
// episode under the same live handle with no recovery/respawn signal and
// no new attempt instance; after the aihub returns the chain catches up
// (the bare repo reaches the outage-written local commits) and a fresh
// published event lands.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestV1S18PushBackoffBackpressure(t *testing.T) {
	inst := newV1Install(t)
	// The driver keeps producing versions through the (short) outage
	// window, then completes — degradation never kills the engine and
	// the scenario closes the WI itself. Each envelope action holds its
	// step open (ms) so the outage window sits INSIDE the run. grill
	// holds only 2s: the pre-outage published event lands within ~150ms,
	// and grill's completion is then the outage's first boundary (the
	// first failed push) without idling; fix and verification hold 8s
	// each, so the ladder reaches its cap inside fix and the whole
	// episode, recovery included, ends before the run does.
	actions := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "ms": 2000},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 8000},
		{"do": "envelope", "id": "verification", "outcome": "completed", "handoff": true, "ms": 8000},
	}
	wi := inst.newWI(t, "s18: push backpressure (backoff cap, inproc rig)",
		actions, nil)
	// The injected ladder: 200ms doubling to a 1s cap, reached after
	// four failures of one Version (well inside an 8s step).
	rig := newInprocRig(t, inst, optPushBackoff(200*time.Millisecond, time.Second))
	t.Cleanup(func() { killOrphans(wi) })
	rig.start(wi)

	engineAlive := func() bool {
		ji, ok := rig.rt.Info(wi)
		if !ok {
			return false
		}
		return ji.Agent != nil
	}
	waitUntil(t, 60*time.Second, "the engine never started", func() bool {
		return engineAlive()
	})

	// The push path is proven healthy before the outage: at least one
	// version reached the aihub's bare repo over the real git face.
	rig.waitEvent(t, wi, "published", 60*time.Second)

	// The outage: stop the REAL aihub. The driver's next boundary
	// transition bumps the version, the monitor's push fails (one
	// push_failed per attempt), and once the retry delay reaches its cap
	// the degradation event fires.
	inst.stopAihub()
	rig.waitEvent(t, wi, "push_failed", 60*time.Second)
	bp := rig.waitEvent(t, wi, "push_backpressure", 60*time.Second)
	var body struct {
		FailedPushes int `json:"failed_pushes"`
	}
	if err := json.Unmarshal(bp.Body, &body); err != nil {
		t.Fatalf("push_backpressure body: %v (%s)", err, bp.Body)
	}
	if body.FailedPushes < 4 {
		t.Fatalf("failed_pushes %d, want >= 4 (the delay doubled to its cap)", body.FailedPushes)
	}

	// Degradation never kills the engine: the handle is still live at
	// the moment the event is observed, and the episode shows none of
	// the recovery/respawn signals nor a new attempt instance.
	if !engineAlive() {
		t.Fatal("the engine handle died across the degradation")
	}
	eventKinds := func() []string {
		var kinds []string
		for _, e := range rig.eventsOf(t, wi) {
			kinds = append(kinds, e.Kind)
		}
		return kinds
	}
	kinds := eventKinds()
	for _, bad := range []string{"wi_agent_rederived", "needs_recovery", "job_stopped"} {
		if n := countOf(kinds, bad); n != 0 {
			t.Fatalf("%s fired %d times during the outage: %v", bad, n, kinds)
		}
	}
	if n := countOf(kinds, "job_started"); n != 1 {
		t.Fatalf("job_started fired %d times, want 1 (no new attempt instance): %v", n, kinds)
	}

	// The catch-up baseline: what the driver produced during the outage
	// lives as local commits in the capsule's chain. Bring the aihub
	// back and demand the server-side bare repo reach that history plus
	// a fresh published event (the retry round or the next boundary
	// push lands the backlog).
	local := capsuleCommitCount(t, rig.dataDir, wi)
	if local == 0 {
		t.Fatal("no local commits after the outage episode")
	}
	publishedBefore := countOf(kinds, "published")
	inst.restartAihub()
	inst.waitBareCommits(t, wi, local, 60*time.Second)
	waitUntil(t, 60*time.Second, "no published event after the recovery push", func() bool {
		return countOf(eventKinds(), "published") > publishedBefore
	})

	// The engine is STILL the same live handle after the recovery.
	if !engineAlive() {
		t.Fatal("the engine handle died across the recovery push")
	}
}
