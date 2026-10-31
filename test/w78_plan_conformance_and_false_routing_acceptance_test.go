package test

// w78_plan_conformance_and_false_routing_acceptance_test.go:,
// authored before `code` from artifacts/spec.md. Runtime scenarios drive the
// shipped lanes through the real daemon + driver and fakepi, judging durable
// faces only; text scenarios read the shipped tree.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const w78FactsHeading = "# Plan conformance (driver-computed facts)"

func w78RunLane(t *testing.T, goal, bundle string, plan []map[string]any) (*v1Daemon, string, string) {
	t.Helper()
	inst, _ := newV1InstallTaskRepo(t)
	wi := newWIWithBundle(t, inst, goal, bundle, plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d, wi, d.capsuleDir(wi)
}

func w78PlanOutput(entries ...map[string]any) map[string]any {
	return map[string]any{"plan_path": "artifacts/plan.md", "step_ids": []string{"s1"},
		"touched_files": entries}
}

func w78Write(path string, obligations ...map[string]any) map[string]any {
	return map[string]any{"path": path, "intent": "write", "coupled_obligations": obligations}
}

func w78Fix(addressed bool, summary string) map[string]any {
	return map[string]any{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
		"output": map[string]any{
			"commits":              []map[string]any{{"sha": "abc1234", "message": "w78: fix"}},
			"build_status":         "green",
			"root_cause_addressed": addressed,
			"regression_test":      "w78_regression_test.go",
			"summary":              summary,
		}}
}

func w78RootCause(verdict, reason string) map[string]any {
	out := map[string]any{"verdict": verdict, "root_cause": "w78: scripted diagnosis",
		"evidence": []map[string]any{{"kind": "test", "ref": "w78_regression_test.go",
			"detail": "the scripted repro distinguishes the two causes"}}}
	if verdict == "fail" {
		out["fail_reason"] = reason
	}
	return map[string]any{"do": "envelope", "id": "root-cause", "outcome": "completed",
		"handoff": true, "output": out}
}

func w78GatePass() map[string]any {
	return map[string]any{"do": "envelope", "outcome": "completed",
		"output": map[string]any{"verdict": "pass", "findings": []any{}}}
}

func w78WaitPrompt(t *testing.T, capDir string, spawn int) string {
	t.Helper()
	path := filepath.Join(capDir, "logs", fmt.Sprintf("fakepi-prompt-%d.txt", spawn))
	waitUntil(t, 120*time.Second, fmt.Sprintf("spawn %d never persisted its prompt", spawn), func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read prompt %s: %v", path, err)
	}
	return string(raw)
}

func w78WaitOrderPrefix(t *testing.T, capDir string, n int, want string) []string {
	t.Helper()
	waitUntil(t, 120*time.Second, fmt.Sprintf("the dispatch journal never reached %d entries", n), func() bool {
		return len(w69Journal(capDir)) >= n
	})
	seq := w69Journal(capDir)
	if got := strings.Join(seq[:n], ","); got != want {
		t.Fatalf("SYMPTOM: dispatch order prefix %s, want %s — a false fix must re-enter root-cause before review or verification", got, want)
	}
	return seq
}

func w78FactsSection(t *testing.T, prompt string) string {
	t.Helper()
	i := strings.Index(prompt, w78FactsHeading)
	if i < 0 {
		t.Fatalf("SYMPTOM: the prompt carries no %q section — the gate must receive both set differences as computed facts, not reconstruct them:\n%s",
			w78FactsHeading, prompt)
	}
	rest := prompt[i:]
	if j := strings.Index(rest[len(w78FactsHeading):], "\n# "); j >= 0 {
		return rest[:len(w78FactsHeading)+j]
	}
	return rest
}

func w78Context(s, needle string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if strings.Contains(line, needle) {
			lo, hi := i-1, i+2
			if lo < 0 {
				lo = 0
			}
			if hi > len(lines) {
				hi = len(lines)
			}
			return strings.Join(lines[lo:hi], "\n")
		}
	}
	return ""
}
func TestW78PlanObligationMissingEntryRejectedBeforeCode(t *testing.T) {
	missing := w78PlanOutput(
		map[string]any{"path": "w78_missing_obligation.txt", "intent": "write"},
	)
	accepted := w78PlanOutput(
		w78Write("w78_missing_obligation.txt", map[string]any{"path": "docs/w78-obligation.md", "note": "table row"}),
		w78Write("w78_plain.txt", map[string]any{"none": true, "reason": "no doc, table or projection enumerates this fixture"}),
	)
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true, "output": missing},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true, "output": accepted},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true},
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: a plan write without a coupled-obligation entry is rejected", "chore", plan)
	rej := waitForEvent(t, d, wi, "envelope_rejected", 30*time.Second)
	if !strings.Contains(string(rej.Body), "w78_missing_obligation.txt") {
		t.Fatalf("SYMPTOM: the plan rejection does not name the offending write %q: %s",
			"w78_missing_obligation.txt", rej.Body)
	}
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	seq := strings.Join(w222Order(t, capDir), ",")
	if seq != "plan,plan,code,review,verification" {
		t.Fatalf("SYMPTOM: dispatch order %s, want the rejected plan, the accepted retry, then code — "+
			"the build step must not run on the missing-entry plan", seq)
	}
}
func TestW78TwoWayConformanceReachesReviewAndVerification(t *testing.T) {
	code := `set -e; cd repos/demo-src; ` +
		`printf 'changed\n' > w78_changed.txt; printf 'undeclared\n' > w78_undeclared.txt; ` +
		`git add -A; git -c user.name=w78 -c user.email=w78@invalid commit -q -m 'w78 code'`
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true,
			"output": w78PlanOutput(
				w78Write("w78_changed.txt", map[string]any{"path": "docs/w78-coupled.md", "note": "table row"}),
				w78Write("w78_missing.txt", map[string]any{"none": true, "reason": "no coupled document"}),
			)},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run": []string{code}, "output": map[string]any{"build_status": "green"}},
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: both conformance differences reach review and verification", "chore", plan)
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	for _, spawn := range []int{3, 4} {
		section := w78FactsSection(t, w78WaitPrompt(t, capDir, spawn))
		for _, path := range []string{"w78_undeclared.txt", "docs/w78-coupled.md", "w78_missing.txt"} {
			if !strings.Contains(section, path) {
				t.Errorf("spawn %d conformance facts omit %q:\n%s", spawn, path, section)
			}
		}
		if ctx := w78Context(section, "w78_undeclared.txt"); !strings.Contains(ctx, "blocking") || !strings.Contains(ctx, "undeclared") {
			t.Errorf("spawn %d: an undeclared change must be a blocking changed-but-undeclared fact, got %q", spawn, ctx)
		}
		if ctx := w78Context(section, "docs/w78-coupled.md"); !strings.Contains(ctx, "blocking") {
			t.Errorf("spawn %d: an unchanged declared coupled obligation must be blocking, got %q", spawn, ctx)
		}
		if ctx := w78Context(section, "w78_missing.txt"); !strings.Contains(ctx, "non-blocking") {
			t.Errorf("spawn %d: an unchanged declared plain write must be non-blocking, got %q", spawn, ctx)
		}
	}
}
func TestW78CitedExtensionCountsAsDeclared(t *testing.T) {
	first := `set -e; cd repos/demo-src; printf 'base\n' > w78_base.txt; ` +
		`git add -A; git -c user.name=w78 -c user.email=w78@invalid commit -q -m 'w78 code'`
	second := `set -e; cd repos/demo-src; printf 'cited\n' > w78_cited.txt; printf 'uncited\n' > w78_uncited.txt; ` +
		`git add -A; git -c user.name=w78 -c user.email=w78@invalid commit -q -m 'w78 review fix'`
	reviewFail := map[string]any{"do": "envelope", "id": "review", "outcome": "gate_failed",
		"output": map[string]any{"verdict": "fail", "fail_reason": "w78: the fixture files are missing",
			"findings": []map[string]any{{"severity": "blocking", "repair_target": "code", "detail": "add the reviewed fixture files"}}}}
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true,
			"output": w78PlanOutput(w78Write("w78_base.txt", map[string]any{"none": true, "reason": "fixture"}))},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run": []string{first}, "output": map[string]any{"build_status": "green"}},
		reviewFail,
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run": []string{second}, "output": map[string]any{
				"build_status": "green",
				"declared_extensions": []map[string]any{{
					"path": "w78_cited.txt", "source": "review", "ref": "s3#1",
				}},
			}},
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: a finding-cited extension counts as declared", "chore", plan)
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	section := w78FactsSection(t, w78WaitPrompt(t, capDir, 5))
	if ctx := w78Context(section, "w78_uncited.txt"); !strings.Contains(ctx, "undeclared") || !strings.Contains(ctx, "blocking") {
		t.Fatalf("SYMPTOM: an uncited added file must stay a blocking changed-but-undeclared fact, got %q:\n%s", ctx, section)
	}
	if ctx := w78Context(section, "w78_cited.txt"); ctx != "" && strings.Contains(ctx, "undeclared") {
		t.Fatalf("SYMPTOM: the file cited as s3#1 is still listed as undeclared: %q", ctx)
	}
	if !strings.Contains(section, "s3#1") {
		t.Fatalf("SYMPTOM: the cited extension does not echo its finding id s3#1:\n%s", section)
	}
}
func TestW78FirstFalseRewindsToRootCause(t *testing.T) {
	const summary = "w78: the first diagnosis named the wrong condition"
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true},
		w78RootCause("pass", ""),
		w78Fix(false, summary),
		w78RootCause("pass", ""),
		w78Fix(true, "w78: the second diagnosis was fixed"),
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: the first honest false rewinds to root-cause", "bug-fix", plan)
	w78WaitOrderPrefix(t, capDir, 4, "repro,root-cause,fix,root-cause")
	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,root-cause,fix,review,verification" {
		t.Fatalf("SYMPTOM: dispatch order %s — the lane must continue through review and verification after the re-diagnosis", got)
	}
	x := readExecution(t, capDir)
	// Owner-approved (code needs_input w78-authored-test-cause-filter, answer A):
	// read the root-cause attempts directly — w222Causes keeps only causes
	// containing "rewind", so it can never return root_cause_unaddressed.
	var archived bool
	var rcAttempts []map[string]any
	if st := x.Steps["root-cause"]; st != nil {
		rcAttempts = st.Attempts
	}
	for _, a := range rcAttempts {
		if a["cause"] == "root_cause_unaddressed" && a["rewind_gate"] == "fix" {
			archived = true
		}
	}
	if !archived {
		t.Fatalf("SYMPTOM: root-cause attempts %v, want the root_cause_unaddressed rewind archive (rewind_gate fix)", rcAttempts)
	}
	recs := w69qRewinds(x)
	if len(recs) != 1 || recs[0]["gate"] != "fix" || recs[0]["to_step"] != "root-cause" || recs[0]["class"] != "charged" {
		t.Fatalf("SYMPTOM: rewind ledger %v, want one charged fix→root-cause record", x.RewindStats)
	}
	if prompt := w78WaitPrompt(t, capDir, 4); !strings.Contains(prompt, summary) {
		t.Fatalf("SYMPTOM: the re-run root-cause prompt lost the fix summary — it must be the pending rewind input:\n%s", prompt)
	}
}
func TestW78RootCauseFailAfterFalseStillRewindsToRepro(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true},
		w78RootCause("pass", ""),
		w78Fix(false, "w78: the diagnosis was wrong"),
		w78RootCause("fail", "w78: the repro lacks the failing environment"),
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true},
		w78RootCause("pass", ""),
		w78Fix(true, "w78: fixed against the re-proven cause"),
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: a root-cause fail after the false routing still rewinds to repro", "bug-fix", plan)
	w78WaitOrderPrefix(t, capDir, 4, "repro,root-cause,fix,root-cause")
	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	want := "repro,root-cause,fix,root-cause,repro,root-cause,fix,review,verification"
	if got := strings.Join(w222Order(t, capDir), ","); got != want {
		t.Fatalf("SYMPTOM: dispatch order %s, want %s — the existing root-cause→repro failure path must survive", got, want)
	}
}
func TestW78SecondFalseParksOnOwnerQuestion(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "repro", "outcome": "completed", "handoff": true},
		w78RootCause("pass", ""),
		w78Fix(false, "w78: first wrong diagnosis"),
		w78RootCause("pass", ""),
		w78Fix(false, "w78: second wrong diagnosis"),
		w78GatePass(), w78GatePass(),
	}
	d, wi, capDir := w78RunLane(t, "w78: the second false asks the owner", "bug-fix", plan)
	w78WaitOrderPrefix(t, capDir, 4, "repro,root-cause,fix,root-cause")
	waitQuestion(t, d, wi, "root_cause_false", 60*time.Second)
	x := readExecution(t, capDir)
	if x.WorkflowStatus == "failed_rewind_exhausted" || x.WorkflowStatus == "completed" {
		t.Fatalf("SYMPTOM: second false settled %q, want the workflow parked at the owner question", x.WorkflowStatus)
	}
	if n := w222RewindCount(x); n != 1 {
		t.Fatalf("SYMPTOM: rewind_stats.count = %v, want exactly one fix→root-cause rewind", n)
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "repro,root-cause,fix,root-cause,fix" {
		t.Fatalf("SYMPTOM: dispatch order %s — the second false must not rewind again or enter the gates", got)
	}
	if n := len(w222Steps(t, capDir, "review")); n != 0 {
		t.Fatalf("SYMPTOM: review dispatched %d times; the false report must never reach review", n)
	}
}
func TestW78StalePlanMechanismsRemoved(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("w78: %s must be readable: %v", rel, err)
		}
		return string(b)
	}
	for _, rel := range []string{"workflows/chore/steps/plan.md", "workflows/feature-full/steps/plan.md"} {
		s := read(rel)
		for _, gone := range []string{"declared_resources", "file-scope locks", "lock gap"} {
			if strings.Contains(s, gone) {
				t.Errorf("w78: %s still claims the nonexistent file-scope lock mechanism (%q)", rel, gone)
			}
		}
		if !strings.Contains(s, "coupled obligation") || !strings.Contains(s, "scope check") {
			t.Errorf("w78: %s must give coupled obligations and scope checking as the real reason for the touched-files list", rel)
		}
	}
	if s := read("workflows/feature-full/steps/plan.md"); strings.Contains(s, "re-planned `code` pass") {
		t.Error("w78: feature-full plan.md still describes the unreachable plan re-entry")
	}
	if s := read("workflows/feature-full/workflow.md"); strings.Contains(s, "re-authoring happens as part of the `code` step") {
		t.Error("w78: feature-full workflow.md still says code re-authors the plan")
	}
}
func TestW78BugFixTextMatchesFalseRouting(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("w78: %s must be readable: %v", rel, err)
		}
		return string(b)
	}
	wf := read("workflows/bug-fix/workflow.md")
	for _, gone := range []string{"automatic blocking finding", "no direct rewind to `root-cause`", "NOT a rewind target", "stop honestly"} {
		if strings.Contains(wf, gone) {
			t.Errorf("w78: bug-fix workflow.md still carries the old clause %q", gone)
		}
	}
	for _, want := range []string{"rewinds", "root-cause", "second false"} {
		if !strings.Contains(wf, want) {
			t.Errorf("w78: bug-fix workflow.md must describe the new routing (%q)", want)
		}
	}
	fix := read("workflows/bug-fix/steps/fix.md")
	if strings.Contains(fix, "there is no direct rewind") {
		t.Error("w78: fix.md still tells the agent there is no direct rewind to root-cause")
	}
	if !strings.Contains(fix, "driver rewinds") || !strings.Contains(fix, "root-cause") {
		t.Error("w78: fix.md must state the driver's one-time rewind to root-cause")
	}
}
func TestW78LaneDescriptionsAndReviewIntroMatchOrder(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("w78: %s must be readable: %v", rel, err)
		}
		return string(b)
	}
	chore := read("workflows/chore/workflow.md")
	if !strings.Contains(chore, "plan → code → review → verification → deliver") {
		t.Error("w78: chore description must show plan → code → review → verification → deliver")
	}
	bug := read("workflows/bug-fix/workflow.md")
	if !strings.Contains(bug, "repro → root-cause → fix → review → verification → deliver") {
		t.Error("w78: bug-fix description must show repro → root-cause → fix → review → verification → deliver")
	}
	for _, gone := range []string{"last step positioned", "mechanical results"} {
		if strings.Contains(bug, gone) {
			t.Errorf("w78: bug-fix review intro still claims %q", gone)
		}
	}
}
func TestW78CouplingRulesLiveInAgentsMd(t *testing.T) {
	root := v1Root(t)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("w78: %s must be readable: %v", rel, err)
		}
		return string(b)
	}
	// The generic couplings only: the sync-list and ledger couplings hold
	// only in the internal tree (the public projection drops them), so
	// their pin lives in an internal-only test.
	agents := read("AGENTS.md")
	if !strings.Contains(agents, "Coupled obligations") {
		t.Error("w78: AGENTS.md must carry the project's coupled-obligations section")
	}
	if !strings.Contains(agents, "skills/polyforge/verbs/doctor.md") {
		t.Errorf("w78: AGENTS.md must name the known coupling %q", "skills/polyforge/verbs/doctor.md")
	}
	for _, rel := range []string{"workflows/chore/steps/plan.md", "workflows/feature-full/steps/plan.md"} {
		if s := read(rel); strings.Contains(s, "docs/sync/") || strings.Contains(s, "skills/polyforge/") {
			t.Errorf("w78: %s names a polyforge-specific path; workflows/ must stay project-neutral", rel)
		}
	}
}
