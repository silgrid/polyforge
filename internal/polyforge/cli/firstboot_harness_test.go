package cli

// firstboot_harness_test.go — polyforge#176 (84-components-C): the
// first boot's harness-reference merge. One function, TestHarnessMerge:
// the pi packages entry and the claude skill symlinks point at
// /components/current, a pre-#174 git entry of this repository is
// replaced in place, and everything that is the user's own — the login,
// other packages, their own skill entries — survives untouched.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// hmFixture builds one components root: releases/<ver> carrying two
// skills, and current pointing at it.
func hmFixture(t *testing.T, root string) string {
	t.Helper()
	ver := filepath.Join(root, "releases", "v0.2.4-test")
	for _, s := range []string{"polyforge", "another"} {
		if err := os.MkdirAll(filepath.Join(ver, "skills", s), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ver, "skills", s, "SKILL.md"), []byte("# skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the routing skill's SessionStart hook script (not executable yet —
	// the merge must chmod +x it, the way install.sh does)
	if err := os.MkdirAll(filepath.Join(ver, "skills", "polyforge", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ver, "skills", "polyforge", "hooks", "claude-session-start.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ver, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	return ver
}

// hmSettings reads a JSON file into a map.
func hmSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

func TestHarnessMerge(t *testing.T) {
	opts := func(home, agent, root string) harnessMergeOpts {
		return harnessMergeOpts{Home: home, AgentDir: agent, ComponentsRoot: root, RepoSlug: "silgrid/polyforge"}
	}

	t.Run("the pi packages entry points at current, and the login and foreign packages survive", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		hmFixture(t, root)
		agent := filepath.Join(home, ".pi", "agent")
		if err := os.MkdirAll(agent, 0o755); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(agent, "settings.json")
		seed := `{
			"model": "keep-me",
			"auth": {"piToken": "the-users-login"},
			"packages": ["npm:foreign-one", {"source": "git:github.com/silgrid/polyforge@v0.1.0", "filters": {"foo": "bar"}}]
		}`
		if err := os.WriteFile(settings, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
		rep, err := mergeHarness(opts(home, agent, root))
		if err != nil {
			t.Fatalf("mergeHarness: %v", err)
		}
		if rep.PiPackages != "replaced" {
			t.Fatalf("a pre-#174 git entry of this repository must be replaced in place (got %q)", rep.PiPackages)
		}
		got := hmSettings(t, settings)
		if got["model"] != "keep-me" || hmMap(t, got["auth"])["piToken"] != "the-users-login" {
			t.Fatalf("the user's own settings must survive: %v", got)
		}
		pk := got["packages"].([]any)
		if len(pk) != 2 {
			t.Fatalf("packages = %v, want the foreign one plus the replaced entry", pk)
		}
		if pk[0] != "npm:foreign-one" {
			t.Fatalf("the foreign package must keep its place: %v", pk)
		}
		// the replaced entry keeps its dict shape and filters, with the
		// source moved to /components/current (install.sh's discipline)
		entry := hmMap(t, pk[1])
		if entry["source"] != filepath.Join(root, "current") || hmMap(t, entry["filters"])["foo"] != "bar" {
			t.Fatalf("the replaced entry = %v, want source %s with its filters kept", entry, filepath.Join(root, "current"))
		}
	})

	t.Run("a fresh agent dir gets a 0600 settings.json with the one entry", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		hmFixture(t, root)
		agent := filepath.Join(home, ".pi", "agent")
		rep, err := mergeHarness(opts(home, agent, root))
		if err != nil {
			t.Fatal(err)
		}
		if rep.PiPackages != "added" {
			t.Fatalf("a fresh settings.json must gain the entry (got %q)", rep.PiPackages)
		}
		settings := filepath.Join(agent, "settings.json")
		fi, err := os.Stat(settings)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("a new settings.json is 0600 (it may hold credentials): %v", fi.Mode().Perm())
		}
		if pk := hmSettings(t, settings)["packages"].([]any); len(pk) != 1 || pk[0] != filepath.Join(root, "current") {
			t.Fatalf("packages = %v, want exactly the components current entry", pk)
		}
	})

	t.Run("a second merge changes nothing", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		hmFixture(t, root)
		agent := filepath.Join(home, ".pi", "agent")
		if _, err := mergeHarness(opts(home, agent, root)); err != nil {
			t.Fatal(err)
		}
		settings := filepath.Join(agent, "settings.json")
		before, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		rep, err := mergeHarness(opts(home, agent, root))
		if err != nil {
			t.Fatal(err)
		}
		if rep.PiPackages != "already" {
			t.Fatalf("an idempotent merge must report already (got %q)", rep.PiPackages)
		}
		after, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) != string(after) {
			t.Fatalf("an idempotent merge must not rewrite the settings:\n%s\n%s", before, after)
		}
	})

	t.Run("the claude skill symlinks point at current, the user's own entries stay", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		hmFixture(t, root)
		// the claude side engages when ~/.claude exists
		if err := os.MkdirAll(filepath.Join(home, ".claude", "skills", "mine"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".claude", "skills", "mine", "SKILL.md"), []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rep, err := mergeHarness(opts(home, filepath.Join(home, ".pi", "agent"), root))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"polyforge", "another"} {
			link := filepath.Join(home, ".claude", "skills", name)
			got, err := os.Readlink(link)
			if err != nil {
				t.Fatalf("skill %s must be a symlink: %v", name, err)
			}
			// the link points THROUGH current: a components upgrade
			// switches current and every reference follows it
			want := filepath.Join(root, "current", "skills", name)
			if got != want {
				t.Fatalf("skill %s -> %s, want %s", name, got, want)
			}
		}
		if fi, err := os.Stat(filepath.Join(home, ".claude", "skills", "mine")); err != nil || !fi.IsDir() {
			t.Fatalf("the user's own skill dir must stay untouched: %v", err)
		}
		if len(rep.SkillsLinked) != 2 || len(rep.SkillsUntouched) != 1 || rep.SkillsUntouched[0] != "mine" {
			t.Fatalf("report = %+v, want 2 linked and the user's own entry named untouched", rep)
		}
		// the SessionStart hook of the routing skill is registered,
		// once
		cl := hmSettings(t, filepath.Join(home, ".claude", "settings.json"))
		hooks := hmMap(t, cl["hooks"])
		ss := hooks["SessionStart"].([]any)
		if len(ss) != 1 {
			t.Fatalf("SessionStart = %v, want exactly one registration", ss)
		}
		h := hmMap(t, ss[0])
		inner := h["hooks"].([]any)
		if len(inner) != 1 || hmMap(t, inner[0])["command"] != filepath.Join(home, ".claude", "skills", "polyforge", "hooks", "claude-session-start.sh") {
			t.Fatalf("the hook entry = %v, want the routing skill's hook command", inner)
		}
		if !rep.ClaudeHook {
			t.Fatal("the report must say the hook was registered")
		}
		// the routing skill's hook script must be executable
		fi, err := os.Stat(filepath.Join(root, "current", "skills", "polyforge", "hooks", "claude-session-start.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("the hook script must be executable: %v", fi.Mode().Perm())
		}
		// re-run: nothing changes
		if _, err := mergeHarness(opts(home, filepath.Join(home, ".pi", "agent"), root)); err != nil {
			t.Fatal(err)
		}
		cl2 := hmSettings(t, filepath.Join(home, ".claude", "settings.json"))
		hooks2 := hmMap(t, cl2["hooks"])
		if len(hooks2["SessionStart"].([]any)) != 1 {
			t.Fatalf("the hook must be registered exactly once, got %v", hooks2["SessionStart"])
		}
	})

	t.Run("a copied (pre-#174) polyforge skill dir is replaced by the symlink", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir()
		hmFixture(t, root)
		old := filepath.Join(home, ".claude", "skills", "polyforge")
		if err := os.MkdirAll(old, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, "SKILL.md"), []byte("# old copy\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mergeHarness(opts(home, filepath.Join(home, ".pi", "agent"), root)); err != nil {
			t.Fatal(err)
		}
		got, err := os.Readlink(old)
		if err != nil {
			t.Fatalf("the copied dir must be replaced by the symlink: %v", err)
		}
		want := filepath.Join(root, "current", "skills", "polyforge")
		if got != want {
			t.Fatalf("the symlink -> %s, want %s", got, want)
		}
	})

	t.Run("no components current refuses instead of dangling", func(t *testing.T) {
		home := t.TempDir()
		root := t.TempDir() // no fixture: no current
		_, err := mergeHarness(opts(home, filepath.Join(home, ".pi", "agent"), root))
		if err == nil {
			t.Fatal("without <root>/current the harness references would dangle — the merge must refuse")
		}
		if _, e := os.Stat(filepath.Join(home, ".pi", "agent", "settings.json")); !os.IsNotExist(e) {
			t.Fatalf("a refused merge must write nothing: %v", e)
		}
	})
}

// hmMap coerces a JSON value into a map with a loud failure.
func hmMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want a JSON object, got %T (%v)", v, v)
	}
	return m
}
