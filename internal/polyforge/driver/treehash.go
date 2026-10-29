// treehash.go: the capsule tree hash and the task-repo
// tree hash (the gate re-judgment guard).
//
// A step re-dispatched as a NEW conversation after a step_failed takes
// the fast path (continue in place, not from scratch) only when the
// capsule tree equals the tree the failed attempt died on. This file
// defines that equality: a SHA-256 over a deterministic, length-prefixed
// byte stream of the capsule's task worktrees and artifacts/.
//
// The reposTreeHash subset covers, for every repos/<name>:
//
//   - the HEAD commit,
//   - the tracked working-tree content (uncommitted and staged edits
//     included: the bytes on disk are read, never a diff), and
//   - non-ignored untracked files;
//
// and NOTHING else. It is the tree a gate judges (the code the build step
// produced), so driver-owned artifacts/steps result archives cannot move
// it between rounds ( criterion 6). capsuleTreeHash adds
// artifacts/ at the capsule root. runtime-state/, session/ and every
// .gitignore'd path (notably node_modules/) are never enumerated, so
// driver-owned churn cannot move either hash. A symlink is identified by
// its target string (Git's own identity for a link) and any other
// non-regular path is skipped, so a link to /dev/zero or a FIFO cannot
// hang hashing.
//
// Every git call goes through repoGit (testfiles.go): its own git-dir,
// system config ignored, fsmonitor and hooks disabled, inherited GIT_*
// stripped, no optional locks, a hard timeout — so an untrusted task repo
// cannot make hashing run a hook, filter or lock. Any git error makes the
// hash functions return an error; every caller treats an error as "no
// hash" (the mismatch path, i.e. today's behavior). There is no fallback
// walk and no second hashing path.
package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// treeHashFn is the package-level test seam: production always calls
// capsuleTreeHash, a test may substitute a probe to observe the call and
// its result. It is a test seam, not a configuration switch — no config
// key selects it.
var treeHashFn = capsuleTreeHash

// reposTreeHashFn is the same seam for the task-repo-only tree hash a
// gate records as its last-judged tree ( criterion 6).
var reposTreeHashFn = reposTreeHash

// capsuleTreeHash answers the capsule's tree hash: a hex-encoded SHA-256
// over every task worktree's HEAD, tracked working-tree bytes (committed
// or not) and non-ignored untracked files, plus the capsule's artifacts/.
// An error means "no hash" (the caller falls back to today's behavior).
func capsuleTreeHash(capsuleRoot string) (string, error) {
	h := sha256.New()
	if err := hashRepos(h, capsuleRoot); err != nil {
		return "", err
	}
	// artifacts/: the capsule root's tracked + non-ignored untracked
	// files under artifacts/ (a path under artifacts/release-*/ is
	// capsule-gitignored and therefore absent — deliberately).
	arts, err := repoGit(capsuleRoot, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", capsule.DirArtifacts)
	if err != nil {
		return "", err
	}
	writeField(h, "artifacts")
	for _, p := range nulSplit(arts) {
		writeBlob(h, capsuleRoot, p)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// reposTreeHash answers the task-repo tree hash: the capsule hash without
// artifacts/. A capsule with no task repos has no code tree to judge, so
// it answers "no hash" rather than a misleading empty-tree digest.
func reposTreeHash(capsuleRoot string) (string, error) {
	if len(taskRepoNames(filepath.Join(capsuleRoot, capsule.DirRepos))) == 0 {
		return "", os.ErrNotExist
	}
	h := sha256.New()
	if err := hashRepos(h, capsuleRoot); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashRepos appends every task worktree's HEAD + tracked + non-ignored
// untracked content to h (the repos half shared by both hashes).
func hashRepos(h hash.Hash, capsuleRoot string) error {
	reposDir := filepath.Join(capsuleRoot, capsule.DirRepos)
	for _, name := range taskRepoNames(reposDir) {
		dir := filepath.Join(reposDir, name)
		head, err := repoGit(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
		if err != nil {
			return err
		}
		tree, err := repoGit(dir, "ls-tree", "-r", "-z", "--name-only", "HEAD")
		if err != nil {
			return err
		}
		cached, err := repoGit(dir, "ls-files", "-z", "--cached")
		if err != nil {
			return err
		}
		others, err := repoGit(dir, "ls-files", "-z", "--others", "--exclude-standard")
		if err != nil {
			return err
		}
		writeField(h, "repo")
		writeField(h, name)
		writeField(h, "head")
		writeField(h, strings.TrimSpace(head))
		for _, p := range nulSplit(strings.Join([]string{tree, cached, others}, "")) {
			writeBlob(h, dir, p)
		}
	}
	return nil
}

// writeField appends one length-independent field: the string and a NUL
// terminator.
func writeField(h hash.Hash, s string) {
	h.Write([]byte(s))
	h.Write([]byte{0})
}

// writeBlob appends one path's identity: its path field, then its blob
// length and bytes, or the "gone" marker when the working-tree path is
// not a regular file (a tracked deletion, a directory, an unreadable
// file). Reading the bytes on disk is what makes uncommitted edits and
// staged additions part of the hash.
//
// A symlink is identified by its target string (Git's own identity for a
// link), never followed: a link to /dev/zero, a FIFO or a huge file
// outside the capsule must not hang or exhaust the driver. Every other
// non-regular path is skipped as "gone". Regular file content streams
// through io.Copy, so a large file is never held in memory whole.
func writeBlob(h hash.Hash, root, rel string) {
	writeField(h, "path")
	writeField(h, rel)
	full := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Lstat(full)
	if err != nil {
		writeField(h, "gone")
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			writeField(h, "gone")
			return
		}
		writeField(h, "link")
		writeField(h, strconv.Itoa(len(target)))
		h.Write([]byte(target))
		h.Write([]byte{0})
		return
	}
	if !fi.Mode().IsRegular() {
		writeField(h, "gone")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeField(h, "gone")
		return
	}
	defer f.Close()
	writeField(h, "blob")
	writeField(h, strconv.FormatInt(fi.Size(), 10))
	_, _ = io.Copy(h, f)
	h.Write([]byte{0})
}

// nulSplit splits a NUL-terminated git listing into its sorted,
// deduplicated paths (the trailing empty element and any blank entry are
// dropped).
func nulSplit(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
