package test

// v2_supervision_test.go: the driver <-> supervisor contract end to end
// (v2-converge 2.2/2.3, appendix B), one historical incident per test,
// against the REAL `pf daemon` + the fakepi step process. Every scenario
// injects the supervision bounds through the daemon's own flags and
// asserts on the durable faces: the events log (step_dead for a
// supervisor verdict, step_failed / candidate_switched / blocked from the
// driver), execution.json (the attempts' cause sub-class, the per-step
// failure ledger, the relaxed bounds) and the fakepi journal (which model
// each dispatch ran under).
//
//	F5   fatal provider error, then idle          -> TestV2F5...
//	F15  UI noise only                            -> v1_s21 shape A
//	#83  long tool within Y / tool beyond Y       -> TestV2Long..., TestV2ToolHung...
//	#99  parked question: no timers               -> v1_s21 (both shapes)
//	#114 provider hang mid-step / zero events     -> w114, TestV2ZeroEvents...
//	#117 trickle progress past T                  -> TestV2StepTimeoutTrickle...
//	F4   model-pin failure exit                   -> TestV2ModelPinFailure...
//	restart: not counted, same model              -> TestV2DaemonRestart...
//	crash loop: bounded, blocked                  -> TestV2DriverCrashLoop...
//	two rules at once: one verdict, one failure   -> TestV2TwoRules...

import (
	"strings"
	"syscall"
	"testing"
	"time"
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

func healthyTail() []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}
}

func plan(first []map[string]any) []map[string]any {
	return append(first, healthyTail()...)
}

// TestV2F5FatalProviderErrorThenIdleModelError: F5. A real harness does
// NOT self-retry after a fatal turn error: the turn ends, nobody sends
// the next prompt, the session idles forever (35+ minutes observed).
// Rule 1 judges it one grace after the error — the turn ended with an
// error and no result file followed — and the death is model_error (a
// model-class death): the first on the candidate resumes the same
// candidate after the retry pacing, the second consecutive one advances
// the candidate. The error text here is a provider error naming no
// throttle, transient, allowance or auth problem: a throttle or
// upstream-transient text takes the D7 same-model path instead
// (v2_live_error_turn_d7_test.go, polyforge#162), an allowance text
// switches at once.
func TestV2F5FatalProviderErrorThenIdleModelError(t *testing.T) {
	inst := newV1Install(t)
	fatal := "provider error: 400 invalid_request_error: messages must alternate between user and assistant"
	wi := inst.newWI(t, "f5: fatal provider error then idle",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "error_turn": fatal},
			{"do": "envelope", "id": "grill", "error_turn": fatal},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		}), nil)
	grace := 2 * time.Second
	d := newV1Daemon(t, inst, optSupervision(supBounds{modelErrorGrace: grace}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	rec := waitForEvent(t, d, wi, "step_dead", 60*time.Second)
	var v verdictEvent
	eventBody(t, rec, &v)
	if v.Cause != "model_error" || !strings.Contains(v.Evidence, "invalid_request_error") {
		t.Fatalf("verdict %+v, want step_dead model_error naming the provider error", v)
	}
	// within the grace (not instantly, not never): the verdict lands
	// between one grace and a generous bound after the error turn
	var errAt time.Time
	for _, j := range fakeJournal(t, d.capsuleDir(wi)) {
		if j["type"] == "error_turn" {
			errAt, _ = time.Parse(time.RFC3339Nano, j["at"].(string))
			break // the first error turn: the one this verdict judged
		}
	}
	vAt, _ := time.Parse(time.RFC3339Nano, rec.At)
	if errAt.IsZero() || vAt.Sub(errAt) < grace-200*time.Millisecond || vAt.Sub(errAt) > grace+8*time.Second {
		t.Fatalf("verdict %v after the error turn, want about the %v grace", vAt.Sub(errAt), grace)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	fails := stepFailures(t, d, wi)
	if len(fails) != 2 || fails[0].Cause != "model_error" || fails[0].Disposition != "retry_same_model" ||
		fails[1].Cause != "model_error" || fails[1].Disposition != "switch_model" || fails[1].From == fails[1].To {
		t.Fatalf("failure records %+v, want model_error/retry_same_model then model_error/switch_model", fails)
	}
	models := spawnModels(t, d.capsuleDir(wi))
	if len(models) < 3 || models[0] != models[1] || models[2] == models[1] {
		t.Fatalf("spawned models %v, want the first model twice (the resume), then the next candidate", models)
	}
}

// TestV2ZeroEventsAfterSpawnHarnessSilent: #114 zero events. The harness
// emits nothing at all after spawn (stuck at startup, stdout held by a
// grandchild): rule 0 judges it within X, the driver retries the SAME
// model, resuming the transcript.
func TestV2ZeroEventsAfterSpawnHarnessSilent(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "#114: zero events after spawn",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "silent": true},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{silence: 2 * time.Second}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	vs := verdicts(t, d, wi)
	if len(vs) != 1 || vs[0].Cause != "harness_silent" || vs[0].SpawnN != 1 {
		t.Fatalf("verdicts %+v, want exactly one harness_silent for dispatch 1", vs)
	}
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Cause != "harness_silent" || fails[0].Disposition != "retry_same_model" || fails[0].Count != 1 || fails[0].Next != "resume" {
		t.Fatalf("failure records %+v, want one harness_silent/retry_same_model/1/resume", fails)
	}
	models := spawnModels(t, d.capsuleDir(wi))
	if len(models) < 2 || models[0] != models[1] {
		t.Fatalf("a silent harness is retried on the SAME model: %v", models)
	}
	spawns := spawnJournal(t, d.capsuleDir(wi))
	if len(spawns) < 2 || spawns[1]["resume"] != true {
		t.Fatalf("the first retry must resume the transcript: %+v", spawns)
	}
}

// TestV2LongToolWithinYSurvives: #83. A long build/edit is live work: the
// harness is record-quiet for far longer than the silence window while the
// tool runs, and the step must NOT be judged dead — rule 3 only matters
// past Y.
func TestV2LongToolWithinYSurvives(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "#83: a long tool within Y survives",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true,
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

// TestV2ToolHungBeyondYRelaxedRetry: a tool beyond Y is tool_hung — same-
// model retry with Y relaxed x2, persisted in execution.json. The retry's
// tool (longer than the old Y, shorter than the relaxed one) survives.
func TestV2ToolHungBeyondYRelaxedRetry(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "#83: tool beyond Y is tool_hung, retry relaxes Y",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "tool_hang": true, "tool_name": "bash"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true,
				"tool_ms": 3000, "tool_name": "bash"},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{silence: 30 * time.Second, tool: 2 * time.Second}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	vs := verdicts(t, d, wi)
	if len(vs) != 1 || vs[0].Cause != "tool_hung" || !strings.Contains(vs[0].Evidence, "bash") {
		t.Fatalf("verdicts %+v, want exactly one tool_hung naming the tool", vs)
	}
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Cause != "tool_hung" || fails[0].Disposition != "retry_same_model" ||
		fails[0].RelaxedTool != 4000 {
		t.Fatalf("failure records %+v, want tool_hung/retry_same_model with Y relaxed to 4000ms", fails)
	}
	x := readExecution(t, d.capsuleDir(wi))
	sup := x.Steps["grill"].Supervision
	if sup == nil || sup["relaxed_tool_ms"] != float64(4000) {
		t.Fatalf("the relaxed Y must persist in execution.json: %+v", sup)
	}
	models := spawnModels(t, d.capsuleDir(wi))
	if len(models) < 2 || models[0] != models[1] {
		t.Fatalf("tool_hung retries the SAME model: %v", models)
	}
}

// TestV2StepTimeoutTrickleWedgeSwitchesCandidate: #117/#124. The step
// trickles one model delta every 100ms forever — every silence rule stays
// satisfied — and only the CUMULATIVE bound T can end it. The time went to
// the model (no tool ran): step_timeout_slow_model, a model-class death.
func TestV2StepTimeoutTrickleWedgeSwitchesCandidate(t *testing.T) {
	inst := newV1Install(t)
	inst.stripWallBudgets = true
	wi := inst.newWI(t, "#117: trickle progress past T",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true,
				"open_turn": true, "noise": "progress", "noise_ms": 100, "ms": 600000},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{
		silence: 30 * time.Second, tool: 30 * time.Second, stepTimeout: 4 * time.Second,
	}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	rec := waitForEvent(t, d, wi, "step_dead", 60*time.Second)
	var v verdictEvent
	eventBody(t, rec, &v)
	if v.Cause != "step_timeout_slow_model" {
		t.Fatalf("verdict %+v, want step_timeout_slow_model", v)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Disposition != "switch_model" || fails[0].From == fails[0].To {
		t.Fatalf("failure records %+v, want one switch_model", fails)
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
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "tool_ms": 600000, "tool_name": "bash"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "tool_ms": 600000, "tool_name": "bash"},
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
	if x.WorkflowStatus != "blocked" || !strings.Contains(x.BlockedReason, "second step timeout") {
		t.Fatalf("execution.json: status %q reason %q", x.WorkflowStatus, x.BlockedReason)
	}
	sup := x.Steps["grill"].Supervision
	// the blocking second timeout doubles T again (6s -> 12s) before it
	// parks, so a human continue runs under a fresh window
	if sup == nil || sup["relaxed_wall_ms"] != float64(12000) {
		t.Fatalf("the relaxed T must persist (doubled again by the blocking timeout): %+v", sup)
	}
	// T accumulates across dispatches: the second dispatch was judged
	// against the runtime the first already spent (it was killed well
	// before a fresh 6s would have elapsed).
	if wall, _ := x.Steps["grill"].BudgetConsumed["wall_ms"].(float64); wall < 5000 || wall > 15000 {
		t.Fatalf("cumulative step wall %vms, want ~6-7s (3s + the remainder of the relaxed 6s)", wall)
	}
	if n := countOf(d.eventKinds(t, wi), "blocked"); n != 1 {
		t.Fatalf("blocked event count %d, want 1: %v", n, d.eventKinds(t, wi))
	}
}

// TestV2ModelPinFailureAdvancesCandidate: F4. A pinned model the harness
// cannot resolve makes it exit non-zero at startup with the reason on
// stderr — an exit WITHOUT a result. The driver reads the model-class
// text and advances the candidate chain (the old same-model retry kept
// re-pinning the dead model forever).
func TestV2ModelPinFailureAdvancesCandidate(t *testing.T) {
	inst := newV1Install(t)
	// grill and fix run healthy on a project-default candidate; verification
	// keeps its own bundle step list, whose head this harness cannot resolve,
	// so ordinal 3 dies at birth and its action is never reached.
	actions := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"}, // spawn 3 dies at birth: never reached
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	}
	wi := inst.newWI(t, "f4: model pin failure exit", actions, nil)
	writePlanExtra(t, inst, wi, actions, nil, map[string]any{"bad_models": []string{"example-gateway/model-a"}})
	d := newV1Daemon(t, inst)
	// The project default keeps grill/fix/deliver off the bundle's step-list
	// head; verification has its own bundle list and ignores the default, so
	// only verification's head is the one bad_models singles out.
	w079Yaml(t, d.workspace, "steps:\n  e2e:\n    default: { models: [pi:example-claude/claude-opus-5, pi:example-openai/gpt-x] }\n")
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	fails := stepFailures(t, d, wi)
	if len(fails) != 1 || fails[0].Step != "verification" || fails[0].Cause != "exit_model_class" || fails[0].Disposition != "switch_model" ||
		fails[0].From != "pi:example-gateway/model-a" || fails[0].To != "pi:example-claude/claude-opus-5" || fails[0].Count != 0 {
		t.Fatalf("failure records %+v, want exit_model_class/switch_model example-gateway/model-a -> example-claude/claude-opus-5 on verification", fails)
	}
	if vs := verdicts(t, d, wi); len(vs) != 0 {
		t.Fatalf("an exit is the driver's own judgement, no supervisor verdict: %+v", vs)
	}
	x := readExecution(t, d.capsuleDir(wi))
	var sawClass bool
	for _, a := range x.Steps["verification"].Attempts {
		if a["failure_cause"] == "exit_model_class" {
			sawClass = true
		}
	}
	if !sawClass {
		t.Fatalf("the attempt must carry the cause sub-class: %+v", x.Steps["verification"].Attempts)
	}
	if x.Steps["verification"].model() != "example-claude/claude-opus-5" {
		t.Fatalf("verification finished on model %q, want the advanced candidate", x.Steps["verification"].model())
	}
}

// TestV2DaemonRestartNotCountedSameModel: a daemon restart mid-dispatch
// re-dispatches the step on the SAME model, counts NO failure and never
// advances the candidate pointer (a restart is not a model-class cause).
func TestV2DaemonRestartNotCountedSameModel(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "restart: not counted, same model",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true, "ms": 60000},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
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
	sup := x.Steps["grill"].Supervision
	if sup != nil {
		if c, _ := sup["failure_count"].(float64); c != 0 {
			t.Fatalf("a daemon restart was counted as a failure: %+v", sup)
		}
	}
	for _, a := range x.Steps["grill"].Attempts {
		if fc, _ := a["failure_cause"].(string); isModelClassCause(fc) {
			t.Fatalf("a daemon restart advanced the candidate pointer: %+v", a)
		}
	}
	if fails := stepFailures(t, d2, wi); len(fails) != 0 {
		t.Fatalf("a daemon restart produced step_failed records: %+v", fails)
	}
}

// TestV2DriverCrashLoopBoundedThenBlocked: #97. A step that keeps dying
// is retried only a bounded number of times: its failure counter parks
// the work item blocked instead of looping forever, and the driver's
// stop on that park is no death (no needs_recovery, no re-derivation).
// The death here: grill completes but leaves a DIRECTORY at its
// handoff convention path, so the next inherit step's handoff read fails
// with a non-absence I/O error (a missing handoff file is merely
// omitted; an unreadable one is an infrastructure inconsistency).
func TestV2DriverCrashLoopBoundedThenBlocked(t *testing.T) {
	inst := newV1Install(t)
	bad := map[string]any{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": false,
		"files": map[string]string{"artifacts/handoff-grill.md/not-a-file": "x"}}
	var actions []map[string]any
	for i := 0; i < 24; i++ {
		actions = append(actions, bad)
	}
	wi := inst.newWI(t, "#97: crash loop is bounded", actions, nil)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "blocked", 180*time.Second)
	kinds := d.eventKinds(t, wi)
	fails := stepFailures(t, d, wi)
	if n := len(fails); n == 0 || n > 4 || fails[n-1].Disposition != "blocked" {
		t.Fatalf("step_failed records %+v, want a small bounded number, the last one parking the work item blocked", fails)
	}
	if n := countOf(kinds, "blocked"); n != 1 {
		t.Fatalf("blocked events %d, want 1: %v", n, kinds)
	}
	if n := countOf(kinds, "needs_recovery") + countOf(kinds, "wi_agent_rederived"); n != 0 {
		t.Fatalf("recovery fired %d times, want 0 (the failure counter bounded the loop; the blocked exit is no death): %v", n, kinds)
	}
}

// TestV2TwoRulesAtOnceOneVerdictOneFailure: with the silence bound and T
// both tiny, a silent harness trips rule 0 AND rule 4 at once. The lower
// number wins, exactly ONE verdict is sent, the driver kills the process
// and records exactly ONE failure — the exit that follows the kill is not
// counted a second time.
func TestV2TwoRulesAtOnceOneVerdictOneFailure(t *testing.T) {
	inst := newV1Install(t)
	inst.stripWallBudgets = true
	wi := inst.newWI(t, "two rules fire at once",
		plan([]map[string]any{
			{"do": "envelope", "id": "grill", "silent": true},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		}), nil)
	d := newV1Daemon(t, inst, optSupervision(supBounds{
		silence: 2 * time.Second, stepTimeout: 2 * time.Second, tool: 30 * time.Second,
	}))
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	vs := verdicts(t, d, wi)
	if len(vs) != 1 || vs[0].Cause != "harness_silent" {
		t.Fatalf("verdicts %+v, want exactly one harness_silent (rule 0 outranks rule 4)", vs)
	}
	if fails := stepFailures(t, d, wi); len(fails) != 1 {
		t.Fatalf("failure records %+v, want exactly one for the dispatch", fails)
	}
	x := readExecution(t, d.capsuleDir(wi))
	infra := 0
	for _, a := range x.Steps["grill"].Attempts {
		if a["cause"] == "infra_failure" {
			infra++
		}
	}
	if infra != 1 {
		t.Fatalf("grill archived %d infra_failure attempts, want 1: %+v", infra, x.Steps["grill"].Attempts)
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
			{"do": "envelope", "id": "grill", "end_turn": true},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
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
