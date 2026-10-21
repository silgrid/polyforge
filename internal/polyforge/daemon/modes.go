package daemon

// modes.go: the auto switch + admission (v2-converge §5), the pending-job
// queue (reference.md §6.3) and the daemon's own small state file. The daemon has
// ONE switch (auto on|off) and ONE number (max concurrent jobs): auto
// off = only explicit pf submit; auto on = the daemon also pulls the
// aihub ready queue by itself (the former drain loop). Manual submits
// stay legal either way. The switch changes ONLY how future work is
// sourced; it never touches running jobs and is fully independent from
// the shutting_down latch. Jobs/attempts bookkeeping is NOT here — it
// lives in Runtime's state.json and is never duplicated (reference.md §6.4).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// Admission error shapes (reference.md §6.1); the HTTP layer maps them to codes.
var errShuttingDown = errors.New("daemon is shutting down (DAEMON-410)")

// ModeCtl stores the auto switch and admits against the shutting_down
// latch. The latch gates BOTH external submissions and the auto-pull's
// next fetch (reference.md §6.2, the shutdown latch) — two different block paths for the same
// "no new jobs from any source" outcome.
type ModeCtl struct {
	mu           sync.Mutex
	auto         bool
	shuttingDown bool
}

// NewModeCtl starts with auto off.
func NewModeCtl() *ModeCtl { return &ModeCtl{} }

// Auto reports the auto-pull switch.
func (m *ModeCtl) Auto() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auto
}

// HeartbeatMode is the mode string the machines heartbeat still carries
// for aihub compatibility: "auto" | "manual".
func (m *ModeCtl) HeartbeatMode() string {
	if m.Auto() {
		return protocol.HeartbeatModeAuto
	}
	return protocol.HeartbeatModeManual
}

func (m *ModeCtl) ShuttingDown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shuttingDown
}

// SetShuttingDown latches the flag one-way (reference.md §6.2: latched BEFORE any
// other shutdown action).
func (m *ModeCtl) SetShuttingDown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shuttingDown = true
}

// SetAuto flips the switch; refused once shutting_down is latched.
func (m *ModeCtl) SetAuto(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shuttingDown {
		return errShuttingDown
	}
	m.auto = on
	return nil
}

// AdmitSubmit decides a single claim/resume: always legal (also
// while auto is on) except once shutting_down is latched (DAEMON-410).
func (m *ModeCtl) AdmitSubmit() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shuttingDown {
		return errShuttingDown
	}
	return nil
}

// AdmitAutoPull decides whether the auto loop may fetch the ready queue:
// only with auto on, never once shutting_down is latched.
func (m *ModeCtl) AdmitAutoPull() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.auto && !m.shuttingDown
}

// ---------------------------------------------------------------------------
// Pending-job queue (reference.md §6.3): priority DESC (urgent > high > normal >
// low), FIFO within a priority, no preemption, ordered backfill. One
// queue and one cap shared by manual submits and auto pulls.

var priorityRank = map[string]int{
	"urgent": 0, "high": 1, "normal": 2, "low": 3,
}

func rankOf(priority string) int {
	if r, ok := priorityRank[priority]; ok {
		return r
	}
	return priorityRank["normal"]
}

// qEntry is one pending submit. The JSON tags are the persisted shape
// (submit-queue.json, v2-converge §5): the answered `accepted` must
// survive a daemon restart. Only the submit's intent is persisted; the
// backoff is the running process's own and starts over at a restart.
type qEntry struct {
	WorkItemID string    `json:"work_item_id"`
	Priority   string    `json:"priority,omitempty"`
	EnqueuedAt time.Time `json:"enqueued_at"`
	// Models is the manual `pf submit --models` candidate list (top
	// precedence level); persisted so a restart before the claim keeps it.
	Models []string `json:"models,omitempty"`
	// SubmittedAt is the wall time the submit was accepted (replay
	// evidence; EnqueuedAt is the FIFO key and equals it for live submits).
	SubmittedAt time.Time `json:"submitted_at"`
	// Origin is the typed submit origin (identity model; chat_id lives in
	// Origin.ChatID). Persisted so a replayed claim hands it to the job
	// record exactly like a live submit.
	Origin runtime.Origin `json:"origin"`
	// NotBefore/Deferrals are the DAEMON-503 backoff: a claim the hub
	// could not be reached for is held back until NotBefore (Take skips
	// it, so other entries proceed) and each deferral doubles the wait.
	NotBefore time.Time `json:"-"`
	Deferrals int       `json:"-"`
}

// Hub-unavailable backoff bounds: 1s, 2s, 4s ... capped at deferralMax.
const (
	deferralBase = time.Second
	deferralMax  = 30 * time.Second
)

// deferralBackoff is the wait after the n-th (1-based) consecutive deferral.
func deferralBackoff(n int) time.Duration {
	d := deferralBase
	for i := 1; i < n && d < deferralMax; i++ {
		d *= 2
	}
	if d > deferralMax {
		d = deferralMax
	}
	return d
}

type jobQueue struct {
	mu      sync.Mutex
	pending []qEntry
	// inflight holds entries Take()n but not yet Settled: the dispatcher
	// popped them and the claim has not finished. Which entries are in
	// flight is not persisted, but the entries stay in the file until
	// Settle, so a crash between the pop and the claim journal cannot
	// lose an accepted submit; the claiming-journal dedupe makes the
	// replayed attempt harmless.
	inflight map[string]qEntry
	// occupied returns the WIs holding a concurrency slot, derived from
	// the runtime's live job list — the single source of truth, so jobs
	// recovered by a restart count like dispatched
	// ones (there is no separate running set to drift).
	occupied func() map[string]bool
	// maxConc is the cap: the configured one (--max-concurrent-jobs /
	// cli.toml [daemon] / default) until a `pf ops max N`, which lasts
	// until the daemon restarts.
	maxConc int
	// now is the clock (tests inject one); nil means time.Now.
	now func() time.Time
	// persist, when non-nil, atomically rewrites the queue file from the
	// given snapshot (pending + inflight). Called under mu, only when the
	// set of entries changes (Enqueue, Settle, Restore, PurgeOrigin).
	persist func([]qEntry) error
}

func NewJobQueue(maxConc int, occupied func() map[string]bool) *jobQueue {
	return &jobQueue{occupied: occupied, inflight: map[string]qEntry{}, maxConc: maxConc}
}

func (q *jobQueue) clock() time.Time {
	if q.now != nil {
		return q.now()
	}
	return time.Now()
}

func (q *jobQueue) SetMaxConcurrent(n int) error {
	if n < 1 {
		return fmt.Errorf("max_concurrent_jobs must be >= 1 (DAEMON-400)")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.maxConc = n
	return nil
}

func (q *jobQueue) MaxConcurrent() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.maxConc
}

// snapshotLocked is the persisted view: inflight first (older), then
// pending. Caller holds mu.
func (q *jobQueue) snapshotLocked() []qEntry {
	out := make([]qEntry, 0, len(q.inflight)+len(q.pending))
	for _, e := range q.inflight {
		out = append(out, e)
	}
	out = append(out, q.pending...)
	return out
}

func (q *jobQueue) persistLocked() error {
	if q.persist == nil {
		return nil
	}
	return q.persist(q.snapshotLocked())
}

// Enqueue appends one entry and persists the queue BEFORE returning: the
// caller answers `accepted` only on a nil error, so an accepted submit is
// always on disk. A persist failure rolls the entry back.
func (q *jobQueue) Enqueue(e qEntry) error {
	_, err := q.EnqueueIf(e, nil)
	return err
}

// EnqueueIf is Enqueue gated by admit, evaluated under the queue lock:
// admit false means nothing lands (false, nil). The auto wave passes
// ModeCtl.AdmitAutoPull so its switch re-check and the append are one
// step against the auto-off flip — SetAuto(false) precedes PurgeOrigin,
// which takes this same lock, so an entry either sees the switch off or
// is appended before the purge runs and is dropped by it (audit B3 race,
// wi_seWTgHeKtaa8). A nil admit always admits.
func (q *jobQueue) EnqueueIf(e qEntry, admit func() bool) (bool, error) {
	now := time.Now()
	if e.EnqueuedAt.IsZero() {
		e.EnqueuedAt = now
	}
	if e.SubmittedAt.IsZero() {
		e.SubmittedAt = e.EnqueuedAt
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if admit != nil && !admit() {
		return false, nil
	}
	q.pending = append(q.pending, e)
	if err := q.persistLocked(); err != nil {
		q.pending = q.pending[:len(q.pending)-1]
		return false, err
	}
	return true, nil
}

// Settle ends the inflight state of a Take()n entry (the claim attempt
// finished — success or failure) and persists the dequeue.
func (q *jobQueue) Settle(wi string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.inflight[wi]; !ok {
		return nil
	}
	delete(q.inflight, wi)
	return q.persistLocked()
}

// Requeue returns a Take()n entry to pending (the claim attempt did not
// run to a verdict — a transient hub failure); the next dispatch pass
// after the entry's backoff retries it (Take skips it until then, so
// other entries are not starved by one WI's failure). The entry keeps its
// original enqueue time, so its rank is unchanged. Nothing is written:
// the entry never left the file.
func (q *jobQueue) Requeue(wi string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.inflight[wi]
	if !ok {
		return
	}
	delete(q.inflight, wi)
	e.Deferrals++
	e.NotBefore = q.clock().Add(deferralBackoff(e.Deferrals))
	q.pending = append(q.pending, e)
}

// Restore appends the entries a restart replays and persists the queue
// in one write. A persist failure rolls them back.
func (q *jobQueue) Restore(es []qEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, es...)
	if err := q.persistLocked(); err != nil {
		q.pending = q.pending[:len(q.pending)-len(es)]
		return err
	}
	return nil
}

// PurgeOrigin drops every not-yet-started (pending) entry of the given
// origin kind from the queue and persists the result (audit B3b: auto
// off = only explicit pf submit, v2-converge §5). Inflight entries —
// already Take()n and mid-claim — are untouched. Returns the dropped
// entries; a persist failure rolls the queue back and is the error
// return.
func (q *jobQueue) PurgeOrigin(kind string) ([]qEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	original := make([]qEntry, len(q.pending))
	copy(original, q.pending)
	kept := q.pending[:0]
	var dropped []qEntry
	for _, e := range q.pending {
		if e.Origin.Kind == kind {
			dropped = append(dropped, e)
			continue
		}
		kept = append(kept, e)
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	q.pending = kept
	if err := q.persistLocked(); err != nil {
		q.pending = original
		return nil, err
	}
	return dropped, nil
}

func (q *jobQueue) IsPending(wi string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.pending {
		if e.WorkItemID == wi {
			return true
		}
	}
	return false
}

func (q *jobQueue) IsRunning(wi string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, inflight := q.inflight[wi]; inflight {
		return true // its claim is under way (B-8)
	}
	return q.occupied()[wi]
}

func (q *jobQueue) LenPending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// busyLocked counts the occupied slots: the runtime's live jobs plus the
// in-flight claims not yet among them. Caller holds mu.
func (q *jobQueue) busyLocked() int {
	busy := q.occupied()
	n := len(busy)
	for wi := range q.inflight {
		if !busy[wi] {
			n++
		}
	}
	return n
}

// FreeSlots reports how many spawn slots are open right now (the wave
// budget fetchAutoWave spends per enqueued entry, audit B3a). It counts
// only RUNNING work and claims under way; the wave budget subtracts the already-queued
// entries itself (wi_seWTgHeKtaa8) — Take() is the start-side gate.
func (q *jobQueue) FreeSlots() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.maxConc - q.busyLocked()
	if n < 0 {
		n = 0
	}
	return n
}

// Take pops the next DUE pending entry IF a slot is free (reference.md §6.3
// backfill: strictly priority desc, then FIFO; an entry still inside its
// DAEMON-503 backoff is skipped). A started job occupies its slot by
// appearing in the runtime's live list; a Take()n entry whose claim is still
// under way occupies one too (the dispatcher runs each claim in its
// own goroutine, so the live list learns of it only once the claim landed).
// An entry whose work item is already in flight waits for that claim to
// settle.
func (q *jobQueue) Take() *qEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 || q.busyLocked() >= q.maxConc {
		return nil
	}
	now := q.clock()
	best := -1
	for i := range q.pending {
		if q.pending[i].NotBefore.After(now) {
			continue
		}
		if _, inflight := q.inflight[q.pending[i].WorkItemID]; inflight {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		a, b := q.pending[i], q.pending[best]
		if rankOf(a.Priority) < rankOf(b.Priority) ||
			(rankOf(a.Priority) == rankOf(b.Priority) && a.EnqueuedAt.Before(b.EnqueuedAt)) {
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	e := q.pending[best]
	q.pending = append(q.pending[:best], q.pending[best+1:]...)
	q.inflight[e.WorkItemID] = e // stays in the file until Settle
	return &e
}

// ---------------------------------------------------------------------------
// The daemon's own state file: the auto switch (daemon-state.json,
// reference.md §6.4). The cap is never persisted: every start takes the
// configured one. Same atomic-write discipline as every daemon-side
// state file.

const daemonStateFile = "daemon-state.json"

type daemonState struct {
	Version int  `json:"version"`
	Auto    bool `json:"auto"`
}

func (d *Daemon) statePath() string {
	return filepath.Join(d.cfg.DataDir, daemonStateFile)
}

func (d *Daemon) loadDaemonState() error {
	d.modes.mu.Lock()
	d.modes.auto = false
	d.modes.mu.Unlock()
	raw, err := os.ReadFile(d.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // first boot
		}
		return err
	}
	var st daemonState
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("daemon: %s unparsable: %w", daemonStateFile, err)
	}
	d.modes.mu.Lock()
	d.modes.auto = st.Auto
	d.modes.mu.Unlock()
	return nil
}

func (d *Daemon) persistDaemonState() error {
	st := daemonState{Version: 1, Auto: d.modes.Auto()}
	blob, err := json.MarshalIndent(&st, "", "  ")
	if err != nil {
		return err
	}
	path := d.statePath()
	return atomicfile.Write(path, blob, 0o600)
}

// persistModeQuietly lands the auto switch after a change;
// a persist failure is loud but never fails the change that already
// happened in memory.
func (d *Daemon) persistModeQuietly() {
	if err := d.persistDaemonState(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: state persist failed: %v\n", err)
	}
}

// ---------------------------------------------------------------------------
// Persisted submit queue (v2-converge §5): <DataDir>/submit-queue.json,
// atomic tmp+fsync+rename on every enqueue / dequeue.

const submitQueueFile = "submit-queue.json"

type submitQueueState struct {
	Version int      `json:"version"`
	Entries []qEntry `json:"entries"`
}

func (d *Daemon) queuePath() string {
	return filepath.Join(d.cfg.DataDir, submitQueueFile)
}

func (d *Daemon) persistQueue(entries []qEntry) error {
	blob, err := json.MarshalIndent(&submitQueueState{Version: 1, Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(d.queuePath(), blob, 0o600)
}

// loadPersistedQueue reads the previous run's queue file (missing = none).
// An unparsable file is set aside as submit-queue.json.corrupt and
// reported — never silently dropped, never fatal to the start.
func (d *Daemon) loadPersistedQueue() []qEntry {
	raw, err := os.ReadFile(d.queuePath())
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "daemon: %s unreadable: %v\n", submitQueueFile, err)
		}
		return nil
	}
	var st submitQueueState
	if err := json.Unmarshal(raw, &st); err != nil {
		_ = os.Rename(d.queuePath(), d.queuePath()+".corrupt")
		fmt.Fprintf(os.Stderr, "daemon: %s unparsable (set aside as .corrupt): %v\n", submitQueueFile, err)
		return nil
	}
	return st.Entries
}

func (d *Daemon) dropReplay(e qEntry, reason string) {
	fmt.Fprintf(os.Stderr, "daemon: submit replay: %s dropped: %s\n", e.WorkItemID, reason)
	if d.obs == nil {
		return
	}
	body := map[string]any{
		"reason":       reason,
		"submitted_at": e.SubmittedAt.UTC().Format(time.RFC3339Nano),
	}
	if e.Origin.Kind != "" {
		body["origin"] = e.Origin
	}
	_ = d.obs.Events().Append(e.WorkItemID, observe.EvSubmitDropped, body)
}

// replaySubmitQueue re-enqueues the submits the previous process accepted
// but never started (v2 §5). It runs strictly after rt.Restart (which
// seeded the B1 claiming journal's starts), before the face serves, and
// asks aihub nothing:
//   - a WI the runtime already holds as a record is dropped; one whose
//     claiming-journal start is still being replayed is kept — its Start
//     queues behind the replay in the work item's runtime actor and is
//     dropped there if the replay adopted it (startEntry) — never claimed
//     twice;
//   - every other entry is enqueued: the claim's own probe and the hub's
//     CAS decide it (a refused start lands a submit_rejected event).
func (d *Daemon) replaySubmitQueue() {
	entries := d.loadPersistedQueue()
	var keep []qEntry
	for i, e := range entries {
		if e.WorkItemID == "" {
			continue
		}
		if e.Origin.Kind == runtime.OriginAuto && !d.modes.Auto() {
			// v2-converge §5 at replay (audit B3b): the previous process's
			// auto entries must not re-enter a machine the operator turned
			// auto off on — the same rule the live auto-off purge applies;
			// explicit (cli/chat) entries replay as before.
			d.dropReplay(e, "auto is off on this machine (only explicit pf submit replays)")
			continue
		}
		if ji, live := d.rt.Info(e.WorkItemID); live && ji.State != runtime.StateClaiming {
			d.dropReplay(e, "the work item is already held by this daemon (claiming-journal replay or kept record)")
			continue
		}
		crashpoint.At("queue.replay:" + strconv.Itoa(i))
		keep = append(keep, e)
	}
	// ONE write, after the filter: the file keeps the previous process's
	// entries until then, so a crash mid-replay loses none of them; it is
	// rewritten even when nothing survived, so dropped entries do not
	// replay again on the next start.
	if err := d.q.Restore(keep); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: submit replay: queue persist failed: %v\n", err)
	}
}
