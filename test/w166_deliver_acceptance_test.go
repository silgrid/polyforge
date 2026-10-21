package test

// w166_deliver_acceptance_test.go: the `deliver` step that ends the
// delivery bundles (polyforge#166), end to end through the real daemon
// and driver. GitHub is never contacted: each task repo's origin is a
// local bare repo and `gh` is a fake first on the daemon's PATH that
// keeps PRs as files (per origin, chosen by the cwd's origin remote) and
// performs `gh pr merge --merge` as a real --no-ff merge into that bare
// origin's main. The step agent is the scripted fakepi: its deliver
// actions run the step's git/gh commands (recorded as real command
// evidence the driver judges) and report the output JSON those commands
// wrote. The driver derives the merge rule's facts itself (the commit
// verification passed on, re-done merges), so these scenarios also pin
// what a crash or a daemon restart mid-deliver can and cannot launder.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGHPRs is the stand-in for GitHub's PR API. It ignores --json/--jq
// and prints the one value each scripted call asks for.
const fakeGHPRs = `#!/bin/sh
echo "gh $*" >> "$FAKE_GH_STATE/calls.log"
origin=$(git remote get-url origin 2>/dev/null) || { echo "fake gh: not in a repo with an origin" >&2; exit 2; }
S="$FAKE_GH_STATE/$(basename "$origin" .git)"; mkdir -p "$S/prs"
[ "$1" = pr ] || { echo "fake gh: unsupported: $*" >&2; exit 2; }
sub=$2; shift 2
head=""; state=all; base=main; target=""; method=""
while [ $# -gt 0 ]; do
  case "$1" in
    --head) head=$2; shift 2 ;;
    --state) state=$2; shift 2 ;;
    --base) base=$2; shift 2 ;;
    --json|-q|--jq|--title|--body|--body-file) shift 2 ;;
    --merge|--squash|--rebase) method=$1; shift ;;
    -*) shift ;;
    *) target=$1; shift ;;
  esac
done
case "$sub" in
  list)
    for d in "$S"/prs/*; do
      [ -d "$d" ] && [ "$(cat "$d/head")" = "$head" ] || continue
      st=$(cat "$d/state")
      case "$state" in
        open) [ "$st" = OPEN ] || continue ;;
        merged) [ "$st" = MERGED ] || continue ;;
      esac
      cat "$d/url"
    done ;;
  create)
    for d in "$S"/prs/*; do
      if [ -d "$d" ] && [ "$(cat "$d/head")" = "$head" ] && [ "$(cat "$d/state")" = OPEN ]; then
        echo "a pull request for branch \"$head\" already exists" >&2; exit 1
      fi
    done
    n=$(( $(ls "$S/prs" | wc -l) + 1 )); d="$S/prs/$n"; mkdir -p "$d"
    echo "$head" > "$d/head"; echo "$base" > "$d/base"; echo OPEN > "$d/state"
    echo "https://github.com/fake/$(basename "$S")/pull/$n" > "$d/url"; cat "$d/url" ;;
  merge)
    d="$S/prs/${target##*/}"
    [ "$method" = --merge ] || { echo "fake gh: refusing merge method '$method'" >&2; exit 1; }
    [ "$(cat "$d/state")" = OPEN ] || { echo "fake gh: PR not open" >&2; exit 1; }
    w=$(mktemp -d)
    git clone -q "$origin" "$w/c" || exit 1
    (cd "$w/c" && git checkout -q "$(cat "$d/base")" &&
      git merge -q --no-ff -m "Merge pull request #${target##*/} from $(cat "$d/head")" "origin/$(cat "$d/head")" &&
      git push -q origin "HEAD:refs/heads/$(cat "$d/base")" && git rev-parse HEAD > "$d/merge") || exit 1
    echo MERGED > "$d/state"; rm -rf "$w" ;;
  view)
    cat "$S/prs/${target##*/}/merge" ;;
esac
`

// deliverWorld is one scenario's fake GitHub: bare origins (main seeded
// with a baseline), the fake gh and its state dir.
type deliverWorld struct {
	t     *testing.T
	root  string
	state string
	bin   string
}

func newDeliverWorld(t *testing.T) *deliverWorld {
	t.Helper()
	root := shortTmp(t, "pfw166")
	t.Cleanup(func() { os.RemoveAll(root) })
	w := &deliverWorld{t: t, root: root, state: filepath.Join(root, "gh"), bin: filepath.Join(root, "bin")}
	for _, d := range []string{w.bin, w.state} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w.bin, "gh"), []byte(fakeGHPRs), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

// origin creates the bare origin <name>.git with a main baseline.
func (w *deliverWorld) origin(name string) string {
	t := w.t
	t.Helper()
	bare := filepath.Join(w.root, name+".git")
	gitRun(t, "", "init", "-q", "--bare", "-b", "main", bare)
	seed := filepath.Join(w.root, "seed-"+name)
	gitRun(t, "", "clone", "-q", bare, seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "commit", "-q", "-m", "baseline")
	gitRun(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	return bare
}

func (w *deliverWorld) daemonEnv() daemonOpt {
	return optEnv("PATH="+w.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_GH_STATE="+w.state)
}

// prState answers repo's PR n state ("" when absent).
func (w *deliverWorld) prState(repo, n string) string {
	b, _ := os.ReadFile(filepath.Join(w.state, repo, "prs", n, "state"))
	return strings.TrimSpace(string(b))
}

// calls counts the fake gh invocations starting with prefix.
func (w *deliverWorld) calls(prefix string) int {
	b, _ := os.ReadFile(filepath.Join(w.state, "calls.log"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// mergedInto asserts origin main is the PR merge commit of the feature
// branch (exactly one merge commit on main).
func (w *deliverWorld) mergedInto(bare, branch string) {
	t := w.t
	t.Helper()
	branchHead := strings.TrimSpace(gitRun(t, bare, "rev-parse", "refs/heads/"+branch))
	mainHead := strings.TrimSpace(gitRun(t, bare, "rev-parse", "refs/heads/main"))
	parents := strings.Fields(gitRun(t, bare, "rev-list", "--parents", "-n", "1", mainHead))
	if len(parents) != 3 || parents[2] != branchHead {
		t.Fatalf("%s main %s is not the PR merge commit of %s (parents %v)", bare, mainHead, branchHead, parents)
	}
	if merges := strings.Fields(gitRun(t, bare, "rev-list", "--merges", "--first-parent", "refs/heads/main")); len(merges) != 1 {
		t.Fatalf("%s main carries %d first-parent merge commits, want 1", bare, len(merges))
	}
}

// --- the scripted deliver procedure (steps/deliver.md), per repo. "$b" is
// the feature branch; literalBranch spells it out before the plan is
// written (the driver reads pushes literally).

func dPre(repo string) string {
	return `set -e; cap="$PWD"; mkdir -p artifacts; cd repos/` + repo + `; `
}

// pushAndPR: fetch; an already-merged repo stops here; else merge
// origin/main, push the feature branch (never forced), reuse or open the PR.
func pushAndPR(repo string) string {
	return dPre(repo) + `git fetch -q origin; ` +
		`url=$(gh pr list --head "$b" --state merged --json url -q '.[0].url' | head -1); ` +
		`if [ -n "$url" ] && git merge-base --is-ancestor HEAD origin/main; then exit 0; fi; ` +
		`git merge -q --no-edit origin/main; ` +
		`remote=$(git ls-remote origin "refs/heads/$b" | cut -f1); ` +
		`if [ -n "$remote" ] && ! git merge-base --is-ancestor "$remote" HEAD; then exit 3; fi; ` +
		`git push -q origin "HEAD:refs/heads/$b"; ` +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`[ -n "$url" ] || gh pr create --base main --head "$b" --title "w166 delivery" --body-file /dev/null`
}

// mergeRecord merges the open PR (if any) with --merge and records the repo.
func mergeRecord(repo, extra string) string {
	return dPre(repo) +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`if [ -n "$url" ]; then gh pr merge "$url" --merge; fi; ` +
		`url=$(gh pr list --head "$b" --state merged --json url -q '.[0].url' | head -1); ` +
		`mc=$(gh pr view "$url" --json mergeCommit -q .mergeCommit.oid); ` +
		`printf '{"name":"` + repo + `","branch":"%s","pr_url":"%s","merged":true,"merge_commit":"%s","tests_passed":true` + extra + `}' "$b" "$url" "$mc" > "$cap/artifacts/deliver-repo-` + repo + `.json"`
}

// claimRecord records repo with the given fields WITHOUT merging anything.
func claimRecord(repo, fields string) string {
	return dPre(repo) +
		`url=$(gh pr list --head "$b" --state open --json url -q '.[0].url' | head -1); ` +
		`printf '{"name":"` + repo + `","branch":"%s","pr_url":"%s",` + fields + `}' "$b" "$url" > "$cap/artifacts/deliver-repo-` + repo + `.json"`
}

// assemble writes artifacts/deliver-output.json from the per-repo records.
const assemble = `set -e; e=$(for f in artifacts/deliver-repo-*.json; do cat "$f"; echo; done | paste -sd, -); ` +
	`printf '{"verdict":"pass","repos":[%s],"delivery_note":"scripted delivery"}' "$e" > artifacts/deliver-output.json`

const deliverOutput = "artifacts/deliver-output.json"

// literalBranch spells the feature branch out in scripted commands.
func literalBranch(branch string, cmds ...string) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = strings.ReplaceAll(c, "$b", branch)
	}
	return out
}

// moveMain lands a commit on bare's main (another PR merged meanwhile).
func moveMain(bare, file, content string) string {
	return `set -e; t=$(mktemp -d); git clone -q ` + bare + ` "$t/c"; cd "$t/c"; echo "` + content + `" > ` + file + `; git add -A; ` +
		`git -c user.name=o -c user.email=o@invalid commit -q -m "other PR"; git push -q origin HEAD:refs/heads/main; rm -rf "$t"`
}

// deliverTestBar is the scenarios' test bar: verification runs it in
// repos/demo and names it in test_bar; deliver re-runs it verbatim.
const deliverTestBar = `test -s work.txt && git diff --quiet HEAD`

const deliverTestBarCmd = `cd repos/demo && ` + deliverTestBar

func verifiedWithTestBar() map[string]any {
	return map[string]any{"verdict": "pass", "test_bar": []map[string]any{{"repo": "demo", "command": deliverTestBar}}}
}

// newDeliverWI files a WI on bundle with an empty plan and answers it and
// its feature branch (the plan is written once the branch is known).
func newDeliverWI(t *testing.T, inst *v1Install, goal, bundle string) (string, string) {
	t.Helper()
	inst.keepDeliver = true
	wi := newWIWithBundle(t, inst, goal, bundle, nil)
	return wi, "polyforge/" + wiPathName(t, inst, wi)
}

func startDeliverDaemon(t *testing.T, inst *v1Install, w *deliverWorld, wi string) *v1Daemon {
	t.Helper()
	d := newV1Daemon(t, inst, w.daemonEnv())
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)
	return d
}

func expectRejection(t *testing.T, d *v1Daemon, wi, want string) {
	t.Helper()
	rej := waitForEvent(t, d, wi, "envelope_rejected", 30*time.Second)
	if !strings.Contains(string(rej.Body), want) {
		t.Fatalf("deliver envelope not refused for %q: %s", want, rej.Body)
	}
}

// TestW166DeliverFeatureFullAutoMergesIdempotently: a feature-full ticket
// whose gates all pass is delivered automatically — the feature branch
// pushed, one PR opened and merged with a merge commit into origin main.
// The first deliver dispatch dies after merging and before reporting; the
// re-dispatch runs the same procedure and reuses the branch and PR: still
// one PR, one merge, accepted without a human (the pushed head is the
// commit verification passed on).
func TestW166DeliverFeatureFullAutoMergesIdempotently(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: feature-full delivered automatically", "feature-full")
	deliver := literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		// dies after pushing, opening AND merging, before the envelope
		{"do": "envelope", "id": "deliver", "run": deliver, "crash": "before"},
		// the re-dispatch: the same idempotent procedure
		{"do": "envelope", "id": "deliver", "outcome": "completed", "run": deliver, "output_file": deliverOutput},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if n := w.calls("gh pr create"); n != 1 {
		t.Fatalf("gh pr create ran %d times, want exactly 1 (idempotent re-dispatch)", n)
	}
	if n := w.calls("gh pr merge"); n != 1 {
		t.Fatalf("gh pr merge ran %d times, want exactly 1", n)
	}
	if n := w.calls("gh pr view"); n != 2 {
		t.Fatalf("the procedure ran to its record %d times, want 2 (the crashed dispatch and the re-dispatch)", n)
	}
	x := readExecution(t, d.capsuleDir(wi))
	if heads, _ := x.Steps["verification"].Output["verified_heads"].(map[string]any); heads["demo"] == nil {
		t.Fatalf("the driver did not record the verified head: %+v", x.Steps["verification"].Output)
	}
	st := x.Steps["deliver"]
	repos, _ := st.Output["repos"].([]any)
	if st.Status != "done" || len(repos) != 1 {
		t.Fatalf("deliver output: %+v", st)
	}
	r, _ := repos[0].(map[string]any)
	mainHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/main"))
	if r["branch"] != branch || r["merged"] != true || r["merge_commit"] != mainHead || r["pr_url"] != "https://github.com/fake/demo/pull/1" {
		t.Fatalf("deliver result does not record branch/PR/merge commit: %+v (want branch %s, merge %s)", r, branch, mainHead)
	}
}

// TestW166DeliverCrashAfterMergeCommitBeforeTests: origin main moved on
// cleanly; the first deliver dispatch merges it and dies before running
// the tests. The re-dispatch sees "Already up to date", skips the tests
// and reports tests_passed — the driver refuses the auto-merge (the
// pushed head is not the verified commit and no test re-run is in this
// dispatch's evidence), the step asks, and only a human's answer merges.
func TestW166DeliverCrashAfterMergeCommitBeforeTests(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: crash between the merge commit and the tests", "chore")
	const qid = "deliver-merge"
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"},
			"run": []string{moveMain(origin, "other.txt", "another PR")}},
		{"do": "envelope", "id": "deliver", "run": []string{dPre("demo") + "git fetch -q origin; git merge -q --no-edit origin/main"}, "crash": "before"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, pushAndPR("demo"), claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble)},
		{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: the tests did not run on the merged head. merge / don't merge / back to code?"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 120*time.Second)
	expectRejection(t, d, wi, "tests are not shown to pass")
	if w.prState("demo", "1") != "OPEN" || w.calls("gh pr merge") != 0 {
		t.Fatalf("PR must be open and unmerged while the question is pending (state %q)", w.prState("demo", "1"))
	}
	answerQuestion(t, d, wi, qid, "merge")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if w.prState("demo", "1") != "MERGED" {
		t.Fatalf("PR state %q after the human's merge answer", w.prState("demo", "1"))
	}
}

// TestW166DeliverHandResolvedConflictStopsAtHuman: origin main moves on
// with a conflicting change; deliver resolves the conflict, re-runs the
// verification commands on the merged head, pushes and opens the PR — and
// reports NO conflict. The driver re-does the merge itself, sees the hand
// resolution and refuses the auto-merge; the merge lands only after a
// human answers "merge".
func TestW166DeliverHandResolvedConflictStopsAtHuman(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: a hand-resolved conflict needs a human", "chore")
	const qid = "deliver-merge"
	testBar := deliverTestBarCmd
	resolve := dPre("demo") + `git fetch -q origin; if git merge -q --no-edit origin/main; then exit 9; fi; ` +
		`{ echo "fake agent repo work"; echo "main moved on"; } > work.txt; git add work.txt; git commit -q --no-edit; ` +
		`git push -q origin "HEAD:refs/heads/$b"; gh pr create --base main --head "$b" --title "w166 conflict" --body-file /dev/null`
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		// the test bar verification records (and deliver must re-run verbatim)
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": verifiedWithTestBar(), "run": []string{testBar}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"},
			"run": []string{moveMain(origin, "work.txt", "main moved on")}},
		// resolves, re-runs the test bar, pushes, opens the PR — and lies about the conflict
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, resolve, testBar,
				claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true,"llm_resolved_conflict":false`), assemble)},
		{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: merging main needed a hand-resolved conflict in work.txt. merge / don't merge / back to code?"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, mergeRecord("demo", `,"llm_resolved_conflict":true`), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 180*time.Second)
	expectRejection(t, d, wi, "had conflicts")
	branchHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/"+branch))
	if w.prState("demo", "1") != "OPEN" || w.calls("gh pr merge") != 0 {
		t.Fatalf("PR must be open and unmerged while the human question is pending (state %q)", w.prState("demo", "1"))
	}
	if strings.Contains(gitRun(t, origin, "branch", "--contains", branchHead), "main") {
		t.Fatal("origin main already contains the delivered branch before the human answered")
	}
	answerQuestion(t, d, wi, qid, "merge")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if w.prState("demo", "1") != "MERGED" {
		t.Fatalf("PR state %q after the human's merge answer", w.prState("demo", "1"))
	}
	mainHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/main"))
	if parents := strings.Fields(gitRun(t, origin, "rev-list", "--parents", "-n", "1", mainHead)); len(parents) != 3 || parents[2] != branchHead {
		t.Fatalf("origin main %s is not the merge of %s (parents %v)", mainHead, branchHead, parents)
	}
}

// TestW166DeliverAutopilotAutoMerges: autopilot has no review step;
// verification's pass stands in for it (owner decision), so a clean
// delivery on the verified commit auto-merges without a human.
func TestW166DeliverAutopilotAutoMerges(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: autopilot auto-merges on verification", "autopilot")
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			t.Fatalf("an envelope was rejected: %s", e.Body)
		}
	}
}

// TestW166DeliverAutopilotHandResolvedConflictAsks: autopilot still asks when
// merging main needed a hand-resolved conflict (driver-derived, whatever
// the step reports); the human answers "don't merge" and the lane
// completes with the PR open and origin main untouched by it.
func TestW166DeliverAutopilotHandResolvedConflictAsks(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: autopilot hand-resolved conflict asks", "autopilot")
	const qid = "deliver-merge"
	resolve := dPre("demo") + `git fetch -q origin; if git merge -q --no-edit origin/main; then exit 9; fi; ` +
		`{ echo "fake agent repo work"; echo "main moved on"; } > work.txt; git add work.txt; git commit -q --no-edit; ` +
		`git push -q origin "HEAD:refs/heads/$b"; gh pr create --base main --head "$b" --title "w166 conflict" --body-file /dev/null`
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		// main moves on with a conflicting change while verification runs
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": verifiedWithTestBar(),
			"run": []string{moveMain(origin, "work.txt", "main moved on"), deliverTestBarCmd}},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, resolve, deliverTestBarCmd,
				claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true,"llm_resolved_conflict":false`), assemble)},
		{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: merging main needed a hand-resolved conflict. merge / don't merge / back to code?"},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, claimRecord("demo", `"merged":false,"not_merged_reason":"human: don't merge yet","tests_passed":true,"llm_resolved_conflict":true`), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 120*time.Second)
	expectRejection(t, d, wi, "had conflicts")
	answerQuestion(t, d, wi, qid, "don't merge")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if w.prState("demo", "1") != "OPEN" || w.calls("gh pr merge") != 0 {
		t.Fatalf("PR must stay open and unmerged (state %q)", w.prState("demo", "1"))
	}
	branchHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/"+branch))
	if strings.Contains(gitRun(t, origin, "branch", "--contains", branchHead), "main") {
		t.Fatal("origin main contains the delivered branch despite \"don't merge\"")
	}
}

// TestW166DeliverMultiRepoCrashBetweenPRAndMerge: a WI with two task repos
// gets one delivery pass per repo. The first dispatch pushes both branches
// and opens both PRs, then dies before merging; the re-dispatch reuses
// both PRs and merges each once.
func TestW166DeliverMultiRepoCrashBetweenPRAndMerge(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	demo, two := w.origin("demo"), w.origin("two")
	wi, branch := newDeliverWI(t, inst, "w166: two repos, crash between PR and merge", "chore")
	cloneTwo := `set -e; mkdir -p repos; git clone -q ` + two + ` repos/two; cd repos/two; echo two > two.txt; git add -A; ` +
		`git -c user.name=a -c user.email=a@invalid commit -q -m "two work"`
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": demo, "run": []string{cloneTwo}},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "deliver", "crash": "before", "run": literalBranch(branch, pushAndPR("demo"), pushAndPR("two"))},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, pushAndPR("demo"), pushAndPR("two"), mergeRecord("demo", ""), mergeRecord("two", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(demo, branch)
	w.mergedInto(two, branch)
	if c, m := w.calls("gh pr create"), w.calls("gh pr merge"); c != 2 || m != 2 {
		t.Fatalf("gh pr create ran %d and gh pr merge %d times, want 2 each (one per repo)", c, m)
	}
	repos, _ := readExecution(t, d.capsuleDir(wi)).Steps["deliver"].Output["repos"].([]any)
	if len(repos) != 2 {
		t.Fatalf("deliver result must carry one entry per repo: %+v", repos)
	}
}

// TestW166DeliverSurvivesDaemonRestart: the daemon crashes while deliver
// is running (after the branch push and the PR); the restarted daemon
// re-dispatches deliver, which reuses the PR and merges once.
func TestW166DeliverSurvivesDaemonRestart(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: daemon restart during deliver", "chore")
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "deliver", "park": true, "run": literalBranch(branch, pushAndPR("demo"))},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitUntil(t, 120*time.Second, "deliver never opened its PR", func() bool { return w.prState("demo", "1") == "OPEN" })
	d.crash()
	d2 := &v1Daemon{
		t: t, inst: inst, sock: d.sock, dataDir: d.dataDir, workspace: d.workspace,
		machineID: d.machineID, extraArgs: d.extraArgs, extraEnv: d.extraEnv,
		logPath: filepath.Join(t.TempDir(), "daemon2.log"),
	}
	t.Cleanup(func() { d2.stop() })
	d2.start()

	waitWorkflowStatus(t, d2, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if c, m := w.calls("gh pr create"), w.calls("gh pr merge"); c != 1 || m != 1 {
		t.Fatalf("gh pr create ran %d and gh pr merge %d times, want 1 each across the restart", c, m)
	}
}

// TestW166DeliverBackToCodeRoundTrip: the human answers "back to code";
// deliver's fail verdict rewinds the lane to code, the gates re-run on the
// new work, and the next deliver reuses the same branch and PR and
// auto-merges the new head.
func TestW166DeliverBackToCodeRoundTrip(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: back to code round trip", "chore")
	const qid = "deliver-merge"
	rework := `set -e; cd repos/demo; echo rework >> work.txt; git -c user.name=a -c user.email=a@invalid commit -q -am "rework after review"`
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1 is open. merge / don't merge / back to code?",
			"run":      literalBranch(branch, pushAndPR("demo"))},
		{"do": "envelope", "id": "deliver", "outcome": "gate_failed",
			"output": map[string]any{"verdict": "fail", "fail_reason": "human: back to code: rework work.txt (PR https://github.com/fake/demo/pull/1)"}},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "run": []string{rework}},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"}},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput,
			"run": literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 120*time.Second)
	answerQuestion(t, d, wi, qid, "back to code: rework work.txt")
	waitWorkflowStatus(t, d, wi, "completed", 180*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if c := w.calls("gh pr create"); c != 1 {
		t.Fatalf("gh pr create ran %d times, want 1 (the PR is reused after the rewind)", c)
	}
	if log := gitRun(t, origin, "log", "--format=%s", "refs/heads/"+branch); !strings.Contains(log, "rework after review") {
		t.Fatalf("the reworked commit was not delivered:\n%s", log)
	}
	x := readExecution(t, d.capsuleDir(wi))
	if n, _ := x.RewindStats["count"].(float64); n < 1 {
		t.Fatalf("no rewind recorded: %+v", x.RewindStats)
	}
}

// TestW166DeliverMainMovedTestBarRerunAutoMerges: origin main moved on
// cleanly; deliver merges it, re-runs verification's test bar after the
// merge (its evidence carries no started_at, as real agents report it)
// and auto-merges — the driver accepts the re-run by order alone.
func TestW166DeliverMainMovedTestBarRerunAutoMerges(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: main moved, test bar re-run, auto-merge", "chore")
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": verifiedWithTestBar(),
			"run": []string{deliverTestBarCmd}, "no_started_at": true},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"},
			"run": []string{moveMain(origin, "other.txt", "another PR")}},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true,
			"run": literalBranch(branch, pushAndPR("demo"), deliverTestBarCmd, mergeRecord("demo", ""), assemble)},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	branchHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/"+branch))
	if p := strings.Fields(gitRun(t, origin, "rev-list", "--parents", "-n", "1", branchHead)); len(p) != 3 {
		t.Fatalf("the delivered head %s is not deliver's merge of the moved main (parents %v)", branchHead, p)
	}
	mainHead := strings.TrimSpace(gitRun(t, origin, "rev-parse", "refs/heads/main"))
	if p := strings.Fields(gitRun(t, origin, "rev-list", "--parents", "-n", "1", mainHead)); len(p) != 3 || p[2] != branchHead {
		t.Fatalf("origin main %s is not the PR merge of %s (parents %v)", mainHead, branchHead, p)
	}
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			t.Fatalf("an envelope was rejected: %s", e.Body)
		}
	}
}

// TestW166DeliverAlreadyMergedAfterMainMoved: main moved on; the first
// deliver dispatch merges it, re-runs the test bar, pushes, opens and
// merges the PR, then dies before reporting. The re-dispatch finds the PR
// merged, re-runs the test bar (HEAD is not the verified commit) and is
// accepted without a human: the recorded merge commit merged the local
// HEAD and the fetched main contains it.
func TestW166DeliverAlreadyMergedAfterMainMoved(t *testing.T) {
	inst := newV1Install(t)
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi, branch := newDeliverWI(t, inst, "w166: already merged after main moved", "chore")
	full := literalBranch(branch, pushAndPR("demo"), deliverTestBarCmd, mergeRecord("demo", ""), assemble)
	inst.setPlanFull(t, wi, []map[string]any{
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true, "name": "demo", "origin": origin},
		{"do": "envelope", "id": "verification", "outcome": "completed", "output": verifiedWithTestBar(), "run": []string{deliverTestBarCmd}},
		{"do": "envelope", "id": "review", "outcome": "completed", "output": map[string]any{"verdict": "pass"},
			"run": []string{moveMain(origin, "other.txt", "another PR")}},
		{"do": "envelope", "id": "deliver", "crash": "before", "no_started_at": true, "run": full},
		{"do": "envelope", "id": "deliver", "outcome": "completed", "output_file": deliverOutput, "no_started_at": true, "run": full},
	}, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	if c, m := w.calls("gh pr create"), w.calls("gh pr merge"); c != 1 || m != 1 {
		t.Fatalf("gh pr create ran %d and gh pr merge %d times, want 1 each", c, m)
	}
	for _, e := range d.eventsOf(t, wi) {
		if e.Kind == "envelope_rejected" {
			t.Fatalf("an envelope was rejected: %s", e.Body)
		}
	}
}
