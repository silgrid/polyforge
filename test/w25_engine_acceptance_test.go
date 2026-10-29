package test

// w25_engine_acceptance_test.go: the end-to-end acceptance suite
// for the release-shipped verification-scope engine, `tools/verify-scope`.
//
// Authored from artifacts/spec.md's `## Acceptance criteria` and `####
// Scenario` blocks BEFORE the engine exists (e2e-author), driving it only
// through its documented CLI surface — the target clone as working directory,
// `--base`, `--run` — plus the shipped text. No implementation internals,
// no testdata path and no internal repository identifier are referenced.
//
// Every scenario is an assertion-level RED on the pre-implementation tree:
// the engine and the conf do not exist yet, so each one fails on a missing
// file or a report assertion, never on a build error.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW25E2E' -count=1 -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// w25e2eRoot answers the repository root (this package's cwd is test/).
func w25e2eRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// w25e2eEnginePath pins the release-shipped engine and answers its path. A
// missing or non-executable file is the baseline red.
func w25e2eEnginePath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(w25e2eRoot(t), "tools", "verify-scope")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("tools/verify-scope must ship in the release tree (the pre-implementation tree has no engine): %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("tools/verify-scope must be executable: mode %v", info.Mode())
	}
	return p
}

// w25e2eGit runs one git command in dir with a fixed identity.
func w25e2eGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=w25e2e", "GIT_AUTHOR_EMAIL=w25e2e@e",
		"GIT_COMMITTER_NAME=w25e2e", "GIT_COMMITTER_EMAIL=w25e2e@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w25e2eWrite writes one file (creating parent dirs).
func w25e2eWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w25e2eCommit stages everything and commits, answering the new HEAD.
func w25e2eCommit(t *testing.T, dir, msg string) string {
	t.Helper()
	w25e2eGit(t, dir, "add", "-A")
	w25e2eGit(t, dir, "commit", "-q", "-m", msg)
	return w25e2eGit(t, dir, "rev-parse", "HEAD")
}

// w25e2eFixture builds a clone whose default base is a recorded origin/main,
// writes the given files, commits a base, and answers the clone dir and base.
func w25e2eFixture(t *testing.T, files map[string]string) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required by the e2e acceptance suite: %v", err)
	}
	dir = t.TempDir()
	w25e2eGit(t, dir, "init", "-q", "-b", "main")
	w25e2eGit(t, dir, "config", "user.email", "w25e2e@e")
	w25e2eGit(t, dir, "config", "user.name", "w25e2e")
	for rel, body := range files {
		w25e2eWrite(t, dir, rel, body)
	}
	base = w25e2eCommit(t, dir, "w25e2e base")
	w25e2eGit(t, dir, "update-ref", "refs/remotes/origin/main", base)
	w25e2eGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return dir, base
}

// w25e2eRun invokes the release engine with cwd at the target clone.
func w25e2eRun(t *testing.T, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(w25e2eEnginePath(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run tools/verify-scope %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// w25e2eReport is the parsed machine-readable report.
type w25e2eReport struct {
	mode     string
	paths    []string
	commands []string
}

func (r w25e2eReport) joined() string { return strings.Join(r.commands, "\n") }

// w25e2eParse validates the #20 report grammar: exactly one leading
// `mode: full|scoped`, then sorted unique `path:` lines, then `command:` lines,
// and nothing else.
func w25e2eParse(t *testing.T, out string) w25e2eReport {
	t.Helper()
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Fatalf("tools/verify-scope printed no report on stdout")
	}
	lines := strings.Split(trimmed, "\n")
	var r w25e2eReport
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
				t.Fatalf("report has a path line %q after a command line (grammar: mode, paths, commands)", ln)
			}
			r.paths = append(r.paths, strings.TrimPrefix(ln, "path: "))
		case strings.HasPrefix(ln, "command: "):
			seenCmd = true
			r.commands = append(r.commands, strings.TrimPrefix(ln, "command: "))
		default:
			t.Fatalf("report line %q is neither `path:` nor `command:` (stdout carries only the grammar)", ln)
		}
	}
	if !sort.StringsAreSorted(r.paths) {
		t.Fatalf("path lines are not sorted lexicographically: %v", r.paths)
	}
	for i := 1; i < len(r.paths); i++ {
		if r.paths[i] == r.paths[i-1] {
			t.Fatalf("report repeats the path %q (paths must be unique)", r.paths[i])
		}
	}
	return r
}

// w25e2eScope runs the engine, requires exit 0, and parses its report.
func w25e2eScope(t *testing.T, dir string, args ...string) w25e2eReport {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, args...)
	if code != 0 {
		t.Fatalf("tools/verify-scope %v: exit %d (stdout %q, stderr %q)", args, code, out, errb)
	}
	return w25e2eParse(t, out)
}

// w25e2eGoModule is a minimal compilable Go module (a leaf plus its importer).
func w25e2eGoModule(mod string) map[string]string {
	return map[string]string{
		"go.mod":        "module " + mod + "\n\ngo 1.21\n",
		"leaf/leaf.go":  "package leaf\n\nfunc Leaf() int { return 1 }\n",
		"app/app.go":    "package app\n\nimport \"" + mod + "/leaf\"\n\nfunc App() int { return leaf.Leaf() }\n",
		"doc/readme.md": "fixture\n",
	}
}

// w25e2eNpmFixture is a minimal npm tree: a referenced source file, one helper
// module imported by another suite, a Playwright suite, and a plain suite that
// names the changed path.
func w25e2eNpmFixture() map[string]string {
	return map[string]string{
		"package.json":    "{\n  \"name\": \"w25e2e-fixture\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test\": \"node e2e/run.mjs\"}\n}\n",
		"src/app.ts":      "export const app = 1;\n",
		"e2e/run.mjs":     "import './helper.mjs';\nconsole.log('run');\n",
		"e2e/helper.mjs":  "export const helper = 1;\n",
		"e2e/plain.mjs":   "// selects on src/app.ts\nconsole.log('plain');\n",
		"e2e/browser.mjs": "import { chromium } from 'playwright';\nconsole.log('browser');\n",
	}
}

// ---------------------------------------------------------------------------
// scenarios
// ---------------------------------------------------------------------------

// TestW25E2EEngineShipsAndReportContract is scenario
// "The release tree ships an executable engine that preserves the #20 report
// contract, base anchoring and --run".
func TestW25E2EEngineShipsAndReportContract(t *testing.T) {
	w25e2eEnginePath(t) // exists + executable
	dir, base := w25e2eFixture(t, w25e2eGoModule("fixture.test/w25e2e"))
	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	head := w25e2eCommit(t, dir, "change leaf")

	// Default base is merge-base HEAD origin/main, never the capsule baseline.
	r := w25e2eScope(t, dir)
	if r.mode != "scoped" || strings.Join(r.paths, ",") != "leaf/leaf.go" {
		t.Fatalf("default base report = mode %q paths %v, want scoped [leaf/leaf.go]", r.mode, r.paths)
	}
	if got := w25e2eGit(t, dir, "merge-base", "HEAD", "origin/main"); got != base {
		t.Fatalf("fixture merge-base = %s, want the base commit %s", got, base)
	}
	over := w25e2eScope(t, dir, "--base", base)
	if strings.Join(over.paths, ",") != "leaf/leaf.go" {
		t.Fatalf("`--base %s` affected set = %v, want [leaf/leaf.go]", base, over.paths)
	}
	w25e2eWrite(t, dir, "capsule.json", `{"repos":{"fixture":"`+strings.Repeat("0", 40)+`"}}`)
	if again := w25e2eScope(t, dir); strings.Join(again.paths, ",") != "leaf/leaf.go" {
		t.Fatalf("a capsule.json baseline changed the affected set: %v", again.paths)
	}
	empty := w25e2eScope(t, dir, "--base", head)
	if empty.mode != "scoped" || len(empty.paths) != 0 {
		t.Fatalf("`--base HEAD` (empty diff) must be scoped with zero paths, got mode %q paths %v", empty.mode, empty.paths)
	}

	// --run executes exactly the reported commands, in order, stopping at the
	// first non-zero. The toolchain is stubbed so nothing is really built.
	stub := t.TempDir()
	logPath := filepath.Join(stub, "calls.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$(basename \"$0\")\" >> \"$W25E2E_LOG\"\n" +
		"if [ -n \"${W25E2E_FAIL_ARG:-}\" ] && printf '%s' \"$*\" | grep -q -- \"$W25E2E_FAIL_ARG\"; then exit 1; fi\nexit 0\n"
	for _, tool := range []string{"go", "gofmt", "python3"} {
		p := filepath.Join(stub, tool)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + stub + ":" + os.Getenv("PATH"), "W25E2E_LOG=" + logPath}
	out, _, code := w25e2eRun(t, dir, env, "--base", "HEAD")
	if code != 0 {
		t.Fatalf("stubbed report run: exit %d", code)
	}
	stubbed := w25e2eParse(t, out)
	if len(stubbed.commands) == 0 {
		t.Fatalf("an empty diff must still report the fast gates")
	}
	if _, _, code := w25e2eRun(t, dir, env, "--base", "HEAD", "--run"); code != 0 {
		t.Fatalf("`--run` over a green command list must exit 0, got %d", code)
	}
	want := make([]string, 0, len(stubbed.commands))
	for _, c := range stubbed.commands {
		switch {
		case strings.Contains(c, "gofmt"):
			want = append(want, "gofmt")
		case strings.Contains(c, "python3"):
			want = append(want, "python3")
		case strings.Contains(c, "go "):
			want = append(want, "go")
		}
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logged := strings.Fields(string(raw))
	if len(logged) < len(want) || strings.Join(logged[len(logged)-len(want):], ",") != strings.Join(want, ",") {
		t.Fatalf("`--run` executed %v, want the reported commands' tools in order %v", logged, want)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env = append(env, "W25E2E_FAIL_ARG=build")
	if _, _, code := w25e2eRun(t, dir, env, "--base", "HEAD", "--run"); code == 0 {
		t.Fatalf("`--run` must exit non-zero when a reported command fails")
	}
	failed, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(failed), "gofmt") || strings.Contains(string(failed), "python3") {
		t.Fatalf("`--run` continued past the first failure:\n%s", failed)
	}
}

// TestW25E2EStackDetection is scenario
// "The engine selects Go or npm from the manifests and degrades to full on
// unsupported or ambiguous stacks".
func TestW25E2EStackDetection(t *testing.T) {
	goDir, _ := w25e2eFixture(t, w25e2eGoModule("fixture.test/w25e2e"))
	goReport := w25e2eScope(t, goDir)
	if !strings.Contains(goReport.joined(), "go build ./...") || strings.Contains(goReport.joined(), "npm run lint") {
		t.Fatalf("a go.mod-only repo must use the Go analyzer, got:\n%s", goReport.joined())
	}

	npmDir, _ := w25e2eFixture(t, w25e2eNpmFixture())
	npmReport := w25e2eScope(t, npmDir)
	if !strings.Contains(npmReport.joined(), "npm run lint") || strings.Contains(npmReport.joined(), "go build ./...") {
		t.Fatalf("a package.json-only repo must use the npm analyzer, got:\n%s", npmReport.joined())
	}

	cargoDir, _ := w25e2eFixture(t, map[string]string{"Cargo.toml": "[package]\nname = \"w25e2e\"\nversion = \"0.1.0\"\n"})
	out, errb, code := w25e2eRun(t, cargoDir, nil)
	if code != 0 {
		t.Fatalf("an unsupported stack must exit 0 with a report, got %d (%s)", code, errb)
	}
	cargo := w25e2eParse(t, out)
	if cargo.mode != "full" || len(cargo.commands) != 0 {
		t.Fatalf("an unsupported stack must be mode full with zero commands, got mode %q commands %v", cargo.mode, cargo.commands)
	}
	if strings.TrimSpace(errb) == "" {
		t.Fatalf("an unsupported stack must explain itself on stderr")
	}

	bothDir, _ := w25e2eFixture(t, map[string]string{
		"go.mod":       "module fixture.test/w25e2e\n\ngo 1.21\n",
		"package.json": "{\n  \"name\": \"w25e2e\"\n}\n",
	})
	out, errb, code = w25e2eRun(t, bothDir, nil)
	if code != 0 {
		t.Fatalf("an ambiguous stack must exit 0 with a report, got %d (%s)", code, errb)
	}
	both := w25e2eParse(t, out)
	if both.mode != "full" {
		t.Fatalf("multiple manifests without a conf must be mode full, got %q", both.mode)
	}
	if strings.TrimSpace(errb) == "" {
		t.Fatalf("an ambiguous stack must explain itself on stderr")
	}

	// A `stack=` pin in the conf resolves the ambiguity.
	pinnedDir, _ := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w25e2e\n\ngo 1.21\n",
		"package.json":                 "{\n  \"name\": \"w25e2e\"\n}\n",
		".polyforge/verify-scope.conf": "stack=go\n",
	})
	pinned := w25e2eScope(t, pinnedDir)
	if !strings.Contains(pinned.joined(), "go build ./...") || strings.Contains(pinned.joined(), "npm run lint") {
		t.Fatalf("stack=go must resolve the multi-manifest ambiguity to Go, got:\n%s", pinned.joined())
	}
}

// TestW25E2EGoScopedSelection is scenario
// "The Go analyzer selects changed packages, non-test reverse dependencies and
// W27 test-only importers".
func TestW25E2EGoScopedSelection(t *testing.T) {
	const mod = "fixture.test/w25e2e"
	dir, base := w25e2eFixture(t, map[string]string{
		"go.mod":                "module " + mod + "\n\ngo 1.21\n",
		"runtime/runtime.go":    "package runtime\n\nfunc Value() int { return 1 }\n",
		"common/bar/bar.go":     "package bar\n\nimport \"" + mod + "/runtime\"\n\nfunc Bar() int { return runtime.Value() }\n",
		"driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
		"driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"" + mod + "/runtime\"\n)\n\nfunc TestDriver(t *testing.T) {}\n",
	})
	w25e2eWrite(t, dir, "runtime/runtime.go", "package runtime\n\nfunc Value() int { return 2 }\n")
	w25e2eCommit(t, dir, "change runtime only")

	r := w25e2eScope(t, dir, "--base", base)
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
		t.Fatalf("no `go test` command selects the changed package:\n%s", r.joined())
	}
	for _, want := range []string{"./runtime", "./common/bar", "./driver"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("the test-binary closure must select %q (driver_test -> runtime): %s", want, pkgCmd)
		}
	}
	if strings.Contains(pkgCmd, "plain") {
		t.Fatalf("unexpected selection in %s", pkgCmd)
	}
}

// TestW25E2ENpmScopedSelection is scenario
// "The npm analyzer selects referenced non-browser e2e suites and excludes
// imported helpers".
func TestW25E2ENpmScopedSelection(t *testing.T) {
	dir, base := w25e2eFixture(t, w25e2eNpmFixture())
	w25e2eWrite(t, dir, "src/app.ts", "export const app = 2;\n")
	w25e2eCommit(t, dir, "change src/app.ts")

	r := w25e2eScope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a source-only npm change must stay scoped, got %q", r.mode)
	}
	joined := r.joined()
	if !strings.Contains(joined, "node e2e/plain.mjs") {
		t.Fatalf("the suite referencing the changed file must be selected:\n%s", joined)
	}
	for _, bad := range []string{"node e2e/browser.mjs", "node e2e/helper.mjs", "node e2e/run.mjs"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("scoped npm selection must not select %q:\n%s", bad, joined)
		}
	}
}

// TestW25E2EFailClosed is scenario
// "Zero-config spanning, npm config changes and uncomputable scopes degrade to
// full".
func TestW25E2EFailClosed(t *testing.T) {
	// Two first-level directories span -> full.
	spanDir, spanBase := w25e2eFixture(t, map[string]string{
		"go.mod":          "module fixture.test/w25e2e\n\ngo 1.21\n",
		"cmd/app/main.go": "package main\n\nfunc main() {}\n",
		"internal/b/b.go": "package b\n\nfunc B() int { return 1 }\n",
	})
	w25e2eWrite(t, spanDir, "cmd/app/main.go", "package main\n\nfunc main() { _ = 1 }\n")
	w25e2eWrite(t, spanDir, "internal/b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	w25e2eCommit(t, spanDir, "span two first-level dirs")
	if r := w25e2eScope(t, spanDir, "--base", spanBase); r.mode != "full" {
		t.Fatalf("a change spanning two first-level directories must be full, got %q", r.mode)
	}

	// Markdown/docs paths do not count toward spanning.
	docDir, docBase := w25e2eFixture(t, w25e2eGoModule("fixture.test/w25e2e"))
	w25e2eWrite(t, docDir, "README.md", "readme\n")
	w25e2eWrite(t, docDir, "doc/readme.md", "fixture v2\n")
	w25e2eCommit(t, docDir, "docs only across root and doc/")
	docReport := w25e2eScope(t, docDir, "--base", docBase)
	if docReport.mode != "scoped" {
		t.Fatalf("pure md/docs paths must not trigger spanning, got %q", docReport.mode)
	}
	if strings.Join(docReport.paths, ",") != "README.md,doc/readme.md" {
		t.Fatalf("docs paths must still be reported, got %v", docReport.paths)
	}

	// An npm configuration change -> full.
	npmDir, npmBase := w25e2eFixture(t, w25e2eNpmFixture())
	w25e2eWrite(t, npmDir, "package.json", "{\n  \"name\": \"w25e2e-fixture\",\n  \"version\": \"2.0.0\"\n}\n")
	w25e2eCommit(t, npmDir, "change package.json")
	if r := w25e2eScope(t, npmDir, "--base", npmBase); r.mode != "full" {
		t.Fatalf("a package.json change must be full, got %q", r.mode)
	}

	// A non-git directory cannot compute a scope -> full, never a wrong scoped.
	nonGit := t.TempDir()
	w25e2eWrite(t, nonGit, "go.mod", "module fixture.test/w25e2e\n\ngo 1.21\n")
	out, errb, code := w25e2eRun(t, nonGit, nil)
	if code != 0 {
		t.Fatalf("a non-git tree must still print a report (exit 0), got %d (%s)", code, errb)
	}
	if r := w25e2eParse(t, out); r.mode != "full" {
		t.Fatalf("a non-git tree must report full, got %q", r.mode)
	}

	// An unexpected `go list` shape fails closed to full.
	shapeDir, shapeBase := w25e2eFixture(t, w25e2eGoModule("fixture.test/w25e2e"))
	w25e2eWrite(t, shapeDir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 3 }\n")
	w25e2eCommit(t, shapeDir, "change leaf")
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "go"), []byte("#!/bin/sh\nprintf '%s\\n' 'unexpected go list shape'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, code = w25e2eRun(t, shapeDir, []string{"PATH=" + stub + ":" + os.Getenv("PATH")}, "--base", shapeBase)
	if code != 0 {
		t.Fatalf("an uncomputable graph must still print a report, got %d", code)
	}
	if r := w25e2eParse(t, out); r.mode != "full" {
		t.Fatalf("an unexpected `go list -test` shape must fail closed to full, got %q", r.mode)
	}
}

// TestW25E2EConfOverrideAndShellHookRejection is scenario
// "The declarative verify-scope.conf pins the stack, overrides commands and
// rejects shell hooks".
func TestW25E2EConfOverrideAndShellHookRejection(t *testing.T) {
	const scopedConf = "stack=go\nfast_gate=echo gate-one\nfast_gate=echo gate-two\nfull_command=echo custom-full\n"
	dir, base := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w25e2e\n\ngo 1.21\n",
		"leaf/leaf.go":                 "package leaf\n\nfunc Leaf() int { return 1 }\n",
		".polyforge/verify-scope.conf": scopedConf,
	})
	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25e2eCommit(t, dir, "leaf change under a custom conf")
	scoped := w25e2eScope(t, dir, "--base", base)
	if !strings.Contains(scoped.joined(), "echo gate-one") || !strings.Contains(scoped.joined(), "echo gate-two") {
		t.Fatalf("fast_gate= declarations must be reported:\n%s", scoped.joined())
	}
	if strings.Contains(scoped.joined(), "echo custom-full") {
		t.Fatalf("full_command must not appear on a scoped report:\n%s", scoped.joined())
	}
	if strings.Index(scoped.joined(), "echo gate-one") > strings.Index(scoped.joined(), "echo gate-two") {
		t.Fatalf("fast_gate order must be preserved:\n%s", scoped.joined())
	}

	// forced_full turns the same change into full and adds the full command.
	fullDir, fullBase := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w25e2e\n\ngo 1.21\n",
		"leaf/leaf.go":                 "package leaf\n\nfunc Leaf() int { return 1 }\n",
		".polyforge/verify-scope.conf": scopedConf + "forced_full=leaf/*\n",
	})
	w25e2eWrite(t, fullDir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25e2eCommit(t, fullDir, "forced-full leaf change")
	full := w25e2eScope(t, fullDir, "--base", fullBase)
	if full.mode != "full" || !strings.Contains(full.joined(), "echo custom-full") {
		t.Fatalf("forced_full must yield mode full with the full_command, got mode %q:\n%s", full.mode, full.joined())
	}

	// A shell hook is rejected with a diagnostic and never executed.
	marker := filepath.Join(t.TempDir(), "pwned")
	hookDir, _ := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w25e2e\n\ngo 1.21\n",
		"leaf/leaf.go":                 "package leaf\n\nfunc Leaf() int { return 1 }\n",
		".polyforge/verify-scope.conf": "stack=go\nfast_gate=echo safe-gate\nfast_gate=$(touch " + marker + ")\n",
	})
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the marker existed before the run")
	}
	_, errb, _ := w25e2eRun(t, hookDir, nil, "--base", "HEAD")
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("a conf command substitution was executed (marker %s exists)", marker)
	}
	if strings.TrimSpace(errb) == "" {
		t.Fatalf("a conf shell hook must be rejected with a stderr diagnostic")
	}

	// A valid conf has no line-count limit.
	var big strings.Builder
	big.WriteString("stack=go\nfast_gate=echo ok\n")
	for i := 0; i < 300; i++ {
		big.WriteString("# filler\n")
	}
	capDir, _ := w25e2eFixture(t, map[string]string{
		"go.mod":                       "module fixture.test/w25e2e\n\ngo 1.21\n",
		".polyforge/verify-scope.conf": big.String(),
	})
	if r := w25e2eScope(t, capDir, "--base", "HEAD"); !strings.Contains(r.joined(), "echo ok") {
		t.Fatalf("a valid conf must have no line cap:\n%s", r.joined())
	}
}

// TestW25E2ERepoMigration is scenario
// "The repository deletes the legacy verify-scope.sh and ships a conf as its
// only override".
func TestW25E2ERepoMigration(t *testing.T) {
	root := w25e2eRoot(t)
	if _, err := os.Stat(filepath.Join(root, ".polyforge", "verify-scope.sh")); !os.IsNotExist(err) {
		t.Fatalf(".polyforge/verify-scope.sh must be deleted (stat err = %v)", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository must ship .polyforge/verify-scope.conf as its only override: %v", err)
	}
	if !strings.Contains(string(raw), "stack=go") {
		t.Fatalf("the repository conf must pin stack=go:\n%s", raw)
	}
	report := w25e2eScope(t, root, "--base", "HEAD")
	if report.mode != "scoped" || len(report.paths) != 0 {
		t.Fatalf("an empty diff over the shipped conf must be scoped with zero paths, got mode %q paths %v", report.mode, report.paths)
	}
}

// TestW25E2EGenericDefaultsAndDocs is scenario
// "The engine, lane text and skill text stay repository-generic and the docs
// describe the engine".
func TestW25E2EGenericDefaultsAndDocs(t *testing.T) {
	root := w25e2eRoot(t)
	// Internal identifiers never spelled literally in this test file.
	internal := []string{"GMIS" + "WE", "ieops" + "-polyforge", "pi-web" + "-gmi"}

	// Docs describe the engine and no longer name the deleted script.
	doc, err := os.ReadFile(filepath.Join(root, "docs", "contributing.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tools/verify-scope", "verify-scope.conf"} {
		if !strings.Contains(string(doc), want) {
			t.Fatalf("docs/contributing.md must describe %q", want)
		}
	}
	if strings.Contains(string(doc), "sh .polyforge/verify-scope.sh") {
		t.Fatalf("docs/contributing.md still presents the deleted script as the runtime entry")
	}

	// Skills carry the new precondition and not the deleted script.
	for _, rel := range []string{"file.md", "execute-intent.md", "doctor.md"} {
		body, err := os.ReadFile(filepath.Join(root, "skills", "polyforge", "verbs", rel))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), ".polyforge/verify-scope.sh") {
			t.Fatalf("skills/polyforge/verbs/%s still tells a filer to look for the deleted script", rel)
		}
		if !strings.Contains(string(body), "verify-scope.conf") && !strings.Contains(string(body), "tools/verify-scope") {
			t.Fatalf("skills/polyforge/verbs/%s must carry the engine/conf precondition", rel)
		}
	}

	// The lane references the shipped engine and has no engine-absent branch.
	lane, err := os.ReadFile(filepath.Join(root, "workflows", "_shared", "steps", "verification.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lane), "tools/verify-scope") {
		t.Fatalf("the verification lane must name the release-shipped engine")
	}
	if strings.Contains(string(lane), ".polyforge/verify-scope.sh") {
		t.Fatalf("the verification lane still names the deleted repo-owned script")
	}

	// The engine defaults carry no repository identifier and run on a
	// synthetic zero-config fixture without a workspace clone.
	engine, err := os.ReadFile(w25e2eEnginePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range internal {
		if strings.Contains(string(engine), m) {
			t.Fatalf("the engine defaults hard-code %q", m)
		}
		if strings.Contains(string(lane), m) {
			t.Fatalf("the lane text hard-codes %q", m)
		}
	}
	synthetic, _ := w25e2eFixture(t, w25e2eGoModule("fixture.test/w25e2e"))
	if r := w25e2eScope(t, synthetic); r.mode != "scoped" {
		t.Fatalf("a synthetic zero-config Go fixture must produce a scoped report, got %q", r.mode)
	}
}
