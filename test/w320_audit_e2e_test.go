// carries: workflows/_shared/scripts/audit-deps

package test

// w320_audit_e2e_test.go: the authored end-to-end scenarios for the inbound
// dependency-vulnerability audit gate (the spec's AC1 and AC6). They drive the
// release-shipped gate helper `workflows/_shared/scripts/audit-deps` only through its documented
// command line (D3: `[--go] [--npm <dir>]... [--ledger <path>]`) and the
// tools' own real interfaces — the live npm registry, the public GHSA data it
// serves, `govulncheck` against vuln.go.dev, and a closed loopback port for
// unreachability. Nothing here reads an implementation internal or a recorded
// tool-output stub.
//
// AC1: a real vulnerable npm fixture (lodash 4.17.20) turns the gate red with
// a `finding:` diagnostic, and a valid owner-ruled exemption row for every
// high/critical advisory the registry reports turns the same run green.
//
// AC6: an absent audit tool and an unreachable advisory source each turn the
// gate red and name their own cause (`tool-missing:` vs `db-unreachable:`),
// never a finding and never the other cause. Missing npm and missing
// govulncheck are both exercised; both audit surfaces are covered.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^(TestW320E2EAC1|TestW320E2EAC6)$' -count=1 -v
//
// Every scenario prints one `PASS S<n>` marker line when it passes, so the
// verification step can bind each scenario to its own output line.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w320EEdge is one high/critical advisory the real npm advisory source
// reports for the fixture, keyed the way the write-time ledger grammar keys a
// row: advisory id plus package.
type w320EEdge struct {
	ID      string
	Package string
}

// w320EHelperPath pins the release-shipped gate helper. A missing helper is
// the pre-implementation baseline red, exactly like a missing engine file in
// the repository's other authored acceptance suites.
func w320EHelperPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "workflows", "_shared", "scripts", "audit-deps"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("the release tree must ship the dependency-audit gate helper workflows/_shared/scripts/audit-deps (the pre-implementation tree has none): %v", err)
	}
	if info.IsDir() {
		t.Fatalf("workflows/_shared/scripts/audit-deps must be the gate helper file, not a directory")
	}
	return p
}

// w320EEnv answers the process environment with the overrides replacing, not
// shadowing, any existing key.
func w320EEnv(overrides ...string) []string {
	drop := map[string]bool{}
	for _, kv := range overrides {
		k, _, _ := strings.Cut(kv, "=")
		drop[k] = true
	}
	out := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[k] {
			out = append(out, kv)
		}
	}
	return append(out, overrides...)
}

// w320EWrite writes one fixture file under dir.
func w320EWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// w320EHelper runs the gate helper through its documented `sh` invocation,
// with cwd at the audited tree, and answers stdout/stderr/exit.
func w320EHelper(t *testing.T, dir string, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{w320EHelperPath(t)}, args...)...)
	cmd.Dir = dir
	cmd.Env = w320EEnv(env...)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run workflows/_shared/scripts/audit-deps %v in %s: %v", args, dir, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), errb.String(), code
}

// w320EToolBin builds a PATH directory that carries the named tools and a
// small utility set, omitting only the tools named in omit. It is how the
// scenarios make one audit tool absent without making the host's other tools
// absent too.
func w320EToolBin(t *testing.T, omit ...string) string {
	t.Helper()
	skip := map[string]bool{}
	for _, name := range omit {
		skip[name] = true
	}
	bin := t.TempDir()
	names := []string{
		"python3", "go", "npm", "node", "govulncheck", "sh",
		"mktemp", "rm", "cat", "sed", "grep", "tr", "date", "uname", "env",
		"dirname", "basename", "awk", "sort", "head", "tail", "wc", "cut",
		"find", "xargs", "cp", "mv", "mkdir", "chmod", "readlink", "realpath",
		"timeout", "git",
	}
	for _, name := range names {
		if skip[name] {
			continue
		}
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	return bin
}

// w320ENpmFixture writes a real npm tree whose lockfile pins a dependency the
// public advisory data marks high/critical today. The lockfile is generated by
// the project's own npm, so the scenario audits the registry's real data.
func w320ENpmFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("npm"); err != nil {
		t.Fatalf("the AC1 scenario needs npm on PATH to build and audit its fixture: %v", err)
	}
	dir := t.TempDir()
	w320EWrite(t, dir, "package.json", ""+
		"{\n  \"name\": \"w320-audit-fixture\",\n  \"version\": \"1.0.0\",\n  \"private\": true,\n  \"dependencies\": {\"lodash\": \"4.17.20\"}\n}\n")
	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund")
	cmd.Dir = dir
	cmd.Env = w320EEnv("npm_config_cache=" + filepath.Join(dir, ".npm-cache"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the npm fixture lockfile with npm (%v):\n%s", err, out)
	}
	return dir
}

// w320ENpmHighFindings asks the live registry (through the project's own npm
// audit) which high/critical advisories the fixture tree carries. This is the
// scenario's external oracle: the ids and packages come from the public
// advisory data, never from a recorded stub.
func w320ENpmHighFindings(t *testing.T, dir string) []w320EEdge {
	t.Helper()
	cmd := exec.Command("npm", "audit", "--json", "--audit-level=high", "--package-lock-only")
	cmd.Dir = dir
	cmd.Env = w320EEnv("npm_config_cache=" + filepath.Join(dir, ".npm-cache"))
	out, err := cmd.Output()
	var report struct {
		Error           json.RawMessage `json:"error"`
		Vulnerabilities map[string]struct {
			Via []json.RawMessage `json:"via"`
		} `json:"vulnerabilities"`
	}
	if uerr := json.Unmarshal(out, &report); uerr != nil {
		t.Fatalf("npm audit --json did not return its JSON report (run error %v): %v\n%s", err, uerr, out)
	}
	if len(report.Error) != 0 {
		t.Fatalf("npm audit's advisory source was unavailable, so AC1's real oracle could not answer: %s", report.Error)
	}
	seen := map[string]bool{}
	var edges []w320EEdge
	for pkg, v := range report.Vulnerabilities {
		for _, raw := range v.Via {
			var via struct {
				Name     string `json:"name"`
				Severity string `json:"severity"`
				URL      string `json:"url"`
			}
			if json.Unmarshal(raw, &via) != nil {
				continue // a string entry names a dependent package, not an advisory
			}
			if via.Severity != "high" && via.Severity != "critical" {
				continue
			}
			if via.Name != "" {
				pkg = via.Name
			}
			id := via.URL
			if i := strings.LastIndex(id, "/"); i >= 0 {
				id = id[i+1:]
			}
			if !strings.HasPrefix(id, "GHSA-") {
				continue
			}
			key := id + "\x00" + pkg
			if seen[key] {
				continue
			}
			seen[key] = true
			edges = append(edges, w320EEdge{ID: id, Package: pkg})
		}
	}
	if len(edges) == 0 {
		t.Fatalf("the live advisory data reports no high/critical advisory for the fixture tree, so AC1's vulnerable fixture no longer proves red")
	}
	return edges
}

// w320ELedger writes one valid owner-ruled, in-window exemption row for every
// high/critical advisory the live source reported, and answers its path.
func w320ELedger(t *testing.T, dir string, edges []w320EEdge) string {
	t.Helper()
	now := time.Now().UTC()
	from := now.Format("2006-01-02")
	expires := now.AddDate(0, 0, 30).Format("2006-01-02")
	var b strings.Builder
	b.WriteString("# e2e fixture ledger: rows generated from the live advisory source\n")
	for _, e := range edges {
		fmt.Fprintf(&b, "exempt=%s|%s|e2e fixture|owner ruling 2026-11-04 e2e|%s|%s\n", e.ID, e.Package, from, expires)
	}
	p := filepath.Join(dir, "e2e-audit-exemptions.conf")
	w320EWrite(t, dir, "e2e-audit-exemptions.conf", b.String())
	return p
}

// w320ERequirePrefix asserts one diagnostic cause is present and the other
// two audit-gate causes are not, so the gate names the reason instead of
// silently passing or reporting a finding.
func w320ERequirePrefix(t *testing.T, label, errb, want string, absent ...string) {
	t.Helper()
	if !strings.Contains(errb, want) {
		t.Fatalf("%s: the gate must report %q on stderr, got:\n%s", label, want, errb)
	}
	for _, a := range absent {
		if strings.Contains(errb, a) {
			t.Fatalf("%s: the gate reported %q as well as %q; the causes must be distinct:\n%s", label, a, want, errb)
		}
	}
}

// TestW320E2EAC1: a vulnerable fixture turns the audit gate red with a
// finding, and a valid owner-ruled exemption row for every high/critical
// advisory the public advisory source reports turns the same tree green.
//
// AC1's oracle is the live public advisory data read through the project's
// own npm audit (no recorded stub), so a newly published high/critical
// advisory on the unchanged fixture keeps the red proof real.
func TestW320E2EAC1(t *testing.T) {
	dir := w320ENpmFixture(t)
	edges := w320ENpmHighFindings(t, dir)

	_, errb, code := w320EHelper(t, dir, nil, "--npm", ".")
	if code != 1 {
		t.Fatalf("AC1: an unexempted high/critical finding must fail the audit gate (exit 1), got exit %d (stderr %q)", code, errb)
	}
	w320ERequirePrefix(t, "AC1 red", errb, "finding:", "tool-missing:", "db-unreachable:")

	ledger := w320ELedger(t, dir, edges)
	_, errb, code = w320EHelper(t, dir, nil, "--npm", ".", "--ledger", ledger)
	if code != 0 {
		t.Fatalf("AC1: the same tree with a valid, unexpired owner-ruled exemption row for every reported high/critical advisory must pass the gate, got exit %d (stderr %q)", code, errb)
	}
	if strings.Contains(errb, "finding:") {
		t.Fatalf("AC1: an exempted finding must not be reported as a finding (stderr %q)", errb)
	}
	fmt.Println("PASS S1")
}

// TestW320E2EAC6: a missing audit tool and an unreachable advisory source each
// turn the audit gate red, and the diagnostic distinguishes `tool-missing:`
// from `db-unreachable:` (and from the `finding:` AC1 covers). Both audit
// surfaces are exercised: npm for tool-missing and registry unreachability,
// govulncheck for tool-missing and vuln.go.dev unreachability.
func TestW320E2EAC6(t *testing.T) {
	npmDir := w320ENpmFixture(t)

	// Missing npm: PATH carries the interpreter and the fixture tools, not npm.
	npmPath := w320EToolBin(t, "npm")
	_, errb, code := w320EHelper(t, npmDir, []string{"PATH=" + npmPath}, "--npm", ".")
	if code != 1 {
		t.Fatalf("AC6 missing npm: the gate must fail (exit 1), got exit %d (stderr %q)", code, errb)
	}
	w320ERequirePrefix(t, "AC6 missing npm", errb, "tool-missing:", "db-unreachable:", "finding:")
	if !strings.Contains(errb, "npm") {
		t.Fatalf("AC6 missing npm: the diagnostic must name the missing tool, got %q", errb)
	}

	// Missing govulncheck: PATH carries go and the fixture tools, not govulncheck.
	goDir := t.TempDir()
	w320EWrite(t, goDir, "go.mod", "module w320-audit-e2e\n\ngo 1.26\n")
	w320EWrite(t, goDir, "main.go", "package main\n\nfunc main() {}\n")
	goPath := w320EToolBin(t, "govulncheck")
	_, errb, code = w320EHelper(t, goDir, []string{"PATH=" + goPath, "GOWORK=off"}, "--go")
	if code != 1 {
		t.Fatalf("AC6 missing govulncheck: the gate must fail (exit 1), got exit %d (stderr %q)", code, errb)
	}
	w320ERequirePrefix(t, "AC6 missing govulncheck", errb, "tool-missing:", "db-unreachable:", "finding:")
	if !strings.Contains(errb, "govulncheck") {
		t.Fatalf("AC6 missing govulncheck: the diagnostic must name the missing tool, got %q", errb)
	}

	// Unreachable npm registry: the lockfile is real, the advisory endpoint
	// is a closed loopback port, using npm's own configuration surface.
	cache := t.TempDir()
	_, errb, code = w320EHelper(t, npmDir, []string{
		"npm_config_registry=http://127.0.0.1:1/",
		"npm_config_cache=" + cache,
	}, "--npm", ".")
	if code != 1 {
		t.Fatalf("AC6 unreachable npm registry: the gate must fail (exit 1), got exit %d (stderr %q)", code, errb)
	}
	w320ERequirePrefix(t, "AC6 unreachable npm registry", errb, "db-unreachable:", "tool-missing:", "finding:")

	// Unreachable vuln.go.dev: govulncheck runs, its HTTPS traffic goes to a
	// closed loopback port, and fresh caches keep a stale database from
	// masking the outage.
	proxyEnv := []string{
		"HTTPS_PROXY=http://127.0.0.1:1",
		"HTTP_PROXY=http://127.0.0.1:1",
		"https_proxy=http://127.0.0.1:1",
		"http_proxy=http://127.0.0.1:1",
		"NO_PROXY=", "no_proxy=",
		"GOWORK=off",
		"GOCACHE=" + t.TempDir(),
		"GOMODCACHE=" + t.TempDir(),
		"GOPATH=" + t.TempDir(),
	}
	_, errb, code = w320EHelper(t, goDir, proxyEnv, "--go")
	if code != 1 {
		t.Fatalf("AC6 unreachable vuln.go.dev: the gate must fail (exit 1), got exit %d (stderr %q)", code, errb)
	}
	w320ERequirePrefix(t, "AC6 unreachable vuln.go.dev", errb, "db-unreachable:", "tool-missing:", "finding:")

	fmt.Println("PASS S2")
}
