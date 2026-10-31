package runtime

// w72_refused_close_settles_once_test.go —, the acceptance the
// repro's own bound is too loose to pin.
//
// The repro (w72_verify_refused_livelock_repro_test.go) only bounds the
// retry count (≤7 rounds in 32×EndRetryBase) and the growth of the gaps.
// Its pre-fix failure was 14–24 full aftermaths, so a fix that merely
// slowed the chain down (exponential backoff, no terminal answer) would
// still pass it while the hub kept being asked to settle a close it had
// already refused. These tests pin the acceptance directly:
//
//	RUNNING record, verify refused (4xx) → the failure aftermath (one
//	  blocked verdict, one SessionEnd) runs EXACTLY ONCE and nothing
//	  retries it; the record parks in a state a person can see.
//	END_PENDING record, verify refused → no SECOND aftermath (the old
//	  code re-ran kill/push/WIP/SessionEnd every EndRetryBase).
//	SEALING record, SessionEnd refused (4xx) → parked, one attempt only.
//	SEALING record, SessionEnd unreachable (transport) → the chain backs
//	  off and TERMINATES at its bound (EndRetryWindow) in a visible state,
//	  instead of retrying forever.
//
// Every assertion counts hub calls and runtime events; nothing here
// depends on exact timings beyond a generous window.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// w72BlockedFinishes drains toOB and answers how many RTFinished(blocked)
// messages the runtime sent.
func w72BlockedFinishes(q interface {
	Pop() (protocol.RTMsg, bool)
}) (n int) {
	for {
		m, ok := q.Pop()
		if !ok {
			return n
		}
		if m.Kind == protocol.RTFinished && m.Reason == protocol.FinishBlocked {
			n++
		}
	}
}

// TestW72RefusedCloseAftermathRunsOnce: a hub that refuses the verify and
// every close answers deterministically, so the blocked verdict's aftermath
// runs once and is never retried.
func TestW72RefusedCloseAftermathRunsOnce(t *testing.T) {
	setGitIdentity(t)
	const base = 50 * time.Millisecond
	const window = 32 * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour // the ceiling stays out of the window
	events := newW72EventCounter(r)
	const wi = "wi_w72_once"
	// the machine key was revoked: verify AND every close are refused
	hub.fakeHub.lookupErr[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "machine key revoked"}

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(window)

	outages := events.get(EventSessionVerifyOutage)
	refused := events.get(EventCloseRefused)
	exhausted := events.get(EventRetryExhausted)
	ends := hub.endTimes()
	finished := w72BlockedFinishes(toOB)
	state, ok := r.Info(wi)

	if outages != 1 || refused != 1 {
		t.Fatalf("a refused verify/close must run its aftermath exactly once: %d session_verify_outage, %d close_refused events "+
			"(want 1/1); SessionEnd attempts=%d", outages, refused, len(ends))
	}
	if finished != 1 || len(ends) != 1 {
		t.Fatalf("the blocked verdict must be delivered once: %d RTFinished(blocked), %d SessionEnd attempts (want 1/1)",
			finished, len(ends))
	}
	if exhausted != 0 {
		t.Fatalf("a refusal is final, not an exhausted retry chain: %d retry_exhausted events", exhausted)
	}
	if !ok || state.State != StateEndPending {
		t.Fatalf("the refused record must stay visible as %s, got %+v (present=%v)", StateEndPending, state, ok)
	}

	// a second window: nothing is retried behind the parked record
	time.Sleep(window)
	if got := len(hub.endTimes()); got != len(ends) {
		t.Fatalf("the parked record was retried: %d SessionEnd attempts after a second window (was %d)", got, len(ends))
	}
	if got := w72BlockedFinishes(toOB); got != 0 {
		t.Fatalf("%d further blocked verdicts after the record parked", got)
	}
	if got := events.get(EventSessionVerifyOutage); got != outages {
		t.Fatalf("%d further session_verify_outage events after the record parked (was %d)", got, outages)
	}
}

// TestW72RefusedVerifyOnClosingRecordIsNotReblocked: the record's close
// already ran its aftermath (it left RUNNING), so the restart that meets a
// refusing hub parks it — it must not re-block it and re-run kill/push/
// WIP/SessionEnd.
func TestW72RefusedVerifyOnClosingRecordIsNotReblocked(t *testing.T) {
	setGitIdentity(t)
	const base = 50 * time.Millisecond
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour
	events := newW72EventCounter(r)
	const wi = "wi_w72_parked"
	hub.fakeHub.lookupErr[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "machine key revoked"}

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	// the crashed daemon's blocked close: END_PENDING, the hub never heard it
	rec.State = string(StateEndPending)
	rec.EndNote = "workflow blocked: the owner holds the next move (resume / cancel)"
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(4 * base)

	if ends := hub.endTimes(); len(ends) != 0 {
		t.Fatalf("an already-closing record must not re-run its close: %d SessionEnd attempts", len(ends))
	}
	if got := w72BlockedFinishes(toOB); got != 0 {
		t.Fatalf("an already-closing record must not re-emit its verdict: %d RTFinished(blocked)", got)
	}
	if got := events.get(EventSessionVerifyOutage); got != 1 {
		t.Fatalf("the refused verify on a closing record must be named once, got %d session_verify_outage events", got)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StateEndPending) {
		t.Fatalf("the parked record must stay end_pending, got %+v", recs)
	}
}

// TestW72RefusedSealCloseIsParked: a SEALING close the hub refuses is
// parked with its verdict, not retried every base interval.
func TestW72RefusedSealCloseIsParked(t *testing.T) {
	setGitIdentity(t)
	const base = 50 * time.Millisecond
	const window = 8 * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour
	events := newW72EventCounter(r)
	const wi = "wi_w72_seal_refused"

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.State = string(StateSealing)
	rec.StopReason = protocol.FinishWrapped
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	hub.fakeHub.hold(wi, rec.SessionID, api.StatusRunning) // the verify MATCHES
	hub.fakeHub.end[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "work item wi_w72_seal_refused was deleted"}
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(window)

	if ends := hub.endTimes(); len(ends) != 1 {
		t.Fatalf("a refused close must be attempted exactly once, got %d SessionEnd attempts", len(ends))
	}
	if got := events.get(EventCloseRefused); got != 1 {
		t.Fatalf("the refused close must leave its one close_refused event, got %d", got)
	}
	if got := events.get(EventRetryExhausted); got != 0 {
		t.Fatalf("a refusal is final, not an exhausted chain: %d retry_exhausted events", got)
	}
	if state, ok := r.Info(wi); !ok || state.State != StateSealing {
		t.Fatalf("the parked close must stay visible as %s, got %+v (present=%v)", StateSealing, state, ok)
	}
}

// TestW72RetryChainIsBounded: an unreachable hub is retried with a growing
// backoff that then holds at the ceiling, and the chain terminates once
// its time window (not a count) has passed, leaving the record (and a
// retry_exhausted event) for a person instead of retrying forever. The
// operator's close door then says the chain is exhausted rather than
// claiming a retry that no longer happens.
func TestW72RetryChainIsBounded(t *testing.T) {
	setGitIdentity(t)
	const (
		base   = 50 * time.Millisecond
		ceil   = 4 * base // three doublings reach it: 50, 100, 200
		window = 10 * ceil
	)
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = ceil
	r.cfg.EndRetryWindow = window
	events := newW72EventCounter(r)
	const wi = "wi_w72_bounded"
	// Sample the backoff the product schedules, not the wall-clock gap it
	// took to run an attempt: each gap is the scheduled wait plus the
	// actor's own work, whose length under load can exceed the 50 ms steps
	// of the schedule and reorder raw gaps. j.retryIn at a
	// SessionEnd is the wait that led to it; a chain whose backoff is
	// cleared repeats the base there.
	var waitsMu sync.Mutex
	var waits []time.Duration
	hub.onEnd = func() {
		j := r.jobOf(wi)
		if j == nil {
			return
		}
		j.mu.Lock()
		in := j.retryIn
		j.mu.Unlock()
		waitsMu.Lock()
		waits = append(waits, in)
		waitsMu.Unlock()
	}

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.State = string(StateSealing)
	rec.StopReason = protocol.FinishWrapped
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	hub.fakeHub.hold(wi, rec.SessionID, api.StatusRunning) // the verify MATCHES
	hub.fakeHub.mu.Lock()
	hub.fakeHub.end[wi] = errors.New("connection refused") // only the close is unreachable
	hub.fakeHub.mu.Unlock()
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)

	// while the chain lives, the close door says it is being retried
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err == nil ||
		!strings.Contains(err.Error(), "retried with backoff") || strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("a close during a live retry chain must answer the in-progress conflict naming the retry, got %v", err)
	}

	waitFor(t, "the retry chain to hit its bound", func() bool { return events.get(EventRetryExhausted) > 0 })
	waitRetryIdle(t, r)

	ends := hub.endTimes()
	gaps := w72Gaps(ends)
	waitsMu.Lock()
	waits = append([]time.Duration(nil), waits...)
	waitsMu.Unlock()
	t.Logf("SessionEnd attempts=%d scheduled waits=%v gaps=%v", len(ends), waits, gaps)
	// enough rounds to see the schedule: the first attempt has no wait, so
	// three rounds contain two waits and the second must have doubled away
	// from the base (waits[2] == 2xbase). Two rounds would only check
	// waits[1] == base, which a chain pinned at the base also produces.
	// How many attempts fit the fixed window is the product's time bound,
	// not an assumption here; the pinned 16-hog runs still gave four.
	if len(waits) < 3 || len(waits) != len(ends) {
		t.Fatalf("too few retries to show the backoff: %d SessionEnd attempts, sampled waits %v", len(ends), waits)
	}
	// the backoff grows away from the base and holds at the ceiling: the
	// wait that led to retry i is the base doubled i-1 times, capped at
	// the ceiling. A chain pinned at the base repeats it here.
	for i := 1; i < len(waits); i++ {
		want := w72ScheduledWait(base, ceil, i)
		if waits[i] != want {
			t.Fatalf("the retry schedule did not back off: retry %d was scheduled after %s, want %s (base %s, ceiling %s; scheduled waits %v)",
				i, waits[i], want, base, ceil, waits)
		}
	}
	// the schedule above is what the product intends; the gaps are what the
	// hub observes. retry() arms its timer after SessionEnd i-1, and Go
	// timers never fire early, so gap i-1 contains the full wait waits[i]
	// and contention can only lengthen it. This is the floor check the
	// rewrite dropped (gaps[i] >= ceiling-ceiling/10 after the doubling):
	// it cannot go red under load, and a timer that ignored retryIn (the
	// #72 symptom, a chain stuck at the base) breaks it.
	for i := 1; i < len(waits) && i-1 < len(gaps); i++ {
		if gaps[i-1] < waits[i] {
			t.Fatalf("the observed retry came early: gap %d is %s, shorter than the %s scheduled before retry %d; gaps %v, scheduled waits %v",
				i-1, gaps[i-1], waits[i], i, gaps, waits)
		}
	}
	// the bound is the window, measured from the product's own chain
	// start (retrySince), not from the first SessionEnd: the first retry
	// is scheduled after it.
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the record vanished before its bound could be checked")
	}
	j.mu.Lock()
	since := j.retrySince
	j.mu.Unlock()
	exhaustedAt := events.at(EventRetryExhausted)
	if since.IsZero() || exhaustedAt.IsZero() {
		t.Fatalf("the chain's start (%v) or exhaustion (%v) was not observed", since, exhaustedAt)
	}
	if span := exhaustedAt.Sub(since); span < window {
		t.Fatalf("the chain gave up after %s from its first retry, before its %s window (%d attempts)", span, window, len(ends))
	}
	if got := events.get(EventRetryExhausted); got != 1 {
		t.Fatalf("the bound must be named once by a retry_exhausted event, got %d", got)
	}
	time.Sleep(2 * ceil) // a bound that leaks would keep calling
	if got := len(hub.endTimes()); got != len(ends) {
		t.Fatalf("the chain kept retrying past its bound: %d SessionEnd attempts (was %d)", got, len(ends))
	}
	if state, ok := r.Info(wi); !ok || state.State != StateSealing {
		t.Fatalf("the record must stay visible as %s, got %+v (present=%v)", StateSealing, state, ok)
	}

	// exhausted: the close door must not promise a retry that never comes
	err := r.Stop(context.Background(), wi, protocol.FinishWrapped)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("a close of the exhausted record must answer the in-progress conflict, got %v", err)
	}
	if !strings.Contains(ae.Detail, "retry chain is exhausted") || !strings.Contains(ae.Detail, "restart the daemon") ||
		strings.Contains(ae.Detail, "retried with backoff") {
		t.Fatalf("the exhausted chain's conflict must say nothing retries it and how to recover, got %q", ae.Detail)
	}
}

// TestRetryWindowDefaultOutlastsAnHourOfOutage: the production default of
// the chain's bound rides out an aihub outage of at least an hour, and
// the backoff ceiling fits inside it many times over.
func TestRetryWindowDefaultOutlastsAnHourOfOutage(t *testing.T) {
	var c Config
	c.fill()
	if c.EndRetryWindow < time.Hour {
		t.Fatalf("the default retry window %s gives up inside an hour-long aihub outage", c.EndRetryWindow)
	}
	if c.EndRetryWindow < 10*c.EndRetryMax {
		t.Fatalf("the default retry window %s holds fewer than ten attempts at the %s ceiling", c.EndRetryWindow, c.EndRetryMax)
	}
}

// TestW72RefusedPendingCloseReplayIsNotRetried: the restart's replay of a
// retained PAUSED record's terminal close (closePending) is a close like
// any other. A non-STALE 4xx SessionEnd refusal is parked by stopOp
// (close_refused) — the reconcile that ran the replay must not wrap that
// into DAEMON-503 and retry the whole replay (kill, seal push, WIP,
// SessionEnd) up to the chain bound.
func TestW72RefusedPendingCloseReplayIsNotRetried(t *testing.T) {
	setGitIdentity(t)
	const base = 50 * time.Millisecond
	const window = 8 * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour
	events := newW72EventCounter(r)
	const wi = "wi_w72_pending_close_refused"

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.State = string(StatePaused)
	rec.CleanPause = true
	rec.StopReason = protocol.FinishWrapped // a terminal close persisted, its SessionEnd lost
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	hub.fakeHub.hold(wi, rec.SessionID, api.StatusRunning) // the verify MATCHES
	hub.fakeHub.end[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "work item " + wi + " was deleted"}
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(window)

	if ends := hub.endTimes(); len(ends) != 1 {
		t.Fatalf("a refused pending-close replay must be attempted exactly once, got %d SessionEnd attempts", len(ends))
	}
	if got := events.get(EventCloseRefused); got != 1 {
		t.Fatalf("the refused replay must leave its one close_refused event, got %d", got)
	}
	if got := events.get(EventRetryExhausted); got != 0 {
		t.Fatalf("a refusal is final, not an exhausted retry chain: %d retry_exhausted events", got)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StatePaused) || recs[0].StopReason != protocol.FinishWrapped {
		t.Fatalf("the parked record must stay paused with its pending %s verdict, got %+v", protocol.FinishWrapped, recs)
	}
	// a second window: nothing re-runs the replay behind the parked record
	time.Sleep(window)
	if got := len(hub.endTimes()); got != 1 {
		t.Fatalf("the parked replay was retried: %d SessionEnd attempts after a second window", got)
	}
}

// TestW72RefusedPendingPauseReplayIsNotRetried: the pending-pause replay
// has the same shape — a 4xx SessionEnd(paused) is a refusal, so the
// replay is parked instead of being retried as a hub outage.
func TestW72RefusedPendingPauseReplayIsNotRetried(t *testing.T) {
	setGitIdentity(t)
	const base = 50 * time.Millisecond
	const window = 8 * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour
	events := newW72EventCounter(r)
	const wi = "wi_w72_pending_pause_refused"

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.State = string(StatePaused)
	rec.CleanPause = true
	rec.StopReason = protocol.FinishPaused // the pause persisted, its SessionEnd never landed
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	hub.fakeHub.hold(wi, rec.SessionID, api.StatusRunning) // the hub still shows running
	hub.fakeHub.end[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "machine key revoked"}
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(window)

	if ends := hub.endTimes(); len(ends) != 1 {
		t.Fatalf("a refused pending-pause replay must be attempted exactly once, got %d SessionEnd attempts", len(ends))
	}
	if got := events.get(EventCloseRefused); got != 1 {
		t.Fatalf("the refused pause replay must leave its one close_refused event, got %d", got)
	}
	if got := events.get(EventRetryExhausted); got != 0 {
		t.Fatalf("a refusal is final, not an exhausted retry chain: %d retry_exhausted events", got)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StatePaused) || recs[0].StopReason != protocol.FinishPaused {
		t.Fatalf("the parked record must stay paused with its pause intent, got %+v", recs)
	}
	time.Sleep(window)
	if got := len(hub.endTimes()); got != 1 {
		t.Fatalf("the parked pause replay was retried: %d SessionEnd attempts after a second window", got)
	}
}
