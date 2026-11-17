package workspace

// layout_test.go: the runtime-artifact convention (polyforge#177,
// #121) — Init's idempotent notes-file upsert (AGENTS.md, else
// CLAUDE.md) and category creation, the stray-entry classification and
// the scratch category layout: retention, strays and prune rules. Temp
// dirs only.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitFreshWorkspace(t *testing.T) {
	ws := t.TempDir()
	res, err := Init(ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scratch != "created" || res.NotesFile != "created" {
		t.Fatalf("fresh init = %+v", res)
	}
	if st, err := os.Stat(filepath.Join(ws, ScratchDir)); err != nil || !st.IsDir() {
		t.Fatalf("scratch/ not created: %v", err)
	}
	// the fixed categories come with it
	for _, cat := range []string{ScratchRawDir, ScratchTasksDir, ScratchSessionsDir} {
		if st, err := os.Stat(filepath.Join(ws, ScratchDir, cat)); err != nil || !st.IsDir() {
			t.Fatalf("scratch/%s not created: %v", cat, err)
		}
	}
	text := readFile(t, filepath.Join(ws, AgentsNotes))
	for _, want := range []string{managedOpen, managedClose, layoutOpen, layoutClose, "doc.design", "doc.review", "doc.decision", "doc.audit", "experience.*", "fact.*", "scratch/", ".repo/", "capsules/", ".polyforge/", "raw/", "tasks/", "sessions/", "3 days", "14 days", "agents/<id>/", "--prune-scratch"} {
		if !strings.Contains(text, want) {
			t.Fatalf("AGENTS.md lacks %q:\n%s", want, text)
		}
	}
	// a second run changes nothing
	res, err = Init(ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scratch != "exists" || res.NotesFile != "unchanged" {
		t.Fatalf("second init = %+v", res)
	}
	if again := readFile(t, filepath.Join(ws, AgentsNotes)); again != text {
		t.Fatalf("second init rewrote AGENTS.md:\n%s", again)
	}
	if res.NotesPath != AgentsNotes {
		t.Fatalf("notes_path = %q, want %s", res.NotesPath, AgentsNotes)
	}
	if _, err := os.Lstat(filepath.Join(ws, ClaudeNotes)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a fresh init created CLAUDE.md: %v", err)
	}
}

func TestInitKeepsHumanPartAndManagedListing(t *testing.T) {
	ws := t.TempDir()
	orig := managedOpen + "\n## Workspace\n\n### polyforge\nrepo listing\n" + managedClose + "\n\n## My notes\n\nhuman text\n"
	writeFile(t, filepath.Join(ws, ClaudeNotes), orig)
	res, err := Init(ws)
	if err != nil {
		t.Fatal(err)
	}
	if res.NotesFile != "inserted" {
		t.Fatalf("init = %+v, want inserted", res)
	}
	text := readFile(t, filepath.Join(ws, ClaudeNotes))
	want := managedOpen + "\n## Workspace\n\n### polyforge\nrepo listing\n\n" + LayoutBlock() + managedClose + "\n\n## My notes\n\nhuman text\n"
	if text != want {
		t.Fatalf("CLAUDE.md =\n%s\nwant\n%s", text, want)
	}
	// idempotent
	if res, err := Init(ws); err != nil || res.NotesFile != "unchanged" {
		t.Fatalf("second init = %+v, %v", res, err)
	}
	// an outdated layout section is replaced in place, nothing else moves
	old := strings.Replace(text, LayoutBlock(), layoutOpen+"\nold convention\n"+layoutClose+"\n", 1)
	writeFile(t, filepath.Join(ws, ClaudeNotes), old)
	if res, err := Init(ws); err != nil || res.NotesFile != "updated" {
		t.Fatalf("refresh init = %+v, %v", res, err)
	}
	if got := readFile(t, filepath.Join(ws, ClaudeNotes)); got != want {
		t.Fatalf("refreshed CLAUDE.md =\n%s\nwant\n%s", got, want)
	}
}

func TestInitWithoutManagedBlockPrependsOne(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ClaudeNotes), "# Human notes\n")
	if res, err := Init(ws); err != nil || res.NotesFile != "inserted" {
		t.Fatalf("init = %+v, %v", res, err)
	}
	text := readFile(t, filepath.Join(ws, ClaudeNotes))
	if !strings.HasPrefix(text, managedOpen+"\n"+LayoutBlock()+managedClose+"\n\n") || !strings.HasSuffix(text, "# Human notes\n") {
		t.Fatalf("CLAUDE.md =\n%s", text)
	}
}

func TestInitRefusesBrokenMarkersAndScratchFile(t *testing.T) {
	ws := t.TempDir()
	broken := "x\n" + managedOpen + "\n" + layoutOpen + "\nno end\n" + managedClose + "\n"
	writeFile(t, filepath.Join(ws, ClaudeNotes), broken)
	if _, err := Init(ws); err == nil {
		t.Fatal("init accepted an unclosed layout section")
	}
	if got := readFile(t, filepath.Join(ws, ClaudeNotes)); got != broken {
		t.Fatalf("a refused init wrote CLAUDE.md: %q", got)
	}
	ws2 := t.TempDir()
	writeFile(t, filepath.Join(ws2, ScratchDir), "a file")
	if _, err := Init(ws2); err == nil {
		t.Fatal("init accepted a scratch file in place of scratch/")
	}
	// a category name occupied by a file (or a symlink) is refused too:
	// pf never follows it and never silently moves it away
	ws3 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws3, ScratchDir), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ws3, ScratchDir, ScratchTasksDir), "a file")
	if _, err := Init(ws3); err == nil || !strings.Contains(err.Error(), ScratchTasksDir) {
		t.Fatalf("init accepted a file in place of scratch/%s: %v", ScratchTasksDir, err)
	}
	if _, err := os.Stat(filepath.Join(ws3, ScratchDir, ScratchTasksDir)); err != nil {
		t.Fatalf("init moved the file away: %v", err)
	}
}

func TestStrayEntries(t *testing.T) {
	ws := t.TempDir()
	for _, d := range []string{".repo/a", "capsules/x", "scratch", ".polyforge/repo-map", ".polyforge/watchers", ".polyforge/workflows/notes", ".pi", ".claude", "docs", "clone/.git"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{".polyforge.yaml", "CLAUDE.md", "AGENTS.md", "go.work", "go.work.sum", ".gitignore", ".mcp.json", ".env.example",
		".polyforge.yaml.bak-v270", "notes.md", "run.log", ".polyforge/design.md", ".polyforge/state.json", ".polyforge/repo-map/x.md"} {
		writeFile(t, filepath.Join(ws, f), "x")
	}
	got, err := StrayEntries(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []Stray{
		{".polyforge.yaml.bak-v270", DestScratch},
		{".polyforge/design.md", DestAihub},
		{".polyforge/state.json", DestScratch},
		{"clone", DestRepo},
		{"docs", DestDir},
		{"notes.md", DestAihub},
		{"run.log", DestScratch},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strays =\n%+v\nwant\n%+v", got, want)
	}
	clean := t.TempDir()
	if _, err := Init(clean); err != nil {
		t.Fatal(err)
	}
	if got, err := StrayEntries(clean); err != nil || len(got) != 0 {
		t.Fatalf("an initialized workspace has strays %+v (%v)", got, err)
	}
}

// mtimeOnly makes the scratch age judge by mtime alone for the test
// (a test cannot backdate a ctime: Chtimes itself sets it to now).
func mtimeOnly(t *testing.T) {
	t.Helper()
	orig := ChangeTime
	ChangeTime = func(os.FileInfo) time.Time { return time.Time{} }
	t.Cleanup(func() { ChangeTime = orig })
}

// scratchBatch writes one batch directory under ws/scratch/<name> with a
// file in it and backdates every path's mtime to at (the judge is mtime-
// only in these tests — mtimeOnly below).
func scratchBatch(t *testing.T, ws, name string, at time.Time) {
	t.Helper()
	p := filepath.Join(ws, ScratchDir, name)
	writeFile(t, filepath.Join(p, "data.bin"), "x")
	for _, q := range []string{filepath.Join(p, "data.bin"), p} {
		if err := os.Chtimes(q, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScratchCategoryClean: a tree that keeps the layout reports clean —
// one fresh batch per category, empty category directories (never stale,
// never a prune target), and a workspace without scratch/.
func TestScratchCategoryClean(t *testing.T) {
	now := time.Now()
	ws := t.TempDir()
	if _, err := Init(ws); err != nil {
		t.Fatal(err)
	}
	for _, cat := range []string{ScratchRawDir, ScratchTasksDir, ScratchSessionsDir} {
		scratchBatch(t, ws, cat+"/b1", now.Add(-time.Hour))
	}
	for _, prune := range []bool{false, true} {
		rep, err := Scratch(ws, now, prune)
		if err != nil || len(rep.Stale) != 0 || len(rep.Stray) != 0 || len(rep.Skipped) != 0 {
			t.Fatalf("a conforming tree must report clean (prune=%v): %+v %v", prune, rep, err)
		}
	}
	// empty categories and no scratch/ at all are clean too
	ws2 := t.TempDir()
	for _, cat := range []string{ScratchRawDir, ScratchTasksDir, ScratchSessionsDir} {
		if err := os.MkdirAll(filepath.Join(ws2, ScratchDir, cat), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if rep, err := Scratch(ws2, now, false); err != nil || len(rep.Stale) != 0 || len(rep.Stray) != 0 {
		t.Fatalf("empty categories must be clean: %+v %v", rep, err)
	}
	if rep, err := Scratch(t.TempDir(), now, false); err != nil || rep.Stale != nil || rep.Stray != nil {
		t.Fatalf("no scratch/: %+v %v", rep, err)
	}
}

// TestScratchCategoryStrays: everything outside the categories — a loose
// top-level file, an unknown top-level directory, a category name
// occupied by a file, a loose file inside a category — is listed one by
// one, at any age, and never removed, not even with prune.
func TestScratchCategoryStrays(t *testing.T) {
	mtimeOnly(t)
	ws := t.TempDir()
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	writeFile(t, filepath.Join(ws, ScratchDir, "loose.log"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, "unknown-dir", "keep.txt"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, ScratchTasksDir), "a file")
	writeFile(t, filepath.Join(ws, ScratchDir, ScratchRawDir, "notes.log"), "x")
	for _, p := range []string{
		filepath.Join(ws, ScratchDir, "loose.log"),
		filepath.Join(ws, ScratchDir, "unknown-dir"),
		filepath.Join(ws, ScratchDir, "unknown-dir", "keep.txt"),
		filepath.Join(ws, ScratchDir, ScratchTasksDir),
		filepath.Join(ws, ScratchDir, ScratchRawDir, "notes.log"),
	} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"loose.log", "raw/notes.log", "tasks", "unknown-dir"}
	rep, err := Scratch(ws, now, false)
	if err != nil || !reflect.DeepEqual(rep.Stray, want) || len(rep.Stale) != 0 || len(rep.Skipped) != 0 {
		t.Fatalf("strays = %+v, want %v (%v)", rep, want, err)
	}
	// prune never removes a stray, however old
	rep, err = Scratch(ws, now, true)
	if err != nil || !reflect.DeepEqual(rep.Stray, want) || len(rep.Stale) != 0 {
		t.Fatalf("prune must not touch strays: %+v %v", rep, err)
	}
	for _, p := range []string{
		filepath.Join(ws, ScratchDir, "loose.log"),
		filepath.Join(ws, ScratchDir, "unknown-dir", "keep.txt"),
		filepath.Join(ws, ScratchDir, ScratchTasksDir),
		filepath.Join(ws, ScratchDir, ScratchRawDir, "notes.log"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("prune removed the stray %s: %v", p, err)
		}
	}
}

// TestScratchCategoryRetention: each category judges its batches by its
// own retention — raw/ 3 days, tasks/ and sessions/ 14 days — by the
// newest change of the whole batch subtree; a batch exactly at the
// boundary stays; prune removes only the stale batches, keeps the
// category roots, is idempotent, and never deletes a just-arrived
// download that keeps an old mtime (its ctime counts).
func TestScratchCategoryRetention(t *testing.T) {
	mtimeOnly(t)
	ws := t.TempDir()
	now := time.Now()
	scratchBatch(t, ws, ScratchRawDir+"/old-batch", now.Add(-4*24*time.Hour))   // past raw's 3 days
	scratchBatch(t, ws, ScratchRawDir+"/new-batch", now.Add(-2*24*time.Hour))   // inside
	scratchBatch(t, ws, ScratchRawDir+"/edge-batch", now.Add(-RawMaxAge))       // exactly at the boundary: kept
	scratchBatch(t, ws, ScratchTasksDir+"/old-b", now.Add(-15*24*time.Hour))    // past 14 days
	scratchBatch(t, ws, ScratchTasksDir+"/new-b", now.Add(-13*24*time.Hour))    // inside
	scratchBatch(t, ws, ScratchSessionsDir+"/old-b", now.Add(-15*24*time.Hour)) // past 14 days
	scratchBatch(t, ws, ScratchSessionsDir+"/new-b", now.Add(-13*24*time.Hour)) // inside
	// a batch whose subtree has one fresh descendant is not stale
	scratchBatch(t, ws, ScratchRawDir+"/mixed-b", now.Add(-10*24*time.Hour))
	writeFile(t, filepath.Join(ws, ScratchDir, ScratchRawDir, "mixed-b", "new.txt"), "x")
	if err := os.Chtimes(filepath.Join(ws, ScratchDir, ScratchRawDir, "mixed-b", "new.txt"), now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := []string{"raw/old-batch", "sessions/old-b", "tasks/old-b"}
	rep, err := Scratch(ws, now, false)
	if err != nil || !reflect.DeepEqual(rep.Stale, want) || len(rep.Stray) != 0 || len(rep.Skipped) != 0 {
		t.Fatalf("stale = %+v, want %v (%v)", rep, want, err)
	}
	pruned, err := Scratch(ws, now, true)
	if err != nil || !reflect.DeepEqual(pruned.Stale, want) {
		t.Fatalf("prune = %+v, want %v (%v)", pruned, want, err)
	}
	left, _ := os.ReadDir(filepath.Join(ws, ScratchDir))
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{ScratchRawDir, ScratchSessionsDir, ScratchTasksDir}) {
		t.Fatalf("scratch after prune = %v: the category roots must stay", names)
	}
	for _, keep := range []string{
		ScratchRawDir + "/new-batch", ScratchRawDir + "/edge-batch", ScratchRawDir + "/mixed-b",
		ScratchTasksDir + "/new-b", ScratchSessionsDir + "/new-b",
	} {
		if _, err := os.Stat(filepath.Join(ws, ScratchDir, keep)); err != nil {
			t.Fatalf("prune removed %s: %v", keep, err)
		}
	}
	for _, gone := range []string{ScratchRawDir + "/old-batch", ScratchTasksDir + "/old-b", ScratchSessionsDir + "/old-b"} {
		if _, err := os.Lstat(filepath.Join(ws, ScratchDir, gone)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("prune kept %s: %v", gone, err)
		}
	}
	// a second prune finds nothing
	if rep, err := Scratch(ws, now, true); err != nil || len(rep.Stale) != 0 {
		t.Fatalf("a second prune must be a no-op: %+v %v", rep, err)
	}
	// a download keeping an old mtime but arriving now (its ctime) is
	// not stale on arrival, in every category
	ws2 := t.TempDir()
	backdated := time.Now().Add(-30 * 24 * time.Hour)
	scratchBatch(t, ws2, ScratchRawDir+"/arrival", backdated)
	scratchBatch(t, ws2, ScratchTasksDir+"/arrival", backdated)
	for _, prune := range []bool{false, true} {
		rep, err := Scratch(ws2, time.Now(), prune)
		if err != nil || len(rep.Stale) != 0 {
			t.Fatalf("a backdated-mtime arrival is stale (prune=%v): %+v %v", prune, rep, err)
		}
	}
	for _, keep := range []string{ScratchRawDir + "/arrival", ScratchTasksDir + "/arrival"} {
		if _, err := os.Stat(filepath.Join(ws2, ScratchDir, keep)); err != nil {
			t.Fatalf("prune removed a fresh arrival: %v", err)
		}
	}
}

// TestScratchCategorySymlinks: a scratch that is a symlink is refused; a
// category replaced by a symlink and a batch that is a symlink are
// strays, never followed, never removed — and the race a replacement
// between the report and the prune run loses to the re-scan: the outside
// target keeps everything.
func TestScratchCategorySymlinks(t *testing.T) {
	mtimeOnly(t)
	now := time.Now()
	// scratch itself a symlink: never scanned or pruned — its target may
	// be anything ($HOME, /)
	ws := t.TempDir()
	target := t.TempDir()
	precious := filepath.Join(target, "precious")
	writeFile(t, precious, "x")
	if err := os.Chtimes(precious, now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(ws, ScratchDir)); err != nil {
		t.Fatal(err)
	}
	for _, prune := range []bool{false, true} {
		if _, err := Scratch(ws, now, prune); err == nil || !strings.Contains(err.Error(), "not a plain directory") {
			t.Fatalf("Scratch(prune=%v) on a symlinked scratch: %v", prune, err)
		}
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("the symlink target lost its file: %v", err)
	}
	if _, err := Init(ws); err == nil {
		t.Fatal("init accepted a symlinked scratch")
	}
	// a category replaced by a symlink: a stray, never followed, never
	// removed — the target outside keeps its old files
	ws2 := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep.txt"), "x")
	if err := os.Chtimes(filepath.Join(outside, "keep.txt"), now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	scratchBatch(t, ws2, ScratchRawDir+"/fresh-b", now.Add(-time.Hour))
	if err := os.Symlink(outside, filepath.Join(ws2, ScratchDir, ScratchTasksDir)); err != nil {
		t.Fatal(err)
	}
	rep, err := Scratch(ws2, now, true)
	if err != nil || !reflect.DeepEqual(rep.Stray, []string{"tasks"}) || len(rep.Stale) != 0 {
		t.Fatalf("a symlinked category must be a stray, never scanned: %+v %v", rep, err)
	}
	if fi, err := os.Lstat(filepath.Join(ws2, ScratchDir, ScratchTasksDir)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("prune removed or replaced the category symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("prune followed the category symlink: %v", err)
	}
	// a batch that is a symlink: a stray, never pruned
	ws3 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws3, ScratchDir, ScratchRawDir, "link-batch")); err != nil {
		t.Fatal(err)
	}
	rep, err = Scratch(ws3, now, true)
	if err != nil || !reflect.DeepEqual(rep.Stray, []string{"raw/link-batch"}) || len(rep.Stale) != 0 {
		t.Fatalf("a symlink batch must be a stray, never pruned: %+v %v", rep, err)
	}
	if fi, err := os.Lstat(filepath.Join(ws3, ScratchDir, ScratchRawDir, "link-batch")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("prune removed the batch symlink: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("prune followed the batch symlink: %v", err)
	}
	// the race: the report calls a raw batch stale; before the prune run
	// the batch is replaced by a symlink to an outside directory — the
	// prune re-scans, sees a symlink, refuses; the outside keeps its files
	ws4 := t.TempDir()
	scratchBatch(t, ws4, ScratchRawDir+"/stale-batch", now.Add(-30*24*time.Hour))
	if rep, err := Scratch(ws4, now, false); err != nil || !reflect.DeepEqual(rep.Stale, []string{"raw/stale-batch"}) {
		t.Fatalf("the report must call the batch stale: %+v %v", rep, err)
	}
	if err := os.RemoveAll(filepath.Join(ws4, ScratchDir, ScratchRawDir, "stale-batch")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(ws4, ScratchDir, ScratchRawDir, "stale-batch")); err != nil {
		t.Fatal(err)
	}
	rep, err = Scratch(ws4, now, true)
	if err != nil || len(rep.Stale) != 0 || !reflect.DeepEqual(rep.Stray, []string{"raw/stale-batch"}) {
		t.Fatalf("the prune run must re-scan and refuse the replacement: %+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("the prune deleted through the replacement: %v", err)
	}
}

func TestInitMarkerEdgeCases(t *testing.T) {
	t.Run("two layout sections refused", func(t *testing.T) {
		ws := t.TempDir()
		body := managedOpen + "\n" + LayoutBlock() + LayoutBlock() + managedClose + "\n"
		writeFile(t, filepath.Join(ws, ClaudeNotes), body)
		if _, err := Init(ws); err == nil || !strings.Contains(err.Error(), "2") {
			t.Fatalf("init on two sections: %v", err)
		}
		if readFile(t, filepath.Join(ws, ClaudeNotes)) != body {
			t.Fatal("a refused init wrote CLAUDE.md")
		}
	})
	t.Run("marker quoted in the human part is ignored", func(t *testing.T) {
		ws := t.TempDir()
		human := "\n## notes\n\nthe section starts at `" + layoutOpen + "` and ends at `" + layoutClose + "`\n"
		writeFile(t, filepath.Join(ws, ClaudeNotes), managedOpen+"\nlisting\n"+managedClose+"\n"+human)
		if res, err := Init(ws); err != nil || res.NotesFile != "inserted" {
			t.Fatalf("init = %+v %v", res, err)
		}
		want := managedOpen + "\nlisting\n\n" + LayoutBlock() + managedClose + "\n" + human
		if got := readFile(t, filepath.Join(ws, ClaudeNotes)); got != want {
			t.Fatalf("CLAUDE.md =\n%s\nwant\n%s", got, want)
		}
		if res, err := Init(ws); err != nil || res.NotesFile != "unchanged" {
			t.Fatalf("second init = %+v %v", res, err)
		}
	})
	t.Run("CRLF kept", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, ClaudeNotes), managedOpen+"\r\nlisting\r\n"+managedClose+"\r\n# me\r\n")
		if _, err := Init(ws); err != nil {
			t.Fatal(err)
		}
		got := readFile(t, filepath.Join(ws, ClaudeNotes))
		if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
			t.Fatalf("mixed line endings after init: %q", got)
		}
		if res, err := Init(ws); err != nil || res.NotesFile != "unchanged" {
			t.Fatalf("second init on CRLF = %+v %v", res, err)
		}
	})
	t.Run("symlinked CLAUDE.md: target written, link kept, mode kept", func(t *testing.T) {
		ws := t.TempDir()
		real := filepath.Join(t.TempDir(), "notes.md")
		writeFile(t, real, "# shared notes\n")
		if err := os.Chmod(real, 0o640); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(ws, ClaudeNotes)
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Init(ws); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("CLAUDE.md is no longer a symlink: %v", err)
		}
		fi, _ := os.Stat(real)
		if got := readFile(t, real); !strings.Contains(got, layoutOpen) || !strings.HasSuffix(got, "# shared notes\n") || fi.Mode().Perm() != 0o640 {
			t.Fatalf("target = %q mode %v", got, fi.Mode().Perm())
		}
		left, _ := os.ReadDir(filepath.Dir(real))
		if len(left) != 1 {
			t.Fatalf("temp files left beside the target: %v", left)
		}
	})
}

// LayoutSection: no CLAUDE.md, no managed block, a layout marker only
// outside the block, the section in place, and an unclosed block (an
// error, as pf init refuses it).
func TestLayoutSection(t *testing.T) {
	cases := []struct {
		name, notes      string // notes "" = no CLAUDE.md
		exists, has, err bool
	}{
		{"absent", "", false, false, false},
		{"no managed block", "# notes\n", true, false, false},
		{"marker outside the block", managedOpen + "\n" + managedClose + "\n" + layoutOpen + "\n", true, false, false},
		{"in place", managedOpen + "\n" + LayoutBlock() + managedClose + "\n", true, true, false},
		{"unclosed block", managedOpen + "\n" + LayoutBlock(), true, false, true},
	}
	for _, c := range cases {
		root := t.TempDir()
		if c.notes != "" {
			writeFile(t, filepath.Join(root, ClaudeNotes), c.notes)
		}
		exists, has, err := LayoutSection(filepath.Join(root, ClaudeNotes))
		if exists != c.exists || has != c.has || (err != nil) != c.err {
			t.Fatalf("%s: LayoutSection = %v, %v, %v; want %v, %v, err=%v", c.name, exists, has, err, c.exists, c.has, c.err)
		}
	}
}

// TestInitToleratesDirSyncFailure: when only the directory fsync after
// the rename fails, the files are in place and `pf init` succeeds; any
// other write failure is still reported.
func TestInitToleratesDirSyncFailure(t *testing.T) {
	orig := layoutWrite
	t.Cleanup(func() { layoutWrite = orig })
	layoutWrite = func(path string, data []byte, perm os.FileMode) error {
		if err := os.WriteFile(path, data, perm); err != nil {
			return err
		}
		return fmt.Errorf("%w: injected", atomicfile.ErrDirSync)
	}
	ws := t.TempDir()
	res, err := Init(ws)
	if err != nil {
		t.Fatalf("a directory-fsync failure must not fail init: %v", err)
	}
	if res.NotesFile != "created" || !strings.Contains(readFile(t, filepath.Join(ws, AgentsNotes)), layoutOpen) {
		t.Fatalf("init = %+v; AGENTS.md must carry the layout", res)
	}
	layoutWrite = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	if _, err := Init(t.TempDir()); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("a real write failure must be reported, got %v", err)
	}
}

// The notes file resolution: AGENTS.md, else
// CLAUDE.md; a separate CLAUDE.md with its own managed block beside
// AGENTS.md is refused, a block-less one ignored; a symlink pair is one
// file.
func TestInitNotesFileResolution(t *testing.T) {
	block := managedOpen + "\n" + LayoutBlock() + managedClose + "\n"
	t.Run("only AGENTS.md", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, AgentsNotes), "# agents\n")
		res, err := Init(ws)
		if err != nil || res.NotesFile != "inserted" || res.NotesPath != AgentsNotes {
			t.Fatalf("init = %+v, %v", res, err)
		}
		if got := readFile(t, filepath.Join(ws, AgentsNotes)); got != block+"\n# agents\n" {
			t.Fatalf("AGENTS.md =\n%s", got)
		}
		if _, err := os.Lstat(filepath.Join(ws, ClaudeNotes)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("init created CLAUDE.md beside AGENTS.md: %v", err)
		}
	})
	t.Run("only CLAUDE.md", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, ClaudeNotes), "# claude\n")
		res, err := Init(ws)
		if err != nil || res.NotesFile != "inserted" || res.NotesPath != ClaudeNotes {
			t.Fatalf("init = %+v, %v", res, err)
		}
		if got := readFile(t, filepath.Join(ws, ClaudeNotes)); got != block+"\n# claude\n" {
			t.Fatalf("CLAUDE.md =\n%s", got)
		}
		if _, err := os.Lstat(filepath.Join(ws, AgentsNotes)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("init created AGENTS.md beside CLAUDE.md: %v", err)
		}
	})
	t.Run("both, CLAUDE.md without a block", func(t *testing.T) {
		ws := t.TempDir()
		writeFile(t, filepath.Join(ws, AgentsNotes), "# agents\n")
		writeFile(t, filepath.Join(ws, ClaudeNotes), "@AGENTS.md\n")
		res, err := Init(ws)
		if err != nil || res.NotesPath != AgentsNotes || res.NotesFile != "inserted" {
			t.Fatalf("init = %+v, %v", res, err)
		}
		if got := readFile(t, filepath.Join(ws, ClaudeNotes)); got != "@AGENTS.md\n" {
			t.Fatalf("init touched CLAUDE.md: %q", got)
		}
	})
	t.Run("both, CLAUDE.md with a duplicate block refused", func(t *testing.T) {
		ws := t.TempDir()
		claude := managedOpen + "\nlisting\n" + managedClose + "\n"
		writeFile(t, filepath.Join(ws, AgentsNotes), "# agents\n")
		writeFile(t, filepath.Join(ws, ClaudeNotes), claude)
		_, err := Init(ws)
		if err == nil || !strings.Contains(err.Error(), ClaudeNotes) || !strings.Contains(err.Error(), "by hand") {
			t.Fatalf("init on a duplicate block: %v", err)
		}
		if readFile(t, filepath.Join(ws, AgentsNotes)) != "# agents\n" || readFile(t, filepath.Join(ws, ClaudeNotes)) != claude {
			t.Fatal("a refused init wrote a notes file")
		}
	})
	for _, dir := range []struct{ link, target string }{{ClaudeNotes, AgentsNotes}, {AgentsNotes, ClaudeNotes}} {
		t.Run("symlink "+dir.link+" -> "+dir.target, func(t *testing.T) {
			ws := t.TempDir()
			writeFile(t, filepath.Join(ws, dir.target), managedOpen+"\nlisting\n"+managedClose+"\n")
			if err := os.Symlink(dir.target, filepath.Join(ws, dir.link)); err != nil {
				t.Fatal(err)
			}
			res, err := Init(ws)
			if err != nil || res.NotesPath != AgentsNotes || res.NotesFile != "inserted" {
				t.Fatalf("init = %+v, %v", res, err)
			}
			if fi, err := os.Lstat(filepath.Join(ws, dir.link)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s is no longer a symlink: %v", dir.link, err)
			}
			if got := readFile(t, filepath.Join(ws, dir.target)); !strings.Contains(got, layoutOpen) {
				t.Fatalf("%s lacks the layout:\n%s", dir.target, got)
			}
		})
	}
}

func TestNotes(t *testing.T) {
	cases := []struct {
		name, agents, claude string // "" = absent
		want                 string
		dup                  bool
	}{
		{"neither", "", "", AgentsNotes, false},
		{"only AGENTS.md", "x\n", "", AgentsNotes, false},
		{"only CLAUDE.md", "", "x\n", ClaudeNotes, false},
		{"both, import only", "x\n", "@AGENTS.md\n", AgentsNotes, false},
		{"both, duplicate block", "x\n", managedOpen + "\n" + managedClose + "\n", AgentsNotes, true},
		{"both, unclosed duplicate block", "x\n", managedOpen + "\n", AgentsNotes, true},
	}
	for _, c := range cases {
		ws := t.TempDir()
		if c.agents != "" {
			writeFile(t, filepath.Join(ws, AgentsNotes), c.agents)
		}
		if c.claude != "" {
			writeFile(t, filepath.Join(ws, ClaudeNotes), c.claude)
		}
		name, dup, err := Notes(ws)
		if err != nil || name != c.want || dup != c.dup {
			t.Fatalf("%s: Notes = %q, %v, %v; want %q, %v", c.name, name, dup, err, c.want, c.dup)
		}
	}
}

func TestIsRepoCloneAndRepoCloneDir(t *testing.T) {
	ws := t.TempDir()
	// a clone: .git is a directory
	clone := filepath.Join(ws, ReposDir, "ra")
	writeFile(t, filepath.Join(clone, ".git", "HEAD"), "ref: refs/heads/main\n")
	// a linked worktree's pointer file: .git is a regular file
	wt := filepath.Join(ws, ReposDir, "rb")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: /elsewhere/rb.git\n")
	// a plain directory: no .git of its own
	plain := filepath.Join(ws, ReposDir, "rc")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsRepoClone(clone) {
		t.Errorf("IsRepoClone(%s) = false, want true (.git directory)", clone)
	}
	if !IsRepoClone(wt) {
		t.Errorf("IsRepoClone(%s) = false, want true (.git pointer file)", wt)
	}
	if IsRepoClone(plain) {
		t.Errorf("IsRepoClone(%s) = true, want false (no .git)", plain)
	}
	if IsRepoClone(filepath.Join(ws, ReposDir, "missing")) {
		t.Errorf("IsRepoClone of a missing dir = true, want false")
	}
	if got, want := RepoCloneDir(ws, "ra"), filepath.Join(ws, ".repo", "ra"); got != want {
		t.Errorf("RepoCloneDir = %q, want %q", got, want)
	}
}
