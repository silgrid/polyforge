package test

// w73_merged_head_acceptance_test.go: — verification proves the
// tree merged with the latest origin default branch, so the driver-written
// `verified_heads` names a merged head and deliver re-runs the carried test
// bar only when its own merge moves HEAD past it.
//
// Authored against artifacts/spec.md BEFORE the implementation
// (e2e-author), through the real daemon + Go driver and the fake harness
// (test/fakepi as pi, the fake `gh`). Each scenario scripts the shipped
// chore lane and judges only records the spec names: the dispatched step
// prompts (logs/fakepi-prompt-<spawn>.txt), execution.json's driver-written
// `verified_heads`, the dispatch's real command evidence, the daemon's
// `envelope_rejected` events and the human-question face. No driver-internal
// symbol is referenced, so the baseline red is assertion-level, never a
// build failure.
//
// What this ticket changes is SHIPPED STEP TEXT (verification must fetch and
// merge before its bar; deliver must state the no-re-run consequence and
// that an earlier dispatch's bar run is never a credential). The red
// assertion of every scenario is therefore the duty text in the DISPATCHED
// prompt; the driver facts around it (verified_heads, the refusal, the
// acceptance) are the regression guard that must stay exactly as it is.
//
// The asserted duty phrases are the contract the shipped text must carry —
// each scenario names its own group in the failure message.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// w73ChoreHead scripts the chore lane up to (excluding) verification
// ( order: plan → code → review → verification → deliver).
func w73ChoreHead(origin string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
	}
}

// w73FetchMerge is verification's side of the new duty, run inside the
// repo exactly as the shipped step text tells the agent to run it.
const w73FetchMerge = `cd repos/demo && git fetch -q origin && git merge --no-edit origin/main`

// w73BarAfterMerge is the carried test bar, run on the merged HEAD.
const w73BarAfterMerge = w73FetchMerge + ` && ` + deliverTestBar

// w73JournalLoose reads the capsule spawn journal leniently (a torn line is
// skipped, a missing file is "not yet").
func w73JournalLoose(capDir string) []map[string]any {
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		var e map[string]any
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// w73PromptOf answers the full prompt of a step's LAST dispatch, from the
// fake pi's prompt ledger.
func w73PromptOf(t *testing.T, capDir, step string) string {
	t.Helper()
	n := 0
	for _, e := range w73JournalLoose(capDir) {
		if s, _ := e["step_id"].(string); s == step {
			if v, ok := e["spawn_n"].(float64); ok && int(v) > n {
				n = int(v)
			}
		}
	}
	if n == 0 {
		t.Fatalf("no spawn-journal entry for step %q", step)
	}
	raw, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-"+strconv.Itoa(n)+".txt"))
	if err != nil {
		t.Fatalf("the dispatched prompt of step %q (spawn %d): %v", step, n, err)
	}
	return string(raw)
}

// w73MustSay asserts the dispatched prompt carries every duty phrase
// (case-insensitive) — the shipped step text the behaviour depends on.
func w73MustSay(t *testing.T, prompt, step string, want ...string) {
	t.Helper()
	low := strings.ToLower(prompt)
	for _, w := range want {
		if !strings.Contains(low, strings.ToLower(w)) {
			t.Errorf("SYMPTOM: the dispatched %s prompt never carries %q — the shipped step text does not "+
				"teach the behaviour this scenario verifies; the prompt must carry every one of %v",
				step, w, want)
		}
	}
}

// w73Commands answers the recorded argv strings of one step's accepted result.
func w73Commands(x *executionView, step string) []string {
	var out []string
	st := x.Steps[step]
	if st == nil {
		return nil
	}
	list, _ := st.Output["commands"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		args, _ := m["argv"].([]any)
		var words []string
		for _, a := range args {
			s, _ := a.(string)
			words = append(words, s)
		}
		out = append(out, strings.Join(words, " "))
	}
	return out
}

// w73VerifiedHeads answers execution.json's verified_heads (nil when absent).
func w73VerifiedHeads(x *executionView) map[string]any {
	if x.Steps["verification"] == nil {
		return nil
	}
	heads, _ := x.Steps["verification"].Output["verified_heads"].(map[string]any)
	return heads
}

// w73IsAncestor answers whether a is an ancestor of b in dir.
func w73IsAncestor(dir, a, b string) bool {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", a, b)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// w73MachineMadeHead asserts the repo HEAD is a machine-made two-parent
// merge (a `git merge` of the fetched tip: its tree equals git's own merge
// result) and answers that HEAD.
func w73MachineMadeHead(t *testing.T, repo string) string {
	t.Helper()
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	parents := strings.Fields(gitRun(t, repo, "rev-list", "--parents", "-n", "1", head))
	if len(parents) != 3 {
		t.Fatalf("%s HEAD %s is not a two-parent merge commit (parents %v); verification did not merge the default branch", repo, head, parents)
	}
	want := strings.TrimSpace(strings.SplitN(strings.TrimSpace(gitRun(t, repo, "merge-tree", "--write-tree", parents[1], parents[2])), "\n", 2)[0])
	got := strings.TrimSpace(gitRun(t, repo, "rev-parse", head+"^{tree}"))
	if want != got {
		t.Fatalf("%s HEAD %s is not machine-made: its tree is %s, git's own merge of its parents is %s", repo, head, got, want)
	}
	return head
}

// w73NoRejection fails when the daemon refused any envelope of the run.
func w73NoRejection(t *testing.T, d *v1Daemon, wi string) {
	t.Helper()
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			t.Fatalf("an envelope was rejected: %s", e.Body)
		}
	}
}

// Scenario 1 (spec: "The verification gate MUST prove the tree merged with
// the latest default branch" + "Deliver MUST NOT re-run the test bar when
// the verified head is still the delivered head"): origin main advances
// before verification; verification fetches and merges it, then runs the
// carried bar on the merged HEAD. `verified_heads` names that machine-made
// merge, and deliver — whose own merge is "Already up to date" — records no
// test_bar command at all, reports tests_passed and is accepted without a
// human.
func TestW73VerifiedHeadIsMergedHeadAndDeliverRecordsNoRerun(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w73: verification proves the merged tree, deliver records no re-run", "chore")
	inst.setPlanFull(t, wi, append(w73ChoreHead(origin),
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": verifiedWithTestBar(), "no_started_at": true,
			"run": []string{moveMain(origin, "other.txt", "another PR"), w73BarAfterMerge}},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true,
			"run":           literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	), nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)

	// (a) the shipped verification text carries the fetch+merge-before-the-bar duty.
	w73MustSay(t, w73PromptOf(t, capDir, "verification"), "verification",
		"git fetch", "git merge --no-edit origin/", "already up to date")

	// (b) verified_heads names the machine-made merge that contains origin/main.
	repo := filepath.Join(capDir, "repos", "demo")
	head := w73MachineMadeHead(t, repo)
	if !w73IsAncestor(repo, "origin/main", "HEAD") {
		t.Fatalf("%s HEAD %s does not contain the fetched origin/main: the bar did not run on the merged tree", repo, head)
	}
	if got := w73VerifiedHeads(x)["demo"]; got != head {
		t.Fatalf("verified_heads.demo = %v, want the merged HEAD %s (verified heads %v)", got, head, w73VerifiedHeads(x))
	}

	// (c) deliver proved nothing again: no bar command in its dispatch, and
	// the driver accepted the merge with no human question.
	for _, cmd := range w73Commands(x, "deliver") {
		if strings.Contains(cmd, "test -s work.txt") {
			t.Fatalf("deliver recorded a test_bar command (%q) although its merge left HEAD equal to verified_heads", cmd)
		}
	}
	st := x.Steps["deliver"]
	repos, _ := st.Output["repos"].([]any)
	if len(repos) != 1 {
		t.Fatalf("deliver output: %+v", st.Output)
	}
	r, _ := repos[0].(map[string]any)
	if r["merged"] != true || r["tests_passed"] != true {
		t.Fatalf("deliver result must report merged:true tests_passed:true without a re-run: %+v", r)
	}
	w73NoRejection(t, d, wi)
	w.mergedInto(origin, branch)
}

// Scenario 2 (spec: "A conflict during the verification-time merge MUST be
// aborted, never resolved"): origin main changes the same file the branch
// changed. Verification's merge conflicts; it aborts, runs the bar on the
// unmerged tree and passes. `verified_heads` stays the pre-merge HEAD, no
// merge commit appears, and deliver then handles the conflict under its
// current rule.
func TestW73ConflictingVerificationMergeIsAborted(t *testing.T) {
	const qid = "w73-deliver-conflict"
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w73: a conflicting verification-time merge is aborted", "chore")
	conflict := `cd repos/demo && git fetch -q origin; ` +
		`if git merge --no-edit origin/main; then echo "w73: the scripted merge was expected to conflict" >&2; exit 9; fi; ` +
		`git merge --abort && ` + deliverTestBar
	inst.setPlanFull(t, wi, append(w73ChoreHead(origin),
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": verifiedWithTestBar(), "no_started_at": true,
			"run": []string{moveMain(origin, "work.txt", "main moved on"), conflict}},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "w73: merging origin/main conflicts; merge / don't merge / back to code?"},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true,
			"run": literalBranch(branch, claimRecord("demo",
				`"merged":false,"not_merged_reason":"merging origin/main conflicts: the human decides","tests_passed":true`), assemble)},
	), nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 180*time.Second)
	answerQuestion(t, d, wi, qid, "don't merge")
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)

	// (a) the shipped verification text carries the abort duty.
	w73MustSay(t, w73PromptOf(t, capDir, "verification"), "verification",
		"git fetch", "git merge --no-edit origin/", "git merge --abort")

	if v, _ := x.Steps["verification"].Output["verdict"].(string); v != "pass" {
		t.Fatalf("the gate must still pass on the unmerged tree: verdict %q (%+v)", v, x.Steps["verification"].Output)
	}
	repo := filepath.Join(capDir, "repos", "demo")
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if parents := strings.Fields(gitRun(t, repo, "rev-list", "--parents", "-n", "1", head)); len(parents) != 2 {
		t.Fatalf("%s HEAD %s is a merge commit (parents %v): the conflicting merge was resolved instead of aborted", repo, head, parents)
	}
	if merges := strings.TrimSpace(gitRun(t, repo, "log", "--merges", "--format=%H", "HEAD")); merges != "" {
		t.Fatalf("%s records a merge commit after the conflicting verification merge: %s", repo, merges)
	}
	if w73IsAncestor(repo, "origin/main", "HEAD") {
		t.Fatalf("%s HEAD %s claims to contain origin/main although the merge was aborted", repo, head)
	}
	if got := w73VerifiedHeads(x)["demo"]; got != head {
		t.Fatalf("verified_heads.demo = %v, want the unmerged HEAD %s (verified heads %v)", got, head, w73VerifiedHeads(x))
	}
	var aborted bool
	for _, cmd := range w73Commands(x, "verification") {
		if strings.Contains(cmd, "git merge --abort") {
			aborted = true
		}
	}
	if !aborted {
		t.Fatalf("the verification dispatch never recorded the abort: %v", w73Commands(x, "verification"))
	}
}

// Scenario 3 (spec: "The verification-time merge MUST be resumable from a
// crash without a half-merged proof"): verification fetches and merges the
// advanced main, then dies before its envelope. No verified_heads exists
// while the step is re-dispatched; the resumed dispatch repeats the fetch
// and merge (a no-op now) and runs the bar, and only its completed result
// stamps verified_heads on the final tree.
func TestW73InterruptedVerificationLeavesNoProof(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w73: an interrupted verification leaves no merged-head proof", "chore")
	inst.setPlanFull(t, wi, append(w73ChoreHead(origin),
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before",
			"run": []string{moveMain(origin, "other.txt", "another PR"), w73FetchMerge}},
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": verifiedWithTestBar(), "no_started_at": true, "ms": 4000,
			"run": []string{w73BarAfterMerge}},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true,
			"run":           literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	), nil)
	d := startDeliverDaemon(t, inst, w, wi)

	capDir := d.capsuleDir(wi)
	// The crash and its in-place resume: the resumed dispatch holds its
	// envelope for 4s, the window this assertion reads.
	w73WaitDispatches(t, capDir, "verification", 2, 150*time.Second)
	if x, err := readExecutionFile(capDir); err == nil && x.Steps["verification"] != nil {
		if heads, _ := x.Steps["verification"].Output["verified_heads"].(map[string]any); heads["demo"] != nil {
			t.Fatalf("SYMPTOM: verified_heads.demo = %v was stamped although the dispatch that merged died before completing", heads["demo"])
		}
	}

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	x := readExecution(t, capDir)

	// (a) the shipped verification text carries the restart-recovery duty.
	w73MustSay(t, w73PromptOf(t, capDir, "verification"), "resumed verification",
		"git fetch", "git merge --no-edit origin/", "resum")

	repo := filepath.Join(capDir, "repos", "demo")
	head := w73MachineMadeHead(t, repo)
	if !w73IsAncestor(repo, "origin/main", "HEAD") {
		t.Fatalf("%s HEAD %s does not contain origin/main: the resumed run proved an unmerged tree", repo, head)
	}
	if got := w73VerifiedHeads(x)["demo"]; got != head {
		t.Fatalf("verified_heads.demo = %v, want the final merged HEAD %s (verified heads %v)", got, head, w73VerifiedHeads(x))
	}
	var fetched, merged bool
	for _, cmd := range w73Commands(x, "verification") {
		if strings.Contains(cmd, "git fetch") {
			fetched = true
		}
		if strings.Contains(cmd, "git merge --no-edit origin/main") {
			merged = true
		}
	}
	if !fetched || !merged {
		t.Fatalf("the resumed verification dispatch did not repeat the fetch and merge: %v", w73Commands(x, "verification"))
	}
	w73NoRejection(t, d, wi)
}

// w73WaitDispatches waits until a step has at least n spawn-journal entries.
func w73WaitDispatches(t *testing.T, capDir, step string, n int, bound time.Duration) {
	t.Helper()
	waitUntil(t, bound, "step "+step+" was never dispatched twice (the crash and its re-dispatch)", func() bool {
		c := 0
		for _, e := range w73JournalLoose(capDir) {
			if s, _ := e["step_id"].(string); s == step {
				c++
			}
		}
		return c >= n
	})
}

// Scenario 4 (spec: "The proof MUST remain a same-dispatch, same-window
// proof and earlier dispatches MUST NOT count" — the verification-dispatch
// case): verification runs the carried bar in ITS dispatch; main then
// advances and deliver's merge moves HEAD past the verified head. Deliver
// records no re-run of the carried bar and claims tests_passed — the driver
// refuses the completed result, the PR stays open and a human is asked for
// the coverage reason. Verification's own bar evidence never covers it.
func TestW73VerificationBarIsNotADeliverCredential(t *testing.T) {
	const qid = "w73-deliver-merge"
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w73: verification's own bar run is not a deliver credential", "chore")
	inst.setPlanFull(t, wi, append(w73ChoreHead(origin),
		// verification runs the bar in this (earlier) dispatch, and origin/main
		// advances AFTER that bar (still before verification completes, so
		// verified_heads is the unmerged head this dispatch proved).
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": verifiedWithTestBar(), "no_started_at": true,
			"run": []string{deliverTestBarCmd, moveMain(origin, "other.txt", "another PR")}},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true,
			"run": literalBranch(branch, pushAndPR("demo"),
				claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble)},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "w73: the delivered head moved past the verified head and this dispatch did not re-run the test bar. merge / don't merge / back to code?"},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true,
			"run": literalBranch(branch, claimRecord("demo",
				`"merged":false,"not_merged_reason":"human: don't merge","tests_passed":true`), assemble)},
	), nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 240*time.Second)
	expectRejection(t, d, wi, "tests are not shown to pass")
	if w.prState("demo", "1") != "OPEN" || w.calls("gh pr merge") != 0 {
		t.Fatalf("the PR must stay open and unmerged while the coverage question is pending (state %q)", w.prState("demo", "1"))
	}
	capDir := d.capsuleDir(wi)
	x := readExecution(t, capDir)

	// (a) the shipped deliver text states that only this dispatch counts.
	w73MustSay(t, w73PromptOf(t, capDir, "deliver"), "deliver", "verification's own bar run")

	// verification's own bar evidence really is on record — and still did
	// not satisfy deliver's requirement.
	var earlier bool
	for _, cmd := range w73Commands(x, "verification") {
		if strings.Contains(cmd, "test -s work.txt") {
			earlier = true
		}
	}
	if !earlier {
		t.Fatalf("the scenario needs verification's own bar run on record: %v", w73Commands(x, "verification"))
	}
	repo := filepath.Join(capDir, "repos", "demo")
	head := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if got := w73VerifiedHeads(x)["demo"]; got == head {
		t.Fatalf("the delivered HEAD %s still equals verified_heads: the scenario did not move HEAD past the proof", got)
	}
	if !w73IsAncestor(repo, "origin/main", "HEAD") {
		t.Fatalf("%s HEAD %s does not contain the advanced origin/main", repo, head)
	}

	answerQuestion(t, d, wi, qid, "don't merge")
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
}
