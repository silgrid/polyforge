package test

// w27_scope_test_import_repro_test.go: the workbench#27 reproduction/regression
// test for the scoped selection's test-import blind spot in
// the release-shipped `tools/verify-scope` with the repository conf.
//
// The reported failure (workbench#20 review, blocking B1): `compute_scoped`
// selects package P only when a changed import path appears in P's non-test
// `go list {{.Deps}}` closure. A package whose `_test.go` files (internal or
// external test package) import the changed package is not selected, even
// though `go test` of that package compiles and runs against the change — while
// the script still reports `mode: scoped`.
//
// The fixture is a minimal offline Go module:
//
//	internal/polyforge/runtime/runtime.go      the changed leaf package
//	internal/common/bar/bar.go                 a real (non-test) reverse dependency
//	internal/polyforge/driver/driver.go        package whose non-test code does NOT import runtime
//	internal/polyforge/driver/driver_test.go   a _test.go file that DOES import runtime
//
// The real entry point is copied into the fixture so its own-location repo
// resolution targets the fixture. The diff base is the pre-change commit and
// the change touches only internal/polyforge/runtime.
//
// Unfixed: the emitted `go test` line selects ./internal/common/bar and
// ./internal/polyforge/runtime but not ./internal/polyforge/driver.
// Fixed:   it also selects ./internal/polyforge/driver.
//
// Run (targeted, from repos/<name>/): GOWORK=off go test ./test/ -run '^TestWB27' -count=1 -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// wb27Git runs one git command in dir with a fixed identity.
func wb27Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wb27", "GIT_AUTHOR_EMAIL=wb27@e",
		"GIT_COMMITTER_NAME=wb27", "GIT_COMMITTER_EMAIL=wb27@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// wb27Write writes one file (creating parent dirs).
func wb27Write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wb27Fixture builds a clone over a local bare origin on main, commits a
// minimal Go module plus a COPY of the real entry point, and answers the clone
// dir and its base SHA.
func wb27Fixture(t *testing.T) (dir, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	src, err := filepath.Abs(filepath.Join("..", "tools", "verify-scope"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("tools/verify-scope must exist: %v", err)
	}
	confSrc, err := filepath.Abs(filepath.Join("..", ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	confRaw, err := os.ReadFile(confSrc)
	if err != nil {
		t.Fatalf(".polyforge/verify-scope.conf must exist: %v", err)
	}
	root := t.TempDir()
	origin, dir := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	wb27Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
	wb27Git(t, root, "clone", "-q", origin, dir)
	wb27Git(t, dir, "checkout", "-q", "-b", "main")
	files := map[string]string{
		"go.mod":                                   "module fixture.test/wb27\n\ngo 1.21\n",
		"internal/polyforge/runtime/runtime.go":    "package runtime\n\nfunc Value() int { return 1 }\n",
		"internal/common/bar/bar.go":               "package bar\n\nimport \"fixture.test/wb27/internal/polyforge/runtime\"\n\nfunc Bar() int { return runtime.Value() }\n",
		"internal/polyforge/driver/driver.go":      "package driver\n\nfunc Name() string { return \"driver\" }\n",
		"internal/polyforge/driver/driver_test.go": "package driver\n\nimport (\n\t\"testing\"\n\n\t_ \"fixture.test/wb27/internal/polyforge/runtime\"\n)\n\nfunc TestDriverImportsRuntime(t *testing.T) {}\n",
	}
	for rel, body := range files {
		wb27Write(t, dir, rel, body)
	}
	wb27Write(t, dir, "tools/verify-scope", string(raw))
	if err := os.Chmod(filepath.Join(dir, "tools", "verify-scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	wb27Write(t, dir, ".polyforge/verify-scope.conf", string(confRaw))
	wb27Git(t, dir, "add", "-A")
	wb27Git(t, dir, "commit", "-q", "-m", "wb27 base")
	wb27Git(t, dir, "push", "-q", "origin", "main")
	return dir, wb27Git(t, dir, "rev-parse", "HEAD")
}

// wb27Run executes the fixture's entry point with cwd at the fixture root.
func wb27Run(t *testing.T, dir string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(dir, "tools", "verify-scope"), args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
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

// TestWB27ScopedSelectionIncludesTestOnlyImporters pins the repro: a package
// whose _test.go imports the changed package is selected for `go test`.
func TestWB27ScopedSelectionIncludesTestOnlyImporters(t *testing.T) {
	dir, base := wb27Fixture(t)
	wb27Write(t, dir, "internal/polyforge/runtime/runtime.go", "package runtime\n\nfunc Value() int { return 2 }\n")
	wb27Git(t, dir, "add", "-A")
	wb27Git(t, dir, "commit", "-q", "-m", "change runtime only")

	out, errb, code := wb27Run(t, dir, "--base", base)
	if code != 0 {
		t.Fatalf("tools/verify-scope --base %s: exit %d (stdout %q, stderr %q)", base, code, out, errb)
	}
	if !strings.Contains(out, "mode: scoped") {
		t.Fatalf("a runtime-only change must stay scoped, got:\n%s", out)
	}
	var pkgCmd string
	for _, line := range strings.Split(out, "\n") {
		c := strings.TrimPrefix(line, "command: ")
		if c != line && strings.Contains(c, "go test") && strings.Contains(c, "internal/polyforge/runtime") {
			pkgCmd = c
		}
	}
	if pkgCmd == "" {
		t.Fatalf("no `go test` command selects the changed package:\n%s", out)
	}
	if !strings.Contains(pkgCmd, "./internal/common/bar") {
		t.Fatalf("the non-test reverse dependency ./internal/common/bar must be selected: %s", pkgCmd)
	}
	if !strings.Contains(pkgCmd, "./internal/polyforge/driver") {
		t.Fatalf("a package whose _test.go imports the changed package must be selected for `go test` even though the changed package is absent from its non-test Deps:\ngot: %s\nfull report:\n%s", pkgCmd, out)
	}
}
