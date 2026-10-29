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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
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
}

type fakeRun struct {
	sp   StepSpawn
	e    fakeEntry
	mu   sync.Mutex
	term bool
	rel  chan struct{}
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
	run := &fakeRun{sp: sp, e: e}
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

// entry builders.
func done(out string) fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeCompleted, Output: json.RawMessage(out)}}
}
func asks(qid, q string) fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeNeedsInput, QuestionID: qid, Question: q}}
}
func gateFAIL() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"FAIL: tests red"}`)}}
}

// lightReviewFAIL is a review-gate FAIL verdict whose every blocking
// finding is marked text_only — polyforge#222's light-path trigger (the
// unmarked twin is gateFAIL's shape plus an unmarked blocking finding).
func lightReviewFAIL() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(
		`{"verdict":"fail","fail_reason":"comment wording only","findings":[{"severity":"blocking","detail":"the doc comment names the old flag","text_only":true}]}`)}}
}
func incomplete() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeIncomplete, Output: json.RawMessage(`{"partial":true}`)}}
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
		Workflow: &Workflow{Steps: steps, MaxTotal: 4, MaxPerStep: 2},
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
	// the spawn carries the done set the handoff producer check reads
	if d0, d1 := sp.spawnAt(0).Done, sp.spawnAt(1).Done; len(d0) != 0 || len(d1) != 1 || !d1["a"] {
		t.Fatalf("done sets: first %v, second %v", d0, d1)
	}
}

func TestRunTerminalFileStopsImmediately(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{}
	// Pre-write a completed execution.json; Run must not dispatch.
	// (The driver is constructed AFTER the file exists — its in-memory
	// state is the load-time snapshot.)
	exec := skeletonExecution(WIInfo{ID: "wi_run"}, &Workflow{Steps: []StepSpec{spec("a")}})
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
		Workflow: &Workflow{Steps: []StepSpec{spec("a")}, MaxTotal: 4, MaxPerStep: 2},
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
	exec := skeletonExecution(WIInfo{ID: "wi_run"}, &Workflow{Steps: []StepSpec{spec("a")}})
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
		Workflow: &Workflow{Steps: []StepSpec{spec("a")}, MaxTotal: 4, MaxPerStep: 2},
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
// needs_input

func TestRunNeedsInputAnswerResumesInPlace(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("grill"))
	sp.script = []fakeEntry{asks("q_1", "lean or compatible?"), done(`{"verdict":"lean"}`)}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()

	waitUntil(t, "the open question on disk", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["grill"].PendingQuestion != nil
	})
	// While waiting, the workflow is RUNNING (a driven ask is not
	// blocked) and no second dispatch happened.
	if s := readExec(t, execPath).WorkflowStatus; s != StatusRunning {
		t.Fatalf("a driven ask keeps the workflow running: %q", s)
	}
	if n := len(sp.dispatched()); n != 1 {
		t.Fatalf("the loop must park on the question, dispatches: %v", sp.dispatched())
	}
	if err := d.Answer("q_1", "the lean one", "alice"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The resume dispatch carried the archived answer and Resume=true.
	if got := sp.dispatched(); len(got) != 2 || !sp.spawnAt(1).Resume {
		t.Fatalf("the answered step continues in place: %v", got)
	}
	if a := sp.spawnAt(1).Answers; len(a) != 1 || a[0].Answer != "the lean one" {
		t.Fatalf("the resume spawn carries the answer: %+v", a)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || exec.Steps["grill"].Status != capsule.StepDone {
		t.Fatalf("final: %+v", exec)
	}
	if ans := exec.Steps["grill"].Answers; len(ans) != 1 || ans[0].QuestionID != "q_1" {
		t.Fatalf("the pair must stay archived: %+v", ans)
	}
}

func TestRunNeedsInputSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{script: []fakeEntry{asks("q_9", "shape?")}}
	mk := func(sp *fakeSpawner) *Driver {
		d, err := New(Config{
			WI:       WIInfo{ID: "wi_r2", CapsuleDir: dir, BundleName: "b"},
			ExecPath: execPath,
			Workflow: &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
			Spawner:  sp,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	}
	d1 := mk(sp)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = d1.Run(ctx) }()
	waitUntil(t, "the question", func() bool {
		exec := tryReadExec(execPath)
		return exec != nil && exec.Steps["s"].PendingQuestion != nil
	})
	cancel() // daemon dies while the question is open
	time.Sleep(10 * time.Millisecond)

	// The restarted driver re-parks on the SAME durable question.
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d2 := mk(sp2)
	doneCh := make(chan error, 1)
	go func() { doneCh <- d2.Run(context.Background()) }()
	q := readExec(t, execPath).Steps["s"].PendingQuestion
	if q == nil || q.ID != "q_9" {
		t.Fatalf("the question must survive the restart: %+v", q)
	}
	if len(sp2.dispatched()) != 0 {
		t.Fatal("the restarted loop must park, not dispatch")
	}
	if err := d2.Answer("q_9", "compatible", "bob"); err != nil {
		t.Fatalf("answer across restart: %v", err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s := readExec(t, execPath).WorkflowStatus; s != StatusCompleted {
		t.Fatalf("completed after restart, got %q", s)
	}
}

// ---------------------------------------------------------------------------
// gate fail / rewind

func TestRunGateFailRewindRecovers(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("build"), spec("polish"), gateSpec("check", "build"))
	sp.script = []fakeEntry{
		done(`{"b":1}`), done(`{"p":1}`), gateFAIL(),
		done(`{"b":2}`), done(`{"p":2}`), done(`{"verdict":"PASS"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The dispatch ledger: the full recovery, exactly the s12 shape.
	// The gate's re-dispatch is FRESH (resume=false): its prior
	// conversation judged the version the rewind just discarded. The
	// rewind still never resets the gate itself (INV-1g — its candidate
	// pointer is preserved).
	got := sp.dispatched()
	want := []string{"build@1(resume=false)", "polish@2(resume=false)", "check@3(resume=false)",
		"build@4(resume=false)", "polish@5(resume=false)", "check@6(resume=false)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dispatch ledger:\n got  %v\n want %v", got, want)
	}
	// The re-dispatched target consumed the routed verdict — the
	// result's full output payload, verbatim (the judgment in full).
	if ri := sp.spawnAt(3).RewindInput; ri == nil || string(ri.Reason) != `{"verdict":"FAIL: tests red"}` || ri.ToStep != "build" {
		t.Fatalf("the rewind verdict must ride the target's dispatch: %+v", ri)
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
	if exec.RewindStats.Count != 1 || exec.ReviewRounds["check"] != 1 {
		t.Fatalf("stats: rewind=%+v review=%+v", exec.RewindStats, exec.ReviewRounds)
	}
	// The round ended at acceptance: no pending_rewind_input remains
	// (the verdict persists across every dispatch of the
	// round and only the acceptance write clears it).
	if pri := exec.Steps["build"].PendingRewindInput; pri != nil {
		t.Fatalf("the verdict is cleared at acceptance: %+v", pri)
	}
}

// TestRunLightRewindResumesBuildSession (polyforge#222): a review FAIL
// whose blocking findings are all text_only rewinds the build step LIGHT —
// the re-dispatch is a RESUME of its previous conversation (the resume
// order is the ledger cause the same write archived, and the started
// event records the shape), carrying the verdict and the light
// re-entry instruction as its rewind input; the review gate's own
// re-entry stays fresh; one slot.
func TestRunLightRewindResumesBuildSession(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("build"), gateSpec("review", "build"))
	sp.script = []fakeEntry{
		done(`{"b":1}`),
		lightReviewFAIL(),
		done(`{"b":2}`), // the light re-entry (a resume)
		done(`{"verdict":"PASS"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := sp.dispatched()
	want := []string{"build@1(resume=false)", "review@2(resume=false)", "build@3(resume=true)", "review@4(resume=false)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dispatch ledger:\n got  %v\n want %v", got, want)
	}
	re := sp.spawnAt(2)
	if !re.Resume || re.Fresh {
		t.Fatalf("the light re-entry must resume the previous conversation: %+v", re)
	}
	if ri := re.RewindInput; ri == nil || ri.ToStep != "build" || ri.Note != LightRewindNote ||
		!strings.Contains(ri.Reason, `"verdict":"fail"`) {
		t.Fatalf("the light re-entry carries the verdict + the light instruction: %+v", ri)
	}
	if g := sp.spawnAt(3); g.Resume || !g.Fresh {
		t.Fatalf("the review gate's own re-entry must stay fresh: %+v", g)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || exec.RewindStats.Count != 1 {
		t.Fatalf("one light slot, completed: %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	var light bool
	for _, a := range exec.Steps["build"].Attempts {
		if a.Cause == CauseGateFailRewindTextOnly && a.RewindGate == "review" {
			light = true
		}
	}
	if !light {
		t.Fatalf("build must carry the light archive: %+v", exec.Steps["build"].Attempts)
	}
	// the started event does not lie about the light re-entry's shape
	var resumed bool
	for _, e := range d.log.(*eventLog).entries {
		if e.kind == "step_started" && e.body["step"] == "build" && e.body["spawn_n"] == int64(3) {
			resumed = e.body["resume"] == true
		}
	}
	if !resumed {
		t.Fatal("the light re-entry's step_started event must record resume: true")
	}
}

// TestRunSelfReportedStructuralFixRewindsFresh (polyforge#222, the
// scenario-2 shape): a light re-entry whose completed result carries
// needs_code_change: true is NOT archived done — the very next dispatch
// is a FRESH re-entry of the same step (not the gate tail), no new rewind
// slot is charged, and the lane then proceeds normally.
func TestRunSelfReportedStructuralFixRewindsFresh(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("build"), gateSpec("review", "build"))
	sp.script = []fakeEntry{
		done(`{"b":1}`),
		lightReviewFAIL(),
		{res: &StepResult{Outcome: OutcomeCompleted, Output: json.RawMessage(`{"b":2,"needs_code_change":true}`)}}, // the resumed session self-reports
		done(`{"b":3}`), // the FRESH re-entry
		done(`{"verdict":"PASS"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := sp.dispatched()
	want := []string{"build@1(resume=false)", "review@2(resume=false)", "build@3(resume=true)", "build@4(resume=false)", "review@5(resume=false)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dispatch ledger:\n got  %v\n want %v", got, want)
	}
	if re := sp.spawnAt(3); re.Resume || re.Fresh {
		t.Fatalf("the self-report fallback re-entry must be fresh (no resume order): %+v", re)
	}
	if ri := sp.spawnAt(3).RewindInput; ri == nil || ri.Note != "" || !strings.Contains(ri.Reason, `"verdict":"fail"`) {
		t.Fatalf("the fresh re-entry carries the same verdict, no light Note: %+v", ri)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted || exec.RewindStats.Count != 1 {
		t.Fatalf("the self-report reuses the light rewind's one slot: %q %+v", exec.WorkflowStatus, exec.RewindStats)
	}
	var causes []string
	for _, a := range exec.Steps["build"].Attempts {
		if a.Cause != "accepted" {
			causes = append(causes, a.Cause)
		}
	}
	if fmt.Sprint(causes) != fmt.Sprint([]string{CauseGateFailRewindTextOnly, CauseGateFailRewind}) {
		t.Fatalf("build rewind archives: %v", causes)
	}
}

// TestBuildResultSpecAssemblesLightRewindFact (polyforge#222): the
// validation snapshot's light-rewind context is the same ledger-cause
// fact the transition's self-report rewind reads — the latest
// non-infra_failure archived attempt being the light rewind — and a
// crash attempt interleaved after it changes nothing (audit-only), while
// a fresh rewind archive or a plain accepted round answer false.
func TestBuildResultSpecAssemblesLightRewindFact(t *testing.T) {
	d, _, _ := runEnv(t, spec("build"), spec("polish"), gateSpec("review", "build"))
	mustInit(t, d)
	seed := func(attempts ...capsule.Attempt) *Driver {
		d.mu.Lock()
		st := d.exec.Steps["build"]
		if st == nil {
			st = &capsule.Step{Status: capsule.StepInProgress}
			d.exec.Steps["build"] = st
		}
		st.Attempts = attempts
		d.mu.Unlock()
		return d
	}
	light := capsule.Attempt{Cause: CauseGateFailRewindTextOnly, RewindGate: "review"}
	seed(light)
	if !d.buildResultSpec("build", 3).LightRewind {
		t.Fatal("the light rewind archive is the light context")
	}
	// a crash attempt interleaved after the light rewind: still light
	seed(append([]capsule.Attempt{light}, capsule.Attempt{Cause: CauseInfraFailure, FailureCause: FailDaemonRestart})...)
	if !d.buildResultSpec("build", 3).LightRewind {
		t.Fatal("an audit-only crash attempt must not change the re-entry's nature")
	}
	// the fresh rewind archive (a self-report already happened): not light
	seed(light, capsule.Attempt{Cause: CauseGateFailRewind, RewindGate: "review"})
	if d.buildResultSpec("build", 3).LightRewind {
		t.Fatal("a fresh rewind archive ends the light context")
	}
	// a plain accepted round: not light
	seed(capsule.Attempt{Cause: "accepted"})
	if d.buildResultSpec("build", 3).LightRewind {
		t.Fatal("an accepted round is no light context")
	}
}

func TestRunGateFailTerminate(t *testing.T) {
	// A gate with no on_fail rewind target has no rewind edge: its FAIL
	// verdict ends the workflow failed_gate (WF-102).
	d, sp, execPath := runEnv(t, spec("a"), StepSpec{ID: "check", Gate: true, ContextPolicy: "fresh"})
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

func TestRunGateFailRewindExhausted(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("a"), gateSpec("check", "a"))
	d.workflow.MaxTotal = 1
	sp.script = []fakeEntry{
		done(`{}`), gateFAIL(), // rewind #1 (the only one allowed)
		done(`{}`), gateFAIL(), // bound hits → failed_rewind_exhausted
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("bound must terminate: %q", exec.WorkflowStatus)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("exactly one rewind may land: %+v / attempts=%d (accepted extra)", exec.RewindStats, len(exec.Steps["a"].Attempts))
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
	// the in-process retry lane re-dispatches on rejection; a stale fence
	// stays stale, so the bound blocks the step with the result_invalid
	// cause code (the fence error rides the blocked reason detail).
	stale := fakeEntry{res: &StepResult{Outcome: OutcomeCompleted, SpawnN: 99}}
	script := make([]fakeEntry, 0, maxEnvelopeRejects+1)
	for i := 0; i <= maxEnvelopeRejects; i++ {
		script = append(script, stale)
	}
	sp.script = script
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("a spent rejection bound must BLOCK, not error: %v", err)
	}
	x := d.Snapshot()
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "result_invalid") {
		t.Fatalf("status %q reason %q, want blocked with result_invalid", x.WorkflowStatus, x.BlockedReason)
	}
	// The start write landed; the result did not mutate anything.
	if st := d.Snapshot().Steps["a"]; st.Status != capsule.StepInProgress {
		t.Fatalf("the step stays in_progress after the rejection: %+v", st)
	}
}

func TestRunMinimalContractAccepted(t *testing.T) {
	// The MINIMAL CONTRACT (polyforge#147): a completed envelope with ONLY
	// the outcome + output pair and the identity/fence fields is accepted;
	// there is no artifact manifest, no protocol stamp and no handoff
	// digest reference — a handoff-declaring step's completed result owes
	// none of them (the handoff is a path-convention read).
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)

	mkDriver := func(sp *fakeSpawner) *Driver {
		d, err := New(Config{
			WI:       WIInfo{ID: "wi_v", CapsuleDir: dir, BundleName: "b"},
			ExecPath: execPath,
			Workflow: &Workflow{Steps: []StepSpec{{ID: "a", ContextPolicy: "fresh", Handoff: true}}, MaxTotal: 4, MaxPerStep: 2},
			Spawner:  sp,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return d
	}

	// A handoff-declaring step's minimal completed result is accepted.
	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	if err := mkDriver(sp).Run(context.Background()); err != nil {
		t.Fatalf("a handoff-declaring step's minimal result must be accepted: %v", err)
	}
	if s := readExec(t, execPath).WorkflowStatus; s != StatusCompleted {
		t.Fatalf("completed: %q", s)
	}

	// A structurally invalid envelope (a needs_input without its question
	// pair — invalid under the old AND the new contract) is rejected —
	// the retry lane re-dispatches, and the spent bound blocks the step
	// with the result_invalid cause code (no relaunch, no error return).
	os.Remove(execPath)
	badScript := []fakeEntry{}
	for i := 0; i <= maxEnvelopeRejects; i++ {
		badScript = append(badScript, badQuestion())
	}
	sp = &fakeSpawner{script: badScript}
	if err := mkDriver(sp).Run(context.Background()); err != nil {
		t.Fatalf("the spent bound must block cleanly, not error: %v", err)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "result_invalid") {
		t.Fatalf("status %q reason %q, want blocked with result_invalid", x.WorkflowStatus, x.BlockedReason)
	}
}

// ---------------------------------------------------------------------------
// workflow loading from a real bundle directory

func TestLoadWorkflowFromBundle(t *testing.T) {
	dir := t.TempDir()
	fm := `---
defaults:
  model_candidates: [pi:prov/a, pi:prov/b]
rewind:
  max_total: 3
steps:
  - id: spec
  - id: code
    context_policy: inherit
    model_candidates: [pi:prov/c]
  - id: check
    gate: true
    context_policy: fresh
    on_fail:
      target: code
---
# body is never parsed
`
	os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "workflow.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("loadWorkflow: %v", err)
	}
	if wf.MaxTotal != 3 || wf.MaxPerStep != defaultMaxRewindPerStep {
		t.Fatalf("bounds: %+v", wf)
	}
	if len(wf.Steps) != 3 || wf.Steps[0].ID != "spec" || wf.Steps[2].ID != "check" {
		t.Fatalf("order is the declaration order: %+v", wf.Steps)
	}
	if wf.Steps[0].ContextPolicy != ContextInherit {
		t.Fatalf("spec step: %+v", wf.Steps[0])
	}
	if wf.Steps[1].ContextPolicy != "inherit" {
		t.Fatalf("code step: %+v", wf.Steps[1])
	}
	of := wf.Steps[2].OnFail
	if of == nil || of.Target != "code" {
		t.Fatalf("on_fail target: %+v", of)
	}

	// The driver built over the bundle directory resolves paths by
	// default and picks candidates on dispatch.
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`), done(`{"verdict":"PASS"}`)}}
	caps := t.TempDir()
	if err := os.MkdirAll(filepath.Join(caps, capsule.DirWorkflows, "mybundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundleMD := filepath.Join(caps, capsule.DirWorkflows, "mybundle", "workflow.md")
	os.WriteFile(bundleMD, []byte(fm), 0o644)
	// the bundle's lists reach the dispatch through the claim-time
	// composition, exactly as the runtime builds StepCandidates
	front, err := workflow.ReadFrontmatter(bundleMD)
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{
		WI:      WIInfo{ID: "wi_l", CapsuleDir: caps, BundleName: "mybundle"},
		Spawner: sp,
		Compose: composeOf(workspace.ComposeCandidates(front, nil, "p", nil)),
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

// badQuestion builds a needs_input result without its question pair —
// deterministic substance-gate rejection (invalid under the old AND the
// new contract).
func badQuestion() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeNeedsInput}}
}

func TestRunRejectedEnvelopeRetriesInProcess(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "fix"})
	sp.script = []fakeEntry{badQuestion(), done(`{}`)}

	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("a recoverable envelope rejection must NOT fail the run: %v", err)
	}
	if n := len(sp.spawns); n != 2 {
		t.Fatalf("the rejected envelope must trigger an in-process re-dispatch, got %d spawns", n)
	}
	// the re-dispatch carries the rejection as visible prompt feedback
	second := sp.spawnAt(1)
	if second.PriorRejection == "" || !strings.Contains(second.PriorRejection, "question") {
		t.Fatalf("the re-dispatch must carry the validation error verbatim, got %q", second.PriorRejection)
	}
	// and the step is done with the second envelope
	exec := loadExecFile(t, execPath)
	if exec.Steps["fix"].Status != capsule.StepDone {
		t.Fatalf("the step must be done after the successful retry, got %v", exec.Steps["fix"].Status)
	}
	// the rejection ledger cleared on success
	if _, ok := d.priorRejection["fix"]; ok {
		t.Fatal("the rejection ledger must clear when the step completes")
	}
}

func TestRunRejectedEnvelopeBlocksResultInvalidAfterBound(t *testing.T) {
	d, sp, execPath := runEnv(t, StepSpec{ID: "fix"})
	// maxEnvelopeRejects rejections then the bound blocks the step
	sp.script = []fakeEntry{badQuestion(), badQuestion(), badQuestion(), badQuestion()}

	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("a spent in-process retry bound must BLOCK cleanly, not error (no relaunch): %v", err)
	}
	if n := len(sp.spawns); n != maxEnvelopeRejects {
		t.Fatalf("exactly the bound (%d) dispatches before the block, got %d", maxEnvelopeRejects, n)
	}
	// every re-dispatch carried the feedback
	for i := 1; i < maxEnvelopeRejects; i++ {
		if sp.spawnAt(i).PriorRejection == "" {
			t.Fatalf("re-dispatch %d must carry the rejection feedback", i)
		}
	}
	exec := loadExecFile(t, execPath)
	if exec.WorkflowStatus != StatusBlocked || !strings.Contains(exec.BlockedReason, "result_invalid") {
		t.Fatalf("status %q reason %q, want blocked with the result_invalid cause code", exec.WorkflowStatus, exec.BlockedReason)
	}
	if exec.RewindStats.Count != 0 {
		t.Fatalf("a result-invalid block must not route on_fail (fire a rewind): rewind_stats=%+v", exec.RewindStats)
	}
}

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

func TestRejectionBoundBlocksOnceWithoutRelaunch(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	log := &eventLog{}
	sp := &fakeSpawner{script: []fakeEntry{badQuestion(), badQuestion(), badQuestion(), badQuestion()}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_rej", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{{ID: "fix"}}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  sp,
		Log:      log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// ONE driver run carries the whole retry lane; a clean nil return is
	// the contract (an error return is what the recovery/relaunch lane
	// feeds on -- it must not fire for a result-invalid block).
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("the bound must block cleanly, not error: %v", err)
	}
	if n := len(sp.spawns); n != maxEnvelopeRejects {
		t.Fatalf("exactly the bound (%d) dispatches before the block, got %d", maxEnvelopeRejects, n)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "result_invalid") {
		t.Fatalf("status %q reason %q, want blocked with result_invalid", x.WorkflowStatus, x.BlockedReason)
	}
	if n := log.count("blocked"); n != 1 {
		t.Fatalf("one blocked event expected, got %d", n)
	}
	if n := log.count("step_failed"); n != 1 {
		t.Fatalf("one step_failed (result_invalid) event expected, got %d", n)
	}
	if x.RewindStats.Count != 0 {
		t.Fatalf("a result-invalid block must not fire a rewind: rewind_stats=%+v", x.RewindStats)
	}
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
		Workflow:    &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
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

func TestAttemptAfterAnswer(t *testing.T) {
	at := "2026-09-20T08:00:02Z"
	attempt := capsule.Attempt{Cause: CauseInfraFailure, InterruptedAt: at}
	cases := []struct {
		name    string
		last    capsule.Attempt
		answers []capsule.ArchivedAnswer
		want    bool
	}{
		{"no attempt", capsule.Attempt{}, nil, false},
		{"no answer", attempt, nil, true},
		{"older answer", attempt, []capsule.ArchivedAnswer{{AnsweredAt: "2026-09-20T08:00:01Z"}}, true},
		{"same stamp", attempt, []capsule.ArchivedAnswer{{AnsweredAt: at}}, true},
		{"newer answer", attempt, []capsule.ArchivedAnswer{{AnsweredAt: "2026-09-20T08:00:03Z"}}, false},
		{"archived fallback", capsule.Attempt{Cause: CauseInfraFailure, ArchivedAt: at},
			[]capsule.ArchivedAnswer{{AnsweredAt: "2026-09-20T08:00:01Z"}}, true},
		{"unparsable answer falls back to lexicographic", attempt,
			[]capsule.ArchivedAnswer{{AnsweredAt: "zzz"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := attemptAfterAnswer(c.last, c.answers); got != c.want {
				t.Fatalf("attemptAfterAnswer(%+v, %+v) = %v, want %v", c.last, c.answers, got, c.want)
			}
		})
	}
}

// prepareDispatch carries the latest attempt's tree hash and the
// death-trigger flag; a newer archived answer flips the flag off.
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
			Workflow: &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
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

	exec.Steps["s"].Answers = []capsule.ArchivedAnswer{{QuestionID: "q", Answer: "go", AnsweredAt: "2026-09-20T08:00:03Z"}}
	writeExec()
	sp2, _, err := newDriver().prepareDispatch("s")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if !sp2.Fresh || sp2.RetryTriggered || sp2.RecordedTreeHash != "abc" {
		t.Fatalf("answer continuation: fresh=%v trigger=%v hash=%q", sp2.Fresh, sp2.RetryTriggered, sp2.RecordedTreeHash)
	}
}
