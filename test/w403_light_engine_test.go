// carries: workflows/_shared/scripts/verify-scope, .polyforge/verify-scope.conf

package test

// w403_light_engine_test.go: the scope engine's two additions
// for the develop lane's light verification.
//
//   - `rerun=<name>|<template>` declares a conf command's unit re-run
//     template: --json reports it on that command (`rerun`, null when none),
//     so a failing unit of an aggregate command (one suite of `npm run
//     test:e2e:all`) is re-run alone. A template without `{unit}`, one naming
//     no declared command and a second one for the same name are diagnosed
//     and contribute nothing.
//   - `--light` is the light tier's report: the conf's forced_full= globs and
//     the module_root= spanning rule force nothing, a changed path no carries:
//     declaration covers is diagnosed instead of forcing full, and at a nested
//     engine root a changed path outside the root is left to the enclosing
//     root's run. The analyzer's own fallbacks (a stack config file such as
//     go.mod, an unbuildable package graph) still answer full.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW403Engine' -count=1 -v

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// w403Command is one reported command with its re-run template.
type w403Command struct {
	Name   string  `json:"name"`
	Phase  string  `json:"phase"`
	Cmd    string  `json:"cmd"`
	Rerun  *string `json:"rerun"`
	Source string  `json:"source"`
}

// w403Report is the subset of the --json report these scenarios judge.
type w403Report struct {
	Mode        string        `json:"mode"`
	Light       *bool         `json:"light"`
	Paths       []string      `json:"paths"`
	Commands    []w403Command `json:"commands"`
	Diagnostics []string      `json:"diagnostics"`
}

func (r w403Report) phase(p string) []string {
	var out []string
	for _, c := range r.Commands {
		if c.Phase == p {
			out = append(out, c.Cmd)
		}
	}
	return out
}

func (r w403Report) command(name string) *w403Command {
	for i := range r.Commands {
		if r.Commands[i].Name == name {
			return &r.Commands[i]
		}
	}
	return nil
}

func (r w403Report) diag(frags ...string) bool {
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

func w403Run(t *testing.T, dir string, args ...string) w403Report {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --json %v in %s: exit %d (stdout %q, stderr %q)", args, dir, code, out, errb)
	}
	var r w403Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not one JSON object: %v\n%s", err, out)
	}
	return r
}

// w403GoFiles is a Go module with two spanning roots and a forced-full glob.
func w403GoFiles(conf string) map[string]string {
	return map[string]string{
		"go.mod":                       "module fixture.test/w403\n\ngo 1.21\n",
		"a/a.go":                       "package a\n\nfunc A() int { return 1 }\n",
		"a/a_test.go":                  "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { _ = A() }\n",
		"b/b.go":                       "package b\n\nfunc B() int { return 2 }\n",
		"b/b_test.go":                  "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) { _ = B() }\n",
		".polyforge/verify-scope.conf": conf,
	}
}

const w403GoConf = `stack=go
fast_gate=echo FAST
check=e2e-all|full|cd sub && npm run test:e2e:all
full_command=echo FULL-TWO
rerun=e2e-all|cd sub && npm run test:e2e:all -- {unit}
rerun=e2e-all|echo second {unit}
rerun=full-1|no placeholder here
rerun=nosuch|echo {unit}
rerun=malformed
forced_full=a/*
module_root=a
module_root=b
`

// TestW403EngineRerunTemplateReported: the declared template rides its
// command; every other command reports null, and each malformed declaration
// is diagnosed and dropped.
func TestW403EngineRerunTemplateReported(t *testing.T) {
	dir, base := w25e2eFixture(t, w403GoFiles(w403GoConf))
	r := w403Run(t, dir, "--full", "--base", base)
	c := r.command("e2e-all")
	if c == nil || c.Rerun == nil || *c.Rerun != "cd sub && npm run test:e2e:all -- {unit}" {
		t.Fatalf("e2e-all must carry its declared rerun template (the first one), got %+v", c)
	}
	for _, other := range []string{"fast-1", "full-1"} {
		if oc := r.command(other); oc == nil || oc.Rerun != nil {
			t.Fatalf("%s must report rerun null (no valid declaration), got %+v", other, oc)
		}
	}
	for _, want := range [][]string{
		{"second rerun", "e2e-all"},
		{"rerun", "{unit}", "full-1"},
		{"rerun", "nosuch"},
		{"rerun", "malformed"},
	} {
		if !r.diag(want...) {
			t.Fatalf("a diagnostic naming %v is missing: %v", want, r.Diagnostics)
		}
	}
}

// TestW403EngineRerunRefusesAQuotedUnit (review finding 5): the unit name
// is shell-quoted where `{unit}` stands, so a template placing it inside
// quotes would pass the quotes themselves (or rely on two quoting layers
// lining up): such a template is diagnosed and dropped. Quotes elsewhere in
// the template, and an escaped quote before the placeholder, are fine.
func TestW403EngineRerunRefusesAQuotedUnit(t *testing.T) {
	conf := `stack=go
check=dq|full|echo DQ
check=sq|full|echo SQ
check=sh|full|echo SH
check=ok|full|echo OK
check=esc|full|echo ESC
rerun=dq|npm run e2e -- "{unit}"
rerun=sq|npm run e2e -- '{unit}'
rerun=sh|sh -c "npm run e2e -- {unit}"
rerun=ok|E2E_REPORTER="dot" npm run e2e -- {unit} --grep 'a b'
rerun=esc|echo \"{unit}\"
`
	dir, base := w25e2eFixture(t, w403GoFiles(conf))
	r := w403Run(t, dir, "--full", "--base", base)
	for _, name := range []string{"dq", "sq", "sh"} {
		if c := r.command(name); c == nil || c.Rerun != nil {
			t.Fatalf("%s: a template with {unit} inside quotes must be dropped, got %+v", name, c)
		}
		if !r.diag("rerun", name, "inside quotes") {
			t.Fatalf("%s: a diagnostic naming the quoted {unit} is missing: %v", name, r.Diagnostics)
		}
	}
	for name, want := range map[string]string{
		"ok":  `E2E_REPORTER="dot" npm run e2e -- {unit} --grep 'a b'`,
		"esc": `echo \"{unit}\"`,
	} {
		if c := r.command(name); c == nil || c.Rerun == nil || *c.Rerun != want {
			t.Fatalf("%s: an unquoted {unit} keeps its template %q, got %+v (diagnostics %v)", name, want, c, r.Diagnostics)
		}
	}
}

// TestW403EngineShippedConfBindsTheE2ETemplate: this repository's conf binds
// its template to the pi-web e2e command, so a failing suite of that
// aggregate command is re-runnable alone (the anonymous full-<i> numbering
// cannot drift onto another command unnoticed).
func TestW403EngineShippedConfBindsTheE2ETemplate(t *testing.T) {
	files := w366Files(t)
	dir, base := w25e2eFixture(t, files)
	r := w403Run(t, dir, "--full", "--base", base)
	var bound []w403Command
	for _, c := range r.Commands {
		if c.Rerun != nil {
			bound = append(bound, c)
		}
	}
	const e2e = "cd pi-web && E2E_SERVER_MODE=start npm run test:e2e:all"
	if len(bound) != 1 || bound[0].Cmd != e2e || *bound[0].Rerun != e2e+" -- {unit}" {
		t.Fatalf("the shipped conf must bind exactly one rerun template, %q -- {unit}, to %q; got %+v (diagnostics %v)", e2e, e2e, bound, r.Diagnostics)
	}
}

// TestW403EngineLightIgnoresForcedFullAndSpanningRoots: a diff the default
// tier sends to full (a forced_full= match spanning two module roots) stays
// scoped under --light, with the affected packages selected.
func TestW403EngineLightIgnoresForcedFullAndSpanningRoots(t *testing.T) {
	dir, base := w25e2eFixture(t, w403GoFiles(w403GoConf))
	w25e2eWrite(t, dir, "a/a.go", "package a\n\nfunc A() int { return 3 }\n")
	w25e2eWrite(t, dir, "b/b.go", "package b\n\nfunc B() int { return 4 }\n")
	w25e2eCommit(t, dir, "w403 change")
	if d := w403Run(t, dir, "--base", base); d.Mode != "full" || d.Light != nil {
		t.Fatalf("without --light the forced-full path must answer full with no light key, got %q light=%v", d.Mode, d.Light)
	}
	r := w403Run(t, dir, "--light", "--base", base)
	if r.Mode != "scoped" || r.Light == nil || !*r.Light {
		t.Fatalf("--light must answer scoped with light: true, got %q light=%v (diagnostics %v)", r.Mode, r.Light, r.Diagnostics)
	}
	if got, want := r.phase("scoped"), []string{"GOWORK=off go test -count=1 ./a ./b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped commands = %v, want %v", got, want)
	}
	if !r.diag("--light", "forced_full") {
		t.Fatalf("--light must say the conf's forced-full triggers do not apply: %v", r.Diagnostics)
	}
}

// TestW403EngineLightKeepsStackFallbacks: a go.mod change still answers full
// under --light (the analyzer cannot scope it).
func TestW403EngineLightKeepsStackFallbacks(t *testing.T) {
	dir, base := w25e2eFixture(t, w403GoFiles(w403GoConf))
	w25e2eWrite(t, dir, "go.mod", "module fixture.test/w403\n\ngo 1.22\n")
	w25e2eCommit(t, dir, "w403 go.mod")
	if r := w403Run(t, dir, "--light", "--base", base); r.Mode != "full" {
		t.Fatalf("a go.mod change must stay full under --light, got %q", r.Mode)
	}
}

// TestW403EngineLightNestedRoot: at the nested root an uncovered path and a
// path outside the root force nothing under --light; the nested conf's
// forced-full path neither. The covered change selects its carrying suite.
func TestW403EngineLightNestedRoot(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{
		"pi-web/lib/panel.ts":    "export const panel = 2;\n",
		"pi-web/proxy.ts":        "export const proxy = 2;\n",
		"pi-web/next.config.ts":  "export default { x: 1 };\n",
		"cmd/app/main.go":        "package main\n\nfunc main() { _ = 1 }\n",
		"pi-web/e2e/other.mjs":   "// carries: lib/other*\nconsole.log('other2');\n",
		"pi-web/docs/guide.md":   "# guide 2\n",
		"pi-web/lib/unrelated.x": "x\n",
	})
	nested := filepath.Join(dir, "pi-web")
	if d := w403Run(t, nested, "--base", base); d.Mode != "full" {
		t.Fatalf("without --light the nested diff must answer full, got %q", d.Mode)
	}
	r := w403Run(t, nested, "--light", "--base", base)
	if r.Mode != "scoped" {
		t.Fatalf("--light at the nested root must stay scoped, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
	}
	if got, want := r.phase("scoped"), []string{"npm run test:e2e:all -- e2e/other.mjs e2e/panel.mjs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped commands = %v, want %v", got, want)
	}
	if slices.ContainsFunc(r.Paths, func(p string) bool { return strings.HasPrefix(p, "../") }) {
		t.Fatalf("--light leaves paths outside the nested root to the enclosing root: %v", r.Paths)
	}
	if !r.diag("--light", "../cmd/app/main.go") || !r.diag("--light", "proxy.ts") {
		t.Fatalf("--light must name the outside path and the uncovered path it leaves: %v", r.Diagnostics)
	}
}

// TestW403EngineLightExcludesFullAndMicro: --light is its own tier.
func TestW403EngineLightExcludesFullAndMicro(t *testing.T) {
	dir, _ := w25e2eFixture(t, w403GoFiles(w403GoConf))
	for _, extra := range []string{"--full", "--micro"} {
		if _, errb, code := w25e2eRun(t, dir, nil, "--light", extra); code != 2 || !strings.Contains(errb, "--light cannot be combined with "+extra) {
			t.Fatalf("--light %s must be a usage error (exit 2 naming --light), got exit %d: %s", extra, code, errb)
		}
	}
}
