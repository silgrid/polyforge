package test

// w22_config_panel_acceptance_test.go: workbench#22 end-to-end acceptance for
// the polyforge half of the pi-web configuration panel — the five `pf config`
// additions of artifacts/spec.md (reload class per key, validator-derived key
// schema, expected-file-hash (CAS) writes with a key-level diff, live read and
// validated byte-preserving write of a workspace's `.polyforge.yaml`
// summarize keys, and the read of a workspace's declared `steps.<project>`
// candidates) plus the read-only daemon fact (`queue.max_configured`) the
// panel's stale-reason column needs.
//
// Authored against the SPEC (`artifacts/e2e-scenarios.md`), BEFORE the
// implementation lands: every assertion is driven through the real pf binary,
// a real daemon on its own socket and the real config files — never engine
// internals. The scenarios are assertion-level reds at baseline (the new
// subcommand/flags/fields are absent), never build reds: this file compiles
// and vets against the tree as it stands.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// w22JSON decodes one CLI --json document (tolerating surrounding text).
func w22JSON(t *testing.T, what, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(w113JSONDocOf(out)), &m); err != nil {
		t.Fatalf("%s: output is not a JSON document: %v (%q)", what, err, out)
	}
	return m
}

// w22Field walks a decoded document by key names.
func w22Field(doc any, path ...string) (any, bool) {
	cur := doc
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// w22Str renders a decoded JSON value for comparison and messages.
func w22Str(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i := range x {
			parts[i] = w22Str(x[i])
		}
		return strings.Join(parts, ",")
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// w22Items indexes a `config list` document's items by key.
func w22Items(t *testing.T, doc map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := doc["items"].([]any)
	if !ok {
		t.Fatalf("config list document carries no items array: %v", doc)
	}
	out := map[string]map[string]any{}
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			if k, ok := m["key"].(string); ok {
				out[k] = m
			}
		}
	}
	return out
}

// w22Walk visits every decoded JSON value.
func w22Walk(v any, visit func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		for _, child := range x {
			w22Walk(child, visit)
		}
	case []any:
		for _, child := range x {
			w22Walk(child, visit)
		}
	}
}

// w22Schemas collects every key→schema pair a document carries, however the
// read nests it (an item's `schema`, a `schema` map keyed by key name, or a
// per-key entry that carries its own kind).
func w22Schemas(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	w22Walk(doc, func(m map[string]any) {
		if key, ok := m["key"].(string); ok {
			if s, ok := m["schema"].(map[string]any); ok {
				out[key] = s
			}
			if _, ok := m["kind"].(string); ok {
				out[key] = m
			}
		}
		if s, ok := m["schema"].(map[string]any); ok {
			for name, val := range s {
				if child, ok := val.(map[string]any); ok {
					out[name] = child
				}
			}
		}
	})
	for _, alias := range [][2]string{
		{"interval_sec", "summarize.interval_sec"},
		{"language", "summarize.language"},
		{"models", "summarize.models"},
	} {
		if s, ok := out[alias[0]]; ok {
			if _, seen := out[alias[1]]; !seen {
				out[alias[1]] = s
			}
		}
	}
	return out
}

// w22ValueSources collects every {value,source} pair of a document, keyed by
// the name it hangs under.
func w22ValueSources(doc map[string]any) map[string][2]any {
	out := map[string][2]any{}
	var walk func(name string, v any)
	walk = func(name string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if val, ok := x["value"]; ok {
				if src, ok := x["source"].(string); ok {
					out[name] = [2]any{val, src}
				}
			}
			for k, child := range x {
				walk(k, child)
			}
		case []any:
			for _, child := range x {
				walk(name, child)
			}
		}
	}
	walk("", doc)
	return out
}

// w22Declared collects every `{declared,models}` entry by the step id it
// hangs under.
func w22Declared(doc map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	w22Walk(doc, func(m map[string]any) {
		for name, child := range m {
			cm, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := cm["declared"].(bool); ok {
				out[name] = cm
			}
		}
	})
	return out
}

// w22WriteFile writes text at path (0644) and returns the path.
func w22WriteFile(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// w22Read reads a file or fails.
func w22Read(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// w22ConfigWithDaemon writes a valid cli.toml (mapping + one [daemon] line)
// and returns its path.
func w22ConfigWithDaemon(t *testing.T, dir, maxJobs string, ws []w113WS) string {
	t.Helper()
	path := w113ConfigFile(t, filepath.Join(dir, "cli.toml"), "http://127.0.0.1:1", "w22key", "", ws)
	return w22WriteFile(t, path, string(w22Read(t, path))+"\n[daemon]\nmax_concurrent_jobs = "+maxJobs+"\n")
}

// ---------------------------------------------------------------------------
// Scenario 1 — the reload class per key.
// ---------------------------------------------------------------------------

// TestW22ConfigListCarriesReloadClass pins the spec's "Class per key": every
// item carries the class the one class table holds (the table the loader and
// the reload path use), the workspace section is next-claim, the existing
// value/source/set/section fields are unchanged, and the same read carries the
// file hash the CAS scenarios need.
func TestW22ConfigListCarriesReloadClass(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\napi_key = \"w22-SENTINEL-KEY\"\nuser_id = \"usr_root\"\n"+
			"\n[daemon]\nmax_concurrent_jobs = 5\n"+
			"\n[workspace.\""+ws+"\"]\nprojects = [\"e2e\"]\n")
	out, code := w3PF(t, nil, w113Env{home: home, config: cfg, json: true}, "", "config", "list")
	if code != 0 {
		t.Fatalf("pf --json config list: exit %d (%q)", code, out)
	}
	doc := w22JSON(t, "config list", out)
	items := w22Items(t, doc)

	want := map[string]string{
		"server":                   "restart-only",
		"api_key":                  "restart-only",
		"daemon_socket":            "restart-only",
		"harness_root":             "restart-only",
		"operator_agent_dir":       "restart-only",
		"user_id":                  "cli-only",
		"claude_bin":               "live",
		"step_timeout":             "live",
		"stall_window":             "live",
		"max_concurrent_jobs":      "live",
		`[workspace."` + ws + `"]`: "next-claim",
	}
	for key, class := range want {
		it, ok := items[key]
		if !ok {
			t.Fatalf("config list has no item for %q (items: %d)", key, len(items))
		}
		if got, _ := it["class"].(string); got != class {
			t.Fatalf("config list item %q class = %q, want %q (the class the loader/reload table holds)", key, got, class)
		}
	}
	for key, it := range items {
		if c, _ := it["class"].(string); c == "" {
			t.Fatalf("config list item %q carries no reload class", key)
		}
	}

	// The per-key value/source/set/section fields stay what they are today.
	if it := items["max_concurrent_jobs"]; w22Str(it["value"]) != "5" || w22Str(it["source"]) != "file" {
		t.Fatalf("max_concurrent_jobs must keep value=5 source=file; got value=%q source=%q", w22Str(it["value"]), w22Str(it["source"]))
	}
	if it := items["api_key"]; it["value"] != nil || it["set"] != true {
		t.Fatalf("api_key must stay masked as set-only; got value=%v set=%v", it["value"], it["set"])
	}
	if it := items[`[workspace."`+ws+`"]`]; it["section"] != true {
		t.Fatalf("a [workspace] item must stay flagged section=true; got %v", it)
	}

	sha, ok := w22Field(doc, "file", "sha256")
	if !ok || w22Str(sha) != w3SHA(t, cfg) {
		t.Fatalf("config list must report the file's sha256 (%s); got %v", w3SHA(t, cfg), sha)
	}
	if path, ok := w22Field(doc, "file", "path"); !ok || w22Str(path) != cfg {
		t.Fatalf("config list must report the file path (%s); got %v", cfg, path)
	}
}

// ---------------------------------------------------------------------------
// Scenario 2 — the validator's constraints in machine-readable form.
// ---------------------------------------------------------------------------

// TestW22KeySchemaComesFromTheValidator pins the spec's "Constraints come from
// the validator": every writable key reports its value kind plus exactly the
// range/enum the existing validator enforces, and nothing it does not — above
// all no `language` enum.
func TestW22KeySchemaComesFromTheValidator(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22ConfigWithDaemon(t, home, "5", []w113WS{{key: ws, projects: []string{"e2e"}}})
	w22WriteFile(t, filepath.Join(ws, ".polyforge.yaml"),
		"# operator comment\nsummarize:\n  models: [\"pi:b-model\", \"pi:a-model\"]\n  language: \"English\"\n  interval_sec: 30\n")

	schemas := map[string]map[string]any{}
	listOut, listCode := w3PF(t, nil, w113Env{home: home, config: cfg, json: true}, "", "config", "list")
	if listCode != 0 {
		t.Fatalf("pf --json config list: exit %d (%q)", listCode, listOut)
	}
	for k, s := range w22Schemas(w22JSON(t, "config list", listOut)) {
		schemas[k] = s
	}
	wsOut, wsCode := w3PF(t, nil, w113Env{home: home, config: cfg, json: true}, "", "config", "workspace", "--cwd", ws)
	if wsCode != 0 {
		t.Fatalf("pf --json config workspace: exit %d (%q)", wsCode, wsOut)
	}
	for k, s := range w22Schemas(w22JSON(t, "config workspace", wsOut)) {
		if _, seen := schemas[k]; !seen {
			schemas[k] = s
		}
	}
	kind := func(key string) string {
		k, _ := schemas[key]["kind"].(string)
		return k
	}
	num := func(key, field string) (float64, bool) {
		f, ok := schemas[key][field].(float64)
		return f, ok
	}

	if got := kind("max_concurrent_jobs"); got != "int" {
		t.Fatalf("max_concurrent_jobs schema kind = %q, want int (schemas: %v)", got, schemas)
	}
	if min, ok := num("max_concurrent_jobs", "min"); !ok || min != 1 {
		t.Fatalf("max_concurrent_jobs schema must carry the validator's min=1; got %v", schemas["max_concurrent_jobs"])
	}
	if _, ok := num("max_concurrent_jobs", "max"); ok {
		t.Fatalf("max_concurrent_jobs has no upper bound in the validator; the schema must not invent one: %v", schemas["max_concurrent_jobs"])
	}
	if got := kind("summarize.interval_sec"); got != "int" {
		t.Fatalf("summarize.interval_sec schema kind = %q, want int (schemas: %v)", got, schemas)
	}
	if min, ok := num("summarize.interval_sec", "min"); !ok || min != 10 {
		t.Fatalf("summarize.interval_sec schema must carry min=10; got %v", schemas["summarize.interval_sec"])
	}
	if max, ok := num("summarize.interval_sec", "max"); !ok || max != 3600 {
		t.Fatalf("summarize.interval_sec schema must carry max=3600; got %v", schemas["summarize.interval_sec"])
	}
	for _, key := range []string{"step_timeout", "stall_window"} {
		if got := kind(key); got != "duration" {
			t.Fatalf("%s schema kind = %q, want duration (schemas: %v)", key, got, schemas)
		}
	}
	if got := kind("summarize.language"); got != "string" {
		t.Fatalf("summarize.language schema kind = %q, want string (schemas: %v)", got, schemas)
	}
	if got := kind("summarize.models"); got != "model-list" {
		t.Fatalf("summarize.models schema kind = %q, want model-list (schemas: %v)", got, schemas)
	}
	if s, ok := schemas["summarize.language"]; ok {
		if _, hasEnum := s["enum"]; hasEnum {
			t.Fatalf("the validator holds no language enum; the schema must not invent one: %v", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 3 — a stale expected hash is refused, the file keeps the hand edit.
// ---------------------------------------------------------------------------

// TestW22StaleHashRefusedWithKeyDiff pins the spec's "Concurrent hand edit": a
// write carrying the hash the caller read, after a terminal edit, is refused
// without touching the file, and the refusal carries a key-level difference
// between the caller's expected state and the file's current state.
func TestW22StaleHashRefusedWithKeyDiff(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22ConfigWithDaemon(t, home, "5", []w113WS{{key: ws, projects: []string{"e2e"}}})
	env := w113Env{home: home, config: cfg}

	readSHA := w3SHA(t, cfg)
	raw := string(w22Read(t, cfg))
	w22WriteFile(t, cfg, strings.Replace(raw, "max_concurrent_jobs = 5", "max_concurrent_jobs = 6", 1))
	editedSHA := w3SHA(t, cfg)
	if editedSHA == readSHA {
		t.Fatal("fixture: the terminal edit did not change the file")
	}

	out, code := w3PF(t, nil, env, "", "config", "set", "max_concurrent_jobs", "7", "--expect-hash", readSHA)
	if code == 0 {
		t.Fatalf("a write with a stale expected hash succeeded: %q", out)
	}
	if got := w3SHA(t, cfg); got != editedSHA {
		t.Fatalf("a refused write touched the file: sha256 %s, want the hand-edited %s", got, editedSHA)
	}
	if !strings.Contains(out, "max_concurrent_jobs") || !strings.Contains(out, "6") {
		t.Fatalf("the refusal must carry a key-level diff naming the key and the file's current value (6); got %q", out)
	}
	if strings.Contains(string(w22Read(t, cfg)), "max_concurrent_jobs = 7") {
		t.Fatalf("the refused value reached the file:\n%s", w22Read(t, cfg))
	}
}

// ---------------------------------------------------------------------------
// Scenario 4 — nothing secret in the difference.
// ---------------------------------------------------------------------------

// TestW22APIKeyDiffIsChangedOnly pins the spec's "Nothing secret in the
// difference": a refusal whose expected state covers api_key reports only
// whether that key changed — never the caller's or the file's value.
func TestW22APIKeyDiffIsChangedOnly(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\napi_key = \"w22-OLD-SENTINEL\"\nuser_id = \"usr_root\"\n"+
			"\n[daemon]\nmax_concurrent_jobs = 5\n"+
			"\n[workspace.\""+ws+"\"]\nprojects = [\"e2e\"]\n")
	env := w113Env{home: home, config: cfg}

	state := map[string]any{
		"file_sha256": w3SHA(t, cfg),
		"values": map[string]any{
			"max_concurrent_jobs": "5",
			"api_key":             map[string]any{"set": true},
		},
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	raw := string(w22Read(t, cfg))
	rotated := strings.Replace(raw, "w22-OLD-SENTINEL", "w22-NEW-SENTINEL", 1)
	w22WriteFile(t, cfg, strings.Replace(rotated, "max_concurrent_jobs = 5", "max_concurrent_jobs = 6", 1))
	editedSHA := w3SHA(t, cfg)

	out, code := w3PF(t, nil, env, "", "config", "set", "max_concurrent_jobs", "7", "--expect-state", string(stateJSON))
	if code == 0 {
		t.Fatalf("a write with a stale expected state succeeded: %q", out)
	}
	if got := w3SHA(t, cfg); got != editedSHA {
		t.Fatalf("the refused write touched the file: %s != %s", got, editedSHA)
	}
	if !strings.Contains(out, "api_key") {
		t.Fatalf("the difference must cover api_key (it changed); got %q", out)
	}
	for _, secret := range []string{"w22-OLD-SENTINEL", "w22-NEW-SENTINEL"} {
		if strings.Contains(out, secret) {
			t.Fatalf("the difference leaked an api_key value (%s): %q", secret, out)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 5 — the comparison happens inside the writer's lock.
// ---------------------------------------------------------------------------

// TestW22FirstWriterWinsInsideTheLock pins the spec's "Comparison inside the
// lock": two writers carrying the same expected hash — the first wins and the
// second is refused, so a caller cannot win by arriving a moment later.
func TestW22FirstWriterWinsInsideTheLock(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22ConfigWithDaemon(t, home, "5", []w113WS{{key: ws, projects: []string{"e2e"}}})
	readSHA := w3SHA(t, cfg)
	pfPath := filepath.Join(v1Bins(t), "pf")
	xdg := filepath.Join(home, ".config")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(value string) *exec.Cmd {
		cmd := exec.Command(pfPath, "--config", cfg, "config", "set", "max_concurrent_jobs", value, "--expect-hash", readSHA)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + xdg}
		return cmd
	}
	first, second := run("7"), run("8")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	firstErr := first.Wait()
	secondErr := second.Wait()
	wins := 0
	if firstErr == nil {
		wins++
	}
	if secondErr == nil {
		wins++
	}
	if wins != 1 {
		t.Fatalf("with one expected hash exactly one writer must win, got %d (first=%v second=%v)", wins, firstErr, secondErr)
	}
	final := string(w22Read(t, cfg))
	if strings.Contains(final, "max_concurrent_jobs = 7") && strings.Contains(final, "max_concurrent_jobs = 8") {
		t.Fatalf("both writers landed:\n%s", final)
	}
	if !strings.Contains(final, "max_concurrent_jobs = 7") && !strings.Contains(final, "max_concurrent_jobs = 8") {
		t.Fatalf("the winning write did not land:\n%s", final)
	}
}

// ---------------------------------------------------------------------------
// Scenario 6 — the summarize read (values, order, sources).
// ---------------------------------------------------------------------------

// TestW22WorkspaceSummarizeRead pins the spec's "Read the three keys" and the
// unmapped half of "Unmapped cwd": models come back ordered, language is free
// text, interval_sec carries its effective value and whether it came from the
// file or a default, and an unmapped directory answers mapped=false without an
// error.
func TestW22WorkspaceSummarizeRead(t *testing.T) {
	wsMapped, wsPlain := t.TempDir(), t.TempDir()
	home := t.TempDir()
	cfg := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\nuser_id = \"usr_root\"\n"+
			"\n[workspace.\""+wsMapped+"\"]\nprojects = [\"e2e\"]\n")
	env := w113Env{home: home, config: cfg}
	w22WriteFile(t, filepath.Join(wsMapped, ".polyforge.yaml"),
		"# keep me\nsummarize:\n  models: [\"pi:zeta\", \"pi:alpha\"]\n  language: \"English free text\"\n  interval_sec: 30\n")

	out, code := w3PF(t, nil, env, "", "config", "workspace", "--cwd", wsMapped)
	if code != 0 {
		t.Fatalf("pf config workspace --cwd <mapped>: exit %d (%q)", code, out)
	}
	doc := w22JSON(t, "config workspace", out)
	if mapped, _ := w22Field(doc, "mapped"); mapped != true {
		t.Fatalf("a mapped cwd must answer mapped=true; got %v", doc)
	}
	values := w22ValueSources(doc)
	if got, ok := values["models"]; !ok || w22Str(got[0]) != "pi:zeta,pi:alpha" || w22Str(got[1]) != "file" {
		t.Fatalf("summarize.models must report the file's order (pi:zeta,pi:alpha) with source file; got %v", values)
	}
	if got, ok := values["language"]; !ok || w22Str(got[0]) != "English free text" || w22Str(got[1]) != "file" {
		t.Fatalf("summarize.language must report the free text with source file; got %v", values)
	}
	if got, ok := values["interval_sec"]; !ok || w22Str(got[0]) != "30" || w22Str(got[1]) != "file" {
		t.Fatalf("summarize.interval_sec must report 30 with source file; got %v", values)
	}

	cfg2 := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\nuser_id = \"usr_root\"\n"+
			"\n[workspace.\""+wsPlain+"\"]\nprojects = [\"e2e\"]\n")
	defOut, defCode := w3PF(t, nil, w113Env{home: home, config: cfg2}, "", "config", "workspace", "--cwd", wsPlain)
	if defCode != 0 {
		t.Fatalf("pf config workspace --cwd <no summarize>: exit %d (%q)", defCode, defOut)
	}
	if got, ok := w22ValueSources(w22JSON(t, "config workspace (defaults)", defOut))["interval_sec"]; !ok || w22Str(got[0]) != "60" || w22Str(got[1]) != "default" {
		t.Fatalf("an absent interval_sec must report the effective 60 with source default; got %v", got)
	}

	unmappedOut, unmappedCode := w3PF(t, nil, env, "", "config", "workspace", "--cwd", t.TempDir())
	if unmappedCode != 0 {
		t.Fatalf("an unmapped cwd must answer (exit 0), got exit %d (%q)", unmappedCode, unmappedOut)
	}
	if mapped, _ := w22Field(w22JSON(t, "config workspace (unmapped)", unmappedOut), "mapped"); mapped != false {
		t.Fatalf("an unmapped cwd must answer mapped=false; got %v", mapped)
	}
}

// ---------------------------------------------------------------------------
// Scenario 7 — the validated, byte-preserving summarize write.
// ---------------------------------------------------------------------------

// TestW22WorkspaceSummarizeWritePinned pins the spec's "Validated write": a
// valid summarize write is atomic, keeps every other byte, is judged by the
// same validator the daemon and `pf doctor` apply, an invalid value is refused
// with the file byte-identical, and a language-only write cannot create an
// entry without `models`.
func TestW22WorkspaceSummarizeWritePinned(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	cfg := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\nuser_id = \"usr_root\"\n"+
			"\n[workspace.\""+ws+"\"]\nprojects = [\"e2e\"]\n")
	env := w113Env{home: home, config: cfg}
	yamlPath := w22WriteFile(t, filepath.Join(ws, ".polyforge.yaml"),
		"# operator comment\nsummarize:\n  models: [\"pi:zeta\", \"pi:alpha\"]\n  interval_sec: 30\n\nsteps:\n  e2e:\n    default: { models: [\"pi:zeta\"] }\n")
	before := string(w22Read(t, yamlPath))
	beforeSHA := w3SHA(t, yamlPath)

	out, code := w3PF(t, nil, env, "", "config", "set", "summarize.interval_sec", "10", "--workspace", ws, "--expect-hash", beforeSHA)
	if code != 0 {
		t.Fatalf("a valid summarize write was refused: exit %d (%q)", code, out)
	}
	after := string(w22Read(t, yamlPath))
	if after == before {
		t.Fatal("the valid summarize write did not land")
	}
	if !strings.Contains(after, "interval_sec: 10") {
		t.Fatalf("the written value is not in the file:\n%s", after)
	}
	if n := w3DiffLines(t, before, after); n != 1 {
		t.Fatalf("the write changed %d lines, want exactly the target one:\n%s", n, after)
	}
	for _, keep := range []string{"# operator comment", "models: [\"pi:zeta\", \"pi:alpha\"]",
		"steps:", "default: { models: [\"pi:zeta\"] }"} {
		if !strings.Contains(after, keep) {
			t.Fatalf("the write dropped %q:\n%s", keep, after)
		}
	}

	// An independent read (no daemon) shows the value.
	readOut, readCode := w3PF(t, nil, env, "", "config", "workspace", "--cwd", ws)
	if readCode != 0 {
		t.Fatalf("an independent read after the write: exit %d (%q)", readCode, readOut)
	}
	if v, ok := w22ValueSources(w22JSON(t, "config workspace", readOut))["interval_sec"]; !ok || w22Str(v[0]) != "10" {
		t.Fatalf("an independent read after the write must show interval_sec 10; got %v", v)
	}
	// `pf doctor` refuses nothing about this workspace's yaml. The fixture
	// runs no daemon (and points at an unreachable server), so the doctor
	// exit code reflects those unrelated environment checks; the assertion
	// is only that no failing check names the written yaml.
	docOut, _ := w3PF(t, nil, w113Env{home: home, config: cfg, json: true}, "", "doctor")
	if bad := w113FailingCheck(w113DoctorJSON(t, docOut), "workspace yaml"); bad != nil {
		t.Fatalf("pf doctor refuses the written yaml: %s: %s", bad.Name, bad.Detail)
	}

	// An invalid value is refused with the validator's own error, file intact.
	invalidSHA := w3SHA(t, yamlPath)
	badOut, badCode := w3PF(t, nil, env, "", "config", "set", "summarize.interval_sec", "9", "--workspace", ws, "--expect-hash", invalidSHA)
	if badCode == 0 {
		t.Fatalf("a summarize.interval_sec of 9 was accepted: %q", badOut)
	}
	if got := w3SHA(t, yamlPath); got != invalidSHA {
		t.Fatalf("the refused write touched the file: %s != %s", got, invalidSHA)
	}
	if !strings.Contains(badOut, "interval_sec") {
		t.Fatalf("the refusal must be the validator's own error naming the key; got %q", badOut)
	}

	// A language-only write cannot create an entry without `models`.
	cold := t.TempDir()
	cfg2 := w22WriteFile(t, filepath.Join(home, "cli.toml"),
		"server = \"http://127.0.0.1:1\"\nuser_id = \"usr_root\"\n"+
			"\n[workspace.\""+cold+"\"]\nprojects = [\"e2e\"]\n")
	coldOut, coldCode := w3PF(t, nil, w113Env{home: home, config: cfg2}, "",
		"config", "set", "summarize.language", "English", "--workspace", cold, "--expect-hash", "absent")
	if coldCode == 0 {
		t.Fatalf("a summarize.language write without models was accepted: %q", coldOut)
	}
	if _, err := os.Stat(filepath.Join(cold, ".polyforge.yaml")); err == nil {
		t.Fatalf("a refused write created %s", filepath.Join(cold, ".polyforge.yaml"))
	}
}

// ---------------------------------------------------------------------------
// Scenario 8 — declared step candidates only.
// ---------------------------------------------------------------------------

// TestW22DeclaredStepCandidates pins the spec's "Declared candidates": each
// declared step and its candidate list in file order is reported, a step the
// file does not declare carries no composed list, and the panel never gets a
// bundle default filled in under a declared value.
func TestW22DeclaredStepCandidates(t *testing.T) {
	ws := t.TempDir()
	cfg := w22ConfigWithDaemon(t, t.TempDir(), "4", []w113WS{{key: ws, projects: []string{"e2e"}}})
	w22WriteFile(t, filepath.Join(ws, ".polyforge.yaml"),
		"summarize:\n  models: [\"pi:zeta\"]\nsteps:\n  e2e:\n    fix: { models: [\"pi:zeta\", \"pi:alpha\"] }\n")
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	d.start()

	out, code := w3PF(t, nil, w113Env{config: cfg, socket: d.sock}, "", "config", "workspace", "--cwd", ws)
	if code != 0 {
		t.Fatalf("pf config workspace --cwd with a daemon: exit %d (%q)", code, out)
	}
	doc := w22JSON(t, "config workspace", out)
	if complete, ok := w22Field(doc, "steps_complete"); ok && complete == false {
		t.Fatalf("with the daemon's workflows dir known the candidate read must be complete; got %v", doc)
	}
	declared := w22Declared(doc)
	fix, ok := declared["fix"]
	if !ok {
		t.Fatalf("the declared step `fix` is missing from the read (declared entries: %v)", declared)
	}
	if v, _ := fix["declared"].(bool); !v {
		t.Fatalf("`fix` is declared in the file but reported declared=false: %v", fix)
	}
	if got := w22Str(fix["models"]); got != "pi:zeta,pi:alpha" {
		t.Fatalf("`fix` candidates must keep the file order (pi:zeta,pi:alpha); got %q", got)
	}
	undeclared, ok := declared["code"]
	if !ok {
		t.Fatalf("a step the file does not declare must still be reported (as undeclared); got %v", declared)
	}
	if v, _ := undeclared["declared"].(bool); v {
		t.Fatalf("`code` is not declared in the file but reported declared=true: %v", undeclared)
	}
	if models, ok := undeclared["models"]; ok && models != nil && w22Str(models) != "" {
		t.Fatalf("an undeclared step must carry no composed list (never a bundle default); got %v", models)
	}
}

// ---------------------------------------------------------------------------
// Scenario 9 — the daemon's loaded cap beside the file value.
// ---------------------------------------------------------------------------

// TestW22DaemonMaxConfiguredFact pins the read-only status fact the panel's
// stale-reason column needs: `queue.max_configured` is the daemon's last
// applied cap, so a temporary `pf ops max` override is distinguishable from a
// file value the daemon has not reloaded, and a reload moves both.
func TestW22DaemonMaxConfiguredFact(t *testing.T) {
	ws := t.TempDir()
	cfg := w22ConfigWithDaemon(t, t.TempDir(), "6", []w113WS{{key: ws, projects: []string{"e2e"}}})
	d := w113NewDaemon(t, nil, w113DOptConfig(cfg))
	d.start()
	env := w113Env{config: cfg, socket: d.sock, json: true}

	readCap := func() (max, configured float64) {
		out, code := w3PF(t, nil, env, "", "ops", "status")
		if code != 0 {
			t.Fatalf("pf ops status: exit %d (%q)", code, out)
		}
		doc := w22JSON(t, "ops status", out)
		m, ok := w22Field(doc, "queue", "max_concurrent")
		if !ok {
			t.Fatalf("ops status has no queue.max_concurrent: %v", doc)
		}
		c, ok := w22Field(doc, "queue", "max_configured")
		if !ok {
			t.Fatalf("ops status must report the additive, read-only queue.max_configured (the panel's stale reason needs it); got %v", doc)
		}
		mf, _ := m.(float64)
		cf, _ := c.(float64)
		return mf, cf
	}

	if max, configured := readCap(); max != 6 || configured != 6 {
		t.Fatalf("at startup max_concurrent and max_configured must both be the file's 6; got %v/%v", max, configured)
	}
	if out, code := w3PF(t, nil, env, "", "ops", "max", "2"); code != 0 {
		t.Fatalf("pf ops max 2: exit %d (%q)", code, out)
	}
	if max, configured := readCap(); max != 2 || configured != 6 {
		t.Fatalf("a pf ops max override must leave max_configured at the configured 6; got max=%v configured=%v", max, configured)
	}
	raw := string(w22Read(t, cfg))
	w22WriteFile(t, cfg, strings.Replace(raw, "max_concurrent_jobs = 6", "max_concurrent_jobs = 9", 1))
	pid := d.cmd.Process.Pid
	if out, code := w3PF(t, nil, env, "", "config", "reload"); code != 0 {
		t.Fatalf("pf config reload after a hand edit: exit %d (%q)", code, out)
	}
	if d.cmd.Process.Pid != pid {
		t.Fatalf("a reload must not restart the daemon (%d -> %d)", pid, d.cmd.Process.Pid)
	}
	if max, configured := readCap(); max != 9 || configured != 9 {
		t.Fatalf("after the reload both the effective cap and max_configured must be the file's 9; got max=%v configured=%v", max, configured)
	}
}
