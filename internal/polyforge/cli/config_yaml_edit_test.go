package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func TestEditSummarizeYAMLReplacesOnlyTheTargetLine(t *testing.T) {
	before := "# operator comment\nsummarize:\n  models: [\"pi:zeta\", \"pi:alpha\"]\n  interval_sec: 30\n\nsteps:\n  e2e:\n    default: { models: [\"pi:zeta\"] }\n"
	after := editSummarizeYAML(before, "summarize.interval_sec", "10", true)
	if after == before {
		t.Fatal("the edit did not land")
	}
	if n := diffLineCount(before, after); n != 1 {
		t.Fatalf("expected exactly one changed line, got %d:\n%s", n, after)
	}
	for _, keep := range []string{"# operator comment", "models: [\"pi:zeta\", \"pi:alpha\"]", "steps:", "default: { models: [\"pi:zeta\"] }"} {
		if !strings.Contains(after, keep) {
			t.Fatalf("edit dropped %q:\n%s", keep, after)
		}
	}
}

func TestEditSummarizeYAMLUnsetModelsRemovesTheEntry(t *testing.T) {
	before := "summarize:\n  models: [\"pi:a\"]\n  language: \"English\"\n\nsteps:\n  e2e:\n"
	after := editSummarizeYAML(before, "summarize.models", "", false)
	if strings.Contains(after, "summarize:") || strings.Contains(after, "models:") {
		t.Fatalf("unset models must remove the whole entry:\n%s", after)
	}
	if !strings.Contains(after, "steps:") {
		t.Fatalf("the rest of the file must survive:\n%s", after)
	}
}

func diffLineCount(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	n := 0
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			n++
		}
	}
	return n
}

// TestWorkspaceLockStaysLayoutClean pins that the summarize writer's flock
// lives at a dot-entry `pf doctor` tolerates: `.polyforge.yaml.lock` would be
// flagged as a disposable backup, so the lock must not use that prefix.
func TestWorkspaceLockStaysLayoutClean(t *testing.T) {
	ws := t.TempDir()
	yamlPath := filepath.Join(ws, workspace.FileName)
	if err := os.WriteFile(yamlPath, []byte("summarize:\n  models: [\"pi:a\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := lockConfigFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	unlockConfigFile(lock)
	if _, err := os.Stat(filepath.Join(ws, ".polyforge.lock")); err != nil {
		t.Fatalf("the workspace lock must be the tolerated .polyforge.lock: %v", err)
	}
	if _, err := os.Stat(yamlPath + ".lock"); err == nil {
		t.Fatal("the workspace lock must not be the doctor-flagged <yaml>.lock")
	}
	strays, err := workspace.StrayEntries(ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range strays {
		if strings.HasPrefix(s.Path, workspace.FileName+".") || s.Path == "polyforge.lock" {
			t.Fatalf("the lock must not be a stray entry: %v", strays)
		}
	}
}
