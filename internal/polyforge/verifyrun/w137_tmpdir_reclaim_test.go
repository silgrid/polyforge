package verifyrun

// w137_tmpdir_reclaim_test.go: AC4 — a run killed by SIGKILL
// cannot execute its own deferred TMPDIR removal. The next verify-run
// reclaims the marked directories whose owner lock is free, and leaves a
// live run's directory alone.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestW137StalePrivateTmpReclaimed(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	dead := filepath.Join(root, tmpDirPrefix+"dead")
	if err := os.Mkdir(dead, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dead, tmpOwnerLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	live, release, err := newPrivateTmp()
	if err != nil {
		t.Fatal(err)
	}
	sweepStaleTmp()
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("a dead owner's TMPDIR %s survived the sweep: %v", dead, err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("a live owner's TMPDIR %s was swept: %v", live, err)
	}
	release()
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Errorf("the live owner's TMPDIR %s survived its release: %v", live, err)
	}
}
