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
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
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
		wf: &Workflow{Steps: steps, MaxTotal: 4, MaxPerStep: 2}}
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

// archived writes what stepRun.Wait archives on collection.
func (e *crashEnv) archived(step string, n int64, res StepResult) {
	e.t.Helper()
	res.StepID, res.WIID, res.SpawnN = step, "wi_crash", n
	blob, _ := json.Marshal(res)
	path := filepath.Join(e.dir, capsule.DirArtifacts, "steps", step, spawnTokenDir(n), "result.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o644); err != nil {
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
// dies after the step's envelope was collected (archived, drop consumed)
// but before execution.json recorded the result. The restarted driver
// settles the collected envelope; the step is not run again.
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
	if x.WorkflowStatus != StatusCompleted || x.Steps["ship"].Output["merged"] != "abc" {
		t.Fatalf("the collected result must settle the step: %q %+v", x.WorkflowStatus, x.Steps["ship"])
	}
}

// TestCrashWithEnvelopeAtDropPathSettles (CR-1): the step wrote its
// envelope but the daemon died before collecting it (the orphan is
// reaped at restart). The envelope at the drop path is settled.
func TestCrashWithEnvelopeAtDropPathSettles(t *testing.T) {
	e := newCrashEnv(t, spec("a"))
	e.crashHeld(nil, func() {
		blob, _ := json.Marshal(StepResult{StepID: "a", WIID: "wi_crash", SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(`{"x":1}`)})
		path := PendingResultPath(e.dir, 1)
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, blob, 0o644)
	})
	sp := &fakeSpawner{}
	if err := e.driver(sp, 1, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 0 {
		t.Fatalf("no re-dispatch expected: %v", got)
	}
	if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("completed expected: %q", x.WorkflowStatus)
	}
	if _, err := os.Stat(PendingResultPath(e.dir, 1)); !os.IsNotExist(err) {
		t.Fatalf("the settled drop must be consumed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, capsule.DirArtifacts, "steps", "a", "s1", "result.json")); err != nil {
		t.Fatalf("the settled drop must be archived: %v", err)
	}
}

// TestSettledEnvelopeIsNotSettledTwice: an envelope whose result already
// landed (a question since answered, an incomplete since resumed) is
// history, not an unsettled result: the restart dispatches as usual.
func TestSettledEnvelopeIsNotSettledTwice(t *testing.T) {
	t.Run("answered question", func(t *testing.T) {
		e := newCrashEnv(t, spec("s"))
		sp := &fakeSpawner{script: []fakeEntry{{
			res: &StepResult{Outcome: OutcomeNeedsInput, QuestionID: "q1", Question: "which?"},
			act: func() {
				e.archived("s", 1, StepResult{Outcome: OutcomeNeedsInput, QuestionID: "q1", Question: "which?"})
			},
		}}}
		d := e.driver(sp, 0, nil)
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- d.Run(ctx) }()
		waitUntil(t, "the question", func() bool {
			x := tryReadExec(e.execPath)
			return x != nil && x.Steps["s"].PendingQuestion != nil
		})
		cancel()
		<-errCh
		if err := d.Answer("q1", "this", "bob"); err != nil {
			t.Fatalf("answer: %v", err)
		}
		// killed after the answer, before the re-dispatch started
		sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
		if err := e.driver(sp2, 1, nil).Run(context.Background()); err != nil {
			t.Fatalf("restarted Run: %v", err)
		}
		if got := sp2.dispatched(); len(got) != 1 {
			t.Fatalf("the answered step must be re-dispatched with its answer: %v", got)
		}
	})
	t.Run("resumed incomplete", func(t *testing.T) {
		e := newCrashEnv(t, spec("s"))
		sp := &fakeSpawner{script: []fakeEntry{{
			res: &StepResult{Outcome: OutcomeIncomplete, Output: json.RawMessage(`{}`)},
			act: func() { e.archived("s", 1, StepResult{Outcome: OutcomeIncomplete, Output: json.RawMessage(`{}`)}) },
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

// TestCrashAroundLightRewindStillResumes (polyforge#222): the light
// order is DURABLE — the resume order is the ledger cause itself, which
// the same single gate-FAIL write that archives the range and routes the
// verdict also records, so a kill -9 between that write and the
// re-dispatch resumes on restart (the ledger and the rewind input are on
// disk), and a kill after the re-entry's step_start resumes through the
// in_progress continuation. No window degrades the light re-entry to
// fresh.
func TestCrashAroundLightRewindStillResumes(t *testing.T) {
	lightOut := `{"verdict":"fail","fail_reason":"comment wording only","findings":[{"severity":"blocking","detail":"d","text_only":true}]}`

	// Window 1: the crash lands between the gate-FAIL transition write
	// (the rewind event fires with the write) and the re-dispatch.
	t.Run("crash between the gate write and the dispatch", func(t *testing.T) {
		e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
		crashed := filepath.Join(t.TempDir(), "execution.json")
		log := &snapshotLog{kind: "rewind", src: e.execPath, dst: crashed}
		sp := &fakeSpawner{script: []fakeEntry{
			done(`{}`), // build (spawn 1)
			{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(lightOut)}}, // review (spawn 2): the light rewind
			done(`{}`),                 // the light re-entry (spawn 3)
			done(`{"verdict":"PASS"}`), // review re-run (spawn 4)
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
		// the ledger IS the resume order: the pending target, its light
		// cause (the fact the restart's prepareDispatch derives the resume
		// from) and the rewind input must all be on disk at the crash instant
		if b := atCrash.Steps["build"]; b == nil || b.Status != capsule.StepPending ||
			!lightRewindContext(b.Attempts) ||
			b.PendingRewindInput == nil || b.PendingRewindInput.Note != LightRewindNote {
			t.Fatalf("the crash instant must hold the durable light order (the pending target, the light ledger cause, the rewind input): %+v", b)
		}
		raw, _ := os.ReadFile(crashed)
		if err := os.WriteFile(e.execPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
		if err := e.driver(sp2, 2, nil).Run(context.Background()); err != nil {
			t.Fatalf("restarted Run: %v", err)
		}
		if got := sp2.dispatched(); len(got) == 0 || !sp2.spawnAt(0).Resume || sp2.spawnAt(0).Fresh {
			t.Fatalf("the restart must resume the light re-entry, never fresh: %v", got)
		}
		if ri := sp2.spawnAt(0).RewindInput; ri == nil || ri.Note != LightRewindNote || !strings.Contains(ri.Reason, "comment wording only") {
			t.Fatalf("the restart must carry the light rewind input: %+v", ri)
		}
		if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
			t.Fatalf("the restarted lane must complete on the one light slot: %q %+v", x.WorkflowStatus, x.RewindStats)
		}
	})

	// Window 2: the crash lands after the re-entry's step_start — the
	// dispatch is in flight, the step in_progress — and the restart
	// resumes through the in_progress continuation.
	t.Run("crash after the re-entry's step_start", func(t *testing.T) {
		e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
		e.crashHeld([]fakeEntry{
			done(`{}`), // build (spawn 1)
			{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(lightOut)}}, // review (spawn 2): the light rewind
		}, func() {}) // the held dispatch IS the light re-entry (spawn 3)
		sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
		if err := e.driver(sp2, 3, nil).Run(context.Background()); err != nil {
			t.Fatalf("restarted Run: %v", err)
		}
		if got := sp2.dispatched(); len(got) == 0 || !sp2.spawnAt(0).Resume {
			t.Fatalf("the restart must resume the in-flight light re-entry: %v", got)
		}
		if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
			t.Fatalf("the resumed lane must complete on the one light slot: %q %+v", x.WorkflowStatus, x.RewindStats)
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
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{"verdict":"PASS"}`)}}
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
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driver(sp2, 2, nil).Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("restarted Run: %v", err)
	}
	got := sp2.dispatched()
	if len(got) == 0 || sp2.spawnAt(0).StepID != "build" {
		t.Fatalf("the FAIL verdict must rewind to build after the crash, dispatched %v", got)
	}
	x := readExec(t, e.execPath)
	if x.RewindStats.Count != 1 || x.ReviewRounds["review"] != 1 {
		t.Fatalf("one verdict, one rewind: rewind=%+v rounds=%+v", x.RewindStats, x.ReviewRounds)
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
		blob, _ := json.Marshal(StepResult{StepID: "a", WIID: "wi_crash", SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(`{}`)})
		_ = os.MkdirAll(filepath.Dir(PendingResultPath(e.dir, 1)), 0o755)
		if err := os.WriteFile(PendingResultPath(e.dir, 1), blob, 0o644); err != nil {
			t.Fatal(err)
		}
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
