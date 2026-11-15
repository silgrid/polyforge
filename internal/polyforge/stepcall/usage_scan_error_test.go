package stepcall

// usage_scan_error_test.go (polyforge#47, AC5): ReadUsage closes every
// file it opens — on the scan-error path too (a line over the scanner's
// 16 MiB token bound makes bufio.Scanner fail with ErrTooLong, and the
// early return used to leave the descriptor open: the daemon is
// long-lived, so every read of a poisoned session file leaked one fd).
// The scan error still zeroes the whole summary (the established
// semantics); the leak is what the fix removes, so the test calls
// ReadUsage in a loop and counts /proc/self/fd.

import (
	"os"
	"path/filepath"
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

// fdCount answers how many file descriptors this process holds.
func fdCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("this platform has no /proc/self/fd to count: %v", err)
	}
	return len(entries)
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

	// The leak: reading the poisoned file over and over must not grow
	// the descriptor count (before the fix each read left one open).
	ReadUsage([]string{poisoned}, false) // warm up: one lazy fd is the tolerance
	before := fdCount(t)
	const rounds = 40
	for i := 0; i < rounds; i++ {
		if got := ReadUsage([]string{poisoned}, false); got.Known {
			t.Fatalf("round %d: the poisoned file must stay unknown: %+v", i, got)
		}
	}
	if grew := fdCount(t) - before; grew > 2 {
		t.Fatalf("%d reads of the scan-error file grew the fd count by %d (before %d, after %d): the file is not closed on the error path", rounds, grew, before, fdCount(t))
	}
}
