package stepscripts

// w404_rules_test.go: the integration lane's library and command rules the
// v1 driver's integrate tests (driver/w404_integrate_test.go, deleted with
// the daemon's integrate actions — workflow contract v2 change 17) held,
// ported onto the v2 commands that carry them: integrate.py collect and
// close, gate.py --triage and revert.py, on the batch fixture
// (integrate_test.go) and the git world (gitworld_test.go). Each test names
// the v1 test it ports.

import (
	"path/filepath"
	"strings"
	"testing"
)

// ruleCheck is the fixture's gate (batchCheck) with TestNew in place of
// TestFlaky: TestNew runs only where its test file exists, and is red
// while the tree carries `breaks-new`.
const ruleCheck = `#!/bin/sh
unit=${1:-}
red=0
if [ -z "$unit" ] || [ "$unit" = TestBroken ]; then
  if [ -e broken ]; then
    echo "--- FAIL: TestBroken (0.00s)"; echo "    broken_test.go:3: the tree carries broken"; red=1
  else
    echo "--- PASS: TestBroken (0.00s)"
  fi
fi
if [ -e pkg/new_test.go ] && { [ -z "$unit" ] || [ "$unit" = TestNew ]; }; then
  if [ -e breaks-new ]; then
    echo "--- FAIL: TestNew (0.00s)"; echo "    new_test.go:3: the tree carries breaks-new"; red=1
  else
    echo "--- PASS: TestNew (0.00s)"
  fi
fi
if [ "$red" = 1 ]; then
  echo "FAIL"; echo "FAIL	example.invalid/demo/pkg	0.01s"; exit 1
fi
echo "PASS"; echo "ok  	example.invalid/demo/pkg	0.01s"
`

// reseed commits files onto origin's main as a new base and tags it green
// (green/20261101-2, newer than the fixture's): the baseline every triage
// then reads.
func (f *batchFixture) reseed(files map[string]string) string {
	t := f.t
	t.Helper()
	for rel, body := range files {
		writeFile(t, f.seed, rel, body)
	}
	c := commitAll(t, f.seed, "reseed")
	gitT(t, f.seed, "tag", "-a", "-m", "{}", "green/20261101-2")
	gitT(t, f.seed, "push", "-q", "origin", "main", "refs/tags/green/20261101-2")
	return c
}

// mainCommit is a commit pushed straight onto origin's main — no landed
// item's merge (a human's): it answers the commit.
func (f *batchFixture) mainCommit(msg string, files map[string]string) string {
	t := f.t
	t.Helper()
	for rel, body := range files {
		writeFile(t, f.seed, rel, body)
	}
	c := commitAll(t, f.seed, msg)
	gitT(t, f.seed, "push", "-q", "origin", "main")
	return c
}

// landRemoving lands an item whose change deletes paths.
func (f *batchFixture) landRemoving(id, slug string, paths ...string) string {
	t := f.t
	t.Helper()
	gitT(t, f.seed, "checkout", "-q", "-b", "feat-"+id)
	gitT(t, f.seed, append([]string{"rm", "-q"}, paths...)...)
	gitT(t, f.seed, "commit", "-q", "-m", id)
	gitT(t, f.seed, "checkout", "-q", "main")
	gitT(t, f.seed, "merge", "-q", "--no-ff", "-m", "Merge "+id, "feat-"+id)
	merge := gitT(t, f.seed, "rev-parse", "HEAD")
	gitT(t, f.seed, "push", "-q", "origin", "main")
	f.items = append(f.items, poolItem{ID: id, Slug: slug, Landed: "2026-11-10T00:00:00Z", Integration: map[string]any{
		"state": "landed", "merge_commits": map[string]any{"app": []any{merge}},
	}})
	return merge
}

// addRepo adds a second task repo to the batch capsule: its own origin
// whose base commit is tagged green, cloned into repos/<name>; it answers
// the clone that lands into that origin.
func (f *batchFixture) addRepo(name string) string {
	t := f.t
	t.Helper()
	root := filepath.Dir(f.dir)
	origin, seed := filepath.Join(root, name+".git"), filepath.Join(root, name+"-seed")
	gitT(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitT(t, root, "clone", "-q", origin, seed)
	gitT(t, seed, "checkout", "-q", "-B", "main")
	writeFile(t, seed, "README", name+"\n")
	commitAll(t, seed, "base")
	gitT(t, seed, "tag", "-a", "-m", "{}", "green/20261101-1")
	gitT(t, seed, "push", "-q", "origin", "main", "refs/tags/green/20261101-1")
	gitT(t, root, "clone", "-q", origin, filepath.Join(f.dir, "repos", name))
	return seed
}

// landIn lands an item into repo through its clone seed (addRepo).
func (f *batchFixture) landIn(seed, repo, id, slug string, files map[string]string) string {
	t := f.t
	t.Helper()
	gitT(t, seed, "checkout", "-q", "-b", "feat-"+id)
	for rel, body := range files {
		writeFile(t, seed, rel, body)
	}
	commitAll(t, seed, id)
	gitT(t, seed, "checkout", "-q", "main")
	gitT(t, seed, "merge", "-q", "--no-ff", "-m", "Merge "+id, "feat-"+id)
	merge := gitT(t, seed, "rev-parse", "HEAD")
	gitT(t, seed, "push", "-q", "origin", "main")
	f.items = append(f.items, poolItem{ID: id, Slug: slug, Landed: "2026-11-10T00:00:00Z", Integration: map[string]any{
		"state": "landed", "merge_commits": map[string]any{repo: []any{merge}},
	}})
	return merge
}

// redTriage lands nothing more: it collects the pool, runs the gate (red)
// and the triage, and answers the triage's one unit.
func (f *batchFixture) redTriage(pool string) triageUnitDetail {
	t := f.t
	t.Helper()
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	if exit, res, errb := f.gate(); exit != 1 || res == nil {
		t.Fatalf("the gate is red: exit %d\n%s", exit, errb)
	}
	exit, res, errb := f.triage(pool)
	if exit != 1 || res == nil {
		t.Fatalf("the triage: exit %d\n%s", exit, errb)
	}
	var td triageDetails
	detailsOf(t, res, &td)
	if len(td.Units) != 1 {
		t.Fatalf("one failing unit: %+v", td.Units)
	}
	return td.Units[0]
}

// TestW404PreExistingRedBlamesNoItem (v1 TestW404PreExistingRedAsksTheOwner):
// a unit red at the newest green tag too, with the same signature, is
// pre-existing — it blames no item.
func TestW404PreExistingRedBlamesNoItem(t *testing.T) {
	f := newBatchFixture(t)
	red := f.land("wi_r", "p#1", map[string]string{"broken": "x\n"})
	f.tagGreen(red, "green/20261102-1") // an earlier batch's tag over the red tree
	f.land("wi_a", "p#2", map[string]string{"pkg/a.go": "package pkg\n"})
	u := f.redTriage(f.pool(nil))
	if u.Unit != "TestBroken" || u.Class != "pre_existing" || u.Item != "" || !strings.Contains(u.Why, "green/20261102-1") {
		t.Fatalf("pre-existing at the green tag, no item blamed: %+v", u)
	}
}

// TestW404AnAddedTestBrokenByAnotherItemIsBlamed (v1
// TestW404AddedTestBrokenByAnotherItemIsBlamed): a unit the baseline does
// not have is the batch's, never pre-existing, and the bisection takes
// "absent" as "not failing": the item that broke it is blamed, not the
// one that added it.
func TestW404AnAddedTestBrokenByAnotherItemIsBlamed(t *testing.T) {
	f := newBatchFixture(t)
	f.reseed(map[string]string{"tests/check.sh": ruleCheck})
	f.land("wi_t", "p#1", map[string]string{"pkg/new_test.go": "package pkg\n\nfunc TestNew(t *testing.T) {}\n"})
	mX := f.land("wi_x", "p#2", map[string]string{"breaks-new": "x\n", "pkg/x.go": "package pkg\n"})
	u := f.redTriage(f.pool(nil))
	if u.Unit != "TestNew" || u.Class != "attributed" || u.Item != "wi_x" || !strings.Contains(u.Why, "bisection: first red at the merge "+mX[:12]) {
		t.Fatalf("TestNew is the batch's, bisected to wi_x: %+v", u)
	}
	if !strings.Contains(u.Base, "does not exist") {
		t.Fatalf("the baseline's verdict names the absent test: %q", u.Base)
	}
}

// TestW404ARedFromOutsideTheBatchBlamesNoItem (v1
// TestW404BisectFindsACommitOutsideTheBatch): a commit pushed straight to
// main between two items' merges breaks the gate. The bisection walks
// every first-parent commit since the baseline, not only the items'
// merges: the first red commit is no landed item's merge, so the red is
// outside the batch and no item is blamed.
func TestW404ARedFromOutsideTheBatchBlamesNoItem(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	human := f.mainCommit("a human commit breaking pkg", map[string]string{"broken": "x\n"})
	f.land("wi_b", "p#2", map[string]string{"pkg/b.go": "package pkg\n"})
	u := f.redTriage(f.pool(nil))
	if u.Class != "unattributed" || u.Item != "" || u.Outside != human || !strings.Contains(u.Why, "outside the batch") {
		t.Fatalf("the first red commit is the human one, outside the batch: %+v (human %s)", u, human)
	}
}

// TestW404ASingleCandidateIsCheckedAgainstHistory (v1
// TestW404SingleCandidateIsCheckedAgainstHistory): the batch's only change
// is not blamed on its area alone. A commit pushed straight to main broke
// the test before the item landed; the unit is red at the item's merge and
// at its first parent too, so the bisection runs and finds that commit:
// outside the batch, no item blamed.
func TestW404ASingleCandidateIsCheckedAgainstHistory(t *testing.T) {
	f := newBatchFixture(t)
	human := f.mainCommit("a human commit breaking pkg", map[string]string{"broken": "x\n"})
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	u := f.redTriage(f.pool(nil))
	if u.Item != "" || u.Outside != human || !strings.Contains(u.Why, "its only candidate by area") {
		t.Fatalf("not blamed on the single candidate: bisected to the human commit: %+v (human %s)", u, human)
	}
}

// TestW404AHeldSourceStopsTheTagNotTheMerges (v1
// TestW404HoldStopsTheTagNotTheMerges): a held source — blamed by a red
// batch, its fix ticket still in develop — stops nothing from landing: the
// next batch takes it with an item that landed meanwhile and gates the
// default branch as it is, still red, so nothing moves; once the fix
// lands a later batch goes green and moves them all to merged.
func TestW404AHeldSourceStopsTheTagNotTheMerges(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_s", "p#1", map[string]string{"broken": "x\n"})
	f.land("wi_n", "p#2", map[string]string{"n.txt": "n\n"})
	held := func(s *poolSnapshot) {
		for i := range s.Items {
			if s.Items[i].ID == "wi_s" {
				s.Items[i].Integration["red_rounds"] = 1.0
				s.Items[i].Integration["fix_tickets"] = []any{"p#3"}
			}
		}
		s.Tickets = map[string]poolTicket{"p#3": {Status: "running"}}
	}
	pool := f.pool(held)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, readResult(t, f.art("batch.json")), &d)
	if len(d.Items) != 2 || d.Items[0].ID != "wi_n" || d.Items[1].ID != "wi_s" || len(d.Holds) != 1 || d.Holds[0].Stuck {
		t.Fatalf("the held source is the batch's beside the new item, its hold open: items %+v holds %+v", d.Items, d.Holds)
	}
	red := f.originHead()
	if exit, _, errb := f.gate(); exit != 1 {
		t.Fatalf("the default branch is still red: %d\n%s", exit, errb)
	}
	exit, res, errb := f.close(pool, true)
	var cd closeDetails
	if exit != 0 || res == nil {
		t.Fatalf("close: exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &cd)
	if cd.IntegrationBatch["result"] != "red" || len(cd.Moves) != 0 {
		t.Fatalf("red: nothing moves: %v %+v", cd.IntegrationBatch, cd.Moves)
	}
	// the fix lands; the next batch is green and moves every landed item
	f.landRemoving("wi_fix", "p#3", "broken")
	pool = f.pool(func(s *poolSnapshot) {
		held(s)
		s.RedHeads["app"] = red
	})
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	if exit, _, errb := f.gate("--mark-green", "--summary", "batch wi_batch"); exit != 0 {
		t.Fatalf("the fix turns the gate green: %d\n%s", exit, errb)
	}
	if exit, res, errb = f.close(pool, true); exit != 0 || res == nil {
		t.Fatalf("close: exit %d\n%s", exit, errb)
	}
	detailsOf(t, res, &cd)
	var moved []string
	for _, m := range cd.Moves {
		moved = append(moved, m.WI)
	}
	if cd.IntegrationBatch["result"] != "green" || strings.Join(moved, ",") != "wi_s,wi_n,wi_fix" {
		t.Fatalf("green: the held source, the item and the fix move to merged: %v %v", cd.IntegrationBatch, moved)
	}
}

// writeGateRepos writes a gate.py result by hand: each repo judged at its
// head, passed or not, tagged or not.
func (f *batchFixture) writeGateRepos(repos map[string]struct {
	head, tag string
	passed    bool
}) {
	rs := map[string]any{}
	for name, r := range repos {
		rs[name] = map[string]any{"head": r.head, "tag": r.tag, "result": map[string]any{"commit": r.head, "passed": r.passed, "runner": "gate.py", "runner_version": "2"}}
	}
	res := &Result{Schema: Schema, Command: NameGate, Details: map[string]any{"mode": "capsule", "gated": sortedStrings(repos), "repos": rs}}
	if err := WriteResult(f.art("gate.json"), res); err != nil {
		f.t.Fatal(err)
	}
}

// TestW404APassedRepoOfARedBatchIsGatedAgain (v1
// TestW404PassedRepoOfARedBatchIsGatedAgain): a batch gates two repos; app
// fails, lib passes, and the gate tags nothing (it tags only when every
// gated repo passed). The batch closes red, moves nothing, and only app's
// head is a red head: the next batch gates lib again, though it has not
// moved.
func TestW404APassedRepoOfARedBatchIsGatedAgain(t *testing.T) {
	f := newBatchFixture(t)
	lib := f.addRepo("lib")
	f.land("wi_r", "p#1", map[string]string{"broken": "x\n"})
	f.landIn(lib, "lib", "wi_y", "p#2", map[string]string{"y.txt": "y\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, readResult(t, f.art("batch.json")), &d)
	if strings.Join(d.Gated, ",") != "app,lib" {
		t.Fatalf("both repos gated: %v", d.Gated)
	}
	app, libHead := d.Heads["app"], d.Heads["lib"]
	f.writeGateRepos(map[string]struct {
		head, tag string
		passed    bool
	}{"app": {head: app}, "lib": {head: libHead, passed: true}})
	exit, res, errb := f.close(pool, true)
	if exit != 0 || res == nil {
		t.Fatalf("a red batch with a passed repo closes red: exit %d %+v\n%s", exit, res, errb)
	}
	var cd closeDetails
	detailsOf(t, res, &cd)
	ib := cd.IntegrationBatch
	if ib["result"] != "red" || ib["tags"] != nil || len(cd.Moves) != 0 {
		t.Fatalf("red, nothing tagged, nothing moves: %v %+v", ib, cd.Moves)
	}
	if main, _ := ib["main"].(map[string]any); len(main) != 1 || main["app"] != app {
		t.Fatalf("only the head the gate failed on is a red head, not lib's that passed: %v", ib["main"])
	}
	// the next batch: lib — unmoved, no red head, its item under no green
	// tag — is gated again
	if exit, _, errb := f.collect(f.pool(func(s *poolSnapshot) { s.RedHeads["app"] = app })); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	detailsOf(t, readResult(t, f.art("batch.json")), &d)
	if strings.Join(d.Gated, ",") != "lib" || len(d.Items) != 1 || d.Items[0].ID != "wi_y" {
		t.Fatalf("lib is gated again with its item: gated %v items %+v", d.Gated, d.Items)
	}
}

// TestW404AnAllPassBatchMissingATagIsRefused (PR 17 review carry-over): a
// batch whose every judged repo passed closes green only on every repo's
// green tag — two repos, one tagged or none, is refused (exit 2: the gate's
// --mark-green did not land), never closed green or red.
func TestW404AnAllPassBatchMissingATagIsRefused(t *testing.T) {
	f := newBatchFixture(t)
	lib := f.addRepo("lib")
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	f.landIn(lib, "lib", "wi_y", "p#2", map[string]string{"y.txt": "y\n"})
	pool := f.pool(nil)
	if exit, _, errb := f.collect(pool); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	var d collectDetails
	detailsOf(t, readResult(t, f.art("batch.json")), &d)
	app, libHead := d.Heads["app"], d.Heads["lib"]
	for name, tags := range map[string][2]string{
		"one tag":  {"green/20261111-1", ""},
		"no tags":  {"", ""},
		"lib only": {"", "green/20261111-1"},
	} {
		f.writeGateRepos(map[string]struct {
			head, tag string
			passed    bool
		}{"app": {head: app, tag: tags[0], passed: true}, "lib": {head: libHead, tag: tags[1], passed: true}})
		exit, res, errb := f.close(pool, true)
		if exit != 2 || res == nil || res.Verdict != VerdictError || !strings.Contains(res.Summary, "carries no green tag") {
			t.Fatalf("%s: an all-pass batch missing a tag: exit %d %+v\n%s", name, exit, res, errb)
		}
	}
}

// TestW404TheGreenTagIsTheLanesOwn (v1 TestW404LandedBatchGoesGreen): the
// green tag gate.py writes on the pinned head carries the lane's own
// identity as its tagger, never the host's.
func TestW404TheGreenTagIsTheLanesOwn(t *testing.T) {
	f := newBatchFixture(t)
	f.land("wi_a", "p#1", map[string]string{"pkg/a.go": "package pkg\n"})
	if exit, _, errb := f.collect(f.pool(nil)); exit != 0 {
		t.Fatalf("collect: %d\n%s", exit, errb)
	}
	exit, res, errb := f.gate("--mark-green", "--summary", "batch wi_batch")
	if exit != 0 || res == nil {
		t.Fatalf("gate.py: exit %d\n%s", exit, errb)
	}
	var gd gateDetails
	detailsOf(t, res, &gd)
	tag := gd.Tags["app"]
	if tag == "" {
		t.Fatalf("no green tag: %+v", gd)
	}
	gitT(t, f.seed, "fetch", "-q", "origin", "refs/tags/"+tag+":refs/tags/"+tag)
	if tagger := gitT(t, f.seed, "for-each-ref", "--format=%(taggername) %(taggeremail)", "refs/tags/"+tag); tagger != laneIdentity {
		t.Fatalf("the green tag's tagger is the lane: %q, want %q", tagger, laneIdentity)
	}
}

// TestW404ARevertRecordedDoneIsLeftAlone (v1
// TestW404DoneRevertPlanIsLeftAlone): a revert the plan record says is done
// is not carried out again — no PR opened, the default branch unmoved —
// even when its record names no revert merge.
func TestW404ARevertRecordedDoneIsLeftAlone(t *testing.T) {
	w := newGitWorld(t, "app")
	src := w.land("app", "wi/src", map[string]string{"src.txt": "s\n"}, "")
	batch := filepath.Join(w.capsule, "artifacts", "integrate", "batch.json")
	w.writeBatch(batch, map[string]string{"app": w.originMain("app")}, []batchItem{{ID: "wi_s", Slug: "p#1", MergeCommits: map[string][]string{"app": {src}}}})
	plan := `{"reverts":{"wi_s":{"wi":"wi_s","name":"p#1","group":["wi_s"],"repos":[{"name":"app","merge_commits":["` + src + `"]}],"done":true}}}`
	writeFile(t, w.capsule, "artifacts/integrate/"+revertPlanName, plan)
	main, prs := w.originMain("app"), w.prCount("app")
	out := filepath.Join(w.capsule, "artifacts", "integrate", "revert.json")
	exit, _, d, errb := runRevert(w, out, "--item", "wi_s")
	if exit != 0 || len(d.Reverts) != 1 || !d.Reverts[0].Done {
		t.Fatalf("exit %d, %+v\n%s", exit, d.Reverts, errb)
	}
	if w.prCount("app") != prs || w.originMain("app") != main {
		t.Fatalf("nothing carried out again: PRs %d (was %d), main %s (was %s)", w.prCount("app"), prs, w.originMain("app"), main)
	}
}
