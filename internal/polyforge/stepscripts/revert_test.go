package stepscripts

// revert_test.go: revert.py (workflows/_shared/scripts) against the fake gh — one item landed in two
// repos (with a landed fix ticket in one) reverted through one PR per
// repo, the pins and the gated repos written, a crash between the two
// repos resumed from the plan record (no second PR for the first repo,
// the open PR of the second merged), the reverts of an earlier round
// carried by a run without --item, the pins only copied when nothing is
// to revert, and the refused invocations; and the record writes --ops-out
// writes (registered with `pf step hub batch`): since workflow contract 1
// each the item's whole record as the step's hub reads it, moved to
// reverted under the lane's rules.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// runRevert runs revert.py over the capsule's batch.json and answers the
// exit, the result and its details.
func runRevert(w *gitWorld, out string, extra ...string) (int, *Result, *revertDetails, string) {
	t := w.t
	t.Helper()
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	args := append([]string{"--capsule", w.capsule, "--batch", batch, "--ops-out", opsPath(out), "--json", "--out", out}, extra...)
	exit, _, errb := invokePy(t, scriptRevert, args, withPath(w.env("PF_ROUND=round-r", "PF_WI_ID=wi_batch"), revertHub(t, batch).pfDir(t)))
	res := readResult(t, out)
	var d revertDetails
	detailsOf(t, res, &d)
	return exit, res, &d, errb
}

// revertHub answers the step's hub as revert.py reads it: every item and
// every held item of the batch file a wrapped work item whose integration
// record is landed with the item's merges (a held one blamed once).
func revertHub(t *testing.T, batch string) *fakeHubReads {
	t.Helper()
	raw, err := os.ReadFile(batch)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Details struct {
			Items []struct {
				ID           string                       `json:"id"`
				Slug         string                       `json:"slug"`
				MergeCommits map[string][]json.RawMessage `json:"merge_commits"`
			} `json:"items"`
			Holds []struct {
				WI           string                       `json:"wi"`
				Name         string                       `json:"name"`
				MergeCommits map[string][]json.RawMessage `json:"merge_commits"`
			} `json:"holds"`
		} `json:"details"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	h := &fakeHubReads{}
	add := func(id, slug string, merges map[string][]json.RawMessage, rec map[string]any) {
		// the record's merge_commits entries pass through as the batch
		// file carries them (a pre-#36 string, a #36 object)
		mc := map[string]any{}
		for repo, cs := range merges {
			entries := make([]any, 0, len(cs))
			for _, raw := range cs {
				var v any
				if json.Unmarshal(raw, &v) == nil {
					entries = append(entries, v)
				}
			}
			mc[repo] = entries
		}
		rec["state"], rec["merge_commits"] = "landed", mc
		h.items = append(h.items, api.WorkItem{ID: id, Slug: slug, Status: api.StatusWrapped, Attrs: map[string]any{"integration": rec}})
	}
	for _, it := range res.Details.Items {
		add(it.ID, it.Slug, it.MergeCommits, map[string]any{})
	}
	for _, it := range res.Details.Holds {
		add(it.WI, it.Name, it.MergeCommits, map[string]any{"red_rounds": 1})
	}
	return h
}

// opsPath answers the moves file beside the result.
func opsPath(out string) string { return filepath.Join(filepath.Dir(out), "revert-ops.json") }

// readOps reads the moves file beside the result as compact JSON ("" when
// there is none).
func readOps(t *testing.T, out string) string {
	t.Helper()
	raw, err := os.ReadFile(opsPath(out))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	var v []map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("the moves file is no JSON array: %v\n%s", err, raw)
	}
	compact, _ := json.Marshal(v)
	return string(compact)
}

func TestRevertOneItemTwoReposResumable(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	// the source lands in both repos, its fix ticket in app only, then
	// someone else lands too
	srcApp := w.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "src-section")
	srcLib := w.land("lib", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	fixApp := w.land("app", "wi/fix", map[string]string{"src.txt": "s-fixed\n"}, "fix-section")
	otherApp := w.land("app", "wi/other", map[string]string{"other.txt": "o\n"}, "")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	pins := map[string]string{"app": w.originMain("app"), "lib": w.originMain("lib")}
	w.writeBatch(batch, pins, []batchItem{
		{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {srcApp}, "lib": {srcLib}}, FixTickets: []string{"p#2"}},
		{ID: "wi_fix", Slug: "p#2", MergeCommits: map[string][]string{"app": {fixApp}}},
		{ID: "wi_other", Slug: "p#3", MergeCommits: map[string][]string{"app": {otherApp}}},
	})
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, res, d, errb := runRevert(w, out, "--item", "p#1")
	if exit != 0 || res.Verdict != VerdictPass || res.Round != "round-r" {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	if len(d.Reverts) != 1 || d.Reverts[0].WI != "wi_src" || d.Reverts[0].Name != "p#1" || strings.Join(d.Reverts[0].Group, ",") != "wi_src,wi_fix" || !d.Reverts[0].Done {
		t.Fatalf("the revert plan: %+v", d.Reverts)
	}
	e := d.Reverts[0]
	app, lib := e.repo("app"), e.repo("lib")
	if app == nil || lib == nil || strings.Join(app.MergeCommits, ",") != srcApp+","+fixApp || strings.Join(lib.MergeCommits, ",") != srcLib {
		t.Fatalf("the merges of the group per repo: %+v %+v", app, lib)
	}
	for _, r := range []*revertRepo{app, lib} {
		if r.RevertPR == "" || r.RevertMerge == "" || r.RevertMerge != w.originMain(r.Name) {
			t.Fatalf("%s: the revert PR merged onto main: %+v (main %s)", r.Name, r, w.originMain(r.Name))
		}
		if d.Heads[r.Name] != r.RevertMerge || res.Heads[r.Name] != r.RevertMerge {
			t.Fatalf("%s: the pin is the revert merge: %v %v", r.Name, d.Heads, res.Heads)
		}
		if w.head(r.Name) != r.RevertMerge {
			t.Fatalf("%s: the clone stands at the revert merge: %s", r.Name, w.head(r.Name))
		}
	}
	if strings.Join(d.Gated, ",") != "app,lib" {
		t.Fatalf("gated: %v", d.Gated)
	}
	// the record write of the landed revert: the whole record, to
	// reverted in this batch with its revert PRs and merges, the develop
	// lane's merges kept, fenced on the whole record as read
	want, _ := json.Marshal([]map[string]any{{"verb": "attrs", "target": "wi_src", "name": "integration", "value": map[string]any{
		"state": "reverted", "batch": "wi_batch", "revert_prs": []string{app.RevertPR, lib.RevertPR}, "revert_merges": map[string]string{"app": app.RevertMerge, "lib": lib.RevertMerge},
		"merge_commits": map[string][]string{"app": {srcApp}, "lib": {srcLib}}}, "if": map[string]any{"integration": map[string]any{
		"state": "landed", "merge_commits": map[string][]string{"app": {srcApp}, "lib": {srcLib}}}}}})
	if got := readOps(t, out); got != string(want) {
		t.Fatalf("the moves:\n got %s\nwant %s", got, want)
	}
	// the revert's tree: the source and its fix gone, the other kept, the
	// upgrade log and its pin untouched
	gitT(t, w.dir("app"), "checkout", "-q", "--detach", d.Heads["app"])
	if _, err := os.Stat(filepath.Join(w.dir("app"), "src.txt")); !os.IsNotExist(err) {
		t.Fatal("src.txt is reverted")
	}
	if fileIn(t, w.dir("app"), "other.txt") != "o\n" {
		t.Fatal("other.txt stays")
	}
	up := fileIn(t, w.dir("app"), "docs/upgrade.md")
	if up != "# upgrade\n\n## src-section\n\n## fix-section\n" || fileIn(t, w.dir("app"), "pin.txt") != pinOf(up)+"\n" {
		t.Fatalf("the append-only log keeps its bytes and its pin:\n%q", up)
	}
	if w.prCount("app") != 4 || w.prCount("lib") != 2 || w.prFile("app", 4, "head") != "integrate/revert-p-1" {
		t.Fatalf("one revert PR per repo on the revert branch: app %d, lib %d, %s", w.prCount("app"), w.prCount("lib"), w.prFile("app", 4, "head"))
	}
	// a run without --item carries the earlier round's revert, opens
	// nothing, and the pins stay
	exit, res2, d2, errb := runRevert(w, out)
	if exit != 0 || len(d2.Reverts) != 1 || d2.Reverts[0].repo("app").RevertMerge != app.RevertMerge || res2.Heads["app"] != res.Heads["app"] || w.prCount("app") != 4 {
		t.Fatalf("exit %d, %+v %v\n%s", exit, d2.Reverts, res2.Heads, errb)
	}
	// --item of an item reverted already is a no-op too
	if exit, _, _, _ = runRevert(w, out, "--item", "wi_src"); exit != 0 || w.prCount("app") != 4 {
		t.Fatalf("exit %d, PRs %d", exit, w.prCount("app"))
	}
}

func TestRevertResumesAfterACrashBetweenRepos(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	srcApp := w.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	srcLib := w.land("lib", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	w.writeBatch(batch, map[string]string{"app": w.originMain("app"), "lib": w.originMain("lib")}, []batchItem{
		{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {srcApp}, "lib": {srcLib}}},
	})
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, _, d, errb := runRevert(w, out, "--item", "p#1")
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	// simulate the crash between lib's PR merging and its record: the plan
	// record loses lib's landing and the entry's done — a re-run must find
	// lib's merged PR and read it back, not open another
	plan := d.Plan
	raw, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	libMerge := d.Reverts[0].repo("lib").RevertMerge
	trimmed := strings.Replace(strings.ReplaceAll(string(raw), libMerge, ""), `"done": true`, `"done": false`, 1)
	if trimmed == string(raw) {
		t.Fatalf("the plan record carries neither lib's merge nor done:\n%s", raw)
	}
	if err := os.WriteFile(plan, []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d2, errb := runRevert(w, out)
	if exit != 0 || d2.Reverts[0].repo("lib").RevertMerge != libMerge || res.Heads["lib"] != libMerge || w.prCount("lib") != 2 || w.prCount("app") != 2 {
		t.Fatalf("the re-run reads lib's merged PR back: exit %d, %+v, PRs app %d lib %d\n%s", exit, d2.Reverts[0].repo("lib"), w.prCount("app"), w.prCount("lib"), errb)
	}
	// a forge refusal: the revert PR opened, its merge refused — exit 1,
	// the plan keeps it; the next run merges that same PR
	w2 := newGitWorld(t, "app")
	src := w2.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch2 := filepath.Join(w2.capsule, "artifacts", "integrate", "batch.json")
	w2.writeBatch(batch2, map[string]string{"app": w2.originMain("app")}, []batchItem{{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {src}}}})
	out2 := filepath.Join(w2.capsule, "artifacts", "integrate", "revert.json")
	if err := os.WriteFile(filepath.Join(w2.ghState, "fail-created-merge"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d, errb = runRevert(w2, out2, "--item", "p#1")
	if exit != 1 || res.Verdict != VerdictFail || !strings.Contains(res.Summary, "did not land in repos/app") || d.Reverts[0].Done || d.Reverts[0].Why == "" {
		t.Fatalf("exit %d, %+v %+v\n%s", exit, res, d.Reverts[0], errb)
	}
	if w2.prCount("app") != 2 || w2.prFile("app", 2, "state") != "OPEN" || d.Reverts[0].repo("app").RevertPR != w2.prURL("app", 2) {
		t.Fatalf("the revert PR is open, on record: %d %s %+v", w2.prCount("app"), w2.prFile("app", 2, "state"), d.Reverts[0].repo("app"))
	}
	if res.Heads["app"] != w2.prFile("app", 1, "merge") {
		t.Fatalf("the pin is still the batch's while the revert is not landed: %v", res.Heads)
	}
	exit, res, d, errb = runRevert(w2, out2)
	if exit != 0 || w2.prCount("app") != 2 || w2.prFile("app", 2, "state") != "MERGED" || d.Reverts[0].repo("app").RevertMerge != w2.prFile("app", 2, "merge") || res.Heads["app"] != w2.originMain("app") || d.Reverts[0].Why != "" {
		t.Fatalf("the next run merges the same PR: exit %d, %+v\n%s", exit, d.Reverts[0], errb)
	}
}

func TestRevertCopiesThePinsWhenNothingIsToRevert(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	pins := map[string]string{"lib": w.originMain("lib"), "app": w.originMain("app")}
	w.writeBatch(batch, pins, nil)
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, res, d, errb := runRevert(w, out)
	if exit != 0 || len(d.Reverts) != 0 || strings.Join(d.Gated, ",") != "app,lib" || !strings.Contains(res.Summary, "nothing to revert") {
		t.Fatalf("exit %d, %+v\n%s", exit, d, errb)
	}
	for repo, sha := range pins {
		if d.Heads[repo] != sha || res.Heads[repo] != sha {
			t.Fatalf("%s: the pins are copied: %v %v", repo, d.Heads, res.Heads)
		}
	}
	if w.prCount("app") != 0 || w.prCount("lib") != 0 {
		t.Fatal("no PR opened")
	}
	// the plan record exists (empty) beside the result
	if d.Plan != filepath.Join(w.capsule, "artifacts", "integrate", revertPlanName) {
		t.Fatalf("plan %s", d.Plan)
	}
	// nothing reverted: no move (an empty array, registered as a no-op)
	if got := readOps(t, out); got != "[]" {
		t.Fatalf("no revert, no move: %q", got)
	}
	// an item ruled reverted with nothing on any default branch is done
	// without a revert: no move either
	w.writeBatch(batch, pins, []batchItem{{ID: "wi_none", Slug: "p#8", MergeCommits: map[string][]string{}}})
	if exit, _, d, errb := runRevert(w, out, "--item", "p#8"); exit != 0 || len(d.Reverts) != 1 || !d.Reverts[0].Done || len(d.Reverts[0].Repos) != 0 || readOps(t, out) != "[]" {
		t.Fatalf("exit %d, %+v, moves %q\n%s", exit, d.Reverts, readOps(t, out), errb)
	}
	// an item neither in the batch nor held is refused, and the moves of an
	// earlier run are not left to be registered as this one's
	exit, _, errb = invokePy(t, scriptRevert, []string{"--capsule", w.capsule, "--batch", batch, "--item", "p#9", "--ops-out", opsPath(out)}, w.env("PF_WI_ID=wi_batch"))
	if exit != 2 || !strings.Contains(errb, "neither a landed item of the batch") || readOps(t, out) != "" {
		t.Fatalf("exit %d, stderr %q, moves %q", exit, errb, readOps(t, out))
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--capsule", w.capsule}, "--batch <batch.json> is required"},
		{[]string{"--capsule", w.capsule, "--batch", batch + ".missing"}, "no such file"},
		{[]string{"--capsule", w.capsule, "--batch", batch, "x"}, "unexpected arguments"},
		{[]string{"--capsule", w.capsule, "--batch", batch, "--ops-out", opsPath(out)}, "--ops-out needs PF_WI_ID"},
	} {
		exit, _, errb := invokePy(t, scriptRevert, tc.args, w.env())
		if exit != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want 2 and %q)", tc.args, exit, errb, tc.want)
		}
	}
}

// TestRevertRevertsAHeldItemWithItsLandedFixes (workflow contract v2,
// change 13): a held item the owner ruled reverted on a stuck hold is
// seldom in the batch's items (its repo stands at the head the blaming
// gate failed on, so it is not gated); revert.py reads it from the
// batch's holds — its merges and its landed fix tickets' — and reverts the
// group, the pins following and the repo gated (the batch gated nothing:
// a repo a revert lands in is gated).
func TestRevertRevertsAHeldItemWithItsLandedFixes(t *testing.T) {
	w := newGitWorld(t, "app")
	src := w.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	fix := w.land("app", "wi/fix", map[string]string{"src.txt": "s-fixed\n"}, "")
	other := w.land("app", "wi/other", map[string]string{"other.txt": "o\n"}, "")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	pins := map[string]string{"app": w.originMain("app")}
	if err := WriteResult(batch, &Result{Schema: Schema, Command: NameIntegrate, Verdict: VerdictPass, Heads: pins,
		Details: map[string]any{"items": []any{}, "gated": []any{}, "holds": []any{map[string]any{
			"wi": "wi_src", "name": "p#1", "stuck": true, "merge_commits": map[string]any{"app": []any{src}},
			"landed_fixes": []any{map[string]any{"ref": "p#2", "merge_commits": map[string]any{"app": []any{fix}}}}}}}, Logs: []string{}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, res, d, errb := runRevert(w, out, "--item", "p#1")
	if exit != 0 || len(d.Reverts) != 1 || !d.Reverts[0].Done || strings.Join(d.Reverts[0].Group, ",") != "wi_src,p#2" {
		t.Fatalf("exit %d, %+v\n%s", exit, d.Reverts, errb)
	}
	app := d.Reverts[0].repo("app")
	if app == nil || strings.Join(app.MergeCommits, ",") != src+","+fix || app.RevertMerge == "" || app.RevertMerge != w.originMain("app") || res.Heads["app"] != app.RevertMerge || strings.Join(d.Gated, ",") != "app" {
		t.Fatalf("the hold's group reverted, the pin following, the repo gated (the batch gated nothing: the revert's repo joins): %+v %v %v", app, res.Heads, d.Gated)
	}
	gitT(t, w.dir("app"), "checkout", "-q", "--detach", d.Heads["app"])
	if _, err := os.Stat(filepath.Join(w.dir("app"), "src.txt")); !os.IsNotExist(err) {
		t.Fatal("the source and its fix are reverted")
	}
	if fileIn(t, w.dir("app"), "other.txt") != "o\n" || other == "" {
		t.Fatal("the other landing stays")
	}
}

// w98RepairPlan writes the repair step's plan file at path: one entry
// per repo the repair fixed (the branch off the pinned head, its head,
// the units and the items the gate attributed them to).
func w98RepairPlan(t *testing.T, path string, entries ...map[string]any) {
	t.Helper()
	repos := make([]any, 0, len(entries))
	for _, e := range entries {
		repos = append(repos, e)
	}
	raw, err := json.Marshal(map[string]any{"round": 1, "repos": repos})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// w98RepairBranch stands the repair step's branch in the capsule's
// clone: one commit fixing a stale test off the pinned head, answering
// the branch head.
func w98RepairBranch(w *gitWorld, repo, branch, pin string) string {
	t := w.t
	d := w.dir(repo)
	gitT(t, d, "fetch", "-q", "origin")
	gitT(t, d, "checkout", "-q", "-B", branch, pin)
	writeFile(t, d, "tests/stale_test.go", "package tests\n\n// the stale expectation the batch fixes in place\n")
	gitT(t, d, "add", "--", "tests/stale_test.go")
	gitT(t, d, "commit", "-q", "-m", "the batch's in-batch repair")
	return w.head(repo)
}

// TestRevertLandsTheBatchRepair (polyforge#98): the repair step's branch
// — one local commit per repo off the pinned head, never pushed by the
// repair — is landed by revert.py under the merge lock it already holds:
// pushed, one PR opened and merged onto the default branch matching the
// pushed head, the pins following the fix's merge (the gate re-runs on
// it, and the green tag lands on the default branch), the details
// carrying the fix for the batch's record. A crash between the merge and
// its record resumes from the merged PR; a refused merge is exit 1 with
// the PR kept for the next run; a fix whose blamed item the same run
// reverts is not landed at all (its PR is never opened).
func TestRevertLandsTheBatchRepair(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	src := w.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	pin := w.originMain("app")
	w.writeBatch(batch, map[string]string{"app": pin, "lib": w.originMain("lib")}, []batchItem{
		{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {src}}},
	})
	branch := "integrate/repair-wi_batch"
	fixHead := w98RepairBranch(w, "app", branch, pin)
	plan := filepath.Join(w.capsule, "artifacts", "integrate", "repair-plan.json")
	w98RepairPlan(t, plan, map[string]any{"name": "app", "branch": branch, "base": pin, "head": fixHead,
		"units": []string{"TestStale"}, "items": []string{"wi_src"}})
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, res, d, errb := runRevert(w, out, "--repair", plan)
	if exit != 0 || res.Verdict != VerdictPass {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	if len(d.Fixes) != 1 {
		t.Fatalf("the details carry the landed fix: %+v", d.Fixes)
	}
	f := d.Fixes[0]
	if f.Name != "app" || f.Branch != branch || f.PR == "" || f.Merge == "" || strings.Join(f.Units, ",") != "TestStale" || strings.Join(f.Items, ",") != "wi_src" || f.Why != "" {
		t.Fatalf("the landed fix: %+v", f)
	}
	if f.Merge != w.originMain("app") || res.Heads["app"] != f.Merge || d.Heads["app"] != f.Merge {
		t.Fatalf("the pin follows the fix's merge onto the default branch: %+v %v %v", f, d.Heads, res.Heads)
	}
	if w.head("app") != f.Merge {
		t.Fatalf("the clone stands at the fix's merge: %s", w.head("app"))
	}
	if fileIn(t, w.dir("app"), "tests/stale_test.go") == "" || fileIn(t, w.dir("app"), "src.txt") != "s\n" {
		t.Fatal("the default branch carries the repair beside the landed change")
	}
	if w.prCount("app") != 2 || w.prFile("app", 2, "state") != "MERGED" || w.prFile("app", 2, "head") != branch {
		t.Fatalf("one fix PR per repo, merged: %d %s %s", w.prCount("app"), w.prFile("app", 2, "state"), w.prFile("app", 2, "head"))
	}
	if got := readOps(t, out); got != "[]" {
		t.Fatalf("nothing reverted: no move: %q", got)
	}
	// a crash between the merge and its record: the plan record loses the
	// fix's landing — the next run reads the merged PR back, opens none
	raw, err := os.ReadFile(d.Plan)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.Replace(string(raw), f.Merge, "", 1)
	if trimmed == string(raw) {
		t.Fatalf("the plan record carries the fix's merge:\n%s", raw)
	}
	if err := os.WriteFile(d.Plan, []byte(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d, errb = runRevert(w, out, "--repair", plan)
	if exit != 0 || len(d.Fixes) != 1 || d.Fixes[0].Merge != f.Merge || d.Fixes[0].PR != f.PR || w.prCount("app") != 2 || res.Heads["app"] != f.Merge {
		t.Fatalf("the re-run reads the merged fix PR back: exit %d, %+v\n%s", exit, d.Fixes, errb)
	}
	// a fix whose blamed item the same run reverts is not landed: no PR
	w2 := newGitWorld(t, "app")
	src2 := w2.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch2 := filepath.Join(w2.capsule, "artifacts", "integrate", "batch.json")
	pin2 := w2.originMain("app")
	w2.writeBatch(batch2, map[string]string{"app": pin2}, []batchItem{
		{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {src2}}},
	})
	fixHead2 := w98RepairBranch(w2, "app", branch, pin2)
	plan2 := filepath.Join(w2.capsule, "artifacts", "integrate", "repair-plan.json")
	w98RepairPlan(t, plan2, map[string]any{"name": "app", "branch": branch, "base": pin2, "head": fixHead2,
		"units": []string{"TestStale"}, "items": []string{"wi_src"}})
	out2 := filepath.Join(w2.capsule, "artifacts", "integrate", "revert.json")
	exit, res, d, errb = runRevert(w2, out2, "--repair", plan2, "--item", "p#1")
	if exit != 0 || len(d.Fixes) != 1 || d.Fixes[0].PR != "" || d.Fixes[0].Merge != "" || d.Fixes[0].Why == "" ||
		len(d.Reverts) != 1 || !d.Reverts[0].Done || res.Heads["app"] != d.Reverts[0].repo("app").RevertMerge {
		t.Fatalf("the reverted item's fix is not landed, the pin follows the revert: exit %d, %+v %+v\n%s", exit, d.Fixes, d.Reverts, errb)
	}
	if w2.prCount("app") != 2 || w2.prFile("app", 2, "head") != "integrate/revert-p-1" {
		t.Fatalf("only the revert's PR: %d %s", w2.prCount("app"), w2.prFile("app", 1, "head"))
	}
	// a refused merge keeps the PR for the next run (exit 1, the why)
	w3 := newGitWorld(t, "app")
	src3 := w3.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch3 := filepath.Join(w3.capsule, "artifacts", "integrate", "batch.json")
	pin3 := w3.originMain("app")
	w3.writeBatch(batch3, map[string]string{"app": pin3}, []batchItem{
		{ID: "wi_src", Slug: "p#1", MergeCommits: map[string][]string{"app": {src3}}},
	})
	fixHead3 := w98RepairBranch(w3, "app", branch, pin3)
	plan3 := filepath.Join(w3.capsule, "artifacts", "integrate", "repair-plan.json")
	w98RepairPlan(t, plan3, map[string]any{"name": "app", "branch": branch, "base": pin3, "head": fixHead3,
		"units": []string{"TestStale"}, "items": []string{"wi_src"}})
	out3 := filepath.Join(w3.capsule, "artifacts", "integrate", "revert.json")
	if err := os.WriteFile(filepath.Join(w3.ghState, "fail-created-merge"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exit, res, d, errb = runRevert(w3, out3, "--repair", plan3)
	if exit != 1 || res.Verdict != VerdictFail || !strings.Contains(res.Summary, "repos/app") || d.Fixes[0].Why == "" || w3.prFile("app", 2, "state") != "OPEN" {
		t.Fatalf("the refused fix merge: exit %d, %+v, %+v\n%s", exit, res, d.Fixes, errb)
	}
	// the fake gh consumes the marker: the PR's merge fails once, the next
	// run merges the same PR
	exit, res, d, errb = runRevert(w3, out3, "--repair", plan3)
	if exit != 0 || d.Fixes[0].Merge == "" || d.Fixes[0].Why != "" || w3.prCount("app") != 2 || res.Heads["app"] != w3.originMain("app") {
		t.Fatalf("the next run merges the same fix PR: exit %d, %+v\n%s", exit, d.Fixes, errb)
	}
	// the repair file of a step that never ran is absent (no fix, the pins
	// copied); a malformed one is refused
	w4 := newGitWorld(t, "app")
	batch4 := filepath.Join(w4.capsule, "artifacts", "integrate", "batch.json")
	pin4 := w4.originMain("app")
	w4.writeBatch(batch4, map[string]string{"app": pin4}, nil)
	out4 := filepath.Join(w4.capsule, "artifacts", "integrate", "revert.json")
	plan4 := filepath.Join(w4.capsule, "artifacts", "integrate", "repair-plan.json")
	exit, _, d, errb = runRevert(w4, out4, "--repair", plan4)
	if exit != 0 || len(d.Fixes) != 0 || d.Heads["app"] != pin4 {
		t.Fatalf("a missing repair plan is a step that never ran: exit %d, %+v\n%s", exit, d, errb)
	}
	if err := os.WriteFile(plan4, []byte("{\"round\": 1, \"repos\": [{\"name\": \"app\"}]}"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, errb = invokePy(t, scriptRevert, []string{"--capsule", w4.capsule, "--batch", batch4, "--repair", plan4}, w4.env("PF_WI_ID=wi_batch"))
	if exit != 2 || !strings.Contains(errb, "repair plan") {
		t.Fatalf("a malformed repair plan is refused: exit %d, stderr %q", exit, errb)
	}
}
