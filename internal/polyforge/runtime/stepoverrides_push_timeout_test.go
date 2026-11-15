package runtime

// stepoverrides_push_timeout_test.go (polyforge#47, AC3): the running
// posture's overlay chain push is bounded (overridePushTimeout — the
// pending write's protocol.PushTimeout deadline). A push stuck in a
// hanging pre-receive hook must not pin the work item's actor: the
// pause and the wrap of the SAME ticket proceed once the bounded push
// gave up, and the overlay's commit stands locally exactly like any
// failed push.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// hangFirstPush installs a pre-receive hook that hangs the FIRST push of
// the work item's capsule chain for minutes (a stuck push) and passes
// every later one: the hook's own marker file is its one-shot state.
func hangFirstPush(t *testing.T, r *Runtime, wi string) {
	t.Helper()
	hub := r.hub.(*fakeHub)
	hooks := filepath.Join(hub.capsuleRepoBase, wi+".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := `#!/bin/sh
d=$(dirname "$0")
if [ -f "$d/../.first-push-hung" ]; then
	exit 0
fi
touch "$d/../.first-push-hung"
sleep 600
exit 0
`
	if err := os.WriteFile(filepath.Join(hooks, "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
}

// hung markers the point the first push has reached the hook.
func firstPushHung(t *testing.T, r *Runtime, wi string) bool {
	t.Helper()
	hub := r.hub.(*fakeHub)
	_, err := os.Stat(filepath.Join(hub.capsuleRepoBase, wi+".git", ".first-push-hung"))
	return err == nil
}

// requireDoneIn runs what and fails the test when it does not return in d.
func requireDoneIn(t *testing.T, d time.Duration, what string, run func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(d):
		t.Fatalf("%s did not finish within %s", what, d)
	}
}

// startOverrideWithStuckPush claims wi, hangs its chain's first push and
// starts the overlay write that pushes into it (a model chain on the
// release step), answering a channel for its outcome.
func startOverrideWithStuckPush(t *testing.T, wi string) (*Runtime, chan OverrideOutcome) {
	t.Helper()
	r, _ := pendingEnv(t)
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if r.jobOf(wi) == nil {
		t.Fatal("the claim must run to a live agent")
	}
	hangFirstPush(t, r, wi)
	prev := overridePushTimeout
	t.Cleanup(func() { overridePushTimeout = prev })
	overridePushTimeout = 300 * time.Millisecond
	out := make(chan OverrideOutcome, 1)
	go func() {
		res, err := r.ApplyStepOverride(context.Background(), wi, OverrideChange{
			Step: "release", Operator: "usr_1", Models: []string{"pi:ov/newer"},
		})
		if err != nil {
			t.Errorf("the overlay write: %v", err)
			out <- OverrideOutcome{}
			return
		}
		out <- res
	}()
	// the stuck push is in flight before anything else is asked of the actor
	deadline := time.Now().Add(10 * time.Second)
	for !firstPushHung(t, r, wi) {
		if time.Now().After(deadline) {
			t.Fatalf("the first push never reached the hanging hook")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return r, out
}

// the overlay write gives up its push inside its deadline and reports the
// failed push, and a close of the same ticket that waits on the actor
// while the push hangs still finishes promptly.
func assertBoundedOverride(t *testing.T, out chan OverrideOutcome) {
	t.Helper()
	select {
	case res := <-out:
		if !res.Changed || res.Posture != OverridePostureRunning {
			t.Fatalf("the overlay write must change and land running: %+v", res)
		}
		if res.Published {
			t.Fatalf("a push stuck in the hook must not be reported published: %+v", res)
		}
		if res.Note == "" {
			t.Fatalf("the unpublished write must say why: %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the overlay write did not give up its stuck push within its bound")
	}
}

func TestStepOverridePushIsBoundedPauseNotBlocked(t *testing.T) {
	const wi = "wi_ov47p"
	r, out := startOverrideWithStuckPush(t, wi)
	// the pause of the same ticket must not wait out the stuck push
	requireDoneIn(t, 20*time.Second, "the pause of the same work item",
		func() error { return r.Stop(context.Background(), wi, protocol.FinishPaused) })
	assertBoundedOverride(t, out)
	if ji, ok := r.Info(wi); !ok || ji.State != StatePaused {
		t.Fatalf("the work item must be paused after the close: %+v", ji)
	}
}

func TestStepOverridePushIsBoundedWrapNotBlocked(t *testing.T) {
	const wi = "wi_ov47w"
	r, out := startOverrideWithStuckPush(t, wi)
	// the wrap of the same ticket must not wait out the stuck push
	requireDoneIn(t, 20*time.Second, "the wrap of the same work item",
		func() error { return r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	assertBoundedOverride(t, out)
}
