// carries: .polyforge/verify-scope.conf, test/*_contract_test.go
package test

// contract_scan_registry_contract_test.go: the whole-repo contract
// tests' registration in the conf. The contract tests under this
// directory that scan repository files — they read only repository
// files, start no process and need no built artifact — are pinned
// nowhere by package analysis: a change to a file they read selects
// them nowhere, so only the integration run's full bar reached them.
// The conf therefore runs them as one named fast check,
// `check=contract-scan|fast|…`, whose fast phase every tier runs (the
// light tier's, build's fast checks, review's host replay and the micro
// lane's — its micro_check= lines carry only what the check does not).
// This suite is the anti-drift guard: every Test function a
// test/*_contract_test.go file carries is either selected by that
// check or excluded below with a reason, and every name the check
// lists exists — a new contract test that is not registered turns this
// red, and so does a name left behind by a removed or renamed test.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestContractScan' -count=1

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// contractScanName is the conf's declared fast check; contractScanCmd is
// the exact command shape it must keep. The guard parses the test names
// out of the -run alternation, so a different command shape turns it
// red: whoever changes the command updates this regexp in the same
// change.
const (
	contractScanName = "contract-scan"
	contractScanCmd  = `^GOWORK=off go test \./test/ -run '\^\(([A-Za-z0-9_|]+)\)\$' -count=1$`
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

// contractScanAlternation reads the conf and returns the -run
// alternation of its contract-scan check: the plain |separated test
// names it runs. It fails the test when the check is not declared
// exactly once, is not a fast check or does not keep the command shape.
func contractScanAlternation(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatalf("the repository conf must exist: %v", err)
	}
	var decl []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "check="+contractScanName+"|") {
			decl = append(decl, ln)
		}
	}
	if len(decl) != 1 {
		t.Fatalf("the conf must declare the whole-repo contract check exactly once, as check=%s|fast|<command>; got %d declaration(s)", contractScanName, len(decl))
	}
	if !strings.HasPrefix(decl[0], "check="+contractScanName+"|fast|") {
		t.Fatalf("the whole-repo contract check must be a fast-phase check (check=%s|fast|<command>, so every tier runs it), got %q", contractScanName, decl[0])
	}
	cmd := strings.TrimPrefix(decl[0], "check="+contractScanName+"|fast|")
	m := regexp.MustCompile(contractScanCmd).FindStringSubmatch(cmd)
	if m == nil {
		t.Fatalf("the %s command must keep the shape GOWORK=off go test ./test/ -run '^(Name|Name|…)$' -count=1 (plain |alternated Test names, so this guard can parse them), got %q", contractScanName, cmd)
	}
	return strings.Split(m[1], "|")
}

// TestContractScanRegistryCoversEveryContractTest: the conf's
// contract-scan fast check runs every contract test that scans the
// repository, and only those — each one either listed or excluded with
// a reason, each listed name a real test.
func TestContractScanRegistryCoversEveryContractTest(t *testing.T) {
	listed := contractScanAlternation(t)
	listedSet := make(map[string]bool, len(listed))
	nameRe := regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	for _, n := range listed {
		if !nameRe.MatchString(n) {
			t.Errorf("the %s alternation carries %q: plain Test names only (no wildcards), so every selected test is one line of drift away from a red", contractScanName, n)
		}
		if listedSet[n] {
			t.Errorf("the %s alternation lists %q twice", contractScanName, n)
		}
		listedSet[n] = true
	}

	files, err := filepath.Glob(filepath.Join(v1Root(t), "test", "*_contract_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no test/*_contract_test.go files found under the repository root: %v", err)
	}
	funcRe := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	discovered := make(map[string]string) // test name -> its file, relative
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, m := range funcRe.FindAllStringSubmatch(string(raw), -1) {
			discovered[m[1]] = filepath.Join("test", filepath.Base(f))
		}
	}

	names := make([]string, 0, len(discovered))
	for n := range discovered {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		reason, excluded := contractScanExcluded[n]
		switch {
		case listedSet[n] && excluded:
			t.Errorf("%s: %s is both run by the %s check and in the exclusion register (%s): pick one", discovered[n], n, contractScanName, reason)
		case listedSet[n]:
			// run by the check on every tier: registered.
		case excluded:
			// kept out of the check for a stated reason: registered.
		default:
			t.Errorf("%s: %s is neither run by the conf's %s check nor in the exclusion register — a contract test that reads only repository files, starts no process and needs no built artifact joins the check's -run alternation in the same change, anything else joins the exclusion register with its reason", discovered[n], n, contractScanName)
		}
	}
	for _, n := range listed {
		if _, ok := discovered[n]; !ok {
			t.Errorf("the %s alternation lists %q, which no test/*_contract_test.go file defines: register real tests only", contractScanName, n)
		}
	}
	for n, reason := range contractScanExcluded {
		if _, ok := discovered[n]; !ok {
			t.Errorf("the exclusion register lists %q (%s), which no test/*_contract_test.go file defines: the entry leaves with its test", n, reason)
		}
	}
}

// TestMicroCheckKeepsNothingTheContractScanCheckRuns: the micro lane
// runs the fast phase (the contract-scan check among it) before its
// micro_check= commands, so a micro_check= -run case that repeats what
// the check already runs is a duplicate run, not coverage — it left the
// lines when the check took the contract tests over.
func TestMicroCheckKeepsNothingTheContractScanCheckRuns(t *testing.T) {
	listedList := contractScanAlternation(t)
	raw, err := os.ReadFile(filepath.Join(v1Root(t), ".polyforge", "verify-scope.conf"))
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
		for _, n := range listedList {
			if re.MatchString(n) {
				t.Errorf("micro_check line %d runs %s, which the %s fast check already runs (the micro lane runs the fast phase first): drop it from micro_check=", micro, n, contractScanName)
			}
		}
	}
	if micro == 0 {
		t.Fatal("the conf declares no micro_check= line to compare against the contract-scan check")
	}
}
