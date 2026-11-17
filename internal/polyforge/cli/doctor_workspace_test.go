package cli

// doctor_workspace_test.go: the polyforge#177 workspace checks of `pf
// doctor` (clean → pass; strays → warn with a destination, untouched;
// stale scratch reported by default, removed only with --prune-scratch)
// and `pf init`. Temp workspaces and a scratch XDG config only.

import (
	"encoding/json"
	"fmt"
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
	// a conforming scratch: one fresh batch inside a category
	touch(t, filepath.Join(ws, "scratch", "raw", "b1", "out.txt"), time.Now())
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

// A CLAUDE.md (the only notes file) without the layout section warns with the `pf init` fix;
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

// A notes file whose managed block carries an OUTDATED layout section (a
// pre-#121 one, before the scratch categories) warns as outdated with the
// `pf init` fix; `pf init` replaces the section in place and the check
// passes again (Q9).
func TestDoctorNotesLayoutOutdatedWarns(t *testing.T) {
	env, ws := doctorWorkspace(t)
	old := "<!-- polyforge:managed:version=\"1.0\" -->\n## Repos\n\n<!-- polyforge:layout -->\nold convention, no categories\n<!-- /polyforge:layout -->\n\n<!-- /polyforge:managed -->\n"
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChLayout)
	if st != "warn" || !strings.Contains(detail, "outdated layout section") || !strings.Contains(detail, "pf init "+ws) {
		t.Fatalf("outdated layout = %s (%s), want warn suggesting pf init %s", st, detail, ws)
	}
	if code, _, stderr := runPf(t, env, "init", ws); code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLayout); st != "pass" {
		t.Fatalf("after pf init: layout = %s (%s), want pass", st, detail)
	}
}

// The notes file is AGENTS.md, else CLAUDE.md: doctor
// checks AGENTS.md and names it; a block-less CLAUDE.md beside it (the
// @AGENTS.md import) is silent; a CLAUDE.md with its own managed block
// warns as a duplicate, and pf init refuses until it is removed by hand.
func TestDoctorWorkspaceAgentsNotes(t *testing.T) {
	env, ws := doctorWorkspace(t)
	agents, claude := filepath.Join(ws, "AGENTS.md"), filepath.Join(ws, "CLAUDE.md")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(agents, "<!-- polyforge:managed:version=\"1.0\" -->\n## Repos\n<!-- /polyforge:managed -->\n")
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLayout); st != "warn" || !strings.Contains(detail, agents+" has no <!-- polyforge:layout -->") || !strings.Contains(detail, "pf init "+ws) {
		t.Fatalf("only AGENTS.md: layout = %s (%s), want warn naming %s", st, detail, agents)
	}
	code, stdout, stderr := runPf(t, env, "init", ws)
	if code != 0 || !strings.Contains(stdout, "AGENTS.md  layout section inserted") {
		t.Fatalf("pf init: exit %d %s (%s)", code, stdout, stderr)
	}
	if _, err := os.Lstat(claude); !os.IsNotExist(err) {
		t.Fatalf("pf init created CLAUDE.md beside AGENTS.md: %v", err)
	}
	write(claude, "@AGENTS.md\n")
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLayout); st != "pass" {
		t.Fatalf("AGENTS.md + import-only CLAUDE.md: layout = %s (%s), want pass", st, detail)
	}
	dup := "<!-- polyforge:managed:version=\"1.0\" -->\nold\n<!-- /polyforge:managed -->\n"
	write(claude, dup)
	_, _, doc = doctorStatus(t, env, "--json", "doctor")
	if st, detail := doctorFind(t, doc, docChLayout); st != "warn" || !strings.Contains(detail, claude+" carries a duplicate polyforge-managed block") || !strings.Contains(detail, agents+" is the canonical notes file") {
		t.Fatalf("duplicate block: layout = %s (%s), want warn naming %s", st, detail, claude)
	}
	before, _ := os.ReadFile(agents)
	if code, _, stderr := runPf(t, env, "init", ws); code == 0 || !strings.Contains(stderr, "remove the block from CLAUDE.md by hand") {
		t.Fatalf("pf init over a duplicate block: exit %d (%s), want a refusal", code, stderr)
	}
	if after, _ := os.ReadFile(agents); string(after) != string(before) {
		t.Fatal("a refused pf init wrote AGENTS.md")
	}
	if got, _ := os.ReadFile(claude); string(got) != dup {
		t.Fatal("pf init edited CLAUDE.md")
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

// TestDoctorScratchWarnsEveryPath (AC1): every entry outside the
// categories is warned one by one — 25 loose top-level files, an unknown
// top-level directory and a loose file inside a category — with no
// 20-row cap on the scratch line (Q8); nothing is removed, and the
// detail says --prune-scratch never removes them.
func TestDoctorScratchWarnsEveryPath(t *testing.T) {
	mtimeOnlyScratch(t)
	env, ws := doctorWorkspace(t)
	now := time.Now()
	for i := 0; i < 25; i++ {
		touch(t, filepath.Join(ws, "scratch", fmt.Sprintf("loose-%02d.log", i)), now.Add(-30*24*time.Hour))
	}
	touch(t, filepath.Join(ws, "scratch", "unknown-dir", "keep.txt"), now.Add(-30*24*time.Hour))
	if err := os.Chtimes(filepath.Join(ws, "scratch", "unknown-dir"), now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(ws, "scratch", "raw", "notes.log"), now.Add(-30*24*time.Hour))
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChScratch)
	if st != "warn" {
		t.Fatalf("scratch = %s (%s), want warn", st, detail)
	}
	for i := 0; i < 25; i++ {
		if !strings.Contains(detail, filepath.Join(ws, "scratch", fmt.Sprintf("loose-%02d.log", i))) {
			t.Fatalf("the detail must list loose-%02d.log by its full path: %s", i, detail)
		}
	}
	for _, want := range []string{
		filepath.Join(ws, "scratch", "unknown-dir"),
		filepath.Join(ws, "scratch", "raw", "notes.log"),
		"--prune-scratch never removes them",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the detail lacks %q: %s", want, detail)
		}
	}
	if strings.Contains(detail, "… more") {
		t.Fatalf("the scratch row must not cap its path list: %s", detail)
	}
	if _, err := os.Stat(filepath.Join(ws, "scratch", "loose-00.log")); err != nil {
		t.Fatalf("doctor removed a stray: %v", err)
	}
}

// TestDoctorScratchPrunesPerRetention (AC2): --prune-scratch removes
// only the batches past their own category's retention — raw/ 3 days,
// tasks/ and sessions/ 14 days — keeps the fresh batches, the category
// roots and every entry outside the categories, and never touches
// anything outside scratch/.
func TestDoctorScratchPrunesPerRetention(t *testing.T) {
	mtimeOnlyScratch(t)
	env, ws := doctorWorkspace(t)
	now := time.Now()
	batch := func(rel string, age time.Duration) {
		t.Helper()
		p := filepath.Join(ws, "scratch", rel, "data.bin")
		touch(t, p, now.Add(-age))
		if err := os.Chtimes(filepath.Dir(p), now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	batch(filepath.Join("raw", "old-b"), 4*24*time.Hour)    // past raw's 3 days
	batch(filepath.Join("raw", "new-b"), 2*24*time.Hour)    // inside
	batch(filepath.Join("tasks", "old-t"), 15*24*time.Hour) // past 14 days
	batch(filepath.Join("tasks", "new-t"), 13*24*time.Hour) // inside
	batch(filepath.Join("sessions", "old-s"), 15*24*time.Hour)
	batch(filepath.Join("sessions", "new-s"), 13*24*time.Hour)
	touch(t, filepath.Join(ws, "scratch", "loose.log"), now.Add(-30*24*time.Hour))
	touch(t, filepath.Join(ws, "scratch", "unknown-dir", "keep.txt"), now.Add(-30*24*time.Hour))
	touch(t, filepath.Join(ws, "keep-me.log"), now.Add(-30*24*time.Hour)) // outside scratch/, never touched

	// default: reported per path, nothing removed
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChScratch)
	if st != "warn" {
		t.Fatalf("scratch = %s (%s), want warn", st, detail)
	}
	for _, rel := range []string{filepath.Join("raw", "old-b"), filepath.Join("tasks", "old-t"), filepath.Join("sessions", "old-s"), "loose.log", "unknown-dir"} {
		if !strings.Contains(detail, filepath.Join(ws, "scratch", rel)) {
			t.Fatalf("the report must list %s by its full path: %s", rel, detail)
		}
	}
	for _, rel := range []string{filepath.Join("raw", "new-b"), filepath.Join("tasks", "new-t"), filepath.Join("sessions", "new-s")} {
		if strings.Contains(detail, filepath.Join(ws, "scratch", rel)) {
			t.Fatalf("the report must not list the fresh batch %s: %s", rel, detail)
		}
	}
	for _, p := range []string{
		filepath.Join(ws, "scratch", "raw", "old-b", "data.bin"),
		filepath.Join(ws, "scratch", "loose.log"),
		filepath.Join(ws, "keep-me.log"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the report-only doctor removed %s: %v", p, err)
		}
	}

	// --prune-scratch: the three stale batches go, everything else stays
	code, stdout, _ := runPf(t, env, "doctor", "--prune-scratch")
	if code != 1 || !strings.Contains(stdout, "pruned 3 batch(es)") {
		t.Fatalf("prune doctor: exit %d output %s", code, stdout)
	}
	for _, rel := range []string{filepath.Join("raw", "old-b"), filepath.Join("tasks", "old-t"), filepath.Join("sessions", "old-s")} {
		if !strings.Contains(stdout, filepath.Join(ws, "scratch", rel)) {
			t.Fatalf("the prune must list %s: %s", rel, stdout)
		}
		if _, err := os.Lstat(filepath.Join(ws, "scratch", rel)); !os.IsNotExist(err) {
			t.Fatalf("the prune kept %s: %v", rel, err)
		}
	}
	for _, p := range []string{
		filepath.Join(ws, "scratch", "raw", "new-b", "data.bin"),
		filepath.Join(ws, "scratch", "tasks", "new-t", "data.bin"),
		filepath.Join(ws, "scratch", "sessions", "new-s", "data.bin"),
		filepath.Join(ws, "scratch", "loose.log"),
		filepath.Join(ws, "scratch", "unknown-dir", "keep.txt"),
		filepath.Join(ws, "scratch", "raw"),
		filepath.Join(ws, "scratch", "tasks"),
		filepath.Join(ws, "scratch", "sessions"),
		filepath.Join(ws, "keep-me.log"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the prune removed %s, which it must never touch: %v", p, err)
		}
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
// row: it reads cli.toml — a path without a block warns
// with the `pf init` fix whatever the yaml holds, doctor writes nothing;
// after `pf init` (which writes the cli.toml block and never the yaml)
// the row passes.
func TestDoctorMemoryPolicyWarnsUntilInit(t *testing.T) {
	env, ws := doctorWorkspace(t)
	steps := "# mine\nsteps:\n  polyforge:\n    default: { models: [pi:a] }\n"
	if err := os.WriteFile(workspace.PathFor(ws), []byte(steps), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, doc := doctorStatus(t, env, "--json", "doctor")
	st, detail := doctorFind(t, doc, docChMemPolicy)
	if st != "warn" || !strings.Contains(detail, "built-in default") || !strings.Contains(detail, "run `pf init "+ws+"`") {
		t.Fatalf("memory policy = %s (%s), want warn with the pf init fix", st, detail)
	}
	if raw, _ := os.ReadFile(workspace.PathFor(ws)); string(raw) != steps {
		t.Fatalf("doctor rewrote %s", workspace.PathFor(ws))
	}
	if code, _, stderr := runPf(t, env, "init", ws); code != 0 {
		t.Fatalf("pf init: exit %d (%s)", code, stderr)
	}
	if raw, _ := os.ReadFile(workspace.PathFor(ws)); string(raw) != steps {
		t.Fatalf("pf init rewrote %s:\n%s", workspace.PathFor(ws), raw)
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
	var res struct{ Workspace, Scratch, Notes_file, Notes_path, Memory_policy, Hint string }
	if err := json.Unmarshal([]byte(stdout), &res); err != nil || res.Workspace != ws || res.Scratch != "created" || res.Notes_file != "inserted" || res.Notes_path != "CLAUDE.md" || res.Memory_policy != "unmapped" || !strings.Contains(res.Hint, "[workspace.") {
		t.Fatalf("pf --json init = %s (%v)", stdout, err)
	}
	if strings.Contains(stdout, "claude_md") || strings.Contains(stdout, "polyforge_yaml") {
		t.Fatalf("pf --json init still carries a retired field: %s", stdout)
	}
	if _, err := os.Stat(filepath.Join(ws, workspace.FileName)); !os.IsNotExist(err) {
		t.Fatalf("pf init created %s (stat err %v)", workspace.FileName, err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws, "CLAUDE.md"))
	if !strings.HasSuffix(string(raw), human) || !strings.Contains(string(raw), "doc.design") {
		t.Fatalf("CLAUDE.md after init:\n%s", raw)
	}
	code, stdout, _ = runPf(t, nil, "init", ws)
	if code != 0 || !strings.Contains(stdout, "scratch/  exists") || !strings.Contains(stdout, "CLAUDE.md  layout section unchanged") || !strings.Contains(stdout, ".default] unmapped") {
		t.Fatalf("second pf init: exit %d output %s", code, stdout)
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, workspace.FileName), []byte("steps: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runPf(t, nil, "init", bad)
	if code != 0 || !strings.Contains(stdout, "AGENTS.md  layout section created") {
		t.Fatalf("pf init over a broken yaml: exit %d output %s (%s)", code, stdout, stderr)
	}
	if raw, _ := os.ReadFile(filepath.Join(bad, workspace.FileName)); string(raw) != "steps: [\n" {
		t.Fatalf("pf init touched the yaml: %q", raw)
	}
	if code, _, stderr := runPf(t, nil, "init", filepath.Join(ws, "nope")); code != 1 || !strings.Contains(stderr, "does not exist") {
		t.Fatalf("pf init on a missing dir: exit %d (%s)", code, stderr)
	}
}
