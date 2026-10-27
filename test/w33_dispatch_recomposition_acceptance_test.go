package test

// w33_dispatch_recomposition_acceptance_test.go: —
// dispatch-time candidate recomposition + ended_without_result (EWR)
// rotation, authored against the SPEC (the capsule's artifacts/spec.md,
// "## Acceptance criteria" and "#### Scenario" blocks) BEFORE the
// implementation lands. Every bar below is RED on the pre-implementation
// tree — an assertion-level red (a wrong dispatch model, a missing
// candidate_switched event, a missing DAEMON-414 block reason), never a
// build red: this file compiles, vets and gofmts against the tree as it
// stands today, and every surface it drives is the project's real e2e
// one — the real pf/aihub/fakepi binaries, real daemon processes, the
// workspace .polyforge.yaml, the capsule's execution.json / spawn
// journal / fakepi journal, the daemon's events file and its persisted
// runtime-state.json. No implementation internal is read.
//
// Scenario map (artifacts/e2e-scenarios.md carries the GIVEN/WHEN/THEN
// forms, the criteria each scenario pins, and the baseline evidence):
//
//   - TestW33ConfigEditGovernsNextDispatch: AC1 (edit governs the next
//     dispatch), AC4 (removed → new list head; the in-flight dispatch is
//     never killed), AC12 (candidate_switched cause config_changed),
//     AC3 (no claim-time snapshot is persisted).
//   - TestW33TwoEwrsSwitchLikeModelError: AC6 (the two-EWR
//     model_error-style switch) + AC9 (the streak survives a SIGKILLed
//     daemon — derived from persisted attempts).
//   - TestW33NeutralAttemptsKeepEwrStreak: AC7 (rate_limited /
//     upstream_transient neither count toward nor break the streak).
//   - TestW33AcceptedResultResetsEwrStreak: AC8's first half (an
//     accepted result resets the streak).
//   - TestW33InvalidYamlBlocksAndResumeContinues: AC5 (the DAEMON-414
//     dispatch block; fix + `pf resume` continues from the checkpoint).
//   - TestW33EwrChainEndExhaustionAndResumeKeepsEndModel: AC10 (chain
//     end keeps exhaustion semantics with an actionable block reason)
//     + AC8's second half (blocked→resume does not restore the bad
//     model).
//   - TestW33ManualModelsFrozenWithEwrRotation: AC11 (manual --models
//     lists are not recomposed; EWR rotation fires within them).
//   - TestW33SummarizeLaneReadsLive: AC2's summarize half (the lane
//     reads its models live, not from a claim-time snapshot).
//   - TestW33NoDocKeepsClaimTimeFreeze: AC13 (no named doc surface
//     keeps the claim-time freeze claim).
//
// AC2's chain half (both yaml and bundle frontmatter read at dispatch
// through the claim-time precedence) is pinned by
// TestW33ConfigEditGovernsNextDispatch (the yaml leg, live at dispatch)
// plus the existing w079/s12 precedence suites; AC9's kill -9 chaos
// regression half is the existing v5 chaos matrix, not re-tested here.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// ---------------------------------------------------------------------------
// Shared fixtures (w33): one daemon identity a restart can bring back,
// an atomic workspace-yaml writer, and the observation faces.

// w33Rig is one workspace + daemon identity: the config, socket and data
// dir pieces a mid-run daemon restart (the SIGKILL chaos shape) needs to
// bring the SAME daemon back.
type w33Rig struct {
	inst *v1Install
	ws   string
	cfg  string
	sock string
	data string
}

// w33RigAt builds the rig over one workspace mapped to project e2e.
func w33RigAt(t *testing.T, inst *v1Install, ws string) *w33Rig {
	t.Helper()
	return &w33Rig{
		inst: inst,
		ws:   ws,
		cfg: w113ConfigFile(t, filepath.Join(t.TempDir(), "cli.toml"),
			inst.url, inst.adminKey, "", []w113WS{{key: ws, projects: []string{"e2e"}}}),
		sock: filepath.Join(shortTmp(t, "w33sock"), "d.sock"),
		data: t.TempDir(),
	}
}

// daemon starts one daemon of the rig (its extra args ride the daemon's
// own flag face — --model-error-grace shortens the supervisor's rule-1
// grace so EWR verdicts land in seconds instead of the 1m default).
func (r *w33Rig) daemon(t *testing.T, extra ...string) *w113Daemon {
	t.Helper()
	opts := []w113DOpt{w113DOptConfig(r.cfg), w113DOptSock(r.sock), w113DOptDataDir(r.data)}
	if len(extra) > 0 {
		opts = append(opts, w113DOptArgs(extra...))
	}
	d := w113NewDaemon(t, r.inst, opts...)
	d.start()
	return d
}

// w33StepYaml builds a workspace yaml pinning one candidate list on every
// autopilot step. The yaml STEP level outranks the bundle's own step-level
// model_candidates (the shipped autopilot pins its verification step), so
// the scenarios — not the bundle — govern every dispatch's list.
func w33StepYaml(list string) string {
	return "steps:\n  e2e:\n" +
		"    grill: { models: [" + list + "] }\n" +
		"    fix: { models: [" + list + "] }\n" +
		"    verification: { models: [" + list + "] }\n"
}

// w33Yaml writes the workspace .polyforge.yaml atomically (tmp+rename):
// with composition live at every dispatch, a concurrent read must never
// see a torn half-edited file.
func w33Yaml(t *testing.T, ws, body string) {
	t.Helper()
	p := filepath.Join(ws, ".polyforge.yaml")
	tmp := p + ".w33tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
}

// w33Event is one daemon event of a work item: its kind and decoded body.
type w33Event struct {
	kind string
	body map[string]any
}

// w33Events reads one work item's events (the daemon's events file), in
// order — the observation face for candidate_switched and friends.
func w33Events(t *testing.T, dataDir, wi string) []w33Event {
	t.Helper()
	var out []w33Event
	for _, line := range strings.Split(eventsRaw(t, dataDir), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e protocol.EventEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.WI != wi {
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(e.Body, &body)
		out = append(out, w33Event{kind: e.Kind, body: body})
	}
	return out
}

// w33EventsOf filters a work item's events by kind.
func w33EventsOf(t *testing.T, dataDir, wi, kind string) []w33Event {
	t.Helper()
	var out []w33Event
	for _, e := range w33Events(t, dataDir, wi) {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// w33WaitEvent polls until one event of the kind lands, answering its
// body.
func w33WaitEvent(t *testing.T, dataDir, wi, kind string, bound time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if evs := w33EventsOf(t, dataDir, wi, kind); len(evs) > 0 {
			return evs[0].body
		}
		if time.Now().After(deadline) {
			var kinds []string
			for _, e := range w33Events(t, dataDir, wi) {
				kinds = append(kinds, e.kind)
			}
			t.Fatalf("no %s event ever landed for %s within %v (events so far: %v)", kind, wi, bound, kinds)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// w33Models answers the models the fake harness was dispatched under,
// in spawn order (the fakepi journal's per-spawn --model pin) — the
// per-dispatch observation face of the candidate composition.
func w33Models(t *testing.T, capDir string) []string {
	t.Helper()
	var out []string
	for _, e := range w159Spawns(t, capDir, "fakepi.jsonl") {
		m, _ := e["model"].(string)
		out = append(out, m)
	}
	return out
}

// w33WaitModels polls until at least n spawns are journaled, answering
// the models in order.
func w33WaitModels(t *testing.T, capDir string, n int, bound time.Duration) []string {
	t.Helper()
	waitUntil(t, bound, fmt.Sprintf("fewer than %d spawns were ever journaled", n), func() bool {
		return len(w159Spawns(t, capDir, "fakepi.jsonl")) >= n
	})
	return w33Models(t, capDir)
}

// w33Causes answers one step's archived attempt failure causes, in
// order (the execution.json attempt ledger).
func w33Causes(t *testing.T, capDir, step string) []string {
	t.Helper()
	st := readExecution(t, capDir).Steps[step]
	if st == nil {
		t.Fatalf("no step %q in execution.json", step)
	}
	var out []string
	for _, a := range st.Attempts {
		if c, _ := a["failure_cause"].(string); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// w33WantModels pins the full per-dispatch model sequence.
func w33WantModels(t *testing.T, capDir string, want []string) {
	t.Helper()
	got := w33Models(t, capDir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("dispatch models %v, want %v", got, want)
	}
}

// w33EwrSwitchEvents answers the candidate_switched events whose cause
// is ended_without_result — the EWR rotation's observable, however many
// first-dispatch or config-driven switch events a daemon also emits.
func w33EwrSwitchEvents(t *testing.T, dataDir, wi string) []w33Event {
	t.Helper()
	var out []w33Event
	for _, e := range w33EventsOf(t, dataDir, wi, "candidate_switched") {
		if e.body["cause"] == "ended_without_result" {
			out = append(out, e)
		}
	}
	return out
}

// w33AssertNoSnapshotKeys pins AC3's observable face: the daemon's
// persisted bookkeeping (data-dir runtime-state.json) carries no
// claim-time candidate or summarize snapshot — with composition live at
// every dispatch there is nothing to snapshot.
func w33AssertNoSnapshotKeys(t *testing.T, dataDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dataDir, "runtime-state.json"))
	if err != nil {
		t.Fatalf("read runtime-state.json: %v", err)
	}
	for _, key := range []string{"step_candidates", "summarize_models", "summarize_language", "summarize_interval_sec"} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("the claim-time snapshot key %q is still persisted in runtime-state.json — dispatches must derive from the live composition, never from a snapshot", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 1 — a config edit governs the NEXT dispatch of a running
// work item (AC1, AC4, AC12, AC3).
// ---------------------------------------------------------------------------

// TestW33ConfigEditGovernsNextDispatch: GIVEN a work item mid-flight
// whose candidate list was composed as [w33-a, w33-b] at claim time,
// WHEN the owner edits `.polyforge.yaml` mid-dispatch to remove w33-a
// (the emergency removal; the list becomes [w33-c, w33-b]), THEN the
// in-flight dispatch is never killed (it survives the edit and dies by
// its own scripted crash), the next dispatch recomposes from the current
// yaml and takes the new list head w33-c, the switch is observable as
// the existing candidate_switched event with cause config_changed, and
// the daemon's persisted bookkeeping carries no claim-time snapshot.
func TestW33ConfigEditGovernsNextDispatch(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	const w33AB = "pi:w33-a, pi:w33-b"
	w33Yaml(t, ws, w33StepYaml(w33AB)+"summarize:\n  models: [pi:w33-s1]\n")
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t)
	wi := w113NewWI(t, inst, "e2e", "w33: config edit governs the next dispatch", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		// fix's first dispatch: 12 s in flight on w33-a, then a bare
		// crash (an unclassified exit — a counted same-model retry).
		{"do": "envelope", "id": "fix", "ms": 12000, "crash": "before"},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)

	// The emergency edit lands while fix's first dispatch is mid-flight
	// on w33-a.
	models := w33WaitModels(t, capDir, 2, 60*time.Second)
	if models[1] != "w33-a" {
		t.Fatalf("fix's first dispatch ran on %q, want w33-a (the pre-edit list head)", models[1])
	}
	w33Yaml(t, ws, w33StepYaml("pi:w33-c, pi:w33-b")+"summarize:\n  models: [pi:w33-s1]\n")

	// No claim-time snapshot is persisted (AC3): the daemon's bookkeeping
	// must carry no step-candidate or summarize snapshot keys.
	w33AssertNoSnapshotKeys(t, d.dataDir)

	w113WaitCompleted(t, capDir, 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	// The in-flight dispatch was never killed by the edit: it ran its
	// full scripted delay and was archived by its own crash.
	if causes := w33Causes(t, capDir, "fix"); causes[0] != "exit_other" {
		t.Fatalf("the in-flight dispatch's archive is %v, want one exit_other attempt (it must survive the config edit)", causes)
	}
	// The post-edit retry and the verification dispatch recompose from
	// the current yaml: the new list head w33-c.
	w33WantModels(t, capDir, []string{"w33-a", "w33-a", "w33-c", "w33-c"})

	// The config-driven switch is observable (AC12): the existing
	// candidate_switched event, cause config_changed, no new event kind.
	var configDriven []w33Event
	for _, e := range w33EventsOf(t, d.dataDir, wi, "candidate_switched") {
		if e.body["cause"] == "config_changed" {
			configDriven = append(configDriven, e)
		}
	}
	if len(configDriven) != 1 {
		t.Fatalf("candidate_switched(config_changed) fired %d times, want exactly 1 (the emergency removal)", len(configDriven))
	}
	if configDriven[0].body["to"] != "pi:w33-c" {
		t.Fatalf("the config-driven switch event names to=%v, want pi:w33-c (the new list head)", configDriven[0].body["to"])
	}
}

// ---------------------------------------------------------------------------
// Scenario 2 — two EWRs switch like a model_error, and the streak
// survives a SIGKILLed daemon (AC6, AC9).
// ---------------------------------------------------------------------------

// TestW33TwoEwrsSwitchLikeModelError: GIVEN a work item dispatched to
// w33-a of [w33-a, w33-b] with one ended_without_result attempt recorded,
// WHEN the daemon is SIGKILLed mid-retry and its restart is followed by
// a second EWR on w33-a, THEN recovery derives pointer and streak purely
// from the persisted attempts (the interrupted attempt neither counts
// nor breaks), the second EWR switches to w33-b exactly as a model_error
// does — the failure counter resets, the next dispatch is fresh — and
// the switch fires the existing candidate_switched event with cause
// ended_without_result.
func TestW33TwoEwrsSwitchLikeModelError(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-a, pi:w33-b"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t, "--model-error-grace", "2s")
	wi := w113NewWI(t, inst, "e2e", "w33: two EWRs switch like a model_error", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #1 on w33-a
		{"do": "envelope", "id": "fix", "park": true},     // alive while the daemon is SIGKILLed
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #2 — the streak reaches the threshold
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)

	// EWR #1 is recorded (cause ended_without_result), and the retry is
	// in flight when the daemon dies hard.
	if body := w33WaitEvent(t, d.dataDir, wi, "step_failed", 90*time.Second); body["cause"] != "ended_without_result" {
		t.Fatalf("the first failure is %v, want ended_without_result", body["cause"])
	}
	w33WaitModels(t, capDir, 3, 60*time.Second)
	d.crash()
	d = rig.daemon(t, "--model-error-grace", "2s")

	w113WaitCompleted(t, capDir, 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	// The streak survived the restart: EWR #1, the neutral
	// daemon_restart interruption, EWR #2 — the switch fires.
	if causes := w33Causes(t, capDir, "fix"); strings.Join(causes, ",") != "ended_without_result,daemon_restart,ended_without_result" {
		t.Fatalf("fix's attempt causes %v, want the EWR / neutral-interruption / EWR ledger", causes)
	}
	// fix's post-switch dispatches run w33-b (verification is its own
	// step: its pointer starts at its list head w33-a).
	w33WantModels(t, capDir, []string{"w33-a", "w33-a", "w33-a", "w33-a", "w33-b", "w33-a"})

	// The model_error-style switch semantics: the counter resets and the
	// next dispatch is fresh.
	failed := w33EventsOf(t, d.dataDir, wi, "step_failed")
	if len(failed) < 2 {
		t.Fatalf("only %d step_failed events, want the two EWR records", len(failed))
	}
	last := failed[len(failed)-1].body
	if last["cause"] != "ended_without_result" || last["disposition"] != "switch_model" {
		t.Fatalf("the second EWR's decision is %+v, want cause ended_without_result / switch_model", last)
	}
	if last["failure_count"] != float64(0) || last["next_dispatch"] != "fresh" {
		t.Fatalf("the switch did not reset the failure counter or dispatch fresh: %+v", last)
	}
	// The switch is observable: candidate_switched, cause
	// ended_without_result, to the next candidate.
	if switches := w33EwrSwitchEvents(t, d.dataDir, wi); len(switches) != 1 || switches[0].body["to"] != "pi:w33-b" {
		t.Fatalf("the EWR rotation's switch events: %+v, want exactly one ended_without_result to pi:w33-b", switches)
	}
}

// ---------------------------------------------------------------------------
// Scenario 3 — neutral attempts neither count toward nor break the EWR
// streak (AC7).
// ---------------------------------------------------------------------------

// TestW33NeutralAttemptsKeepEwrStreak: GIVEN one EWR on w33-a of
// [w33-a, w33-b], WHEN a rate_limited and an upstream_transient attempt
// interleave (the pi-web- shape), and a second EWR on w33-a
// follows, THEN the throttles and transients neither incremented nor
// reset the streak — the switch fires exactly at the second EWR.
func TestW33NeutralAttemptsKeepEwrStreak(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-a, pi:w33-b"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t, "--model-error-grace", "2s")
	wi := w113NewWI(t, inst, "e2e", "w33: neutral attempts keep the EWR streak", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "end_turn": true},
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": "HTTP 429: too many requests (rate limit exceeded)"},
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": "the upstream connection was reset mid-stream"},
		{"do": "envelope", "id": "fix", "end_turn": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	w113WaitCompleted(t, capDir, 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	// The interleave is on the ledger exactly as scripted (a guard: the
	// throttle and the transient are classified, not counted failures).
	if causes := w33Causes(t, capDir, "fix"); strings.Join(causes, ",") !=
		"ended_without_result,rate_limited,upstream_transient,ended_without_result" {
		t.Fatalf("fix's attempt causes %v, want the EWR / rate_limited / upstream_transient / EWR interleave", causes)
	}
	// The streak skipped both neutrals: the switch fires exactly at the
	// second EWR — never earlier, never not at all. (verification is
	// its own step: it starts from its list head w33-a.)
	w33WantModels(t, capDir, []string{"w33-a", "w33-a", "w33-a", "w33-a", "w33-a", "w33-b", "w33-a"})
	if switches := w33EwrSwitchEvents(t, d.dataDir, wi); len(switches) != 1 || switches[0].body["to"] != "pi:w33-b" {
		t.Fatalf("the EWR rotation's switch events: %+v, want exactly one ended_without_result to pi:w33-b", switches)
	}
}

// ---------------------------------------------------------------------------
// Scenario 4 — an accepted result resets the EWR streak (AC8, first
// half).
// ---------------------------------------------------------------------------

// TestW33AcceptedResultResetsEwrStreak: GIVEN one EWR on w33-a of
// [w33-a, w33-b], WHEN a step result is accepted on w33-a (a needs_input
// round the owner answers), and two further EWRs follow on the
// continuation, THEN the accepted result reset the streak — the switch
// fires only at the SECOND post-answer EWR, never at the first.
func TestW33AcceptedResultResetsEwrStreak(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-a, pi:w33-b"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t, "--model-error-grace", "2s")
	const qid = "q_w33"
	wi := w113NewWI(t, inst, "e2e", "w33: an accepted result resets the EWR streak", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #1 on w33-a
		{"do": "envelope", "id": "fix", "outcome": "needs_input", "question_id": qid, "question": "lean or compatible?"},
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR — the streak restarts: no switch yet
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR — streak 2: the switch fires
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitExecution(t, capDir, 120*time.Second, "fix never asked its question", func(x *executionView) bool {
		st := x.Steps["fix"]
		return st != nil && st.PendingQuestion != nil
	})
	if out := d.pfWant(inst, -1, "answer", wi, "--text", "lean", "--question", qid, "--by", "usr_root"); !strings.Contains(out, "delivered") {
		t.Fatalf("pf answer: %q", out)
	}
	// Without the rotation the third EWR reaches the failure bound and
	// blocks — a bounded, diagnosable red, not a silent timeout.
	waitUntil(t, 150*time.Second, "the work item neither completed nor blocked", func() bool {
		x, err := readExecutionFile(capDir)
		return err == nil && (x.WorkflowStatus == "completed" || x.WorkflowStatus == "blocked")
	})
	if x := readExecution(t, capDir); x.WorkflowStatus != "completed" {
		t.Fatalf("the work item blocked instead of completing: %q — with no EWR rotation the third EWR hit the failure bound", x.BlockedReason)
	}

	// The switch fires only at the second post-answer EWR: the first
	// post-answer EWR still dispatches w33-a (the accepted result reset
	// the streak — had it been neutral, the switch would have fired one
	// attempt earlier). (verification is its own step: head w33-a.)
	w33WantModels(t, capDir, []string{"w33-a", "w33-a", "w33-a", "w33-a", "w33-a", "w33-b", "w33-a"})
	if switches := w33EwrSwitchEvents(t, d.dataDir, wi); len(switches) != 1 || switches[0].body["to"] != "pi:w33-b" {
		t.Fatalf("the EWR rotation's switch events: %+v, want exactly one ended_without_result to pi:w33-b", switches)
	}
}

// ---------------------------------------------------------------------------
// Scenario 5 — invalid yaml at dispatch time blocks; fix + resume
// continues from the checkpoint (AC5).
// ---------------------------------------------------------------------------

// TestW33InvalidYamlBlocksAndResumeContinues: GIVEN a work item mid-run
// whose `.polyforge.yaml` is syntactically broken by a half-finished
// edit, WHEN the daemon attempts the next dispatch, THEN the work item
// blocks with the same DAEMON-414-class error an invalid yaml raises at
// claim time, and WHEN the owner fixes the yaml and runs `pf resume`,
// the dispatch continues from the preserved checkpoint — every step ran
// exactly once, no progress lost.
func TestW33InvalidYamlBlocksAndResumeContinues(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-a"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t)
	wi := w113NewWI(t, inst, "e2e", "w33: invalid yaml blocks the dispatch", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 12000},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)

	// The half-finished edit lands while fix's dispatch is in flight;
	// the NEXT dispatch (verification) must meet it.
	w33WaitModels(t, capDir, 2, 60*time.Second)
	w33Yaml(t, ws, "steps: [broken — a half-finished edit\n")
	waitUntil(t, 120*time.Second, "the half-edited yaml neither blocked nor completed the work item", func() bool {
		x, err := readExecutionFile(capDir)
		return err == nil && (x.WorkflowStatus == "blocked" || x.WorkflowStatus == "completed")
	})
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Fatalf("the half-edited .polyforge.yaml never blocked the dispatch (workflow %q): the claim-time list carried the work item past the invalid config", x.WorkflowStatus)
	}
	if !strings.Contains(x.BlockedReason, "DAEMON-414") {
		t.Fatalf("the dispatch block reason does not name the DAEMON-414-class config error: %q", x.BlockedReason)
	}

	// The owner fixes the yaml and resumes: the dispatch continues from
	// the preserved checkpoint — three dispatches total, one per step.
	w33Yaml(t, ws, w33StepYaml("pi:w33-a"))
	// the hub's blocked close lands shortly after execution.json shows
	// blocked; `pf resume` reads the hub status first
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	d.pfWant(inst, -1, "resume", wi)
	w113WaitCompleted(t, capDir, 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	w33WantModels(t, capDir, []string{"w33-a", "w33-a", "w33-a"})
}

// ---------------------------------------------------------------------------
// Scenario 6 — EWR at chain end keeps exhaustion semantics; resume does
// not restore the bad model (AC10, AC8 second half).
// ---------------------------------------------------------------------------

// TestW33EwrChainEndExhaustionAndResumeKeepsEndModel: GIVEN a work item
// whose pointer sits at the last candidate w33-b of [w33-a, w33-b],
// WHEN two EWRs accumulate on w33-b (rotation wanted, no room), THEN the
// EWR is counted as a same-model retry — the existing failure bound
// blocks on the third — AND the block reason names the current model
// and the escape hatch (edit .polyforge.yaml, then `pf resume`), AND
// `pf resume` keeps the end model w33-b: the human loop cannot silently
// restore the earlier candidate.
func TestW33EwrChainEndExhaustionAndResumeKeepsEndModel(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-a, pi:w33-b"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t, "--model-error-grace", "2s")
	wi := w113NewWI(t, inst, "e2e", "w33: chain-end EWR exhaustion blocks actionably", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		// A model-class failure advances to w33-b (existing behavior).
		{"do": "envelope", "id": "fix", "crash": "before", "crash_stderr": w079ProviderErr},
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #1 on w33-b
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #2: rotation wanted, chain end — a counted retry
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #3: the failure bound blocks
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitExecution(t, capDir, 180*time.Second, "the chain-end EWR episode never blocked", func(x *executionView) bool {
		return x.WorkflowStatus == "blocked"
	})

	// The episode's ledger: the model-class switch, then three EWRs on
	// the end model.
	if causes := w33Causes(t, capDir, "fix"); strings.Join(causes, ",") !=
		"exit_model_class,ended_without_result,ended_without_result,ended_without_result" {
		t.Fatalf("fix's attempt causes %v, want the model-class switch then three EWRs on the end model", causes)
	}
	// Rotation at the chain end is exhaustion, not a switch: the
	// existing candidates_exhausted event fires.
	if n := len(w33EventsOf(t, d.dataDir, wi, "candidates_exhausted")); n != 1 {
		t.Fatalf("candidates_exhausted fired %d times, want exactly 1 (the second EWR at the chain end)", n)
	}
	// The block reason is actionable: it names the current model and the
	// edit-yaml-then-resume escape hatch.
	reason := readExecution(t, capDir).BlockedReason
	for _, needle := range []string{"w33-b", ".polyforge.yaml", "resume"} {
		if !strings.Contains(reason, needle) {
			t.Fatalf("the EWR bound block reason %q does not name %q (the current model / the escape hatch)", reason, needle)
		}
	}

	// The owner resumes: the streak is not reset and the pointer stays
	// at the end model — the earlier candidate is never silently
	// restored. (verification is its own step: it starts from its list
	// head w33-a, not from fix's end model.)
	// the hub's blocked close lands shortly after execution.json shows
	// blocked; `pf resume` reads the hub status first
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	d.pfWant(inst, -1, "resume", wi)
	w113WaitCompleted(t, capDir, 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	models := w33Models(t, capDir)
	if len(models) != 7 || models[5] != "w33-b" {
		t.Fatalf("post-resume dispatch models %v, want the blocked→resume continuation of fix on the end model w33-b (7 dispatches)", models)
	}
}

// ---------------------------------------------------------------------------
// Scenario 7 — manual --models lists stay frozen; EWR rotation applies
// within them (AC11).
// ---------------------------------------------------------------------------

// TestW33ManualModelsFrozenWithEwrRotation: GIVEN a work item submitted
// with `pf submit --models pi:w33-m1,pi:w33-m2` whose yaml is edited
// mid-run, WHEN two EWRs accumulate on w33-m1, THEN the dispatches keep
// using the frozen manual list (the yaml edit never reaches them) and
// the rotation fires within it — the next manual candidate w33-m2.
func TestW33ManualModelsFrozenWithEwrRotation(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w33Yaml(t, ws, w33StepYaml("pi:w33-y1"))
	rig := w33RigAt(t, inst, ws)
	d := rig.daemon(t, "--model-error-grace", "2s")
	wi := w113NewWI(t, inst, "e2e", "w33: manual models frozen, EWR rotates within", []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #1 on w33-m1
		{"do": "envelope", "id": "fix", "end_turn": true}, // EWR #2 — rotation within the manual list
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "verification", "outcome": "completed"},
	})
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi, "--models", "pi:w33-m1,pi:w33-m2")
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)

	// The yaml is edited after the submit (the claim composed the manual
	// list): the manual list is the submitter's explicit choice and must
	// never be recomposed.
	w33Yaml(t, ws, w33StepYaml("pi:w33-z9"))
	// Without the rotation the third EWR reaches the failure bound and
	// blocks — a bounded, diagnosable red, not a silent timeout.
	waitUntil(t, 150*time.Second, "the work item neither completed nor blocked", func() bool {
		x, err := readExecutionFile(capDir)
		return err == nil && (x.WorkflowStatus == "completed" || x.WorkflowStatus == "blocked")
	})
	if x := readExecution(t, capDir); x.WorkflowStatus != "completed" {
		t.Fatalf("the work item blocked instead of completing: %q — with no EWR rotation the third EWR hit the failure bound", x.BlockedReason)
	}

	for _, m := range w33Models(t, capDir) {
		if m != "w33-m1" && m != "w33-m2" {
			t.Fatalf("a dispatch ran on %q — the manual list must stay frozen (never the yaml's w33-y1/w33-z9)", m)
		}
	}
	// The EWR rotation fires within the frozen manual list. (verification
	// is its own step: its manual-list pointer starts at the head w33-m1.)
	w33WantModels(t, capDir, []string{"w33-m1", "w33-m1", "w33-m1", "w33-m2", "w33-m1"})
	if switches := w33EwrSwitchEvents(t, d.dataDir, wi); len(switches) != 1 || switches[0].body["to"] != "pi:w33-m2" {
		t.Fatalf("the EWR rotation's switch events: %+v, want exactly one ended_without_result to pi:w33-m2", switches)
	}
}

// ---------------------------------------------------------------------------
// Scenario 8 — the summarize lane reads its config live (AC2).
// ---------------------------------------------------------------------------

// TestW33SummarizeLaneReadsLive: GIVEN a running work item whose
// summarize lane was composed as [w33-s1] at claim time, WHEN the owner
// edits the yaml's summarize models to [w33-s2] and the next summarize
// demand fires (interval passed, transcript grown), THEN the call
// dispatches w33-s2 — the lane config is read live from the same
// composition, never from a claim-time snapshot.
func TestW33SummarizeLaneReadsLive(t *testing.T) {
	inst := newV1Install(t)
	d := newV1Daemon(t, inst)
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:w33-s1]\n")
	d.start()
	wi := w224NewWI(t, inst, "w33: the summarize lane reads live", func(wi string) []map[string]any {
		return []map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "run": []string{w224GrowCmd(70)}, "ms": 90000},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}
	})
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 30*time.Second)
	capDir := d.capsuleDir(wi)
	t.Cleanup(func() { killOrphans(wi) })

	// The first demand answers from w33-s1.
	w224Demand(t, inst, d, wi)
	w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 15*time.Second), "w33-s1", "none")

	// The owner edits the summarize models mid-flight; the next call
	// (the 60 s interval has passed and the transcript has grown) must
	// read the lane config live.
	w224Yaml(t, d.workspace, "summarize:\n  models: [pi:w33-s2]\n")
	w224AwaitRun(t, capDir, "PI_CODING_AGENT_SESSION_DIR", 100*time.Second)
	w224Demand(t, inst, d, wi)
	w224AssertSummaryLine(t, w224AwaitSummary(t, inst, d, wi, 15*time.Second), "w33-s2", "none")
}

// ---------------------------------------------------------------------------
// Scenario 9 — no doc keeps the claim-time freeze (AC13).
// ---------------------------------------------------------------------------

// TestW33NoDocKeepsClaimTimeFreeze: GIVEN the doc surfaces that assert
// the claim-time candidate freeze, WHEN this change ships, THEN none of
// them keeps the "an edit governs the next claim only" claim and the
// .polyforge.yaml semantics are documented at dispatch time. (The
// recover.go / compose.go comment updates ride the code units that
// delete the paths they describe — a doc test does not read source
// comments.)
func TestW33NoDocKeepsClaimTimeFreeze(t *testing.T) {
	root := v1Root(t)
	for _, tc := range []struct {
		path    string
		needles []string
	}{
		{"docs/reference/config.md", []string{"next claim"}},
		{"skills/polyforge/SKILL.md", []string{"next claim"}},
		{"docs/design/polyforge-vnext/reference.md", []string{"next claim", "Composed once per claim"}},
	} {
		raw, err := os.ReadFile(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		for _, needle := range tc.needles {
			if strings.Contains(string(raw), needle) {
				t.Errorf("%s still asserts the claim-time candidate freeze (%q)", tc.path, needle)
			}
		}
	}
	// The replacement semantics are stated: the yaml is read at every
	// dispatch, not once per claim.
	for _, path := range []string{"docs/reference/config.md", "skills/polyforge/SKILL.md"} {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(raw), "every dispatch") {
			t.Errorf("%s does not state the dispatch-time recomposition (\"every dispatch\")", path)
		}
	}
}
