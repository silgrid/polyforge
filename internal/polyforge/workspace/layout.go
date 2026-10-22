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
//	  .polyforge/       polyforge-generated files only (no documents)
//	  scratch/          disposable output; entries older than 14 days are stale
//
// `pf init` applies it (Init); `pf doctor` lints it (StrayEntries,
// Notes, LayoutSection, Scratch). Nothing here moves or deletes anything except
// Scratch with prune, and that only inside scratch/.

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

// ScratchMaxAge is the age past which a scratch entry is stale.
const ScratchMaxAge = 14 * 24 * time.Hour

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

// generatedAllowed are the entries tolerated under .polyforge/: legacy v1
// artifacts nothing generates any more, allowed so existing workspaces do
// not warn; they may be removed by hand.
var generatedAllowed = map[string]bool{
	"repo-map": true,
	"watchers": true,
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
	Stale   []string // stale entry names, sorted (with prune: the ones removed)
	Skipped []string // "name: reason" for entries that could not be judged or removed
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

// Scratch answers — and, with prune, removes — the scratch/ entries
// whose newest change is older than maxAge at now. The newest change is
// the later of mtime and ctime, of the entry and, for a directory, of
// every path under it (symlinks are not followed); ctime counts because
// a download or an extracted archive keeps its old mtimes yet arrived
// just now. Each entry is judged immediately before its removal; a file
// written into a stale directory between that judgment and RemoveAll is
// removed with it (scratch/ is disposable by convention). Every target
// sits directly under the resolved scratch dir: nothing outside it is
// ever touched. An entry that cannot be judged or removed is skipped and
// reported, never fatal. A workspace without scratch/ has none.
func Scratch(root string, now time.Time, maxAge time.Duration, prune bool) (ScratchReport, error) {
	var rep ScratchReport
	dir, err := scratchRoot(root)
	if err != nil || dir == "" {
		return rep, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rep, err
	}
	cutoff := now.Add(-maxAge)
	for _, e := range entries {
		name := e.Name()
		newest, err := newestChange(filepath.Join(dir, name))
		if err == nil && newest.Before(cutoff) && prune {
			err = os.RemoveAll(filepath.Join(dir, name))
		}
		switch {
		case err != nil:
			rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s: %v", name, err))
		case newest.Before(cutoff):
			rep.Stale = append(rep.Stale, name)
		}
	}
	sort.Strings(rep.Stale)
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
// (markers included).
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
- ` + "`scratch/`" + ` — disposable output (logs, briefs, e2e output, downloads);
  ` + "`pf doctor`" + ` reports entries older than 14 days, ` + "`pf doctor --prune-scratch`" + ` removes them;
- ` + "`.polyforge/`" + ` — polyforge-generated files only.

Never write documents into the workspace root or ` + "`.polyforge/`" + `;
` + "`pf doctor`" + ` warns about anything else found there.
` + layoutClose + "\n"
}

// InitResult reports what Init did.
type InitResult struct {
	Workspace string `json:"workspace"`
	Scratch   string `json:"scratch"`    // created | exists
	NotesFile string `json:"notes_file"` // created | inserted | updated | unchanged
	NotesPath string `json:"notes_path"` // the notes file chosen (Notes): AGENTS.md | CLAUDE.md
	// Yaml is what happened to .polyforge.yaml's memory_policy section:
	// created (a new file holding only it) | appended | unchanged |
	// skipped (the file does not load: YamlWarning says why).
	Yaml        string `json:"polyforge_yaml"`
	YamlWarning string `json:"polyforge_yaml_warning,omitempty"`
}

// Init gives an existing workspace directory its local layout: scratch/,
// the layout section of the notes file Notes picks (upsertLayout; written
// atomically with its mode kept, through a symlink to its target) and the
// default memory_policy of .polyforge.yaml (planMemoryPolicy, planned
// first; a yaml that does not load is left untouched and reported in
// YamlWarning). A CLAUDE.md that carries a second polyforge-managed block
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
	ypath, ydata, ymode, yhow, yerr := planMemoryPolicy(root)
	name, dup, err := Notes(root)
	if err != nil {
		return res, err
	}
	if dup {
		return res, fmt.Errorf("%s carries a polyforge-managed block, but %s is the workspace notes file — remove the block from %s by hand, then re-run",
			filepath.Join(root, ClaudeNotes), AgentsNotes, ClaudeNotes)
	}
	scratch := filepath.Join(root, ScratchDir)
	switch st, err := os.Lstat(scratch); {
	case err == nil && st.IsDir():
		res.Scratch = "exists"
	case err == nil:
		return res, fmt.Errorf("%s exists and is not a plain directory (a symlink or a file)", scratch)
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(scratch, 0o755); err != nil {
			return res, err
		}
		res.Scratch = "created"
	default:
		return res, err
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
	if yerr != nil {
		res.Yaml, res.YamlWarning = "skipped", yerr.Error()
		return res, nil
	}
	if res.Yaml = yhow; ydata != nil {
		return res, writeLayoutFile(ypath, ydata, ymode)
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
