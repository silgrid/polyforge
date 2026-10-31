package gitops

// w170_carrier_rewind_repro_test.go: repro. After a rewind
// re-executes the work and rewrites the task repo's commits, the carrier
// ref on the hub still names the superseded tip. The next publish must
// reconcile this work item's own carrier (move it to the worktree's
// current head), not fail non-fast-forward on every step and leave the
// resume copy on the abandoned history.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// The fixture is the shape: a task repo whose head was
// carried once, then a rewind rewrote that commit (same work content, new
// SHA), so the hub's carrier tip is no longer an ancestor of the head the
// worktree now publishes. The carrier must end at the rewritten head.
func TestW170CarrierSelfHealsRewrittenTipAfterRewind(t *testing.T) {
	setIdentity(t)
	ctx := context.Background()
	o, base := taskOpts(t, "wi_w170")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "pi-web-gmi")
	publishedHead := mkTaskRepoWithWork(t, o.CapsuleDir, "pi-web-gmi", filepath.Join(base, "origin"))

	if _, _, _, err := CommitPush(ctx, o, "observe: v1"); err != nil {
		t.Fatalf("first CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("pi-web-gmi")); !ok || got != publishedHead {
		t.Fatalf("carrier before the rewind = %q (%v), want %q", got, ok, publishedHead)
	}

	// The rewind: the step re-executes and rewrites the work commit. The
	// content is preserved (only the commit message changes here), so the
	// hub's old tip and the new head carry the same work; only the history
	// shape differs — the old tip is no longer an ancestor of the new head.
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "--amend", "-m", "repo work (rewind rewrite)")
	rewrittenHead := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
	if rewrittenHead == publishedHead {
		t.Fatal("fixture: the rewind must rewrite the commit SHA")
	}

	o.CarrierAlerts = &CarrierAlerts{}
	var events []string
	o.EventSink = func(kind string, _ map[string]any) { events = append(events, kind) }

	// The next publish rides the chain push as usual; the carrier lane must
	// land the rewritten head on the hub's refs/pf-task/<repo>.
	if _, _, _, err := CommitPush(ctx, o, "observe: v2 after rewind"); err != nil {
		t.Fatalf("CommitPush after the rewind must not fail the chain publish: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("pi-web-gmi")); !ok || got != rewrittenHead {
		t.Fatalf("carrier after the rewind = %q (%v), want the worktree head %q (events: %v)", got, ok, rewrittenHead, events)
	}
}
