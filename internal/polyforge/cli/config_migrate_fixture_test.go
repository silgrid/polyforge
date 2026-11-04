package cli

// config_migrate_fixture_test.go: the real-data pin of — the
// three workspace blocks as recorded on 2026-11-02 (summarize: identical
// models, interval_sec 120, the languages Chinese / Japanese / Chinese;
// memory_policy: one path with an extra project entry, one with a
// differently worded default), values verbatim and the workspace paths and
// project names neutral. `pf config migrate` runs on a temp copy of the
// tree through the config verb: the recorded values refuse (they disagree
// on language), and once the source is aligned (the owner's deploy step)
// the move keeps every project's effective policy byte-equal, the
// summarize values equal, every other yaml byte, and a second run is a
// no-op.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
	"gopkg.in/yaml.v3"
)

// copyW286Fixture copies testdata/w286 into a temp root, rooting the
// cli.toml mapping there, and answers the root.
func copyW286Fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.Walk(filepath.Join("testdata", "w286"), func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join("testdata", "w286"), p)
		dst := filepath.Join(root, rel)
		if fi.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		raw = bytes.ReplaceAll(raw, []byte("@ROOT@"), []byte(root))
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// yamlEffectivePolicy computes a project's effective policy straight from
// a legacy yaml's memory_policy block (decoded independently of the
// migration), through the unchanged merge rule.
func yamlEffectivePolicy(t *testing.T, raw []byte, project string) []byte {
	t.Helper()
	var doc struct {
		MemoryPolicy map[string]struct {
			Record *[]string `yaml:"record"`
			Skip   *[]string `yaml:"skip"`
		} `yaml:"memory_policy"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	entries := map[string]workspace.PolicyEntry{}
	for k, v := range doc.MemoryPolicy {
		var e workspace.PolicyEntry
		if v.Record != nil {
			e.Record = *v.Record
		}
		if v.Skip != nil {
			e.Skip = *v.Skip
		}
		entries[k] = e
	}
	blob, _ := json.Marshal(workspace.MemoryPolicyFor(entries, project))
	return blob
}

func TestMigrateRealDataFixtureKeepsMeaning(t *testing.T) {
	root := copyW286Fixture(t)
	cfgPath := filepath.Join(root, "cli.toml")
	out := &bytes.Buffer{}
	ctx := &Context{ConfigPath: cfgPath, Stdout: out, Stderr: out}
	ws := map[string]string{"alpha": filepath.Join(root, "ws-alpha"), "beta": filepath.Join(root, "ws-beta"), "gamma": filepath.Join(root, "ws-gamma")}
	projects := map[string][]string{"alpha": {"alpha"}, "beta": {"beta", "beta-two"}, "gamma": {"gamma"}}

	// Every fixture yaml is refused whole by the loader, naming the file,
	// the line and the migrate command.
	for name, dir := range ws {
		_, err := workspace.Load(dir, "", projects[name])
		if err == nil {
			t.Fatalf("%s: a yaml carrying the retired sections loaded", name)
		}
		for _, needle := range []string{workspace.PathFor(dir), "line ", "`summarize:`", "`memory_policy:`", "pf config migrate"} {
			if !strings.Contains(err.Error(), needle) {
				t.Fatalf("%s: the refusal lacks %q: %v", name, needle, err)
			}
		}
	}

	// The recorded values disagree on language: refused, nothing written.
	before := map[string][]byte{cfgPath: mustReadBytes(t, cfgPath)}
	for _, dir := range ws {
		before[workspace.PathFor(dir)] = mustReadBytes(t, workspace.PathFor(dir))
	}
	err := runConfig(ctx, nil, []string{"migrate"})
	if err == nil || !strings.Contains(err.Error(), `language="Japanese"`) || !strings.Contains(err.Error(), `language="Chinese"`) {
		t.Fatalf("the recorded language disagreement must refuse listing both values, got %v", err)
	}
	for f, b := range before {
		if !bytes.Equal(mustReadBytes(t, f), b) {
			t.Fatalf("the refused run wrote %s", f)
		}
	}

	// The owner's deploy step aligns the source (beta: Japanese → Chinese).
	betaYaml := workspace.PathFor(ws["beta"])
	aligned := bytes.Replace(before[betaYaml], []byte(`language: "Japanese"`), []byte(`language: "Chinese"`), 1)
	if err := os.WriteFile(betaYaml, aligned, 0o644); err != nil {
		t.Fatal(err)
	}
	before[betaYaml] = aligned
	wantPolicy := map[string][]byte{}
	for name, dir := range ws {
		for _, p := range projects[name] {
			wantPolicy[p] = yamlEffectivePolicy(t, before[workspace.PathFor(dir)], p)
		}
	}

	out.Reset()
	if err := runConfig(ctx, nil, []string{"migrate"}); err != nil {
		t.Fatalf("migrate on the aligned fixture: %v\n%s", err, out)
	}
	cfg, err := LoadFileConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	models, lang, interval := cfg.Summarize.Lane()
	if !reflect.DeepEqual(models, []string{"pi:example-gateway/deepseek-v4.1-flash-1017", "claude:claude-haiku-5-5"}) || lang != "Chinese" || interval != 120 {
		t.Fatalf("summarize after migrate = %v %q %d", models, lang, interval)
	}
	for name, dir := range ws {
		for _, p := range projects[name] {
			got, _ := json.Marshal(workspace.MemoryPolicyFor(cfg.MemoryPolicy[dir], p))
			if !bytes.Equal(got, wantPolicy[p]) {
				t.Fatalf("project %s: effective policy changed:\nbefore %s\nafter  %s", p, wantPolicy[p], got)
			}
		}
	}
	text := string(mustReadBytes(t, cfgPath))
	if !strings.HasPrefix(text, string(before[cfgPath])) || strings.Count(text, "[summarize]") != 1 {
		t.Fatalf("cli.toml must keep its bytes and gain one [summarize] table:\n%s", text)
	}
	if !strings.Contains(text, `"Process logs, step outputs, full spec/plan (already in the ticket and capsule)",`) {
		t.Fatalf("a comma-bearing item did not move verbatim:\n%s", text)
	}

	// Each yaml loses exactly the two sections (and the init comment
	// block above memory_policy); every other byte stays.
	for name, dir := range ws {
		y := workspace.PathFor(dir)
		got := string(mustReadBytes(t, y))
		pre := string(before[y])
		want := pre[:strings.Index(pre, "summarize:\n")]
		if got != want {
			t.Fatalf("%s after migrate:\n%q\nwant\n%q", name, got, want)
		}
		if _, err := workspace.Load(dir, "", projects[name]); err != nil {
			t.Fatalf("%s: the migrated yaml does not load: %v", name, err)
		}
	}

	// A second run is a no-op.
	after := map[string][]byte{cfgPath: mustReadBytes(t, cfgPath)}
	for _, dir := range ws {
		after[workspace.PathFor(dir)] = mustReadBytes(t, workspace.PathFor(dir))
	}
	out.Reset()
	if err := runConfig(ctx, nil, []string{"migrate"}); err != nil || strings.TrimSpace(out.String()) != "nothing to migrate" {
		t.Fatalf("second run: %v %q", err, out)
	}
	for f, b := range after {
		if !bytes.Equal(mustReadBytes(t, f), b) {
			t.Fatalf("the no-op run changed %s", f)
		}
	}
}

func mustReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
