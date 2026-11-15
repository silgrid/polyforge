package stepcall

// usage_scan_error_test.go (polyforge#47, AC5): ReadUsage closes every
// file it opens — on the scan-error path too (a line over the scanner's
// 16 MiB token bound makes bufio.Scanner fail with ErrTooLong, and the
// early return used to leave the descriptor open: the daemon is
// long-lived, so every read of a poisoned session file leaked one fd).
// The scan error still zeroes the whole summary (the established
// semantics). The leak is pinned on the descriptor itself: with the
// garbage collector paused (the os.File finalizer that would close a
// stray fd cannot run without one), one read of the poisoned file must
// leave no open descriptor to it.

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

// overlongUsageFile answers a session file whose single line exceeds the
// scanner's max token size (16 MiB): sc.Scan() ends with ErrTooLong.
func overlongUsageFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "poisoned.jsonl")
	line := strings.Repeat("x", 16<<20+1)
	if err := os.WriteFile(p, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fdTargets answers the fd number → readlink target map of this process.
func fdTargets(t *testing.T) map[int]string {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("this platform has no /proc/self/fd to read: %v", err)
	}
	out := map[int]string{}
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil {
			out[n] = target
		}
	}
	return out
}

func TestUsageScanErrorClosesEveryFile(t *testing.T) {
	poisoned := overlongUsageFile(t)
	valid := writeUsageFixture(t, `{"type":"assistant","message":{"id":"m1","usage":{"input_tokens":5,"output_tokens":3}}}`)

	// The scan error still discards everything (unchanged semantics): a
	// valid file before the poisoned one does not survive it.
	got := ReadUsage([]string{valid, poisoned}, false)
	if got.Known {
		t.Fatalf("a scan error must keep the summary unknown: %+v", got)
	}

	// The leak: with GC paused (no finalizer can close a stray os.File),
	// one read of the poisoned file must leave no open fd to it.
	old := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(old)
	before := fdTargets(t)
	ReadUsage([]string{poisoned}, false)
	after := fdTargets(t)
	for fd, target := range after {
		if _, had := before[fd]; had {
			continue
		}
		if strings.HasSuffix(target, "poisoned.jsonl") {
			t.Fatalf("fd %d to %s survived ReadUsage: the scan-error path does not close the file", fd, target)
		}
	}
}
