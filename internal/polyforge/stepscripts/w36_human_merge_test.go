package stepscripts

// w36_human_merge_test.go (u6): merge.py in the human-merge mode
// (runtime-state/step-modes.json names the merge step `human`,
// polyforge#36) — the mode the develop lane's merge step runs in when
// the project declares `steps.<project>.merge.by: human`. The script
// then never merges anything itself: a PR the person merged is taken by
// the human landing rules (each of GitHub's three merge methods lands
// the verified change, as the commit set with its merge flags), and a
// PR that did not land — closed without merging, merged with content
// that does not match the verified change, still open (the default
// branch's freshness is never judged in this mode) — is exit 3, the
// verdict partial: the step asks the owner, never a FAIL back to sync,
// and the summary never carries the lane's uncounted-rewind substring.
// The landing record merge_commits carries each landed commit with its
// merge flag (the default mode's record alike). Without the mode file
// the script behaves exactly as before.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w36Git runs git in dir with a fixed committer date (a landing's replay
// of commits made seconds ago would otherwise answer the original shas).
func w36Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=human", "GIT_AUTHOR_EMAIL=human@example.invalid",
		"GIT_COMMITTER_NAME=human", "GIT_COMMITTER_EMAIL=human@example.invalid",
		"GIT_COMMITTER_DATE=2005-04-05T22:13:13 +0000")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w36ModeFile writes the capsule's step modes naming merge human.
func w36ModeFile(t *testing.T, w *gitWorld) {
	t.Helper()
	writeFile(t, filepath.Join(w.capsule, "runtime-state"), "step-modes.json", `{"merge": "human"}`+"\n")
}

// w36HumanLands performs the person's landing of the published branch on
// origin's main the named GitHub way (merge, rebase or squash; amend
// changes the landed content — a squash of something else) and marks the
// PR MERGED at the forge's mergeCommit. Answers the commit set the
// person landed as, oldest first (each with its merge flag).
func w36HumanLands(t *testing.T, w *gitWorld, repo, method string, amend map[string]string) []landedCommit {
	t.Helper()
	tmp := filepath.Join(w.root, "human-"+method)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	w36Git(t, w.root, "clone", "-q", filepath.Join(w.origins, repo+".git"), tmp)
	w36Git(t, tmp, "fetch", "-q", "origin")
	w36Git(t, tmp, "checkout", "-q", "-B", "main", "origin/main")
	switch method {
	case "merge":
		w36Git(t, tmp, "merge", "-q", "--no-ff", "-m", "Merge pull request #1 from x/"+w.branch, "origin/"+w.branch)
	case "rebase":
		fork := w36Git(t, tmp, "merge-base", "origin/main", "origin/"+w.branch)
		for _, c := range strings.Fields(w36Git(t, tmp, "rev-list", "--reverse", "--no-merges", fork+"..origin/"+w.branch)) {
			w36Git(t, tmp, "cherry-pick", c)
		}
	case "squash":
		w36Git(t, tmp, "merge", "-q", "--squash", "origin/"+w.branch)
		for rel, body := range amend {
			writeFile(t, tmp, rel, body)
		}
		w36Git(t, tmp, "add", "-A")
		w36Git(t, tmp, "commit", "-q", "-m", w.branch+" (#1)")
	}
	w36Git(t, tmp, "push", "-q", "origin", "HEAD:refs/heads/main")
	merge := w36Git(t, tmp, "rev-parse", "HEAD")
	d := w.prDir(repo, 1)
	for k, v := range map[string]string{"state": "MERGED\n", "merge": merge + "\n"} {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := []landedCommit{{Sha: merge, Merge: method == "merge"}}
	if method == "rebase" {
		out = []landedCommit{
			{Sha: w36Git(t, tmp, "rev-parse", merge+"^"), Merge: false},
			{Sha: merge, Merge: false},
		}
	}
	return out
}

// w36Inputs publishes repo's branch and writes the publish artifact and
// a verified result whose details carry the default branch's base (the
// freshness the default mode judges). Answers the publish entry, the
// verified head and the artifact paths.
func w36Inputs(t *testing.T, w *gitWorld, repo, section2 string) (publishEntry, string, string, string, string) {
	t.Helper()
	w.change(repo, map[string]string{"feature.txt": "f\n"}, "a-section")
	head := w.change(repo, map[string]string{"feature2.txt": "f2\n"}, section2)
	e := w.publish(repo)
	base := w.originMain(repo)
	publish := filepath.Join(w.capsule, "artifacts", "publish.json")
	verified := filepath.Join(w.capsule, "artifacts", "verify", "result.json")
	out := filepath.Join(w.capsule, "artifacts", "merge", "result.json")
	w.writePublish(publish, e)
	if err := WriteResult(verified, &Result{Schema: Schema, Command: "verify.py", Verdict: VerdictPass,
		Heads:   map[string]string{repo: head},
		Details: map[string]any{"repos": []any{map[string]any{"name": repo, "base": base}}}, Logs: []string{}}); err != nil {
		t.Fatal(err)
	}
	return e, head, publish, verified, out
}

// w36RunMerge runs merge.py in the human-merge mode (the mode file and
// PF_STEP both set) and answers the exit, the result and its details.
func w36RunMerge(t *testing.T, w *gitWorld, publish, verified, out string) (int, *Result, *mergeDetails, string) {
	t.Helper()
	exit, _, errb := invokePy(t, scriptMerge, []string{"--capsule", w.capsule, "--publish", publish, "--verified", verified, "--landing-out", landingPath(out), "--json", "--out", out},
		w.env("PF_ROUND=round-h", "PF_STEP=merge"))
	res := readResult(t, out)
	var d mergeDetails
	detailsOf(t, res, &d)
	return exit, res, &d, errb
}

// w36Landing reads the landing record beside the result as the shaped
// JSON (nil when there is none).
func w36Landing(t *testing.T, out string) *landingRecord {
	t.Helper()
	raw := readLanding(t, out)
	if raw == nil {
		return nil
	}
	var rec landingRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("the landing record: %v\n%s", err, raw)
	}
	return &rec
}

func TestW36HumanMergeTakesAllThreeMethods(t *testing.T) {
	for _, method := range []string{"merge", "rebase", "squash"} {
		t.Run(method, func(t *testing.T) {
			w := newGitWorld(t, "app")
			e, head, publish, verified, out := w36Inputs(t, w, "app", "b-section")
			w36ModeFile(t, w)
			landedSet := w36HumanLands(t, w, "app", method, nil)
			// the landing moved the default branch past the verified
			// base: the human mode never judges the freshness
			exit, res, d, errb := w36RunMerge(t, w, publish, verified, out)
			if exit != 0 || res.Verdict != VerdictPass {
				t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
			}
			r := mergeRepoOf(t, d, "app")
			if r.How != HowMergedByHuman || r.ByThisRun || r.Head != head || r.MergeCommit != landedSet[len(landedSet)-1].Sha || r.Why != "" {
				t.Fatalf("taken by the human landing rules: %+v (landed %v)", r, landedSet)
			}
			if got := d.MergeCommits["app"]; len(got) != len(landedSet) {
				t.Fatalf("details merge_commits %v, want %v", got, landedSet)
			}
			rec := w36Landing(t, out)
			if rec == nil || rec.State != "landed" || len(rec.Repos) != 1 {
				t.Fatalf("the landing record: %+v", rec)
			}
			lr := rec.Repos[0]
			if lr.Repo != "app" || lr.PR != e.PRURL || lr.Head != head || lr.LightVerifiedHead != head || lr.MergeCommit != r.MergeCommit || lr.Accepted {
				t.Fatalf("the landing record's repo entry: %+v", lr)
			}
			if got := rec.MergeCommits["app"]; len(got) != len(landedSet) || got[len(got)-1].Sha != r.MergeCommit {
				t.Fatalf("the record's merge_commits %v, want the landed set %v", got, landedSet)
			}
			for i, c := range rec.MergeCommits["app"] {
				if c != landedSet[i] {
					t.Fatalf("the record's merge_commits[%d] = %+v, want %+v", i, c, landedSet[i])
				}
			}
			if calls := countLines(t, filepath.Join(w.ghState, "calls.log"), "gh pr merge"); calls != 0 {
				t.Fatalf("the human mode never asks the forge to merge: %d call(s)", calls)
			}
		})
	}
}

func TestW36HumanMergeMismatchAsksTheOwner(t *testing.T) {
	w := newGitWorld(t, "app")
	_, head, publish, verified, out := w36Inputs(t, w, "app", "b-section")
	w36ModeFile(t, w)
	w36HumanLands(t, w, "app", "squash", map[string]string{"feature2.txt": "changed on the way\n"})
	exit, res, d, errb := w36RunMerge(t, w, publish, verified, out)
	if exit != 3 || res.Verdict != "partial" {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	r := mergeRepoOf(t, d, "app")
	if r.How != "" || !strings.Contains(r.Why, "does not match the verified change") || !strings.Contains(r.Why, short(head)) {
		t.Fatalf("the mismatch names what it compared: %+v", r)
	}
	if strings.Contains(res.Summary, "the default branch moved since verification") {
		t.Fatalf("the ask never carries the uncounted-rewind substring: %q", res.Summary)
	}
	if rec := w36Landing(t, out); rec != nil {
		t.Fatalf("a run that landed nothing left a landing record: %+v", rec)
	}
}

func TestW36HumanMergeClosedAsksTheOwner(t *testing.T) {
	w := newGitWorld(t, "app")
	_, _, publish, verified, out := w36Inputs(t, w, "app", "b-section")
	w36ModeFile(t, w)
	if err := os.WriteFile(filepath.Join(w.prDir("app", 1), "state"), []byte("CLOSED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d, errb := w36RunMerge(t, w, publish, verified, out)
	if exit != 3 || res.Verdict != "partial" {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	if r := mergeRepoOf(t, d, "app"); r.How != "" || !strings.Contains(r.Why, "is closed without merging") {
		t.Fatalf("the closed PR asks the owner with its own reason: %+v", r)
	}
	if strings.Contains(res.Summary, "the default branch moved since verification") {
		t.Fatalf("the ask never carries the uncounted-rewind substring: %q", res.Summary)
	}
	if rec := w36Landing(t, out); rec != nil {
		t.Fatalf("a closed PR left a landing record: %+v", rec)
	}
}

func TestW36HumanMergeNeedsTheModeFile(t *testing.T) {
	// without the mode file the closed PR is the default mode's refusal
	// (exit 1, back to sync) and an open PR over a moved default branch
	// is its freshness refusal — the human mode turns both into its own
	// ask (exit 3), the freshness never judged
	w := newGitWorld(t, "app")
	_, _, publish, verified, out := w36Inputs(t, w, "app", "b-section")
	if err := os.WriteFile(filepath.Join(w.prDir("app", 1), "state"), []byte("CLOSED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, _, _ := w36RunMerge(t, w, publish, verified, out)
	if exit != 1 || res.Verdict != VerdictFail || !strings.Contains(res.Summary, "is closed") {
		t.Fatalf("the default mode's closed refusal: exit %d, %+v", exit, res)
	}
	if rec := w36Landing(t, out); rec != nil {
		t.Fatalf("the default mode's refusal left a landing record: %+v", rec)
	}

	// an open PR whose default branch moved since verification: the
	// default mode's freshness refusal, then — the mode file set — the
	// human mode's ask (exit 3, still open), never the freshness
	w2 := newGitWorld(t, "app")
	_, _, publish, verified, out = w36Inputs(t, w2, "app", "b-section")
	w2.mainMoves("app", map[string]string{"stray.txt": "s\n"}, "stray-section")
	exit, res, _, errb := w36RunMerge(t, w2, publish, verified, out)
	if exit != 1 || !strings.Contains(res.Summary, "the default branch moved since verification") {
		t.Fatalf("the default mode's freshness refusal: exit %d, %+v\n%s", exit, res, errb)
	}
	if calls := countLines(t, filepath.Join(w2.ghState, "calls.log"), "gh pr merge"); calls != 0 {
		t.Fatalf("the refused default merge asked the forge: %d call(s)", calls)
	}
	w36ModeFile(t, w2)
	exit, res, d, errb := w36RunMerge(t, w2, publish, verified, out)
	if exit != 3 || res.Verdict != "partial" {
		t.Fatalf("the human mode's ask: exit %d, %+v\n%s", exit, res, errb)
	}
	if r := mergeRepoOf(t, d, "app"); !strings.Contains(r.Why, "is still open") || strings.Contains(r.Why, "the default branch moved") {
		t.Fatalf("the human mode asks over the open PR, never the freshness: %+v", r)
	}
	fmt.Println("the human mode's routes are its own; the default mode's are unchanged")
}

// w36PlainLands lands plain commits on origin's main of repo (a person's
// squash or rebase landing: no merge commit) and answers their shas,
// oldest first.
func w36PlainLands(t *testing.T, w *gitWorld, repo string, commits []map[string]string) []string {
	t.Helper()
	seed := w.seeds[repo]
	gitT(t, seed, "fetch", "-q", "origin")
	gitT(t, seed, "checkout", "-q", "-B", "main", "origin/main")
	var out []string
	for i, files := range commits {
		for rel, body := range files {
			writeFile(t, seed, rel, body)
		}
		out = append(out, commitAll(t, seed, fmt.Sprintf("plain landing %d", i)))
	}
	gitT(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	return out
}

// w36BatchWithObjects writes the collect result at path with items whose
// merge_commits are the #36 record's object entries (each sha with its
// merge flag) beside one legacy string-shaped item.
func w36BatchWithObjects(t *testing.T, w *gitWorld, path string, pins map[string]string, squash, rebase []string, legacy string) {
	t.Helper()
	squashEntries, rebaseEntries, legacyEntries := []any{}, []any{}, []any{}
	for _, sha := range squash {
		squashEntries = append(squashEntries, map[string]any{"sha": sha, "merge": false})
	}
	for _, sha := range rebase {
		rebaseEntries = append(rebaseEntries, map[string]any{"sha": sha, "merge": false})
	}
	legacyEntries = append(legacyEntries, legacy)
	raw, _ := json.MarshalIndent(map[string]any{
		"schema": Schema, "command": "integrate.py", "verdict": "pass", "exit_code": 0,
		"heads": pins,
		"details": map[string]any{
			"batch": "wi_batch", "gated": []string{"app"},
			"items": []any{
				map[string]any{"id": "wi_sq", "slug": "p#1", "merge_commits": map[string]any{"app": squashEntries}},
				map[string]any{"id": "wi_rb", "slug": "p#2", "merge_commits": map[string]any{"app": rebaseEntries}},
				map[string]any{"id": "wi_m", "slug": "p#3", "merge_commits": map[string]any{"app": legacyEntries}},
			},
		},
	}, "", "  ")
	writeFile(t, filepath.Dir(path), filepath.Base(path), string(raw))
}

// TestW36RevertRollsBackSquashAndRebaseLandings: the record's object
// entries (a squash landing's one plain commit, a rebase landing's two —
// each named with its merge flag, polyforge#36) and a legacy string entry
// all revert: revert.py asks the mainline flag of the commit itself, so
// the plain commits revert without -m 1 and the merge commit as before.
func TestW36RevertRollsBackSquashAndRebaseLandings(t *testing.T) {
	w := newGitWorld(t, "app")
	squash := w36PlainLands(t, w, "app", []map[string]string{{"squash.txt": "s\n"}})
	rebase := w36PlainLands(t, w, "app", []map[string]string{{"rebase1.txt": "r1\n"}, {"rebase2.txt": "r2\n"}})
	legacy := w.land("app", "wi/legacy", map[string]string{"legacy.txt": "l\n"}, "legacy-section")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	w36BatchWithObjects(t, w, batch, map[string]string{"app": w.originMain("app")}, squash, rebase, legacy)
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, res, _, errb := runRevert(w, out, "--item", "p#1", "--item", "p#2", "--item", "p#3")
	if exit != 0 || res.Verdict != VerdictPass {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	// the squash and the rebase landings are gone, the merge landing's
	// change with them, the upgrade log untouched
	gitT(t, w.dir("app"), "checkout", "-q", "--detach", w.originMain("app"))
	for _, rel := range []string{"squash.txt", "rebase1.txt", "rebase2.txt", "legacy.txt"} {
		if _, err := os.Stat(filepath.Join(w.dir("app"), rel)); !os.IsNotExist(err) {
			t.Fatalf("%s is reverted", rel)
		}
	}
}
