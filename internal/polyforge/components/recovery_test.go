package components

// recovery_test.go — AC1's interruption clause: a kill at any commit
// stage of the transaction, then the reboot's re-run, converges on
// current complete and previous explainable. The crash matrix drives
// the transaction's own crash points through a child process
// (PF_CRASH_AT SIGKILLs it, the cellcfg migration test's pattern), so
// the states asserted are the real on-disk remains, not simulations.

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
)

// TestInstallCrashMatrix: one child per crash point, then the same
// install again in the parent (what a reboot's first boot does), and
// the converged state is asserted: current on the new complete
// version, previous naming the version it replaced, no journal, no
// staging leftovers.
func TestInstallCrashMatrix(t *testing.T) {
	if os.Getenv("PF_COMPONENTS_CHILD") != "" {
		t.Skip("the child runs TestInstallCrashChild only")
	}
	points := []string{crashStaged, crashPublished, crashJournaled, crashCurrentSwapped}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			root := t.TempDir()
			older := installSeed(t, seedSpec{})
			if _, err := Install(Options{Root: root, Seed: older, BaseVersion: "v0.2.4", Platform: "linux/amd64"}); err != nil {
				t.Fatalf("the v0.2.4 install before the crash: %v", err)
			}
			newer := installSeed(t, seedSpec{version: "v0.2.5", minBase: "v0.2.4"})

			// the child: the upgrade that dies at the crash point.
			cmd := exec.Command(os.Args[0], "-test.run=^TestInstallCrashChild$", "-test.count=1")
			cmd.Env = append(os.Environ(),
				"PF_COMPONENTS_CHILD=1",
				"PF_COMPONENTS_ROOT="+root,
				"PF_COMPONENTS_SEED="+newer,
				crashpoint.EnvVar+"="+point,
			)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the child completed instead of crashing at %s:\n%s", point, out)
			}
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
				t.Fatalf("the child did not die of the crash point %s: %v\n%s", point, err, out)
			}

			// the reboot: the same install again, in the parent, with
			// no crash point configured.
			if _, err := Install(Options{Root: root, Seed: newer, BaseVersion: "v0.2.5", Platform: "linux/amd64"}); err != nil {
				t.Fatalf("the reboot's install after the crash at %s: %v", point, err)
			}

			// the converged state.
			if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.5") {
				t.Errorf("after the crash at %s, current -> %q, want releases/v0.2.5", point, target)
			}
			dir := filepath.Join(root, ReleasesDirname, "v0.2.5")
			m, err := ReadManifest(dir)
			if err != nil {
				t.Fatalf("the converged current's manifest: %v", err)
			}
			if err := m.VerifyTree(dir); err != nil {
				t.Errorf("after the crash at %s, current is not complete: %v", point, err)
			}
			prev, err := os.Readlink(filepath.Join(root, PreviousLinkname))
			if err != nil {
				t.Fatalf("after the crash at %s, previous is absent: %v", point, err)
			}
			if prev != filepath.Join(ReleasesDirname, "v0.2.4") {
				t.Errorf("after the crash at %s, previous -> %q, want releases/v0.2.4 (the version current replaced)", point, prev)
			}
			if _, err := os.Lstat(filepath.Join(root, JournalFilename)); err == nil {
				t.Errorf("after the crash at %s, the journal survived the reboot's install", point)
			}
			if entries, err := os.ReadDir(filepath.Join(root, StagingDirname)); err == nil && len(entries) > 0 {
				t.Errorf("after the crash at %s, staging keeps entries: %d", point, len(entries))
			}
		})
	}
}

// TestInstallCrashChild is the child half of the matrix: it runs the
// upgrade that the crash point kills. It never returns normally — the
// parent treats a normal return as a failure to crash.
func TestInstallCrashChild(t *testing.T) {
	root := os.Getenv("PF_COMPONENTS_ROOT")
	if root == "" {
		t.Skip("not the crash child")
	}
	_, err := Install(Options{
		Root:        root,
		Seed:        os.Getenv("PF_COMPONENTS_SEED"),
		BaseVersion: "v0.2.5",
		Platform:    "linux/amd64",
	})
	t.Fatalf("the crash child completed the install (no crash at %s): %v", os.Getenv(crashpoint.EnvVar), err)
}

// TestRecoverJournalCompletion pins the journal's two rules in
// isolation: a journal whose switch already committed completes
// previous and clears; a journal whose switch never committed is
// discarded with previous untouched.
func TestRecoverJournalCompletion(t *testing.T) {
	committed := func(t *testing.T) string {
		// v0.2.4 then v0.2.5 current; rebuild the moment between the
		// current swap and the previous update by hand.
		root := t.TempDir()
		for _, step := range []struct {
			spec seedSpec
			base string
		}{{seedSpec{}, "v0.2.4"}, {seedSpec{version: "v0.2.5", minBase: "v0.2.4"}, "v0.2.5"}} {
			if _, err := Install(Options{Root: root, Seed: installSeed(t, step.spec), BaseVersion: step.base, Platform: "linux/amd64"}); err != nil {
				t.Fatalf("setup install: %v", err)
			}
		}
		if err := os.Remove(filepath.Join(root, PreviousLinkname)); err != nil {
			t.Fatal(err)
		}
		if err := writeJournal(root, &journal{SchemaVersion: journalSchemaVersion, NewVersion: "v0.2.5", OldVersion: "v0.2.4"}); err != nil {
			t.Fatal(err)
		}
		return root
	}
	t.Run("committed switch: previous completed, journal cleared", func(t *testing.T) {
		root := committed(t)
		if err := recoverPending(root); err != nil {
			t.Fatalf("recoverPending: %v", err)
		}
		prev, err := os.Readlink(filepath.Join(root, PreviousLinkname))
		if err != nil {
			t.Fatalf("previous was not completed: %v", err)
		}
		if prev != filepath.Join(ReleasesDirname, "v0.2.4") {
			t.Errorf("previous -> %q, want releases/v0.2.4", prev)
		}
		if _, err := os.Lstat(filepath.Join(root, JournalFilename)); err == nil {
			t.Error("the journal survived its completed recovery")
		}
	})
	t.Run("uncommitted switch: journal discarded, previous untouched", func(t *testing.T) {
		root := committed(t)
		// wind current back to the old version: the journal's switch
		// never committed.
		if err := swapLink(root, CurrentLinkname, filepath.Join(ReleasesDirname, "v0.2.4")); err != nil {
			t.Fatal(err)
		}
		if err := recoverPending(root); err != nil {
			t.Fatalf("recoverPending: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, JournalFilename)); err == nil {
			t.Error("an uncommitted journal survived")
		}
		if prev, err := os.Readlink(filepath.Join(root, PreviousLinkname)); err == nil {
			t.Errorf("the discarded journal left previous -> %q; an uncommitted switch must not touch it", prev)
		}
		if target, _ := CurrentTarget(root); target != filepath.Join(ReleasesDirname, "v0.2.4") {
			t.Errorf("current -> %q, want it left on the old version", target)
		}
	})
	t.Run("a malformed journal is an error, not a guess", func(t *testing.T) {
		root := committed(t)
		if err := os.WriteFile(filepath.Join(root, JournalFilename), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := recoverPending(root); err == nil {
			t.Error("a malformed journal recovered silently")
		}
	})
}

// TestRecoverCleansStaging pins that staging leftovers — the remains of
// a copy a crash interrupted — never survive into another install.
func TestRecoverCleansStaging(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, StagingDirname, "v0.2.5.999"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, StagingDirname, "v0.2.5.999", "x"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recoverPending(root); err != nil {
		t.Fatalf("recoverPending: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, StagingDirname)); err == nil && len(entries) > 0 {
		t.Errorf("staging leftovers survived: %d entries", len(entries))
	}

	// a root without a staging directory at all is a fine state.
	fresh := t.TempDir()
	if err := recoverPending(fresh); err != nil {
		t.Errorf("recoverPending on a root without staging: %v", err)
	}
}
