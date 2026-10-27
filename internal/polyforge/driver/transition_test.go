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
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// eventLog is the counting EventLogger fake.
type eventLog struct {
	entries []loggedEvent
}

type loggedEvent struct {
	wi   string
	kind string
	body map[string]any
}

func (l *eventLog) Append(wi, kind string, body any) error {
	b, _ := body.(map[string]any)
	l.entries = append(l.entries, loggedEvent{wi: wi, kind: kind, body: b})
	return nil
}

func (l *eventLog) kinds() []string {
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
		Workflow: &Workflow{Steps: steps, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
		Log:      log,
		Now:      clk.now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, log, clk, execPath
}

func spec(id string) StepSpec { return StepSpec{ID: id, ContextPolicy: "fresh"} }

func gateSpec(id, target string) StepSpec {
	return StepSpec{
		ID:            id,
		Gate:          true,
		ContextPolicy: "fresh",
		OnFail:        &OnFail{Target: target},
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

func TestTransitionStepStartFreshConsumesRewindInput(t *testing.T) {
	d, _, clk, _ := testEnv(t, spec("a"), spec("b"))
	mustInit(t, d)
	// Simulate a rewind having parked its verdict on step a.
	d.mu.Lock()
	d.exec.Steps["a"].PendingRewindInput = &capsule.RewindInput{ToStep: "a", Reason: "fix it harder"}
	d.exec.Steps["a"].Output = map[string]any{"stale": true}
	d.exec.Steps["a"].BudgetConsumed = capsule.Budget{WallMs: 42}
	d.mu.Unlock()

	before := clk.t
	mustStart(t, d, "a", 1)
	snap := d.Snapshot()
	st := snap.Steps["a"]
	if st.Status != capsule.StepInProgress || st.PendingRewindInput != nil {
		t.Fatalf("start must flip to in_progress and consume the rewind verdict: %+v", st)
	}
	if st.Output != nil || st.BudgetConsumed.WallMs != 0 {
		t.Fatalf("a fresh start reopens a clean window: output=%v budget=%+v", st.Output, st.BudgetConsumed)
	}
	if st.Model != "prov/m1" || st.StepStartedAt == "" || st.StepStartedAt <= before.Format(time.RFC3339Nano) {
		t.Fatalf("start must record model + a fresh measurement window: %+v", st)
	}
	if snap.CurrentStep != "a" || snap.WorkflowStatus != StatusRunning {
		t.Fatalf("start must move the position: %+v", snap)
	}
}

func TestTransitionStepStartResumeRefreshesWindow(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	first := d.Snapshot().Steps["a"].StepStartedAt
	// needs_input parks the step; the answer resume re-dispatches it
	// in place — the SAME step_start kind, resume shape.
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: &StepResult{StepID: "a", SpawnN: 1,
			Outcome: OutcomeNeedsInput, QuestionID: "q_1", Question: "which shape?"},
	}); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	if err := d.Answer("q_1", "the lean one", "alice"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	mustStart(t, d, "a", 2) // resume re-dispatch
	st := d.Snapshot().Steps["a"]
	if st.Status != capsule.StepInProgress || st.StepStartedAt == first {
		t.Fatalf("resume must refresh the window, got started_at=%q (first %q)", st.StepStartedAt, first)
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

// output.commands persists the decoded evidence (known fields only): an
// unknown key an agent adds to an entry, such as a full stdout dump,
// never reaches execution.json.
func TestTransitionCommandsKeepKnownFieldsOnly(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	big := strings.Repeat("x", 64<<10)
	res := completedResult("a", 1, `{}`)
	res.CommandEvidence = json.RawMessage(`[{"argv":["go","test"],"duration_ms":5,"stdout":"` + big + `"}]`)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: res}); err != nil {
		t.Fatalf("result: %v", err)
	}
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "stdout") || strings.Contains(string(raw), big[:1024]) {
		t.Fatalf("an unknown evidence key must not be persisted (%d bytes)", len(raw))
	}
	cmds, _ := d.Snapshot().Steps["a"].Output["commands"].([]any)
	if len(cmds) != 1 {
		t.Fatalf("the decoded entry must be persisted: %+v", d.Snapshot().Steps["a"].Output["commands"])
	}
}

func TestTransitionStepResultCompleted(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	res := completedResult("a", 1, `{"summary":"A"}`)
	res.CommandEvidence = rawEvidence([]CommandEvidence{{
		Argv: []string{"go", "test", "./..."}, Cwd: "/w", ExitCode: 0,
		StartedAt: time.Now(), DurationMs: 1200, LogPath: "logs/a.log",
	}})
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: res}); err != nil {
		t.Fatalf("result: %v", err)
	}
	st := d.Snapshot().Steps["a"]
	if st.Status != capsule.StepDone || st.Output["summary"] != "A" {
		t.Fatalf("completed must record done + output: %+v", st)
	}
	// Snapshot deep-copies via JSON, so the evidence comes back as the
	// generic decode — assert on the re-marshaled shape (B10 keys).
	cmds, ok := st.Output["commands"].([]any)
	if !ok || len(cmds) != 1 {
		t.Fatalf("B10 command evidence must land in output.commands: %+v", st.Output["commands"])
	}
	cmd, _ := cmds[0].(map[string]any)
	if cmd["log_path"] != "logs/a.log" || cmd["duration_ms"] != float64(1200) {
		t.Fatalf("bad evidence entry: %+v", cmd)
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
	// needs_input without a question payload is refused too.
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: &StepResult{StepID: "a", SpawnN: 1, Outcome: OutcomeNeedsInput},
	}); err == nil {
		t.Fatal("needs_input without question_id must be refused")
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
			Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"FAIL"}`)},
	}); err != nil {
		t.Fatalf("gate_failed: %v", err)
	}
	disk := readExec(t, execPath)
	st := disk.Steps["check"]
	if st.Status != capsule.StepInProgress || st.Output["verdict"] != "FAIL" {
		t.Fatalf("a FAILing gate is never done and holds its verdict: %+v", st)
	}
	if disk.ReviewRounds["check"] != 1 {
		t.Fatalf("review_rounds must count the evaluation: %+v", disk.ReviewRounds)
	}
	// the verdict and its rewind are ONE durable write: no crash can
	// leave a recorded FAIL that nothing consumes
	if disk.Version != before+1 || disk.CurrentStep != "a" || disk.RewindStats.Count != 1 ||
		disk.Steps["a"].PendingRewindInput == nil {
		t.Fatalf("the gate verdict must land with its rewind in one write: version %d->%d, %+v", before, disk.Version, disk)
	}
	// incomplete → blocked, position frozen, partial output recorded.
	mustStart(t, d, "a", 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 3,
		Result: &StepResult{StepID: "a", SpawnN: 3,
			Outcome: OutcomeIncomplete, Output: json.RawMessage(`{"partial":true}`)},
	}); err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	snap := d.Snapshot()
	if snap.WorkflowStatus != StatusBlocked || snap.CurrentStep != "a" || snap.Steps["a"].Status != capsule.StepInProgress {
		t.Fatalf("incomplete must block with the position frozen: %+v", snap)
	}
	if snap.Steps["a"].Output["partial"] != true {
		t.Fatalf("the honest partial output must be recorded: %+v", snap.Steps["a"])
	}
}

func TestTransitionNeedsInputAndAnswer(t *testing.T) {
	d, log, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: &StepResult{StepID: "a", SpawnN: 1,
			Outcome: OutcomeNeedsInput, QuestionID: "q_abc123", Question: "lean or compatible?",
			Output: json.RawMessage(`{"ctx":1}`)},
	}); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	snap := d.Snapshot()
	q := snap.Steps["a"].PendingQuestion
	if q == nil || q.ID != "q_abc123" || q.Question != "lean or compatible?" || q.AskedAt == "" {
		t.Fatalf("the open question must persist atomically: %+v", q)
	}
	if string(q.Context) != `{"ctx":1}` {
		t.Fatalf("the ask context rides the question: %s", q.Context)
	}
	if snap.WorkflowStatus != StatusRunning {
		t.Fatalf("a driven ask is waiting, not blocked: %q", snap.WorkflowStatus)
	}
	// Wrong question id is refused (the D9 binding).
	if err := d.Answer("q_other", "nope", "bob"); err == nil {
		t.Fatal("a mismatched question id must be refused")
	}
	if err := d.Answer("q_abc123", "the lean one", "alice"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	st := d.Snapshot().Steps["a"]
	if st.PendingQuestion != nil || len(st.Answers) != 1 {
		t.Fatalf("the answer must archive + clear in one write: %+v", st)
	}
	a := st.Answers[0]
	if a.QuestionID != "q_abc123" || a.Answer != "the lean one" || a.AnsweredBy != "alice" || a.AnsweredAt == "" {
		t.Fatalf("bad archived answer: %+v", a)
	}
	// A second answer for the consumed question has nothing to bind to.
	if err := d.Answer("q_abc123", "again", "alice"); err == nil {
		t.Fatal("an answer with no open question must be refused")
	}
	got := log.kinds()
	want := []string{"exec_init", "step_started", "needs_input", "answered"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event kinds: want %v, got %v", want, got)
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
		Gate:   &GateVerdict{GateStepID: gateID, TargetStep: "build", VerdictText: "comment wording only"},
	}); err != nil {
		t.Fatalf("gate_fail: %v", err)
	}
	return readExec(t, execPath)
}

// TestTransitionGateFailLightRewindEligibility (polyforge#222): only a
// REVIEW-gate FAIL whose recorded blocking findings (at least one) are
// all marked text_only takes the light path — the light cause on the
// target, the verdict + light instruction as the rewind input, the durable
// resume order. Every other shape (an unmarked blocking finding, no
// blocking findings at all, a malformed findings record, another gate)
// rewinds fresh exactly as before; non-blocking findings never enter the
// rule. The rewind accounting (one slot, superseded range, gate-side
// fresh re-entry window) is identical on both paths.
func TestTransitionGateFailLightRewindEligibility(t *testing.T) {
	cases := []struct {
		name     string
		gate     string
		findings string
		light    bool
	}{
		{"all blocking findings text-only", "review", `[{"severity":"blocking","detail":"d","text_only":true}]`, true},
		{"one unmarked blocking finding", "review", `[{"severity":"blocking","detail":"d","text_only":true},{"severity":"blocking","detail":"d2"}]`, false},
		{"no blocking findings", "review", `[{"severity":"non-blocking","detail":"d","text_only":true}]`, false},
		{"non-blocking markers ignored", "review", `[{"severity":"blocking","detail":"d","text_only":true},{"severity":"non-blocking","detail":"n"}]`, true},
		{"no findings recorded", "review", "", false},
		{"findings not a list", "review", `{"severity":"blocking","text_only":true}`, false},
		{"finding not an object", "review", `["blocking"]`, false},
		{"text_only not a boolean", "review", `[{"severity":"blocking","detail":"d","text_only":"true"}]`, false},
		{"non-review gate never light (verification FAIL)", "verification", `[{"severity":"blocking","detail":"d","text_only":true}]`, false},
		{"non-review gate never light (deliver back to code)", "deliver", `[{"severity":"blocking","detail":"d","text_only":true}]`, false},
	}
	for _, tc := range cases {
		exec := lightGateFail(t, tc.gate, lightReviewOutput(tc.findings))
		build, polish := exec.Steps["build"], exec.Steps["polish"]
		wantCause := CauseGateFailRewind
		if tc.light {
			wantCause = CauseGateFailRewindTextOnly
		}
		att := build.Attempts[len(build.Attempts)-1]
		if att.Cause != wantCause || att.RewindGate != tc.gate {
			t.Errorf("%s: build archived as (%s, %s), want (%s, %s)", tc.name, att.Cause, att.RewindGate, wantCause, tc.gate)
		}
		if sup := polish.Attempts[len(polish.Attempts)-1]; sup.Cause != CauseSupersededByRewind || sup.RewindGate != tc.gate {
			t.Errorf("%s: superseded range must keep its cause: %+v", tc.name, polish.Attempts)
		}
		for _, st := range []*capsule.Step{build, polish} {
			if st.Status != capsule.StepPending || st.Output != nil {
				t.Errorf("%s: the range must reset pending with output cleared: %+v", tc.name, st)
			}
		}
		if exec.RewindStats.Count != 1 || len(exec.RewindStats.ToStep) != 1 || exec.RewindStats.ToStep[0] != "build" {
			t.Errorf("%s: exactly one slot, logged to build: %+v", tc.name, exec.RewindStats)
		}
		if pri := build.PendingRewindInput; pri == nil || pri.ToStep != "build" || pri.Reason != "comment wording only" {
			t.Errorf("%s: the verdict must route to build: %+v", tc.name, pri)
		} else if want := LightRewindNote; tc.light && pri.Note != want || !tc.light && pri.Note != "" {
			t.Errorf("%s: rewind input Note = %q, want the light instruction on the light path and empty on the fresh one", tc.name, pri.Note)
		}
		// the resume order is the ledger cause (one fact, one place — review
		// B2): the light context the re-dispatch derives its resume from is
		// exactly the archived cause above; no second order exists anywhere
		if got := lightRewindContext(build.Attempts); got != tc.light {
			t.Errorf("%s: the ledger light context = %v, want %v (the resume order is the ledger cause)", tc.name, got, tc.light)
		}
		// the gate itself: in_progress holding its verdict, its own
		// re-entry ordered fresh, its window reset (D6) — on both paths.
		gate := exec.Steps[tc.gate]
		if gate.Status != capsule.StepInProgress || gate.Output["verdict"] != "fail" {
			t.Errorf("%s: the gate is never done and holds its verdict: %+v", tc.name, gate)
		}
		if gate.Supervision == nil || gate.Supervision.NextDispatch != capsule.NextDispatchFresh {
			t.Errorf("%s: the gate's own re-entry must stay fresh: %+v", tc.name, gate.Supervision)
		}
		if gate.BudgetConsumed != (capsule.Budget{}) {
			t.Errorf("%s: D6 window reset missing: %+v", tc.name, gate.BudgetConsumed)
		}
		if exec.CurrentStep != "build" {
			t.Errorf("%s: the position must move back to build: %q", tc.name, exec.CurrentStep)
		}
	}
}

// TestTransitionGateFailLightBoundsHold: a light rewind is no budget
// bypass — an over-bound light FAIL ends failed_rewind_exhausted BEFORE
// any mutation (no second archive, no second slot, no marker or verdict
// left behind), exactly like the fresh rewind.
func TestTransitionGateFailLightBoundsHold(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	failLight := func(n int64) {
		t.Helper()
		mustStart(t, d, "review", n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "review", SpawnN: n,
			Result: &StepResult{StepID: "review", SpawnN: n, Outcome: OutcomeGateFailed,
				Output: lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)},
			Gate: &GateVerdict{GateStepID: "review", TargetStep: "build", VerdictText: "comment wording only"},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	// round 1: the light rewind lands (one slot, the resume order set).
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1, Result: completedResult("build", 1, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	failLight(2)
	// round 2: build re-enters (consuming the order) and completes; the
	// second light FAIL hits max_per_step.
	mustStart(t, d, "build", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 3, Result: completedResult("build", 3, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	failLight(4)
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the second light rewind must hit max_per_step, got %q", exec.WorkflowStatus)
	}
	build := exec.Steps["build"]
	var light int
	for _, a := range build.Attempts {
		if a.Cause == CauseGateFailRewindTextOnly {
			light++
		}
		if a.Cause == CauseGateFailRewind {
			t.Errorf("the exhausted rewind must not archive: %+v", a)
		}
	}
	if light != 1 || exec.RewindStats.Count != 1 {
		t.Fatalf("the bound fired before any mutation: light archives %d, count %d", light, exec.RewindStats.Count)
	}
	if build.PendingRewindInput != nil {
		t.Errorf("the exhausted rewind must not route a verdict: %+v", build.PendingRewindInput)
	}
	// the ledger (the resume order's one home) is untouched by the bound
	// too: no new rewind archive may land — the scans above pin exactly
	// the one light archive and no fresh one, so round 2's re-entry keeps
	// the resume it derived and nothing new is ordered
}

// TestTransitionLightRewindOrderIsDurable: the resume order and the
// rewind input land in the SAME single gate-FAIL write — the order is
// the ledger fact (the light cause archived on the pending target), so
// the persisted file (the restart's only authority) already carries
// both, and a crash anywhere around a light rewind recovers through the
// ordinary restart path with no downgrade to fresh.
func TestTransitionLightRewindOrderIsDurable(t *testing.T) {
	exec := lightGateFail(t, "review", lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`))
	build := exec.Steps["build"]
	if build.Status != capsule.StepPending || !lightRewindContext(build.Attempts) {
		t.Fatalf("the persisted file must carry the resume order (the pending target + the light ledger cause): %q %+v", build.Status, build.Attempts)
	}
	att := build.Attempts[len(build.Attempts)-1]
	if att.Cause != CauseGateFailRewindTextOnly || att.RewindGate != "review" {
		t.Fatalf("the light archive must name the gate: %+v", att)
	}
	if pri := build.PendingRewindInput; pri == nil || pri.Note != LightRewindNote || pri.Reason != "comment wording only" {
		t.Fatalf("the persisted file must carry the verdict + light instruction: %+v", pri)
	}
}

// TestTransitionSelfReportRewindRewindsFreshSameSlot (polyforge#222): a
// completed result of a light re-entry carrying needs_code_change: true is
// NOT archived done — the same write archives the resumed attempt as
// gate_fail_rewind (rewind_gate = the light gate, the delivered result and
// consumed budget archived), resets the step pending with output cleared,
// routes the SAME FAIL verdict (re-derived from the gate's recorded
// output) as the fresh re-entry's rewind input (no Note), charges NO new
// slot, leaves the position on the step, and emits the rewind event —
// never step_done for a step that is not done.
func TestTransitionSelfReportRewindRewindsFreshSameSlot(t *testing.T) {
	d, log, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	mustInit(t, d)
	failLight := func(n int64) {
		t.Helper()
		mustStart(t, d, "review", n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "review", SpawnN: n,
			Result: &StepResult{StepID: "review", SpawnN: n, Outcome: OutcomeGateFailed,
				Output: lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)},
			Gate: &GateVerdict{GateStepID: "review", TargetStep: "build", VerdictText: "comment wording only"},
		}); err != nil {
			t.Fatalf("gate_fail %d: %v", n, err)
		}
	}
	// round 1: build completes, review FAILs all-text-only (the light rewind).
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1,
		Result: completedResult("build", 1, `{"ok":1}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	failLight(2)
	// round 2: the light re-entry starts (consuming the order) and
	// self-reports a structural fix.
	mustStart(t, d, "build", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 3,
		Result: completedResult("build", 3, `{"ok":2,"needs_code_change":true}`)}); err != nil {
		t.Fatalf("self-report result: %v", err)
	}
	exec := readExec(t, execPath)
	st := exec.Steps["build"]
	if st.Status != capsule.StepPending || st.Output != nil {
		t.Fatalf("the self-report must rewind the step, never archive it done: %+v", st)
	}
	att := st.Attempts[len(st.Attempts)-1]
	if att.Cause != CauseGateFailRewind || att.RewindGate != "review" || att.ArchivedAt == "" || att.ConsumedBudget == nil {
		t.Fatalf("the self-report archive must mirror the range rewind's shape: %+v", att)
	}
	var delivered map[string]any
	if json.Unmarshal(att.Result, &delivered) != nil || delivered["needs_code_change"] != true || delivered["ok"] != float64(2) {
		t.Fatalf("the delivered result must ride the archived attempt: %s", att.Result)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("the self-report reuses the light rewind's slot (no charge): %+v", exec.RewindStats)
	}
	if pri := st.PendingRewindInput; pri == nil || pri.ToStep != "build" || pri.Note != "" ||
		!strings.Contains(pri.Reason, "comment wording only") || !strings.Contains(pri.Reason, `"verdict":"fail"`) {
		t.Fatalf("the same FAIL verdict must ride the fresh re-entry (no Note): %+v", pri)
	}
	if exec.CurrentStep != "build" {
		t.Fatalf("the position stays on the rewound step: %q", exec.CurrentStep)
	}
	if got := log.kinds(); got[len(got)-1] != "rewind" {
		t.Fatalf("the self-report emits the rewind event, never step_done: %v", got)
	}
	var rewound bool
	for _, e := range log.entries {
		if e.kind == "rewind" && e.body["gate"] == "review" && e.body["target"] == "build" {
			rewound = true
		}
		if e.kind == "step_done" && e.body["step"] == "build" && e.body["spawn_n"] == int64(3) {
			t.Fatalf("a self-reporting dispatch is never done: %+v", e.body)
		}
	}
	if !rewound {
		t.Fatal("the self-report rewind event must name the light gate and the target")
	}
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
		Gate: &GateVerdict{GateStepID: "review", TargetStep: "build", VerdictText: "comment wording only"},
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
	if st.Status != capsule.StepDone || st.Output["ok"] != float64(2) {
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
			Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"FAIL: tests red"}`)},
		Gate: &GateVerdict{GateStepID: "check", TargetStep: "build",
			VerdictText: "FAIL: tests red"},
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
		if st.Status != capsule.StepPending || st.Output != nil {
			t.Fatalf("%s must be reset pending with cleared output: %+v", tc.id, st)
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
		if string(att.Result) != `{"ok":true}` {
			t.Fatalf("the archived attempt carries the prior result: %s", att.Result)
		}
	}
	// The gate itself: untouched, still in_progress holding the verdict.
	if st := snap.Steps["check"]; st.Status != capsule.StepInProgress || len(st.Attempts) != 0 || st.Output["verdict"] == nil {
		t.Fatalf("the gate is neither archived nor reset: %+v", st)
	}
	// Position, stats, routed verdict.
	if snap.CurrentStep != "build" {
		t.Fatalf("position must move back to the target: %q", snap.CurrentStep)
	}
	if snap.RewindStats.Count != 1 || len(snap.RewindStats.ToStep) != 1 || snap.RewindStats.ToStep[0] != "build" {
		t.Fatalf("rewind stats: %+v", snap.RewindStats)
	}
	if pri := snap.Steps["build"].PendingRewindInput; pri == nil || pri.Reason != "FAIL: tests red" || pri.ToStep != "build" {
		t.Fatalf("the verdict must route to the target (D8): %+v", pri)
	}
}

func TestTransitionGateFailBounds(t *testing.T) {
	// max_total = 1: the SECOND rewind hits the bound and must terminate
	// WITHOUT mutating anything (bounds run before the archive).
	d, _, _, _ := testEnv(t, spec("a"), gateSpec("check", "a"))
	d.workflow.MaxTotal = 1
	mustInit(t, d)
	for round := 0; round < 2; round++ {
		n := int64(round*2 + 1)
		mustStart(t, d, "a", n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "a", SpawnN: n, Result: completedResult("a", n, `{}`),
		}); err != nil {
			t.Fatalf("result a (round %d): %v", round, err)
		}
		mustStart(t, d, "check", n+1)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "check", SpawnN: n + 1,
			Result: &StepResult{StepID: "check", SpawnN: n + 1, Outcome: OutcomeGateFailed},
			Gate:   &GateVerdict{GateStepID: "check", TargetStep: "a", VerdictText: "v"},
		}); err != nil {
			t.Fatalf("gate_fail (round %d): %v", round, err)
		}
	}
	snap := d.Snapshot()
	if snap.WorkflowStatus != StatusFailedRewindExhausted {
		t.Fatalf("the second rewind must hit max_total: %q", snap.WorkflowStatus)
	}
	// Round 2's step a was NOT re-archived: the bound fired first.
	// ("accepted" attempts from R14 are extra; count only rewind archives.)
	rewindArchives := 0
	for _, att := range snap.Steps["a"].Attempts {
		if att.Cause == CauseGateFailRewind {
			rewindArchives++
		}
	}
	if rewindArchives != 1 {
		t.Fatalf("the exhausted rewind must not re-archive: %d rewind archives (attempts=%d)", rewindArchives, len(snap.Steps["a"].Attempts))
	}
	if snap.RewindStats.Count != 1 {
		t.Fatalf("the exhausted rewind must not count: %+v", snap.RewindStats)
	}
	// max_per_step behaves the same way per target.
	d2, _, _, _ := testEnv(t, spec("a"), gateSpec("check", "a"))
	d2.workflow.MaxPerStep = 1
	mustInit(t, d2)
	mustStart(t, d2, "a", 1)
	if err := d2.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`)}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d2, "check", 2)
	if err := d2.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 2,
		Result: &StepResult{StepID: "check", SpawnN: 2, Outcome: OutcomeGateFailed},
		Gate:   &GateVerdict{GateStepID: "check", TargetStep: "a", VerdictText: "v"},
	}); err != nil {
		t.Fatalf("first rewind: %v", err)
	}
	// ...re-run and FAIL again: per-step bound hits.
	mustStart(t, d2, "a", 3)
	if err := d2.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 3, Result: completedResult("a", 3, `{}`)}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d2, "check", 4)
	if err := d2.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 4,
		Result: &StepResult{StepID: "check", SpawnN: 4, Outcome: OutcomeGateFailed},
		Gate:   &GateVerdict{GateStepID: "check", TargetStep: "a", VerdictText: "v"},
	}); err != nil {
		t.Fatalf("second rewind: %v", err)
	}
	if s := d2.Snapshot().WorkflowStatus; s != StatusFailedRewindExhausted {
		t.Fatalf("max_per_step must terminate: %q", s)
	}
}

func TestTransitionGateFailTerminateAndIllegalTarget(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"), gateSpec("check", "a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "a", SpawnN: 1, Result: completedResult("a", 1, `{}`)}); err != nil {
		t.Fatalf("result: %v", err)
	}
	mustStart(t, d, "check", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "check", SpawnN: 2,
		Result: &StepResult{StepID: "check", SpawnN: 2, Outcome: OutcomeGateFailed},
		Gate:   &GateVerdict{GateStepID: "check", VerdictText: "v"},
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

	d2, _, _, _ := testEnv(t, spec("a"), spec("b"), gateSpec("check", "a"))
	mustInit(t, d2)
	mustStart(t, d2, "check", 1)
	// A rewind target at or after the gate is illegal ("b" in
	// [a, b, check] IS legal — the target only needs to be earlier than
	// the gate); the refused verdict leaves the gate untouched.
	for _, bad := range []string{"check", "nope"} {
		err := d2.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "check", SpawnN: 1,
			Result: &StepResult{StepID: "check", SpawnN: 1, Outcome: OutcomeGateFailed},
			Gate:   &GateVerdict{GateStepID: "check", TargetStep: bad, VerdictText: "v"},
		})
		if err == nil {
			t.Fatalf("rewind target %q must be refused", bad)
		}
	}
	if st := d2.Snapshot().Steps["check"]; st.Status != capsule.StepInProgress || st.Output != nil {
		t.Fatalf("a refused gate verdict must not half-land: %+v", st)
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
// validation (an output that is not a JSON object) leaves the dispatch
// ordinal and the step's runtime as they were — every apply judges before
// it mutates.
func TestTransitionRefusalMutatesNothing(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	before := d.Snapshot()
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1, WallMs: 5000, Result: completedResult("a", 1, `[1]`),
	}); err == nil {
		t.Fatal("a non-object output must be refused")
	}
	after := d.Snapshot()
	if after.Version != before.Version || after.Steps["a"].Dispatch != before.Steps["a"].Dispatch ||
		after.Steps["a"].BudgetConsumed.WallMs != 0 || after.BudgetConsumed.WallMs != 0 {
		t.Fatalf("a refused result mutated memory: before=%+v after=%+v", before.Steps["a"], after.Steps["a"])
	}
}

// A question carried only in the output object names the same question
// everywhere: the persisted pending question, the needs_input event, and
// the park that follows (which must not re-announce it as a restart).
func TestTransitionNeedsInputEmbeddedQuestionEventMatchesPersisted(t *testing.T) {
	d, log, _, _ := testEnv(t, spec("a"))
	mustInit(t, d)
	mustStart(t, d, "a", 1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "a", SpawnN: 1,
		Result: &StepResult{StepID: "a", SpawnN: 1, Outcome: OutcomeNeedsInput,
			Output: json.RawMessage(`{"question_id":"q_emb","question":"which one?"}`)},
	}); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	if q := d.Snapshot().Steps["a"].PendingQuestion; q == nil || q.ID != "q_emb" || q.Question != "which one?" {
		t.Fatalf("persisted question: %+v", q)
	}
	d.announceWaiting()
	var asked []map[string]any
	for _, e := range log.entries {
		if e.kind == "needs_input" {
			asked = append(asked, e.body)
		}
	}
	if len(asked) != 1 || asked[0]["question_id"] != "q_emb" || asked[0]["question"] != "which one?" {
		t.Fatalf("one needs_input event naming the persisted question, got %v", asked)
	}
}

// The interrupted-dispatch archive records the tree hash carried on the
// request (workbench#21): the caller reads it off the lock, the
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

// TestVerdictForRestrictsDeliverRewindTo (workbench#19 B1): the deliver
// `rewind_to` override is honored ONLY for `verification` while the
// recorded tier is scoped; every other value (code, spec, deliver, an
// unknown step) and `verification` on a full-tier ticket keep the gate's
// declared on_fail target.
func TestVerdictForRestrictsDeliverRewindTo(t *testing.T) {
	d, _, _, _ := testEnv(t,
		spec("spec"), spec("code"), spec("fix"), spec("verification"), spec("review"),
		gateSpec("deliver", "fix"))
	res := func(out string) *StepResult {
		return &StepResult{StepID: "deliver", SpawnN: 1, Outcome: OutcomeGateFailed, Output: json.RawMessage(out)}
	}
	setTier := func(tier string) {
		d.mu.Lock()
		d.exec.ScopeTier = tier
		d.mu.Unlock()
	}
	setTier(tierScoped)
	for _, tc := range []struct{ name, out, want string }{
		{"verification honored while scoped", `{"verdict":"fail","rewind_to":"verification"}`, "verification"},
		{"code ignored", `{"verdict":"fail","rewind_to":"code"}`, "fix"},
		{"spec ignored", `{"verdict":"fail","rewind_to":"spec"}`, "fix"},
		{"deliver ignored (self target)", `{"verdict":"fail","rewind_to":"deliver"}`, "fix"},
		{"unknown ignored", `{"verdict":"fail","rewind_to":"nowhere"}`, "fix"},
		{"absent keeps on_fail", `{"verdict":"fail"}`, "fix"},
	} {
		if got := d.verdictFor("deliver", res(tc.out)).TargetStep; got != tc.want {
			t.Errorf("scoped %s: TargetStep = %q, want %q", tc.name, got, tc.want)
		}
	}
	// A full-tier ticket has no 「全量重验」 answer to offer: the override is
	// ignored and the declared on_fail target stands.
	for _, tier := range []string{tierFull, ""} {
		setTier(tier)
		if got := d.verdictFor("deliver", res(`{"verdict":"fail","rewind_to":"verification"}`)).TargetStep; got != "fix" {
			t.Errorf("tier %q: TargetStep = %q, want %q (a full ticket keeps on_fail)", tier, got, "fix")
		}
	}
	// The override never applies off the deliver step.
	setTier(tierScoped)
	if got := d.verdictFor("review", res(`{"verdict":"fail","rewind_to":"verification"}`)).TargetStep; got != "" {
		t.Errorf("a non-deliver step honored rewind_to: TargetStep = %q", got)
	}
}

// TestTransitionVerificationFailResetsTierFull (workbench#19 B3): a
// verification-gate FAIL resets the recorded tier to full, so the in-place
// re-run renders the FULL section and a scoped claim is rejected (spec
// A3). A review-gate FAIL keeps the scoped tier, so the incremental
// re-verification can carry `scope.prior`.
func TestTransitionVerificationFailResetsTierFull(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate string
		want string
	}{
		{"verification FAIL resets to full", verificationStepID, tierFull},
		{"review FAIL keeps scoped", reviewStepID, tierScoped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _, execPath := testEnv(t, spec("code"), gateSpec(tc.gate, "code"))
			mustInit(t, d)
			d.mu.Lock()
			d.exec.ScopeTier = tierScoped
			d.mu.Unlock()
			mustStart(t, d, "code", 1)
			if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "code", SpawnN: 1,
				Result: completedResult("code", 1, `{"ok":true}`)}); err != nil {
				t.Fatalf("code result: %v", err)
			}
			mustStart(t, d, tc.gate, 2)
			if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: tc.gate, SpawnN: 2,
				Result: &StepResult{WIID: "wi_t1", StepID: tc.gate, SpawnN: 2, Outcome: OutcomeGateFailed,
					Output: json.RawMessage(`{"verdict":"fail","fail_reason":"red"}`)},
				Gate: &GateVerdict{GateStepID: tc.gate, TargetStep: "code", VerdictText: "red"}}); err != nil {
				t.Fatalf("%s gate_fail: %v", tc.gate, err)
			}
			if got := readExec(t, execPath).ScopeTier; got != tc.want {
				t.Fatalf("after %s FAIL: recorded tier = %q, want %q", tc.gate, got, tc.want)
			}
		})
	}
}

// TestStepEventsCarryCandidateIdentity pins workbench#14 requirement 1: the
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
