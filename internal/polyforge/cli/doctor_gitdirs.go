package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_gitdirs.go: the "git dir retention" row of `pf doctor`. The
// runtime never deletes a work item's external git dir
// (<data-dir>/git/<wi>.git) nor a quarantined scene
// (<wi>.git.quarantined.<ts>, capsules/<name>.quarantined.<ts>):
// quarantine only renames, so the bytes always survive. This row
// reports them — count and size — and `pf doctor --prune-git-dirs`
// removes exactly the ones it calls reclaimable or retired. Deletion
// stays an explicit operator act; the default run never writes.
//
// An entry is deletable only when ALL hold: its work item's aihub state
// is terminal (wrapped, failed, canceled); its newest change is older
// than gitDirRetention; the daemon answers GET /status and names no job
// for that work item; and, for a set-aside capsule, no task-repo clone
// under its repos/ carries an uncommitted change or a commit that neither
// its own nor the mapped workspace's .repo/<name> clone's remote-tracking
// refs carry (a scene's own refs stop updating at quarantine time; the
// .repo clone keeps fetched). Such a scene is retained, together with the
// work item's git dirs (the scene's pair was moved aside together by
// quarantine), because a prune must never delete work no remote carries. A
// clone that cannot be read keeps the scene too: nothing is deleted on an
// unproven clean. aihub unreachable, an unknown work item or an unreadable
// entry is "unknown": counted, never deleted. The daemon down prunes
// nothing.
//
// The run stays cheap however many entries there are, because it reads
// only what a verdict needs. Local facts come first: an entry whose own
// mtime is inside the retention (its newest change is no older) or that
// a daemon job names is retained with no aihub read and no walk. The
// owners left are read in one list per mapped project, per id only for
// an owner no list names (and per id outright when they number no more
// than the projects). Only an entry of a terminal work item — the one
// kind a prune may remove — is walked, for its newest change and its
// size, so the sizes reported are what a prune frees.

const docChGitDirs = "git dir retention"

// gitDirRetention is the age past which a terminal work item's git dir or
// quarantined scene is reclaimable (the scratch/ default).
const gitDirRetention = 14 * 24 * time.Hour

// gitDirListLimit is the page a project list asks for: aihub's list cap
// (core maxListLimit), the project's newest work items first.
const gitDirListLimit = 1000

// retainedDir is one scanned entry.
type retainedDir struct {
	Path        string
	WI          string // "" when the owning work item could not be read
	Quarantined bool
	Size        int64     // measureDir's sum; 0 for an entry never walked
	Newest      time.Time // the entry's own mtime; measureDir raises it to the newest under it
	Err         string    // non-empty: the entry could not be read
}

// retention classes.
const (
	retReclaimable = "reclaimable" // a terminal work item's live git dir past the retention
	retRetired     = "retired"     // a terminal work item's quarantined scene past the retention
	retRetained    = "retained"    // everything a prune must keep
	retUnknown     = "unknown"     // the owner or the entry could not be read
)

// scanRetainedDirs lists the external git dirs and quarantined git dirs
// under <dataDir>/git and the quarantined capsules of every workspace.
func scanRetainedDirs(dataDir string, wss []string) ([]retainedDir, []string) {
	var out []retainedDir
	var errs []string
	gitRoot := filepath.Join(dataDir, "git")
	entries, err := os.ReadDir(gitRoot)
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Sprintf("%s: %v", gitRoot, err))
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		d := retainedDir{Path: filepath.Join(gitRoot, name)}
		if wi, ts, ok := strings.Cut(name, ".git.quarantined."); ok && wi != "" && ts != "" {
			d.WI, d.Quarantined = wi, true
		} else if wi, ok := strings.CutSuffix(name, ".git"); ok && wi != "" {
			d.WI = wi
		} else {
			continue // askpass/ and anything else the runtime keeps there
		}
		out = append(out, statDir(d))
	}
	for _, ws := range wss {
		capRoot := filepath.Join(ws, "capsules")
		entries, err := os.ReadDir(capRoot)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("%s: %v", capRoot, err))
			}
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !strings.Contains(e.Name(), ".quarantined.") {
				continue
			}
			d := retainedDir{Path: filepath.Join(capRoot, e.Name()), Quarantined: true}
			if raw, err := os.ReadFile(filepath.Join(d.Path, capsule.CapsuleJSON)); err == nil {
				var id capsule.Identity
				if json.Unmarshal(raw, &id) == nil {
					d.WI = id.WorkItemID
				}
			}
			out = append(out, statDir(d))
		}
	}
	return out, errs
}

// statDir fills an entry's own mtime, the floor of its newest change.
func statDir(d retainedDir) retainedDir {
	st, err := os.Lstat(d.Path)
	if err != nil {
		d.Err = err.Error()
		return d
	}
	d.Newest = st.ModTime()
	return d
}

// measureEntry is the row's one walk (a test seam: the walk's cost grows
// with the tree, so the tests pin which entries take it).
var measureEntry = measureDir

// measureDir walks an entry for its size and newest change (symlinks are
// not followed).
func measureDir(d retainedDir) retainedDir {
	err := filepath.WalkDir(d.Path, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			d.Size += info.Size()
		}
		if info.ModTime().After(d.Newest) {
			d.Newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		d.Err = err.Error()
	}
	return d
}

// terminalState reports an aihub status no work item leaves.
func terminalState(state string) bool {
	return state == api.StatusWrapped || state == api.StatusFailed || state == api.StatusCanceled
}

// keepLocally judges an entry from local facts alone, before any aihub
// read or walk: an unreadable entry or one with no known owner is
// unknown; one a daemon job names, or whose own mtime is inside the
// retention, is retained whatever its owner's state. ok=false: the
// owner's state decides (classifyRetained).
func keepLocally(d retainedDir, live map[string]bool, now time.Time) (class string, ok bool) {
	switch {
	case d.Err != "" || d.WI == "":
		return retUnknown, true
	case live[d.WI], now.Sub(d.Newest) <= gitDirRetention:
		return retRetained, true
	}
	return "", false
}

// classifyRetained judges one entry. state is the owning work item's
// aihub status ("" = not read); live names the daemon's jobs. d.Newest
// must be the walked newest change when state is terminal.
func classifyRetained(d retainedDir, state string, live map[string]bool, now time.Time) string {
	switch {
	case d.Err != "" || d.WI == "" || state == "":
		return retUnknown
	case !terminalState(state), live[d.WI], now.Sub(d.Newest) <= gitDirRetention:
		return retRetained
	case d.Quarantined:
		return retRetired
	default:
		return retReclaimable
	}
}

// workItemStates answers each owner's aihub status ("" = not read). More
// owners than mapped projects are read with one list per project (its
// newest gitDirListLimit, stopping once every owner is named), then per
// id for the owners no list named; otherwise per id outright. So the
// reads number at most min(owners, projects + owners no list names).
func workItemStates(client *aihubclient.Client, projects, owners []string) map[string]string {
	states := make(map[string]string, len(owners))
	if len(owners) > len(projects) {
		want := make(map[string]bool, len(owners))
		for _, id := range owners {
			want[id] = true
		}
		for _, p := range projects {
			if len(states) == len(owners) {
				break
			}
			raw, err := client.ListWorkItemsRaw(p, "", gitDirListLimit)
			if err != nil {
				continue // its owners fall to the per-id read
			}
			var page struct {
				Items []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"items"`
			}
			if json.Unmarshal(raw, &page) != nil {
				continue
			}
			for _, wi := range page.Items {
				if want[wi.ID] {
					states[wi.ID] = wi.Status
				}
			}
		}
	}
	for _, id := range owners {
		if _, named := states[id]; named {
			continue
		}
		states[id] = ""
		if wi, err := client.GetWorkItem(id); err == nil {
			states[id] = wi.Status
		}
	}
	return states
}

// sceneHoldsUnmergedWork answers why a set-aside capsule must not be
// pruned: one of its task-repo clones (repos/<name>) carries an
// uncommitted change or a commit no fresh remote-tracking ref carries.
// Only a capsule has repos/, so an external git dir answers "no". keep
// also covers a clone or a repos/ that cannot be read: a prune deletes
// nothing it cannot prove clean.
func sceneHoldsUnmergedWork(capsuleDir string) (reason string, keep bool) {
	reposDir := filepath.Join(capsuleDir, capsule.DirRepos)
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		return "repos/ unreadable: " + err.Error(), true
	}
	// capsuleDir is <ws>/capsules/<name> (scanRetainedDirs builds it so):
	// <ws>/.repo/<name> is the clone the workspace keeps fetched beside
	// the scene's own frozen refs.
	ws := filepath.Dir(filepath.Dir(capsuleDir))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		clone := filepath.Join(reposDir, e.Name())
		if !workspace.IsRepoClone(clone) {
			continue
		}
		if reason, keep := cloneHoldsUnmergedWork(clone, workspace.RepoCloneDir(ws, e.Name())); keep {
			return e.Name() + ": " + reason, true
		}
	}
	return "", false
}

// cloneHoldsUnmergedWork answers why one task-repo clone must not be
// pruned. A commit counts as carried when either the clone's own
// remote-tracking refs or the mapped workspace's .repo/<name> clone's
// (the fresher view) reach it. Read-only, no fetch: an unreadable clone,
// a missing upstream object or any probe error keeps the scene.
func cloneHoldsUnmergedWork(clone, upstream string) (reason string, keep bool) {
	dirty, err := probeGit(clone, "status", "--porcelain")
	if err != nil {
		return err.Error(), true
	}
	if strings.TrimSpace(dirty) != "" {
		return "uncommitted changes", true
	}
	unpushed, err := probeGit(clone, "rev-list", "--all", "--not", "--remotes")
	if err != nil {
		return err.Error(), true
	}
	shas := strings.Fields(unpushed)
	if len(shas) == 0 {
		return "", false
	}
	left, err := probeGit(upstream, append(append([]string{"rev-list", "--count"}, shas...), "--not", "--remotes")...)
	if err != nil {
		return "commits on no remote-tracking ref (unreadable fresher .repo view: " + err.Error() + ")", true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(left)); err != nil || n > 0 {
		return "commits on no remote-tracking ref", true
	}
	return "", false
}

// gitDirsCheck is the row: it reports every class's count, the size of
// the reclaimable and retired ones and, with prune, removes those.
func gitDirsCheck(r *doctorRun, dataDir string, now time.Time) (string, string) {
	dirs, errs := scanRetainedDirs(dataDir, r.present)
	live := map[string]bool{}
	for _, j := range r.status.Jobs {
		live[j.WorkItemID] = true
	}
	classes := make([]string, len(dirs))
	var owners []string
	asked := map[string]bool{}
	for i, d := range dirs {
		if class, ok := keepLocally(d, live, now); ok {
			classes[i] = class
		} else if !asked[d.WI] {
			asked[d.WI] = true
			owners = append(owners, d.WI)
		}
	}
	states := map[string]string{}
	if r.reachOK && len(owners) > 0 {
		states = workItemStates(aihubclient.New(r.cfg.Server, r.cfg.APIKey), r.cfg.Mapping.Projects(), owners)
	}
	type tally struct {
		n    int
		size int64
	}
	for i, d := range dirs {
		class := classes[i]
		if class == "" {
			state := states[d.WI]
			if terminalState(state) {
				d = measureEntry(d) // only a terminal owner's entry may be pruned: walk it
				dirs[i] = d
			}
			classes[i] = classifyRetained(d, state, live, now)
		}
	}
	// A prune must not delete work no remote carries: a set-aside capsule
	// whose task-repo clone is dirty or holds commits no fresh
	// remote-tracking ref carries is retained, and so are the work item's
	// git dirs (quarantine moved the scene's pair aside together).
	var kept []string
	keptScene := map[string]string{}
	for i, d := range dirs {
		if classes[i] != retReclaimable && classes[i] != retRetired {
			continue
		}
		if reason, keep := sceneHoldsUnmergedWork(d.Path); keep {
			classes[i] = retRetained
			keptScene[d.WI] = reason
			kept = append(kept, d.Path+" ("+reason+")")
		}
	}
	for i, d := range dirs {
		if (classes[i] != retReclaimable && classes[i] != retRetired) || d.WI == "" {
			continue
		}
		if reason, ok := keptScene[d.WI]; ok {
			classes[i] = retRetained
			kept = append(kept, d.Path+" (scene kept: "+reason+")")
		}
	}
	counts := map[string]*tally{}
	var doomed []retainedDir
	for i, d := range dirs {
		class := classes[i]
		if counts[class] == nil {
			counts[class] = &tally{}
		}
		counts[class].n++
		counts[class].size += d.Size
		if class == retReclaimable || class == retRetired {
			doomed = append(doomed, d)
		}
	}
	var parts []string
	for _, class := range []string{retReclaimable, retRetired, retRetained, retUnknown} {
		t := counts[class]
		if t == nil {
			t = &tally{}
		}
		if class == retReclaimable || class == retRetired {
			parts = append(parts, fmt.Sprintf("%s %d (%s)", class, t.n, fmtSize(t.size)))
		} else {
			parts = append(parts, fmt.Sprintf("%s %d", class, t.n))
		}
	}
	detail := fmt.Sprintf("external git dirs under %s and set-aside scenes: %s — reclaimable/retired = a wrapped, failed or canceled work item's git dir / set-aside dir untouched for more than %d days and not a daemon job (sized: what a prune frees)",
		filepath.Join(dataDir, "git"), strings.Join(parts, ", "), int(gitDirRetention/(24*time.Hour)))
	if !r.reachOK {
		detail += "; aihub unreachable: every entry not retained by its age or a daemon job is unknown"
	}
	if len(kept) > 0 {
		detail += "; kept for unmerged work: " + strings.Join(kept, "; ")
	}
	status := "pass"
	switch {
	case len(doomed) > 0 && !r.pruneGitDirs:
		status = "warn"
		detail += "; `pf doctor --prune-git-dirs` removes the reclaimable and retired ones (nothing was deleted)"
	case len(doomed) > 0 && !r.sockOK:
		status = "warn"
		detail += "; nothing pruned: the daemon does not answer GET /status, so its live jobs are unknown — start it with `pf daemon ensure` and re-run"
	case len(doomed) > 0:
		var removed []string
		for _, d := range doomed {
			if err := os.RemoveAll(d.Path); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", d.Path, err))
				continue
			}
			removed = append(removed, d.Path)
		}
		detail += fmt.Sprintf("; pruned %d: %s", len(removed), strings.Join(removed, "; "))
	case r.pruneGitDirs:
		detail += "; nothing to prune"
	}
	var unread []string
	for _, d := range dirs {
		if d.Err != "" {
			unread = append(unread, d.Path+": "+d.Err)
		}
	}
	if errs = append(errs, unread...); len(errs) > 0 {
		status = "warn"
		detail += "; not checked: " + strings.Join(capRows(errs), "; ")
	}
	return status, detail
}

// fmtSize renders a byte count in binary units.
func fmtSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
