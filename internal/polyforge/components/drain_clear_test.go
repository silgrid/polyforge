// carries: internal/polyforge/components/upgrade.go,
//
//	internal/polyforge/components/rollback.go
package components

import (
	"errors"
	"strings"
	"testing"
)

// errFaceStopped is the drain-error path's transport failure: the
// daemon face stops answering mid-wait.
var errFaceStopped = errors.New("the daemon face stopped answering")

// TestComponentsVerifyARefusedSeedNeverLeavesTheDrain pins review r2#5 /
// AC2's refusal half: a failure AFTER the drain is set must never
// leave the Host refusing new work forever — the orchestration asks
// the daemon to restart (the drain's only documented end) and says so
// in its error. The case: a seed Install refuses (an older version),
// so nothing switched at all.
func TestComponentsVerifyARefusedSeedNeverLeavesTheDrain(t *testing.T) {
	root, _ := upgradeFixture(t)
	older := t.TempDir()
	writeVersionDir(t, older, seedSpec{version: "v0.2.3", minBase: "v0.2.3"})
	before := rootState(t, root)
	face := &fakeFace{} // idle: the drain is set and accepted at once

	err := runUpgrade(t, root, older, face, nil)
	if err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("the older seed must be refused: %v", err)
	}
	if !strings.Contains(err.Error(), "drain was cleared") {
		t.Fatalf("a failure after the drain must say the drain was cleared: %v", err)
	}
	if face.restartCalls() != 1 {
		t.Fatalf("the failed run must ask the daemon to restart exactly once, got %d", face.restartCalls())
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a refused upgrade changes nothing:\n%s", got)
	}
	s, rerr := ReadUpgradeStatus(root)
	if rerr != nil || s == nil || s.State != UpgradeStateFailed || s.Error == "" {
		t.Fatalf("the failure must be recorded with its reason: %+v (%v)", s, rerr)
	}
}

// TestComponentsRollbackDrainErrorClearsTheDrain pins the same rule on
// the rollback's own error path: a drain-phase failure (the daemon face
// stops answering mid-wait) still clears the drain it set.
func TestComponentsRollbackDrainErrorClearsTheDrain(t *testing.T) {
	root := rollbackFixture(t)
	face := &fakeFace{busy: [][]string{{"default"}}}
	face.onDrain = func(n int, f *fakeFace) {
		if n == 1 {
			f.drainErrs = map[int]error{1: errFaceStopped}
		}
	}
	err := runRollback(t, root, face, nil)
	if err == nil {
		t.Fatal("a daemon face that stops answering must fail the rollback")
	}
	if !strings.Contains(err.Error(), "drain was cleared") {
		t.Fatalf("a failure after the drain must say the drain was cleared: %v", err)
	}
	if face.restartCalls() != 1 {
		t.Fatalf("the failed run must ask the daemon to restart exactly once, got %d", face.restartCalls())
	}
}
