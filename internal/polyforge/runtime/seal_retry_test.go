package runtime

// seal_retry_test.go — repo review 2026-10-16 B-4: a terminal close whose
// SessionEnd fails (hub blip) used to leave the job SEALING with no retry
// lane: the agent was dead, the monitor stopped, and every `pf wrap`
// answered "a close is already in progress" until a daemon restart. The
// close is now retried in the background like the blocked end_pending
// lane; a STALE_SESSION answer stops the retry and finishes locally.

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func sealRetryRuntime(t *testing.T, wi string) (*Runtime, *fakeHub, func() []string) {
	t.Helper()
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
	var mu sync.Mutex
	var kinds []string
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		mu.Lock()
		kinds = append(kinds, kind)
		mu.Unlock()
	}
	t.Cleanup(func() { r.Close(); terminateJobAgent(r, wi) })
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m := recvRT(t, toOB); m.Kind != protocol.RTStarted {
		t.Fatalf("expected RTStarted, got %+v", m)
	}
	// the production bare repo advertises push options (the session
	// fingerprint rides every push); the rig's plain bare does not
	bare := filepath.Join(hub.capsuleRepoBase, wi+".git")
	if out, err := exec.Command("git", "--git-dir", bare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("bare config: %v: %s", err, out)
	}
	return r, hub, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), kinds...)
	}
}

func waitJobGone(t *testing.T, r *Runtime, wi string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		gone := r.jobOf(wi) == nil
		if gone {
			waitRetryIdle(t, r)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	ji, _ := r.Info(wi)
	t.Fatalf("job %s never settled (state %s)", wi, ji.State)
}

func TestSealSessionEndBlipIsRetriedToDone(t *testing.T) {
	const wi = "wi_b4retry"
	r, hub, _ := sealRetryRuntime(t, wi)
	hub.mu.Lock()
	hub.end[wi] = errors.New("connection refused")
	hub.mu.Unlock()

	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err == nil {
		t.Fatal("the failed SessionEnd must surface")
	}
	if ji, ok := r.Info(wi); !ok || ji.State != StateSealing {
		t.Fatalf("job after the failed close = %+v (ok=%v), want SEALING", ji, ok)
	}
	hub.mu.Lock()
	delete(hub.end, wi) // the hub is back
	hub.mu.Unlock()

	waitJobGone(t, r, wi)
	if end := hub.lastEnd(); end.Status != api.StatusWrapped {
		t.Fatalf("the retried close must carry the recorded verdict, got %+v", end)
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("the settled close must drop the record, got %+v", recs)
	}
}

func TestSealRetryStaleSessionFinishesLocally(t *testing.T) {
	const wi = "wi_b4stale"
	r, hub, kinds := sealRetryRuntime(t, wi)
	hub.mu.Lock()
	hub.end[wi] = errors.New("connection refused")
	hub.mu.Unlock()

	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err == nil {
		t.Fatal("the failed SessionEnd must surface")
	}
	// An admin ends the session meanwhile: every further close answers
	// STALE_SESSION.
	hub.mu.Lock()
	hub.end[wi] = &api.Error{Code: api.ErrCoreForbidden, Detail: "session does not match the current holder", Extra: []byte(`{"reason":"STALE_SESSION"}`)}
	hub.mu.Unlock()

	waitJobGone(t, r, wi)
	found := false
	for _, k := range kinds() {
		if k == "seal_finished_locally" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a local finish must leave a seal_finished_locally event, got %v", kinds())
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("the local finish must drop the record, got %+v", recs)
	}
}
