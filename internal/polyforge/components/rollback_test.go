// carries: internal/polyforge/components/rollback.go,
//
//	internal/polyforge/components/upgrade.go
package components

// rollback_test.go — polyforge#190 (84-inplace-upgrade), u3: the
// one-click rollback, authored test-first against AC6's own words:
// the swap of current and previous with the same drain and restarts
// the upgrade takes; and every refusal — a missing, corrupt,
// cross-channel, schema-migrated or base-unsatisfiable previous —
// names its reason, changes nothing at all and never reaches the
// daemon. Expected values come from the ticket and the layout's own
// rules (polyforge#162), never from the implementation's output.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rollbackFixture answers one components root with current v0.2.5 and
// previous v0.2.4 — the state an upgrade leaves behind.
func rollbackFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, spec := range []seedSpec{
		{version: "v0.2.4", minBase: "v0.2.4"},
		{version: "v0.2.5", minBase: "v0.2.4"},
	} {
		writeVersionDir(t, filepath.Join(root, ReleasesDirname, spec.version), spec)
	}
	if err := os.Symlink(filepath.Join(ReleasesDirname, "v0.2.5"), filepath.Join(root, CurrentLinkname)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ReleasesDirname, "v0.2.4"), filepath.Join(root, PreviousLinkname)); err != nil {
		t.Fatal(err)
	}
	return root
}

// runRollback runs one rollback with the test's defaults.
func runRollback(t *testing.T, root string, face DaemonFace, over func(*RollbackOptions)) error {
	t.Helper()
	opts := RollbackOptions{
		Root: root, BaseVersion: "v0.2.4", Face: face, Poll: time.Millisecond,
		RestartPiWeb: func() error { return nil },
	}
	if over != nil {
		over(&opts)
	}
	_, err := Rollback(context.Background(), opts)
	return err
}

// TestComponentsRollbackSwapsCurrentAndPrevious pins AC6's main line:
// the rollback swaps the two pointers — current on the previous
// version, previous on the one current replaced — after the same drain
// the upgrade takes, and asks for the same restarts.
func TestComponentsRollbackSwapsCurrentAndPrevious(t *testing.T) {
	root := rollbackFixture(t)
	face := &fakeFace{} // idle from the first drain
	if err := runRollback(t, root, face, nil); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if target, err := CurrentTarget(root); err != nil || target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Fatalf("current must point at the previous version, got %s (%v)", target, err)
	}
	if prev, err := os.Readlink(filepath.Join(root, PreviousLinkname)); err != nil || prev != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Fatalf("previous must name the version current replaced, got %s (%v)", prev, err)
	}
	if face.restartCalls() != 1 {
		t.Fatalf("the rollback asks the daemon to restart exactly once, got %d", face.restartCalls())
	}
	s, err := ReadUpgradeStatus(root)
	if err != nil || s == nil {
		t.Fatalf("the rollback must be recorded: %v", err)
	}
	if s.State != UpgradeStateDone || s.Operation != OperationRollback || s.From != "v0.2.5" || s.To != "v0.2.4" {
		t.Fatalf("the rollback's record: %+v", s)
	}
	// the journal of the swap is consumed: an interrupted rollback's
	// remains would be visible.
	if _, err := os.Stat(filepath.Join(root, JournalFilename)); !os.IsNotExist(err) {
		t.Fatalf("the swap's journal must be consumed: %v", err)
	}
}

// TestComponentsRollbackDrainsAndWaitsLikeTheUpgrade pins the ticket's
// Q6 ruling: the rollback goes through the same drain — a busy Host is
// waited out (the status file says so), and the swap happens only
// after the work ended.
func TestComponentsRollbackDrainsAndWaitsLikeTheUpgrade(t *testing.T) {
	root := rollbackFixture(t)
	face := &fakeFace{busy: [][]string{{"default"}, nil}}
	seenWaiting := false
	face.onDrain = func(n int, f *fakeFace) {
		if s, err := ReadUpgradeStatus(root); err == nil && s != nil && s.State == UpgradeStateWaiting {
			seenWaiting = true
		}
	}
	if err := runRollback(t, root, face, nil); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if face.drainCalls() < 2 {
		t.Fatalf("a busy Host must be waited out, got %d drain calls", face.drainCalls())
	}
	if !seenWaiting {
		t.Fatal("the rollback's wait must be visible in the status file")
	}
	if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.4") {
		t.Fatalf("the swap must happen after the work ended, current is %s", target)
	}
}

// TestComponentsRollbackCancelStandsDown pins the cancel's rollback
// half: the marker stands the run down — the daemon is asked to
// restart (the drain ends with it), nothing else changes.
func TestComponentsRollbackCancelStandsDown(t *testing.T) {
	root := rollbackFixture(t)
	before := rootState(t, root)
	face := &fakeFace{busy: [][]string{{"default"}}} // busy forever
	face.onDrain = func(n int, f *fakeFace) {
		if n == 1 {
			if err := RequestUpgradeCancel(root); err != nil {
				t.Errorf("RequestUpgradeCancel: %v", err)
			}
		}
	}
	if err := runRollback(t, root, face, nil); err != nil {
		t.Fatalf("a cancelled rollback is not a failure: %v", err)
	}
	if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.5") {
		t.Fatalf("a cancelled rollback leaves current where it was, got %s", target)
	}
	if got := rootState(t, root); got != before {
		t.Fatalf("a cancelled rollback changes nothing:\n%s", got)
	}
	s, err := ReadUpgradeStatus(root)
	if err != nil || s == nil || s.State != UpgradeStateCancelled {
		t.Fatalf("the cancel must be recorded: %+v (%v)", s, err)
	}
}

// TestComponentsRollbackRefusalsChangeNothing pins AC6's refusal half,
// case by case: every refusal names its reason in the status file,
// leaves both pointers exactly as they were and never reaches the
// daemon (the drain is not set).
func TestComponentsRollbackRefusalsChangeNothing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string // the reason's marker words
	}{
		{
			name:   "no previous at all",
			mutate: func(t *testing.T, root string) { _ = os.Remove(filepath.Join(root, PreviousLinkname)) },
			want:   "previous",
		},
		{
			name: "a corrupt previous",
			mutate: func(t *testing.T, root string) {
				target, err := os.Readlink(filepath.Join(root, PreviousLinkname))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, target, "skills/polyforge/SKILL.md"), []byte("tampered\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not match",
		},
		{
			name: "a cross-channel previous",
			mutate: func(t *testing.T, root string) {
				// previous points at the internal line's v0.2.3-gmi.1
				spec := seedSpec{version: "v0.2.3-gmi.1", minBase: "v0.2.3"}
				writeVersionDir(t, filepath.Join(root, ReleasesDirname, spec.version), spec)
				_ = os.Remove(filepath.Join(root, PreviousLinkname))
				if err := os.Symlink(filepath.Join(ReleasesDirname, spec.version), filepath.Join(root, PreviousLinkname)); err != nil {
					t.Fatal(err)
				}
			},
			want: "channel",
		},
		{
			name: "a base too old for the previous",
			mutate: func(t *testing.T, root string) {
				spec := seedSpec{version: "v0.2.6", minBase: "v0.2.9"}
				writeVersionDir(t, filepath.Join(root, ReleasesDirname, spec.version), spec)
				_ = os.Remove(filepath.Join(root, PreviousLinkname))
				if err := os.Symlink(filepath.Join(ReleasesDirname, spec.version), filepath.Join(root, PreviousLinkname)); err != nil {
					t.Fatal(err)
				}
			},
			want: "base",
		},
		{
			name: "a schema the layout cannot judge",
			mutate: func(t *testing.T, root string) {
				target, err := os.Readlink(filepath.Join(root, PreviousLinkname))
				if err != nil {
					t.Fatal(err)
				}
				body, err := os.ReadFile(filepath.Join(root, target, ManifestFilename))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, target, ManifestFilename),
					[]byte(strings.Replace(string(body), `"schemaVersion": 1`, `"schemaVersion": 2`, 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "schema",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := rollbackFixture(t)
			tc.mutate(t, root)
			before := rootState(t, root)
			face := &fakeFace{}
			err := runRollback(t, root, face, nil)
			if err == nil {
				t.Fatalf("%s: the rollback must be refused", tc.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("%s: the refusal must name %q, got: %v", tc.name, tc.want, err)
			}
			if got := rootState(t, root); got != before {
				t.Fatalf("%s: a refused rollback changes nothing:\n%s", tc.name, got)
			}
			if len(face.calls) != 0 {
				t.Fatalf("%s: a refused rollback never reaches the daemon, got %v", tc.name, face.callVerbs())
			}
			s, readErr := ReadUpgradeStatus(root)
			if readErr != nil || s == nil || s.State != UpgradeStateFailed || s.Error == "" {
				t.Fatalf("%s: the refusal must be recorded with its reason: %+v (%v)", tc.name, s, readErr)
			}
			if strings.Contains(err.Error(), "not implemented") {
				t.Fatalf("%s: the refusal is a stub, not a judgement", tc.name)
			}
		})
	}
}
