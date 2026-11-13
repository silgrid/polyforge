// carries: internal/polyforge/cli/*
// carries: internal/polyforge/workspace/*
//
// w286_config_cli_toml_e2e_test.go: end-to-end acceptance for
// the pf half of moving summarize and memory_policy out of each workspace's
// `.polyforge.yaml` and into cli.toml. Authored against artifacts/spec.md
// before the implementation lands; every assertion is driven through the real
// pf binary, the real cli.toml/yaml files and the real filesystem, never an
// engine internal. The baseline reds are assertion-level (an unknown table, a
// missing field, an unknown subcommand), never build reds: this file compiles
// and vets against the tree as it stands.
//
// Each of the ten scenarios prints a single PASS marker only after all of its
// assertions pass (reused from the pf-authored suite per the
// grill row 6 ruling, with the pi-web scenarios dropped):
//
//	PASS W286-S1 ... through PASS W286-S10 ...
package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

type w286WS struct {
	path     string
	projects []string
}

// w286Config writes a hermetic cli.toml (mapping + optional trailing tables)
// and returns its path.
func w286Config(t *testing.T, dir string, ws []w286WS, extra string) string {
	t.Helper()
	entries := make([]w113WS, len(ws))
	for i, w := range ws {
		entries[i] = w113WS{key: w.path, projects: w.projects}
	}
	path := w113ConfigFile(t, filepath.Join(dir, "cli.toml"), "http://127.0.0.1:1", "w286-key", "", entries)
	if extra != "" {
		w286Write(t, path, string(w286Read(t, path))+extra)
	}
	return path
}

func w286Write(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func w286Read(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// w286Run runs the real pf binary once and returns (combined output, exit).
func w286Run(t *testing.T, e w113Env, args ...string) (string, int) {
	t.Helper()
	return w3PF(t, nil, e, "", args...)
}

// w286OK runs pf and fails the test on a non-zero exit.
func w286OK(t *testing.T, what string, e w113Env, args ...string) string {
	t.Helper()
	out, code := w286Run(t, e, args...)
	if code != 0 {
		t.Fatalf("%s: pf %v exited %d, want 0; output:\n%s", what, args, code, out)
	}
	return out
}

// w286JSON decodes one --json document, tolerating surrounding text.
func w286JSON(t *testing.T, what, out string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &doc); err != nil {
		t.Fatalf("%s is not a JSON document: %v; output:\n%s", what, err, out)
	}
	return doc
}

// w286Item answers one `config list` item by key.
func w286Item(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	items, ok := doc["items"].([]any)
	if !ok {
		t.Fatalf("config list carries no items array: %v", doc)
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if ok && item["key"] == key {
			return item
		}
	}
	t.Fatalf("config list carries no item %q; document: %v", key, doc)
	return nil
}

// w286Value answers one item's value.
func w286Value(t *testing.T, doc map[string]any, key string) any {
	t.Helper()
	return w286Item(t, doc, key)["value"]
}

// w286Strings renders a JSON string array as []string.
func w286Strings(t *testing.T, what string, raw any) []string {
	t.Helper()
	arr, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s is not a JSON array: %#v", what, raw)
	}
	out := make([]string, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s[%d] is not a string: %#v", what, i, v)
		}
		out[i] = s
	}
	return out
}

// w286PolicyEntry answers one path/entry block of the memory_policy read.
func w286PolicyEntry(t *testing.T, doc map[string]any, path, entry string) map[string]any {
	t.Helper()
	policy, ok := doc["memory_policy"].(map[string]any)
	if !ok {
		t.Fatalf("config list carries no memory_policy object: %v", doc)
	}
	perPath, ok := policy[path].(map[string]any)
	if !ok {
		t.Fatalf("memory_policy carries no block for %s; document: %v", path, doc)
	}
	e, ok := perPath[entry].(map[string]any)
	if !ok {
		t.Fatalf("memory_policy[%s] carries no %q entry; block: %v", path, entry, perPath)
	}
	return e
}

// w286MustContain fails unless every needle appears in text.
func w286MustContain(t *testing.T, what, text string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Fatalf("%s does not contain %q; text:\n%s", what, n, text)
		}
	}
}

// w286NoFamily reports whether a config workspace document still carries one
// of the moved families.
func w286NoFamily(t *testing.T, what, out string) {
	t.Helper()
	for _, key := range []string{`"summarize"`, `"memory_policy"`} {
		if strings.Contains(out, key) {
			t.Fatalf("%s still carries %s; document:\n%s", what, key, out)
		}
	}
}

// w286LegacyYAML is a valid workspace yaml carrying both old sections, the
// comments around them and an unrelated steps section.
func w286LegacyYAML(project, language string, defaultRecord string) string {
	return `# kept: workspace-owned step settings
summarize:
  models:
    - "pi:model-a"
    - "claude:model-b"
  language: "` + language + `"
  interval_sec: 120
memory_policy:
  default:
    record:
      - "` + defaultRecord + `"
    skip:
      - "never record this"
  ` + project + `:
    record:
      - "` + project + ` convention"
    skip: []
steps:
  ` + project + `:
    fix:
      models: ["pi:step-model"]
# kept: trailing comment
`
}

func w286Home(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// ---------------------------------------------------------------------------
// S1 — the summarize table is the lane's only source; config list reads it.
// ---------------------------------------------------------------------------
func TestW286SummarizeTableIsOnlySource(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, `
[summarize]
models = ["pi:model-a", "claude:model-b"]
language = "Chinese"
interval_sec = 120
`)
	e := w113Env{home: home, config: cfg, json: true}
	list := w286JSON(t, "config list", w286OK(t, "config list", e, "config", "list"))
	if got := strings.Join(w286Strings(t, "summarize.models", w286Value(t, list, "summarize.models")), ","); got != "pi:model-a,claude:model-b" {
		t.Fatalf("summarize.models = %q, want pi:model-a,claude:model-b", got)
	}
	if got := w286Value(t, list, "summarize.language"); got != "Chinese" {
		t.Fatalf("summarize.language = %#v, want Chinese", got)
	}
	if got := w286Value(t, list, "summarize.interval_sec"); got != float64(120) {
		t.Fatalf("summarize.interval_sec = %#v, want 120", got)
	}
	workspace := w286OK(t, "config workspace", e, "config", "workspace", "--cwd", ws)
	w286NoFamily(t, "config workspace", workspace)
	if !strings.Contains(workspace, `"alpha"`) {
		t.Fatalf("config workspace lost its project mapping; document:\n%s", workspace)
	}
	t.Log("PASS W286-S1 summarize lives in cli.toml and config list is its only read face")
}

// ---------------------------------------------------------------------------
// S2 — optional summarize keys keep today's defaults.
// ---------------------------------------------------------------------------
func TestW286SummarizeOptionalDefaults(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, `
[summarize]
models = ["pi:model-a"]
`)
	e := w113Env{home: home, config: cfg, json: true}
	doc := w286JSON(t, "config list", w286OK(t, "config list", e, "config", "list"))
	if got := w286Value(t, doc, "summarize.language"); got != "中文" {
		t.Fatalf("summarize.language default = %#v, want 中文", got)
	}
	if got := w286Value(t, doc, "summarize.interval_sec"); got != float64(60) {
		t.Fatalf("summarize.interval_sec default = %#v, want 60", got)
	}
	t.Log("PASS W286-S2 omitted summarize keys resolve to the documented defaults")
}

// ---------------------------------------------------------------------------
// S3 — memory_policy is a path-keyed family read only through config list.
// ---------------------------------------------------------------------------
func TestW286MemoryPolicyPathFamily(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	wsA, wsB := filepath.Join(dir, "ws-alpha"), filepath.Join(dir, "ws-beta")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := w286Config(t, dir, []w286WS{
		{path: wsA, projects: []string{"alpha"}},
		{path: wsB, projects: []string{"beta"}},
	}, `
[summarize]
models = ["pi:model-a"]

[memory_policy."`+wsA+`".default]
record = [
  "alpha default note",
  "Process logs, step outputs, full spec/plan, diffs",
]
skip = ["alpha secret"]

[memory_policy."`+wsA+`".alpha]
record = ["alpha project note"]
skip = []

[memory_policy."`+wsB+`".default]
record = ["beta default note"]
skip = ["beta secret"]
`)
	e := w113Env{home: home, config: cfg, json: true}
	doc := w286JSON(t, "config list", w286OK(t, "config list", e, "config", "list"))
	aDefault := w286PolicyEntry(t, doc, wsA, "default")
	if got := w286Strings(t, "alpha default record", aDefault["record"]); len(got) != 2 || got[1] != "Process logs, step outputs, full spec/plan, diffs" {
		t.Fatalf("alpha default record = %#v, want the two declared items", got)
	}
	if got := w286Strings(t, "alpha project record", w286PolicyEntry(t, doc, wsA, "alpha")["record"]); len(got) != 1 || got[0] != "alpha project note" {
		t.Fatalf("alpha project record = %#v", got)
	}
	if got := w286Strings(t, "beta default record", w286PolicyEntry(t, doc, wsB, "default")["record"]); len(got) != 1 || got[0] != "beta default note" {
		t.Fatalf("beta default record = %#v, want only beta's own block", got)
	}
	for _, ws := range []string{wsA, wsB} {
		w286NoFamily(t, "config workspace "+ws, w286OK(t, "config workspace", e, "config", "workspace", "--cwd", ws))
	}
	t.Log("PASS W286-S3 memory_policy keeps one block per workspace path and config workspace drops it")
}

// ---------------------------------------------------------------------------
// S4 — multi-line arrays with quoted commas parse whole; unknown keys refuse.
// ---------------------------------------------------------------------------
func TestW286ParserMultilineAndStrict(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, `
[summarize]
models = [
  "pi:model-a",
  "claude:model-b"
]

[memory_policy."`+ws+`".default]
record = [
  "Process logs, step outputs, full spec/plan, diffs",
  "Owner decisions",
]
skip = [
  "Secrets",
]
`)
	e := w113Env{home: home, config: cfg, json: true}
	doc := w286JSON(t, "config list", w286OK(t, "config list", e, "config", "list"))
	if got := w286Strings(t, "summarize.models", w286Value(t, doc, "summarize.models")); len(got) != 2 || got[1] != "claude:model-b" {
		t.Fatalf("multi-line summarize.models = %#v, want two whole items", got)
	}
	record := w286Strings(t, "memory_policy record", w286PolicyEntry(t, doc, ws, "default")["record"])
	if len(record) != 2 || record[0] != "Process logs, step outputs, full spec/plan, diffs" {
		t.Fatalf("a comma inside a quoted item was split: %#v", record)
	}

	bad := w286Config(t, t.TempDir(), []w286WS{{path: ws, projects: []string{"alpha"}}}, `
[summarize]
models = ["pi:model-a"]
unexpected = "x"
`)
	out, code := w286Run(t, w113Env{home: home, config: bad}, "config", "list")
	if code == 0 {
		t.Fatalf("an unknown summarize key loaded: %s", out)
	}
	w286MustContain(t, "the unknown-key refusal", out, "line", "models", "language", "interval_sec")
	t.Log("PASS W286-S4 multi-line arrays keep quoted commas as data and unknown table keys are line-named")
}

// ---------------------------------------------------------------------------
// S5 — legacy yaml sections are refused whole; doctor reads cli.toml.
// ---------------------------------------------------------------------------
func TestW286LegacyYamlRefused(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	yamlPath := w286Write(t, filepath.Join(ws, ".polyforge.yaml"), w286LegacyYAML("alpha", "Japanese", "legacy note"))
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, "")
	e := w113Env{home: home, config: cfg, json: true}

	out, code := w286Run(t, e, "config", "workspace", "--cwd", ws)
	if code == 0 {
		t.Fatalf("config workspace accepted a legacy yaml: %s", out)
	}
	w286MustContain(t, "the legacy refusal", out, filepath.Base(yamlPath), "line", "pf config migrate")

	doctorOut, _ := w286Run(t, w113Env{home: home, config: cfg, json: true}, "doctor")
	doctor := w286JSON(t, "doctor", doctorOut)
	checks, _ := doctor["checks"].([]any)
	found := false
	for _, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok || check["status"] != "fail" {
			continue
		}
		detail, _ := check["detail"].(string)
		name, _ := check["name"].(string)
		if strings.Contains(detail, filepath.Base(yamlPath)) && strings.Contains(detail, "line") && strings.Contains(detail, "pf config migrate") {
			found = true
			_ = name
			break
		}
	}
	if !found {
		t.Fatalf("doctor has no failing row naming the legacy yaml, its line and the migrate hint; report:\n%s", doctorOut)
	}

	// The policy row now reads cli.toml: a cli-declared block with no yaml
	// section is not a warning.
	cleanWS := filepath.Join(dir, "ws-clean")
	if err := os.MkdirAll(cleanWS, 0o755); err != nil {
		t.Fatal(err)
	}
	w286Write(t, filepath.Join(cleanWS, ".polyforge.yaml"), "steps:\n  clean:\n    fix:\n      models: [\"pi:step-model\"]\n")
	cleanCfg := w286Config(t, t.TempDir(), []w286WS{{path: cleanWS, projects: []string{"clean"}}}, `
[summarize]
models = ["pi:model-a"]

[memory_policy."`+cleanWS+`".default]
record = ["cli policy note"]
skip = []
`)
	cleanDoctor := w286JSON(t, "doctor", mustRunDoctor(t, w113Env{home: w286Home(t), config: cleanCfg, json: true}))
	cleanChecks, _ := cleanDoctor["checks"].([]any)
	policyRow := map[string]any(nil)
	for _, raw := range cleanChecks {
		if check, ok := raw.(map[string]any); ok && check["name"] == "workspace memory policy" {
			policyRow = check
			break
		}
	}
	if policyRow == nil {
		t.Fatalf("doctor carries no workspace memory policy row; report: %v", cleanDoctor)
	}
	if policyRow["status"] != "pass" {
		t.Fatalf("the policy row still reads the yaml instead of the cli.toml block: %v", policyRow)
	}
	t.Log("PASS W286-S5 legacy yaml sections refuse whole and doctor's policy row reads cli.toml")
}

func mustRunDoctor(t *testing.T, e w113Env) string {
	t.Helper()
	out, _ := w286Run(t, e, "doctor")
	return out
}

// ---------------------------------------------------------------------------
// S6 — config set/unset write both families with the documented flags.
// ---------------------------------------------------------------------------
func TestW286ConfigSetWritesFamilies(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, `
# keep this comment
[summarize]
models = ["pi:model-a"]
`)
	e := w113Env{home: home, config: cfg}
	w286OK(t, "config set summarize.language", e, "config", "set", "summarize.language", "Japanese")
	text := string(w286Read(t, cfg))
	w286MustContain(t, "cli.toml after the summarize write", text, "Japanese", "# keep this comment", "pi:model-a")

	out, code := w286Run(t, e, "config", "set", "summarize.models", "pi:model-b", "--workspace", ws)
	if code == 0 || !strings.Contains(out, "summarize.models") {
		t.Fatalf("summarize.* accepted --workspace (exit %d): %s", code, out)
	}
	w286OK(t, "config set memory_policy", e, "config", "set", "memory_policy.alpha.record", `["alpha note, with comma"]`, "--workspace", ws)
	text = string(w286Read(t, cfg))
	w286MustContain(t, "cli.toml after the memory_policy write", text, ws, "memory_policy", "alpha note, with comma", "# keep this comment", "Japanese")
	out, code = w286Run(t, e, "config", "set", "memory_policy.alpha.skip", `["secret"]`)
	if code == 0 {
		t.Fatalf("memory_policy.* accepted a write without a path selection: %s", out)
	}
	w286OK(t, "config unset memory_policy", e, "config", "unset", "memory_policy.alpha.record", "--workspace", ws)
	text = string(w286Read(t, cfg))
	if strings.Contains(text, "alpha note, with comma") {
		t.Fatalf("unset left the removed list behind:\n%s", text)
	}
	w286MustContain(t, "cli.toml after unset", text, "# keep this comment", "Japanese", "pi:model-a")
	t.Log("PASS W286-S6 config set/unset use the documented keys and path selection")
}

// ---------------------------------------------------------------------------
// S7 — migrate moves values, clears the yaml sections, keeps other bytes, backs up.
// ---------------------------------------------------------------------------
func TestW286MigrateMovesValues(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	wsA, wsB, wsC := filepath.Join(dir, "ws-alpha"), filepath.Join(dir, "ws-beta"), filepath.Join(dir, "ws-gamma")
	for _, ws := range []string{wsA, wsB, wsC} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	yamlA := w286Write(t, filepath.Join(wsA, ".polyforge.yaml"), w286LegacyYAML("alpha", "Chinese", "alpha default note"))
	yamlB := w286Write(t, filepath.Join(wsB, ".polyforge.yaml"), w286LegacyYAML("beta", "Chinese", "beta default note"))
	beforeA, beforeB := string(w286Read(t, yamlA)), string(w286Read(t, yamlB))
	cfg := w286Config(t, dir, []w286WS{
		{path: wsA, projects: []string{"alpha"}},
		{path: wsB, projects: []string{"beta"}},
		{path: wsC, projects: []string{"gamma"}},
	}, "")
	beforeCFG := string(w286Read(t, cfg))

	out, code := w286Run(t, w113Env{home: home, config: cfg}, "config", "migrate")
	if code != 0 {
		t.Fatalf("config migrate exited %d (want 0):\n%s", code, out)
	}
	text := string(w286Read(t, cfg))
	w286MustContain(t, "migrated cli.toml", text, "[summarize]", "pi:model-a", "claude:model-b", "Chinese", "interval_sec = 120", wsA, wsB, "alpha default note", "beta default note")
	for _, y := range []string{yamlA, yamlB} {
		ytext := string(w286Read(t, y))
		if strings.Contains(ytext, "summarize:") || strings.Contains(ytext, "memory_policy:") {
			t.Fatalf("%s still carries a legacy section:\n%s", y, ytext)
		}
		w286MustContain(t, y, ytext, "# kept: workspace-owned step settings", "steps:", "pi:step-model", "# kept: trailing comment")
	}
	backupsA, _ := filepath.Glob(yamlA + ".bak-*")
	backupsB, _ := filepath.Glob(yamlB + ".bak-*")
	backupsCFG, _ := filepath.Glob(cfg + ".bak-*")
	if len(backupsA) != 1 || len(backupsB) != 1 || len(backupsCFG) != 1 {
		t.Fatalf("backups per changed file = yamlA %v, yamlB %v, cli.toml %v; want exactly one each", backupsA, backupsB, backupsCFG)
	}
	if string(w286Read(t, backupsA[0])) != beforeA || string(w286Read(t, backupsB[0])) != beforeB || string(w286Read(t, backupsCFG[0])) != beforeCFG {
		t.Fatalf("a backup does not hold the pre-change bytes")
	}
	if entries, _ := os.ReadDir(wsC); len(entries) != 0 {
		t.Fatalf("the workspace without a yaml was not skipped cleanly: %v", entries)
	}
	t.Log("PASS W286-S7 migrate moves values verbatim, clears the yaml sections, keeps other bytes and backs up each change")
}

// ---------------------------------------------------------------------------
// S8 — an interrupted (cli.toml already written) run converges; rerun is a no-op.
// ---------------------------------------------------------------------------
func TestW286MigrateConvergesAndNoOp(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := w286Write(t, filepath.Join(ws, ".polyforge.yaml"), w286LegacyYAML("alpha", "Chinese", "alpha default note"))
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, `
[summarize]
models = ["pi:model-a", "claude:model-b"]
language = "Chinese"
interval_sec = 120

[memory_policy."`+ws+`".default]
record = [
  "alpha default note",
]
skip = [
  "never record this",
]

[memory_policy."`+ws+`".alpha]
record = [
  "alpha convention",
]
skip = []
`)
	cfgBefore := string(w286Read(t, cfg))
	out, code := w286Run(t, w113Env{home: home, config: cfg}, "config", "migrate")
	if code != 0 {
		t.Fatalf("the interrupted-state run exited %d (want 0):\n%s", code, out)
	}
	if got := string(w286Read(t, cfg)); got != cfgBefore {
		t.Fatalf("a run with nothing left to add rewrote cli.toml:\nbefore:\n%s\nafter:\n%s", cfgBefore, got)
	}
	ytext := string(w286Read(t, yaml))
	if strings.Contains(ytext, "summarize:") || strings.Contains(ytext, "memory_policy:") {
		t.Fatalf("the interrupted-state run did not finish the yaml deletion:\n%s", ytext)
	}
	yamlAfter := string(w286Read(t, yaml))
	out, code = w286Run(t, w113Env{home: home, config: cfg}, "config", "migrate")
	if code != 0 {
		t.Fatalf("the second run exited %d (want 0):\n%s", code, out)
	}
	if !strings.Contains(out, "nothing to migrate") {
		t.Fatalf("the second run did not report `nothing to migrate`:\n%s", out)
	}
	if string(w286Read(t, cfg)) != cfgBefore || string(w286Read(t, yaml)) != yamlAfter {
		t.Fatalf("the no-op second run changed a byte")
	}
	t.Log("PASS W286-S8 migrate converges from the cli.toml-written state and a second run changes nothing")
}

// ---------------------------------------------------------------------------
// S9 — disagreeing values refuse itemized, with zero writes.
// ---------------------------------------------------------------------------
func TestW286MigrateRefusesDisagreement(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	wsA, wsB := filepath.Join(dir, "ws-alpha"), filepath.Join(dir, "ws-beta")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	yamlA := w286Write(t, filepath.Join(wsA, ".polyforge.yaml"), w286LegacyYAML("alpha", "Japanese", "alpha note"))
	yamlB := w286Write(t, filepath.Join(wsB, ".polyforge.yaml"), w286LegacyYAML("beta", "Chinese", "beta note"))
	cfg := w286Config(t, dir, []w286WS{
		{path: wsA, projects: []string{"alpha"}},
		{path: wsB, projects: []string{"beta"}},
	}, "")
	before := map[string]string{cfg: string(w286Read(t, cfg)), yamlA: string(w286Read(t, yamlA)), yamlB: string(w286Read(t, yamlB))}
	out, code := w286Run(t, w113Env{home: home, config: cfg}, "config", "migrate")
	if code == 0 {
		t.Fatalf("migrate picked a side between disagreeing summarize values:\n%s", out)
	}
	w286MustContain(t, "the summarize disagreement", out, "Japanese", "Chinese")
	for path, text := range before {
		if string(w286Read(t, path)) != text {
			t.Fatalf("a refused run wrote %s", path)
		}
	}

	// Same-path policy conflict: an existing cli.toml entry differs from the
	// yaml block. It refuses naming the path and entry, and writes nothing.
	conflictCfg := w286Config(t, t.TempDir(), []w286WS{{path: wsA, projects: []string{"alpha"}}}, `
[summarize]
models = ["pi:model-a", "claude:model-b"]
language = "Japanese"
interval_sec = 120

[memory_policy."`+wsA+`".default]
record = ["cli wins?"]
skip = ["never record this"]
`)
	beforeCfg, beforeYaml := string(w286Read(t, conflictCfg)), string(w286Read(t, yamlA))
	out, code = w286Run(t, w113Env{home: w286Home(t), config: conflictCfg}, "config", "migrate")
	if code == 0 {
		t.Fatalf("migrate overwrote a differing same-path policy:\n%s", out)
	}
	w286MustContain(t, "the policy conflict", out, wsA, "default")
	if string(w286Read(t, conflictCfg)) != beforeCfg || string(w286Read(t, yamlA)) != beforeYaml {
		t.Fatalf("the policy-conflict refusal wrote a file")
	}
	t.Log("PASS W286-S9 migrate refuses disagreement and a same-path conflict with an itemized error and zero writes")
}

// ---------------------------------------------------------------------------
// S10 — pf init writes the default policy into cli.toml, never the yaml.
// ---------------------------------------------------------------------------
func TestW286InitWritesCliPolicy(t *testing.T) {
	dir, home := t.TempDir(), w286Home(t)
	ws := filepath.Join(dir, "ws-alpha")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := w286Write(t, filepath.Join(ws, ".polyforge.yaml"), "steps:\n  alpha:\n    fix:\n      models: [\"pi:step-model\"]\n")
	cfg := w286Config(t, dir, []w286WS{{path: ws, projects: []string{"alpha"}}}, "")
	e := w113Env{home: home, config: cfg}
	w286OK(t, "pf init", e, "init", ws)
	text := string(w286Read(t, cfg))
	if !regexp.MustCompile(`\[memory_policy\."` + regexp.QuoteMeta(ws) + `"(\.|\.")default`).MatchString(text) {
		t.Fatalf("pf init wrote no [memory_policy.\"<path>\".default] block into cli.toml:\n%s", text)
	}
	w286MustContain(t, "the init-written cli.toml block", text, "record", "skip")
	if strings.Contains(string(w286Read(t, yaml)), "memory_policy:") {
		t.Fatalf("pf init re-created the yaml memory_policy section")
	}
	once := string(w286Read(t, cfg))
	w286OK(t, "pf init rerun", e, "init", ws)
	if string(w286Read(t, cfg)) != once {
		t.Fatalf("a second pf init changed cli.toml")
	}

	unmapped := filepath.Join(dir, "ws-unmapped")
	if err := os.MkdirAll(unmapped, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code := w286Run(t, e, "init", unmapped)
	if code != 0 {
		t.Fatalf("pf init on an unmapped workspace exited %d: %s", code, out)
	}
	if string(w286Read(t, cfg)) != once {
		t.Fatalf("pf init wrote cli.toml for an unmapped workspace")
	}
	w286MustContain(t, "the unmapped hint", out, "[workspace")
	t.Log("PASS W286-S10 pf init writes the built-in cli.toml policy once and leaves the yaml alone")
}
