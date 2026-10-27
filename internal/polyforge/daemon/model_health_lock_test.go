package daemon

// model_health_lock_test.go: workbench#14 — the model-health snapshot is a
// data-dir write, so it must happen only after Run holds the single-instance
// flock. A start the guard refuses must not touch (or create) the running
// daemon's model-health.json.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
)

func TestNewAndRefusedRunWriteNoModelHealthSnapshot(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	sock := filepath.Join(dir, "d.sock")
	for _, p := range []string{filepath.Join(ws, "capsules"), data, hubBase} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Another daemon owns this data dir: hold the same flock Run takes.
	lock, err := acquireDataDirLock(data)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	cfg := e2eConfig(sock, ws, data, hubBase, newFakeHub(), &parkedSpawner{release: make(chan struct{})})
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.modelHealthTracker() != nil {
		t.Fatal("New opened the model-health tracker before Run held the data-dir lock")
	}
	if err := d.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "another daemon") {
		t.Fatalf("Run = %v, want the single-instance guard refusal", err)
	}
	path := filepath.Join(data, modelhealth.ProfileFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused start wrote %s: stat err = %v", path, err)
	}
}
