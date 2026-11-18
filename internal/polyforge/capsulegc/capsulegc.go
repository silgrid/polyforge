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

// ParseDeclaration parses one repository's capsule-gc declaration: a
// `rebuild_dir=<repo-root-relative directory>` line per declared
// directory, `#` comments and blank lines ignored. One unknown key or
// one unsafe entry rejects the whole declaration — a declaration never
// widens what it may remove — and an empty declaration (or none at
// all) is no error and enables nothing (polyforge#127, AC3).
func ParseDeclaration(raw []byte) ([]string, error) {
	return nil, nil
}
