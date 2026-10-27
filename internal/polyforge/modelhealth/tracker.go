package modelhealth

// tracker.go: the daemon-side owner of the durable model-health snapshot.
// Open rebuilds the aggregate from events/*.jsonl, writes
// <DataDir>/model-health.json atomically, seeds the episode map from the
// log and starts the tracker goroutine. Ingest is the append observer's
// entry point and only enqueues the entry; the tracker goroutine folds
// the queue, rebuilds the profile, evaluates the episodes, emits the
// model_unhealthy events and writes the snapshot. Profile/Summary serve
// the last snapshot the tracker goroutine built.
//
// No tracker I/O ever runs in a caller's goroutine: the observe layer
// invokes the observer after releasing its per-WI file lock, and the
// observer itself does only an in-memory enqueue. The O(N) rebuild, the
// model_unhealthy append and the snapshot write all happen on the
// tracker's own goroutine, never in the appender's (which may hold the
// driver's per-work-item lock).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// ProfileFile is the durable aggregate inside the daemon data dir.
const ProfileFile = "model-health.json"

// Tracker owns the pending ingest queue, the in-memory records, the
// armed-episode map and the snapshot writer.
type Tracker struct {
	dataDir   string
	path      string
	retention time.Duration
	clock     func() time.Time
	emit      func(Alert)

	// pendingMu guards pending, pendingFires and closed. The observer's
	// hot path (Ingest) takes only this lock, never the aggregation lock,
	// so it never waits on the tracker's O(N) work.
	pendingMu    sync.Mutex
	pending      []protocol.EventEntry
	pendingFires []Alert
	closed       bool

	mu          sync.Mutex
	entries     []protocol.EventEntry
	alerted     map[string]bool
	snapshot    Profile
	snapshotSet bool

	wake      chan struct{}
	flush     chan chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Open rebuilds the snapshot from events/*.jsonl, writes it atomically
// and returns the live tracker. emit (may be nil) is called once per
// newly unhealthy model+effort episode, on the tracker goroutine; its own
// model_unhealthy append is something Ingest later observes as a no-op.
func Open(dataDir string, retention time.Duration, clock func() time.Time, emit func(Alert)) (*Tracker, error) {
	if clock == nil {
		clock = time.Now
	}
	if retention <= 0 {
		retention = observe.EventsRetention
	}
	entries, err := observe.ReadEventsDir(dataDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("modelhealth: read events: %w", err)
		}
		entries = nil
	}
	t := &Tracker{
		dataDir:   dataDir,
		path:      filepath.Join(dataDir, ProfileFile),
		retention: retention,
		clock:     clock,
		emit:      emit,
		alerted:   map[string]bool{},
		wake:      make(chan struct{}, 1),
		flush:     make(chan chan struct{}),
		done:      make(chan struct{}),
	}
	now := t.clock()
	t.entries = prune(entries, now, retention)
	t.mu.Lock()
	r := build(t.entries, now, retention)
	t.snapshot, t.snapshotSet = r.profile, true
	for k := range r.reported {
		t.alerted[k] = true
	}
	initialFires := evaluateLocked(t.alerted, r.alerts)
	t.mu.Unlock()
	// The initial alerts are emitted by the tracker goroutine, never by
	// Open's caller (which may be assembling the daemon).
	t.pendingMu.Lock()
	t.pendingFires = append(t.pendingFires, initialFires...)
	t.pendingMu.Unlock()
	if err := t.writeNow(); err != nil {
		return nil, err
	}
	t.wg.Add(1)
	go t.loop()
	select {
	case t.wake <- struct{}{}:
	default:
	}
	return t, nil
}

// Ingest enqueues one appended event and returns. It performs no build,
// no file write and no emit: the observer runs in the appender's
// goroutine, which may hold a per-work-item lock. Enqueueing never
// blocks, so the tracker's own model_unhealthy append re-enters here
// safely.
func (t *Tracker) Ingest(e protocol.EventEntry) {
	if !retainedKind(e.Kind) {
		return
	}
	t.pendingMu.Lock()
	if t.closed {
		t.pendingMu.Unlock()
		return
	}
	t.pending = append(t.pending, e)
	t.pendingMu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Profile returns the most recent snapshot the tracker goroutine built.
// The window's from/to advance only when the records change (an Open or
// an Ingest); between data changes every reader sees the same document,
// which keeps GET /models and `pf ops models --json` byte-consistent.
func (t *Tracker) Profile(now time.Time) Profile {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snapshotSet {
		return t.snapshot
	}
	r := build(t.entries, now, t.retention)
	t.snapshot, t.snapshotSet = r.profile, true
	return t.snapshot
}

// Summary is the additive GET /status object.
func (t *Tracker) Summary(now time.Time) Summary {
	p := t.Profile(now)
	return Summary{Window: p.Window, Warnings: p.Warnings}
}

// Close flushes the final snapshot and stops the tracker goroutine.
// Idempotent.
func (t *Tracker) Close() error {
	t.closeOnce.Do(func() {
		t.pendingMu.Lock()
		t.closed = true
		t.pendingMu.Unlock()
		close(t.done)
		t.wg.Wait()
	})
	return nil
}

// sync blocks until every event enqueued before the call has been folded
// and its snapshot written. Tests use it to observe the asynchronous
// tracker deterministically; production never waits on ingestion.
func (t *Tracker) sync() {
	ack := make(chan struct{})
	select {
	case t.flush <- ack:
	case <-t.done:
		return
	}
	<-ack
}

// loop is the tracker's own goroutine: it folds the queue, evaluates the
// episodes, emits model_unhealthy and writes the snapshot.
func (t *Tracker) loop() {
	defer t.wg.Done()
	for {
		select {
		case <-t.wake:
			t.process()
		case ack := <-t.flush:
			t.process()
			close(ack)
		case <-t.done:
			t.process()
			return
		}
	}
}

// process folds every pending event, rebuilds the profile, evaluates the
// armed episodes and writes one snapshot — all on the tracker goroutine.
func (t *Tracker) process() {
	t.pendingMu.Lock()
	batch := t.pending
	t.pending = nil
	fires := t.pendingFires
	t.pendingFires = nil
	t.pendingMu.Unlock()

	if len(batch) > 0 {
		now := t.clock()
		t.mu.Lock()
		t.entries = append(t.entries, batch...)
		t.entries = prune(t.entries, now, t.retention)
		r := build(t.entries, now, t.retention)
		t.snapshot, t.snapshotSet = r.profile, true
		fires = append(fires, evaluateLocked(t.alerted, r.alerts)...)
		t.mu.Unlock()
		if err := t.writeNow(); err != nil {
			fmt.Fprintf(os.Stderr, "modelhealth: snapshot write failed (degraded, events are the truth): %v\n", err)
		}
	}
	for _, a := range fires {
		t.fire(a)
	}
}

func (t *Tracker) writeNow() error {
	t.mu.Lock()
	p := t.snapshot
	t.mu.Unlock()
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("modelhealth: marshal profile: %w", err)
	}
	return atomicfile.Write(t.path, append(raw, '\n'), 0o644)
}

func (t *Tracker) fire(a Alert) {
	if t.emit != nil {
		t.emit(a)
	}
}

// retainedKind is the closed set of kinds build reads. The tracker keeps
// only these: published/transcript_missing and the rest are about half
// the real volume and change nothing here.
func retainedKind(kind string) bool {
	switch kind {
	case kindStepStarted, kindStepFailed, kindStepDone, kindGateVerdict, kindNeedsInput, kindIncomplete, kindUnhealthy:
		return true
	default:
		return false
	}
}

// prune drops irrelevant kinds, unparseable records and records at or
// before now-retention (the window is half-open: (start, now]).
func prune(entries []protocol.EventEntry, now time.Time, retention time.Duration) []protocol.EventEntry {
	cut := now.Add(-retention)
	out := entries[:0]
	for _, e := range entries {
		if !retainedKind(e.Kind) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || !at.After(cut) {
			continue
		}
		out = append(out, e)
	}
	return out
}
