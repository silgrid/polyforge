package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// migrateRig is one cli.toml mapping the given workspaces (each its own
// project), and a Context running the verb against it.
type migrateRig struct {
	ctx  *Context
	out  *bytes.Buffer
	cfg  string
	dirs map[string]string
}

func newMigrateRig(t *testing.T, projects ...string) *migrateRig {
	t.Helper()
	dir := t.TempDir()
	r := &migrateRig{out: &bytes.Buffer{}, cfg: filepath.Join(dir, "cli.toml"), dirs: map[string]string{}}
	body := "# operator comment\nserver = \"http://127.0.0.1:1\"\n"
	for _, p := range projects {
		ws := filepath.Join(dir, "ws-"+p)
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		r.dirs[p] = ws
		body += "\n[workspace.\"" + ws + "\"]\nprojects = [\"" + p + "\"]\n"
	}
	mustWrite(t, r.cfg, body)
	r.ctx = &Context{ConfigPath: r.cfg, Stdout: r.out, Stderr: r.out}
	return r
}

func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func backups(t *testing.T, file string) []string {
	t.Helper()
	got, _ := filepath.Glob(file + ".bak-*")
	return got
}

func legacyYaml(project, language, record string) string {
	return "summarize:\n  models: [\"pi:m-a\"]\n  language: \"" + language + "\"\n" +
		"memory_policy:\n  default:\n    record: [\"" + record + "\"]\n  " + project + ":\n    skip: []\n" +
		"steps:\n  " + project + ":\n    fix: { models: [\"pi:s\"] }\n"
}

// TestMigrateMovesConvergesAndBacksUp pins the apply half: values land
// verbatim in cli.toml (one [summarize], one block per path, in yaml entry
// order), the yamls lose exactly the sections, every changed file gets one
// backup of its pre-change bytes, a workspace without a yaml is skipped,
// and the second run is `nothing to migrate` with zero diff and no backup.
func TestMigrateMovesConvergesAndBacksUp(t *testing.T) {
	r := newMigrateRig(t, "alpha", "beta", "gamma")
	ya, yb := workspace.PathFor(r.dirs["alpha"]), workspace.PathFor(r.dirs["beta"])
	mustWrite(t, ya, legacyYaml("alpha", "Chinese", "alpha, with comma"))
	mustWrite(t, yb, legacyYaml("beta", "Chinese", "beta note"))
	before := map[string]string{r.cfg: mustRead(t, r.cfg), ya: mustRead(t, ya), yb: mustRead(t, yb)}
	if err := runConfigMigrate(r.ctx, nil); err != nil {
		t.Fatalf("migrate: %v\n%s", err, r.out)
	}
	cfg, err := LoadFileConfig(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m, l, i := cfg.Summarize.Lane(); strings.Join(m, ",") != "pi:m-a" || l != "Chinese" || i != 60 {
		t.Fatalf("summarize = %v %q %d", m, l, i)
	}
	if strings.Contains(mustRead(t, r.cfg), "interval_sec") {
		t.Fatal("an undeclared optional key was invented")
	}
	if got := cfg.MemoryPolicy[r.dirs["alpha"]]["default"].Record; len(got) != 1 || got[0] != "alpha, with comma" {
		t.Fatalf("alpha default = %#v", got)
	}
	if got := cfg.MemoryPolicy[r.dirs["beta"]]["beta"]; got.Skip == nil || len(got.Skip) != 0 || got.Record != nil {
		t.Fatalf("beta entry = %#v (a declared empty list stays declared)", got)
	}
	if !strings.HasPrefix(mustRead(t, r.cfg), before[r.cfg]) {
		t.Fatal("cli.toml lost a pre-existing byte")
	}
	for _, y := range []string{ya, yb} {
		if secs, _ := workspace.LegacySections([]byte(mustRead(t, y))); len(secs) != 0 || !strings.Contains(mustRead(t, y), "fix: { models: [\"pi:s\"] }") {
			t.Fatalf("%s after migrate:\n%s", y, mustRead(t, y))
		}
	}
	for file, pre := range before {
		b := backups(t, file)
		if len(b) != 1 || mustRead(t, b[0]) != pre {
			t.Fatalf("%s backups = %v (want exactly one holding the pre-change bytes)", file, b)
		}
	}
	if entries, _ := os.ReadDir(r.dirs["gamma"]); len(entries) != 0 {
		t.Fatalf("the yaml-less workspace was touched: %v", entries)
	}
	after := map[string]string{r.cfg: mustRead(t, r.cfg), ya: mustRead(t, ya), yb: mustRead(t, yb)}
	r.out.Reset()
	if err := runConfigMigrate(r.ctx, nil); err != nil || strings.TrimSpace(r.out.String()) != "nothing to migrate" {
		t.Fatalf("second run: %v %q", err, r.out)
	}
	for file, text := range after {
		if mustRead(t, file) != text || len(backups(t, file)) != 1 {
			t.Fatalf("the no-op run changed %s or backed it up again", file)
		}
	}
}

// TestMigrateConvergesAfterInterruption pins the write order: with
// cli.toml already holding the values (an interruption after its write),
// the re-run removes only the yaml sections and leaves cli.toml alone.
func TestMigrateConvergesAfterInterruption(t *testing.T) {
	r := newMigrateRig(t, "alpha")
	y := workspace.PathFor(r.dirs["alpha"])
	mustWrite(t, y, legacyYaml("alpha", "Chinese", "note"))
	if err := runConfigMigrate(r.ctx, nil); err != nil {
		t.Fatal(err)
	}
	done := mustRead(t, r.cfg)
	// rewind the yaml only: the state an interruption leaves behind
	mustWrite(t, y, legacyYaml("alpha", "Chinese", "note"))
	for _, b := range append(backups(t, r.cfg), backups(t, y)...) {
		_ = os.Remove(b)
	}
	if err := runConfigMigrate(r.ctx, nil); err != nil {
		t.Fatalf("the converging run: %v", err)
	}
	if mustRead(t, r.cfg) != done || len(backups(t, r.cfg)) != 0 {
		t.Fatal("the converging run rewrote cli.toml")
	}
	if secs, _ := workspace.LegacySections([]byte(mustRead(t, y))); len(secs) != 0 || len(backups(t, y)) != 1 {
		t.Fatal("the converging run did not finish the yaml")
	}
}

// TestMigrateRefusesWithZeroWrites pins refuse-don't-pick: disagreeing
// summarize values between workspaces, against an existing [summarize],
// and a same-path policy conflict each fail itemized without a write;
// identical existing values are no conflict.
func TestMigrateRefusesWithZeroWrites(t *testing.T) {
	check := func(t *testing.T, r *migrateRig, want ...string) {
		t.Helper()
		files := []string{r.cfg}
		for _, ws := range r.dirs {
			files = append(files, workspace.PathFor(ws))
		}
		before := map[string]string{}
		for _, f := range files {
			before[f], _ = func() (string, error) { b, e := os.ReadFile(f); return string(b), e }()
		}
		err := runConfigMigrate(r.ctx, nil)
		if err == nil {
			t.Fatal("a disagreement was migrated")
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Fatalf("the refusal lacks %q: %v", w, err)
			}
		}
		for _, f := range files {
			if b, _ := os.ReadFile(f); string(b) != before[f] || len(backups(t, f)) != 0 {
				t.Fatalf("a refused run wrote %s", f)
			}
		}
	}
	t.Run("between workspaces", func(t *testing.T) {
		r := newMigrateRig(t, "alpha", "beta")
		mustWrite(t, workspace.PathFor(r.dirs["alpha"]), legacyYaml("alpha", "Japanese", "a"))
		mustWrite(t, workspace.PathFor(r.dirs["beta"]), legacyYaml("beta", "Chinese", "b"))
		check(t, r, "summarize values disagree", "Japanese", "Chinese", workspace.PathFor(r.dirs["alpha"]))
	})
	t.Run("default equals an omitted key", func(t *testing.T) {
		r := newMigrateRig(t, "alpha", "beta")
		mustWrite(t, workspace.PathFor(r.dirs["alpha"]), legacyYaml("alpha", "中文", "a"))
		mustWrite(t, workspace.PathFor(r.dirs["beta"]), "summarize:\n  models: [\"pi:m-a\"]\n")
		if err := runConfigMigrate(r.ctx, nil); err != nil {
			t.Fatalf("an omitted language equals the default: %v", err)
		}
	})
	t.Run("against an existing table", func(t *testing.T) {
		r := newMigrateRig(t, "alpha")
		mustWrite(t, r.cfg, mustRead(t, r.cfg)+"\n[summarize]\nmodels = [\"pi:m-a\"]\nlanguage = \"English\"\n")
		mustWrite(t, workspace.PathFor(r.dirs["alpha"]), legacyYaml("alpha", "Chinese", "a"))
		check(t, r, "[summarize]", "English", "Chinese")
	})
	t.Run("same-path policy conflict", func(t *testing.T) {
		r := newMigrateRig(t, "alpha")
		ws := r.dirs["alpha"]
		mustWrite(t, r.cfg, mustRead(t, r.cfg)+"\n[memory_policy.\""+ws+"\".default]\nrecord = [\"other\"]\n")
		mustWrite(t, workspace.PathFor(ws), "memory_policy:\n  default:\n    record: [\"mine\"]\n")
		check(t, r, ws, "default")
	})
	t.Run("identical existing values", func(t *testing.T) {
		r := newMigrateRig(t, "alpha")
		ws := r.dirs["alpha"]
		mustWrite(t, r.cfg, mustRead(t, r.cfg)+"\n[memory_policy.\""+ws+"\".default]\nrecord = [\"mine\"]\n")
		pre := mustRead(t, r.cfg)
		mustWrite(t, workspace.PathFor(ws), "memory_policy:\n  default:\n    record: [\"mine\"]\n")
		if err := runConfigMigrate(r.ctx, nil); err != nil {
			t.Fatalf("identical values must not refuse: %v", err)
		}
		if mustRead(t, r.cfg) != pre {
			t.Fatal("identical values rewrote cli.toml")
		}
	})
	t.Run("invalid source value", func(t *testing.T) {
		r := newMigrateRig(t, "alpha")
		mustWrite(t, workspace.PathFor(r.dirs["alpha"]), "summarize:\n  models: [\"unprefixed\"]\n")
		check(t, r, "line 1", "summarize: models")
	})
}

// TestMigrateBackupNeverOverwrites pins the backup naming: an existing
// backup of the same second is kept and the next free suffix is taken.
func TestMigrateBackupNeverOverwrites(t *testing.T) {
	r := newMigrateRig(t, "alpha")
	y := workspace.PathFor(r.dirs["alpha"])
	mustWrite(t, y, legacyYaml("alpha", "Chinese", "a"))
	at := time.Date(2026, 11, 2, 16, 30, 0, 0, time.UTC)
	orig := migrateNow
	migrateNow = func() time.Time { return at }
	t.Cleanup(func() { migrateNow = orig })
	taken := y + ".bak-20261102T163000Z"
	mustWrite(t, taken, "an older backup")
	if err := runConfigMigrate(r.ctx, nil); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, taken) != "an older backup" {
		t.Fatal("an existing backup was overwritten")
	}
	if _, err := os.Stat(taken + "-1"); err != nil {
		t.Fatalf("the next free suffix was not taken: %v", err)
	}
}
