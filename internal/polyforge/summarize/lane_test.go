package summarize

// lane_test.go: the lane's gates, table-driven over a fake clock and a
// fake dispatcher (the package is table-testable by construction — the
// route resolves the current live dispatch's facts, the lane is pure
// logic over them). Every gate is pinned on its own: the demand gate
// (no Marked → zero calls however the transcript grows), the growth
// gate (a silent transcript stays silent; a fresh lane's zero position
// makes the first watched growth a call), the interval gate (the
// job's claim-time spacing, 60 s by default) with the in-flight marker beside it (no call starts while
// the previous one is in flight — pinned past the interval too), the
// cap (no 21st call this attempt, capped true, reset on a new dispatch
// token), the walk (failure advances, the last entry retries, success
// keeps), the memory-only restart shape, and the projection (the four
// held fields).

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// callInterval is the default per-WI call-start spacing the gate tests
// pass to Marked: the claim-time resolution of an absent
// summarize.interval_sec (polyforge#250).
const callInterval = time.Duration(workspace.DefaultSummarizeIntervalSec) * time.Second

// fakeClock is the clock seam: a movable instant.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// fakeDispatcher records every call and answers per candidate through
// answer (nil = a plain line). block, when non-nil, keeps a call in
// flight until the test closes it.
type fakeDispatcher struct {
	mu     sync.Mutex
	calls  []string // candidate, per call, in order
	answer func(candidate string) (string, error)
	block  chan struct{}
}

func (f *fakeDispatcher) Dispatch(candidate, _ string) (string, error) {
	// the call is recorded BEFORE the optional block: a blocked call is
	// in flight — started, not yet landed — and the tests count starts.
	f.mu.Lock()
	f.calls = append(f.calls, candidate)
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	if f.answer == nil {
		return "the agent is working", nil
	}
	return f.answer(candidate)
}

func (f *fakeDispatcher) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newTestLane answers a lane on a fake dispatcher (the caller wires the
// clock when a test advances time).
func newTestLane(d *fakeDispatcher) *Lane {
	l := New(d)
	return l
}

// transcript is a growing test transcript.
type transcript struct {
	t   *testing.T
	dir string
	n   int64
}

func newTranscript(t *testing.T) *transcript {
	tr := &transcript{t: t, dir: t.TempDir()}
	tr.grow(`{"t":"turn","text":"hello"}`)
	return tr
}

// grow appends one record and answers the new size.
func (tr *transcript) grow(line string) int64 {
	tr.t.Helper()
	p := filepath.Join(tr.dir, "live.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		tr.t.Fatal(err)
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		tr.t.Fatal(err)
	}
	f.Close()
	st, err := os.Stat(p)
	if err != nil {
		tr.t.Fatal(err)
	}
	tr.n = st.Size()
	return tr.n
}

// facts answers the current dispatch facts (the token names the
// dispatch; the tests change it to model a new one).
func (tr *transcript) facts(token string) Dispatch {
	return Dispatch{Token: token, Path: filepath.Join(tr.dir, "live.jsonl"), Size: tr.n}
}

// awaitCalls polls the dispatcher's call count up to want.
func awaitCalls(t *testing.T, d *fakeDispatcher, want int, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if d.n() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("only %d of %d calls dispatched within %v: %v", d.n(), want, bound, d.calls)
}

// noMoreCalls asserts for bound that no call records itself beyond
// the want the lane already made — a bounded NEGATIVE wait. The
// immediate d.n() check after a Marked cannot alone catch the
// pre-marker lane (d4d67cf9^): it recorded an unauthorized second
// start only inside the background goroutine, after BuildPrompt's
// file read, so the immediate check races past it and the later
// awaits pass with the extra calls; within the bound the goroutine
// always records itself, so the wait fails on the old lane instead of
// racing past it (the marker-gated lane never starts the call, so any
// bound sees the same count).
func noMoreCalls(t *testing.T, d *fakeDispatcher, want int, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if d.n() > want {
			t.Fatalf("a call started beyond the %d the lane made within %v: %v", want, bound, d.calls)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// awaitHeld polls the projection until the summary line want is held.
func awaitHeld(t *testing.T, l *Lane, wi, want string, bound time.Duration) Projection {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if p := l.Projection(wi); p.Summary != nil && *p.Summary == want {
			return p
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the summary %q never landed for %s within %v", want, wi, bound)
	return Projection{}
}

// awaitErr polls the projection until an error carrying substr is held;
// it answers the raw text.
func awaitErr(t *testing.T, l *Lane, wi, substr string, bound time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if p := l.Projection(wi); p.SummaryError != nil && strings.Contains(*p.SummaryError, substr) {
			return *p.SummaryError
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no error carrying %q ever landed for %s within %v", substr, wi, bound)
	return ""
}

// Nobody watching means zero calls: without a demand-marked request the
// lane never dispatches, however the transcript grows — the old
// unconditional lane's cost hole stays closed.
func TestNobodyWatchingMeansZeroCalls(t *testing.T) {
	l := newTestLane(&fakeDispatcher{})
	tr := newTranscript(t)
	for i := 0; i < 3; i++ {
		tr.grow(`{"t":"turn","text":"more work"}`)
	}
	if p := l.Projection("wi"); p.Summary != nil || p.SummaryError != nil {
		t.Fatalf("an unwatched transcript armed the lane: %+v", p)
	}
}

// A silent transcript stays silent: growth fires one call; no further
// growth → no further call, even with the interval long passed.
func TestSilentTranscriptStaysSilent(t *testing.T) {
	d := &fakeDispatcher{}
	l := newTestLane(d)
	l.now = (&fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}).Now
	tr := newTranscript(t)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 1, 2*time.Second)
	awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
	// the interval passes, but the transcript has not grown: no call
	l.now = func() time.Time { return time.Date(2026, 10, 20, 12, 2, 0, 0, time.UTC) }
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	if d.n() != 1 {
		t.Fatalf("a silent transcript fired a call: %v", d.calls)
	}
}

// The claim-time interval spaces calls (polyforge#250): table-driven
// over configured values and the default. A marked request on grown
// transcript 1 s before the interval passes starts no second call; at
// the interval the next one fires. The default case pins the spacing at
// 60 s (an absent summarize.interval_sec is identical to the #224
// behavior).
func TestIntervalSpacesCalls(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		spacing  time.Duration // the expected observable spacing
	}{
		{"configured 10 s", 10 * time.Second, 10 * time.Second},
		{"configured 120 s", 120 * time.Second, 120 * time.Second},
		{"default stays 60 s", callInterval, 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
			d := &fakeDispatcher{}
			l := newTestLane(d)
			l.now = c.Now
			tr := newTranscript(t)
			l.Marked("wi", []string{"pi:m"}, "中文", tc.interval, tr.facts("d1"))
			awaitCalls(t, d, 1, 2*time.Second)
			awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
			// 1 s short of the spacing, with fresh growth: no second call
			c.advance(tc.spacing - time.Second)
			tr.grow(`{"t":"turn","text":"grown"}`)
			l.Marked("wi", []string{"pi:m"}, "中文", tc.interval, tr.facts("d1"))
			if d.n() != 1 {
				t.Fatalf("a second call fired %v after the first (spacing %v): %v", tc.spacing-time.Second, tc.spacing, d.calls)
			}
			// the spacing passed: the next marked request fires
			c.advance(time.Second)
			l.Marked("wi", []string{"pi:m"}, "中文", tc.interval, tr.facts("d1"))
			awaitCalls(t, d, 2, 2*time.Second)
			awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
		})
	}
}

// A call in flight blocks the next start: the in-flight marker is set
// at the gate pass and cleared only in land(), so a marked request
// inside the flight — with fresh growth — starts no second call
// (the interval clock spaces starts; the marker guards the flight).
func TestInFlightCallBlocksSecondStart(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := &fakeDispatcher{block: make(chan struct{})}
	l := newTestLane(d)
	l.now = c.Now
	tr := newTranscript(t)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 1, 2*time.Second)
	// the call is in flight (blocked in the fake); growth lands, a
	// marked request arrives — no second call starts
	tr.grow(`{"t":"turn","text":"in-flight growth"}`)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	if d.n() != 1 {
		t.Fatalf("a second call started while one was in flight: %v", d.calls)
	}
	close(d.block)
	awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
}

// A call still in flight PAST the 60 s interval blocks the next start:
// a call may run up to the five-minute dispatch timeout, so the
// start-time clock alone cannot guard the flight — 60 s after a slow
// call's start the spacing passes while the call still runs, and the
// marker alone keeps the starts serialized (no overlapping calls, so
// no landing can apply out of order). After the call lands, the next
// marked request on grown transcript fires.
func TestInFlightCallPastIntervalBlocksNextStart(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := &fakeDispatcher{block: make(chan struct{})}
	l := newTestLane(d)
	l.now = c.Now
	tr := newTranscript(t)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 1, 2*time.Second)
	// the call is still in flight; the interval passes with growth to
	// summarize — the marker alone blocks the second start
	c.advance(callInterval + 30*time.Second)
	tr.grow(`{"t":"turn","text":"slow in-flight growth"}`)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	if d.n() != 1 {
		t.Fatalf("a second call started while the first was still in flight past the 60 s interval: %v", d.calls)
	}
	// the immediate check alone cannot discriminate (see noMoreCalls):
	// the pre-marker lane's second call records itself only inside
	// the background goroutine, so the check races past it — the
	// bounded negative wait holds the assertion instead
	noMoreCalls(t, d, 1, 50*time.Millisecond)
	// the flight ends; the next marked request (interval and flight both
	// passed) fires
	close(d.block)
	awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
	// the flight's proof: only the one call ever started — the count is
	// still 1 after the flight lands, before the third Marked (the
	// pre-marker lane's second call would have recorded itself by now:
	// the closed block released it too)
	if d.n() != 1 {
		t.Fatalf("a second call started while the first was in flight past the 60 s interval: %v", d.calls)
	}
	tr.grow(`{"t":"turn","text":"grown after the flight"}`)
	c.advance(callInterval)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 2, 2*time.Second)
	awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
}

// The cap keeps the last summary honest: at most 20 calls per dispatch;
// at the cap no further call starts this attempt, the last summary
// stays with its age and capped is true; the counter resets when a new
// dispatch begins (a changed token).
func TestCapKeepsLastSummaryHonest(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := &fakeDispatcher{}
	l := newTestLane(d)
	l.now = c.Now
	tr := newTranscript(t)
	for i := 0; i < capPerAttempt; i++ {
		tr.grow(`{"t":"turn","text":"growth"}`)
		l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
		awaitCalls(t, d, i+1, 2*time.Second)
		awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
		c.advance(callInterval + time.Second)
	}
	if p := l.Projection("wi"); p.Summary == nil || p.SummaryCapped == nil || !*p.SummaryCapped {
		t.Fatalf("at the cap the last summary must stay with capped true: %+v", p)
	}
	// the 21st marked request (growth + interval passed): no call
	tr.grow(`{"t":"turn","text":"past the cap"}`)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	if d.n() != capPerAttempt {
		t.Fatalf("a 21st call fired this attempt: %v", d.calls)
	}
	// a new dispatch (a changed token) resets the counter: the next
	// watched growth fires again, and capped resets with it
	tr.grow(`{"t":"turn","text":"new dispatch"}`)
	l.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d2"))
	awaitCalls(t, d, capPerAttempt+1, 2*time.Second)
	awaitHeld(t, l, "wi", "the agent is working", 2*time.Second)
	if p := l.Projection("wi"); p.SummaryCapped != nil {
		t.Fatalf("capped must reset with the counter: %+v", p)
	}
}

// A restart-shaped fresh lane (a new Lane value — the state is
// memory-only) re-arms from the zero position: the current transcript
// size is a growth again, and the interval clock starts over.
func TestFreshLaneReArmsFromZero(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := &fakeDispatcher{}
	l1 := newTestLane(d)
	l1.now = c.Now
	tr := newTranscript(t)
	l1.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 1, 2*time.Second)
	awaitHeld(t, l1, "wi", "the agent is working", 2*time.Second)
	// the restart: a whole new lane (nothing persists — no state file,
	// no recovery path); the transcript has NOT grown since the call,
	// but the fresh lane's zero position makes it a growth again
	l2 := newTestLane(d)
	l2.now = c.Now
	l2.Marked("wi", []string{"pi:m"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 2, 2*time.Second)
	awaitHeld(t, l2, "wi", "the agent is working", 2*time.Second)
}

// The walk: a failed call advances the candidate pointer (the next
// eligible call dispatches the NEXT candidate); at the last entry the
// same candidate retries; a success keeps the pointer and clears the
// error.
func TestWalkFailureAdvancesLastRetriesSuccessKeeps(t *testing.T) {
	c := &fakeClock{now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}
	d := &fakeDispatcher{}
	l := newTestLane(d)
	l.now = c.Now
	tr := newTranscript(t)
	d.answer = func(candidate string) (string, error) {
		if candidate == "pi:bad" {
			return "", errFake("raw dispatch failure: exit 1")
		}
		return "the agent is grilling the owner", nil
	}
	// the first call fails on pi:bad → the raw error lands, the pointer advances
	l.Marked("wi", []string{"pi:bad", "claude:good"}, "中文", callInterval, tr.facts("d1"))
	if raw := awaitErr(t, l, "wi", "raw dispatch failure: exit 1", 2*time.Second); !strings.Contains(raw, "exit 1") {
		t.Fatalf("the failed call must land the RAW error text: %q", raw)
	}
	// growth + interval → the next call dispatches the NEXT candidate
	tr.grow(`{"t":"turn","text":"grown"}`)
	c.advance(callInterval + time.Second)
	l.Marked("wi", []string{"pi:bad", "claude:good"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 2, 2*time.Second)
	if got := d.calls[1]; got != "claude:good" {
		t.Fatalf("the walk must dispatch the NEXT candidate, got %q: %v", got, d.calls)
	}
	if p := awaitHeld(t, l, "wi", "the agent is grilling the owner", 2*time.Second); p.SummaryError != nil {
		t.Fatalf("a success must clear the error: %+v", p)
	}
	// the pointer stays on the success; a further failure at the LAST
	// entry retries it (the pointer does not run past the end)
	d.answer = func(string) (string, error) { return "", errFake("late failure") }
	tr.grow(`{"t":"turn","text":"grown again"}`)
	c.advance(callInterval + time.Second)
	l.Marked("wi", []string{"pi:bad", "claude:good"}, "中文", callInterval, tr.facts("d1"))
	awaitCalls(t, d, 3, 2*time.Second)
	awaitErr(t, l, "wi", "late failure", 2*time.Second)
	if got := d.calls[2]; got != "claude:good" {
		t.Fatalf("the last entry must retry, not run past the end: %v", d.calls)
	}
}

// errFake is a test error whose text rides summary_error verbatim.
type errFake string

func (e errFake) Error() string { return string(e) }

// The projection's presence semantics: nothing held → all four fields
// absent (the absence is silent, never an error).
func TestProjectionPresenceSemantics(t *testing.T) {
	l := newTestLane(&fakeDispatcher{})
	if p := l.Projection("wi"); p.Summary != nil || p.SummaryError != nil || p.SummaryAgeSec != nil || p.SummaryCapped != nil {
		t.Fatalf("an unwatched WI must carry no summarize field: %+v", p)
	}
}
