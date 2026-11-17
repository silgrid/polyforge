// carries: workflows/_shared/scripts/verify-scope

package test

// w111_scoped_command_test.go (polyforge#111 u2): the conf key
// `scoped_command=<glob>|<command>` — the owner-confirmed conditional
// command mapping (2026-11-17T20:55:26Z). A changed path an exclude= match
// has not removed that matches the glob adds the declared command to the
// scoped selection, once per declaration, in file order, after the
// analyzer's own commands, reported as a scoped command of source conf;
// the light tier included, --full and --micro never; a value without its
// `|`, an empty glob or an empty command is diagnosed and fails the conf
// closed.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW111ScopedCommand$' -count=1 -v

import (
	"encoding/json"
	"strings"
	"testing"
)

// w111scCommand is one reported command; the source reaches the JSON too.
type w111scCommand struct {
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Source string `json:"source"`
	Cmd    string `json:"cmd"`
}

// w111scReport is the subset of the --json report this file judges.
type w111scReport struct {
	Mode        string          `json:"mode"`
	Paths       []string        `json:"paths"`
	Commands    []w111scCommand `json:"commands"`
	Diagnostics []string        `json:"diagnostics"`
}

func w111scRun(t *testing.T, dir string, args ...string) w111scReport {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --json %v in %s: exit %d (stdout %q, stderr %q)", args, dir, code, out, errb)
	}
	var r w111scReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not one JSON object: %v\n%s", err, out)
	}
	return r
}

func (r w111scReport) scoped() []w111scCommand {
	var out []w111scCommand
	for _, c := range r.Commands {
		if c.Phase == "scoped" {
			out = append(out, c)
		}
	}
	return out
}

func (r w111scReport) diag(frags ...string) bool {
	for _, d := range r.Diagnostics {
		hit := true
		for _, f := range frags {
			hit = hit && strings.Contains(d, f)
		}
		if hit {
			return true
		}
	}
	return false
}

// w111scFiles is a Go module whose conf declares two conditional commands.
func w111scFiles(conf string) map[string]string {
	return map[string]string{
		"go.mod":                       "module fixture.test/w111sc\n\ngo 1.21\n",
		"lib/lib.go":                   "package lib\n\nfunc Value() int { return 1 }\n",
		"docs/guide.md":                "# guide\n",
		"other/thing.txt":              "x\n",
		".polyforge/verify-scope.conf": conf,
	}
}

const w111scConf = `stack=go
fast_gate=echo FAST
scoped_command=lib/*|python3 -m pytest tests/
scoped_command=docs/*|echo DOCS-CHANGED
`

// TestW111ScopedCommandSelected: a matching changed path adds the declared
// command after the analyzer's own scoped commands (light and default
// scoped alike), reported as a scoped command of source conf; a non-matching
// path adds nothing.
func TestW111ScopedCommandSelected(t *testing.T) {
	for _, light := range []bool{false, true} {
		dir, base := w25e2eFixture(t, w111scFiles(w111scConf))
		w25e2eWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
		w25e2eCommit(t, dir, "change lib")
		args := []string{"--base", base}
		if light {
			args = append([]string{"--light"}, args...)
		}
		r := w111scRun(t, dir, args...)
		if r.Mode != "scoped" {
			t.Fatalf("a lib change must stay scoped (light=%v), got %q (diagnostics %v)", light, r.Mode, r.Diagnostics)
		}
		got := r.scoped()
		if len(got) != 2 || !strings.Contains(got[0].Cmd, "./lib") || got[0].Source != "default" {
			t.Fatalf("the analyzer's package command comes first: %+v", got)
		}
		if got[1].Cmd != "python3 -m pytest tests/" || got[1].Source != "conf" {
			t.Fatalf("the matched scoped_command follows, source conf: %+v", got)
		}

		dir, base = w25e2eFixture(t, w111scFiles(w111scConf))
		w25e2eWrite(t, dir, "docs/guide.md", "# guide 2\n")
		w25e2eCommit(t, dir, "change docs")
		r = w111scRun(t, dir, args...)
		if got := r.scoped(); len(got) != 1 || got[0].Cmd != "echo DOCS-CHANGED" || got[0].Source != "conf" {
			t.Fatalf("a docs change matches only the docs declaration: %+v", got)
		}

		dir, base = w25e2eFixture(t, w111scFiles(w111scConf))
		w25e2eWrite(t, dir, "other/thing.txt", "y\n")
		w25e2eCommit(t, dir, "change other")
		r = w111scRun(t, dir, args...)
		if got := r.scoped(); len(got) != 0 {
			t.Fatalf("a path no declaration matches adds nothing: %+v", got)
		}
	}
}

// TestW111ScopedCommandExcludedAndOnce: an exclude= match removes the path
// from the mapping's reach — a change confined to the excluded path adds
// nothing (delete the engine's path_excluded check and this reds) — and one
// declaration adds its command however many of its paths changed.
func TestW111ScopedCommandExcludedAndOnce(t *testing.T) {
	conf := "stack=go\nfast_gate=echo FAST\nexclude=lib/skip.go\nscoped_command=lib/*|echo LIB-SUITE\n"
	dir, base := w25e2eFixture(t, w111scFiles(conf))
	w25e2eWrite(t, dir, "lib/skip.go", "package lib\n\nfunc Skipped() int { return 1 }\n")
	w25e2eCommit(t, dir, "change the excluded path alone")
	r := w111scRun(t, dir, "--light", "--base", base)
	got := r.scoped()
	for _, c := range got {
		if c.Source == "conf" {
			t.Fatalf("a changed path an exclude= match removed must not trigger the declaration:\n%+v", got)
		}
	}

	dir, base = w25e2eFixture(t, w111scFiles(conf))
	w25e2eWrite(t, dir, "lib/skip.go", "package lib\n\nfunc Skipped() int { return 1 }\n")
	w25e2eWrite(t, dir, "lib/two.go", "package lib\n\nfunc Two() int { return 2 }\n")
	w25e2eCommit(t, dir, "change lib twice")
	r = w111scRun(t, dir, "--light", "--base", base)
	got = r.scoped()
	count := 0
	for _, c := range got {
		if c.Cmd == "echo LIB-SUITE" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("one declaration adds its command once however many paths match: %+v", got)
	}
	if len(got) != 2 || !strings.Contains(got[0].Cmd, "./lib") {
		t.Fatalf("the excluded path still counts for the analyzer's own package selection, the mapping adds once: %+v", got)
	}
}

// TestW111ScopedCommandMalformedFailsClosed: a declaration without its |,
// with an empty glob or an empty command is diagnosed and fails the conf
// closed.
func TestW111ScopedCommandMalformedFailsClosed(t *testing.T) {
	for name, line := range map[string]string{
		"no separator":  "scoped_command=echo NO-PIPE",
		"empty glob":    "scoped_command=|echo EMPTY-GLOB",
		"empty command": "scoped_command=lib/*|",
	} {
		t.Run(name, func(t *testing.T) {
			conf := "stack=go\nfast_gate=echo FAST\n" + line + "\n"
			dir, base := w25e2eFixture(t, w111scFiles(conf))
			w25e2eWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
			w25e2eCommit(t, dir, "change lib")
			r := w111scRun(t, dir, "--light", "--base", base)
			if r.Mode != "full" {
				t.Fatalf("a malformed scoped_command must fail closed, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
			}
			if !r.diag("scoped_command", "falling back to the full tier") {
				t.Fatalf("the diagnostic must name the rejected scoped_command line: %v", r.Diagnostics)
			}
		})
	}
}

// TestW111ScopedCommandFullAndMicroNever: the mapping reaches the scoped
// tier only — a --full report carries no scoped_command, and a --micro
// report is the fast phase plus the micro checks.
func TestW111ScopedCommandFullAndMicroNever(t *testing.T) {
	files := w111scFiles(w111scConf)
	files[".polyforge/verify-scope.conf"] = "stack=go\nfast_gate=echo FAST\nmicro_path=docs/*\nmicro_check=echo MICRO\nscoped_command=docs/*|echo DOCS-CHANGED\n"
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "docs/guide.md", "# guide 2\n")
	w25e2eCommit(t, dir, "change docs")

	f := w111scRun(t, dir, "--full", "--base", base)
	if f.Mode != "full" {
		t.Fatalf("--full answers full, got %q", f.Mode)
	}
	for _, c := range f.Commands {
		if strings.Contains(c.Cmd, "DOCS-CHANGED") {
			t.Fatalf("a full-tier report never carries a scoped_command: %+v", f.Commands)
		}
	}
	m := w111scRun(t, dir, "--micro", "--base", base)
	if m.Mode != "micro" {
		t.Fatalf("--micro answers micro, got %q", m.Mode)
	}
	for _, c := range m.Commands {
		if strings.Contains(c.Cmd, "DOCS-CHANGED") {
			t.Fatalf("a micro report never carries a scoped_command: %+v", m.Commands)
		}
	}
}

// TestW111ScopedCommandNestedRoot: a nested engine root reads its own
// conf's declarations, its paths relative to itself.
func TestW111ScopedCommandNestedRoot(t *testing.T) {
	files := map[string]string{
		"go.mod":                           "module fixture.test/w111sc\n\ngo 1.21\n",
		"cmd/app/main.go":                  "package main\n\nfunc main() {}\n",
		"sub/go.mod":                       "module fixture.test/w111sc/sub\n\ngo 1.21\n",
		"sub/lib/lib.go":                   "package lib\n\nfunc Value() int { return 1 }\n",
		"sub/.polyforge/verify-scope.conf": "stack=go\nfast_gate=echo FAST\nscoped_command=lib/*|python3 -m pytest tests/\n",
	}
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "sub/lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
	w25e2eCommit(t, dir, "change sub/lib")
	r := w111scRun(t, dir+"/sub", "--light", "--base", base)
	got := r.scoped()
	if len(got) != 2 || !strings.Contains(got[0].Cmd, "./lib") || got[1].Cmd != "python3 -m pytest tests/" || got[1].Source != "conf" {
		t.Fatalf("the nested root reads its own scoped_command lines, paths relative to itself: %+v", got)
	}
}
