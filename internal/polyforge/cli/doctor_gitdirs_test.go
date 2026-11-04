package cli

// doctor_gitdirs_test.go: the "capsule retention" row — the default run
// reports and deletes nothing; --prune-capsules acts only on a terminal
// work item's entries past the retention that no daemon job names —
// slimming a wrapped work item's capsule to artifacts/ + capsule.json,
// deleting the rest — and on nothing at all while the daemon is down; the
// work-item reads it makes are bounded by the mapped projects, not by the
// entries.

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// gitDirsFixture lays out a data dir and a workspace's quarantined
// capsules, backdating every entry except the fresh one.
type gitDirsFixture struct {
	env                                                 map[string]string
	reclaim, retiredGit, retiredCap, fresh, active, job string
	ghost, activeCap, askpass                           string

	rec *reqRecorder
	// unlisted names the known work items the project list leaves out
	// (an owner past the list's page): only a per-id read finds them.
	unlisted map[string]bool
}

func newGitDirsFixture(t *testing.T, daemonUp bool) gitDirsFixture {
	t.Helper()
	states := map[string]string{"wi_done": "wrapped", "wi_fresh": "failed", "wi_active": "running", "wi_job": "canceled"}
	rec, unlisted := &reqRecorder{}, map[string]bool{}
	srv := newFakeAihub(t, rec, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/work-items" {
			var items []string
			for id, st := range states {
				if !unlisted[id] && r.URL.Query().Get("project") == "polyforge" {
					items = append(items, `{"id":"`+id+`","status":"`+st+`"}`)
				}
			}
			io.WriteString(w, `{"items":[`+strings.Join(items, ",")+`],"total":`+strconv.Itoa(len(items))+`}`)
			return
		}
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/v1/work-items/"); ok {
			if st, known := states[id]; known {
				io.WriteString(w, `{"id":"`+id+`","status":"`+st+`"}`)
				return
			}
			w.WriteHeader(404)
			io.WriteString(w, `{"code":"CORE-404","detail":"no such work item"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/ready-queue") {
			io.WriteString(w, `{"project":"polyforge","items":[],"running":[],"paused":[]}`)
			return
		}
		io.WriteString(w, `{}`)
	})
	sock := filepath.Join(t.TempDir(), "dead.sock")
	if daemonUp {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"machine_id":"m-g","jobs":[{"work_item_id":"wi_job","status":"running"}],"queue":{}}`)
		})
		sock = newFakeDaemon(t, mux)
	}
	env := doctorServerConfig(t, srv.URL, "k_ok", sock, "polyforge")
	ws := filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
	data := t.TempDir()
	env["POLYFORGE_DATA_DIR"] = data
	git := filepath.Join(data, "git")
	f := gitDirsFixture{
		env:        env,
		rec:        rec,
		unlisted:   unlisted,
		reclaim:    filepath.Join(git, "wi_done.git"),
		retiredGit: filepath.Join(git, "wi_done.git.quarantined.abc"),
		retiredCap: filepath.Join(ws, "capsules", "polyforge-1.quarantined.abc"),
		fresh:      filepath.Join(git, "wi_fresh.git"),
		active:     filepath.Join(git, "wi_active.git"),
		job:        filepath.Join(git, "wi_job.git"),
		ghost:      filepath.Join(git, "wi_ghost.git"),
		activeCap:  filepath.Join(ws, "capsules", "polyforge-2.quarantined.def"),
		askpass:    filepath.Join(git, "askpass"),
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, d := range []string{f.reclaim, f.retiredGit, f.fresh, f.active, f.job, f.ghost, f.askpass} {
		touch(t, filepath.Join(d, "objects", "blob"), old)
	}
	touch(t, filepath.Join(f.retiredCap, "capsule.json"), old)
	os.WriteFile(filepath.Join(f.retiredCap, "capsule.json"), []byte(`{"work_item_id":"wi_done"}`), 0o644)
	// the wrapped work item's set-aside capsule: a slim keeps artifacts/
	touch(t, filepath.Join(f.retiredCap, "artifacts", "plan.md"), old)
	touch(t, filepath.Join(f.retiredCap, "runtime-state", "execution.json"), old)
	touch(t, filepath.Join(f.activeCap, "capsule.json"), old)
	os.WriteFile(filepath.Join(f.activeCap, "capsule.json"), []byte(`{"work_item_id":"wi_active"}`), 0o644)
	// backdate every path but the fresh dir's
	for _, root := range []string{git, filepath.Join(ws, "capsules")} {
		filepath.WalkDir(root, func(p string, _ fs.DirEntry, _ error) error {
			if !strings.HasPrefix(p, f.fresh) {
				os.Chtimes(p, old, old)
			}
			return nil
		})
	}
	return f
}

func assertExist(t *testing.T, want bool, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Fatalf("%s exists = %t, want %t", p, err == nil, want)
		}
	}
}

func TestDoctorGitDirsReportOnlyByDefault(t *testing.T) {
	f := newGitDirsFixture(t, true)
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "warn" {
		t.Fatalf("status = %s (%s), want warn", st, detail)
	}
	for _, want := range []string{"reclaimable 1 (", "retired 2 (", "retained 4, ", "unknown 1 — ", "--prune-capsules", "nothing was deleted"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	// A Linux install's final doctor run carries no darwin Gatekeeper
	// vocabulary (TestW237InstallLinuxNeverSigns): the default report
	// names no path, so the word must not appear in the row's own text.
	if strings.Contains(detail, "quarantine") {
		t.Fatalf("the default report says \"quarantine\": %s", detail)
	}
	assertExist(t, true, f.reclaim, f.retiredGit, f.retiredCap, f.fresh, f.active, f.job, f.ghost, f.activeCap, f.askpass)
}

func TestDoctorGitDirsPruneRemovesOnlyTerminalPastRetention(t *testing.T) {
	f := newGitDirsFixture(t, true)
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "pass" || !strings.Contains(detail, "pruned 2: ") || !strings.Contains(detail, "slimmed 1 to artifacts/ + capsule.json: "+f.retiredCap) {
		t.Fatalf("status = %s (%s), want pass with 2 pruned and the wrapped scene slimmed", st, detail)
	}
	for _, p := range []string{f.reclaim, f.retiredGit} {
		if !strings.Contains(detail, p) {
			t.Fatalf("detail does not list removed %s: %s", p, detail)
		}
	}
	assertExist(t, false, f.reclaim, f.retiredGit, filepath.Join(f.retiredCap, "runtime-state"))
	assertSlimmed(t, f.retiredCap)
	assertExist(t, true, f.fresh, f.active, f.job, f.ghost, f.activeCap, f.askpass)
}

// assertSlimmed: the capsule holds exactly artifacts/ (its files intact)
// and capsule.json.
func assertSlimmed(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"artifacts", "capsule.json"}) {
		t.Fatalf("%s holds %v, want exactly [artifacts capsule.json]", dir, names)
	}
	assertExist(t, true, filepath.Join(dir, "artifacts", "plan.md"))
}

func TestDoctorGitDirsPruneNothingWhileDaemonDown(t *testing.T) {
	f := newGitDirsFixture(t, false)
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "warn" || !strings.Contains(detail, "nothing pruned") {
		t.Fatalf("status = %s (%s), want warn naming nothing pruned", st, detail)
	}
	assertExist(t, true, f.reclaim, f.retiredGit, f.retiredCap, f.fresh, f.active, f.job, f.ghost, f.activeCap, f.askpass)
}

// workItemReads answers the run's aihub work-item reads: the project
// lists and the ids read one by one.
func workItemReads(rec *reqRecorder) (lists int, ids []string) {
	for _, q := range rec.all() {
		if strings.HasPrefix(q.Path, "/api/v1/work-items?") {
			lists++
		} else if id, ok := strings.CutPrefix(q.Path, "/api/v1/work-items/"); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return lists, ids
}

// TestDoctorGitDirsAihubReadsAreBounded: the row reads aihub per mapped
// project, not per entry. Eight entries name five owners: wi_fresh's
// dir is young and wi_job's is a daemon job, so neither owner is read;
// the other three take ONE project list, plus a per-id read only for
// what the list did not name — wi_ghost (no such work item) and
// wi_active (left off the list: an owner past its page). The verdicts
// are the ones a read per entry gives.
func TestDoctorGitDirsAihubReadsAreBounded(t *testing.T) {
	f := newGitDirsFixture(t, true)
	f.unlisted["wi_active"] = true
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor")
	_, detail := doctorFind(t, doc, docChCapsuleRetention)
	if lists, ids := workItemReads(f.rec); lists != 1 || !slices.Equal(ids, []string{"wi_active", "wi_ghost"}) {
		t.Fatalf("work-item reads: %d list(s), per-id %v; want 1 list (one mapped project) and per-id [wi_active wi_ghost] (what it did not name)", lists, ids)
	}
	for _, want := range []string{"reclaimable 1 (", "retired 2 (", "retained 4, ", "unknown 1 — "} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
}

// TestDoctorGitDirsLocalRetentionReadsNothing: an entry younger than the
// retention or named by a daemon job is retained on local facts alone,
// so with every entry so the row reads no work item; and an owner count
// no larger than the mapped projects is read per id, never listed.
func TestDoctorGitDirsLocalRetentionReadsNothing(t *testing.T) {
	cases := []struct {
		name       string
		oldReclaim bool // leave wi_done's git dir past the retention
		wantIDs    []string
		wantClass  []string
	}{
		{"all local", false, nil, []string{"reclaimable 0 (0 B)", "retired 0 (0 B)", "retained 8, ", "unknown 0 — "}},
		{"one owner", true, []string{"wi_done"}, []string{"reclaimable 1 (", "retired 0 (0 B)", "retained 7, ", "unknown 0 — "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newGitDirsFixture(t, true)
			young := []string{f.retiredGit, f.retiredCap, f.active, f.ghost, f.activeCap} // f.job stays old: a daemon job names it
			if !c.oldReclaim {
				young = append(young, f.reclaim)
			}
			now := time.Now()
			for _, d := range young {
				if err := os.Chtimes(d, now, now); err != nil {
					t.Fatal(err)
				}
			}
			_, _, doc := doctorStatus(t, f.env, "--json", "doctor")
			_, detail := doctorFind(t, doc, docChCapsuleRetention)
			if lists, ids := workItemReads(f.rec); lists != 0 || !slices.Equal(ids, c.wantIDs) {
				t.Fatalf("work-item reads: %d list(s), per-id %v; want no list and per-id %v", lists, ids, c.wantIDs)
			}
			for _, want := range c.wantClass {
				if !strings.Contains(detail, want) {
					t.Fatalf("detail lacks %q: %s", want, detail)
				}
			}
		})
	}
}

// TestDoctorGitDirsWalksOnlyPruneCandidates: the walk (the row's cost
// that grows with the tree) is taken only by an entry a prune may
// remove — a terminal owner's, past the retention by its own mtime, no
// daemon job's; the young, the live job's, the running owner's and the
// unknown owner's entries are judged without it. And the walk is what
// keeps a terminal owner's entry whose own mtime is old but whose tree
// changed inside the retention: wi_fresh's dir is backdated here, its
// objects/ is not, and a prune leaves it.
func TestDoctorGitDirsWalksOnlyPruneCandidates(t *testing.T) {
	f := newGitDirsFixture(t, true)
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(f.fresh, old, old); err != nil {
		t.Fatal(err)
	}
	var walked []string
	orig := measureEntry
	measureEntry = func(d retainedDir) retainedDir {
		walked = append(walked, d.Path)
		return orig(d)
	}
	defer func() { measureEntry = orig }()
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	if st, detail := doctorFind(t, doc, docChCapsuleRetention); st != "pass" || !strings.Contains(detail, "pruned 2: ") || !strings.Contains(detail, "slimmed 1 ") {
		t.Fatalf("status = %s (%s), want pass with 2 pruned and 1 slimmed", st, detail)
	}
	slices.Sort(walked)
	want := []string{f.reclaim, f.retiredGit, f.retiredCap, f.fresh}
	slices.Sort(want)
	if !slices.Equal(walked, want) {
		t.Fatalf("walked %v, want only the terminal owners' entries %v", walked, want)
	}
	assertExist(t, false, f.reclaim, f.retiredGit)
	assertSlimmed(t, f.retiredCap)
	assertExist(t, true, f.fresh, f.active, f.job, f.ghost, f.activeCap, f.askpass)
}

// TestDoctorGitDirsKeepsSceneWithUncommittedChange: the guard's second
// face — a set-aside capsule whose task-repo clone is dirty but holds no
// commit is kept just like one with unpushed commits, and its scene's
// external git dir is kept with it. The clone is unborn (no commit, no
// remote), so only `git status` can see this work.
func TestDoctorGitDirsKeepsSceneWithUncommittedChange(t *testing.T) {
	f := newGitDirsFixture(t, true)
	repo := filepath.Join(f.retiredCap, "repos", "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	err := filepath.Walk(f.retiredCap, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "pass" || !strings.Contains(detail, "uncommitted changes") {
		t.Fatalf("status = %s (%s), want pass naming the uncommitted changes", st, detail)
	}
	assertExist(t, true, f.retiredCap, repo, f.retiredGit, f.reclaim)
}

// TestDoctorGitDirsPrunesSceneWhoseWorkTheWorkspaceCarried: the fresher
// view face of the guard. A set-aside scene's own refs stop updating at
// quarantine time, so its clone can call a commit unpushed that the
// mapped workspace's .repo/<name> clone already reaches through its
// remote-tracking ref. Such a scene holds no unmerged work: its wrapped
// work item's capsule is slimmed and its paired git dir pruned.
func TestDoctorGitDirsPrunesSceneWhoseWorkTheWorkspaceCarried(t *testing.T) {
	f := newGitDirsFixture(t, true)
	ws := filepath.Dir(filepath.Dir(f.retiredCap))
	upstream := reposClone(t, ws, "demo", "main")
	repo := filepath.Join(f.retiredCap, "repos", "demo")
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		t.Fatal(err)
	}
	reposGit(t, ws, "clone", "-q", upstream, repo)
	// The quarantine-time state: the scene's own remote-tracking refs are
	// gone, while .repo/demo still reaches the commit through origin/main.
	reposGit(t, repo, "remote", "remove", "origin")
	old := time.Now().Add(-30 * 24 * time.Hour)
	err := filepath.Walk(f.retiredCap, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "pass" || !strings.Contains(detail, "pruned 2: ") || !strings.Contains(detail, "slimmed 1 ") {
		t.Fatalf("status = %s (%s), want pass with 2 pruned and 1 slimmed", st, detail)
	}
	if strings.Contains(detail, "kept for unmerged work") {
		t.Fatalf("the scene whose work .repo/demo carries is reported as unmerged: %s", detail)
	}
	assertExist(t, false, f.reclaim, f.retiredGit, repo)
	assertSlimmed(t, f.retiredCap)
	assertExist(t, true, upstream)
}

// liveCapsule lays out a live capsule (capsules/<name>, no quarantine
// suffix) of wi with every top-level entry a claim leaves, backdated past
// the retention.
func liveCapsule(t *testing.T, f gitDirsFixture, name, wi string, full bool) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(f.retiredCap), name)
	old := time.Now().Add(-30 * 24 * time.Hour)
	touch(t, filepath.Join(dir, "artifacts", "plan.md"), old)
	touch(t, filepath.Join(dir, "capsule.json"), old)
	if err := os.WriteFile(filepath.Join(dir, "capsule.json"), []byte(`{"work_item_id":"`+wi+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if full {
		for _, p := range []string{".git", ".gitignore", "repos/demo/README.md", "runtime-state/execution.json",
			"logs/step.log", "session/transcript.jsonl", "workflows/chore/workflow.md", "sync/state.json"} {
			touch(t, filepath.Join(dir, p), old)
		}
	}
	err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDoctorCapsuleRetentionSlimsWrappedLiveCapsule: a
// wrapped work item's live capsule past the retention is reported
// reclaimable and a prune slims it to artifacts/ + capsule.json; a failed
// work item's live capsule is never touched, and a capsule already
// slimmed is retained without a walk.
func TestDoctorCapsuleRetentionSlimsWrappedLiveCapsule(t *testing.T) {
	f := newGitDirsFixture(t, true)
	wrapped := liveCapsule(t, f, "demo-3", "wi_done", true)
	failed := liveCapsule(t, f, "demo-4", "wi_fresh", true)
	slim := liveCapsule(t, f, "demo-5", "wi_done", false)

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "warn" {
		t.Fatalf("status = %s (%s), want warn", st, detail)
	}
	for _, want := range []string{"reclaimable 2 (", "retired 2 (", "retained 6, ", "unknown 1 — ", "slimmed to artifacts/ + capsule.json", "nothing was deleted"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	assertExist(t, true, filepath.Join(wrapped, "repos"), filepath.Join(wrapped, ".git"))

	var walked []string
	orig := measureEntry
	measureEntry = func(d retainedDir) retainedDir {
		walked = append(walked, d.Path)
		return orig(d)
	}
	defer func() { measureEntry = orig }()
	_, _, doc = doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail = doctorFind(t, doc, docChCapsuleRetention)
	if st != "pass" || !strings.Contains(detail, "slimmed 2 to artifacts/ + capsule.json: ") || !strings.Contains(detail, wrapped) {
		t.Fatalf("status = %s (%s), want pass with the wrapped capsule slimmed", st, detail)
	}
	assertSlimmed(t, wrapped)
	assertSlimmed(t, slim)
	for _, p := range []string{".git", ".gitignore", "repos", "runtime-state", "logs", "session", "workflows", "sync"} {
		assertExist(t, true, filepath.Join(failed, p))
	}
	if slices.Contains(walked, slim) || slices.Contains(walked, failed) {
		t.Fatalf("walked %v: neither an already slimmed capsule nor a failed work item's live capsule may be walked", walked)
	}

	// The slimmed capsule is retained by the next run, not slimmed again.
	_, _, doc = doctorStatus(t, f.env, "--json", "doctor")
	if _, detail := doctorFind(t, doc, docChCapsuleRetention); !strings.Contains(detail, "reclaimable 0 (0 B)") {
		t.Fatalf("a slimmed capsule is still reclaimable: %s", detail)
	}
}

// TestDoctorCapsuleRetentionKeepsWrappedCapsuleWithUnmergedWork: the
// guard covers a live capsule too — a wrapped work item's
// capsule whose task clone is dirty is retained whole, named with why.
func TestDoctorCapsuleRetentionKeepsWrappedCapsuleWithUnmergedWork(t *testing.T) {
	f := newGitDirsFixture(t, true)
	dir := liveCapsule(t, f, "demo-3", "wi_done", true)
	repo := filepath.Join(dir, "repos", "demo")
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "pass" || !strings.Contains(detail, dir+" (demo: uncommitted changes)") {
		t.Fatalf("status = %s (%s), want pass naming the kept capsule", st, detail)
	}
	for _, p := range []string{".git", "repos/demo/README.md", "runtime-state", "logs"} {
		assertExist(t, true, filepath.Join(dir, p))
	}
}

// TestDoctorCapsuleRetentionSlimsNothingWhileDaemonDown: the live jobs
// are unknown without the daemon, so no capsule is slimmed.
func TestDoctorCapsuleRetentionSlimsNothingWhileDaemonDown(t *testing.T) {
	f := newGitDirsFixture(t, false)
	dir := liveCapsule(t, f, "demo-3", "wi_done", true)
	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-capsules")
	st, detail := doctorFind(t, doc, docChCapsuleRetention)
	if st != "warn" || !strings.Contains(detail, "nothing pruned") {
		t.Fatalf("status = %s (%s), want warn naming nothing pruned", st, detail)
	}
	for _, p := range []string{".git", "repos", "runtime-state", "logs", "session", "workflows", "sync"} {
		assertExist(t, true, filepath.Join(dir, p))
	}
}
