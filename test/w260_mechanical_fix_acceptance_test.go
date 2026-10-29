package test

// w260_mechanical_fix_acceptance_test.go: polyforge#260 — the
// mechanical-fix fast lane (a verification FAIL whose every failed check
// is static rewinds cheaply: one mechanical rewind that resumes the code
// session, one light recheck over the bound commands, fail-closed
// escalation to the existing full verification), end to end through the
// real daemon + driver and the fake harness. Authored against
// artifacts/spec.md BEFORE the implementation (e2e-author): every
// scenario drives the SHIPPED feature-full bundle (whose verification
// step entry the implementation arms with `mechanical_fix: true`) with
// scripted envelopes and judges only records the spec names — the
// execution.json attempts ledger, rewind stats, the spawn journal
// (resume vs fresh), the driver-written verification output keys
// (mechanical_pass, verified_heads, the carried test_bar) — never an
// implementation-internal surface. The unarmed-lane criterion (a lane
// without the key rewinds exactly as today) is today's behavior and is
// judged by the existing suites; the classification's static-only
// boundary is the verification agent's own duty (the driver classifies
// nothing), backstopped by deliver's unconditional full-bar re-run
// (scenario 3).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The fake lane's authored acceptance suite: one title, echoed verbatim
// by every verification verdict the lane's output schema asks to carry
// it (feature-full declares authored_titles/scenarios required).
const w260Title = "the mechanical fast lane rounds a static-only FAIL cheaply"

// w260BoundCmd is the one failed check's repro command (a static,
// deterministic check) — the light recheck re-runs exactly it.
const w260BoundCmd = "git status --porcelain"

// w260BarCmd is the full test bar the FAILING full verification
// recorded; deliver must re-run exactly it after a light pass.
const w260BarCmd = "git diff --quiet HEAD"

// w260Bound is the FAIL verdict's mechanical bound: per failed check
// (lint) its repo, its exact command and the file it named.
func w260Bound() []map[string]any {
	return []map[string]any{{
		"check": "lint", "repo": "demo", "command": w260BoundCmd,
		"files": []string{"work.txt"},
	}}
}

// w260Bar is the full test bar the FAILING verification recorded.
func w260Bar() []map[string]any {
	return []map[string]any{{"repo": "demo", "command": w260BarCmd}}
}

// w260Head is the feature-full lane up to (excluding) its first review
// (review runs right after code, verification behind it):
// the four clarification steps, the authored acceptance suite, and code
// claiming the demo task repo from origin.
func w260Head(origin string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true,
			"output": map[string]any{
				"scenario_titles": []string{w260Title},
				"scenarios_path":  "artifacts/e2e-scenarios.md",
			}},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"name": "demo", "origin": origin},
	}
}

// w260FailVerdict is the armed lane's full verification FAIL: every
// failed check static (lint only; the authored scenarios pass), the
// bound covering it exactly, and the full test bar recorded for the
// light-pass handover.
func w260FailVerdict() map[string]any {
	return map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
		"evidence": []map[string]any{{
			"argv":      []string{"sh", "-c", "cd repos/demo && " + w260BoundCmd},
			"exit_code": 1, "duration_ms": 5,
		}},
		"output": map[string]any{
			"verdict":     "fail",
			"fail_reason": "lint: repos/demo work.txt carries trailing whitespace (check lint)",
			"checks": []map[string]any{
				{"name": "build", "status": "pass"},
				{"name": "lint", "status": "fail", "detail": "work.txt carries trailing whitespace"},
				{"name": "authored-scenarios", "status": "pass"},
			},
			"authored_titles":  []string{w260Title},
			"scenarios":        []map[string]any{{"title": w260Title, "status": "pass"}},
			"test_bar":         w260Bar(),
			"mechanical_bound": w260Bound(),
		}}
}

// w260CodeRepair is code's light re-entry: an in-bound repair commit
// (only the bound-named work.txt — a file the claim commit already
// tracks, so the repair diff is a modification, M — well inside the
// caps) — the light recheck's repair-diff base is the FAIL-settlement
// repo HEAD. (The fixture originally appended to an untracked README,
// an add; the owner's answer (a) of 2026-10-23 authorized this shape.)
func w260CodeRepair() map[string]any {
	return map[string]any{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
		"run": []string{
			"cd repos/demo && printf 'linted\\n' >> work.txt && git add work.txt && " +
				"git -c user.name=pf -c user.email=pf@polyforge.invalid " +
				"commit -q -m 'mechanical repair: work.txt whitespace'",
		}}
}

// w260CodeRepairStray is scenario 2's light re-entry: the in-bound
// repair commit PLUS a second, out-of-bound commit (an add on a file
// the bound does not name). NB2 (polyforge#261): the out-of-bound
// commit rides the re-entry's own script — one writer, one index, and
// strictly before the re-entry's completion, hence before the light
// recheck's pre-dispatch diff measurement. The old shape (the test
// side committing OTHER.txt in repos/demo right after the rewind
// archive, while the resumed re-entry still held the same repo's
// worktree and index) raced the re-entry's own commit on index.lock
// and, on a late landing, the pre-dispatch measurement (a settlement-
// time escalation, three verification dispatches) — the #140 lesson:
// no sleeps or retries, order by construction.
func w260CodeRepairStray() map[string]any {
	e := w260CodeRepair()
	e["run"] = append(e["run"].([]string),
		"cd repos/demo && printf 'outside the bound\\n' > OTHER.txt && git add OTHER.txt && "+
			"git -c user.name=pf -c user.email=pf@polyforge.invalid "+
			"commit -q -m 'outside the bound'")
	return e
}

// w260LightPass is the light recheck's verdict: pass over the checks,
// no full-bar fields (the light output schema), and real command
// evidence for the one bound command re-run inside its repo.
func w260LightPass(holdMS int) map[string]any {
	a := map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
		"run": []string{"cd repos/demo && " + w260BoundCmd},
		"output": map[string]any{
			"verdict": "pass",
			"checks":  []map[string]any{{"name": "lint", "status": "pass"}},
		}}
	if holdMS > 0 {
		a["ms"] = holdMS
	}
	return a
}

// w260FullPass is a FULL verification pass (the escalated re-run after
// a broken bound, or today's normal re-run): the whole bar, the authored
// suite, the test bar of its own.
func w260FullPass() map[string]any {
	return map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
		"run": []string{"cd repos/demo && " + w260BoundCmd},
		"output": map[string]any{
			"verdict": "pass",
			"checks": []map[string]any{
				{"name": "build", "status": "pass"},
				{"name": "lint", "status": "pass"},
				{"name": "authored-scenarios", "status": "pass"},
			},
			"authored_titles": []string{w260Title},
			"scenarios":       []map[string]any{{"title": w260Title, "status": "pass"}},
			"test_bar":        w260Bar(),
		}}
}

func w260ReviewPass() map[string]any {
	return map[string]any{"do": "envelope", "id": "review", "outcome": "completed",
		"output": map[string]any{"verdict": "pass"}}
}

// w260JSON answers v's canonical JSON (a byte-faithfulness comparison
// base).
func w260JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

// w260SubmitFeatureFull submits one feature-full work item with plan
// and answers its daemon and capsule dir (the caller drives the wait).
func w260SubmitFeatureFull(t *testing.T, inst *v1Install, goal string, plan []map[string]any) (*v1Daemon, string) {
	t.Helper()
	wi := newWIWithBundle(t, inst, goal, "feature-full", plan)
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d, d.capsuleDir(wi)
}

// w260AssertMechanicalRewind checks the mechanical rewind's ledger
// facts: code archived with the mechanical light-rewind cause from the
// verification gate, exactly one rewind slot, and the code re-entry
// resuming code's previous conversation.
func w260AssertMechanicalRewind(t *testing.T, x *executionView, capDir string) {
	t.Helper()
	var mech bool
	if st := x.Steps["code"]; st != nil {
		for _, a := range st.Attempts {
			if a["cause"] == "gate_fail_rewind_mechanical" && a["rewind_gate"] == "verification" {
				mech = true
			}
		}
	}
	if !mech {
		t.Errorf("SYMPTOM: no code attempt with cause gate_fail_rewind_mechanical and "+
			"rewind_gate=verification — code attempt causes %v (the FAIL verdict was not "+
			"consumed as a mechanical classification)", w222Causes(x, "code"))
	}
	if n := w222RewindCount(x); n != 1 {
		t.Errorf("rewind_stats.count = %v, want exactly one slot (rewind_stats=%v)", n, x.RewindStats)
	}
	code := w222Steps(t, capDir, "code")
	if len(code) != 2 {
		t.Errorf("code dispatches %d, want 2 (build, light re-entry): %v", len(code), code)
	} else if code[1]["resume"] != true || code[1]["transcript"] != code[0]["transcript"] {
		t.Errorf("SYMPTOM: the mechanical rewind's code re-entry did not resume the previous "+
			"conversation — first %v, re-entry %v", code[0]["transcript"], code[1])
	}
}

// w260AssertLightPass checks the light-pass settlement: a
// mechanical_pass record carrying the audit facts (the bound copied
// from the FAIL verdict, the measured diff, the recheck command's
// result, escalated: false), NO verified_heads, and the FAIL verdict's
// own test_bar carried byte-faithfully.
func w260AssertLightPass(t *testing.T, x *executionView) {
	t.Helper()
	ver := x.Steps["verification"]
	if ver == nil || ver.Status != "done" {
		t.Fatalf("verification not done: %+v", ver)
	}
	if _, has := ver.Output["verified_heads"]; has {
		t.Errorf("SYMPTOM: the light pass wrote verified_heads (%s) — a head verified only "+
			"by a light recheck is never tests-covered", w260JSON(ver.Output["verified_heads"]))
	}
	mp, _ := ver.Output["mechanical_pass"].(map[string]any)
	if mp == nil {
		t.Errorf("SYMPTOM: no mechanical_pass record on verification's done output — the "+
			"recheck settled as a full pass (output keys: %s)", w260JSON(ver.Output))
		return
	}
	if mp["escalated"] != false {
		t.Errorf("mechanical_pass.escalated = %v, want false", mp["escalated"])
	}
	if got := w260JSON(mp["bound"]); got != w260JSON(w260Bound()) {
		t.Errorf("SYMPTOM: mechanical_pass.bound = %s, want the FAIL verdict's bound %s "+
			"(copied, never re-typed)", got, w260JSON(w260Bound()))
	}
	if diff, _ := mp["diff"].(map[string]any); diff == nil {
		t.Errorf("mechanical_pass carries no measured diff: %s", w260JSON(mp))
	} else {
		if f, _ := diff["files"].(float64); f != 1 {
			t.Errorf("mechanical_pass.diff.files = %v, want 1 (the in-bound work.txt repair)", diff["files"])
		}
		if l, _ := diff["lines"].(float64); l < 1 {
			t.Errorf("mechanical_pass.diff.lines = %v, want ≥ 1 (the repair commit's line)", diff["lines"])
		}
	}
	rec, _ := mp["recheck"].([]any)
	if len(rec) != 1 {
		t.Errorf("mechanical_pass.recheck = %s, want exactly the one bound command", w260JSON(mp["recheck"]))
	} else if e, _ := rec[0].(map[string]any); e["repo"] != "demo" ||
		e["command"] != w260BoundCmd || e["exit_code"] != float64(0) {
		t.Errorf("SYMPTOM: mechanical_pass.recheck entry = %s, want repo demo, command %q, exit_code 0",
			w260JSON(e), w260BoundCmd)
	}
	if got := w260JSON(ver.Output["test_bar"]); got != w260JSON(w260Bar()) {
		t.Errorf("SYMPTOM: the carried test_bar = %s, want the FAIL verdict's recorded bar %s "+
			"byte-for-byte (never re-typed by the step agent)", got, w260JSON(w260Bar()))
	}
}

// Scenario 1 (pi, feature-full): an armed lane's all-static FAIL
// verdict takes the fast lane — the mechanical rewind charges one slot
// and resumes the code session, the in-bound repair settles a light
// recheck over exactly the bound command, the light-pass record carries
// the audit facts, no verified_heads is written, the test bar is carried
// byte-faithfully, and the review re-runs in full over the repaired tree
// before the light recheck ('s order).
func TestW260MechanicalRewindResumesCodeAndSettlesLightPass(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	plan := append(w260Head(remote.url),
		w260ReviewPass(),  // the first review, in full (before verification)
		w260FailVerdict(), // the full verification FAILs all-static
		w260CodeRepair(),  // the resumed code session fixes inside the bound
		w260ReviewPass(),  // the re-run review over the repaired tree
		w260LightPass(0),  // the light recheck over the bound command
	)
	_, capDir := w260SubmitFeatureFull(t, inst, "w260: an all-static FAIL takes the light path", plan)
	w222WaitSettled(t, capDir)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	w260AssertMechanicalRewind(t, x, capDir)
	w260AssertLightPass(t, x)
	if got := strings.Join(w222Order(t, capDir), ","); got !=
		"grill,spec,grill-review,plan,e2e-author,code,review,verification,code,review,verification" {
		t.Errorf("dispatch order %s — the light recheck must sit behind the resumed code "+
			"re-entry and the re-run review", got)
	}
}

// Scenario 2 (pi, feature-full): the repair diff breaks the bound (a
// commit on a file the bound does not name, an add) — the light path
// escalates IN PLACE, in the same dispatch, to the existing full
// verification: no light dispatch, no new rewind slot, an audit-only
// escalation attempt on verification, and the escalated full pass
// records verified_heads and its own test_bar exactly as today. The
// out-of-bound commit rides the resumed re-entry itself (NB2,
// polyforge#261: one writer — see w260CodeRepairStray).
func TestW260OutOfBoundRepairEscalatesToFullVerification(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	plan := append(w260Head(remote.url),
		w260ReviewPass(),
		w260FailVerdict(),
		w260CodeRepairStray(), // the resumed code session repairs in-bound, then commits outside it
		w260ReviewPass(),      // the re-run review over the repaired tree
		w260FullPass(),        // the escalated full verification (no light dispatch)
	)
	_, capDir := w260SubmitFeatureFull(t, inst, "w260: an out-of-bound repair escalates in place", plan)
	w222WaitSettled(t, capDir)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s)", x.WorkflowStatus, execDigest(x))
	}
	w260AssertMechanicalRewind(t, x, capDir)
	var escalated bool
	if st := x.Steps["verification"]; st != nil {
		for _, a := range st.Attempts {
			if a["cause"] == "light_recheck_escalated" {
				escalated = true
				if det, _ := a["detail"].(string); det == "" {
					t.Errorf("the light_recheck_escalated attempt carries no reason in detail: %v", a)
				}
			}
		}
	}
	if !escalated {
		t.Errorf("SYMPTOM: no light_recheck_escalated attempt on verification (causes %v) — "+
			"a broken repair bound must escalate in the same dispatch", w222Causes(x, "verification"))
	}
	ver := x.Steps["verification"]
	if ver == nil || ver.Status != "done" {
		t.Fatalf("verification not done: %+v", ver)
	}
	if _, has := ver.Output["mechanical_pass"]; has {
		t.Errorf("an escalated full pass must not carry mechanical_pass: %s", w260JSON(ver.Output))
	}
	// The escalated full pass records exactly as today: verified heads
	// and its own test bar.
	heads, _ := ver.Output["verified_heads"].(map[string]any)
	if heads == nil || heads["demo"] == nil {
		t.Errorf("SYMPTOM: the escalated full pass recorded no verified head for demo: %s",
			w260JSON(ver.Output["verified_heads"]))
	}
	if got := w260JSON(ver.Output["test_bar"]); got != w260JSON(w260Bar()) {
		t.Errorf("the escalated full pass's own test_bar = %s, want %s", got, w260JSON(w260Bar()))
	}
	if ver := w222Steps(t, capDir, "verification"); len(ver) != 2 || ver[1]["resume"] != false {
		t.Errorf("verification dispatches %v — the escalation must replace the light dispatch "+
			"in place (no third dispatch, the full re-run fresh)", ver)
	}
}

// Scenario 3 (pi, feature-full, deliver kept): after a light pass the
// workflow carries NO verified head, so deliver's tests-covered
// guarantee rests entirely on the merge dispatch's re-run of the
// CARRIED test bar — unconditional, with command evidence. The scripted
// deliver procedure re-runs it between its merge and the PR merge, and
// the delivery is accepted without a human.
func TestW260DeliverRerunsCarriedTestBarAfterLightPass(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w260: deliver re-runs the carried bar after a light pass", "feature-full")
	// The scripted deliver procedure: push and open the PR, re-run the
	// carried test bar in the merge dispatch, merge, record.
	deliver := literalBranch(branch, pushAndPR("demo"))
	deliver = append(deliver, "cd repos/demo && "+w260BarCmd)
	deliver = append(deliver, literalBranch(branch, mergeRecord("demo", ""), assemble)...)
	plan := append(w260Head(origin),
		w260ReviewPass(),
		w260FailVerdict(),
		w260CodeRepair(),
		w260ReviewPass(),
		w260LightPass(0),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run": deliver, "output_file": deliverOutput},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)
	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if n := w.calls("gh pr create"); n != 1 {
		t.Errorf("gh pr create ran %d times, want exactly 1", n)
	}
	if n := w.calls("gh pr merge"); n != 1 {
		t.Errorf("gh pr merge ran %d times, want exactly 1", n)
	}
	x := readExecution(t, d.capsuleDir(wi))
	if st := x.Steps["deliver"]; st == nil || st.Status != "done" {
		t.Fatalf("deliver not done without a human — the merge dispatch's recorded re-run of "+
			"the carried bar must cover the tests: %+v", st)
	}
	w260AssertMechanicalRewind(t, x, d.capsuleDir(wi))
	w260AssertLightPass(t, x)
}

// Scenario 4 (pi, feature-full): kill -9 mid-light-recheck (the recheck
// dispatched and holding) — the restart derives the next dispatch from
// execution.json alone: the light recheck re-dispatches and replays
// idempotently (no second rewind slot, no half state), settles the
// light pass and the lane wraps.
func TestW260Kill9MidLightRecheckReplaysIdempotently(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	plan := append(w260Head(remote.url),
		w260ReviewPass(),
		w260FailVerdict(),
		w260CodeRepair(),
		w260ReviewPass(),
		w260LightPass(6000), // the light recheck, holding mid-dispatch
		w260LightPass(0),    // the replay after the restart
	)
	d, capDir := w260SubmitFeatureFull(t, inst, "w260: kill -9 mid-light-recheck", plan)
	// The light recheck dispatched (journal entry 11: the lane's eleventh
	// spawn — adds the re-run review between the repair and
	// the recheck) and holds: crash the daemon inside the hold.
	waitUntil(t, 180*time.Second, "the light recheck never dispatched", func() bool {
		return len(spawnJournal(t, capDir)) >= 11
	})
	d.crash()
	d2 := d.restarted()
	d2.start()
	w222WaitSettled(t, capDir)
	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Errorf("workflow_status %q, want completed (%s) — the interrupted light recheck "+
			"must replay with no half state", x.WorkflowStatus, execDigest(x))
	}
	w260AssertMechanicalRewind(t, x, capDir)
	w260AssertLightPass(t, x)
	if got := len(w222Steps(t, capDir, "verification")); got != 3 {
		t.Errorf("verification dispatches %d, want 3 (the FAIL verdict, the killed light "+
			"recheck, the replay) — a completed step must not run twice beyond the replay", got)
	}
}
