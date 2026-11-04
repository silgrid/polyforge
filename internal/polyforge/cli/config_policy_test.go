package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// TestInitMemoryPolicyWritesTheBuiltinBlockOnce pins `pf init`'s cli.toml
// half: a mapped path without a block gains the commented built-in default
// block (its lists equal to the built-in policy), a re-run is
// byte-identical, an existing entry is left alone, an unmapped path writes
// nothing.
func TestInitMemoryPolicyWritesTheBuiltinBlockOnce(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(t.TempDir(), "cli.toml")
	orig := "# operator\nserver = \"http://x\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"alpha\"]\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	how, err := initMemoryPolicy(path, ws)
	if err != nil || how != "inserted" {
		t.Fatalf("first init = %q, %v", how, err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(raw), orig) || !strings.Contains(string(raw), "# memory_policy:") {
		t.Fatalf("the block must be appended with its comment, keeping every byte:\n%s", raw)
	}
	cfg, err := ParseConfigBytes(path, raw)
	if err != nil {
		t.Fatal(err)
	}
	builtin := workspace.DefaultMemoryPolicy()
	if got := cfg.MemoryPolicy[ws]["default"]; !reflect.DeepEqual(got.Record, builtin.Record) || !reflect.DeepEqual(got.Skip, builtin.Skip) {
		t.Fatalf("the written block = %+v, want the built-in lists %+v", got, builtin)
	}
	if how, err := initMemoryPolicy(path, ws); err != nil || how != "unchanged" {
		t.Fatalf("second init = %q, %v", how, err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(raw) {
		t.Fatal("a second init changed cli.toml")
	}
	own := orig + "\n[memory_policy.\"" + ws + "\".alpha]\nrecord = [\"mine\"]\n"
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}
	if how, err := initMemoryPolicy(path, ws); err != nil || how != "unchanged" {
		t.Fatalf("an existing entry: %q, %v", how, err)
	}
	if how, err := initMemoryPolicy(path, t.TempDir()); err != nil || how != "unmapped" {
		t.Fatalf("an unmapped path: %q, %v", how, err)
	}
	if after, _ := os.ReadFile(path); string(after) != own {
		t.Fatal("an unchanged/unmapped init wrote cli.toml")
	}
}
