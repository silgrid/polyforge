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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
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
	if len(got) != 2 || got[0] != "GOWORK=off go test -count=1 ./lib" || got[1] != want {
		t.Fatalf("the changed package's own command and the carries-matched harness test are the selection:\n got %v\nwant [GOWORK=off go test -count=1 ./lib, %s]", got, want)
	}
	for _, absent := range []string{"TestCarriedOther", "TestPlainUncarried"} {
		if strings.Contains(strings.Join(got, "\n"), absent) {
			t.Fatalf("%s must not be selected by a lib change:\n%v", absent, got)
		}
	}
}

// TestW111GoHarnessCarries: the declared harness directory's carries
// declarations are consumed — on the light tier and on the default scoped
// tier alike — and a documentation path matching a glob selects its
// harness test the same way the npm carries layer would. The deep path pins
// the glob's `*` crossing `/`: a path.Match reading (where `*` stops at a
// separator) would leave docs/sub/page.md uncovered by docs/* and this red.
func TestW111GoHarnessCarries(t *testing.T) {
	w111HarnessCarried(t, false)
	w111HarnessCarried(t, true)

	dir, base := w25e2eFixture(t, w111HarnessFiles())
	w25e2eWrite(t, dir, "docs/sub/page.md", "# deep page\n")
	w25e2eCommit(t, dir, "add a deep doc path")
	r := w403Run(t, dir, "--light", "--base", base)
	if got := r.phase("scoped"); len(got) != 1 || got[0] != "E2E_RUN='^(TestCarriedOther)$' sh test/harness.sh" {
		t.Fatalf("a changed path below a directory a harness test's carries glob names (docs/* covering docs/sub/page.md, `*` crossing `/`) must select it:\n got %v", got)
	}

	dir, base = w25e2eFixture(t, w111HarnessFiles())
	w25e2eWrite(t, dir, "docs/guide.md", "# guide 2\n")
	w25e2eCommit(t, dir, "change docs")
	r = w403Run(t, dir, "--light", "--base", base)
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

// ---------------------------------------------------------------------------
// polyforge#272 U1: the asks record's paths select its harness consumers.
// ---------------------------------------------------------------------------

// The repository files the polyforge#272 fixtures copy their declarations
// from. They are spelled as repository-relative paths on purpose: the
// engine's literal-reference rule then selects this file's tests whenever
// one of them changes, so a change to the 6c/6d headers or to the conf is
// re-judged here.
const (
	w111SixCFile = "test/v2_6c_ask_awaiting_owner_test.go"
	w111SixDFile = "test/v2_6d_owner_relay_test.go"
	w111ConfFile = ".polyforge/verify-scope.conf"
	w111Harness  = "test/e2e-parallel.sh"
)

// w111RealSource reads one repository file by its repository-relative path.
func w111RealSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(w25e2eRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// w111RealHead answers a real harness file's opening comment block: every
// line before its first line of code (blank and `//` lines), verbatim.
func w111RealHead(t *testing.T, rel string) string {
	t.Helper()
	var head strings.Builder
	for _, ln := range strings.SplitAfter(w111RealSource(t, rel), "\n") {
		trimmed := strings.TrimSpace(ln)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			break
		}
		head.WriteString(ln)
	}
	return head.String()
}

// w111RealTestNames answers the top-level `func TestX(t *testing.T)` names
// of a real harness file in declaration order, read with go/parser — an
// implementation of its own, not the engine's regular expression.
func w111RealTestNames(t *testing.T, rel string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), rel, w111RealSource(t, rel), parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var names []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	return names
}

// w111FixtureSuite is a harness test file for a fixture: the real file's
// opening block, then empty tests under the real names.
func w111FixtureSuite(head string, names []string) string {
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("package e2e\n\nimport \"testing\"\n")
	for _, n := range names {
		b.WriteString("\nfunc " + n + "(t *testing.T) {}\n")
	}
	return b.String()
}

// w111RealConf answers the real conf's go_harness=, forced_full= and
// harness full_command= lines (read, never copied by hand).
func w111RealConf(t *testing.T) string {
	t.Helper()
	var keep []string
	harness, full := false, false
	for _, ln := range strings.Split(w111RealSource(t, w111ConfFile), "\n") {
		switch {
		case strings.HasPrefix(ln, "go_harness="):
			harness = harness || ln == "go_harness="+w111Harness
			keep = append(keep, ln)
		case strings.HasPrefix(ln, "forced_full="):
			keep = append(keep, ln)
		case ln == "full_command=GOWORK=off sh "+w111Harness:
			full = true
			keep = append(keep, ln)
		}
	}
	if !harness || !full {
		t.Fatalf("%s must declare go_harness=%s and full_command=GOWORK=off sh %s (got %q)", w111ConfFile, w111Harness, w111Harness, keep)
	}
	return strings.Join(keep, "\n") + "\n"
}

// w111Placeholder is a one-line Go file of the package its directory names.
func w111Placeholder(rel string) string {
	return "package " + path.Base(path.Dir(rel)) + "\n"
}

// w111HarnessSelection answers the test names the report's harness command
// selects (nil when it selects none).
func w111HarnessSelection(t *testing.T, r w403Report) map[string]bool {
	t.Helper()
	prefix, suffix := "E2E_RUN='^(", ")$' sh "+w111Harness
	var sel map[string]bool
	for _, c := range r.phase("scoped") {
		if !strings.HasPrefix(c, prefix) || !strings.HasSuffix(c, suffix) {
			continue
		}
		if sel != nil {
			t.Fatalf("the report must carry one harness command, got another: %q", c)
		}
		sel = map[string]bool{}
		for _, n := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(c, prefix), suffix), "|") {
			sel[n] = true
		}
	}
	return sel
}

// w111ChangeOne commits a fixture, then one change to rel (a new file when
// the fixture lacks it), and answers the engine's report.
func w111ChangeOne(t *testing.T, files map[string]string, rel string, light bool) w403Report {
	t.Helper()
	dir, base := w25e2eFixture(t, files)
	body, ok := files[rel]
	if !ok {
		body = w111Placeholder(rel)
	}
	w25e2eWrite(t, dir, rel, body+"\n// changed by the scenario\n")
	w25e2eCommit(t, dir, "change "+rel)
	args := []string{"--base", base}
	if light {
		args = append([]string{"--light"}, args...)
	}
	return w403Run(t, dir, args...)
}

// w111AskFiles is the asks-record fixture: the asks record and its readers
// and writers as placeholders, and the 6c/6d harness files with their real
// opening blocks. dropSixD names a 6d header line fragment the negative
// control removes.
func w111AskFiles(t *testing.T, sixC, sixD []string, dropSixD string) map[string]string {
	t.Helper()
	files := map[string]string{
		"go.mod":     "module fixture.test/w111ask\n\ngo 1.21\n",
		"docs/x.md":  "# x\n",
		w111Harness:  "#!/bin/sh\nexit 0\n",
		w111ConfFile: w111RealConf(t),
		w111SixCFile: w111FixtureSuite(w111RealHead(t, w111SixCFile), sixC),
	}
	for _, rel := range []string{
		"internal/common/capsule/asks.go",
		"internal/polyforge/driver/ask.go",
		"internal/polyforge/driver/relay.go",
		"internal/polyforge/driver/autoanswer.go",
		"internal/polyforge/driver/ownermsg.go",
		"internal/polyforge/daemon/stepcall.go",
		"internal/polyforge/daemon/answer.go",
		"internal/polyforge/runtime/runtime.go",
	} {
		files[rel] = w111Placeholder(rel)
	}
	head := w111RealHead(t, w111SixDFile)
	if dropSixD != "" {
		var kept []string
		for _, ln := range strings.SplitAfter(head, "\n") {
			if !strings.Contains(ln, dropSixD) {
				kept = append(kept, ln)
			}
		}
		head = strings.Join(kept, "")
	}
	files[w111SixDFile] = w111FixtureSuite(head, sixD)
	return files
}

// TestW111GoHarnessAskPathsSelectConsumers (polyforge#272 AC1): a change
// confined to the asks record's format or to a path that reads or writes it
// selects both asks harness suites — v2_6c (the ask and the awaiting wait)
// and v2_6d (the owner's reply and its delivery) — on the light tier; on
// the default tier the asks record itself stays scoped with both, and the
// driver/daemon paths take the full tier with the whole harness. The
// expected names are parsed from the real suites; the real 6c/6d opening
// blocks and the real conf are the declarations under test.
func TestW111GoHarnessAskPathsSelectConsumers(t *testing.T) {
	sixC := w111RealTestNames(t, w111SixCFile)
	sixD := w111RealTestNames(t, w111SixDFile)
	nC, nD := 0, 0
	for _, n := range sixC {
		if strings.HasPrefix(n, "TestV26c") {
			nC++
		}
	}
	for _, n := range sixD {
		if strings.HasPrefix(n, "TestV26d") {
			nD++
		}
	}
	if len(sixC)+len(sixD) != 8 || nC != 2 || nD != 6 {
		t.Fatalf("the asks harness is 8 tests — 2 TestV26c in %s, 6 TestV26d in %s (polyforge#272 analysis §2); parsed %v and %v", w111SixCFile, w111SixDFile, sixC, sixD)
	}
	want := append(append([]string{}, sixC...), sixD...)
	files := w111AskFiles(t, sixC, sixD, "")

	mustSelect := func(r w403Report, label string, names []string) {
		t.Helper()
		if r.Mode != "scoped" {
			t.Fatalf("%s: the change must stay scoped, got %q (diagnostics %v)", label, r.Mode, r.Diagnostics)
		}
		sel := w111HarnessSelection(t, r)
		for _, n := range names {
			if !sel[n] {
				t.Fatalf("%s: the harness selection must select %s (the asks record's consumer):\n%v", label, n, r.phase("scoped"))
			}
		}
	}
	mustNotSelect := func(r w403Report, label string, names []string) {
		t.Helper()
		sel := w111HarnessSelection(t, r)
		for _, n := range names {
			if sel[n] {
				t.Fatalf("%s: the harness selection must not select %s:\n%v", label, n, r.phase("scoped"))
			}
		}
	}

	// Light tier: the record, every reader and writer, and a new file of the
	// same family each select all 8.
	for _, rel := range []string{
		"internal/common/capsule/asks.go",
		"internal/polyforge/driver/ask.go",
		"internal/polyforge/driver/relay.go",
		"internal/polyforge/driver/autoanswer.go",
		"internal/polyforge/daemon/stepcall.go",
		"internal/polyforge/daemon/answer.go",
		"internal/polyforge/driver/ownermsg.go",
		"internal/common/capsule/asks_more.go",
		"internal/polyforge/driver/autoanswer_more.go",
	} {
		mustSelect(w111ChangeOne(t, files, rel, true), "light "+rel, want)
	}

	// Default tier: the record stays scoped with all 8; driver and daemon
	// paths are forced to the full tier, whose harness runs unfiltered.
	mustSelect(w111ChangeOne(t, files, "internal/common/capsule/asks.go", false), "default internal/common/capsule/asks.go", want)
	for _, rel := range []string{
		"internal/polyforge/driver/ask.go",
		"internal/polyforge/driver/relay.go",
		"internal/polyforge/driver/autoanswer.go",
		"internal/polyforge/driver/ownermsg.go",
		"internal/polyforge/daemon/stepcall.go",
		"internal/polyforge/daemon/answer.go",
	} {
		r := w111ChangeOne(t, files, rel, false)
		if r.Mode != "full" {
			t.Fatalf("default %s: a forced_full path must take the full tier, got %q (diagnostics %v)", rel, r.Mode, r.Diagnostics)
		}
		harness := false
		for _, c := range r.phase("full") {
			if strings.Contains(c, "E2E_RUN") {
				t.Fatalf("default %s: the full tier's harness runs unfiltered, got %q", rel, c)
			}
			harness = harness || c == "GOWORK=off sh "+w111Harness
		}
		if !harness {
			t.Fatalf("default %s: the full tier must run GOWORK=off sh %s:\n%v", rel, w111Harness, r.phase("full"))
		}
	}

	// Negative cases: an unrelated doc selects none of the 8; runtime.go
	// (the owner's 2B ruling: not declared) selects no 6d test; and without
	// 6d's asks declaration the record no longer reaches 6d — the positive
	// cases rest on the declaration itself.
	mustNotSelect(w111ChangeOne(t, files, "docs/x.md", true), "light docs/x.md", want)
	mustNotSelect(w111ChangeOne(t, files, "internal/polyforge/runtime/runtime.go", true), "light internal/polyforge/runtime/runtime.go", sixD)
	dropped := w111AskFiles(t, sixC, sixD, "internal/common/capsule/asks")
	mustNotSelect(w111ChangeOne(t, dropped, "internal/common/capsule/asks.go", true), "light internal/common/capsule/asks.go without 6d's asks declaration", sixD)
}

// ---------------------------------------------------------------------------
// polyforge#272 U2: common/capsule's other persisted formats select the
// harness suites that accept them.
// ---------------------------------------------------------------------------

// The harness suites whose acceptance reads one of common/capsule's
// persisted formats (selection-audit.md B, the owner's 3A ruling), spelled
// repository-relative for the literal-reference rule as above.
const (
	w111PF25File   = "test/pf25_step_overrides_test.go"
	w111V1S27File  = "test/v1_s27_gate_fail_verdict_completed_test.go"
	w111W159File   = "test/w159_harness_acceptance_test.go"
	w111CapsuleDir = "internal/common/capsule/"
)

// TestW111GoHarnessCapsuleFormatsSelectConsumers (polyforge#272 AC1): a
// change to the step-overrides record, the capsule record or the spawn
// journal selects, on the light tier, every test of the harness suite that
// accepts that format (pf25, v1_s27, w159); a new file of the overrides or
// spawn-journal family does the same; the capsule package's files no
// harness suite reads (epicchildren, subagents) select none of them. The
// expected names are parsed from the real suites; their real opening blocks
// are the declarations under test.
func TestW111GoHarnessCapsuleFormatsSelectConsumers(t *testing.T) {
	consumers := map[string][]string{}
	for _, rel := range []string{w111PF25File, w111V1S27File, w111W159File} {
		names := w111RealTestNames(t, rel)
		if len(names) == 0 {
			t.Fatalf("%s declares no Test function", rel)
		}
		consumers[rel] = names
	}
	files := map[string]string{
		"go.mod":     "module fixture.test/w111fmt\n\ngo 1.21\n",
		w111Harness:  "#!/bin/sh\nexit 0\n",
		w111ConfFile: w111RealConf(t),
	}
	for rel, names := range consumers {
		files[rel] = w111FixtureSuite(w111RealHead(t, rel), names)
	}
	for _, f := range []string{"overrides.go", "capsule.go", "spawnjournal.go", "epicchildren.go", "subagents.go"} {
		files[w111CapsuleDir+f] = w111Placeholder(w111CapsuleDir + f)
	}

	for _, c := range []struct{ change, consumer string }{
		{"overrides.go", w111PF25File},
		{"capsule.go", w111V1S27File},
		{"spawnjournal.go", w111W159File},
		{"overrides_more.go", w111PF25File},
		{"spawnjournal_more.go", w111W159File},
	} {
		rel := w111CapsuleDir + c.change
		r := w111ChangeOne(t, files, rel, true)
		if r.Mode != "scoped" {
			t.Fatalf("light %s: the change must stay scoped, got %q (diagnostics %v)", rel, r.Mode, r.Diagnostics)
		}
		sel := w111HarnessSelection(t, r)
		for _, n := range consumers[c.consumer] {
			if !sel[n] {
				t.Fatalf("light %s: the harness selection must select %s (%s accepts that format):\n%v", rel, n, c.consumer, r.phase("scoped"))
			}
		}
	}
	for _, f := range []string{"epicchildren.go", "subagents.go"} {
		rel := w111CapsuleDir + f
		sel := w111HarnessSelection(t, w111ChangeOne(t, files, rel, true))
		for consumer, names := range consumers {
			for _, n := range names {
				if sel[n] {
					t.Fatalf("light %s: no harness suite reads that file, yet %s (%s) is selected", rel, n, consumer)
				}
			}
		}
	}
}
