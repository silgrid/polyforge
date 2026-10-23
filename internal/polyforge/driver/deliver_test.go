package driver

// deliver_test.go: the delivery binding (polyforge#166) — the command
// rule over the deliver step's recorded evidence (push only the feature
// branch, merge only with --merge, no main-push override) and the merge
// rule (auto-merge only under a passed review, passing tests and no
// LLM-resolved conflict; anything else, and every unmerged PR, owes a
// human answer).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// tgit runs git in dir for test fixtures: no global/system config, a
// fixed identity.
func tgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// deliverCapsule lays <tmp>/polyforge-166/repos/demo (a real repo with
// one commit) and answers the ReposDir, so the feature branch is
// polyforge/polyforge-166.
func deliverCapsule(t *testing.T) string {
	t.Helper()
	repos := filepath.Join(t.TempDir(), "polyforge-166", "repos")
	dir := filepath.Join(repos, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git")
	tgit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	tgit(t, dir, "init", "-q", "-b", "main")
	tgit(t, dir, "remote", "add", "origin", bare)
	dwrite(t, filepath.Join(dir, "base.txt"), "base\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "baseline")
	tgit(t, dir, "push", "-q", "origin", "main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "remote", "set-head", "origin", "main")
	dwrite(t, filepath.Join(dir, "a.txt"), "a\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "work")
	pushFeature(t, dir)
	return repos
}

// pushFeature pushes HEAD to the feature branch (moving
// refs/remotes/origin/polyforge/polyforge-166 with it).
func pushFeature(t *testing.T, dir string) {
	t.Helper()
	tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/polyforge/polyforge-166")
}

func dwrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// deliverSpec: verification passed on repos/demo's current HEAD.
func deliverSpec(repos string, reviewPassed bool, answers int) ResultSpec {
	head, _ := repoGit(filepath.Join(repos, "demo"), "rev-parse", "HEAD")
	return ResultSpec{WIID: "wi_d", StepID: deliverStepID, SpawnN: 1, Gate: true,
		RequireCommandEvidence: true, ReposDir: repos, ReviewPassed: reviewPassed, HasReview: true, HumanAnswers: answers,
		VerifiedHeads:   map[string]string{"demo": strings.TrimSpace(head)},
		VerifiedTestBar: []testBarCmd{{Repo: "demo", Command: "make test"}}}
}

func ev(cmds ...string) []CommandEvidence {
	var out []CommandEvidence
	for _, c := range cmds {
		out = append(out, CommandEvidence{Argv: []string{"sh", "-c", c}, DurationMs: 5})
	}
	return out
}

const goodPush = "cd repos/demo && git push origin HEAD:refs/heads/polyforge/polyforge-166"

func deliverRes(outcome Outcome, output string, evidence []CommandEvidence) *StepResult {
	return &StepResult{WIID: "wi_d", StepID: deliverStepID, SpawnN: 1, Outcome: outcome,
		Output: json.RawMessage(output), CommandEvidence: rawEvidence(evidence)}
}

const mergedOut = `{"verdict":"pass","repos":[{"name":"demo","branch":"polyforge/polyforge-166","pr_url":"https://github.com/o/r/pull/1","merged":true,"merge_commit":"abc","tests_passed":true}]}`

func TestDeliverCommandRule(t *testing.T) {
	repos := deliverCapsule(t)
	cases := []struct {
		name string
		cmds []string
		bad  string // "" = accepted; else a substring of the rejection
	}{
		{"feature branch push", []string{goodPush, "gh pr merge https://github.com/o/r/pull/1 --merge"}, ""},
		{"argv-form push", nil, ""},
		{"git -C push", []string{"git -C repos/demo push -u origin HEAD:polyforge/polyforge-166"}, ""},
		{"push main", []string{"cd repos/demo && git push origin HEAD:refs/heads/main"}, "not the work item's feature branch"},
		{"push main by name", []string{"git push origin main"}, "not the work item's feature branch"},
		{"push without refspec", []string{"cd repos/demo; git push"}, "without an explicit refspec"},
		{"push remote only", []string{"git push origin"}, "without an explicit refspec"},
		{"other feature branch", []string{"git push origin HEAD:polyforge/other-1"}, "not the work item's feature branch"},
		{"force", []string{"git push --force origin HEAD:polyforge/polyforge-166"}, "--force"},
		{"force short", []string{"git push -f origin HEAD:polyforge/polyforge-166"}, "force/delete"},
		{"force lease", []string{"git push --force-with-lease=x origin HEAD:polyforge/polyforge-166"}, "--force-with-lease"},
		{"plus refspec", []string{"git push origin +HEAD:polyforge/polyforge-166"}, "forced refspec"},
		{"delete refspec", []string{"git push origin :polyforge/polyforge-166"}, "deleting refspec"},
		{"all", []string{"git push --all origin"}, "--all"},
		{"tags", []string{"git push --tags origin HEAD:polyforge/polyforge-166"}, "--tags"},
		{"tag refspec", []string{"git push origin refs/tags/v1.0.0"}, "not the work item's feature branch"},
		{"main override", []string{"PF_ALLOW_MAIN_PUSH=1 git push origin HEAD:polyforge/polyforge-166"}, "PF_ALLOW_MAIN_PUSH"},
		{"quoted main push in a script", []string{`bash -c "cd repos/demo && git push origin 'HEAD:main'"`}, "not the work item's feature branch"},
		{"squash merge", []string{"gh pr merge 1 --squash"}, "--squash"},
		{"rebase merge", []string{"gh pr merge 1 --merge --rebase"}, "--rebase"},
		{"admin merge", []string{"gh pr merge 1 --merge --admin"}, "--admin"},
		{"merge method missing", []string{"gh pr merge 1"}, "without --merge"},
		// command position only: words that merely mention the commands
		{"pr title mentions git push", []string{`gh pr create --title "polyforge#166: deliver does git push only to the feature branch" --body-file b.md`}, ""},
		{"echo mentions gh pr merge", []string{`echo "next: gh pr merge 1 --squash"`}, ""},
		{"grep for the override", []string{"grep -rn PF_ALLOW_MAIN_PUSH ."}, ""},
		{"if wrapper", []string{"if git push origin HEAD:main; then echo ok; fi"}, "not the work item's feature branch"},
		{"env override", []string{"env PF_ALLOW_MAIN_PUSH=1 git push origin HEAD:polyforge/polyforge-166"}, "PF_ALLOW_MAIN_PUSH"},
		{"exported override", []string{"export PF_ALLOW_MAIN_PUSH=1; git push origin HEAD:polyforge/polyforge-166"}, "PF_ALLOW_MAIN_PUSH"},
		{"git-dir option", []string{"git --git-dir repos/demo/.git push origin main"}, "not the work item's feature branch"},
		{"hooks bypass", []string{"git push --no-verify origin HEAD:polyforge/polyforge-166"}, "--no-verify"},
		{"hooksPath override", []string{"git -c core.hooksPath=/dev/null push origin HEAD:polyforge/polyforge-166"}, "core.hooksPath"},
		{"gh repo flag squash", []string{"gh -R o/r pr merge 1 --squash"}, "--squash"},
		{"gh auto merge", []string{"gh pr merge 1 --merge --auto"}, "--auto"},
		{"gh repo sync", []string{"gh repo sync o/r"}, "gh repo sync"},
		// redirections are not refspecs
		{"stderr redirect", []string{"git push origin HEAD:refs/heads/polyforge/polyforge-166 2>&1 | tail -5"}, ""},
		{"stdout to null", []string{"git push -q origin HEAD:refs/heads/polyforge/polyforge-166 >/dev/null"}, ""},
		{"bare redirect", []string{"git push origin HEAD:refs/heads/polyforge/polyforge-166 > push.log 2> err.log"}, ""},
		{"both redirect", []string{"git push origin HEAD:refs/heads/polyforge/polyforge-166 &>push.log"}, ""},
		{"redirect does not hide main", []string{"git push origin HEAD:main 2>&1"}, "not the work item's feature branch"},
		// abbreviated long options git accepts
		{"abbreviated force", []string{"git push --forc origin HEAD:polyforge/polyforge-166"}, "--force"},
		{"abbreviated delete", []string{"git push --del origin polyforge/polyforge-166"}, "--delete"},
		{"abbreviated no-verify", []string{"git push --no-ver origin HEAD:polyforge/polyforge-166"}, "--no-verify"},
		{"allowed long options", []string{"git push --set-upstream --porcelain --atomic origin HEAD:polyforge/polyforge-166"}, ""},
		// git configuration changed for the push
		{"global config swapped", []string{"GIT_CONFIG_GLOBAL=/tmp/x git push origin HEAD:polyforge/polyforge-166"}, "GIT_CONFIG_GLOBAL"},
		{"git dir swapped", []string{"GIT_DIR=/tmp/x.git git push origin HEAD:polyforge/polyforge-166"}, "GIT_DIR"},
		{"ssh command and prompt allowed", []string{"GIT_TERMINAL_PROMPT=0 GIT_SSH_COMMAND='ssh -o BatchMode=yes' git push origin HEAD:polyforge/polyforge-166"}, ""},
		{"index file swapped", []string{"GIT_TERMINAL_PROMPT=0 GIT_INDEX_FILE=/tmp/i git push origin HEAD:polyforge/polyforge-166"}, "GIT_INDEX_FILE"},
		{"admin as value form", []string{"gh pr merge 1 --merge --admin=true"}, "--admin=true"},
		{"auto as value form", []string{"gh pr merge 1 --merge --auto=true"}, "--auto=true"},
		{"squash as value form", []string{"gh pr merge 1 --squash=true"}, "--squash=true"},
		{"admin turned off", []string{"gh pr merge 1 --merge --admin=false"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evidence := ev(c.cmds...)
			if c.name == "argv-form push" {
				evidence = []CommandEvidence{{Argv: []string{"git", "-C", "repos/demo", "push", "origin", "HEAD:refs/heads/polyforge/polyforge-166"}, DurationMs: 3}}
			}
			err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, evidence), deliverSpec(repos, true, 0))
			switch {
			case c.bad == "" && err != nil:
				t.Fatalf("rejected: %v", err)
			case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
				t.Fatalf("want rejection containing %q, got %v", c.bad, err)
			}
		})
	}
	// The command rule judges every delivered outcome that carries
	// evidence, not just completed ones.
	q := deliverRes(OutcomeNeedsInput, `{}`, ev("git push origin HEAD:main"))
	q.QuestionID, q.Question = "deliver-merge", "merge?"
	if err := ValidateStepResult(q, deliverSpec(repos, true, 0)); err == nil {
		t.Fatal("a needs_input deliver envelope that pushed main was accepted")
	}
}

func TestDeliverMergeRule(t *testing.T) {
	repos := deliverCapsule(t)
	out := func(repo string) string {
		return `{"verdict":"pass","repos":[` + repo + `]}`
	}
	const base = `"name":"demo","branch":"polyforge/polyforge-166","pr_url":"https://github.com/o/r/pull/1"`
	cases := []struct {
		name    string
		output  string
		review  bool
		answers int
		bad     string
	}{
		{"auto merge", mergedOut, true, 0, ""},
		{"llm-resolved conflict needs a human", out(`{` + base + `,"merged":true,"merge_commit":"abc","tests_passed":true,"llm_resolved_conflict":true}`), true, 0, "LLM-resolved conflict"},
		{"llm-resolved conflict after a human answer", out(`{` + base + `,"merged":true,"merge_commit":"abc","tests_passed":true,"llm_resolved_conflict":true}`), true, 1, ""},
		{"no passed review needs a human", mergedOut, false, 0, "independent review"},
		{"no passed review after a human answer", mergedOut, false, 1, ""},
		{"failed tests need a human", out(`{` + base + `,"merged":true,"merge_commit":"abc","tests_passed":false}`), true, 0, "reports failing tests"},
		{"unmerged needs a human", out(`{` + base + `,"merged":false,"not_merged_reason":"human said no"}`), true, 0, "left unmerged"},
		{"unmerged after a human answer", out(`{` + base + `,"merged":false,"not_merged_reason":"human said no"}`), true, 1, ""},
		{"unmerged without reason", out(`{` + base + `,"merged":false}`), true, 1, "not_merged_reason"},
		{"merged without merge commit", out(`{` + base + `,"merged":true,"tests_passed":true}`), true, 0, "merge_commit"},
		{"merged unknown", out(`{` + base + `}`), true, 0, "merged true or false"},
		{"no pr", out(`{"name":"demo","branch":"polyforge/polyforge-166","merged":true,"merge_commit":"a","tests_passed":true}`), true, 0, "pr_url"},
		{"wrong branch", out(`{"name":"demo","branch":"main","pr_url":"u","merged":true,"merge_commit":"a","tests_passed":true}`), true, 0, "feature branch"},
		{"declared repo missing", out(`{"name":"other","skipped_reason":"nothing"}`), true, 0, `declared repo "demo"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateStepResult(deliverRes(OutcomeCompleted, c.output, ev(goodPush)), deliverSpec(repos, c.review, c.answers))
			switch {
			case c.bad == "" && err != nil:
				t.Fatalf("rejected: %v", err)
			case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
				t.Fatalf("want rejection containing %q, got %v", c.bad, err)
			}
		})
	}
	// "back to code" is a fail verdict: re-bound to gate_failed (the rewind),
	// never judged by the merge rule.
	res := deliverRes(OutcomeCompleted, `{"verdict":"fail","fail_reason":"human: back to code"}`, ev(goodPush))
	if err := ValidateStepResult(res, deliverSpec(repos, true, 1)); err != nil || res.Outcome != OutcomeGateFailed {
		t.Fatalf("back-to-code verdict: err=%v outcome=%s", err, res.Outcome)
	}
}

func TestReviewPassed(t *testing.T) {
	done := func(out map[string]any) *capsule.Step { return &capsule.Step{Status: capsule.StepDone, Output: out} }
	cases := []struct {
		name string
		st   *capsule.Step
		want bool
	}{
		{"no review step", nil, false},
		{"pass", done(map[string]any{"verdict": "pass"}), true},
		{"pass with non-blocking", done(map[string]any{"verdict": "pass", "findings": []any{map[string]any{"severity": "non-blocking"}}}), true},
		{"pass with blocking", done(map[string]any{"verdict": "pass", "findings": []any{map[string]any{"severity": "blocking"}}}), false},
		{"fail", done(map[string]any{"verdict": "fail"}), false},
		{"not done", &capsule.Step{Status: "in_progress", Output: map[string]any{"verdict": "pass"}}, false},
	}
	for _, c := range cases {
		if got := reviewPassed(c.st); got != c.want {
			t.Errorf("%s: reviewPassed = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDeliverMergeFacts: the merge rule's facts come from the capsule, not
// the step's self-report (polyforge#166 review B1).
func TestDeliverMergeFacts(t *testing.T) {
	// moved builds a repo whose HEAD moved past the verified commit by a
	// merge of a side branch; resolve hand-edits the merge result.
	moved := func(t *testing.T, conflict, handEdit bool) (string, ResultSpec) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		base := tgit(t, dir, "rev-parse", "HEAD~0")
		tgit(t, dir, "checkout", "-q", "-b", "side", base)
		if conflict {
			dwrite(t, filepath.Join(dir, "a.txt"), "side\n")
		} else {
			dwrite(t, filepath.Join(dir, "b.txt"), "b\n")
		}
		tgit(t, dir, "add", "-A")
		tgit(t, dir, "commit", "-q", "-m", "side")
		tgit(t, dir, "checkout", "-q", "main")
		dwrite(t, filepath.Join(dir, "a.txt"), "main\n")
		tgit(t, dir, "commit", "-q", "-am", "main work")
		spec.VerifiedHeads["demo"] = tgit(t, dir, "rev-parse", "HEAD")
		pushFeature(t, dir)
		if conflict {
			cmd := exec.Command("git", "merge", "-q", "--no-edit", "side")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@i", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@i")
			_ = cmd.Run() // conflicts
			dwrite(t, filepath.Join(dir, "a.txt"), "main\nside\n")
			tgit(t, dir, "add", "-A")
			tgit(t, dir, "commit", "-q", "--no-edit")
		} else {
			tgit(t, dir, "merge", "-q", "--no-edit", "side")
			if handEdit {
				dwrite(t, filepath.Join(dir, "b.txt"), "edited\n")
				tgit(t, dir, "add", "-A")
				tgit(t, dir, "commit", "-q", "--amend", "--no-edit")
			}
		}
		pushFeature(t, dir)
		return repos, spec
	}
	rerun := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && make test"}, DurationMs: 9}}
	// the test bar ran, then the merge: not after the last merge
	stale := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && make test"}, DurationMs: 9},
		{Argv: []string{"git", "-C", "repos/demo", "merge", "--no-edit", "side"}, DurationMs: 9}}
	// the merge, then the test bar — but in another repo, or failing
	otherRepo := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && git merge --no-edit side"}, DurationMs: 9},
		{Argv: []string{"sh", "-c", "cd repos/other && make test"}, DurationMs: 9}}
	failing := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && make   test"}, DurationMs: 9, ExitCode: 2}}
	ordered := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && git merge -q --no-edit side 2>&1"}, DurationMs: 9},
		{Argv: []string{"make", "test"}, Cwd: "repos/demo", DurationMs: 9}}
	push := ev(goodPush)
	cases := []struct {
		name               string
		conflict, handEdit bool
		evidence           []CommandEvidence
		output             string
		bad                string
	}{
		{"clean merge, tests not re-run", false, false, push, mergedOut, "tests are not shown to pass"},
		{"clean merge, tests re-run before the merge", false, false, append(push, stale...), mergedOut, "tests are not shown to pass"},
		{"clean merge, tests re-run after it", false, false, append(push, rerun...), mergedOut, ""},
		{"clean merge, test bar in another repo", false, false, append(push, otherRepo...), mergedOut, "tests are not shown to pass"},
		{"clean merge, test bar failing", false, false, append(push, failing...), mergedOut, "tests are not shown to pass"},
		{"clean merge, merge then test bar by cwd", false, false, append(push, ordered...), mergedOut, ""},
		{"hand-resolved conflict, flag says no conflict", true, false, append(push, rerun...), mergedOut, "had conflicts"},
		{"clean merge edited by hand", false, true, append(push, rerun...), mergedOut, "differs from git's own merge result"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repos, spec := moved(t, c.conflict, c.handEdit)
			err := ValidateStepResult(deliverRes(OutcomeCompleted, c.output, c.evidence), spec)
			switch {
			case c.bad == "" && err != nil:
				t.Fatalf("rejected: %v", err)
			case c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)):
				t.Fatalf("want rejection containing %q, got %v", c.bad, err)
			}
			if c.bad != "" { // the same result passes once a human answered
				spec.HumanAnswers = 1
				if err := ValidateStepResult(deliverRes(OutcomeCompleted, c.output, c.evidence), spec); err != nil {
					t.Fatalf("rejected after a human answer: %v", err)
				}
			}
			_ = repos
		})
	}
	t.Run("non-merge commit after verification", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		dwrite(t, filepath.Join(dir, "a.txt"), "changed in deliver\n")
		tgit(t, dir, "commit", "-q", "-am", "deliver edit")
		err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, append(push, rerun...)), spec)
		if err == nil || !strings.Contains(err.Error(), "not a two-parent merge") {
			t.Fatalf("want a hand-made-change rejection, got %v", err)
		}
	})
	t.Run("no verification record", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		spec.VerifiedHeads = nil
		err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, push), spec)
		if err == nil || !strings.Contains(err.Error(), "no verification record") {
			t.Fatalf("want a missing-record rejection, got %v", err)
		}
	})
	t.Run("skipped repo", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		skipped := `{"verdict":"pass","repos":[{"name":"demo","skipped_reason":"nothing to deliver"}]}`
		capJSON := filepath.Join(filepath.Dir(repos), capsule.CapsuleJSON)
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, skipped, push), spec); err == nil || !strings.Contains(err.Error(), "baseline is not recorded") {
			t.Fatalf("skip without a baseline: %v", err)
		}
		head := spec.VerifiedHeads["demo"]
		dwrite(t, capJSON, `{"repos":{"demo":"`+head+`"}}`)
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, skipped, push), spec); err != nil {
			t.Fatalf("skip at the baseline rejected: %v", err)
		}
		dir := filepath.Join(repos, "demo")
		dwrite(t, filepath.Join(dir, "a.txt"), "more\n")
		tgit(t, dir, "commit", "-q", "-am", "more")
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, skipped, push), spec); err == nil || !strings.Contains(err.Error(), "beyond its baseline") {
			t.Fatalf("skip with commits: %v", err)
		}
	})
}

// TestDeliverDeliveredHead: the pushed and merged head is tied to the
// local HEAD the facts were derived from.
func TestDeliverDeliveredHead(t *testing.T) {
	push := ev(goodPush)
	t.Run("HEAD moved after the push", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		tgit(t, dir, "commit", "-q", "--allow-empty", "-m", "after the push")
		spec.VerifiedHeads["demo"] = tgit(t, dir, "rev-parse", "HEAD")
		err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, push), spec)
		if err == nil || !strings.Contains(err.Error(), "is not the local HEAD") {
			t.Fatalf("want a pushed-head mismatch, got %v", err)
		}
	})
	t.Run("not delivered from the latest default branch", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		// origin main moves on; the fetch sees it, HEAD never merged it
		other := filepath.Join(t.TempDir(), "other")
		tgit(t, filepath.Dir(other), "clone", "-q", filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git"), other)
		tgit(t, other, "commit", "-q", "--allow-empty", "-m", "another PR")
		tgit(t, other, "push", "-q", "origin", "HEAD:main")
		tgit(t, dir, "fetch", "-q", "origin")
		err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, push), spec)
		if err == nil || !strings.Contains(err.Error(), "not delivered from the latest default branch") {
			t.Fatalf("want a stale-main rejection, got %v", err)
		}
	})
	t.Run("already merged", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		// the PR merge lands on origin main (a --no-ff merge of HEAD)
		other := filepath.Join(t.TempDir(), "other")
		tgit(t, filepath.Dir(other), "clone", "-q", filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git"), other)
		tgit(t, other, "merge", "-q", "--no-ff", "-m", "Merge pull request #1", "origin/polyforge/polyforge-166")
		tgit(t, other, "push", "-q", "origin", "HEAD:main")
		mc := tgit(t, other, "rev-parse", "HEAD")
		tgit(t, dir, "fetch", "-q", "origin")
		out := strings.Replace(mergedOut, `"merge_commit":"abc"`, `"merge_commit":"`+mc+`"`, 1)
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, out, push), spec); err != nil {
			t.Fatalf("an already-merged PR of HEAD was refused: %v", err)
		}
		// a recorded merge commit that did not merge HEAD
		bad := strings.Replace(mergedOut, `"merge_commit":"abc"`, `"merge_commit":"`+tgit(t, other, "rev-parse", "HEAD^1")+`"`, 1)
		if err := ValidateStepResult(deliverRes(OutcomeCompleted, bad, push), spec); err == nil || !strings.Contains(err.Error(), "did not merge the local HEAD") {
			t.Fatalf("want a merge-commit mismatch, got %v", err)
		}
	})
}

// TestDeliverAutopilotEligibility: a workflow without a review step
// (autopilot) auto-merges on verification's pass when the mechanical facts
// hold; a workflow with one still needs the review passed.
func TestDeliverAutopilotEligibility(t *testing.T) {
	repos := deliverCapsule(t)
	quick := deliverSpec(repos, false, 0)
	quick.HasReview = false
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), quick); err != nil {
		t.Fatalf("autopilot on the verified commit refused: %v", err)
	}
	withReview := deliverSpec(repos, false, 0)
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), withReview); err == nil || !strings.Contains(err.Error(), "independent review") {
		t.Fatalf("a workflow with a review step merged without it: %v", err)
	}
}

// polyforge#164 x #166: the authored-tests gate (H4) does not judge the
// deliver step. Merging the latest default branch, which also edited an
// authored test file, changes that file's blob since the author commit;
// gate 7 would refuse the delivery and tell the agent to restore the
// author commit's version, undoing upstream's edit.
func TestDeliverExemptFromAuthoredTestsGate(t *testing.T) {
	repos := deliverCapsule(t)
	dir := filepath.Join(repos, "demo")
	up := filepath.Join(t.TempDir(), "up")
	tgit(t, filepath.Dir(up), "clone", "-q", filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git"), up)
	upstream := func(body, msg string) {
		dwrite(t, filepath.Join(up, "x_test.go"), body)
		tgit(t, up, "add", "-A")
		tgit(t, up, "commit", "-q", "-m", msg)
		tgit(t, up, "push", "-q", "origin", "HEAD:main")
	}
	// The test file exists on main; the work item picks it up, then
	// e2e-author adds its acceptance test to it (verification passes there).
	upstream("base\n", "upstream adds x_test.go")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
	dwrite(t, filepath.Join(dir, "x_test.go"), "base\nauthored\n")
	tgit(t, dir, "commit", "-q", "-am", "author tests")
	authored := tgit(t, dir, "rev-parse", "HEAD")
	verified := authored
	pushFeature(t, dir)
	// main moves on and edits the same file; deliver merges it (clean).
	upstream("upstream\nbase\n", "upstream edits x_test.go")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
	pushFeature(t, dir)
	if got, _ := os.ReadFile(filepath.Join(dir, "x_test.go")); string(got) != "upstream\nbase\nauthored\n" {
		t.Fatalf("fixture: want a clean merge of both edits, got %q", got)
	}

	// The driver's spec for deliver carries no authored-tests baseline...
	capsuleDir := filepath.Dir(repos)
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_d", CapsuleDir: capsuleDir, BundleName: "b"},
		ExecPath: filepath.Join(capsuleDir, capsule.ExecutionJSON),
		Workflow: &Workflow{Steps: []StepSpec{spec("e2e-author"), spec("code"), spec(deliverStepID)}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.mu.Lock()
	d.exec.Steps["e2e-author"] = &capsule.Step{Status: capsule.StepDone, Output: map[string]any{
		"test_files": []any{"x_test.go"}, testFilesCommitKey: map[string]any{"demo": authored}}}
	d.mu.Unlock()
	if at := d.buildResultSpec("code", 1).AuthoredTests; at == nil || len(at.Files) != 1 {
		t.Fatalf("a non-deliver step stays under the authored-tests gate: %+v", at)
	}
	spec := d.buildResultSpec(deliverStepID, 1)
	if spec.AuthoredTests != nil {
		t.Fatalf("deliver must not be judged by the authored-tests gate: %+v", spec.AuthoredTests)
	}
	// ...so the delivery of the merged head is accepted.
	spec.HasReview, spec.ReviewPassed = true, true
	spec.VerifiedHeads = map[string]string{"demo": verified}
	spec.VerifiedTestBar = []testBarCmd{{Repo: "demo", Command: "make test"}}
	evidence := append(ev(goodPush), CommandEvidence{Argv: []string{"sh", "-c", "cd repos/demo && git merge --no-edit origin/main"}, DurationMs: 9},
		CommandEvidence{Argv: []string{"sh", "-c", "cd repos/demo && make test"}, DurationMs: 9})
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, evidence), spec); err != nil {
		t.Fatalf("a clean delivery merge touching an authored test must be accepted: %v", err)
	}
	// The scenario does reach gate 7 when a baseline is set (what the
	// exemption prevents).
	spec.AuthoredTests = &AuthoredTests{Files: []string{"x_test.go"}, Baseline: map[string]string{"demo": authored}}
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, evidence), spec); err == nil || !strings.Contains(err.Error(), "changed authored test files") {
		t.Fatalf("fixture: with a baseline the merge would trip gate 7, got %v", err)
	}
}

// TestDeliverLightPassMergeRule (polyforge#260): a light-passed repo
// (the last verification pass was a light recheck - no verified head,
// the light record's recheck head) is tests-covered ONLY by this
// dispatch's recorded re-run of the carried test_bar - unconditionally,
// no head-equality shortcut - and the hand-made-history walk anchors on
// the recheck head. An empty carried bar, a missing re-run or a
// hand-resolved merge means a human decides; an escalated full pass
// (a verified head present) keeps today's rules unchanged.
func TestDeliverLightPassMergeRule(t *testing.T) {
	// lightPassed: verification light-passed at main's HEAD (no verified
	// head, the recheck head), then the deliver merge moves HEAD past it
	// by a side branch (hand-edited when asked).
	lightPassed := func(t *testing.T, handEdit bool) (string, ResultSpec) {
		t.Helper()
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		tgit(t, dir, "checkout", "-q", "-b", "side", tgit(t, dir, "rev-parse", "HEAD"))
		dwrite(t, filepath.Join(dir, "b.txt"), "b\n")
		tgit(t, dir, "add", "-A")
		tgit(t, dir, "commit", "-q", "-m", "side")
		tgit(t, dir, "checkout", "-q", "main")
		dwrite(t, filepath.Join(dir, "a.txt"), "main\n")
		tgit(t, dir, "commit", "-q", "-am", "main work")
		// the light recheck passed here: no verified head, the recheck head
		// is the head the deliver merge starts from
		light := tgit(t, dir, "rev-parse", "HEAD")
		spec.VerifiedHeads = nil // a light pass never writes verified_heads
		spec.LightRecheckHeads = map[string]string{"demo": light}
		tgit(t, dir, "merge", "-q", "--no-edit", "side")
		if handEdit {
			dwrite(t, filepath.Join(dir, "b.txt"), "edited\n")
			tgit(t, dir, "add", "-A")
			tgit(t, dir, "commit", "-q", "--amend", "--no-edit")
		}
		pushFeature(t, dir)
		return repos, spec
	}
	rerun := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && make test"}, DurationMs: 9}}
	failingBar := []CommandEvidence{{Argv: []string{"sh", "-c", "cd repos/demo && make   test"}, DurationMs: 9, ExitCode: 2}}
	push := ev(goodPush)
	cases := []struct {
		name     string
		handEdit bool
		noBar    bool
		evidence []CommandEvidence
		bad      string // "" = eligible without a human
	}{
		{"carried bar re-run in the merge dispatch", false, false, append(push, rerun...), ""},
		{"no re-run at all", false, false, push, "light recheck"},
		{"re-run failing", false, false, append(push, failingBar...), "light recheck"},
		{"no carried bar entry", false, true, append(push, rerun...), "tests are not shown to pass"},
		{"hand-edited merge after the recheck head", true, false, append(push, rerun...), "differs from git's own merge result"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, spec := lightPassed(t, c.handEdit)
			if c.noBar {
				spec.VerifiedTestBar = nil
			}
			r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166",
				PRURL: "https://github.com/o/r/pull/1", Merged: ptrTrue(), MergeCommit: "abc"}
			why := mergeBlocker(spec, r, "polyforge/polyforge-166", c.evidence)
			if c.bad == "" {
				if why != "" {
					t.Fatalf("the light-passed merge must be eligible, got %q", why)
				}
				return
			}
			if why == "" || !strings.Contains(why, c.bad) {
				t.Fatalf("the blocker = %q, want it to contain %q", why, c.bad)
			}
		})
	}
	// an escalated full pass (a verified head present) keeps today's rules
	// unchanged: the head differs from the verified commit and the bar was
	// not re-run - today's message, not the light-path one.
	t.Run("escalated full pass keeps today's rules", func(t *testing.T) {
		repos, spec := lightPassed(t, false)
		dir := filepath.Join(repos, "demo")
		spec.VerifiedHeads = map[string]string{"demo": tgit(t, dir, "rev-parse", "HEAD~1")}
		spec.LightRecheckHeads = nil
		r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166",
			PRURL: "https://github.com/o/r/pull/1", Merged: ptrTrue(), MergeCommit: "abc"}
		why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush))
		if !strings.Contains(why, "differs from the commit verification passed on") {
			t.Fatalf("the escalated-full-pass blocker = %q, want today's message", why)
		}
	})
}

func ptrTrue() *bool { b := true; return &b }
