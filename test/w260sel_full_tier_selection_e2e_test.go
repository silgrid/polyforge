// carries: workflows/_shared/scripts/verify-scope

package test

// w260sel_full_tier_selection_e2e_test.go: authored acceptance scenarios for
// the full-tier e2e selection of an opted-in repository (verification scope,
// part A of the work item): a repository opts in by declaring its smoke
// suites in `.polyforge/verify-scope.conf`, and the full tier then runs its
// fast gates plus the carries-selected suites union the declared smoke
// suites, with the three fallback classes: a documentation-only diff runs the
// smoke suites only, a covered non-documentation path beside an uncovered one
// still selects and lists the uncovered path, and a forced-full match or a
// structural selection diagnostic runs the full e2e command.
//
// Authored from artifacts/spec.md's `## Acceptance criteria` and `####
// Scenario` blocks BEFORE the implementation, and driven only through the
// engine's documented CLI surface (`--full`, `--json` over a real git
// fixture), so the baseline red is assertion-level (the report carries no
// `e2e_selection` block yet), never a build or wiring failure. The scoped
// tier, the release lane and the no-declaration repository keep today's
// behaviour; those are regression criteria owned by the existing suites and
// get no scenario here.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW260Sel' -count=1 -v
//
// Every scenario prints one `PASS S<n>` marker line when it passes, so the
// verification step can bind each scenario to its own output line.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// w260SelConf is the opted-in declaration: the new repeatable smoke key
// beside the fast gate, the suite runner and the full command it keeps as
// the carried full bar.
const w260SelConf = `stack=npm
fast_gate=echo FAST
e2e_runner=npm run test:e2e:all --
full_command=npm run test:e2e:all
smoke_suite=e2e/smoke-core.mjs
smoke_suite=e2e/smoke-jobs.mjs
`

// w260SelNpmFiles is an npm tree whose four suites declare their covered
// paths, one helper is imported by one suite, the runner target is excluded,
// and the smoke suites carry nothing.
func w260SelNpmFiles() map[string]string {
	return map[string]string{
		"package.json": "" +
			"{\n  \"name\": \"w260-sel\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test:e2e:all\": \"node e2e/all.mjs\"}\n}\n",
		"components/Panel.tsx":         "export const Panel = 1;\n",
		"components/Other.tsx":         "export const Other = 1;\n",
		"internal/secret.ts":           "export const secret = 1;\n",
		"scripts/deploy.sh":            "#!/bin/sh\necho deploy\n",
		"docs/guide.md":                "# guide\n",
		"e2e/timeouts.mjs":             "export const t = 1;\n",
		"e2e/panel.mjs":                "// carries: components/Panel*\nconsole.log('panel');\n",
		"e2e/other.mjs":                "// carries: components/Other*\nconsole.log('other');\n",
		"e2e/panel-import.mjs":         "// carries: never/*\nimport { t } from './timeouts.mjs';\nconsole.log(t);\n",
		"e2e/smoke-core.mjs":           "// carries: never/*\nconsole.log('smoke core');\n",
		"e2e/smoke-jobs.mjs":           "// carries: never/*\nconsole.log('smoke jobs');\n",
		"e2e/all.mjs":                  "// carries: components/*\nconsole.log('all');\n",
		".polyforge/verify-scope.conf": w260SelConf,
	}
}

// w260SelSelection is the report's `e2e_selection` block.
type w260SelSelection struct {
	Mode           string   `json:"mode"`
	Suites         []string `json:"suites"`
	UncoveredPaths []string `json:"uncovered_paths"`
	FullCommands   []string `json:"full_commands"`
}

// w260SelReport is the subset of the JSON report these scenarios judge.
// Unknown fields are ignored so the report can grow.
type w260SelReport struct {
	Mode         string            `json:"mode"`
	Commands     []w260SelCommand  `json:"commands"`
	Diagnostics  []string          `json:"diagnostics"`
	E2ESelection *w260SelSelection `json:"e2e_selection"`
}

// w260SelCommand is one reported command.
type w260SelCommand struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Cmd   string `json:"cmd"`
}

// w260SelRun runs the engine at the full tier and decodes its JSON report.
func w260SelRun(t *testing.T, dir string) (w260SelReport, string) {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, "--full", "--json")
	if code != 0 {
		t.Fatalf("workflows/_shared/scripts/verify-scope --full --json: exit %d (stdout %q, stderr %q)", code, out, errb)
	}
	var r w260SelReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not one JSON object: %v\n%s", err, out)
	}
	return r, errb
}

// w260SelCmds answers the reported command lines.
func (r w260SelReport) cmds() []string {
	out := make([]string, 0, len(r.Commands))
	for _, c := range r.Commands {
		out = append(out, c.Cmd)
	}
	return out
}

// w260SelHasCmd reports whether exactly this command line is reported.
func (r w260SelReport) hasCmd(cmd string) bool {
	for _, c := range r.cmds() {
		if c == cmd {
			return true
		}
	}
	return false
}

// w260SelE2ECmds answers every reported command that names an e2e suite.
func (r w260SelReport) e2eCmds() []string {
	var out []string
	for _, c := range r.cmds() {
		if strings.Contains(c, "e2e/") {
			out = append(out, c)
		}
	}
	return out
}

// w260SelRequire audits one successful selection: the report names the mode,
// the selected suites and the uncovered paths, and the commands actually
// reported are exactly the fast gate plus one runner command carrying that
// suite list — the record matches the run.
func w260SelRequire(t *testing.T, r w260SelReport, mode string, suites []string) {
	t.Helper()
	if r.E2ESelection == nil {
		t.Fatalf("the full-tier report carries no e2e_selection block (mode/suites/uncovered_paths): "+
			"commands %v, diagnostics %v", r.cmds(), r.Diagnostics)
	}
	if r.E2ESelection.Mode != mode {
		t.Fatalf("e2e_selection.mode = %q, want %q (selection %+v, commands %v)",
			r.E2ESelection.Mode, mode, *r.E2ESelection, r.cmds())
	}
	if !reflect.DeepEqual(r.E2ESelection.Suites, suites) {
		t.Fatalf("e2e_selection.suites = %v, want %v", r.E2ESelection.Suites, suites)
	}
	want := "npm run test:e2e:all -- " + strings.Join(suites, " ")
	if !r.hasCmd(want) {
		t.Fatalf("the reported commands do not run the recorded selection: missing %q in %v", want, r.cmds())
	}
	if got := r.e2eCmds(); len(got) != 1 || got[0] != want {
		t.Fatalf("the e2e phase must run exactly the recorded selection command %q, got %v", want, got)
	}
	if !r.hasCmd("echo FAST") {
		t.Fatalf("the fast gates must still run; commands %v", r.cmds())
	}
}

// w260SelChange writes one file and commits it against the fixture base.
func w260SelChange(t *testing.T, dir, rel, body string) string {
	t.Helper()
	w25e2eWrite(t, dir, rel, body)
	return w25e2eCommit(t, dir, "w260sel change "+rel)
}

// w260SelFixture builds the opted-in fixture and answers its dir and base.
func w260SelFixture(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	return w25e2eFixture(t, files)
}

// w260SelSorted answers the sorted union the selection must report.
func w260SelSorted(suites ...string) []string {
	out := append([]string(nil), suites...)
	sort.Strings(out)
	return out
}

// TestW260SelS01PinnedFileSelection: a change to one file a single suite
// declares runs that suite plus the declared smoke suites, and no other
// suite; the report names the mode, the suites and the carried full command.
func TestW260SelS01PinnedFileSelection(t *testing.T) {
	dir, _ := w260SelFixture(t, w260SelNpmFiles())
	w260SelChange(t, dir, "components/Panel.tsx", "export const Panel = 2;\n")
	r, _ := w260SelRun(t, dir)

	suites := w260SelSorted("e2e/panel.mjs", "e2e/smoke-core.mjs", "e2e/smoke-jobs.mjs")
	w260SelRequire(t, r, "carries", suites)
	for _, not := range []string{"e2e/other.mjs", "e2e/panel-import.mjs", "e2e/all.mjs"} {
		for _, c := range r.e2eCmds() {
			if strings.Contains(c, not) {
				t.Fatalf("the selection must not run %s, commands %v", not, r.e2eCmds())
			}
		}
	}
	if r.E2ESelection == nil || len(r.E2ESelection.FullCommands) == 0 {
		t.Fatalf("the report must record the full command the selection carries for delivery: %+v", r.E2ESelection)
	}
	if !w260SelHasValue(r.E2ESelection.FullCommands, "npm run test:e2e:all") {
		t.Fatalf("e2e_selection.full_commands = %v, want the repository's full e2e command", r.E2ESelection.FullCommands)
	}
	fmt.Println("PASS S01")
}

// TestW260SelS02SharedHelperSelection: a change to a helper that one suite
// imports selects the importing suite plus the declared smoke suites.
func TestW260SelS02SharedHelperSelection(t *testing.T) {
	dir, _ := w260SelFixture(t, w260SelNpmFiles())
	w260SelChange(t, dir, "e2e/timeouts.mjs", "export const t = 2;\n")
	r, _ := w260SelRun(t, dir)

	suites := w260SelSorted("e2e/panel-import.mjs", "e2e/smoke-core.mjs", "e2e/smoke-jobs.mjs")
	w260SelRequire(t, r, "carries", suites)
	for _, c := range r.e2eCmds() {
		if strings.Contains(c, "e2e/panel.mjs") || strings.Contains(c, "e2e/other.mjs") {
			t.Fatalf("a helper change must select only its importers plus smoke, got %v", r.e2eCmds())
		}
	}
	fmt.Println("PASS S02")
}

// TestW260SelS03DocsOnlySmoke: a documentation-only diff keeps the fast gates
// and runs only the declared smoke suites in the e2e phase.
func TestW260SelS03DocsOnlySmoke(t *testing.T) {
	dir, _ := w260SelFixture(t, w260SelNpmFiles())
	w260SelChange(t, dir, "docs/guide.md", "# guide changed\n")
	r, _ := w260SelRun(t, dir)

	suites := w260SelSorted("e2e/smoke-core.mjs", "e2e/smoke-jobs.mjs")
	w260SelRequire(t, r, "smoke", suites)
	fmt.Println("PASS S03")
}

// TestW260SelS04UncoveredPathRecorded: a covered file beside an uncovered
// non-documentation file still runs the selection plus smoke, and the report
// lists the uncovered path instead of falling back to the full e2e command.
func TestW260SelS04UncoveredPathRecorded(t *testing.T) {
	dir, _ := w260SelFixture(t, w260SelNpmFiles())
	w260SelChange(t, dir, "components/Panel.tsx", "export const Panel = 2;\n")
	w260SelChange(t, dir, "scripts/deploy.sh", "#!/bin/sh\necho deploy 2\n")
	r, _ := w260SelRun(t, dir)

	suites := w260SelSorted("e2e/panel.mjs", "e2e/smoke-core.mjs", "e2e/smoke-jobs.mjs")
	w260SelRequire(t, r, "carries", suites)
	if r.E2ESelection == nil || !reflect.DeepEqual(r.E2ESelection.UncoveredPaths, []string{"scripts/deploy.sh"}) {
		t.Fatalf("e2e_selection.uncovered_paths = %+v, want [scripts/deploy.sh]", r.E2ESelection)
	}
	if r.hasCmd("npm run test:e2e:all") {
		t.Fatalf("an uncovered path must not force the full e2e command: commands %v", r.cmds())
	}
	fmt.Println("PASS S04")
}

// TestW260SelS05FullFallbackRecorded: a forced_full match and a changed
// selection conf each run the full e2e command and record the fallback as
// mode full, the structural diagnostic included.
func TestW260SelS05FullFallbackRecorded(t *testing.T) {
	forced := w260SelNpmFiles()
	forced[".polyforge/verify-scope.conf"] = w260SelConf + "forced_full=internal/*\n"
	dir, _ := w260SelFixture(t, forced)
	w260SelChange(t, dir, "internal/secret.ts", "export const secret = 2;\n")
	r, _ := w260SelRun(t, dir)
	if r.E2ESelection == nil {
		t.Fatalf("the forced-full report carries no e2e_selection block: commands %v, diagnostics %v", r.cmds(), r.Diagnostics)
	}
	if r.E2ESelection.Mode != "full" {
		t.Fatalf("a forced_full match must select mode full, got %q (%+v)", r.E2ESelection.Mode, *r.E2ESelection)
	}
	if !r.hasCmd("npm run test:e2e:all") {
		t.Fatalf("a forced_full match must run the repository's full e2e command, commands %v", r.cmds())
	}
	if len(r.e2eCmds()) != 0 {
		t.Fatalf("a forced_full match must not run a selection beside the full command: %v", r.e2eCmds())
	}

	structural, _ := w260SelFixture(t, w260SelNpmFiles())
	w260SelChange(t, structural, ".polyforge/verify-scope.conf", w260SelConf+"# the selection conf itself changed\n")
	s, errb := w260SelRun(t, structural)
	if s.E2ESelection == nil {
		t.Fatalf("the changed-conf report carries no e2e_selection block: commands %v, diagnostics %v", s.cmds(), s.Diagnostics)
	}
	if s.E2ESelection.Mode != "full" {
		t.Fatalf("a changed selection conf must select mode full, got %q (%+v)", s.E2ESelection.Mode, *s.E2ESelection)
	}
	if !s.hasCmd("npm run test:e2e:all") {
		t.Fatalf("a changed selection conf must run the full e2e command, commands %v", s.cmds())
	}
	if strings.TrimSpace(errb) == "" {
		t.Fatalf("a structural fallback must be diagnosed on stderr (the conf change), diagnostics %v", s.Diagnostics)
	}
	fmt.Println("PASS S05")
}

// w260SelHasValue answers whether the list carries the value.
func w260SelHasValue(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
