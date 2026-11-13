package daemon

// decisions.go: the daemon's decision feed the
// machine-level record of every human decision the daemon observed, read
// by the Pi Web Polyforge panel and `pf watch decisions`. It replaced the
// per-chat subscription model (chat-subs.json, the resident `pf watch
// --chat` watcher and its wake lines): nothing is routed to a chat any
// more; the feed is ONE list per Host, every entry naming its Cell.
//
// Sources — all observed by the daemon itself, none polled by a client:
//   - every appended event of every Cell's event log (the observer):
//     the decision table (watcher.Classify) decides which kinds are
//     decisions; a step's result, an owner's send-back and `job_started`
//     resolve earlier entries, and a final end (completed, failed,
//     canceled) the work item's carrier_push_failed ones;
//     the owner's reply naming a question's ask (`owner_message`, kind
//     reply, ask_at = the entry's question id) marks that entry replied — no longer needing a human, still
//     unresolved until the step hands over;
//   - every runtime release (RTFinished → observe's job_finished event) with
//     the reason the close actually ended with — a pause, wrap or fail
//     through this daemon, a wrap that degraded to blocked, a bounded-fail
//     block, a supersede (which resolves what stood open);
//   - a blocked or paused work item with no local job: the Cell's
//     reconcile loop re-reads its aihub status every decisionReconcileEvery
//     and records canceled / completed / failed, or resolves the entry
//     when the work item runs again (`pf cancel` and a resume elsewhere
//     never pass through this daemon);
//   - a background task's terminal transition (task.Config.OnTerminal).
//
// <host-data-dir>/decisions.json holds the entries (newest
// decisionFeedMax, none older than decisionFeedMaxAge), written atomically
// under the feed's own mutex — a Host-global store like the chat state it
// replaced; no job-table lock is ever held while it writes.
//
// Reachability: GET /decisions is a read any local peer may make (like
// GET /status); POST /decisions/dismiss sits behind the UDS write guard
// (refuseStepOrigin), so a step or task process tree is refused
// DAEMON-403. Neither reaches aihub.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/watcher"
)

const (
	decisionsFile          = "decisions.json"
	decisionFeedMax        = 300
	decisionFeedMaxAge     = 14 * 24 * time.Hour
	decisionReconcileEvery = time.Minute
	// The driver's events of a step's accepted result (transition.go
	// emitLocked): a completed step, a gate's verdict, an incomplete step.
	evStepDone    = "step_done"
	evGateVerdict = "gate_verdict"
	evIncomplete  = "incomplete"
)

// decisionFeed is the Host-global feed store.
type decisionFeed struct {
	mu   sync.Mutex
	path string
	doc  feedDoc
	now  func() time.Time
}

type feedDoc struct {
	Version int `json:"version"`
	// Next is the id the next entry gets (ids never repeat, even after
	// the oldest entries were pruned).
	Next    int64               `json:"next"`
	Entries []protocol.Decision `json:"entries"` // oldest first
}

// newDecisionFeed loads (or starts) the feed at path.
func newDecisionFeed(path string) (*decisionFeed, error) {
	f := &decisionFeed{path: path, doc: feedDoc{Version: 1, Next: 1}, now: time.Now}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &f.doc); err != nil {
			// a corrupt feed never stops the daemon: set it aside, start empty
			aside := fmt.Sprintf("%s.corrupt-%s", path, f.now().UTC().Format("20060102T150405Z"))
			if rerr := os.Rename(path, aside); rerr != nil {
				return nil, fmt.Errorf("%s unparsable (%v) and could not be set aside: %w", decisionsFile, err, rerr)
			}
			fmt.Fprintf(os.Stderr, "daemon: %s unparsable (%v): set aside as %s; the decision feed starts empty\n", decisionsFile, err, aside)
			f.doc = feedDoc{Version: 1, Next: 1}
			return f, nil
		}
		if f.doc.Next < 1 {
			f.doc.Next = 1
		}
		for _, e := range f.doc.Entries {
			if e.ID >= f.doc.Next {
				f.doc.Next = e.ID + 1
			}
		}
	case !os.IsNotExist(err):
		return nil, err
	}
	return f, nil
}

func normProfile(p string) string {
	if p == "" {
		return cellcfg.DefaultProfile
	}
	return p
}

// persistLocked prunes and writes the feed. An open entry (it still needs
// a human) is never pruned; of the rest, entries older than
// decisionFeedMaxAge go, then the oldest until decisionFeedMax remain —
// resolved and dismissed ones before informational ones that are neither.
func (f *decisionFeed) persistLocked() error {
	cut := f.now().Add(-decisionFeedMaxAge)
	keep := f.doc.Entries[:0]
	for _, e := range f.doc.Entries {
		if at, err := time.Parse(time.RFC3339, e.At); err == nil && at.Before(cut) && !e.Open() {
			continue
		}
		keep = append(keep, e)
	}
	for _, closed := range []func(protocol.Decision) bool{
		func(e protocol.Decision) bool { return e.Resolved || e.Dismissed },
		func(e protocol.Decision) bool { return !e.Open() },
	} {
		excess := len(keep) - decisionFeedMax
		if excess <= 0 {
			break
		}
		trimmed := keep[:0]
		for _, e := range keep {
			if excess > 0 && closed(e) {
				excess--
				continue
			}
			trimmed = append(trimmed, e)
		}
		keep = trimmed
	}
	f.doc.Entries = keep
	blob, err := json.MarshalIndent(&f.doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(f.path, blob, 0o600)
}

// appendLocked adds one entry (the caller persists).
func (f *decisionFeed) appendLocked(e protocol.Decision) {
	e.ID = f.doc.Next
	f.doc.Next++
	if e.At == "" {
		e.At = f.now().UTC().Format(time.RFC3339)
	}
	f.doc.Entries = append(f.doc.Entries, e)
}

// resolveLocked marks the work item's unresolved entries of the given
// classes resolved (nil classes = every class); reports a change.
func (f *decisionFeed) resolveLocked(profile, wi string, classes map[string]bool) bool {
	changed := false
	for i := range f.doc.Entries {
		e := &f.doc.Entries[i]
		if e.Kind != protocol.DecisionWorkItem || e.WI != wi || e.Profile != profile || e.Resolved {
			continue
		}
		if classes != nil && !classes[e.Class] {
			continue
		}
		e.Resolved = true
		changed = true
	}
	return changed
}

// supersededByState: the classes a later state decision resolves.
var supersededByState = map[string]bool{"needs_input": true, "blocked": true, "paused": true}

// endedByFinal: the classes a final state decision resolves besides —
// carrier_push_failed warns that a resume may lose commits, and a work
// item that ended for good is never resumed.
var endedByFinal = map[string]bool{"carrier_push_failed": true}

// stateLike reports a decision that announces a state rather than an
// occurrence: a pending question, a work-item status. Such a decision is
// recorded once while an unresolved twin stands (a re-announced question,
// a hub read that repeats the blocked event); an occurrence (a dropped
// submit, a restart) is recorded every time it happens.
func stateLike(d watcher.Decision) bool { return d.Status || d.Class == "needs_input" }

// hasUnresolvedLocked reports an unresolved entry of the same work item,
// class, source and identifier.
func (f *decisionFeed) hasUnresolvedLocked(profile, wi string, d watcher.Decision) bool {
	for i := len(f.doc.Entries) - 1; i >= 0; i-- {
		e := f.doc.Entries[i]
		if e.Kind == protocol.DecisionWorkItem && e.WI == wi && e.Profile == profile && !e.Resolved &&
			e.Class == d.Class && e.Source == d.Source && e.QuestionID == d.QuestionID && e.Repo == d.Repo {
			return true
		}
	}
	return false
}

// record adds a classified work-item decision: a state decision first
// resolves the work item's earlier needs_input/blocked/paused entries (a
// final one its carrier_push_failed entries too), and a state-like
// decision with an unresolved twin is not added again.
// Reports whether anything changed.
func (f *decisionFeed) record(profile, wi, slug, at string, d watcher.Decision) (bool, error) {
	profile = normProfile(profile)
	f.mu.Lock()
	defer f.mu.Unlock()
	if stateLike(d) && f.hasUnresolvedLocked(profile, wi, d) {
		return false, nil
	}
	if d.Status {
		f.resolveLocked(profile, wi, supersededByState)
	}
	if d.Final {
		f.resolveLocked(profile, wi, endedByFinal)
	}
	f.appendLocked(protocol.Decision{
		At: at, Kind: protocol.DecisionWorkItem, Profile: profile, WI: wi, Slug: slug,
		Class: d.Class, Source: d.Source, Phrase: d.Phrase, QuestionID: d.QuestionID, Repo: d.Repo,
		Attention: d.Attention,
	})
	return true, f.persistLocked()
}

// recordRelease records the runtime's release: the work item's unresolved
// end entries (a state decision of another class: a terminal completed
// recorded moments before a close that ended blocked) are superseded —
// resolved, and a final release resolves its carrier_push_failed entries —
// and the release's own class is recorded unless an unresolved
// entry of that class already stands (the driver's terminal or blocked
// event, a hub read recorded it first, whatever its source).
func (f *decisionFeed) recordRelease(profile, wi, slug, at string, d watcher.Decision) (bool, error) {
	profile = normProfile(profile)
	f.mu.Lock()
	held, changed := false, false
	for i := range f.doc.Entries {
		e := &f.doc.Entries[i]
		if e.Kind != protocol.DecisionWorkItem || e.WI != wi || e.Profile != profile || e.Resolved {
			continue
		}
		if e.Class == d.Class {
			held = true
			continue
		}
		if _, isState := watcher.StatusDecision(e.Source); isState || e.Class == "terminal" || d.Final && endedByFinal[e.Class] {
			e.Resolved, changed = true, true
		}
	}
	var err error
	if changed {
		err = f.persistLocked()
	}
	f.mu.Unlock()
	if err != nil || held {
		return changed, err
	}
	return f.record(profile, wi, slug, at, d)
}

// bodyBool reads one boolean field of an event body.
func bodyBool(body json.RawMessage, field string) (bool, bool) {
	var b map[string]any
	if len(body) == 0 || json.Unmarshal(body, &b) != nil {
		return false, false
	}
	v, ok := b[field].(bool)
	return v, ok
}

// bodyString reads one string field of an event body ("" when absent).
func bodyString(body json.RawMessage, field string) string {
	var b map[string]any
	if len(body) == 0 || json.Unmarshal(body, &b) != nil {
		return ""
	}
	s, _ := b[field].(string)
	return s
}

// resolveExcept resolves the work item's unresolved entries of every class
// but the kept ones, and persists on a change.
func (f *decisionFeed) resolveExcept(profile, wi string, keep map[string]bool) error {
	profile = normProfile(profile)
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := false
	for i := range f.doc.Entries {
		e := &f.doc.Entries[i]
		if e.Kind == protocol.DecisionWorkItem && e.WI == wi && e.Profile == profile && !e.Resolved && !keep[e.Class] {
			e.Resolved, changed = true, true
		}
	}
	if !changed {
		return nil
	}
	return f.persistLocked()
}

// resolve marks the work item's entries of the classes resolved (nil =
// every class) and persists on a change.
func (f *decisionFeed) resolve(profile, wi string, classes map[string]bool) error {
	profile = normProfile(profile)
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.resolveLocked(profile, wi, classes) {
		return nil
	}
	return f.persistLocked()
}

// markReplied marks the work item's unresolved needs_input entries of the
// question askAt (its ask's stamp: the entry's QuestionID) replied at the
// reply's stamp, no longer needing attention; persists on a change.
func (f *decisionFeed) markReplied(profile, wi, askAt, at string) error {
	if askAt == "" {
		return nil
	}
	profile = normProfile(profile)
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := false
	for i := range f.doc.Entries {
		e := &f.doc.Entries[i]
		if e.Kind != protocol.DecisionWorkItem || e.WI != wi || e.Profile != profile || e.Resolved ||
			e.Class != "needs_input" || e.QuestionID != askAt || e.Replied {
			continue
		}
		e.Replied, e.RepliedAt, e.Attention = true, at, false
		changed = true
	}
	if !changed {
		return nil
	}
	return f.persistLocked()
}

// Ingest is the event-log observer's half: one appended event of the
// Cell profile. slug answers the work item's display name; it is called
// before the feed's lock is taken.
func (f *decisionFeed) Ingest(profile string, e protocol.EventEntry, slug func(string) string) {
	if e.WI == "" {
		return // daemon-scope events name no work item
	}
	var err error
	switch e.Kind {
	case evStepDone, evGateVerdict, evIncomplete:
		// the step's agent wrote its result: a `pf ask` it had open is over
		// (workflow contract v2 §5.2, PR 6d) — the owner's reply only marks
		// it replied (driver.EvOwnerMessage below), the daemon never judges
		// whether it answered the question; a new ask records a new entry
		// and supersedes this one (stateLike)
		err = f.resolve(profile, e.WI, map[string]bool{"needs_input": true})
	case driver.EvOwnerMessage:
		// the owner replied to a question: the entry whose
		// question id is the reply's ask_at no longer needs a human — it
		// stays unresolved until the step hands over; an interjection
		// (kind message) changes nothing
		if bodyString(e.Body, "kind") == "reply" {
			err = f.markReplied(profile, e.WI, bodyString(e.Body, "ask_at"), bodyString(e.Body, "at"))
		}
	case driver.EvOwnerRewind:
		// the owner sent the work item back: the question it
		// withdrew and the block it lifted are over
		err = f.resolve(profile, e.WI, map[string]bool{"needs_input": true, "blocked": true})
	case observe.EvJobStarted:
		// the work item runs (again): what was announced before is over
		err = f.resolve(profile, e.WI, nil)
	case observe.EvJobFinished:
		// the runtime's release, the close's final word (the reason it
		// actually ended with: a wrap whose push failed ends blocked, a
		// bounded fail ends blocked; a dropped record's hub status): an
		// earlier end of another class recorded for the same close is
		// superseded, a class another source already recorded is not
		// repeated; a supersede or a local seal the hub never confirmed is
		// the informational superseded; a re-queued work item resolves
		// what stood open.
		reason := bodyString(e.Body, "reason")
		s := ""
		if slug != nil {
			s = slug(e.WI)
		}
		if unconfirmed, _ := bodyBool(e.Body, "unconfirmed"); unconfirmed {
			// a local seal aihub never confirmed (its status unknown): a
			// blocked or paused end is recorded as such — tracked, so the
			// one-minute aihub re-read corrects it — and any other end is
			// the informational superseded, resolving what stood open
			// except a block or pause (an unconfirmed end never hides a
			// state a human must act on).
			if d, ok := watcher.StatusDecision(reason); ok && !d.Final {
				_, err = f.recordRelease(profile, e.WI, s, e.At, d)
			} else if d, ok := watcher.ReleaseDecision(protocol.FinishSuperseded); ok {
				if err = f.resolveExcept(profile, e.WI, map[string]bool{"blocked": true, "paused": true}); err == nil {
					d.Status = false // recorded without superseding the kept block or pause
					_, err = f.record(profile, e.WI, s, e.At, d)
				}
			}
			break
		}
		if d, ok := watcher.ReleaseDecision(reason); ok {
			if reason == protocol.FinishSuperseded {
				if err = f.resolve(profile, e.WI, nil); err == nil {
					_, err = f.record(profile, e.WI, s, e.At, d)
				}
			} else {
				_, err = f.recordRelease(profile, e.WI, s, e.At, d)
			}
		} else {
			err = f.resolve(profile, e.WI, nil)
		}
	default:
		d, ok := watcher.Classify(e.Kind, e.Body)
		if !ok {
			return
		}
		s := ""
		if slug != nil {
			s = slug(e.WI)
		}
		_, err = f.record(profile, e.WI, s, e.At, d)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: decision feed: %s %s: %v\n", e.WI, e.Kind, err)
	}
}

// RecordStatus records a state decision a daemon close or a hub read
// observed (wrapped, failed, canceled, paused, blocked); an unresolved
// twin is not recorded again.
func (f *decisionFeed) RecordStatus(profile, wi, slug, status string) error {
	d, ok := watcher.StatusDecision(status)
	if !ok {
		return nil
	}
	_, err := f.record(profile, wi, slug, "", d)
	return err
}

// RecordTask records a background task's terminal transition.
func (f *decisionFeed) RecordTask(profile string, t task.Task) error {
	d, ok := watcher.TaskDecision(string(t.State))
	if !ok {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appendLocked(protocol.Decision{
		Kind: protocol.DecisionTask, Profile: normProfile(profile), TaskID: t.ID,
		Class: d.Class, Source: d.Source, Phrase: d.Phrase, Text: t.Summary(), Attention: d.Attention,
	})
	return f.persistLocked()
}

// List answers the feed newest first with its open count.
func (f *decisionFeed) List() protocol.DecisionFeed {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := protocol.DecisionFeed{Decisions: make([]protocol.Decision, 0, len(f.doc.Entries))}
	for i := len(f.doc.Entries) - 1; i >= 0; i-- {
		e := f.doc.Entries[i]
		out.Decisions = append(out.Decisions, e)
		if e.Open() {
			out.Attention++
		}
	}
	return out
}

// Dismiss marks entries dismissed (the named ids, or every open one);
// answers how many it marked.
func (f *decisionFeed) Dismiss(ids []int64, all bool) (int, error) {
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for i := range f.doc.Entries {
		e := &f.doc.Entries[i]
		if e.Dismissed || !(want[e.ID] || all && e.Open()) {
			continue
		}
		e.Dismissed = true
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, f.persistLocked()
}

// PurgeProfile counts (and, applied, drops) the entries naming profile
// — a removed Cell's permanent cleanup (purge.go).
func (f *decisionFeed) PurgeProfile(profile string, apply bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := make([]protocol.Decision, 0, len(f.doc.Entries))
	n := 0
	for _, e := range f.doc.Entries {
		if e.Profile == profile {
			n++
			continue
		}
		keep = append(keep, e)
	}
	if !apply || n == 0 {
		return n, nil
	}
	f.doc.Entries = keep
	return n, f.persistLocked()
}

// trackedFor answers the Cell's work items whose newest state entry is an
// unresolved non-final one (blocked, paused: watcher statusRules' Final) —
// the reconcile loop's set.
func (f *decisionFeed) trackedFor(profile string) []string {
	profile = normProfile(profile)
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	for i := len(f.doc.Entries) - 1; i >= 0; i-- {
		e := f.doc.Entries[i]
		if e.Kind != protocol.DecisionWorkItem || e.Profile != profile || seen[e.WI] {
			continue
		}
		d, isState := watcher.StatusDecision(e.Source)
		if !isState {
			continue // not a state entry (terminal is final: never tracked)
		}
		seen[e.WI] = true
		if !e.Resolved && !d.Final {
			out = append(out, e.WI)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The Cell's side: observer, closes, tasks, reconcile.
// ---------------------------------------------------------------------------

// feedOf answers the decision feed: the Host's shared one, else this
// Daemon's own under its data dir (the unhosted test seam).
func feedOf(cfg Config) (*decisionFeed, error) {
	if cfg.SharedFeed != nil {
		return cfg.SharedFeed, nil
	}
	return newDecisionFeed(cfg.DataDir + "/" + decisionsFile)
}

// ingestDecision is this Cell's event-log observer half.
func (d *Daemon) ingestDecision(e protocol.EventEntry) {
	if d.feed == nil {
		return
	}
	d.feed.Ingest(d.profile(), e, d.localSlug)
}

// localSlug answers a work item's display name from the local job record
// ("" when the runtime holds none — the feed shows the id). No hub read:
// the observer runs on every append.
func (d *Daemon) localSlug(wi string) string {
	if d.rt == nil {
		return ""
	}
	if ji, ok := d.rt.Info(wi); ok {
		return ji.Slug
	}
	return ""
}

// recordTask is task.Config.OnTerminal's feed half. A synchronous task
// (message analyze / prompt: its in-process caller waits and answers from
// it) is no background-task decision.
func (d *Daemon) recordTask(t task.Task) {
	if d.feed == nil || t.Spec.Synchronous {
		return
	}
	if err := d.feed.RecordTask(d.profile(), t); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: decision feed: task %s: %v\n", t.ID, err)
	}
}

// reconcileDecisions re-reads the aihub status of this Cell's tracked
// work items (an unresolved blocked or paused state with no running local
// job): a final status is recorded, a work item that runs again resolves.
func (d *Daemon) reconcileDecisions() {
	if d.feed == nil || d.hub == nil {
		return
	}
	for _, wi := range d.feed.trackedFor(d.profile()) {
		if ji, ok := d.rt.Info(wi); ok && ji.State != runtime.StatePaused && ji.State != runtime.StateEndPending {
			continue // a live job: its own events decide
		}
		item, err := d.hub.GetWorkItem(wi)
		if err != nil || item == nil {
			continue // transport or unknown: try again next round
		}
		if _, isState := watcher.StatusDecision(item.Status); isState {
			// a final status ends the tracking; a non-final one is the
			// unresolved twin again (not recorded twice)
			_ = d.feed.RecordStatus(d.profile(), wi, item.Slug, item.Status)
		} else {
			// queued, running: the work item moves again
			_ = d.feed.resolve(d.profile(), wi, nil)
		}
	}
}

// decisionReconcileLoop runs reconcileDecisions every
// decisionReconcileEvery until ctx ends.
func (d *Daemon) decisionReconcileLoop(ctx context.Context) {
	t := time.NewTicker(decisionReconcileEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.reconcileDecisions()
		}
	}
}

// ---------------------------------------------------------------------------
// Routes.
// ---------------------------------------------------------------------------

// decisionsRoute serves GET /decisions.
func (d *Daemon) decisionsRoute(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.feed.List())
}

// decisionsDismissRoute serves POST /decisions/dismiss {"ids"} | {"all"}.
func (d *Daemon) decisionsDismissRoute(w http.ResponseWriter, r *http.Request) {
	var body protocol.DecisionDismiss
	if !decodeBody(w, r, &body) {
		return
	}
	if !body.All && len(body.IDs) == 0 {
		writeErr(w, api.ErrDaemonBadInput, "dismiss needs ids or all")
		return
	}
	for _, id := range body.IDs {
		if id <= 0 {
			writeErr(w, api.ErrDaemonBadInput, "decision ids are positive integers")
			return
		}
	}
	n, err := d.feed.Dismiss(body.IDs, body.All)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, protocol.DecisionDismissAnswer{Dismissed: n})
}
