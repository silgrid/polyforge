package stepscripts

// merge_test.go: merge.py (workflows/_shared/scripts) against the fake gh — the published PRs merged
// with --match-head-commit at the verified heads, a PR moved off the
// verified head (nothing merged), every repo judged before anything
// merges, a PR merged elsewhere taken by the landing rule or refused, a
// crashed run's own merge taken on re-entry, the forge's refusal, and the
// refused invocations; and the landing record --landing-out writes on a
// landing (workflow contract 4: registered with `pf step hub wrap-attrs
// integration`), removed when the run does not land.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mergeInputs publishes every repo of the world and writes the publish
// artifact and the verification result (heads = the published heads),
// answering their paths and the result path.
func mergeInputs(w *gitWorld) (publish, verified, out string) {
	var entries []publishEntry
	heads := map[string]string{}
	for _, r := range w.repos {
		e := w.publish(r)
		entries = append(entries, e)
		heads[r] = e.Head
	}
	publish = filepath.Join(w.capsule, "artifacts", "publish.json")
	verified = filepath.Join(w.capsule, "artifacts", "verify", "result.json")
	w.writePublish(publish, entries...)
	w.writeVerified(verified, heads)
	return publish, verified, filepath.Join(w.capsule, "artifacts", "merge", "result.json")
}

// runMerge runs merge.py and answers the exit, the result and its details.
func runMerge(w *gitWorld, publish, verified, out string) (int, *Result, *mergeDetails, string) {
	t := w.t
	t.Helper()
	exit, _, errb := invokePy(t, scriptMerge, []string{"--capsule", w.capsule, "--publish", publish, "--verified", verified, "--landing-out", landingPath(out), "--json", "--out", out}, w.env("PF_ROUND=round-m"))
	res := readResult(t, out)
	var d mergeDetails
	detailsOf(t, res, &d)
	return exit, res, &d, errb
}

// landingPath answers the landing record's path beside the result.
func landingPath(out string) string { return filepath.Join(filepath.Dir(out), "landing.json") }

// readLanding reads the landing record beside the result, as JSON text
// (nil when there is none); a field the record does not declare refuses
// it.
func readLanding(t *testing.T, out string) []byte {
	t.Helper()
	raw, err := os.ReadFile(landingPath(out))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var l landingRecord
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		t.Fatalf("the landing record is not one: %v\n%s", err, raw)
	}
	compact, _ := json.Marshal(l)
	return compact
}

func mergeRepoOf(t *testing.T, d *mergeDetails, name string) *mergeRepo {
	t.Helper()
	for _, r := range d.Repos {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no repos entry for %s: %+v", name, d.Repos)
	return nil
}

func TestMergeMergesAtTheVerifiedHead(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	appHead := w.change("app", map[string]string{"feature.txt": "f\n"}, "app-section")
	libHead := w.change("lib", map[string]string{"lib.txt": "l\n"}, "")
	publish, verified, out := mergeInputs(w)
	exit, res, d, errb := runMerge(w, publish, verified, out)
	if exit != 0 || res.Verdict != VerdictPass || res.Round != "round-m" {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	for repo, head := range map[string]string{"app": appHead, "lib": libHead} {
		r := mergeRepoOf(t, d, repo)
		if r.How != HowMerged || !r.ByThisRun || r.Head != head || r.MergeCommit == "" || r.MergeCommit != w.prFile(repo, 1, "merge") || r.BranchSide != head || r.Why != "" {
			t.Fatalf("%s: %+v (fake merge %s)", repo, r, w.prFile(repo, 1, "merge"))
		}
		if w.prFile(repo, 1, "state") != "MERGED" || w.originMain(repo) != r.MergeCommit {
			t.Fatalf("%s: the PR is merged on origin's main: %s %s", repo, w.prFile(repo, 1, "state"), w.originMain(repo))
		}
		if ps := parents(t, w.dir(repo), r.MergeCommit); len(ps) != 2 || ps[1] != head {
			t.Fatalf("%s: a merge commit of the verified head: %v", repo, ps)
		}
		if got := d.MergeCommits[repo]; len(got) != 1 || got[0] != r.MergeCommit {
			t.Fatalf("%s: merge_commits %v", repo, d.MergeCommits)
		}
		if res.Heads[repo] != head {
			t.Fatalf("%s: heads %v", repo, res.Heads)
		}
	}
	if !strings.Contains(res.Summary, "2 PR(s) landed") {
		t.Fatalf("summary %q", res.Summary)
	}
	// the landing record: per repo its PR, the verified head as both the
	// merged and the light-verified head, its merge commit; no waiver
	app, lib := mergeRepoOf(t, d, "app"), mergeRepoOf(t, d, "lib")
	want, _ := json.Marshal(landingRecord{State: "landed", Repos: []landingRepo{
		{Repo: "app", PR: app.PRURL, Head: appHead, LightVerifiedHead: appHead, MergeCommit: app.MergeCommit},
		{Repo: "lib", PR: lib.PRURL, Head: libHead, LightVerifiedHead: libHead, MergeCommit: lib.MergeCommit},
	}, MergeCommits: map[string][]string{"app": {app.MergeCommit}, "lib": {lib.MergeCommit}}})
	if got := readLanding(t, out); string(got) != string(want) {
		t.Fatalf("the landing record:\n got %s\nwant %s", got, want)
	}
	if raw, _ := os.ReadFile(landingPath(out)); strings.Contains(string(raw), "waived") || strings.Contains(string(raw), "accepted") {
		t.Fatalf("the lane's own merge carries no waiver and no acceptance: %s", raw)
	}
	// a re-run (a crash after the merge, before the result): the merges
	// are found and taken as the lane's own
	exit, _, d, errb = runMerge(w, publish, verified, out)
	if exit != 0 {
		t.Fatalf("re-run: exit %d\n%s", exit, errb)
	}
	if r := mergeRepoOf(t, d, "app"); r.How != HowMerged || r.ByThisRun || r.MergeCommit != w.prFile("app", 1, "merge") {
		t.Fatalf("re-run takes the merge as the lane's: %+v", r)
	}
	if calls := countLines(t, filepath.Join(w.ghState, "calls.log"), "gh pr merge"); calls != 2 {
		t.Fatalf("the forge was asked to merge twice in all, not again on the re-run: %d", calls)
	}
}

func TestMergeAMovedHeadMergesNothing(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	w.change("lib", map[string]string{"lib.txt": "l\n"}, "")
	publish, verified, out := mergeInputs(w)
	// lib's branch moves after verification
	moved := w.change("lib", map[string]string{"lib.txt": "l2\n"}, "")
	gitT(t, w.dir("lib"), "push", "-q", "origin", "HEAD:refs/heads/"+w.branch)
	writeFile(t, filepath.Dir(out), "landing.json", `{"state":"landed","repos":[]}`) // an earlier run's
	exit, res, d, errb := runMerge(w, publish, verified, out)
	if exit != 1 || res.Verdict != VerdictFail || !strings.HasPrefix(res.Summary, "nothing merged") {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	if got := readLanding(t, out); got != nil {
		t.Fatalf("a run that landed nothing left a landing record: %s", got)
	}
	lib := mergeRepoOf(t, d, "lib")
	if !strings.Contains(lib.Why, "not the verified head") || !strings.Contains(lib.Why, short(moved)) || lib.How != "" {
		t.Fatalf("lib: %+v", lib)
	}
	// app was fine, and still is not merged: every repo is judged first
	if app := mergeRepoOf(t, d, "app"); app.How != "" || app.Why != "" || w.prFile("app", 1, "state") != "OPEN" || w.prFile("lib", 1, "state") != "OPEN" {
		t.Fatalf("app: %+v, states %s %s", app, w.prFile("app", 1, "state"), w.prFile("lib", 1, "state"))
	}
	if calls := countLines(t, filepath.Join(w.ghState, "calls.log"), "gh pr merge"); calls != 0 {
		t.Fatalf("the forge was never asked: %d", calls)
	}
}

func TestMergeTakesAPRMergedElsewhereByTheLandingRule(t *testing.T) {
	w := newGitWorld(t, "app")
	verifiedHead := w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, out := mergeInputs(w)
	// someone pushes one more commit and merges the PR by hand: the merge
	// commit is on main and contains the verified head
	later := w.change("app", map[string]string{"feature.txt": "f2\n"}, "")
	gitT(t, w.dir("app"), "push", "-q", "origin", "HEAD:refs/heads/"+w.branch)
	if out, err := w.gh(w.dir("app"), "pr", "merge", w.prURL("app", 1), "--merge", "--match-head-commit", later); err != nil {
		t.Fatalf("merging by hand: %v\n%s", err, out)
	}
	exit, res, d, errb := runMerge(w, publish, verified, out)
	if exit != 0 {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	r := mergeRepoOf(t, d, "app")
	if r.How != HowMergedElsewhere || r.ByThisRun || r.MergeCommit != w.prFile("app", 1, "merge") || r.BranchSide != later || r.Head != verifiedHead {
		t.Fatalf("taken by the landing rule: %+v", r)
	}
	// its landing: the merge's branch side as the head, the verified head
	// as the light-verified one, accepted (nobody verified that merge)
	want, _ := json.Marshal(landingRecord{State: "landed", Repos: []landingRepo{
		{Repo: "app", PR: r.PRURL, Head: later, LightVerifiedHead: verifiedHead, MergeCommit: r.MergeCommit, Accepted: true},
	}, MergeCommits: map[string][]string{"app": {r.MergeCommit}}})
	if got := readLanding(t, out); string(got) != string(want) {
		t.Fatalf("the landing record of a merge made elsewhere:\n got %s\nwant %s", got, want)
	}
	// a merge commit that does not contain the verified head is refused
	w2 := newGitWorld(t, "app")
	head := w2.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, out = mergeInputs(w2)
	stray := w2.mainMoves("app", map[string]string{"stray.txt": "s\n"}, "")
	for k, v := range map[string]string{"state": "MERGED", "merge": stray} {
		if err := os.WriteFile(filepath.Join(w2.prDir("app", 1), k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exit, res, d, _ = runMerge(w2, publish, verified, out)
	r = mergeRepoOf(t, d, "app")
	if exit != 1 || r.How != "" || !strings.Contains(r.Why, "does not contain the light-verified head "+short(head)) || !strings.Contains(r.Why, "merged outside this step") {
		t.Fatalf("exit %d, %+v", exit, r)
	}
	// and a squash merge (no branch side) that lands the tree without the
	// head is refused the same way
	w3 := newGitWorld(t, "app")
	w3.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, out = mergeInputs(w3)
	squash := w3.mainMoves("app", map[string]string{"feature.txt": "f\n"}, "")
	for k, v := range map[string]string{"state": "MERGED", "merge": squash} {
		if err := os.WriteFile(filepath.Join(w3.prDir("app", 1), k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if exit, _, d, _ = runMerge(w3, publish, verified, out); exit != 1 || !strings.Contains(mergeRepoOf(t, d, "app").Why, "no branch side") {
		t.Fatalf("exit %d, %+v", exit, mergeRepoOf(t, d, "app"))
	}
}

func TestMergeTheForgesRefusalStopsThePass(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	w.change("lib", map[string]string{"lib.txt": "l\n"}, "")
	publish, verified, out := mergeInputs(w)
	// the forge refuses app's merge once (a conflict with main, say)
	if err := os.WriteFile(filepath.Join(w.prDir("app", 1), "fail-merge"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d, errb := runMerge(w, publish, verified, out)
	app := mergeRepoOf(t, d, "app")
	if exit != 1 || !strings.Contains(app.Why, "the forge refused the merge") || !strings.Contains(res.Summary, "1 later PR(s) stay open") {
		t.Fatalf("exit %d, %+v, %+v\n%s", exit, res, app, errb)
	}
	if w.prFile("app", 1, "state") != "OPEN" || w.prFile("lib", 1, "state") != "OPEN" || mergeRepoOf(t, d, "lib").How != "" {
		t.Fatal("nothing merged: the pass stops at the refusal, lib stays open")
	}
	// the next run merges both
	if exit, _, _, errb = runMerge(w, publish, verified, out); exit != 0 || w.prFile("app", 1, "state") != "MERGED" || w.prFile("lib", 1, "state") != "MERGED" {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	// a closed PR is refused
	w2 := newGitWorld(t, "app")
	w2.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, out = mergeInputs(w2)
	if err := os.WriteFile(filepath.Join(w2.prDir("app", 1), "state"), []byte("CLOSED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if exit, _, d, _ = runMerge(w2, publish, verified, out); exit != 1 || !strings.Contains(mergeRepoOf(t, d, "app").Why, "is closed") {
		t.Fatalf("exit %d, %+v", exit, mergeRepoOf(t, d, "app"))
	}
}

func TestMergeRefusesUnusableInvocations(t *testing.T) {
	w := newGitWorld(t, "app")
	w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	publish, verified, out := mergeInputs(w)
	noHead := filepath.Join(w.capsule, "artifacts", "verify", "none.json")
	w.writeVerified(noHead, map[string]string{})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--capsule", w.capsule, "--publish", publish}, "both required"},
		{[]string{"--capsule", w.capsule, "--publish", publish, "--verified", verified, "x"}, "unexpected arguments"},
		{[]string{"--capsule", w.capsule, "--publish", publish + ".missing", "--verified", verified}, "no such file"},
		{[]string{"--capsule", w.capsule, "--publish", publish, "--verified", noHead}, "names no head for the published repo app"},
	} {
		exit, _, errb := invokePy(t, scriptMerge, tc.args, w.env())
		if exit != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want 2 and %q)", tc.args, exit, errb, tc.want)
		}
	}
	if w.prFile("app", 1, "state") != "OPEN" {
		t.Fatal("a refused invocation merges nothing")
	}
	// a run that is not judged still writes the result
	exit, _, _ := invokePy(t, scriptMerge, []string{"--capsule", w.capsule, "--publish", publish, "--verified", noHead, "--out", out}, w.env())
	if res := readResult(t, out); exit != 2 || res.Verdict != VerdictError {
		t.Fatalf("exit %d, %+v", exit, res)
	}
}

// countLines answers how many lines of the file start with prefix.
func countLines(t *testing.T, path, prefix string) int {
	t.Helper()
	raw, _ := os.ReadFile(path)
	n := 0
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}
