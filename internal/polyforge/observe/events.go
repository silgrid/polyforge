package observe

// events.go: the daemon events store — one JSONL file per work item.
//
// Every observation the supervisor makes AND every driver event is
// appended as ONE line (common/protocol.EventEntry, the wire-stable shape
// the CLI and the aihub read face share) to
// <DataDir>/events/wi-<id>.jsonl; daemon-scope entries (no work item) go
// to events/daemon.jsonl. Each line carries its per-WI seq, MONOTONIC
// across daemon restarts: a work item's next seq is its file's last seq
// plus one, learned the first time the work item is touched. The watch
// face's ?after cursor reads the file. The execution-state authority is
// NEVER here — it stays the capsule's execution.json (INV-1); events are
// news, not state, and an append failure is a logged degradation, never
// a correctness dependency. A work item's file is dropped once it has
// been silent for EventsRetention (at open).

import (
	"bufio"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// Event kinds emitted by the supervisor. Driver events (step_started,
// step_done, needs_input, answered, interrupted, rewind, terminal,
// step_dead, step_failed, blocked, light_recheck_escalated (the
// mechanical fast lane's in-place escalation, polyforge#260), ...)
// land in the same log through the
// daemon binding — a supervisor verdict is recorded there, by the driver
// that consumes it. A closed set per emitter: job lifecycle, recovery
// and the push bookkeeping.
const (
	EvJobStarted       = "job_started"
	EvAgentRecovered   = "wi_agent_rederived"
	EvNeedsRecovery    = "needs_recovery"
	EvPublished        = "published"
	EvPushFailed       = "push_failed"
	EvPushBackpressure = "push_backpressure"
	// EvPushPoisonedLocal is the V8-repair classification of the LOCAL
	// gitlink precheck refusal (gitops' typed GIT-412
	// LOCAL_GITLINK_DETECTED — the poison never entered local history).
	// Deliberately a SEPARATE kind from push_failed: the M8 read-through
	// counts push_failed/published/wrap_blocked only, and a local
	// precheck refusal is not "an already-occurred boundary sync request
	// that failed" — it is the daemon declining to CREATE a commit that
	// never legally existed, so it belongs in neither M8's numerator nor
	// its denominator. A server-side pre-receive rejection of an
	// already-committed gitlink (should one ever occur again) stays an
	// ordinary push_failed and is counted, unchanged.
	EvPushPoisonedLocal = "push_poisoned_local"
	// EvTranscriptMissing is the F3 typed warning: a commit-boundary
	// transcript sync could not locate the derivation's live harness
	// transcript (the harness wrote nothing under its session store).
	// Advisory only — the commit/push proceed without the transcript;
	// one event per attempt, never a spam loop.
	EvTranscriptMissing = "transcript_missing"
	EvJobStopped        = "job_stopped"
	// EvQueueFetchDenied names the signal it actually carries: a
	// project's ready-queue fetch was AUTHORIZATION-DENIED (typed aihub
	// error with HTTP 401/403/404 — e.g. the machine key revoked
	// mid-run), raised by the auto-pull wave (daemon/daemon.go
	// fetchAutoWave).
	// Daemon-scope: appended with wi="" (the fetch owns no job) and
	// kept in the durable events log only. Edge-triggered once per
	// healthy→denied episode; the episode clears on the next successful
	// fetch. Denial MUST surface as this event, never as silent idling; transport-class
	// losses (network/5xx) stay silent idle, unchanged.
	EvQueueFetchDenied = "queue_fetch_denied"
	// EvRetiredEnvIgnored (machine-level, no work item; decision
	// 2026-10-16 G2): a retired POLYFORGE_* variable is set in the
	// daemon's environment with a value that differs from what the daemon
	// resolved — it is no longer read, so harness state would otherwise
	// move without a word.
	EvRetiredEnvIgnored = "retired_env_ignored"
	// EvSessionSuperseded is the v1.5 M4 verdict record: the periodic
	// session revalidation learned another daemon holds this work item
	// (matches=false). Payload carries the CURRENT holder's public
	// fingerprint — never a secret. The monitor stops supervising and
	// the runtime kills/quarantines/releases; no hub transition happens
	// (superseded is not terminal).
	EvSessionSuperseded = "session_superseded"
	// EvSessionVerifyFailed is the M4 transport-class record: a periodic
	// session-verify could not reach aihub. Log-only by contract — no
	// action is ever taken on a transport error; the retry is bounded.
	EvSessionVerifyFailed = "session_verify_failed"
	// EvSubmitRejected (v2 §5): an accepted submit's runtime Start failed at
	// dispatch (live or replayed after a restart); body carries the error,
	// intent and the entry's submitted_at/origin. The submit was answered
	// `accepted` earlier, so this durable event is how watchers learn it
	// did not start.
	EvSubmitRejected = "submit_rejected"
	// EvSubmitDropped (v2 §5): a persisted submit was NOT replayed after a
	// restart (resume intent, or aihub no longer shows the WI
	// queued, or the claiming journal already owns it); body names why.
	EvSubmitDropped = "submit_dropped"
	// EvDaemonRestarted (v2 §6): written into every WI the daemon
	// recovered or parked after restart reconciliation; body carries the
	// job's state.
	EvDaemonRestarted = "daemon_restarted"
	// EvModelUnhealthy is the model-health tracker's edge-triggered
	// episode event (workbench#14): daemon-scope (wi=""), one per
	// model+effort episode, body = modelhealth.Alert (never a secret).
	EvModelUnhealthy = "model_unhealthy"
	// EvConfigReload is the successful cli.toml reload record
	// (workbench#3): daemon-scope, body = applied/pending/overridden key
	// names only (never a value).
	EvConfigReload = "config_reload"
	// EvConfigReloadFailed is the refused reload record: the daemon
	// applied nothing and stays up; the body carries the affected key
	// names only.
	EvConfigReloadFailed = "config_reload_failed"
)

// EventsDir is the per-WI events directory inside the daemon data dir.
const EventsDir = "events"

const (
	// HistoryCap bounds one ?after answer (oldest first); the watcher's
	// next poll continues after the last seq it was given.
	HistoryCap = 5000
	// EventsRetention: a work item's file silent this long is removed at
	// open. Exported because it is the model-health profile window's ONE
	// owner (workbench#14): the tracker states the same rolling window.
	EventsRetention = 30 * 24 * time.Hour
	daemonEvents    = "daemon.jsonl"
)

// EventLog appends protocol.EventEntry lines to the per-WI files (each
// append opens, writes and closes: no descriptor outlives it). Safe for
// concurrent use: monitor goroutines, push sub-goroutines, the driver and
// the HTTP read face all touch it. mu guards only the file table; a work
// item's I/O runs under its own wiLog lock.
type EventLog struct {
	dir    string
	mu     sync.Mutex
	logs   map[string]*wiLog
	closed bool
	// observer is invoked once per successful append, AFTER the per-WI
	// lock is released (workbench#14): a callback that appends must not
	// deadlock. Set once before serving (SetObserver).
	observer func(protocol.EventEntry)
}

// wiLog is one work item's file and its seq counter.
type wiLog struct {
	mu     sync.Mutex
	path   string
	seq    int64 // the last seq written (0 = none)
	loaded bool  // seq was learned from the file
}

// NewEventLog opens (creating if needed) <dataDir>/events/ and drops the
// files (a work item's, or the daemon-scope one) silent past
// EventsRetention.
func NewEventLog(dataDir string) (*EventLog, error) {
	dir := filepath.Join(dataDir, EventsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if des, err := os.ReadDir(dir); err == nil {
		for _, de := range des {
			if fi, err := de.Info(); err == nil && time.Since(fi.ModTime()) > EventsRetention {
				_ = os.Remove(filepath.Join(dir, de.Name()))
			}
		}
	}
	return &EventLog{dir: dir, logs: map[string]*wiLog{}}, nil
}

func eventsFileName(wi string) string {
	if wi == "" {
		return daemonEvents
	}
	return "wi-" + url.PathEscape(wi) + ".jsonl"
}

// of answers wi's log (nil once closed); create=false answers nil for a
// work item never appended to by this process, so reads of arbitrary ids
// grow nothing.
func (l *EventLog) of(wi string, create bool) *wiLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	w := l.logs[wi]
	if w == nil && create {
		w = &wiLog{path: filepath.Join(l.dir, eventsFileName(wi))}
		l.logs[wi] = w
	}
	return w
}

// loadLocked learns the last seq from the file (w.mu held).
// An unreadable file leaves the counter unlearned (and the append that
// asked fails) rather than restart the numbering at 1.
func (w *wiLog) loadLocked() error {
	if w.loaded {
		return nil
	}
	es, err := readEventsFile(w.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range es {
		w.seq = max(w.seq, e.Seq)
	}
	w.loaded = true
	return nil
}

// Append writes one event for work item wi ("" = daemon scope). body may
// be nil. The write is best-effort by design (§五: an events failure
// never blocks the agent); a failed write is returned and consumes no
// seq.
func (l *EventLog) Append(wi, kind string, body any) error {
	entry := protocol.EventEntry{Kind: kind, WI: wi}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		entry.Body = raw
	}
	w := l.of(wi, true)
	if w == nil {
		return os.ErrClosed
	}
	w.mu.Lock()
	if err := w.loadLocked(); err != nil {
		w.mu.Unlock()
		return err
	}
	entry.Seq = w.seq + 1
	entry.At = time.Now().UTC().Format(time.RFC3339Nano) // under the lock: per WI, time order is seq order
	line, err := json.Marshal(entry)
	if err != nil {
		w.mu.Unlock()
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		w.mu.Unlock()
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		w.mu.Unlock()
		return err
	}
	w.seq = entry.Seq
	w.mu.Unlock()
	// The observer runs OUTSIDE the per-WI lock: the model-health tracker
	// ingests here, and a callback is allowed to append again.
	l.mu.Lock()
	obs := l.observer
	l.mu.Unlock()
	if obs != nil {
		obs(entry)
	}
	return nil
}

// SetObserver installs the append observer (one callback; a later call
// replaces it). It must be set before serving; an append in flight
// completes first and is not replayed.
func (l *EventLog) SetObserver(fn func(protocol.EventEntry)) {
	l.mu.Lock()
	l.observer = fn
	l.mu.Unlock()
}

// SinceAfter returns wi's entries whose seq is strictly greater than
// after, oldest first (at most HistoryCap) — the watch snapshot's ?after
// cursor (#21).
func (l *EventLog) SinceAfter(wi string, after int64) []protocol.EventEntry {
	if l.LastSeq(wi) <= after {
		return nil
	}
	es, _ := readEventsFile(filepath.Join(l.dir, eventsFileName(wi)))
	out := make([]protocol.EventEntry, 0, len(es))
	for _, e := range es {
		if e.Seq > after && e.WI == wi {
			out = append(out, e)
			if len(out) >= HistoryCap {
				break
			}
		}
	}
	return out
}

// Newest answers wi's newest entry whose kind match accepts (ok=false:
// none), reading the whole file.
func (l *EventLog) Newest(wi string, match func(kind string) bool) (protocol.EventEntry, bool) {
	es, _ := readEventsFile(filepath.Join(l.dir, eventsFileName(wi)))
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].WI == wi && match(es[i].Kind) {
			return es[i], true
		}
	}
	return protocol.EventEntry{}, false
}

// LastSeq is the highest seq ever appended for wi (0 = none). The
// counter is learned from the file once and kept; an id with no file
// adds nothing to the table.
func (l *EventLog) LastSeq(wi string) int64 {
	w := l.of(wi, false)
	if w == nil {
		if _, err := os.Stat(filepath.Join(l.dir, eventsFileName(wi))); err != nil {
			return 0
		}
		if w = l.of(wi, true); w == nil {
			return 0
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.loadLocked() != nil {
		return 0
	}
	return w.seq
}

// Close ends the log: later appends answer os.ErrClosed (an append in
// flight completes).
func (l *EventLog) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return nil
}

// ReadEventsDir reads every events file under dataDir, ordered by time
// (per work item that is seq order) — the test/diagnostic face.
func ReadEventsDir(dataDir string) ([]protocol.EventEntry, error) {
	dir := filepath.Join(dataDir, EventsDir)
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []protocol.EventEntry
	for _, de := range des {
		es, err := readEventsFile(filepath.Join(dir, de.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, es...)
	}
	at := func(e protocol.EventEntry) time.Time { t, _ := time.Parse(time.RFC3339Nano, e.At); return t }
	sort.SliceStable(out, func(i, j int) bool { return at(out[i]).Before(at(out[j])) })
	return out, nil
}

// readEventsFile parses one events file, lines of any length; a torn or
// foreign line is skipped.
func readEventsFile(path string) ([]protocol.EventEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []protocol.EventEntry
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		var e protocol.EventEntry
		if len(line) > 0 && json.Unmarshal(line, &e) == nil && e.Seq > 0 {
			out = append(out, e)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}
