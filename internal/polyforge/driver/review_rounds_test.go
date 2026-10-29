package driver

// review_rounds_test.go: the light and converging
// exemptions from the per-edge rewind quota. A converging review round
// (every blocking finding a residual_of the last verdict round's, and
// strictly fewer) charges no gate→target edge slot, a light rewind
// never did — both still count toward max_total; a needs_input or
// incomplete round is no verdict round: it moves neither the baseline
// nor the counters.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// reviewFailOut builds one review FAIL verdict's output.
func reviewFailOut(findings string) json.RawMessage {
	return json.RawMessage(`{"verdict":"fail","fail_reason":"blocking findings remain","findings":` + findings + `}`)
}

// runReviewRound completes build and delivers the review verdict at
// ordinals n, n+1.
func runReviewRound(t *testing.T, d *Driver, out json.RawMessage, n int64) {
	t.Helper()
	mustStart(t, d, "build", n)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "build", SpawnN: n, Result: completedResult("build", n, `{}`),
	}); err != nil {
		t.Fatalf("result build %d: %v", n, err)
	}
	mustStart(t, d, "review", n+1)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: n + 1,
		Result: &StepResult{StepID: "review", SpawnN: n + 1, Outcome: OutcomeGateFailed, Output: out},
	}); err != nil {
		t.Fatalf("review fail %d: %v", n+1, err)
	}
}

// wantRound asserts the review step's LastReviewRound ordinal and
// blocking count (the heads ride only when the round's settlement
// pre-read them — see TestVerdictRoundsRecordJudgedHeads).
func wantRound(t *testing.T, rr *capsule.ReviewRound, round, blocking int64) {
	t.Helper()
	if rr == nil || rr.Round != round || rr.Blocking != blocking {
		t.Fatalf("LastReviewRound = %+v, want {round %d, blocking %d}", rr, round, blocking)
	}
}

// TestReviewConvergingSequenceChargesNoEdge: 3 → 2 → 1 → pass with
// every post-first round fully residual is a converging sequence — the
// first round charges the edge, the converging rounds charge none, and
// a max_per_step of 1 never trips: the lane completes. Every round
// still counts toward max_total and updates the verdict-round record.
func TestReviewConvergingSequenceChargesNoEdge(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxTotal = 10
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a"},
		{"severity":"blocking","detail":"b"},
		{"severity":"blocking","detail":"c"}]`), 1)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a","residual_of":"s2#1"},
		{"severity":"blocking","detail":"b","residual_of":"s2#2"}]`), 3)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a","residual_of":"s4#1"}]`), 5)
	wantRound(t, readExec(t, execPath).Steps["review"].LastReviewRound, 3, 1)
	// the pass round ends the lane; its record is the pass round's.
	mustStart(t, d, "build", 7)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "build", SpawnN: 7, Result: completedResult("build", 7, `{}`),
	}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	mustStart(t, d, "review", 8)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 8,
		Result: completedResult("review", 8, `{"verdict":"pass"}`),
	}); err != nil {
		t.Fatalf("review pass: %v", err)
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusCompleted, SpawnN: 8}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 3 {
		t.Fatalf("the converging sequence must complete on three max_total slots: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	wantCharged := []bool{true, false, false}
	for i, want := range wantCharged {
		if got := x.RewindStats.Rewinds[i]; got.Charged != want || got.Gate != "review" || got.ToStep != "build" {
			t.Fatalf("rewind record %d = %+v, want charged=%v", i, got, want)
		}
	}
	wantRound(t, x.Steps["review"].LastReviewRound, 3, 0)
}

// TestReviewNewDiscoveryChargesTheEdge: a round that mixes residuals
// with any unmarked blocking finding, or carries no baseline to converge
// against, is charged like any fresh one — the edge bound still ends the
// workflow (fail closed on a missing residual_of).
func TestReviewNewDiscoveryChargesTheEdge(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxTotal = 10
	d.workflow.MaxPerStep = 2
	mustInit(t, d)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a"},
		{"severity":"blocking","detail":"b"},
		{"severity":"blocking","detail":"c"}]`), 1)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a","residual_of":"s2#1"},
		{"severity":"blocking","detail":"new bug"}]`), 3) // a new discovery: charged
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"another new bug"}]`), 5) // charged: the edge is at its bound
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != 2 {
		t.Fatalf("the third charged round must exhaust the edge: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	for i, want := range []bool{true, true} {
		if got := x.RewindStats.Rewinds[i]; got.Charged != want {
			t.Fatalf("rewind record %d = %+v, want charged=%v", i, got, want)
		}
	}
}

// TestReviewResidualsWithoutBaselineCharge: residual_of markers without
// a verdict-round baseline prove nothing — the first round is charged
// like any fresh one even when every finding carries a marker.
func TestReviewResidualsWithoutBaselineCharge(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	mustInit(t, d)
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a","residual_of":"s0#1"},
		{"severity":"blocking","detail":"b","residual_of":"s0#2"}]`), 1)
	x := readExec(t, execPath)
	if x.RewindStats.Count != 1 || len(x.RewindStats.Rewinds) != 1 || !x.RewindStats.Rewinds[0].Charged {
		t.Fatalf("a first round with no baseline must charge: %+v", x.RewindStats)
	}
}

// TestReviewNonVerdictRoundsMoveNoBaseline: a needs_input or incomplete
// review round carries no verdict — it writes no round record, clears
// nothing and counts nothing; the next FAIL round converges against the
// last verdict round's facts exactly as before.
func TestReviewNonVerdictRoundsMoveNoBaseline(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxTotal = 10
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	// round 1: a charged FAIL with three blocking findings.
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a"},
		{"severity":"blocking","detail":"b"},
		{"severity":"blocking","detail":"c"}]`), 1)
	wantRound(t, readExec(t, execPath).Steps["review"].LastReviewRound, 1, 3)
	// a needs_input round: no verdict, no record move, no counter move.
	mustStart(t, d, "build", 3)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "build", SpawnN: 3, Result: completedResult("build", 3, `{}`),
	}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	mustStart(t, d, "review", 4)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 4,
		Result: &StepResult{StepID: "review", SpawnN: 4, Outcome: OutcomeNeedsInput, QuestionID: "q1", Question: "which?"},
	}); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	x := readExec(t, execPath)
	wantRound(t, x.Steps["review"].LastReviewRound, 1, 3)
	if x.ReviewRounds["review"] != 1 || x.RewindStats.Count != 1 {
		t.Fatalf("a needs_input round must not move the counters: rounds=%v stats=%+v", x.ReviewRounds, x.RewindStats)
	}
	if err := d.Answer("q1", "do that", "owner"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	// the answered re-run FAILs with two residuals: converging against
	// {1,3} — charged=false (build is done; the re-dispatched review is a
	// resume of the in-progress gate).
	mustStart(t, d, "review", 5)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 5,
		Result: &StepResult{StepID: "review", SpawnN: 5, Outcome: OutcomeGateFailed,
			Output: reviewFailOut(`[
			{"severity":"blocking","detail":"a","residual_of":"s2#1"},
			{"severity":"blocking","detail":"b","residual_of":"s2#2"}]`)},
	}); err != nil {
		t.Fatalf("review fail after the answer: %v", err)
	}
	x = readExec(t, execPath)
	if x.RewindStats.Count != 2 || len(x.RewindStats.Rewinds) != 2 || x.RewindStats.Rewinds[1].Charged {
		t.Fatalf("the converging round after the answer must charge no edge: %+v", x.RewindStats)
	}
	// an incomplete round: blocked, no record move; the resumed re-run
	// converges against the same {2,2}.
	mustStart(t, d, "review", 6)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 6,
		Result: &StepResult{StepID: "review", SpawnN: 6, Outcome: OutcomeIncomplete, Output: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatalf("incomplete: %v", err)
	}
	x = readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("an incomplete review parks the workflow: %q", x.WorkflowStatus)
	}
	wantRound(t, x.Steps["review"].LastReviewRound, 2, 2)
	if err := d.transition(TransitionRequest{Kind: TransUnblock, SpawnN: 6}); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	runReviewRound(t, d, reviewFailOut(`[
		{"severity":"blocking","detail":"a","residual_of":"s6#1"}]`), 7)
	x = readExec(t, execPath)
	if x.RewindStats.Count != 3 || x.RewindStats.Rewinds[2].Charged {
		t.Fatalf("the converging round after the resume must charge no edge: %+v", x.RewindStats)
	}
}

// TestReviewLightRewindCountsTowardMaxTotalOnly: the text-only light
// rewind charges no edge slot but still counts toward max_total — the
// bound that stops a light loop is the global one (the per-edge
// accounting is pinned by TestTransitionGateFailLightBoundsHold).
func TestReviewLightRewindCountsTowardMaxTotalOnly(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	d.workflow.MaxTotal = 2
	d.workflow.MaxPerStep = 1
	mustInit(t, d)
	lightOut := lightReviewOutput(`[{"severity":"blocking","detail":"d","text_only":true}]`)
	failLight := func(n int64) {
		t.Helper()
		mustStart(t, d, "build", n)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "build", SpawnN: n, Result: completedResult("build", n, `{}`),
		}); err != nil {
			t.Fatalf("result build %d: %v", n, err)
		}
		mustStart(t, d, "review", n+1)
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: "review", SpawnN: n + 1,
			Result: &StepResult{StepID: "review", SpawnN: n + 1, Outcome: OutcomeGateFailed, Output: lightOut},
		}); err != nil {
			t.Fatalf("light fail %d: %v", n+1, err)
		}
	}
	failLight(1)
	failLight(3)
	failLight(5) // count 2 = max_total: exhausted before any mutation
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusFailedRewindExhausted || x.RewindStats.Count != 2 {
		t.Fatalf("light rewinds must exhaust max_total, never the edge: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	for i := range x.RewindStats.Rewinds {
		if x.RewindStats.Rewinds[i].Charged {
			t.Fatalf("a light rewind must charge no edge slot: %+v", x.RewindStats.Rewinds[i])
		}
	}
}

// TestVerdictRoundsRecordJudgedHeads (code review): every
// VERDICT-BEARING review round records the task-repo heads it judged on
// its own LastReviewRound — a FAIL names the tree it failed, a PASS the
// tree it covered — so the next re-entry's fix surface has an anchor
// that survives rewinds (reviewed_heads exists on a PASS alone and a
// rewind archives it). A needs_input round moves nothing.
func TestVerdictRoundsRecordJudgedHeads(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	// one task repo with a commit on main
	dir := filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, dir, "init", "-q", "-b", "main")
	dwrite(t, filepath.Join(dir, "work.txt"), "w\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "work")
	head1 := tgit(t, dir, "rev-parse", "HEAD")

	mustInit(t, d)
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1, Result: completedResult("build", 1, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	// round 1: a FAIL verdict records the heads of the tree it failed.
	failRes := &StepResult{StepID: "review", SpawnN: 2, Outcome: OutcomeGateFailed,
		Output: reviewFailOut(`[{"severity":"blocking","detail":"a"}]`)}
	mustStart(t, d, "review", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 2, Result: failRes,
		JudgedHeads: d.judgedHeads("review", failRes),
	}); err != nil {
		t.Fatalf("review fail: %v", err)
	}
	x := readExec(t, execPath)
	wantRound(t, x.Steps["review"].LastReviewRound, 1, 1)
	if got := x.Steps["review"].LastReviewRound.Heads["demo"]; got != head1 {
		t.Fatalf("the FAIL round must record the heads it judged: %+v", x.Steps["review"].LastReviewRound.Heads)
	}
	// the fix lands; a needs_input round moves neither record nor heads.
	dwrite(t, filepath.Join(dir, "work.txt"), "fixed\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "fix")
	head2 := tgit(t, dir, "rev-parse", "HEAD")
	mustStart(t, d, "build", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 3, Result: completedResult("build", 3, `{}`)}); err != nil {
		t.Fatalf("result build re-entry: %v", err)
	}
	mustStart(t, d, "review", 4)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 4,
		Result: &StepResult{StepID: "review", SpawnN: 4, Outcome: OutcomeNeedsInput, QuestionID: "q1", Question: "which?"},
	}); err != nil {
		t.Fatalf("needs_input: %v", err)
	}
	x = readExec(t, execPath)
	wantRound(t, x.Steps["review"].LastReviewRound, 1, 1)
	if got := x.Steps["review"].LastReviewRound.Heads["demo"]; got != head1 {
		t.Fatalf("a needs_input round must not move the recorded heads: %+v", x.Steps["review"].LastReviewRound.Heads)
	}
	if err := d.Answer("q1", "do that", "owner"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	// the answered re-run PASSes on the fixed tree: its record carries
	// the new heads (and the same round ordinal — a pass adds no round).
	passRes := &StepResult{StepID: "review", SpawnN: 5, Outcome: OutcomeCompleted,
		Output: json.RawMessage(`{"verdict":"pass"}`)}
	mustStart(t, d, "review", 5)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 5, Result: passRes,
		JudgedHeads:   d.judgedHeads("review", passRes),
		ReviewedHeads: d.reviewedHeads("review", passRes),
	}); err != nil {
		t.Fatalf("review pass: %v", err)
	}
	x = readExec(t, execPath)
	wantRound(t, x.Steps["review"].LastReviewRound, 1, 0)
	if got := x.Steps["review"].LastReviewRound.Heads["demo"]; got != head2 {
		t.Fatalf("the pass round must record the heads it covered: %+v", x.Steps["review"].LastReviewRound.Heads)
	}
	if out := x.Steps["review"].Output; out[reviewedHeadsKey] == nil {
		t.Fatal("the pass round must still write its reviewed_heads (the deliver anchor)")
	}
}
