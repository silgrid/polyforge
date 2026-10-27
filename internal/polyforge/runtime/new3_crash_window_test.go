package runtime

// REPRO test (review NEW-3): the crash window between the
// retained-Paused wrap close and its SessionEnd.
//
// Scenario under test, exactly as reported:
//  1. an operator pause leaves the work item in the RETAINED Paused
//     state (record state=paused, clean_pause latched; the hub holds the
//     session paused with clean_pause=true) — v1.5 review Finding 2;
//  2. the operator wraps the retained record (close reason wrapped).
//     stopOp's fromPaused branch persists the wrap intent on the record
//     (state stays paused, stop_reason=wrapped) BEFORE the terminal
//     SessionEnd goes out;
//  3. the daemon crashes in the window between that persist and the
//     SessionEnd reaching the server;
//  4. the daemon restarts and reconciles the record against the hub,
//     which never heard the wrap (it still answers paused +
//     clean_pause=true, matching our session).
//
// The scripted SessionEnd failure in step 3 stands in for the crash: the
// close never settles server-side, and the on-disk aftermath is exactly
// the crash window's (asserted below before the restart is exercised).
//
// EXPECTED behavior per the work item's goal: the daemon restart must
// either REPLAY the pending wrap close (a SessionEnd with the wrapped
// status reaches the hub on our session secret) or LOUDLY SURFACE the
// pending close (an error return or a diagnostic event) — never
// silently reconcile the record back to a plain retained operator
// pause, forgetting the wrap intent.
//
// This test currently FAILS: it encodes the reported symptom.

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestRestartForgetsPendingWrapOfRetainedPausedRecord(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	cfg := Config{
		MachineID: "m-test",
		Mapping:   mappingFor(filepath.Join(dir, "ws")),
		DataDir:   filepath.Join(dir, "data"),
	}

	// -- Phase 1: the operator pause, through the real close path.
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	toOB1 := supervise.NewQueue[protocol.RTMsg]()
	r1 := New(cfg, hub, sp, toOB1, supervise.NewQueue[protocol.OBMsg]())
	wi := "wi_new3"
	sess := fakeSession(wi)
	hub.hold(wi, sess, api.StatusRunning)
	j := seedJob(r1, wi)
	initGitCapsule(t, j)

	if err := r1.Stop(context.Background(), wi, protocol.FinishPaused); err != nil {
		t.Fatalf("pause close: %v", err)
	}
	msg := recvRT(t, toOB1)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishPaused {
		t.Fatalf("the pause close must release observe with RTFinished(paused), got %+v", msg)
	}

	// -- Phase 2: the wrap close of the retained paused record. The
	// scripted SessionEnd failure is the crash stand-in: the wrap intent
	// is persisted on the record, but the terminal verdict never
	// reaches the server.
	hub.end[wi] = fmt.Errorf("daemon crashed before SessionEnd reached the server (scripted)")
	wrapErr := r1.Stop(context.Background(), wi, protocol.FinishWrapped)
	if wrapErr == nil {
		t.Fatal("the wrap close must surface the scripted session-end failure")
	}

	// The crash window's on-disk state: the retained record is still
	// paused (clean_pause latched) but now carries the pending wrap
	// intent in stop_reason. This is what a daemon crash between the
	// fromPaused persist and SessionEnd leaves behind.
	recs := loadRecords(t, r1)
	if len(recs) != 1 {
		t.Fatalf("the crash-window state must retain exactly one record, got %+v", recs)
	}
	if recs[0].State != string(StatePaused) || !recs[0].CleanPause {
		t.Fatalf("the crash-window record must still be the retained paused shape, got %+v", recs[0])
	}
	if recs[0].StopReason != protocol.FinishWrapped {
		t.Fatalf("the crash-window record must carry the pending wrap intent (stop_reason), got %+v", recs[0])
	}
	// the crash itself: nothing of the first daemon runs on (its pending
	// close's retry would otherwise race the restarted daemon)
	r1.Close()

	// -- Phase 3: daemon restart. The hub never heard the wrap: it still
	// answers paused + clean_pause=true under our session.
	hub2 := newFakeHub()
	hub2.hold(wi, sess, api.StatusPaused)
	hub2.cleanPause[wi] = true
	sp2 := &scriptedSpawner{}
	var mu sync.Mutex
	var events []string
	cfg2 := cfg
	cfg2.EventTap = func(wiID, kind string, body map[string]any) {
		mu.Lock()
		events = append(events, wiID+"/"+kind)
		mu.Unlock()
	}
	toOB2 := supervise.NewQueue[protocol.RTMsg]()
	r2 := New(cfg2, hub2, sp2, toOB2, supervise.NewQueue[protocol.OBMsg]())
	restartErr := restartSettled(t, r2)

	// -- EXPECTED: the pending wrap close is replayed to the hub (a
	// SessionEnd(wrapped) on our session secret — the hub's status for
	// the work item becomes wrapped) or loudly surfaced (Restart
	// returns an error, or a diagnostic event names the pending close).
	mu.Lock()
	surfaced := len(events) > 0
	mu.Unlock()
	replayed := false
	for _, e := range hub2.ends {
		if e.Status == api.StatusWrapped && e.SessionID == sess {
			replayed = true
		}
	}
	if !replayed && !surfaced && restartErr == nil {
		after := loadRecords(t, r2)
		shape := fmt.Sprintf("%+v", after)
		if len(after) == 1 {
			shape = fmt.Sprintf("%+v", *after[0])
		}
		t.Fatalf("REPORTED FAILURE (review NEW-3): the daemon restart silently forgot the pending wrap close of the retained-Paused record — "+
			"no SessionEnd(wrapped) replay reached the hub (ends=%v), no diagnostic event fired (events=%v), Restart returned nil; "+
			"the record was silently reconciled back to a plain retained operator pause: %s",
			hub2.ends, events, shape)
	}
}
