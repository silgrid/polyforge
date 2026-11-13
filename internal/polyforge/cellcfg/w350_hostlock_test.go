package cellcfg

// w350_hostlock_test.go (D1): the data dir's Host lock is
// exclusive — a second holder (a Host start while a migration runs, or the
// reverse) is refused with ErrHostLockHeld naming daemon.lock — it is
// released by Close, and it never creates a missing data dir.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW350HostLockIsExclusiveAndCreatesNoDataDir(t *testing.T) {
	dir := t.TempDir()
	first, err := HoldHostLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HoldHostLock(dir); !errors.Is(err, ErrHostLockHeld) || !strings.Contains(err.Error(), filepath.Join(dir, HostLockFile)) {
		t.Fatalf("a second holder must be refused naming the lock: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := HoldHostLock(dir)
	if err != nil {
		t.Fatalf("the released lock must be free again: %v", err)
	}
	_ = again.Close()

	missing := filepath.Join(t.TempDir(), "absent")
	if _, err := HoldHostLock(missing); err == nil || errors.Is(err, ErrHostLockHeld) {
		t.Fatalf("a missing data dir must be refused as such: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("the lock created a missing data dir")
	}
}
