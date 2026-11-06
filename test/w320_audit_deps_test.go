package test

// w320_audit_deps_test.go: unit tests for the release-shipped dependency-audit
// gate helper `workflows/_shared/scripts/audit-deps`, with no network. Stub `npm` and
// `govulncheck` (test/testdata/audit/bin/) come first on PATH and replay the
// recorded tool output next to them; every ledger is written into a temp
// directory. The authored end-to-end scenarios (w320_audit_e2e_test.go) carry
// the real advisory sources; these cases pin the helper's own decisions:
// thresholds, whole-tree scope, the ledger's validity/expiry/stale rules and
// the fail-closed causes.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW320Audit' -count=1

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w320aRoot answers the repository root (this package's cwd is test/).
func w320aRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// w320aFixture answers the absolute path of a recorded tool-output file.
func w320aFixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(w320aRoot(t), "test", "testdata", "audit", name)
}

// w320aRun is one helper invocation's environment: the stub outputs, the
// extra environment and the args.
type w320aRun struct {
	npmOut, npmErr, npmRC          string
	govulnOut, govulnErr, govulnRC string
	path                           string // PATH override; empty = stubs first, then the host PATH
	args                           []string
}

// w320aExec runs `sh workflows/_shared/scripts/audit-deps` in dir and answers stderr, the exit
// status and the stub argument log.
func w320aExec(t *testing.T, dir string, r w320aRun) (string, int, string) {
	t.Helper()
	argLog := filepath.Join(t.TempDir(), "args")
	path := r.path
	if path == "" {
		path = w320aFixture(t, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	env := []string{"PATH=" + path, "W320_STUB_ARGS=" + argLog}
	add := func(k, v string) {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	add("W320_NPM_OUT", r.npmOut)
	add("W320_NPM_ERR", r.npmErr)
	add("W320_NPM_RC", r.npmRC)
	add("W320_GOVULN_OUT", r.govulnOut)
	add("W320_GOVULN_ERR", r.govulnErr)
	add("W320_GOVULN_RC", r.govulnRC)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PATH" || strings.HasPrefix(k, "W320_") || k == "NODE_ENV" {
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command("/bin/sh", append([]string{filepath.Join(w320aRoot(t), "workflows", "_shared", "scripts", "audit-deps")}, r.args...)...)
	cmd.Dir = dir
	cmd.Env = env
	var errb strings.Builder
	cmd.Stderr = &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run workflows/_shared/scripts/audit-deps: %v", err)
		}
		code = ee.ExitCode()
	}
	args, _ := os.ReadFile(argLog)
	return errb.String(), code, string(args)
}

// w320aTree answers a repository-like temp dir with an npm tree at web/.
func w320aTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// w320aLedger writes rows (already formatted) into a ledger file and answers
// its path.
func w320aLedger(t *testing.T, rows ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audit-exemptions.conf")
	if err := os.WriteFile(p, []byte("# ledger\n\n"+strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// w320aDay answers today (UTC) shifted by days, as YYYY-MM-DD.
func w320aDay(days int) string {
	return time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
}

// w320aRow answers one exemption row.
func w320aRow(adv, pkg, from, expires string) string {
	return fmt.Sprintf("exempt=%s|%s|fixture reason|owner ruling 2026-11-04 fixture|%s|%s", adv, pkg, from, expires)
}

func w320aWant(t *testing.T, label, errb string, code, wantCode int, has []string, hasNot []string) {
	t.Helper()
	if code != wantCode {
		t.Fatalf("%s: exit %d, want %d; stderr:\n%s", label, code, wantCode, errb)
	}
	for _, s := range has {
		if !strings.Contains(errb, s) {
			t.Fatalf("%s: stderr lacks %q:\n%s", label, s, errb)
		}
	}
	for _, s := range hasNot {
		if strings.Contains(errb, s) {
			t.Fatalf("%s: stderr carries %q:\n%s", label, s, errb)
		}
	}
}

// TestW320AuditNpmThreshold: a high advisory is a red finding, a moderate-only
// tree is green, and the npm run audits the whole lockfile tree (dev
// dependencies included) at the high level.
func TestW320AuditNpmThreshold(t *testing.T) {
	dir := w320aTree(t)
	errb, code, args := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web"}})
	w320aWant(t, "high", errb, code, 1,
		[]string{"finding: surface npm:web: GHSA-587h-7wmj-h5pm in vite (high", "surface npm:web: audited, 1 finding(s)"},
		[]string{"GHSA-67mh-4wv8-2f99", "GHSA-4w7w-66w2-5vf9", "tool-missing:", "db-unreachable:"})
	for _, a := range []string{"audit", "--json", "--audit-level=high", "--package-lock-only", "--include=dev"} {
		if !strings.Contains(" "+strings.Join(strings.Fields(args), " ")+" ", " "+a+" ") {
			t.Fatalf("npm audit ran without %s: %q", a, args)
		}
	}
	if strings.Contains(args, "--omit") || strings.Contains(args, "--production") {
		t.Fatalf("npm audit must audit the whole tree, never omit dev dependencies: %q", args)
	}

	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-moderate.json"), args: []string{"--npm", "web"}})
	w320aWant(t, "moderate only", errb, code, 0, []string{"surface npm:web: audited, 0 finding(s)"}, []string{"finding:"})
}

// TestW320AuditNpmDevOnly: the only high hit is in a dev dependency; a run
// that narrowed the tree to production dependencies would see none (the stub
// answers an empty report to --omit=dev), so the gate must stay red.
func TestW320AuditNpmDevOnly(t *testing.T) {
	dir := w320aTree(t)
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web"}})
	w320aWant(t, "dev-only high", errb, code, 1, []string{"finding: surface npm:web: GHSA-587h-7wmj-h5pm"}, nil)
	// NODE_ENV=production makes a bare npm audit drop dev dependencies; the
	// helper's explicit --include=dev keeps them, and the stub sees it.
	t.Setenv("NODE_ENV", "production")
	_, _, args := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web"}})
	if !strings.Contains(args, "--include=dev") {
		t.Fatalf("npm audit must name --include=dev explicitly: %q", args)
	}
}

// TestW320AuditLedgerExempts: a valid in-date row exempts its finding (exit 0,
// no finding:); an absent ledger exempts nothing.
func TestW320AuditLedgerExempts(t *testing.T) {
	dir := w320aTree(t)
	ledger := w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-1), w320aDay(60)))
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "valid row", errb, code, 0, nil, []string{"finding:", "stale:", "ledger-"})

	// The default ledger path is .polyforge/audit-exemptions.conf in the
	// current directory; absent, nothing is exempt.
	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web"}})
	w320aWant(t, "absent ledger", errb, code, 1, []string{"finding:"}, []string{"ledger-"})
	if err := os.MkdirAll(filepath.Join(dir, ".polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	row := w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(0), w320aDay(90)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".polyforge", "audit-exemptions.conf"), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web"}})
	w320aWant(t, "default ledger, 90-day window", errb, code, 0, nil, []string{"finding:", "ledger-"})

	// The key is advisory + package: the same advisory on another package is
	// not exempt.
	ledger = w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite-other", w320aDay(-1), w320aDay(60)))
	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "wrong package", errb, code, 1, []string{"finding: surface npm:web: GHSA-587h-7wmj-h5pm in vite", "stale:"}, nil)
}

// TestW320AuditLedgerExpired: a row past its expiry is not honoured — the
// finding is red again and the row is reported.
func TestW320AuditLedgerExpired(t *testing.T) {
	dir := w320aTree(t)
	ledger := w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-40), w320aDay(-1)))
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "expired row", errb, code, 1, []string{"ledger-expired:", "finding: surface npm:web: GHSA-587h-7wmj-h5pm"}, []string{"ledger-invalid:"})

	// An expired row alone (nothing to exempt) is reported, never red.
	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-moderate.json"), args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "expired row, no finding", errb, code, 0, []string{"ledger-expired:"}, []string{"finding:"})
}

// TestW320AuditLedgerInvalid: a row missing a field, with a bad advisory id,
// a bad date, a future start or a window over 90 days is never honoured and
// turns the gate red.
func TestW320AuditLedgerInvalid(t *testing.T) {
	dir := w320aTree(t)
	cases := map[string]string{
		"missing owner ruling": fmt.Sprintf("exempt=GHSA-587h-7wmj-h5pm|vite|fixture reason||%s|%s", w320aDay(-1), w320aDay(30)),
		"five fields":          fmt.Sprintf("exempt=GHSA-587h-7wmj-h5pm|vite|fixture reason|%s|%s", w320aDay(-1), w320aDay(30)),
		"pipe inside a value":  fmt.Sprintf("exempt=GHSA-587h-7wmj-h5pm|vite|a|b|owner|%s|%s", w320aDay(-1), w320aDay(30)),
		"not an advisory id":   w320aRow("CVE-2026-0001", "vite", w320aDay(-1), w320aDay(30)),
		"bad date":             w320aRow("GHSA-587h-7wmj-h5pm", "vite", "2026-13-01", w320aDay(30)),
		"95-day window":        w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-5), w320aDay(90)),
		"future start":         w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(2), w320aDay(30)),
		"unknown key":          "ignore=GHSA-587h-7wmj-h5pm",
	}
	for label, row := range cases {
		ledger := w320aLedger(t, row)
		errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1", args: []string{"--npm", "web", "--ledger", ledger}})
		w320aWant(t, label, errb, code, 1, []string{"ledger-invalid:", "finding: surface npm:web: GHSA-587h-7wmj-h5pm"}, nil)
	}
	// An invalid row is red even when nothing needs an exemption.
	ledger := w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-5), w320aDay(90)))
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-moderate.json"), args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "invalid row, no finding", errb, code, 1, []string{"ledger-invalid:"}, []string{"finding:"})
}

// TestW320AuditLedgerStale: a valid row that matches no current finding is
// reported as stale and never turns the gate red.
func TestW320AuditLedgerStale(t *testing.T) {
	dir := w320aTree(t)
	ledger := w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-1), w320aDay(30)))
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-moderate.json"), args: []string{"--npm", "web", "--ledger", ledger}})
	w320aWant(t, "stale row", errb, code, 0, []string{"stale:", "GHSA-587h-7wmj-h5pm|vite"}, []string{"finding:"})
}

// TestW320AuditToolMissing: an absent npm or govulncheck is red with
// tool-missing: naming the tool and its install hint, never a skip.
func TestW320AuditToolMissing(t *testing.T) {
	dir := w320aTree(t)
	bin := t.TempDir()
	for _, name := range []string{"python3", "mktemp", "rm", "cat"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("host tool %s: %v", name, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	errb, code, _ := w320aExec(t, dir, w320aRun{path: bin, args: []string{"--npm", "web"}})
	w320aWant(t, "npm absent", errb, code, 1, []string{"tool-missing: npm"}, []string{"db-unreachable:", "finding:", "stale:"})
	errb, code, _ = w320aExec(t, dir, w320aRun{path: bin, args: []string{"--go"}})
	w320aWant(t, "go toolchain absent", errb, code, 1, []string{"tool-missing: go"}, []string{"db-unreachable:", "finding:"})
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("host go: %v", err)
	}
	if err := os.Symlink(goBin, filepath.Join(bin, "go")); err != nil {
		t.Fatal(err)
	}
	errb, code, _ = w320aExec(t, dir, w320aRun{path: bin, args: []string{"--go"}})
	w320aWant(t, "govulncheck absent", errb, code, 1,
		[]string{"tool-missing: govulncheck", "go install golang.org/x/vuln/cmd/govulncheck@v1.9.0"},
		[]string{"db-unreachable:", "finding:"})
	// A valid row is not reported stale when a surface could not be audited.
	ledger := w320aLedger(t, w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-1), w320aDay(30)))
	errb, _, _ = w320aExec(t, dir, w320aRun{path: bin, args: []string{"--npm", "web", "--ledger", ledger}})
	if strings.Contains(errb, "stale:") {
		t.Fatalf("an unaudited surface must not make a row stale:\n%s", errb)
	}
}

// TestW320AuditDBUnreachable: the npm registry error envelope and the
// govulncheck database fetch failure are red with db-unreachable:, distinct
// from tool-missing: and finding:.
func TestW320AuditDBUnreachable(t *testing.T) {
	dir := w320aTree(t)
	errb, code, _ := w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-error.json"), npmRC: "1", args: []string{"--npm", "web"}})
	w320aWant(t, "npm registry down", errb, code, 1, []string{"db-unreachable: surface npm:web", "ECONNREFUSED"}, []string{"tool-missing:", "finding:"})
	errb, code, _ = w320aExec(t, dir, w320aRun{
		govulnOut: w320aFixture(t, "govulncheck-dbdown.json"), govulnErr: w320aFixture(t, "govulncheck-dbdown.err"), govulnRC: "1",
		args: []string{"--go"},
	})
	w320aWant(t, "vuln db down", errb, code, 1, []string{"db-unreachable: surface go:.", "fetching vulnerabilities"}, []string{"tool-missing:", "finding:"})
	// Any other tool failure is still red, under its own audit-deps: cause.
	errb, code, _ = w320aExec(t, dir, w320aRun{
		govulnOut: w320aFixture(t, "govulncheck-clean.json"), govulnRC: "1",
		args: []string{"--go"},
	})
	w320aWant(t, "govulncheck error", errb, code, 1, []string{"audit-deps: surface go:."}, []string{"db-unreachable:", "tool-missing:", "finding:"})
	// A missing npm tree directory is red too.
	errb, code, _ = w320aExec(t, dir, w320aRun{npmOut: w320aFixture(t, "npm-moderate.json"), args: []string{"--npm", "absent"}})
	w320aWant(t, "absent npm dir", errb, code, 1, []string{"audit-deps: surface npm:absent"}, nil)
}

// TestW320AuditGoReachability: govulncheck's symbol mode reports module-,
// package- and symbol-level findings; only the reachable (symbol-level) one
// is a finding, an imported-but-unreached package is not.
func TestW320AuditGoReachability(t *testing.T) {
	dir := w320aTree(t)
	errb, code, args := w320aExec(t, dir, w320aRun{govulnOut: w320aFixture(t, "govulncheck-reachable.json"), args: []string{"--go"}})
	w320aWant(t, "reachable", errb, code, 1,
		[]string{"finding: surface go:.: GO-2026-5320 in example.com/reached", "surface go:.: audited, 1 finding(s)"},
		[]string{"GO-2026-4970"})
	if !strings.HasPrefix(args, "GOWORK=off -json ./...") {
		t.Fatalf("govulncheck must run as GOWORK=off govulncheck -json ./..., got %q", args)
	}
	ledger := w320aLedger(t, w320aRow("GO-2026-5320", "example.com/reached", w320aDay(-1), w320aDay(30)))
	errb, code, _ = w320aExec(t, dir, w320aRun{govulnOut: w320aFixture(t, "govulncheck-reachable.json"), args: []string{"--go", "--ledger", ledger}})
	w320aWant(t, "reachable exempted", errb, code, 0, nil, []string{"finding:", "stale:"})
	errb, code, _ = w320aExec(t, dir, w320aRun{govulnOut: w320aFixture(t, "govulncheck-clean.json"), args: []string{"--go"}})
	w320aWant(t, "clean", errb, code, 0, []string{"surface go:.: audited, 0 finding(s)"}, []string{"finding:"})
	// One run covers every surface: a finding in either is red.
	errb, code, _ = w320aExec(t, dir, w320aRun{
		govulnOut: w320aFixture(t, "govulncheck-clean.json"), npmOut: w320aFixture(t, "npm-high.json"), npmRC: "1",
		args: []string{"--go", "--npm", "web"},
	})
	w320aWant(t, "two surfaces", errb, code, 1, []string{"surface go:.: audited", "finding: surface npm:web"}, nil)
}

// TestW320AuditUsage: no surface or an unknown argument is a usage error.
func TestW320AuditUsage(t *testing.T) {
	dir := w320aTree(t)
	for _, args := range [][]string{nil, {"--skip"}, {"--npm"}} {
		errb, code, _ := w320aExec(t, dir, w320aRun{args: args})
		if code != 2 {
			t.Fatalf("args %q: exit %d, want 2 (usage); stderr:\n%s", args, code, errb)
		}
	}
}
