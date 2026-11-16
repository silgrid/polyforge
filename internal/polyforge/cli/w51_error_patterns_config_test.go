package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

// w51_error_patterns_config_test.go (polyforge#51): the two additional
// failure-classification pattern lists and the two slow-model latency
// thresholds as [daemon] keys — parse, validate, write rendering, read-back
// and the Host-key inheritance of a named Cell's file.

func w51Key(t *testing.T, name string) configKey {
	t.Helper()
	k, err := parseConfigKey(name)
	if err != nil {
		t.Fatalf("parseConfigKey(%q): %v", name, err)
	}
	if k.kind != keyDaemon {
		t.Fatalf("parseConfigKey(%q) kind = %d, want keyDaemon", name, k.kind)
	}
	if classOfKey(k) != ConfigLive {
		t.Fatalf("%s must be a live key", name)
	}
	return k
}

func TestW51ErrorPatternsKeysInKeyModel(t *testing.T) {
	for _, name := range []string{"transient_error_patterns", "model_error_patterns"} {
		w51Key(t, name)
		s := ConfigSchema(name)
		if s == nil || s["kind"] != "regex-list" {
			t.Fatalf("ConfigSchema(%q) = %v, want kind regex-list", name, s)
		}
	}
	for _, name := range []string{"slow_model_median_latency", "slow_model_p90_latency"} {
		w51Key(t, name)
		s := ConfigSchema(name)
		if s == nil || s["kind"] != "duration" {
			t.Fatalf("ConfigSchema(%q) = %v, want kind duration", name, s)
		}
	}
}

func TestW51ErrorPatternsParse(t *testing.T) {
	s, err := parseDaemonTable("cli.toml", []daemonKV{
		{key: "transient_error_patterns", val: `['\bupstream\b hiccup', "payment retry"]`, line: 3},
		{key: "model_error_patterns", val: `['model "x" unavailable']`, line: 4},
		{key: "slow_model_median_latency", val: `"61s"`, line: 5},
		{key: "slow_model_p90_latency", val: `"3m"`, line: 6},
	})
	if err != nil {
		t.Fatalf("parseDaemonTable: %v", err)
	}
	if len(s.TransientErrorPatterns) != 2 || s.TransientErrorPatterns[0] != `\bupstream\b hiccup` || s.TransientErrorPatterns[1] != "payment retry" {
		t.Fatalf("TransientErrorPatterns = %q, want the two patterns verbatim (a backslash item reads back as written)", s.TransientErrorPatterns)
	}
	if len(s.ModelErrorPatterns) != 1 || s.ModelErrorPatterns[0] != `model "x" unavailable` {
		t.Fatalf("ModelErrorPatterns = %q", s.ModelErrorPatterns)
	}
	if s.SlowModelMedianLatency != 61*time.Second || s.SlowModelP90Latency != 3*time.Minute {
		t.Fatalf("slow thresholds = %s / %s, want 61s / 3m", s.SlowModelMedianLatency, s.SlowModelP90Latency)
	}
}

// TestW51ErrorPatternsFileParse: a multi-line array inside the [daemon]
// table parses through the same logical-line continuation the projects
// array uses, and a whole file loads through the shared parser.
func TestW51ErrorPatternsFileParse(t *testing.T) {
	text := "[daemon]\ntransient_error_patterns = ['\\b402\\b',\n  'overload',]\nslow_model_p90_latency = \"45s\"\n"
	cfg, err := ParseConfigBytes("cli.toml", []byte(text))
	if err != nil {
		t.Fatalf("ParseConfigBytes: %v", err)
	}
	if len(cfg.Daemon.TransientErrorPatterns) != 2 || cfg.Daemon.TransientErrorPatterns[0] != `\b402\b` || cfg.Daemon.TransientErrorPatterns[1] != "overload" {
		t.Fatalf("TransientErrorPatterns = %q", cfg.Daemon.TransientErrorPatterns)
	}
	if cfg.Daemon.SlowModelP90Latency != 45*time.Second {
		t.Fatalf("SlowModelP90Latency = %s", cfg.Daemon.SlowModelP90Latency)
	}
}

func TestW51ErrorPatternsRefusals(t *testing.T) {
	for _, kv := range []struct {
		key, val, wantReason string
	}{
		{"transient_error_patterns", `'('`, "compilable RE2 regex"},
		{"model_error_patterns", `['(?=x)']`, "compilable RE2 regex"},
		{"transient_error_patterns", `['']`, "empty"},
		{"transient_error_patterns", `'oops'`, "array of quoted"},
		{"transient_error_patterns", `["ok", 'unterminated']`, "array of quoted"},
		{"slow_model_median_latency", `"0"`, "positive Go duration"},
		{"slow_model_p90_latency", `"abc"`, "Go duration"},
		{"slow_model_median_latency", `61s`, "quoted"},
	} {
		if _, err := parseDaemonTable("cli.toml", []daemonKV{{key: kv.key, val: kv.val, line: 7}}); err == nil ||
			!strings.Contains(err.Error(), kv.key) || !strings.Contains(err.Error(), kv.wantReason) {
			t.Errorf("%s = %s must be refused naming the key and %q, got %v", kv.key, kv.val, kv.wantReason, err)
		}
	}
}

// TestW51ErrorPatternsRoundTrip: `pf config set` takes a JSON list and
// renders TOML whose items parse back to the exact patterns — a backslash
// pattern is written as a single-quoted literal so it survives the parser
// (polyforge#51 Q2: the parser stays strict, the writer adapts).
func TestW51ErrorPatternsRoundTrip(t *testing.T) {
	key := w51Key(t, "transient_error_patterns")
	val, _, err := configSetValue(nil, key, []string{`["\\b402\\b", "payment required"]`})
	if err != nil {
		t.Fatalf("configSetValue: %v", err)
	}
	if !strings.HasPrefix(val, "[") || !strings.Contains(val, `'`) {
		t.Fatalf("rendered value %q must be a TOML array using a single-quoted literal for the backslash item", val)
	}
	s, err := parseDaemonTable("cli.toml", []daemonKV{{key: "transient_error_patterns", val: val, line: 1}})
	if err != nil {
		t.Fatalf("the rendered value must parse: %v (value %q)", err, val)
	}
	if len(s.TransientErrorPatterns) != 2 || s.TransientErrorPatterns[0] != `\b402\b` || s.TransientErrorPatterns[1] != "payment required" {
		t.Fatalf("round trip = %q, want the exact patterns back", s.TransientErrorPatterns)
	}
}

// TestW51ErrorPatternsWriteRefusals: a pattern holding both ' and a
// backslash cannot be written — the refusal says so and suggests \\x27 —
// and a pattern with a newline is refused too (the TOML subset is
// one-line).
func TestW51ErrorPatternsWriteRefusals(t *testing.T) {
	key := w51Key(t, "model_error_patterns")
	_, _, err := configSetValue(nil, key, []string{`["model 'x' \\b"]`})
	if err == nil || !strings.Contains(err.Error(), "model_error_patterns") || !strings.Contains(err.Error(), `\x27`) {
		t.Fatalf("a pattern with both ' and a backslash must be refused naming the key and suggesting \\x27: %v", err)
	}
	_, _, err = configSetValue(nil, key, []string{`["a\nb"]`})
	if err == nil || !strings.Contains(err.Error(), "model_error_patterns") {
		t.Fatalf("a pattern with a newline must be refused naming the key: %v", err)
	}
	if _, _, err := configSetValue(nil, key, []string{`not json`}); err == nil {
		t.Fatal("a non-JSON value must be refused")
	}
	if val, _, err := configSetValue(nil, key, []string{`[]`}); err != nil || val != "[]" {
		t.Fatalf("an empty list must write an empty array: %q, %v", val, err)
	}
}

// TestW51ErrorPatternsListRendering: `pf config list` renders the lists and
// the documented defaults of the two thresholds.
func TestW51ErrorPatternsListRendering(t *testing.T) {
	s := DaemonSettings{
		TransientErrorPatterns: []string{`\b402\b`},
		SlowModelMedianLatency:  61 * time.Second,
	}
	if got := daemonListValue(s, "transient_error_patterns"); len(got) != 1 || got[0] != `\b402\b` {
		t.Fatalf("daemonListValue(transient) = %q", got)
	}
	if got := daemonListValue(s, "model_error_patterns"); got != nil {
		t.Fatalf("daemonListValue(model) = %q, want nil when unset", got)
	}
	if got := daemonValue(s, "slow_model_median_latency"); got != "1m1s" {
		t.Fatalf("daemonValue(slow_model_median_latency) = %q", got)
	}
	if daemonDefault("slow_model_median_latency") != "60s" || daemonDefault("slow_model_p90_latency") != "180s" {
		t.Fatalf("the documented defaults must render: %q / %q", daemonDefault("slow_model_median_latency"), daemonDefault("slow_model_p90_latency"))
	}
}

// TestW51ErrorPatternsInheritHost: a named Cell's resolved config inherits
// the Host's [daemon] Host-only values, like max_concurrent_jobs.
func TestW51ErrorPatternsInheritHost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli-alpha.toml")
	if err := os.WriteFile(path, []byte("server = \"http://hub:1\"\napi_key = \"k\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	host := Config{Daemon: DaemonSettings{
		TransientErrorPatterns: []string{`\b402\b`},
		SlowModelP90Latency:    3 * time.Minute,
	}, Mapping: &mapping.Plan{}}
	cell, err := LoadCellFile(host, path)
	if err != nil {
		t.Fatalf("LoadCellFile: %v", err)
	}
	if len(cell.Daemon.TransientErrorPatterns) != 1 || cell.Daemon.TransientErrorPatterns[0] != `\b402\b` {
		t.Fatalf("the Cell must inherit the Host's patterns: %q", cell.Daemon.TransientErrorPatterns)
	}
	if cell.Daemon.SlowModelP90Latency != 3*time.Minute {
		t.Fatalf("the Cell must inherit the Host's slow threshold: %s", cell.Daemon.SlowModelP90Latency)
	}
}
