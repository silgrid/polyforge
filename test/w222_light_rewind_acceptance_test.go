package test

// w222_light_rewind_acceptance_test.go: polyforge#222 — the lightweight
// rewind for text-only review findings, end to end through the real
// daemon + driver and the fake harness (test/fakepi: both the fake `pi`
// and the fake `claude`). Authored against artifacts/spec.md BEFORE the
// implementation (e2e-author): every scenario drives the shipped bundles
// (bug-fix on pi, chore with its build step on claude) with scripted
// envelopes and judges only records the spec names — the execution.json
// attempts ledger and rewind stats, the capsule spawn journal (resume vs
// fresh on both harnesses), the fake harness's own ledgers and the
// prompts the step agents received.
//
// Plans are scripted per spawn ordinal (the Nth envelope action fires for
// spawn N), so a light rewind and a fresh one script identically; the
// records tell them apart.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w222TextOnlyFail is a review FAIL verdict whose every blocking finding
// carries text_only: true; extra findings (e.g. a non-blocking one with
// no marker) ride along.
func w222TextOnlyFail(extra ...map[string]any) map[string]any {
	findings := []map[string]any{{"severity": "blocking", "repair_target": "code", "text_only": true,
		"detail": "the doc comment above the changed function still names the old flag"}}
	findings = append(findings, extra...)
	return map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
		"verdict":     "fail",
		"fail_reason": "comment wording only: the doc comment names the old flag",
		"findings":    findings,
	}}
}

func w222Env(id string, handoff bool) map[string]any {
	return map[string]any{"do": "envelope", "id": id, "outcome": "completed", "handoff": handoff}
}

// w222BugFixHead is the bug-fix lane up to (excluding) its first review
// (review runs right after fix, before verification).
func w222BugFixHead() []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true,
			"files": map[string]string{"artifacts/repro.md": "# repro\n\nfake agent reproduction record\n"}},
		w222Env("root-cause", true), w222Env("fix", true),
	}
}

// w222RunPi runs one bug-fix work item on the pi fake until the workflow
// leaves running (or the bound expires) and answers its capsule dir.
func w222RunPi(t *testing.T, goal string, plan []map[string]any) string {
	t.Helper()
	inst, _ := newV1InstallTaskRepo(t)
	wi := newWIWithBundle(t, inst, goal, "bug-fix", plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	capDir := d.capsuleDir(wi)
	w222WaitSettled(t, capDir)
	return capDir
}

// w222WaitSettled waits until the workflow is no longer running.
func w222WaitSettled(t *testing.T, capDir string) {
	t.Helper()
	waitExecution(t, capDir, 180*time.Second, "the workflow never settled", func(x *executionView) bool {
		return x.WorkflowStatus != "" && x.WorkflowStatus != "running"
	})
}

// w222Steps answers the spawn journal's entries of one step, in order.
func w222Steps(t *testing.T, capDir, step string) []map[string]any {
	var out []map[string]any
	for _, e := range w159SpawnJournal(t, capDir) {
		if e["step_id"] == step {
			out = append(out, e)
		}
	}
	return out
}

// w222Order answers the spawn journal's step ids in dispatch order.
func w222Order(t *testing.T, capDir string) []string {
	var out []string
	for _, e := range w159SpawnJournal(t, capDir) {
		s, _ := e["step_id"].(string)
		out = append(out, s)
	}
	return out
}

// w222Causes answers the rewind-related attempt causes archived on one
// step, in order (the ledger's other causes, e.g. accepted, are skipped).
func w222Causes(x *executionView, step string) []string {
	var out []string
	if st := x.Steps[step]; st != nil {
		for _, a := range st.Attempts {
			if c, _ := a["cause"].(string); strings.Contains(c, "rewind") {
				out = append(out, c)
			}
		}
	}
	return out
}

func w222RewindCount(x *executionView) float64 {
	n, _ := x.RewindStats["count"].(float64)
	return n
}

// w222AssertLightLedger checks the ledger side of one light rewind onto
// build: its archived attempt carries gate_fail_rewind_text_only from the
// review gate, verification keeps superseded_by_rewind, one slot.
func w222AssertLightLedger(t *testing.T, x *executionView, build string) {
	t.Helper()
	var light bool
	if st := x.Steps[build]; st != nil {
		for _, a := range st.Attempts {
			if a["cause"] == "gate_fail_rewind_text_only" && a["rewind_gate"] == "review" {
				light = true
			}
		}
	}
	if !light {
		t.Errorf("no %s attempt with cause gate_fail_rewind_text_only and rewind_gate=review — %s attempt causes %v",
			build, build, w222Causes(x, build))
	}
	// review rewinds [fix, review) — verification sits
	// AFTER review now, so a review FAIL supersedes nothing; the fact the
	// old order pinned (verification superseded) belongs to the deliver
	// FAIL range and is pinned by w166's back-to-code round trip.
	if n := w222RewindCount(x); n != 1 {
		t.Errorf("rewind_stats.count = %v, want exactly one slot (rewind_stats=%v)", n, x.RewindStats)
	}
}

// Scenario 1 (pi, bug-fix `fix`): an all-text-only review FAIL — one
// blocking text_only finding plus an unmarked NON-blocking one — takes the
// light path: the fix re-dispatch resumes its previous pi transcript, the
// ledger names the light cause in one slot, and the gate tail (a fresh
// review, then verification) re-runs unchanged ( order).
func TestW222LightRewindResumesPiBuildSession(t *testing.T) {
	plan := append(w222BugFixHead(),
		w222TextOnlyFail(map[string]any{"severity": "non-blocking", "detail": "nit: a log line could be shorter"}),
		w67ChangedRound("fix", "demo-src", 1), // the light round's text fix
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w222: text-only review fail resumes fix (pi)", plan)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	w222AssertLightLedger(t, x, "fix")

	fix := w222Steps(t, capDir, "fix")
	if len(fix) != 2 {
		t.Fatalf("fix dispatches %d, want 2 (build, light re-entry): %v", len(fix), fix)
	}
	if fix[1]["resume"] != true || fix[1]["transcript"] != fix[0]["transcript"] {
		t.Errorf("SYMPTOM: the light re-entry did not resume the previous pi transcript — first %v, re-entry %v", fix[0], fix[1])
	}
	// the fake pi saw --session <previous transcript> on the re-entry
	var resumed bool
	for _, e := range w159Spawns(t, capDir, "fakepi.jsonl") {
		if e["resume"] == true && e["session"] == fix[0]["transcript"] {
			resumed = true
		}
	}
	if !resumed {
		t.Errorf("fake pi never launched with --session %v (resume)", fix[0]["transcript"])
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,review,fix,review,verification" {
		t.Errorf("dispatch order %s: the gate tail must re-run unchanged after the light rewind", got)
	}
	if rv := w222Steps(t, capDir, "review"); len(rv) == 2 && rv[1]["resume"] != false {
		t.Errorf("the review gate's re-entry must stay fresh: %v", rv[1])
	}
}

// Scenario 2 (pi, bug-fix `fix`): the resumed session self-reports a
// structural fix (needs_code_change: true on a completed result) — the
// driver does not archive fix as done: the very next dispatch is a FRESH
// fix, no new rewind slot is charged, and the lane then proceeds.
func TestW222SelfReportedStructuralFixRewindsFreshSameSlot(t *testing.T) {
	plan := append(w222BugFixHead(), w222TextOnlyFail(),
		map[string]any{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
			"output": map[string]any{"needs_code_change": true}},
		w67ChangedRound("fix", "demo-src", 1), // the fresh structural fix
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w222: resumed fix self-reports a structural fix", plan)
	x := readExecution(t, capDir)
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,review,fix,fix,review,verification" {
		t.Errorf("SYMPTOM: dispatch order %s — a needs_code_change result must be followed by a fresh fix, not review", got)
	}
	fix := w222Steps(t, capDir, "fix")
	if len(fix) >= 3 && (fix[1]["resume"] != true || fix[2]["resume"] != false) {
		t.Errorf("fix re-entries: light %v (want resume), self-report fallback %v (want fresh)", fix[1]["resume"], fix[2]["resume"])
	}
	if n := w222RewindCount(x); n != 1 {
		t.Errorf("rewind_stats.count = %v, want 1: the self-report fallback reuses the light rewind's slot", n)
	}
	if c := w222Causes(x, "fix"); len(c) == 0 || c[0] != "gate_fail_rewind_text_only" {
		t.Errorf("fix attempt causes %v, want the light cause first", c)
	}
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
}

// Scenario 3 (pi, bug-fix `fix`): after a light round, the re-run review
// records a blocking mis-marking finding (unmarked) — a NEW gate FAIL: its
// own rewind slot, the fresh path.
func TestW222MisMarkingFailRewindsFreshOwnSlot(t *testing.T) {
	plan := append(w222BugFixHead(), w222TextOnlyFail(),
		w67ChangedRound("fix", "demo-src", 1), // the light round's edit
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "mis-marking: the light round changed an assertion, outside the text-only boundary",
			"findings":    []map[string]any{{"severity": "blocking", "repair_target": "code", "detail": "light round left text-only scope"}},
		}},
		w67ChangedRound("fix", "demo-src", 2), // the fresh round's fix
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	)
	capDir := w222RunPi(t, "w222: mis-marked light round rewinds fresh", plan)
	x := readExecution(t, capDir)
	if c := strings.Join(w222Causes(x, "fix"), ","); c != "gate_fail_rewind_text_only,gate_fail_rewind" {
		t.Errorf("SYMPTOM: fix attempt causes [%s], want the light cause then a fresh gate_fail_rewind", c)
	}
	if n := w222RewindCount(x); n != 2 {
		t.Errorf("rewind_stats.count = %v, want 2: the mis-marking FAIL is a new rewind event with its own slot", n)
	}
	fix := w222Steps(t, capDir, "fix")
	if len(fix) != 3 {
		t.Fatalf("fix dispatches %d, want 3: %v", len(fix), fix)
	}
	if fix[1]["resume"] != true || fix[2]["resume"] != false {
		t.Errorf("fix re-entries: light %v (want resume), after mis-marking %v (want fresh)", fix[1]["resume"], fix[2]["resume"])
	}
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
}

// w222ChoreClaude runs a chore work item whose build step (code) runs on
// claude (review too when reviewOnClaude), answers daemon-side handles.
func w222ChoreClaude(t *testing.T, goal string, reviewOnClaude bool, plan []map[string]any) (string, string) {
	t.Helper()
	inst := newV1Install(t)
	ws := t.TempDir()
	yaml := "steps:\n  e2e:\n    code: { models: [claude:claude-opus-5-5] }\n"
	if reviewOnClaude {
		yaml += "    review: { models: [claude:claude-opus-5-5] }\n"
	}
	w079Yaml(t, ws, yaml)
	d, _, root := w159HarnessRoot(t, inst, ws, "")
	wi := newWIWithBundle(t, inst, goal, "chore", plan)
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	return capDir, root
}

// w222ChorePlan scripts the chore lane in the order
// (code → review → verification): the review FAIL right after code, the
// light code re-entry, the fresh review re-run, then verification. The
// hold lands on the review FAIL (spawn 3) — the window the session-gone
// scenario deletes the native session in.
func w222ChorePlan(reviewMS int) []map[string]any {
	fail := w222TextOnlyFail()
	if reviewMS > 0 {
		fail["ms"] = reviewMS
	}
	return []map[string]any{
		w222Env("plan", true), w222Env("code", true), fail,
		w222Env("code", true),
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		w222Env("verification", false),
	}
}

func w222Prompt(t *testing.T, capDir string, spawnN int) string {
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakeclaude-prompt-"+strconv.Itoa(spawnN)+".txt"))
	if err != nil {
		t.Errorf("claude prompt of spawn %d: %v", spawnN, err)
	}
	return string(raw)
}

// Scenario 4 (claude, chore `code`): the light rewind resumes the SAME
// claude native session (--resume <uuid>); the review prompt offers the
// text_only marker, the resumed build prompt carries the needs_code_change
// self-report instruction, and the fresh review re-run is pointed at the
// light-rewind cause it must judge for mis-marking.
func TestW222LightRewindResumesClaudeBuildSession(t *testing.T) {
	capDir, _ := w222ChoreClaude(t, "w222: text-only review fail resumes code (claude)", true, w222ChorePlan(0))
	w222WaitSettled(t, capDir)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	w222AssertLightLedger(t, x, "code")
	code := w222Steps(t, capDir, "code")
	if len(code) != 2 {
		t.Fatalf("code dispatches %d, want 2: %v", len(code), code)
	}
	first, _ := code[0]["harness_session"].(string)
	if code[1]["resume"] != true || code[1]["harness_session"] != first || first == "" {
		t.Errorf("SYMPTOM: the light re-entry did not resume claude session %q — re-entry %v", first, code[1])
	}
	var resumedArgv bool
	for _, e := range w159Spawns(t, capDir, "fakeclaude.jsonl") {
		if e["resume"] == true && e["session"] == first && strings.Contains(w159Argv(e), "--resume "+first) {
			resumedArgv = true
		}
	}
	if !resumedArgv {
		t.Errorf("fake claude never launched with --resume %s", first)
	}
	if p := w222Prompt(t, capDir, 3); !strings.Contains(p, "text_only") {
		t.Errorf("the review prompt (spawn 3) never offers the text_only finding marker")
	}
	if p := w222Prompt(t, capDir, 4); !strings.Contains(p, "needs_code_change") {
		t.Errorf("the resumed build prompt (spawn 4) never instructs the needs_code_change self-report")
	}
	if p := w222Prompt(t, capDir, 5); !strings.Contains(p, "gate_fail_rewind_text_only") {
		t.Errorf("the re-run review prompt (spawn 5) is never pointed at the gate_fail_rewind_text_only record")
	}
	if rv := w222Steps(t, capDir, "review"); len(rv) == 2 && rv[1]["resume"] != false {
		t.Errorf("the review gate's re-entry must stay fresh: %v", rv[1])
	}
}

// Scenario 5 (claude, chore `code`): the light rewind's resume is
// impossible (the claude native session is gone) — today's fallback: a
// fresh conversation with the previous transcript as read-only reference,
// recorded as fresh, still the light cause, in the same single slot.
func TestW222LightRewindSessionGoneFallsBackFresh(t *testing.T) {
	capDir, root := w222ChoreClaude(t, "w222: light rewind with the claude session gone", false, w222ChorePlan(6000))
	// the review FAIL (spawn 3) holds its envelope for 6s: delete the native
	// session of the finished code dispatch before review FAILs.
	waitUntil(t, 120*time.Second, "review never dispatched", func() bool {
		raw, _ := os.ReadFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"))
		return strings.Contains(string(raw), `"step_id":"review"`)
	})
	native := filepath.Join(root, "claude-config", "projects", w159DashedName(t, capDir))
	if _, err := os.Stat(native); err != nil {
		t.Fatalf("the code dispatch's native claude session dir is not where the locator looks: %v", err)
	}
	if err := os.RemoveAll(native); err != nil {
		t.Fatal(err)
	}
	w222WaitSettled(t, capDir)
	x := readExecution(t, capDir)
	w222AssertLightLedger(t, x, "code")
	code := w222Steps(t, capDir, "code")
	if len(code) != 2 {
		t.Fatalf("code dispatches %d, want 2: %v", len(code), code)
	}
	if code[1]["resume"] != false || code[1]["harness_session"] == code[0]["harness_session"] {
		t.Errorf("the re-entry must be recorded fresh with a new session: %v", code[1])
	}
	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 2 || cl[1]["prompt_has_reference"] != true {
		t.Errorf("the fresh fallback must carry the previous transcript as read-only reference: %v", cl)
	}
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
}
