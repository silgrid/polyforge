package runtime

// targetbranch.go: the work item's fixed target branch
// (capsule.TargetBranchJSON) — polyforge#80: a sub-ticket whose parent's
// lane declares children_branch works on the substituted branch. The
// FIRST claim computes it from the parent and the parent's lane — read
// from the hub and the daemon's lane set, never the capsule's own lane
// snapshot — and writes it into the capsule BEFORE the task repos are
// cloned; every later (re-)claim reads the fixed value and never re-reads
// the parent, so a parent re-bound or a lane changed while the sub-ticket
// runs never moves the branch. A claim that must compute the value and
// cannot read the parent, or the parent's lane, is refused naming why
// (never a silent fall-back to the default branch); a sub-ticket whose
// parent's lane declares nothing — a parent bound to no lane (a registry
// ticket) counts as declaring nothing, exactly like a parent whose lane
// declares no children_branch — and a work item without a parent keep
// the claim exactly as it was.

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
// parent it was computed from. The object keeps named fields so the epic
// ticket's own record can extend it later (its checkout start point and
// merge target).
type targetBranchRecord struct {
	Target string `json:"target"`
	Parent string `json:"parent"`
}

// fixTargetBranch answers the work item's target branch and fixes it
// into the capsule when the capsule does not carry the file yet (""
// when the work item has none). The FIRST claim that needs the value
// reads the parent and its lane; a claim that cannot is refused with the
// typed reason (targetBranchErr), which becomes the blocked note.
func (r *Runtime) fixTargetBranch(j *job) (string, error) {
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.TargetBranchJSON))
	if raw, err := os.ReadFile(path); err == nil {
		var fixed targetBranchRecord
		if err := json.Unmarshal(raw, &fixed); err != nil {
			return "", targetBranchErr(j, "%s does not parse: %v", capsule.TargetBranchJSON, err)
		}
		return fixed.Target, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", targetBranchErr(j, "%s is unreadable: %v", capsule.TargetBranchJSON, err)
	}
	if j.item == nil || j.item.ParentID == nil || *j.item.ParentID == "" {
		return "", nil // no parent: the claim behaves exactly as before
	}
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
