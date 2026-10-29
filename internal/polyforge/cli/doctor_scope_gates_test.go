package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// doctor_scope_gates_test.go: `pf doctor`'s "scoped-gates readiness" row
//. The row reads, never executes, the release tree's engine and
// the clone's manifests/conf. Fixtures install a manifest or a conf instead of
// the retired repo-owned script.

// scopedGatesRow asserts the scoped-gates readiness row in detail with the
// expected status and answers it.
func scopedGatesRow(t *testing.T, doc doctorDoc, want string) (string, string) {
	t.Helper()
	for _, c := range doc.Checks {
		if c.Name == docChScopedGates {
			if c.Status != want {
				t.Fatalf("check %q = %q (%q), want %q", docChScopedGates, c.Status, c.Detail, want)
			}
			return c.Status, c.Detail
		}
	}
	t.Fatalf("no %q check in %+v", docChScopedGates, doc.Checks)
	return "", ""
}

// scopedGatesDoctorWithHome runs the doctor with a mapped workspace and an
// optional HOME (the release tree lives under it).
func scopedGatesDoctorWithHome(t *testing.T, srv, ws, home string) doctorDoc {
	t.Helper()
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"" + srv + "\"\napi_key = \"k\"\ndaemon_socket = \"" + filepath.Join(dir, "gone.sock") + "\"\n" +
		"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	if home != "" {
		env["HOME"] = home
		env["POLYFORGE_DATA_DIR"] = filepath.Join(home, ".local", "share", "polyforge")
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	return doc
}

// installReleaseEngine writes a presence-only release-tree engine under home.
func installReleaseEngine(t *testing.T, home string) string {
	t.Helper()
	rel := filepath.Join(home, ".local", "share", "polyforge", "release")
	engine := filepath.Join(rel, "current", scopedGatesEngine)
	if err := os.MkdirAll(filepath.Dir(engine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return engine
}

// writeScopedManifest installs a stack manifest in the clone.
func writeScopedManifest(t *testing.T, clone, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(clone, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeScopedConf installs the declarative repository override.
func writeScopedConf(t *testing.T, clone, body string) {
	t.Helper()
	p := filepath.Join(clone, ".polyforge", "verify-scope.conf")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The first valid stack= pin wins; an invalid one is ignored, as in the engine.
func TestScopedGatesStackFirstValidPin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "verify-scope.conf")
	body := "stack=bogus\nstack=npm\nstack=go\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := scopedGatesReadConf(p); got != "npm" {
		t.Fatalf("scopedGatesReadConf pin = %q, want the first valid pin npm", got)
	}
}

// Engine default: the release tree carries tools/verify-scope, so a repo with
// a supported manifest is scoped-ready with no repo conf.
func TestScopedGatesEngineDefaultPass(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "go.mod", "module example.test/x\n\ngo 1.21\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "pass")
	if !strings.Contains(detail, "1 of 1") || !strings.Contains(detail, scopedGatesEngine) {
		t.Fatalf("engine-default detail does not state the readiness: %q", detail)
	}
}

// Repo conf: a conf with a stack pin is effective even without a release-tree
// engine in the fixture home.
func TestScopedGatesRepoConfPass(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "package.json", "{\n  \"name\": \"x\"\n}\n")
	writeScopedConf(t, clone, "stack=npm\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "pass")
	if !strings.Contains(detail, "1 of 1") || !strings.Contains(detail, scopedGatesConf) {
		t.Fatalf("repo-conf detail does not state the readiness: %q", detail)
	}
}

// A conf without the release-tree engine does nothing: warn, never pass.
func TestScopedGatesRepoConfWithoutEngineWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "package.json", "{\n  \"name\": \"x\"\n}\n")
	writeScopedConf(t, clone, "stack=npm\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, t.TempDir())
	_, detail := scopedGatesRow(t, doc, "warn")
	if !strings.Contains(detail, scopedGatesEngine) || !strings.Contains(detail, "falls back to full") {
		t.Fatalf("a conf without the engine must warn: %q", detail)
	}
}

// Unpinned multi-manifest: warn, naming the fall-back fact and the authority.
func TestScopedGatesAmbiguousWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "go.mod", "module example.test/x\n\ngo 1.21\n")
	writeScopedManifest(t, clone, "package.json", "{\n  \"name\": \"x\"\n}\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "warn")
	for _, tok := range []string{"pi-web", "unpinned multi-manifest", "falls back to full", scopedGatesPointer} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("ambiguous detail lacks %q: %q", tok, detail)
		}
	}
}

// Unsupported stack: warn, never fail.
func TestScopedGatesUnsupportedWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "Cargo.toml", "[package]\nname = \"x\"\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "warn")
	if !strings.Contains(detail, "no supported stack manifest") || !strings.Contains(detail, "falls back to full") {
		t.Fatalf("unsupported detail is not the expected warn: %q", detail)
	}
}

// Missing prerequisite: no release-tree engine and no repo conf warns.
func TestScopedGatesMissingEngineAndConfWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "go.mod", "module example.test/x\n\ngo 1.21\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, t.TempDir())
	_, detail := scopedGatesRow(t, doc, "warn")
	for _, tok := range []string{"pi-web", scopedGatesEngine, "falls back to full"} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("missing-prerequisite detail lacks %q: %q", tok, detail)
		}
	}
}

// No clone: a registered repo with no local clone is unknown (counted in
// the detail, never faked green), and the row is never a fail.
func TestScopedGatesNoCloneUnknown(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir() // no .repo/ at all
	doc := scopedGatesDoctorWithHome(t, srv, ws, t.TempDir())
	_, detail := scopedGatesRow(t, doc, "pass")
	if !strings.Contains(detail, "no local clone") || !strings.Contains(detail, "pi-web") || !strings.Contains(detail, "unknown") {
		t.Fatalf("no-clone detail does not state the unknown repo: %q", detail)
	}
}

// Unreachable aihub: the registered-repo set cannot be read, so the row
// degrades to skip naming the reason — never a fail and never a faked pass.
func TestScopedGatesUnreachableSkipsNotFail(t *testing.T) {
	ws := t.TempDir()
	reposClone(t, ws, "pi-web", "main")
	// Build the unreachable config by hand (the shared helper takes a home).
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"http://127.0.0.1:59997\"\napi_key = \"k\"\ndaemon_socket = \"" + filepath.Join(dir, "gone.sock") + "\"\n" +
		"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, map[string]string{"XDG_CONFIG_HOME": xdg}, "--json", "doctor")
	_, detail := scopedGatesRow(t, doc, "skip")
	if !strings.Contains(detail, "not decidable") {
		t.Fatalf("unreachable detail does not name the undecidable set: %q", detail)
	}
}

// Non-regression and warn-only: the row is additive. A dirty clone keeps its
// pre-existing repo-clones warning, the scoped-gates row warns about the
// unsupported stack in the same run, `ok` stays true and the exit code stays
// 0 (warn never fails), and `--harness` runs neither.
func TestScopedGatesAdditiveWarnOnlyExit0(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	reposDirty(t, reposClone(t, ws, "pi-web", "release/v0.0.27"))

	sock := liveDaemonSocket(t, "m-sg")
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "polyforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "server = \"" + srv + "\"\napi_key = \"k\"\ndaemon_socket = \"" + sock + "\"\n" +
		"\n[workspace.\"" + ws + "\"]\nprojects = [\"e2e\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": xdg}
	env["HOME"] = displayHome(t, `{"statusLine":{"type":"command","command":"pf watch --statusline --harness-session -"}}`,
		`{"packages":["~/.local/share/polyforge/release/current"]}`)
	layoutRelease(t, env["HOME"])
	layoutClaudeLink(t, env["HOME"])
	env["PATH"] = fakeTmuxPath(t, 0)
	code, _, doc := doctorStatus(t, env, "--json", "doctor")
	if code != 0 || !doc.OK {
		t.Fatalf("pf doctor: exit %d ok=%t with only warn rows, want 0/true (%+v)", code, doc.OK, doc.Checks)
	}
	if _, detail := scopedGatesRow(t, doc, "warn"); !strings.Contains(detail, "pi-web") {
		t.Fatalf("the scoped-gates warning does not name the repo: %q", detail)
	}
	if _, detail := reposRow(t, doc, "warn"); !strings.Contains(detail, "pi-web") {
		t.Fatalf("the pre-existing repo-clones warning changed: %q", detail)
	}

	// harness run: no scoped-gates row and no repo name.
	_, _, hdoc := doctorStatus(t, env, "--json", "doctor", "--harness")
	for _, c := range hdoc.Checks {
		if c.Name == docChScopedGates || strings.Contains(c.Detail, "pi-web") {
			t.Fatalf("doctor --harness ran or mentioned the scoped-gates check: %q", c.Detail)
		}
	}
}

// A registered project with no repos is decidable (nothing to check) and
// answers a pass counting zero, never a faked warn or a fail.
func TestScopedGatesNoRegisteredReposPassNotFail(t *testing.T) {
	srv := (&reposAihub{
		known: map[string][]api.ProjectRepo{"e2e": {}},
		items: map[string]string{},
	}).server(t)
	ws := t.TempDir()
	doc := scopedGatesDoctorWithHome(t, srv, ws, t.TempDir())
	_, detail := scopedGatesRow(t, doc, "pass")
	if !strings.Contains(detail, "0 of 0") || !strings.Contains(detail, scopedGatesEngine) {
		t.Fatalf("no-repos detail does not state the empty read: %q", detail)
	}
}

// The engine is never executed: a script that would leave a marker stays
// untouched, and the row still reports readiness from a presence read.
func TestScopedGatesNeverExecutesEngine(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	marker := filepath.Join(home, "engine-ran")
	rel := installReleaseEngine(t, home)
	if err := os.WriteFile(rel, []byte("#!/bin/sh\ntouch \""+marker+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "go.mod", "module example.test/x\n\ngo 1.21\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	if _, detail := scopedGatesRow(t, doc, "pass"); !strings.Contains(detail, "1 of 1") {
		t.Fatalf("engine-presence detail: %q", detail)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("the doctor executed the release engine (marker exists)")
	}
}

// The conf reader mirrors the engine: stack=none is a valid pin, and only
// the command lines the engine accepts count — fast_gate=, full_command= and
// well-formed check= lines; an empty value, a leading command substitution,
// a bad phase and a reserved or malformed check name are no command.
func TestScopedGatesReadConfPinsAndCountsCommands(t *testing.T) {
	p := filepath.Join(t.TempDir(), "verify-scope.conf")
	body := strings.Join([]string{
		"# comment",
		"stack=bogus",
		"stack=none",
		"stack=go",
		"fast_gate=cargo fmt --check",
		"full_command=cargo test",
		"  check=clippy|fast|cargo clippy -- -D warnings",
		"check=fast-1|fast|reserved name",
		"check=bad name|fast|malformed name",
		"check=slowpoke|slow|bad phase",
		"check=empty|full|",
		"fast_gate=$(touch pwned)",
		"full_command=`touch pwned`",
		"fast_gate=",
		"exclude=docs/*",
		"test_glob=tests/*.py",
	}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pin, n := scopedGatesReadConf(p)
	if pin != "none" || n != 3 {
		t.Fatalf("scopedGatesReadConf = (%q, %d), want the first valid pin none and 3 commands", pin, n)
	}
	if pin, n := scopedGatesReadConf(filepath.Join(t.TempDir(), "absent.conf")); pin != "" || n != 0 {
		t.Fatalf("a missing conf = (%q, %d), want no pin and no command", pin, n)
	}
}

// Another stack (no go.mod / package.json) whose conf declares its bar: the
// engine reports the conf's commands on the full tier, so the repo conf is
// effective — pass, saying it is full tier only.
func TestScopedGatesOtherStackConfPass(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "Cargo.toml", "[package]\nname = \"x\"\n")
	writeScopedConf(t, clone, "fast_gate=cargo fmt --check\ncheck=test|full|cargo test\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "pass")
	for _, tok := range []string{"repo conf effective", "full tier only", "e2e/pi-web"} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("other-stack conf detail lacks %q: %q", tok, detail)
		}
	}
}

// Another stack with no conf: warn, with the consequence spelled out — the
// bar is derived ad hoc every run, there is no scoped tier, and a future
// runner dispatch fails closed.
func TestScopedGatesOtherStackNoConfWarnsWithConsequence(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "pyproject.toml", "[project]\nname = \"x\"\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "warn")
	for _, tok := range []string{"pi-web", "no supported stack manifest", "ad hoc on every run", "no scoped tier", "falls back to full", "runner dispatch fails closed", scopedGatesConf, scopedGatesPointer} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("other-stack no-conf detail lacks %q: %q", tok, detail)
		}
	}
}

// Another stack whose conf declares no command the engine accepts is the
// same gap as no conf: warn.
func TestScopedGatesOtherStackConfWithoutCommandWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "Cargo.toml", "[package]\nname = \"x\"\n")
	writeScopedConf(t, clone, "exclude=docs/*\nfast_gate=$(touch pwned)\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "warn")
	if !strings.Contains(detail, "no repo conf command") || !strings.Contains(detail, "runner dispatch fails closed") {
		t.Fatalf("a command-less conf must warn like no conf: %q", detail)
	}
}

// A stack=none pin counts as a conf: it resolves even an unpinned
// multi-manifest repo to conf-only (pass when it declares a command), and a
// stack=none conf with no command warns.
func TestScopedGatesStackNonePinCountsAsConf(t *testing.T) {
	srv := reposAihubDefault(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "go.mod", "module example.test/x\n\ngo 1.21\n")
	writeScopedManifest(t, clone, "package.json", "{\n  \"name\": \"x\"\n}\n")
	writeScopedConf(t, clone, "stack=none\ncheck=lint|fast|make lint\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "pass")
	if !strings.Contains(detail, "repo conf effective") || strings.Contains(detail, "unpinned multi-manifest") {
		t.Fatalf("a stack=none pin with a command must be the effective repo conf: %q", detail)
	}

	writeScopedConf(t, clone, "stack=none\n")
	doc = scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail = scopedGatesRow(t, doc, "warn")
	if !strings.Contains(detail, "pins stack=none but declares no command") || !strings.Contains(detail, "no scoped tier") {
		t.Fatalf("a command-less stack=none conf must warn with the consequence: %q", detail)
	}
}

// A conf-only repo still needs the release-tree engine: warn without it.
func TestScopedGatesOtherStackConfWithoutEngineWarns(t *testing.T) {
	srv := reposAihubDefault(t)
	ws := t.TempDir()
	clone := reposClone(t, ws, "pi-web", "main")
	writeScopedManifest(t, clone, "Cargo.toml", "[package]\nname = \"x\"\n")
	writeScopedConf(t, clone, "full_command=cargo test\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, t.TempDir())
	_, detail := scopedGatesRow(t, doc, "warn")
	if !strings.Contains(detail, scopedGatesEngine) || strings.Contains(detail, "repo conf effective") {
		t.Fatalf("a conf-only repo without the engine must warn about the engine: %q", detail)
	}
}

// Mixed: a scoped-ready Go repo and a conf-only repo pass together; the
// detail counts the scoped-ready one and names the conf-only one apart.
func TestScopedGatesMixedScopedReadyAndConfOnlyPass(t *testing.T) {
	srv := (&reposAihub{
		known: map[string][]api.ProjectRepo{"e2e": {
			{Name: "pi-web", URL: "file:///nonexistent/pi-web.git"},
			{Name: "charts", URL: "file:///nonexistent/charts.git"},
		}},
		items: map[string]string{},
	}).server(t)
	home := t.TempDir()
	installReleaseEngine(t, home)
	ws := t.TempDir()
	writeScopedManifest(t, reposClone(t, ws, "pi-web", "main"), "go.mod", "module example.test/x\n\ngo 1.21\n")
	charts := reposClone(t, ws, "charts", "main")
	writeScopedConf(t, charts, "check=lint|fast|helm lint charts/*\n")
	doc := scopedGatesDoctorWithHome(t, srv, ws, home)
	_, detail := scopedGatesRow(t, doc, "pass")
	for _, tok := range []string{"1 of 2 registered repo(s) are scoped-ready", "1 repo(s) with no analyzer", "repo conf effective", "e2e/charts"} {
		if !strings.Contains(detail, tok) {
			t.Fatalf("mixed detail lacks %q: %q", tok, detail)
		}
	}
	if strings.Contains(detail, "e2e/pi-web") {
		t.Fatalf("the scoped-ready repo must not be listed as conf-only: %q", detail)
	}
}
