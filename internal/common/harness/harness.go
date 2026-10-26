// Package harness names the step harnesses a workflow step can run on
// (polyforge#159) and the one model-candidate format (polyforge#175,
// thinking effort polyforge#176).
//
// A model candidate is `<harness>:<model>[:<effort>]`: the harness of the
// closed set (pi | claude), the model id that harness's --model takes,
// and optionally a thinking effort of the closed effort set. The harness
// is split off at the FIRST `:`; of the rest, only a LAST `:` segment
// that is an effort-set word is the effort — every other `:` belongs to
// the model id (`pi:ollama/llama3:8b` is the model `ollama/llama3:8b`;
// `pi:ollama/llama3:8b:high` adds effort high). The prefix is mandatory
// and is the whole truth of where a candidate runs — there is no
// inference from the model id's shape and no per-step harness setting.
// One candidate chain may therefore mix harnesses and efforts
// (`[claude:claude-opus-5-5:high, pi:example-gateway/model-a:low]`). No
// effort keeps the harness's own default (no flag is passed).
package harness

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The closed harness set. An absent harness ("") is Pi where a record
// predates the harness field (spawn-journal lines before #159).
const (
	Pi     = "pi"
	Claude = "claude"
)

// Valid reports whether name is a harness of the closed set.
func Valid(name string) bool { return name == Pi || name == Claude }

// Of normalizes an absent harness to Pi.
func Of(name string) string {
	if name == "" {
		return Pi
	}
	return name
}

// The closed thinking-effort set (polyforge#176): the union of the
// levels the two harnesses accept. Verified 2026-10-16 against the
// installed binaries' --help: pi `--thinking <level>  Set thinking
// level: off, minimal, low, medium, high, xhigh, max`; claude `--effort
// <level>  Effort level for the current session (low, medium, high,
// xhigh, max)`.
var Efforts = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// harnessEfforts is the subset of Efforts each harness accepts.
var harnessEfforts = map[string][]string{
	Pi:     Efforts,
	Claude: {"low", "medium", "high", "xhigh", "max"},
}

// ValidEffort reports whether e is a word of the closed effort set.
func ValidEffort(e string) bool {
	for _, x := range Efforts {
		if x == e {
			return true
		}
	}
	return false
}

// EffortsOf answers the effort levels harness h accepts (nil for a
// harness outside the closed set).
func EffortsOf(h string) []string { return harnessEfforts[h] }

func accepts(h, e string) bool {
	for _, x := range harnessEfforts[h] {
		if x == e {
			return true
		}
	}
	return false
}

// Candidate is one parsed model candidate: the harness it runs on, the
// model id handed to that harness, and the thinking effort ("" = none
// given: the harness's own default, no flag passed).
type Candidate struct {
	Harness string
	Model   string
	Effort  string
}

// String answers the canonical `<harness>:<model>[:<effort>]` form.
func (c Candidate) String() string {
	s := c.Harness + ":" + c.Model
	if c.Effort != "" {
		s += ":" + c.Effort
	}
	return s
}

// EffortArgs answers the argv that hands the effort to the candidate's
// harness: pi `--thinking <level>`, claude `--effort <level>`; nil when
// no effort was given (exactly the pre-#176 argv). Pi takes the flag,
// not its `--model <id>:<level>` shorthand, so --model stays exactly
// the model id (a model id may itself end in `:<word>`, which pi's own
// shorthand parse would then have to disambiguate again) and both
// harnesses get the same shape: one dedicated flag.
func (c Candidate) EffortArgs() []string {
	if c.Effort == "" {
		return nil
	}
	switch c.Harness {
	case Claude:
		return []string{"--effort", c.Effort}
	case Pi:
		return []string{"--thinking", c.Effort}
	}
	return nil
}

// PrefixHint is the shape every refusal of an unprefixed candidate names.
const PrefixHint = "pi:… or claude:…"

// ParseCandidate parses one `<harness>:<model>[:<effort>]` candidate
// (surrounding whitespace of the whole string and of each part is
// ignored). A candidate without a `:`, with a harness outside the closed
// set, with an empty model, with an effort its harness does not accept,
// or with two trailing effort words (`…:high:low` — which one would
// win is a harness detail, so it is refused) is refused; the error text
// quotes the candidate and is meant to be prefixed with its location by
// the caller. A last segment outside the effort set (`:8b`, a typo such
// as `:hgih`) is part of the model id: the closed set is the only
// way to tell an effort from a model name, so a misspelt effort
// reaches the harness as a model id and fails there, loudly.
func ParseCandidate(s string) (Candidate, error) {
	raw := strings.TrimSpace(s)
	h, m, ok := strings.Cut(raw, ":")
	if !ok {
		return Candidate{}, fmt.Errorf("%q needs a harness prefix (%s)", raw, PrefixHint)
	}
	h, m = strings.TrimSpace(h), strings.TrimSpace(m)
	if !Valid(h) {
		return Candidate{}, fmt.Errorf("%q has an unknown harness prefix %q (want %s)", raw, h, PrefixHint)
	}
	effort := ""
	if i := strings.LastIndex(m, ":"); i >= 0 {
		if e := strings.TrimSpace(m[i+1:]); ValidEffort(e) {
			effort, m = e, strings.TrimSpace(m[:i])
		}
	}
	if m == "" {
		return Candidate{}, fmt.Errorf("%q names no model after the harness prefix (want %s)", raw, PrefixHint)
	}
	if effort != "" {
		if i := strings.LastIndex(m, ":"); i >= 0 && ValidEffort(strings.TrimSpace(m[i+1:])) {
			return Candidate{}, fmt.Errorf("%q names two efforts (%q and %q); give one: <harness>:<model>[:<effort>]", raw, strings.TrimSpace(m[i+1:]), effort)
		}
		if !accepts(h, effort) {
			return Candidate{}, fmt.Errorf("%q: effort %q is not accepted by %s (it accepts %s)", raw, effort, h, strings.Join(harnessEfforts[h], "|"))
		}
	}
	return Candidate{Harness: h, Model: m, Effort: effort}, nil
}

// Normalize answers the canonical form of one candidate, or the
// ParseCandidate refusal.
func Normalize(s string) (string, error) {
	c, err := ParseCandidate(s)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}

// ---------------------------------------------------------------------------
// Claude Code session identity (polyforge#159). The daemon mints the
// session uuid of every claude dispatch itself (--session-id), so the
// native transcript's file name is known before the process starts:
// <claude config dir>/projects/<dashed cwd>/<uuid>.jsonl. The locator
// globs by the uuid instead of re-deriving Claude Code's directory
// naming rule, and accepts only the canonical uuid shape — the id may
// be read back from the agent-writable spawn journal.

// NewSessionID mints a random (v4) uuid in canonical lowercase form.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// ValidSessionID reports whether s is a canonical lowercase uuid
// (8-4-4-4-12 hex digits) — the only shape a session id is trusted in.
func ValidSessionID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// ClaudeProjectDirName is Claude Code's per-cwd project directory name:
// every character outside [A-Za-z0-9] becomes '-'.
func ClaudeProjectDirName(cwd string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, cwd)
}

// LocateClaudeSession answers the native session file of uuid for a step
// that ran in capsuleDir: <config dir>/projects/<dir of capsuleDir>/<uuid>.jsonl,
// a non-empty regular file — or "" when id is not a canonical uuid or no
// such file exists. configDir is EXPLICIT (polyforge#171): the caller
// names the claude-config dir it hard-set into the child env (the
// runtime config's ClaudeConfigDir); the locator never falls back to the
// daemon process env (the child env is the only place CLAUDE_CONFIG_DIR
// is ever set in production). Path stays direct concatenation (no mtime
// locator, ruling 7). Only the project directory of the capsule (as
// given, or with symlinks resolved) is consulted: a same-named file of
// another cwd is never copied or resumed.
func LocateClaudeSession(id, configDir, capsuleDir string) string {
	if !ValidSessionID(id) || configDir == "" || capsuleDir == "" {
		return ""
	}
	dirs := []string{ClaudeProjectDirName(capsuleDir)}
	if real, err := filepath.EvalSymlinks(capsuleDir); err == nil && real != capsuleDir {
		dirs = append(dirs, ClaudeProjectDirName(real))
	}
	for _, d := range dirs {
		p := filepath.Join(configDir, "projects", d, id+".jsonl")
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
			return p
		}
	}
	return ""
}
