package daemon

// verifyqueue.go: the heavy-run admission queue. Every
// dispatch of a step declaring `resources: { admission: heavy }` asks it
// before its process starts; a run is admitted only while two gates are
// open at once:
//
//   - a global heavy-run slot: fewer than [daemon] max_concurrent_bars
//     (live, default 2) admitted runs are running;
//   - the load: the 1-minute load average (/proc/loadavg) is below
//     [daemon] bar_load_threshold (live, default cores-4, at least 1) —
//     ignored when the load cannot be read, and while no admitted run is
//     running (so a busy machine still makes progress, one run at a time).
//
// A denied run waits here in the admission order (vqRank): the work item's
// priority, then the time it first queued (the driver passes the stamp its
// execution.json recorded, so a daemon restart re-queues it in place),
// then arrival — no process, no budget, no job slot (occupiedSlots leaves
// a queued run out). Position i is the i-th in that order. The dispatch
// tick re-evaluates the queue; an admission or a release re-evaluates it
// at once. (The per-repo bar lock whose reservations made a third, repo
// gate left with workflow contract 4,.)
//
// State is memory only (the driver's execution.json is the durable record).
// Locking (AGENTS.md runtime locking): mu guards the in-memory queue; the
// load is read before it is taken.

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// verifyQueue is the heavy-run admission. One per Host (shared by its
// Cells), or one per daemon when unhosted.
type verifyQueue struct {
	mu sync.Mutex
	// maxBars / threshold are the configured values (0 = the default).
	maxBars   int
	threshold float64
	// load reads the 1-minute load average (ok=false: unknown, the gate is
	// ignored); now stamps entries.
	load    func() (float64, bool)
	now     func() time.Time
	queued  []*vqEntry
	running map[*vqEntry]bool
	seq     int64 // arrivals so far (vqRank's last key)
}

// vqRank is a run's place in the admission order: the work
// item's priority, then the time the run first queued, then its arrival.
type vqRank struct {
	prio  int
	since time.Time
	seq   int64
}

// before reports whether a goes ahead of b.
func (a vqRank) before(b vqRank) bool {
	if a.prio != b.prio {
		return a.prio < b.prio
	}
	if !a.since.Equal(b.since) {
		return a.since.Before(b.since)
	}
	return a.seq < b.seq
}

// vqEntry is one ask: queued until granted, then running until released.
type vqEntry struct {
	q        *verifyQueue
	req      driver.AdmissionRequest
	ready    chan struct{}
	granted  bool
	released bool
	position int
	reason   string
	seq      int64 // its arrival
}

// rank answers the entry's place in the admission order.
func (e *vqEntry) rank() vqRank {
	return vqRank{prio: rankOf(e.req.Priority), since: e.req.Since, seq: e.seq}
}

func newVerifyQueue(maxBars int, threshold float64) *verifyQueue {
	return &verifyQueue{maxBars: maxBars, threshold: threshold, load: readLoadAvg, now: time.Now, running: map[*vqEntry]bool{}}
}

// SetLimits re-sets the configured slot count and load threshold (0 =
// the default) and admits what they now allow.
func (q *verifyQueue) SetLimits(maxBars int, threshold float64) {
	load, ok := q.load()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.maxBars, q.threshold = maxBars, threshold
	q.admitLocked(load, ok)
}

// limitsLocked answers the effective slot count and load threshold.
func (q *verifyQueue) limitsLocked() (int, float64) {
	n, thr := q.maxBars, q.threshold
	if n <= 0 {
		n = cli.DefaultMaxConcurrentBars
	}
	if thr <= 0 {
		thr = cli.DefaultBarLoadThreshold()
	}
	return n, thr
}

// enter queues one ask and admits what the gates allow now.
func (q *verifyQueue) enter(req driver.AdmissionRequest) *vqEntry {
	load, ok := q.load()
	q.mu.Lock()
	defer q.mu.Unlock()
	e := &vqEntry{q: q, req: req, ready: make(chan struct{})}
	q.seq++
	e.seq = q.seq
	if e.req.Since.IsZero() {
		e.req.Since = q.now()
	}
	q.queued = append(q.queued, e)
	q.admitLocked(load, ok)
	return e
}

// Tick re-evaluates the queue (the dispatch tick).
func (q *verifyQueue) Tick() {
	load, ok := q.load()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.admitLocked(load, ok)
}

// admitLocked grants queued asks in the admission order while the gates
// allow, and records each remaining ask's position and reason. q.mu held.
func (q *verifyQueue) admitLocked(load float64, loadOK bool) {
	sort.SliceStable(q.queued, func(i, j int) bool { return q.queued[i].rank().before(q.queued[j].rank()) })
	limit, thr := q.limitsLocked()
	queued := q.queued
	q.queued = make([]*vqEntry, 0, len(queued))
	for _, e := range queued {
		reason := ""
		switch {
		case len(q.running) >= limit:
			reason = fmt.Sprintf("heavy-run slots full (%d/%d running)", len(q.running), limit)
		case loadOK && len(q.running) > 0 && load >= thr:
			reason = fmt.Sprintf("load %.2f at or above the threshold %s", load, strconv.FormatFloat(thr, 'g', -1, 64))
		}
		if reason == "" {
			e.granted, e.reason, e.position = true, "", 0
			q.running[e] = true
			close(e.ready)
			continue
		}
		e.reason = reason
		q.queued = append(q.queued, e)
	}
	for i, e := range q.queued {
		e.position = i + 1
	}
}

// release ends an ask: an admitted run gives its slot back, a queued one
// leaves the queue; what the freed slot allows is admitted at once.
func (e *vqEntry) release() {
	q := e.q
	load, ok := q.load()
	q.mu.Lock()
	defer q.mu.Unlock()
	if e.released {
		return
	}
	e.released = true
	if e.granted {
		delete(q.running, e)
	} else {
		for i, x := range q.queued {
			if x == e {
				q.queued = append(q.queued[:i], q.queued[i+1:]...)
				break
			}
		}
	}
	q.admitLocked(load, ok)
}

// queuedFor reports whether a run of wi waits in the queue (it then holds
// no job slot).
func (q *verifyQueue) queuedFor(wi string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.queued {
		if e.req.WI == wi {
			return true
		}
	}
	return false
}

// verifyWaiting is one work item's queued state for the watch and status
// faces.
type verifyWaiting struct {
	position int
	reason   string
	since    time.Time
}

// waiting answers wi's queued state; false when no run of wi is queued.
func (q *verifyQueue) waiting(wi string) (verifyWaiting, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.queued {
		if e.req.WI == wi {
			return verifyWaiting{position: e.position, reason: e.reason, since: e.req.Since}, true
		}
	}
	return verifyWaiting{}, false
}

// statusFace answers wi's queued state as the status face's job row shows
// it (nil = not queued).
func (q *verifyQueue) statusFace(wi string) *protocol.WatchVerifyQueue {
	w, ok := q.waiting(wi)
	if !ok {
		return nil
	}
	return &protocol.WatchVerifyQueue{Position: w.position, Reason: w.reason, Since: w.since.UTC().Format(time.RFC3339)}
}

// readLoadAvg reads the 1-minute load average from /proc/loadavg
// (ok=false where there is none: the load gate is then ignored).
func readLoadAvg() (float64, bool) {
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString(' ')
	if err != nil && line == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// ---------------------------------------------------------------------------
// The driver's face.

// cellAdmission is one Cell's driver.VerifyAdmission over the (possibly
// shared) queue.
type cellAdmission struct{ d *Daemon }

var _ driver.VerifyAdmission = cellAdmission{}

// Enter implements driver.VerifyAdmission.
func (a cellAdmission) Enter(req driver.AdmissionRequest) driver.AdmissionTicket {
	return a.d.verifyQ.enter(req)
}

// Ready implements driver.AdmissionTicket.
func (e *vqEntry) Ready() <-chan struct{} { return e.ready }

// State implements driver.AdmissionTicket.
func (e *vqEntry) State() (int, string) {
	e.q.mu.Lock()
	defer e.q.mu.Unlock()
	return e.position, e.reason
}

// Release implements driver.AdmissionTicket.
func (e *vqEntry) Release() { e.release() }
