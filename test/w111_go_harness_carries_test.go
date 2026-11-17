// carries: workflows/_shared/scripts/verify-scope

package test

// w111_go_harness_carries_test.go (polyforge#111 u1): the Go analyzer
// consumes the carries: declarations of the declared go_harness directory's
// *_test.go files — the declarations test/ has carried as comments all
// along (w348's `internal/polyforge/daemon/*` was the light tier's blind
// spot that sent #74 to the full gate). A changed path matching a harness
// test's carries glob adds that file's test names to the same E2E_RUN
// command the literal-reference rule builds; a harness test without a
// declaration is unaffected; a malformed declaration fails closed, exactly
// like the npm carries layer. A repository without a declared go_harness
// consumes none of it (test/ is an ordinary package there).
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW111GoHarnessCarries$' -count=1 -v

import (
	"strings"
	"testing"
)

// w111HarnessFiles is a Go module whose declared harness owns test/: one
// harness test declares `lib/*`, another `docs/*`, and one declares nothing
// — so no changed path can reach a test name through the literal-reference
// rule (the globs are not substrings of any path) and every selection below
// is the carries rule's own.
func w111HarnessFiles() map[string]string {
	return map[string]string{
		"go.mod":                       "module fixture.test/w111\n\ngo 1.21\n",
		"lib/lib.go":                   "package lib\n\nfunc Value() int { return 1 }\n",
		"docs/guide.md":                "# guide\n",
		"other/thing.txt":              "x\n",
		"test/harness.sh":              "#!/bin/sh\nexit 0\n",
		"test/accept_test.go":          "// carries: lib/*\npackage e2e\n\nimport \"testing\"\n\nfunc TestCarriedAcceptance(t *testing.T) {}\n",
		"test/other_test.go":           "// carries: docs/*\npackage e2e\n\nimport \"testing\"\n\nfunc TestCarriedOther(t *testing.T) {}\n",
		"test/plain_test.go":           "package e2e\n\nimport \"testing\"\n\nfunc TestPlainUncarried(t *testing.T) {}\n",
		".polyforge/verify-scope.conf": "stack=go\ngo_harness=test/harness.sh\nfast_gate=echo FAST\n",
	}
}

// w111HarnessCarried: a change matching a harness test's carries glob
// selects that test through the harness command, merged with the
// literal-referenced names into one command; a non-matching glob and an
// undeclared harness test select nothing.
func w111HarnessCarried(t *testing.T, light bool) {
	t.Helper()
	dir, base := w25e2eFixture(t, w111HarnessFiles())
	w25e2eWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
	w25e2eCommit(t, dir, "change lib")
	args := []string{"--base", base}
	if light {
		args = append([]string{"--light"}, args...)
	}
	r := w403Run(t, dir, args...)
	if r.Mode != "scoped" {
		t.Fatalf("a carried lib change must stay scoped (light=%v), got %q (diagnostics %v)", light, r.Mode, r.Diagnostics)
	}
	want := "E2E_RUN='^(TestCarriedAcceptance)$' sh test/harness.sh"
	got := r.phase("scoped")
	if len(got) != 2 || !strings.Contains(got[0], "./lib") || got[1] != want {
		t.Fatalf("the changed package's own command and the carries-matched harness test are the selection:\n got %v\nwant [go test ./lib, %s]", got, want)
	}
	for _, absent := range []string{"TestCarriedOther", "TestPlainUncarried"} {
		if strings.Contains(strings.Join(got, "\n"), absent) {
			t.Fatalf("%s must not be selected by a lib change:\n%v", absent, got)
		}
	}
}

// TestW111GoHarnessCarriesSelected: the declared harness directory's carries
// declarations are consumed — on the light tier and on the default scoped
// tier alike — and a documentation path matching a glob selects its
// harness test the same way the npm carries layer would.
func TestW111GoHarnessCarries(t *testing.T) {
	w111HarnessCarried(t, false)
	w111HarnessCarried(t, true)

	dir, base := w25e2eFixture(t, w111HarnessFiles())
	w25e2eWrite(t, dir, "docs/guide.md", "# guide 2\n")
	w25e2eCommit(t, dir, "change docs")
	r := w403Run(t, dir, "--light", "--base", base)
	if got := r.phase("scoped"); len(got) != 1 || got[0] != "E2E_RUN='^(TestCarriedOther)$' sh test/harness.sh" {
		t.Fatalf("a changed path matching a harness test's carries glob must select it (the npm carries layer's semantics):\n got %v", got)
	}
}

// TestW111GoHarnessCarriesNeedAMatch: a changed path no glob covers selects
// no harness test.
func TestW111GoHarnessCarriesNeedAMatch(t *testing.T) {
	dir, base := w25e2eFixture(t, w111HarnessFiles())
	w25e2eWrite(t, dir, "other/thing.txt", "y\n")
	w25e2eCommit(t, dir, "change other")
	r := w403Run(t, dir, "--light", "--base", base)
	if got := r.phase("scoped"); len(got) != 0 {
		t.Fatalf("a path covered by no carries declaration and named by no test source must select nothing:\n%v", got)
	}
}

// TestW111GoHarnessCarriesMalformedFailsClosed: a harness test file whose
// opening block carries a malformed declaration (an empty value, a carries
// word without its colon) makes the declarations untrustworthy: the report
// falls back to the full tier, diagnosed — the npm carries layer's rule.
func TestW111GoHarnessCarriesMalformedFailsClosed(t *testing.T) {
	for name, head := range map[string]string{
		"empty value":        "// carries:\n",
		"word without colon": "// carries lib/*\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir, base := w25e2eFixture(t, w111HarnessFiles())
			w25e2eWrite(t, dir, "test/bad_test.go", head+"package e2e\n\nimport \"testing\"\n\nfunc TestBadCarries(t *testing.T) {}\n")
			w25e2eCommit(t, dir, "add bad harness test")
			r := w403Run(t, dir, "--light", "--base", base)
			if r.Mode != "full" {
				t.Fatalf("a malformed carries declaration in the harness directory must fail closed, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
			}
			if !r.diag("carries", "test/bad_test.go", "malformed") {
				t.Fatalf("the diagnostic must name the malformed harness test: %v", r.Diagnostics)
			}
		})
	}
}

// TestW111GoHarnessCarriesNeedAHarness: without a declared go_harness the
// test directory is an ordinary package and carries declarations there are
// consumed by nobody.
func TestW111GoHarnessCarriesNeedAHarness(t *testing.T) {
	files := w111HarnessFiles()
	delete(files, ".polyforge/verify-scope.conf")
	delete(files, "test/harness.sh")
	delete(files, "test/other_test.go")
	delete(files, "test/plain_test.go")
	files["test/accept_test.go"] = "// carries: lib/*\npackage e2e\n\nimport \"testing\"\n\nfunc TestCarriedAcceptance(t *testing.T) {}\n"
	dir, base := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "lib/lib.go", "package lib\n\nfunc Value() int { return 2 }\n")
	w25e2eCommit(t, dir, "change lib")
	r := w403Run(t, dir, "--light", "--base", base)
	if r.Mode != "scoped" {
		t.Fatalf("a repo without a declared harness must stay scoped, got %q (diagnostics %v)", r.Mode, r.Diagnostics)
	}
	joined := strings.Join(r.phase("scoped"), "\n")
	if strings.Contains(joined, "E2E_RUN") || strings.Contains(joined, "TestCarriedAcceptance") {
		t.Fatalf("without a declared go_harness the carries comment of an ordinary test package must select nothing:\n%s", joined)
	}
	if !strings.Contains(joined, "./lib") {
		t.Fatalf("the changed package's own tests must still be selected:\n%s", joined)
	}
}
