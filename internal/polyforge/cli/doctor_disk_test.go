package cli

// doctor_disk_test.go: the "disk watermark" row a
// filesystem used above 70% warns, at or below passes, a filesystem
// shared by two workspaces is reported once, the capsules/ byte count is
// what the tree holds (no symlink followed), and a workspace whose
// filesystem cannot be read is named and warns, never a faked green.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDisk replaces the statfs seam for one test.
func fakeDisk(t *testing.T, answer func(path string) (diskFS, error)) {
	t.Helper()
	orig := diskStat
	diskStat = answer
	t.Cleanup(func() { diskStat = orig })
}

const gib = uint64(1) << 30

func TestDoctorDiskWatermark(t *testing.T) {
	ws := t.TempDir()
	at := time.Now()
	touch(t, filepath.Join(ws, "capsules", "demo-1", "repos", "demo", "a.bin"), at) // 1 byte each
	touch(t, filepath.Join(ws, "capsules", "demo-1", "artifacts", "plan.md"), at)
	if err := os.WriteFile(filepath.Join(ws, "capsules", "demo-2.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	// a symlink out of the tree is not followed
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "big"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "capsules", "demo-1", "link")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		used       uint64
		wantStatus string
		wantPct    string
	}{
		{"below", 700 * gib, "pass", "70% used (700.0 GiB of 1000.0 GiB)"},
		{"above", 701 * gib, "warn", "71% used (701.0 GiB of 1000.0 GiB)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeDisk(t, func(string) (diskFS, error) {
				return diskFS{Dev: 1, Total: 1000 * gib, Used: c.used, Avail: 1000*gib - c.used}, nil
			})
			got := diskCheck([]string{ws})
			if got.Status != c.wantStatus {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Detail, c.wantStatus)
			}
			for _, want := range []string{c.wantPct, ws + " capsules/ 2.0 KiB"} {
				if !strings.Contains(got.Detail, want) {
					t.Fatalf("detail lacks %q: %s", want, got.Detail)
				}
			}
			if above := strings.Contains(got.Detail, "above the 70% watermark"); above != (c.wantStatus == "warn") {
				t.Fatalf("watermark note = %t: %s", above, got.Detail)
			}
		})
	}
}

func TestDoctorDiskSharedFilesystemOnceAndUnreadableNamed(t *testing.T) {
	a, b, gone := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "gone")
	fakeDisk(t, func(path string) (diskFS, error) {
		if path == gone {
			return diskFS{}, errors.New("no such file or directory")
		}
		return diskFS{Dev: 7, Total: 100 * gib, Used: 10 * gib, Avail: 90 * gib}, nil
	})
	got := diskCheck([]string{a, b, gone})
	if got.Status != "warn" {
		t.Fatalf("status = %s (%s), want warn for the unreadable workspace", got.Status, got.Detail)
	}
	if n := strings.Count(got.Detail, "% used ("); n != 1 {
		t.Fatalf("a filesystem shared by two workspaces is reported %d times: %s", n, got.Detail)
	}
	for _, want := range []string{a + " capsules/ 0 B", b + " capsules/ 0 B", "not measured: " + gone + ": no such file"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("detail lacks %q: %s", want, got.Detail)
		}
	}
	if strings.Contains(got.Detail, "watermark —") {
		t.Fatalf("10%% used must not be above the watermark: %s", got.Detail)
	}
}

// TestDoctorDiskRealStatfs: the real seam reads this test's own
// filesystem — a sane, non-empty answer.
func TestDoctorDiskRealStatfs(t *testing.T) {
	d, err := statfsDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d.Total == 0 || d.Used > d.Total || d.usedPct() > 100 {
		t.Fatalf("statfs answer %+v", d)
	}
}
