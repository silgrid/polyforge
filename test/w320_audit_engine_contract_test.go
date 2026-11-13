package test

// w320_audit_engine_contract_test.go: the engine contract for the dependency-
// audit gate. workflows/_shared/scripts/verify-scope appends exactly one full-phase command named
// `audit` (source default, no prerequisites, null timeout) as the last command
// of every full-tier report of a repository with an analyzer and at least one
// auditable surface — the root go.mod and every package-lock.json /
// npm-shrinkwrap.json outside node_modules/ — and never to a scoped, micro or
// no-analyzer report. No conf key removes, replaces or weakens it: it follows
// a conf full_command= replacement and the carries selection, `audit` is a
// reserved check name, and a check_* line cannot bind prerequisites to it.
// The --json shape does not move, so the schema stays 1. At a nested engine
// root the audit reads the repository's one ledger, at the top of the git
// work tree, through --ledger.
//
// The helpers w320AuditCmd and w320AuditAppended are also the documented
// allowance the frozen byte-contrast oracles use: engine output == frozen
// output + exactly the audit line.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW320AuditEngine' -count=1

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// w320AuditCmd answers the audit command an engine living in engineDir
// reports for the given surface flags (" --go --npm 'webui'" style).
func w320AuditCmd(engineDir, surfaces string) string {
	return "sh '" + filepath.Join(engineDir, "audit-deps") + "'" + surfaces
}

// w320AuditSurfaces answers the surface flags of a repository tree, by the
// rule the engine documents: the root go.mod, then every lockfile directory
// outside node_modules/ (and .git/), sorted.
func w320AuditSurfaces(t *testing.T, dir string) string {
	t.Helper()
	out := ""
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		out = " --go"
	}
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (d.Name() == "package-lock.json" || d.Name() == "npm-shrinkwrap.json") {
			rel, _ := filepath.Rel(dir, filepath.Dir(p))
			seen[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for d := range seen {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		out += " --npm '" + d + "'"
	}
	return out
}

// w320AuditAppended answers the line report a frozen (pre-audit) oracle's
// output becomes with the audit gate: a full report gains exactly one
// trailing `command:` line when the tree has a surface; a scoped report is
// unchanged.
func w320AuditAppended(t *testing.T, frozenOut, dir, engineDir string) string {
	t.Helper()
	if !strings.HasPrefix(frozenOut, "mode: full\n") {
		return frozenOut
	}
	s := w320AuditSurfaces(t, dir)
	if s == "" {
		return frozenOut
	}
	return frozenOut + "command: " + w320AuditCmd(engineDir, s) + "\n"
}

type w320aeCommand struct {
	Name        string   `json:"name"`
	Phase       string   `json:"phase"`
	Cmd         string   `json:"cmd"`
	RequiresEnv []string `json:"requires_env"`
	RequiresCmd []string `json:"requires_cmd"`
	Timeout     *string  `json:"timeout"`
	Source      string   `json:"source"`
}

type w320aeReport struct {
	Schema       int             `json:"schema"`
	Mode         string          `json:"mode"`
	Commands     []w320aeCommand `json:"commands"`
	Diagnostics  []string        `json:"diagnostics"`
	E2ESelection *struct {
		Mode         string   `json:"mode"`
		FullCommands []string `json:"full_commands"`
	} `json:"e2e_selection"`
}

func w320aeJSON(t *testing.T, dir string, args ...string) w320aeReport {
	t.Helper()
	out, errb, code := w25e2eRun(t, dir, nil, append([]string{"--json"}, args...)...)
	if code != 0 {
		t.Fatalf("verify-scope --json %v: exit %d (stderr %q)", args, code, errb)
	}
	var r w320aeReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("--json stdout is not JSON: %v\n%s", err, out)
	}
	return r
}

func w320aeEngineDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(w25e2eEnginePath(t))
}

// w320aeAudits answers every reported command named audit or running the
// audit helper.
func w320aeAudits(r w320aeReport) []w320aeCommand {
	var out []w320aeCommand
	for _, c := range r.Commands {
		if c.Name == "audit" || strings.Contains(c.Cmd, "audit-deps") {
			out = append(out, c)
		}
	}
	return out
}

// w320aeRequireAudit asserts the report ends in exactly one audit command of
// the documented shape.
func w320aeRequireAudit(t *testing.T, label string, r w320aeReport, wantCmd string) {
	t.Helper()
	if r.Schema != 1 {
		t.Fatalf("%s: schema %d, want 1 (the audit is a command entry; the shape does not move)", label, r.Schema)
	}
	audits := w320aeAudits(r)
	if len(audits) != 1 {
		t.Fatalf("%s: want exactly one audit command, got %+v", label, r.Commands)
	}
	last := r.Commands[len(r.Commands)-1]
	if last.Name != "audit" || last.Phase != "full" || last.Source != "default" || last.Cmd != wantCmd ||
		len(last.RequiresEnv) != 0 || len(last.RequiresCmd) != 0 || last.Timeout != nil {
		t.Fatalf("%s: the last command must be the audit gate %q (phase full, source default, no prerequisites, null timeout), got %+v", label, wantCmd, last)
	}
}

func w320aeRequireNoAudit(t *testing.T, label string, r w320aeReport) {
	t.Helper()
	if a := w320aeAudits(r); len(a) != 0 {
		t.Fatalf("%s: no audit command may be reported, got %+v", label, a)
	}
}

// TestW320AuditEngineGoWithNpmTree: a Go repository with npm lockfiles under
// subdirectories gets `--go --npm <dir>...` (sorted, node_modules/ skipped),
// in the JSON report and the line report alike, on the full tier only.
func TestW320AuditEngineGoWithNpmTree(t *testing.T) {
	files := w25e2eGoModule("fixture.test/w320")
	files["webui/package.json"] = "{\"name\":\"webui\"}\n"
	files["webui/package-lock.json"] = "{\"lockfileVersion\":3}\n"
	files["tools/site/npm-shrinkwrap.json"] = "{\"lockfileVersion\":3}\n"
	files["webui/node_modules/dep/package-lock.json"] = "{\"lockfileVersion\":3}\n"
	dir, _ := w25e2eFixture(t, files)
	want := w320AuditCmd(w320aeEngineDir(t), " --go --npm 'tools/site' --npm 'webui'")
	w320aeRequireAudit(t, "Go + npm trees, --full", w320aeJSON(t, dir, "--full"), want)

	out, errb, code := w25e2eRun(t, dir, nil, "--full")
	if code != 0 {
		t.Fatalf("line report: exit %d (%s)", code, errb)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[len(lines)-1] != "command: "+want {
		t.Fatalf("the line report must end in the audit command, got:\n%s", out)
	}

	// The scoped tier (empty diff here) carries no audit.
	w320aeRequireNoAudit(t, "scoped", w320aeJSON(t, dir))
	// A change that forces full carries it.
	w25e2eWrite(t, dir, "go.mod", "module fixture.test/w320\n\ngo 1.22\n")
	w25e2eCommit(t, dir, "change go.mod")
	r := w320aeJSON(t, dir)
	if r.Mode != "full" {
		t.Fatalf("a go.mod change must be full, got %q", r.Mode)
	}
	w320aeRequireAudit(t, "go.mod change", r, want)
	// exclude= does not narrow the audited surfaces.
	w25e2eWrite(t, dir, ".polyforge/verify-scope.conf", "exclude=webui/*\nexclude=package-lock.json\n")
	w25e2eCommit(t, dir, "exclude the npm tree")
	w320aeRequireAudit(t, "exclude= present", w320aeJSON(t, dir, "--full"), want)
}

// TestW320AuditEngineNpmTrees: an npm repository with a root and a nested
// lockfile gets `--npm '.' --npm 'demo'`; an npm repository with no lockfile
// gets no audit (the lockfile is the audited tree).
func TestW320AuditEngineNpmTrees(t *testing.T) {
	files := map[string]string{
		"package.json":           "{\n  \"name\": \"w320-npm\",\n  \"scripts\": {\"lint\": \"eslint .\"}\n}\n",
		"package-lock.json":      "{\"lockfileVersion\":3}\n",
		"demo/package.json":      "{\"name\":\"demo\"}\n",
		"demo/package-lock.json": "{\"lockfileVersion\":3}\n",
		"lib/a.ts":               "export const a = 1;\n",
	}
	dir, _ := w25e2eFixture(t, files)
	w320aeRequireAudit(t, "npm root + demo", w320aeJSON(t, dir, "--full"), w320AuditCmd(w320aeEngineDir(t), " --npm '.' --npm 'demo'"))

	delete(files, "package-lock.json")
	delete(files, "demo/package-lock.json")
	bare, _ := w25e2eFixture(t, files)
	w320aeRequireNoAudit(t, "npm without a lockfile", w320aeJSON(t, bare, "--full"))
}

// TestW320AuditEngineNoAnalyzer: stack=none, an unsupported tree and an
// ambiguous one report no audit even with a lockfile present; --micro never
// carries it either.
func TestW320AuditEngineNoAnalyzer(t *testing.T) {
	lock := "{\"lockfileVersion\":3}\n"
	none, _ := w25e2eFixture(t, map[string]string{
		"package.json":                 "{\"name\":\"x\"}\n",
		"package-lock.json":            lock,
		".polyforge/verify-scope.conf": "stack=none\nfull_command=echo full\n",
	})
	w320aeRequireNoAudit(t, "stack=none", w320aeJSON(t, none, "--full"))
	unsupported, _ := w25e2eFixture(t, map[string]string{"web/package-lock.json": lock, "README.md": "x\n"})
	w320aeRequireNoAudit(t, "unsupported", w320aeJSON(t, unsupported, "--full"))
	files := w25e2eGoModule("fixture.test/w320amb")
	files["package.json"] = "{\"name\":\"x\"}\n"
	files["package-lock.json"] = lock
	ambiguous, _ := w25e2eFixture(t, files)
	w320aeRequireNoAudit(t, "ambiguous", w320aeJSON(t, ambiguous, "--full"))

	micro := w25e2eGoModule("fixture.test/w320micro")
	micro[".polyforge/verify-scope.conf"] = "micro_path=doc/*\nmicro_check=echo micro\n"
	mdir, _ := w25e2eFixture(t, micro)
	w25e2eWrite(t, mdir, "doc/readme.md", "changed\n")
	w25e2eCommit(t, mdir, "doc change")
	w320aeRequireNoAudit(t, "--micro", w320aeJSON(t, mdir, "--micro"))
}

// TestW320AuditEngineConfCannotReach: a conf full_command= replacement keeps
// the audit as the last command; a check named audit or audit-* is diagnosed
// and ignored; a check_* line naming audit binds nothing to it.
func TestW320AuditEngineConfCannotReach(t *testing.T) {
	files := w25e2eGoModule("fixture.test/w320conf")
	files[".polyforge/verify-scope.conf"] = "" +
		"fast_gate=echo fast\n" +
		"full_command=echo replaced-full\n" +
		"check=audit|full|true\n" +
		"check=audit-lite|fast|true\n" +
		"check_requires_cmd=audit|no-such-tool\n" +
		"check_requires_env=audit|NO_SUCH_VAR\n" +
		"check_timeout=audit|1s\n"
	dir, _ := w25e2eFixture(t, files)
	r := w320aeJSON(t, dir, "--full")
	want := w320AuditCmd(w320aeEngineDir(t), " --go")
	w320aeRequireAudit(t, "conf replacement", r, want)
	var cmds []string
	for _, c := range r.Commands {
		cmds = append(cmds, c.Name+":"+c.Cmd)
	}
	if !reflect.DeepEqual(cmds, []string{"fast-1:echo fast", "full-1:echo replaced-full", "audit:" + want}) {
		t.Fatalf("the conf's commands then the audit gate, got %q", cmds)
	}
	diags := strings.Join(r.Diagnostics, "\n")
	for _, name := range []string{"audit", "audit-lite"} {
		if !strings.Contains(diags, "conf rejects check name '"+name+"' (reserved for the engine's dependency-audit gate)") {
			t.Fatalf("a check named %s must be diagnosed as reserved:\n%s", name, diags)
		}
	}
	for _, key := range []string{"check_requires_cmd", "check_requires_env", "check_timeout"} {
		if !strings.Contains(diags, "conf ignores "+key+" for 'audit'") {
			t.Fatalf("%s naming audit must be diagnosed and ignored:\n%s", key, diags)
		}
	}
}

// TestW320AuditEngineSelection: an opted-in repository's carries selection
// replaces the full e2e tail, never the audit; full_commands (the bar a
// delivery re-runs) ends in it too.
func TestW320AuditEngineSelection(t *testing.T) {
	files := w260SelNpmFiles()
	files["package-lock.json"] = "{\"lockfileVersion\":3}\n"
	dir, _ := w25e2eFixture(t, files)
	w25e2eWrite(t, dir, "components/Panel.tsx", "export const Panel = 2;\n")
	w25e2eCommit(t, dir, "change the panel")
	r := w320aeJSON(t, dir, "--full")
	want := w320AuditCmd(w320aeEngineDir(t), " --npm '.'")
	w320aeRequireAudit(t, "carries selection", r, want)
	if r.E2ESelection == nil || r.E2ESelection.Mode != "carries" {
		t.Fatalf("the fixture must select by carries, got %+v", r.E2ESelection)
	}
	fc := r.E2ESelection.FullCommands
	if len(fc) == 0 || fc[len(fc)-1] != want {
		t.Fatalf("e2e_selection.full_commands must end in the audit gate, got %q", fc)
	}
}

// w320aeRunPlanned runs one planned command the way the light verification
// runs a nested root's command — from the work tree's top, behind
// `cd <root> &&` — with the stub audit tools (test/testdata/audit/bin/) first
// on PATH replaying a high npm advisory and a clean govulncheck stream; it
// answers stderr and the exit status.
func w320aeRunPlanned(t *testing.T, top, nestedRel, planned string) (string, int) {
	t.Helper()
	env := []string{
		"PATH=" + w320aFixture(t, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"W320_NPM_OUT=" + w320aFixture(t, "npm-high.json"), "W320_NPM_RC=1",
		"W320_GOVULN_OUT=" + w320aFixture(t, "govulncheck-clean.json"),
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "PATH" || strings.HasPrefix(k, "W320_") || k == "NODE_ENV" {
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command("bash", "-c", "cd "+nestedRel+" && "+planned)
	cmd.Dir = top
	cmd.Env = env
	var errb strings.Builder
	cmd.Stderr = &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run the planned audit %q: %v", planned, err)
		}
		code = ee.ExitCode()
	}
	return errb.String(), code
}

// TestW320AuditEngineNestedRootReadsTopLedger: at a nested engine root (a
// subdirectory with its own conf, whose commands run behind `cd <root> &&`)
// the audit reads the repository's one exemption ledger, at the top of the
// git work tree: the engine passes it as --ledger relative to the nested
// root (one ../ per segment, so the same command reads a baseline work
// tree's ledger too), while the top's own audit stays byte-identical (its
// default path already is the top's ledger). The real trigger is a change to
// the nested package manifest: the nested root's light report falls back to
// full and its full plan, the audit included, runs. Run as planned, the
// command honours a row of the top's ledger; with the top's ledger absent
// nothing is exempt, and a ledger under the nested root is not the
// repository's.
func TestW320AuditEngineNestedRootReadsTopLedger(t *testing.T) {
	lock := "{\"lockfileVersion\":3}\n"
	files := w366Files(t)
	files["pi-web/package-lock.json"] = lock
	files["pi-web/demo/package.json"] = "{\"name\":\"demo\"}\n"
	files["pi-web/demo/package-lock.json"] = lock
	files["tools/site/.polyforge/verify-scope.conf"] = "stack=npm\n"
	files["tools/site/package.json"] = "{\"name\":\"site\"}\n"
	files["tools/site/package-lock.json"] = lock
	row := w320aRow("GHSA-587h-7wmj-h5pm", "vite", w320aDay(-1), w320aDay(60)) + "\n"
	files[".polyforge/audit-exemptions.conf"] = row
	dir, base := w25e2eFixture(t, files)
	engineDir := w320aeEngineDir(t)
	nested := filepath.Join(dir, "pi-web")

	// The trigger: a nested manifest change makes the nested light report full.
	w25e2eWrite(t, dir, "pi-web/package.json",
		"{\n  \"name\": \"w366-nested\",\n  \"scripts\": {\"lint\": \"eslint .\", \"test:e2e:all\": \"node e2e/all.mjs\", \"x\": \"true\"}\n}\n")
	w25e2eCommit(t, dir, "change the nested manifest")
	r := w320aeJSON(t, nested, "--light", "--base", base)
	if r.Mode != "full" || len(r.Commands) == 0 {
		t.Fatalf("a nested manifest change must make the nested light report full, got %q %+v", r.Mode, r.Commands)
	}
	planned := r.Commands[len(r.Commands)-1].Cmd

	// Run as planned: the top's row exempts the finding in both nested trees.
	errb, code := w320aeRunPlanned(t, dir, "pi-web", planned)
	if code != 0 || strings.Contains(errb, "finding:") || strings.Contains(errb, "ledger-") || strings.Contains(errb, "stale:") {
		t.Fatalf("the nested audit %q must honour the top's ledger row (exit 0, no finding:): exit %d\n%s", planned, code, errb)
	}
	wantNested := w320AuditCmd(engineDir, " --go --npm '.' --npm 'demo' --ledger '../.polyforge/audit-exemptions.conf'")
	w320aeRequireAudit(t, "pi-web/ manifest change, --light", r, wantNested)
	w320aeRequireAudit(t, "pi-web/, --full", w320aeJSON(t, nested, "--full"), wantNested)
	w320aeRequireAudit(t, "tools/site/, --full", w320aeJSON(t, filepath.Join(dir, "tools", "site"), "--full"),
		w320AuditCmd(engineDir, " --npm '.' --ledger '../../.polyforge/audit-exemptions.conf'"))

	// The top: unchanged, no --ledger.
	w320aeRequireAudit(t, "the top, --full", w320aeJSON(t, dir, "--full"),
		w320AuditCmd(engineDir, " --go --npm 'pi-web' --npm 'pi-web/demo' --npm 'tools/site'"))

	// The top's ledger absent: nothing is exempt, and a ledger under the
	// nested root is not the repository's.
	if err := os.Remove(filepath.Join(dir, ".polyforge", "audit-exemptions.conf")); err != nil {
		t.Fatal(err)
	}
	w25e2eWrite(t, dir, "pi-web/.polyforge/audit-exemptions.conf", row)
	errb, code = w320aeRunPlanned(t, dir, "pi-web", planned)
	if code != 1 || !strings.Contains(errb, "finding: surface npm:.: GHSA-587h-7wmj-h5pm") ||
		!strings.Contains(errb, "finding: surface npm:demo: GHSA-587h-7wmj-h5pm") {
		t.Fatalf("with the top's ledger absent the nested audit must be red on both trees (a nested ledger exempts nothing): exit %d\n%s", code, errb)
	}
}
