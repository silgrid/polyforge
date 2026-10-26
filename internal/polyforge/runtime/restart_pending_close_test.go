package runtime

// NEW-3 coverage: the pending-close crash window
// of a retained Paused record beyond the wrap reason the repro test
// drives. stopOp's fromPaused branch persists ANY terminal close intent
// (wrapped|failed|blocked) on the retained record before SessionEnd goes
// out; a daemon crash in that window must never make the restart forget
// it. These tests drive the real close paths (operator pause, then the
// terminal close with a scripted SessionEnd failure as the crash
// stand-in) and assert the restart REPLAYS the close — plus the
// keep-and-retry shape when the replay itself fails.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// seedPendingCloseOfRetainedPause drives the real paths into the NEW-3
// crash window: an operator pause (record retained, clean_pause latched
// server-side), then a terminal close whose SessionEnd never reaches the
// hub (the scripted failure is the crash stand-in — the on-disk and hub
// aftermath are identical). It answers the first runtime plus the crash
// window's persisted record shape.
func seedPendingCloseOfRetainedPause(t *testing.T, wi string, reason string) (*Runtime, *fakeHub, *job, string) {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	cfg := Config{
		MachineID: "m-test",
		Mapping:   mappingFor(filepath.Join(dir, "ws")),
		DataDir:   filepath.Join(dir, "data"),
	}
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(cfg, hub, sp, toOB, supervise.NewQueue[protocol.OBMsg]())
	sess := fakeSession(wi)
	hub.hold(wi, sess, api.StatusRunning)
	j := seedJob(r, wi)
	initGitCapsule(t, j)

	// the operator pause: retained Paused record, hub paused + clean
	if err := r.Stop(context.Background(), wi, protocol.FinishPaused); err != nil {
		t.Fatalf("pause close: %v", err)
	}
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishPaused {
		t.Fatalf("the pause close must release observe with RTFinished(paused), got %+v", msg)
	}
	// the terminal close, crashing before SessionEnd reaches the hub
	hub.end[wi] = fmt.Errorf("daemon crashed before SessionEnd reached the server (scripted)")
	if err := r.Stop(context.Background(), wi, reason); err == nil {
		t.Fatal("the terminal close must surface the scripted session-end failure")
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 {
		t.Fatalf("the crash-window state must retain exactly one record, got %+v", recs)
	}
	if recs[0].State != string(StatePaused) || !recs[0].CleanPause || recs[0].StopReason != reason {
		t.Fatalf("the crash-window record must be the retained paused shape carrying the pending %s intent, got %+v", reason, recs[0])
	}
	// the crash itself: nothing of the first daemon runs on (its pending
	// close's retry would otherwise race the restarted daemon)
	r.Close()
	return r, hub, j, sess
}

// restartOver rebuilds a fresh daemon (r2) over the first runtime's
// DataDir, with a hub scripted to the crash's truth (the terminal close
// never landed: the session still matches, paused, clean) and an
// EventTap capturing diagnostic events, then runs Restart. endErr (if
// non-nil) keeps the SessionEnd outage going through the replay too.
func restartOver(t *testing.T, r *Runtime, wi, sess string, endErr error, events *[]string) (*Runtime, *fakeHub, error) {
	t.Helper()
	r2, hub2, _, err := restartOverHub(t, r, wi, sess, api.StatusPaused, true, endErr, events)
	return r2, hub2, err
}

// restartOverHub is restartOver with the hub's answer for the session
// scripted (status, clean_pause), and the restarted daemon's spawner.
func restartOverHub(t *testing.T, r *Runtime, wi, sess, status string, clean bool, endErr error, events *[]string) (*Runtime, *fakeHub, *scriptedSpawner, error) {
	t.Helper()
	cfg2 := r.cfg
	cfg2.EventTap = func(wiID, kind string, body map[string]any) {
		*events = append(*events, wiID+"/"+kind)
	}
	hub2 := newFakeHub()
	hub2.hold(wi, sess, status)
	hub2.cleanPause[wi] = clean
	if endErr != nil {
		hub2.end[wi] = endErr
	}
	sp2 := &scriptedSpawner{}
	// a real (scripted) engine: a resume would spawn through sp2
	cfg2.AgentStarter = scriptedEngineStarter(sp2)
	r2 := New(cfg2, hub2, sp2, supervise.NewQueue[protocol.RTMsg](), supervise.NewQueue[protocol.OBMsg]())
	return r2, hub2, sp2, restartSettled(t, r2)
}

// TestRestartReplaysPendingFailedAndBlockedCloseOfRetainedPausedRecord:
// the failed and blocked closes of a retained Paused record hit the same
// fromPaused persist-first window as the wrap (the repro test); the
// restart must replay each one so the terminal verdict reaches the hub.
func TestRestartReplaysPendingFailedAndBlockedCloseOfRetainedPausedRecord(t *testing.T) {
	for _, reason := range []string{protocol.FinishFailed, protocol.FinishBlocked} {
		wi := "wi_new3_" + reason
		r1, _, _, sess := seedPendingCloseOfRetainedPause(t, wi, reason)

		var events []string
		r2, hub2, err := restartOver(t, r1, wi, sess, nil, &events)
		if err != nil {
			t.Fatalf("%s: Restart: %v", reason, err)
		}

		replayed := false
		for _, e := range hub2.ends {
			if e.Status == reason && e.SessionID == sess {
				replayed = true
			}
		}
		if !replayed {
			t.Fatalf("%s: the restart must replay the pending close — a SessionEnd(%s) on the record's session secret must reach the hub, got ends=%v events=%v", reason, reason, hub2.ends, events)
		}
		if got := hub2.status[wi]; got != reason {
			t.Fatalf("%s: the hub must hold the terminal verdict after the replay, got %q", reason, got)
		}
		// the close completed: the record is gone, the wrap/fail intent
		// no longer sits inert on disk.
		if recs := loadRecords(t, r2); len(recs) != 0 {
			t.Fatalf("%s: the replayed close must drop the record, got %+v", reason, recs)
		}
		found := false
		for _, e := range events {
			if e == wi+"/pending_close_replay" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: the replay must be surfaced as a diagnostic event, got %v", reason, events)
		}
	}
}

// TestRestartPendingCloseReplayFailureKeepsRecordAndSurfaces: when the
// replay itself fails (the hub is still unreachable for SessionEnd), the
// record and its pending intent must survive for the next restart's
// retry, and the pending close must be loudly surfaced — never silently
// reconciled back to a plain retained pause.
func TestRestartPendingCloseReplayFailureKeepsRecordAndSurfaces(t *testing.T) {
	wi := "wi_new3_replayfail"
	r1, _, _, sess := seedPendingCloseOfRetainedPause(t, wi, protocol.FinishWrapped)

	var events []string
	r2, hub2, err := restartOver(t, r1, wi, sess, fmt.Errorf("the SessionEnd outage persists (scripted)"), &events)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	// the replay's SessionEnd fails too (the scripted outage persists)
	if len(hub2.ends) != 0 {
		t.Fatalf("the scripted outage must refuse the replay, got ends=%v", hub2.ends)
	}

	// LOUD: the diagnostic event names the pending close.
	found := false
	for _, e := range events {
		if e == wi+"/pending_close_replay" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the failed replay must still surface the pending close, got %v", events)
	}
	// KEPT: the record survives with its pending wrap intent intact —
	// the next restart re-verifies and retries the replay.
	recs := loadRecords(t, r2)
	if len(recs) != 1 || recs[0].WorkItemID != wi {
		t.Fatalf("a failed replay must keep the record for the next restart, got %+v", recs)
	}
	if recs[0].State != string(StatePaused) || !recs[0].CleanPause || recs[0].StopReason != protocol.FinishWrapped {
		t.Fatalf("the kept record must carry the pending wrap intent, got %+v", recs[0])
	}
	// and the retry is real: once the hub accepts SessionEnd again, the
	// next restart completes the close.
	hub2.mu.Lock()
	delete(hub2.end, wi)
	hub2.mu.Unlock()
	if err := restartSettled(t, r2); err != nil {
		t.Fatalf("retry Restart: %v", err)
	}
	replayed := false
	for _, e := range hub2.ends {
		if e.Status == api.StatusWrapped && e.SessionID == sess {
			replayed = true
		}
	}
	if !replayed {
		t.Fatalf("the retried restart must complete the pending wrap close, got ends=%v", hub2.ends)
	}
	if recs := loadRecords(t, r2); len(recs) != 0 {
		t.Fatalf("the completed close must drop the record, got %+v", recs)
	}
}

// TestRestartPlainRetainedPauseIsNotReplayed: the guard — a plain
// retained operator pause (stop_reason=paused, exactly what pauseOp
// persists) must NOT be mistaken for a pending terminal close: zero
// SessionEnd calls, the record retained.
func TestRestartPlainRetainedPauseIsNotReplayed(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	cfg := Config{
		MachineID: "m-test",
		Mapping:   mappingFor(filepath.Join(dir, "ws")),
		DataDir:   filepath.Join(dir, "data"),
	}
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(cfg, hub, sp, toOB, supervise.NewQueue[protocol.OBMsg]())
	wi := "wi_new3_plainpause"
	sess := fakeSession(wi)
	hub.hold(wi, sess, api.StatusRunning)
	j := seedJob(r, wi)
	initGitCapsule(t, j)

	if err := r.Stop(context.Background(), wi, protocol.FinishPaused); err != nil {
		t.Fatalf("pause close: %v", err)
	}
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishPaused {
		t.Fatalf("the pause close must release observe with RTFinished(paused), got %+v", msg)
	}

	var events []string
	var mu sync.Mutex
	cfg2 := cfg
	cfg2.EventTap = func(wiID, kind string, body map[string]any) {
		mu.Lock()
		events = append(events, wiID+"/"+kind)
		mu.Unlock()
	}
	hub2 := newFakeHub()
	hub2.hold(wi, sess, api.StatusPaused)
	hub2.cleanPause[wi] = true
	r2 := New(cfg2, hub2, sp, supervise.NewQueue[protocol.RTMsg](), supervise.NewQueue[protocol.OBMsg]())
	if err := restartSettled(t, r2); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if len(hub2.ends) != 0 {
		t.Fatalf("a plain retained pause must produce no SessionEnd, got %v", hub2.ends)
	}
	recs := loadRecords(t, r2)
	if len(recs) != 1 || recs[0].WorkItemID != wi || recs[0].State != string(StatePaused) {
		t.Fatalf("the plain retained pause must stay retained and paused, got %+v", recs)
	}
	if recs[0].StopReason != protocol.FinishPaused {
		t.Fatalf("the retained pause's stop_reason must stay the pause's own, got %+v", recs[0])
	}
}

// TestRestartReplaysPendingCloseWhateverTheHubSaysOfTheLiveSession
// (polyforge#197): a retained Paused record carrying a terminal
// stop_reason is replayed as that close whenever the hub still shows the
// session live — running (the pause's own SessionEnd never landed either)
// or paused with clean_pause cleared (a pf resume landed at the hub) as
// well as paused + clean — and is never resumed: no agent is spawned.
func TestRestartReplaysPendingCloseWhateverTheHubSaysOfTheLiveSession(t *testing.T) {
	hubs := []struct {
		name   string
		status string
		clean  bool
	}{
		{"running", api.StatusRunning, false},
		{"paused, clean_pause cleared", api.StatusPaused, false},
		{"paused + clean_pause", api.StatusPaused, true},
	}
	for _, h := range hubs {
		for _, reason := range []string{protocol.FinishWrapped, protocol.FinishFailed, protocol.FinishBlocked} {
			name := h.name + "/" + reason
			wi := "wi_197_" + reason
			r1, _, _, sess := seedPendingCloseOfRetainedPause(t, wi, reason)
			var events []string
			r2, hub2, sp2, err := restartOverHub(t, r1, wi, sess, h.status, h.clean, nil, &events)
			if err != nil {
				t.Fatalf("%s: Restart: %v", name, err)
			}
			hub2.mu.Lock()
			ends, status := append([]api.SessionEndRequest(nil), hub2.ends...), hub2.status[wi]
			hub2.mu.Unlock()
			replayed := false
			for _, e := range ends {
				if e.Status == reason && e.SessionID == sess {
					replayed = true
				}
			}
			if !replayed || status != reason {
				t.Fatalf("%s: the pending close must be replayed (SessionEnd(%s) on the record's session), got ends=%v hub=%q events=%v", name, reason, ends, status, events)
			}
			sp2.mu.Lock()
			spawned := len(sp2.calls)
			sp2.mu.Unlock()
			if spawned != 0 {
				t.Fatalf("%s: a work item with a pending %s close must never be resumed, got %d spawn(s)", name, reason, spawned)
			}
			if recs := loadRecords(t, r2); len(recs) != 0 {
				t.Fatalf("%s: the replayed close must drop the record, got %+v", name, recs)
			}
			if !slices.Contains(events, wi+"/pending_close_replay") {
				t.Fatalf("%s: the replay must be surfaced as pending_close_replay, got %v", name, events)
			}
		}
	}
}

// TestResumeRefusedWhileACloseIsPending (polyforge#197): pf resume on a
// paused record whose terminal close has not reached aihub is refused —
// a resume must not race the close it would cancel — and nothing spawns.
func TestResumeRefusedWhileACloseIsPending(t *testing.T) {
	wi := "wi_197_resume"
	r1, _, _, sess := seedPendingCloseOfRetainedPause(t, wi, protocol.FinishWrapped)
	var events []string
	// the hub still unreachable for SessionEnd: the restart's replay fails
	// and keeps the record (and arms its retry)
	r2, _, sp2, err := restartOverHub(t, r1, wi, sess, api.StatusPaused, false, fmt.Errorf("SessionEnd outage (scripted)"), &events)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !slices.Contains(events, wi+"/pending_close_replay_failed") {
		t.Fatalf("a failed replay must be surfaced as its own event, got %v", events)
	}
	_, rerr := r2.ResumePaused(context.Background(), wi)
	var ae *api.Error
	if !errors.As(rerr, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "wrapped close of the paused record is pending") {
		t.Fatalf("pf resume must be refused naming the pending close, got %v", rerr)
	}
	sp2.mu.Lock()
	defer sp2.mu.Unlock()
	if len(sp2.calls) != 0 {
		t.Fatalf("a refused resume must spawn nothing, got %d", len(sp2.calls))
	}
}

// TestPausedCloseSessionEndFailureIsRetriedLive (polyforge#197): a wrap
// of a retained paused record whose SessionEnd fails is retried by the
// running daemon (not only by the next restart) until it lands.
func TestPausedCloseSessionEndFailureIsRetriedLive(t *testing.T) {
	setGitIdentity(t)
	wi := "wi_197_live"
	dir := t.TempDir()
	cfg := Config{
		MachineID:    "m-test",
		Mapping:      mappingFor(filepath.Join(dir, "ws")),
		DataDir:      filepath.Join(dir, "data"),
		EndRetryBase: 10 * time.Millisecond,
	}
	hub := newFakeHub()
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(cfg, hub, &scriptedSpawner{}, toOB, supervise.NewQueue[protocol.OBMsg]())
	t.Cleanup(r.Close)
	sess := fakeSession(wi)
	hub.hold(wi, sess, api.StatusRunning)
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	if err := r.Stop(context.Background(), wi, protocol.FinishPaused); err != nil {
		t.Fatalf("pause close: %v", err)
	}
	recvRT(t, toOB)

	hub.mu.Lock()
	hub.end[wi] = fmt.Errorf("SessionEnd outage (scripted)")
	hub.mu.Unlock()
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err == nil {
		t.Fatal("the wrap close must surface the scripted session-end failure")
	}
	// a repeat close (or a verdict swap) while it is pending is refused
	err := r.Stop(context.Background(), wi, protocol.FinishFailed)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "wrapped close is already in progress") {
		t.Fatalf("a close over a pending close must be refused naming it, got %v", err)
	}
	hub.mu.Lock()
	delete(hub.end, wi) // aihub is back
	hub.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		hub.mu.Lock()
		status := hub.status[wi]
		hub.mu.Unlock()
		if status == protocol.FinishWrapped && len(loadRecords(t, r)) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the failed close was never retried: hub %q, records %+v", status, loadRecords(t, r))
		}
		time.Sleep(10 * time.Millisecond)
	}
}
