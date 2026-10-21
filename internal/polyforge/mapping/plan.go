// Package mapping holds the workspace→projects plan of record read from
// cli.toml's `[workspace."<path>"]` sections (polyforge#113) and its
// inverse view. One workspace maps many projects; the inverse — every
// project resolves to exactly one workspace — is validated at build
// time, so the daemon's per-claim capsule routing is a total function.
//
// The package is a stdlib-only leaf: the CLI loader (cli.LoadConfig, the
// single choke point both the pf CLI and the daemon read through)
// constructs a Plan; the daemon, the runtime and `pf doctor` consume it.
package mapping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// osUserHomeDir is indirection for tests (the expansion needs the home
// directory; the tests pin it through the real environment).
var osUserHomeDir = os.UserHomeDir

// Entry is one `[workspace."<key>"]` section as written in the config:
// the key exactly as spelled (validation happens in Build) and the
// section's projects list.
type Entry struct {
	Workspace string
	Projects  []string
}

// Plan is the validated mapping: the ordered workspace→projects view
// (config declaration order is preserved — queue merged mode and drain
// iterate projects deterministically in that order) plus the inverse
// project→workspace view the capsule routing reads.
type Plan struct {
	entries []Entry
	inverse map[string]string
}

// UnmappedError is the typed inverse-mapping miss: the project has no
// workspace on this machine. The daemon claim path re-surfaces it as a
// typed DAEMON-4xx; placement verbs surface it verbatim. The message
// carries the migration pointers the contract demands (the [workspace]
// shape and `pf doctor`).
type UnmappedError struct {
	Project string
}

func (e *UnmappedError) Error() string {
	return fmt.Sprintf("project %q is not mapped on this machine — placement needs a home: add a [workspace.\"<path>\"] section listing %q in cli.toml (`pf doctor` checks the mapping)", e.Project, e.Project)
}

// KeyError is a malformed workspace key (relative after ~ expansion, or
// a trailing path separator). Loud, never normalized away.
type KeyError struct {
	Key    string
	Reason string
}

func (e *KeyError) Error() string {
	return fmt.Sprintf("workspace key %q: %s", e.Key, e.Reason)
}

// DuplicateError is a project listed twice within one workspace's
// projects array (the inverse would be fine, the list is not honest).
type DuplicateError struct {
	Project   string
	Workspace string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("project %q is listed twice in workspace %s's projects array — remove the duplicate entry", e.Project, e.Workspace)
}

// AmbiguityError is a project mapped under two workspaces: the
// project→workspace inverse would not be a function.
type AmbiguityError struct {
	Project    string
	Workspaces []string // both (all) claiming workspaces, in order
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("project %q is mapped under two workspaces (%s and %s) — the project→workspace inverse must be a function; keep it in exactly one [workspace] section",
		e.Project, e.Workspaces[0], e.Workspaces[1])
}

// Build validates the ordered entries and returns the Plan. Every
// rejection names the offending key/project/workspace(s) — the loud
// load-error posture the config contract pins:
//
//   - `~` at the start of a key expands to the user's home directory;
//     the result MUST be absolute (relative keys are load errors);
//   - trailing path separators are rejected, not silently normalized;
//   - symlinks are not resolved — the literal expanded key is what the
//     daemon uses for placement;
//   - a project duplicated within one workspace's list is a load error;
//   - a project under two workspaces is a load error naming both.
func Build(entries []Entry) (*Plan, error) {
	p := &Plan{entries: make([]Entry, 0, len(entries)), inverse: map[string]string{}}
	home, _ := os.UserHomeDir()
	claim := map[string]string{} // project → first claiming workspace
	for _, e := range entries {
		key, err := expandKey(e.Workspace, home)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, proj := range e.Projects {
			if strings.TrimSpace(proj) == "" {
				return nil, fmt.Errorf("workspace %s: projects entries must be non-empty names", key)
			}
			if seen[proj] {
				return nil, &DuplicateError{Project: proj, Workspace: key}
			}
			seen[proj] = true
			if first, clash := claim[proj]; clash {
				return nil, &AmbiguityError{Project: proj, Workspaces: []string{first, key}}
			}
			claim[proj] = key
			p.inverse[proj] = key
		}
		p.entries = append(p.entries, Entry{Workspace: key, Projects: append([]string(nil), e.Projects...)})
	}
	return p, nil
}

// expandKey applies the pinned key normalization: `~` at the start
// expands to the home directory, the result must be absolute, and a
// trailing path separator is a load error (never silently trimmed).
func expandKey(key, home string) (string, error) {
	if key == "" {
		return "", &KeyError{Key: key, Reason: "empty workspace key"}
	}
	expanded := key
	switch {
	case key == "~":
		if home == "" {
			return "", &KeyError{Key: key, Reason: "cannot expand ~: home directory unknown"}
		}
		expanded = home
	case strings.HasPrefix(key, "~/"):
		if home == "" {
			return "", &KeyError{Key: key, Reason: "cannot expand ~: home directory unknown"}
		}
		expanded = filepath.Join(home, key[2:])
	}
	if strings.HasSuffix(expanded, "/") || strings.HasSuffix(expanded, string(filepath.Separator)) {
		return "", &KeyError{Key: key, Reason: "trailing path separator — write the workspace path without it"}
	}
	if !filepath.IsAbs(expanded) {
		return "", &KeyError{Key: key, Reason: "workspace keys must be absolute after ~ expansion (a relative path cannot anchor capsule placement)"}
	}
	return expanded, nil
}

// Empty reports whether the plan carries no workspaces (a config with
// zero [workspace] sections — the fresh-install template shape).
func (p *Plan) Empty() bool { return p == nil || len(p.entries) == 0 }

// Workspaces answers the mapped workspace paths in config declaration
// order (deterministic).
func (p *Plan) Workspaces() []string {
	out := make([]string, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, e.Workspace)
	}
	return out
}

// Projects answers every mapped project in config declaration order
// (workspaces in file order, projects in their array order) — the
// deterministic iteration order the merged queue/drain/recommendation
// paths use.
func (p *Plan) Projects() []string {
	out := make([]string, 0, len(p.inverse))
	for _, e := range p.entries {
		out = append(out, e.Projects...)
	}
	return out
}

// ProjectsOf answers the projects one workspace maps, in array order
// (non-nil, possibly empty, for a mapped workspace; nil when the
// workspace is not mapped).
func (p *Plan) ProjectsOf(workspace string) []string {
	if p == nil {
		return nil
	}
	for _, e := range p.entries {
		if e.Workspace == workspace {
			return append([]string{}, e.Projects...)
		}
	}
	return nil
}

// WorkspaceFor resolves the inverse: the one workspace whose projects
// list carries project. An unmapped project answers the typed
// *UnmappedError (the daemon claim path re-surfaces it as DAEMON-4xx).
func (p *Plan) WorkspaceFor(project string) (string, error) {
	if p != nil {
		if ws, ok := p.inverse[project]; ok {
			return ws, nil
		}
	}
	return "", &UnmappedError{Project: project}
}
