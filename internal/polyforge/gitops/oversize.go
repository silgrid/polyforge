package gitops

// oversize.go: the capsule chain's blob-size guard. The
// hub's pre-receive hook refuses every chain push that carries a blob over
// capsule.MaxBlobBytes, so one such file committed into the chain blocks
// every later push of the work item (a 30 MB trace a step
// downloaded under artifacts/ held the chain locally for nine pushes).
//
//   - Before every chain commit (CommitPush) a file over the limit is left
//     out: an untracked one through a daemon-owned block of the capsule
//     git-dir's info/exclude (never the tracked .gitignore), a tracked one
//     by dropping it from the index (the commit records its removal). Each
//     is listed with path, size and sha256 in the tracked manifest
//     capsule.OversizeManifest and announced once in an
//     EventChainOversizeLeftOut event. The block is rebuilt before every
//     commit, so a file back at or under the limit returns to the chain.
//     The working-tree pass runs before `git add -A` (a multi-GB file is
//     never hashed); the staged tree is checked again after it (a file
//     that grew in between), and a commit whose tree would still hold a
//     blob over the limit is refused rather than made. Tracked files under
//     capsule.DirDownloads leave the index the same way (the capsule
//     .gitignore keeps new ones out).
//   - A push the hook still refuses for size (a blob committed before the
//     guard existed, or by a step's own `git commit` in the capsule root)
//     answers an *OversizeRefusal naming each refused blob's path, the
//     commit that records it and the unpushed range.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// EventChainOversizeLeftOut is the event kind raised when a chain commit
// leaves files out for their size: body `files` (the OversizeFile entries
// the manifest did not list yet with the same size and sha256), `limit`
// and `manifest`. A file version is announced once.
const EventChainOversizeLeftOut = "chain_oversize_left_out"

// OversizeFile is one file left out of the capsule chain for its size.
type OversizeFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// SHA256 is the file's digest when the guard read it; "" when the
	// file could not be read.
	SHA256 string `json:"sha256,omitempty"`
	// Modified is the file's mtime (RFC 3339, UTC): with Size, the key
	// under which a later commit reuses SHA256 instead of re-reading a
	// large file.
	Modified string `json:"modified"`
}

// oversizeManifest is capsule.OversizeManifest: the files the chain
// currently leaves out for their size.
type oversizeManifest struct {
	Limit int64          `json:"limit"`
	Files []OversizeFile `json:"files"`
}

// The daemon-owned block of info/exclude: one anchored literal pattern
// per left-out path between the two marker lines.
const (
	oversizeBlockBegin = "# pf-oversize:begin (daemon-owned: files over the capsule chain's blob limit, rebuilt before every commit)"
	oversizeBlockEnd   = "# pf-oversize:end"
)

// infoExcludePath answers the capsule git-dir's info/exclude.
func infoExcludePath(o Opts) string {
	gitDir := o.GitDir
	if gitDir == "" {
		gitDir = filepath.Join(o.CapsuleDir, ".git")
	}
	return filepath.Join(gitDir, "info", "exclude")
}

// readOversizeBlock answers info/exclude's lines outside the oversize
// block and the paths the block holds.
func readOversizeBlock(path string) (outside, held []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	in := false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# pf-oversize:begin"):
			in = true
		case line == oversizeBlockEnd:
			in = false
		case in:
			if p := unescapeIgnore(strings.TrimPrefix(line, "/")); p != "" {
				held = append(held, p)
			}
		case line != "" || len(outside) > 0:
			outside = append(outside, line)
		}
	}
	return outside, held, nil
}

// unescapeIgnore inverts escapeIgnore.
func unescapeIgnore(p string) string {
	var b strings.Builder
	esc := false
	for _, r := range p {
		if r == '\\' && !esc {
			esc = true
			continue
		}
		esc = false
		b.WriteRune(r)
	}
	return b.String()
}

// writeOversizeBlock rewrites info/exclude with the block holding paths
// (no block when there are none); nothing is written when it already
// reads so. A path holding a line break cannot be written as a pattern:
// it stays out of the block, and the staged-tree check keeps it out of
// the commit instead.
func writeOversizeBlock(path string, outside, paths []string) error {
	lines := append([]string{}, outside...)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var block []string
	for _, p := range paths {
		if !strings.ContainsAny(p, "\n\r") {
			block = append(block, "/"+escapeIgnore(p))
		}
	}
	if len(block) > 0 {
		lines = append(append(append(lines, oversizeBlockBegin), block...), oversizeBlockEnd)
	}
	want := ""
	if len(lines) > 0 {
		want = strings.Join(lines, "\n") + "\n"
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(raw) == want {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, []byte(want), 0o644)
}

// readOversizeManifest answers the manifest's entries by path (none when
// it is absent or unreadable — the guard then re-reads every digest) and
// whether the file exists.
func readOversizeManifest(capsuleDir string) (map[string]OversizeFile, bool) {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.OversizeManifest)))
	if err != nil {
		return nil, !os.IsNotExist(err)
	}
	var m oversizeManifest
	if json.Unmarshal(raw, &m) != nil {
		return nil, true
	}
	out := make(map[string]OversizeFile, len(m.Files))
	for _, f := range m.Files {
		out[f.Path] = f
	}
	return out, true
}

// fileSHA256 answers the hex sha256 of a regular file ("" when it cannot
// be read).
func fileSHA256(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// underDownloads reports whether a capsule-relative path lies in the
// raw downloads directory.
func underDownloads(p string) bool {
	return strings.HasPrefix(p, capsule.DirDownloads+"/")
}

// leaveOutOversize is the guard's pass over the capsule, run under the
// per-WI lock before a commit: the index's paths, the untracked
// not-ignored ones and those the oversize block holds are measured
// (lstat; only regular files count), and every one over
// capsule.MaxBlobBytes — plus each of forced, whatever its size reads now
// (a staged blob found over the limit) — is left out: the block is
// rewritten to exactly that set, the ones in the index leave it with
// every tracked path under capsule.DirDownloads, and the manifest is
// rewritten when it changed. It answers the entries the previous
// manifest did not list with the same size and sha256.
//
// Every path the guard hands git (here and in guardStagedTree) is an
// agent-chosen file name, so it goes as a literal path
// (--literal-pathspecs), never as a pathspec: a `:`-prefixed name is
// pathspec magic and a `[x]` name a glob, and either would act on a
// sibling instead of (or beside) the file named.
func leaveOutOversize(o Opts, env []string, forced []string) ([]OversizeFile, error) {
	dir := o.CapsuleDir
	exclude := infoExcludePath(o)
	outside, held, err := readOversizeBlock(exclude)
	if err != nil {
		return nil, err
	}
	tracked, err := git(env, dir, "ls-files", "-z", "--cached")
	if err != nil {
		return nil, err
	}
	others, err := git(env, dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	inIndex := map[string]bool{}
	candidates := map[string]bool{}
	var untrack []string
	for _, p := range strings.Split(tracked, "\x00") {
		switch {
		case p == "" || inIndex[p]:
		case underDownloads(p):
			inIndex[p] = true
			untrack = append(untrack, p)
		default:
			inIndex[p] = true
			candidates[p] = true
		}
	}
	for _, p := range strings.Split(others, "\x00") {
		candidates[p] = true
	}
	for _, p := range held {
		candidates[p] = true
	}
	force := map[string]bool{}
	for _, p := range forced {
		candidates[p], force[p] = true, true
	}
	delete(candidates, "")
	paths := make([]string, 0, len(candidates))
	for p := range candidates {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	prior, manifestExists := readOversizeManifest(dir)
	files := []OversizeFile{}
	var leftPaths []string
	for _, p := range paths {
		abs := filepath.Join(dir, filepath.FromSlash(p))
		st, err := os.Lstat(abs)
		if err != nil || !st.Mode().IsRegular() || (st.Size() <= capsule.MaxBlobBytes && !force[p]) {
			continue
		}
		f := OversizeFile{Path: p, Size: st.Size(), Modified: st.ModTime().UTC().Format(time.RFC3339Nano)}
		if was, ok := prior[p]; ok && was.Size == f.Size && was.Modified == f.Modified && was.SHA256 != "" {
			f.SHA256 = was.SHA256
		} else {
			f.SHA256 = fileSHA256(abs)
		}
		files = append(files, f)
		leftPaths = append(leftPaths, p)
		if inIndex[p] {
			untrack = append(untrack, p)
		}
	}
	// the block before the index: a path that leaves the index is
	// already excluded when `add -A` runs
	if err := writeOversizeBlock(exclude, outside, leftPaths); err != nil {
		return nil, err
	}
	for i := 0; i < len(untrack); i += 200 {
		if _, err := git(env, dir, append([]string{"--literal-pathspecs", "rm", "--cached", "-q", "--ignore-unmatch", "--"}, untrack[i:min(i+200, len(untrack))]...)...); err != nil {
			return nil, err
		}
	}
	if len(files) > 0 || manifestExists {
		blob, _ := json.MarshalIndent(oversizeManifest{Limit: capsule.MaxBlobBytes, Files: files}, "", "  ")
		blob = append(blob, '\n')
		mpath := filepath.Join(dir, filepath.FromSlash(capsule.OversizeManifest))
		if raw, err := os.ReadFile(mpath); err != nil || !bytes.Equal(raw, blob) {
			if err := os.MkdirAll(filepath.Dir(mpath), 0o755); err != nil {
				return nil, err
			}
			if err := atomicfile.Write(mpath, blob, 0o644); err != nil {
				return nil, err
			}
		}
	}
	var fresh []OversizeFile
	for _, f := range files {
		if was, ok := prior[f.Path]; !ok || was.Size != f.Size || was.SHA256 != f.SHA256 {
			fresh = append(fresh, f)
		}
	}
	return fresh, nil
}

// stagedOversize answers the paths whose blob in the staged tree is over
// capsule.MaxBlobBytes (write-tree writes the tree objects the commit
// would write anyway).
func stagedOversize(env []string, dir string) ([]string, error) {
	tree, err := git(env, dir, "write-tree")
	if err != nil {
		return nil, err
	}
	out, err := git(env, dir, "ls-tree", "-r", "-l", "-z", strings.TrimSpace(tree))
	if err != nil {
		return nil, err
	}
	var over []string
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta) // mode type sha size
		if !ok || len(f) != 4 || f[1] != "blob" {
			continue
		}
		if n, err := strconv.ParseInt(f[3], 10, 64); err == nil && n > capsule.MaxBlobBytes {
			over = append(over, p)
		}
	}
	return over, nil
}

// guardStagedTree is the guard's check of the staged tree, after `git add
// -A`: every path whose staged blob is over the limit is left out
// (leaveOutOversize, forced) and the rewritten manifest is staged; a tree
// that still holds such a blob unstages everything and refuses the
// commit — no commit is better than one no push can carry.
func guardStagedTree(o Opts, env []string) error {
	late, err := stagedOversize(env, o.CapsuleDir)
	if err != nil || len(late) == 0 {
		return err
	}
	fresh, err := leaveOutOversize(o, env, late)
	if err != nil {
		return err
	}
	o.announceOversize(fresh)
	if _, err := git(env, o.CapsuleDir, "--literal-pathspecs", "add", "--", capsule.OversizeManifest); err != nil {
		return err
	}
	if still, err := stagedOversize(env, o.CapsuleDir); err != nil || len(still) > 0 {
		_, _ = git(env, o.CapsuleDir, "reset", "-q")
		if err != nil {
			return err
		}
		return fmt.Errorf("gitops: no commit was created: the staged tree still holds a blob over the %d-byte chain limit at %s", capsule.MaxBlobBytes, strings.Join(still, ", "))
	}
	return nil
}

// announceOversize raises one EventChainOversizeLeftOut for files (none
// when files is empty or no sink is bound).
func (o Opts) announceOversize(files []OversizeFile) {
	if len(files) == 0 || o.EventSink == nil {
		return
	}
	o.EventSink(EventChainOversizeLeftOut, map[string]any{
		"files":    files,
		"limit":    capsule.MaxBlobBytes,
		"manifest": capsule.OversizeManifest,
	})
}

// RefusedBlob is one blob a chain push was refused for, resolved against
// the unpushed range.
type RefusedBlob struct {
	// Path is a path the range records the blob at ("" when no commit of
	// the range names it).
	Path string `json:"path"`
	Blob string `json:"blob"`
	Size int64  `json:"size"`
	// Commit is the oldest commit of the range that records the blob at
	// Path.
	Commit string `json:"commit,omitempty"`
}

// OversizeRefusal is a chain push the hub refused for its blob size
// limit, resolved to what a human can act on: the refused blobs' paths
// and commits, and the unpushed range that holds them. It unwraps to the
// push's own error, so every classifier of push errors reads it
// unchanged.
type OversizeRefusal struct {
	Limit int64
	// Range is the unpushed commit range: `<pushed>..<head>`, or `<head>`
	// when nothing of the chain was pushed yet.
	Range string
	Blobs []RefusedBlob
	// Push is the push's own error (git's output included).
	Push error
}

// Summary is the refusal in one sentence, naming every path and the
// range.
func (e *OversizeRefusal) Summary() string {
	parts := make([]string, 0, len(e.Blobs))
	for _, b := range e.Blobs {
		if b.Path == "" {
			parts = append(parts, fmt.Sprintf("blob %s (%d bytes; no commit of the range records it)", b.Blob, b.Size))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%d bytes, blob %s, committed in %s)", b.Path, b.Size, b.Blob, b.Commit))
	}
	return fmt.Sprintf("the hub refused the chain push: %d file(s) over its %d-byte blob limit in the unpushed commits %s: %s. "+
		"Drop them from those commits (pause the work item and keep a copy of the chain first); new commits leave such files out on their own (%s)",
		len(e.Blobs), e.Limit, e.Range, strings.Join(parts, "; "), capsule.OversizeManifest)
}

func (e *OversizeRefusal) Error() string {
	if e.Push == nil {
		return e.Summary()
	}
	return e.Summary() + " — " + e.Push.Error()
}

func (e *OversizeRefusal) Unwrap() error { return e.Push }

// PushFailureFacts answers the structured facts a push_failed event
// carries beside its error: for a size refusal the unpushed `range` and
// the refused blobs as `oversize`; nil for any other failure.
func PushFailureFacts(err error) map[string]any {
	var r *OversizeRefusal
	if !errors.As(err, &r) {
		return nil
	}
	return map[string]any{"range": r.Range, "oversize": r.Blobs}
}

// The hook's size refusal: "blob exceeds the <limit>-byte limit: " then
// one "<sha> (<size> bytes)" per refused blob (internal/aihub/git/hook.go).
var (
	blobLimitRe   = regexp.MustCompile(`blob exceeds the ([0-9]+)-byte limit: `)
	refusedBlobRe = regexp.MustCompile(`\b([0-9a-f]{40}(?:[0-9a-f]{24})?) \(([0-9]+) bytes\)`)
)

// diagnoseOversize answers perr resolved into an *OversizeRefusal when
// the hub refused the chain push for its blob limit (perr unchanged
// otherwise): each refused blob is looked up in the unpushed range.
func diagnoseOversize(o Opts, perr error) error {
	msg := perr.Error()
	loc := blobLimitRe.FindStringSubmatchIndex(msg)
	if loc == nil {
		return perr
	}
	limit, _ := strconv.ParseInt(msg[loc[2]:loc[3]], 10, 64)
	sizes := map[string]int64{}
	var order []string
	for _, m := range refusedBlobRe.FindAllStringSubmatch(msg[loc[1]:], -1) {
		if _, seen := sizes[m[1]]; !seen {
			order = append(order, m[1])
		}
		sizes[m[1]], _ = strconv.ParseInt(m[2], 10, 64)
	}
	if len(order) == 0 {
		return perr
	}
	env := o.Env()
	head, err := git(env, o.CapsuleDir, "rev-parse", "HEAD")
	if err != nil {
		return perr
	}
	rng := strings.TrimSpace(head)
	if pushed, err := git(env, o.CapsuleDir, "rev-parse", "--verify", "-q", pushedRef); err == nil {
		rng = strings.TrimSpace(pushed) + ".." + rng
	}
	type key struct{ blob, path string }
	commits := map[key]string{}
	if out, err := git(env, o.CapsuleDir, "log", "-m", "--root", "--no-renames", "--raw", "--no-abbrev", "--format=commit %H", rng); err == nil {
		commit := ""
		for _, line := range strings.Split(out, "\n") {
			if c, ok := strings.CutPrefix(line, "commit "); ok {
				commit = c
				continue
			}
			meta, path, ok := strings.Cut(line, "\t")
			f := strings.Fields(meta) // :oldmode newmode oldsha newsha status
			if !ok || len(f) != 5 {
				continue
			}
			if _, want := sizes[f[3]]; want {
				commits[key{f[3], path}] = commit // newest first: the last one seen is the oldest
			}
		}
	}
	ref := &OversizeRefusal{Limit: limit, Range: rng, Push: perr}
	for _, blob := range order {
		var found []RefusedBlob
		for k, c := range commits {
			if k.blob == blob {
				found = append(found, RefusedBlob{Path: k.path, Blob: blob, Size: sizes[blob], Commit: c})
			}
		}
		if len(found) == 0 {
			found = []RefusedBlob{{Blob: blob, Size: sizes[blob]}}
		}
		sort.Slice(found, func(a, b int) bool { return found[a].Path < found[b].Path })
		ref.Blobs = append(ref.Blobs, found...)
	}
	return ref
}
