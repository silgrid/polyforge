package cellcfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"cli.toml", "cli-alpha.toml", "cli-default.toml", "cli-Bad.toml", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("Discover: %+v", got)
	}
	byName := map[string]CellFile{}
	for _, c := range got {
		byName[c.Profile] = c
	}
	if byName["alpha"].NameErr != nil || byName["default"].NameErr == nil || byName["Bad"].NameErr == nil {
		t.Fatalf("name validity: %+v", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "instances"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(dir); err == nil {
		t.Fatal("an instances/ directory must be refused by name")
	}
}

func TestMatchCwd(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	sub := filepath.Join(a, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	idx := []IndexEntry{{Profile: "default", Workspace: b}, {Profile: "alpha", Workspace: a}}
	if p, ok := MatchCwd(idx, sub); !ok || p != "alpha" {
		t.Fatalf("descendant cwd: %q %v", p, ok)
	}
	if p, ok := MatchCwd(idx, b); !ok || p != "default" {
		t.Fatalf("exact cwd: %q %v", p, ok)
	}
	if _, ok := MatchCwd(idx, t.TempDir()); ok {
		t.Fatal("an unmapped cwd matched")
	}
	if _, ok := MatchCwd(append(idx, IndexEntry{Profile: "beta", Workspace: a}), sub); ok {
		t.Fatal("two matching Cells must answer no unique match")
	}
}
