package runtime

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSealReadOnlyKeepsExecutableBits pins the seal's mode rule: it clears
// write bits instead of forcing a fixed 0444, so an executable file seals as
// 0555 and stays runnable. A fixed 0444 made execve fail with EACCES, and a
// dependency tree hard-linked from a sealed capsule inherited that mode.
func TestSealReadOnlyKeepsExecutableBits(t *testing.T) {
	root := t.TempDir()
	defer unseal(t, root)

	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "data.txt")
	if err := os.WriteFile(plain, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(plain, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := sealReadOnly(root); err != nil {
		t.Fatalf("sealReadOnly: %v", err)
	}

	for path, want := range map[string]os.FileMode{
		root:  0o555,
		dir:   0o555,
		exe:   0o555,
		plain: 0o444,
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("sealed %s mode = %04o, want %04o", path, got, want)
		}
	}
	if out, err := exec.Command(exe).CombinedOutput(); err != nil {
		t.Fatalf("a sealed executable must still run: %v (%s)", err, out)
	}
}

// TestSealReadOnlyHardlinkedExecutableStaysRunnable pins the failing shape: a
// dependency tree taken from a sealed scene through a hard link. The link
// shares the inode's mode, so an executable sealed 0444 failed execve with
// EACCES; with write bits alone cleared it stays 0555 and runs.
func TestSealReadOnlyHardlinkedExecutableStaysRunnable(t *testing.T) {
	root := t.TempDir()
	defer unseal(t, root)

	sealed := filepath.Join(root, "sealed")
	if err := os.MkdirAll(filepath.Join(sealed, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(sealed, "bin", "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tool, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := sealReadOnly(sealed); err != nil {
		t.Fatalf("sealReadOnly: %v", err)
	}

	copied := filepath.Join(root, "copied")
	if err := os.Mkdir(copied, 0o755); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(copied, "tool")
	if err := os.Link(tool, linked); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(linked).CombinedOutput(); err != nil {
		t.Fatalf("a hard link of a sealed executable must still run: %v (%s)", err, out)
	}
}
