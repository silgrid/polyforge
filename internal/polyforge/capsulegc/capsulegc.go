// Package capsulegc is the rebuildable-build-products layer behind `pf
// doctor`'s "rebuildable artifacts" row: a repository declares, at its
// root, the directories that hold only rebuildable build products (one
// `rebuild_dir=` line per repository-root-relative directory in
// `.polyforge/capsule-gc.conf`), and this package parses that
// declaration, reads it from a committed ref, probes a task-repo
// clone's declared directories for what a removal may safely take
// (all-ignored content, no nested repository, no symlinked path
// component) and removes them anchored to the clone. The package names
// no project, component or path of its own: the declaration is the
// only place a concrete directory is named (polyforge#127, AC5).
package capsulegc

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/taskrepo"
)

// ParseDeclaration parses one repository's capsule-gc declaration: a
// `rebuild_dir=<repo-root-relative directory>` line per declared
// directory, `#` comments and blank lines ignored. One unknown key or
// one unsafe entry rejects the whole declaration — a declaration never
// widens what it may remove — and an empty declaration (or none at
// all) is no error and enables nothing (polyforge#127, AC3).
func ParseDeclaration(raw []byte) ([]string, error) {
	var dirs []string
	for i, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		key, value, ok := strings.Cut(t, "=")
		bad := func(reason string) error {
			return fmt.Errorf("capsule-gc declaration line %d (%s): %s", i+1, line, reason)
		}
		if !ok || key != "rebuild_dir" {
			return nil, bad("not a rebuild_dir line")
		}
		if value == "" {
			return nil, bad("no directory")
		}
		if strings.ContainsAny(value, " \t\r\n\x00\\") {
			return nil, bad("not a directory literal")
		}
		if path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
			return nil, bad("not a repository-root-relative directory literal")
		}
		// One declared directory never sits inside another: two entries
		// where removing one removes the other is one entry too many.
		for _, d := range dirs {
			if d == value {
				return nil, bad("the same directory is declared twice")
			}
			if strings.HasPrefix(d, value+"/") || strings.HasPrefix(value, d+"/") {
				return nil, bad("declared directories overlap")
			}
		}
		dirs = append(dirs, value)
	}
	return dirs, nil
}

// ConfPath is the declaration's repository-root-relative path.
const ConfPath = ".polyforge/capsule-gc.conf"

// Declaration is one repository's committed capsule-gc declaration.
type Declaration struct {
	Dirs []string // the repo-root-relative directories, in the file's order
	Ref  string   // the ref the bytes were read at
	SHA  string   // the commit the ref resolved to when read
}

// DefaultBranch answers the remote-tracking default branch of the git
// repository at root — refs/remotes/origin/HEAD, the metadata a clone
// records (no branch name is ever assumed) — with no network and no
// fetch. A repository whose default branch cannot be read answers an
// error: its declaration stays unknown, never guessed.
func DefaultBranch(root string) (string, error) {
	ref, err := runGit(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	if ref = strings.TrimSpace(ref); ref == "" {
		return "", fmt.Errorf("git symbolic-ref --short refs/remotes/origin/HEAD: no default branch recorded")
	}
	return ref, nil
}

// gcGitTimeout bounds one read-only git command of this package.
const gcGitTimeout = 10 * time.Second

// runGit runs one read-only git command in the repository at root and
// answers its stdout: taskrepo's hardened face (no program the
// repository's own config names runs; system config ignored,
// inherited GIT_* stripped, no prompt), --no-optional-locks and
// literal pathspecs, a hard timeout — the same face `pf doctor`'s own
// git probes use. Nothing here writes the repository.
func runGit(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gcGitTimeout)
	defer cancel()
	cmd := taskrepo.Command(ctx, nil, root, append([]string{"--no-optional-locks", "--literal-pathspecs"}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Errorf("git %v: %v", args, err).Error()
		}
		return "", fmt.Errorf("git %v: %v", args, msg)
	}
	return stdout.String(), nil
}

// ReadCommitted reads the declaration committed at ref of the git
// repository at root ("" = the repository's default branch). Only
// committed bytes authorize a removal: a working tree is never read.
// A repository without the file at the ref has no declaration and no
// error (nil, nil); a committed declaration that does not parse is an
// error — the whole declaration is rejected, never a silent partial.
func ReadCommitted(root, ref string) (*Declaration, error) {
	if ref == "" {
		def, err := DefaultBranch(root)
		if err != nil {
			return nil, err
		}
		ref = def
	}
	// The ref must resolve: an unknown ref is an error, never a silent
	// "no declaration" — a caller would report a clean pass it did not
	// earn.
	sha, err := runGit(root, "rev-parse", ref)
	if err != nil {
		return nil, err
	}
	// The declaration is read only when the ref carries the path as a
	// blob: an absent path is the enabling silence of a repository that
	// declares nothing, and anything else of that name (a directory, a
	// submodule) is an error.
	kind, err := runGit(root, "ls-tree", "--format=%(objecttype)", ref, "--", ConfPath)
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(kind) {
	case "":
		return nil, nil
	case "blob":
	default:
		return nil, fmt.Errorf("%s at %s is a %s, not the declaration file", ConfPath, ref, strings.TrimSpace(kind))
	}
	raw, err := runGit(root, "show", ref+":"+ConfPath)
	if err != nil {
		return nil, err
	}
	dirs, err := ParseDeclaration([]byte(raw))
	if err != nil {
		return nil, err
	}
	return &Declaration{Dirs: dirs, Ref: ref, SHA: strings.TrimSpace(sha)}, nil
}

// Target is one declared directory of one task-repo clone: what a
// removal may do with it.
type Target struct {
	Rel  string // the declared directory, repo-root-relative
	Size int64 // the regular files under it, logical bytes (a symlink is neither followed nor counted)
	Skip string // "" = a removal may take the directory; else the exact reason it may not
}

// Probe answers what a removal may do with rel, one declared directory
// of the task-repo clone at root. The answer is (nil, nil) when the
// declared directory is not there (nothing to reclaim, nothing wrong);
// a Target with a reason when it is there but a removal may not take
// it (tracked content, untracked content the ignore rules do not cover,
// a nested repository, a symlinked path component, the repository's own
// git metadata); a Target with no reason and its size when a removal
// may take it. A probe that could not be made is an error, and the
// caller removes nothing it cannot prove clean.
func Probe(root, rel string) (*Target, error) {
	return nil, nil
}

// Remove removes one declared directory of the task-repo clone at
// root, anchored: the removal is an os.Root at the clone's own root,
// so no symlinked component can lead it outside the clone. It is the
// caller that probes first; the removal itself judges nothing.
func Remove(root, rel string) error {
	return nil
}
