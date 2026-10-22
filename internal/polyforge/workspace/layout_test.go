package workspace

// layout_test.go: the runtime-artifact convention (polyforge#177) —
// Init's idempotent notes-file upsert (AGENTS.md, else CLAUDE.md), the stray-entry classification
// and the scratch staleness/prune rules. Temp dirs only.

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
	text := readFile(t, filepath.Join(ws, AgentsNotes))
	for _, want := range []string{managedOpen, managedClose, layoutOpen, layoutClose, "doc.design", "doc.review", "doc.decision", "doc.audit", "experience.*", "fact.*", "scratch/", ".repo/", "capsules/", ".polyforge/", "14 days", "--prune-scratch"} {
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
}

func TestStrayEntries(t *testing.T) {
	ws := t.TempDir()
	for _, d := range []string{".repo/a", "capsules/x", "scratch", ".polyforge/repo-map", ".polyforge/watchers", ".pi", ".claude", "docs", "clone/.git"} {
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

func TestStaleScratchAndPrune(t *testing.T) {
	mtimeOnly(t)
	ws := t.TempDir()
	now := time.Now()
	old := now.Add(-15 * 24 * time.Hour)
	set := func(rel string, at time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(ws, ScratchDir, rel), at, at); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(ws, ScratchDir, "old.log"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, "fresh.log"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, "olddir", "a.txt"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, "mixed", "old.txt"), "x")
	writeFile(t, filepath.Join(ws, ScratchDir, "mixed", "new.txt"), "x")
	set("old.log", old)
	set("olddir/a.txt", old)
	set("olddir", old)
	set("mixed/old.txt", old)
	set("mixed", old) // the dir is old, but new.txt under it is fresh
	rep, err := Scratch(ws, now, ScratchMaxAge, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"old.log", "olddir"}; !reflect.DeepEqual(rep.Stale, want) || len(rep.Skipped) != 0 {
		t.Fatalf("stale = %+v, want %v", rep, want)
	}
	pruned, err := Scratch(ws, now, ScratchMaxAge, true)
	if err != nil || !reflect.DeepEqual(pruned.Stale, rep.Stale) {
		t.Fatalf("prune = %+v, %v", pruned, err)
	}
	left, _ := os.ReadDir(filepath.Join(ws, ScratchDir))
	var names []string
	for _, e := range left {
		names = append(names, e.Name())
	}
	if want := []string{"fresh.log", "mixed"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("scratch after prune = %v, want %v", names, want)
	}
	if rep, err := Scratch(t.TempDir(), now, ScratchMaxAge, false); err != nil || rep.Stale != nil {
		t.Fatalf("no scratch/: %+v %v", rep, err)
	}
}

// TestScratchCountsCtime: a download or an extracted archive keeps an
// old mtime but arrives now (its ctime) — it is not stale on arrival.
func TestScratchCountsCtime(t *testing.T) {
	ws := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour)
	writeFile(t, filepath.Join(ws, ScratchDir, "download.tar"), "x")
	if err := os.Chtimes(filepath.Join(ws, ScratchDir, "download.tar"), old, old); err != nil {
		t.Fatal(err)
	}
	rep, err := Scratch(ws, time.Now(), ScratchMaxAge, false)
	if err != nil || len(rep.Stale) != 0 {
		t.Fatalf("a backdated-mtime arrival is stale: %+v %v", rep, err)
	}
	rep, err = Scratch(ws, time.Now(), ScratchMaxAge, true)
	if err != nil || len(rep.Stale) != 0 {
		t.Fatalf("prune removed a fresh arrival: %+v %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(ws, ScratchDir, "download.tar")); err != nil {
		t.Fatal(err)
	}
}

// TestScratchSymlinkNeverFollowed: a scratch that is a symlink is never
// scanned or pruned — its target may be anything ($HOME, /).
func TestScratchSymlinkNeverFollowed(t *testing.T) {
	mtimeOnly(t)
	ws := t.TempDir()
	target := t.TempDir()
	old := time.Now().Add(-30 * 24 * time.Hour)
	precious := filepath.Join(target, "precious")
	writeFile(t, precious, "x")
	if err := os.Chtimes(precious, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(ws, ScratchDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Scratch(ws, time.Now(), ScratchMaxAge, false); err == nil || !strings.Contains(err.Error(), "not a plain directory") {
		t.Fatalf("Scratch(prune=false) on a symlinked scratch: %v", err)
	}
	if rep, err := Scratch(ws, time.Now(), ScratchMaxAge, true); err == nil || len(rep.Stale) != 0 {
		t.Fatalf("Scratch(prune=true) on a symlinked scratch: %+v %v", rep, err)
	}
	if _, err := os.Stat(precious); err != nil {
		t.Fatalf("the symlink target lost its file: %v", err)
	}
	if _, err := Init(ws); err == nil {
		t.Fatal("init accepted a symlinked scratch")
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
