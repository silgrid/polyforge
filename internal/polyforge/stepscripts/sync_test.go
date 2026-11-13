package stepscripts

// sync_test.go: sync.py (workflows/_shared/scripts) on fixture repos — a clean merge and a
// fast-forward, the integrate.conf conflicts resolved mechanically, a
// real conflict left in progress (exit 1, the files listed) and finished
// by `--continue` (its refusals, then the same result file rewritten), a
// crash mid-merge re-entered (the leftover merge aborted, a commit the
// run never made reset off the branch, the merge made again), and the
// refused invocations.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncOut answers the lane's result path in the world's capsule.
func syncOut(w *gitWorld) string {
	return filepath.Join(w.capsule, "artifacts", "sync", "result.json")
}

// runSync runs sync.py in the world and answers the exit, the result and
// its details.
func runSync(w *gitWorld, extra ...string) (int, *Result, *syncRecord, string) {
	t := w.t
	t.Helper()
	args := append([]string{"--capsule", w.capsule, "--json", "--out", syncOut(w)}, extra...)
	exit, stdout, errb := invokePy(t, scriptSync, args, w.env("PF_ROUND=round-s"))
	var res *Result
	var rec syncRecord
	if _, err := os.Stat(syncOut(w)); err == nil {
		res = readResult(t, syncOut(w))
		detailsOf(t, res, &rec)
		var fromStdout Result
		if err := json.Unmarshal([]byte(stdout), &fromStdout); err != nil || fromStdout.ExitCode != res.ExitCode {
			t.Fatalf("stdout is not the result: %v\n%s", err, stdout)
		}
	}
	return exit, res, &rec, errb
}

func syncRepoOf(t *testing.T, rec *syncRecord, name string) *syncRepo {
	t.Helper()
	r := rec.repo(name)
	if r == nil {
		t.Fatalf("no repos entry for %s: %+v", name, rec.Repos)
	}
	return r
}

func TestSyncMergesCleanlyAndFastForwards(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	before := w.change("app", map[string]string{"feature.txt": "f\n"}, "")
	appMain := w.mainMoves("app", map[string]string{"other.txt": "o\n"}, "")
	libMain := w.mainMoves("lib", map[string]string{"lib.txt": "l\n"}, "")
	libBefore := w.head("lib")
	exit, res, rec, errb := runSync(w)
	if exit != 0 || res.Verdict != VerdictPass || res.Round != "round-s" {
		t.Fatalf("exit %d, result %+v\n%s", exit, res, errb)
	}
	app := syncRepoOf(t, rec, "app")
	if app.Before != before || app.DefaultHead != appMain || app.DefaultBranch != "main" || app.Branch != w.branch || app.FastForwarded {
		t.Fatalf("app: %+v", app)
	}
	if ps := parents(t, w.dir("app"), app.After); len(ps) != 2 || ps[0] != before || ps[1] != appMain || w.head("app") != app.After {
		t.Fatalf("app's after is the merge of %s and %s: %s %v", short(before), short(appMain), short(app.After), ps)
	}
	if len(app.Mechanical) != 0 || len(app.Conflicts) != 0 || len(app.Resolved) != 0 || len(app.Incoming) != 1 || !strings.Contains(app.Incoming[0], "main: moves") {
		t.Fatalf("app's lists: %+v", app)
	}
	lib := syncRepoOf(t, rec, "lib")
	if lib.Before != libBefore || lib.After != libMain || !lib.FastForwarded || w.head("lib") != libMain {
		t.Fatalf("lib is fast-forwarded (no commit of its own): %+v head %s", lib, w.head("lib"))
	}
	if res.Heads["app"] != app.After || res.Heads["lib"] != libMain || len(res.Heads) != 2 {
		t.Fatalf("heads: %v", res.Heads)
	}
	if _, err := os.Stat(rec.Record); !os.IsNotExist(err) {
		t.Fatalf("the record %s is removed once the run settles: %v", rec.Record, err)
	}
	if rec.Record != filepath.Join(w.capsule, "artifacts", "sync", syncRecordName) {
		t.Fatalf("the record lives beside the result: %s", rec.Record)
	}
	// the clone's info/attributes is left as found (none)
	if _, err := os.Stat(filepath.Join(w.dir("app"), ".git", "info", "attributes")); !os.IsNotExist(err) {
		t.Fatalf("the merge attribute override must not outlive the merge: %v", err)
	}
	// idempotent: a second run has nothing to merge
	exit, _, rec2, errb := runSync(w)
	if exit != 0 {
		t.Fatalf("second run: exit %d\n%s", exit, errb)
	}
	if a := syncRepoOf(t, rec2, "app"); a.Before != app.After || a.After != app.After || a.FastForwarded {
		t.Fatalf("second run: app already contains main: %+v", a)
	}
	// the agent's own view: the summary without --json
	exit, stdout, _ := invokePy(t, scriptSync, []string{"--capsule", w.capsule}, w.env())
	if exit != 0 || !strings.Contains(stdout, "already contains") {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
}

func TestSyncResolvesMechanicalConflicts(t *testing.T) {
	w := newGitWorld(t, "app")
	w.change("app", map[string]string{"feature.txt": "f\n"}, "branch-section")
	w.mainMoves("app", nil, "main-section")
	exit, res, rec, errb := runSync(w)
	if exit != 0 {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	app := syncRepoOf(t, rec, "app")
	if strings.Join(app.Mechanical, ",") != "docs/upgrade.md,pin.txt" || len(app.Conflicts) != 0 || len(app.Resolved) != 0 || app.After == "" {
		t.Fatalf("the integrate.conf conflicts are resolved mechanically: %+v", app)
	}
	up := fileIn(t, w.dir("app"), "docs/upgrade.md")
	if up != "# upgrade\n\n## main-section\n\n## branch-section\n" || fileIn(t, w.dir("app"), "pin.txt") != pinOf(up)+"\n" {
		t.Fatalf("the log is main's then the branch's section, the pin recomputed:\n%q\n%s", up, fileIn(t, w.dir("app"), "pin.txt"))
	}
	if st, _ := gitT(t, w.dir("app"), "status", "--porcelain"), w.head("app"); st != "" || w.head("app") != app.After {
		t.Fatalf("the merge is committed, the tree clean: %q", st)
	}
}

func TestSyncRealConflictThenContinue(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	before := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "branch-section")
	main := w.mainMoves("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "main-section")
	libMain := w.mainMoves("lib", map[string]string{"lib.txt": "l\n"}, "")
	exit, res, rec, errb := runSync(w)
	if exit != 1 || res.Verdict != VerdictFail || !strings.Contains(res.Summary, "sync.py --continue") || !strings.Contains(res.Summary, "repos/app: pkg/code.go") {
		t.Fatalf("exit %d, result %+v\n%s", exit, res, errb)
	}
	app := syncRepoOf(t, rec, "app")
	if strings.Join(app.Conflicts, ",") != "pkg/code.go" || strings.Join(app.Mechanical, ",") != "docs/upgrade.md,pin.txt" || app.After != "" || app.Before != before {
		t.Fatalf("app: %+v", app)
	}
	if mh := gitT(t, w.dir("app"), "rev-parse", "MERGE_HEAD"); mh != main || w.head("app") != before || res.Heads["app"] != before {
		t.Fatalf("the merge is left in progress at %s (MERGE_HEAD %s, HEAD %s)", short(before), short(mh), short(w.head("app")))
	}
	if lib := syncRepoOf(t, rec, "lib"); lib.After != libMain || res.Heads["lib"] != libMain {
		t.Fatalf("lib synced regardless: %+v", lib)
	}
	if _, err := os.Stat(rec.Record); err != nil {
		t.Fatalf("the record stays while a conflict is pending: %v", err)
	}
	// --continue before anything is resolved: refused, the file still listed
	exit, res, rec, _ = runSync(w, "--continue")
	if exit != 1 || strings.Join(syncRepoOf(t, rec, "app").Conflicts, ",") != "pkg/code.go" || !strings.Contains(res.Summary, "unmerged paths remain") {
		t.Fatalf("exit %d, %+v", exit, res)
	}
	// a resolution with a marker left, staged
	writeFile(t, w.dir("app"), "pkg/code.go", "package pkg\n\n<<<<<<< HEAD\nfunc F() int { return 2 }\n=======\nfunc F() int { return 3 }\n>>>>>>> main\n")
	gitT(t, w.dir("app"), "add", "pkg/code.go")
	exit, res, rec, _ = runSync(w, "--continue")
	if exit != 1 || !strings.Contains(res.Summary, "conflict marker") || strings.Join(syncRepoOf(t, rec, "app").Conflicts, ",") != "pkg/code.go" {
		t.Fatalf("a residual marker is refused: exit %d, %+v", exit, res)
	}
	// resolved, but a mechanically resolved file changed
	writeFile(t, w.dir("app"), "pkg/code.go", "package pkg\n\nfunc F() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "pkg/code.go")
	pin := fileIn(t, w.dir("app"), "pin.txt")
	writeFile(t, w.dir("app"), "pin.txt", "0000\n")
	gitT(t, w.dir("app"), "add", "pin.txt")
	exit, res, _, _ = runSync(w, "--continue")
	if exit != 1 || !strings.Contains(res.Summary, "pin.txt was resolved mechanically") {
		t.Fatalf("a changed mechanical resolution is refused: exit %d, %+v", exit, res)
	}
	writeFile(t, w.dir("app"), "pin.txt", pin)
	gitT(t, w.dir("app"), "add", "pin.txt")
	// unstaged: refused too
	writeFile(t, w.dir("app"), "feature.txt", "unstaged\n")
	gitT(t, w.dir("app"), "add", "feature.txt")
	writeFile(t, w.dir("app"), "feature.txt", "unstaged2\n")
	exit, res, _, _ = runSync(w, "--continue")
	if exit != 1 || !strings.Contains(res.Summary, "not staged") {
		t.Fatalf("an unstaged change is refused: exit %d, %+v", exit, res)
	}
	gitT(t, w.dir("app"), "add", "feature.txt")
	// the resolution is committed; the SAME result file is rewritten
	exit, res, rec, errb = runSync(w, "--continue")
	if exit != 0 || res.Verdict != VerdictPass || res.Round != "round-s" {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	app = syncRepoOf(t, rec, "app")
	if len(app.Conflicts) != 0 || strings.Join(app.Resolved, ",") != "pkg/code.go" || strings.Join(app.Mechanical, ",") != "docs/upgrade.md,pin.txt" || app.Before != before || app.DefaultHead != main {
		t.Fatalf("the rewritten result: %+v", app)
	}
	if ps := parents(t, w.dir("app"), app.After); len(ps) != 2 || ps[0] != before || ps[1] != main || w.head("app") != app.After || res.Heads["app"] != app.After {
		t.Fatalf("after is the merge commit: %s %v", short(app.After), ps)
	}
	if lib := syncRepoOf(t, rec, "lib"); lib.After != libMain || res.Heads["lib"] != libMain {
		t.Fatalf("the rewritten result still carries lib: %+v %v", lib, res.Heads)
	}
	if got := fileIn(t, w.dir("app"), "pkg/code.go"); got != "package pkg\n\nfunc F() int { return 5 }\n" || gitT(t, w.dir("app"), "status", "--porcelain") != "" {
		t.Fatalf("the agent's resolution is what was committed: %q", got)
	}
	if _, err := os.Stat(rec.Record); !os.IsNotExist(err) {
		t.Fatal("the record is removed once the run settles")
	}
	// nothing left to continue
	exit, _, errb = invokePy(t, scriptSync, []string{"--capsule", w.capsule, "--continue", "--out", syncOut(w)}, w.env())
	if exit != 2 || !strings.Contains(errb, "no sync in progress") {
		t.Fatalf("exit %d, stderr %q", exit, errb)
	}
}

func TestSyncReentryResetsAndMergesAgain(t *testing.T) {
	w := newGitWorld(t, "app")
	before := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "")
	main := w.mainMoves("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "")
	if exit, _, _, errb := runSync(w); exit != 1 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	// a crash mid-merge: the leftover merge is aborted, the merge made again
	exit, _, rec, errb := runSync(w)
	app := syncRepoOf(t, rec, "app")
	if exit != 1 || !app.SelfHealed || strings.Join(app.Conflicts, ",") != "pkg/code.go" || w.head("app") != before || gitT(t, w.dir("app"), "rev-parse", "MERGE_HEAD") != main {
		t.Fatalf("re-entry: exit %d, %+v\n%s", exit, app, errb)
	}
	// the agent committed the merge by hand instead of `--continue`: a HEAD
	// the run never recorded is reset to before, the commit kept
	writeFile(t, w.dir("app"), "pkg/code.go", "package pkg\n\nfunc F() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "-A")
	gitT(t, w.dir("app"), "commit", "-q", "--no-edit")
	stray := w.head("app")
	exit, _, rec, errb = runSync(w)
	app = syncRepoOf(t, rec, "app")
	if exit != 1 || len(app.Discarded) != 1 || app.Discarded[0].Commit != stray || w.head("app") != before || strings.Join(app.Conflicts, ",") != "pkg/code.go" {
		t.Fatalf("re-entry resets the stray commit: exit %d, %+v\n%s", exit, app, errb)
	}
	if kept := gitT(t, w.dir("app"), "rev-parse", app.Discarded[0].Ref); kept != stray || !strings.HasPrefix(app.Discarded[0].Ref, "refs/polyforge/discarded/pf-sync/") {
		t.Fatalf("the stray commit is kept under %s: %s", app.Discarded[0].Ref, kept)
	}
	// another branch checked out under the run: refused (exit 2), nothing touched
	gitT(t, w.dir("app"), "merge", "--abort")
	gitT(t, w.dir("app"), "checkout", "-q", "-b", "elsewhere")
	exit, res, _, errb := runSync(w)
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "has elsewhere checked out") {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
	gitT(t, w.dir("app"), "checkout", "-q", w.branch)
	// resolved in place and continued: the run settles
	if exit, _, _, _ := runSync(w); exit != 1 {
		t.Fatal("the conflict is back")
	}
	writeFile(t, w.dir("app"), "pkg/code.go", "package pkg\n\nfunc F() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "pkg/code.go")
	exit, res, rec, errb = runSync(w, "--continue")
	if exit != 0 || strings.Join(syncRepoOf(t, rec, "app").Resolved, ",") != "pkg/code.go" || res.Heads["app"] != w.head("app") {
		t.Fatalf("exit %d, %+v\n%s", exit, res, errb)
	}
}

func TestSyncRefusesUnusableInvocations(t *testing.T) {
	w := newGitWorld(t, "app")
	empty := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--capsule", empty}, "task repo"},
		{[]string{"--capsule", w.capsule, "extra"}, "unexpected arguments"},
		{[]string{"--capsule", w.capsule, "--continue"}, "no sync in progress"},
	} {
		exit, _, errb := invokePy(t, scriptSync, tc.args, w.env())
		if exit != 2 || !strings.Contains(errb, tc.want) {
			t.Errorf("%v: exit %d, stderr %q (want 2 and %q)", tc.args, exit, errb, tc.want)
		}
	}
	// a linked worktree as a task repo is refused: its attribute override
	// would be the common dir's
	gitT(t, w.dir("app"), "worktree", "add", "-q", "--detach", filepath.Join(w.capsule, "repos", "linked"))
	exit, res, _, _ := runSync(w)
	if exit != 2 || !strings.Contains(res.Summary, "linked git worktree") {
		t.Fatalf("exit %d, %+v", exit, res)
	}
}

// TestSyncContinueOverRoundsRecordsEveryResolvedFile (review round 1):
// two conflicted files resolved over two `--continue` rounds — the first
// round, one file still unmerged, moves the resolved one to `resolved`
// and keeps the other under `conflicts`; the second round commits with
// `resolved` naming both. A resolved file marked again comes back under
// `conflicts`. And a `--continue` after the run settled is refused without
// touching the settled result file.
func TestSyncContinueOverRoundsRecordsEveryResolvedFile(t *testing.T) {
	w := newGitWorld(t, "app")
	w.change("app", map[string]string{"pkg/a.go": "package pkg\n\nfunc A() int { return 2 }\n", "pkg/b.go": "package pkg\n\nfunc B() int { return 2 }\n"}, "")
	w.mainMoves("app", map[string]string{"pkg/a.go": "package pkg\n\nfunc A() int { return 3 }\n", "pkg/b.go": "package pkg\n\nfunc B() int { return 3 }\n"}, "")
	exit, _, rec, errb := runSync(w)
	if exit != 1 || strings.Join(syncRepoOf(t, rec, "app").Conflicts, ",") != "pkg/a.go,pkg/b.go" {
		t.Fatalf("exit %d, %+v\n%s", exit, syncRepoOf(t, rec, "app"), errb)
	}
	// round 1: a resolved, b still unmerged
	writeFile(t, w.dir("app"), "pkg/a.go", "package pkg\n\nfunc A() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "pkg/a.go")
	exit, _, rec, _ = runSync(w, "--continue")
	app := syncRepoOf(t, rec, "app")
	if exit != 1 || strings.Join(app.Resolved, ",") != "pkg/a.go" || strings.Join(app.Conflicts, ",") != "pkg/b.go" {
		t.Fatalf("round 1 moves a to resolved, keeps b: exit %d, %+v", exit, app)
	}
	// a resolved file marked again comes back under conflicts
	writeFile(t, w.dir("app"), "pkg/a.go", "package pkg\n\n<<<<<<< HEAD\nfunc A() int { return 2 }\n=======\nfunc A() int { return 3 }\n>>>>>>> main\n")
	gitT(t, w.dir("app"), "add", "pkg/a.go")
	exit, _, rec, _ = runSync(w, "--continue")
	app = syncRepoOf(t, rec, "app")
	if exit != 1 || strings.Join(app.Conflicts, ",") != "pkg/b.go,pkg/a.go" || len(app.Resolved) != 0 {
		t.Fatalf("a marked again is pending again: exit %d, %+v", exit, app)
	}
	writeFile(t, w.dir("app"), "pkg/a.go", "package pkg\n\nfunc A() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "pkg/a.go")
	if exit, _, rec, _ = runSync(w, "--continue"); exit != 1 || strings.Join(syncRepoOf(t, rec, "app").Resolved, ",") != "pkg/a.go" {
		t.Fatalf("exit %d, %+v", exit, syncRepoOf(t, rec, "app"))
	}
	// round 2: b resolved — the commit records both
	writeFile(t, w.dir("app"), "pkg/b.go", "package pkg\n\nfunc B() int { return 5 }\n")
	gitT(t, w.dir("app"), "add", "pkg/b.go")
	exit, res, rec, errb := runSync(w, "--continue")
	app = syncRepoOf(t, rec, "app")
	if exit != 0 || strings.Join(app.Resolved, ",") != "pkg/a.go,pkg/b.go" || len(app.Conflicts) != 0 || app.After != w.head("app") {
		t.Fatalf("round 2 records every resolved file: exit %d, %+v\n%s", exit, app, errb)
	}
	// a stray --continue after the settle: refused, the settled result untouched
	before, _ := os.ReadFile(syncOut(w))
	exit, stdout, errb := invokePy(t, scriptSync, []string{"--capsule", w.capsule, "--continue", "--json", "--out", syncOut(w)}, w.env())
	after, _ := os.ReadFile(syncOut(w))
	if exit != 2 || stdout != "" || !strings.Contains(errb, "left as it is") || string(before) != string(after) || res.Verdict != VerdictPass {
		t.Fatalf("exit %d, stdout %q, stderr %q, result changed: %v", exit, stdout, errb, string(before) != string(after))
	}
}

// runSyncRound is runSync under the step round PF_ROUND=round.
func runSyncRound(w *gitWorld, round string, extra ...string) (int, *Result, *syncRecord, string) {
	t := w.t
	t.Helper()
	args := append([]string{"--capsule", w.capsule, "--json", "--out", syncOut(w)}, extra...)
	exit, _, errb := invokePy(t, scriptSync, args, w.env("PF_ROUND="+round))
	var res *Result
	var rec syncRecord
	if _, err := os.Stat(syncOut(w)); err == nil {
		res = readResult(t, syncOut(w))
		detailsOf(t, res, &rec)
	}
	return exit, res, &rec, errb
}

// discardedRefs answers the refs a re-entry kept discarded commits under.
func discardedRefs(t *testing.T, dir string) string {
	t.Helper()
	return gitT(t, dir, "for-each-ref", "--format=%(refname)", "refs/polyforge/discarded/")
}

// TestSyncAbortDropsTheMergeAndTheRecord: `sync.py --abort` — what the
// sync agent runs before it delivers a rewind or cancel intent — aborts
// the merge a real conflict left in progress (the tree back at the
// branch's own HEAD), removes the record, keeps a repo the run already
// merged, and writes the result marked aborted; a plain run after it
// starts over (no re-entry, nothing reset). With no sync in progress it
// aborts nothing and still succeeds.
func TestSyncAbortDropsTheMergeAndTheRecord(t *testing.T) {
	w := newGitWorld(t, "app", "lib")
	before := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "")
	w.mainMoves("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "")
	libMain := w.mainMoves("lib", map[string]string{"lib.txt": "l\n"}, "")
	if exit, _, _, errb := runSyncRound(w, "1"); exit != 1 {
		t.Fatalf("the conflict: exit %d\n%s", exit, errb)
	}
	exit, res, rec, errb := runSyncRound(w, "1", "--abort")
	if exit != 0 || res == nil || res.Round != "1" || !rec.Aborted || !strings.Contains(res.Summary, "aborted") {
		t.Fatalf("--abort: exit %d, %+v\n%s", exit, res, errb)
	}
	app := syncRepoOf(t, rec, "app")
	if !app.Aborted || app.After != "" || strings.Join(app.Conflicts, ",") != "pkg/code.go" {
		t.Fatalf("the aborted repo names what it abandoned and no after: %+v", app)
	}
	if lib := syncRepoOf(t, rec, "lib"); lib.Aborted || lib.After != libMain || w.head("lib") != libMain {
		t.Fatalf("a repo the run already merged keeps its merge: %+v (HEAD %s)", lib, short(w.head("lib")))
	}
	if _, err := os.Stat(filepath.Join(w.dir("app"), ".git", "MERGE_HEAD")); !os.IsNotExist(err) || w.head("app") != before || gitT(t, w.dir("app"), "status", "--porcelain") != "" {
		t.Fatalf("the merge is aborted and the tree clean at %s (HEAD %s)", short(before), short(w.head("app")))
	}
	if _, err := os.Stat(rec.Record); !os.IsNotExist(err) {
		t.Fatalf("the record is removed: %v", err)
	}
	// nothing in progress any more: --abort again aborts nothing, exit 0
	if exit, res, rec, errb := runSyncRound(w, "1", "--abort"); exit != 0 || !rec.Aborted || len(rec.Repos) != 0 || !strings.Contains(res.Summary, "nothing to abort") {
		t.Fatalf("--abort with no sync in progress: exit %d, %+v\n%s", exit, res, errb)
	}
	// --abort and --continue together: a usage error
	if exit, _, _, _ := runSyncRound(w, "1", "--abort", "--continue"); exit != 2 {
		t.Fatalf("--abort --continue: exit %d", exit)
	}
	// a plain run starts over: the same conflict, nothing discarded
	exit, _, rec, errb = runSyncRound(w, "2")
	if app := syncRepoOf(t, rec, "app"); exit != 1 || len(app.Discarded) != 0 || app.SelfHealed || app.Before != before {
		t.Fatalf("a run after the abort starts over: exit %d, %+v\n%s", exit, app, errb)
	}
}

// TestSyncRecordOfAnEarlierRoundNeverResetsNewerCommits: a sync record
// left by an earlier round (a real conflict the agent never aborted, then
// a rewind to build, which committed on top) is superseded, never held
// to: the leftover merge is aborted and the sync starts over from HEAD —
// build's commits stay on the branch, nothing is discarded.
func TestSyncRecordOfAnEarlierRoundNeverResetsNewerCommits(t *testing.T) {
	w := newGitWorld(t, "app")
	before := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "")
	main := w.mainMoves("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "")
	if exit, _, _, errb := runSyncRound(w, "1"); exit != 1 {
		t.Fatalf("the conflict: exit %d\n%s", exit, errb)
	}
	// build, resumed by a rewind, leaves the merge and adapts the change to
	// the default branch's
	gitT(t, w.dir("app"), "merge", "--abort")
	build := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n", "pkg/more.go": "package pkg\n"}, "")
	exit, res, rec, errb := runSyncRound(w, "2")
	app := syncRepoOf(t, rec, "app")
	if exit != 0 || len(app.Discarded) != 0 || discardedRefs(t, w.dir("app")) != "" || app.Before != build {
		t.Fatalf("an earlier round's record is superseded, nothing reset: exit %d, %+v (summary %q)\n%s", exit, app, res.Summary, errb)
	}
	// build's commit and the default branch both on the branch (gitT fails
	// the test when merge-base --is-ancestor exits non-zero)
	gitT(t, w.dir("app"), "merge-base", "--is-ancestor", build, "HEAD")
	gitT(t, w.dir("app"), "merge-base", "--is-ancestor", main, "HEAD")
	if !strings.Contains(strings.Join(app.Notes, " "), "round 1") || before == build {
		t.Fatalf("the superseded record is named: %+v", app.Notes)
	}
}

// TestSyncRefusesToDiscardWorkCommittedAfterItStarted: within one round, a
// HEAD that moved past `before` with commits that are not the sync's own
// merge (work committed on top of the record) is refused (exit 2), never
// reset — the commits stay; `sync.py --abort` drops the record and the
// next run keeps them. A hand-made merge commit (the sync's own merge) is
// still reset and made again (TestSyncReentryResetsAndMergesAgain).
func TestSyncRefusesToDiscardWorkCommittedAfterItStarted(t *testing.T) {
	w := newGitWorld(t, "app")
	w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 2 }\n"}, "")
	w.mainMoves("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "")
	if exit, _, _, errb := runSyncRound(w, "1"); exit != 1 {
		t.Fatalf("the conflict: exit %d\n%s", exit, errb)
	}
	gitT(t, w.dir("app"), "merge", "--abort")
	work := w.change("app", map[string]string{"pkg/code.go": "package pkg\n\nfunc F() int { return 3 }\n"}, "")
	exit, res, _, errb := runSyncRound(w, "1")
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, short(work)) || !strings.Contains(res.Summary, "sync.py --abort") {
		t.Fatalf("work committed after the sync started is refused, not discarded: exit %d, %+v\n%s", exit, res, errb)
	}
	if w.head("app") != work || discardedRefs(t, w.dir("app")) != "" {
		t.Fatalf("nothing reset: HEAD %s, discarded %q", short(w.head("app")), discardedRefs(t, w.dir("app")))
	}
	if exit, _, _, errb := runSyncRound(w, "1", "--abort"); exit != 0 {
		t.Fatalf("--abort: exit %d\n%s", exit, errb)
	}
	exit, _, rec, errb := runSyncRound(w, "1")
	if app := syncRepoOf(t, rec, "app"); exit != 0 || app.Before != work || len(app.Discarded) != 0 {
		t.Fatalf("after the abort the run keeps the work: exit %d, %+v\n%s", exit, app, errb)
	}
}
