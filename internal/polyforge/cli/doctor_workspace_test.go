package cli

// doctor_workspace_test.go: the polyforge#177 workspace checks of `pf
// doctor` (clean → pass; strays → warn with a destination, untouched;
// stale scratch reported by default, removed only with --prune-scratch)
// and `pf init`. Temp workspaces and a scratch XDG config only.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctorWorkspace answers a scratch config mapping one existing
// workspace (plus its path) behind a fake aihub and no daemon.
func doctorWorkspace(t *testing.T) (map[string]string, string) {
	t.Helper()
	srv := aiSever(t, &reqRecorder{}, map[string]bool{"polyforge": true}, 0, "")
	env := doctorServerConfig(t, srv.URL, "k_ok", filepath.Join(t.TempDir(), "dead.sock"), "polyforge")
	return env, filepath.Join(filepath.Dir(env["XDG_CONFIG_HOME"]), "ws-polyforge")
}

func touch(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorWorkspaceLayoutCleanPasses(t *testing.T) {
	env, ws := doctorWorkspace(t)
	if code, _, stderr := runPf(t, env, "init", ws); code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	for _, d := range []string{".repo/polyforge", "capsules/polyforge-1", ".polyforge/repo-map", ".pi"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, filepath.Join(ws, ".polyforge.yaml"), time.Now())
	touch(t, filepath.Join(ws, "scratch", "fresh.log"), time.Now())
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	for _, name := range []string{docChLayout, docChScratch} {
		if st, detail := doctorFind(t, doc, name); st != "pass" {
			t.Fatalf("%s = %s (%s), want pass", name, st, detail)
		}
	}
}

func TestDoctorWorkspaceLayoutWarnsWithDestination(t *testing.T) {
	env, ws := doctorWorkspace(t)
	touch(t, filepath.Join(ws, "brief.md"), time.Now())
	touch(t, filepath.Join(ws, "app.apk"), time.Now())
	touch(t, filepath.Join(ws, ".polyforge", "design-review.md"), time.Now())
	code, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChLayout)
	if st != "warn" {
		t.Fatalf("layout = %s (%s), want warn", st, detail)
	}
	for _, want := range []string{
		filepath.Join(ws, "brief.md") + " → aihub",
		filepath.Join(ws, "app.apk") + " → scratch/",
		filepath.Join(ws, ".polyforge", "design-review.md") + " → aihub",
		"pf remember", "nothing was moved or deleted",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("layout detail lacks %q: %s", want, detail)
		}
	}
	if strings.Contains(detail, "pf init") {
		t.Fatalf("no CLAUDE.md, yet the detail suggests pf init: %s", detail)
	}
	for _, f := range []string{"brief.md", "app.apk", ".polyforge/design-review.md"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Fatalf("doctor touched %s: %v", f, err)
		}
	}
	// warn never moves the exit code: the only fail here is the dead socket
	if code != 1 || countFails(doctorReport{Checks: toChecks(doc)}) != 1 {
		t.Fatalf("exit %d, checks %+v", code, doc.Checks)
	}
}

// A CLAUDE.md without the layout section warns with the `pf init` fix;
// `pf init` clears it. (No CLAUDE.md at all is the clean case above
// minus init — see TestDoctorWorkspaceLayoutWarnsWithDestination, whose
// detail carries no layout-section row.)
func TestDoctorWorkspaceNotesWithoutLayoutWarns(t *testing.T) {
	env, ws := doctorWorkspace(t)
	notes := "<!-- polyforge:managed:version=\"1.0\" -->\n## Repos\n<!-- /polyforge:managed -->\n\n<!-- polyforge:layout --> quoted outside the block\n"
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(notes), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChLayout)
	if st != "warn" || !strings.Contains(detail, "pf init "+ws) || !strings.Contains(detail, "polyforge:layout") {
		t.Fatalf("layout = %s (%s), want warn suggesting pf init %s", st, detail, ws)
	}
	if code, _, stderr := runPf(t, env, "init", ws); code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLayout); st != "pass" {
		t.Fatalf("after pf init: layout = %s (%s), want pass", st, detail)
	}
}

func toChecks(doc doctorDoc) []doctorCheck {
	var out []doctorCheck
	for _, c := range doc.Checks {
		out = append(out, doctorCheck{Name: c.Name, Status: c.Status, Detail: c.Detail})
	}
	return out
}

// mtimeOnlyScratch makes the scratch age judge by mtime alone (a test
// cannot backdate a ctime).
func mtimeOnlyScratch(t *testing.T) {
	t.Helper()
	orig := workspace.ChangeTime
	workspace.ChangeTime = func(os.FileInfo) time.Time { return time.Time{} }
	t.Cleanup(func() { workspace.ChangeTime = orig })
}

func TestDoctorScratchReportsThenPrunesOnFlag(t *testing.T) {
	mtimeOnlyScratch(t)
	env, ws := doctorWorkspace(t)
	old := time.Now().Add(-15 * 24 * time.Hour)
	touch(t, filepath.Join(ws, "scratch", "old.log"), old)
	touch(t, filepath.Join(ws, "scratch", "e2e", "out.txt"), old)
	if err := os.Chtimes(filepath.Join(ws, "scratch", "e2e"), old, old); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(ws, "scratch", "fresh.log"), time.Now())
	touch(t, filepath.Join(ws, "scratch", "edge.log"), time.Now().Add(-13*24*time.Hour))

	// default: reported, nothing removed
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChScratch)
	if st != "warn" || !strings.Contains(detail, filepath.Join(ws, "scratch", "old.log")) ||
		!strings.Contains(detail, filepath.Join(ws, "scratch", "e2e")) || strings.Contains(detail, "fresh.log") ||
		strings.Contains(detail, "edge.log") || !strings.Contains(detail, "--prune-scratch") {
		t.Fatalf("report-only scratch = %s (%s)", st, detail)
	}
	if _, err := os.Stat(filepath.Join(ws, "scratch", "old.log")); err != nil {
		t.Fatalf("report-only doctor removed old.log: %v", err)
	}

	// --prune-scratch: removed and listed; the fresh entries stay
	code, stdout, _ := runPf(t, env, "doctor", "--prune-scratch")
	if code != 1 || !strings.Contains(stdout, "pruned 2 scratch entr(ies)") ||
		!strings.Contains(stdout, filepath.Join(ws, "scratch", "old.log")) {
		t.Fatalf("prune doctor: exit %d output %s", code, stdout)
	}
	left, _ := os.ReadDir(filepath.Join(ws, "scratch"))
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "edge.log,fresh.log" {
		t.Fatalf("scratch after prune = %v", names)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChScratch); st != "pass" {
		t.Fatalf("after prune scratch = %s (%s)", st, detail)
	}
}

func TestDoctorScratchSymlinkNeverPruned(t *testing.T) {
	mtimeOnlyScratch(t)
	env, ws := doctorWorkspace(t)
	target := t.TempDir()
	precious := filepath.Join(target, "precious")
	touch(t, precious, time.Now().Add(-30*24*time.Hour))
	if err := os.Symlink(target, filepath.Join(ws, "scratch")); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor", "--prune-scratch")
	if st, detail := doctorFind(t, doc, docChScratch); st != "warn" || !strings.Contains(detail, "not a plain directory") {
		t.Fatalf("scratch symlink = %s (%s)", st, detail)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("doctor --prune-scratch deleted through the symlink: %v", err)
	}
}

// TestDoctorMemoryPolicyWarnsUntilInit pins the read-only memory policy
// row: no .polyforge.yaml passes on the built-in default; a file without
// the section warns with the `pf init` fix and doctor writes nothing;
// after `pf init` the row passes.
func TestDoctorMemoryPolicyWarnsUntilInit(t *testing.T) {
	env, ws := doctorWorkspace(t)
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChMemPolicy); st != "pass" || !strings.Contains(detail, "built-in default") || !strings.Contains(detail, "editable copy") {
		t.Fatalf("no yaml: memory policy = %s (%s), want pass on the built-in default", st, detail)
	}
	steps := "# mine\nsteps:\n  polyforge:\n    default: { models: [pi:a] }\n"
	if err := os.WriteFile(workspace.PathFor(ws), []byte(steps), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChMemPolicy)
	if st != "warn" || !strings.Contains(detail, "declares no memory_policy") || !strings.Contains(detail, "run `pf init "+ws+"`") {
		t.Fatalf("memory policy = %s (%s), want warn with the pf init fix", st, detail)
	}
	if raw, _ := os.ReadFile(workspace.PathFor(ws)); string(raw) != steps {
		t.Fatalf("doctor rewrote %s", workspace.PathFor(ws))
	}
	if code, _, stderr := runPf(t, env, "init", ws); code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChMemPolicy); st != "pass" {
		t.Fatalf("memory policy after init = %s (%s), want pass", st, detail)
	}
}

func TestDoctorWorkspaceChecksSkipWithoutMapping(t *testing.T) {
	srv := aiSever(t, &reqRecorder{}, nil, 0, "")
	env := doctorServerConfig(t, srv.URL, "k", "")
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	for _, name := range []string{docChLayout, docChScratch, docChMemPolicy} {
		if st, _ := doctorFind(t, doc, name); st != "skip" {
			t.Fatalf("%s = %s without a mapping, want skip", name, st)
		}
	}
}

func TestInitVerb(t *testing.T) {
	ws := t.TempDir()
	human := "# notes\n\nkeep me\n"
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(human), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPf(t, nil, "--json", "init", ws)
	if code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	var res struct{ Workspace, Scratch, Claude_md, Polyforge_yaml string }
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res.Workspace != ws || res.Scratch != "created" || res.Claude_md != "inserted" || res.Polyforge_yaml != "created" {
		t.Fatalf("pf --json init = %s (%v)", stdout, err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if !strings.HasSuffix(string(raw), human) || !strings.Contains(string(raw), "doc.design") {
		t.Fatalf("CLAUDE.md after init:\n%s", raw)
	}
	code, stdout, _ = runPf(t, nil, "init", ws)
	if code != 0 || !strings.Contains(stdout, "scratch/  exists") || !strings.Contains(stdout, "layout section unchanged") || !strings.Contains(stdout, "memory_policy section unchanged") {
		t.Fatalf("second pf init: exit %d output %s", code, stdout)
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, workspace.FileName), []byte("steps: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runPf(t, nil, "init", bad)
	if code != 0 || !strings.Contains(stdout, "layout section created") || !strings.Contains(stdout, "memory_policy section skipped") ||
		!strings.Contains(stdout, "warning: .polyforge.yaml left untouched:") {
		t.Fatalf("pf init over a broken yaml: exit %d output %s (%s)", code, stdout, stderr)
	}
	if code, _, stderr := runPf(t, nil, "init", filepath.Join(ws, "nope")); code != 1 || !strings.Contains(stderr, "does not exist") {
		t.Fatalf("pf init on a missing dir: exit %d (%s)", code, stderr)
	}
}
