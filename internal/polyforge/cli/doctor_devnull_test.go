package cli

// doctor_devnull_test.go: the `null device` row fails when the null device
// is not the platform's character device and names the atomic recovery
// command. Every case reads a stand-in path or the null device's stat;
// nothing here writes to or replaces the real null device.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDoctorNullDeviceRow(t *testing.T) {
	const linuxRecovery = "mknod -m 666 /dev/null.new c 1 3 && mv -f /dev/null.new /dev/null"

	broken := filepath.Join(t.TempDir(), "null")
	if err := os.WriteFile(broken, []byte("bash: line 1: x: command not found\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := devNullCheck(broken)
	if c.Name != docChDevNull {
		t.Errorf("row name: got %q, want %q", c.Name, docChDevNull)
	}
	if c.Status != "fail" {
		t.Errorf("a regular file holding text must fail, got %+v", c)
	}
	if !strings.Contains(c.Detail, "regular file") {
		t.Errorf("the detail must say the path is a regular file, got %q", c.Detail)
	}
	if runtime.GOOS == "linux" && !strings.Contains(c.Detail, linuxRecovery) {
		t.Errorf("the detail must carry the recovery command %q, got %q", linuxRecovery, c.Detail)
	}

	if c := devNullCheck(filepath.Join(t.TempDir(), "missing")); c.Status != "fail" {
		t.Errorf("a missing path must fail, got %+v", c)
	}

	if runtime.GOOS == "linux" {
		// /dev/zero is a character device too (1,5): the wrong device is no pass.
		if c := devNullCheck("/dev/zero"); c.Status != "fail" {
			t.Errorf("/dev/zero must fail, got %+v", c)
		}
	}

	if c := devNullCheck(os.DevNull); c.Status != "pass" {
		t.Errorf("%s must pass on a healthy host, got %+v", os.DevNull, c)
	}

	found := false
	for _, d := range doctorChecks {
		if d.name == docChDevNull {
			found = true
			if d.need != needConfig {
				t.Errorf("the null device row must need the config like its neighbours, got need %v", d.need)
			}
		}
	}
	if !found {
		t.Errorf("the null device row must be registered in doctorChecks")
	}
	for _, h := range harnessDisplayChecks(false, false) {
		if h.Name == docChDevNull {
			t.Errorf("--harness must not run the null device row: %+v", h)
		}
	}
}
