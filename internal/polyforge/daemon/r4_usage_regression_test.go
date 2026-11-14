package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestR4LiveUsageUsesLocator(t *testing.T) {
	capDir := t.TempDir()
	live := filepath.Join(t.TempDir(), "live.jsonl")
	if err := os.MkdirAll(filepath.Join(capDir, capsule.DirRuntimeState), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := capsule.SpawnJournalEntry{StepID: "build", SpawnN: 2, StartedAt: "2026-01-01T00:01:00Z", Transcript: filepath.Join(capDir, capsule.DirSession, "old.jsonl")}
	raw, _ := json.Marshal(entry)
	if err := os.WriteFile(filepath.Join(capDir, capsule.DirRuntimeState, capsule.SpawnJournalFile), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(`{"type":"assistant","message":{"id":"new","usage":{"input_tokens":20,"output_tokens":2}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{locateTranscript: func(string, []capsule.SpawnJournalEntry) string { return live }}
	got := handoffUsage(d, capDir, "build", 2)
	if !got.Known || got.Input != 20 || got.Output != 2 {
		t.Fatalf("usage = %+v", got)
	}
}
