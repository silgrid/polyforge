package driver

// driver_test.go: the Run-loop tests — step sequencing, the outcome
// handlers (completed / gate_failed / needs_input / incomplete),
// rewind flows, terminal handling, crash recovery through the durable
// file, and result validation (artifact manifest verification).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ---------------------------------------------------------------------------
// The scripted spawner: one fakeEntry per expected dispatch, FIFO.

type fakeEntry struct {
	res  *StepResult
	err  error
	hold bool // block Wait until ctx or Terminate
	// cancelErr is what a held Wait returns on ctx end (default ctx.Err()).
	cancelErr error
	// spawnErr makes SpawnStep itself fail (a harness that died at startup).
	spawnErr error
	// act runs inside Wait before the result is returned: the step's
	// work on the tree (e.g. a commit in a task repo).
	act func()
	// disposition is the run's answer to an owner message (relay_test.go;
	// "" = started), refuse the error it refuses one with.
	disposition string
	refuse      error
}

type fakeRun struct {
	sp   StepSpawn
	e    fakeEntry
	mu   sync.Mutex
	term bool
	rel  chan struct{}
	// the relay face (relay_test.go): the dispatch's delivery watch the
	// test feeds, the owner messages the run accepted, and how it
	// answers them (fakeEntry.disposition, fakeEntry.refuse).
	watch *deliveryWatch
	sent  []spawn.Message
}

func (r *fakeRun) Wait(ctx context.Context) (*StepResult, error) {
	if r.e.hold {
		select {
		case <-ctx.Done():
			if r.e.cancelErr != nil {
				return nil, r.e.cancelErr
			}
			return nil, ctx.Err()
		case <-r.rel:
		}
	}
	if r.e.act != nil {
		r.e.act()
	}
	if r.e.res == nil {
		return nil, r.e.err
	}
	clone := *r.e.res
	if clone.StepID == "" {
		clone.StepID = r.sp.StepID
	}
	if clone.WIID == "" {
		clone.WIID = r.sp.WIID
	}
	if clone.SpawnN == 0 {
		clone.SpawnN = r.sp.SpawnN
	}
	return &clone, nil
}

func (r *fakeRun) Terminate(bound time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.term = true
	if r.rel != nil {
		select {
		case <-r.rel:
		default:
			close(r.rel)
		}
	}
	return nil
}

func (r *fakeRun) terminated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.term
}

type fakeSpawner struct {
	mu     sync.Mutex
	spawns []StepSpawn
	runs   []*fakeRun
	script []fakeEntry
}

func (s *fakeSpawner) SpawnStep(ctx context.Context, sp StepSpawn) (StepRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.script) == 0 {
		return nil, fmt.Errorf("fake spawner: script exhausted (step %q, spawn %d)", sp.StepID, sp.SpawnN)
	}
	e := s.script[0]
	s.script = s.script[1:]
	if e.spawnErr != nil {
		s.spawns = append(s.spawns, sp)
		return nil, e.spawnErr
	}
	run := &fakeRun{sp: sp, e: e, watch: newDeliveryWatch(sp.Harness)}
	if e.hold {
		run.rel = make(chan struct{})
	}
	s.spawns = append(s.spawns, sp)
	s.runs = append(s.runs, run)
	return run, nil
}

func (s *fakeSpawner) dispatched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.spawns))
	for _, sp := range s.spawns {
		out = append(out, fmt.Sprintf("%s@%d(resume=%v)", sp.StepID, sp.SpawnN, sp.Resume))
	}
	return out
}

func (s *fakeSpawner) spawnAt(i int) StepSpawn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawns[i]
}

// entry builders: each a dispatch ending with its `pf step handoff`
// (workflow contract 4) — the accepted hand-off the daemon records on the
// call ledger, as the run's Wait answers it (ledgerHandoff's shape).
func handoffOf(status, reason string) *stepcall.Handoff {
	return &stepcall.Handoff{Status: status, Reason: reason}
}

// done's argument is what the step built (it reaches no record: the
// hand-off is the step's whole result); the others route a gate's fail
// and a step's blocked.
func done(string) fakeEntry {
	return fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusDone, "")}}
}
func gateFAIL() fakeEntry {
	return fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusFail, "tests red")}}
}
func incomplete() fakeEntry {
	return fakeEntry{res: &StepResult{Handoff: handoffOf(stepcall.StatusBlocked, "build dependency unavailable")}}
}

// runEnv builds a fresh capsule + driver over a scripted spawner.
func runEnv(t *testing.T, steps ...StepSpec) (*Driver, *fakeSpawner, string) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, steps...),
		Spawner:  sp,
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, execPath
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---------------------------------------------------------------------------
// sequencing

func TestRunHappyPathSequencesAndCompletes(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"), spec("b"))
	sp.script = []fakeEntry{done(`{"summary":"A"}`), done(`{"summary":"B"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 2 {
		t.Fatalf("two dispatches, got %v", got)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || exec.CurrentStep != "b" {
		t.Fatalf("final: status=%q current=%q", exec.WorkflowStatus, exec.CurrentStep)
	}
	if exec.Steps["a"].Status != capsule.StepDone || exec.Steps["b"].Status != capsule.StepDone {
		t.Fatalf("all steps done: %+v", exec.Steps)
	}
	// init + start/result a + start/result b + terminal = 6 writes,
	// one Version bump each (the observe C3 signal).
	if exec.Version != 6 {
		t.Fatalf("version: want 6, got %d", exec.Version)
	}
	if d.SpawnN() != 2 {
		t.Fatalf("spawn counter: %d", d.SpawnN())
	}
}

func TestRunTerminalFileStopsImmediately(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{}
	// Pre-write a completed execution.json; Run must not dispatch.
	// (The driver is constructed AFTER the file exists — its in-memory
	// state is the load-time snapshot.)
	exec := skeletonExecution(WIInfo{ID: "wi_run"}, testLane(0, []StepSpec{spec("a")}...))
	exec.WorkflowStatus = StatusCompleted
	exec.Steps["a"].Status = capsule.StepDone
	exec.Version = 7
	blob, _ := json.MarshalIndent(exec, "", "  ")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{spec("a")}...),
		Spawner:  sp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sp.dispatched()) != 0 {
		t.Fatalf("a terminal workflow dispatches nothing: %v", sp.dispatched())
	}
	if v := readExec(t, execPath).Version; v != 7 {
		t.Fatalf("no writes expected on a terminal load, version %d", v)
	}
}

func TestRunResumeOfCrashedInProgressStep(t *testing.T) {
	// A crash between the step_start write and the process existing
	// leaves the step in_progress on disk; the next Run re-dispatches it.
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	exec := skeletonExecution(WIInfo{ID: "wi_run"}, testLane(0, []StepSpec{spec("a")}...))
	exec.Steps["a"].Status = capsule.StepInProgress
	exec.CurrentStep = "a"
	exec.Version = 2 // init + the crashed start write
	blob, _ := json.MarshalIndent(exec, "", "  ")
	os.MkdirAll(filepath.Dir(execPath), 0o755)
	os.WriteFile(execPath, blob, 0o644)

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{spec("a")}...),
		Spawner:  sp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 1 || !sp.spawnAt(0).Resume {
		t.Fatalf("the crashed step must re-dispatch as a resume: %v", got)
	}
	final := readExec(t, execPath)
	if s := final.WorkflowStatus; s != StatusCompleted {
		t.Fatalf("completed expected, got %q", s)
	}
	if final.Version <= 2 {
		t.Fatalf("the resumed run must keep bumping the version: %d", final.Version)
	}
}

// ---------------------------------------------------------------------------
// gate fail / rewind

func TestRunGateFailRewindRecovers(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("build"), spec("polish"), gateSpec("check", "build"))
	sp.script = []fakeEntry{
		done(`{"b":1}`), done(`{"p":1}`), gateFAIL(),
		done(`{"b":2}`), done(`{"p":2}`), done(`{"verdict":"pass"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The dispatch ledger: the full recovery, exactly the s12 shape.
	// The rewound target RESUMES its previous round (every
	// gate→target rewind, structural findings included); the step between
	// target and gate and the gate's own re-dispatch are FRESH
	// (resume=false): the gate's prior conversation judged the version
	// being redone. The rewind still never resets the gate itself (INV-1g
	// — its candidate pointer is preserved).
	got := sp.dispatched()
	want := []string{"build@1(resume=false)", "polish@2(resume=false)", "check@3(resume=false)",
		"build@4(resume=true)", "polish@5(resume=false)", "check@6(resume=false)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dispatch ledger:\n got  %v\n want %v", got, want)
	}
	if re := sp.spawnAt(3); !re.RewindResume {
		t.Fatalf("the rewind re-entry is a rewind resume: %+v", re)
	}
	if g := sp.spawnAt(5); g.Resume || !g.Fresh {
		t.Fatalf("a gate re-entered after its own gate-fail rewind must dispatch Fresh, not Resume: %+v", g)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("the flow must complete after the rewind: %q", exec.WorkflowStatus)
	}
	// Each successful completion adds an "accepted" attempt (R14), plus
	// the rewind archive: the target build is archived gate_fail_rewind,
	// polish (strictly between target and gate) superseded_by_rewind
	// (owner ruling grill-201-r1, polyforge#201).
	for _, tc := range []struct{ id, cause string }{
		{"build", CauseGateFailRewind}, {"polish", CauseSupersededByRewind},
	} {
		var hasRewind bool
		for _, att := range exec.Steps[tc.id].Attempts {
			if att.Cause == tc.cause && att.RewindGate == "check" {
				hasRewind = true
			}
		}
		if !hasRewind {
			t.Fatalf("%s missing %s archive: %+v", tc.id, tc.cause, exec.Steps[tc.id].Attempts)
		}
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("stats: rewind=%+v", exec.RewindStats)
	}
}

func TestRunGateFailTerminate(t *testing.T) {
	// A gate with no on_fail rewind target has no rewind edge: its FAIL
	// verdict ends the workflow failed_gate (WF-102).
	d, sp, execPath := runEnv(t, spec("a"), StepSpec{ID: "check", Gate: true})
	sp.script = []fakeEntry{done(`{}`), gateFAIL()}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 2 {
		t.Fatalf("no dispatch after the terminal: %v", got)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedGate || exec.Steps["check"].Status != capsule.StepInProgress {
		t.Fatalf("terminate shape: status=%q check=%+v", exec.WorkflowStatus, exec.Steps["check"])
	}
}

// ---------------------------------------------------------------------------
// incomplete / errors

func TestRunIncompleteParksBlocked(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"), spec("b"))
	sp.script = []fakeEntry{done(`{}`), incomplete()}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusBlocked || exec.CurrentStep != "b" || exec.Steps["b"].Status != capsule.StepInProgress {
		t.Fatalf("incomplete parks blocked, position frozen: %+v", exec)
	}
	if exec.Steps["a"].Status != capsule.StepDone {
		t.Fatalf("prior steps stay done: %+v", exec.Steps["a"])
	}
	// The owner's resume verdict: unblock + re-dispatch in place, then
	// the re-invoked loop carries the flow to completion.
	if err := d.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if s := readExec(t, execPath).WorkflowStatus; s != StatusRunning {
		t.Fatalf("the resume must write running back: %q", s)
	}
	sp.script = []fakeEntry{done(`{"b":2}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	if s := readExec(t, execPath).WorkflowStatus; s != StatusCompleted {
		t.Fatalf("completed after resume: %q", s)
	}
	// A resume outside blocked is refused — the loop owns running starts.
	if err := d.ResumeCurrent(); err == nil {
		t.Fatal("resume outside blocked must be refused")
	}
}

// polyforge#116: the §6.4 owner resume must keep the spawn ledger
// honest — the verdict consumes no derivation ordinal, so the loop's
// re-dispatch of the frozen step takes exactly the next ordinal. (The
// old shape wrote the resume's step_start at n+1 and the loop then
// dispatched at n+2: a phantom derivation between the verdict and the
// re-dispatch it names — the e2e fakepi's ordinal indexing skips a
// plan action.)
func TestResumeCurrentLedgerHonest(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"), spec("b"))
	sp.script = []fakeEntry{done(`{}`), incomplete()}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	pre := len(sp.dispatched())
	if err := d.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if s := readExec(t, execPath).WorkflowStatus; s != StatusRunning {
		t.Fatalf("the verdict writes running back: %q", s)
	}
	if s := readExec(t, execPath).Steps["b"]; s == nil || s.Status != capsule.StepInProgress {
		t.Fatalf("the verdict keeps the position frozen: %+v", s)
	}
	sp.script = []fakeEntry{done(`{"b":2}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	got := sp.dispatched()
	if len(got) != pre+1 {
		t.Fatalf("the owner resume re-dispatches the frozen step exactly once: %v (pre %d)", got, pre)
	}
	if !sp.spawnAt(pre).Resume {
		t.Fatalf("the re-dispatch is a resume in place: %v", got)
	}
	if n := sp.spawnAt(pre).SpawnN; n != 3 {
		t.Fatalf("the re-dispatch carries ordinal %d, want 3 — no phantom derivation between the verdict and the dispatch (ledger honesty)", n)
	}
	if s := readExec(t, execPath).Steps["b"]; s == nil || s.Status != capsule.StepDone {
		t.Fatalf("the re-dispatched step completes: %+v", s)
	}
}

func TestRunContextCancelDuringDispatch(t *testing.T) {
	d, sp, _ := runEnv(t, spec("a"))
	sp.script = []fakeEntry{{hold: true}}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run must surface ctx cancellation, got %v", err)
	}
	if !sp.runs[0].terminated() {
		t.Fatal("the orphaned dispatch must be torn down")
	}
}

func TestRunStaleResultFenceRejected(t *testing.T) {
	d, sp, _ := runEnv(t, spec("a"))
	// a hand-off naming another dispatch than the one settled is fenced
	// out: Run stops on the stale fence and the record is untouched
	stale := fakeEntry{res: &StepResult{SpawnN: 99, Handoff: handoffOf(stepcall.StatusDone, "")}}
	sp.script = []fakeEntry{stale}
	if err := d.Run(context.Background()); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("a stale hand-off stops Run on the fence: %v", err)
	}
	// The start write landed; the result did not mutate anything.
	if st := d.Snapshot().Steps["a"]; st.Status != capsule.StepInProgress {
		t.Fatalf("the step stays in_progress after the rejection: %+v", st)
	}
}

// ---------------------------------------------------------------------------
// workflow loading from a real bundle directory

func TestLoadWorkflowFromBundle(t *testing.T) {
	const front = `max_rewinds: 3
defaults:
  model_candidates: [pi:prov/a, pi:prov/b]
steps:
  - id: spec
  - id: code
    model_candidates: [pi:prov/c]
  - id: check
    gate: true
    on_fail:
      target: code
`
	dir := workflowtest.WriteLane(t, t.TempDir(), "mybundle", front)
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("loadWorkflow: %v", err)
	}
	if wf.MaxRewinds != 3 {
		t.Fatalf("budget (workflow contract 3): %+v", wf)
	}
	if len(wf.Steps) != 3 || wf.Steps[0].ID != "spec" || wf.Steps[2].ID != "check" {
		t.Fatalf("order is the declaration order: %+v", wf.Steps)
	}
	of := wf.Steps[2].OnFail
	if of == nil || of.Target != "code" {
		t.Fatalf("on_fail target: %+v", of)
	}

	// The driver built over the bundle directory resolves paths by
	// default and picks candidates on dispatch.
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`), done(`{"verdict":"pass"}`)}}
	caps := t.TempDir()
	workflowtest.WriteLane(t, filepath.Join(caps, capsule.DirWorkflows), "mybundle", front)
	// the bundle's lists reach the dispatch through the claim-time
	// composition, exactly as the runtime builds StepCandidates
	lane, err := workflow.LoadStrict(filepath.Join(caps, capsule.DirWorkflows, "mybundle"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{
		WI:      WIInfo{ID: "wi_l", CapsuleDir: caps, BundleName: "mybundle"},
		Spawner: sp,
		Compose: composeOf(workspace.ComposeCandidates(lane, nil, "p", nil)),
	})
	if err != nil {
		t.Fatalf("New over the default bundle path: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := sp.spawnAt(0).Model; m != "prov/a" {
		t.Fatalf("first dispatch uses candidate 1 of the defaults chain, got %q", m)
	}
	if m := sp.spawnAt(1).Model; m != "prov/c" {
		t.Fatalf("step-level chain wins, got %q", m)
	}
	if m := sp.spawnAt(2).Model; m != "prov/a" {
		t.Fatalf("defaults chain for the gate, got %q", m)
	}
}

// ---------------------------------------------------------------------------
// In-process envelope-rejection retry lane (the #97/#107 lessons).

func loadExecFile(t *testing.T, path string) *capsule.Execution {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var exec capsule.Execution
	if err := json.Unmarshal(raw, &exec); err != nil {
		t.Fatal(err)
	}
	return &exec
}

func (l *eventLog) count(kind string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.entries {
		if e.kind == kind {
			n++
		}
	}
	return n
}

// The recovery lane's interrupted archive (Run's daemon-restart path)
// computes the tree hash before the transition takes the driver's lock
// and records it on the daemon_restart attempt.
func TestRunInterruptedArchiveComputesTreeHash(t *testing.T) {
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "run-interrupted-hash", nil }

	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "s", capsule.StepInProgress, nil)
	seedDispatch(t, execPath, "s", 1)
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d, err := New(Config{
		WI:          WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath:    execPath,
		Workflow:    testLane(4, []StepSpec{spec("s")}...),
		Spawner:     sp,
		Log:         &eventLog{},
		Interrupted: &InterruptedDispatch{Detail: FailDaemonRestart},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	at := readExec(t, execPath).Steps["s"].Attempts
	if len(at) == 0 || at[0].TreeHash != "run-interrupted-hash" || at[0].FailureCause != FailDaemonRestart {
		t.Fatalf("the daemon_restart archive must carry the computed tree hash: %+v", at)
	}
}

// ---------------------------------------------------------------------------
// The re-dispatch fast-path decision inputs.

// prepareDispatch carries the latest attempt's tree hash and the
// death-trigger flag. (A newer archived answer that flipped the flag off
// left with the run request's answers, workflow contract v2 change 18.)
func TestPrepareDispatchCarriesTreeHashAndTrigger(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	exec := capsule.Execution{
		CapsuleID: "cap_wi_run", WorkItemID: "wi_run",
		WorkflowStatus: capsule.WorkflowStatusRunning, CurrentStep: "s",
		Steps: map[string]*capsule.Step{
			"s": {
				Status:    capsule.StepInProgress,
				Candidate: "pi:a",
				Attempts: []capsule.Attempt{{
					Cause: CauseInfraFailure, FailureCause: FailExitModel,
					InterruptedAt: "2026-09-20T08:00:02Z", TreeHash: "abc",
				}},
				Supervision: &capsule.StepSupervision{NextDispatch: capsule.NextDispatchFresh},
			},
		},
	}
	writeExec := func() {
		blob, err := json.MarshalIndent(exec, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(execPath, blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	newDriver := func() *Driver {
		d, err := New(Config{
			WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
			ExecPath: execPath,
			Workflow: testLane(4, []StepSpec{spec("s")}...),
			Spawner:  &fakeSpawner{},
			Log:      &eventLog{},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	}
	writeExec()
	sp, _, err := newDriver().prepareDispatch("s")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if !sp.Fresh || !sp.RetryTriggered || sp.RecordedTreeHash != "abc" || sp.RecordedAttemptAt != "2026-09-20T08:00:02Z" {
		t.Fatalf("death-triggered fresh dispatch: fresh=%v trigger=%v hash=%q attemptAt=%q", sp.Fresh, sp.RetryTriggered, sp.RecordedTreeHash, sp.RecordedAttemptAt)
	}
}
