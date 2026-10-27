package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteReplacesAndSetsPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "f.json")
	for _, body := range []string{"one", "two"} {
		if err := Write(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != body {
			t.Fatalf("got %q, %v; want %q", got, err, body)
		}
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", fi.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}

func TestWriteReadonlyPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ro")
	if err := Write(p, []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("y"), 0o444); err != nil {
		t.Fatalf("replace of read-only file: %v", err)
	}
}

func TestWriteFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	// target is a directory: rename must fail and the temp must be removed
	p := filepath.Join(dir, "target")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("x"), 0o644); err == nil {
		t.Fatal("expected error")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}

func TestWriteConcurrentNoCollision(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- Write(p, []byte(fmt.Sprintf("writer-%02d", i)), 0o644)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}
	got, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(got), "writer-") || len(got) != len("writer-00") {
		t.Fatalf("torn content %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}
