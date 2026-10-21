package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// doctor_repos_test.go: the read-only git probe layer fixtures — real git
// repos built inside t.TempDir() (init, commit, branch, worktree add,
// dirty file, unborn HEAD); nothing is ever created outside the test's
// own temp directories.

// reposGit runs one repository-shaping git command (the fixtures write;
// the probe layer under test never does) and fails the test on error.
func reposGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=pf-test", "GIT_AUTHOR_EMAIL=pf-test@invalid",
		"GIT_COMMITTER_NAME=pf-test", "GIT_COMMITTER_EMAIL=pf-test@invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (dir %s): %v: %s", args, dir, err, out)
	}
}

// reposClone shapes ws/.repo/<name> as a real git clone of a fresh bare
// remote, on branch, with that branch pushed (a remote-tracking ref set):
// clean and pushed until the caller dirties it. Returns the clone dir.
func reposClone(t *testing.T, ws, name, branch string) string {
	t.Helper()
	bare := t.TempDir()
	reposGit(t, bare, "init", "-q", "--bare", "-b", "main")
	dir := filepath.Join(ws, ".repo", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reposGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\necho deploy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reposGit(t, dir, "add", ".")
	reposGit(t, dir, "commit", "-q", "-m", "init")
	reposGit(t, dir, "remote", "add", "origin", bare)
	reposGit(t, dir, "push", "-q", "-u", "origin", "main")
	if branch != "main" {
		reposGit(t, dir, "checkout", "-q", "-b", branch)
		reposGit(t, dir, "push", "-q", "-u", "origin", branch)
	}
	return dir
}

// reposDirty modifies the tracked deploy.sh (` M deploy.sh`).
func reposDirty(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\necho hand edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func reposUnit(t *testing.T, units []repoUnit, suffix string) repoUnit {
	t.Helper()
	for _, u := range units {
		if strings.HasSuffix(u.Dir, suffix) {
			return u
		}
	}
	t.Fatalf("no unit with dir suffix %q in %+v", suffix, units)
	return repoUnit{}
}

// Facts: a pushed branch is pushed; a dirty tracked file is dirty; a
// committed-but-unpushed commit flags like uncommitted changes. The same
// repo's unit answers all three shapes.
func TestRepoUnitsFactsOnBranch(t *testing.T) {
	ws := t.TempDir()
	dir := reposClone(t, ws, "one", "release/v0.0.27")

	units := workspaceRepoUnits(ws)
	if len(units) != 1 {
		t.Fatalf("units: %+v, want one clone", units)
	}
	u := reposUnit(t, units, ".repo/one")
	if u.Dir != dir || u.CloneName != "one" || u.Branch != "release/v0.0.27" || u.Dirty || u.Unpushed || u.Checked != "" {
		t.Fatalf("pushed clean clone: %+v", u)
	}

	reposDirty(t, dir)
	u = workspaceRepoUnits(ws)[0]
	if !u.Dirty || u.Unpushed {
		t.Fatalf("dirty clone: %+v", u)
	}

	reposGit(t, dir, "add", ".")
	reposGit(t, dir, "commit", "-q", "-m", "local only")
	u = workspaceRepoUnits(ws)[0]
	if u.Dirty || !u.Unpushed {
		t.Fatalf("unpushed local commit: %+v", u)
	}
}

// Enumeration: clones and only clones — a sibling without .git and the
// wrapper directory of the worktree shape are never units.
func TestRepoUnitsEnumeratesClonesOnly(t *testing.T) {
	ws := t.TempDir()
	reposClone(t, ws, "a", "main")
	reposClone(t, ws, "b", "main")
	if err := os.MkdirAll(filepath.Join(ws, ".repo", "not-a-repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".repo", "not-a-repo", "notes.txt"), []byte("plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	units := workspaceRepoUnits(ws)
	if len(units) != 2 {
		t.Fatalf("units: %+v, want exactly clones a and b", units)
	}
	for _, u := range units {
		if !strings.HasPrefix(u.Dir, filepath.Join(ws, ".repo")) || u.Checked != "" {
			t.Fatalf("unexpected unit: %+v", u)
		}
	}
}

// Linked worktrees: reached through the clone's own worktree list, at any
// location (the nested wrap/cl-wtree shape of the acceptance suite); the
// wrapper directory itself is never a unit. A worktree sitting directly
// under .repo/ carries its .git pointer file and is enumerated as the
// clone it is — deduplicated against the linked face, one unit.
func TestRepoUnitsLinkedWorktreesReachedAndDeduped(t *testing.T) {
	ws := t.TempDir()
	parent := reposClone(t, ws, "parent", "main")
	nested := filepath.Join(ws, ".repo", "wrap", "cl-wtree")
	reposGit(t, parent, "worktree", "add", "-q", "-b", "feature/wt", nested)
	if err := os.WriteFile(filepath.Join(nested, "scratch-edit.txt"), []byte("untracked hand work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	units := workspaceRepoUnits(ws)
	if len(units) != 2 {
		t.Fatalf("units: %+v, want parent + nested worktree (wrapper dir included)", units)
	}
	wu := reposUnit(t, units, "wrap/cl-wtree")
	if wu.Branch != "feature/wt" || !wu.Dirty || wu.Unpushed || wu.CloneName != "parent" {
		t.Fatalf("nested worktree: %+v", wu)
	}
	pu := reposUnit(t, units, ".repo/parent")
	if pu.Dirty || pu.Branch != "main" || pu.CloneName != "parent" {
		t.Fatalf("parent clone: %+v (the worktree's dirt must not leak across units)", pu)
	}

	// The same git worktree added directly under .repo/: the pointer file
	// enumerates it as the clone it is; no second unit.
	ws2 := t.TempDir()
	parent2 := reposClone(t, ws2, "parent", "main")
	direct := filepath.Join(ws2, ".repo", "wt-direct")
	reposGit(t, parent2, "worktree", "add", "-q", "-b", "feature/direct", direct)
	units2 := workspaceRepoUnits(ws2)
	if len(units2) != 2 {
		t.Fatalf("units (direct worktree): %+v, want parent + wt-direct once", units2)
	}
	du := reposUnit(t, units2, ".repo/wt-direct")
	if du.Branch != "feature/direct" || du.CloneName != "wt-direct" || du.Checked != "" {
		t.Fatalf("direct worktree: %+v", du)
	}
}

// Detached HEAD: the branch is "" (the delivery decision has nothing to
// read); the dirt is still reported.
func TestRepoUnitsDetachedHead(t *testing.T) {
	ws := t.TempDir()
	reposClone(t, ws, "det", "main")
	reposGit(t, filepath.Join(ws, ".repo", "det"), "checkout", "-q", "--detach")

	u := reposUnit(t, workspaceRepoUnits(ws), ".repo/det")
	if u.Branch != "" || u.Checked != "" {
		t.Fatalf("detached clone: %+v", u)
	}
	reposDirty(t, filepath.Join(ws, ".repo", "det"))
	u = workspaceRepoUnits(ws)[0]
	if u.Branch != "" || !u.Dirty {
		t.Fatalf("detached dirty clone: %+v", u)
	}
}

// No remotes at all: HEAD flags as unpushed (the committed work is on no
// human face). Unborn HEAD: judged by status alone.
func TestRepoUnitsNoRemotesAndUnbornHead(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".repo", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	reposGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("hand work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reposGit(t, dir, "add", ".")
	reposGit(t, dir, "commit", "-q", "-m", "hand work, never pushed")

	u := reposUnit(t, workspaceRepoUnits(ws), ".repo/local")
	if !u.Unpushed || u.Dirty || u.Branch != "main" || u.Checked != "" {
		t.Fatalf("no-remotes clone: %+v", u)
	}

	unborn := filepath.Join(ws, ".repo", "fresh")
	if err := os.MkdirAll(unborn, 0o755); err != nil {
		t.Fatal(err)
	}
	reposGit(t, unborn, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(unborn, "draft.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u = reposUnit(t, workspaceRepoUnits(ws), ".repo/fresh")
	if u.Branch != "main" || !u.Dirty || u.Unpushed || u.Checked != "" {
		t.Fatalf("unborn HEAD clone: %+v", u)
	}
}

// Never-blank convention: a repo whose probe fails carries its exact
// reason (an unscannable unit is listed, never silently clean).
func TestRepoUnitsUnscannableCarriesReason(t *testing.T) {
	ws := t.TempDir()
	reposClone(t, ws, "good", "main")
	bad := filepath.Join(ws, ".repo", "bad")
	if err := os.MkdirAll(filepath.Join(bad, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	units := workspaceRepoUnits(ws)
	bu := reposUnit(t, units, ".repo/bad")
	if bu.Checked == "" {
		t.Fatalf(".git dir only: probe must not fake clean: %+v", bu)
	}
	// The good clone is unaffected: still scanned, still clean-and-pushed.
	gu := reposUnit(t, units, ".repo/good")
	if gu.Checked != "" || gu.Dirty || gu.Unpushed {
		t.Fatalf("good clone behind a bad one: %+v", gu)
	}
}

// A linked worktree whose checked-out directory is gone: git keeps listing
// it, the probe fails, the unit carries its reason (never a clean face);
// the listing clone stays scannable, its own dirt its own unit's business.
func TestRepoUnitsGoneWorktreeCarriesReason(t *testing.T) {
	ws := t.TempDir()
	wt := filepath.Join(t.TempDir(), "wt")
	clone := reposClone(t, ws, "lost", "main")
	reposGit(t, clone, "worktree", "add", "-q", "-b", "feature/x", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	units := workspaceRepoUnits(ws)
	if len(units) != 2 {
		t.Fatalf("units: %+v, want clone + gone worktree", units)
	}
	cu := reposUnit(t, units, ".repo/lost")
	if cu.Checked != "" && cu.Dir == clone {
		t.Fatalf("clone unit must stay scannable: %+v", cu)
	}
	wu := reposUnit(t, units, filepath.Base(wt))
	if wu.Checked == "" {
		t.Fatalf("gone worktree: probe must carry a reason, not fake clean: %+v", wu)
	}
}

// Doctor-row scenario fixtures. The fake aihub serves the project face
// (the assignment facts) and the work-item face (coverage); the config
// maps ONE workspace to project "e2e", with repo "pi-web" registered —
// the acceptance suite's registry shape.

// reposAihub is the small aihub the doctor-row scenarios drive.
type reposAihub struct {
	known    map[string][]api.ProjectRepo // project → repos (GET /api/v1/projects/<p>)
	items    map[string]string            // slug → status (GET /api/v1/work-items/<slug>); 404 otherwise
	projFail map[string]bool              // project names served unanswerable (mid-response close)
	slugged  map[string]bool              // slug faces served unanswerable
}

// reposAihubUrl closes the fixture's transport mid-response for the
// forced-fail faces so the CLI sees a transport failure, never a typed
// one (which would read as a definitive answer).
func hijackClose(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	if conn, _, err := hj.Hijack(); err == nil {
		conn.Close()
	}
}

func (f *reposAihub) server(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/work-items/") {
			slug, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/v1/work-items/"))
			if f.slugged[slug] {
				hijackClose(w)
				return
			}
			if st, ok := f.items[slug]; ok {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"wi_x","slug":"`+slug+`","project":"e2e","status":"`+st+`"}`)
				return
			}
			w.WriteHeader(404)
			io.WriteString(w, `{"code":"CORE-404","detail":"no such work item"}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/projects/") && strings.HasSuffix(r.URL.Path, "/ready-queue") {
			// the doctor's aihub projects / auth probe: the member-readable read
			p, _ := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/ready-queue"))
			if _, ok := f.known[p]; !ok {
				w.WriteHeader(404)
				io.WriteString(w, `{"code":"CORE-404","detail":"no such project"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"project":"`+p+`","items":[],"running":[],"paused":[]}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/projects/") {
			p, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"))
			if f.projFail[p] {
				hijackClose(w)
				return
			}
			repos, ok := f.known[p]
			if !ok {
				w.WriteHeader(404)
				io.WriteString(w, `{"code":"CORE-404","detail":"no such project"}`)
				return
			}
			var rows []string
			for _, rr := range repos {
				rows = append(rows, `{"name":"`+rr.Name+`","url":"`+rr.URL+`"}`)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"prj_1","name":"`+p+`","repos":[`+strings.Join(rows, ",")+`]}`)
			return
		}
		w.WriteHeader(404)
		io.WriteString(w, `{"code":"CORE-404","detail":"?"}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// reposAihubDefault: project "e2e" with repo "pi-web" registered; WI
// status map empty (delivery branches 404 unless a slug is pinned).
func reposAihubDefault(t *testing.T) string {
	t.Helper()
	f := &reposAihub{
		known: map[string][]api.ProjectRepo{"e2e": {{Name: "pi-web", URL: "file:///nonexistent/pi-web.git"}}},
		items: map[string]string{},
	}
	return f.server(t)
}

// reposDoctor runs pf --json doctor over a config that maps ws to the
// e2e project (the registry shape; the config's socket is a gone path so
// the daemon-flavored rows skip/fail — the scenarios assert the repos
// clones row itself). server overrides the config server ("" = srv).
func reposDoctor(t *testing.T, srv, server, ws string, extra ...string) doctorDoc {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if server == "" {
		server = srv
	}
	sock := filepath.Join(dir, "gone.sock")
	var b strings.Builder
	b.WriteString("server = \"" + server + "\"\napi_key = \"k\"\ndaemon_socket = \"" + sock + "\"\n")
	b.WriteString("\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n")
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, map[string]string{"XDG_CONFIG_HOME": xdg}, append([]string{"--json", "doctor"}, extra...)...)
	return doc
}

// reposFind (reposStamp): asserts the repos clones row in detail with the
// expected status and answers it.
func reposRow(t *testing.T, doc doctorDoc, want string) (string, string) {
	t.Helper()
	for _, c := range doc.Checks {
		if c.Name == docChRepoClones {
			if c.Status != want {
				t.Fatalf("check %q = %q (%q), want %q", docChRepoClones, c.Status, c.Detail, want)
			}
			return c.Status, c.Detail
		}
	}
	t.Fatalf("no %q check in %+v", docChRepoClones, doc.Checks)
	return "", ""
}

// The wired row: today's real pi-web shape — a dirty clone on a
// release branch warns, and the warning names ruling 11's remediation
// (pf file create + the polyforge/ delivery-branch shape + or-discard).
func TestRepoClonesDirtyReleaseWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "pi-web", "release/v0.0.27"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	for _, tok := range []string{"pf file create", "polyforge/", "discard", "--project e2e --repo pi-web"} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("the pi-web warning does not name remediation token %q: %q", tok, detail)
		}
	}
}

// A clean-pushed clone's committed-but-unpushed commit flags like
// uncommitted changes.
func TestRepoClonesUnpushedLocalCommitWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	dir := reposClone(t, ws, "cl-unpushed", "feature/w198")
	reposDirty(t, dir)
	reposGit(t, dir, "add", ".")
	reposGit(t, dir, "commit", "-q", "-m", "local only")
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-unpushed") || !strings.Contains(detail, "remote-tracking ref") {
		t.Fatalf("the unpushed-commit warning does not name the clone and the remote facts: %q", detail)
	}
}

// Coverage: a dirty delivery-branch clone whose WI is queued (in flight)
// covers it — the row warns for nothing over it; the anchoring dirty
// release clone in the same workspace still warns.
func TestRepoClonesCoveredInFlightSilent(t *testing.T) {
	srv := (&reposAihub{
		known: map[string][]api.ProjectRepo{"e2e": {{Name: "pi-web", URL: "file:///n/pi-web.git"}}},
		items: map[string]string{"e2e#9": api.StatusQueued},
	}).server(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-covered", "polyforge/e2e-9"))
	reposDirty(t, reposClone(t, ws, "cl-anchor", "release/v0.0.27"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if strings.Contains(detail, "cl-covered") {
		t.Fatalf("a queued WI's delivery face covered the clone (reported anyway?): %q", detail)
	}
	if !strings.Contains(detail, "cl-anchor") {
		t.Fatalf("the anchor clone went unreported: %q", detail)
	}
}

// The delivery face of a terminal WI never covers; a detached HEAD is
// decidable uncovered. Both warn, remediation named.
func TestRepoClonesTerminalAndDetachedWarn(t *testing.T) {
	// The registry shape with one WI pinned to its terminal status (the
	// default fixture's delivery faces answer 404 — see the 404 test).
	srv := (&reposAihub{
		known: map[string][]api.ProjectRepo{"e2e": {{Name: "pi-web", URL: "file:///n/pi-web.git"}}},
		items: map[string]string{"e2e#9": api.StatusCanceled},
	}).server(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-canceled", "polyforge/e2e-9"))
	det := reposClone(t, ws, "cl-detached", "main")
	reposGit(t, det, "checkout", "-q", "--detach")
	reposDirty(t, det)
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-canceled") || !strings.Contains(detail, api.StatusCanceled) {
		t.Fatalf("the canceled-WI warning does not name the clone and the terminal WI: %q", detail)
	}
	if !strings.Contains(detail, "cl-detached") {
		t.Fatalf("a detached-HEAD dirty clone went unreported: %q", detail)
	}
}

// An unregistered dirty clone warns with the assignment face as fact.
func TestRepoClonesUnregisteredWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-stray", "main"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-stray") || !strings.Contains(detail, "not registered in any mapped project") {
		t.Fatalf("the unregistered clone's warning lacks the assignment fact: %q", detail)
	}
	if strings.Contains(detail, "--project e2e --repo cl-stray") {
		t.Fatalf("the unregistered clone was assigned a project anyway: %q", detail)
	}
}

// Clean clones on any branch and a plain (non-git) .repo entry never
// warn; the anchor dirty clone in the same workspace still warns.
func TestRepoClonesCleanAndPlainSilent(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposClone(t, ws, "cl-clean", "release/v0.0.27")
	plain := filepath.Join(ws, ".repo", "cl-plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	reposDirty(t, reposClone(t, ws, "cl-anchor", "main"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if strings.Contains(detail, "cl-clean") || strings.Contains(detail, "cl-plain") {
		t.Fatalf("a clean clone or a plain entry was warned over: %q", detail)
	}
	if !strings.Contains(detail, "cl-anchor") {
		t.Fatalf("the anchor clone went unreported: %q", detail)
	}
}

// A linked worktree living in a notable wrapper dir is scanned through
// its owning clone's worktree list; the wrapper itself is never a unit.
func TestRepoClonesLinkedWorktreeScanned(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	parent := reposClone(t, ws, "cl-parent", "main")
	nested := filepath.Join(ws, ".repo", "cl-wrap", "cl-wtree")
	reposGit(t, parent, "worktree", "add", "-q", "-b", "feature/wt", nested)
	if err := os.WriteFile(filepath.Join(nested, "scratch-edit.txt"), []byte("untracked hand work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-wtree") || !strings.Contains(detail, "cl-parent is not registered") {
		t.Fatalf("the linked worktree's warning does not name it under its owning clone: %q", detail)
	}
}

// Warn-only: the warning never moves the exit code and `doctor
// --harness` never runs the check (it does not mention the clone).
func TestRepoClonesWarnOnlyNotInHarness(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-exit", "release/v0.0.27"))

	// The healthy machine's fixture (TestDoctorHealthyMachineAllPassExit0's
	// shape) with the daemon socket: a warn must leave ok true and the
	// exit code 0.
	sock := liveDaemonSocket(t, "m-w")
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"" + srv + "\"\napi_key = \"k\"\ndaemon_socket = \"" + sock + "\"\n" +
		"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	env["HOME"] = displayHome(t, `{"statusLine":{"type":"command","command":"pf watch --statusline --harness-session -"}}`,
		`{"packages":["~/.local/share/polyforge/release/current"]}`)
	layoutRelease(t, env["HOME"])
	layoutClaudeLink(t, env["HOME"])
	env["PATH"] = fakeTmuxPath(t, 0)
	code, _, doc := doctorStatus(t, env, "--json", "doctor")
	if code != 0 || !doc.OK {
		t.Fatalf("pf doctor: exit %d ok=%t with only a clone warning, want 0/true (%+v)", code, doc.OK, doc.Checks)
	}
	if _, detail := reposRow(t, doc, "warn"); !strings.Contains(detail, "cl-exit") {
		t.Fatalf("the only-warning report does not name the clone: %q", detail)
	}

	// harness run: no repos clones row and no clone name.
	code, _, hdoc := doctorStatus(t, env, "--json", "doctor", "--harness")
	for _, c := range hdoc.Checks {
		if c.Name == docChRepoClones || strings.Contains(c.Detail, "cl-exit") {
			t.Fatalf("doctor --harness ran or mentioned the clone check: %q (%+v)", c.Detail, code)
		}
	}
}

// aihub unreachable: the delivery-branch dirty clone's warning needs the
// WI state that could not be read — it becomes a skip note naming the
// clone and the reason (never an "uncovered" warning); a release-branch
// dirty clone in a sibling workspace still warns (locally decidable).
func TestRepoClonesUnreachableSplit(t *testing.T) {
	srv := reposAihubDefault(t)
	wsA := t.TempDir()
	reposDirty(t, reposClone(t, wsA, "cl-delivery", "polyforge/e2e-7"))
	doc := reposDoctor(t, srv, "http://127.0.0.1:59997", wsA)
	_, detail := reposRow(t, doc, "skip")
	if !strings.Contains(detail, "cl-delivery") {
		t.Fatalf("the delivery face's skip note does not name the clone: %q", detail)
	}

	wsB := t.TempDir()
	reposDirty(t, reposClone(t, wsB, "cl-local", "release/v0.0.27"))
	doc = reposDoctor(t, srv, "http://127.0.0.1:59997", wsB)
	_, detail = reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-local") {
		t.Fatalf("the locally decidable dirty release clone went unreported: %q", detail)
	}
	if strings.Contains(detail, "not registered in any mapped project") {
		t.Fatalf("sans answer the assignment fact is wrongly stated as named: %q", detail)
	}
}

// A healthy run without .repo at all (every acceptance machine's baseline
// shape) answers pass — the row needs nothing.
func TestRepoClonesPassNoUnits(t *testing.T) {
	ws := t.TempDir()
	// no .repo dir in ws; the config maps it.
	srv := reposAihubDefault(t)
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "pass")
	if !strings.Contains(detail, "0") {
		t.Fatalf("non-units .repo pass detail does not say the count: %q", detail)
	}
}

// A terminal-WI 404 answer (in these fixtures, any delivery face not
// pinned to a status) is a definitive uncovered fact — a warning names it.
func TestRepoClonesDelivery404Warns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-404", "polyforge/e2e-9"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "warn")
	if !strings.Contains(detail, "cl-404") || !strings.Contains(detail, "404") {
		t.Fatalf("the 404 delivery face's warning does not name the clone and the answer: %q", detail)
	}
}

// The WI face is one transport failure short: delivery face dirty clone
// → skip note; the sibling locally decidable shape still warns.
func TestRepoClonesWiReadFailSkip(t *testing.T) {
	f := &reposAihub{
		known:   map[string][]api.ProjectRepo{"e2e": {{Name: "pi-web", URL: "file:///n/pi-web.git"}}},
		items:   map[string]string{"e2e#9": api.StatusQueued},
		slugged: map[string]bool{"e2e#9": true},
	}
	srv := f.server(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "cl-dial", "polyforge/e2e-9"))
	doc := reposDoctor(t, srv, "", ws)
	_, detail := reposRow(t, doc, "skip")
	if !strings.Contains(detail, "cl-dial") {
		t.Fatalf("the WI-read failure's skip note does not name the clone: %q", detail)
	}
}
