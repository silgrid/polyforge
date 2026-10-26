package capsule

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSpawnJournalLoose(t *testing.T) {
	dir := t.TempDir()
	if es, err := ReadSpawnJournal(dir); es != nil || err != nil {
		t.Fatalf("absent journal: %v %v", es, err)
	}
	p := filepath.Join(dir, DirRuntimeState, SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `{"pid":2,"started_at":"s","spawn_n":1}` + "\n" + "not json\n" +
		`{"pid":3,"spawn_n":5,"step_id":"a","transcript":"/t"}` + "\n" + `{"pid":4,"spawn`
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	es, err := ReadSpawnJournal(dir)
	if err != nil || len(es) != 2 || es[1].StepID != "a" || es[1].Transcript != "/t" {
		t.Fatalf("got %+v err=%v", es, err)
	}
	if n, err := SpawnJournalMaxN(dir); n != 5 || err != nil {
		t.Fatalf("max = %d %v", n, err)
	}
}
