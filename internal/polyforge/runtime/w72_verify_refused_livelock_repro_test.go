package runtime

// w72_verify_refused_livelock_repro_test.go — reproduction
// (no diagnosis here: this file only makes the reported symptom observable).
//
// Reported failure: when aihub refuses the session verify with a typed 4xx
// (the machine key was rotated or revoked, or the work item was deleted),
// the blocked/end_pending close is re-run in full every EndRetryBase with
// the backoff never growing — the probe saw 26 session_verify_outage events
// and 26 RTFinished(blocked) in 6 s at EndRetryBase=200ms, where exponential
// backoff allows at most 6. Each round repeats the whole failure aftermath:
// git commit+push of the capsule, the /proc orphan sweep, the task-repo WIP
// lane and two hub calls.
//
// The second entry named in the work item (read from the code, untested
// there) is the SEALING close whose SessionEnd fails while the hub stays
// reachable: stopOp schedules its own retry, but the caller's reconcile
// resets retryIn, so the retry chain stays pinned at EndRetryBase instead
// of doubling.
//
// Both tests drive a REAL daemon restart over a seeded record — the state a
// crashed daemon left behind — and measure the retry chain in a fixed
// window: 32×EndRetryBase, in which exponential backoff from EndRetryBase
// allows 5 attempts (at 1, 3, 7, 15, 31 × base). The bound below leaves room
// for two more.

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// w72WindowFactor and w72RetryBound: the measured window (×EndRetryBase) and
// the most retry rounds exponential backoff from that base allows in it.
const (
	w72WindowFactor = 32
	w72RetryBound   = 7
)

// w72RecordingHub timestamps every SessionVerify and SessionEnd that reaches
// the hub, so a test can report the retry chain's spacing and count.
type w72RecordingHub struct {
	*fakeHub
	mu       sync.Mutex
	verifyAt []time.Time
	endAt    []time.Time
}

func (h *w72RecordingHub) SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error) {
	h.stamp(&h.verifyAt)
	return h.fakeHub.SessionVerify(wi, sessionID)
}

func (h *w72RecordingHub) SessionEnd(wi string, req api.SessionEndRequest) error {
	h.stamp(&h.endAt)
	return h.fakeHub.SessionEnd(wi, req)
}

func (h *w72RecordingHub) stamp(dst *[]time.Time) {
	h.mu.Lock()
	*dst = append(*dst, time.Now())
	h.mu.Unlock()
}

func (h *w72RecordingHub) verifyTimes() []time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Time(nil), h.verifyAt...)
}

func (h *w72RecordingHub) endTimes() []time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]time.Time(nil), h.endAt...)
}

// w72Gaps answers the spacing between consecutive stamps.
func w72Gaps(at []time.Time) []time.Duration {
	gaps := make([]time.Duration, 0, len(at))
	for i := 1; i < len(at); i++ {
		gaps = append(gaps, at[i].Sub(at[i-1]).Round(time.Millisecond))
	}
	return gaps
}

// w72BackoffGrows reports whether the spacing between retry rounds grew
// across the window — at least 3× from the first gap to the last. An
// exponential chain doubles per round; a chain whose backoff is cleared stays
// flat. Fewer than three rounds cannot show a trend and count as "grew".
func w72BackoffGrows(at []time.Time) bool {
	gaps := w72Gaps(at)
	if len(gaps) < 2 {
		return true
	}
	return gaps[len(gaps)-1] >= 3*gaps[0]
}

// w72EventCounter counts the runtime events a test cares about through the
// Config.EventTap seam (the daemon's durable events log).
type w72EventCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newW72EventCounter(r *Runtime) *w72EventCounter {
	c := &w72EventCounter{counts: map[string]int{}}
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		c.mu.Lock()
		c.counts[kind]++
		c.mu.Unlock()
	}
	return c
}

func (c *w72EventCounter) get(kind string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[kind]
}

// The refused-verify entry: a RUNNING record whose verify the hub refuses
// (4xx) must not re-run the failure aftermath every EndRetryBase.
func TestW72RefusedVerifyCloseIsNotRerunEveryBase(t *testing.T) {
	setGitIdentity(t)
	const base = 100 * time.Millisecond
	const window = w72WindowFactor * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour // the ceiling stays out of the window
	events := newW72EventCounter(r)
	const wi = "wi_w72_refused_verify"
	// the machine key was revoked: verify (and therefore every close sent
	// under that session) is refused with a typed 4xx
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
	ends := hub.endTimes()
	attempts := hub.verifyTimes()
	finished := 0
	for {
		m, ok := toOB.Pop()
		if !ok {
			break
		}
		if m.Kind == protocol.RTFinished && m.Reason == protocol.FinishBlocked {
			finished++
		}
	}
	state, ok := r.Info(wi)
	t.Logf(" evidence (refused verify): EndRetryBase=%s, window=%s, %d session_verify_outage events, "+
		"%d RTFinished(blocked), %d SessionEnd attempts, %d verify calls, final record=%+v (present=%v); cleanup gaps=%v",
		base, window, outages, finished, len(ends), len(attempts), state, ok, w72Gaps(ends))
	if outages == 0 {
		t.Fatal("no session_verify_outage event: the refused-verify path never ran, so this repro is vacuous")
	}
	if outages > w72RetryBound && !w72BackoffGrows(ends) {
		t.Fatalf("REPORTED FAILURE: a verify the hub refuses with a 4xx re-ran the whole "+
			"blocked/end_pending failure aftermath %d times in %s (EndRetryBase=%s, cleanup gaps=%v) — exponential "+
			"backoff from the base allows at most %d rounds in that window, and the spacing never grew; the retry "+
			"chain neither backs off nor terminates",
			outages, window, base, w72Gaps(ends), w72RetryBound)
	}
}

// The sealing entry: a SEALING record whose SessionEnd the hub refuses while
// the verify matches. stopOp schedules its own backoff; the caller must not
// clear it.
func TestW72SelfScheduledSealCloseRetryKeepsItsBackoff(t *testing.T) {
	setGitIdentity(t)
	const base = 100 * time.Millisecond
	const window = w72WindowFactor * base
	hub := &w72RecordingHub{fakeHub: newFakeHub()}
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	r.cfg.EndRetryBase = base
	r.cfg.EndRetryMax = time.Hour
	const wi = "wi_w72_seal_backoff"

	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	// the crashed daemon's close: SEALING with its verdict already recorded
	rec.State = string(StateSealing)
	rec.StopReason = protocol.FinishWrapped
	rec.RemoteURL = initGitCapsule(t, r.restoredJob(rec))
	hub.fakeHub.hold(wi, rec.SessionID, api.StatusRunning) // the verify MATCHES
	hub.fakeHub.end[wi] = errors.New("connection refused") // only the SessionEnd fails
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	time.Sleep(window)

	ends := hub.endTimes()
	state, ok := r.Info(wi)
	t.Logf(" evidence (sealing close): EndRetryBase=%s, window=%s, %d SessionEnd attempts, final record=%+v (present=%v); retry gaps=%v",
		base, window, len(ends), state, ok, w72Gaps(ends))
	if len(ends) == 0 {
		t.Fatal("no SessionEnd attempt: the recorded close was never retried, so this repro is vacuous")
	}
	if len(ends) > w72RetryBound && !w72BackoffGrows(ends) {
		t.Fatalf("REPORTED FAILURE (sealing entry): a SEALING close whose SessionEnd keeps failing was "+
			"re-run %d times in %s (EndRetryBase=%s, retry gaps=%v) — the backoff stopOp schedules is reset by the "+
			"calling reconcile, so the chain stays pinned at the base instead of doubling (exponential allows at "+
			"most %d attempts in that window, and the spacing never grew)",
			len(ends), window, base, w72Gaps(ends), w72RetryBound)
	}
}
