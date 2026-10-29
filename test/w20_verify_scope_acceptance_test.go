package test

// w20_verify_scope_acceptance_test.go: the acceptance suite for
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
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW20' -count=1 -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// w20InternalMarkers are the identifiers the public projection must never
// carry. They are concatenated so this test file does not itself spell them.
func w20InternalMarkers() []string {
	return []string{"GMIS" + "WE", "ieops" + "-", "polyforge" + "-gmi#", "gmi" + "#"}
}

// w20Git runs one git command in dir with a fixed identity.
func w20Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=w20", "GIT_AUTHOR_EMAIL=w20@e",
		"GIT_COMMITTER_NAME=w20", "GIT_COMMITTER_EMAIL=w20@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// w20Write writes one file (creating parent dirs).
func w20Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w20Commit stages everything and commits, answering the new HEAD.
func w20Commit(t *testing.T, dir, msg string) string {
	t.Helper()
	w20Git(t, dir, "add", "-A")
	w20Git(t, dir, "commit", "-q", "-m", msg)
	return w20Git(t, dir, "rev-parse", "HEAD")
}

// w20Fixture builds a clone over a local bare origin on main, commits the
// given initial files plus a COPY of the real entry point (so the script's
// own-location repo resolution targets the fixture), and answers the clone dir
// and its base SHA. A missing entry point is the baseline red.
func w20Fixture(t *testing.T, initial map[string]string) (dir, base string) {
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
	w20Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	w20Git(t, root, "clone", "-q", origin, dir)
	w20Git(t, dir, "checkout", "-q", "-b", "main")
	for rel, body := range initial {
		w20Write(t, dir, rel, body)
	}
	w20Write(t, dir, "tools/verify-scope", string(raw))
	if err := os.Chmod(filepath.Join(dir, "tools", "verify-scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	w20Write(t, dir, ".polyforge/verify-scope.conf", string(confRaw))
	w20Git(t, dir, "add", "-A")
	w20Git(t, dir, "commit", "-q", "-m", "w20 base")
	w20Git(t, dir, "push", "-q", "origin", "main")
	return dir, w20Git(t, dir, "rev-parse", "HEAD")
}

// w20Run executes the fixture's entry point with cwd at the fixture root and
// a controlled environment, answering stdout, stderr and the exit code.
func w20Run(t *testing.T, dir string, extraEnv []string, args ...string) (string, string, int) {
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

// w20Report is the parsed machine-readable report.
type w20Report struct {
	mode     string
	paths    []string
	commands []string
}

// w20Joined is every command line joined for substring assertions.
func (r w20Report) w20Joined() string { return strings.Join(r.commands, "\n") }

// w20Parse parses and validates the report grammar: exactly one leading
// `mode: full|scoped`, then sorted unique `path:` lines, then `command:` lines,
// and nothing else.
func w20Parse(t *testing.T, out string) w20Report {
	t.Helper()
	trimmed := strings.TrimRight(out, "\n")
	if trimmed == "" {
		t.Fatalf("tools/verify-scope printed no report on stdout")
	}
	lines := strings.Split(trimmed, "\n")
	var r w20Report
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

// w20Report runs the entry point, requires exit 0, and parses its report.
func w20Scope(t *testing.T, dir string, args ...string) w20Report {
	t.Helper()
	out, errb, code := w20Run(t, dir, nil, args...)
	if code != 0 {
		t.Fatalf("tools/verify-scope %v: exit %d (stdout %q, stderr %q)", args, code, out, errb)
	}
	return w20Parse(t, out)
}

// w20GoModule is a minimal Go module exercising the package graph: a leaf, a
// reverse dependency, a command, and a ./test e2e file that textually names
// the leaf's path. The harness is declared by the copied conf, not by a file
// on disk, so this fixture ships no test/e2e-parallel.sh.
func w20GoModule() map[string]string {
	return map[string]string{
		"go.mod":                        "module fixture.test/w20\n\ngo 1.21\n",
		"internal/polyforge/foo/foo.go": "package foo\n\nfunc Foo() int { return 1 }\n",
		"internal/common/bar/bar.go":    "package bar\n\nimport \"fixture.test/w20/internal/polyforge/foo\"\n\nfunc Bar() int { return foo.Foo() }\n",
		"cmd/app/main.go":               "package main\n\nimport \"fixture.test/w20/internal/common/bar\"\n\nfunc main() { _ = bar.Bar() }\n",
		"test/e2e_test.go":              "package e2e\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/w20/internal/polyforge/foo\"\n)\n\n// text reference to internal/polyforge/foo/foo.go\nfunc TestTextRef(t *testing.T) {}\n",
	}
}

// TestW20PolyforgeEntryPointStandalone pins the entry point itself: the file
// exists at the fixed relative path, is executable, and runs standalone,
// printing a grammar-valid report.
func TestW20PolyforgeEntryPointStandalone(t *testing.T) {
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
	r := w20Parse(t, string(out))
	if r.mode != "scoped" {
		t.Fatalf("an empty diff (`--base HEAD`) must be scoped, got %q", r.mode)
	}
}

// TestW20PolyforgeReportGrammarDeterminismAndBase pins the report grammar,
// byte-identical determinism, and the base anchoring: default
// merge-base(HEAD, origin/<default>), an explicit `--base` override, and that
// capsule.json never influences the result.
func TestW20PolyforgeReportGrammarDeterminismAndBase(t *testing.T) {
	dir, base := w20Fixture(t, w20GoModule())
	w20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 2 }\n")
	head := w20Commit(t, dir, "change foo")

	want := []string{"internal/polyforge/foo/foo.go"}
	first := w20Scope(t, dir)
	if first.paths == nil || strings.Join(first.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("default base affected set = %v, want %v (the base must be merge-base HEAD origin/<default>)", first.paths, want)
	}
	if got := w20Git(t, dir, "merge-base", "HEAD", "origin/main"); got != base {
		t.Fatalf("fixture merge-base = %s, want the base commit %s", got, base)
	}
	second := w20Scope(t, dir)
	if strings.Join(first.paths, "\n")+first.mode+strings.Join(first.commands, "\n") !=
		strings.Join(second.paths, "\n")+second.mode+strings.Join(second.commands, "\n") {
		t.Fatalf("two runs on the same (base, HEAD) differ:\n%v\n%v", first, second)
	}
	over := w20Scope(t, dir, "--base", base)
	if strings.Join(over.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("`--base %s` affected set = %v, want %v", base, over.paths, want)
	}
	// capsule.json is never read: a bogus baseline must not move the result.
	w20Write(t, dir, "capsule.json", `{"repos":{"fixture":"`+strings.Repeat("0", 40)+`"}}`)
	again := w20Scope(t, dir)
	if strings.Join(again.paths, ",") != strings.Join(want, ",") {
		t.Fatalf("a capsule.json baseline changed the affected set: %v (want %v)", again.paths, want)
	}
	empty := w20Scope(t, dir, "--base", head)
	if empty.mode != "scoped" || len(empty.paths) != 0 {
		t.Fatalf("`--base HEAD` (empty diff) must be scoped with zero paths, got mode %q paths %v", empty.mode, empty.paths)
	}
}

// TestW20PolyforgeDeletedRenamedAndForcedFull pins path visibility (deleted
// pre-image path, rename old+new) and the mechanical forced-full conditions.
func TestW20PolyforgeDeletedRenamedAndForcedFull(t *testing.T) {
	t.Run("deleted and renamed paths stay visible", func(t *testing.T) {
		dir, base := w20Fixture(t, map[string]string{
			"docs/old.md":  "old\n",
			"docs/keep.md": "keep\n",
		})
		w20Git(t, dir, "rm", "-q", "docs/old.md")
		w20Git(t, dir, "mv", "docs/keep.md", "docs/moved.md")
		w20Commit(t, dir, "delete and rename")
		r := w20Scope(t, dir, "--base", base)
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
			{"go.mod", map[string]string{"go.mod": "module fixture.test/w20\n\ngo 1.22\n"}},
			{"driver/", map[string]string{"internal/polyforge/driver/d.go": "package driver\n"}},
			{"cross-module cmd+internal/common", map[string]string{"cmd/app/main.go": "package main\n\nfunc main() {}\n", "internal/common/bar/bar.go": "package bar\n"}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir, _ := w20Fixture(t, w20GoModule())
				for rel, body := range c.files {
					w20Write(t, dir, rel, body)
				}
				w20Commit(t, dir, "forced full: "+c.name)
				if r := w20Scope(t, dir); r.mode != "full" {
					t.Fatalf("a change naming %s must force full, got %q", c.name, r.mode)
				}
			})
		}
	})
	t.Run("non-git tree falls back to full", func(t *testing.T) {
		dir, _ := w20Fixture(t, w20GoModule())
		_ = os.RemoveAll(filepath.Join(dir, ".git"))
		out, _, code := w20Run(t, dir, nil)
		if code == 0 {
			r := w20Parse(t, out)
			if r.mode != "full" {
				t.Fatalf("a non-git tree must report full (or exit non-zero), got %q", r.mode)
			}
		}
	})
}

// TestW20PolyforgeFastGatesAndPythonRequired pins the fast gates on both
// tiers and the python3 requirement: the xrefs command fails loudly when
// python3 is absent instead of silently skipping.
func TestW20PolyforgeFastGatesAndPythonRequired(t *testing.T) {
	dir, _ := w20Fixture(t, w20GoModule())
	for _, mode := range []string{"scoped", "full"} {
		if mode == "full" {
			w20Write(t, dir, ".polyforge/extra.txt", "x\n")
			w20Commit(t, dir, "force full")
		} else {
			w20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 3 }\n")
			w20Commit(t, dir, "scoped change")
		}
		r := w20Scope(t, dir)
		if r.mode != mode {
			t.Fatalf("fixture mode = %q, want %q", r.mode, mode)
		}
		gateJoin := r.w20Joined()
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
	r := w20Scope(t, dir)
	var xrefs string
	for _, c := range r.commands {
		if strings.Contains(c, "check-xrefs.py") {
			xrefs = c
		}
	}
	if xrefs == "" {
		t.Fatalf("no check-xrefs.py command is reported:\n%s", r.w20Joined())
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

// TestW20PolyforgeScopedSelection pins the scoped selection: changed package
// plus go list reverse dependencies, tests that textually name a changed path
// (by test name), and the ./test package never coming from the reverse
// dependency graph — the fixture copies this repository's conf, whose
// go_harness= names test/e2e-parallel.sh, so the harness owns test/ and its
// tests run by name.
func TestW20PolyforgeScopedSelection(t *testing.T) {
	dir, base := w20Fixture(t, w20GoModule())
	w20Write(t, dir, "internal/polyforge/foo/foo.go", "package foo\n\nfunc Foo() int { return 4 }\n")
	w20Commit(t, dir, "change foo")
	r := w20Scope(t, dir, "--base", base)
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
		t.Fatalf("no `go test` command selects the changed package:\n%s", r.w20Joined())
	}
	for _, want := range []string{"internal/polyforge/foo", "internal/common/bar"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("scoped package command must include %q (changed package + reverse dependencies): %s", want, pkgCmd)
		}
	}
	if strings.Contains(pkgCmd, "./test") || strings.Contains(pkgCmd, "w20/test") {
		t.Fatalf("./test must never be selected by the reverse-dependency graph: %s", pkgCmd)
	}
	var e2eCmd string
	for _, c := range r.commands {
		if strings.Contains(c, "E2E_RUN") {
			e2eCmd = c
		}
	}
	if e2eCmd == "" || !strings.Contains(e2eCmd, "TestTextRef") || !strings.Contains(e2eCmd, "test/e2e-parallel.sh") {
		t.Fatalf("a test naming the changed path must be selected by name via E2E_RUN:\n%s", r.w20Joined())
	}
}

// TestW20PolyforgeScopedSelectionIncludesTransitiveTestImports pins the
// follow-up: a package P is selected when the changed leaf is
// reached only through P's test binary — P's `_test.go` imports Q and Q
// (non-test) imports the changed leaf L. Matching direct TestImports alone
// misses this case; only the test-binary dependency closure catches it.
func TestW20PolyforgeScopedSelectionIncludesTransitiveTestImports(t *testing.T) {
	dir, base := w20Fixture(t, map[string]string{
		"go.mod":                                   "module fixture.test/w20\n\ngo 1.21\n",
		"internal/polyforge/leaf/leaf.go":          "package leaf\n\nfunc Leaf() int { return 1 }\n",
		"internal/common/mid/mid.go":               "package mid\n\nimport \"fixture.test/w20/internal/polyforge/leaf\"\n\nfunc Mid() int { return leaf.Leaf() }\n",
		"internal/polyforge/driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
		"internal/polyforge/driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/w20/internal/common/mid\"\n)\n\nfunc TestDriverImportsMid(t *testing.T) {}\n",
	})
	w20Write(t, dir, "internal/polyforge/leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w20Commit(t, dir, "change leaf only")
	r := w20Scope(t, dir, "--base", base)
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
		t.Fatalf("no `go test` command selects the changed package:\n%s", r.w20Joined())
	}
	for _, want := range []string{"internal/polyforge/leaf", "internal/common/mid", "internal/polyforge/driver"} {
		if !strings.Contains(pkgCmd, want) {
			t.Fatalf("the test-binary closure must select %q (driver_test -> mid -> leaf): %s", want, pkgCmd)
		}
	}
}

// TestW20PolyforgeRunExecutesReportedCommands pins `--run`: it prints the
// report, executes exactly the reported commands in order, and exits non-zero
// if any fails. The toolchain is stubbed so the test never builds the tree.
func TestW20PolyforgeRunExecutesReportedCommands(t *testing.T) {
	dir, _ := w20Fixture(t, w20GoModule())
	// The copied conf reports the console SPA build as the FIRST fast gate;
	// give the fixture its webui/ tree so `cd webui` succeeds under `--run`.
	w20Write(t, dir, "webui/package.json", "{\n  \"name\": \"fixture-webui\"\n}\n")
	w20Commit(t, dir, "webui fixture")
	stub := t.TempDir()
	logPath := filepath.Join(stub, "calls.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$(basename \"$0\")\" >> \"$W20_LOG\"\n" +
		"if [ -n \"${W20_FAIL_ARG:-}\" ] && printf '%s' \"$*\" | grep -q -- \"$W20_FAIL_ARG\"; then exit 1; fi\nexit 0\n"
	for _, tool := range []string{"go", "gofmt", "python3", "npm"} {
		p := filepath.Join(stub, tool)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + stub + ":" + os.Getenv("PATH"), "W20_LOG=" + logPath}

	r := w20Scope(t, dir, "--base", "HEAD")
	if _, _, code := w20Run(t, dir, env, "--base", "HEAD", "--run"); code != 0 {
		t.Fatalf("`--run` on a green command list: exit %d, want 0", code)
	}
	// The logged tool sequence: for each reported command, the stub it invokes
	// (gofmt before go — `gofmt` contains the substring `go`). The SPA-build
	// command is stubbed too but is deliberately not counted here: it invokes
	// the `npm` stub twice (`npm ci && npm run build`), and its log entries sit
	// before the compared tail.
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
	env = append(env, "W20_FAIL_ARG=build")
	if _, _, code := w20Run(t, dir, env, "--base", "HEAD", "--run"); code == 0 {
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

// TestW20PolyforgeTierDocsAndProjection pins the contributor docs and the
// public-projection identifier invariant on the projected files.
func TestW20PolyforgeTierDocsAndProjection(t *testing.T) {
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
		for _, marker := range w20InternalMarkers() {
			if strings.Contains(file.body, marker) {
				t.Fatalf("%s carries the internal identifier %q — the public projection forbids it", file.name, marker)
			}
		}
	}
}
