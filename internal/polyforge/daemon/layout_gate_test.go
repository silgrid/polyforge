package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
)

// TestServingAMigratedDirClosesTheRollbackWindow: the Host stamps the
// rollback-window marker when it first serves a migrated data dir — before
// any Cell restores a job, queues a submit, records a task report or
// writes a subscription there — so `pf migrate cell-layout --rollback` is
// refused from the first serving start on; a data dir no migration ran on
// gets no marker.
func TestServingAMigratedDirClosesTheRollbackWindow(t *testing.T) {
	fresh := t.TempDir()
	if err := stampServedLayout(fresh); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cellcfg.CellDir(fresh, cellcfg.DefaultProfile), cellcfg.AcceptedWorkMarker)); !os.IsNotExist(err) {
		t.Fatalf("a never-migrated dir must get no marker: %v", err)
	}

	dir := t.TempDir()
	for name, body := range map[string]string{"daemon-id": "m-old\n", "chat-subs.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cellcfg.ApplyMigration(dir); err != nil {
		t.Fatal(err)
	}
	if err := stampServedLayout(dir); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cellcfg.CellDir(dir, cellcfg.DefaultProfile), cellcfg.AcceptedWorkMarker)
	first, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("serving a migrated dir must stamp the marker: %v", err)
	}
	if err := stampServedLayout(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(marker); string(again) != string(first) {
		t.Fatal("a later start must keep the first stamp")
	}
	if err := cellcfg.RollbackMigration(dir); err == nil || !strings.Contains(err.Error(), "repair forward") {
		t.Fatalf("rollback after the Host served the new layout must be refused naming forward repair: %v", err)
	}
}
