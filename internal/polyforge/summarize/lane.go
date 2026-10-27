// Package summarize is the demand-gated transcript-delta watch summarize
// lane (polyforge#224): per-WI memory-only state, the growth/interval/
// in-flight/cap gates, the one-plain-line prompt, the one-shot harness
// dispatch and the on-failure candidate walk. The daemon wires it into
// the watch route (the demand=1 marker on GET /jobs/{wi}/watch); the
// CLI only adds the --demand flag. The demand-marked request IS the
// demand — its gates run on it. The dispatch facts are caller-resolved
// (the route resolves the CURRENT LIVE dispatch's transcript through
// the shared per-dispatch rule; a WI with no live running step dispatch
// has no source and no call — assumption A11), so the package is
// table-testable by construction — a fake clock and a fake dispatcher
// drive every gate.
//
// The lane re-adds the summarize feature the simplify era deleted
// (1254d93f) under the owner's explicit feature-freeze waiver (grill
// answer 2), with the demand gate closing the old unconditional-dispatch
// cost hole: a call happens only while a human is looking. Nothing of
// #181's deleted concepts returns (no heartbeat, no attention channel,
// no liveness registration); the model source is the reserved yaml key
// (never the old per-harness cli.toml key), and the state is
// memory-only (no watchers directory, no state file, no recovery path).
package summarize

import (
	"sync"
	"time"
)

// The lane's named constants — the spec's picks, never scattered
// literals.
// The minimum spacing between summarize call STARTS per WI is no
// constant: it is the workspace's live summarize.interval_sec
// (polyforge#250, read at every request per polyforge#33; 60 s unless the yaml says otherwise), passed to
// Marked. The spacing is one fact of the start gate; the in-flight
// guard is the other (wiLane.inFlight): a call may run up to the
// five-minute dispatch timeout, so the start-time clock alone cannot
// keep a second call from starting while one is still in flight — at
// least the interval between call starts per WI, AND no call starts
// while the previous one is in flight.
const (
	// capPerAttempt is the per-step-attempt call cap: at most 20 calls
	// per dispatch; at the cap the lane holds capped and no further call
	// starts this attempt. The counter resets when a new dispatch
	// begins (a changed dispatch token).
	capPerAttempt = 20
)

// Dispatch is the current LIVE dispatch's live-transcript facts the
// CALLER (the daemon's watch route) resolved: the dispatch identity (the
// latest spawn-journal entry's spawn token — the journal is
// agent-writable, so the token is only the reset key, never a trust
// boundary; the transcript path itself comes from the shared
// per-dispatch rule), the resolved transcript path and its byte size at
// gate time. The zero Dispatch (no token, no path) is no dispatch: no
// source, no growth, no call — a WI with no live running step dispatch
// never grows (assumption A11: summarize calls happen only inside a
// running step attempt; the daemon gates the resolution on the live
// dispatch itself).
type Dispatch struct {
	Token string
	Path  string
	Size  int64
}

// Lane is the per-WI memory-only summarize lane state: the delta
// position, the interval clock, the per-attempt cap counter, the
// candidate pointer, the held summary and error — all memory, all
// under the lane's own mutex. Nothing persists: a daemon restart
// (crash-equals-shutdown) resets it all — the next watched growth
// re-summarizes from the zero position and the cap counter starts over
// (a restart is rare and costs at most one re-summary). The mutex is
// never the job-table lock and is never held across the model call or
// its landing: the call runs in a background goroutine off the request
// path, and only the state transitions take the mutex.
type Lane struct {
	mu       sync.Mutex
	now      func() time.Time
	dispatch Dispatcher
	wis      map[string]*wiLane
}

// wiLane is one WI's lane state (created on the first demand-marked
// request; a fresh Lane — a restart — starts empty).
type wiLane struct {
	// dispatchTok is the dispatch identity the position was recorded
	// under: a changed token = a new dispatch began → the delta position
	// and the cap counter reset. The interval clock does NOT reset (the
	// spacing is per WI, not per dispatch).
	dispatchTok string
	// position is the transcript byte size the last call summarized
	// through (the size read at gate time; a few more bytes landing
	// while the prompt builds count as growth for the next call).
	position int64
	// lastCall is the last call's start time — the interval clock.
	lastCall time.Time
	// inFlight: a call is in flight (started, not yet landed). Set under
	// the mutex at the gate pass, cleared in land(). No call starts
	// while one is in flight: a call may run up to the five-minute
	// dispatch timeout, so the interval clock alone cannot guard it —
	// once the interval has passed since a slow call's start, the
	// spacing passes while the call still runs, and overlapping calls
	// would land out of order (a late older result overwriting a newer
	// summary, its age reset, the pointer advanced twice).
	inFlight bool
	// calls is the per-attempt (per-dispatch) call counter.
	calls int
	// pointer is the candidate pointer: a failed call advances it (the
	// last entry retries); a success keeps it.
	pointer int
	// summary/summaryAt: the held line (the last finished summarize
	// call) and its finish time ("" / zero = none held).
	summary   string
	summaryAt time.Time
	// summaryErr is the raw error text of the latest failed call (""
	// after a success or when none failed).
	summaryErr string
	// capped: the per-attempt cap is reached (no further call this
	// attempt; the last summary stays).
	capped bool
}

// New answers a lane wired to the dispatch seam (the daemon binds the
// exec dispatcher in production; the tests bind a fake). The clock seam
// (now) defaults to time.Now — the package's own tests swap it.
func New(dispatch Dispatcher) *Lane {
	return &Lane{
		now:      time.Now,
		dispatch: dispatch,
		wis:      map[string]*wiLane{},
	}
}

// Marked arms the lane for one demand-marked snapshot request (the
// human-side demand the caller already authorized — the route's peer
// check; a step-peer marker never reaches here). The demand-marked
// request IS the demand — no timestamp is recorded and no other
// consumer exists: the gates run on the very request that carries the
// marker, so the demand is fresh by construction. The gates run in the
// spec's order: configured (a non-empty candidate list) → growth (a
// changed dispatch token resets the position and the cap counter
// first; then the size must exceed the recorded position) → interval
// (now − lastCall ≥ interval) and in-flight (no call starts while
// the previous one is in flight) → cap (calls < capPerAttempt). All
// pass → the position (the size read at gate time — the bytes the call
// summarizes through) and the interval clock are recorded, the
// in-flight marker is set, the call is counted, and exactly ONE
// background call starts on the candidate the pointer names. Any gate
// failing leaves the state untouched. Unconfigured (an empty candidate
// list) is silently off: zero calls, no error anywhere — "off" is a
// legitimate state in the demand-gate era, never the old lane's typed
// failure. language is the workspace's live narration language
// (polyforge#243) the call's prompt asks for; interval is the live per-WI
// call-start spacing (polyforge#250).
func (l *Lane) Marked(wi string, models []string, language string, interval time.Duration, cur Dispatch) {
	if len(models) == 0 {
		return // the lane is off for this project (no state touched)
	}
	l.mu.Lock()
	w := l.wis[wi]
	if w == nil {
		w = &wiLane{}
		l.wis[wi] = w
	}
	// growth (A11): no dispatch → no source → no growth → no call
	if cur.Token == "" || cur.Path == "" {
		l.mu.Unlock()
		return
	}
	if w.dispatchTok != cur.Token {
		// a new dispatch began: reset the delta position and the cap
		// counter (the candidate pointer and the interval clock stay)
		w.dispatchTok = cur.Token
		w.position = 0
		w.calls = 0
		w.capped = false
	}
	if cur.Size <= w.position {
		l.mu.Unlock()
		return // a silent transcript stays silent
	}
	now := l.now()
	if w.inFlight {
		l.mu.Unlock()
		return // no call starts while the previous one is in flight
	}
	if !w.lastCall.IsZero() && now.Sub(w.lastCall) < interval {
		l.mu.Unlock()
		return // the interval spaces call starts
	}
	if w.calls >= capPerAttempt {
		l.mu.Unlock()
		return // at the cap: no further call this attempt; capped holds
	}
	// All gates pass: record the position (the size at gate time), the
	// interval clock and the in-flight marker, and count the call.
	w.position = cur.Size
	w.lastCall = now
	w.inFlight = true
	w.calls++
	if w.calls >= capPerAttempt {
		w.capped = true
	}
	idx := w.pointer
	if idx >= len(models) {
		idx = len(models) - 1 // a walked-past pointer sits on the last entry
	}
	candidate := models[idx]
	l.mu.Unlock()
	l.startCall(wi, models, language, candidate, cur)
}

// startCall runs ONE summarize call in the background: build the
// prompt from the transcript tail (I/O under NO lock), dispatch through
// the seam (the 5-minute bound), and land the result under the lane's
// mutex only. No lock that guards daemon shared data is held across the
// call or its landing; a slow or hung call never delays any snapshot
// response.
func (l *Lane) startCall(wi string, models []string, language, candidate string, cur Dispatch) {
	go func() {
		prompt, err := BuildPrompt(cur.Path, language)
		var out string
		if err == nil {
			out, err = l.dispatch.Dispatch(candidate, prompt)
		}
		l.land(wi, models, out, err)
	}()
}

// land is the background call's landing — the one state transition the
// call makes, under the lane's mutex only. The in-flight marker clears
// first (the call ended, succeeded or not — the next start is gated
// anew). A failure lands the RAW error text (the harness's own
// stderr/exit wording, never faked) and advances the candidate pointer
// (the last entry retries); a success holds the whitespace-collapsed,
// 200-character-capped line with its finish time and clears the error.
func (l *Lane) land(wi string, models []string, out string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.wis[wi]
	if w == nil {
		return // the lane entry died with a restart-shaped reset
	}
	w.inFlight = false
	if err != nil {
		w.summaryErr = err.Error()
		if w.pointer < len(models)-1 {
			w.pointer++ // a failed call advances; the last entry retries
		}
		return
	}
	w.summary, w.summaryAt, w.summaryErr = HoldLine(out), l.now(), ""
}

// Projection is the lane's held narration for one WI — the four watch
// snapshot fields, flat beside progress, all presence-typed (absence is
// silent, never an error). The summary is narration, never authority:
// the snapshot's authoritative fields are unchanged.
type Projection struct {
	Summary       *string
	SummaryError  *string
	SummaryAgeSec *int64
	SummaryCapped *bool
}

// Projection answers the lane's held fields for wi: the last finished
// line, the raw error text of the latest failed call (no value after a
// success or when none failed), the summary's age in seconds (present
// only together with a summary) and the cap-reached marker (present and
// true only while the per-attempt cap is reached). Nothing waits on a
// call in flight: the projection answers the pre-call state (which may
// lag by one call).
func (l *Lane) Projection(wi string) Projection {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.wis[wi]
	if w == nil {
		return Projection{}
	}
	p := Projection{}
	if w.summary != "" {
		s := w.summary
		p.Summary = &s
		age := int64(l.now().Sub(w.summaryAt) / time.Second)
		if age < 0 {
			age = 0
		}
		p.SummaryAgeSec = &age
	}
	if w.summaryErr != "" {
		e := w.summaryErr
		p.SummaryError = &e
	}
	if w.capped {
		c := true
		p.SummaryCapped = &c
	}
	return p
}
