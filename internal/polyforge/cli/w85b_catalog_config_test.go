package cli

// w85b_catalog_config_test.go: the Host-only [step_tiers]
// model-candidate catalog (polyforge#143, 85b) — the parse, the
// Host/Cell partition and the inheritance (u1), and `pf config`'s
// addressing of step_tiers.<tier> (u2).

import (
	"encoding/json"
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
		{"empty candidate list", "[step_tiers]\ndefault = []\n", "is empty"},
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

// TestW85bCatalogSetUnsetList (u2): `pf config` addresses the catalog —
// set writes one tier's row (the value a JSON list, the candidates
// normalized to canonical form, every unrelated byte preserved), unset
// removes the row and the emptied table, get refuses pointing at list
// (the one read face, the [summarize] table's rule), `list --json --host`
// answers one item per tier carrying key, class and schema
// {kind:"model-list"} (every item carries class — Pi Web's list gate
// drops a whole list that misses one), and a named Cell's scope refuses
// the Host-only family. Expected values are the candidates this test
// writes; the class and schema kinds come from the ticket and the
// configListItem shape the older keys already pin.
func TestW85bCatalogSetUnsetList(t *testing.T) {
	dir := cellScopeFixture(t)
	hostPath := filepath.Join(dir, "cli.toml")

	if code, out, errOut := runPfCode("config", "set", "step_tiers.default", ` ["pi:prov/main", "claude:sonnet"]`, "--host"); code != 0 {
		t.Fatalf("set: exit %d (%s%s)", code, out, errOut)
	}
	raw, _ := os.ReadFile(hostPath)
	if !strings.Contains(string(raw), "[step_tiers]") || !strings.Contains(string(raw), `default = ["pi:prov/main", "claude:sonnet"]`) {
		t.Fatalf("the set must write the tier's row inside [step_tiers]:\n%s", raw)
	}
	if code, out, errOut := runPfCode("config", "set", "step_tiers.cheap", ` [" pi:flash : low "]`, "--host"); code != 0 {
		t.Fatalf("set (normalize): exit %d (%s%s)", code, out, errOut)
	}
	raw, _ = os.ReadFile(hostPath)
	if !strings.Contains(string(raw), `cheap = ["pi:flash:low"]`) {
		t.Fatalf("a candidate must land in canonical form:\n%s", raw)
	}
	if !strings.Contains(string(raw), "max_concurrent_jobs = 2") {
		t.Fatalf("an unrelated row must survive the set:\n%s", raw)
	}

	if code, _, errOut := runPfCode("config", "get", "step_tiers.default"); code == 0 || !strings.Contains(errOut, "config list") {
		t.Fatalf("get must refuse pointing at `pf config list`: exit %d %q", code, errOut)
	}

	code, out, _ := runPfCode("--json", "config", "list", "--host")
	var doc struct {
		Items []struct {
			Key    string         `json:"key"`
			Value  any            `json:"value"`
			Class  string         `json:"class"`
			Schema map[string]any `json:"schema"`
		} `json:"items"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatalf("list --json --host: exit %d %q", code, out)
	}
	byKey := map[string]struct {
		Value any
		Class string
		Kind  any
	}{}
	for _, it := range doc.Items {
		if it.Class == "" {
			t.Fatalf("every list item must carry class (Pi Web's list gate drops the whole list otherwise): %+v", it)
		}
		var kind any
		if it.Schema != nil {
			kind = it.Schema["kind"]
		}
		byKey[it.Key] = struct {
			Value any
			Class string
			Kind  any
		}{it.Value, it.Class, kind}
	}
	d, ok := byKey["step_tiers.default"]
	if !ok || d.Class != "live" || d.Kind != "model-list" {
		t.Fatalf("step_tiers.default item = %+v, want class live and schema kind model-list", d)
	}
	list, _ := d.Value.([]any)
	if len(list) != 2 || list[0] != "pi:prov/main" || list[1] != "claude:sonnet" {
		t.Fatalf("step_tiers.default value = %v, want the two candidates as written", d.Value)
	}
	if _, ok := byKey["step_tiers.cheap"]; !ok {
		t.Fatalf("every tier must answer one list item: %v", byKey)
	}

	code, out, _ = runPfCode("config", "list", "--host")
	if code != 0 || !strings.Contains(out, `step_tiers.default = ["pi:prov/main", "claude:sonnet"] (source: file, class: live)`) {
		t.Fatalf("the text list must render the tier row: exit %d %q", code, out)
	}

	if code, out, errOut := runPfCode("config", "unset", "step_tiers.default", "--host"); code != 0 {
		t.Fatalf("unset: exit %d (%s%s)", code, out, errOut)
	}
	if code, out, errOut := runPfCode("config", "unset", "step_tiers.cheap", "--host"); code != 0 {
		t.Fatalf("unset: exit %d (%s%s)", code, out, errOut)
	}
	raw, _ = os.ReadFile(hostPath)
	if strings.Contains(string(raw), "step_tiers") {
		t.Fatalf("the emptied table must go with its last row:\n%s", raw)
	}

	if code, _, errOut := runPfCode("--profile", "alpha", "config", "set", "step_tiers.default", ` ["pi:x"]`); code == 0 || !strings.Contains(errOut, "Host-only") {
		t.Fatalf("a named Cell's scope must refuse the Host-only family: exit %d %q", code, errOut)
	}
	if code, _, errOut := runPfCode("config", "set", "step_tiers.BadName", ` ["pi:x"]`, "--host"); code == 0 || !strings.Contains(errOut, "step_tiers.BadName") {
		t.Fatalf("a tier outside the grammar must be refused naming the key: exit %d %q", code, errOut)
	}
	if code, _, errOut := runPfCode("config", "set", "step_tiers.default", ` ["gpt"]`, "--host"); code == 0 || !strings.Contains(errOut, "gpt") {
		t.Fatalf("a candidate without a harness prefix must be refused by the proposed-bytes parse: exit %d %q", code, errOut)
	}
}
