package driver

// deliver_facts.go: the merge-rule facts the driver derives itself
// (polyforge#166) instead of trusting the deliver step's
// self-report, which a crash or restart mid-deliver can make wrong (a
// re-dispatch sees "Already up to date" and no longer knows that an
// earlier dispatch merged — or hand-resolved — anything).
//
//   - verified heads: when `verification` completes, the driver records
//     each task repo's HEAD in its output (verifiedHeadsKey) — never
//     step-supplied (the step-result transition overwrites the key).
//   - tests covered: a repo's pushed HEAD equals the commit verification
//     passed on, OR this dispatch's command evidence — in recorded order —
//     ran every command of verification's test_bar for that repo, in that
//     repo, with exit 0, after the dispatch's last `git merge`/`git pull`
//     there and before the `gh pr merge` of its PR. No timestamps (started_at stays optional and ignored).
//     Evidence of an earlier, crashed dispatch is gone, so it never
//     counts; a missing test_bar is "not shown" — the human's call.
//   - hand-resolved: every first-parent commit in <verified>..HEAD must be
//     a merge whose tree equals what `git merge-tree --write-tree` makes
//     of its two parents without conflict; anything else (a conflicted or
//     differently resolved merge, a non-merge commit, a missing record)
//     counts as hand-made, whatever the step reports.
//   - delivered head: refs/remotes/origin/<feature branch> (moved by the
//     push) is HEAD, and HEAD contains the fetched default branch — or,
//     for a PR merged earlier, the recorded merge commit merged HEAD.
//
// All of this is detection after the fact: by the time the envelope is
// judged the push and the merge have happened. A failed fact escalates to
// the human; prevention rests on the step contract and the machine's
// pre-push hook.
//
// mergeBlocker is the merge rule itself; its first condition keeps the
// owner's autopilot rule (no review step) one visible line.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// verifiedHeadsKey is the driver-written key of verification's output:
// task repo name -> HEAD sha at the moment verification completed.
const verifiedHeadsKey = "verified_heads"

// mergeBlocker answers why one merged repo is not eligible to merge
// without a human ("" = eligible): the independent review passed with no
// blocking finding (in a workflow that has one), no conflict was
// hand-resolved, the delivered head is the local HEAD on the latest
// default branch, and the tests are covered on the pushed head. The
// pre-flight (nothing uncommitted, no release residue) is the step
// contract's part. autopilot has no review step: there verification's
// pass (a gate the lane cannot pass by without) stands in for it — owner
// decision, polyforge#166.
func mergeBlocker(spec ResultSpec, r deliverRepo, branch string, ev []CommandEvidence) string {
	if spec.HasReview && !spec.ReviewPassed {
		return "no independent review passed with no blocking finding"
	}
	handMade := func(why string) string {
		return "the delivered history has a hand-resolved or hand-made change (" + why + ")"
	}
	verified := spec.VerifiedHeads[r.Name]
	if spec.ReposDir == "" || verified == "" {
		return handMade("no verification record of this repo's head")
	}
	dir := filepath.Join(spec.ReposDir, r.Name)
	headOut, err := repoGit(dir, "rev-parse", "HEAD")
	if err != nil {
		return handMade("HEAD unreadable")
	}
	head := strings.TrimSpace(headOut)
	if head != verified {
		if why := handMadeHistory(dir, verified, head); why != "" {
			return handMade(why)
		}
	}
	if why := deliveredHeadMismatch(dir, head, branch, strings.TrimSpace(r.MergeCommit)); why != "" {
		return "the delivered head cannot be tied to the local HEAD (" + why + ")"
	}
	switch {
	case r.LLMResolvedConflict:
		return "the step reports an LLM-resolved conflict"
	case r.TestsPassed != nil && !*r.TestsPassed:
		return "the step reports failing tests"
	case head != verified && !testBarRerun(r.Name, strings.TrimSpace(r.PRURL), spec.VerifiedTestBar, evidenceEvents(ev, filepath.Dir(spec.ReposDir))):
		return "the tests are not shown to pass on the pushed head (it differs from the commit verification passed on and this dispatch did not re-run verification's test_bar in that repo after its last merge and before its gh pr merge)"
	}
	return ""
}

// deliveredHeadMismatch ties what was pushed and merged to the local
// HEAD the facts were derived from: the push updated
// refs/remotes/origin/<branch> to HEAD; HEAD contains the fetched
// origin default branch (delivered from the latest main) — or, for a PR
// merged before (the fetched default branch already contains HEAD), the
// recorded merge commit's second parent is HEAD (checked when that
// commit is present locally).
func deliveredHeadMismatch(dir, head, branch, mergeCommit string) string {
	if branch != "" {
		tracked, err := repoGit(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch)
		if err != nil || strings.TrimSpace(tracked) != head {
			return "origin/" + branch + " (the pushed branch) is not the local HEAD"
		}
	}
	def := ""
	if out, err := repoGit(dir, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		def = strings.TrimSpace(out)
	} else {
		for _, b := range []string{"main", "master"} {
			if _, err := repoGit(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+b); err == nil {
				def = "refs/remotes/origin/" + b
				break
			}
		}
	}
	if def == "" {
		return "the fetched origin default branch is unknown"
	}
	if _, err := repoGit(dir, "merge-base", "--is-ancestor", def, head); err == nil {
		return ""
	}
	if _, err := repoGit(dir, "merge-base", "--is-ancestor", head, def); err != nil {
		return "HEAD does not contain " + strings.TrimPrefix(def, "refs/remotes/") + " (not delivered from the latest default branch)"
	}
	// already merged: the default branch moved past HEAD
	if mergeCommit != "" {
		if _, err := repoGit(dir, "cat-file", "-e", mergeCommit+"^{commit}"); err == nil {
			p2, err := repoGit(dir, "rev-parse", "--verify", "-q", mergeCommit+"^2")
			if err != nil || strings.TrimSpace(p2) != head {
				return "the recorded merge commit " + shortSHA(mergeCommit) + " did not merge the local HEAD"
			}
		}
	}
	return ""
}

// handMadeHistory answers "" when every first-parent commit in
// verified..head is a merge git reproduces without conflict, else why not.
func handMadeHistory(dir, verified, head string) string {
	if _, err := repoGit(dir, "merge-base", "--is-ancestor", verified, head); err != nil {
		return "the verified commit " + shortSHA(verified) + " is not an ancestor of HEAD"
	}
	out, err := repoGit(dir, "rev-list", "--first-parent", "--parents", verified+".."+head)
	if err != nil {
		return "history unreadable"
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 3 {
			return "commit " + shortSHA(f[0]) + " is not a two-parent merge"
		}
		want, err := repoGit(dir, "merge-tree", "--write-tree", f[1], f[2])
		if err != nil {
			return "merge " + shortSHA(f[0]) + " had conflicts"
		}
		got, err := repoGit(dir, "rev-parse", f[0]+"^{tree}")
		tree, _, _ := strings.Cut(strings.TrimSpace(want), "\n")
		if err != nil || strings.TrimSpace(tree) != strings.TrimSpace(got) {
			return "merge " + shortSHA(f[0]) + " differs from git's own merge result"
		}
	}
	return ""
}

// testBarCmd is one entry of verification's test bar: a command run
// inside repos/<Repo>.
type testBarCmd struct {
	Repo    string `json:"repo"`
	Command string `json:"command"`
}

// evEvent is one simple command of the dispatch's evidence, in recorded
// order: the repo it ran in (from the entry's cwd, `cd` and `git -C`),
// its words, its comparison key and its entry's exit code.
type evEvent struct {
	repo    string
	words   []string // commandWords: finds cd / git / gh
	key     string   // barKey: compared with the test bar
	exit    int
	merge   bool // git merge / git pull
	prMerge bool // gh pr merge
}

// barKey is a simple command as the test-bar comparison sees it, on
// both sides alike: redirections dropped, quotes and whitespace
// normalised (by shellSegments), nothing else removed. Leading
// NAME=value assignments and wrapper words stay, so `GOWORK=off go build`
// or `E2E_RUN=a sh e2e.sh` is matched only by that exact command.
func barKey(seg []string) string {
	return strings.Join(dropRedirections(seg), " ")
}

// commandWords is a simple command's command word onward: redirections
// dropped, and the leading NAME=value assignments, shell keywords and
// wrapper words (env, timeout, …) skipped. It identifies cd / git / gh;
// it is never the test-bar comparison key (barKey). nil = no command word.
func commandWords(seg []string) []string {
	seg = dropRedirections(seg)
	i := 0
	for i < len(seg) && (isAssignment(seg[i]) || shellKeywords[seg[i]] || commandWrappers[filepath.Base(seg[i])]) {
		i++
	}
	if i >= len(seg) {
		return nil
	}
	return seg[i:]
}

// evidenceEvents flattens command evidence into simple commands in order.
func evidenceEvents(ev []CommandEvidence, capsuleRoot string) []evEvent {
	var out []evEvent
	for _, e := range ev {
		dir := capsuleRel(e.Cwd, capsuleRoot)
		var segs [][]string
		switch {
		case len(e.Argv) == 1:
			segs = shellSegments(e.Argv[0])
		case len(e.Argv) > 2 && shells[filepath.Base(e.Argv[0])] && strings.HasPrefix(e.Argv[1], "-") && strings.Contains(e.Argv[1], "c"):
			segs = shellSegments(e.Argv[2])
		default:
			segs = [][]string{e.Argv}
		}
		for _, seg := range segs {
			words := commandWords(seg)
			if words == nil {
				continue
			}
			if words[0] == "cd" {
				if len(words) > 1 {
					dir = joinRel(dir, words[1], capsuleRoot)
				}
				continue
			}
			ev := evEvent{repo: repoOfRel(dir), words: words, key: barKey(seg), exit: e.ExitCode}
			if filepath.Base(words[0]) == "git" {
				j, opts := gitSubcommand(words[1:])
				for _, o := range opts {
					if o[0] == "-C" {
						ev.repo = repoOfRel(joinRel(dir, o[1], capsuleRoot))
					}
				}
				ev.merge = j+1 < len(words) && (words[j+1] == "merge" || words[j+1] == "pull")
			}
			if filepath.Base(words[0]) == "gh" {
				_, ev.prMerge = ghPRMergeArgs(words[1:])
			}
			out = append(out, ev)
		}
	}
	return out
}

// capsuleRel answers p relative to the capsule root ("" = the root).
func capsuleRel(p, capsuleRoot string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(capsuleRoot, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "?"
		}
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func joinRel(dir, arg, capsuleRoot string) string {
	if filepath.IsAbs(arg) {
		return capsuleRel(arg, capsuleRoot)
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(dir, arg)))
}

// repoOfRel answers the task repo a capsule-relative dir lies in.
func repoOfRel(dir string) string {
	parts := strings.Split(dir, "/")
	if len(parts) >= 2 && parts[0] == capsule.DirRepos {
		return parts[1]
	}
	return ""
}

// testBarRerun: every test-bar command verification named for repo
// appears in this dispatch's evidence, run in that repo with exit 0,
// in order, AFTER the dispatch's last merge in that repo (no merge in
// this dispatch: anywhere in it) and BEFORE the first `gh pr merge` of
// that repo's PR after that merge — run in the repo or naming prURL —
// since the PR merge is what the re-run must cover (polyforge#203).
// Both sides compare exactly, as barKey (quotes stripped, whitespace
// collapsed, redirections dropped, leading assignments and wrappers
// kept; a leading `cd` sets the repo). No test bar for the repo
// means nothing to prove it with.
func testBarRerun(repo, prURL string, bar []testBarCmd, events []evEvent) bool {
	start := 0
	for i, e := range events {
		if e.merge && e.repo == repo {
			start = i + 1
		}
	}
	end := len(events)
	for i := start; i < len(events); i++ {
		if events[i].prMerge && (events[i].repo == repo || prURL != "" && slices.Contains(events[i].words, prURL)) {
			end = i
			break
		}
	}
	need := 0
	for _, tb := range bar {
		if tb.Repo != repo {
			continue
		}
		for _, seg := range shellSegments(tb.Command) {
			words := commandWords(seg)
			if words == nil || words[0] == "cd" {
				continue
			}
			key := barKey(seg)
			need++
			found := false
			for start < end {
				e := events[start]
				start++
				if e.repo == repo && e.exit == 0 && e.key == key {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return need > 0
}

// checkNothingToDeliver: a skipped repo has no commit beyond its
// claim-time baseline (capsule.json repos).
func checkNothingToDeliver(reposDir, repo string) error {
	if reposDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(reposDir), capsule.CapsuleJSON))
	var id capsule.Identity
	if err == nil {
		err = json.Unmarshal(raw, &id)
	}
	if err != nil || id.Repos[repo] == "" {
		return errors.New("its claim-time baseline is not recorded, so nothing-to-deliver cannot be confirmed")
	}
	out, err := repoGit(filepath.Join(reposDir, repo), "rev-list", "--count", id.Repos[repo]+"..HEAD")
	if err != nil {
		return fmt.Errorf("its history is unreadable: %v", err)
	}
	if n := strings.TrimSpace(out); n != "0" {
		return fmt.Errorf("it has %s commit(s) beyond its baseline", n)
	}
	return nil
}

// verificationRecord reads the verified heads (driver-written) and the
// test bar (step-named) back from the verification step's output (in
// memory or reloaded from execution.json).
func verificationRecord(st *capsule.Step) (map[string]string, []testBarCmd) {
	if st == nil || st.Status != capsule.StepDone {
		return nil, nil
	}
	heads := map[string]string{}
	if h, ok := st.Output[verifiedHeadsKey].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				heads[k] = s
			}
		}
	}
	var bar []testBarCmd
	if raw, err := json.Marshal(st.Output["test_bar"]); err == nil {
		_ = json.Unmarshal(raw, &bar)
	}
	return heads, bar
}
