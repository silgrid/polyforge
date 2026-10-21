package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
)

// doctor_layout.go: the install-layout half of the harness family plus
// the daemon supervision and prerequisites checks.
//
//   - pi / claude install layout: pi references the local release
//     checkout (<data-dir>/release/current) as a package, and
//     ~/.claude/skills/polyforge is a symlink into it — the layout
//     tools/install.sh sets up; anything else warns with the fix.
//   - v1 harness leftovers: v1 polyforge artifacts still hooked into pi
//     or Claude Code warn with backup-first cleanup commands; the doctor
//     never removes anything. Migration hygiene only: removed at v3.0
//     (decision 2026-10-16 G18) — delete docChV1Leftovers, the v1* vars
//     and their check then.
//   - daemon supervision: a live daemon under the tmux restart loop (or
//     an active systemd unit) passes, an unsupervised one warns with the
//     fix; a dead daemon is the "daemon socket" fail.
//   - prerequisites: the five tools the setup path needs (gh, curl,
//     python3, tmux, git) reported by LookPath — MISSING ones are named
//     with what to obtain; the check is warn-only (never fails, installs
//     nothing), so a host with every daemon check green is not made
//     "failing" by an absent developer tool.
//
// Like the display checks these never fail: pass, warn or skip.

const (
	docChClaudeLayout = "claude install layout"
	docChPiLayout     = "pi install layout"
	docChV1Leftovers  = "v1 harness leftovers"
	docChSupervision  = "daemon supervision"
)

// prerequisiteHints is the one place stating what a missing setup tool
// is obtained from, per OS (macOS line first — the darwin hosts are new
// territory).
var prerequisiteHints = map[string]string{
	"gh":      "macOS: brew install gh   |  Linux: apt install gh   (then: gh auth login)",
	"curl":    "macOS: xcode-select --install  |  Linux: apt install curl",
	"python3": "macOS: xcode-select --install  |  Linux: apt install python3",
	"tmux":    "macOS: brew install tmux  |  Linux: apt install tmux",
	"git":     "macOS: xcode-select --install  |  Linux: apt install git",
}

// prerequisiteOrder keeps the report's tool list stable.
var prerequisiteOrder = []string{"gh", "curl", "python3", "tmux", "git"}

// prerequisitesCheck reports the five setup tools' presence on PATH —
// warn-only: every missing one is named with what to obtain, never a
// fail, and the doctor never installs anything.
func prerequisitesCheck(lookPath func(string) (string, error)) doctorCheck {
	var missing []string
	for _, tool := range prerequisiteOrder {
		if _, err := lookPath(tool); err != nil {
			missing = append(missing, tool+" — "+prerequisiteHints[tool])
		}
	}
	if len(missing) == 0 {
		return doctorCheck{Name: docChPrereqs, Status: "pass",
			Detail: "all five setup tools on PATH: " + strings.Join(prerequisiteOrder, ", ")}
	}
	return doctorCheck{Name: docChPrereqs, Status: "warn",
		Detail: "missing (checked and reported; the doctor never installs anything): " + strings.Join(missing, "; ")}
}

// v1ClaudeSkills are the v1 Claude Code skills (archive/00-architecture.md §11.1).
var v1ClaudeSkills = []string{
	"using-polyforge", "pf-spec", "pf-plan", "pf-retro", "pf-crystallize", "pf-revise",
	"pf-project", "pf-user", "pf-sync", "pf-release", "pf-init", "pf-doctor", "pf-update",
}

// v1PkgRe matches a pi package / extension source of the v1 aihub repo.
var v1PkgRe = regexp.MustCompile(`(?i)ieops-aihub`)

// v1ExtFileRe matches a settings-listed v1 bridge extension file.
var v1ExtFileRe = regexp.MustCompile(`(^|/)polyforge/index\.(js|ts)$`)

// releaseCurrent is the stable release checkout link.
func releaseCurrent(home string) string {
	return filepath.Join(DataDir(home), "release", "current")
}

// piSettings is the slice of pi's settings.json the doctor reads.
type piSettings struct {
	Packages   []json.RawMessage `json:"packages"`
	Extensions []any             `json:"extensions"`
}

// readPiSettings loads <agentDir>/settings.json (exists=false when absent).
func readPiSettings(agentDir string) (cfg piSettings, exists bool, err error) {
	exists, err = readJSONFile(filepath.Join(agentDir, "settings.json"), &cfg)
	return cfg, exists, err
}

// readJSONFile decodes a harness settings file into v: exists=false when
// it is absent; a read error as is; a parse error naming the file.
func readJSONFile(path string, v any) (exists bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return true, fmt.Errorf("%s does not parse as a settings object: %v", path, err)
	}
	return true, nil
}

// notSetUp answers the skip detail when a harness's dir is absent.
func notSetUp(dir, harness string) (string, bool) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "no " + dir + ": " + harness + " is not set up here", true
	}
	return "", false
}

// pkgSource answers a packages entry's source (a string, or an object's
// "source").
func pkgSource(p json.RawMessage) string {
	var s string
	if json.Unmarshal(p, &s) == nil {
		return s
	}
	var o struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(p, &o)
	return o.Source
}

// piLocalPath resolves a pi package source that is a local path the way
// pi does (`~` → home, relative → from the settings file's dir); "" for
// npm/git/url sources.
func piLocalPath(src, agentDir, home string) string {
	s := strings.TrimSpace(src)
	switch {
	case s == "", strings.HasPrefix(s, "npm:"), strings.HasPrefix(s, "git:"),
		strings.Contains(s, "://"), strings.HasPrefix(s, "git@"), strings.HasPrefix(s, "github.com"):
		return ""
	case s == "~":
		return home
	case strings.HasPrefix(s, "~/"):
		return filepath.Join(home, s[2:])
	case filepath.IsAbs(s):
		return filepath.Clean(s)
	}
	return filepath.Join(agentDir, s)
}

// piLayoutCheck judges whether pi references polyforge the way
// tools/install.sh sets it up: one local package entry naming
// <data-dir>/release/current.
func piLayoutCheck(agentDir, home string) doctorCheck {
	c := doctorCheck{Name: docChPiLayout}
	if skip, ok := notSetUp(agentDir, "pi"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	rel := releaseCurrent(home)
	fix := "re-run tools/install.sh (it writes the local package entry \"" + rel + "\" in place of a git entry)"
	cfg, exists, err := readPiSettings(agentDir)
	if err != nil {
		c.Status, c.Detail = "warn", "cannot tell: "+err.Error()
		return c
	}
	if !exists {
		c.Status, c.Detail = "warn", "no polyforge pi package: "+filepath.Join(agentDir, "settings.json")+" does not exist — "+fix
		return c
	}
	var git, other []string
	current := false
	for _, p := range cfg.Packages {
		src := pkgSource(p)
		if polyforgePkgRe.MatchString(src) {
			git = append(git, src)
			continue
		}
		lp := piLocalPath(src, agentDir, home)
		if lp == "" {
			continue
		}
		if lp == filepath.Clean(rel) {
			current = true
		} else if _, serr := os.Stat(filepath.Join(lp, "skills", "polyforge", "SKILL.md")); serr == nil {
			other = append(other, src)
		}
	}
	switch {
	case len(git) > 0:
		c.Status, c.Detail = "warn", "pi loads polyforge from a git clone ("+strings.Join(git, ", ")+"), not the local release — "+fix
	case len(other) > 0:
		c.Status, c.Detail = "warn", "pi loads polyforge from "+strings.Join(other, ", ")+", not the release tools/install.sh manages ("+rel+") — "+fix
	case !current:
		c.Status, c.Detail = "warn", "no polyforge pi package in "+filepath.Join(agentDir, "settings.json")+" — "+fix
	default:
		if _, serr := os.Stat(filepath.Join(rel, "package.json")); serr != nil {
			c.Status, c.Detail = "warn", "the pi package entry names "+rel+", but no release is checked out there — re-run tools/install.sh"
			return c
		}
		c.Status, c.Detail = "pass", "pi package "+rel+" (the local release checkout; a release switch takes effect at the next pi session)"
	}
	return c
}

// claudeLayoutCheck judges whether Claude Code's polyforge skill is the
// symlink tools/install.sh creates.
func claudeLayoutCheck(home string) doctorCheck {
	c := doctorCheck{Name: docChClaudeLayout}
	dir := filepath.Join(home, ".claude")
	if skip, ok := notSetUp(dir, "Claude Code"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	want := filepath.Join(releaseCurrent(home), "skills", "polyforge")
	skill := filepath.Join(dir, "skills", "polyforge")
	fix := "re-run tools/install.sh (it links " + skill + " -> " + want + ")"
	st, err := os.Lstat(skill)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Status, c.Detail = "warn", "the polyforge skill is not installed for Claude Code — "+fix
	case err != nil:
		c.Status, c.Detail = "warn", "cannot tell: "+err.Error()
	case st.Mode()&os.ModeSymlink != 0:
		target, _ := os.Readlink(skill)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(skill), target)
		}
		if filepath.Clean(target) != filepath.Clean(want) {
			c.Status, c.Detail = "warn", skill+" links to "+target+", not the release tools/install.sh manages — "+fix
		} else if _, serr := os.Stat(filepath.Join(skill, "SKILL.md")); serr != nil {
			c.Status, c.Detail = "warn", skill+" links to "+want+", but no release is checked out there — re-run tools/install.sh"
		} else {
			c.Status, c.Detail = "pass", skill+" -> "+want
		}
	default:
		c.Status, c.Detail = "warn", skill+" is a copied directory (the layout before polyforge#174), not a link to the release — "+fix
	}
	return c
}

// v1LeftoversCheck finds v1 polyforge artifacts still installed in a
// harness and prints backup-first cleanup commands.
func v1LeftoversCheck(home, agentDir string) doctorCheck {
	c := doctorCheck{Name: docChV1Leftovers}
	var found, cmds, manual []string
	backup := `B="$HOME/polyforge-v1-backup-$(date +%Y%m%d%H%M%S)"; mkdir -p "$B"`
	cmds = append(cmds, backup)

	// pi: settings packages / extensions, the auto-loaded extension dir
	if cfg, exists, err := readPiSettings(agentDir); exists && err == nil {
		settings := filepath.Join(agentDir, "settings.json")
		var piCmds []string
		for _, p := range cfg.Packages {
			if src := pkgSource(p); v1PkgRe.MatchString(src) {
				found = append(found, "pi package "+src)
				piCmds = append(piCmds, "pi remove "+shellQuote(src))
			}
		}
		for _, x := range cfg.Extensions {
			if e, ok := x.(string); ok && (v1PkgRe.MatchString(e) || v1ExtFileRe.MatchString(e)) {
				found = append(found, "pi settings extension "+e)
				manual = append(manual, "delete the extensions entry "+e+" from "+settings)
			}
		}
		if len(piCmds) > 0 || len(manual) > 0 {
			cmds = append(cmds, "cp -a "+shellQuote(settings)+` "$B/pi-settings.json"`)
			cmds = append(cmds, piCmds...)
		}
	}
	for _, name := range []string{"polyforge", "polyforge.ts", "polyforge.js"} {
		p := filepath.Join(agentDir, "extensions", name)
		if _, err := os.Lstat(p); err == nil {
			found = append(found, "pi auto-loaded extension "+p)
			cmds = append(cmds, "mv "+shellQuote(p)+` "$B/pi-extension-`+name+`"`)
		}
	}

	// Claude Code: v1 skills and plugins
	claude := filepath.Join(home, ".claude")
	for _, name := range v1ClaudeSkills {
		p := filepath.Join(claude, "skills", name)
		if _, err := os.Lstat(p); err == nil {
			found = append(found, "claude skill "+p)
			cmds = append(cmds, "mv "+shellQuote(p)+` "$B/claude-skill-`+name+`"`)
		}
	}
	plugins := v1ClaudePlugins(claude)
	if len(plugins) > 0 {
		cmds = append(cmds,
			"{ cp -a "+shellQuote(filepath.Join(claude, "plugins", "installed_plugins.json"))+` "$B/" 2>/dev/null || true; }`,
			"{ cp -a "+shellQuote(filepath.Join(claude, "settings.json"))+` "$B/claude-settings.json" 2>/dev/null || true; }`)
		for _, p := range plugins {
			found = append(found, "claude plugin "+p)
			cmds = append(cmds, "claude plugin uninstall "+shellQuote(p))
		}
	}
	if len(found) == 0 {
		c.Status, c.Detail = "pass", "no v1 polyforge artifacts in the pi or Claude Code setup"
		return c
	}
	c.Status = "warn"
	c.Detail = "v1 polyforge artifacts are still installed (" + strings.Join(found, "; ") +
		") — they hook the same session events as the v2 adapters; remove them (backup first, then restart the harness sessions): " +
		strings.Join(cmds, " && ")
	if len(manual) > 0 {
		c.Detail += " — then, by hand (the settings file is backed up in $B): " + strings.Join(manual, "; ")
	}
	return c
}

// v1ClaudePlugins answers the installed Claude Code plugins that belong
// to v1 polyforge (named polyforge, or from the ieops-aihub marketplace).
func v1ClaudePlugins(claudeDir string) []string {
	seen := map[string]bool{}
	match := func(key string) {
		name, market, _ := strings.Cut(key, "@")
		if name == "polyforge" || v1PkgRe.MatchString(market) {
			seen[key] = true
		}
	}
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	var settings struct {
		EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
	}
	if _, err := readJSONFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"), &installed); err != nil {
		installed.Plugins = nil
	}
	if _, err := readJSONFile(filepath.Join(claudeDir, "settings.json"), &settings); err != nil {
		settings.EnabledPlugins = nil
	}
	for _, m := range []map[string]json.RawMessage{installed.Plugins, settings.EnabledPlugins} {
		for k := range m {
			match(k)
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// shellQuote single-quotes s for a copy-paste command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ensureFixCommand restarts an unsupervised live daemon under the loop:
// a graceful stop (running jobs are checkpointed), wait until the socket
// is quiet AND the daemon released its data-dir lock (the process is
// really gone — the same test tools/install.sh's daemon_running uses),
// then ensure.
func ensureFixCommand(home string) string {
	lock := shellQuote(filepath.Join(DataDir(home), "daemon.lock"))
	return "pf ops shutdown && while pf ops status >/dev/null 2>&1 || ! flock -n " + lock + " true; do sleep 2; done; pf daemon ensure"
}

// supervisionCheck judges whether a live daemon is supervised.
func supervisionCheck(daemonUp bool) doctorCheck {
	c := doctorCheck{Name: docChSupervision}
	home, _ := os.UserHomeDir()
	fix := ensureFixCommand(home)
	if !daemonUp {
		c.Status, c.Detail = "skip", "not run: the daemon does not answer — start it: pf daemon ensure"
		return c
	}
	loop, err := supervisor.LoopRunning()
	switch {
	case loop:
		c.Status, c.Detail = "pass", "the daemon runs under the restart loop in tmux session "+supervisor.SessionName+" (a crash is restarted within ~5s; after a reboot the next harness session runs pf daemon ensure)"
	case supervisor.SystemdActive():
		c.Status, c.Detail = "pass", "systemd unit "+supervisor.SystemdUnit+" is active"
	case errors.Is(err, supervisor.ErrNoTmux):
		c.Status, c.Detail = "warn", "the daemon runs without supervision and tmux is not installed: a crash is not restarted — install tmux, then: "+fix
	default:
		c.Status, c.Detail = "warn", "the daemon runs without supervision (no tmux session "+supervisor.SessionName+", no active "+supervisor.SystemdUnit+"): a crash is not restarted — restart it under the loop (graceful; running jobs are checkpointed first): "+fix
	}
	return c
}
