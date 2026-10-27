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
//     passed on, OR this dispatch's command evidence ran every command of
//     verification's test_bar for that repo, in that repo, with exit 0,
//     after the dispatch's last `git merge`/`git pull` there and before the
//     `gh pr merge` of its PR. Only that window is ordered: each bar
//     command is looked for by itself (workbench#60). No timestamps
//     (started_at stays optional and ignored).
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
	// workbench#19: a scoped verification pass is a merging credential only
	// when review answered scope_adequate true; false or absent routes the
	// delivery to the human question, exactly like any other unmet merge
	// condition. A full verification keeps today's rule (VerificationScoped
	// is false, so this condition never applies).
	if spec.VerificationScoped && (spec.ReviewScopeAdequate == nil || !*spec.ReviewScopeAdequate) {
		return "the verification was scoped but review did not answer scope_adequate true"
	}
	handMade := func(why string) string {
		return "the delivered history has a hand-resolved or hand-made change (" + why + ")"
	}
	verified := spec.VerifiedHeads[r.Name]
	// polyforge#260: a light-passed repo (the last verification pass was a
	// light recheck) carries no verified head — a head verified only by a
	// light recheck is never tests-covered — so the merge rule anchors on
	// the light record's recheck head instead (the anchor replacement: the
	// range the old anchor covered splits into the repair range
	// (diff_base..recheck_head, covered strictly by the driver's
	// modifications-only/caps check) and recheck_head..HEAD — the deliver
	// merge itself, which must be git-reproducible like any other).
	light := spec.LightRecheckHeads[r.Name]
	if spec.ReposDir == "" || (verified == "" && light == "") {
		return handMade("no verification record of this repo's head")
	}
	dir := filepath.Join(spec.ReposDir, r.Name)
	headOut, err := repoGit(dir, "rev-parse", "HEAD")
	if err != nil {
		return handMade("HEAD unreadable")
	}
	head := strings.TrimSpace(headOut)
	switch {
	case verified != "":
		if head != verified {
			if why := handMadeHistory(dir, verified, head); why != "" {
				return handMade(why)
			}
		}
	case light != "":
		if why := handMadeHistory(dir, light, head); why != "" {
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
	case verified != "":
		if head != verified && !testBarRerun(r.Name, strings.TrimSpace(r.PRURL), spec.VerifiedTestBar, evidenceEvents(ev, filepath.Dir(spec.ReposDir))) {
			return "the tests are not shown to pass on the pushed head (it differs from the commit verification passed on and this dispatch did not re-run verification's test_bar in that repo after its last merge and before its gh pr merge)"
		}
	default:
		// the light pass: the tests are covered ONLY by this dispatch's
		// recorded re-run of the carried test_bar — unconditional (no
		// head-equality shortcut exists on the light path: a light recheck
		// never ran the full bar, so there is no verified head to equal). A
		// repo with no carried test_bar entry keeps today's rule — a human
		// decides (testBarRerun answers false: nothing to prove it with).
		if !testBarRerun(r.Name, strings.TrimSpace(r.PRURL), spec.VerifiedTestBar, evidenceEvents(ev, filepath.Dir(spec.ReposDir))) {
			return "the tests are not shown to pass on the pushed head (the last verification pass was a light recheck, which never ran the full bar: only this dispatch's recorded re-run of the carried test_bar, after its last merge and before its gh pr merge, covers them)"
		}
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
	def := originDefaultRef(dir)
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
// normalised (by barSegments), nothing else removed. Leading
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
		var segs, bodies [][]string
		switch {
		case len(e.Argv) == 1:
			segs, bodies = barSegmentsFacts(e.Argv[0])
		case len(e.Argv) > 2 && shells[filepath.Base(e.Argv[0])] && strings.HasPrefix(e.Argv[1], "-") && strings.Contains(e.Argv[1], "c"):
			segs, bodies = barSegmentsFacts(e.Argv[2])
		default:
			segs, bodies = barArgvFacts(e.Argv)
		}
		out = appendSegments(out, dir, segs, bodies, e.ExitCode, capsuleRoot)
	}
	return out
}

// barSegmentsFacts is barSegments plus, per segment, the bodies of the
// command substitutions it contains — taken by the same scanner while it
// knows quoting, so a $(...) inside single quotes stays literal.
func barSegmentsFacts(s string) ([][]string, [][]string) {
	var bodies [][]string
	segs := scanSegments(s, true, func(_ []string, b []string) { bodies = append(bodies, b) })
	return segs, bodies
}

// barArgvFacts is barArgvWords plus the substitution bodies of the words
// that carry one. A recorded argv loses whether its substitution was
// quoted; a word that still holds `$(` or a backtick is read as the
// substitution the comparison key already treats it as. The argv stays
// one segment, so every body is flattened into that segment's single
// entry: a later word's substitution must not be dropped behind an
// earlier word's plain `$VAR`.
func barArgvFacts(argv []string) ([][]string, [][]string) {
	var bodies []string
	for _, w := range argv {
		if !strings.ContainsAny(w, "$`") {
			continue
		}
		scanSegments(w, true, func(_ []string, b []string) { bodies = append(bodies, b...) })
	}
	return [][]string{barArgvWords(argv)}, [][]string{bodies}
}

// appendSegments flattens one evidence entry's simple commands into
// events in order. A `cd` moves the entry's directory and is no event
// itself; a substitution contributes the facts it hides (nestedEvents).
func appendSegments(out []evEvent, dir string, segs, bodies [][]string, exit int, capsuleRoot string) []evEvent {
	for i, seg := range segs {
		if i < len(bodies) {
			out = append(out, nestedEvents(dir, bodies[i], exit, capsuleRoot)...)
		}
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
		out = append(out, commandEvent(dir, seg, words, exit, capsuleRoot))
	}
	return out
}

// commandEvent is one simple command as the evidence reads it: the repo
// it ran in (a leading `cd` already applied, `git -C` wins), its words,
// its comparison key, its exit status and its own merge / PR-merge
// facts.
func commandEvent(dir string, seg, words []string, exit int, capsuleRoot string) evEvent {
	ev := evEvent{repo: repoOfRel(dir), words: words, key: barKey(seg), exit: exit}
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
	return ev
}

// barArgvWords is a multi-word argv as the comparison sees it: every
// word is one word already, and a recorded argv loses whether a
// substitution inside it was quoted, so only a word that still carries
// shell syntax ($, a backtick or a quote) goes through the scanner —
// the same one as the bar side — and every other word stays literal. An
// unquoted `|` or `(` in an argv word is an argument, not a pipe
// (workbench#58 entry 29: `-run A|B` is one word).
func barArgvWords(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, w := range argv {
		if strings.ContainsAny(w, "$`'\"") {
			if segs := barSegments(w); len(segs) == 1 && len(segs[0]) > 0 {
				out = append(out, segs[0]...)
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

// nestedEvents reads the commands a command substitution hides (bodies
// are the substitution bodies of one simple command, barSegments keeps
// $(...) inside its word) as events of their own. They carry no
// comparison key — only the window facts — and keep their own words and
// repo: a `git -C` or a `cd` inside the substitution, scoped to it (a
// subshell cannot move the dispatch's directory), and the PR URL a
// hidden `gh pr merge` names. A body is split by shellSegments, which
// has no notion of a heredoc: text a heredoc feeds (say a PR body with
// `--body "$(cat <<'EOF' … EOF)"`) is read as commands too. No recorded
// dispatch uses that shape (bodies go through `--body-file`); the
// limitation is noted here, not handled.
func nestedEvents(dir string, bodies []string, exit int, capsuleRoot string) []evEvent {
	var out []evEvent
	for _, body := range bodies {
		inside := dir
		for _, inner := range shellSegments(body) {
			words := commandWords(inner)
			if words == nil {
				continue
			}
			if words[0] == "cd" {
				if len(words) > 1 {
					inside = joinRel(inside, words[1], capsuleRoot)
				}
				continue
			}
			ev := commandEvent(inside, inner, words, exit, capsuleRoot)
			ev.key = ""
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
// AFTER the dispatch's last merge in that repo (no merge in this
// dispatch: anywhere in it) and BEFORE the first `gh pr merge` of that
// repo's PR after that merge — run in the repo or naming prURL —
// since the PR merge is what the re-run must cover (polyforge#203).
// Only that window is ordered: each bar command is matched by itself,
// in any order (workbench#60 — a dispatch that recorded the bar in its
// execution order still re-ran it). No evidence event serves two bar
// keys. Both sides compare exactly, as barKey (quotes stripped,
// whitespace collapsed, redirections dropped, a command substitution one
// canonical word; leading assignments and wrappers kept; a leading `cd`
// sets the repo). No test bar for the repo means nothing to prove it
// with.
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
	var keys []string
	for _, tb := range bar {
		if tb.Repo != repo {
			continue
		}
		for _, seg := range barSegments(tb.Command) {
			words := commandWords(seg)
			if words == nil || words[0] == "cd" {
				continue
			}
			keys = append(keys, barKey(seg))
		}
	}
	if len(keys) == 0 {
		return false
	}
	used := make([]bool, end-start)
	for _, key := range keys {
		found := false
		for i := start; i < end; i++ {
			if used[i-start] {
				continue
			}
			if e := events[i]; e.repo == repo && e.exit == 0 && e.key == key {
				used[i-start] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// originDefaultRef names the fetched origin default branch
// (refs/remotes/origin/HEAD, else origin's main or master), or "" when
// none is known.
func originDefaultRef(dir string) string {
	if out, err := repoGit(dir, "symbolic-ref", "-q", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(out)
	}
	for _, b := range []string{"main", "master"} {
		if _, err := repoGit(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+b); err == nil {
			return "refs/remotes/origin/" + b
		}
	}
	return ""
}

// checkNothingToDeliver: a skipped repo has no commit beyond the fetched
// origin default branch. capsule.json repos is not the anchor: a
// re-materialization re-stamps it to the restored HEAD (the carrier
// restore), which would make any restored work look delivered
//. A pushed feature branch is not the anchor either —
// pushed-but-unmerged work is still undelivered.
func checkNothingToDeliver(reposDir, repo string) error {
	if reposDir == "" {
		return nil
	}
	dir := filepath.Join(reposDir, repo)
	def := originDefaultRef(dir)
	if def == "" {
		return errors.New("the fetched origin default branch is unknown, so nothing-to-deliver cannot be confirmed")
	}
	out, err := repoGit(dir, "log", "--format=%h", def+"..HEAD")
	if err != nil {
		return fmt.Errorf("its history is unreadable: %v", err)
	}
	if commits := strings.Fields(out); len(commits) > 0 {
		named := commits
		if len(named) > 10 {
			named = append(named[:10:10], "…")
		}
		return fmt.Errorf("it has %d commit(s) beyond %s (%s): deliver them, the skip would strand them", len(commits), strings.TrimPrefix(def, "refs/remotes/"), strings.Join(named, " "))
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

// lightRecheckHeads reads the recheck heads of a light-passed verification
// record (mechanical_pass.recheck_heads, driver-written) — the deliver
// merge rule's anchor on the light path (polyforge#260: a light pass
// never writes verified_heads).
func lightRecheckHeads(st *capsule.Step) map[string]string {
	if st == nil || st.Status != capsule.StepDone {
		return nil
	}
	mp, ok := st.Output[mechanicalPassKey].(map[string]any)
	if !ok {
		return nil
	}
	heads := map[string]string{}
	if h, ok := mp["recheck_heads"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				heads[k] = s
			}
		}
	}
	return heads
}
