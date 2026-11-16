// carries: .polyforge/verify-scope.conf, test/*_contract_test.go, internal/contracttest/*_contract_test.go
package test

// contract_scan_registry_contract_test.go: the whole-repo contract
// tests' one-place registration (polyforge#66). The contract tests that
// scan repository files — they read only repository files, start no
// process and need no built artifact — are selected by no package
// analysis: a change to a file they read picks them nowhere, so only
// the widest bar reached them. They live in this one package, and the
// conf's named fast check runs the package whole, in every tier: the
// light tier's fast phase, build's fast checks, review's host replay,
// the micro lane's (whose micro_check= lines keep only what this check
// does not run) and the full tier's. A new contract test is one change:
// the file lands here and nothing else moves — the command selects by
// package, not by name, so neither the conf nor the frozen seed
// changes with a test.
//
// This suite is the anti-drift guard, rewritten for that convention: a
// test/*_contract_test.go Test function outside this package must sit
// in the exclusion register with its reason (the standard for staying
// out: not reading only repository files, starting a process or needing
// a built artifact), an exclusion that names no outside test is stale,
// the package must hold the tests the check runs, and a micro_check=
// line must not repeat one. The package sits outside test/ and its
// import path does not end in /test on purpose: the verify-scope engine
// and the frozen seed run every test/*.go file's tests through the
// acceptance harness (E2E_RUN), which compiles package test only, and a
// contract test under test/ would turn a text-reference selection into
// an E2E_RUN naming a test that package no longer defines; and the full
// tier's unit command — `go test $(go list ./... | grep -v
// '/test$')`, the one `make test` and the CI full-bar mirror — carries
// every package whose import path does not end in /test, so the
// contract tests stay in the full bar exactly as they were, beside
// the check that runs them in every tier's fast phase.
//
// Run (targeted): GOWORK=off go test ./internal/contracttest/ -count=1

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// contractScanName is the conf's declared fast check; contractScanCmd is
// the exact command shape it must keep: the whole contract package, no
// per-test -run list. Whoever changes the command updates this regexp
// in the same change.
const (
	contractScanName = "contract-scan"
	contractScanCmd  = `^GOWORK=off go test \./internal/contracttest/ -count=1$`
)

// contractScanPkg is the contract package's repository-relative
// directory; contractScanOutside is the directory a contract test may
// leave only through the exclusion register.
const (
	contractScanPkg     = `internal/contracttest`
	contractScanOutside = `test`
)

// contractScanExcluded is the register of Test functions a
// test/*_contract_test.go file carries that the whole-repo check must
// not run, each with its reason: the standard is reading only
// repository files, starting no process and needing no built artifact.
// It shrinks only: an entry leaves with its test.
var contractScanExcluded = map[string]string{
	"TestW320AuditEngineConfCannotReach":          "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
	"TestW320AuditEngineGoWithNpmTree":            "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
	"TestW320AuditEngineNestedRootReadsTopLedger": "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
	"TestW320AuditEngineNoAnalyzer":               "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
	"TestW320AuditEngineNpmTrees":                 "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
	"TestW320AuditEngineSelection":                "spawns the shipped audit engine as a subprocess: the whole-repo check starts no process",
}

// contractScanFuncRe matches a top-level Test function declaration.
var contractScanFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

// contractScanTestFuncs answers the Test functions the *_contract_test.go
// files of root's dir declare, keyed by name, with the file that
// carries each, relative to root.
func contractScanTestFuncs(root, dir string) map[string]string {
	files, err := filepath.Glob(filepath.Join(root, dir, "*_contract_test.go"))
	if err != nil {
		return map[string]string{}
	}
	found := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		rel := dir + "/" + filepath.Base(f)
		for _, m := range contractScanFuncRe.FindAllStringSubmatch(string(raw), -1) {
			found[m[1]] = rel
		}
	}
	return found
}

// contractScanViolations answers every way the repository layout at
// root violates the one-place convention, one message each: the conf's
// contract-scan declaration and command shape, the *_contract_test.go
// tests outside the contract package, and the package's own emptiness,
// judged against the exclusion register the caller passes (the guard's
// own register for the real repository, a layout's own for a synthetic
// one). It reads files only, so the guard can run it on synthetic
// layouts too.
func contractScanViolations(root string, excluded map[string]string) []string {
	var v []string
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		return []string{"the repository conf must exist: " + err.Error()}
	}
	var decl []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "check="+contractScanName+"|") {
			decl = append(decl, ln)
		}
	}
	if len(decl) != 1 {
		return append(v, fmt.Sprintf("the conf must declare the whole-repo contract check exactly once, as check=%s|fast|<command>; got %d declaration(s)", contractScanName, len(decl)))
	}
	if !strings.HasPrefix(decl[0], "check="+contractScanName+"|fast|") {
		return append(v, fmt.Sprintf("the whole-repo contract check must be a fast-phase check (check=%s|fast|<command>, so every tier runs it), got %q", contractScanName, decl[0]))
	}
	cmd := strings.TrimPrefix(decl[0], "check="+contractScanName+"|fast|")
	if regexp.MustCompile(contractScanCmd).FindStringSubmatch(cmd) == nil {
		v = append(v, fmt.Sprintf("the %s command must select the whole contract package, exactly `GOWORK=off go test ./%s/ -count=1` — a per-test -run list is the two-places maintenance (conf plus frozen seed) this package removed; whoever changes the command updates the guard in the same change, got %q", contractScanName, contractScanPkg, cmd))
	}

	outside := contractScanTestFuncs(root, contractScanOutside)
	inside := contractScanTestFuncs(root, contractScanPkg)
	names := make([]string, 0, len(outside))
	for n := range outside {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := excluded[n]; !ok {
			v = append(v, fmt.Sprintf("%s: %s sits outside the contract package with no exclusion — a contract test that reads only repository files, starts no process and needs no built artifact goes in %s/ and needs no registration anywhere; anything else joins the exclusion register with its reason", outside[n], n, contractScanPkg))
		}
	}
	excludedNames := make([]string, 0, len(excluded))
	for n := range excluded {
		excludedNames = append(excludedNames, n)
	}
	sort.Strings(excludedNames)
	for _, n := range excludedNames {
		switch {
		case outside[n] != "":
		case inside[n] != "":
			v = append(v, fmt.Sprintf("the exclusion register lists %q (%s), which the contract package already selects: drop the entry", n, excluded[n]))
		default:
			v = append(v, fmt.Sprintf("the exclusion register lists %q (%s), which no %s/*_contract_test.go file defines: the entry leaves with its test", n, excluded[n], contractScanOutside))
		}
	}
	if len(inside) == 0 {
		v = append(v, fmt.Sprintf("the contract package %s holds no *_contract_test.go test: the %s check would run nothing", contractScanPkg, contractScanName))
	}
	return v
}

// TestContractScanRegistryCoversEveryContractTest: the conf's
// contract-scan fast check selects the whole contract package, every
// contract test outside it is excluded with a reason, and the register
// carries nothing stale.
func TestContractScanRegistryCoversEveryContractTest(t *testing.T) {
	for _, v := range contractScanViolations(v1Root(t), contractScanExcluded) {
		t.Error(v)
	}
}

// TestContractScanCatchesAMisplacedContractTest: the guard's scan
// reports a contract test left outside the package, and stays silent on
// a conforming layout and on an outside test excluded with its reason
// — the catch itself, on synthetic repositories (the real one
// conforms, so only these pin the reporting).
func TestContractScanCatchesAMisplacedContractTest(t *testing.T) {
	layout := func(t *testing.T, stray string) string {
		t.Helper()
		root := t.TempDir()
		conf := "check=" + contractScanName + "|fast|GOWORK=off go test ./" + contractScanPkg + "/ -count=1\n"
		if err := os.MkdirAll(filepath.Join(root, ".polyforge"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".polyforge", "verify-scope.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Join(root, filepath.FromSlash(contractScanPkg))
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "ok_contract_test.go"), []byte("package test\n\nfunc TestOk(t *testing.T) {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if stray != "" {
			p := filepath.Join(root, filepath.FromSlash(stray))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("package test\n\nfunc TestStray(t *testing.T) {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	t.Run("a contract test outside the package is reported", func(t *testing.T) {
		v := contractScanViolations(layout(t, "test/stray_contract_test.go"), map[string]string{})
		if len(v) != 1 || !strings.Contains(v[0], "test/stray_contract_test.go") || !strings.Contains(v[0], "TestStray") {
			t.Fatalf("the guard must report exactly the misplaced contract test (its file and its name); got %d violation(s):\n%s", len(v), strings.Join(v, "\n"))
		}
	})
	t.Run("a conforming layout stays silent", func(t *testing.T) {
		if v := contractScanViolations(layout(t, ""), map[string]string{}); len(v) != 0 {
			t.Fatalf("the guard must report nothing on a conforming layout; got:\n%s", strings.Join(v, "\n"))
		}
	})
	t.Run("an outside test with a stated exclusion stays silent", func(t *testing.T) {
		v := contractScanViolations(layout(t, "test/stray_contract_test.go"), map[string]string{"TestStray": "starts a process: the whole-repo check starts no process"})
		if len(v) != 0 {
			t.Fatalf("the guard must report nothing when the outside test is excluded with its reason; got:\n%s", strings.Join(v, "\n"))
		}
	})
}

// TestMicroCheckKeepsNothingTheContractScanCheckRuns: the micro lane
// runs the fast phase (the contract-scan check among it) before its
// micro_check= commands, so a micro_check= -run case that repeats what
// the check already runs is a duplicate run, not coverage.
func TestMicroCheckKeepsNothingTheContractScanCheckRuns(t *testing.T) {
	root := v1Root(t)
	inside := contractScanTestFuncs(root, contractScanPkg)
	if len(inside) == 0 {
		t.Fatalf("the contract package %s holds no test for the micro comparison", contractScanPkg)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	micro := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(ln, "micro_check=") {
			continue
		}
		micro++
		cmd := strings.TrimPrefix(ln, "micro_check=")
		i := strings.Index(cmd, " -run '")
		if i < 0 {
			continue // a command that names no test cannot repeat one
		}
		rest := cmd[i+len(" -run '"):]
		j := strings.Index(rest, "'")
		if j < 0 {
			t.Fatalf("micro_check line %d carries an unterminated -run pattern: %q", micro, ln)
		}
		re, err := regexp.Compile("^(?:" + rest[:j] + ")$")
		if err != nil {
			t.Fatalf("micro_check line %d carries a pattern that is not a Go regexp: %v", micro, err)
		}
		for n := range inside {
			if re.MatchString(n) {
				t.Errorf("micro_check line %d runs %s, which the %s fast check already runs (the micro lane runs the fast phase first): drop it from micro_check=", micro, n, contractScanName)
			}
		}
	}
	if micro == 0 {
		t.Fatal("the conf declares no micro_check= line to compare against the contract-scan check")
	}
}
