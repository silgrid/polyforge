// Package atomicfile is the one crash-safe "replace this file" primitive
// every state writer in the repo uses.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrDirSync marks a post-rename failure: the file was replaced but the
// parent-directory fsync could not confirm the rename is durable.
var ErrDirSync = errors.New("atomicfile: rename committed but directory fsync failed")

// Write lands data at path: the parent directory is created (0o755), data
// is written to a uniquely named temp file in that directory, fsynced,
// chmodded to perm, renamed over path, and the parent directory is then
// fsynced. The temp file is removed on every failure before the rename.
// A directory-fsync failure is returned wrapping ErrDirSync: the file may
// already be replaced, the on-disk state is uncertain, not unchanged.
func Write(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return fail(err)
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	df, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("%w: open dir: %v", ErrDirSync, err)
	}
	defer df.Close()
	if err := df.Sync(); err != nil {
		return fmt.Errorf("%w: %v", ErrDirSync, err)
	}
	return nil
}
