package driver

// transition_test.go: the state-machine and atomic-write tests for the
// single write entry point — fencing, per-kind validation before any
// mutation, the failed driver after a persist failure, and the durable
// execution.json shape the observe lane keeps consuming.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// eventLog is the counting EventLogger fake. Its methods take the
// mutex, so a test may poll them while the Run goroutine is still
// appending (the direct .entries reads stay for after Run returns).
type eventLog struct {
	mu      sync.Mutex
	entries []loggedEvent
}

type loggedEvent struct {
	wi   string
	kind string
	body map[string]any
}

func (l *eventLog) Append(wi, kind string, body any) error {
	b, _ := body.(map[string]any)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, loggedEvent{wi: wi, kind: kind, body: b})
	return nil
}

// has reports whether an event of the kind carrying the key/value pair
// was appended. Safe to poll from the test goroutine while Run runs.
func (l *eventLog) has(kind, key string, val any) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.entries {
		if e.kind == kind && e.body[key] == val {
			return true
		}
	}
	return false
}

func (l *eventLog) kinds() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, e.kind)
	}
	return out
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { c.t = c.t.Add(time.Second); return c.t }

// testEnv builds a Driver over a temp capsule with a programmatic
// workflow and no spawner calls expected.
func testEnv(t *testing.T, steps ...StepSpec) (*Driver, *eventLog, *clock, string) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	log := &eventLog{}
	clk := &clock{t: time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_t1", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, steps...),
		Spawner:  &fakeSpawner{},
		Log:      log,
		Now:      clk.now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, log, clk, execPath
}

func spec(id string) StepSpec { return StepSpec{ID: id} }

func gateSpec(id, target string) StepSpec {
	return StepSpec{
		ID:     id,
		Gate:   true,
		OnFail: &OnFail{Target: target},
	}
}

// mustInit persists the skeleton and returns the post-init version.
func mustInit(t *testing.T, d *Driver) {
	t.Helper()
	if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: 0}); err != nil {
		t.Fatalf("init: %v", err)
	}
}

// mustStart starts a step as derivation n.
func mustStart(t *testing.T, d *Driver, stepID string, n int64) {
	t.Helper()
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: stepID, Model: "prov/m1", SpawnN: n}); err != nil {
		t.Fatalf("start %s: %v", stepID, err)
	}
}

func completedResult(stepID string, n int64, output string) *StepResult {
	return &StepResult{
		WIID:    "wi_t1",
		StepID:  stepID,
		SpawnN:  n,
		Outcome: OutcomeCompleted,
		Output:  json.RawMessage(output),
	}
}

// readExec parses the durable file (never the driver's memory — the
// tests assert what actually landed on disk).
func readExec(t *testing.T, path string) *capsule.Execution {
	t.Helper()
	exec := tryReadExec(path)
	if exec == nil {
		t.Fatalf("read execution.json: %s", path)
	}
	return exec
}

// tryReadExec is the nil-tolerant variant for polling conditions (the
// file legitimately does not exist before the init write).
func tryReadExec(path string) *capsule.Execution {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var exec capsule.Execution
	if err := json.Unmarshal(raw, &exec); err != nil {
		return nil
	}
	return &exec
}

// tmpLeftovers lists the atomic-write temp files next to path.
func tmpLeftovers(path string) []string {
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp"))
	return m
}

// ---------------------------------------------------------------------------
// init + atomic persistence

func TestTransitionInitWritesSkeleton(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusRunning || exec.CurrentStep != "" {
		t.Fatalf("bad skeleton: status=%q current=%q", exec.WorkflowStatus, exec.CurrentStep)
	}
	if exec.Version != 1 {
		t.Fatalf("init must write version 1, got %d", exec.Version)
	}
	if exec.CapsuleID != "cap_wi_t1" || exec.WorkItemID != "wi_t1" {
		t.Fatalf("bad identity: %+v", exec)
	}
	for _, id := range []string{"a", "b"} {
		if s := exec.Steps[id]; s == nil || s.Status != capsule.StepPending {
			t.Fatalf("step %s must be pending in the skeleton, got %+v", id, s)
		}
	}
	if m := tmpLeftovers(execPath); len(m) > 0 {
		t.Fatalf("tmp file must not survive the rename: %v", m)
	}
	// Double init is refused — there is exactly one first write.
	if err := d.transition(TransitionRequest{Kind: TransInit, SpawnN: 0}); err == nil {
		t.Fatal("double init must be refused")
	}
	if got := log.kinds(); len(got) != 1 || got[0] != "exec_init" {
		t.Fatalf("init must emit exactly one exec_init event, got %v", got)
	}
}

func TestTransitionVersionBumpsPerWrite(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: completedResult("a", 1, `{"summary":"A"}`),
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d, "b", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "b", SpawnN: 2,
		Result: completedResult("b", 2, `{"summary":"B"}`),
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusCompleted, SpawnN: 2}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.Version != 6 { // init + start/result a + start/result b + terminal
		t.Fatalf("version must bump once per write: want 6, got %d", exec.Version)
	}
	if exec.WorkflowStatus != StatusCompleted || exec.Steps["a"].Status != capsule.StepDone || exec.Steps["b"].Status != capsule.StepDone {
		t.Fatalf("final state wrong: %+v", exec)
	}
	if exec.UpdatedAt == "" {
		t.Fatal("every write must stamp updated_at")
	}
}

// ---------------------------------------------------------------------------
// fencing

func TestTransitionFence(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	mustInit(t, d)

	// step_start must carry EXACTLY current+1 — neither the current
	// ordinal nor one further ahead.
	for _, bad := range []int64{0, 2, 3} {
		err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "a", SpawnN: bad})
		if !errors.Is(err, ErrStaleFence) {
			t.Fatalf("step_start with spawn %d must be fenced, got %v", bad, err)
		}
	}
	// Every other kind must carry the CURRENT ordinal.
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusFailedGate, SpawnN: 1}); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("non-start with the next ordinal must be fenced, got %v", err)
	}
	// A fenced request changed nothing on disk.
	if exec := readExec(t, execPath); exec.Version != 1 || exec.CurrentStep != "" {
		t.Fatalf("fenced request mutated the file: %+v", exec)
	}
	// The correct ordinal passes and advances the counter.
	mustStart(t, d, "a", 1)
	// ... and afterwards the OLD ordinal is stale for result delivery.
	err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 0,
		Result: completedResult("a", 0, `{}`),
	})
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("result with a stale spawn must be fenced, got %v", err)
	}
	// The result envelope's own spawn_n must agree with the request.
	err = d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: &StepResult{StepID: "a", SpawnN: 99, Outcome: OutcomeCompleted},
	})
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("envelope/request spawn disagreement must be fenced, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// step_start

func TestTransitionStepStartOpensAFreshWindow(t *testing.T) {
	d, _, clk, _ := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	// Simulate a rewind having reset step a after a measured window.
	d.mu.Lock()
	d.exec.Steps["a"].BudgetConsumed = capsule.Budget{WallMs: 42}
	d.mu.Unlock()

	before := clk.t
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "a", Model: "prov/m1", Candidate: "pi:prov/m1", SpawnN: 1}); err != nil {
		t.Fatalf("start a: %v", err)
	}
	snap := d.Snapshot()
	st := snap.Steps["a"]
	if st.Status != capsule.StepInProgress || st.Round != 1 {
		t.Fatalf("start must flip to in_progress and open the round: %+v", st)
	}
	if st.BudgetConsumed.WallMs != 0 {
		t.Fatalf("a fresh start reopens a clean window: budget=%+v", st.BudgetConsumed)
	}
	if c, err := harness.ParseCandidate(st.Candidate); err != nil || c.Model != "prov/m1" ||
		st.StepStartedAt == "" || st.StepStartedAt <= before.Format(time.RFC3339Nano) {
		t.Fatalf("start must record the candidate (the one model fact) + a fresh measurement window: %+v", st)
	}
	if snap.CurrentStep != "a" || snap.WorkflowStatus != StatusRunning {
		t.Fatalf("start must move the position: %+v", snap)
	}

	// The round ends at acceptance.
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: completedResult("a", 1, `{}`)}); err != nil {
		t.Fatalf("result a: %v", err)
	}
	if st := d.Snapshot().Steps["a"]; st.Status != capsule.StepDone {
		t.Fatalf("acceptance must end the round: %+v", st)
	}
}

func TestTransitionStepStartIllegal(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "nope", SpawnN: 1}); err == nil {
		t.Fatal("unknown step must be refused")
	}
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: completedResult("a", 1, `{}`),
	}); err != nil {
		t.Fatalf("result: %v", err)
	}
	// A done step never restarts.
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "a", SpawnN: 2}); err == nil {
		t.Fatal("a done step must not start")
	}
	// A blocked workflow only resumes its CURRENT step.
	mustStart(t, d, "b", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "b", SpawnN: 2,
		Result: &StepResult{StepID: "b", SpawnN: 2, Outcome: OutcomeIncomplete},
	}); err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "b", SpawnN: 3}); err != nil {
		t.Fatalf("resuming the blocked current step must be legal: %v", err)
	}
	if s := d.Snapshot().WorkflowStatus; s != StatusRunning {
		t.Fatalf("the blocked resume must rewrite running, got %q", s)
	}
}

// ---------------------------------------------------------------------------
// step_result

func TestTransitionStepResultCompleted(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	res := completedResult("a", 1, `{"summary":"A"}`)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: res}); err != nil {
		t.Fatalf("result: %v", err)
	}
	st := d.Snapshot().Steps["a"]
	if st.Status != capsule.StepDone || len(st.Attempts) != 1 || st.Attempts[0].Cause != "accepted" {
		t.Fatalf("completed must record done and archive the accepted attempt: %+v", st)
	}
}

func TestTransitionStepResultRefusals(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	// No envelope, unknown outcome, foreign work item — all refused
	// before any mutation.
	mustStart(t, d, "a", 1)
	cases := []TransitionRequest{
		{Kind: TransStepResult, StepID: "a", SpawnN: 1},
		{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: &StepResult{StepID: "a", SpawnN: 1, Outcome: OutcomeWeird}},
		{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: &StepResult{WIID: "wi_OTHER", StepID: "a", SpawnN: 1, Outcome: OutcomeCompleted}},
		// Result for a step that is not current.
		{Kind: TransStepResult, StepID: "b", SpawnN: 1, Result: completedResult("b", 1, `{}`)},
	}
	for i, req := range cases {
		if err := d.transition(req); err == nil {
			t.Fatalf("case %d must be refused", i)
		}
	}
	if st := d.Snapshot().Steps["a"]; st.Status != capsule.StepInProgress {
		t.Fatalf("refused results must not mutate: %+v", st)
	}
}

const OutcomeWeird Outcome = "weird"

func TestTransitionStepResultGateFailedAndIncomplete(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"), gateSpec("check", "a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`)}); err != nil {
		t.Fatalf("result a: %v", err)
	}
	mustStart(t, d, "check", 2)
	before := readExec(t, execPath).Version
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 2,
		Result: &StepResult{StepID: "check", SpawnN: 2,
			Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"fail","fail_reason":"r"}`)},
	}); err != nil {
		t.Fatalf("gate_failed: %v", err)
	}
	disk := readExec(t, execPath)
	st := disk.Steps["check"]
	if st.Status != capsule.StepInProgress {
		t.Fatalf("a FAILing gate is never done: %+v", st)
	}
	// the verdict and its rewind are ONE durable write: no crash can
	// leave a recorded FAIL that nothing consumes
	if disk.Version != before+1 || disk.CurrentStep != "a" || disk.RewindStats.Count != 1 ||
		disk.Steps["a"].Status != capsule.StepPending {
		t.Fatalf("the gate verdict must land with its rewind in one write: version %d->%d, %+v", before, disk.Version, disk)
	}
	// incomplete → blocked, position frozen, the blocker in the reason.
	mustStart(t, d, "a", 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 3,
		Result: &StepResult{StepID: "a", SpawnN: 3, Outcome: OutcomeIncomplete,
			Blocker: map[string]any{"reason": "the registry is down", "evidence": "artifacts/handoff.md"}},
	}); err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	snap := d.Snapshot()
	if snap.WorkflowStatus != StatusBlocked || snap.CurrentStep != "a" || snap.Steps["a"].Status != capsule.StepInProgress {
		t.Fatalf("incomplete must block with the position frozen: %+v", snap)
	}
	if why, ev := StepBlockedParts(snap.BlockedReason); !StepBlocked(snap.BlockedReason) || why != "the registry is down" || ev != "artifacts/handoff.md" {
		t.Fatalf("the blocker is the blocked reason: %q", snap.BlockedReason)
	}
}

// ---------------------------------------------------------------------------
// gate_fail / rewind

// lightReviewOutput builds one review-gate FAIL output over the given
// findings JSON (the light path's eligibility input; "" = no findings key).
func lightReviewOutput(findings string) json.RawMessage {
	out := `{"verdict":"fail","fail_reason":"comment wording only"`
	if findings != "" {
		out += `,"findings":` + findings
	}
	return json.RawMessage(out + `}`)
}

// lightGateFail drives build -> polish -> one gate FAILing over the given
// output and answers the persisted execution state (the restart's
// authority, not the driver's memory).
func lightGateFail(t *testing.T, gateID string, output json.RawMessage) *capsule.Execution {
	t.Helper()
	d, _, _, execPath := testEnv(t, spec("build"), spec("polish"), gateSpec(gateID, "build"))
	mustInit(t, d)
	for _, s := range []struct {
		id string
		n  int64
	}{
		{"build", 1}, {"polish", 2},
	} {
		mustStart(t, d, s.id, s.n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: s.id, SpawnN: s.n,
			Result: completedResult(s.id, s.n, `{"ok":true}`),
		}); err != nil {
			t.Fatalf("result %s: %v", s.id, err)
		}
	}
	mustStart(t, d, gateID, 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: gateID, SpawnN: 3,
		Result: &StepResult{StepID: gateID, SpawnN: 3, Outcome: OutcomeGateFailed, Output: output},
	}); err != nil {
		t.Fatalf("gate_fail: %v", err)
	}
	return readExec(t, execPath)
}

// TestTransitionLightCompletionWithoutSelfReportIsDone: a light re-entry
// that completes WITHOUT the field is handled exactly as today — done,
// the accepted attempt archived, no rewind.
func TestTransitionLightCompletionWithoutSelfReportIsDone(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("build"), gateSpec("review", "build"))
	mustInit(t, d)
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1,
		Result: completedResult("build", 1, `{"ok":1}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	mustStart(t, d, "review", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 2,
		Result: &StepResult{StepID: "review", SpawnN: 2, Outcome: OutcomeGateFailed,
			Output: lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)},
	}); err != nil {
		t.Fatalf("gate_fail: %v", err)
	}
	mustStart(t, d, "build", 3) // the light re-entry
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 3,
		Result: completedResult("build", 3, `{"ok":2}`)}); err != nil {
		t.Fatalf("light completion: %v", err)
	}
	snap := d.Snapshot()
	st := snap.Steps["build"]
	if st.Status != capsule.StepDone {
		t.Fatalf("a normal light completion is done exactly as today: %+v", st)
	}
	if att := st.Attempts[len(st.Attempts)-1]; att.Cause != "accepted" {
		t.Fatalf("the accepted attempt archives as today: %+v", att)
	}
	if snap.RewindStats.Count != 1 {
		t.Fatalf("no second rewind may land: %+v", snap.RewindStats)
	}
}

func TestTransitionGateFailRewind(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("build"), spec("polish"), gateSpec("check", "build"))
	mustInit(t, d)
	for _, s := range []struct {
		id string
		n  int64
	}{
		{"build", 1}, {"polish", 2},
	} {
		mustStart(t, d, s.id, s.n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: s.id, SpawnN: s.n,
			Result: completedResult(s.id, s.n, `{"ok":true}`),
		}); err != nil {
			t.Fatalf("result %s: %v", s.id, err)
		}
	}
	mustStart(t, d, "check", 3)
	// The gate FAILs.
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 3,
		Result: &StepResult{StepID: "check", SpawnN: 3,
			Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"fail","fail_reason":"tests red"}`)},
	}); err != nil {
		t.Fatalf("gate_fail: %v", err)
	}
	snap := d.Snapshot()
	// [build, polish] archived + reset to pending with output cleared.
	// The target is archived as gate_fail_rewind; polish, strictly
	// between the target and the gate, failed nothing and is archived as
	// superseded_by_rewind (owner ruling grill-201-r1, polyforge#201).
	for _, tc := range []struct{ id, cause string }{
		{"build", CauseGateFailRewind}, {"polish", CauseSupersededByRewind},
	} {
		st := snap.Steps[tc.id]
		if st.Status != capsule.StepPending {
			t.Fatalf("%s must be reset pending: %+v", tc.id, st)
		}
		// Attempts include the "accepted" record (R14) first; the rewind
		// archive is the newest entry.
		if n := len(st.Attempts); n != 2 || st.Attempts[0].Cause != "accepted" {
			t.Fatalf("%s attempts: want [accepted, rewind archive], got %+v", tc.id, st.Attempts)
		}
		att := st.Attempts[1]
		if att.Cause != tc.cause || att.RewindGate != "check" || att.ArchivedAt == "" ||
			att.AttemptSeq != 2 || att.ConsumedBudget == nil {
			t.Fatalf("bad rewind archive on %s (want cause %s): %+v", tc.id, tc.cause, att)
		}
	}
	// The gate itself: never archived nor reset, still in_progress; its
	// ledger carries only the audit-only record of the rewound dispatch
	// (verification_attempt_ledger_test.go).
	if st := snap.Steps["check"]; st.Status != capsule.StepInProgress ||
		len(st.Attempts) != 1 || st.Attempts[0].Cause != CauseGateFailVerdict {
		t.Fatalf("the gate is neither archived nor reset: %+v", st)
	}
	// Position, stats, routed verdict.
	if snap.CurrentStep != "build" {
		t.Fatalf("position must move back to the target: %q", snap.CurrentStep)
	}
	if snap.RewindStats.Count != 1 || len(snap.RewindStats.Rewinds) != 1 ||
		snap.RewindStats.Rewinds[0].Gate != "check" || snap.RewindStats.Rewinds[0].ToStep != "build" ||
		snap.RewindStats.Rewinds[0].Class != capsule.RewindCharged {
		t.Fatalf("rewind stats: %+v", snap.RewindStats)
	}
}

// The rewind budget is the daemon's to hold at the call (`pf step handoff`
// refuses a fail not citing the owner once RewindBudgetSpent answers): the
// driver itself rewinds every FAIL it settles. Every charged rewind counts
// toward max_rewinds, whichever edge made it; an owner's does not
// (the driver's own budget park left with contract 3).
func TestTransitionGateFailBounds(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), gateSpec("check", "a"))
	d.workflow.MaxRewinds = 1
	mustInit(t, d)
	fail := func(n int64, perOwner bool) {
		t.Helper()
		mustStart(t, d, "a", n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: n, Result: completedResult("a", n, `{}`)}); err != nil {
			t.Fatalf("result a: %v", err)
		}
		mustStart(t, d, "check", n+1)
		res := &StepResult{StepID: "check", SpawnN: n + 1, Outcome: OutcomeGateFailed}
		if perOwner {
			res.Handoff = &stepcall.Handoff{Status: stepcall.StatusFail, PerOwner: "2026-11-11T09:00:00Z"}
		}
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "check", SpawnN: n + 1, Result: res}); err != nil {
			t.Fatalf("gate_fail: %v", err)
		}
	}
	fail(1, true)
	if why := RewindBudgetSpent(d.workflow, d.Snapshot()); why != "" {
		t.Fatalf("an owner's rewind is charged to nothing: %s", why)
	}
	fail(3, false)
	snap := d.Snapshot()
	if snap.WorkflowStatus != StatusRunning || snap.RewindStats.Count != 2 || snap.CurrentStep != "a" {
		t.Fatalf("the driver rewinds every FAIL it settles: %q %+v", snap.WorkflowStatus, snap.RewindStats)
	}
	if why := RewindBudgetSpent(d.workflow, snap); !strings.Contains(why, "1 of max_rewinds 1") {
		t.Fatalf("one charged rewind spends max_rewinds 1: %q", why)
	}
}

// TestTransitionGateFailTerminate: a gate whose on_fail names no rewind
// target ends the workflow failed_gate on its FAIL verdict. (A target at
// or after the gate cannot reach the driver: LoadWorkflow refuses it.)
func TestTransitionGateFailTerminate(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), gateSpec("check", ""))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`)}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d, "check", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 2,
		Result: &StepResult{StepID: "check", SpawnN: 2, Outcome: OutcomeGateFailed},
	}); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if s := d.Snapshot().WorkflowStatus; s != StatusFailedGate {
		t.Fatalf("terminate must land failed_gate, got %q", s)
	}
	// Terminal freeze: nothing transitions anymore.
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "a", SpawnN: 3}); err == nil {
		t.Fatal("a terminal workflow must refuse further transitions")
	}
}

// ---------------------------------------------------------------------------
// terminal + budget

func TestTransitionTerminalValidation(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: "wrapped", SpawnN: 0}); err == nil {
		t.Fatal("a status outside the closed set must be refused")
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusCompleted, SpawnN: 0}); err == nil {
		t.Fatal("completed with steps pending must be refused")
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusFailedGate, Reason: "owner kill", SpawnN: 0}); err != nil {
		t.Fatalf("failure terminal from running must be legal: %v", err)
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusFailedGate, SpawnN: 0}); err == nil {
		t.Fatal("terminal-on-terminal must be refused")
	}
}

// ---------------------------------------------------------------------------
// persist failure → the driver fails, the restart recovers from disk

func TestTransitionPersistFailureFailsTheDriver(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	mustInit(t, d)
	onDisk := readExec(t, execPath).Version
	// A directory at the target path: the tmp write succeeds, the rename
	// cannot replace a directory — the persist fails after apply mutated
	// memory.
	occupied := filepath.Join(t.TempDir(), capsule.ExecutionJSON)
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	d.execPath = occupied
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "a", Model: "m", SpawnN: 1}); err == nil {
		t.Fatal("the persist must fail over an occupied path")
	}
	if m := tmpLeftovers(occupied); len(m) > 0 {
		t.Fatalf("the tmp file must be cleaned on failure: %v", m)
	}
	if d.SpawnN() != 0 {
		t.Fatalf("the spawn counter must not advance over a failed start: %d", d.SpawnN())
	}
	// The path is healthy again, but memory may disagree with the file:
	// every further transition is refused and nothing is written.
	d.execPath = execPath
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusFailedGate, SpawnN: 0}); err == nil {
		t.Fatal("a failed driver must refuse every further transition")
	}
	if got := readExec(t, execPath).Version; got != onDisk {
		t.Fatalf("a failed driver wrote execution.json: version %d -> %d", onDisk, got)
	}
	if err := d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("Run of a failed driver must return the persist failure, got %v", err)
	}
	// The restart reloads the file: the failed start never happened.
	d2, err := New(d.cfg)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	snap := d2.Snapshot()
	if snap.Version != onDisk || snap.CurrentStep != "" || snap.Steps["a"].Status != capsule.StepPending {
		t.Fatalf("the restart must recover the on-disk state: version=%d current=%q a=%s", snap.Version, snap.CurrentStep, snap.Steps["a"].Status)
	}
}

// TestTransitionRefusalMutatesNothing: a step_result refused by its own
// validation (an outcome outside the closed set) leaves the dispatch
// ordinal and the step's runtime as they were — every apply judges before
// it mutates.
func TestTransitionRefusalMutatesNothing(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	before := d.Snapshot()
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1, WallMs: 5000, Result: &StepResult{StepID: "a", SpawnN: 1, Outcome: OutcomeWeird},
	}); err == nil {
		t.Fatal("an outcome outside the closed set must be refused")
	}
	after := d.Snapshot()
	if after.Version != before.Version || after.Steps["a"].Dispatch != before.Steps["a"].Dispatch ||
		after.Steps["a"].BudgetConsumed.WallMs != 0 || after.BudgetConsumed.WallMs != 0 {
		t.Fatalf("a refused result mutated memory: before=%+v after=%+v", before.Steps["a"], after.Steps["a"])
	}
}

// The interrupted-dispatch archive records the tree hash carried on the
// request: the caller reads it off the lock, the
// transition only writes it.
func TestInterruptedArchiveRecordsTreeHash(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransInterrupted, Reason: "driver_stalled", SpawnN: 1, TreeHash: "interrupted-hash",
	}); err != nil {
		t.Fatalf("interrupted: %v", err)
	}
	at := readExec(t, execPath).Steps["a"].Attempts
	if len(at) != 1 || at[0].TreeHash != "interrupted-hash" || at[0].FailureCause != FailDriverRelaunch {
		t.Fatalf("the interrupted archive must carry the request's tree hash: %+v", at)
	}
}

// TestVerdictForIgnoresRewindTo: a deliver FAIL's output
// field `rewind_to` (the retired 「全量重验」 override) never moves the
// rewind: every gate keeps its declared on_fail target.
func TestVerdictForIgnoresRewindTo(t *testing.T) {
	d, _, _, _ := testEnv(t,
		spec("spec"), spec("code"), spec("fix"), spec("verification"), spec("review"),
		gateSpec("deliver", "fix"))
	res := func(out string) *StepResult {
		return &StepResult{StepID: "deliver", SpawnN: 1, Outcome: OutcomeGateFailed, Output: json.RawMessage(out)}
	}
	if got := d.verdictFor("deliver", res(`{"verdict":"fail","rewind_to":"verification"}`)).TargetStep; got != "fix" {
		t.Errorf("TargetStep = %q, want the declared on_fail %q", got, "fix")
	}
}

// TestStepEventsCarryCandidateIdentity pins requirement 1: the
// started and failed events self-describe the dispatch's composed candidate
// and the failure names the candidate that DIED even when the decision
// advances Step.Candidate; step_dead keeps its existing body.
func TestStepEventsCarryCandidateIdentity(t *testing.T) {
	d, log, _, _ := testEnv(t, spec("a"))
	d.cands = map[string][]string{"a": {"pi:a", "pi:b"}}
	mustInit(t, d)
	if err := d.transition(TransitionRequest{
		Kind: TransStepStart, StepID: "a", Model: "a", Candidate: "pi:a", SpawnN: 1,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := d.transition(TransitionRequest{
		Kind: TransStepFailed, StepID: "a", SpawnN: 1,
		Failure: &StepFailure{Cause: FailExitModel, Detail: "provider died"},
	}); err != nil {
		t.Fatalf("step_failed: %v", err)
	}

	var start, fail map[string]any
	for _, e := range log.entries {
		switch e.kind {
		case "step_started":
			start = e.body
		case "step_failed":
			fail = e.body
		}
	}
	if start == nil || start["candidate"] != "pi:a" {
		t.Fatalf("step_started candidate = %v, want pi:a (body %v)", start["candidate"], start)
	}
	if fail == nil || fail["candidate"] != "pi:a" {
		t.Fatalf("step_failed candidate = %v, want the dying dispatch's pi:a, not its successor (body %v)", fail["candidate"], fail)
	}
	if got := d.exec.Steps["a"].Candidate; got != "pi:b" {
		t.Fatalf("the decision must still advance Step.Candidate to pi:b, got %q", got)
	}
	// step_dead is emitted by the supervisor verdict path, not this
	// transition; supervision_test.go pins only its count and evidence. The
	// no-candidate body shape is pinned end to end by e2e scenario 7
	// (TestW14EventCandidateIdentity). Here we only assert this change did
	// not graft a candidate onto it.
	for _, e := range log.entries {
		if e.kind == "step_dead" {
			if _, ok := e.body["candidate"]; ok {
				t.Fatalf("step_dead must keep its existing shape (no candidate): %v", e.body)
			}
		}
	}
}

// testLane answers a test's execution graph: the steps and the lane's
// max_rewinds.
func testLane(maxRewinds int, steps ...StepSpec) *Workflow {
	return &Workflow{Steps: steps, MaxRewinds: maxRewinds}
}

// A gate FAIL that ends the workflow (a gate without on_fail) emits the
// terminal event naming the status and the gate, after its gate_verdict —
// the decision feed classifies it (watcher.Classify("terminal")).
func TestGateFailWithoutOnFailEmitsTerminal(t *testing.T) {
	d, log, _, _ := testEnv(t, spec("a"), StepSpec{ID: "check", Gate: true})
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	resultAt(t, d, "a", 1, OutcomeCompleted, `{}`)
	mustStart(t, d, "check", 2)
	resultAt(t, d, "check", 2, OutcomeGateFailed, `{"verdict":"fail","fail_reason":"r"}`)
	var kinds []string
	var terminal map[string]any
	for _, e := range log.entries {
		kinds = append(kinds, e.kind)
		if e.kind == "terminal" {
			terminal = e.body
		}
	}
	if terminal == nil || terminal["status"] != StatusFailedGate || terminal["gate"] != "check" {
		t.Fatalf("SYMPTOM: the terminal gate FAIL emitted no terminal{failed_gate, gate}: %v", kinds)
	}
	if i, j := slices.Index(kinds, "gate_verdict"), slices.Index(kinds, "terminal"); i < 0 || j < i {
		t.Fatalf("terminal follows the gate_verdict: %v", kinds)
	}
}

// bodies answers the bodies of the events of kind, in order.
func (l *eventLog) bodies(kind string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, e := range l.entries {
		if e.kind == kind {
			out = append(out, e.body)
		}
	}
	return out
}
