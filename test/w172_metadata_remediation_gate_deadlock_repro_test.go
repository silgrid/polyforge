package test

// w172_metadata_remediation_gate_deadlock_repro_test.go:,
// reproduced end to end through the real daemon + Go driver and fakepi on
// the real chore lane (plan → code → review → verification).
//
// The reported deadlock: review round 1 fails with blocking findings that
// are exactly the driver-computed plan-conformance facts (files changed but
// not declared). Nothing needs a code change — the repair is metadata: code
// re-submits the same task-repo tree and declares the files in its
// `declared_extensions` output, citing the findings. The
// unchanged-tree guard only compares the task-repo tree hash, so it parks
// the workflow blocked instead of re-running review on the same tree; the
// owner's resume only clears the guard, and the next round re-submits the
// same tree and parks again. The two rules cannot both be satisfied.
//
// Fixture A (TestW172DeclaredExtensionOnUnchangedTreeMustRerunReview) is the
// failing case: after the metadata round the conformance facts the gate
// judges are no longer blocking, so the gate must be re-run instead of
// parked. Fixture B (TestW172StillUnconformedTreeStaysBlocked) pins the
// guard's surviving intent: a metadata round that leaves a changed file
// undeclared still re-submits an unconformed tree, so the workflow stays
// blocked.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// w172PlanOutput is the plan step's output declaring one or more writes.
func w172PlanOutput(paths ...string) map[string]any {
	files := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		files = append(files, map[string]any{
			"path": p, "intent": "write",
			"coupled_obligations": []map[string]any{{
				"none": true, "reason": fmt.Sprintf("w172 fixture: no document, table or projection enumerates %s", p),
			}},
		})
	}
	return map[string]any{"plan_path": "artifacts/plan.md", "step_ids": []string{"s1"}, "touched_files": files}
}

// w172CommitRound is one scripted build round that commits the named files
// into repos/demo-src (the e2e fixture task repo), so the task-repo tree
// really moves and the driver's plan-conformance diff sees them.
func w172CommitRound(round int, paths ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "set -e; cd repos/demo-src; ")
	for _, p := range paths {
		fmt.Fprintf(&b, "printf 'w172 round %d\\n' > %s; ", round, p)
	}
	fmt.Fprintf(&b, "git add %s; ", strings.Join(paths, " "))
	fmt.Fprintf(&b, "git -c user.name=fake-agent -c user.email=fake-agent@invalid commit -q -m 'w172 build round %d'", round)
	return b.String()
}

// w172ReviewFailUnconformed is the gate's FAIL verdict for driver-computed
// undeclared files: every blocking finding is text_only (the metadata repair
// touches capsule artifacts only), and each names the path it is about, the
// way the real round-1 verdict of did.
func w172ReviewFailUnconformed(paths ...string) map[string]any {
	findings := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		findings = append(findings, map[string]any{
			"severity": "blocking", "repair_target": "code", "text_only": true,
			"detail": fmt.Sprintf("`%s` changed but is not declared (driver conformance fact); no code change is needed, declare it citing this finding", p),
		})
	}
	quoted := make([]string, 0, len(paths))
	for _, p := range paths {
		quoted = append(quoted, fmt.Sprintf("`%s`", p))
	}
	return map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{
		"verdict":     "fail",
		"fail_reason": fmt.Sprintf("w172: %s changed but the plan does not declare them; none needs a code change, declare them in declared_extensions", strings.Join(quoted, ", ")),
		"findings":    findings,
	}}
}

// w172MetadataRound is the light build re-entry that repairs only the
// metadata: it commits nothing to the task repo (the tree stays byte-
// identical) and declares the cited files in its own output.
func w172MetadataRound(extensions ...map[string]any) map[string]any {
	return map[string]any{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
		"output": map[string]any{
			"build_status":        "green",
			"summary":             "w172: the undeclared files are declared in declared_extensions; no code or test change was needed",
			"declared_extensions": extensions,
		}}
}

func w172Extension(path, findingID string) map[string]any {
	return map[string]any{"path": path, "source": "review", "ref": findingID}
}

func w172GatePass() map[string]any {
	return map[string]any{"do": "envelope", "outcome": "completed",
		"output": map[string]any{"verdict": "pass", "findings": []any{}}}
}

func w172RunLane(t *testing.T, goal string, plan []map[string]any) (*v1Daemon, string, string) {
	t.Helper()
	inst, _ := newV1InstallTaskRepo(t)
	wi := newWIWithBundle(t, inst, goal, "chore", plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d, wi, d.capsuleDir(wi)
}

func w172Prompt(t *testing.T, capDir string, spawn int) string {
	t.Helper()
	return w78WaitPrompt(t, capDir, spawn)
}

// w172Subsection answers the text under one "## " heading of the facts
// section ("" when the heading is absent).
func w172Subsection(section, heading string) string {
	i := strings.Index(section, heading)
	if i < 0 {
		return ""
	}
	rest := section[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}

func w172FactsSection(t *testing.T, prompt string) string {
	t.Helper()
	i := strings.Index(prompt, w78FactsHeading)
	if i < 0 {
		t.Fatalf("the review prompt carries no %q section:\n%s", w78FactsHeading, prompt)
	}
	rest := prompt[i:]
	if j := strings.Index(rest[len(w78FactsHeading):], "\n# "); j >= 0 {
		return rest[:len(w78FactsHeading)+j]
	}
	return rest
}

// TestW172DeclaredExtensionOnUnchangedTreeMustRerunReview is the reported
// deadlock. A review FAIL driven only by the driver's undeclared-file facts
// rewinds code; code re-submits the identical task-repo tree and declares
// the file with a finding citation. The gate must then be re-run (the tree
// is equivalent only because the metadata it judges changed), and that
// round must judge the new facts — the cited file is declared, not
// undeclared.
//
// RED on the pre-fix tree: the unchanged-tree guard parks the workflow
// blocked with "gate tree unchanged" and review round 2 never dispatches,
// even though the facts the gate would judge are no longer blocking.
func TestW172DeclaredExtensionOnUnchangedTreeMustRerunReview(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true,
			"output": w172PlanOutput("w172_declared.txt")},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run":    []string{w172CommitRound(1, "w172_declared.txt", "w172_undeclared.txt")},
			"output": map[string]any{"build_status": "green"}},
		// review round 1: FAIL on the driver's changed-but-undeclared fact
		w172ReviewFailUnconformed("w172_undeclared.txt"),
		// the rewound build round repairs the metadata only: same tree,
		// the file declared with a citation of the round-1 finding
		w172MetadataRound(w172Extension("w172_undeclared.txt", "s1#1")),
		// the gate's re-run would validate the new conformance facts
		w172GatePass(),
		w172GatePass(),
	}
	_, _, capDir := w172RunLane(t, "w172: a declared extension on an unchanged tree must re-run the gate", plan)
	waitExecution(t, capDir, 120*time.Second, "the workflow never settled", func(x *executionView) bool {
		return x.WorkflowStatus != "" && x.WorkflowStatus != "running"
	})

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "completed" {
		t.Fatalf("SYMPTOM: workflow_status %q, want completed — a metadata-only repair (declared_extensions on the same tree) must let the gate re-judge the new conformance facts instead of parking blocked: %s\nblocked_reason: %s",
			x.WorkflowStatus, execDigest(x), x.BlockedReason)
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "plan,code,review,code,review,verification" {
		t.Fatalf("SYMPTOM: dispatch order %s, want the gate re-run after the metadata round (plan,code,review,code,review,verification)", got)
	}
	if n := len(w222Steps(t, capDir, "review")); n != 2 {
		t.Fatalf("review dispatched %d times, want 2 (the FAIL round and the re-run on the repaired metadata)", n)
	}
	if n := w222RewindCount(x); n != 1 {
		t.Errorf("rewind_stats.count = %v, want 1 — the metadata round is not a second rewind event (%v)", n, x.RewindStats)
	}

	// The re-run's facts: the cited file is declared (a finding-cited
	// extension), never changed-but-undeclared.
	section := w172FactsSection(t, w172Prompt(t, capDir, 5))
	if !strings.Contains(section, "cites review finding s1#1") {
		t.Errorf("SYMPTOM: the re-run's conformance facts do not list `w172_undeclared.txt` as a finding-cited extension:\n%s", section)
	}
	if undeclared := w172Subsection(section, "## Changed but not declared"); strings.Contains(undeclared, "w172_undeclared.txt") {
		t.Errorf("SYMPTOM: a file declared by the metadata round is still a changed-but-undeclared fact in the re-run:\n%s", section)
	}
}

// TestW172StillUnconformedTreeStaysBlocked pins the guard's surviving
// intent: the same metadata round, but a second changed file stays
// undeclared. The tree the gate would judge is still unconformed, so it
// must not be re-run; the workflow parks blocked as before.
func TestW172StillUnconformedTreeStaysBlocked(t *testing.T) {
	plan := []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true,
			"output": w172PlanOutput("w172_declared.txt")},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"run":    []string{w172CommitRound(1, "w172_declared.txt", "w172_undeclared.txt", "w172_other.txt")},
			"output": map[string]any{"build_status": "green"}},
		w172ReviewFailUnconformed("w172_undeclared.txt", "w172_other.txt"),
		// declares only one of the two cited files
		w172MetadataRound(w172Extension("w172_undeclared.txt", "s1#1")),
		// what a re-run would consume if the guard were weakened
		w172GatePass(),
		w172GatePass(),
	}
	d, wi, capDir := w172RunLane(t, "w172: an unconformed tree must still block", plan)
	waitWorkflowStatus(t, d, wi, "blocked", 180*time.Second)

	x := readExecution(t, capDir)
	if x.WorkflowStatus != "blocked" {
		t.Fatalf("workflow_status %q, want blocked — `w172_other.txt` is still changed and undeclared, so the gate must not re-run: %s",
			x.WorkflowStatus, execDigest(x))
	}
	if !strings.Contains(x.BlockedReason, "gate tree unchanged") {
		t.Errorf("blocked reason misses %q: %s", "gate tree unchanged", x.BlockedReason)
	}
	if got := strings.Join(w222Order(t, capDir), ","); got != "plan,code,review,code" {
		t.Errorf("dispatch order %s, want the FAIL round and the metadata round only — no gate re-run on an unconformed tree", got)
	}
	if n := w222RewindCount(x); n != 1 {
		t.Errorf("rewind_stats.count = %v, want 1 (the guard charges no extra slot)", n)
	}
}
