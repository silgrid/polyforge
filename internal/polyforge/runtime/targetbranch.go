package runtime

// targetbranch.go: the work item's fixed target branch
// (capsule.TargetBranchJSON) — polyforge#80: a sub-ticket whose parent's
// lane declares children_branch works on the substituted branch; and
// polyforge#117: a work item whose OWN lane declares `epic` works on the
// children branch of its own seq, recorded as the record's `checkout`
// key with no `target` (the epic merges into the origin's default
// branch). The FIRST claim computes it — from the parent and the parent's
// lane for a sub-ticket, from the item's own lane for an epic — reading
// the hub and the daemon's lane set, never the capsule's own lane
// snapshot — and writes it into the capsule BEFORE the task repos are
// cloned; every later (re-)claim reads the fixed value and never re-reads
// the lane, so a lane changed while the work item runs never moves the
// branch. A claim that must compute the value and cannot read the lane is
// refused naming why (never a silent fall-back to the default branch);
// a sub-ticket whose parent's lane declares nothing — a parent bound to
// no lane (a registry ticket) counts as declaring nothing, exactly like
// a lane that declares no children_branch — and a work item on a lane
// that declares neither children_branch nor epic keep the claim exactly
// as it was.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// targetBranchRecord is the fixed file's shape: the branch and the
// parent a sub-ticket's value was computed from, and — an epic's own
// record (polyforge#117) — the checkout key. The object keeps named
// fields so the readers judge the shape themselves.
type targetBranchRecord struct {
	Target string `json:"target,omitempty"`
	Parent string `json:"parent,omitempty"`
	// Checkout is an EPIC's own record: the branch the epic checks out
	// (the children branch of its own seq). An epic record carries NO
	// target: the epic merges into the origin's default branch, which
	// every reader takes as the change's base exactly as for a work item
	// with no fixed target — so the two roles (the checkout start point,
	// the merge target) never collide in one key (Q4).
	Checkout string `json:"checkout,omitempty"`
}

// fixTargetBranch answers the work item's checkout branch and fixes it
// into the capsule when the capsule does not carry the file yet (""
// when the work item has none). The FIRST claim that needs the value
// computes it — a sub-ticket's from the parent and the parent's lane, an
// EPIC's (its own lane declares `epic`, polyforge#117) from its own lane
// and its own seq — and a claim that cannot is refused with the typed
// reason (targetBranchErr), which becomes the blocked note.
func (r *Runtime) fixTargetBranch(j *job) (string, error) {
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.TargetBranchJSON))
	if raw, err := os.ReadFile(path); err == nil {
		var fixed targetBranchRecord
		if err := json.Unmarshal(raw, &fixed); err != nil {
			return "", targetBranchErr(j, "%s does not parse: %v", capsule.TargetBranchJSON, err)
		}
		// an epic's own record carries the checkout key (and no target);
		// a sub-ticket's the target (and no checkout)
		if fixed.Checkout != "" {
			return fixed.Checkout, nil
		}
		return fixed.Target, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", targetBranchErr(j, "%s is unreadable: %v", capsule.TargetBranchJSON, err)
	}
	if j.item != nil && j.item.ParentID != nil && *j.item.ParentID != "" {
		if checkout, err := r.fixSubTicketBranch(j, path); checkout != "" || err != nil {
			return checkout, err
		}
	}
	// the item's own lane may declare it an epic (polyforge#117): the
	// checkout branch is the children branch of its OWN seq, recorded
	// with no target — the epic merges into the default branch
	if j.item != nil && j.bundleName != "" {
		lane, lerr := r.readLane(j.project, j.bundleName)
		switch {
		case lerr != nil:
			return "", targetBranchErr(j, "the item's own lane %s does not load: %v", j.bundleName, lerr)
		case lane == nil:
			return "", targetBranchErr(j, "the item's own lane %s is not in this daemon's lane set (nothing is claimed; re-bind the work item to an installed lane)", j.bundleName)
		case lane.Epic:
			if lane.ChildrenBranch == "" {
				return "", targetBranchErr(j, "the epic lane %s declares no children_branch to name the checkout branch", j.bundleName)
			}
			checkout := workflow.ChildrenBranchTarget(lane.ChildrenBranch, j.item.Seq, j.item.ProjectName)
			if !workflow.ValidBranchName(checkout) {
				return "", targetBranchErr(j, "the lane %s names the checkout branch %q for this epic, which is not a legal git branch name", j.bundleName, checkout)
			}
			blob, err := json.Marshal(targetBranchRecord{Checkout: checkout})
			if err != nil {
				return "", targetBranchErr(j, "%s cannot be written: %v", capsule.TargetBranchJSON, err)
			}
			if err := atomicfile.Write(path, append(blob, '\n'), 0o644); err != nil {
				return "", targetBranchErr(j, "%s cannot be written: %v", capsule.TargetBranchJSON, err)
			}
			return checkout, nil
		}
	}
	return "", nil
}

// fixSubTicketBranch answers a sub-ticket's fixed branch ("" when its
// parent's lane declares none) and writes it into the capsule at path —
// polyforge#80's computation, unchanged: the FIRST claim reads the parent
// and its lane — from the hub and the daemon's lane set, never the
// capsule's own lane snapshot — and every later (re-)claim keeps the
// fixed value.
func (r *Runtime) fixSubTicketBranch(j *job, path string) (string, error) {
	parent, err := r.hub.GetWorkItem(*j.item.ParentID)
	if err != nil {
		return "", targetBranchErr(j, "the parent %s cannot be read: %v", *j.item.ParentID, err)
	}
	if parent.BundleName == nil || *parent.BundleName == "" {
		// polyforge#114: a parent bound to no lane (a registry ticket
		// used as the parent) declares nothing, exactly like a lane that
		// declares no children_branch — the claim behaves exactly as
		// before; only a parent that NAMES a lane this daemon cannot
		// read (below) is refused.
		return "", nil
	}
	lane, _, lerr := r.laneSet(parent.ProjectName).Load(*parent.BundleName)
	switch {
	case lerr != nil:
		return "", targetBranchErr(j, "the lane %s of the parent %s does not load: %v", *parent.BundleName, api.DisplayName(parent.Slug, parent.ID), lerr)
	case lane == nil:
		return "", targetBranchErr(j, "the lane %s of the parent %s is not in this daemon's lane set (nothing is claimed; re-bind the parent to an installed lane)", *parent.BundleName, api.DisplayName(parent.Slug, parent.ID))
	case lane.ChildrenBranch == "":
		return "", nil // the parent's lane declares none: the claim behaves exactly as before
	}
	target := workflow.ChildrenBranchTarget(lane.ChildrenBranch, parent.Seq, parent.ProjectName)
	if !workflow.ValidBranchName(target) {
		return "", targetBranchErr(j, "the lane %s of the parent %s names the branch %q, which is not a legal git branch name", *parent.BundleName, api.DisplayName(parent.Slug, parent.ID), target)
	}
	who := api.DisplayName(parent.Slug, parent.ID)
	blob, err := json.Marshal(targetBranchRecord{Target: target, Parent: who})
	if err != nil {
		return "", targetBranchErr(j, "%s cannot be written: %v", capsule.TargetBranchJSON, err)
	}
	if err := atomicfile.Write(path, append(blob, '\n'), 0o644); err != nil {
		return "", targetBranchErr(j, "%s cannot be written: %v", capsule.TargetBranchJSON, err)
	}
	return target, nil
}

// targetBranchErr builds the typed refusal of a claim that cannot fix
// its target branch: DAEMON-507 like the claim's clone failures, so the
// detail becomes the blocked note (claimScene) — the claim is refused
// naming why, never a silent fall-back to the default branch.
func targetBranchErr(j *job, format string, args ...any) *api.Error {
	return &api.Error{
		Code: api.ErrDaemonSpawnFail,
		Detail: fmt.Sprintf("work item %s: the sub-ticket's target branch could not be fixed: "+format,
			append([]any{j.wi}, args...)...),
	}
}
