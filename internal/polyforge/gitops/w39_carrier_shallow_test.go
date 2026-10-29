package gitops

// fix: the carrier push is shallow. The shallow roots are the
// WI line's upstream boundary (`rev-list --boundary <head> --not
// --exclude=origin/polyforge/* --remotes=origin`), never the WI's first
// own commit and never just the hub's old tip: the WI's own pushed feature
// branch is excluded, a merged upstream line contributes each merge
// parent's boundary, and so an upstream-only asset behind the hook cannot
// block a first push after a merge or the update that follows one. When
// the walk answers nothing the cut falls back to the hub tip, the recorded
// baseline or the head, and the carried history then includes the upstream
// commits between the cut and the head. The restored clone must stay
// NON-shallow and keep origin/<default> as an ancestor of the restored
// head — deliver's ancestry check, default-branch merges and review's
// merge-base diff depend on it.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aihubgit "github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w39ShallowAssetBlob answers the blob of the upstream-only asset the
// origin's history holds (added, then deleted by the next upstream commit).
func w39ShallowAssetBlob(t *testing.T, origin, path string) string {
	t.Helper()
	return strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD^:"+path))
}

// w39AssertRestoredAncestry is the resume oracle: the carrier restore reads
// the head back into a full clone that is still non-shallow and keeps
// origin/main as an ancestor of the restored head.
func w39AssertRestoredAncestry(t *testing.T, clone, restored string) {
	t.Helper()
	if got := strings.TrimSpace(mustRun(t, "git", "-C", clone, "rev-parse", "--is-shallow-repository")); got != "false" {
		t.Fatalf("the restored clone is shallow (is-shallow-repository=%s): resume loses its ancestry to origin", got)
	}
	if out, err := run("git", "-C", clone, "merge-base", "--is-ancestor", "origin/main", restored); err != nil {
		t.Fatalf("origin/main is not an ancestor of the restored head %s (%v: %s): deliver and review cannot merge-base against it", restored, err, strings.TrimSpace(out))
	}
}

// w39ShallowRoots answers the hub's carrier shallow roots.
func w39ShallowRoots(t *testing.T, hub string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hub, "shallow"))
	if err != nil {
		t.Fatalf("read the hub shallow file: %v", err)
	}
	return strings.Fields(string(raw))
}

// TestCarrierShallowKeepsOwnCommitsDropsUpstream publishes a task repo whose
// upstream history holds an asset that a later upstream commit deletes (the
// skypilot shape) and whose WI line has two commits. The carrier must name
// the WI head, carry both WI commits, root them at the upstream boundary
// (not at a WI commit), never hold the upstream-only asset blob, and
// round-trip into a non-shallow clone that still descends from origin/main.
func TestCarrierShallowKeepsOwnCommitsDropsUpstream(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_shallow_own")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)

	origin := filepath.Join(base, "origin", "app")
	mustRun(t, "git", "init", "-q", "-b", "main", origin)
	if err := os.WriteFile(filepath.Join(origin, "asset.bin"), bytes.Repeat([]byte{7}, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", origin, "add", "-A")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "upstream: add the asset")
	assetBlob := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD:asset.bin"))
	mustRun(t, "git", "-C", origin, "rm", "-q", "asset.bin")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "upstream: delete the asset")
	upstreamHead := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD"))

	// The claim-time clone plus the WI's two own commits.
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "app")
	mustRun(t, "git", "clone", "-q", origin, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work1.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "WI: work one")
	if err := os.WriteFile(filepath.Join(repoDir, "work2.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "WI: work two")
	workHead := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))

	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("app"))
	if !ok || carried != workHead {
		t.Fatalf("carrier = %q (%v), want the WI head %q", carried, ok, workHead)
	}
	// The boundary commit is the upstream head (the claim base), which sits
	// on the hub as a shallow root; both WI commits sit above it.
	if n := strings.TrimSpace(mustRun(t, "git", "--git-dir", o.RemoteURL, "rev-list", "--count", workHead)); n != "3" {
		t.Fatalf("the carrier reaches %s commits, want the 2 WI commits + the boundary commit", n)
	}
	roots := w39ShallowRoots(t, o.RemoteURL)
	if len(roots) != 1 || roots[0] != upstreamHead {
		t.Fatalf("carrier shallow roots = %v, want [the upstream boundary %s]", roots, upstreamHead)
	}
	// The upstream-only asset blob is not in the carrier's history, even
	// though the boundary commit itself is.
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", assetBlob); err == nil {
		t.Fatalf("the hub holds the upstream-only asset blob %s: the carrier still carries upstream history (%s)", assetBlob, strings.TrimSpace(out))
	}

	fresh := filepath.Join(base, "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	restored, err := FetchTaskRef(context.Background(), o, fresh, "app")
	if err != nil || restored != workHead {
		t.Fatalf("FetchTaskRef = %q, %v; want the WI head %q", restored, err, workHead)
	}
	w39AssertRestoredAncestry(t, fresh, restored)
}

// TestCarrierShallowMergesMovedUpstream pushes a WI line that merges a moved
// upstream twice: the first carrier push already contains such a merge, and
// the second is an update of that line after another upstream move. Both
// pushes face the production hook with a carrier cap below the upstream-only
// asset, so a cut that missed a merge parent would fail the push. Both must
// land without the asset and restore into a non-shallow clone.
func TestCarrierShallowMergesMovedUpstream(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_shallow_merge")

	mgr, err := aihubgit.NewManager(filepath.Join(base, "hubdata"), nil)
	if err != nil {
		t.Fatalf("aihub git manager: %v", err)
	}
	mgr.MaxBlobBytes = 1 << 20
	mgr.MaxTaskBlobBytes = 1 << 20
	if err := mgr.EnsureBare(o.WorkItemID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	o.RemoteURL = mgr.RepoDir(o.WorkItemID)

	origin := filepath.Join(base, "origin", "app")
	w39Origin(t, origin, 2<<20, 0)
	assetBlob := w39ShallowAssetBlob(t, origin, "streamlit_app.mp4")

	mkTaskCapsule(t, o)
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "app")
	mustRun(t, "git", "clone", "-q", origin, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work1.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "WI: work one")

	// Upstream moves after the claim, and the WI line merges it: the first
	// carrier push already has an upstream merge parent.
	w39MoveUpstream(t, origin, 0)
	mustRun(t, "git", "-C", repoDir, "fetch", "-q", "origin")
	mustRun(t, "git", "-C", repoDir, "merge", "-q", "--no-edit", "origin/main")
	firstHead := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))

	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	w39AssertCarrier(t, o, origin, assetBlob, firstHead, "first push after a merge")

	// A second upstream move, merged into the carried line: the update push
	// must cut at the new merge parent too.
	w39MoveUpstream(t, origin, 1)
	mustRun(t, "git", "-C", repoDir, "fetch", "-q", "origin")
	mustRun(t, "git", "-C", repoDir, "merge", "-q", "--no-edit", "origin/main")
	secondHead := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))

	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("update publish: %v", err)
	}
	w39AssertCarrier(t, o, origin, assetBlob, secondHead, "update after a merge")
}

// TestCarrierShallowDeliveredMergeLands is the deliver shape of
// code review: the carrier is at the WI's Wn, upstream moves,
// the WI merges it as M, and deliver pushes M to origin as the WI's
// feature branch (refs/remotes/origin/polyforge/<name> == HEAD, which
// deliver's deliveredHeadMismatch enforces). The upstream walk must still
// cut at the merge parent and the claim base, not treat the WI's own
// pushed line as upstream: pre-fix the walk was empty, the head itself
// became the shallow root, and the hub refused the update as
// non-fast-forward, so the carrier stayed at Wn and a re-claim lost every
// post-deliver commit. A further commit F on top of M must land too.
func TestCarrierShallowDeliveredMergeLands(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_shallow_deliver")

	mgr, err := aihubgit.NewManager(filepath.Join(base, "hubdata"), nil)
	if err != nil {
		t.Fatalf("aihub git manager: %v", err)
	}
	mgr.MaxBlobBytes = 1 << 20
	mgr.MaxTaskBlobBytes = 1 << 20
	if err := mgr.EnsureBare(o.WorkItemID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	o.RemoteURL = mgr.RepoDir(o.WorkItemID)

	origin := filepath.Join(base, "origin", "app")
	w39Origin(t, origin, 2<<20, 0)
	assetBlob := w39ShallowAssetBlob(t, origin, "streamlit_app.mp4")
	claimBase := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD"))

	mkTaskCapsule(t, o)
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "app")
	mustRun(t, "git", "clone", "-q", origin, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work1.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "WI: work one")
	wn := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))

	// The pre-deliver carrier: refs/pf-task/app is at the WI's Wn.
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("carrier push before deliver: %v", err)
	}
	if carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("app")); !ok || carried != wn {
		t.Fatalf("carrier before deliver = %q (%v), want the WI head %q", carried, ok, wn)
	}

	// Deliver: upstream moves, the WI merges it as M, and the feature
	// branch is pushed to origin (its remote-tracking ref becomes M).
	w39MoveUpstream(t, origin, 0)
	upstreamNew := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD"))
	mustRun(t, "git", "-C", repoDir, "fetch", "-q", "origin")
	mustRun(t, "git", "-C", repoDir, "merge", "-q", "--no-edit", "origin/main")
	m := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
	branch := "polyforge/" + filepath.Base(o.CapsuleDir)
	mustRun(t, "git", "-C", repoDir, "update-ref", "refs/remotes/origin/"+branch, m)

	// The carrier update after deliver must land M, with the merge parent
	// and the claim base as the cut, and M not rooted.
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("carrier push after the delivered merge: %v", err)
	}
	carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("app"))
	if !ok || carried != m {
		t.Fatalf("carrier after the delivered merge = %q (%v), want the merge head %q: the WI's own pushed branch was treated as upstream, the head became the shallow root, and the update was refused (the carrier stays at %s)",
			carried, ok, m, wn)
	}
	w39AssertDeliveredCarrier(t, o, origin, assetBlob, m, claimBase, upstreamNew, "after the delivered merge")

	// A further commit F on top of M (the branch already on origin at M)
	// must land too: every later carrier push of the WI must stay
	// fast-forwardable.
	if err := os.WriteFile(filepath.Join(repoDir, "work2.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "WI: work two")
	f := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("carrier push after F: %v", err)
	}
	if carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("app")); !ok || carried != f {
		t.Fatalf("carrier after F = %q (%v), want %q: the post-deliver commit is not durable", carried, ok, f)
	}
	w39AssertDeliveredCarrier(t, o, origin, assetBlob, f, claimBase, upstreamNew, "after F")
}

// w39AssertDeliveredCarrier checks one carrier of the deliver shape: it
// names head, its shallow roots are exactly the claim base and the new
// upstream head (never a WI commit), the upstream-only asset blob is
// absent, and a fresh origin clone restores head non-shallow with
// origin/main as an ancestor.
func w39AssertDeliveredCarrier(t *testing.T, o Opts, origin, assetBlob, head, claimBase, upstreamNew, phase string) {
	t.Helper()
	want := map[string]bool{claimBase: true, upstreamNew: true}
	roots := w39ShallowRoots(t, o.RemoteURL)
	got := map[string]bool{}
	for _, root := range roots {
		got[root] = true
	}
	for root := range want {
		if !got[root] {
			t.Fatalf("%s: shallow roots = %v, want %s among them: the cut missed the upstream boundary", phase, roots, root)
		}
	}
	if got[head] {
		t.Fatalf("%s: the head %s is a shallow root: the update is pushed parentless and refused non-fast-forward", phase, head)
	}
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", assetBlob); err == nil {
		t.Fatalf("%s: the hub holds the upstream-only asset blob %s: the upstream history was carried (%s)", phase, assetBlob, strings.TrimSpace(out))
	}
	fresh := filepath.Join(t.TempDir(), "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	restored, err := FetchTaskRef(context.Background(), o, fresh, "app")
	if err != nil || restored != head {
		t.Fatalf("%s: FetchTaskRef = %q, %v; want %q", phase, restored, err, head)
	}
	w39AssertRestoredAncestry(t, fresh, restored)
}

// w39MoveUpstream advances the origin's main by one commit.
func w39MoveUpstream(t *testing.T, origin string, n int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(origin, fmt.Sprintf("upstream-move-%d.txt", n)), []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", origin, "add", "-A")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "upstream: move")
}

// w39AssertCarrier checks one published carrier: it names head, its shallow
// roots are upstream commits (no WI commit is a root), the upstream-only
// asset blob is absent from the hub, and a fresh claim clone restores head
// with origin/main as an ancestor and without becoming shallow.
func w39AssertCarrier(t *testing.T, o Opts, origin, assetBlob, head, phase string) {
	t.Helper()
	carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("app"))
	if !ok || carried != head {
		t.Fatalf("%s: carrier = %q (%v), want the WI head %q", phase, carried, ok, head)
	}
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "app")
	wiCommits := strings.Fields(mustRun(t, "git", "-C", repoDir, "rev-list", head, "--not", "--remotes=origin"))
	roots := w39ShallowRoots(t, o.RemoteURL)
	if len(roots) == 0 {
		t.Fatalf("%s: the carrier has no shallow root", phase)
	}
	for _, root := range roots {
		for _, own := range wiCommits {
			if root == own {
				t.Fatalf("%s: shallow root %s is a WI commit; the cut must be at the upstream boundary", phase, root)
			}
		}
	}
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", assetBlob); err == nil {
		t.Fatalf("%s: the hub holds the upstream-only asset blob %s: the upstream history was carried (%s)", phase, assetBlob, strings.TrimSpace(out))
	}

	fresh := filepath.Join(t.TempDir(), "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	restored, err := FetchTaskRef(context.Background(), o, fresh, "app")
	if err != nil || restored != head {
		t.Fatalf("%s: FetchTaskRef = %q, %v; want %q", phase, restored, err, head)
	}
	w39AssertRestoredAncestry(t, fresh, restored)
}
