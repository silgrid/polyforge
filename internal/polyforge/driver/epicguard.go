package driver

// epicguard.go (polyforge#117, AC2/Q5): the epic's running re-check and
// its resume reset.
//
// The re-check: before EVERY dispatch (the merge's included) the driver
// compares the hub's current children — Config.EpicChildren, the
// runtime's hub read over the epic's own id — with the claim's immutable
// snapshot (capsule.ChildrenJSON). A child that failed or was canceled,
// one the snapshot does not carry, one gone from the live set, or a
// status that moved parks the workflow blocked on the driver's own
// authority (TransBlocked): no question is asked, the owner's next
// message resumes it — exactly the settled Q3 (运行中由 daemon 进入本地
// blocked，daemon 不提问) and Q5 (运行时核对子票成员与状态，有变化立即
// 阻塞). A snapshot the claim wrote but the driver cannot read parks the
// workflow too, naming why — never a silent pass over an unreadable
// baseline.
//
// The resume reset: a changed children snapshot wrote capsule.EpicResumeJSON
// (runtime/epicchildren.go) beside its new version. The plan pass reads
// it first and, after the owner's resume (a claim of the parked capsule
// is one, start.go), lands it as ONE owner-class rewind to the step it
// names (the lane's resume_reset): the steps from there through the current one archive
// and reset, the ledger records the owner class — charged to no budget,
// Q5 is the owner's settled word — and the marker is consumed. A marker
// whose target is not behind the current step (the epic parked before
// review ran) resets nothing and is consumed the same.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// EvEpicResume is the resume reset's event: the snapshot version that
// moved, the step the lane re-runs from and the archived steps.
const EvEpicResume = "epic_resume"

// TransEpicResume is the plan pass's landing of the resume marker: an
// owner-class rewind to the step the marker names, charged to no budget.
const TransEpicResume TransitionKind = "epic_resume"

// epicChildrenPath is the capsule's snapshot path.
func epicChildrenPath(capsuleDir string) string {
	return filepath.Join(capsuleDir, filepath.FromSlash(capsule.ChildrenJSON))
}

// epicResumePath is the capsule's resume marker path.
func epicResumePath(capsuleDir string) string {
	return filepath.Join(capsuleDir, filepath.FromSlash(capsule.EpicResumeJSON))
}

// readEpicChildren answers the capsule's snapshot and whether it is
// there at all; present-but-unreadable answers the error (the claim
// wrote the file, so a torn one is the machine's to fix, never a pass).
func readEpicChildren(capsuleDir string) (capsule.EpicChildren, bool, error) {
	raw, err := os.ReadFile(epicChildrenPath(capsuleDir))
	if os.IsNotExist(err) {
		return capsule.EpicChildren{}, false, nil
	}
	if err != nil {
		return capsule.EpicChildren{}, true, fmt.Errorf("the epic's children snapshot is unreadable: %w", err)
	}
	var snap capsule.EpicChildren
	if err := json.Unmarshal(raw, &snap); err != nil {
		return capsule.EpicChildren{}, true, fmt.Errorf("the epic's children snapshot does not parse: %w", err)
	}
	return snap, true, nil
}

// epicChildrenBlocked answers why the dispatch of stepID may not run over
// the epic's children as they stand ("" = it may): the hub read, the
// snapshot read or the comparison. Presenting the reason parks the
// workflow (the caller's TransBlocked); the hub read's own failure is a
// reason like any other — the owner sees a machine note, never a
// question, and the file reads run outside the mutex.
func (d *Driver) epicChildrenBlocked(stepID string) string {
	if d.cfg.EpicChildren == nil {
		return "" // not an epic, or the lane runs no re-check
	}
	snap, present, err := readEpicChildren(d.wi.CapsuleDir)
	if !present {
		return "" // no snapshot: the capsule is not an epic's
	}
	if err != nil {
		return fmt.Sprintf("step %s: the epic's children re-check could not read the claim's snapshot: %v", stepID, err)
	}
	live, lerr := d.cfg.EpicChildren()
	if lerr != nil {
		return fmt.Sprintf("step %s: the epic's children cannot be read from the work-item service: %v", stepID, lerr)
	}
	byID := map[string]capsule.EpicChild{}
	for _, c := range snap.Children {
		byID[c.ID] = c
	}
	liveIDs := map[string]bool{}
	for _, c := range live {
		liveIDs[c.ID] = true
		had, ok := byID[c.ID]
		switch {
		case !ok:
			return fmt.Sprintf("step %s: the epic's children changed: %s (in the live set, not the claim's snapshot) — the epic is blocked for the owner", stepID, epicChildName(c))
		case had.Status != c.Status:
			return fmt.Sprintf("step %s: the epic's children changed: %s is %s (the claim's snapshot has it %s) — the epic is blocked for the owner", stepID, epicChildName(c), c.Status, had.Status)
		}
	}
	for _, c := range snap.Children {
		if !liveIDs[c.ID] {
			return fmt.Sprintf("step %s: the epic's children changed: %s is no longer among the epic's children — the epic is blocked for the owner", stepID, epicChildName(c))
		}
	}
	return ""
}

// epicChildName answers the child's slug, else its id.
func epicChildName(c capsule.EpicChild) string {
	if c.Slug != "" {
		return c.Slug
	}
	return c.ID
}

// epicResumePending answers the resume marker and whether the plan pass
// must land it; a marker that does not parse is an error (the claim
// wrote it atomically; a torn one is the machine's).
func (d *Driver) epicResumePending() (capsule.EpicResume, bool, error) {
	raw, err := os.ReadFile(epicResumePath(d.wi.CapsuleDir))
	if os.IsNotExist(err) {
		return capsule.EpicResume{}, false, nil
	}
	if err != nil {
		return capsule.EpicResume{}, true, fmt.Errorf("the epic's resume marker is unreadable: %w", err)
	}
	var marker capsule.EpicResume
	if err := json.Unmarshal(raw, &marker); err != nil {
		return capsule.EpicResume{}, true, fmt.Errorf("the epic's resume marker does not parse: %w", err)
	}
	if marker.ResetFrom == "" {
		return capsule.EpicResume{}, true, fmt.Errorf("the epic's resume marker names no step to reset from")
	}
	return marker, true, nil
}

// applyEpicResumeLocked lands the resume marker as an owner-class rewind
// to the step it names: nothing to reset when that step is not behind the
// current one (the epic parked before it ran) — the marker is consumed
// either way. The event beside the owner rewind's own names the snapshot
// version that moved.
func (d *Driver) applyEpicResumeLocked(req TransitionRequest, info *appliedInfo) error {
	rw := req.OwnerRewind
	if rw == nil {
		return fmt.Errorf("driver: an epic resume write carries no rewind request")
	}
	targets := OwnerRewindTargets(d.workflow, d.exec)
	for _, t := range targets {
		if t == rw.To {
			err := d.applyOwnerRewindLocked(TransitionRequest{OwnerRewind: rw}, info)
			if err == nil {
				info.extra = append(info.extra, appliedEvent{EvEpicResume, map[string]any{
					"spawn_n": d.spawnN, "to": rw.To, "reason": clipRunes(rw.Reason, 2000),
				}})
			}
			return err
		}
	}
	// nothing to reset: the marker is stale for this position, consumed
	// without a move
	return nil
}
