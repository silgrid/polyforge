// carries: internal/polyforge/driver/*, internal/polyforge/cli/*, internal/polyforge/daemon/*
// carries: workflows/_shared/steps/*, workflows/feature-full/workflow.md

package test

// w260bar_deliver_resume_e2e_test.go: authored acceptance scenarios for the
// delivery side of the change (parts A's invariant and B of the work item):
// an opted-in full-tier delivery re-runs its carried full bar even when its
// merge leaves HEAD equal to the verified head, and a deliver step whose
// first dispatch dies mid-bar leaves daemon-recorded bar-run entries that a
// re-dispatch of the same run reuses, only for the bar keys still missing,
// while a run recorded at another head is never reused.
//
// Authored from artifacts/spec.md's `## Acceptance criteria` and `####
// Scenario` blocks BEFORE the implementation, through the real daemon + Go
// driver and the fake harness (test/fakepi as pi, the fake `gh`). Each
// scenario scripts the shipped chore lane and judges only records the spec
// names: the dispatched step's command evidence in execution.json, the
// daemon's durable events, the PR state, and the per-work-item bar-run
// record the spec requires the daemon to keep. No driver-internal symbol is
// referenced, so the baseline red is assertion-level (the bar-run entry
// point and the record do not exist yet), never a build failure.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW260Bar' -count=1 -v
//
// Every scenario prints one `PASS H<n>` marker line when it passes, so the
// verification step can bind each scenario to its own output line.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The carried test bar: two bar keys, one per entry, so a resumed run can be
// asked for exactly the missing one.
const (
	w260DelBarFast = "test -s work.txt"
	w260DelBarE2E  = "npm run test:e2e:all"
)

// w260DelBar is the carried test bar full-tier verification records.
func w260DelBar() []map[string]any {
	return []map[string]any{
		{"repo": "demo", "command": w260DelBarFast},
		{"repo": "demo", "command": w260DelBarE2E},
	}
}

// w260DelOptIn writes and commits the smoke-suite declaration that opts the
// delivered head into full-tier e2e selection. dPre leaves the shell in the
// repo, so the commit lands on the delivered branch before review passes.
const w260DelOptIn = `mkdir -p .polyforge && ` +
	`printf 'stack=npm\nsmoke_suite=e2e/run.mjs\n' > .polyforge/verify-scope.conf && ` +
	`git add -A && git commit -q -m "w260: declare the smoke suites"`

// w260DelBarRun runs one carried test_bar command through the deliver step's
// bar-run entry point, from the repo the bar names (the entry point records
// argv, cwd, exit, duration and HEAD).
func w260DelBarRun(cmd string) string {
	return `cd repos/demo && pf bar-run -- '` + cmd + `'`
}

// w260DelBarPush is the delivery's push half with the contract's no-op-merge
// skip: it merges origin/<default> only when the remote default branch has
// moved past HEAD, so a re-dispatch does not reset the re-run window.
func w260DelBarPush(repo string) string {
	return dPre(repo) + `git fetch -q origin; ` +
		`if ! git merge-base --is-ancestor origin/main HEAD; then git merge -q --no-edit origin/main; fi; ` +
		`git push -q origin "HEAD:refs/heads/$b"; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`[ -n "$url" ] || gh pr create --base main --head "$b" --title "w260 delivery" --body-file /dev/null`
}

// w260DelSettled is what one deliver run settled on while a scenario watches it:
// completion, a refused envelope, or an owner question.
type w260DelSettled struct {
	completed bool
	rejected  string
	question  map[string]any
}

// w260DelSettle polls until the delivery completes, an envelope is refused, or a
// question parks it — whichever first.
func w260DelSettle(t *testing.T, d *v1Daemon, wi string, bound time.Duration) w260DelSettled {
	t.Helper()
	var out w260DelSettled
	waitUntil(t, bound, "the delivery never settled (completed, refused envelope or owner question)", func() bool {
		if x, err := readExecutionFile(d.capsuleDir(wi)); err == nil {
			if x.WorkflowStatus == "completed" {
				out.completed = true
				return true
			}
			for _, st := range x.Steps {
				if st.PendingQuestion != nil {
					out.question = *st.PendingQuestion
					return true
				}
			}
		}
		for _, e := range d.eventsOf(t, wi) {
			if e.Kind == "envelope_rejected" {
				out.rejected = string(e.Body)
				return true
			}
		}
		return false
	})
	return out
}

// w260DelBarEntry is one daemon-recorded bar run.
type w260DelBarEntry struct {
	argv []string
	head string
	exit int
}

// w260DelBarRecords reads the work item's daemon-kept bar-run record (the record
// the spec requires to survive spawn death). It reads every line of every
// file under the daemon's bar-runs directory and skips a torn line, so the
// scenario does not depend on the storage format beyond the spec's fields.
func w260DelBarRecords(t *testing.T, d *v1Daemon) []w260DelBarEntry {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(d.dataDir, "bar-runs", "*.jsonl"))
	if err != nil {
		t.Fatalf("bar-runs dir: %v", err)
	}
	var out []w260DelBarEntry
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read bar-run record %s: %v", p, err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			e := w260DelBarEntry{}
			switch argv := m["argv"].(type) {
			case []any:
				for _, a := range argv {
					if s, ok := a.(string); ok {
						e.argv = append(e.argv, s)
					}
				}
			case string:
				e.argv = []string{argv}
			}
			e.head, _ = m["head"].(string)
			if v, ok := m["exit"].(float64); ok {
				e.exit = int(v)
			}
			out = append(out, e)
		}
	}
	return out
}

// w260DelBarEntryFor answers the record of one bar command (nil when absent).
func w260DelBarEntryFor(entries []w260DelBarEntry, cmd string) *w260DelBarEntry {
	for i := range entries {
		if strings.Contains(strings.Join(entries[i].argv, " "), cmd) {
			return &entries[i]
		}
	}
	return nil
}

// w260DelRepoHead answers the delivered repo's local HEAD.
func w260DelRepoHead(t *testing.T, capDir, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitRun(t, filepath.Join(capDir, "repos", repo), "rev-parse", "HEAD"))
}

// TestW260BarOptedInHeadEqualityReruns: a full-tier delivery of an opted-in
// repository whose merge leaves HEAD equal to the verified head MUST re-run
// its carried full bar inside the merge window; a result that claims
// tests_passed without that re-run is refused, and the re-dispatch that does
// run the bar is accepted.
func TestW260BarOptedInHeadEqualityReruns(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w260: an opted-in delivery re-runs its full bar at the verified head", "chore")

	// spawn 1: push, then claim tests_passed without any bar re-run.
	claim := literalBranch(branch, pushAndPR("demo"),
		claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble)
	// spawn 2 (the refused envelope's re-dispatch): re-run the whole carried
	// bar through the bar-run entry point, then merge the PR.
	rerun := literalBranch(branch, w260DelBarPush("demo"),
		w260DelBarRun(w260DelBarFast), w260DelBarRun(w260DelBarE2E),
		mergeRecord("demo", ""), assemble)

	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"},
			"run": []string{dPre("demo") + w260DelOptIn}},
		{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": map[string]any{"verdict": "pass", "test_bar": w260DelBar()}},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true, "run": claim},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true, "run": rerun},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	first := w260DelSettle(t, d, wi, 240*time.Second)
	if first.completed && first.rejected == "" {
		t.Fatalf("SYMPTOM: the opted-in full-tier delivery was accepted on the HEAD-equality shortcut " +
			"without re-running its carried full bar; a full-tier delivery of an opted-in repository must " +
			"record the full-bar re-run inside its merge window even when HEAD equals the verified head")
	}
	if first.question != nil {
		t.Fatalf("the delivery asked a human instead of re-dispatching to re-run its bar: %v", first.question)
	}
	low := strings.ToLower(first.rejected)
	if !strings.Contains(low, "test") && !strings.Contains(low, "bar") {
		t.Fatalf("the first deliver result must be refused because the full bar was not re-run, got %q", first.rejected)
	}

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if q := pendingQuestion(t, d.capsuleDir(wi)); q != nil {
		t.Fatalf("the delivery parked on a question although the full bar was re-run: %v", q)
	}
	if w.prState("demo", "1") != "MERGED" {
		t.Fatalf("PR state %q after the re-run delivery, want MERGED", w.prState("demo", "1"))
	}
	if n := w.calls("gh pr merge"); n != 1 {
		t.Fatalf("gh pr merge ran %d times, want exactly 1 (the shortcut proof must not merge twice)", n)
	}
	if n := stepDispatches(t, d.capsuleDir(wi), "deliver"); n < 2 {
		t.Fatalf("deliver was dispatched %d time(s): the compliant re-dispatch that runs the bar is missing", n)
	}
	cmds := w73Commands(readExecution(t, d.capsuleDir(wi)), "deliver")
	for _, want := range []string{"pf bar-run -- '" + w260DelBarFast + "'", "pf bar-run -- '" + w260DelBarE2E + "'"} {
		found := false
		for _, c := range cmds {
			if strings.Contains(c, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("the accepted deliver dispatch never ran %q through the bar-run entry point: %v", want, cmds)
		}
	}
	fmt.Println("PASS H1")
}

// TestW260BarReuseAcrossSpawnDeath: a first deliver dispatch that records a
// fast-gate bar run at the pushed head and then dies before the e2e bar
// leaves a daemon record; a re-dispatch of the same run (origin unmoved, no
// new merge) reuses that run, runs only the missing bar key, and the merge
// still passes the unchanged window/key/one-use rules.
func TestW260BarReuseAcrossSpawnDeath(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w260: a re-dispatch reuses the recorded bar run of a dead spawn", "chore")

	// spawn 1: merge the moved origin, push, record the fast-gate bar run at
	// the pushed head, then die before the e2e bar.
	died := literalBranch(branch, pushAndPR("demo"), w260DelBarRun(w260DelBarFast))
	// spawn 2: origin did not move, so no merge; push the same head, run only
	// the missing e2e bar key, then merge the PR.
	resume := literalBranch(branch, w260DelBarPush("demo"), w260DelBarRun(w260DelBarE2E),
		mergeRecord("demo", ""), assemble)

	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		// origin/main moves on during verification, so deliver's merge moves
		// HEAD past the verified head and a bar re-run is owed.
		{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": map[string]any{"verdict": "pass", "test_bar": w260DelBar()},
			"run":    []string{moveMain(origin, "other.txt", "another PR")}},
		{"do": "envelope", "id": "deliver", "run": died, "crash": "before"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true, "run": resume},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	settled := w260DelSettle(t, d, wi, 300*time.Second)
	if settled.rejected != "" {
		t.Fatalf("SYMPTOM: the re-dispatch's delivery was refused (%s) although the first dispatch had "+
			"recorded its fast-gate bar run at the pushed head and no merge moved it; the recorded run must "+
			"count as re-run and only the missing bar key must run again", settled.rejected)
	}
	if settled.question != nil {
		t.Fatalf("the resumed delivery asked a human instead of reusing its recorded bar run: %v", settled.question)
	}
	if !settled.completed {
		t.Fatalf("the resumed delivery did not complete")
	}
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if w.prState("demo", "1") != "MERGED" {
		t.Fatalf("PR state %q, want MERGED (the merge must still pass the unchanged window/key rules)", w.prState("demo", "1"))
	}
	if n := w.calls("gh pr merge"); n != 1 {
		t.Fatalf("gh pr merge ran %d times, want exactly 1", n)
	}

	capDir := d.capsuleDir(wi)
	cmds := w73Commands(readExecution(t, capDir), "deliver")
	for _, c := range cmds {
		if strings.Contains(c, "pf bar-run -- '"+w260DelBarFast+"'") {
			t.Fatalf("the resumed dispatch re-ran the fast-gate bar command the dead spawn had already recorded: %v", cmds)
		}
	}
	missing := false
	for _, c := range cmds {
		if strings.Contains(c, "pf bar-run -- '"+w260DelBarE2E+"'") {
			missing = true
		}
	}
	if !missing {
		t.Fatalf("the resumed dispatch did not run the missing e2e bar key: %v", cmds)
	}
	head := w260DelRepoHead(t, capDir, "demo")
	recs := w260DelBarRecords(t, d)
	fast := w260DelBarEntryFor(recs, w260DelBarFast)
	e2e := w260DelBarEntryFor(recs, w260DelBarE2E)
	if fast == nil || e2e == nil {
		t.Fatalf("the daemon record must hold both completed bar runs across the spawn boundary, got %d entries", len(recs))
	}
	if fast.head != head || e2e.head != head {
		t.Fatalf("the recorded runs must be at the pushed head %s, got fast=%s e2e=%s", head, fast.head, e2e.head)
	}
	if fast.exit != 0 || e2e.exit != 0 {
		t.Fatalf("the recorded runs must show exit 0, got fast=%d e2e=%d", fast.exit, e2e.exit)
	}
	fmt.Println("PASS H2")
}

// TestW260BarAdvancedOriginNoReuse: when the remote default branch advances
// between the two spawns of one deliver run, the earlier spawn's recorded bar
// run is at another head and MUST NOT be reused; the re-dispatch that runs
// only the missing key is refused, and the full bar on the new head is owed.
func TestW260BarAdvancedOriginNoReuse(t *testing.T) {
	const qid = "w260-bar-advanced-origin"
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w260: a bar run recorded at another head is never reused", "chore")

	// spawn 1: merge the moved origin, push, record the fast bar at that head,
	// then die.
	// origin then advances again while this spawn is still alive (its
	// evidence is never judged: it dies before any envelope).
	died := literalBranch(branch, pushAndPR("demo"), w260DelBarRun(w260DelBarFast),
		moveMain(origin, "second.txt", "a second PR"))
	// spawn 2: origin advances again, so the contract's skip does not apply:
	// merge the advanced branch (a new head), run only the missing e2e key and
	// claim tests_passed — the recorded fast run is at the OLD head.
	lazy := literalBranch(branch,
		w260DelBarPush("demo"), w260DelBarRun(w260DelBarE2E),
		claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble)

	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "verification", "outcome": "completed",
			"output": map[string]any{"verdict": "pass", "test_bar": w260DelBar()},
			"run":    []string{moveMain(origin, "first.txt", "a first PR")}},
		{"do": "envelope", "id": "deliver", "run": died, "crash": "before"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true, "run": lazy},
		{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "w260: the bar run recorded by the earlier dispatch is at the pre-merge head; run the full bar on the merged head? merge / don't merge / back to code?"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"no_started_at": true, "run": literalBranch(branch, mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	capDir := d.capsuleDir(wi)
	// The first spawn completed its fast-gate bar run and died; the daemon must
	// have recorded it before the re-dispatch reconciles anything.
	w73WaitDispatches(t, capDir, "deliver", 2, 180*time.Second)
	recs := w260DelBarRecords(t, d)
	fast := w260DelBarEntryFor(recs, w260DelBarFast)
	if fast == nil {
		t.Fatalf("SYMPTOM: the daemon recorded no bar run for the first spawn's completed fast-gate command; "+
			"the bar-run record must outlive the spawn (entries %d)", len(recs))
	}
	if fast.head == "" || fast.exit != 0 {
		t.Fatalf("the recorded fast bar run must carry its HEAD and exit 0: %+v", *fast)
	}

	// The re-dispatch merged the advanced default branch and ran only the
	// missing key; the recorded run is at another head, so it is no credential.
	settled := w260DelSettle(t, d, wi, 300*time.Second)
	if settled.completed {
		t.Fatalf("SYMPTOM: the lazy re-dispatch was accepted although it reused nothing legitimately: "+
			"the fast bar run recorded at head %s is not the delivered head, so the full bar was owed", fast.head)
	}
	if settled.rejected != "" {
		low := strings.ToLower(settled.rejected)
		if !strings.Contains(low, "test") && !strings.Contains(low, "bar") {
			t.Fatalf("the delivery must be refused because the full bar was not re-run on the new head, got %q", settled.rejected)
		}
	}
	if settled.question == nil {
		waitQuestion(t, d, wi, qid, 180*time.Second)
	}
	if w.prState("demo", "1") != "OPEN" || w.calls("gh pr merge") != 0 {
		t.Fatalf("the PR must stay open and unmerged while the full-bar question is pending (state %q)", w.prState("demo", "1"))
	}
	head := w260DelRepoHead(t, capDir, "demo")
	e2e := w260DelBarEntryFor(w260DelBarRecords(t, d), w260DelBarE2E)
	if e2e == nil || e2e.head != head {
		t.Fatalf("the missing bar key must be recorded at the new merged head %s: %+v", head, e2e)
	}
	if fast.head == head {
		t.Fatalf("the scenario needs two different heads: the recorded fast run and the delivered head are both %s", head)
	}

	answerQuestion(t, d, wi, qid, "merge")
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	fmt.Println("PASS H3")
}
