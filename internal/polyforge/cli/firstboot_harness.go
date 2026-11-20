package cli

// firstboot_harness.go — polyforge#176 (84-components-C): the
// workstation first boot's harness-reference merge, the ticket's
// settled Q5 shape: a MINIMAL merge written in Go, independent of the
// host installer (tools/install.sh) — the host installer keeps its own
// copy for host installs, and the boundary between the two is
// documented in docs/workstation.md: both merge the same two facts
// (the pi packages entry and the claude skill symlinks) toward their
// own release root, the installer toward <data-dir>/release/current,
// this one toward /components/current — and neither is called by the
// other.
//
// The merge is minimal and preserving: a user's login (the pi agent
// dir's auth), their other packages and their own skill entries are
// never touched; only this repository's own entry is replaced. Every
// reference points THROUGH the components volume's current symlink, so
// an upgrade that switches current re-points them all at once.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// harnessMergeOpts is the merge's input.
type harnessMergeOpts struct {
	// Home is $HOME (the claude side's root).
	Home string
	// AgentDir is the pi agent dir (PI_CODING_AGENT_DIR; ~/.pi/agent is
	// the default the whole stack agrees on) — settings.json lives in
	// it and a user's login (auth.json) stays untouched beside it.
	AgentDir string
	// ComponentsRoot is the components volume's root (/components):
	// the release checkout the references point at is <root>/current.
	ComponentsRoot string
	// RepoSlug is the repository's owner/name a pre-#174 git package
	// entry of this repository is recognized by (the same identity
	// tools/install.sh's $REPO carries).
	RepoSlug string
}

// harnessMergeReport is what one merge did (printed by the caller; no
// credentials ever appear here).
type harnessMergeReport struct {
	// PiPackages is the pi settings' packages outcome: added, replaced
	// or already.
	PiPackages string
	// SkillsLinked lists the claude skill names newly linked.
	SkillsLinked []string
	// SkillsKept lists the claude skills already pointing at current.
	SkillsKept []string
	// SkillsUntouched lists the entries that are the user's own and
	// stay as they were.
	SkillsUntouched []string
	// ClaudeHook says the claude SessionStart hook was registered.
	ClaudeHook bool
}

// mergeHarness merges the harness references: the pi packages entry and
// the claude skill symlinks point at <ComponentsRoot>/current, a
// pre-existing entry of this repository is replaced in place, and
// everything else in the settings (a login, other packages, the user's
// own skill entries) is preserved.
func mergeHarness(o harnessMergeOpts) (*harnessMergeReport, error) {
	current := filepath.Join(o.ComponentsRoot, "current")
	if _, err := os.Lstat(current); err != nil {
		return nil, fmt.Errorf("harness merge: the components root %s carries no current — the harness references would dangle; the first boot installs the seed before this stage", o.ComponentsRoot)
	}
	rep := &harnessMergeReport{}
	action, err := mergePiPackages(filepath.Join(o.AgentDir, "settings.json"), o.RepoSlug, current)
	if err != nil {
		return nil, err
	}
	rep.PiPackages = action

	// the claude side engages only where claude lives (the installer's
	// own condition): ~/.claude exists or the binary is on PATH.
	claudeDir := filepath.Join(o.Home, ".claude")
	if _, err := os.Stat(claudeDir); err == nil || func() bool { _, err := exec.LookPath("claude"); return err == nil }() {
		skillsDir := filepath.Join(claudeDir, "skills")
		linked, kept, untouched, err := mergeClaudeSkills(skillsDir, filepath.Join(current, "skills"))
		if err != nil {
			return nil, err
		}
		rep.SkillsLinked, rep.SkillsKept, rep.SkillsUntouched = linked, kept, untouched
		hook := filepath.Join(skillsDir, "polyforge", "hooks", "claude-session-start.sh")
		if err := os.Chmod(hook, 0o755); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("harness merge: the routing skill's hook %s: %w", hook, err)
		}
		if err := registerClaudeHook(filepath.Join(claudeDir, "settings.json"), hook); err != nil {
			return nil, err
		}
		rep.ClaudeHook = true
	}
	return rep, nil
}

// piSrc answers an entry's source: a bare string entry is its own
// source, an object entry's "source" field.
func piSrc(e any) string {
	switch v := e.(type) {
	case string:
		return v
	case map[string]any:
		s, _ := v["source"].(string)
		return s
	}
	return ""
}

// piLocal resolves a local package source (the installer's own rules:
// npm:, git:, git@ and github.com schemes and URLs are not local; ~
// expands against home; a relative path joins the settings file's
// directory). ok=false for every non-local source.
func piLocal(s, settingsDir, home string) (string, bool) {
	if s == "" || strings.HasPrefix(s, "npm:") || strings.HasPrefix(s, "git:") ||
		strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "github.com") || strings.Contains(s, "://") {
		return "", false
	}
	if s == "~" || strings.HasPrefix(s, "~/") {
		s = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(s, "~"), "/"))
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(settingsDir, s)
	}
	return filepath.Clean(s), true
}

// mergePiPackages points the pi settings' packages entry at target,
// replacing this repository's own entry (a pre-#174 git entry, or a
// local entry already pointing at the target root) in place and
// keeping every other entry. It answers added, replaced or already.
func mergePiPackages(path, repoSlug, target string) (string, error) {
	cfg, existed, err := loadJSONSettings(path)
	if err != nil {
		return "", err
	}
	raw, _ := cfg["packages"]
	pk, ok := raw.([]any)
	if raw != nil && !ok {
		return "", fmt.Errorf("harness merge: %s: unexpected packages shape (%T)", path, raw)
	}
	gitRe := regexp.MustCompile(`github\.com[:/]` + regexp.QuoteMeta(repoSlug) + `(\.git)?(@|$)`)
	settingsDir, home := filepath.Dir(path), homeOf()
	ours := []int{}
	for i, e := range pk {
		s := piSrc(e)
		if gitRe.MatchString(s) {
			ours = append(ours, i)
			continue
		}
		if lp, ok := piLocal(s, settingsDir, home); ok && lp == filepath.Clean(target) {
			ours = append(ours, i)
		}
	}
	if len(ours) == 1 && piSrc(pk[ours[0]]) == filepath.Clean(target) {
		return "already", nil
	}
	// the replacement entry keeps an object entry's other fields (its
	// filters), the way the installer's merge does
	entry := any(target)
	if len(ours) > 0 {
		if m, ok := pk[ours[0]].(map[string]any); ok {
			copied := map[string]any{}
			for k, v := range m {
				copied[k] = v
			}
			copied["source"] = target
			entry = copied
		}
	}
	next := []any{}
	inserted := false
	for i, e := range pk {
		if containsInt(ours, i) {
			if !inserted {
				next = append(next, entry)
				inserted = true
			}
			continue
		}
		next = append(next, e)
	}
	if !inserted {
		next = append(next, entry)
	}
	cfg["packages"] = next
	if err := saveJSONSettings(path, cfg, existed); err != nil {
		return "", err
	}
	if !existed {
		return "added", nil
	}
	return "replaced", nil
}

// containsInt answers whether n is in the list.
func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// mergeClaudeSkills links every skill of the release into the claude
// skills dir. The routing skill (polyforge) is always made the current
// link (a copied pre-#174 directory is removed first, the installer's
// own transition); every other name respects the user's own entry —
// an entry that is not this release's link stays exactly as it was.
func mergeClaudeSkills(claudeDir, currentSkills string) (linked, kept, untouched []string, err error) {
	entries, err := os.ReadDir(currentSkills)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("harness merge: the release skills: %w", err)
	}
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return nil, nil, nil, err
	}
	releaseSkills := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, err := os.Stat(filepath.Join(currentSkills, name, "SKILL.md")); err != nil {
			continue
		}
		releaseSkills[name] = true
		src := filepath.Join(currentSkills, name)
		dst := filepath.Join(claudeDir, name)
		if name == "polyforge" {
			// the routing skill is ours by name: a copied directory
			// (the pre-#174 shape) or a stale link is replaced
			if fi, err := os.Lstat(dst); err == nil && fi.Mode()&os.ModeSymlink == 0 {
				if err := os.RemoveAll(dst); err != nil {
					return nil, nil, nil, fmt.Errorf("harness merge: the old copied %s dir: %w", dst, err)
				}
			} else if err == nil {
				if err := os.Remove(dst); err != nil {
					return nil, nil, nil, err
				}
			}
			if err := os.Symlink(src, dst); err != nil {
				return nil, nil, nil, err
			}
			linked = append(linked, name)
			continue
		}
		if fi, err := os.Lstat(dst); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				if got, err := os.Readlink(dst); err == nil && got == src {
					kept = append(kept, name)
					continue
				}
			}
			untouched = append(untouched, name)
			continue
		}
		if err := os.Symlink(src, dst); err != nil {
			return nil, nil, nil, err
		}
		linked = append(linked, name)
	}
	// the entries that are the user's own (not one of this release's
	// skills) are named untouched: the merge never touches them, and the
	// first boot's output says so
	if own, err := os.ReadDir(claudeDir); err == nil {
		for _, e := range own {
			name := e.Name()
			if _, isRelease := releaseSkills[name]; isRelease {
				continue
			}
			untouched = append(untouched, name)
		}
	}
	return linked, kept, untouched, nil
}

// registerClaudeHook registers the routing skill's SessionStart hook in
// the claude settings, exactly once (the installer's own shape).
func registerClaudeHook(path, hook string) error {
	cfg, existed, err := loadJSONSettings(path)
	if err != nil {
		return err
	}
	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		cfg["hooks"] = hooks
	}
	ss, _ := hooks["SessionStart"].([]any)
	for _, e := range ss {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if ok && hm["command"] == hook {
				return nil // already registered
			}
		}
	}
	hooks["SessionStart"] = append(ss, map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": hook}},
	})
	return saveJSONSettings(path, cfg, existed)
}

// loadJSONSettings reads a settings JSON file ({} when absent). A
// malformed file is a loud error, never silently replaced.
func loadJSONSettings(path string) (map[string]any, bool, error) {
	cfg := map[string]any{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, false, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return cfg, true, nil
}

// saveJSONSettings lands cfg atomically, the installer's own
// discipline: a changed existing file is backed up beside it, the
// fresh bytes go through an exclusive 0600 temp file, an existing
// file's mode is kept, and the rename is the commit.
func saveJSONSettings(path string, cfg map[string]any, existed bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if existed {
		backup := path + ".bak-polyforge-" + time.Now().Format("20060102150405")
		if err := os.Rename(path, backup); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if existed {
		if fi, err := os.Stat(path); err == nil {
			_ = os.Chmod(tmp, fi.Mode().Perm())
		}
	}
	return os.Rename(tmp, path)
}

// homeOf answers $HOME ("" never reaches the local-path rules above —
// an unset HOME leaves ~ unresolvable and the entry non-local).
func homeOf() string {
	return os.Getenv("HOME")
}
