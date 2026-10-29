package test

// w25_verify_scope_engine_test.go: engine-level contract tests for the
// release-shipped verification-scope reporter, `tools/verify-scope`.
//
// These cover the parts the authored acceptance suite delegates to `code`:
// the frozen Go and npm byte-identical oracles, report grammar edge cases,
// the declarative conf parser's rejection rules, the zero-config spanning
// default and the fail-closed graph shape. Test identifiers carry a W25/w25
// prefix so they cannot collide with the authored acceptance suite.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25Engine' -count=1

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// w25engRoot answers the repository root (this package's cwd is test/).
func w25engRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// w25engEnginePath pins the shipped engine path.
func w25engEnginePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(w25engRoot(t), "tools", "verify-scope")
}

// w25engGit runs one git command in dir with a fixed identity.
func w25engGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=w25eng", "GIT_AUTHOR_EMAIL=w25eng@e",
		"GIT_COMMITTER_NAME=w25eng", "GIT_COMMITTER_EMAIL=w25eng@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w25engWrite writes one file (creating parent dirs).
func w25engWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w25engCopy writes src at dst with the given mode.
func w25engCopy(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dst, mode); err != nil {
		t.Fatal(err)
	}
}

// w25engCommit stages everything and commits, answering the new HEAD.
func w25engCommit(t *testing.T, dir, msg string) string {
	t.Helper()
	w25engGit(t, dir, "add", "-A")
	w25engGit(t, dir, "commit", "-q", "-m", msg)
	return w25engGit(t, dir, "rev-parse", "HEAD")
}

// w25engClone builds a git fixture with the shipped engine (and optionally
// the repo conf and a frozen seed script), commits a base, records
// origin/main, and answers the clone dir and base SHA.
func w25engClone(t *testing.T, files map[string]string, conf string, frozen string) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = t.TempDir()
	w25engGit(t, dir, "init", "-q", "-b", "main")
	w25engGit(t, dir, "config", "user.email", "w25eng@e")
	w25engGit(t, dir, "config", "user.name", "w25eng")
	for rel, body := range files {
		w25engWrite(t, dir, rel, body)
	}
	if conf != "" {
		w25engWrite(t, dir, ".polyforge/verify-scope.conf", conf)
	}
	w25engCopy(t, w25engEnginePath(t), filepath.Join(dir, "tools", "verify-scope"), 0o755)
	if frozen != "" {
		w25engCopy(t, frozen, filepath.Join(dir, ".polyforge", "verify-scope.sh"), 0o755)
	}
	base = w25engCommit(t, dir, "w25eng base")
	w25engGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
	w25engGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return dir, base
}

// w25engRun executes a command in dir and answers stdout, stderr and exit.
func w25engRun(t *testing.T, dir, bin string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// w25engReport is the parsed machine-readable report.
type w25engReport struct {
	mode     string
	paths    []string
	commands []string
}

func (r w25engReport) joined() string { return strings.Join(r.commands, "\n") }

// w25engParse validates the #20 report grammar.
func w25engParse(t *testing.T, out string) w25engReport {
	t.Helper()
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Fatalf("the engine printed no report on stdout")
	}
	lines := strings.Split(trimmed, "\n")
	var r w25engReport
	if !strings.HasPrefix(lines[0], "mode: ") {
		t.Fatalf("report first line %q is not `mode: full|scoped`", lines[0])
	}
	r.mode = strings.TrimPrefix(lines[0], "mode: ")
	if r.mode != "full" && r.mode != "scoped" {
		t.Fatalf("report mode %q is neither full nor scoped", r.mode)
	}
	seenCmd := false
	for _, ln := range lines[1:] {
		switch {
		case strings.HasPrefix(ln, "path: "):
			if seenCmd {
				t.Fatalf("report has a path line %q after a command line", ln)
			}
			r.paths = append(r.paths, strings.TrimPrefix(ln, "path: "))
		case strings.HasPrefix(ln, "command: "):
			seenCmd = true
			r.commands = append(r.commands, strings.TrimPrefix(ln, "command: "))
		default:
			t.Fatalf("report line %q is neither `path:` nor `command:`", ln)
		}
	}
	if !sort.StringsAreSorted(r.paths) {
		t.Fatalf("path lines are not sorted: %v", r.paths)
	}
	for i := 1; i < len(r.paths); i++ {
		if r.paths[i] == r.paths[i-1] {
			t.Fatalf("report repeats the path %q", r.paths[i])
		}
	}
	return r
}

// w25engScope runs the fixture's engine, requires exit 0, and parses.
func w25engScope(t *testing.T, dir string, args ...string) w25engReport {
	t.Helper()
	out, errb, code := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, args...)
	if code != 0 {
		t.Fatalf("tools/verify-scope %v: exit %d (stdout %q, stderr %q)", args, code, out, errb)
	}
	return w25engParse(t, out)
}

// w25engGoModule is a minimal Go module with a leaf and a first-level app.
func w25engGoModule() map[string]string {
	return map[string]string{
		"go.mod":          "module fixture.test/w25eng\n\ngo 1.21\n",
		"leaf/leaf.go":    "package leaf\n\nfunc Leaf() int { return 1 }\n",
		"app/app.go":      "package app\n\nimport \"fixture.test/w25eng/leaf\"\n\nfunc App() int { return leaf.Leaf() }\n",
		"doc/readme.md":   "fixture\n",
		"README.md":       "readme\n",
		"cmd/app/main.go": "package main\n\nfunc main() {}\n",
	}
}

// w25engIeopsConf reads this repository's shipped conf, so the contrast
// catches drift in the declared commands as well as in the engine.
func w25engIeopsConf(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w25engRoot(t), ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	return string(raw)
}

// TestW25EngineReportGrammarAndBase pins the report grammar, the default
// merge-base anchoring, the --base override and the empty-diff shape.
func TestW25EngineReportGrammarAndBase(t *testing.T) {
	dir, base := w25engClone(t, w25engGoModule(), "", "")
	w25engWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25engCommit(t, dir, "change leaf")

	r := w25engScope(t, dir)
	if r.mode != "scoped" || strings.Join(r.paths, ",") != "leaf/leaf.go" {
		t.Fatalf("default base report = mode %q paths %v, want scoped [leaf/leaf.go]", r.mode, r.paths)
	}
	if got := w25engGit(t, dir, "merge-base", "HEAD", "origin/main"); got != base {
		t.Fatalf("fixture merge-base = %s, want %s", got, base)
	}
	if over := w25engScope(t, dir, "--base="+base); strings.Join(over.paths, ",") != "leaf/leaf.go" {
		t.Fatalf("--base=<rev> affected set = %v", over.paths)
	}
	empty := w25engScope(t, dir, "--base", "HEAD")
	if empty.mode != "scoped" || len(empty.paths) != 0 {
		t.Fatalf("empty diff must be scoped with zero paths, got mode %q paths %v", empty.mode, empty.paths)
	}
	if _, _, code := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--bogus"); code != 2 {
		t.Fatalf("an unknown argument must exit 2, got %d", code)
	}
}

// TestW25EngineConfLintAndOpaqueCommands pins the declarative conf boundary:
// the file is never sourced or evaluated, a leading command substitution is a
// lint diagnostic (and is skipped, even under --run), an unknown key is
// diagnosed, an opaque command runs only as a reported command: line, and
// there is no line cap.
func TestW25EngineConfLintAndOpaqueCommands(t *testing.T) {
	// A leading command substitution is a lint diagnostic, not a security
	// boundary: the line is skipped and the marker never appears, even with --run.
	marker := filepath.Join(t.TempDir(), "pwned")
	hook := "stack=go\nfast_gate=echo safe\nfast_gate=$(touch " + marker + ")\n"
	dir, _ := w25engClone(t, w25engGoModule(), hook, "")
	out, errb, code := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--base", "HEAD", "--run")
	if code != 0 {
		t.Fatalf("a lint-diagnosed conf line must not fail the report, got exit %d (%s)", code, errb)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the lint-diagnosed command substitution was executed")
	}
	if !strings.Contains(errb, "lint diagnostic") {
		t.Fatalf("a leading command substitution must be diagnosed as a lint diagnostic, got %q", errb)
	}
	if !strings.Contains(out, "command: echo safe") {
		t.Fatalf("the valid fast gate must still be reported:\n%s", out)
	}

	// An opaque command is reported and runs only under --run, with the same
	// trust as the repo's own Makefile.
	ran := filepath.Join(t.TempDir(), "ran")
	opaque := "stack=go\nfast_gate=touch " + ran + "\n"
	dir2, _ := w25engClone(t, w25engGoModule(), opaque, "")
	if _, errb2, code := w25engRun(t, dir2, filepath.Join(dir2, "tools", "verify-scope"), nil, "--base", "HEAD"); code != 0 {
		t.Fatalf("an opaque command must not run without --run: exit %d (%s)", code, errb2)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatalf("an opaque command ran without --run")
	}
	runOut, runErr, runCode := w25engRun(t, dir2, filepath.Join(dir2, "tools", "verify-scope"), nil, "--base", "HEAD", "--run")
	if runCode != 0 {
		t.Fatalf("an opaque command must run under --run: exit %d (%s)", runCode, runErr)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the opaque command did not run under --run")
	}
	if !strings.Contains(runOut, "command: touch "+ran) {
		t.Fatalf("the opaque command must be visible as a command: line:\n%s", runOut)
	}

	unknown := "stack=go\nfast_gate=echo ok\nmystery=value\n"
	dir3, _ := w25engClone(t, w25engGoModule(), unknown, "")
	_, errb3, _ := w25engRun(t, dir3, filepath.Join(dir3, "tools", "verify-scope"), nil, "--base", "HEAD")
	if !strings.Contains(errb3, "unknown key") {
		t.Fatalf("an unknown conf key must be diagnosed on stderr, got %q", errb3)
	}

	var big strings.Builder
	big.WriteString("stack=go\nfast_gate=echo ok\n")
	for i := 0; i < 300; i++ {
		big.WriteString("# filler\n")
	}
	dir4, _ := w25engClone(t, w25engGoModule(), big.String(), "")
	if r := w25engScope(t, dir4, "--base", "HEAD"); !strings.Contains(r.joined(), "echo ok") {
		t.Fatalf("a valid conf must have no line cap:\n%s", r.joined())
	}
}

// TestW25EngineZeroConfigSpanningIgnoresDocs pins the zero-config defaults:
// two first-level directories force full, prose paths never count.
func TestW25EngineZeroConfigSpanningIgnoresDocs(t *testing.T) {
	files := map[string]string{
		"go.mod":          "module fixture.test/w25eng\n\ngo 1.21\n",
		"cmd/app/main.go": "package main\n\nfunc main() {}\n",
		"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n",
		"doc/readme.md":   "fixture\n",
		"README.md":       "readme\n",
	}
	dir, base := w25engClone(t, files, "", "")
	w25engWrite(t, dir, "cmd/app/main.go", "package main\n\nfunc main() { _ = 1 }\n")
	w25engWrite(t, dir, "internal/b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	w25engCommit(t, dir, "span two first-level dirs")
	if r := w25engScope(t, dir, "--base", base); r.mode != "full" {
		t.Fatalf("two first-level directories must be full, got %q", r.mode)
	}

	docsDir, docsBase := w25engClone(t, files, "", "")
	w25engWrite(t, docsDir, "README.md", "readme v2\n")
	w25engWrite(t, docsDir, "doc/readme.md", "fixture v2\n")
	w25engCommit(t, docsDir, "docs only")
	r := w25engScope(t, docsDir, "--base", docsBase)
	if r.mode != "scoped" {
		t.Fatalf("pure md/docs paths must stay scoped, got %q", r.mode)
	}
	if strings.Join(r.paths, ",") != "README.md,doc/readme.md" {
		t.Fatalf("docs paths must still be reported, got %v", r.paths)
	}
}

// TestW25EngineTestImportClosure pins the W27 fold: a package reached only
// through its _test.go import closure is selected.
func TestW25EngineTestImportClosure(t *testing.T) {
	const mod = "fixture.test/w25eng"
	files := map[string]string{
		"go.mod":                "module " + mod + "\n\ngo 1.21\n",
		"runtime/runtime.go":    "package runtime\n\nfunc Value() int { return 1 }\n",
		"common/bar/bar.go":     "package bar\n\nimport \"" + mod + "/runtime\"\n\nfunc Bar() int { return runtime.Value() }\n",
		"driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
		"driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"" + mod + "/runtime\"\n)\n\nfunc TestDriver(t *testing.T) {}\n",
	}
	dir, base := w25engClone(t, files, "", "")
	w25engWrite(t, dir, "runtime/runtime.go", "package runtime\n\nfunc Value() int { return 2 }\n")
	w25engCommit(t, dir, "change runtime")
	r := w25engScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a single-package change must stay scoped, got %q", r.mode)
	}
	var pkgCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "go test") && strings.Contains(c, "./runtime") {
			pkgCmd = c
		}
	}
	if pkgCmd == "" {
		t.Fatalf("no go test command selects the changed package:\n%s", r.joined())
	}
	for _, want := range []string{"./runtime", "./common/bar", "./driver"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("the test-binary closure must select %q: %s", want, pkgCmd)
		}
	}
}

// TestW25EngineZeroConfigTestPackageStaysOrdinary pins the stack-generic Go
// default: a top-level test package is an ordinary package and must be
// selected through the reverse-dependency graph instead of being dropped,
// even when the tree happens to ship a test/e2e-parallel.sh file — only a
// declared go_harness= changes the behavior.
func TestW25EngineZeroConfigTestPackageStaysOrdinary(t *testing.T) {
	const mod = "fixture.test/w25eng"
	dir, base := w25engClone(t, map[string]string{
		"go.mod":               "module " + mod + "\n\ngo 1.21\n",
		"lib/lib.go":           "package lib\n\nfunc Value() int { return 1 }\n",
		"test/e2e-parallel.sh": "#!/bin/sh\nexit 0\n",
		"test/e2e_test.go":     "package e2e\n\nimport (\n\t\"testing\"\n\n\t_ \"" + mod + "/lib\"\n)\n\nfunc TestAcceptance(t *testing.T) {}\n",
	}, "", "")
	w25engWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
	w25engCommit(t, dir, "change lib")
	r := w25engScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a zero-config repo with a test package must stay scoped, got %q", r.mode)
	}
	if !strings.Contains(r.joined(), "./test") {
		t.Fatalf("the test package reached only through its import must be selected:\n%s", r.joined())
	}
	if strings.Contains(r.joined(), "E2E_RUN") {
		t.Fatalf("a repo without a declared go_harness= must not get a harness command, even when test/e2e-parallel.sh exists:\n%s", r.joined())
	}
}

// TestW25EngineGoHarnessDeclared pins the declarative harness: a conf
// `go_harness=<path>` keeps the harness directory out of the package graph and
// runs the declared harness for the tests whose source references a changed
// path.
func TestW25EngineGoHarnessDeclared(t *testing.T) {
	const mod = "fixture.test/w25eng"
	dir, base := w25engClone(t, map[string]string{
		"go.mod":              "module " + mod + "\n\ngo 1.21\n",
		"lib/lib.go":          "package lib\n\nfunc Value() int { return 1 }\n",
		"test/harness.sh":     "#!/bin/sh\nexit 0\n",
		"test/accept_test.go": "package e2e\n\nimport (\n\t\"testing\"\n\n\t_ \"" + mod + "/lib\"\n)\n\n// text reference to lib/lib.go\nfunc TestAcceptance(t *testing.T) {}\n",
	}, "stack=go\ngo_harness=test/harness.sh\n", "")
	w25engWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
	w25engCommit(t, dir, "change lib")
	r := w25engScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a declared-harness repo must stay scoped, got %q", r.mode)
	}
	if strings.Contains(r.joined(), "./test") {
		t.Fatalf("the declared harness directory must stay out of the package graph:\n%s", r.joined())
	}
	var e2eCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "E2E_RUN") {
			e2eCmd = c
		}
	}
	if e2eCmd == "" || !strings.Contains(e2eCmd, "TestAcceptance") || !strings.Contains(e2eCmd, "sh test/harness.sh") {
		t.Fatalf("the declared harness must run the referencing tests by name (sh test/harness.sh):\n%s", r.joined())
	}
}

// TestW25EngineGoSyntheticFixture runs the committed zero-config Go fixture,
// the synthetic non-workbench tree the acceptance criterion names.
func TestW25EngineGoSyntheticFixture(t *testing.T) {
	root := w25engRoot(t)
	fixture := filepath.Join(root, "test", "testdata", "verify-scope", "go-fixture")
	files := map[string]string{}
	err := filepath.WalkDir(fixture, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(fixture, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, base := w25engClone(t, files, "", "")
	w25engWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25engCommit(t, dir, "change the fixture leaf")
	r := w25engScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("the synthetic Go fixture must stay scoped, got %q", r.mode)
	}
	if !strings.Contains(r.joined(), "./app") || !strings.Contains(r.joined(), "./leaf") {
		t.Fatalf("the fixture's test-binary closure must select app and leaf:\n%s", r.joined())
	}
}

// TestW25EngineNpmGenericDefaults pins the stack-generic npm defaults: a
// package that declares neither lint nor test:terminal must not get commands
// for scripts it does not have, and a package with no TypeScript must not get
// a typecheck gate.
func TestW25EngineNpmGenericDefaults(t *testing.T) {
	files := map[string]string{
		"package.json":  "{\n  \"name\": \"w25eng-npm\",\n  \"scripts\": {\"test\": \"node e2e/run.mjs\"}\n}\n",
		"src/app.ts":    "export const app = 1;\n",
		"e2e/run.mjs":   "console.log('run');\n",
		"e2e/plain.mjs": "// selects on src/app.ts\nconsole.log('plain');\n",
	}
	dir, base := w25engClone(t, files, "", "")
	w25engWrite(t, dir, "src/app.ts", "export const app = 2;\n")
	w25engCommit(t, dir, "change a source file")
	r := w25engScope(t, dir, "--base", base)
	joined := r.joined()
	for _, bad := range []string{"npm run lint", "npm run test:e2e", "npm run test:terminal", "tsc --noEmit"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("a package without that script must not report %q:\n%s", bad, joined)
		}
	}
	if !strings.Contains(joined, "npm test") {
		t.Fatalf("the declared test script must be reported:\n%s", joined)
	}

	w25engWrite(t, dir, "package.json", "{\n  \"name\": \"w25eng-npm\",\n  \"version\": \"2.0.0\"\n}\n")
	w25engCommit(t, dir, "change package.json")
	full := w25engScope(t, dir, "--base", base)
	if full.mode != "full" {
		t.Fatalf("a package.json change must be full, got %q", full.mode)
	}
	for _, c := range full.commands {
		if c == "npm run test:e2e" || c == "npm run test:terminal" || c == "npm run lint" {
			t.Fatalf("the full tier must not report a script the package does not declare: %s", c)
		}
	}
}

// TestW25EngineUnexpectedShapeFallsBackFull pins the fail-closed shape check:
// a malformed record and a well-formed graph that omits the `P.test` records
// the fold needs both degrade to full.
func TestW25EngineUnexpectedShapeFallsBackFull(t *testing.T) {
	cases := []struct {
		name string
		stub string
	}{
		{
			name: "malformed-record",
			stub: "#!/bin/sh\nprintf '%s\\n' 'unexpected go list shape'\nexit 0\n",
		},
		{
			name: "well-formed-without-test-record",
			stub: "#!/bin/sh\n" +
				"printf 'fixture.test/w25eng/leaf|%s/leaf|fixture.test/w25eng/leaf|1|0\\n' \"$PWD\"\n" +
				"printf 'fixture.test/w25eng|%s|fixture.test/w25eng/leaf|0|0\\n' \"$PWD\"\n" +
				"exit 0\n",
		},
		{
			// The old three-field shape is still an unexpected shape now.
			name: "three-field-records-without-test-record",
			stub: "#!/bin/sh\n" +
				"printf 'fixture.test/w25eng/leaf|%s/leaf|fixture.test/w25eng/leaf\\n' \"$PWD\"\n" +
				"printf 'fixture.test/w25eng|%s|fixture.test/w25eng/leaf\\n' \"$PWD\"\n" +
				"exit 0\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, base := w25engClone(t, w25engGoModule(), "", "")
			// Give the changed package a test file the missing .test record
			// would have covered.
			w25engWrite(t, dir, "leaf/leaf_test.go", "package leaf\n\nimport \"testing\"\n\nfunc TestLeaf(t *testing.T) {}\n")
			w25engWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 3 }\n")
			w25engCommit(t, dir, "change leaf with a test file")
			stub := t.TempDir()
			if err := os.WriteFile(filepath.Join(stub, "go"), []byte(c.stub), 0o755); err != nil {
				t.Fatal(err)
			}
			out, _, code := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"),
				[]string{"PATH=" + stub + ":" + os.Getenv("PATH")}, "--base", base)
			if code != 0 {
				t.Fatalf("an uncomputable graph must still print a report, got exit %d", code)
			}
			if r := w25engParse(t, out); r.mode != "full" {
				t.Fatalf("an unexpected go list shape must fail closed to full, got %q", r.mode)
			}
		})
	}
}

// TestW25EngineUnsupportedStackFallsBackFull pins the unsupported-stack full
// report: zero commands, exit 0, a stderr reason.
func TestW25EngineUnsupportedStackFallsBackFull(t *testing.T) {
	dir, _ := w25engClone(t, map[string]string{"Cargo.toml": "[package]\nname = \"w25eng\"\nversion = \"0.1.0\"\n"}, "", "")
	out, errb, code := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil)
	if code != 0 {
		t.Fatalf("an unsupported stack must exit 0, got %d (%s)", code, errb)
	}
	r := w25engParse(t, out)
	if r.mode != "full" || len(r.commands) != 0 {
		t.Fatalf("an unsupported stack must be full with zero commands, got %q %v", r.mode, r.commands)
	}
	if strings.TrimSpace(errb) == "" {
		t.Fatalf("an unsupported stack must explain itself on stderr")
	}
}

// TestW25EngineGoByteContrast runs the frozen seed script and the engine with
// the shipped conf over w20/w27-shaped fixtures and requires byte-identical
// stdout.
func TestW25EngineGoByteContrast(t *testing.T) {
	frozen := filepath.Join(w25engRoot(t), "test", "testdata", "verify-scope", "verify-scope-go.sh")
	if _, err := os.Stat(frozen); err != nil {
		t.Fatalf("the frozen Go seed must exist: %v", err)
	}
	cases := []struct {
		name  string
		files map[string]string
		write map[string]string
	}{
		{
			name: "w20-shaped",
			files: map[string]string{
				"go.mod":                        "module fixture.test/w20\n\ngo 1.21\n",
				"internal/polyforge/foo/foo.go": "package foo\n\nfunc Foo() int { return 1 }\n",
				"internal/common/bar/bar.go":    "package bar\n\nimport \"fixture.test/w20/internal/polyforge/foo\"\n\nfunc Bar() int { return foo.Foo() }\n",
				"cmd/app/main.go":               "package main\n\nimport \"fixture.test/w20/internal/common/bar\"\n\nfunc main() { _ = bar.Bar() }\n",
				"test/e2e_test.go":              "package e2e\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/w20/internal/polyforge/foo\"\n)\n\n// text reference to internal/polyforge/foo/foo.go\nfunc TestTextRef(t *testing.T) {}\n",
				"test/e2e-parallel.sh":          "#!/bin/sh\nexit 0\n",
			},
			write: map[string]string{
				"internal/polyforge/foo/foo.go": "package foo\n\nfunc Foo() int { return 2 }\n",
			},
		},
		{
			name: "w27-shaped",
			files: map[string]string{
				"go.mod":                                   "module fixture.test/w27\n\ngo 1.21\n",
				"internal/polyforge/runtime/runtime.go":    "package runtime\n\nfunc Value() int { return 1 }\n",
				"internal/common/bar/bar.go":               "package bar\n\nimport \"fixture.test/w27/internal/polyforge/runtime\"\n\nfunc Bar() int { return runtime.Value() }\n",
				"internal/polyforge/driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
				"internal/polyforge/driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/w27/internal/polyforge/runtime\"\n)\n\nfunc TestDriverImportsRuntime(t *testing.T) {}\n",
			},
			write: map[string]string{
				"internal/polyforge/runtime/runtime.go": "package runtime\n\nfunc Value() int { return 2 }\n",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, base := w25engClone(t, c.files, w25engIeopsConf(t), frozen)
			for rel, body := range c.write {
				w25engWrite(t, dir, rel, body)
			}
			w25engCommit(t, dir, "contrast change")
			frozenOut, frozenErr, frozenCode := w25engRun(t, dir, filepath.Join(dir, ".polyforge", "verify-scope.sh"), nil, "--base", base)
			if frozenCode != 0 {
				t.Fatalf("frozen seed exited %d: %s", frozenCode, frozenErr)
			}
			engineOut, engineErr, engineCode := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--base", base)
			if engineCode != 0 {
				t.Fatalf("engine exited %d: %s", engineCode, engineErr)
			}
			if frozenOut != engineOut {
				t.Fatalf("engine stdout differs from the frozen seed\n--- frozen ---\n%s--- engine ---\n%s", frozenOut, engineOut)
			}
		})
	}
}

// w25engNpmData answers the npm testdata directory.
func w25engNpmData(t *testing.T) string {
	t.Helper()
	return filepath.Join(w25engRoot(t), "test", "testdata", "verify-scope", "npm")
}

// w25engNpmFiles loads the committed synthetic npm fixture tree, excluding the
// range data and the fixture conf (both passed separately).
func w25engNpmFiles(t *testing.T) map[string]string {
	t.Helper()
	root := w25engNpmData(t)
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "ranges.txt" || rel == "verify-scope.conf" || strings.HasPrefix(rel, "ranges/") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// w25engNpmConf reads the fixture conf carrying the seed repo's declarations.
func w25engNpmConf(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w25engNpmData(t), "verify-scope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// w25engNpmRange is one recorded merge range: a neutral name and the merge
// commit it was materialised from.
type w25engNpmRange struct {
	name string
	sha  string
}

// w25engNpmRanges reads the recorded range list.
func w25engNpmRanges(t *testing.T) []w25engNpmRange {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w25engNpmData(t), "ranges.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []w25engNpmRange
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			t.Fatalf("bad ranges.txt line %q", line)
		}
		out = append(out, w25engNpmRange{parts[0], parts[1]})
	}
	if len(out) != 10 {
		t.Fatalf("ranges.txt must list the ten recorded ranges, got %d", len(out))
	}
	return out
}

// w25engRangeChange is one recorded name-status entry.
type w25engRangeChange struct {
	status string
	old    string
	path   string
}

// w25engRangeChanges parses a range's name-status list.
func w25engRangeChanges(t *testing.T, dir string) []w25engRangeChange {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "name-status.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var out []w25engRangeChange
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			t.Fatalf("bad name-status line %q", line)
		}
		c := w25engRangeChange{status: parts[0], path: parts[1]}
		if strings.HasPrefix(c.status, "R") || strings.HasPrefix(c.status, "C") {
			if len(parts) < 3 {
				t.Fatalf("bad rename name-status line %q", line)
			}
			c.old, c.path = parts[1], parts[2]
		}
		out = append(out, c)
	}
	return out
}

// w25engRangeFiles loads a range's materialised HEAD tree (package.json and
// e2e/*.mjs; name-status.txt is metadata).
func w25engRangeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "name-status.txt" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// w25engNpmRangeFixture rebuilds a recorded range as a git fixture: the base
// commit carries the pre-image, HEAD carries the materialised range tree plus a
// placeholder for every changed path the selection does not read.
func w25engNpmRangeFixture(t *testing.T, name string) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := filepath.Join(w25engNpmData(t), "ranges", name)
	changes := w25engRangeChanges(t, root)
	files := w25engRangeFiles(t, root)
	statuses := map[string]string{}
	for _, c := range changes {
		statuses[c.path] = c.status
	}
	placeholder := func(rel string) string { return "// recorded range " + name + " " + rel + "\n" }
	headPlaceholder := func(rel string) string { return "// recorded range head " + name + " " + rel + "\n" }

	dir = t.TempDir()
	w25engGit(t, dir, "init", "-q", "-b", "main")
	w25engGit(t, dir, "config", "user.email", "w25eng@e")
	w25engGit(t, dir, "config", "user.name", "w25eng")

	// Base: unchanged files carry their real content; a placeholder stands in
	// for every changed path that exists as a pre-image.
	for rel, body := range files {
		st := statuses[rel]
		switch {
		case strings.HasPrefix(st, "A"), strings.HasPrefix(st, "R"), strings.HasPrefix(st, "C"):
			continue
		case strings.HasPrefix(st, "M"):
			w25engWrite(t, dir, rel, placeholder(rel))
		default:
			w25engWrite(t, dir, rel, body)
		}
	}
	for _, c := range changes {
		if strings.HasPrefix(c.status, "A") {
			continue
		}
		if c.old != "" {
			w25engWrite(t, dir, c.old, placeholder(c.old))
			continue
		}
		if _, ok := files[c.path]; !ok {
			w25engWrite(t, dir, c.path, placeholder(c.path))
		}
	}
	base = w25engCommit(t, dir, "range base")
	w25engGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
	w25engGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	// HEAD: the materialised content; deletions and rename sources disappear.
	for _, c := range changes {
		if strings.HasPrefix(c.status, "D") {
			_ = os.Remove(filepath.Join(dir, c.path))
		}
		if c.old != "" {
			_ = os.Remove(filepath.Join(dir, c.old))
		}
	}
	for rel, body := range files {
		w25engWrite(t, dir, rel, body)
	}
	for _, c := range changes {
		if _, ok := files[c.path]; ok {
			continue
		}
		if strings.HasPrefix(c.status, "D") || c.old != "" {
			continue
		}
		w25engWrite(t, dir, c.path, headPlaceholder(c.path))
	}
	w25engCommit(t, dir, "range head")
	return dir, base
}

// TestW25NpmAnalyzerSelectsReferencedSuites pins the npm scoped selection on
// the committed fixture.
func TestW25NpmAnalyzerSelectsReferencedSuites(t *testing.T) {
	frozen := filepath.Join(w25engRoot(t), "test", "testdata", "verify-scope", "verify-scope-npm.sh")
	dir, base := w25engClone(t, w25engNpmFiles(t), w25engNpmConf(t), frozen)
	w25engWrite(t, dir, "src/app.ts", "export const app = 2;\n")
	w25engCommit(t, dir, "change the referenced source")
	r := w25engScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a source-only npm change must stay scoped, got %q", r.mode)
	}
	joined := r.joined()
	if !strings.Contains(joined, "node e2e/plain.mjs") {
		t.Fatalf("the suite referencing the changed file must be selected:\n%s", joined)
	}
	for _, bad := range []string{"node e2e/browser.mjs", "node e2e/helper.mjs", "node e2e/run.mjs", "node e2e/themes.mjs"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("scoped npm selection must not select %q:\n%s", bad, joined)
		}
	}
	w25engWrite(t, dir, "package.json", "{\n  \"name\": \"w25-npm-fixture\",\n  \"version\": \"2.0.0\"\n}\n")
	w25engCommit(t, dir, "change package.json")
	if full := w25engScope(t, dir, "--base", base); full.mode != "full" {
		t.Fatalf("a package.json change must be full, got %q", full.mode)
	}
}

// TestW25NpmChangedSuiteMatchesFrozenSeed pins the exclude= boundary: a
// changed standalone suite that references its own path is selected in the
// scoped tier by both the engine and the frozen seed (exclude= suppresses the
// full standalone pass only), so the old seed/engine divergence is closed.
func TestW25NpmChangedSuiteMatchesFrozenSeed(t *testing.T) {
	frozen := filepath.Join(w25engRoot(t), "test", "testdata", "verify-scope", "verify-scope-npm.sh")
	dir, base := w25engClone(t, w25engNpmFiles(t), w25engNpmConf(t), frozen)
	w25engWrite(t, dir, "e2e/themes.mjs", "// changed e2e/themes.mjs\nconsole.log('themes');\n")
	w25engCommit(t, dir, "change a standalone suite")
	frozenOut, frozenErr, frozenCode := w25engRun(t, dir, filepath.Join(dir, ".polyforge", "verify-scope.sh"), nil, "--base", base)
	if frozenCode != 0 {
		t.Fatalf("frozen seed exited %d: %s", frozenCode, frozenErr)
	}
	engineOut, engineErr, engineCode := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--base", base)
	if engineCode != 0 {
		t.Fatalf("engine exited %d: %s", engineCode, engineErr)
	}
	if frozenOut != engineOut {
		t.Fatalf("engine stdout differs from the frozen seed on a changed suite\n--- frozen ---\n%s--- engine ---\n%s", frozenOut, engineOut)
	}
	if !strings.Contains(engineOut, "node e2e/themes.mjs") {
		t.Fatalf("the changed standalone suite must be selected:\n%s", engineOut)
	}
}

// TestW25NpmByteContrast replays the ten recorded merge ranges against the
// frozen seed script and requires byte-identical stdout.
func TestW25NpmByteContrast(t *testing.T) {
	frozen := filepath.Join(w25engRoot(t), "test", "testdata", "verify-scope", "verify-scope-npm.sh")
	if _, err := os.Stat(frozen); err != nil {
		t.Fatalf("the frozen npm seed must exist: %v", err)
	}
	conf := w25engNpmConf(t)
	scopedSeen, fullSeen := false, false
	for _, rng := range w25engNpmRanges(t) {
		t.Run(rng.name, func(t *testing.T) {
			dir, base := w25engNpmRangeFixture(t, rng.name)
			w25engWrite(t, dir, ".polyforge/verify-scope.conf", conf)
			w25engCopy(t, frozen, filepath.Join(dir, ".polyforge", "verify-scope.sh"), 0o755)
			w25engCopy(t, w25engEnginePath(t), filepath.Join(dir, "tools", "verify-scope"), 0o755)
			frozenOut, frozenErr, frozenCode := w25engRun(t, dir, filepath.Join(dir, ".polyforge", "verify-scope.sh"), nil, "--base", base)
			if frozenCode != 0 {
				t.Fatalf("frozen seed exited %d: %s", frozenCode, frozenErr)
			}
			engineOut, engineErr, engineCode := w25engRun(t, dir, filepath.Join(dir, "tools", "verify-scope"), nil, "--base", base)
			if engineCode != 0 {
				t.Fatalf("engine exited %d: %s", engineCode, engineErr)
			}
			if frozenOut != engineOut {
				t.Fatalf("engine stdout differs from the frozen seed\n--- frozen ---\n%s--- engine ---\n%s", frozenOut, engineOut)
			}
			r := w25engParse(t, engineOut)
			scopedSeen = scopedSeen || r.mode == "scoped"
			fullSeen = fullSeen || r.mode == "full"
		})
	}
	if !scopedSeen || !fullSeen {
		t.Fatalf("the recorded ranges must exercise both scoped (%t) and full (%t)", scopedSeen, fullSeen)
	}
}
