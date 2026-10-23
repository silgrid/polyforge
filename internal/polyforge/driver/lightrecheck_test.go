package driver

// lightrecheck_test.go: the mechanical fast lane's light side
// (polyforge#260) - the light recheck's pre-dispatch phase (the bound
// coverage validation, the deterministic repair-diff measurement), the
// in-place escalation fallback, the light-pass settlement (no
// verified_heads, the carried test_bar, the mechanical_pass record) and
// the light output schema - through the real transition entry point and
// over real temp git repos.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// mechBoundOf parses one bound fixture.
func mechBoundOf(t *testing.T, raw string) []mechBound {
	t.Helper()
	bound, err := parseMechBound(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("bound fixture: %v", err)
	}
	return bound
}

// failVerdictOutput is the gate's recorded FAIL verdict the coverage
// validation reads (its checks[]): lint fail beside passing checks.
var failVerdictOutput = map[string]any{
	"verdict": "fail",
	"checks": []any{
		map[string]any{"name": "build", "status": "pass"},
		map[string]any{"name": "lint", "status": "fail", "detail": "main.go carries trailing whitespace"},
		map[string]any{"name": "authored-scenarios", "status": "pass"},
	},
}

// TestValidateBoundCoverage: the bound must cover the FAIL verdict's
// failed checks exactly - every fail check named by exactly one entry,
// every entry naming a fail check, each repo a declared task repo. Every
// other shape answers an escalation reason.
func TestValidateBoundCoverage(t *testing.T) {
	dir := t.TempDir()
	repo := gitRepo(t, dir, "app", map[string]string{"main.go": "v1\n"})
	_ = repo
	reposDir := filepath.Join(dir, capsule.DirRepos)
	const lintBound = `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`
	cases := []struct {
		name   string
		bound  string
		out    map[string]any
		obbled string // "" = covered
	}{
		{"covered exactly", lintBound, failVerdictOutput, ""},
		{"a failed check uncovered", `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`,
			map[string]any{"verdict": "fail", "checks": []any{
				map[string]any{"name": "lint", "status": "fail"},
				map[string]any{"name": "vet", "status": "fail"},
			}}, "vet"},
		{"a duplicate entry", `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]},{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`,
			failVerdictOutput, "exactly one"},
		{"an entry naming a passing check", `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]},{"check":"build","repo":"app","command":"go build ./...","files":["main.go"]}]`,
			failVerdictOutput, "did not record fail"},
		{"an entry naming an unknown check", `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]},{"check":"spell","repo":"app","command":"misspell .","files":["main.go"]}]`,
			failVerdictOutput, "did not record fail"},
		{"an entry naming an undeclared repo", `[{"check":"lint","repo":"other","command":"gofmt -l .","files":["main.go"]}]`,
			failVerdictOutput, "not a declared task repo"},
		{"no checks recorded at all", lintBound,
			map[string]any{"verdict": "fail"}, "no checks"},
		{"no failed check to cover", lintBound,
			map[string]any{"verdict": "fail", "checks": []any{
				map[string]any{"name": "build", "status": "pass"},
			}}, "no failed check"},
	}
	for _, tc := range cases {
		bound := mechBoundOf(t, tc.bound)
		if got := validateBoundCoverage(bound, tc.out, reposDir); got == "" && tc.obbled != "" {
			t.Errorf("%s: covered, want the reason %q", tc.name, tc.obbled)
		} else if got != "" && (tc.obbled == "" || !strings.Contains(got, tc.obbled)) {
			t.Errorf("%s: reason %q, want it to contain %q", tc.name, got, tc.obbled)
		}
	}
}

// TestMeasureRepairDiff: the repair diff is measured deterministically
// against the bound - per repo, every changed path a modification (M) of
// a bound-named file; the lines and files summed across repos against
// the hard-coded 40/5 caps. Every break answers a reason naming it; the
// same state measured twice derives identically (the crash-replay
// guarantee).
func TestMeasureRepairDiff(t *testing.T) {
	dir := t.TempDir()
	reposDir := filepath.Join(dir, capsule.DirRepos)
	commit := func(repo, msg string, files map[string]string) {
		t.Helper()
		for rel, body := range files {
			writeFile(t, repo, rel, body)
			runGit(t, repo, "add", "-A")
		}
		runGit(t, repo, "commit", "-q", "--allow-empty", "-m", msg)
	}
	bound := mechBoundOf(t, `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`)

	// the in-bound repair: one modification of the bound-named file
	repo := gitRepo(t, dir, "app", map[string]string{"main.go": "v1\n"})
	base := headOf(t, repo)
	commit(repo, "repair", map[string]string{"main.go": "v1 fixed\n"})
	diff, reason := measureRepairDiff(bound, map[string]string{"app": base}, reposDir)
	if reason != "" {
		t.Fatalf("an in-bound repair must hold: %s", reason)
	}
	if diff.Files != 1 || diff.Lines != 2 {
		t.Fatalf("the measured diff = %+v, want 1 file / 2 lines (one modified line)", diff)
	}
	// deterministic: the same state measured twice derives identically
	// (the crash-between-prep-and-settle replay)
	again, reasonAgain := measureRepairDiff(bound, map[string]string{"app": base}, reposDir)
	if reasonAgain != "" || again != diff {
		t.Fatalf("the re-derived measurement differs: %+v/%q vs %+v/%q", again, reasonAgain, diff, reason)
	}

	// the breaks, each answering a reason that names it
	broken := func(name, want string, breakIt func(repo string)) {
		t.Helper()
		repoName := "app2-" + strings.ToLower(name)
		// the bound names the repo it measures (every bound repo must carry
		// a diff base - the fail-closed rule the missing-base case pins)
		repoBound := mechBoundOf(t, fmt.Sprintf(`[{"check":"lint","repo":%q,"command":"gofmt -l .","files":["main.go"]}]`, repoName))
		r := gitRepo(t, dir, repoName, map[string]string{"main.go": "v1\n", "other.go": "o\n"})
		b := headOf(t, r)
		breakIt(r)
		runGit(t, r, "add", "-A")
		runGit(t, r, "commit", "-q", "-m", "break")
		if _, reason := measureRepairDiff(repoBound, map[string]string{repoName: b}, reposDir); reason == "" {
			t.Errorf("%s: the broken bound held", name)
		} else if !strings.Contains(reason, want) {
			t.Errorf("%s: reason %q, want it to name %q", name, reason, want)
		}
	}
	broken("add", `status "A"`, func(repo string) {
		writeFile(t, repo, "new.go", "n\n")
	})
	broken("delete", `status "D"`, func(repo string) {
		if err := os.Remove(filepath.Join(repo, "other.go")); err != nil {
			t.Fatal(err)
		}
	})
	broken("outside", "does not name", func(repo string) {
		writeFile(t, repo, "other.go", "o2\n")
	})

	// the caps: a six-file bound with all six files modified breaks the
	// file cap; a 50-line change on one bound-named file breaks the line cap
	sixBound := mechBoundOf(t, `[`+
		`{"check":"lint","repo":"caps","command":"gofmt -l .","files":["main.go","f0.go","f1.go","f2.go","f3.go","f4.go"]}`+
		`]`)
	r := gitRepo(t, dir, "caps", map[string]string{"main.go": "v1\n", "f0.go": "0\n", "f1.go": "1\n", "f2.go": "2\n", "f3.go": "3\n", "f4.go": "4\n"})
	b := headOf(t, r)
	commit(r, "six files", map[string]string{"main.go": "v2\n", "f0.go": "0b\n", "f1.go": "1b\n", "f2.go": "2b\n", "f3.go": "3b\n", "f4.go": "4b\n"})
	if _, reason := measureRepairDiff(sixBound, map[string]string{"caps": b}, reposDir); reason == "" {
		t.Errorf("a 6-file repair must break the file cap")
	} else if !strings.Contains(reason, "at most 5") {
		t.Errorf("the file-cap reason: %q", reason)
	}
	// 41 changed lines on one bound-named file
	var big strings.Builder
	for i := 0; i < 50; i++ {
		big.WriteString("line\n")
	}
	r2 := gitRepo(t, dir, "lines", map[string]string{"main.go": "v1\n"})
	b2 := headOf(t, r2)
	commit(r2, "41 lines", map[string]string{"main.go": big.String()})
	linesBound := mechBoundOf(t, `[{"check":"lint","repo":"lines","command":"gofmt -l .","files":["main.go"]}]`)
	if _, reason := measureRepairDiff(linesBound, map[string]string{"lines": b2}, reposDir); reason == "" {
		t.Errorf("a 50-line repair must break the line cap")
	} else if !strings.Contains(reason, "at most 40") {
		t.Errorf("the line-cap reason: %q", reason)
	}
	// an unreadable base sha breaks it (fail-closed)
	if _, reason := measureRepairDiff(bound, map[string]string{"app": "0000000000000000000000000000000000000000"}, reposDir); reason == "" {
		t.Errorf("an unreadable diff base must break the bound")
	}
	// a bound repo with NO diff base entry (its HEAD was unreadable at
	// the FAIL verdict's settlement) is never measured — fail closed,
	// never open
	if _, reason := measureRepairDiff(bound, map[string]string{}, reposDir); reason == "" {
		t.Errorf("a bound repo without a diff base must break the bound (fail closed, never open)")
	} else if !strings.Contains(reason, "no repair-diff base") {
		t.Errorf("the missing-base reason: %q", reason)
	}
}

// TestLightRecheckEvidence: the light recheck's own command evidence must
// show every bound command run with exit 0 in its repo - the
// testBarRerun machinery (a leading cd sets the repo, barKey compares the
// command text) - and build the recheck record entries; anything else
// answers the escalation reason.
func TestLightRecheckEvidence(t *testing.T) {
	root := t.TempDir()
	bound := mechBoundOf(t, `[{"check":"lint","repo":"app","command":"gofmt -l .","files":["main.go"]}]`)
	const cdEvidence = `[{"argv":["sh","-c","cd repos/app && gofmt -l ."],"exit_code":0,"duration_ms":5}]`
	recheck, reason := lightRecheckEvidence(bound, json.RawMessage(cdEvidence), root)
	if reason != "" {
		t.Fatalf("the cd-shaped evidence must cover the bound command: %s", reason)
	}
	if len(recheck) != 1 || recheck[0]["repo"] != "app" || recheck[0]["command"] != "gofmt -l ." || recheck[0]["exit_code"] != 0 {
		t.Fatalf("the recheck record = %v, want the bound command's entry", recheck)
	}
	// the cwd-shaped evidence is the same machinery
	_, reason = lightRecheckEvidence(bound, json.RawMessage(`[{"argv":["gofmt","-l","."],"cwd":"repos/app","exit_code":0,"duration_ms":5}]`), root)
	if reason != "" {
		t.Fatalf("the cwd-shaped evidence must cover the bound command: %s", reason)
	}
	for _, tc := range []struct {
		name   string
		evJSON string
		want   string
	}{
		{"no evidence at all", ``, "no command evidence"},
		{"exit non-zero", `[{"argv":["sh","-c","cd repos/app && gofmt -l ."],"exit_code":1,"duration_ms":5}]`, "exit 0"},
		{"the wrong repo", `[{"argv":["sh","-c","cd repos/other && gofmt -l ."],"exit_code":0,"duration_ms":5}]`, "not shown run"},
		{"a different command", `[{"argv":["sh","-c","cd repos/app && go vet ./..."],"exit_code":0,"duration_ms":5}]`, "not shown run"},
	} {
		if _, reason := lightRecheckEvidence(bound, json.RawMessage(tc.evJSON), root); reason == "" {
			t.Errorf("%s: covered", tc.name)
		} else if !strings.Contains(reason, tc.want) {
			t.Errorf("%s: reason %q, want it to contain %q", tc.name, reason, tc.want)
		}
	}
}

// lightRecheckFAIL is a light recheck FAIL verdict (a bound command still
// red): the gate_failed outcome the verdict binding would produce.
func lightRecheckFAIL() fakeEntry {
	return fakeEntry{res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(
		`{"verdict":"fail","fail_reason":"the bound command is still red: main.go still carries trailing whitespace","checks":[{"name":"lint","status":"fail","detail":"still red"}]}`)}}
}

// TestRunMechanicalFastLaneSettlesLightPass: the whole light path through
// the real Run loop - the armed FAIL rewinds mechanically (one slot, code
// resumes), the light re-entry repairs inside the bound, and the light
// recheck settles a light pass: the mechanical_pass record with the audit
// facts, NO verified_heads, the FAIL verdict's own test_bar carried
// verbatim (the #203 lesson), the marker cleared, the workflow completed.
func TestRunMechanicalFastLaneSettlesLightPass(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("code"), mechGateSpec("code"))
	repo := gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"main.go": "v1\n"})
	repair := func() {
		if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("v1 fixed\n"), 0o644); err != nil {
			t.Error(err)
			return
		}
		if err := gitIn(repo, "commit", "-q", "-am", "mechanical repair: main.go"); err != nil {
			t.Error(err)
		}
	}
	lightPass := fakeEntry{res: &StepResult{Outcome: OutcomeCompleted,
		// NB4 (polyforge#261): the forged step-supplied mechanical_pass
		// must be stripped and the driver-written record kept — the delete
		// sits before the light-settle write below it.
		Output:          json.RawMessage(`{"verdict":"pass","checks":[{"name":"lint","status":"pass"}],"mechanical_pass":{"forged":true}}`),
		CommandEvidence: json.RawMessage(`[{"argv":["sh","-c","cd repos/app && gofmt -l ."],"exit_code":0,"duration_ms":5}]`)}}
	sp.script = []fakeEntry{
		done(`{}`),                         // code's first round
		mechanicalFAIL(),                   // the armed verification FAILs all-static, classified
		{res: done(`{}`).res, act: repair}, // the light re-entry repairs inside the bound
		lightPass,                          // the light recheck over the bound command
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", exec.WorkflowStatus)
	}
	// the mechanical rewind: one slot, code archived with the mechanical cause
	if exec.RewindStats.Count != 1 {
		t.Fatalf("rewind_stats = %+v, want exactly one slot", exec.RewindStats)
	}
	var mech bool
	for _, a := range exec.Steps["code"].Attempts {
		if a.Cause == CauseGateFailRewindMechanical && a.RewindGate == verificationStepID {
			mech = true
		}
	}
	if !mech {
		t.Fatalf("code must be archived with the mechanical light cause: %+v", exec.Steps["code"].Attempts)
	}
	// code dispatched twice: the first round, the resumed light re-entry
	var code []StepSpawn
	for _, s := range sp.spawns {
		if s.StepID == "code" {
			code = append(code, s)
		}
	}
	if len(code) != 2 || !code[1].Resume {
		t.Fatalf("code dispatches %v, want 2 with the re-entry resumed", code)
	}
	// the light pass settlement
	ver := exec.Steps[verificationStepID]
	if ver.Status != capsule.StepDone || ver.PendingLightRecheck != nil {
		t.Fatalf("verification must be done with the marker cleared: %+v", ver)
	}
	if _, has := ver.Output[verifiedHeadsKey]; has {
		t.Fatalf("a light pass must not write verified_heads: %s", canoJSON(ver.Output[verifiedHeadsKey]))
	}
	mp, _ := ver.Output[mechanicalPassKey].(map[string]any)
	if mp == nil {
		t.Fatalf("no mechanical_pass record on the light-passed output: %s", canoJSON(ver.Output))
	}
	if mp["escalated"] != false {
		t.Errorf("mechanical_pass.escalated = %v, want false", mp["escalated"])
	}
	var wantBound any
	if err := json.Unmarshal([]byte(mechanicalBoundJSON), &wantBound); err != nil {
		t.Fatal(err)
	}
	if got := canoJSON(mp["bound"]); got != canoJSON(wantBound) {
		t.Errorf("mechanical_pass.bound = %s, want the FAIL verdict's classification %s", got, mechanicalBoundJSON)
	}
	if diff, _ := mp["diff"].(map[string]any); diff == nil || diff["files"] != float64(1) || diff["lines"] != float64(2) {
		t.Errorf("mechanical_pass.diff = %v, want the measured 1 file / 2 lines", mp["diff"])
	}
	rec, _ := mp["recheck"].([]any)
	if len(rec) != 1 {
		t.Errorf("mechanical_pass.recheck = %s, want exactly the bound command", canoJSON(mp["recheck"]))
	} else if e, _ := rec[0].(map[string]any); e["repo"] != "app" || e["command"] != "gofmt -l ." || e["exit_code"] != float64(0) {
		t.Errorf("mechanical_pass.recheck entry = %s, want the bound command run green in repos/app", canoJSON(e))
	}
	if heads, _ := mp["recheck_heads"].(map[string]any); heads == nil || heads["app"] == nil {
		t.Errorf("mechanical_pass.recheck_heads = %s, want the driver-read recheck head for app", canoJSON(mp["recheck_heads"]))
	}
	// the carried test_bar: the FAIL verdict's own record, byte-faithful
	// (the driver copies it; the step agent never re-transcribes it)
	var wantBar any
	if err := json.Unmarshal([]byte(`[{"repo":"app","command":"go test ./..."}]`), &wantBar); err != nil {
		t.Fatal(err)
	}
	if got := canoJSON(ver.Output["test_bar"]); got != canoJSON(wantBar) {
		t.Errorf("the carried test_bar = %s, want the FAIL verdict's recorded bar %s", got, canoJSON(wantBar))
	}
	// the IN-MEMORY round-trip (the regression the deliver merge rule once
	// hit): buildResultSpec reads the driver's live state, not the disk —
	// the light record's recheck_heads must assert there too, or deliver's
	// merge rule sees no light head and asks a human for every light-passed
	// repo.
	spec := d.buildResultSpec(deliverStepID, 5)
	if spec.LightRecheckHeads["app"] == "" {
		t.Fatalf("buildResultSpec(deliver) carries no light recheck head in memory: %+v", spec.LightRecheckHeads)
	}
	if spec.VerifiedHeads["app"] != "" {
		t.Errorf("a light-passed workflow must carry no verified head to deliver either: %+v", spec.VerifiedHeads)
	}
}

// TestRunBrokenRepairBoundEscalatesInPlace: a repair diff that breaks the
// bound (a commit on a file the bound does not name) escalates IN PLACE,
// in the same dispatch, to the existing full verification - no light
// dispatch, no new rewind slot, an audit-only light_recheck_escalated
// attempt on verification - and the escalated full pass records
// verified_heads and its own test_bar exactly as today.
func TestRunBrokenRepairBoundEscalatesInPlace(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("code"), mechGateSpec("code"))
	gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"main.go": "v1\n"})
	outOfBound := func() {
		writeFile(t, filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, "app"), "OTHER.txt", "outside the bound\n")
		if err := gitIn(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, "app"), "add", "OTHER.txt"); err != nil {
			t.Error(err)
		}
		if err := gitIn(filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, "app"), "commit", "-q", "-m", "outside"); err != nil {
			t.Error(err)
		}
	}
	fullPass := fakeEntry{res: &StepResult{Outcome: OutcomeCompleted,
		// NB4 (polyforge#261): the forged step-supplied mechanical_pass on
		// the completed verification result must be stripped (the pass is
		// the ESCALATED full re-run's, not a light pass — no driver-written
		// record exists to keep).
		Output:          json.RawMessage(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},{"name":"lint","status":"pass"},{"name":"authored-scenarios","status":"pass"}],"test_bar":[{"repo":"app","command":"go test ./..."}],"mechanical_pass":{"forged":true}}`),
		CommandEvidence: json.RawMessage(`[{"argv":["sh","-c","cd repos/app && go test ./..."],"exit_code":0,"duration_ms":50}]`)}}
	sp.script = []fakeEntry{
		done(`{}`),
		mechanicalFAIL(),
		{res: done(`{}`).res, act: outOfBound}, // the light re-entry commits OUTSIDE the bound
		fullPass,                               // the escalated full verification (no light dispatch)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", exec.WorkflowStatus)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("rewind_stats = %+v, want exactly one slot (the escalation charges none)", exec.RewindStats)
	}
	ver := exec.Steps[verificationStepID]
	if ver.Status != capsule.StepDone || ver.PendingLightRecheck != nil {
		t.Fatalf("verification must be done with the marker cleared: %+v", ver)
	}
	if _, has := ver.Output[mechanicalPassKey]; has {
		t.Fatalf("an escalated full pass must not carry mechanical_pass: %s", canoJSON(ver.Output))
	}
	var escalated bool
	for _, a := range ver.Attempts {
		if a.Cause == CauseLightRecheckEscalated {
			escalated = true
			if a.Detail == "" {
				t.Errorf("the light_recheck_escalated attempt carries no reason in detail: %+v", a)
			}
		}
	}
	if !escalated {
		t.Fatalf("no light_recheck_escalated attempt on verification (causes: %v)", attemptCauses(ver))
	}
	// the escalation's audit record (the spec's complete audit trail): the
	// marker's bound and diff base, the measured (broken) diff, the
	// escalation flag and the reason — archived in the attempt's result
	// before the same write cleared the marker
	audit := escalationAudit(t, ver)
	assertEscalationAuditBound(t, audit)
	if reason, _ := audit["reason"].(string); !strings.Contains(reason, "want a modification") {
		t.Errorf("the escalation audit's reason = %q, want the broken-bound reason (modifications only)", audit["reason"])
	}
	if diff, _ := audit["diff"].(map[string]any); diff == nil {
		t.Errorf("the escalation audit carries no measured diff: %s", canoJSON(audit))
	}
	if audit["recheck"] != nil {
		t.Errorf("a pre-dispatch escalation never ran a recheck: %s", canoJSON(audit["recheck"]))
	}
	// the escalation event: exactly one light_recheck_escalated, carrying
	// the reason — and never a rewind (the escalation charges no slot and
	// routes no verdict)
	escEvents := eventsOf(t, d, EvLightRecheckEscalated)
	if len(escEvents) != 1 {
		t.Fatalf("light_recheck_escalated events = %d, want exactly one (one event per escalation)", len(escEvents))
	}
	if det, _ := escEvents[0].body["detail"].(string); !strings.Contains(det, "want a modification") {
		t.Errorf("the escalation event's detail = %q, want the broken-bound reason (modifications only)", det)
	}
	if n := len(eventsOf(t, d, "rewind")); n != 1 {
		t.Errorf("rewind events = %d, want exactly one (the mechanical rewind's; the escalation never rewinds)", n)
	}
	// the escalated full pass records exactly as today: verified heads and
	// its own test bar
	heads, _ := ver.Output[verifiedHeadsKey].(map[string]any)
	if heads == nil || heads["app"] == nil {
		t.Fatalf("the escalated full pass recorded no verified head for app: %s", canoJSON(ver.Output[verifiedHeadsKey]))
	}
	var wantBar any
	if err := json.Unmarshal([]byte(`[{"repo":"app","command":"go test ./..."}]`), &wantBar); err != nil {
		t.Fatal(err)
	}
	if got := canoJSON(ver.Output["test_bar"]); got != canoJSON(wantBar) {
		t.Errorf("the escalated full pass's own test_bar = %s, want the fixture bar %s", got, canoJSON(wantBar))
	}
	// verification dispatched exactly twice: the FAIL verdict, the
	// escalated full re-run (fresh) - no light dispatch
	var vers []StepSpawn
	for _, s := range sp.spawns {
		if s.StepID == verificationStepID {
			vers = append(vers, s)
		}
	}
	if len(vers) != 2 || vers[1].Resume {
		t.Fatalf("verification dispatches %v, want 2 with the escalated full re-run fresh", vers)
	}
	if !vers[1].EscalatedRecheck {
		t.Errorf("the escalated full re-run's dispatch must carry the escalation order (the prompt's escalation note)")
	}
}

func attemptCauses(st *capsule.Step) []string {
	var out []string
	for _, a := range st.Attempts {
		out = append(out, a.Cause)
	}
	return out
}

// eventsOf collects the driver's logged events of one kind (runEnv's
// eventLog fake — the same log the daemon binding would carry them to).
func eventsOf(t *testing.T, d *Driver, kind string) []loggedEvent {
	t.Helper()
	l, ok := d.log.(*eventLog)
	if !ok {
		t.Fatalf("runEnv's log is not the eventLog fake: %T", d.log)
	}
	var out []loggedEvent
	for _, e := range l.entries {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// escalationAudit reads the light_recheck_escalated attempt's audit
// record (its Result field) from one settled verification step — the
// spec's complete audit trail: the marker's bound and diff base, the
// measured diff, the recheck's own record, the escalation flag and the
// reason.
func escalationAudit(t *testing.T, ver *capsule.Step) map[string]any {
	t.Helper()
	for _, a := range ver.Attempts {
		if a.Cause != CauseLightRecheckEscalated {
			continue
		}
		if len(a.Result) == 0 {
			t.Fatalf("the light_recheck_escalated attempt carries no audit record in result: %+v", a)
		}
		var audit map[string]any
		if err := json.Unmarshal(a.Result, &audit); err != nil {
			t.Fatalf("the escalation attempt's result is not JSON: %s", a.Result)
		}
		return audit
	}
	t.Fatalf("no light_recheck_escalated attempt on verification (causes: %v)", attemptCauses(ver))
	return nil
}

// assertEscalationAuditBound checks the escalation audit's bound and
// diff base: the FAIL verdict's own classification (copied verbatim into
// the marker) and the FAIL-settlement base the marker carried.
func assertEscalationAuditBound(t *testing.T, audit map[string]any) {
	t.Helper()
	var wantBound any
	if err := json.Unmarshal([]byte(mechanicalBoundJSON), &wantBound); err != nil {
		t.Fatal(err)
	}
	if got := canoJSON(audit["bound"]); got != canoJSON(wantBound) {
		t.Errorf("the escalation audit's bound = %s, want the FAIL verdict's classification %s", got, mechanicalBoundJSON)
	}
	if db, _ := audit["diff_base"].(map[string]any); db == nil || db["app"] == nil {
		t.Errorf("the escalation audit's diff_base = %s, want the marker's FAIL-settlement base", canoJSON(audit["diff_base"]))
	}
	if audit["escalated"] != true {
		t.Errorf("the escalation audit's escalated = %v, want true", audit["escalated"])
	}
}

// TestRunLightRecheckFailVerdictEscalates: a light recheck that does not
// pass (a bound command still red -> its own FAIL verdict) escalates in
// the result's own settlement write - no rewind, no new slot - and the
// escalated full verification then settles under today's rules.
func TestRunLightRecheckFailVerdictEscalates(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("code"), mechGateSpec("code"))
	gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"main.go": "v1\n"})
	fullPass := fakeEntry{res: &StepResult{Outcome: OutcomeCompleted,
		Output:          json.RawMessage(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},{"name":"lint","status":"pass"}]}`),
		CommandEvidence: json.RawMessage(`[{"argv":["sh","-c","cd repos/app && go test ./..."],"exit_code":0,"duration_ms":50}]`)}}
	sp.script = []fakeEntry{
		done(`{}`),         // code's first round
		mechanicalFAIL(),   // the armed classified FAIL rewinds mechanically
		done(`{}`),         // the light re-entry completes (nothing repaired)
		lightRecheckFAIL(), // the light recheck FAILs (still red)
		fullPass,           // the escalated full verification passes
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	exec := readExec(t, execPath)
	if exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", exec.WorkflowStatus)
	}
	if exec.RewindStats.Count != 1 {
		t.Fatalf("rewind_stats = %+v, want exactly one slot (the recheck-FAIL escalation charges none)", exec.RewindStats)
	}
	ver := exec.Steps[verificationStepID]
	var escalated bool
	for _, a := range ver.Attempts {
		if a.Cause == CauseLightRecheckEscalated {
			escalated = true
		}
		if a.Cause == CauseGateFailRewind || a.Cause == CauseGateFailRewindMechanical {
			// the gate itself is never archived by a rewind
			if a.RewindGate == verificationStepID {
				t.Errorf("a recheck-FAIL escalation must not rewind: %+v", a)
			}
		}
	}
	if !escalated {
		t.Fatalf("no light_recheck_escalated attempt on verification: %v", attemptCauses(ver))
	}
	if _, has := ver.Output[mechanicalPassKey]; has {
		t.Fatalf("an escalated full pass must not carry mechanical_pass: %s", canoJSON(ver.Output))
	}
	// the escalation's audit record: the recheck's own recorded checks ride
	// it (the recheck ran), beside the marker's bound, diff base and the
	// re-measured (empty) repair diff
	audit := escalationAudit(t, ver)
	assertEscalationAuditBound(t, audit)
	if reason, _ := audit["reason"].(string); !strings.Contains(reason, "still red") {
		t.Errorf("the escalation audit's reason = %q, want the recheck's own fail reason", audit["reason"])
	}
	if diff, _ := audit["diff"].(map[string]any); diff == nil {
		t.Errorf("the escalation audit carries no measured diff: %s", canoJSON(audit))
	} else if diff["files"] != float64(0) || diff["lines"] != float64(0) {
		t.Errorf("the escalation audit's diff = %s, want the measured empty repair diff (nothing was repaired)", canoJSON(diff))
	}
	var wantChecks any
	if err := json.Unmarshal([]byte(`[{"name":"lint","status":"fail","detail":"still red"}]`), &wantChecks); err != nil {
		t.Fatal(err)
	}
	if got := canoJSON(audit["recheck"]); got != canoJSON(wantChecks) {
		t.Errorf("the escalation audit's recheck = %s, want the light recheck's own recorded checks %s", got, canoJSON(wantChecks))
	}
	// the escalation event: exactly one light_recheck_escalated, carrying
	// the recheck's own fail reason — never a rewind, never a second
	// gate_verdict (the recheck's FAIL verdict is not a gate judgment)
	escEvents := eventsOf(t, d, EvLightRecheckEscalated)
	if len(escEvents) != 1 {
		t.Fatalf("light_recheck_escalated events = %d, want exactly one (one event per escalation)", len(escEvents))
	}
	if det, _ := escEvents[0].body["detail"].(string); !strings.Contains(det, "still red") {
		t.Errorf("the escalation event's detail = %q, want the recheck's own fail reason", det)
	}
	if n := len(eventsOf(t, d, "rewind")); n != 1 {
		t.Errorf("rewind events = %d, want exactly one (the mechanical rewind's; a recheck-FAIL escalation never rewinds)", n)
	}
	if n := len(eventsOf(t, d, "gate_verdict")); n != 1 {
		t.Errorf("gate_verdict events = %d, want exactly one (the FAIL verdict's; the recheck FAIL escalates, never a gate verdict)", n)
	}
	// NB3 (polyforge#261): the in-place escalation is not a review round
	// — review_rounds[verification] keeps exactly the armed FAIL's single
	// full-gate evaluation, with no second entry from the recheck's own
	// FAIL verdict.
	if n := exec.ReviewRounds[verificationStepID]; n != 1 {
		t.Fatalf("review_rounds[verification] = %d, want 1 (the armed FAIL alone; the escalation is not a round)", n)
	}
}

// TestLightOutputSchemaCompiles (polyforge#260): the shared fast-lane
// material's declared LIGHT output schema compiles, validates the light
// recheck's own output (verdict + checks, no full-bar fields demanded) and
// rejects the self-contradictory shapes.
func TestLightOutputSchemaCompiles(t *testing.T) {
	s, err := loadLightOutputSchema(filepath.Join(shippedWorkflowsDir, "feature-full"))
	if err != nil || s == nil || s.Schema == nil {
		t.Fatalf("the shared fast-lane material's light schema must compile: %v", err)
	}
	lightOK := `{"verdict":"pass","checks":[{"name":"lint","status":"pass"}]}`
	if err := checkOutputSchema(s.Schema, json.RawMessage(lightOK)); err != nil {
		t.Fatalf("a light pass output must validate: %v", err)
	}
	lightFail := `{"verdict":"fail","fail_reason":"still red","checks":[{"name":"lint","status":"fail"}]}`
	if err := checkOutputSchema(s.Schema, json.RawMessage(lightFail)); err != nil {
		t.Fatalf("a light fail output must validate: %v", err)
	}
	for _, bad := range []string{
		`{"verdict":"pass","checks":[]}`,                                   // no checks
		`{"verdict":"pass","checks":[{"name":"lint","status":"skipped"}]}`, // a skipped check under pass
		`{"verdict":"pass","checks":[{"name":"lint","status":"fail"}]}`,    // a failed check under pass
		`{"verdict":"fail","checks":[{"name":"lint","status":"fail"}]}`,    // a fail verdict without fail_reason
		`{"checks":[{"name":"lint","status":"pass"}]}`,                     // no verdict
	} {
		if err := checkOutputSchema(s.Schema, json.RawMessage(bad)); err == nil {
			t.Errorf("the light schema must reject %s", bad)
		}
	}
}

// TestLightRecheckPromptCarriesLightSection (polyforge#260): the light
// recheck dispatch's prompt carries the light section (the bound commands
// per repo + the shared light contract) and the LIGHT output schema
// instead of the full one - and not the classification duty (the recheck
// does not classify).
func TestLightRecheckPromptCarriesLightSection(t *testing.T) {
	capDir := capsuleWithShippedBundle(t, "feature-full")
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "mechanical-fix.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	for _, p := range shared {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_light", CapsuleDir: capDir, BundleName: "feature-full", SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	order := &LightRecheckOrder{
		Bound:    []mechBound{{Check: "lint", Repo: "app", Command: "gofmt -l .", Files: []string{"main.go"}}},
		DiffBase: map[string]string{"app": "0123456789abcdef"},
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "verification", SpawnN: 9, LightRecheck: order,
	}); err != nil {
		t.Fatalf("SpawnStep light recheck: %v", err)
	}
	p, _ := sp.last()
	for _, want := range []string{
		"# Light recheck (the mechanical fast lane)",
		"bounded to its failed checks",
		"`repos/app`, command `gofmt -l .`",
		"files: `main.go`",
		"Re-run EXACTLY each bound command",
		"never fabricate a pass",
	} {
		if !strings.Contains(p.InitialPrompt, want) {
			t.Errorf("the light recheck prompt lost %q", want)
		}
	}
	for _, noise := range []string{
		"# Mechanical classification (this lane is armed)",
		"required: [verdict, checks, authored_titles, scenarios]",
	} {
		if strings.Contains(p.InitialPrompt, noise) {
			t.Errorf("the light recheck prompt carries %q (the full bar's or the classification duty's)", noise)
		}
	}
	// the light output schema is the one shown: the material's own text
	s, err := loadLightOutputSchema(filepath.Join(shippedWorkflowsDir, "feature-full"))
	if err != nil || s == nil {
		t.Fatalf("light schema: %v", err)
	}
	if !strings.Contains(p.InitialPrompt, s.Text) {
		t.Errorf("the light recheck prompt must show the light output schema")
	}
}

// TestEscalatedFullRerunPromptCarriesEscalationNote (polyforge#260): the
// ESCALATED full verification re-run's prompt carries the escalation note
// (the report's fast-lane section duty - the light recheck that would have
// written it never settled) beside the classification duty (an escalated
// full FAIL may be classified mechanical again) - and no light section
// (no marker, no light dispatch).
func TestEscalatedFullRerunPromptCarriesEscalationNote(t *testing.T) {
	capDir := capsuleWithShippedBundle(t, "feature-full")
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "mechanical-fix.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	for _, p := range shared {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_esc", CapsuleDir: capDir, BundleName: "feature-full", SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "verification", SpawnN: 9, EscalatedRecheck: true,
	}); err != nil {
		t.Fatalf("SpawnStep escalated full re-run: %v", err)
	}
	p, _ := sp.last()
	for _, want := range []string{
		"# Escalated full verification (the mechanical fast lane)",
		"escalated in place to this full verification",
		"write the fast-lane section of artifacts/verification-report.md",
	} {
		if !strings.Contains(p.InitialPrompt, want) {
			t.Errorf("the escalated full re-run's prompt lost %q", want)
		}
	}
	// the classification duty still rides (an escalated full FAIL may be
	// classified mechanical again); no light section (no marker)
	if !strings.Contains(p.InitialPrompt, "# Mechanical classification (this lane is armed)") {
		t.Errorf("the escalated full re-run must keep the classification duty")
	}
	if strings.Contains(p.InitialPrompt, "# Light recheck (the mechanical fast lane)") {
		t.Errorf("the escalated full re-run is not a light dispatch")
	}
}
