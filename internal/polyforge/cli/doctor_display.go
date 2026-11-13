package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// doctor_display.go: the harness checks of `pf doctor`. Human decisions
// are shown and handled in the Pi Web Polyforge panel: the
// resident chat watcher, its Claude Code statusLine progress face and the
// pi widget are retired. What is left to judge per harness: Claude Code's
// statusLine must not still run the retired `pf watch --statusline`
// (it would print a usage error in the status line), and pi must load the
// polyforge package (the routing skill and the pf-chat session extension —
// its daemon self-heal). The judgement is static: the statusLine command
// and the absolute files it names are read, never executed, and no user
// file is written. pass = fine; warn = a leftover or a missing package
// (the detail says what to do), or cannot tell; skip = the harness is not
// set up here. The retired chat state files are their own row
// (doctor_chat_leftovers.go).

const (
	docChClaudeStatusline = "claude statusline"
	docChPiPackage        = "pi package"
)

// pfStatuslineRe matches a `pf watch ... --statusline` invocation on
// one line (bare `pf`, a path ending in /pf, or quoted as in
// `bash -c 'pf watch ...'`).
var pfStatuslineRe = regexp.MustCompile(`(^|[\s/;|&('"` + "`" + `])pf\s+watch\b.*--statusline`)

// polyforgePkgRe matches a pi package source of the polyforge repo.
var polyforgePkgRe = regexp.MustCompile(`github\.com[:/]silgrid/polyforge(\.git)?(@|$)`)

// maxScriptBytes bounds a statusLine script read (a binary named on the
// command line is not a script).
const maxScriptBytes = 1 << 20

// harnessDisplayChecks answers the harness checks for this user;
// fixLayout lets the claude install layout check link its skill, and
// pruneChat lets the retired chat state row delete what it finds.
func harnessDisplayChecks(fixLayout, pruneChat bool) []doctorCheck {
	home, _ := os.UserHomeDir()
	piDir := piAgentDir(home)
	return []doctorCheck{
		claudeStatuslineCheck(home), piPackageCheck(piDir, home),
		claudeLayoutCheck(home, fixLayout), piLayoutCheck(piDir, home),
		v1LeftoversCheck(home, piDir),
		retiredChatStateCheck(DataDir(home), chatRuntimeDir(), pruneChat),
	}
}

// piAgentDir is pi's agent dir for this user: $PI_CODING_AGENT_DIR,
// else ~/.pi/agent.
func piAgentDir(home string) string {
	if d := os.Getenv("PI_CODING_AGENT_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".pi", "agent")
}

// runsPfStatusline reports whether shell text invokes the progress face
// (comment lines ignored).
func runsPfStatusline(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.HasPrefix(l, "#") && pfStatuslineRe.MatchString(l) {
			return true
		}
	}
	return false
}

// claudeStatuslineCheck judges whether Claude Code's statusLine still
// runs the retired `pf watch --statusline` — directly, or through a
// script its command names.
func claudeStatuslineCheck(home string) doctorCheck {
	c := doctorCheck{Name: docChClaudeStatusline}
	dir := filepath.Join(home, ".claude")
	if skip, ok := notSetUp(dir, "Claude Code"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	path := filepath.Join(dir, "settings.json")
	var cfg struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if exists, err := readJSONFile(path, &cfg); err != nil {
		c.Status, c.Detail = "warn", "cannot tell: "+err.Error()
		return c
	} else if !exists {
		c.Status, c.Detail = "pass", "no "+path+": no statusLine runs the retired pf watch --statusline"
		return c
	}
	cmd, _ := cfg.StatusLine["command"].(string)
	retired := func(where string) doctorCheck {
		c.Status, c.Detail = "warn", where+" still runs `pf watch --statusline`, retired with the chat watcher (decisions are shown in the Pi Web Polyforge panel) — remove that invocation from the statusLine in "+path
		return c
	}
	if runsPfStatusline(cmd) {
		return retired("the statusLine command")
	}
	for _, tok := range strings.Fields(cmd) {
		f := strings.Trim(tok, `"';&|()`)
		if strings.HasPrefix(f, "~/") {
			f = filepath.Join(home, f[2:])
		}
		f = strings.NewReplacer("${HOME}", home, "$HOME", home).Replace(f)
		if !filepath.IsAbs(f) {
			continue
		}
		st, err := os.Stat(f)
		if err != nil || !st.Mode().IsRegular() || st.Size() > maxScriptBytes {
			continue
		}
		if body, err := os.ReadFile(f); err == nil && runsPfStatusline(string(body)) {
			return retired("the statusLine script " + f)
		}
	}
	c.Status, c.Detail = "pass", "the statusLine does not run the retired pf watch --statusline"
	return c
}

// piPackageCheck judges whether pi loads the polyforge pi package (the
// routing skill and the pf-chat session extension, which starts the
// daemon when a pi session finds it down): the local release checkout
// tools/install.sh references, any local checkout carrying pf-chat, a git
// entry of the repo, or the extension file itself listed in pi's
// settings.
func piPackageCheck(agentDir, home string) doctorCheck {
	c := doctorCheck{Name: docChPiPackage}
	if skip, ok := notSetUp(agentDir, "pi"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	hint := "the polyforge pi package (routing skill + pf-chat session extension) is not installed — re-run tools/install.sh, or add the local release \"" + releaseCurrent(home) + "\" to packages in " + filepath.Join(agentDir, "settings.json")
	cfg, exists, err := readPiSettings(agentDir)
	if err != nil {
		c.Status, c.Detail = "warn", "cannot tell: "+err.Error()
		return c
	}
	if !exists {
		c.Status, c.Detail = "warn", hint
		return c
	}
	for _, p := range cfg.Packages {
		s := pkgSource(p)
		if polyforgePkgRe.MatchString(s) {
			c.Status, c.Detail = "pass", fmt.Sprintf("pi package %s carries the pf-chat extension", s)
			return c
		}
		if lp := piLocalPath(s, agentDir, home); lp != "" {
			if _, err := os.Stat(filepath.Join(lp, "skills", "polyforge", "extensions", "pf-chat.ts")); err == nil {
				c.Status, c.Detail = "pass", fmt.Sprintf("pi package %s carries the pf-chat extension", s)
				return c
			}
		}
	}
	for _, x := range cfg.Extensions {
		if e, ok := x.(string); ok && filepath.Base(e) == "pf-chat.ts" {
			c.Status, c.Detail = "pass", "pi extension "+e+" is listed"
			return c
		}
	}
	c.Status, c.Detail = "warn", hint
	return c
}
