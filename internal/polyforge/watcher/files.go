package watcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// ChatIDRe is the legal shape of a chat id (it names files).
var ChatIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// RuntimeDir is the per-user directory for the pidfile, lock and status
// file: $XDG_RUNTIME_DIR/polyforge, else a 0700 dir under the temp dir
// keyed by uid.
func RuntimeDir() string {
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		return filepath.Join(x, "polyforge")
	}
	return tmpFallbackDir()
}

func tmpFallbackDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("polyforge-%d", os.Getuid()))
}

// EnsureDir creates dir (0700). The shared-/tmp fallback is a name any
// local user can pre-create, so it is also verified: it must be a real
// directory (not a symlink) owned by the current uid and not group/world
// writable — otherwise it is refused, never used.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if dir != tmpFallbackDir() {
		return nil
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("runtime dir %s is not a directory (refusing to use it)", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("runtime dir %s is not owned by uid %d (refusing to use it)", dir, os.Getuid())
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("runtime dir %s is group/world-writable (mode %o; refusing to use it)", dir, fi.Mode().Perm())
	}
	return nil
}

// PIDInfo is the pidfile content.
type PIDInfo struct {
	PID  int    `json:"pid"`
	Chat string `json:"chat"`
}

// Files are one chat's runtime files.
type Files struct {
	Dir  string
	Chat string
}

// NewFiles addresses the runtime files of chat in dir (RuntimeDir() when
// dir is empty).
func NewFiles(dir, chat string) Files {
	if dir == "" {
		dir = RuntimeDir()
	}
	return Files{Dir: dir, Chat: chat}
}

func (f Files) lockPath() string { return filepath.Join(f.Dir, "chat-"+f.Chat+".lock") }
func (f Files) pidPath() string  { return filepath.Join(f.Dir, "chat-"+f.Chat+".pid") }

// StatusPath is the chat's progress file: the resident watcher writes
// it, `--statusline` reads it.
func (f Files) StatusPath() string {
	return filepath.Join(f.Dir, "chat-"+f.Chat+".status")
}

func (f Files) ensureDir() error { return EnsureDir(f.Dir) }

// ErrLocked means another watcher holds the chat's lock.
var ErrLocked = errors.New("another watcher is already running for this chat")

// Lock takes the chat's exclusive lock (one watcher per chat), waiting up
// to wait for a previous holder that is exiting. The returned release func
// drops the lock and removes the pidfile.
func (f Files) Lock(wait time.Duration) (release func(), err error) {
	if err := f.ensureDir(); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(f.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			lf.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				if info, ok := f.ReadPID(); ok {
					return nil, fmt.Errorf("%w (pid %d)", ErrLocked, info.PID)
				}
				return nil, ErrLocked
			}
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		_ = os.Remove(f.pidPath())
		_ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
		lf.Close()
	}, nil
}

// WritePID records the running watcher.
func (f Files) WritePID() error {
	blob, _ := json.Marshal(PIDInfo{PID: os.Getpid(), Chat: f.Chat})
	return writeAtomic(f.pidPath(), blob, 0o600)
}

// ReadPID reads the pidfile; ok is false when absent or unparsable.
func (f Files) ReadPID() (PIDInfo, bool) {
	raw, err := os.ReadFile(f.pidPath())
	if err != nil {
		return PIDInfo{}, false
	}
	var info PIDInfo
	if json.Unmarshal(raw, &info) != nil || info.PID <= 0 {
		return PIDInfo{}, false
	}
	return info, true
}

// Alive reports whether the pidfile names a live process.
func (info PIDInfo) Alive() bool {
	if info.PID <= 0 {
		return false
	}
	err := syscall.Kill(info.PID, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// WriteStatus replaces the progress file with one line per entry.
func (f Files) WriteStatus(lines []string) error {
	data := []byte(strings.Join(lines, "\n"))
	if len(lines) > 0 {
		data = append(data, '\n')
	}
	return writeAtomic(f.StatusPath(), data, 0o600)
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(path, data, perm)
}

// ReadStatusline answers what `--statusline` prints: the progress file of
// the chat's watcher. It consults the pidfile for liveness, never the
// daemon. A watcher that is gone and a file that has not been refreshed
// within maxStale print nothing.
func (f Files) ReadStatusline(maxStale time.Duration) string {
	path := f.StatusPath()
	alive := false
	if info, ok := f.ReadPID(); ok {
		alive = info.Alive()
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if !alive && time.Since(st.ModTime()) > maxStale {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}
