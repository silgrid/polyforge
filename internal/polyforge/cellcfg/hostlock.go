package cellcfg

// hostlock.go: the data dir's single-instance lock (daemon.lock at the
// root): a Host holds it for its whole life, and `pf migrate cell-layout`
// holds it for the whole of a mutating run (--apply, --rollback,
// --restore-backup --confirm), so a Host start and a migration can never
// interleave — whichever comes second is refused before it reads or
// writes any Cell state. The kernel drops the lock with the holder's
// process (crash == shutdown).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// HostLockFile is the lock file at the data dir's root.
const HostLockFile = "daemon.lock"

// ErrHostLockHeld is HoldHostLock's answer when another process holds the
// lock.
var ErrHostLockHeld = errors.New("the data-dir lock is held by another process")

// HoldHostLock takes the data dir's lock (LOCK_EX|LOCK_NB, creating the
// lock file) and answers the open file whose Close releases it. It never
// creates the data dir: a missing one is refused. A held lock answers an
// error wrapping ErrHostLockHeld that names the lock file.
func HoldHostLock(dataDir string) (*os.File, error) {
	fi, err := os.Stat(dataDir)
	if err != nil {
		return nil, fmt.Errorf("the data dir %s: %w", dataDir, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("the data dir %s is not a directory", dataDir)
	}
	path := filepath.Join(dataDir, HostLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s", ErrHostLockHeld, path)
	}
	return f, nil
}
