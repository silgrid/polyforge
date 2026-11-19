package cli

// w85b_catalog_config_test.go: the Host-only [step_tiers]
// model-candidate catalog (polyforge#143, 85b) — the parse, the
// Host/Cell partition and the inheritance (u1), and `pf config`'s
// addressing of step_tiers.<tier> (u2).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestW85bCatalogParseAndHostOnly (u1): the [step_tiers] table parses
// into canonical candidates, refuses a tier name outside the lane tier
// grammar, an unprefixed candidate, an empty list, a repeated candidate
// and a repeated tier, appears once per file, is refused in a named
// Cell's file naming the file, the line and the key, and is inherited by
// a named Cell's resolved config like every Host-only value. Expected
// shapes come from the ticket (Q1/Q2: one [step_tiers] table, keys
// step_tiers.<tier>, candidates `<harness>:<model>[:<effort>]`) and the
// cli.toml grammar the parser already enforces — never from this
// implementation's own output.
func TestW85bCatalogParseAndHostOnly(t *testing.T) {
	cfg, err := ParseConfigBytes("/tmp/cli.toml", []byte(
		"[step_tiers]\ndefault = [\"pi:provider/model\", \"claude:alias\"]\ncheap = ['pi:small:low']\n"))
	if err != nil {
		t.Fatalf("a well-formed catalog must parse: %v", err)
	}
	if got := cfg.StepTiers["default"]; len(got) != 2 || got[0] != "pi:provider/model" || got[1] != "claude:alias" {
		t.Fatalf("default tier = %q, want the two candidates as written", got)
	}
	if got := cfg.StepTiers["cheap"]; len(got) != 1 || got[0] != "pi:small:low" {
		t.Fatalf("cheap tier = %q, want the candidate with its effort", got)
	}
	for _, bad := range []struct {
		name, body, want string
	}{
		{"tier name outside the lane tier grammar", "[step_tiers]\nDefault = [\"pi:x\"]\n", "Default"},
		{"unprefixed candidate", "[step_tiers]\ndefault = [\"gpt\"]\n", "gpt"},
		{"empty candidate list", "[step_tiers]\ndefault = []\n", "default"},
		{"repeated candidate", "[step_tiers]\ndefault = [\"pi:a\", \"pi:a\"]\n", "repeats"},
		{"repeated tier", "[step_tiers]\ndefault = [\"pi:a\"]\ndefault = [\"pi:b\"]\n", "duplicate key"},
		{"repeated table", "[step_tiers]\n[step_tiers]\ndefault = [\"pi:a\"]\n", "duplicate [step_tiers]"},
	} {
		_, err := ParseConfigBytes("/tmp/cli.toml", []byte(bad.body))
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%s must refuse naming %q: %v", bad.name, bad.want, err)
		}
	}
	// the Host/Cell partition: the Host file carries the catalog, a
	// named Cell's file refuses it naming the file, the line and the key
	_, err = ParseConfigBytes("/tmp/cli-alpha.toml", []byte("[step_tiers]\ndefault = [\"pi:x\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "/tmp/cli-alpha.toml") || !strings.Contains(err.Error(), "line 1") ||
		!strings.Contains(err.Error(), "[step_tiers]") || !strings.Contains(err.Error(), "Host-only key") {
		t.Fatalf("a named Cell's file must refuse the catalog naming the file, the line and the key: %v", err)
	}
	// a named Cell's resolved config inherits the Host's catalog
	host, err := ParseConfigBytes("/tmp/cli.toml", []byte("[step_tiers]\ndefault = [\"pi:h/model\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cellPath := filepath.Join(dir, "cli-alpha.toml")
	if err := os.WriteFile(cellPath, []byte("server = \"http://alpha.invalid:1\"\napi_key = \"k\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cell, err := LoadCellFile(host, cellPath)
	if err != nil {
		t.Fatalf("LoadCellFile: %v", err)
	}
	if got := cell.StepTiers["default"]; len(got) != 1 || got[0] != "pi:h/model" {
		t.Fatalf("the Cell must inherit the Host's catalog: %q", got)
	}
}
