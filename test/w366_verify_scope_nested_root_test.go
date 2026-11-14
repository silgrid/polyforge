// carries: workflows/_shared/scripts/verify-scope

package test

// w366_verify_scope_nested_root_test.go: the scope engine at a
// nested project root. The merged repository keeps the pi-web package under
// pi-web/ with its own `.polyforge/verify-scope.conf`, which applies when the
// engine runs with `cd pi-web`. git names a diff's paths from the work tree's
// top (`pi-web/lib/...`) while the suites' `carries:` globs, the conf's globs
// and the listed files are relative to the engine root (`lib/...`); the
// engine now makes the diff relative to its root too. These scenarios pin
// that at a nested root a covered change selects only its carrying suites
// (plus the declared smoke suites at the full tier), that every fail-closed
// rule still holds there — an uncovered path, a path outside the root, the
// nested conf's forced-full paths and a change to the nested conf itself —
// that --micro reads the nested root's own conf, and that a run at the work
// tree's top is unchanged.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW366' -count=1 -v

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// w366NestedConf is the nested package's declaration: its own fast gate, a
// forced-full path, the suite runner, the full e2e command and one smoke
// suite (the full-tier e2e selection opt-in).
const w366NestedConf = `stack=npm
fast_gate=echo NESTED-FAST
forced_full=.polyforge
forced_full=.polyforge/*
forced_full=next.config.ts
e2e_runner=npm run test:e2e:all --
full_command=npm run test:e2e:all
smoke_suite=e2e/smoke.mjs
`

// w366Files is a work tree whose top is a Go module driven by this
// repository's shipped root conf, with an npm package under pi-web/ that
// carries its own conf: two suites declare one lib file each, the runner
// target is never a suite, and the smoke suite declares nothing it covers.
func w366Files(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"go.mod":                              "module fixture.test/w366\n\ngo 1.21\n",
		"cmd/app/main.go":                     "package main\n\nfunc main() {}\n",
		".polyforge/verify-scope.conf":        w25engShippedConf(t),
		"pi-web/go.mod":                       "module fixture.test/w366/pi-web\n\ngo 1.21\n",
		"pi-web/package.json":                 "{\n  \"name\": \"w366-nested\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test:e2e:all\": \"node e2e/all.mjs\"}\n}\n",
		"pi-web/.polyforge/verify-scope.conf": w366NestedConf,
		"pi-web/next.config.ts":               "export default {};\n",
		"pi-web/lib/panel.ts":                 "export const panel = 1;\n",
		"pi-web/lib/other.ts":                 "export const other = 1;\n",
		"pi-web/proxy.ts":                     "export const proxy = 1;\n",
		"pi-web/docs/guide.md":                "# guide\n",
		"pi-web/e2e/all.mjs":                  "// carries: lib/*\nconsole.log('all');\n",
		"pi-web/e2e/panel.mjs":                "// carries: lib/panel*\nconsole.log('panel');\n",
		"pi-web/e2e/other.mjs":                "// carries: lib/other*\nconsole.log('other');\n",
		"pi-web/e2e/smoke.mjs":                "// carries: never/*\nconsole.log('smoke');\n",
	}
}

// w366Report is the subset of the --json report these scenarios judge.
type w366Report struct {
	Root              string            `json:"root"`
	Conf              string            `json:"conf"`
	ConfChangedInDiff bool              `json:"conf_changed_in_diff"`
	Mode              string            `json:"mode"`
	Paths             []string          `json:"paths"`
	Commands          []w260SelCommand  `json:"commands"`
	Diagnostics       []string          `json:"diagnostics"`
	SmokeSuite        []string          `json:"smoke_suite"`
	E2ESelection      *w260SelSelection `json:"e2e_selection"`
	MicroPaths        []string          `json:"micro_paths"`
}

// w366Run runs the engine with dir as its root and decodes the report.
func w366Run(t *testing.T, dir string, args ...string) w366Report {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --json %v in %s: exit %d (stdout %q, stderr %q)", args, dir, code, out, errb)
	}
	var r w366Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not one JSON object: %v\n%s", err, out)
	}
	return r
}

// phase answers the commands of one phase, in report order.
func (r w366Report) phase(p string) []string {
	var out []string
	for _, c := range r.Commands {
		if c.Phase == p {
			out = append(out, c.Cmd)
		}
	}
	return out
}

// e2e answers every reported command that runs the package's e2e runner.
func (r w366Report) e2e() []string {
	var out []string
	for _, c := range r.Commands {
		if strings.HasPrefix(c.Cmd, "npm run test:e2e:all") {
			out = append(out, c.Cmd)
		}
	}
	return out
}

// diag reports whether one diagnostic names every given fragment.
func (r w366Report) diag(frags ...string) bool {
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

// w366Fixture builds the nested fixture, commits the given changes on top of
// its base and answers the work tree's top and the base.
func w366Fixture(t *testing.T, change map[string]string) (string, string) {
	t.Helper()
	dir, base := w25e2eFixture(t, w366Files(t))
	for rel, body := range change {
		w25e2eWrite(t, dir, rel, body)
	}
	w25e2eCommit(t, dir, "w366 change")
	return dir, base
}

// TestW366NestedRootScopedSelectsCarryingSuites: at the nested root a covered
// change is reported relative to that root and selects exactly the suite
// whose carries: glob names it, through the declared runner.
func TestW366NestedRootScopedSelectsCarryingSuites(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{"pi-web/lib/panel.ts": "export const panel = 2;\n"})
	r := w366Run(t, filepath.Join(dir, "pi-web"), "--base", base)
	if r.Mode != "scoped" {
		t.Fatalf("a covered change at the nested root must stay scoped, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
	}
	if !reflect.DeepEqual(r.Paths, []string{"lib/panel.ts"}) {
		t.Fatalf("paths = %v, want the root-relative [lib/panel.ts]", r.Paths)
	}
	if got, want := r.phase("scoped"), []string{"npm run test:e2e:all -- e2e/panel.mjs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped commands = %v, want %v", got, want)
	}
	if got, want := r.phase("fast"), []string{"echo NESTED-FAST"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fast gates = %v, want the nested conf's %v", got, want)
	}
	if len(r.Diagnostics) != 0 {
		t.Fatalf("a covered change must not be diagnosed: %v", r.Diagnostics)
	}
	// The line report carries the same root-relative path.
	out, errb, code := w25e2eRun(t, filepath.Join(dir, "pi-web"), nil, "--base", base)
	if code != 0 {
		t.Fatalf("line report: exit %d (%s)", code, errb)
	}
	if lr := w25e2eParse(t, out); lr.mode != "scoped" || !reflect.DeepEqual(lr.paths, []string{"lib/panel.ts"}) {
		t.Fatalf("line report = %q %v, want scoped [lib/panel.ts]", lr.mode, lr.paths)
	}
}

// TestW366NestedRootFullTierSelectsCarriesPlusSmoke: at the full tier the
// same change runs only its carrying suite plus the declared smoke suite,
// and the full e2e command stays the carried bar.
func TestW366NestedRootFullTierSelectsCarriesPlusSmoke(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{"pi-web/lib/panel.ts": "export const panel = 2;\n"})
	r := w366Run(t, filepath.Join(dir, "pi-web"), "--full", "--base", base)
	if r.Mode != "full" || r.E2ESelection == nil {
		t.Fatalf("--full must report mode full with an e2e_selection, got %q %+v (diagnostics %v)", r.Mode, r.E2ESelection, r.Diagnostics)
	}
	sel := *r.E2ESelection
	if sel.Mode != "carries" || !reflect.DeepEqual(sel.Suites, []string{"e2e/panel.mjs", "e2e/smoke.mjs"}) || len(sel.UncoveredPaths) != 0 {
		t.Fatalf("e2e_selection = %+v, want carries [e2e/panel.mjs e2e/smoke.mjs] with no uncovered path", sel)
	}
	if got, want := r.e2e(), []string{"npm run test:e2e:all -- e2e/panel.mjs e2e/smoke.mjs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("e2e commands = %v, want exactly %v", got, want)
	}
	if !slices.Contains(sel.FullCommands, "npm run test:e2e:all") {
		t.Fatalf("the carried full tail must keep the full e2e command: %v", sel.FullCommands)
	}
}

// TestW366NestedRootUncoveredPathFallsBackFull: a path under the nested root
// that no suite carries still sends the scoped tier to full, named; the
// full-tier selection lists it as uncovered (its documented class) and runs
// the smoke suite.
func TestW366NestedRootUncoveredPathFallsBackFull(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{"pi-web/proxy.ts": "export const proxy = 2;\n"})
	nested := filepath.Join(dir, "pi-web")
	s := w366Run(t, nested, "--base", base)
	if s.Mode != "full" || !s.diag("changed path proxy.ts is covered by no carries: declaration") {
		t.Fatalf("an uncovered path must fall back to full, named root-relative; got %q %v", s.Mode, s.Diagnostics)
	}
	if got, want := s.e2e(), []string{"npm run test:e2e:all"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the full tier must run the full e2e command %v, got %v", want, got)
	}
	f := w366Run(t, nested, "--full", "--base", base)
	if f.E2ESelection == nil || f.E2ESelection.Mode != "carries" ||
		!reflect.DeepEqual(f.E2ESelection.Suites, []string{"e2e/smoke.mjs"}) ||
		!reflect.DeepEqual(f.E2ESelection.UncoveredPaths, []string{"proxy.ts"}) {
		t.Fatalf("e2e_selection = %+v, want carries [e2e/smoke.mjs] listing uncovered [proxy.ts]", f.E2ESelection)
	}
}

// TestW366NestedRootOutsidePathFallsBackFull: a change outside the nested
// root is reported as ../<path> and no declaration of the root can account
// for it: the scoped tier is full and the full-tier selection runs the full
// tail, each diagnosed.
func TestW366NestedRootOutsidePathFallsBackFull(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{
		"pi-web/lib/panel.ts": "export const panel = 2;\n",
		"cmd/app/main.go":     "package main\n\nfunc main() { println() }\n",
	})
	nested := filepath.Join(dir, "pi-web")
	s := w366Run(t, nested, "--base", base)
	if !reflect.DeepEqual(s.Paths, []string{"../cmd/app/main.go", "lib/panel.ts"}) {
		t.Fatalf("paths = %v, want [../cmd/app/main.go lib/panel.ts]", s.Paths)
	}
	if s.Mode != "full" || !s.diag("changed path ../cmd/app/main.go is outside the engine root (pi-web of the git work tree)") {
		t.Fatalf("a path outside the root must fall back to full, diagnosed; got %q %v", s.Mode, s.Diagnostics)
	}
	f := w366Run(t, nested, "--full", "--base", base)
	if f.E2ESelection == nil || f.E2ESelection.Mode != "full" ||
		!f.diag("full-tier e2e selection falls back to the full e2e command: changed path ../cmd/app/main.go is outside the engine root") {
		t.Fatalf("the full-tier selection must fall back to the full tail, diagnosed; got %+v %v", f.E2ESelection, f.Diagnostics)
	}
	if got, want := f.e2e(), []string{"npm run test:e2e:all"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("e2e commands = %v, want the full tail %v", got, want)
	}
}

// TestW366NestedRootOwnRulesForceFull: the nested root's own full-tier
// triggers now see its paths — the npm manifest, a forced_full= glob and
// the nested conf itself (before, `pi-web/package.json` matched none of them
// and the full-tier selection ran the smoke suite only).
func TestW366NestedRootOwnRulesForceFull(t *testing.T) {
	for _, tc := range []struct{ rel, body, reason string }{
		{"pi-web/package.json", "{\n  \"name\": \"w366-nested\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test:e2e:all\": \"node e2e/all.mjs\", \"x\": \"true\"}\n}\n", "changed path package.json is forced full"},
		{"pi-web/next.config.ts", "export default { x: 1 };\n", "changed path next.config.ts is forced full"},
		{"pi-web/.polyforge/verify-scope.conf", w366NestedConf + "# touched\n", "the diff changes .polyforge/verify-scope.conf"},
	} {
		t.Run(filepath.Base(tc.rel), func(t *testing.T) {
			dir, base := w366Fixture(t, map[string]string{tc.rel: tc.body})
			nested := filepath.Join(dir, "pi-web")
			if s := w366Run(t, nested, "--base", base); s.Mode != "full" {
				t.Fatalf("%s must force the full tier, got %q", tc.rel, s.Mode)
			}
			f := w366Run(t, nested, "--full", "--base", base)
			if f.E2ESelection == nil || f.E2ESelection.Mode != "full" || !f.diag(tc.reason) {
				t.Fatalf("%s: the full-tier selection must run the full tail (%q); got %+v %v", tc.rel, tc.reason, f.E2ESelection, f.Diagnostics)
			}
			if strings.HasSuffix(tc.rel, "verify-scope.conf") && !f.ConfChangedInDiff {
				t.Fatalf("conf_changed_in_diff must be true for a change to the nested conf")
			}
		})
	}
}

// TestW366NestedRootMicroReadsOwnConf: --micro reads the nested root's conf
// at the diff base, not the work tree top's.
func TestW366NestedRootMicroReadsOwnConf(t *testing.T) {
	files := w366Files(t)
	files["pi-web/.polyforge/verify-scope.conf"] = w366NestedConf + "micro_path=docs/*\nmicro_check=echo NESTED-MICRO\n"
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "pi-web/docs/guide.md", "# guide 2\n")
	w25e2eCommit(t, dir, "w366 docs change")
	r := w366Run(t, filepath.Join(dir, "pi-web"), "--micro", "--base", base)
	var got []string
	for _, c := range r.Commands {
		got = append(got, c.Phase+":"+c.Cmd)
	}
	if want := []string{"fast:echo NESTED-FAST", "micro:echo NESTED-MICRO"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("--micro at the nested root = %v, want the nested conf's %v (diagnostics %v)", got, want, r.Diagnostics)
	}
	if !reflect.DeepEqual(r.MicroPaths, []string{"docs/*"}) {
		t.Fatalf("micro_paths = %v, want the nested conf's [docs/*]", r.MicroPaths)
	}
}

// TestW366TopRootUnchanged: at the work tree's top the shipped root conf
// still forces the full tier for a pi-web/ change, with top-relative paths,
// the pi-web bar in the full tail and no outside-root diagnostic.
func TestW366TopRootUnchanged(t *testing.T) {
	dir, base := w366Fixture(t, map[string]string{"pi-web/lib/panel.ts": "export const panel = 2;\n"})
	for _, args := range [][]string{{"--base", base}, {"--full", "--base", base}} {
		r := w366Run(t, dir, args...)
		if r.Mode != "full" || !reflect.DeepEqual(r.Paths, []string{"pi-web/lib/panel.ts"}) {
			t.Fatalf("%v at the top: mode %q paths %v, want full [pi-web/lib/panel.ts]", args, r.Mode, r.Paths)
		}
		if !slices.Contains(r.phase("full"), "cd pi-web && E2E_SERVER_MODE=start npm run test:e2e:all") || r.E2ESelection != nil {
			t.Fatalf("%v at the top must run the pi-web bar in the full tail without a selection: %v %+v", args, r.phase("full"), r.E2ESelection)
		}
		if r.diag("outside the engine root") {
			t.Fatalf("%v at the top must not diagnose an outside path: %v", args, r.Diagnostics)
		}
	}
}

// TestW366ShippedPiWebSubtreeSelectsByCarries runs the engine at the
// shipped pi-web/ subtree (its committed conf and suites, copied into a
// throwaway work tree under pi-web/): a change to one lib file selects only
// the suites whose carries: globs name it — a strict subset of the suites —
// and the full tier runs exactly those plus the declared smoke suites; a new
// path no suite carries still falls back to full.
func TestW366ShippedPiWebSubtreeSelectsByCarries(t *testing.T) {
	root := w25e2eRoot(t)
	for _, bin := range []string{"git", "tar", "node", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is required to run the engine's npm analyzer at the shipped subtree: %v", bin, err)
		}
	}
	dir := t.TempDir()
	w25e2eGit(t, dir, "init", "-q", "-b", "main")
	arch := exec.Command("sh", "-c", `git -C "$1" archive --format=tar HEAD pi-web | tar -x -C "$2"`, "sh", root, dir)
	if out, err := arch.CombinedOutput(); err != nil {
		t.Fatalf("export the committed pi-web/ subtree: %v\n%s", err, out)
	}
	base := w25e2eCommit(t, dir, "w366 shipped subtree")
	nested := filepath.Join(dir, "pi-web")
	const changed = "lib/machine-load.ts"
	raw, err := os.ReadFile(filepath.Join(nested, changed))
	if err != nil {
		t.Fatalf("the shipped subtree must hold %s: %v", changed, err)
	}
	w25e2eWrite(t, dir, "pi-web/"+changed, string(raw)+"\n// w366\n")
	w25e2eCommit(t, dir, "w366 change "+changed)

	s := w366Run(t, nested, "--base", base)
	if s.Mode != "scoped" || !reflect.DeepEqual(s.Paths, []string{changed}) {
		t.Fatalf("a covered pi-web change must stay scoped with the root-relative path; got %q %v (diagnostics %v)", s.Mode, s.Paths, s.Diagnostics)
	}
	scoped := s.phase("scoped")
	if len(scoped) != 1 || !strings.HasPrefix(scoped[0], "npm run test:e2e:all -- ") {
		t.Fatalf("the scoped tier must run the selected suites as one runner command, got %v", scoped)
	}
	carrying := strings.Fields(strings.TrimPrefix(scoped[0], "npm run test:e2e:all -- "))
	if !slices.Contains(carrying, "e2e/machine-load.mjs") {
		t.Fatalf("the suite declaring lib/machine-load* must be selected: %v", carrying)
	}

	f := w366Run(t, nested, "--full", "--base", base)
	if f.E2ESelection == nil || f.E2ESelection.Mode != "carries" || len(f.E2ESelection.UncoveredPaths) != 0 {
		t.Fatalf("the full tier must select by carries with no uncovered path: %+v (diagnostics %v)", f.E2ESelection, f.Diagnostics)
	}
	if len(f.SmokeSuite) == 0 {
		t.Fatalf("the shipped pi-web conf must declare its smoke suites")
	}
	want := append(append([]string(nil), carrying...), f.SmokeSuite...)
	slices.Sort(want)
	want = slices.Compact(want)
	if !reflect.DeepEqual(f.E2ESelection.Suites, want) {
		t.Fatalf("e2e_selection.suites = %v, want the carrying suites %v plus the smoke suites %v", f.E2ESelection.Suites, carrying, f.SmokeSuite)
	}
	all := 0
	for _, c := range f.E2ESelection.FullCommands {
		if c == "E2E_SERVER_MODE=start npm run test:e2e:all" {
			all++
		}
	}
	if all != 1 {
		t.Fatalf("the carried full tail must keep the full e2e command once: %v", f.E2ESelection.FullCommands)
	}
	entries, err := os.ReadDir(filepath.Join(nested, "e2e"))
	if err != nil {
		t.Fatal(err)
	}
	mjs := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".mjs") {
			mjs++
		}
	}
	if len(f.E2ESelection.Suites) >= mjs/2 {
		t.Fatalf("the selection (%d suites) must be a narrow subset of the %d e2e modules: %v", len(f.E2ESelection.Suites), mjs, f.E2ESelection.Suites)
	}

	// A path no suite carries still falls back to full.
	w25e2eWrite(t, dir, "pi-web/scripts/w366-uncovered.sh", "#!/bin/sh\n")
	w25e2eCommit(t, dir, "w366 uncovered path")
	u := w366Run(t, nested, "--base", base)
	if u.Mode != "full" || !u.diag("changed path scripts/w366-uncovered.sh is covered by no carries: declaration") {
		t.Fatalf("an uncovered pi-web path must fall back to full, named; got %q %v", u.Mode, u.Diagnostics)
	}
}
