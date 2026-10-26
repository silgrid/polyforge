package gitops

// gitops_gitlink_test.go: the LOCAL gitlink precheck (V8 repair) — an
// embedded git repository (nested .git) staged by `git add -A` must be
// refused with the typed GIT-412 LOCAL_GITLINK_DETECTED error BEFORE any
// commit is created, so the poison can never enter local history (the
// pilot's #48 chain was a gitlink that DID get committed: every later
// push of the standing HEAD was rejected by aihub's pre-receive 185
// times). The refusal must name the polluted path, leave no commit, and
// the documented cleanup must genuinely unblock the next CommitPush.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// mkEmbedded lays a real nested git repository with one commit under
// the capsule (the #48 poison shape: a directory whose .git makes
// `git add -A` stage a mode-160000 gitlink entry).
func mkEmbedded(t *testing.T, capsuleDir, name string) string {
	t.Helper()
	dir := filepath.Join(capsuleDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"git", "-C", dir, "init", "-q", "-b", "main", "."},
		{"git", "-C", dir, "config", "user.email", "embedded@invalid"},
		{"git", "-C", dir, "config", "user.name", "embedded"},
	} {
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "inner.txt"), []byte("inner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run("git", "-C", dir, "add", "-A"); err != nil {
		t.Fatalf("git add (embedded): %v: %s", err, out)
	}
	if out, err := run("git", "-C", dir, "commit", "-q", "-m", "embedded history"); err != nil {
		t.Fatalf("git commit (embedded): %v: %s", err, out)
	}
	return dir
}

func TestCommitPushRefusesLocalGitlinkNoCommit(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_gitlink")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	// A healthy chain first: one ordinary commit must land.
	writeDirty(t, o, "clean.txt", "one\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: execution v1"); err != nil || !pushed {
		t.Fatalf("healthy CommitPush: pushed=%v err=%v", pushed, err)
	}

	// The poison: an embedded repo plus more ordinary work in the tree.
	mkEmbedded(t, o.CapsuleDir, "poison")
	writeDirty(t, o, "more.txt", "two\n")
	sha, pushed, _, err := CommitPush(context.Background(), o, "observe: execution v2")
	if err == nil {
		t.Fatal("CommitPush over an embedded repository must be refused")
	}
	if pushed {
		t.Fatal("a refused CommitPush must not report a push")
	}
	if sha != "" {
		t.Fatalf("a refused CommitPush must not answer a commit SHA (got %q)", sha)
	}
	if !IsLocalGitlink(err) {
		t.Fatalf("error must be the typed local gitlink refusal, got: %v", err)
	}
	var ae *api.Error
	if !asAPIError(err, &ae) || ae.Code != api.ErrGitGitlink {
		t.Fatalf("error must carry code %s, got: %+v", api.ErrGitGitlink, ae)
	}
	if !strings.Contains(err.Error(), "LOCAL_GITLINK_DETECTED") ||
		!strings.Contains(err.Error(), "poison") {
		t.Fatalf("refusal must name the sentinel and the polluted path: %v", err)
	}
	if !strings.Contains(err.Error(), "git rm --cached") {
		t.Fatalf("refusal must carry the cleanup recipe: %v", err)
	}

	// THE key assertion: no commit was created — the poison never
	// entered local history (rev-list count still 1, and the bare repo
	// still holds exactly the first commit).
	out, rerr := run("git", "--git-dir", o.GitDir, "rev-list", "--count", "HEAD")
	if rerr != nil {
		t.Fatalf("rev-list: %v: %s", rerr, out)
	}
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("local history must hold exactly the pre-poison commit, got %s commits", strings.TrimSpace(out))
	}
	if n := len(bare.pushesSeen(t)); n != 1 {
		t.Fatalf("bare repo must have seen exactly the healthy push, got %d", n)
	}
	// The staged index must not linger poisoned (best-effort reset ran).
	out, _ = run("git", "--git-dir", o.GitDir, "ls-files", "--stage")
	if strings.Contains(out, "160000") {
		t.Fatalf("index must not retain the gitlink after the refusal:\n%s", out)
	}

	// The documented cleanup genuinely unblocks the chain: delete the
	// nested .git, and the next CommitPush commits the very same tree
	// as ordinary files.
	if err := os.RemoveAll(filepath.Join(o.CapsuleDir, "poison", ".git")); err != nil {
		t.Fatal(err)
	}
	sha2, pushed2, _, err2 := CommitPush(context.Background(), o, "observe: execution v2 (cleaned)")
	if err2 != nil || !pushed2 {
		t.Fatalf("CommitPush after cleanup: pushed=%v err=%v", pushed2, err2)
	}
	if sha2 == "" {
		t.Fatal("cleaned CommitPush must answer its SHA")
	}
	// The landing commit's tree must contain the former poison as PLAIN
	// files, no gitlink.
	out, _ = run("git", "--git-dir", o.GitDir, "ls-tree", "-r", sha2)
	if strings.Contains(out, "160000") {
		t.Fatalf("cleaned commit must not contain a gitlink:\n%s", out)
	}
	if !strings.Contains(out, "poison/inner.txt") {
		t.Fatalf("cleaned commit must carry the former embedded files as plain blobs:\n%s", out)
	}
}

func TestCommitPushRefusesUnbornEmbeddedRepo(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_gitlink_unborn")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	// An embedded repo with NO commit checked out fails at `git add -A`
	// itself — the same poison shape one step earlier. It must surface
	// as the same typed refusal, not a bare git error.
	dir := filepath.Join(o.CapsuleDir, "poison2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run("git", "init", "-q", "-b", "main", dir); err != nil {
		t.Fatalf("git init (embedded): %v: %s", err, out)
	}
	writeDirty(t, o, "clean.txt", "one\n")
	_, pushed, _, err := CommitPush(context.Background(), o, "observe: execution v1")
	if err == nil || pushed {
		t.Fatalf("CommitPush over an unborn embedded repo must be refused (err=%v pushed=%v)", err, pushed)
	}
	if !IsLocalGitlink(err) {
		t.Fatalf("error must be the typed local gitlink refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "`git rm --cached poison2`") {
		t.Fatalf("the refusal must name the embedded repository in its cleanup recipe: %v", err)
	}
	// Still no commit in local history (unborn HEAD: rev-parse fails,
	// which is itself the proof nothing was committed).
	if _, rerr := run("git", "--git-dir", o.GitDir, "rev-parse", "--verify", "-q", "HEAD"); rerr == nil {
		t.Fatal("no commit may exist after the refusal (HEAD must be unborn)")
	}
	if n := len(bare.pushesSeen(t)); n != 0 {
		t.Fatalf("bare repo must have seen no push, got %d", n)
	}

	// Cleanup unblocks here too.
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, pushed2, _, err2 := CommitPush(context.Background(), o, "observe: execution v1 (cleaned)"); err2 != nil || !pushed2 {
		t.Fatalf("CommitPush after cleanup: pushed=%v err=%v", pushed2, err2)
	}
}

func TestIsLocalGitlinkRejectsLookalikes(t *testing.T) {
	// A server-side pre-receive gitlink rejection arrives as an ordinary
	// push error (code GIT-500, detail quoting the hook's stderr) — it
	// must NOT classify as the local precheck refusal.
	hook := &api.Error{Code: api.ErrGitInternal, Detail: "gitlink (mode 160000) in commit abc is not allowed"}
	if IsLocalGitlink(hook) {
		t.Fatal("a server-side hook rejection must not classify as local gitlink")
	}
	plain := &api.Error{Code: api.ErrGitGitlink, Detail: "some other gitlink context"}
	if IsLocalGitlink(plain) {
		t.Fatal("only the LOCAL_GITLINK_DETECTED sentinel classifies as local")
	}
	if IsLocalGitlink(nil) {
		t.Fatal("nil never classifies")
	}
}

func asAPIError(err error, target **api.Error) bool {
	ae, ok := err.(*api.Error)
	if ok {
		*target = ae
	}
	return ok
}

// repo review 2026-10-16 B-25/V-13: a gitlink under repos/ means the
// capsule .gitignore lost its line; the refusal must point there and
// never tell anyone to delete a task repo's .git (its only history).
func TestGitlinkRemedyNeverDeletesATaskRepo(t *testing.T) {
	remedy := func(path string) string { return refuseGitlink(nil, t.TempDir(), "x", path).Error() }
	r := remedy("repos/app")
	if !strings.Contains(r, "/repos/") || !strings.Contains(r, ".gitignore") || !strings.Contains(r, "Do NOT delete") {
		t.Fatalf("task-repo remedy = %q", r)
	}
	if strings.Contains(r, "git rm --cached") || strings.Contains(r, "delete its .git") {
		t.Fatalf("task-repo remedy must not offer the destructive recipe: %q", r)
	}
	if g := remedy("poison"); !strings.Contains(g, "git rm --cached") || !strings.Contains(g, "disposable") {
		t.Fatalf("residue remedy = %q", g)
	}
}
