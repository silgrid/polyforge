package gitops

// gitops_carrier_alternates_test.go: a task repo that a step agent cloned
// under repos/ from a workspace clone (`git clone --shared`, polyforge#264)
// keeps its ancestor objects in a borrowed store: its object store is
// listed in .git/objects/info/alternates and it owns no pack of its own.
// That is a RECOVERABLE carrier state — every object the carrier needs can
// be pulled into the task repo — so the carrier lane must self-contain the
// repo (repack, drop the alternates reference) and push, not refuse the
// whole repo as a terminal failure. Only a borrowed store that cannot be
// pulled in stays refused, with the original "borrows objects from another
// store" reason.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// mkBorrowedTaskRepo lays the shape polyforge#264 reported: a workspace
// clone next to the capsule (the object source a step agent borrows from)
// and a task clone under repos/<name> made from it with --shared, so the
// task repo owns no objects of its own and reads every ancestor object
// through .git/objects/info/alternates. The agent's unpushed commit on top
// adds its objects locally. Answers the task repo dir, its work head SHA
// and the workspace clone it borrows from.
func mkBorrowedTaskRepo(t *testing.T, o Opts, base, name string) (repoDir, head, workspace string) {
	t.Helper()
	origin := filepath.Join(base, "origin", name)
	if out, err := run("git", "init", "-q", "-b", "main", origin); err != nil {
		t.Fatalf("task origin init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", origin, "add", "-A")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "init")
	workspace = filepath.Join(base, "workspace", name)
	mustRun(t, "git", "clone", "-q", origin, workspace)
	repoDir = filepath.Join(o.CapsuleDir, capsule.DirRepos, name)
	mustRun(t, "git", "clone", "-q", "--shared", workspace, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "repo work")
	// The borrow-only precondition: an alternates file and no local pack.
	if _, err := os.Stat(filepath.Join(repoDir, ".git", "objects", "info", "alternates")); err != nil {
		t.Fatalf("the task repo is not borrowing objects (no alternates): %v", err)
	}
	if packs, _ := filepath.Glob(filepath.Join(repoDir, ".git", "objects", "pack", "*.pack")); len(packs) != 0 {
		t.Fatalf("a borrow-only clone must not have local packs, found %v", packs)
	}
	return repoDir, strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD")), workspace
}

// A borrowed task repo is healed and carried: the carrier lands on the hub
// at the repo's work head, the alternates reference is gone afterwards, and
// the repo stays readable once the borrowed-from workspace clone is deleted.
func TestCarrierSelfHealsBorrowedTaskRepo(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_alt1")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	repoDir, head, workspace := mkBorrowedTaskRepo(t, o, base, "demo-src")

	var kinds []string
	var bodies []map[string]any
	o.EventSink = func(kind string, b map[string]any) { kinds = append(kinds, kind); bodies = append(bodies, b) }
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != head {
		t.Fatalf("carrier on the hub = %q (%v), want the borrowed repo's work head %q (events=%v bodies=%v)",
			got, ok, head, kinds, bodies)
	}
	if len(kinds) != 0 {
		t.Fatalf("a self-contained carrier must raise nothing: %v %v", kinds, bodies)
	}
	if _, err := os.Stat(filepath.Join(repoDir, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("the task repo still borrows objects after the carrier push (alternates stat err = %v)", err)
	}
	// Self-contained means the borrowed-from store is not needed any more.
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if out, err := run("git", "-C", repoDir, "fsck", "--connectivity-only"); err != nil {
		t.Fatalf("the healed task repo still depends on the borrowed store: %v: %s", err, out)
	}
}

// The refusal stays for a borrowed store that cannot be pulled in (its
// alternate objects are gone): the carrier does not land and the original
// "borrows objects from another store" reason is reported.
func TestCarrierRefusesBorrowedStoreItCannotSelfContain(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_alt2")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	_, _, workspace := mkBorrowedTaskRepo(t, o, base, "demo-src")
	// The borrowed store disappears: the repo cannot become self-contained.
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}

	var kinds []string
	var bodies []map[string]any
	o.EventSink = func(kind string, b map[string]any) { kinds = append(kinds, kind); bodies = append(bodies, b) }
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("a refused carrier must not fail the chain publish: %v", err)
	}
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); ok {
		t.Fatal("a carrier whose objects cannot be made local must not land")
	}
	if len(kinds) != 1 || kinds[0] != EventCarrierPushFailed {
		t.Fatalf("events = %v, want one %s", kinds, EventCarrierPushFailed)
	}
	if e, _ := bodies[0]["error"].(string); !strings.Contains(e, "borrows objects from another store") {
		t.Fatalf("event body = %v, want the original borrowed-store refusal", bodies[0])
	}
}
