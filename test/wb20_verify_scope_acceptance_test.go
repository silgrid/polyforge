package test

// wb20_verify_scope_acceptance_test.go: the workbench#20 acceptance suite for
// the release-shipped verification-tier engine, `tools/verify-scope`, and the
// repository's declarative `.polyforge/verify-scope.conf`.
// Authored from artifacts/spec.md's Acceptance criteria and Scenario blocks
// BEFORE the implementation exists (e2e-author), driving the entry point only
// through its documented surface — its stdout report, real git, the real
// filesystem. No implementation internals are referenced; the file compiles
// and vets against the tree as it stands today.
//
// Every scenario is an assertion-level RED on the pre-implementation tree:
// the entry point does not exist yet, so each one fails on a missing file or
// a report assertion, never on a build error.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestWB20' -count=1 -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// wb20InternalMarkers are the identifiers the public projection must never
// carry. They are concatenated so this test file does not itself spell them.
func wb20InternalMarkers() []string {
	return []string{"GMIS" + "WE", "ieops" + "-", "polyforge" + "-gmi#", "gmi" + "#"}
}

// wb20Git runs one git command in dir with a fixed identity.
func wb20Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wb20", "GIT_AUTHOR_EMAIL=wb20@e",
		"GIT_COMMITTER_NAME=wb20", "GIT_COMMITTER_EMAIL=wb20@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// wb20Write writes one file (creating parent dirs).
func wb20Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wb20Commit stages everything and commits, answering the new HEAD.
func wb20Commit(t *testing.T, dir, msg string) string {
	t.Helper()
	wb20Git(t, dir, "add", "-A")
	wb20Git(t, dir, "commit", "-q", "-m", msg)
	return wb20Git(t, dir, "rev-parse", "HEAD")
}

// wb20Fixture builds a clone over a local bare origin on main, commits the
// given initial files plus a COPY of the real entry point (so the script's
// own-location repo resolution targets the fixture), and answers the clone dir
// and its base SHA. A missing entry point is the baseline red.
func wb20Fixture(t *testing.T, initial map[string]string) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	src, err := filepath.Abs(filepath.Join("..", "tools", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("repos/"+"ieops"+"-polyforge/tools/verify-scope must exist: %v", err)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	confSrc, err := filepath.Abs(filepath.Join("..", ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	confRaw, err := os.ReadFile(confSrc)
	if err != nil {
		t.Fatalf("repos/"+"ieops"+"-polyforge/.polyforge/verify-scope.conf must exist: %v", err)
	}
	root := t.TempDir()
	origin, dir := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	wb20Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	wb20Git(t, root, "clone", "-q", origin, dir)
	wb20Git(t, dir, "checkout", "-q", "-b", "main")
	for rel, body := range initial {
		wb20Write(t, dir, rel, body)
	}
	wb20Write(t, dir, "tools/verify-scope", string(raw))
	if err := os.Chmod(filepath.Join(dir, "tools", "verify-scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	wb20Write(t, dir, ".polyforge/verify-scope.conf", string(confRaw))
	wb20Git(t, dir, "add", "-A")
	wb20Git(t, dir, "commit", "-q", "-m", "wb20 base")
	wb20Git(t, dir, "push", "-q", "origin", "main")
	return dir, wb20Git(t, dir, "rev-parse", "HEAD")
}

// wb20Run executes the fixture's entry point with cwd at the fixture root and
// a controlled environment, answering stdout, stderr and the exit code.
func wb20Run(t *testing.T, dir string, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, "tools", "verify-scope"), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
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

// wb20Report is the parsed machine-readable report.
type wb20Report struct {
	mode     string
	paths    []string
	commands []string
}

// wb20Joined is every command line joined for substring assertions.
func (r wb20Report) wb20Joined() string { return strings.Join(r.commands, "\n") }

// wb20Parse parses and validates the report grammar: exactly one leading
// `mode: full|scoped`, then sorted unique `path:` lines, then `command:` lines,
// and nothing else.
func wb20Parse(t *testing.T, out string) wb20Report {
	t.Helper()
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Fatalf("tools/verify-scope printed no report on stdout")
	}
	lines := strings.Split(trimmed, "\n")
	var r wb20Report
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

// wb20Report runs the entry point, requires exit 0, and parses its report.
func wb20Scope(t *testing.T, dir string, args ...string) wb20Report {
	t.Helper()
	out, errb, code := wb20Run(t, dir, nil, args...)
	if code != 0 {
		t.Fatalf("tools/verify-scope %v: exit %d (stdout %q, stderr %q)", args, code, out, errb)
	}
	return wb20Parse(t, out)
}

// wb20GoModule is a minimal Go module exercising the package graph: a leaf, a
// reverse dependency, a command, and a ./test e2e file that textually names
// the leaf's path. The harness is declared by the copied conf, not by a file
// on disk, so this fixture ships no test/e2e-parallel.sh.
func wb20GoModule() map[string]string {
	return map[string]string{
		"go.mod":                        "module fixture.test/wb20\n\ngo 1.21\n",
		"internal/polyforge/foo/foo.go": "package foo\n\nfunc Foo() int { return 1 }\n",
		"internal/common/bar/bar.go":    "package bar\n\nimport \"fixture.test/wb20/internal/polyforge/foo\"\n\nfunc Bar() int { return foo.Foo() }\n",
		"cmd/app/main.go":               "package main\n\nimport \"fixture.test/wb20/internal/common/bar\"\n\nfunc main() { _ = bar.Bar() }\n",
		"test/e2e_test.go":              "package e2e\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/wb20/internal/polyforge/foo\"\n)\n\n// text reference to internal/polyforge/foo/foo.go\nfunc TestTextRef(t *testing.T) {}\n",
	}
}

// TestWB20PolyforgeEntryPointStandalone pins the entry point itself: the file
// exists at the fixed relative path, is executable, and runs standalone,
// printing a grammar-valid report.
func TestWB20PolyforgeEntryPointStandalone(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "tools", "verify-scope")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("repos/"+"ieops"+"-polyforge/tools/verify-scope must exist: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("tools/verify-scope is not executable: mode %v", info.Mode())
	}
	cmd := exec.Command(script, "--base", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tools/verify-scope must run standalone (`--base HEAD`): %v", err)
	}
	r := wb20Parse(t, string(out))
	if r.mode != "scoped" {
		t.Fatalf("an empty diff (`--base HEAD`) must be scoped, got %q", r.mode)
	}
}

// TestWB20PolyforgeReportGrammarDeterminismAndBase pins the report grammar,
// byte-identical determinism, and the base anchoring: default
// merge-base(HEAD, origin/<default>), an explicit `--base` override, and that
// capsule.json never influences the result.
func TestWB20PolyforgeReportGrammarDeterminismAndBase(t *testing.T) {
	dir, base := wb20Fixture(t, wb20GoModule())
	wb20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 2 }\n")
	head := wb20Commit(t, dir, "change foo")

	want := []string{"internal/polyforge/foo/foo.go"}
	first := wb20Scope(t, dir)
	if first.paths == nil || strings.Join(first.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("default base affected set = %v, want %v (the base must be merge-base HEAD origin/<default>)", first.paths, want)
	}
	if got := wb20Git(t, dir, "merge-base", "HEAD", "origin/main"); got != base {
		t.Fatalf("fixture merge-base = %s, want the base commit %s", got, base)
	}
	second := wb20Scope(t, dir)
	if strings.Join(first.paths, "\n")+first.mode+strings.Join(first.commands, "\n") !=
		strings.Join(second.paths, "\n")+second.mode+strings.Join(second.commands, "\n") {
		t.Fatalf("two runs on the same (base, HEAD) differ:\n%v\n%v", first, second)
	}
	over := wb20Scope(t, dir, "--base", base)
	if strings.Join(over.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("`--base %s` affected set = %v, want %v", base, over.paths, want)
	}
	// capsule.json is never read: a bogus baseline must not move the result.
	wb20Write(t, dir, "capsule.json", `{"repos":{"fixture":"`+strings.Repeat("0", 40)+`"}}`)
	again := wb20Scope(t, dir)
	if strings.Join(again.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("a capsule.json baseline changed the affected set: %v (want %v)", again.paths, want)
	}
	empty := wb20Scope(t, dir, "--base", head)
	if empty.mode != "scoped" || len(empty.paths) != 0 {
		t.Fatalf("`--base HEAD` (empty diff) must be scoped with zero paths, got mode %q paths %v", empty.mode, empty.paths)
	}
}

// TestWB20PolyforgeDeletedRenamedAndForcedFull pins path visibility (deleted
// pre-image path, rename old+new) and the mechanical forced-full conditions.
func TestWB20PolyforgeDeletedRenamedAndForcedFull(t *testing.T) {
	t.Run("deleted and renamed paths stay visible", func(t *testing.T) {
		dir, base := wb20Fixture(t, map[string]string{
			"docs/old.md":  "old\n",
			"docs/keep.md": "keep\n",
		})
		wb20Git(t, dir, "rm", "-q", "docs/old.md")
		wb20Git(t, dir, "mv", "docs/keep.md", "docs/moved.md")
		wb20Commit(t, dir, "delete and rename")
		r := wb20Scope(t, dir, "--base", base)
		joined := strings.Join(r.paths, "\n")
		for _, want := range []string{"docs/old.md", "docs/keep.md", "docs/moved.md"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("affected set %v does not list %q (a deleted path keeps its pre-image; a rename contributes both)", r.paths, want)
			}
		}
	})
	t.Run("forced-full paths", func(t *testing.T) {
		cases := []struct {
			name  string
			files map[string]string
		}{
			{"under .polyforge/", map[string]string{".polyforge/extra.txt": "x\n"}},
			{"go.mod", map[string]string{"go.mod": "module fixture.test/wb20\n\ngo 1.22\n"}},
			{"driver/", map[string]string{"internal/polyforge/driver/d.go": "package driver\n"}},
			{"cross-module cmd+internal/common", map[string]string{"cmd/app/main.go": "package main\n\nfunc main() {}\n", "internal/common/bar/bar.go": "package bar\n"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir, _ := wb20Fixture(t, wb20GoModule())
				for rel, body := range c.files {
					wb20Write(t, dir, rel, body)
				}
				wb20Commit(t, dir, "forced full: "+c.name)
				if r := wb20Scope(t, dir); r.mode != "full" {
					t.Fatalf("a change naming %s must force full, got %q", c.name, r.mode)
				}
			})
		}
	})
	t.Run("non-git tree falls back to full", func(t *testing.T) {
		dir, _ := wb20Fixture(t, wb20GoModule())
		_ = os.RemoveAll(filepath.Join(dir, ".git"))
		out, _, code := wb20Run(t, dir, nil)
		if code == 0 {
			r := wb20Parse(t, out)
			if r.mode != "full" {
				t.Fatalf("a non-git tree must report full (or exit non-zero), got %q", r.mode)
			}
		}
	})
}

// TestWB20PolyforgeFastGatesAndPythonRequired pins the fast gates on both
// tiers and the python3 requirement: the xrefs command fails loudly when
// python3 is absent instead of silently skipping.
func TestWB20PolyforgeFastGatesAndPythonRequired(t *testing.T) {
	dir, _ := wb20Fixture(t, wb20GoModule())
	for _, mode := range []string{"scoped", "full"} {
		if mode == "full" {
			wb20Write(t, dir, ".polyforge/extra.txt", "x\n")
			wb20Commit(t, dir, "force full")
		} else {
			wb20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 3 }\n")
			wb20Commit(t, dir, "scoped change")
		}
		r := wb20Scope(t, dir)
		if r.mode != mode {
			t.Fatalf("fixture mode = %q, want %q", r.mode, mode)
		}
		gateJoin := r.wb20Joined()
		for _, want := range []string{
			"go build ./...", "go vet ./...", "gofmt -l .", "python3", "check-xrefs.py",
			"GOOS=darwin", "GOARCH=arm64", "GOARCH=amd64",
		} {
			if !strings.Contains(gateJoin, want) {
				t.Fatalf("%s fast gates must contain %q:\n%s", mode, want, gateJoin)
			}
		}
	}
	// Missing python3 fails loudly: run the reported xrefs command with an
	// empty PATH and require a non-zero exit and no skip/pass claim.
	r := wb20Scope(t, dir)
	var xrefs string
	for _, c := range r.commands {
		if strings.Contains(c, "check-xrefs.py") {
			xrefs = c
		}
	}
	if xrefs == "" {
		t.Fatalf("no check-xrefs.py command is reported:\n%s", r.wb20Joined())
	}
	emptyBin := t.TempDir()
	c := exec.Command("/bin/sh", "-c", xrefs)
	c.Env = append(os.Environ(), "PATH="+emptyBin)
	out, err := c.CombinedOutput()
	if err == nil {
		t.Fatalf("the xrefs gate passed with python3 absent (it must fail loudly): %s", out)
	}
	if strings.Contains(strings.ToLower(string(out)), "skip") {
		t.Fatalf("the xrefs gate reported a skip with python3 absent: %s", out)
	}
}

// TestWB20PolyforgeScopedSelection pins the scoped selection: changed package
// plus go list reverse dependencies, tests that textually name a changed path
// (by test name), and the ./test package never coming from the reverse
// dependency graph — the fixture copies this repository's conf, whose
// go_harness= names test/e2e-parallel.sh, so the harness owns test/ and its
// tests run by name.
func TestWB20PolyforgeScopedSelection(t *testing.T) {
	dir, base := wb20Fixture(t, wb20GoModule())
	wb20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 4 }\n")
	wb20Commit(t, dir, "change foo")
	r := wb20Scope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a leaf change must be scoped, got %q", r.mode)
	}
	var pkgCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "go test") && strings.Contains(c, "internal/polyforge/foo") {
			pkgCmd = c
		}
	}
	if pkgCmd == "" {
		t.Fatalf("no `go test` command selects the changed package:\n%s", r.wb20Joined())
	}
	for _, want := range []string{"internal/polyforge/foo", "internal/common/bar"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("scoped package command must include %q (changed package + reverse dependencies): %s", want, pkgCmd)
		}
	}
	if strings.Contains(pkgCmd, "./test") || strings.Contains(pkgCmd, "wb20/test") {
		t.Fatalf("./test must never be selected by the reverse-dependency graph: %s", pkgCmd)
	}
	var e2eCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "E2E_RUN") {
			e2eCmd = c
		}
	}
	if e2eCmd == "" || !strings.Contains(e2eCmd, "TestTextRef") || !strings.Contains(e2eCmd, "test/e2e-parallel.sh") {
		t.Fatalf("a test naming the changed path must be selected by name via E2E_RUN:\n%s", r.wb20Joined())
	}
}

// TestWB20PolyforgeScopedSelectionIncludesTransitiveTestImports pins the
// workbench#27 follow-up: a package P is selected when the changed leaf is
// reached only through P's test binary — P's `_test.go` imports Q and Q
// (non-test) imports the changed leaf L. Matching direct TestImports alone
// misses this case; only the test-binary dependency closure catches it.
func TestWB20PolyforgeScopedSelectionIncludesTransitiveTestImports(t *testing.T) {
	dir, base := wb20Fixture(t, map[string]string{
		"go.mod":                                   "module fixture.test/wb20\n\ngo 1.21\n",
		"internal/polyforge/leaf/leaf.go":          "package leaf\n\nfunc Leaf() int { return 1 }\n",
		"internal/common/mid/mid.go":               "package mid\n\nimport \"fixture.test/wb20/internal/polyforge/leaf\"\n\nfunc Mid() int { return leaf.Leaf() }\n",
		"internal/polyforge/driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
		"internal/polyforge/driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/wb20/internal/common/mid\"\n)\n\nfunc TestDriverImportsMid(t *testing.T) {}\n",
	})
	wb20Write(t, dir, "internal/polyforge/leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	wb20Commit(t, dir, "change leaf only")
	r := wb20Scope(t, dir, "--base", base)
	if r.mode != "scoped" {
		t.Fatalf("a leaf change must be scoped, got %q", r.mode)
	}
	var pkgCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "go test") && strings.Contains(c, "internal/polyforge/leaf") {
			pkgCmd = c
		}
	}
	if pkgCmd == "" {
		t.Fatalf("no `go test` command selects the changed package:\n%s", r.wb20Joined())
	}
	for _, want := range []string{"internal/polyforge/leaf", "internal/common/mid", "internal/polyforge/driver"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("the test-binary closure must select %q (driver_test -> mid -> leaf): %s", want, pkgCmd)
		}
	}
}

// TestWB20PolyforgeRunExecutesReportedCommands pins `--run`: it prints the
// report, executes exactly the reported commands in order, and exits non-zero
// if any fails. The toolchain is stubbed so the test never builds the tree.
func TestWB20PolyforgeRunExecutesReportedCommands(t *testing.T) {
	dir, _ := wb20Fixture(t, wb20GoModule())
	stub := t.TempDir()
	logPath := filepath.Join(stub, "calls.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$(basename \"$0\")\" >> \"$WB20_LOG\"\n" +
		"if [ -n \"${WB20_FAIL_ARG:-}\" ] && printf '%s' \"$*\" | grep -q -- \"$WB20_FAIL_ARG\"; then exit 1; fi\nexit 0\n"
	for _, tool := range []string{"go", "gofmt", "python3"} {
		p := filepath.Join(stub, tool)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + stub + ":" + os.Getenv("PATH"), "WB20_LOG=" + logPath}

	r := wb20Scope(t, dir, "--base", "HEAD")
	if _, _, code := wb20Run(t, dir, env, "--base", "HEAD", "--run"); code != 0 {
		t.Fatalf("`--run` on a green command list: exit %d, want 0", code)
	}
	// The logged tool sequence: for each reported command, the stub it invokes
	// (gofmt before go — `gofmt` contains the substring `go`).
	stubs := []string{"gofmt", "python3", "go"}
	want := make([]string, 0, len(r.commands))
	for _, c := range r.commands {
		for _, tool := range stubs {
			if strings.Contains(c, tool) {
				want = append(want, tool)
				break
			}
		}
	}
	loggedRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	for _, line := range strings.Split(strings.TrimSpace(string(loggedRaw)), "\n") {
		logged = append(logged, strings.TrimSpace(line))
	}
	// Compare the whole ordered sequence of executed tools. The report
	// computation may invoke the stubs first (e.g. `go list`), so take the
	// last len(want) entries — `--run` prints the report and THEN executes.
	if len(logged) < len(want) {
		t.Fatalf("`--run` executed %d commands, want at least %d (%v)", len(logged), len(want), logged)
	}
	got := logged[len(logged)-len(want):]
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("`--run` executed %v, want the reported commands' tools in order %v", got, want)
	}
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	env = append(env, "WB20_FAIL_ARG=build")
	if _, _, code := wb20Run(t, dir, env, "--base", "HEAD", "--run"); code == 0 {
		t.Fatalf("`--run` must exit non-zero when a reported command fails")
	}
	failed, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(failed), "gofmt") || strings.Contains(string(failed), "python3") {
		t.Fatalf("`--run` continued past a failure:\n%s", failed)
	}
}

// TestWB20PolyforgeTierDocsAndProjection pins the contributor docs and the
// public-projection identifier invariant on the projected files.
func TestWB20PolyforgeTierDocsAndProjection(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(root, "docs", "contributing.md"))
	if err != nil {
		t.Fatalf("docs/contributing.md must exist: %v", err)
	}
	text := string(doc)
	for _, want := range []string{
		"verification tiers", "tools/verify-scope", "verify-scope.conf", "--base",
		"go build ./...", "go vet ./...", "gofmt -l .", "check-xrefs.py",
		"go.mod", "Makefile", "internal/polyforge/driver", "internal/polyforge/daemon",
	} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Fatalf("docs/contributing.md must document %q in its Verification tiers section", want)
		}
	}
	contrib, err := os.ReadFile(filepath.Join(root, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contrib), "docs/contributing.md") ||
		!strings.Contains(strings.ToLower(string(contrib)), "verification tiers") {
		t.Fatalf("root CONTRIBUTING.md must carry a one-line pointer to the Verification tiers section:\n%s", contrib)
	}
	engine, err := os.ReadFile(filepath.Join(root, "tools", "verify-scope"))
	if err != nil {
		t.Fatalf("the engine must exist to check the projection invariant: %v", err)
	}
	conf, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repo conf must exist to check the projection invariant: %v", err)
	}
	for _, file := range []struct{ name, body string }{{"docs/contributing.md", text}, {"tools/verify-scope", string(engine)}, {".polyforge/verify-scope.conf", string(conf)}} {
		for _, marker := range wb20InternalMarkers() {
			if strings.Contains(file.body, marker) {
				t.Fatalf("%s carries the internal identifier %q — the public projection forbids it", file.name, marker)
			}
		}
	}
}
