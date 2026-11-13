package driver

// crash_window_test.go: kill -9 at the driver's crash windows. A crash
// is the in-process state abandoned at an exact point (the context
// cancelled while the dispatch is held, or execution.json copied at the
// moment an event fires) followed by a fresh driver over the files a
// dead daemon would leave.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

type crashEnv struct {
	t        *testing.T
	dir      string
	execPath string
	wf       *Workflow
}

func newCrashEnv(t *testing.T, steps ...StepSpec) *crashEnv {
	dir := t.TempDir()
	return &crashEnv{t: t, dir: dir, execPath: filepath.Join(dir, capsule.ExecutionJSON),
		wf: testLane(4, steps...)}
}

// driver builds a driver over the env's capsule, seeded at spawnN (the
// spawn-journal max a restarted bridge seeds from).
func (e *crashEnv) driver(sp StepSpawner, spawnN int64, log EventLogger) *Driver {
	e.t.Helper()
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_crash", CapsuleDir: e.dir, BundleName: "b"},
		ExecPath: e.execPath,
		Workflow: e.wf,
		Spawner:  sp,
		SpawnN:   spawnN,
		Log:      log,
	})
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	return d
}

// archived records the dispatch's accepted hand-off on the call ledger,
// as the daemon does when the step calls `pf step handoff` (workflow
// contract 4): the result's outcome as its status (completed → done,
// gate_failed → fail, incomplete → blocked), its SubmittedAt as the
// result line's stamp.
func (e *crashEnv) archived(step string, n int64, res StepResult) {
	e.t.Helper()
	status := map[Outcome]string{OutcomeCompleted: stepcall.StatusDone, OutcomeGateFailed: stepcall.StatusFail, OutcomeIncomplete: stepcall.StatusBlocked}[res.Outcome]
	if status == "" {
		e.t.Fatalf("no hand-off status for outcome %q", res.Outcome)
	}
	call, err := stepcall.Append(e.dir, stepcall.Line{Kind: stepcall.KindCall, Step: step, Spawn: n, Verb: stepcall.VerbHandoff})
	if err != nil {
		e.t.Fatal(err)
	}
	line := stepcall.Line{Kind: stepcall.KindResult, Step: step, Spawn: n, Verb: stepcall.VerbHandoff, Call: call.Seq, Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: status}}
	if !res.SubmittedAt.IsZero() {
		line.At = res.SubmittedAt.UTC().Format(time.RFC3339Nano)
	}
	if _, err := stepcall.Append(e.dir, line); err != nil {
		e.t.Fatal(err)
	}
}

// crashHeld runs a driver until its last dispatch is in flight, runs
// act (the dispatch's work on disk), then kills the driver there: the
// dispatch's process is gone and the result write never happened.
func (e *crashEnv) crashHeld(prefix []fakeEntry, act func()) {
	e.t.Helper()
	sp := &fakeSpawner{script: append(prefix, fakeEntry{hold: true})}
	d := e.driver(sp, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(e.t, "the held dispatch", func() bool { return len(sp.dispatched()) == len(prefix)+1 })
	act()
	cancel()
	if err := <-errCh; err == nil {
		e.t.Fatal("the killed driver must not report a clean stop")
	}
}

// TestCrashAfterEnvelopeCollectedSettlesWithoutRerun (CR-1): the daemon
// dies after the step's hand-off was recorded on the call ledger but
// before execution.json recorded the result. The restarted driver settles
// the recorded hand-off; the step is not run again.
func TestCrashAfterEnvelopeCollectedSettlesWithoutRerun(t *testing.T) {
	e := newCrashEnv(t, spec("build"), spec("ship"))
	e.crashHeld([]fakeEntry{done(`{}`)}, func() {
		e.archived("ship", 2, StepResult{Outcome: OutcomeCompleted, Output: json.RawMessage(`{"merged":"abc"}`)})
	})
	if x := readExec(t, e.execPath); x.Steps["ship"].Status != capsule.StepInProgress {
		t.Fatalf("the crash must leave deliver in_progress: %+v", x.Steps["ship"])
	}

	sp := &fakeSpawner{}
	if err := e.driver(sp, 2, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 0 {
		t.Fatalf("a delivered step must not be dispatched again: %v", got)
	}
	x := readExec(t, e.execPath)
	if x.WorkflowStatus != StatusCompleted || x.Steps["ship"].Status != capsule.StepDone {
		t.Fatalf("the collected result must settle the step: %q %+v", x.WorkflowStatus, x.Steps["ship"])
	}
}

// TestSettledEnvelopeIsNotSettledTwice: an envelope whose result already
// landed (an incomplete since resumed) is history, not an unsettled
// result: the restart dispatches as usual.
func TestSettledEnvelopeIsNotSettledTwice(t *testing.T) {
	t.Run("resumed incomplete", func(t *testing.T) {
		e := newCrashEnv(t, spec("s"))
		sp := &fakeSpawner{script: []fakeEntry{{
			res: &StepResult{Handoff: handoffOf(stepcall.StatusBlocked, "build dependency unavailable")},
			act: func() { e.archived("s", 1, StepResult{Outcome: OutcomeIncomplete}) },
		}}}
		if err := e.driver(sp, 0, nil).Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		d := e.driver(&fakeSpawner{script: []fakeEntry{done(`{}`)}}, 1, nil)
		if err := d.ResumeCurrent(); err != nil {
			t.Fatalf("resume: %v", err)
		}
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted {
			t.Fatalf("the resumed step must run again, not re-park on its old envelope: %q", x.WorkflowStatus)
		}
	})
}

// snapshotLog copies execution.json when an event of the given kind is
// emitted: the file a kill -9 at that instant leaves behind.
type snapshotLog struct {
	mu       sync.Mutex
	kind     string
	src, dst string
	taken    bool
}

func (l *snapshotLog) Append(_, kind string, _ any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if kind != l.kind || l.taken {
		return nil
	}
	raw, err := os.ReadFile(l.src)
	if err != nil {
		return err
	}
	l.taken = true
	return os.WriteFile(l.dst, raw, 0o644)
}

// TestCrashAroundRewindStillResumes: the rewind re-entry's
// order is DURABLE — the resume order is the ledger cause itself, which
// the same single gate-FAIL write that archives the range and records the
// rewind (count, edge attribution) also records, so a kill -9 between that
// write and the re-dispatch resumes on restart (the ledger is on disk) and
// the replayed round counts exactly once. (A kill after the re-entry's step_start is
// rewind_reentry_crash_test.go's
// TestInterruptedRestartAfterRewindStepStartKeepsTheRewindResume.)
func TestCrashAroundRewindStillResumes(t *testing.T) {

	// The crash lands between the gate-FAIL transition write (the rewind
	// event fires with the write) and the re-dispatch.
	t.Run("crash between the gate write and the dispatch", func(t *testing.T) {
		e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
		crashed := filepath.Join(t.TempDir(), "execution.json")
		log := &snapshotLog{kind: "rewind", src: e.execPath, dst: crashed}
		sp := &fakeSpawner{script: []fakeEntry{
			done(`{}`),                 // build (spawn 1)
			gateFAIL(),                 // review (spawn 2): the rewind
			done(`{}`),                 // the re-entry (spawn 3)
			done(`{"verdict":"pass"}`), // review re-run (spawn 4)
		}}
		if err := e.driver(sp, 0, log).Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !log.taken {
			t.Fatal("no rewind event fired")
		}
		atCrash := tryReadExec(crashed)
		if atCrash == nil {
			t.Fatal("the crash-instant execution.json is unreadable")
		}
		// the ledger IS the resume order: the pending target and its rewind
		// cause (the fact the restart's prepareDispatch derives the resume
		// from) must be on disk at the crash instant
		if b := atCrash.Steps["build"]; b == nil || b.Status != capsule.StepPending ||
			!rewindResumeContext(b.Attempts) {
			t.Fatalf("the crash instant must hold the durable rewind order (the pending target, the ledger cause): %+v", b)
		}
		if atCrash.RewindStats.Count != 1 || len(atCrash.RewindStats.Rewinds) != 1 ||
			atCrash.RewindStats.Rewinds[0] != (capsule.RewindRecord{Gate: "review", ToStep: "build", Class: capsule.RewindCharged}) {
			t.Fatalf("the crash instant must hold the full rewind record (count + edge attribution): %+v", atCrash.RewindStats)
		}
		raw, _ := os.ReadFile(crashed)
		if err := os.WriteFile(e.execPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
		if err := e.driver(sp2, 2, nil).Run(context.Background()); err != nil {
			t.Fatalf("restarted Run: %v", err)
		}
		if got := sp2.dispatched(); len(got) == 0 || !sp2.spawnAt(0).Resume || sp2.spawnAt(0).Fresh {
			t.Fatalf("the restart must resume the re-entry, never fresh: %v", got)
		}
		if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
			t.Fatalf("the restarted lane must complete on the one rewind: %q %+v", x.WorkflowStatus, x.RewindStats)
		}
	})
}

// TestCrashAtGateVerdictKeepsTheVerdict (CR-2): a kill -9 the instant
// the gate's FAIL verdict is durable must not lose it — the restarted
// driver dispatches the rewind target, not the gate again.
func TestCrashAtGateVerdictKeepsTheVerdict(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	crashed := filepath.Join(t.TempDir(), "execution.json")
	log := &snapshotLog{kind: "gate_verdict", src: e.execPath, dst: crashed}
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{"verdict":"pass"}`)}}
	if err := e.driver(sp, 0, log).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !log.taken {
		t.Fatal("no gate_verdict event fired")
	}
	raw, _ := os.ReadFile(crashed)
	if err := os.WriteFile(e.execPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
	if err := e.driver(sp2, 2, nil).Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("restarted Run: %v", err)
	}
	got := sp2.dispatched()
	if len(got) == 0 || sp2.spawnAt(0).StepID != "build" {
		t.Fatalf("the FAIL verdict must rewind to build after the crash, dispatched %v", got)
	}
	x := readExec(t, e.execPath)
	if x.RewindStats.Count != 1 {
		t.Fatalf("one verdict, one rewind: rewind=%+v", x.RewindStats)
	}
}

// crashedStart writes the execution.json of a daemon killed after step
// a's step_start at ordinal n, before the dispatch's journal line.
func (e *crashEnv) crashedStart(n int64) {
	e.t.Helper()
	exec := skeletonExecution(WIInfo{ID: "wi_crash"}, e.wf)
	exec.WorkItemID = "wi_crash"
	exec.WorkflowStatus = StatusRunning
	exec.CurrentStep = "a"
	exec.Steps["a"].Status = capsule.StepInProgress
	exec.Steps["a"].Dispatch = n
	exec.Version = 2
	blob, _ := json.MarshalIndent(exec, "", "  ")
	if err := os.MkdirAll(filepath.Dir(e.execPath), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.execPath, blob, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// TestCrashBeforeJournalLineNeverReusesOrdinal (CR-9): the process of
// ordinal 1 started but the daemon died before journaling it, so the
// journal (the bridge's seed) knows no ordinal. Its envelope is settled
// if it wrote one; otherwise the re-dispatch takes ordinal 2, never 1.
func TestCrashBeforeJournalLineNeverReusesOrdinal(t *testing.T) {
	t.Run("envelope written", func(t *testing.T) {
		e := newCrashEnv(t, spec("a"))
		e.crashedStart(1)
		e.archived("a", 1, StepResult{Outcome: OutcomeCompleted})
		sp := &fakeSpawner{}
		if err := e.driver(sp, 0, nil).Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if x := readExec(t, e.execPath); len(sp.dispatched()) != 0 || x.WorkflowStatus != StatusCompleted {
			t.Fatalf("the orphan's envelope must settle the step: %v, %q", sp.dispatched(), x.WorkflowStatus)
		}
	})
	t.Run("no envelope", func(t *testing.T) {
		e := newCrashEnv(t, spec("a"))
		e.crashedStart(1)
		sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
		if err := e.driver(sp, 0, nil).Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := sp.dispatched(); len(got) != 1 || sp.spawnAt(0).SpawnN != 2 {
			t.Fatalf("the re-dispatch must take a fresh ordinal: %v", got)
		}
	})
}
