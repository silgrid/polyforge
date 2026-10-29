package gitops

// repro: the carrier ref (refs/pf-task/<name>) carries the task
// repo's WHOLE upstream history, so a large object that only the upstream
// history holds (skypilot's 21 MB streamlit_app.mp4, added and later deleted
// upstream) still has to be uploaded on the first carrier push. Against an
// aihub whose carrier blob cap is 16 MiB — the limit the WI reports from the
// field ("aihub: blob exceeds the 16777216-byte limit") — that push is
// refused, the repo is never carried, and a re-claim can only restore the
// origin's history: the WI's own commits are gone.
//
// The two tests are the failing oracle of the WI (the carrier must store only
// the WI's own commits, so an upstream-only asset cannot block it):
//
//   - TestW39UpstreamOversizeAssetBlocksCarrierPush reproduces the field
//     failure end to end through the real carrier lane and the production
//     aihub pre-receive hook.
//   - TestW39CarrierRefCarriesOnlyTheWorkItemsOwnCommits pins the mechanism
//     at this tree's own defaults (no size involved). Its oracle was recast
//     with the owner's approval: the literal "the upstream
//     head is not an ancestor of the carrier" cannot hold together with a
//     working resume, because the claim base must sit on the hub as the
//     carrier's shallow root (root-cause E6/E8). The safe-property form is
//     used instead: no upstream-only blob and no commit below the WI line's
//     upstream boundary on the hub; boundary commits appear only as shallow
//     roots; and the restored clone is non-shallow with origin/<default> as
//     an ancestor of the restored head.
//
// Neither test asserts a cause or a fix shape; they assert only what the WI
// says must hold after the fix.

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

// w39DeployedCarrierCap is the carrier blob cap of the aihub the WI reports
// from the field (16 MiB: the event body is "aihub: blob exceeds the
// 16777216-byte limit"). This tree's default carrier cap is larger
// (capsule.MaxTaskBlobBytes, polyforge#158), so the test pins the deployed
// value to reproduce the reported refusal with the shipped hook.
const w39DeployedCarrierCap = 16 << 20

// w39Origin lays a task-repo origin whose UPSTREAM history holds one
// oversize asset commit that a later upstream commit deletes — so the asset
// is reachable from the upstream head but absent from the tree the WI works
// on, exactly the skypilot shape. Answers the upstream head.
func w39Origin(t *testing.T, origin string, assetBytes int, upstreamCommits int) string {
	t.Helper()
	if out, err := run("git", "init", "-q", "-b", "main", origin); err != nil {
		t.Fatalf("task origin init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "streamlit_app.mp4"), bytes.Repeat([]byte{7}, assetBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", origin, "add", "-A")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "upstream: add the asset")
	mustRun(t, "git", "-C", origin, "rm", "-q", "streamlit_app.mp4")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "upstream: delete the asset")
	for i := 0; i < upstreamCommits; i++ {
		if err := os.WriteFile(filepath.Join(origin, fmt.Sprintf("upstream-%d.txt", i)), []byte("upstream\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "git", "-C", origin, "add", "-A")
		mustRun(t, "git", "-C", origin, "commit", "-q", "-m", fmt.Sprintf("upstream: change %d", i))
	}
	return strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", "HEAD"))
}

// w39ClaimShape clones the origin into the capsule's repos/<name> (the
// claim-time clone) and makes the WI's one own commit on top. Answers the
// work head.
func w39ClaimShape(t *testing.T, o Opts, origin, name string) string {
	t.Helper()
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, name)
	mustRun(t, "git", "clone", "-q", origin, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "the WI's own work")
	return strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
}

// The reported failure, end to end: publish a task repo whose upstream
// history holds a 21 MB asset (deleted upstream, absent from the WI's tree)
// against the deployed 16 MiB carrier cap. Expected: the carrier push lands
// the WI's head. Observed today: the hook refuses the whole push because the
// upstream-only asset is part of the history being carried, so refs/pf-task/
// skypilot never exists and nothing of the WI's work is durable.
func TestW39UpstreamOversizeAssetBlocksCarrierPush(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_w39_oversize")

	// The production hook, baked by aihub's own Manager at the deployed cap.
	mgr, err := aihubgit.NewManager(filepath.Join(base, "hubdata"), nil)
	if err != nil {
		t.Fatalf("aihub git manager: %v", err)
	}
	mgr.MaxBlobBytes = w39DeployedCarrierCap
	mgr.MaxTaskBlobBytes = w39DeployedCarrierCap
	if err := mgr.EnsureBare(o.WorkItemID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	o.RemoteURL = mgr.RepoDir(o.WorkItemID)

	origin := filepath.Join(base, "origin", "skypilot")
	w39Origin(t, origin, 21<<20, 0)

	mkTaskCapsule(t, o)
	workHead := w39ClaimShape(t, o, origin, "skypilot")

	var events []string
	o.EventSink = func(kind string, body map[string]any) {
		events = append(events, fmt.Sprintf("%s %v", kind, body))
	}
	// The carrier failure never fails the chain publish (best-effort-loud):
	// reaching the assertions below is the point, the carrier is not.
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("the carrier failure must not fail the chain publish: %v", err)
	}

	got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("skypilot"))
	if !ok {
		t.Fatalf("the carrier ref %s does not exist after the publish: the WI's own commit %s is not durable, a re-claim restores only the origin (carrier events: %v)",
			TaskRefName("skypilot"), workHead, events)
	}
	if got != workHead {
		t.Fatalf("the carrier names %s, want the WI's own head %s (carrier events: %v)", got, workHead, events)
	}
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", workHead); err != nil {
		t.Fatalf("the hub does not hold the WI's own commit %s (%v: %s)", workHead, err, out)
	}

	// The re-claim half of the WI's acceptance: a fresh clone of the origin
	// (what a resume starts from) must read the WI's head back from the
	// carrier. Today there is nothing to read, so the work is lost.
	fresh := filepath.Join(base, "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	carried, err := FetchTaskRef(context.Background(), o, fresh, "skypilot")
	if err != nil {
		t.Fatalf("FetchTaskRef after the failed carrier push: %v", err)
	}
	if carried != workHead {
		t.Fatalf("a re-claim reads the carried head %q, want the WI's own head %s: resume restores only the origin and loses the WI's commits (carrier events: %v)",
			carried, workHead, events)
	}
}

// The mechanism, at this tree's own defaults and with no size games: the
// carrier ref must store only the WI's own commits, so the hub must hold no
// upstream-only blob and no commit below the WI line's upstream boundary. The
// boundary (claim-base) commit appears only as a shallow root, never as
// carried history. Observed today (pre-fix): the carrier ref is the task
// repo's unmodified history, upstream commits included — which is what drags
// every upstream object (and the 21 MB asset above) into the push.
func TestW39CarrierRefCarriesOnlyTheWorkItemsOwnCommits(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_w39_shallow")
	mkBare(t, o.RemoteURL, false)

	origin := filepath.Join(base, "origin", "skypilot")
	upstreamHead := w39Origin(t, origin, 4096, 3)
	// The root commit introduced the asset; the next commits deleted it and
	// moved upstream. Both the asset blob and everything below the claim
	// base must stay off the hub.
	assetCommit := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-list", "--max-parents=0", "HEAD"))
	assetBlob := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", assetCommit+":streamlit_app.mp4"))
	boundaryParent := strings.TrimSpace(mustRun(t, "git", "-C", origin, "rev-parse", upstreamHead+"^"))

	mkTaskCapsule(t, o)
	workHead := w39ClaimShape(t, o, origin, "skypilot")

	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	carried, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("skypilot"))
	if !ok {
		t.Fatalf("the carrier ref %s does not exist after the publish", TaskRefName("skypilot"))
	}
	if carried != workHead {
		t.Fatalf("the carrier names %s, want the WI's own head %s", carried, workHead)
	}

	// Recast oracle (owner-approved): no upstream-only blob on the hub.
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", assetBlob); err == nil {
		t.Fatalf("the hub holds the upstream-only asset blob %s: the carrier still carries the task repo's upstream history (%s)",
			assetBlob, strings.TrimSpace(out))
	}
	// No commit below the WI line's upstream boundary on the hub.
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", boundaryParent); err == nil {
		t.Fatalf("the hub holds %s, a commit below the upstream boundary %s: the carrier still carries upstream history (%s)",
			boundaryParent, upstreamHead, strings.TrimSpace(out))
	}
	// The boundary commit itself is present, but only as the shallow root.
	if out, err := run("git", "--git-dir", o.RemoteURL, "cat-file", "-e", upstreamHead); err != nil {
		t.Fatalf("the hub lacks the boundary commit %s (%v: %s)", upstreamHead, err, strings.TrimSpace(out))
	}
	raw, err := os.ReadFile(filepath.Join(o.RemoteURL, "shallow"))
	if err != nil {
		t.Fatalf("read the hub shallow file: %v", err)
	}
	if roots := strings.Fields(string(raw)); len(roots) != 1 || roots[0] != upstreamHead {
		t.Fatalf("carrier shallow roots = %v, want [the upstream boundary %s]", roots, upstreamHead)
	}

	// The resume half: a fresh origin clone (what a re-claim starts from)
	// reads the WI head back, stays non-shallow and keeps origin/main as an
	// ancestor of the restored head.
	fresh := filepath.Join(base, "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	restored, err := FetchTaskRef(context.Background(), o, fresh, "skypilot")
	if err != nil || restored != workHead {
		t.Fatalf("a re-claim reads the carried head %q, %v; want the WI's own head %s", restored, err, workHead)
	}
	if got := strings.TrimSpace(mustRun(t, "git", "-C", fresh, "rev-parse", "--is-shallow-repository")); got != "false" {
		t.Fatalf("the restored clone is shallow (is-shallow-repository=%s): resume loses its ancestry to origin", got)
	}
	if out, err := run("git", "-C", fresh, "merge-base", "--is-ancestor", "origin/main", restored); err != nil {
		t.Fatalf("origin/main is not an ancestor of the restored head %s (%v: %s): deliver and review cannot merge-base against it",
			restored, err, strings.TrimSpace(out))
	}
}
