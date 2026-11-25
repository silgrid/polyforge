package daemon

// shutdown_drain_test.go — polyforge#278: the shutdown drains the runtime
// actors' accepted operations before its final persist and its face
// close, so Run returns, and the data-dir lock comes down, only after the
// last write of runtime-state.json landed (reference.md §6.2).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// TestRunReturnsOnlyAfterTheRuntimeDrained: a wrap the actor accepted is
// parked in the hub's SessionEnd, before its closing persist, when the
// shutdown starts. Run must not return and the lock must stay held while
// it is in flight; once the hub answers, the close's final write lands,
// and only then Run returns nil, the lock is free and no .tmp is left.
func TestRunReturnsOnlyAfterTheRuntimeDrained(t *testing.T) {
	const wi = "wi_dr_live"
	td := newTestDaemon(t, nil)
	td.submitClaim(t, wi)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// registered after newTestDaemon's cleanup, so it runs first: a red
	// run never leaves the close parked behind the daemon's own settle
	t.Cleanup(unblock)
	td.hub.mu.Lock()
	td.hub.blockEnd = release
	td.hub.mu.Unlock()

	// the controllable slow persist: the wrap is accepted by the work
	// item's actor, and its closing persist (DONE, the record dropped)
	// waits behind the parked SessionEnd
	wrapDone := make(chan struct{})
	go func() {
		defer close(wrapDone)
		_ = td.rt.Stop(td.runCtx, wi, "wrapped")
	}()
	waitFor(t, "the wrap of "+wi+" to reach SEALING", 10*time.Second, func() bool {
		ji, ok := td.rt.Info(wi)
		return ok && ji.State == runtime.StateSealing
	})

	drainEntered := make(chan struct{})
	drain := td.drainRuntime
	td.drainRuntime = func(ctx context.Context) error {
		close(drainEntered)
		return drain(ctx)
	}
	shutErr := make(chan error, 1)
	go func() { shutErr <- td.Shutdown(context.Background(), DirectShutdownCause()) }()

	// A: the shutdown reaches the runtime drain; Run has not returned
	select {
	case <-drainEntered:
	case <-td.runDone:
		t.Fatal("Run returned while an accepted close was still in flight")
	case <-time.After(10 * time.Second):
		t.Fatal("within 10s of the shutdown neither the runtime drain started nor Run returned")
	}

	// B: the data-dir lock is still held by this daemon
	if f, err := acquireDataDirLock(td.cfg.DataDir); err == nil {
		_ = f.Close()
		t.Fatal("the data-dir lock was free while the runtime drain waited on an accepted close")
	}

	// C: the hub answers; the shutdown and Run both end with nil
	unblock()
	deadline := time.After(15 * time.Second)
	select {
	case err := <-shutErr:
		if err != nil {
			t.Fatalf("Shutdown = %v, want nil", err)
		}
	case <-deadline:
		t.Fatal("Shutdown did not return within 15s of the hub's answer")
	}
	select {
	case err := <-td.runDone:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-deadline:
		t.Fatal("Run did not return within 15s of the hub's answer")
	}

	// D: read as soon as Run returned: the close's final write already
	// landed (a wrapped close ends DONE and drops the record, §3.5), no
	// temp file of the atomic write is left, the actors are idle and the
	// lock is free
	statePath := filepath.Join(td.cfg.DataDir, "runtime-state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("runtime-state.json after Run returned: %v", err)
	}
	var st struct {
		Jobs []struct {
			WorkItemID string `json:"work_item_id"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("runtime-state.json unparsable after Run returned: %v\n%s", err, raw)
	}
	for _, j := range st.Jobs {
		if j.WorkItemID == wi {
			t.Fatalf("runtime-state.json still records %s after Run returned: the wrapped close's final write had not landed\n%s", wi, raw)
		}
	}
	tmps, err := filepath.Glob(filepath.Join(td.cfg.DataDir, ".runtime-state.json.*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tmps) != 0 {
		t.Fatalf("temp files of the state write left after Run returned: %v", tmps)
	}
	if !td.rt.Idle() {
		t.Fatal("a runtime actor still runs after Run returned")
	}
	f, err := acquireDataDirLock(td.cfg.DataDir)
	if err != nil {
		t.Fatalf("the data-dir lock is still held after Run returned: %v", err)
	}
	_ = f.Close()

	select {
	case <-wrapDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the wrap's Stop call never returned")
	}
}
