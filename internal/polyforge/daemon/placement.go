package daemon

// placement.go: the submit face's placement + missing-clone signalling.
// A submit answer states where the claim will land —
// the local daemon's machine id and the workspace / capsule landing path
// the work item will occupy, the same resolution the claim itself uses —
// and warns on every repo clone missing from the mapped workspace, still
// accepting: the claim clones fresh, so a missing clone is a warning,
// never a refusal (the cross-machine end-session → other-machine claim
// pattern stays legal, pi#122). Warn-only throughout: a hub read that
// fails skips that part of the check, never blocking the submit.

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// repoDecl is one stored repo_inventory entry: {"name": "..."}.
type repoDecl struct {
	Name string `json:"name"`
}

// wiView is the daemon-local work-item view read: the frozen api.WorkItem
// plus the repo_inventory the pf-side typed read drops (api.WorkItem
// itself gains nothing — the inventory rides alongside it, the same way
// the server's WIView carries it).
type wiView struct {
	api.WorkItem
	RepoInventory []repoDecl `json:"repo_inventory,omitempty"`
}

// submitPlacement is one submit's placement + missing-clone context.
type submitPlacement struct {
	MachineID    string   // the local daemon's machine id (the claim's target machine)
	CapsulePath  string   // <mapped-ws>/capsules/<project>-<seq>, the claim's own resolution
	MissingRepos []string // sorted; every checked repo whose clone is missing
}

// placementFor answers one probed work item's placement: the local
// daemon's machine id and the capsule landing path the claim itself
// resolves (WorkspaceFor(project) + capsules/ + api.PathName — the same
// resolution runtime.capsuleDirForProject uses), plus every repo the
// claim will clone whose clone is missing from the mapped workspace. An
// unmapped project answers nil — no placement exists to state.
func (d *Daemon) placementFor(probe *api.WorkItem) *submitPlacement {
	ws, err := d.cfg.Mapping.WorkspaceFor(probe.ProjectName)
	if err != nil {
		return nil
	}
	p := &submitPlacement{
		MachineID:   d.cfg.MachineID,
		CapsulePath: filepath.Join(ws, workspace.CapsulesDir, api.PathName(probe.Slug, probe.ID)),
	}
	for _, name := range d.claimRepoNames(probe.ID, probe.ProjectName) {
		if !workspace.IsRepoClone(workspace.RepoCloneDir(ws, name)) {
			p.MissingRepos = append(p.MissingRepos, name)
		}
	}
	sort.Strings(p.MissingRepos)
	return p
}

// claimRepoNames answers the repo names the claim will clone: the work
// item's frozen repo_inventory when it declares one — exactly that list,
// an undeclared project repo is not checked — else every repo of its
// project. Either read failing answers nil: the missing-clone check is
// silently skipped (warn-only, never blocking). Both reads ride existing
// endpoints through the existing generic hub Do — no new aihub face.
func (d *Daemon) claimRepoNames(wi, project string) []string {
	var view wiView
	if err := d.hub.Do("GET", "/api/v1/work-items/"+url.PathEscape(wi), nil, &view); err != nil {
		return nil
	}
	var names []string
	for _, r := range view.RepoInventory {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	if len(names) > 0 {
		return names
	}
	var proj api.Project
	if err := d.hub.Do("GET", "/api/v1/projects/"+url.PathEscape(project), nil, &proj); err != nil {
		return nil
	}
	for _, r := range proj.Repos {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	return names
}

// warnMissingClones logs one warning line naming every repo the claim
// will clone whose clone is missing from the mapped workspace — the
// auto-pull face of the missing-clone warning (an auto-pulled claim has
// no submit output to read, so the daemon log is the only face). One
// line, nothing blocked or delayed, no event type or field; a hub read
// that fails skips the line silently (warn-only, never blocking).
func (d *Daemon) warnMissingClones(wi string) {
	probe, err := d.hub.GetWorkItem(wi)
	if err != nil || probe == nil || probe.ID == "" {
		return
	}
	if p := d.placementFor(probe); p != nil && len(p.MissingRepos) > 0 {
		fmt.Fprintf(os.Stderr, "daemon: auto claim %s: no local clone under .repo/ for repo(s) %s — the claim clones fresh\n",
			wi, strings.Join(p.MissingRepos, ", "))
	}
}
