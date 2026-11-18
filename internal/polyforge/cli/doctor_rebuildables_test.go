package cli

// doctor_rebuildables_test.go: the "rebuildable artifacts" row — the
// default run reports the declared rebuildable build products of
// terminal work items' capsules and deletes nothing;
// --prune-rebuildables removes exactly the declared directories that
// passed the probe (all-ignored content, no tracked or
// untracked-not-ignored file, no nested repository, no symlinked
// component), reading the declaration from the mapped workspace's
// .repo view's committed default branch (Q3=B, the owner's ruling) and
// recording the commit it read; a capsule of a work item still in
// flight, one a daemon job holds, and any repository whose declaration
// cannot be proven are kept and named; a repository that declares
// nothing changes nothing (AC3); artifacts/, capsule.json, the git
// directories, the local commits and the git status never move — a
// repository with unpushed commits loses its ignored build products and
// nothing else (polyforge#127, u4 — AC1, AC2, AC3, AC4). Expected
// values are the fixture's own bytes and the fixture's own git (the
// clone's status, HEAD and unpushed count read before the run), never
// the implementation's output.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rgcGit runs one git command in dir and fails the test on its error.
func rgcGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "HOME="+t.TempDir())
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, errb.String())
	}
	return out.String()
}

// rgcSrc answers a fresh source repository whose default branch (main)
// carries the capsule-gc declaration conf ("" = a repository that
// declares nothing) beside a .gitignore that ignores cache/ and a
// tracked README — the repository a workspace's .repo view and a
// capsule's task clone are both cloned from.
func rgcSrc(t *testing.T, conf string) string {
	t.Helper()
	src := t.TempDir()
	rgcGit(t, src, "-c", "init.defaultBranch=main", "init", "-q")
	add := []string{"add", ".gitignore", "README.md"}
	if conf != "" {
		if err := os.MkdirAll(filepath.Join(src, ".polyforge"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, ".polyforge", "capsule-gc.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		add = append(add, ".polyforge/capsule-gc.conf")
	}
	if err := os.WriteFile(filepath.Join(src, ".gitignore"), []byte("cache/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rgcGit(t, src, add...)
	rgcGit(t, src, "-c", "user.email=rgc@example.com", "-c", "user.name=rgc", "commit", "-q", "-m", "the declaration")
	return src
}

// rgcView clones src as the workspace's .repo/<name> view — the
// committed default branch Q3=B reads the declaration from.
func rgcView(t *testing.T, f gitDirsFixture, src, name string) string {
	t.Helper()
	ws := filepath.Dir(filepath.Dir(f.retiredCap))
	if err := os.MkdirAll(filepath.Join(ws, ".repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	view := filepath.Join(ws, ".repo", name)
	rgcGit(t, ws, "clone", "--quiet", src, view)
	return view
}

// rgcCapsule lays out one live capsule of the work item wi with a real
// task clone of src under repos/<repo> and the capsule's gitdir
// pointer, and answers the capsule's path. The ignored cache/ content
// the caller writes is what the row should count or remove.
func rgcCapsule(t *testing.T, f gitDirsFixture, name, wi, src, repo string) string {
	t.Helper()
	dir := liveCapsule(t, f, name, wi, false)
	touch(t, filepath.Join(dir, ".git"), time.Now().Add(-30*24*time.Hour))
	clone := filepath.Join(dir, "repos", repo)
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	rgcGit(t, filepath.Dir(clone), "clone", "--quiet", src, clone)
	return dir
}

// rgcWrite lays out one regular file of n bytes.
func rgcWrite(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDoctorRebuildablesReportOnlyByDefault: the default run reports
// the terminal work items' declared build products per workspace with
// a pointer to the switch and deletes nothing; a capsule of a work
// item still in flight and one a daemon job holds are kept and named
// with why; the row is registered right after capsule retention and
// carried in the doctor verb's enumeration (polyforge#127, u4 — AC1's
// default half, AC4).
func TestDoctorRebuildablesReportOnlyByDefault(t *testing.T) {
	f := newGitDirsFixture(t, true)
	src := rgcSrc(t, "rebuild_dir=cache\n")
	rgcView(t, f, src, "demo")

	wrapped := rgcCapsule(t, f, "demo-3", "wi_done", src, "demo")
	rgcWrite(t, filepath.Join(wrapped, "repos", "demo", "cache", "app.bin"), 100)
	running := rgcCapsule(t, f, "demo-4", "wi_active", src, "demo")
	rgcWrite(t, filepath.Join(running, "repos", "demo", "cache", "app.bin"), 50)
	held := rgcCapsule(t, f, "demo-5", "wi_job", src, "demo")
	rgcWrite(t, filepath.Join(held, "repos", "demo", "cache", "app.bin"), 50)

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChRebuildables)
	if st != "warn" {
		t.Fatalf("status = %s (%s), want warn with the switch's pointer", st, detail)
	}
	for _, want := range []string{"100 B", "--prune-rebuildables", "nothing was deleted", "the work item is running", "held by a daemon job"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	assertExist(t, true,
		filepath.Join(wrapped, "repos", "demo", "cache", "app.bin"),
		filepath.Join(running, "repos", "demo", "cache", "app.bin"),
		filepath.Join(held, "repos", "demo", "cache", "app.bin"))

	idx, ret := -1, -1
	for i, d := range doctorChecks {
		switch d.name {
		case docChRebuildables:
			idx = i
		case docChCapsuleRetention:
			ret = i
		}
	}
	if idx != ret+1 {
		t.Fatalf("%q must be registered in doctorChecks right after %q (at %d, retention at %d)", docChRebuildables, docChCapsuleRetention, idx, ret)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "skills", "polyforge", "verbs", "doctor.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "| `"+docChRebuildables+"` |") {
		t.Errorf("skills/polyforge/verbs/doctor.md has no `%s` row", docChRebuildables)
	}
}

// TestDoctorRebuildablesPruneRemovesDeclaredDirs: the switch removes
// exactly the declared directories that passed the probe, listing each
// removed path with the declaration's ref and commit; artifacts/,
// capsule.json, the gitdir pointer, the tracked work and the clone's
// git status and HEAD are what they were; a repository without a .repo
// view is kept whole and named, a repository that declares nothing
// changes nothing; the second run finds nothing and is no error
// (polyforge#127, u4 — AC1, AC3).
func TestDoctorRebuildablesPruneRemovesDeclaredDirs(t *testing.T) {
	f := newGitDirsFixture(t, true)
	src := rgcSrc(t, "rebuild_dir=cache\n")
	view := rgcView(t, f, src, "demo")
	blank := rgcSrc(t, "")
	rgcView(t, f, blank, "declaredless")

	wrapped := rgcCapsule(t, f, "demo-3", "wi_done", src, "demo")
	rgcWrite(t, filepath.Join(wrapped, "repos", "demo", "cache", "app.bin"), 100)
	clone := filepath.Join(wrapped, "repos", "demo")
	status := rgcGit(t, clone, "status", "--porcelain")
	head := strings.TrimSpace(rgcGit(t, clone, "rev-parse", "HEAD"))
	sha := strings.TrimSpace(rgcGit(t, view, "rev-parse", "origin/main"))

	// a repository whose .repo view does not exist: the declaration is
	// unproven, the capsule is kept whole and named
	noview := rgcCapsule(t, f, "demo-7", "wi_done", blank, "other")
	rgcWrite(t, filepath.Join(noview, "repos", "other", "cache", "app.bin"), 100)
	// a repository that declares nothing: today's behavior, unchanged
	undeclared := rgcCapsule(t, f, "demo-8", "wi_done", blank, "declaredless")
	rgcWrite(t, filepath.Join(undeclared, "repos", "declaredless", "cache", "app.bin"), 100)

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-rebuildables")
	st, detail := doctorFind(t, doc, docChRebuildables)
	if st != "pass" {
		t.Fatalf("status = %s (%s), want pass with the removal done", st, detail)
	}
	for _, want := range []string{"removed 1: ", filepath.Join(wrapped, "repos", "demo", "cache"), "declared at origin/main " + sha} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	if !strings.Contains(detail, "the declaration could not be read") {
		t.Fatalf("detail must name the capsule whose .repo view is missing: %s", detail)
	}
	assertExist(t, false, filepath.Join(wrapped, "repos", "demo", "cache"))
	assertExist(t, true,
		filepath.Join(clone, "README.md"),
		filepath.Join(wrapped, "artifacts", "plan.md"),
		filepath.Join(wrapped, "capsule.json"),
		filepath.Join(wrapped, ".git"),
		filepath.Join(noview, "repos", "other", "cache", "app.bin"),
		filepath.Join(undeclared, "repos", "declaredless", "cache", "app.bin"))
	if after := rgcGit(t, clone, "status", "--porcelain"); after != status {
		t.Fatalf("the clone's git status moved: was %q, now %q", status, after)
	}
	if after := strings.TrimSpace(rgcGit(t, clone, "rev-parse", "HEAD")); after != head {
		t.Fatalf("the clone's HEAD moved: was %q, now %q", head, after)
	}

	// The second run finds nothing and is no error.
	_, _, doc = doctorStatus(t, f.env, "--json", "doctor", "--prune-rebuildables")
	st, detail = doctorFind(t, doc, docChRebuildables)
	if st != "pass" || !strings.Contains(detail, "nothing to remove") {
		t.Fatalf("the second run = %s (%s), want pass with nothing to remove", st, detail)
	}
	assertExist(t, false, filepath.Join(wrapped, "repos", "demo", "cache"))
}

// TestDoctorRebuildablesProtectsTheLive: the switch never touches a
// capsule of a work item still in flight or one a daemon job holds; a
// repository with unpushed commits loses its ignored build products
// and nothing else — its status, HEAD and unpushed commits are
// byte-for-byte what they were; and while the daemon does not answer
// nothing is removed at all (polyforge#127, u4 — AC2).
func TestDoctorRebuildablesProtectsTheLive(t *testing.T) {
	f := newGitDirsFixture(t, true)
	src := rgcSrc(t, "rebuild_dir=cache\n")
	rgcView(t, f, src, "demo")

	running := rgcCapsule(t, f, "demo-4", "wi_active", src, "demo")
	rgcWrite(t, filepath.Join(running, "repos", "demo", "cache", "app.bin"), 50)
	held := rgcCapsule(t, f, "demo-5", "wi_job", src, "demo")
	rgcWrite(t, filepath.Join(held, "repos", "demo", "cache", "app.bin"), 50)

	// a terminal work item's repository carrying local, unpushed work
	// beside the declared build products
	unpushed := rgcCapsule(t, f, "demo-6", "wi_fresh", src, "demo")
	clone := filepath.Join(unpushed, "repos", "demo")
	rgcWrite(t, filepath.Join(clone, "work.txt"), 10)
	rgcGit(t, clone, "add", "work.txt")
	rgcGit(t, clone, "-c", "user.email=rgc@example.com", "-c", "user.name=rgc", "commit", "-q", "-m", "local work")
	rgcWrite(t, filepath.Join(clone, "cache", "app.bin"), 100)
	status := rgcGit(t, clone, "status", "--porcelain")
	head := strings.TrimSpace(rgcGit(t, clone, "rev-parse", "HEAD"))
	unpushedCommits := rgcGit(t, clone, "rev-list", "--all", "--not", "--remotes")

	_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-rebuildables")
	st, detail := doctorFind(t, doc, docChRebuildables)
	if st != "pass" {
		t.Fatalf("status = %s (%s), want pass with the protections named", st, detail)
	}
	assertExist(t, true,
		filepath.Join(running, "repos", "demo", "cache", "app.bin"),
		filepath.Join(held, "repos", "demo", "cache", "app.bin"),
		filepath.Join(clone, "work.txt"))
	assertExist(t, false, filepath.Join(clone, "cache"))
	for _, want := range []string{"the work item is running", "held by a daemon job"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	if after := rgcGit(t, clone, "status", "--porcelain"); after != status {
		t.Fatalf("the unpushed repository's status moved: was %q, now %q", status, after)
	}
	if after := strings.TrimSpace(rgcGit(t, clone, "rev-parse", "HEAD")); after != head {
		t.Fatalf("the unpushed repository's HEAD moved: was %q, now %q", head, after)
	}
	if after := rgcGit(t, clone, "rev-list", "--all", "--not", "--remotes"); after != unpushedCommits {
		t.Fatalf("the unpushed commits moved: was %q, now %q", unpushedCommits, after)
	}

	// While the daemon does not answer, its live jobs are unknown: a
	// terminal work item's capsule is kept whole, whatever the switch.
	t.Run("nothing moves while the daemon is down", func(t *testing.T) {
		f := newGitDirsFixture(t, false)
		src := rgcSrc(t, "rebuild_dir=cache\n")
		rgcView(t, f, src, "demo")
		wrapped := rgcCapsule(t, f, "demo-3", "wi_done", src, "demo")
		rgcWrite(t, filepath.Join(wrapped, "repos", "demo", "cache", "app.bin"), 100)

		_, _, doc := doctorStatus(t, f.env, "--json", "doctor", "--prune-rebuildables")
		st, detail := doctorFind(t, doc, docChRebuildables)
		if st == "fail" {
			t.Fatalf("the row fails: %s", detail)
		}
		if !strings.Contains(detail, "aihub state could not be read") {
			t.Fatalf("the daemon-down report must name the unreadable state: %s", detail)
		}
		assertExist(t, true, filepath.Join(wrapped, "repos", "demo", "cache", "app.bin"))
	})
}
