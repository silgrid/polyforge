// carries: internal/polyforge/components/upgrade.go,
//
//	internal/polyforge/components/status.go
package components

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestComponentsUpgradeDrainRefusesASecondConcurrentRun pins the
// transaction exclusion: one upgrade (or rollback) at a time on a
// components volume. A second concurrent run is refused naming the
// running transaction and changes nothing at all.
func TestComponentsUpgradeDrainRefusesASecondConcurrentRun(t *testing.T) {
	root, seed := upgradeFixture(t)
	before := rootState(t, root)
	first := &fakeFace{busy: [][]string{{"default"}}} // busy forever: the first run waits
	started := make(chan struct{})
	var wg sync.WaitGroup
	var firstErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		firstErr = runUpgrade(t, root, seed, first, nil)
	}()
	<-started
	deadline := time.Now().Add(2 * time.Second)
	for first.drainCalls() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the first transaction never reached its drain wait")
		}
		time.Sleep(time.Millisecond)
	}
	face := &fakeFace{}
	err := runUpgrade(t, root, seed, face, nil)
	if err == nil || !strings.Contains(err.Error(), "another component transaction") {
		t.Fatalf("a second concurrent transaction must be refused naming the running one: %v", err)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused concurrent transaction changes nothing:\n%s", got)
	}
	// the first run finishes on the force request (the same in-volume
	// handoff a waiting `pf components upgrade --force` takes).
	if err := RequestUpgradeForce(root); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("the first transaction must finish on its own terms: %v", firstErr)
	}
}

// TestComponentsUpgradeDrainConsumesStaleCancelMarker pins the marker
// hygiene: a cancel or force marker left by an earlier run never stands
// down (or forces) a LATER upgrade — a fresh transaction clears both at
// its start, and a finished run leaves neither behind.
func TestComponentsUpgradeDrainConsumesStaleCancelMarker(t *testing.T) {
	root, seed := upgradeFixture(t)
	if err := RequestUpgradeCancel(root); err != nil {
		t.Fatal(err)
	}
	if err := RequestUpgradeForce(root); err != nil {
		t.Fatal(err)
	}
	face := &fakeFace{} // idle
	if err := runUpgrade(t, root, seed, face, nil); err != nil {
		t.Fatalf("an upgrade on an idle Host must complete: %v", err)
	}
	if UpgradeCancelRequested(root) {
		t.Fatal("a stale cancel marker must be cleared by the fresh transaction")
	}
	if _, err := os.Stat(filepath.Join(root, "upgrade-force")); err == nil {
		t.Fatal("a stale force marker must be cleared by the fresh transaction")
	}
}
