package verifyrun

// tmpsweep.go: private TMPDIR ownership and reclamation. A command's TMPDIR
// is removed by a defer, so a verify-run killed hard (the pi harness sends
// SIGKILL to its detached bash children when the daemon tears the step
// down;) leaves it behind. Each directory therefore carries
// an owner flock held for the life of its verify-run process: the kernel
// drops the lock however the process dies, and the next `pf verify-run`
// removes every marked directory whose lock is free. The directory is
// created under a staging name and renamed into the sweep pattern only
// after the lock is held, so a concurrent sweep never sees it unlocked.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	tmpDirPrefix   = "pf-verify-tmp-"
	tmpStagePrefix = ".pf-verify-tmp-"
	tmpOwnerLock   = "owner.lock"
)

// newPrivateTmp makes one command's private TMPDIR and returns the function
// that removes it and releases its owner lock.
func newPrivateTmp() (string, func(), error) {
	stage, err := os.MkdirTemp("", tmpStagePrefix)
	if err != nil {
		return "", nil, err
	}
	lock, err := os.OpenFile(filepath.Join(stage, tmpOwnerLock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(stage)
		return "", nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		_ = os.RemoveAll(stage)
		return "", nil, err
	}
	dir := filepath.Join(os.TempDir(), tmpDirPrefix+strings.TrimPrefix(filepath.Base(stage), tmpStagePrefix))
	if err := os.Rename(stage, dir); err != nil {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		_ = os.RemoveAll(stage)
		return "", nil, err
	}
	release := func() {
		// release before removing: no I/O under the lock, and a sweep
		// that races this removal has the same idempotent target.
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		_ = os.RemoveAll(dir)
	}
	return dir, release, nil
}

// sweepStaleTmp removes every marked private TMPDIR whose owner lock is
// free. A directory without the owner lock is left alone: it carries no
// proof that its owner is gone.
func sweepStaleTmp() {
	root := os.TempDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), tmpDirPrefix) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		lock, err := os.OpenFile(filepath.Join(dir, tmpOwnerLock), os.O_RDWR, 0)
		if err != nil {
			continue
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			_ = os.RemoveAll(dir)
		}
		_ = lock.Close()
	}
}
