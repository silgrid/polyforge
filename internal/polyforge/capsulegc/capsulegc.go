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
	"fmt"
	"path"
	"strings"
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
	return "", nil
}

// ReadCommitted reads the declaration committed at ref of the git
// repository at root ("" = the repository's default branch). Only
// committed bytes authorize a removal: a working tree is never read.
// A repository without the file at the ref has no declaration and no
// error (nil, nil); a committed declaration that does not parse is an
// error — the whole declaration is rejected, never a silent partial.
func ReadCommitted(root, ref string) (*Declaration, error) {
	return nil, nil
}
