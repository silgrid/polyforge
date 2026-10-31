package gitops

// w170_carrier_rewind_hook_test.go: against the REAL aihub
// pre-receive hook. The repro test pins the carrier end state on a hub
// without hooks; this file pins that the production hook accepts the
// daemon's lease-guarded replace of a same-line rewind, while foreign
// content is still refused and the divergence alert is raised once per
// job instead of on every publish.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aihubgit "github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w170RealHookHub provisions the WI's bare repo through aihub's own
// Manager, so the fixture runs the shipped pre-receive hook.
func w170RealHookHub(t *testing.T, o *Opts, base string) {
	t.Helper()
	mgr, err := aihubgit.NewManager(filepath.Join(base, "hubdata"), nil)
	if err != nil {
		t.Fatalf("aihub git manager: %v", err)
	}
	if err := mgr.EnsureBare(o.WorkItemID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	o.RemoteURL = mgr.RepoDir(o.WorkItemID)
}

// The #159 shape, on the production hook: publish, amend the carried work
// commit (same content, new SHA), publish again. The carrier must move to
// the rewritten head and the successful reconcile must raise no event.
func TestW170CarrierRewindLandsThroughRealAihubHook(t *testing.T) {
	setIdentity(t)
	ctx := context.Background()
	o, base := taskOpts(t, "wi_w170_hook")
	w170RealHookHub(t, &o, base)
	mkTaskCapsule(t, o)
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "pi-web-gmi")
	publishedHead := mkTaskRepoWithWork(t, o.CapsuleDir, "pi-web-gmi", filepath.Join(base, "origin"))

	if _, _, _, err := CommitPush(ctx, o, "observe: v1"); err != nil {
		t.Fatalf("first CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("pi-web-gmi")); !ok || got != publishedHead {
		t.Fatalf("carrier before the rewind = %q (%v), want %q", got, ok, publishedHead)
	}

	mustRun(t, "git", "-C", repoDir, "commit", "-q", "--amend", "-m", "repo work (rewind rewrite)")
	rewrittenHead := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
	if rewrittenHead == publishedHead {
		t.Fatal("fixture: the rewind must rewrite the commit SHA")
	}

	o.CarrierAlerts = &CarrierAlerts{}
	var events []string
	o.EventSink = func(kind string, _ map[string]any) { events = append(events, kind) }

	if _, _, _, err := CommitPush(ctx, o, "observe: v2 after rewind"); err != nil {
		t.Fatalf("CommitPush after the rewind: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("pi-web-gmi")); !ok || got != rewrittenHead {
		t.Fatalf("carrier after the rewind = %q (%v), want the rewritten head %q (events: %v)", got, ok, rewrittenHead, events)
	}
	if len(events) != 0 {
		t.Fatalf("a successful rewind reconcile raised %v, want no events", events)
	}
}

// Foreign content is not a rewind: reset the task repo to the origin commit
// and commit unrelated work. The stale carrier's paths are gone, so the
// containment gate must keep it, raise carrier_push_failed + task_ref_diverged
// once, and stay quiet on the next publish.
func TestW170CarrierForeignRewriteRefusedAndAlertedOnce(t *testing.T) {
	setIdentity(t)
	ctx := context.Background()
	o, base := taskOpts(t, "wi_w170_foreign")
	w170RealHookHub(t, &o, base)
	mkTaskCapsule(t, o)
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "pi-web-gmi")
	publishedHead := mkTaskRepoWithWork(t, o.CapsuleDir, "pi-web-gmi", filepath.Join(base, "origin"))
	if _, _, _, err := CommitPush(ctx, o, "observe: v1"); err != nil {
		t.Fatalf("first CommitPush: %v", err)
	}

	mustRun(t, "git", "-C", repoDir, "reset", "-q", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("foreign work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "foreign work")

	o.CarrierAlerts = &CarrierAlerts{}
	var events []string
	o.EventSink = func(kind string, _ map[string]any) { events = append(events, kind) }

	if _, _, _, err := CommitPush(ctx, o, "observe: foreign"); err != nil {
		t.Fatalf("a rejected carrier must not fail the chain publish: %v", err)
	}
	if got, _ := bareRefSHA(t, o.RemoteURL, TaskRefName("pi-web-gmi")); got != publishedHead {
		t.Fatalf("foreign rewrite moved the carrier to %q, want it kept at %q", got, publishedHead)
	}
	if len(events) != 2 || events[0] != EventCarrierPushFailed || events[1] != EventTaskRefDiverged {
		t.Fatalf("events = %v, want [%s %s]", events, EventCarrierPushFailed, EventTaskRefDiverged)
	}

	if _, _, _, err := CommitPush(ctx, o, "observe: foreign again"); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("a persisting divergence re-raised %v, want exactly the first pair", events)
	}
}
