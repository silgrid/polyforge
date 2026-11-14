package test

// v2_supervision_test.go: the driver <-> supervisor contract end to end
// (v2-converge 2.2/2.3, appendix B), against the REAL `pf daemon` + the
// fakepi step process. Every scenario injects the supervision bounds
// through the daemon's own flags and asserts on the durable faces: the
// events log (step_dead for a supervisor verdict, step_failed /
// candidate_switched / blocked from the driver), execution.json (the
// attempts' cause sub-class, the per-step failure ledger, the relaxed
// bounds) and the fakepi journal (which model each dispatch ran under).
//
// The supervision rules (design #483 §7) keep one detailed test each in
// internal/polyforge/driver/supervision_rules_test.go and at most one e2e
// smoke each:
//
//	ended without a hand-off -> TestV2OkEndedTurnWithoutResultRetriesSameModel
//	model class              -> w33's TestW33ChainEndModelDeathBlocksAndResumeKeepsEndModel
//	transient                -> v2_live_error_turn_d7_test.go
//	timeout                  -> TestV2StepTimeoutLongTaskRelaxesThenBlocks
//	other exit               -> v1_s17_recovery_budget_test.go
//
// and the facts around them:
//
//	#83  long tool within Y                       -> TestV2LongToolWithinYSurvives
//	F15  UI noise only                            -> v1_s21 shape A
//	#99  parked question: no timers               -> v1_s21 (both shapes)
//	#114 provider hang mid-step                   -> w114
//	restart: not counted, same model              -> TestV2DaemonRestart...
//	two rules at once: one verdict, one failure   -> TestV2TwoRules...

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// failEvent is the step_failed event body.
type failEvent struct {
	Step        string `json:"step"`
	SpawnN      int64  `json:"spawn_n"`
	Cause       string `json:"cause"`
	Disposition string `json:"disposition"`
	Count       int    `json:"failure_count"`
	Next        string `json:"next_dispatch"`
	From        string `json:"from_model"`
	To          string `json:"to_model"`
	RelaxedTool int64  `json:"relaxed_tool_ms"`
	RelaxedWall int64  `json:"relaxed_wall_ms"`
	Exhausted   bool   `json:"candidates_exhausted"`
}

func stepFailures(t *testing.T, d *v1Daemon, wi string) []failEvent {
	t.Helper()
	var out []failEvent
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind != "step_failed" {
			continue
		}
		var f failEvent
		eventBody(t, e, &f)
		out = append(out, f)
	}
	return out
}

type verdictEvent struct {
	Step     string `json:"step"`
	SpawnN   int64  `json:"spawn_n"`
	Cause    string `json:"cause"`
	Evidence string `json:"evidence"`
}

func verdicts(t *testing.T, d *v1Daemon, wi string) []verdictEvent {
	t.Helper()
	var out []verdictEvent
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind != "step_dead" {
			continue
		}
		var v verdictEvent
		eventBody(t, e, &v)
		out = append(out, v)
	}
	return out
}

// spawnModels lists the pinned model of every fakepi spawn, in order.
func spawnModels(t *testing.T, capDir string) []string {
	t.Helper()
	var out []string
	for _, rec := range fakeJournal(t, capDir) {
		if rec["type"] == "spawn" {
			m, _ := rec["model"].(string)
			out = append(out, m)
		}
	}
	return out
}

// healthyTail is the vehicle lane's healthy rest after its first step
// (fix): the review gate passes.
func healthyTail() []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

func plan(first []map[string]any) []map[string]any {
	return append(first, healthyTail()...)
}

// TestV2LongToolWithinYSurvives: #83. A long build/edit is live work: the
// harness is record-quiet for far longer than the silence window while the
// tool runs, and the step must NOT be judged dead — rule 3 only matters
// past Y.
func TestV2LongToolWithinYSurvives(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "#83: a long tool within Y survives",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed",
				"tool_ms": 6000, "tool_name": "bash"},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{silence: time.Second, tool: 30 * time.Second}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	if vs := verdicts(t, d, wi); len(vs) != 0 {
		t.Fatalf("a tool within Y was judged dead: %+v", vs)
	}
	if n := len(stepFailures(t, d, wi)); n != 0 {
		t.Fatalf("a healthy long tool left %d failure records", n)
	}
}

// TestV2StepTimeoutLongTaskRelaxesThenBlocks: step_timeout_long_task (the
// time went to a tool) retries the same model with T relaxed x2; a SECOND
// timeout blocks the step. T accumulates across the step's dispatches, the
// blocked state is durable (execution.json + the aihub work item), and the
// owner is told (blocked event).
func TestV2StepTimeoutLongTaskRelaxesThenBlocks(t *testing.T) {
	inst := newV1Install(t)
	inst.stripWallBudgets = true
	wi := inst.newWI(t, "T: long task relaxes once, the second timeout blocks",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "tool_ms": 600000, "tool_name": "bash"},
			{"do": "envelope", "id": "fix", "outcome": "completed", "tool_ms": 600000, "tool_name": "bash"},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{
		silence: 30 * time.Second, tool: 5 * time.Minute, stepTimeout: 3 * time.Second,
	}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "blocked", 120*time.Second)
	fails := stepFailures(t, d, wi)
	if len(fails) != 2 {
		t.Fatalf("failure records %+v, want two step_timeout_long_task", fails)
	}
	if fails[0].Cause != "step_timeout_long_task" || fails[0].Disposition != "retry_same_model" || fails[0].RelaxedWall != 6000 {
		t.Fatalf("first timeout %+v, want retry_same_model with T relaxed to 6000ms", fails[0])
	}
	if fails[1].Cause != "step_timeout_long_task" || fails[1].Disposition != "blocked" {
		t.Fatalf("second timeout %+v, want blocked", fails[1])
	}
	x := readExecution(t, d.capsuleDir(wi))
	// design #483 §7: a timeout resumes once with the bound doubled; a
	// second one parks, its reason naming the doubled bound it ran out
	if x.WorkflowStatus != "blocked" || !strings.Contains(x.BlockedReason, "timed out again (step_timeout_long_task) after its bound was already doubled once") {
		t.Fatalf("execution.json: status %q reason %q", x.WorkflowStatus, x.BlockedReason)
	}
	sup := x.Steps["fix"].Supervision
	// the blocking second timeout doubles T again (6s -> 12s) before it
	// parks, so a human continue runs under a fresh window
	if sup == nil || sup["relaxed_wall_ms"] != float64(12000) {
		t.Fatalf("the relaxed T must persist (doubled again by the blocking timeout): %+v", sup)
	}
	// T accumulates across dispatches: the second dispatch was judged
	// against the runtime the first already spent (it was killed well
	// before a fresh 6s would have elapsed).
	if wall, _ := x.Steps["fix"].BudgetConsumed["wall_ms"].(float64); wall < 5000 || wall > 15000 {
		t.Fatalf("cumulative step wall %vms, want ~6-7s (3s + the remainder of the relaxed 6s)", wall)
	}
	if n := countOf(d.eventKinds(t, wi), "blocked"); n != 1 {
		t.Fatalf("blocked event count %d, want 1: %v", n, d.eventKinds(t, wi))
	}
}

// TestV2DaemonRestartNotCountedSameModel: a daemon restart mid-dispatch
// re-dispatches the step on the SAME model, counts NO failure and never
// advances the candidate pointer (a restart is not a model-class cause).
func TestV2DaemonRestartNotCountedSameModel(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "restart: not counted, same model",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 60000},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
		}), nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	waitSpawnTranscript(t, d, wi, 1, 30*time.Second)
	waitUntil(t, 30*time.Second, "fakepi never journaled its first spawn", func() bool {
		return len(spawnModels(t, d.capsuleDir(wi))) >= 1
	})
	firstModels := spawnModels(t, d.capsuleDir(wi))
	if len(firstModels) == 0 {
		t.Fatalf("no fakepi spawn record before the crash")
	}

	d.crash()
	for _, pid := range orphanPIDs(wi) { // the dispatch dies with its daemon's tree
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	d2 := restartSameDaemon(t, d, "daemon2.log")
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)

	models := spawnModels(t, d2.capsuleDir(wi))
	if len(models) < 2 || models[1] != firstModels[0] {
		t.Fatalf("the restart re-dispatch changed the model: %v (first %v)", models, firstModels)
	}
	x := readExecution(t, d2.capsuleDir(wi))
	sup := x.Steps["fix"].Supervision
	if sup != nil {
		if c, _ := sup["failure_count"].(float64); c != 0 {
			t.Fatalf("a daemon restart was counted as a failure: %+v", sup)
		}
	}
	for _, a := range x.Steps["fix"].Attempts {
		if fc, _ := a["failure_cause"].(string); isModelClassCause(fc) {
			t.Fatalf("a daemon restart advanced the candidate pointer: %+v", a)
		}
	}
	if fails := stepFailures(t, d2, wi); len(fails) != 0 {
		t.Fatalf("a daemon restart produced step_failed records: %+v", fails)
	}
}

// TestV2TwoRulesAtOnceOneVerdictOneFailure: with the silence bound and T
// both tiny, a silent harness trips rule 0 AND rule 4 at once. The lower
// number wins, exactly ONE verdict is sent, the driver kills the process
// and records exactly ONE failure — the exit that follows the kill is not
// counted a second time.
//
// The retry must complete under the same tiny T, and D6 makes T cumulative
// across the step's dispatches while rule 0 (unlike a timeout) does not
// relax it: dispatch 2 is born with T already spent, so rule 4 kills it at
// the supervisor's first look unless its hand-off is recorded by then. Its
// own `pf step handoff` (the fakepi's 250ms settle, the scripted 150ms
// delay, the call) only beat that first look by tens of milliseconds, and a
// loaded host lost the race: the retry died as step_timeout_long_task, the
// next dispatch took the review step's scripted hand-off and review, left
// with none, timed out until the item blocked. The retry's hand-off is
// therefore recorded in the step-call ledger (runtime-state/step-calls.jsonl,
// where Wait and the verdict path's final look collect it) before the
// dispatch starts: a delivery wins any verdict, so the outcome no longer
// depends on tick timing. (It used to be staged at the old envelope drop
// path, runtime-state/pending-results/<spawn>.json; nothing has read that
// path since hand-offs moved to `pf step handoff`, which is why this test
// became flaky again.) The retry itself still dispatches (asserted below),
// and the scripted 150ms delivery delay is kept so a tree that goes back to
// relying on delivery timing fails here under load. Rule 0 outranking rule 4
// at one instant is pinned deterministically by TestJudgeRuleTable's
// priority case.
func TestV2TwoRulesAtOnceOneVerdictOneFailure(t *testing.T) {
	inst := newV1Install(t)
	inst.stripWallBudgets = true
	wi := inst.newWI(t, "two rules fire at once",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "silent": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 150},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{
		silence: 2 * time.Second, stepTimeout: 2 * time.Second, tool: 30 * time.Second,
	}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// Record the retry's hand-off. The capsule is materialized by the
	// claim; execution.json is the driver's first write, so its presence
	// means the claim is done and the record will not be wiped by
	// materialization. Dispatch 1 is silent for its whole life (no ledger
	// line of its own), so these two lines are the ledger's only writes
	// until dispatch 2 starts.
	capDir := d.capsuleDir(wi)
	waitUntil(t, 30*time.Second, "the capsule runtime state never materialized", func() bool {
		_, err := os.Stat(filepath.Join(capDir, "runtime-state", "execution.json"))
		return err == nil
	})
	call, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindCall, Step: "fix", Spawn: 2, Verb: stepcall.VerbHandoff,
		Args: json.RawMessage(`{"status":"done"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stepcall.Append(capDir, stepcall.Line{Kind: stepcall.KindResult, Step: "fix", Spawn: 2, Verb: stepcall.VerbHandoff,
		Call: call.Seq, Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: stepcall.StatusDone}}); err != nil {
		t.Fatal(err)
	}

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	vs := verdicts(t, d, wi)
	if len(vs) != 1 || vs[0].Cause != "harness_silent" {
		t.Fatalf("verdicts %+v, want exactly one harness_silent (rule 0 outranks rule 4)", vs)
	}
	if fails := stepFailures(t, d, wi); len(fails) != 1 {
		t.Fatalf("failure records %+v, want exactly one for the dispatch", fails)
	}
	spawns := spawnJournal(t, d.capsuleDir(wi))
	if len(spawns) < 2 || spawns[1]["spawn_n"] != float64(2) {
		t.Fatalf("spawn journal %+v, want the retry dispatched as spawn 2", spawns)
	}
	x := readExecution(t, d.capsuleDir(wi))
	infra := 0
	for _, a := range x.Steps["fix"].Attempts {
		if a["cause"] == "infra_failure" {
			infra++
		}
	}
	if infra != 1 {
		t.Fatalf("fix archived %d infra_failure attempts, want 1: %+v", infra, x.Steps["fix"].Attempts)
	}
}

// TestV2OkEndedTurnWithoutResultRetriesSameModel: rule 1b. A turn that
// ENDS NORMALLY and then idles with no result file is dead one grace
// later (ended_without_result), judged as a same-model retry — NOT a
// candidate switch — and the same-session retry carries the
// ended-without-result feedback (the short reminder).
func TestV2OkEndedTurnWithoutResultRetriesSameModel(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "rule 1b: ok-ended turn without result",
		plan([]map[string]any{
			{"do": "envelope", "id": "fix", "end_turn": true},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
		}), nil)
	grace := 2 * time.Second
	d := newV1Daemon(t, inst, optSupervision(supBounds{modelErrorGrace: grace}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	rec := waitForEvent(t, d, wi, "step_dead", 60*time.Second)
	var v verdictEvent
	eventBody(t, rec, &v)
	if v.Cause != "ended_without_result" {
		t.Fatalf("verdict %+v, want step_dead ended_without_result", v)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Cause != "ended_without_result" || fails[0].Disposition != "retry_same_model" ||
		fails[0].Count != 1 {
		t.Fatalf("failure records %+v, want one ended_without_result/retry_same_model", fails)
	}
	models := spawnModels(t, d.capsuleDir(wi))
	if len(models) < 2 || models[0] != models[1] {
		t.Fatalf("ended_without_result retries the SAME model: %v", models)
	}
	var hints []bool
	for _, j := range fakeJournal(t, d.capsuleDir(wi)) {
		if j["type"] == "prompt" {
			h, _ := j["ended_without_result_hint"].(bool)
			hints = append(hints, h)
		}
	}
	if len(hints) < 2 || hints[0] || !hints[1] {
		t.Fatalf("prompt hints %v: only the retry may carry the ended-without-result feedback", hints)
	}
}

// isModelClassCause mirrors driver.IsModelClass: the causes that advance
// the candidate pointer.
func isModelClassCause(c string) bool {
	switch c {
	case "model_error", "model_hung", "step_timeout_slow_model", "exit_model_class":
		return true
	}
	return false
}
