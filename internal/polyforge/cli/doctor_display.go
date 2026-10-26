package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// doctor_display.go: the harness progress-display checks of `pf doctor`.
// The watcher produces progress whether or not anyone sees it; these
// checks tell whether the user's harness is wired to show it — Claude
// Code through its statusLine, pi through the pf-chat extension — and,
// when it is not, print a copyable snippet. The judgement is static: the
// statusLine command and the absolute files it names are read, never
// executed (a user's command may have side effects or hang), and no user
// file is written. pass = will show; warn = will not show, or cannot
// tell (the detail says which); skip = the harness is not set up here.

const (
	docChClaudeDisplay = "claude progress display"
	docChPiDisplay     = "pi progress display"
)

// statuslineCommand is the progress face a harness status line runs.
const statuslineCommand = "pf watch --statusline --harness-session -"

// pfStatuslineRe matches a `pf watch ... --statusline` invocation on
// one line (bare `pf`, a path ending in /pf, or quoted as in
// `bash -c 'pf watch ...'`).
var pfStatuslineRe = regexp.MustCompile(`(^|[\s/;|&('"` + "`" + `])pf\s+watch\b.*--statusline`)

// scriptRe matches a token naming a script file by its extension.
var scriptRe = regexp.MustCompile(`\.(sh|bash|zsh|py|js|ts|rb|pl)$`)

// polyforgePkgRe matches a pi package source of the polyforge repo.
var polyforgePkgRe = regexp.MustCompile(`github\.com[:/]silgrid/polyforge(\.git)?(@|$)`)

// maxScriptBytes bounds a statusLine script read (a binary named on the
// command line is not a script).
const maxScriptBytes = 1 << 20

// harnessDisplayChecks answers both harness checks for this user.
func harnessDisplayChecks() []doctorCheck {
	home, _ := os.UserHomeDir()
	piDir := os.Getenv("PI_CODING_AGENT_DIR")
	if piDir == "" {
		piDir = filepath.Join(home, ".pi", "agent")
	}
	return []doctorCheck{
		claudeDisplayCheck(home), piDisplayCheck(piDir, home),
		claudeLayoutCheck(home), piLayoutCheck(piDir, home),
		v1LeftoversCheck(home, piDir),
	}
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

// statusLineJSON renders a copyable `"statusLine": {...}` fragment.
func statusLineJSON(sl map[string]any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(sl)
	return `"statusLine": ` + strings.TrimSpace(b.String())
}

// claudeDisplayCheck judges whether Claude Code's statusLine shows the
// progress rows: directly, or through a script its command names.
func claudeDisplayCheck(home string) doctorCheck {
	c := doctorCheck{Name: docChClaudeDisplay}
	dir := filepath.Join(home, ".claude")
	if skip, ok := notSetUp(dir, "Claude Code"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	path := filepath.Join(dir, "settings.json")
	full := statusLineJSON(map[string]any{"type": "command", "command": statuslineCommand})
	var cfg struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if exists, err := readJSONFile(path, &cfg); err != nil {
		c.Status, c.Detail = "warn", "cannot tell: "+err.Error()
		return c
	} else if !exists {
		c.Status, c.Detail = "warn", "will not show: "+path+" does not exist — create it with {"+full+"}"
		return c
	}
	cmd, _ := cfg.StatusLine["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		c.Status, c.Detail = "warn", "will not show: no statusLine command in "+path+" — add "+full
		return c
	}
	if runsPfStatusline(cmd) {
		c.Status, c.Detail = "pass", "the statusLine command runs pf watch --statusline"
		return c
	}
	// A file the command names that cannot be read (relative, missing,
	// split by quoting) or a read script naming another script leaves
	// the verdict open: "cannot tell", not a definite "will not show".
	var unresolved []string
	for _, tok := range strings.Fields(cmd) {
		f := strings.Trim(tok, `"';&|()`)
		if strings.HasPrefix(f, "~/") {
			f = filepath.Join(home, f[2:])
		}
		f = strings.NewReplacer("${HOME}", home, "$HOME", home).Replace(f)
		if !filepath.IsAbs(f) {
			if scriptRe.MatchString(f) {
				unresolved = append(unresolved, f)
			}
			continue
		}
		st, err := os.Stat(f)
		if err != nil {
			unresolved = append(unresolved, f)
			continue
		}
		if !st.Mode().IsRegular() || st.Size() > maxScriptBytes {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			unresolved = append(unresolved, f)
			continue
		}
		if runsPfStatusline(string(body)) {
			c.Status, c.Detail = "pass", "the statusLine script "+f+" runs pf watch --statusline"
			return c
		}
		for _, line := range strings.Split(string(body), "\n") {
			if l := strings.TrimSpace(line); strings.HasPrefix(l, "#") {
				continue
			}
			for _, w := range strings.Fields(line) {
				if w = strings.Trim(w, `"';&|()`); scriptRe.MatchString(w) {
					unresolved = append(unresolved, f+" → "+w)
				}
			}
		}
	}
	if len(unresolved) > 0 {
		c.Status, c.Detail = "warn", "cannot tell: the statusLine command in "+path+
			" does not run pf watch --statusline itself and names script(s) not checked ("+strings.Join(unresolved, ", ")+
			") — progress shows only if one of them runs "+statuslineCommand
		return c
	}
	// Append, never replace: the wrapper feeds the session JSON to the
	// user's own command first (its non-empty output kept, ended by one
	// newline), then to the progress face, whose rows follow below. The
	// group closes on a new line so a trailing `&` or `# comment` in the
	// user's command stays intact.
	sl := map[string]any{}
	for k, v := range cfg.StatusLine {
		sl[k] = v
	}
	sl["command"] = `input=$(cat); out=$(printf '%s' "$input" | { ` + cmd + "\n" +
		`}); [ -z "$out" ] || printf '%s\n' "$out"; printf '%s' "$input" | ` + statuslineCommand + ` 2>/dev/null`
	c.Status, c.Detail = "warn", "will not show: the statusLine command in "+path+
		" does not run pf watch --statusline — to keep its output and append the progress rows, set "+statusLineJSON(sl)
	return c
}

// piDisplayCheck judges whether pi loads the pf-chat extension: the
// polyforge pi package (which carries it: the local release checkout
// tools/install.sh references, any local checkout carrying pf-chat, or a
// git entry of the repo) or the extension file itself is listed in pi's
// settings.
func piDisplayCheck(agentDir, home string) doctorCheck {
	c := doctorCheck{Name: docChPiDisplay}
	if skip, ok := notSetUp(agentDir, "pi"); ok {
		c.Status, c.Detail = "skip", skip
		return c
	}
	hint := "will not show: the polyforge pi package (pf-chat extension) is not installed — re-run tools/install.sh, or add the local release \"" + releaseCurrent(home) + "\" to packages in " + filepath.Join(agentDir, "settings.json")
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
