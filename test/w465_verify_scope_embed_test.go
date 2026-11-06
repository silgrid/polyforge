// carries: workflows/_shared/scripts/verify-scope

package test

// w465_verify_scope_embed_test.go: the scope engine's Go
// package graph does not depend on a front-end build having run. A package
// that embeds a build output (`//go:embed all:dist`) makes `go list` fail
// while that directory is absent — on a fresh clone, before the console
// build — and the light tier fell back to full for that alone. The engine now
// lists the graph with `go list -e`: an embed pattern matching no file is
// tolerated (it changes no package's dependencies), and every other package
// error still fails closed to the full tier.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW465Engine' -count=1 -v

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// w465EmbedFiles is a Go module whose package web embeds a dist tree that is
// not there, and whose package app imports both web and leaf.
func w465EmbedFiles() map[string]string {
	return map[string]string{
		"go.mod":          "module fixture.test/w465\n\ngo 1.21\n",
		"web/web.go":      "package web\n\nimport \"embed\"\n\n//go:embed all:dist\nvar Dist embed.FS\n",
		"web/web_test.go": "package web\n\nimport \"testing\"\n\nfunc TestWeb(t *testing.T) { _ = Dist }\n",
		"leaf/leaf.go":    "package leaf\n\nfunc Leaf() int { return 1 }\n",
		"app/app.go": "package app\n\nimport (\n\t\"fixture.test/w465/leaf\"\n\t\"fixture.test/w465/web\"\n)\n\n" +
			"func App() int { _ = web.Dist; return leaf.Leaf() }\n",
		"app/app_test.go": "package app\n\nimport \"testing\"\n\nfunc TestApp(t *testing.T) { _ = App() }\n",
	}
}

func w465NeedGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not on PATH: %v", err)
	}
}

func w465AssertAbsent(t *testing.T, dir, rel string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
		t.Fatalf("%s must be absent in %s for this scenario (stat err %v)", rel, dir, err)
	}
}

// TestW465EngineLightToleratesAMissingEmbedDir: a change to leaf, with web's
// embed dir missing, answers a scoped plan on the light and the default tier.
func TestW465EngineLightToleratesAMissingEmbedDir(t *testing.T) {
	w465NeedGo(t)
	dir, base := w25e2eFixture(t, w465EmbedFiles())
	w465AssertAbsent(t, dir, "web/dist")
	w25e2eWrite(t, dir, "leaf/leaf.go", "package leaf\n\nfunc Leaf() int { return 2 }\n")
	w25e2eCommit(t, dir, "change leaf")

	for _, tier := range [][]string{{"--light"}, nil} {
		args := append(append([]string{}, tier...), "--base", base)
		r := w403Run(t, dir, args...)
		if r.Mode != "scoped" {
			t.Fatalf("%v: a missing embed dir must not force the full tier: mode %q, diagnostics %q", args, r.Mode, r.Diagnostics)
		}
		if r.diag("go list failed") {
			t.Fatalf("%v: a tolerated embed error must add no go list diagnostic: %q", args, r.Diagnostics)
		}
		want := "GOWORK=off go test -count=1 ./app ./leaf"
		if got := r.phase("scoped"); !slices.Contains(got, want) {
			t.Fatalf("%v: the scoped commands must include %q (leaf and its importer app), got %q", args, want, got)
		}
	}
}

// TestW465EngineOtherPackageErrorsStillFallBack: an unresolvable import is a
// package error the engine still refuses: the light tier answers full and
// names the go list failure.
func TestW465EngineOtherPackageErrorsStillFallBack(t *testing.T) {
	w465NeedGo(t)
	files := w465EmbedFiles()
	files["c/c.go"] = "package c\n\nimport _ \"fixture.test/w465/missing\"\n\nfunc C() int { return 1 }\n"
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "c/c.go", "package c\n\nimport _ \"fixture.test/w465/missing\"\n\nfunc C() int { return 2 }\n")
	w25e2eCommit(t, dir, "change c")

	r := w403Run(t, dir, "--light", "--base", base)
	if r.Mode != "full" {
		t.Fatalf("an unresolvable import must still force the full tier: mode %q, diagnostics %q", r.Mode, r.Diagnostics)
	}
	if !r.diag("go list failed", "no required module provides package", "falling back to the full tier") {
		t.Fatalf("the fallback must name the go list failure and its package error: %q", r.Diagnostics)
	}
}

// TestW465EngineLightThisRepoFreshClone: a fresh clone of this repository
// (no console build, so no webui/dist) answers a scoped light plan for a
// change to one package.
func TestW465EngineLightThisRepoFreshClone(t *testing.T) {
	w465NeedGo(t)
	src := w25e2eRoot(t)
	dir := filepath.Join(t.TempDir(), "clone")
	w25e2eGit(t, filepath.Dir(dir), "clone", "-q", src, dir)
	w465AssertAbsent(t, dir, "webui/dist")
	base := w25e2eGit(t, dir, "rev-parse", "HEAD")
	rel := "internal/polyforge/taskrepo/taskrepo.go"
	body, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	w25e2eWrite(t, dir, rel, string(body)+"\n// a fresh-clone change\n")
	w25e2eGit(t, dir, "commit", "-q", "-am", "change one package")

	r := w403Run(t, dir, "--light", "--base", base)
	if r.Mode != "scoped" {
		t.Fatalf("a fresh clone without webui/dist must get a scoped light plan: mode %q, diagnostics %q", r.Mode, r.Diagnostics)
	}
	if r.diag("go list failed") {
		t.Fatalf("no go list failure may be diagnosed on a fresh clone: %q", r.Diagnostics)
	}
	named := false
	for _, c := range r.phase("scoped") {
		if strings.HasPrefix(c, "GOWORK=off go test ") && slices.Contains(strings.Fields(c), "./internal/polyforge/taskrepo") {
			named = true
		}
	}
	if !named {
		t.Fatalf("a scoped go test command must name ./internal/polyforge/taskrepo, got %q", r.phase("scoped"))
	}
}
