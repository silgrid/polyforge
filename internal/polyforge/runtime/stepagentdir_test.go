package runtime

// The dedicated step agent directory (audit-subtraction pass B, addendum
// 2 / owner ruling A): the minimal-set invariant, the copy (not link)
// discipline, the re-sync on a changed source, the placement under the
// daemon data dir, and the loud refusal when an explicitly configured
// source carries no credentials.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
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
	// the subagent TOOL: one of the two extensions the snapshot carries
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
	// the daemon-owned worker definition is embedded, regardless of the
	// operator's reviewer definition.
	for _, rel := range []string{"agents/worker.md"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was not installed: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "agents/reviewer.md")); !os.IsNotExist(err) {
		t.Fatalf("operator agent definitions must not be copied: %v", err)
	}
	// extensions/ carries the allowlisted extensions and NOTHING else
	// (os.ReadDir is name-sorted: guard sorts before subagent)
	if exts := listDirNames(t, filepath.Join(dir, "extensions")); !slices.Equal(exts, []string{"guard", "subagent"}) {
		t.Fatalf("extensions/ must hold exactly guard and subagent, got %v", exts)
	}
	for _, rel := range []string{"extensions/subagent/index.ts", "extensions/guard/index.ts", "extensions/guard/rules.ts"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was not copied: %v", rel, err)
		}
	}
	// the guard extension rides the same embed: the installed bytes are
	// the embedded bytes, and a test file never reaches the step dir
	for _, rel := range []string{"extensions/guard/index.ts", "extensions/guard/rules.ts"} {
		want, err := fs.ReadFile(stepagentassets.FS, rel)
		if err != nil {
			t.Fatalf("embedded %s: %v", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("the guard file %s must match the embedded bytes: %v", rel, err)
		}
	}
	werr := filepath.WalkDir(dir, func(p string, de os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if de.Type().IsRegular() && strings.HasSuffix(de.Name(), ".test.ts") {
			t.Errorf("a node test file reached the step agent dir: %s", p)
		}
		return nil
	})
	if werr != nil {
		t.Fatal(werr)
	}
	// Embedded bytes win over operator content, including modification.
	operatorExt := filepath.Join(src, "extensions", "subagent", "index.ts")
	if err := os.WriteFile(operatorExt, []byte("operator replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatal(err)
	}
	embeddedWant, err := fs.ReadFile(stepagentassets.FS, "extensions/subagent/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	embeddedGot, err := os.ReadFile(filepath.Join(dir, "extensions", "subagent", "index.ts"))
	if err != nil || string(embeddedGot) != string(embeddedWant) {
		t.Fatalf("embedded extension was replaced: %v", err)
	}
	// the same discipline for the guard: an operator-placed guard file
	// never replaces the embedded one (analysis §2 AC3)
	if err := os.MkdirAll(filepath.Join(src, "extensions", "guard"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extensions", "guard", "index.ts"), []byte("operator guard"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"extensions/guard/index.ts", "extensions/guard/rules.ts"} {
		want, err := fs.ReadFile(stepagentassets.FS, rel)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("the embedded %s was replaced by operator content: %v", rel, err)
		}
	}
	// models.json keeps the source mode (0600 here)
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
	if _, err := os.Stat(filepath.Join(dir, "agents", "worker.md")); err != nil {
		t.Fatalf("the built-in worker must survive refresh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "reviewer.md")); !os.IsNotExist(err) {
		t.Fatalf("operator definitions must remain absent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents", "nested")); !os.IsNotExist(err) {
		t.Fatalf("operator agent tree must remain absent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "subagent", "index.ts")); err != nil {
		t.Fatalf("the subagent extension must survive the prune: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "extensions", "guard", "index.ts")); err != nil {
		t.Fatalf("the guard extension must survive the prune: %v", err)
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
	// The embedded extension is immutable and survives any operator edit or deletion.
	if _, err := os.Stat(filepath.Join(dir, "extensions", "subagent", "index.ts")); err != nil {
		t.Fatalf("the built-in subagent extension must survive the prune: %v", err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("the per-spawn refresh must not prune: %v", err)
	}
	// the next daemon start prunes the extra
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("daemon-start sync: %v", err)
	}
	for _, gone := range []string{"extra.txt"} {
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

// TestStepAgentDirRefreshAfterOperatorExtensionDeleted pins the residual
// use case of polyforge#227 AC3: the operator deletes the extension from
// their own agent directory (and a damaged snapshot loses it too), and the
// next per-spawn refresh restores every built-in file from the daemon's
// embed while leaving operator files the snapshot legitimately carries
// alone — the refresh never prunes.
func TestStepAgentDirRefreshAfterOperatorExtensionDeleted(t *testing.T) {
	src := operatorDirFixture(t)
	cfg := Config{DataDir: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
	cfg.FillDefaults()
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	dir := cfg.StepAgentDir()
	// the operator removes the extension from their own directory; the
	// snapshot is damaged the same way in this scenario.
	if err := os.RemoveAll(filepath.Join(src, "extensions", "subagent")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "extensions", "subagent")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "extensions", "guard")); err != nil {
		t.Fatal(err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatalf("refresh after the operator deleted the extension: %v", err)
	}
	want := map[string]string{
		"agents/worker.md":              "agents/worker.md",
		"extensions/subagent/index.ts":  "extensions/subagent/index.ts",
		"extensions/subagent/agents.ts": "extensions/subagent/agents.ts",
		"extensions/subagent/SOURCE.md": "extensions/subagent/SOURCE.md",
		"extensions/guard/index.ts":     "extensions/guard/index.ts",
		"extensions/guard/rules.ts":     "extensions/guard/rules.ts",
	}
	for rel, asset := range want {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("after refresh %s is unavailable: %v", rel, err)
			continue
		}
		embed, err := stepagentassets.FS.ReadFile(asset)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(embed) {
			t.Errorf("after refresh %s differs from the embedded resource", rel)
		}
	}
	// the refresh never prunes: a file the snapshot carries beyond the
	// minimal set survives it (pruning is the daemon-start sync's).
	stray := filepath.Join(dir, "step-agent-notes.txt")
	if err := os.WriteFile(stray, []byte("kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.refreshStepAgentDir(); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("the per-spawn refresh pruned a file it does not own: %v", err)
	}
}

// TestExportPiSubagentProbeStepAgent (polyforge#228 AC2): the manual entry
// the live probe's scratch flow uses. With PF_PROBE_EXPORT_DIR set it runs
// the SAME refresh the daemon runs and writes the resulting step-agent copy
// there, with no operator or daemon configuration read: the caller supplies
// a scratch source (its credentials, if any, are the caller's). The copy's
// builtin resources must be the embedded bytes — a probe over anything else
// would not be evidence about the shipped worker.
func TestExportPiSubagentProbeStepAgent(t *testing.T) {
	dst := os.Getenv("PF_PROBE_EXPORT_DIR")
	if dst == "" {
		t.Skip("set PF_PROBE_EXPORT_DIR to export a refreshed step-agent copy for the live probe")
	}
	if err := refreshStepAgentMinimalSet(t.TempDir(), dst); err != nil {
		t.Fatalf("refresh into %s: %v", dst, err)
	}
	for _, rel := range []string{"agents/worker.md", "extensions/subagent/index.ts", "extensions/subagent/agents.ts"} {
		want, err := stepagentassets.FS.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("the export lacks %s: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("the exported %s is not the embedded resource", rel)
		}
	}
}
