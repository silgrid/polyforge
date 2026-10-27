package cli

// config_write.go: the crash-atomic, flock-serialized cli.toml writer.
// The daemon-UDS preflight runs BEFORE the flock is taken; the flock then
// serializes the local read-modify-write — the read, the validation of
// the edited bytes (which probes the mapped workspaces) and the atomic
// write all run under it, which is exactly why the lock exists. It is a
// leaf the daemon never takes, so it cannot invert with the job-table
// lock. The daemon notification runs after the lock is released.

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// lockConfigFile takes the writer flock for path and returns the held handle.
// Concurrent `pf config` writers serialize here; the daemon never takes this
// lock (it is a leaf on the CLI side). A workspace `.polyforge.yaml` uses a
// sibling `.polyforge.lock` — a dot-entry the workspace layout convention
// tolerates — because `<yaml>.lock` would match `pf doctor`'s disposable
// `.polyforge.yaml.` backup prefix. The lock directory is never created for a
// workspace yaml (a mapped workspace must already exist).
func lockConfigFile(path string) (*os.File, error) {
	lockPath := path + ".lock"
	if filepath.Base(path) == workspace.FileName {
		lockPath = filepath.Join(filepath.Dir(path), ".polyforge.lock")
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// unlockConfigFile releases the writer flock.
func unlockConfigFile(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// writeConfigAtomic lands data through the one atomic-file primitive with
// the two config crashpoints (PF_CRASH_AT=config.before-rename /
// config.after-rename). An existing file keeps its mode; a new file is
// 0600.
func writeConfigAtomic(path string, data []byte) error {
	perm := os.FileMode(0o600)
	if m, ok := atomicfile.ReadMode(path); ok {
		perm = m
	}
	return atomicfile.WriteHooked(path, data, perm,
		func() { crashpoint.At("config.before-rename") },
		func() { crashpoint.At("config.after-rename") })
}

// readOneLine reads one line (without its terminator) from r.
func readOneLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
