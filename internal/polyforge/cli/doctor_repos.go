package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// doctor_repos.go: the read-only git probe layer behind `pf doctor`'s
// "workspace repo clones" check. Read-only: every command inside a mapped
// workspace's `.repo/` clones and their linked worktrees is one git
// invocation with a short timeout and `--no-optional-locks` — the repos are
// never written, never fetched, never mutated by the layer.
//
// Scan units. Phase one enumerates: every directory directly under
// `<workspace>/.repo/` that carries a `.git` is the clone it is (a `.git`
// directory, or a `.git` pointer file — a linked worktree sitting directly
// in .repo/ itself). Phase two reaches: each enumerated clone's linked
// worktrees come from `git worktree list --porcelain` run in that clone —
// authoritative over the `pi-web-worktrees/*` wrapper and any other
// location, at the face of the clone they hang off. Directory entries
// without a `.git` of their own (e.g. the `pi-web-worktrees/` wrapper) are
// never units. Units are deduplicated by cleaned path, and a phase-one
// enumerated worktree keeps its own enumerated face.
//
// Local facts per unit (never blanked: a failing command is the unit's
// "not checked" reason, the doctor's own never-blank convention):
//
//   - branch: `git symbolic-ref --short HEAD`; failing = detached HEAD ("").
//     An unborn HEAD still answers its branch.
//   - dirty: `git status --porcelain` non-empty (modified/staged tracked +
//     untracked non-ignored — the ruling-4 shape).
//   - unpushed: HEAD is on no remote-tracking ref (`git branch -r --contains
//     HEAD` answers empty). Remote-tracking refs only — no network, no
//     fetch — so a repo with no remotes at all flags as unpushed. An unborn
//     HEAD carries no commit: unpushed is false, the repo is judged by
//     status alone.

// repoClonesGitTimeout bounds one read-only git invocation.
const repoClonesGitTimeout = 10 * time.Second

// A clone unit is one scanned git repo: a clone directly under a mapped
// workspace's `.repo/` or a worktree linked to one.
type repoUnit struct {
	Dir       string // the unit's own directory, cleaned and absolute
	CloneName string // the registry match face: the basename of the clone under .repo/ the unit hangs off — its own when the unit is enumerated by phase one, owning clone's when it is phase two
	Branch    string // the checked-out branch; "" = detached HEAD
	Dirty     bool   // Uncommitted: tracked modified/staged + untracked non-ignored
	Unpushed  bool   // HEAD commit on no remote-tracking ref
	Checked   string // "" = facts read; else the exact reason the probe failed
}

// workspaceRepoUnits enumerates the scan units of one mapped workspace's
// .repo/: clones directly under it (phase one) plus their linked worktrees
// (phase two). No .repo/ at all is no units, not an error — the workspace
// carries no clones.
func workspaceRepoUnits(ws string) []repoUnit {
	repoDir := filepath.Join(ws, ".repo")
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []repoUnit{{Dir: repoDir, CloneName: filepath.Base(repoDir), Checked: err.Error()}}
	}
	seen := map[string]bool{}
	units := make([]repoUnit, 0, len(entries))
	// Phase one: every entry carrying .git is the clone it is (the
	// clone-presence rule is workspace.IsRepoClone — the one rule every
	// check matches by).
	for _, e := range entries {
		clone := filepath.Join(repoDir, e.Name())
		if !workspace.IsRepoClone(clone) {
			continue
		}
		seen[clone] = true
		u := repoUnit{Dir: clone, CloneName: filepath.Base(clone)}
		probeRepoUnit(&u)
		units = append(units, u)
	}
	// Phase two: each clone's linked worktrees — authoritative over the
	// wrapper directory they hang off for the face, any other location; a
	// phase-one enumerated worktree keeps its own enumerated face.
	for i := range units {
		u := &units[i]
		wts, err := cloneWorktreeDirs(u.Dir)
		if err != nil {
			u.Checked = err.Error()
			continue
		}
		for _, wt := range wts {
			if wt == u.Dir || seen[wt] {
				continue
			}
			seen[wt] = true
			wu := repoUnit{Dir: wt, CloneName: u.CloneName}
			probeRepoUnit(&wu)
			units = append(units, wu)
		}
	}
	return units
}

// cloneWorktreeDirs parses `git worktree list --porcelain` in dir and cleans
// every entry's `worktree <path>` row (including dir itself when the clone
// is the main worktree).
func cloneWorktreeDirs(dir string) ([]string, error) {
	out, err := probeGit(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "worktree "); ok {
			dirs = append(dirs, filepath.Clean(rest))
		}
	}
	return dirs, nil
}

// probeRepoUnit fills one unit's local facts, or its exact failure reason.
// Never blanks the clean face that was read before a later command failed —
// but never fakes green either: Checked non-empty means the consumer lists
// the unit and counts it neither clean nor covered.
func probeRepoUnit(u *repoUnit) {
	status, err := probeGit(u.Dir, "status", "--porcelain")
	if err != nil {
		u.Checked = err.Error()
		return
	}
	u.Dirty = strings.TrimSpace(status) != ""
	if br, brerr := probeGit(u.Dir, "symbolic-ref", "--short", "HEAD"); brerr == nil {
		u.Branch = strings.TrimSpace(br)
	} // else detached HEAD: no branch
	if _, err := probeGit(u.Dir, "rev-parse", "-q", "HEAD"); err != nil {
		return // unborn HEAD: nothing committed — judged by status alone
	}
	contained, err := probeGit(u.Dir, "branch", "-r", "--contains", "HEAD")
	if err != nil {
		u.Checked = err.Error()
		return
	}
	u.Unpushed = strings.TrimSpace(contained) == ""
}

// probeGit runs one read-only git command inside an (untrusted) mapped
// .repo/ clone: its own git-dir (its inherited GIT_* stripped), system
// config ignored, fsmonitor and hooks disabled, literal pathspecs, no
// optional locks and a hard timeout (the deliver facts' face — the doctor
// never writes into someone's clone, not even an advice file).
func probeGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoClonesGitTimeout)
	defer cancel()
	argv := append([]string{
		"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null",
		"--no-optional-locks", "--literal-pathspecs",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = dir
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "LC_ALL=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
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

// The check. One aggregated doctor row for every mapped workspace's
// `.repo/` clone units, registered in doctorChecks (verbs_doctor.go)
// with prerequisite needMapping — NOT needAihub: the row runs when aihub
// is unreachable so locally decidable uncoverage still fires, and every
// unit the row cannot decide by aihub becomes a skip note instead (one
// faked pass is one silent uncovered clone). Warn-only: the row never
// moves the exit code, and `--harness` skips doctorChecks wholesale.

// docChRepoClones is the row name (sibling convention: one const beside
// the layer it implements, the workspace-family rows in
// doctor_workspace.go).
const docChRepoClones = "workspace repo clones"

// w198InFlightWI are the WI states that cover a delivery-branch unit
// (ruling 8: queued, running, paused and blocked all say the WI is in
// flight; every terminal state never covers).
var w198InFlightWI = []string{
	api.StatusQueued, api.StatusRunning, api.StatusPaused, api.StatusBlocked,
}

// cloneEntry is one unit's verdict: kind "" none (clean, or covered),
// "warn" a locally decidable or terminal-WI uncoverage with its ONE
// remediation sentence, "skip" a unit honoured in the detail but firing
// no warning — its cover went unread, never a silently clean face.
type cloneEntry struct {
	kind string
	note string
}

// cloneUnitVerdict answers one scanned unit's verdict.
func cloneUnitVerdict(u repoUnit, aihub *aihubclient.Client, reg *repoRegistry) cloneEntry {
	if u.Checked != "" {
		return cloneEntry{kind: "skip", note: u.Dir + ": git facts not checked: " + u.Checked + " (skipped without a faked green)"}
	}
	if !u.Dirty && !u.Unpushed {
		return cloneEntry{} // clean and pushed: nothing to cover
	}
	var facts []string
	if u.Dirty {
		facts = append(facts, "uncommitted change(s) (modified, staged or untracked)")
	}
	if u.Unpushed {
		facts = append(facts, "local commit(s) on no remote-tracking ref")
	}
	if proj, seq, ok := repoDeliveryBranchFace(u.Branch); ok {
		slug := proj + "#" + seq
		wi, err := aihub.GetWorkItem(slug)
		if err != nil {
			var ae *api.Error
			if errors.As(err, &ae) && ae.HTTPStatus() == 404 {
				return cloneEntry{kind: "warn", note: u.Dir + ": branch " + u.Branch + " names work item " + slug +
					" (aihub answers 404: it does not exist — the delivery face covers nothing) with " + strings.Join(facts, " and ") +
					"; " + cloneRemediation(u.CloneName, reg)}
			}
			return cloneEntry{kind: "skip", note: u.Dir + ": branch " + u.Branch + " names work item " + slug +
				" (the delivery face): WI state not decidable — aihub answered: " + err.Error() + " (skipped without a faked green)"}
		}
		if slices.Contains(w198InFlightWI, wi.Status) {
			return cloneEntry{} // the in-flight WI covers it (silence)
		}
		return cloneEntry{kind: "warn", note: u.Dir + ": branch " + u.Branch + " names work item " + slug +
			" (status " + wi.Status + " — a terminal state never covers) with " + strings.Join(facts, " and ") +
			"; " + cloneRemediation(u.CloneName, reg)}
	}
	// Locally decidable: any non-delivery branch or detached HEAD — the
	// branch face covers nothing WI-wise.
	branch := u.Branch
	if branch == "" {
		branch = "a detached HEAD"
	}
	return cloneEntry{kind: "warn", note: u.Dir + ": branch " + branch + " with " + strings.Join(facts, " and ") +
		"; " + cloneRemediation(u.CloneName, reg)}
}

// repoDeliveryBranchFace classifies a branch as the delivery face
// polyforge/<project>-<seq> (api.PathName's documented shape: the seq is
// the part after the last '-'; project names never carry '#').
func repoDeliveryBranchFace(branch string) (proj, seq string, ok bool) {
	rest, has := strings.CutPrefix(branch, "polyforge/")
	if !has {
		return "", "", false
	}
	i := strings.LastIndex(rest, "-")
	if i <= 0 {
		return "", "", false
	}
	proj, seq = rest[:i], rest[i+1:]
	if !isDigits(seq) || strings.ContainsAny(proj, "# ") {
		return "", "", false
	}
	return proj, seq, true
}

// isDigits answers s as one non-empty digit run.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// repoRegistry is the clone→project-repo assignment face (plan decision
// 2), read only when a warning fires: enrich-only, never warn/skip-moving.
type repoRegistry struct {
	client   *aihubclient.Client
	projects []string
	read     bool
	answer   error               // the read face's outcome when attempted
	all      map[string][]string // repo name → registering projects, config order
}

// assigned reads (once) every mapped project's repos and answers the
// registering projects of name, plus whether the read succeeded.
func (reg *repoRegistry) assigned(name string) (ps []string, read bool) {
	if !reg.read {
		reg.read = true
		reg.answer = nil
		reg.all = map[string][]string{}
		for _, proj := range reg.projects {
			raw, err := reg.client.GetProject(proj)
			if err != nil {
				if reg.answer == nil {
					reg.answer = err // the first unanswerable read: the assignment face is incomplete this run
				}
				continue
			}
			var projRec api.Project
			if err := json.Unmarshal(raw, &projRec); err == nil {
				for _, repo := range projRec.Repos {
					if repo.Name != "" {
						reg.all[repo.Name] = append(reg.all[repo.Name], proj)
					}
				}
			}
		}
	}
	if reg.all == nil {
		return nil, false
	}
	return reg.all[name], true
}

// cloneRemediation is ruling 11's remediation sentence with the
// assignment facts as read (assigned: the full pf file create face;
// readable-unassigned: the not-registered note; assignment unreadable:
// the bare face — the detail silently says nothing aihub did not answer).
func cloneRemediation(name string, reg *repoRegistry) string {
	rem := "file a WI first (`pf file create"
	ps, _ := reg.assigned(name) // assignment-read honesty lives in reg.answer
	switch {
	case len(ps) == 1:
		rem += " --project " + ps[0] + " --repo " + name + "`, then carry the dirty work on its `polyforge/<project>-<seq>` delivery branch, or discard it"
	case len(ps) > 1:
		rem += " --project " + ps[0] + " --repo " + name + "` (also registered by projects " + strings.Join(ps[1:], ", ") +
			"), then carry the dirty work on its `polyforge/<project>-<seq>` delivery branch, or discard it"
	case reg.answer == nil: // every assignment face answered: name's absence is a fact
		rem += " --repo " + name + "` (" + name + " is not registered in any mapped project), then carry the dirty work on its `polyforge/<project>-<seq>` delivery branch, or discard it"
	default: // the assignment face went partially or wholly unread: the detail keeps the bare face
		rem += " --repo " + name + "`, then carry the dirty work on its `polyforge/<project>-<seq>` delivery branch, or discard it"
	}
	return rem
}

// repoClonesCheck is the doctor row body: scan every present mapped
// workspace's .repo/ units through the layer above, aggregate their
// verdicts into ONE row (status pass|skip|warn as plan decision 6 —
// never fail; pass says how many were scanned).
func repoClonesCheck(r *doctorRun) (string, string) {
	aihub := aihubclient.New(r.cfg.Server, r.cfg.APIKey)
	reg := repoRegistry{client: aihub, projects: r.cfg.Mapping.Projects()}
	var warnNotes, skipNotes []string
	scanned := 0
	for _, ws := range r.present {
		for _, u := range workspaceRepoUnits(ws) {
			scanned++
			e := cloneUnitVerdict(u, aihub, &reg)
			switch e.kind {
			case "warn":
				warnNotes = append(warnNotes, e.note)
			case "skip":
				skipNotes = append(skipNotes, e.note)
			}
		}
	}
	switch {
	case len(warnNotes) > 0:
		var parts []string
		parts = append(parts, fmt.Sprintf("%d .repo clone unit(s) with uncovered change(s): %s — nothing is moved or committed; the remediation is each unit's", len(warnNotes), strings.Join(capRows(warnNotes), "; ")))
		if reg.answer != nil {
			parts = append(parts, fmt.Sprintf("the assignment face is incomplete: %v", reg.answer))
		}
		if len(skipNotes) > 0 {
			parts = append(parts, fmt.Sprintf("also skipped (no faked green): %s", strings.Join(capRows(skipNotes), "; ")))
		}
		return "warn", strings.Join(parts, "; ")
	case len(skipNotes) > 0:
		return "skip", fmt.Sprintf("workspace-clone state not decidable, %d unit(s) reported without a faked green (reason per unit): %s", len(skipNotes), strings.Join(capRows(skipNotes), "; "))
	default:
		return "pass", fmt.Sprintf("%d .repo clone unit(s) scanned, all clean or covered (no uncovered change)", scanned)
	}
}
