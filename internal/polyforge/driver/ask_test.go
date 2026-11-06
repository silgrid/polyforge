package driver

// ask_test.go: the awaiting-owner state (ask.go, workflow contract v2
// §5.2, PR 6c) over the scripted spawner: an open `pf ask` of the running
// dispatch marks it awaiting (the event, the supervisor fact, the slot
// release), the warm window ends the process and the step stays awaiting
// with no failure archived and no re-dispatch; a restarted driver parks on
// the open ask without an interruption archive or a dispatch; a stale ask
// (another dispatch) parks nothing; a v1 needs_input envelope and its
// answer are mirrored as an ask and a reply line.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// ownerWaits records the OwnerWait calls (the runtime's job flag).
type ownerWaits struct {
	mu    sync.Mutex
	calls []bool
}

func (o *ownerWaits) set(w bool) {
	o.mu.Lock()
	o.calls = append(o.calls, w)
	o.mu.Unlock()
}

func (o *ownerWaits) last() (bool, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.calls) == 0 {
		return false, false
	}
	return o.calls[len(o.calls)-1], true
}

// askEnv is a driver over a scripted spawner with a link, a fast ask poll
// and a short warm window, over the capsule dir.
func askEnv(t *testing.T, dir string, sp *fakeSpawner, interrupted *InterruptedDispatch, warm time.Duration) (*Driver, *safeLog, *ownerWaits, *supervise.Link) {
	t.Helper()
	log := &safeLog{}
	ow := &ownerWaits{}
	link := supervise.NewLink("wi_run")
	d, err := New(Config{
		WI:          WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath:    filepath.Join(dir, capsule.ExecutionJSON),
		Workflow:    testLane(4, spec("fix"), spec("review")),
		Spawner:     sp,
		Log:         log,
		Link:        link,
		Limits:      supervise.Defaults{AskWarmWindow: warm},
		AskPoll:     5 * time.Millisecond,
		OwnerWait:   ow.set,
		Interrupted: interrupted,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, log, ow, link
}

// drainFacts answers every fact kind the link queued so far.
func drainFacts(link *supervise.Link) []string {
	var out []string
	for _, f := range link.Facts.Drain() {
		out = append(out, f.FactKind())
	}
	return out
}

func writeAsk(t *testing.T, dir, step string, spawn int64, text string) capsule.AskLine {
	t.Helper()
	l := capsule.AskLine{At: time.Now().UTC().Format(time.RFC3339Nano), Step: step, Spawn: spawn, Kind: capsule.AskKindAsk, Text: text}
	if err := capsule.AppendAsk(dir, l); err != nil {
		t.Fatal(err)
	}
	return l
}

// The ask parks the dispatch awaiting the owner: the event carries the
// question, the supervisor is told, the slot is released; the warm window
// ends the process and the step stays in_progress on the same dispatch —
// no step_failed, no step_dead, no second dispatch, and the loop parks
// (a cancel ends it) with the slot still released.
func TestAskParksTheDispatchAwaitingOwnerThroughTheWarmWindow(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, done(`{}`), done(`{}`)}}
	d, log, ow, link := askEnv(t, dir, sp, nil, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	exec := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	n := exec.Steps["fix"].Dispatch
	if n != 1 {
		t.Fatalf("dispatch ordinal %d", n)
	}
	drainFacts(link)
	ask := writeAsk(t, dir, "fix", n, "lean or compatible?")

	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
	ev := log.byKind(EvAwaitingOwner)[0].body
	if ev["step"] != "fix" || ev["spawn_n"] != n || ev["ask_at"] != ask.At || ev["question_id"] != ask.At || ev["question"] != "lean or compatible?" {
		t.Fatalf("awaiting_owner body: %v", ev)
	}
	if w, ok := ow.last(); !ok || !w {
		t.Fatalf("the slot was not released: %v", ow.calls)
	}
	waitUntil(t, "the step_owner_wait fact", func() bool {
		for _, k := range drainFacts(link) {
			if k == "step_owner_wait" {
				return true
			}
		}
		return false
	})
	// the warm window ends the process: the fake's Terminate is the proof
	waitUntil(t, "the process to be ended", func() bool { return sp.runs[0].terminated() })
	// ... and the step stays awaiting: in_progress on the same dispatch,
	// nothing archived, no re-dispatch, the park announced once
	time.Sleep(60 * time.Millisecond)
	exec = readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	st := exec.Steps["fix"]
	if st.Status != capsule.StepInProgress || st.Dispatch != n || len(st.Attempts) != 0 || st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("the step moved: %+v", st)
	}
	if exec.WorkflowStatus != StatusRunning {
		t.Fatalf("workflow %s", exec.WorkflowStatus)
	}
	for _, k := range []string{"step_failed", "step_dead", "step_started"} {
		if n := len(log.byKind(k)); (k == "step_started" && n != 1) || (k != "step_started" && n != 0) {
			t.Fatalf("%s events: %d (%v)", k, n, log.entries)
		}
	}
	if got := sp.dispatched(); len(got) != 1 {
		t.Fatalf("re-dispatched: %v", got)
	}
	if n := len(log.byKind(EvAwaitingOwner)); n != 1 {
		t.Fatalf("awaiting_owner announced %d times in one life", n)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Run returned while awaiting the owner: %v", err)
	default:
	}
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("a cancelled park returns the cancellation")
	}
	// the record is the question's: one ask line, open
	if open := capsule.OpenAsk(mustAsks(t, dir), "fix", n); open == nil || open.At != ask.At {
		t.Fatalf("the ask: %+v", open)
	}
}

// A restarted driver finds the open ask and parks without an interruption
// archive, without a claim re-entry booking and without a dispatch; the
// event is re-announced with resumed: true and the slot stays released.
func TestAskRestartReconciliationParksWithoutDispatch(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
	d, log, _, _ := askEnv(t, dir, sp, nil, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	ask := writeAsk(t, dir, "fix", 1, "which one?")
	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
	cancel() // the daemon goes down with the process alive
	<-errCh

	// the restart: the recovery lane relaunches with the interruption cause
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`)}}
	d2, log2, ow2, link2 := askEnv(t, dir, sp2, &InterruptedDispatch{Detail: FailDaemonRestart}, time.Hour)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	errCh2 := make(chan error, 1)
	go func() { errCh2 <- d2.Run(ctx2) }()
	waitUntil(t, "the re-announced awaiting_owner", func() bool { return len(log2.byKind(EvAwaitingOwner)) == 1 })
	ev := log2.byKind(EvAwaitingOwner)[0].body
	if ev["resumed"] != true || ev["ask_at"] != ask.At || ev["question"] != "which one?" {
		t.Fatalf("re-announce body: %v", ev)
	}
	time.Sleep(40 * time.Millisecond)
	if got := sp2.dispatched(); len(got) != 0 {
		t.Fatalf("the restart re-dispatched: %v", got)
	}
	exec := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	st := exec.Steps["fix"]
	// no interruption archived (the `interrupted` transition lands with no
	// state change) and no wall booked: the dispatch is settled by its ask
	if st.Status != capsule.StepInProgress || st.Dispatch != 1 || len(st.Attempts) != 0 || st.BudgetConsumed.WallMs != 0 {
		t.Fatalf("the restart archived or moved the step: %+v", st)
	}
	if w, ok := ow2.last(); !ok || !w {
		t.Fatalf("the slot is not released after the restart: %v", ow2.calls)
	}
	waitUntil(t, "the waiting-human fact", func() bool {
		for _, k := range drainFacts(link2) {
			if k == "step_waiting_human" {
				return true
			}
		}
		return false
	})
	select {
	case err := <-errCh2:
		t.Fatalf("Run returned: %v", err)
	default:
	}
	// a reply closes the ask: the loop re-plans and dispatches the step
	// again (contract 1 resumes it in place; the relay is PR 6d's)
	if err := capsule.AppendAsk(dir, capsule.AskLine{At: time.Now().UTC().Format(time.RFC3339Nano), Kind: capsule.AskKindReply, AskAt: ask.At, Text: "this one", By: "owner"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the re-dispatch after the reply", func() bool { return len(sp2.dispatched()) >= 1 })
	if sp2.spawnAt(0).StepID != "fix" || !sp2.spawnAt(0).Resume {
		t.Fatalf("the re-dispatch: %v", sp2.dispatched())
	}
	if w, _ := ow2.last(); w {
		t.Fatalf("the slot was not taken back before the dispatch: %v", ow2.calls)
	}
	if err := <-errCh2; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// An ask of another dispatch (an earlier spawn, another step) parks
// nothing: the step completes as scripted.
func TestStaleAskParksNothing(t *testing.T) {
	dir := t.TempDir()
	writeAsk(t, dir, "fix", 7, "stale spawn")
	writeAsk(t, dir, "review", 1, "stale step")
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`)}}
	d, log, ow, _ := askEnv(t, dir, sp, nil, time.Hour)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(log.byKind(EvAwaitingOwner)) != 0 {
		t.Fatal("a stale ask parked the step")
	}
	if got := sp.dispatched(); len(got) != 2 {
		t.Fatalf("dispatches: %v", got)
	}
	for _, w := range ow.calls {
		if w {
			t.Fatalf("the slot was released for a stale ask: %v", ow.calls)
		}
	}
}

func mustAsks(t *testing.T, dir string) []capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// orderLog records, at the awaiting_owner event's append, whether the
// runtime's owner-wait flag (ownerWaits) already says the slot is
// released.
type orderLog struct {
	ow       *ownerWaits
	mu       sync.Mutex
	events   int
	released []bool
}

func (l *orderLog) Append(_, kind string, _ any) error {
	if kind != EvAwaitingOwner {
		return nil
	}
	w, ok := l.ow.last()
	l.mu.Lock()
	l.events++
	l.released = append(l.released, ok && w)
	l.mu.Unlock()
	return nil
}

// TestAwaitingOwnerReleasesTheSlotBeforeTheEvent: the running slot is
// released before the awaiting_owner event is written — on the dispatch
// wait's mark and on a restart's re-announcement alike — so an observer
// that reads the queue as soon as it sees the event never finds the
// asking step still holding its slot (the 6c/6d e2e race: the event used
// to be appended first, and a status read right after it saw Running 1
// under load).
func TestAwaitingOwnerReleasesTheSlotBeforeTheEvent(t *testing.T) {
	dir := t.TempDir()
	ow := &ownerWaits{}
	log := &orderLog{ow: ow}
	d, err := New(Config{
		WI:        WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath:  filepath.Join(dir, capsule.ExecutionJSON),
		Workflow:  testLane(4, spec("fix"), spec("review")),
		Spawner:   &fakeSpawner{},
		Log:       log,
		Link:      supervise.NewLink("wi_run"),
		OwnerWait: ow.set,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ask := &capsule.AskLine{At: "2026-11-11T00:00:00.000000001Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindAsk, Text: "which way?"}
	d.markAwaitingOwner("fix", 1, ask)
	again := &capsule.AskLine{At: "2026-11-11T00:00:01.000000001Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindAsk, Text: "still which way?"}
	d.announceParkedAsk(again)
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.events != 2 {
		t.Fatalf("want the mark's and the re-announcement's awaiting_owner events, got %d", log.events)
	}
	for i, r := range log.released {
		if !r {
			t.Fatalf("awaiting_owner event %d was written while the slot was still held (owner-wait calls %v)", i+1, ow.calls)
		}
	}
}
