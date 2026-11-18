package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/capsulegc"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_rebuildables.go: the "rebuildable artifacts" row of `pf
// doctor`, run once per Cell. A repository declares the directories
// under its root that hold only rebuildable build products (one
// rebuild_dir= line each, .polyforge/capsule-gc.conf); this row reports
// — and `--prune-rebuildables` removes — those directories where they
// sit inside the terminal work items' capsules, without waiting for
// the 14-day retention (which stays whole and untouched: the retention
// row still owns the scene slim and the git dirs). The declaration is
// read from the mapped workspace's .repo/<name> clone, its default
// branch's committed bytes (the owner's Q3=B ruling: the retroactive
// view that reaches the backlog predating the declaration; the commit
// read is recorded in the report), and only a probe-passing directory
// is ever removed — all-ignored content, no tracked or
// untracked-not-ignored file, no nested repository, no symlinked
// component, never the repository's own .git; the removal is anchored
// to its clone. artifacts/, capsule.json, the git directories, the
// local commits and the git status never move (a repository with
// unpushed commits loses its ignored build products and nothing else).
// A capsule of a work item still in flight, one a daemon job holds,
// one whose state cannot be read and a repository whose declaration
// cannot be proven are kept and named; a repository that declares
// nothing changes nothing. The default run deletes nothing, and a
// prune removes nothing while the daemon is down. The row reuses the
// retention row's scan and work-item state reads; the declaration, the
// probe and the anchored removal live in internal/polyforge/capsulegc
// (polyforge#127).

// docChRebuildables is the row name (sibling convention: one const
// beside the layer it implements).
const docChRebuildables = "rebuildable artifacts"

// gcCandidate is one removable directory of one prune run: the clone it
// sits in, the declared directory, and the declaration it was read
// from (its ref and commit, the removal's audit trail).
type gcCandidate struct {
	clone string
	rel   string
	decl  *capsulegc.Declaration
}

// rebuildablesCheck is the row: it reports, per mapped workspace of
// this Cell, the bytes of the declared rebuildable build products
// inside the terminal work items' capsules (logical bytes, what a
// prune frees), and with --prune-rebuildables removes exactly the
// probe-passing declared directories, listing each removed path with
// the declaration's ref and commit.
func rebuildablesCheck(r *doctorRun, dataDir string) (string, string) {
	dirs, errs := scanRetainedDirs(dataDir, r.present)
	live := map[string]bool{}
	for _, j := range r.status.Jobs {
		live[j.WorkItemID] = true
	}
	var caps []retainedDir
	var owners []string
	asked := map[string]bool{}
	for _, d := range dirs {
		if !d.Capsule {
			continue // an external git dir holds no build products
		}
		caps = append(caps, d)
		if d.Err != "" || d.WI == "" || live[d.WI] || asked[d.WI] {
			continue
		}
		asked[d.WI] = true
		owners = append(owners, d.WI)
	}
	states := map[string]string{}
	if r.reachOK && len(owners) > 0 {
		states = workItemStates(newProxyClient(r.cfg.DaemonSocket, r.profile), r.cfg.Mapping.Projects(), owners)
	}
	type wsTally struct {
		terminal int
		size     int64
		kept     []string
	}
	tallies := map[string]*wsTally{}
	var wsOrder []string
	tally := func(ws string) *wsTally {
		if tallies[ws] == nil {
			tallies[ws] = &wsTally{}
			wsOrder = append(wsOrder, ws)
		}
		return tallies[ws]
	}
	var candidates []gcCandidate
	for _, d := range caps {
		ws := filepath.Dir(filepath.Dir(d.Path))
		t := tally(ws)
		keep := func(why string) { t.kept = append(t.kept, d.Path+": "+why) }
		switch {
		case d.Err != "" || d.WI == "":
			keep("unreadable")
		case live[d.WI]:
			keep("held by a daemon job")
		default:
			state := states[d.WI]
			switch {
			case state == "":
				keep("the work item's aihub state could not be read")
			case !terminalState(state):
				keep("the work item is " + state)
			default:
				t.terminal++
				reposDir := filepath.Join(d.Path, capsule.DirRepos)
				entries, err := os.ReadDir(reposDir)
				if err != nil {
					if !os.IsNotExist(err) {
						t.kept = append(t.kept, reposDir+": "+err.Error())
					}
					continue
				}
				for _, e := range entries {
					if !e.IsDir() {
						continue
					}
					clone := filepath.Join(reposDir, e.Name())
					if !workspace.IsRepoClone(clone) {
						continue
					}
					// Q3=B: the declaration is read from the mapped
					// workspace's .repo clone, its committed default
					// branch — never a capsule clone's working tree.
					decl, err := capsulegc.ReadCommitted(workspace.RepoCloneDir(ws, e.Name()), "")
					switch {
					case err != nil:
						t.kept = append(t.kept, clone+": the declaration could not be read: "+err.Error())
					case decl == nil:
						// the repository declares nothing: its capsules
						// keep today's behavior (AC3)
					default:
						for _, rel := range decl.Dirs {
							tgt, err := capsulegc.Probe(clone, rel)
							switch {
							case err != nil:
								t.kept = append(t.kept, clone+"/"+rel+": "+err.Error())
							case tgt == nil:
								// not there: nothing to reclaim
							case tgt.Skip != "":
								t.kept = append(t.kept, clone+"/"+rel+": kept: "+tgt.Skip)
							default:
								t.size += tgt.Size
								candidates = append(candidates, gcCandidate{clone: clone, rel: rel, decl: decl})
							}
						}
					}
				}
			}
		}
	}
	var parts []string
	for _, ws := range wsOrder {
		t := tallies[ws]
		p := fmt.Sprintf("%s: %d terminal capsule(s) no daemon job holds, %s in declared rebuildable directories", ws, t.terminal, fmtSize(t.size))
		if len(t.kept) > 0 {
			p += " — kept: " + strings.Join(capRows(t.kept), "; ")
		}
		parts = append(parts, p)
	}
	detail := "the rebuildable build products `pf doctor --prune-rebuildables` may remove — the declared directories inside the terminal work items' capsules, per workspace (logical bytes, what a prune frees): "
	if len(parts) == 0 {
		detail += "no mapped workspace holds a capsule"
	} else {
		detail += strings.Join(parts, "; ")
	}
	detail += "; a repository's declaration is its own, read from the committed default branch through the mapped workspace's .repo clone, and a repository that declares nothing changes nothing"
	status := "pass"
	switch {
	case len(candidates) > 0 && !r.pruneRebuildables:
		status = "warn"
		detail += "; `pf doctor --prune-rebuildables` removes the declared directories behind these numbers (nothing was deleted)"
	case len(candidates) > 0 && !r.sockOK:
		status = "warn"
		detail += "; nothing removed: the daemon does not answer GET /status, so its live jobs are unknown — start it with `pf daemon ensure` and re-run"
	case len(candidates) > 0:
		var removed []string
		for _, c := range candidates {
			if err := capsulegc.Remove(c.clone, c.rel); err != nil {
				errs = append(errs, fmt.Sprintf("%s/%s: %v", c.clone, c.rel, err))
				continue
			}
			removed = append(removed, fmt.Sprintf("%s (declared at %s %s)", filepath.Join(c.clone, c.rel), c.decl.Ref, c.decl.SHA))
		}
		if len(removed) > 0 {
			detail += fmt.Sprintf("; removed %d: %s", len(removed), strings.Join(capRows(removed), "; "))
		}
	case r.pruneRebuildables:
		detail += "; nothing to remove"
	}
	if len(errs) > 0 {
		status = "warn"
		detail += "; not checked: " + strings.Join(capRows(errs), "; ")
	}
	return status, detail
}
