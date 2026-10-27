package daemon

// b8_async_dispatch_test.go — repo review 2026-10-16 B-8: the dispatcher
// runs each claim in its own goroutine; the queue counts claims under
// way against the concurrency cap, and a shutdown during a clone defers
// the claim instead of turning the work item blocked.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// Two claims taken back to back respect the slot limit even though
// neither is in the runtime's job list yet, and a second entry for a
// work item whose claim is under way waits for it.
func TestB8QueueCountsClaimsUnderWayAgainstSlots(t *testing.T) {
	q := NewJobQueue(1, func() map[string]bool { return map[string]bool{} })
	for _, wi := range []string{"wi_a", "wi_b"} {
		if err := q.Enqueue(qEntry{WorkItemID: wi}); err != nil {
			t.Fatal(err)
		}
	}
	if e := q.Take(); e == nil || e.WorkItemID != "wi_a" {
		t.Fatalf("first take = %+v", e)
	}
	if e := q.Take(); e != nil {
		t.Fatalf("a second claim started past max=1 while the first is under way: %+v", e)
	}
	if n := q.FreeSlots(); n != 0 {
		t.Fatalf("free slots = %d with a claim under way, want 0", n)
	}
	if !q.IsRunning("wi_a") {
		t.Fatal("a claim under way must count as running (the auto wave must not re-enqueue it)")
	}
	if err := q.Settle("wi_a"); err != nil {
		t.Fatal(err)
	}
	if e := q.Take(); e == nil || e.WorkItemID != "wi_b" {
		t.Fatalf("take after the settle = %+v, want wi_b", e)
	}

	q = NewJobQueue(4, func() map[string]bool { return map[string]bool{} })
	_ = q.Enqueue(qEntry{WorkItemID: "wi_dup"})
	if e := q.Take(); e == nil {
		t.Fatal("no take")
	}
	_ = q.Enqueue(qEntry{WorkItemID: "wi_dup"})
	if e := q.Take(); e != nil {
		t.Fatalf("a second claim of a work item under way was started: %+v", e)
	}
}

// hangingRemote: an http git remote that accepts and never answers.
func hangingRemote(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	return "http://" + ln.Addr().String() + "/app.git"
}

// The dispatcher no longer runs the claim on the Run loop: with max=1 a
// hung clone holds the one slot (the second submit stays queued, never
// claimed), the faces keep answering, and a shutdown during the clone
// completes promptly WITHOUT ending the session — the claim stays in the
// claiming journal and both submits stay in the queue file for the next
// start.
func TestB8DispatchSlotLimitAndShutdownDuringClone(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.MaxConcurrent = 1 })
	const slow, next = "wi_b8_slow", "wi_b8_next"
	td.hub.mu.Lock()
	td.hub.claimRepos = map[string][]api.ProjectRepo{slow: {{Name: "app", URL: hangingRemote(t)}}}
	td.hub.mu.Unlock()
	for _, wi := range []string{slow, next} {
		mkBare(t, td.cfg.GitRemoteBase, wi)
		if _, err := td.client.ClaimWithModels(wi, "normal", nil); err != nil {
			t.Fatalf("claim %s: %v", wi, err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if ji, ok := td.rt.Info(slow); ok && ji.State == runtime.StateClaiming {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached CLAIMING", slow)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // several dispatch ticks
	td.hub.mu.Lock()
	claims := len(td.hub.claims)
	td.hub.mu.Unlock()
	if claims != 1 {
		t.Fatalf("hub claims = %d with max=1 and one claim under way, want 1", claims)
	}
	if _, live := td.rt.Info(next); live {
		t.Fatalf("%s started past the slot limit", next)
	}
	if _, err := td.client.Status(); err != nil {
		t.Fatalf("status while a clone hangs: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- td.Shutdown(context.Background(), DirectShutdownCause()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown hung behind the clone")
	}
	if n := td.hub.endCount(); n != 0 {
		t.Fatalf("the shutdown ended %d session(s) (%+v): a cancelled clone must not block the work item", n, td.hub.lastEnd())
	}
	raw, err := os.ReadFile(filepath.Join(td.cfg.DataDir, "claiming.json"))
	if err != nil {
		t.Fatalf("claiming journal: %v", err)
	}
	var cj struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &cj); err != nil || cj.Items[slow] == nil {
		t.Fatalf("claiming journal lacks the deferred claim of %s: %s", slow, raw)
	}
	raw, err = os.ReadFile(filepath.Join(td.cfg.DataDir, submitQueueFile))
	if err != nil {
		t.Fatalf("submit queue: %v", err)
	}
	var sq submitQueueState
	if err := json.Unmarshal(raw, &sq); err != nil {
		t.Fatalf("submit queue undecodable: %v: %s", err, raw)
	}
	seen := map[string]bool{}
	for _, e := range sq.Entries {
		seen[e.WorkItemID] = true
	}
	if !seen[slow] || !seen[next] {
		t.Fatalf("submit queue after the shutdown = %s, want both entries kept", raw)
	}
}

// review nit of B-8: a claim dispatched while a long close of another
// work item (here a wrap whose SessionEnd hangs) holds that work item's
// lock must not delay the shutdown — it waits for no claim, so the
// sequence (and its WIP step) proceeds at once.
func TestB8ShutdownNotDelayedByClaimQueuedOnLock(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_b8_live")
	release := make(chan struct{})
	td.hub.mu.Lock()
	td.hub.blockEnd = release
	td.hub.mu.Unlock()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	wrapDone := make(chan struct{})
	go func() {
		defer close(wrapDone)
		_ = td.rt.Stop(td.runCtx, "wi_b8_live", "wrapped") // holds its lock inside the parked SessionEnd
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if ji, ok := td.rt.Info("wi_b8_live"); ok && ji.State == runtime.StateSealing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the wrap never reached SEALING")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mkBare(t, td.cfg.GitRemoteBase, "wi_b8_queued")
	if _, err := td.client.ClaimWithModels("wi_b8_queued", "normal", nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // the dispatcher's Start runs meanwhile
	t0 := time.Now()
	if err := td.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Fatalf("shutdown took %s behind a claim queued on the lock", d)
	}
	unblock()
	<-wrapDone
}
