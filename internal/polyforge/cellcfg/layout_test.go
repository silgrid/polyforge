package cellcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLayout(t *testing.T) {
	fresh := t.TempDir()
	isFresh, err := CheckLayout(fresh)
	if err != nil || !isFresh {
		t.Fatalf("an empty data dir is fresh: %v %v", isFresh, err)
	}
	if err := InitLayout(fresh); err != nil {
		t.Fatal(err)
	}
	if isFresh, err := CheckLayout(fresh); err != nil || isFresh {
		t.Fatalf("an initialized data dir passes: %v %v", isFresh, err)
	}

	old := t.TempDir()
	if err := os.WriteFile(filepath.Join(old, "daemon-id"), []byte("m-x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = CheckLayout(old)
	if err == nil || !strings.Contains(err.Error(), "pf migrate cell-layout") {
		t.Fatalf("the pre-Cell layout must be refused naming the migration: %v", err)
	}

	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, LayoutVersionFile), []byte("cells-v9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckLayout(other); err == nil {
		t.Fatal("a mismatching layout version must be refused")
	}
}
