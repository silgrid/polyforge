package test

// w227_carries_e2e_test.go: authored acceptance scenarios for the
// stack-neutral `carries:` layer of the release-shipped verification-scope
// engine and the python/rust analyzers.
//
// Authored from artifacts/spec.md's `## Acceptance criteria` and `####
// Scenario` blocks BEFORE the implementation exists. Every scenario drives
// the engine only through its documented CLI surface — the target clone as
// working directory, `--base`, `--full`, `--json` — over real git fixtures.
// No implementation internals are referenced. It reuses the w25e2e fixture
// helpers (same package).
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW227CarriesS' -count=1 -v
//
// Every scenario prints one `PASS S<n>` marker line when it passes, so the
// verification step can bind each scenario to its own output line.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// w227Repo builds and commits a git fixture and answers its dir and base SHA.
func w227Repo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	return w25e2eFixture(t, files)
}

// w227Change writes one file and commits it against the fixture base.
func w227Change(t *testing.T, dir, rel, body string) string {
	t.Helper()
	w25e2eWrite(t, dir, rel, body)
	return w25e2eCommit(t, dir, "w227 change "+rel)
}

// w227Run parses the line report and answers stderr too.
func w227Run(t *testing.T, dir string, args ...string) (w25e2eReport, string) {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, args...)
	if code != 0 {
		t.Fatalf("tools/verify-scope %v: exit %d (stdout %q, stderr %q)", args, code, out, errb)
	}
	return w25e2eParse(t, out), errb
}

// w227Scoped runs the engine on the fixture's recorded base.
func w227Scoped(t *testing.T, dir string) w25e2eReport {
	t.Helper()
	r, _ := w227Run(t, dir)
	return r
}

// w227JSONReport is the subset of the schema-1 report these scenarios judge.
type w227JSONReport struct {
	Mode        string            `json:"mode"`
	Stack       string            `json:"stack"`
	Analyzer    *string           `json:"analyzer"`
	Diagnostics []string          `json:"diagnostics"`
	Commands    []w227JSONCommand `json:"commands"`
}

type w227JSONCommand struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Cmd   string `json:"cmd"`
}

// w227JSON runs --json and decodes only the fields these scenarios judge.
// Unknown echo fields are ignored so the contract can grow without rewriting
// the scenarios. It also requires the JSON diagnostics to equal stderr.
func w227JSON(t *testing.T, dir string) w227JSONReport {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, "--json")
	if code != 0 {
		t.Fatalf("tools/verify-scope --json: exit %d (stderr %q)", code, errb)
	}
	var r w227JSONReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not one JSON object: %v\n%s", err, out)
	}
	var stderrLines []string
	for _, ln := range strings.Split(strings.TrimRight(errb, "\n"), "\n") {
		if ln != "" {
			stderrLines = append(stderrLines, ln)
		}
	}
	if len(stderrLines) == 0 {
		stderrLines = nil
	}
	if len(r.Diagnostics) == 0 {
		r.Diagnostics = nil
	}
	if !reflect.DeepEqual(r.Diagnostics, stderrLines) {
		t.Fatalf("JSON diagnostics must equal the stderr lines:\njson   %q\nstderr %q", r.Diagnostics, stderrLines)
	}
	return r
}

// w227Echo answers one extra JSON echo field (the new conf keys).
func w227Echo(t *testing.T, dir, key string) any {
	t.Helper()
	out, _, code := w25e2eRun(t, dir, nil, "--json")
	if code != 0 {
		t.Fatalf("tools/verify-scope --json: exit %d", code)
	}
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("--json: %v", err)
	}
	return raw[key]
}

// w227HasCmd reports whether the report carries exactly the command.
func w227HasCmd(r w25e2eReport, cmd string) bool {
	for _, c := range r.commands {
		if c == cmd {
			return true
		}
	}
	return false
}

// w227RequireCmd fails unless the report carries exactly the command.
func w227RequireCmd(t *testing.T, r w25e2eReport, want string) {
	t.Helper()
	if !w227HasCmd(r, want) {
		t.Fatalf("missing command %q; report is mode %q commands:\n%s", want, r.mode, r.joined())
	}
}

// w227RequireNoCmd fails when any command contains the fragment.
func w227RequireNoCmd(t *testing.T, r w25e2eReport, fragment string) {
	t.Helper()
	if strings.Contains(r.joined(), fragment) {
		t.Fatalf("unexpected command fragment %q in:\n%s", fragment, r.joined())
	}
}

// w227NpmFiles is a small npm tree whose three suites all declare their
// covered paths in the opening comment block.
func w227NpmFiles() map[string]string {
	return map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-npm\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"components/Other.tsx":          "export const Other = 1;\n",
		"lib/other/thing.ts":            "export const thing = 1;\n",
		"e2e/panel-a.mjs":               "// carries: components/Polyforge*\nconsole.log('a');\n",
		"e2e/panel-b.mjs":               "// carries: components/Polyforge*\nconsole.log('b');\n",
		"e2e/other.mjs":                 "// carries: lib/other*\nconsole.log('other');\n",
	}
}

// w227RunnerFiles is a minimal npm tree for the runner-key scenarios.
func w227RunnerFiles() map[string]string {
	return map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-runner\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test:e2e:all\": \"node e2e/all.mjs\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"e2e/a.mjs":                     "// carries: components/Polyforge*\nconsole.log('a');\n",
		"e2e/b.mjs":                     "// carries: components/Polyforge*\nconsole.log('b');\n",
		"e2e/c.mjs":                     "// carries: components/Polyforge*\nconsole.log('c');\n",
		"e2e/all.mjs":                   "// carries: components/Polyforge*\nconsole.log('components/PolyforgePanel.tsx');\n",
	}
}

// w227PyFiles is a python stub: one unit test that imports the changed
// module, one declared e2e suite, and pytest's collection files beside it.
func w227PyFiles() map[string]string {
	return map[string]string{
		"pyproject.toml":         "[project]\nname = \"w227-py\"\nversion = \"0.0.0\"\n",
		"app/mod.py":             "def value():\n    return 1\n",
		"tests/unit/test_mod.py": "from app.mod import value\n\n\ndef test_value():\n    assert value() == 1\n",
		"e2e/test_panel.py":      "# carries: app/*\nimport app.mod\n\n\ndef test_panel():\n    assert app.mod.value()\n",
		"tests/e2e/conftest.py":  "import pytest\n",
		"tests/e2e/__init__.py":  "",
		"tests/e2e/test_flow.py": "# carries: app/flow*\n\n\ndef test_flow():\n    assert True\n",
	}
}

// w227RsFiles is a rust stub: one declared acceptance suite using the changed
// module, and a tests/common/mod.rs helper beside it.
func w227RsFiles() map[string]string {
	return map[string]string{
		"Cargo.toml":          "[package]\nname = \"w227\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":          "pub mod app;\n\npub fn root() -> u32 {\n    1\n}\n",
		"src/app.rs":          "pub fn value() -> u32 {\n    1\n}\n",
		"tests/acceptance.rs": "// carries: src/*\nuse w227::app::value;\n\n#[test]\nfn accepts() {\n    assert_eq!(value(), 1);\n}\n",
		"tests/common/mod.rs": "pub fn helper() {}\n",
	}
}

// ---------------------------------------------------------------------------
// scenarios
// ---------------------------------------------------------------------------

// TestW227CarriesS01HeaderDeclarations: a suite declares its covered paths in
// the opening comment block; several lines union; `*` crosses `/`; a python
// suite's `# carries:` header is parsed the same way.
func TestW227CarriesS01HeaderDeclarations(t *testing.T) {
	npmDir, _ := w227Repo(t, map[string]string{
		"package.json":        "{\n  \"name\": \"w227-union\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/a/one.ts": "export const one = 1;\n",
		"lib/b/deep/two.ts":   "export const two = 1;\n",
		"e2e/union.mjs":       "// carries: components/a*\n// carries: lib/b*\nconsole.log('union');\n",
	})
	w227Change(t, npmDir, "components/a/one.ts", "export const one = 2;\n")
	w227Change(t, npmDir, "lib/b/deep/two.ts", "export const two = 2;\n")
	r := w227Scoped(t, npmDir)
	if r.mode != "scoped" {
		t.Fatalf("a declaration covering the diff must stay scoped, got %q", r.mode)
	}
	w227RequireCmd(t, r, "node e2e/union.mjs")
	if strings.Count(r.joined(), "node e2e/union.mjs") != 1 {
		t.Fatalf("the union of two declaration lines must select the suite once:\n%s", r.joined())
	}

	pyDir, _ := w227Repo(t, w227PyFiles())
	w227Change(t, pyDir, "app/mod.py", "def value():\n    return 2\n")
	pr := w227Scoped(t, pyDir)
	if pr.mode != "scoped" {
		t.Fatalf("a python suite's # carries: header must select it, got mode %q", pr.mode)
	}
	w227RequireCmd(t, pr, "pytest e2e/test_panel.py")
	fmt.Println("PASS S01")
}

// TestW227CarriesS02OutsideOpeningBlock: a carries: line after code is not a
// declaration; the suite stays undeclared and the report is full.
func TestW227CarriesS02OutsideOpeningBlock(t *testing.T) {
	files := w227NpmFiles()
	files["e2e/trailing.mjs"] = "console.log('trailing');\n// carries: components/*\n"
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "full" {
		t.Fatalf("a carries: line outside the opening block must leave the suite undeclared and force full, got %q", r.mode)
	}
	if !strings.Contains(errb, "e2e/trailing.mjs") {
		t.Fatalf("the undeclared suite must be named on stderr, got %q", errb)
	}
	fmt.Println("PASS S02")
}

// TestW227CarriesS03Selection: a changed path selects every declaring suite
// and no other; a declaration that hits nothing selects nothing.
func TestW227CarriesS03Selection(t *testing.T) {
	dir, _ := w227Repo(t, w227NpmFiles())
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("two suites declare the changed path, so the tier stays scoped, got %q", r.mode)
	}
	w227RequireCmd(t, r, "node e2e/panel-a.mjs")
	w227RequireCmd(t, r, "node e2e/panel-b.mjs")
	w227RequireNoCmd(t, r, "node e2e/other.mjs")
	if strings.Index(r.joined(), "node e2e/panel-a.mjs") > strings.Index(r.joined(), "node e2e/panel-b.mjs") {
		t.Fatalf("selected suites must be reported in a stable order:\n%s", r.joined())
	}
	fmt.Println("PASS S03")
}

// TestW227CarriesS04UndeclaredSuite: any undeclared suite in the repository
// forces full and is named on stderr and in the JSON diagnostics.
func TestW227CarriesS04UndeclaredSuite(t *testing.T) {
	files := w227NpmFiles()
	files["e2e/undeclared.mjs"] = "console.log('undeclared');\n"
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "full" {
		t.Fatalf("one undeclared suite must force full even when the diff is covered, got %q", r.mode)
	}
	if !strings.Contains(errb, "e2e/undeclared.mjs") {
		t.Fatalf("the undeclared suite must be named on stderr, got %q", errb)
	}
	j := w227JSON(t, dir)
	if !strings.Contains(strings.Join(j.Diagnostics, "\n"), "e2e/undeclared.mjs") {
		t.Fatalf("the undeclared suite must appear in the JSON diagnostics, got %q", j.Diagnostics)
	}
	fmt.Println("PASS S04")
}

// TestW227CarriesS05UncoveredPath: a changed path no declaration accounts for
// forces full and is named.
func TestW227CarriesS05UncoveredPath(t *testing.T) {
	dir, _ := w227Repo(t, w227NpmFiles())
	w227Change(t, dir, "scripts/deploy.sh", "#!/bin/sh\necho deploy\n")
	r, errb := w227Run(t, dir)
	if r.mode != "full" {
		t.Fatalf("a changed path no declaration covers must force full, got %q", r.mode)
	}
	if !strings.Contains(errb, "scripts/deploy.sh") {
		t.Fatalf("the uncovered path must be named on stderr, got %q", errb)
	}
	fmt.Println("PASS S05")
}

// TestW227CarriesS06MalformedDeclaration: an empty or unparseable carries:
// line forces full with a diagnostic; the engine never guesses coverage.
func TestW227CarriesS06MalformedDeclaration(t *testing.T) {
	files := w227NpmFiles()
	files["e2e/empty.mjs"] = "// carries:\nconsole.log('empty');\n"
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "full" {
		t.Fatalf("an empty carries: value must force full, got %q", r.mode)
	}
	if !strings.Contains(strings.ToLower(errb), "carries") {
		t.Fatalf("a malformed declaration must be diagnosed, got %q", errb)
	}

	other := w227NpmFiles()
	other["e2e/badline.mjs"] = "// carries components/*\nconsole.log('bad');\n"
	dir2, _ := w227Repo(t, other)
	w227Change(t, dir2, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r2, errb2 := w227Run(t, dir2)
	if r2.mode != "full" {
		t.Fatalf("a carries line the engine cannot parse must force full, got %q", r2.mode)
	}
	if !strings.Contains(strings.ToLower(errb2), "carries") {
		t.Fatalf("the unparseable carries line must be diagnosed, got %q", errb2)
	}
	fmt.Println("PASS S06")
}

// TestW227CarriesS07Exemptions: a changed suite selects itself and keeps the
// tier scoped; documentation paths and exclude= matches never force full.
func TestW227CarriesS07Exemptions(t *testing.T) {
	dir, _ := w227Repo(t, w227NpmFiles())
	w227Change(t, dir, "e2e/panel-a.mjs", "// carries: components/Polyforge*\nconsole.log('a2');\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("a changed suite must select itself and keep the tier scoped, got %q", r.mode)
	}
	w227RequireCmd(t, r, "node e2e/panel-a.mjs")

	files := w227NpmFiles()
	files["docs/foo.md"] = "old\n"
	files["e2e/themes.mjs"] = "console.log('themes');\n"
	files[".polyforge/verify-scope.conf"] = "stack=npm\nexclude=themes.mjs\n"
	dir2, _ := w227Repo(t, files)
	w227Change(t, dir2, "docs/foo.md", "new\n")
	w227Change(t, dir2, "e2e/themes.mjs", "console.log('themes2');\n")
	r2, errb2 := w227Run(t, dir2)
	if r2.mode != "scoped" {
		t.Fatalf("a documentation path and an exclude= suite must not force full, got %q (%s)", r2.mode, errb2)
	}
	fmt.Println("PASS S07")
}

// TestW227CarriesS08Helpers: a changed helper selects every importer
// transitively; an unresolvable import graph forces full.
func TestW227CarriesS08Helpers(t *testing.T) {
	files := map[string]string{
		"package.json":     "{\n  \"name\": \"w227-helper\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"e2e/timeouts.mjs": "export const t = 1;\n",
		"e2e/a.mjs":        "// carries: app/*\nimport { t } from './timeouts.mjs';\nconsole.log(t);\n",
		"e2e/b.mjs":        "// carries: app/*\nimport './a.mjs';\nconsole.log('b');\n",
		"e2e/c.mjs":        "// carries: app/*\nimport './b.mjs';\nconsole.log('c');\n",
		"app/one.ts":       "export const one = 1;\n",
	}
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "e2e/timeouts.mjs", "export const t = 2;\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("a changed helper selects its importers instead of forcing full, got %q", r.mode)
	}
	for _, want := range []string{"node e2e/a.mjs", "node e2e/b.mjs", "node e2e/c.mjs"} {
		w227RequireCmd(t, r, want)
	}

	broken := w227NpmFiles()
	broken["e2e/d.mjs"] = "// carries: components/Polyforge*\nimport './missing.mjs';\nconsole.log('d');\n"
	dir2, _ := w227Repo(t, broken)
	w227Change(t, dir2, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r2, errb2 := w227Run(t, dir2)
	if r2.mode != "full" {
		t.Fatalf("an unresolvable import graph must force full, got %q", r2.mode)
	}
	if strings.TrimSpace(errb2) == "" {
		t.Fatalf("an unresolvable import graph must be diagnosed on stderr")
	}
	fmt.Println("PASS S08")
}

// TestW227CarriesS09NoTextSelection: a suite that only mentions a changed path
// in text is not selected; with no declaration the repository goes full.
func TestW227CarriesS09NoTextSelection(t *testing.T) {
	dir, _ := w227Repo(t, map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-text\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"e2e/textonly.mjs":              "console.log('components/PolyforgePanel.tsx');\n",
	})
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "full" {
		t.Fatalf("a textual reference without a carries header must force full, got %q", r.mode)
	}
	if !strings.Contains(errb, "e2e/textonly.mjs") {
		t.Fatalf("the text-only suite must be reported undeclared, got %q", errb)
	}
	fmt.Println("PASS S09")
}

// TestW227CarriesS10Runner: e2e_runner= joins the selected suites into exactly
// one command in a stable order; without it, one command per suite.
func TestW227CarriesS10Runner(t *testing.T) {
	files := w227RunnerFiles()
	files[".polyforge/verify-scope.conf"] = "stack=npm\ne2e_runner=npm run test:e2e:all --\n"
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("the selected suites cover the diff, so the tier stays scoped, got %q", r.mode)
	}
	var joined []string
	for _, c := range r.commands {
		if strings.HasPrefix(c, "npm run test:e2e:all --") {
			joined = append(joined, c)
		}
	}
	if len(joined) != 1 {
		t.Fatalf("a declared runner must join the selection into exactly one command, got %q", joined)
	}
	last := -1
	for _, suite := range []string{"e2e/a.mjs", "e2e/b.mjs", "e2e/c.mjs"} {
		idx := strings.Index(joined[0], suite)
		if idx < 0 {
			t.Fatalf("the runner command %q must append %q", joined[0], suite)
		}
		if idx <= last {
			t.Fatalf("selected suites must append in a stable order: %q", joined[0])
		}
		last = idx
	}

	plain, _ := w227Repo(t, w227RunnerFiles())
	w227Change(t, plain, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	pr := w227Scoped(t, plain)
	for _, suite := range []string{"e2e/a.mjs", "e2e/b.mjs", "e2e/c.mjs"} {
		w227RequireCmd(t, pr, "node "+suite)
	}
	if strings.Contains(pr.joined(), "test:e2e:all") {
		t.Fatalf("no e2e_runner= means one command per suite, got:\n%s", pr.joined())
	}
	fmt.Println("PASS S10")
}

// TestW227CarriesS11RunnerTarget: the e2e_runner= target is not a suite; it is
// never selected by text and never appears in the full tail.
func TestW227CarriesS11RunnerTarget(t *testing.T) {
	files := w227RunnerFiles()
	files[".polyforge/verify-scope.conf"] = "stack=npm\ne2e_runner=npm run test:e2e:all --\n"
	dir, _ := w227Repo(t, files)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("the runner target must not leave the repository undeclared, got %q (%s)", r.mode, errb)
	}
	w227RequireNoCmd(t, r, "node e2e/all.mjs")
	if !strings.Contains(r.joined(), "npm run test:e2e:all -- e2e/a.mjs") {
		t.Fatalf("the runner command must select e2e/a.mjs:\n%s", r.joined())
	}
	if got := w227Echo(t, dir, "e2e_runner"); got != "npm run test:e2e:all --" {
		t.Fatalf("the JSON must echo e2e_runner, got %v", got)
	}
	full, _ := w227Run(t, dir, "--full")
	w227RequireNoCmd(t, full, "node e2e/all.mjs")
	fmt.Println("PASS S11")
}

// TestW227CarriesS12Map: e2e_map= replaces a suite's declaration outright (a
// path only the replaced header declares forces full: never merged); a
// mapping to a suite that does not exist forces full.
func TestW227CarriesS12Map(t *testing.T) {
	dir, _ := w227Repo(t, map[string]string{
		"package.json":                 "{\n  \"name\": \"w227-map\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/a/one.ts":          "export const one = 1;\n",
		"components/b/two.ts":          "export const two = 1;\n",
		"e2e/that.mjs":                 "// carries: components/a*\nconsole.log('that');\n",
		".polyforge/verify-scope.conf": "stack=npm\ne2e_map=components/b*=e2e/that.mjs\n",
	})
	w227Change(t, dir, "components/b/two.ts", "export const two = 2;\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("an e2e_map declaration covers the diff, so the tier stays scoped, got %q", r.mode)
	}
	w227RequireCmd(t, r, "node e2e/that.mjs")

	// replaced, not merged: the map replaces the suite's
	// header, so a path only the replaced header declares is uncovered
	dirR, _ := w227Repo(t, map[string]string{
		"package.json":                 "{\n  \"name\": \"w227-map-replaced\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/a/one.ts":          "export const one = 1;\n",
		"components/b/two.ts":          "export const two = 1;\n",
		"e2e/that.mjs":                 "// carries: components/a*\nconsole.log('that');\n",
		".polyforge/verify-scope.conf": "stack=npm\ne2e_map=components/b*=e2e/that.mjs\n",
	})
	w227Change(t, dirR, "components/a/one.ts", "export const one = 2;\n")
	rR, errbR := w227Run(t, dirR)
	if rR.mode != "full" {
		t.Fatalf("e2e_map replaces the suite's header (never merges with it): a path only the replaced header declares must force full, got %q", rR.mode)
	}
	if !strings.Contains(errbR, "changed path components/a/one.ts is covered by no carries: declaration") {
		t.Fatalf("the uncovered path must be named, got %q", errbR)
	}

	dir2, _ := w227Repo(t, map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-map2\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"e2e/that.mjs":                  "// carries: components/a*\nconsole.log('that');\n",
		".polyforge/verify-scope.conf":  "stack=npm\ne2e_map=components/b*=e2e/absent.mjs\n",
	})
	w227Change(t, dir2, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r2, errb2 := w227Run(t, dir2)
	if r2.mode != "full" {
		t.Fatalf("an e2e_map target that does not exist must force full, got %q", r2.mode)
	}
	if !strings.Contains(errb2, "e2e/absent.mjs") {
		t.Fatalf("the absent mapped suite must be named, got %q", errb2)
	}
	fmt.Println("PASS S12")
}

// TestW227CarriesS13SuiteGlob: suite_glob= overrides discovery for the
// repository, and the undeclared/selection rules apply to that set.
func TestW227CarriesS13SuiteGlob(t *testing.T) {
	dir, _ := w227Repo(t, map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-glob\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"checks/accept.mjs":             "// carries: components/Polyforge*\nconsole.log('accept');\n",
		".polyforge/verify-scope.conf":  "stack=npm\nsuite_glob=checks/*.mjs\n",
	})
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r := w227Scoped(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("suite_glob= discovery must find the declaring suite, got %q", r.mode)
	}
	w227RequireCmd(t, r, "node checks/accept.mjs")
	list, ok := w227Echo(t, dir, "suite_glob").([]any)
	if !ok || len(list) != 1 || list[0] != "checks/*.mjs" {
		t.Fatalf("the JSON must echo suite_glob, got %v", list)
	}

	dir2, _ := w227Repo(t, map[string]string{
		"package.json":                  "{\n  \"name\": \"w227-glob2\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"components/PolyforgePanel.tsx": "export const Panel = 1;\n",
		"checks/accept.mjs":             "// carries: components/Polyforge*\nconsole.log('accept');\n",
		"checks/undeclared.mjs":         "console.log('undeclared');\n",
		".polyforge/verify-scope.conf":  "stack=npm\nsuite_glob=checks/*.mjs\n",
	})
	w227Change(t, dir2, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r2, errb2 := w227Run(t, dir2)
	if r2.mode != "full" || !strings.Contains(errb2, "checks/undeclared.mjs") {
		t.Fatalf("an undeclared suite in the suite_glob set must force full and be named, got %q (%s)", r2.mode, errb2)
	}
	fmt.Println("PASS S13")
}

// TestW227CarriesS14ConfFailClosed: every new key's empty or malformed form
// falls back to full with a diagnostic; an unknown key stays ignorable.
func TestW227CarriesS14ConfFailClosed(t *testing.T) {
	cases := []struct{ name, conf, want string }{
		{"empty runner", "stack=npm\ne2e_runner=\n", "e2e_runner"},
		{"empty suite_glob", "stack=npm\nsuite_glob=\n", "suite_glob"},
		{"malformed e2e_map", "stack=npm\ne2e_map=components/b*\n", "e2e_map"},
	}
	for _, tc := range cases {
		files := w227NpmFiles()
		files[".polyforge/verify-scope.conf"] = tc.conf
		dir, _ := w227Repo(t, files)
		w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
		r, errb := w227Run(t, dir)
		if r.mode != "full" {
			t.Fatalf("%s: an empty or malformed key must force full, got %q", tc.name, r.mode)
		}
		if !strings.Contains(errb, tc.want) {
			t.Fatalf("%s: the diagnostic must name the offending line (%s), got %q", tc.name, tc.want, errb)
		}
	}

	unknown := w227NpmFiles()
	unknown[".polyforge/verify-scope.conf"] = "stack=npm\nmystery=1\n"
	dir, _ := w227Repo(t, unknown)
	w227Change(t, dir, "components/PolyforgePanel.tsx", "export const Panel = 2;\n")
	r, errb := w227Run(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("an unknown key stays ignorable, got mode %q", r.mode)
	}
	w227RequireCmd(t, r, "node e2e/panel-a.mjs")
	if !strings.Contains(errb, "mystery") {
		t.Fatalf("an unknown key must be diagnosed and ignored, got %q", errb)
	}
	fmt.Println("PASS S14")
}

// TestW227CarriesS15PythonAnalyzer: the python analyzer keeps the Go
// analyzer's three-tier shape, fails closed on low confidence, and follows
// pytest collection for suite discovery.
func TestW227CarriesS15PythonAnalyzer(t *testing.T) {
	dir, _ := w227Repo(t, w227PyFiles())
	w227Change(t, dir, "app/mod.py", "def value():\n    return 2\n")
	r, errb := w227Run(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("a resolvable python diff must stay scoped, got %q (%s)", r.mode, errb)
	}
	w227RequireCmd(t, r, "python -m compileall -q")
	w227RequireCmd(t, r, "pytest e2e/test_panel.py")
	if !strings.Contains(r.joined(), "pytest tests/unit") {
		t.Fatalf("the scoped tier must carry the heuristically selected unit test:\n%s", r.joined())
	}
	w227RequireNoCmd(t, r, "conftest.py")
	w227RequireNoCmd(t, r, "__init__.py")
	if strings.Contains(errb, "conftest.py") || strings.Contains(errb, "__init__.py") {
		t.Fatalf("pytest collection files are not suites and must not be undeclared: %q", errb)
	}
	j := w227JSON(t, dir)
	if j.Stack != "python" || j.Analyzer == nil || *j.Analyzer != "python" {
		t.Fatalf("python JSON = stack %q analyzer %v, want python/python", j.Stack, j.Analyzer)
	}

	full, _ := w227Run(t, dir, "--full")
	if len(full.commands) == 0 || full.commands[len(full.commands)-1] != "pytest" {
		t.Fatalf("the python full tier must end with plain pytest, got:\n%s", full.joined())
	}

	low, _ := w227Repo(t, w227PyFiles())
	w227Change(t, low, "app/unused/thing.py", "def thing():\n    return 1\n")
	lr, lerr := w227Run(t, low)
	if lr.mode != "full" {
		t.Fatalf("an unresolvable affected test must fall back to full, got %q", lr.mode)
	}
	if strings.TrimSpace(lerr) == "" {
		t.Fatalf("a low-confidence python scope must be diagnosed")
	}
	fmt.Println("PASS S15")
}

// TestW227CarriesS16RustAnalyzer: the rust analyzer keeps the three-tier
// shape, fails closed on low confidence, and counts only top-level
// tests/*.rs as suites.
func TestW227CarriesS16RustAnalyzer(t *testing.T) {
	dir, _ := w227Repo(t, w227RsFiles())
	w227Change(t, dir, "src/app.rs", "pub fn value() -> u32 {\n    2\n}\n")
	r, errb := w227Run(t, dir)
	if r.mode != "scoped" {
		t.Fatalf("a resolvable rust diff must stay scoped, got %q (%s)", r.mode, errb)
	}
	w227RequireCmd(t, r, "cargo build --all-targets")
	w227RequireCmd(t, r, "cargo test --test acceptance")
	w227RequireNoCmd(t, r, "tests/common/mod.rs")
	if strings.Contains(errb, "tests/common/mod.rs") {
		t.Fatalf("tests/common/mod.rs is a helper, not an undeclared suite: %q", errb)
	}
	j := w227JSON(t, dir)
	if j.Stack != "rust" || j.Analyzer == nil || *j.Analyzer != "rust" {
		t.Fatalf("rust JSON = stack %q analyzer %v, want rust/rust", j.Stack, j.Analyzer)
	}

	full, _ := w227Run(t, dir, "--full")
	if len(full.commands) == 0 || full.commands[len(full.commands)-1] != "cargo test" {
		t.Fatalf("the rust full tier must end with plain cargo test, got:\n%s", full.joined())
	}

	low, _ := w227Repo(t, w227RsFiles())
	w227Change(t, low, "src/unused.rs", "pub fn unused() -> u32 {\n    1\n}\n")
	lr, lerr := w227Run(t, low)
	if lr.mode != "full" {
		t.Fatalf("an unresolvable rust affected test must fall back to full, got %q", lr.mode)
	}
	if strings.TrimSpace(lerr) == "" {
		t.Fatalf("a low-confidence rust scope must be diagnosed")
	}
	fmt.Println("PASS S16")
}

// TestW227CarriesS17StackDetection: the new manifests select their analyzers;
// an unpinned multi-manifest repository stays ambiguous -> full with the
// conf's commands only; a stack= pin still resolves it.
func TestW227CarriesS17StackDetection(t *testing.T) {
	pyDir, _ := w227Repo(t, w227PyFiles())
	w227Change(t, pyDir, "app/mod.py", "def value():\n    return 2\n")
	py := w227JSON(t, pyDir)
	if py.Stack != "python" || py.Analyzer == nil || *py.Analyzer != "python" || py.Mode != "scoped" {
		t.Fatalf("pyproject.toml = stack %q analyzer %v mode %q, want python/python/scoped", py.Stack, py.Analyzer, py.Mode)
	}

	req := w227PyFiles()
	delete(req, "pyproject.toml")
	req["requirements.txt"] = "pytest\n"
	reqDir, _ := w227Repo(t, req)
	w227Change(t, reqDir, "app/mod.py", "def value():\n    return 2\n")
	rr := w227JSON(t, reqDir)
	if rr.Stack != "python" || rr.Analyzer == nil || *rr.Analyzer != "python" {
		t.Fatalf("requirements.txt = stack %q analyzer %v, want python/python", rr.Stack, rr.Analyzer)
	}

	rsDir, _ := w227Repo(t, w227RsFiles())
	w227Change(t, rsDir, "src/app.rs", "pub fn value() -> u32 {\n    2\n}\n")
	rs := w227JSON(t, rsDir)
	if rs.Stack != "rust" || rs.Analyzer == nil || *rs.Analyzer != "rust" {
		t.Fatalf("Cargo.toml = stack %q analyzer %v, want rust/rust", rs.Stack, rs.Analyzer)
	}

	amb := w227PyFiles()
	amb["Cargo.toml"] = "[package]\nname = \"w227\"\nversion = \"0.1.0\"\n"
	amb[".polyforge/verify-scope.conf"] = "fast_gate=echo amb-fast\nfull_command=echo amb-full\n"
	ambDir, _ := w227Repo(t, amb)
	w227Change(t, ambDir, "app/mod.py", "def value():\n    return 3\n")
	ar, aerr := w227Run(t, ambDir)
	if ar.mode != "full" {
		t.Fatalf("an unpinned multi-manifest repository stays full, got %q", ar.mode)
	}
	if strings.Join(ar.commands, "\n") != "echo amb-fast\necho amb-full" {
		t.Fatalf("an ambiguous repository reports only its conf's commands, got %q", ar.commands)
	}
	if strings.TrimSpace(aerr) == "" {
		t.Fatalf("an ambiguous stack must be diagnosed")
	}
	aj := w227JSON(t, ambDir)
	if aj.Stack != "ambiguous" || aj.Analyzer != nil {
		t.Fatalf("ambiguous JSON = stack %q analyzer %v, want ambiguous/null", aj.Stack, aj.Analyzer)
	}

	pinned := w227PyFiles()
	pinned["Cargo.toml"] = "[package]\nname = \"w227\"\nversion = \"0.1.0\"\n"
	pinned[".polyforge/verify-scope.conf"] = "stack=python\n"
	pinDir, _ := w227Repo(t, pinned)
	w227Change(t, pinDir, "app/mod.py", "def value():\n    return 4\n")
	pj := w227JSON(t, pinDir)
	if pj.Stack != "python" || pj.Analyzer == nil || *pj.Analyzer != "python" || pj.Mode != "scoped" {
		t.Fatalf("stack=python must resolve the ambiguity, got stack %q analyzer %v mode %q", pj.Stack, pj.Analyzer, pj.Mode)
	}
	fmt.Println("PASS S17")
}
