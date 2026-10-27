package runtime

// The dedicated step agent directory (audit-subtraction pass B, addendum
// 2 / owner ruling A): the minimal-set invariant, the copy (not link)
// discipline, the re-sync on a changed source, the placement under the
// daemon data dir, and the loud refusal when an explicitly configured
// source carries no credentials.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// operatorDirFixture lays out a realistic operator agent directory: the
// minimal set AND the isolation set (settings.json declaring a
// user-level package — the reinstall-poison shape — plus the other
// instruction surfaces that must never be snapshotted).
func operatorDirFixture(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("auth.json", `{"token":"operator-secret"}`)
	write("models.json", `{"providers":{"x":{}}}`)
	if err := os.Chmod(filepath.Join(src, "models.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	write("agents/reviewer.md", "# reviewer subagent\n")
	// the subagent TOOL: the one extension the snapshot carries
	write("extensions/subagent/index.ts", "// subagent tool\n")
	write("extensions/subagent/agents.ts", "// subagent discovery\n")
	write("agents/nested/deep.md", "# nested subagent\n")
	// the isolation set — none of this may ever be copied
	write("settings.json", `{"packages":["npm:pi-background-tasks"]}`)
	write("keybindings.json", `{}`)
	write("mcp.json", `{}`)
	write("AGENTS.md", "operator context\n")
	write("extensions/ext.js", "// operator extension\n")
	write("extensions/polyforge/index.ts", "// interactive-session extension\n")
	write("skills/op/SKILL.md", "operator skill\n")
	return src
}

func listDirNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func TestStepAgentDirMinimalSetAndPlacement(t *testing.T) {
	src := operatorDirFixture(t)
	cfg := Config{DataDir: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
	cfg.FillDefaults()
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	dir := cfg.StepAgentDir()
	// A1: under the daemon data dir, never inside a capsule.
	if rel, err := filepath.Rel(cfg.DataDir, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the dedicated dir %s is not under the data dir %s", dir, cfg.DataDir)
	}
	// A2: EXACTLY the enumerated set.
	got := listDirNames(t, dir)
	want := map[string]bool{"auth.json": true, "models.json": true, "agents": true, "extensions": true}
	if len(got) != len(want) {
		t.Fatalf("the dedicated dir must hold exactly auth.json/models.json/agents/extensions, got %v", got)
	}
	for _, n := range got {
		if !want[n] {
			t.Fatalf("unexpected entry %q in the dedicated dir (%v)", n, got)
		}
	}
	// the subagent definitions travel, recursively
	for _, rel := range []string{"agents/reviewer.md", "agents/nested/deep.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was not copied: %v", rel, err)
		}
	}
	// extensions/ carries the subagent tool and NOTHING else
	if exts := listDirNames(t, filepath.Join(dir, "extensions")); len(exts) != 1 || exts[0] != "subagent" {
		t.Fatalf("extensions/ must hold exactly subagent, got %v", exts)
	}
	for _, rel := range []string{"extensions/subagent/index.ts", "extensions/subagent/agents.ts"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was not copied: %v", rel, err)
		}
	}
	// models.json keeps the source's mode (0600 here)
	if ms, err := os.Stat(filepath.Join(dir, "models.json")); err != nil || ms.Mode().Perm() != 0o600 {
		t.Fatalf("models.json must keep the source mode 0600: %v %v", ms, err)
	}
	// credentials land 0600 and are a COPY (no symlink into the
	// operator's directory, which would re-open the whole dir)
	st, err := os.Lstat(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("auth.json must be a copy, never a symlink into the operator agent dir")
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json must be 0600, got %v", perm)
	}
}

func TestStepAgentDirNeverCopiesSettings(t *testing.T) {
	src := operatorDirFixture(t)
	cfg := Config{DataDir: t.TempDir(), HarnessRoot: t.TempDir(), OperatorAgentDir: src}
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// the whole tree, not just the top level: no settings.json and no
	// package declaration anywhere under the dedicated directory
	err := filepath.WalkDir(cfg.StepAgentDir(), func(p string, de os.DirEntry, err error) error {
		if err != nil || !de.Type().IsRegular() {
			return err
		}
		if de.Name() == "settings.json" {
			t.Fatalf("settings.json (the user-level package declaration) was copied to %s", p)
		}
		raw, rerr := os.ReadFile(p)
		if rerr == nil && strings.Contains(string(raw), "pi-background-tasks") {
			t.Fatalf("a background-task package declaration reached %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStepAgentDirResyncFollowsSourceAndPrunesExtras(t *testing.T) {
	src := operatorDirFixture(t)
	cfg := Config{DataDir: t.TempDir(), HarnessRoot: t.TempDir(), OperatorAgentDir: src}
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	dir := cfg.StepAgentDir()
	// an operator credential rotation (size+mtime change) is picked up
	// by the next sync — the per-spawn re-check
	rotated := `{"token":"rotated-secret-value"}`
	authSrc := filepath.Join(src, "auth.json")
	if err := os.WriteFile(authSrc, []byte(rotated), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(authSrc, future, future); err != nil {
		t.Fatal(err)
	}
	// something (a stray write, an older layout) put extras in the
	// dedicated dir: the sync restores the minimal-set invariant
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"packages":["npm:x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "extensions", "stray"), 0o755); err != nil {
		t.Fatal(err)
	}
	// a source file that disappeared must disappear here too
	if err := os.Remove(filepath.Join(src, "models.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(src, "agents", "reviewer.md")); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil || string(raw) != rotated {
		t.Fatalf("the rotated credentials were not re-copied: %q (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "models.json")); !os.IsNotExist(err) {
		t.Fatalf("a models.json the source no longer has must be dropped: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "reviewer.md")); !os.IsNotExist(err) {
		t.Fatalf("a deleted subagent definition must be dropped: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "nested", "deep.md")); err != nil {
		t.Fatalf("a surviving subagent definition must stay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "subagent", "index.ts")); err != nil {
		t.Fatalf("the subagent extension must survive the prune: %v", err)
	}
	for _, extra := range []string{"settings.json", "extensions/stray"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(extra))); !os.IsNotExist(err) {
			t.Fatalf("the sync must prune %q from the dedicated dir: %v", extra, err)
		}
	}
}

// The per-spawn refresh touches only the allowlisted members: an extra
// entry is left alone (a live step process may be using the directory);
// the prune is the daemon-start sync's job.
func TestStepAgentDirPerSpawnRefreshNeverPrunes(t *testing.T) {
	src := operatorDirFixture(t)
	cfg := Config{DataDir: t.TempDir(), HarnessRoot: t.TempDir(), OperatorAgentDir: src}
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("daemon-start sync: %v", err)
	}
	dir := cfg.StepAgentDir()
	extra := filepath.Join(dir, "extra.txt")
	if err := os.WriteFile(extra, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the subagent extension leaves the source: the refresh drops it
	// (allowlisted member, absent source ⇒ absent here)
	if err := os.RemoveAll(filepath.Join(src, "extensions", "subagent")); err != nil {
		t.Fatal(err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("the per-spawn refresh must not prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "subagent")); !os.IsNotExist(err) {
		t.Fatalf("a subagent extension the source no longer has must be dropped: %v", err)
	}
	// the next daemon start prunes the extra (and the now-empty extensions/)
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("daemon-start sync: %v", err)
	}
	for _, gone := range []string{"extra.txt", "extensions"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("the daemon-start sync must prune %s: %v", gone, err)
		}
	}
}

func TestStepAgentDirExplicitSourceWithoutCredentialsRefuses(t *testing.T) {
	src := t.TempDir() // configured, but no auth.json
	cfg := Config{DataDir: t.TempDir(), HarnessRoot: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
	err := cfg.SyncStepAgentDir()
	if err == nil {
		t.Fatal("an explicitly configured source without credentials must refuse (a step agent without credentials is a dead end)")
	}
	var sae *StepAgentDirError
	if !errors.As(err, &sae) {
		t.Fatalf("the refusal must be the typed StepAgentDirError, got %T: %v", err, err)
	}
	// the implicit default (no flag/env) carries on instead: the harness
	// adapter may hold its credentials elsewhere
	cfg2 := Config{DataDir: t.TempDir(), HarnessRoot: t.TempDir(), OperatorAgentDir: src}
	if err := cfg2.SyncStepAgentDir(); err != nil {
		t.Fatalf("the implicit default without file credentials must carry on, got: %v", err)
	}
	if _, err := os.Stat(cfg2.StepAgentDir()); err != nil {
		t.Fatalf("the dedicated dir must exist even without credentials: %v", err)
	}
}

func TestStepAgentDirDefaultsAndSessionsAreSeparate(t *testing.T) {
	cfg := Config{DataDir: t.TempDir()}
	cfg.FillDefaults()
	if cfg.OperatorAgentDir == "" {
		t.Fatal("the source defaults to the operator agent dir")
	}
	if cfg.OperatorAgentDirExplicit {
		t.Fatal("a defaulted source is never explicit (its missing credentials are a warning, not a refusal)")
	}
	if cfg.HarnessRoot != cfg.DataDir || cfg.HarnessRoot == "" {
		t.Fatalf("the harness root must default to the data dir (only the op-observable default path; the flag/env override moves it), got %q (data %q)", cfg.HarnessRoot, cfg.DataDir)
	}
	if cfg.StepAgentDir() != filepath.Join(cfg.HarnessRoot, StepAgentDirName) {
		t.Fatalf("the dedicated dir must derive from the harness root, got %q", cfg.StepAgentDir())
	}
	if cfg.StepAgentSessionsDir() != filepath.Join(cfg.HarnessRoot, StepAgentSessionsDirName) {
		t.Fatalf("the session store must derive from the harness root, got %q", cfg.StepAgentSessionsDir())
	}
	if rel, err := filepath.Rel(cfg.DataDir, cfg.StepAgentDir()); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("the dedicated dir %s is not under the data dir %s", cfg.StepAgentDir(), cfg.DataDir)
	}
	// polyforge#171: the three dirs are siblings under ONE harness root.
	if n, want := cfg.ClaudeConfigDir(), filepath.Join(cfg.HarnessRoot, ClaudeConfigDirName); n != want {
		t.Fatalf("claude-config derivation %q, want %q", n, want)
	}
	if cfg.ClaudeConfigDir() == cfg.StepAgentSessionsDir() || cfg.StepAgentDir() == cfg.ClaudeConfigDir() {
		t.Fatal("the harness dirs are not siblings under one root")
	}
	// an explicit harness-root override moves the whole tree, never just
	// one dir (polyforge#171 unification)
	over := Config{DataDir: cfg.DataDir, HarnessRoot: t.TempDir()}
	over.FillDefaults()
	if over.HarnessRoot == over.DataDir {
		t.Fatal("an explicit harness root was overwritten by the data-dir default")
	}
	if over.StepAgentDir() != filepath.Join(over.HarnessRoot, StepAgentDirName) ||
		over.StepAgentSessionsDir() != filepath.Join(over.HarnessRoot, StepAgentSessionsDirName) ||
		over.ClaudeConfigDir() != filepath.Join(over.HarnessRoot, ClaudeConfigDirName) {
		t.Fatalf("the harness-root override did not move the three dirs: %+v", over)
	}
	// the sessions live OUTSIDE the dedicated agent dir, so the minimal
	// set stays exactly the enumerated one
	if rel, err := filepath.Rel(cfg.StepAgentDir(), cfg.StepAgentSessionsDir()); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("the session store %s must not sit inside the dedicated agent dir %s", cfg.StepAgentSessionsDir(), cfg.StepAgentDir())
	}
}
