package workspace

// layout.go: the workspace runtime-artifact convention (polyforge#177).
//
// Anything meant to be SHARED — designs, reviews, decision sheets,
// audits, experience — lives in aihub (a work item's content or a
// memory), never in a file of the workspace. The workspace itself holds
// only what is local:
//
//	<workspace>/
//	  .polyforge.yaml   the human-written model candidates and memory policy
//	  AGENTS.md         the workspace notes (polyforge-managed block + human part);
//	                    a workspace without AGENTS.md keeps them in CLAUDE.md (Notes)
//	  .repo/<repo>/     clones for humans and interactive agents
//	  capsules/<wi>/    work item workspaces (daemon-managed)
//	  .polyforge/       polyforge-generated files only (no documents) —
//	                    and workflows/<lane>/, the workspace's user lanes
//	  scratch/          disposable output in fixed categories (polyforge#121):
//	                    raw/ (raw data, 3 days), tasks/ (background
//	                    tasks, 14 days), sessions/ (sessions, 14 days);
//	                    no loose file at its top level
//
// `pf init` applies it (Init); `pf doctor` lints it (StrayEntries,
// Notes, LayoutSection, Scratch). Nothing here moves or deletes anything except
// Scratch with prune, and that only stale batches inside scratch/'s
// categories.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// The local layout's names.
const (
	ScratchDir   = "scratch"
	CapsulesDir  = "capsules"
	ReposDir     = ".repo"
	GeneratedDir = ".polyforge"
)

// The two names the workspace notes file goes by (Notes picks one).
const (
	AgentsNotes = "AGENTS.md"
	ClaudeNotes = "CLAUDE.md"
)

// The scratch categories: scratch/'s fixed subdirectories, each named
// by who writes into it and how long it keeps a batch (polyforge#121):
//
//	raw/      raw data (batch JSON, logs, downloaded archives, history
//	          exports), whoever writes it — kept 3 days;
//	tasks/    a background task's other disposable output — kept 14 days;
//	sessions/ a session's other intermediate output — kept 14 days.
//
// A category's immediate subdirectory is one batch — the unit doctor
// judges and --prune-scratch removes. Everything else under scratch/ (a
// loose file at the top level, an unknown top-level directory, a loose
// file or symlink inside a category, a category name occupied by anything
// but a plain directory) is only ever reported, never removed.
const (
	ScratchRawDir      = "raw"
	ScratchTasksDir    = "tasks"
	ScratchSessionsDir = "sessions"

	RawMaxAge      = 3 * 24 * time.Hour
	TasksMaxAge    = 14 * 24 * time.Hour
	SessionsMaxAge = 14 * 24 * time.Hour
)

// IsRepoClone reports whether dir is a git clone: its .git is a directory
// (a clone) or a regular file (a linked worktree's pointer file — a
// worktree that sits directly under .repo/ is itself enumerated as the
// clone it is). It is the ONE clone-presence rule every check matches by:
// `pf doctor`'s repo enumeration and the daemon's
// missing-clone warning share it.
func IsRepoClone(dir string) bool {
	fi, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// RepoCloneDir answers the clone location of repo name under the
// workspace at ws: <ws>/.repo/<name> (this package owns the convention).
func RepoCloneDir(ws, name string) string {
	return filepath.Join(ws, ReposDir, name)
}

// rootAllowed are the non-dot root entries of the convention. Every
// dot-entry is tolerated too (harnesses and tools own .pi, .claude,
// .mcp.json, .gitignore, .git, …) except the config backups StrayEntries
// names — those are disposable.
var rootAllowed = map[string]bool{
	CapsulesDir:       true,
	ScratchDir:        true,
	AgentsNotes:       true,
	ClaudeNotes:       true,
	"CLAUDE.local.md": true,
	// A Go workspace file at the root ties the .repo/ clones together.
	"go.work":     true,
	"go.work.sum": true,
}

// generatedAllowed are the entries tolerated under .polyforge/: the
// workspace's user lanes (workflows/, workflow contract "User lanes"),
// and legacy v1 artifacts nothing generates any more, allowed so existing
// workspaces do not warn (those may be removed by hand).
var generatedAllowed = map[string]bool{
	"workflows": true,
	"repo-map":  true,
	"watchers":  true,
}

// Stray is one entry outside the convention and where it should go.
type Stray struct {
	Path string // workspace-relative, e.g. "notes.md" or ".polyforge/design.md"
	Dest string // the suggested destination (one of the Dest* values)
}

// The suggested destinations of a stray entry.
const (
	DestAihub   = "aihub (its work item's content, or a doc.* memory)"
	DestScratch = "scratch/"
	DestRepo    = ".repo/"
	DestDir     = "scratch/ (share any documents in it through aihub)"
)

// docExt are the extensions treated as prose documents.
var docExt = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".pdf": true, ".html": true, ".rst": true, ".adoc": true}

// StrayEntries lists the entries of the workspace root and of
// .polyforge/ that sit outside the convention, sorted by path. It reads
// only; a missing root answers an error, a missing .polyforge/ nothing.
func StrayEntries(root string) ([]Stray, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Stray
	for _, e := range entries {
		name := e.Name()
		if rootAllowed[name] {
			continue
		}
		if strings.HasPrefix(name, ".") {
			if strings.HasPrefix(name, FileName+".") { // .polyforge.yaml.bak-…: a disposable backup
				out = append(out, Stray{Path: name, Dest: DestScratch})
			}
			continue
		}
		out = append(out, Stray{Path: name, Dest: destFor(filepath.Join(root, name), e)})
	}
	gen, err := os.ReadDir(filepath.Join(root, GeneratedDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		return nil, err
	}
	for _, e := range gen {
		if generatedAllowed[e.Name()] {
			continue
		}
		out = append(out, Stray{Path: GeneratedDir + "/" + e.Name(), Dest: destFor(filepath.Join(root, GeneratedDir, e.Name()), e)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func destFor(path string, e fs.DirEntry) string {
	if e.IsDir() {
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			return DestRepo
		}
		return DestDir
	}
	if docExt[strings.ToLower(filepath.Ext(e.Name()))] {
		return DestAihub
	}
	return DestScratch
}

// ScratchReport is the outcome of a scratch/ scan or prune.
type ScratchReport struct {
	Stale   []string // batches past their category's retention, sorted ("raw/<batch>"); with prune: the ones removed
	Stray   []string // entries outside the layout, sorted (top-level non-categories, a category occupied by a file or symlink, a loose file or symlink inside a category) — reported only, never removed
	Skipped []string // "<path>: reason" for entries that could not be judged or removed
}

// scratchRoot answers the resolved path of <root>/scratch, or "" when
// there is none. Anything but a plain directory — a symlink above all,
// whose target may be anything — is refused: it is never scanned or
// pruned.
func scratchRoot(root string) (string, error) {
	p := filepath.Join(root, ScratchDir)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a plain directory (a symlink or a file) — not scanned or pruned", p)
	}
	// the workspace path itself may run through symlinks; removal
	// targets are checked against the resolved scratch dir
	return filepath.EvalSymlinks(p)
}

// scratchCategories is the layout's one category table: each fixed
// subdirectory of scratch/ and how long one of its batches is kept.
var scratchCategories = []struct {
	dir string
	age time.Duration
}{
	{ScratchRawDir, RawMaxAge},
	{ScratchTasksDir, TasksMaxAge},
	{ScratchSessionsDir, SessionsMaxAge},
}

// categoryAge answers the retention of the category named dir, or -1
// when it is not one of scratch/'s categories.
func categoryAge(dir string) time.Duration {
	for _, c := range scratchCategories {
		if c.dir == dir {
			return c.age
		}
	}
	return -1
}

// Scratch answers — and, with prune, removes — the scratch/ batches past
// their category's retention at now (the layout rule the header comment
// carries: a category's immediate subdirectory is one batch, the unit
// doctor judges and --prune-scratch removes; the pre-#121 top-level
// age rule is replaced — every entry outside the categories is a stray,
// reported only, at any age, never removed). A batch is judged by the
// newest change of its whole subtree — the later of mtime and ctime of
// every path under it, symlinks not followed; ctime counts because a
// download or an extracted archive keeps its old mtimes yet arrived just
// now — and strictly before its category's cutoff (a batch exactly at
// the boundary stays). Each batch is judged immediately before its
// removal; a file written into a stale batch between that judgment and
// RemoveAll is removed with it (scratch/ is disposable by convention).
// Every deletion goes through an os.Root anchored at the resolved scratch
// dir, so a category or a batch swapped for a symlink between the
// listing and the removal can never lead a RemoveAll outside scratch/ —
// a name whose resolution escapes the anchor is an error, reported in
// Skipped, never a deletion. The age walk itself stays path-based (a
// swapped category can at worst misjudge, read-only; its removal then
// fails through the anchor). The category roots are never removed. An
// entry that cannot be judged or removed is skipped and reported, never
// fatal. A workspace without scratch/ has none.
func Scratch(root string, now time.Time, prune bool) (ScratchReport, error) {
	var rep ScratchReport
	dir, err := scratchRoot(root)
	if err != nil || dir == "" {
		return rep, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rep, err
	}
	anchor, err := os.OpenRoot(dir)
	if err != nil {
		return rep, err
	}
	defer anchor.Close()
	for _, e := range entries {
		name := e.Name()
		age := categoryAge(name)
		if age < 0 { // a loose file or an unknown directory at the top level
			rep.Stray = append(rep.Stray, name)
			continue
		}
		if !e.IsDir() { // a category name occupied by a file or a symlink
			rep.Stray = append(rep.Stray, name)
			continue
		}
		cat, err := anchor.Open(name)
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		batches, err := cat.ReadDir(-1)
		cat.Close()
		if err != nil {
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		cutoff := now.Add(-age)
		for _, b := range batches {
			bname := b.Name()
			rel := name + "/" + bname
			if !b.IsDir() { // a loose file or a symlink where a batch belongs
				rep.Stray = append(rep.Stray, rel)
				continue
			}
			newest, err := newestChange(filepath.Join(dir, name, bname))
			if err == nil && newest.Before(cutoff) && prune {
				err = anchor.RemoveAll(rel)
			}
			switch {
			case err != nil:
				rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", rel, err))
			case newest.Before(cutoff):
				rep.Stale = append(rep.Stale, rel)
			}
		}
	}
	sort.Strings(rep.Stale)
	sort.Strings(rep.Stray)
	return rep, nil
}

// ChangeTime answers a file's ctime (zero where the platform does not
// report it). A variable only so tests — which cannot backdate a ctime —
// can judge by mtime alone.
var ChangeTime = changeTime

// newestChange walks path (without following symlinks) and answers the
// newest mtime or ctime seen.
func newestChange(path string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		for _, t := range []time.Time{info.ModTime(), ChangeTime(info)} {
			if t.After(newest) {
				newest = t
			}
		}
		return nil
	})
	return newest, err
}

// The notes file markers: the polyforge-managed block (shared with the
// repo listing a workspace may already carry) and, inside it, the
// layout section `pf init` owns.
const (
	managedOpen  = `<!-- polyforge:managed:version="1.0" -->`
	managedOpenP = "<!-- polyforge:managed"
	managedClose = "<!-- /polyforge:managed -->"
	layoutOpen   = "<!-- polyforge:layout -->"
	layoutClose  = "<!-- /polyforge:layout -->"
)

// LayoutBlock is the convention as `pf init` writes it into the notes file
// (markers included). The scratch part is the one source of the category
// names and retentions the contract tests hold the skills and docs to.
func LayoutBlock() string {
	return layoutOpen + `
## Workspace layout (polyforge)

Anything meant to be shared — designs, reviews, decision sheets, audits,
experience — goes to aihub, never into a file here:

- a design → its work item's content while the work item is open
  (` + "`pf file edit <wi> --content @file`" + `), else a memory
  ` + "`pf remember --project <p> --type doc.design --work-item <wi> @file`" + `;
- a review / decision sheet / audit → ` + "`pf remember --project <p> --type doc.review`" + ` /
  ` + "`doc.decision`" + ` / ` + "`doc.audit`" + ` (with ` + "`--work-item`" + ` when it belongs to one);
- experience → ` + "`experience.*`" + ` / ` + "`fact.*`" + ` memories.

Locally only:

- ` + "`.repo/<repo>/`" + ` — repository clones;
- ` + "`capsules/<wi>/`" + ` — work item workspaces (the daemon manages them);
- ` + "`scratch/`" + ` — disposable output; no loose file at its top
  level. Its fixed categories, one directory per run, named by who
  writes and how long it keeps:
  - ` + "`scratch/raw/`" + ` — raw data (batch JSON, logs, downloaded
    archives, history exports), whoever writes it; a run's directory
    is ` + "`<UTC time>-<source id>-<unique suffix>`" + `; kept 3 days;
  - ` + "`scratch/tasks/`" + ` — a background task's other disposable
    output, one directory per task (` + "`<task id>`" + `); kept 14 days;
  - ` + "`scratch/sessions/`" + ` — a session's other intermediate
    output, one directory per run (` + "`<UTC time>-<id>`" + `), its
    sub-agents under ` + "`agents/<id>/`" + ` inside it; kept 14 days.
  ` + "`pf doctor`" + ` reports every entry outside the categories and
  every batch past its retention; ` + "`pf doctor --prune-scratch`" + `
  removes only stale batches inside the categories, nothing else;
- ` + "`.polyforge/`" + ` — polyforge-generated files only, and
  ` + "`.polyforge/workflows/<lane>/`" + ` — this workspace's own workflow lanes (user lanes).

Never write documents into the workspace root or ` + "`.polyforge/`" + `;
` + "`pf doctor`" + ` warns about anything else found there.
` + layoutClose + "\n"
}

// ensureScratch creates scratch/ and its fixed categories under root and
// answers the scratch path; created reports whether scratch/ itself was
// made now. A scratch or a category that exists as anything but a plain
// directory is refused — never followed, never moved away (the one rule
// Init applies and TaskScratch reuses).
func ensureScratch(root string) (dir string, created bool, err error) {
	dir = filepath.Join(root, ScratchDir)
	switch st, e := os.Lstat(dir); {
	case e == nil && st.IsDir():
	case e == nil:
		return "", false, fmt.Errorf("%s exists and is not a plain directory (a symlink or a file)", dir)
	case errors.Is(e, fs.ErrNotExist):
		if e := os.Mkdir(dir, 0o755); e != nil {
			return "", false, e
		}
		created = true
	default:
		return "", false, e
	}
	// the fixed categories come with scratch/ (polyforge#121): created
	// when missing, refused when a category name is occupied by anything
	// but a plain directory
	for _, cat := range []string{ScratchRawDir, ScratchTasksDir, ScratchSessionsDir} {
		p := filepath.Join(dir, cat)
		switch st, e := os.Lstat(p); {
		case e == nil && st.IsDir():
		case e == nil:
			return "", false, fmt.Errorf("%s exists and is not a plain directory (a symlink or a file)", p)
		case errors.Is(e, fs.ErrNotExist):
			if e := os.Mkdir(p, 0o755); e != nil {
				return "", false, e
			}
		default:
			return "", false, e
		}
	}
	return dir, created, nil
}

// InitResult reports what Init did.
type InitResult struct {
	Workspace string `json:"workspace"`
	Scratch   string `json:"scratch"`    // created | exists
	NotesFile string `json:"notes_file"` // created | inserted | updated | unchanged
	NotesPath string `json:"notes_path"` // the notes file chosen (Notes): AGENTS.md | CLAUDE.md
}

// Init gives an existing workspace directory its local layout: scratch/
// and the layout section of the notes file Notes picks (upsertLayout;
// written atomically with its mode kept, through a symlink to its target).
// It never writes .polyforge.yaml: the default memory policy is a cli.toml
// block `pf init` writes itself. A CLAUDE.md that carries a second polyforge-managed block
// beside AGENTS.md is refused before anything is written: pf never edits
// it, the operator removes that block by hand. Idempotent.
func Init(root string) (InitResult, error) {
	res := InitResult{Workspace: root}
	st, err := os.Stat(root)
	if err != nil {
		return res, err
	}
	if !st.IsDir() {
		return res, fmt.Errorf("%s is not a directory", root)
	}
	name, dup, err := Notes(root)
	if err != nil {
		return res, err
	}
	if dup {
		return res, fmt.Errorf("%s carries a polyforge-managed block, but %s is the workspace notes file — remove the block from %s by hand, then re-run",
			filepath.Join(root, ClaudeNotes), AgentsNotes, ClaudeNotes)
	}
	_, created, err := ensureScratch(root)
	if err != nil {
		return res, err
	}
	res.Scratch = "exists"
	if created {
		res.Scratch = "created"
	}

	notes := filepath.Join(root, name)
	if fi, err := os.Lstat(notes); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(notes)
		if err != nil {
			return res, fmt.Errorf("%s is a symlink that does not resolve: %w", notes, err)
		}
		notes = target
	}
	raw, err := os.ReadFile(notes)
	var mode fs.FileMode = 0o644
	switch {
	case errors.Is(err, fs.ErrNotExist):
		raw = nil
	case err != nil:
		return res, err
	default:
		if fi, serr := os.Stat(notes); serr == nil {
			mode = fi.Mode().Perm()
		}
	}
	text, how, err := upsertLayout(string(raw), raw == nil)
	if err != nil {
		return res, fmt.Errorf("%s: %w", notes, err)
	}
	res.NotesFile, res.NotesPath = how, name
	if how != "unchanged" {
		if err := writeLayoutFile(notes, []byte(text), mode); err != nil {
			return res, err
		}
	}
	return res, nil
}

// layoutWrite is the layout files' writer (a test seam).
var layoutWrite = atomicfile.Write

// writeLayoutFile replaces one file `pf init` owns. A failed directory
// fsync after the rename is not an error: the file is in place, and a
// lost rename is repaired by running `pf init` again.
func writeLayoutFile(path string, data []byte, mode fs.FileMode) error {
	if err := layoutWrite(path, data, mode); err != nil && !errors.Is(err, atomicfile.ErrDirSync) {
		return err
	}
	return nil
}

// Notes resolves the workspace notes file: AGENTS.md when it exists,
// else CLAUDE.md when that exists, else AGENTS.md (the one `pf init`
// creates). Both names are permanent; the rule is the same for Init and
// `pf doctor`. dup reports a CLAUDE.md that is a separate file beside
// AGENTS.md and carries a polyforge-managed block of its own — a second
// block that would drift from the canonical one. Two names resolving to
// the same file (a symlink either way) are one file, never a duplicate.
func Notes(root string) (name string, dup bool, err error) {
	agents, claude := filepath.Join(root, AgentsNotes), filepath.Join(root, ClaudeNotes)
	if _, err := os.Lstat(agents); errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Lstat(claude); err == nil {
			return ClaudeNotes, false, nil
		}
		return AgentsNotes, false, nil
	} else if err != nil {
		return "", false, err
	}
	cfi, err := os.Stat(claude)
	if errors.Is(err, fs.ErrNotExist) {
		return AgentsNotes, false, nil
	}
	if err != nil {
		return "", false, err
	}
	afi, err := os.Stat(agents)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", agents, err)
	}
	if os.SameFile(afi, cfi) {
		return AgentsNotes, false, nil
	}
	raw, err := os.ReadFile(claude)
	if err != nil {
		return "", false, err
	}
	i, _, _ := managedBlock(string(raw)) // an unclosed block is still a block
	return AgentsNotes, i >= 0, nil
}

// LayoutSection reports whether the notes file at path exists and, when
// it does, whether its polyforge-managed block carries the layout
// section (the same placement rule as upsertLayout: a layout marker
// outside the managed block does not count). A missing file is
// (false, false, nil): there is nothing `pf init` would be repairing. A
// managed block without its closing marker is an error, as in Init.
func LayoutSection(path string) (exists, has bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	text := string(raw)
	i, at, err := managedBlock(text)
	if i < 0 || err != nil {
		return true, false, err
	}
	return true, strings.Contains(text[i:at], layoutOpen), nil
}

// LayoutSectionText answers the notes file's layout section — the text
// from its opening to its closing marker, both included, line endings
// normalized to \n — and whether the file exists. A file without a managed
// block, or a block without a layout section, answers ("", true, nil);
// a missing file ("", false, nil); an unclosed block the error Init and
// LayoutSection answer. `pf doctor` compares the answer with
// LayoutBlock(): anything else is an outdated section, one `pf init`
// refreshes (polyforge#121, Q9).
func LayoutSectionText(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	i, at, err := managedBlock(text)
	if err != nil {
		return "", true, err
	}
	if i < 0 {
		return "", true, nil
	}
	inner := text[i:at]
	s := strings.Index(inner, layoutOpen)
	if s < 0 {
		return "", true, nil
	}
	k := strings.Index(text[i+s:at], layoutClose)
	if k < 0 {
		return "", true, fmt.Errorf("%s has no closing %s inside the polyforge-managed block — fix the file by hand, then re-run", layoutOpen, layoutClose)
	}
	return text[i+s : i+s+k+len(layoutClose)], true, nil
}

// managedBlock locates the polyforge-managed block: its opening marker's
// index and its closing marker's (-1, -1 when there is none). An unclosed
// block is an error — the same refusal pf init answers.
func managedBlock(text string) (int, int, error) {
	i := strings.Index(text, managedOpenP)
	if i < 0 {
		return -1, -1, nil
	}
	j := strings.Index(text[i:], managedClose)
	if j < 0 {
		return i, -1, fmt.Errorf("the polyforge-managed block has no closing %s — fix the file by hand", managedClose)
	}
	return i, i + j, nil
}

// upsertLayout answers the notes file text with the layout section in
// place and how it got there. The layout markers count only inside the
// polyforge-managed block (one quoted in the human part is ignored);
// more than one layout section there is refused. A CRLF file gets a CRLF
// section.
func upsertLayout(text string, absent bool) (string, string, error) {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	block := strings.ReplaceAll(LayoutBlock(), "\n", nl)
	if absent {
		return managedOpen + nl + block + managedClose + nl, "created", nil
	}
	i, at, err := managedBlock(text)
	if err != nil {
		return "", "", fmt.Errorf("%w, then re-run", err)
	}
	if i < 0 {
		sep := nl
		if text == "" {
			sep = ""
		}
		return managedOpen + nl + block + managedClose + nl + sep + text, "inserted", nil
	}
	inner := text[i:at]
	switch n := strings.Count(inner, layoutOpen); {
	case n > 1:
		return "", "", fmt.Errorf("the polyforge-managed block carries %d %s sections — keep one, then re-run", n, layoutOpen)
	case n == 1:
		s := i + strings.Index(inner, layoutOpen)
		k := strings.Index(text[s:at], layoutClose)
		if k < 0 {
			return "", "", fmt.Errorf("%s has no closing %s inside the polyforge-managed block — fix the file by hand, then re-run", layoutOpen, layoutClose)
		}
		end := s + k + len(layoutClose)
		if strings.HasPrefix(text[end:], nl) {
			end += len(nl)
		}
		if text[s:end] == block {
			return text, "unchanged", nil
		}
		return text[:s] + block + text[end:], "updated", nil
	}
	if strings.Contains(inner, layoutClose) {
		return "", "", fmt.Errorf("a stray %s sits in the polyforge-managed block — fix the file by hand, then re-run", layoutClose)
	}
	before := text[:at]
	if !strings.HasSuffix(before, "\n") {
		before += nl
	}
	if !strings.HasSuffix(before, nl+nl) {
		before += nl
	}
	return before + block + text[at:], "inserted", nil
}
