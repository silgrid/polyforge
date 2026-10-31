package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	data := t.TempDir()
	s := NewStore(data)
	if s.Dir() != filepath.Join(data, ReportsDirName) {
		t.Fatalf("store dir %q", s.Dir())
	}
	now := time.Now().UTC().Truncate(time.Second)
	tk := Task{
		ID: "task_0123456789abcdef", Spec: Spec{Project: "demo", Prompt: "hi", MaxDuration: time.Minute, MaxTokens: 5},
		State: StateCompleted, CreatedAt: now, StartedAt: now, EndedAt: now.Add(time.Second),
		Candidate: "pi:m", WallConsumed: time.Second, TokensConsumed: 3,
		Report: "the full report body", Truncated: true,
	}
	if err := s.Save(tk); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(tk.ID)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.ID != tk.ID || got.State != StateCompleted || got.Report != tk.Report || !got.Truncated || got.Project != "demo" {
		t.Fatalf("round trip lost fields: %+v", got)
	}
	// No temp files left behind.
	ents, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != tk.ID+".json" {
			t.Fatalf("unexpected store entry %q (atomic write must leave no temp)", e.Name())
		}
	}
	if _, ok, _ := s.Load("task_ffffffffffffffff"); ok {
		t.Fatal("an absent report must answer not-found")
	}
}

// TestStoreKeepsCallerAndReporter: the spawn-time caller id and reporter
// snapshot survive a Save/Load round trip under their spec keys.
func TestStoreKeepsCallerAndReporter(t *testing.T) {
	s := NewStore(t.TempDir())
	tk := Task{
		ID: "task_00000000000000a1", State: StateCompleted, CreatedAt: time.Now().UTC(),
		Spec: Spec{Project: "demo", MaxDuration: time.Minute, MaxTokens: 5,
			CallerID: "usr_carol", Reporter: "Carol"},
	}
	if err := s.Save(tk); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), tk.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"caller_id":"usr_carol"`, `"reporter":"Carol"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("stored record lacks %s: %s", want, raw)
		}
	}
	got, ok, err := s.Load(tk.ID)
	if err != nil || !ok {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if got.CallerID != "usr_carol" || got.Reporter != "Carol" {
		t.Fatalf("round trip lost caller/reporter: %+v", got.Spec)
	}
}

func TestStoreListOrderAndEmpty(t *testing.T) {
	s := NewStore(t.TempDir())
	if got, err := s.List(); err != nil || len(got) != 0 {
		t.Fatalf("empty store: %v %v", got, err)
	}
	base := time.Now().UTC()
	for i, id := range []string{"task_0000000000000001", "task_0000000000000002", "task_0000000000000003"} {
		if err := s.Save(Task{ID: id, CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "task_0000000000000001" || got[2].ID != "task_0000000000000003" {
		t.Fatalf("list must be oldest-first: %+v", got)
	}
}

func TestStoreRetention(t *testing.T) {
	s := NewStore(t.TempDir())
	now := time.Now().UTC()
	// 60 recent entries: the newest 50 stay, the oldest 10 go.
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("task_%016x", i)
		if err := s.Save(Task{ID: id, CreatedAt: now.Add(-time.Duration(60-i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	// One report older than 7 days: it goes regardless of rank.
	old := Task{ID: "task_00000000000000ff", CreatedAt: now.Add(-8 * 24 * time.Hour)}
	if err := s.Save(old); err != nil {
		t.Fatal(err)
	}
	removed, err := s.Prune(now)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 11 {
		t.Fatalf("pruned %d, want 11", removed)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != RetentionMaxEntries {
		t.Fatalf("kept %d, want %d", len(got), RetentionMaxEntries)
	}
	for _, tk := range got {
		if tk.ID == old.ID {
			t.Fatal("a report older than 7 days must be pruned")
		}
	}
	// The newest entry survives.
	if _, ok, _ := s.Load(fmt.Sprintf("task_%016x", 59)); !ok {
		t.Fatal("the newest entry must survive")
	}
	// Pruning is idempotent.
	if removed, err := s.Prune(now); err != nil || removed != 0 {
		t.Fatalf("second prune: %d %v", removed, err)
	}
}
