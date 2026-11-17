package runtime

// epicchildren.go: the epic's children snapshot
// (runtime-state/children.json, polyforge#117 Q5). An epic's FIRST claim
// writes it — every child's id, slug, status and its landing record (the
// hub attrs key a wrapped child's landing lives under) verbatim, version
// 1 — and it is IMMUTABLE
// while the epic runs: a re-claim over unchanged children keeps the file
// byte for byte, so a crash restart never refreshes a baseline review
// already judged. A re-claim whose children changed — a child added or
// its status moved — writes the NEXT version beside a resume marker
// (runtime-state/epic-resume.json) that orders the driver to re-run the
// steps from review onward (an owner-class rewind, charged to no budget:
// Q5 is the owner's settled word). The snapshot's shape is the Go/Python
// dual contract (the scripts read it through pfstep/children.py):
//
//	{"version": <n>, "children": [{"id", "slug", "status", "landing"}]}
//
// The children sort by seq, so two claims of the one epic write the same
// bytes over unchanged children. A claim that cannot read the children
// refuses naming why (the same DAEMON-507 class as the target branch) —
// never an empty snapshot that would silently narrow what review judges.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// childLandingAttr is the hub attrs key a wrapped child's landing record
// lives under: the snapshot copies the record VERBATIM (the key is the
// record's data-format name, written by the merge step's wrap-attrs and
// read back by the landing record's own rules).
const childLandingAttr = "integration"

// writeEpicChildren snapshots j's children when the capsule is an epic's
// (its target-branch record carries a checkout key and no target): the
// first claim writes version 1, an unchanged re-claim keeps the file, a
// changed one writes the next version and the resume marker. A hub read
// that fails refuses the claim (DAEMON-507, the blocked note names why).
func (r *Runtime) writeEpicChildren(j *job) error {
	if !capsuleIsEpic(j.capsuleDir) {
		return nil // not an epic: no snapshot, the claim behaves as before
	}
	path := filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.ChildrenJSON))
	current, err := r.hub.ListChildren(j.wi)
	if err != nil {
		return epicChildrenErr(j, "the children cannot be read: %v", err)
	}
	next := capsule.EpicChildren{Children: snapshotChildren(current)}
	var existing []byte
	if raw, err := os.ReadFile(path); err == nil {
		existing = raw
		var have capsule.EpicChildren
		if err := json.Unmarshal(raw, &have); err != nil {
			return epicChildrenErr(j, "%s does not parse: %v", capsule.ChildrenJSON, err)
		}
		if sameChildren(have, next) {
			return nil // unchanged: the reviewed baseline stays byte for byte
		}
		next.Version = have.Version + 1
	} else if !os.IsNotExist(err) {
		return epicChildrenErr(j, "%s is unreadable: %v", capsule.ChildrenJSON, err)
	} else {
		next.Version = 1
	}
	blob, err := json.Marshal(next)
	if err != nil {
		return epicChildrenErr(j, "%s cannot be written: %v", capsule.ChildrenJSON, err)
	}
	if err := atomicfile.Write(path, append(blob, '\n'), 0o644); err != nil {
		return epicChildrenErr(j, "%s cannot be written: %v", capsule.ChildrenJSON, err)
	}
	if existing == nil {
		return nil // the first claim: nothing was reviewed yet, nothing re-runs
	}
	marker, err := json.Marshal(capsule.EpicResume{ResetFrom: "sync", SnapshotVersion: next.Version})
	if err != nil {
		return epicChildrenErr(j, "%s cannot be written: %v", capsule.EpicResumeJSON, err)
	}
	if err := atomicfile.Write(filepath.Join(j.capsuleDir, filepath.FromSlash(capsule.EpicResumeJSON)), append(marker, '\n'), 0o644); err != nil {
		return epicChildrenErr(j, "%s cannot be written: %v", capsule.EpicResumeJSON, err)
	}
	return nil
}

// snapshotChildren answers the snapshot's children of the hub's list:
// the child fields and the landing record verbatim, sorted by seq.
func snapshotChildren(items []api.WorkItem) []capsule.EpicChild {
	sorted := append([]api.WorkItem(nil), items...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Seq < sorted[b].Seq })
	out := make([]capsule.EpicChild, 0, len(sorted))
	for _, wi := range sorted {
		c := capsule.EpicChild{ID: wi.ID, Slug: wi.Slug, Status: wi.Status}
		if wi.Attrs != nil {
			if landing, ok := wi.Attrs[childLandingAttr]; ok && landing != nil {
				if raw, err := json.Marshal(landing); err == nil {
					c.Landing = raw
				}
			}
		}
		out = append(out, c)
	}
	return out
}

// sameChildren answers whether the existing snapshot and the next one
// carry the same children (the version key apart): the ids, statuses and
// landing records all match. A missing landing (nil) and an explicit
// null are the one absence, however JSON spelled it.
func sameChildren(have capsule.EpicChildren, next capsule.EpicChildren) bool {
	if len(have.Children) != len(next.Children) {
		return false
	}
	for i := range have.Children {
		a, b := have.Children[i], next.Children[i]
		if a.ID != b.ID || a.Slug != b.Slug || a.Status != b.Status ||
			landingKey(a.Landing) != landingKey(b.Landing) {
			return false
		}
	}
	return true
}

// landingKey answers the landing record's comparison key: the raw JSON
// with an absent one (nil or the null literal) normalized to the one
// empty spelling.
func landingKey(raw []byte) string {
	s := string(raw)
	if s == "" || s == "null" {
		return ""
	}
	return s
}

// capsuleIsEpic answers whether the capsule's target-branch record names
// an epic (the checkout key and no target — targetbranch.go).
func capsuleIsEpic(capsuleDir string) bool {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, filepath.FromSlash(capsule.TargetBranchJSON)))
	if err != nil {
		return false
	}
	var rec struct {
		Target   string `json:"target"`
		Checkout string `json:"checkout"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		return false
	}
	return rec.Checkout != "" && rec.Target == ""
}

// epicChildrenErr builds the typed refusal of a claim that cannot
// snapshot its children: DAEMON-507 like the claim's other fixings, so
// the detail becomes the blocked note (claimScene).
func epicChildrenErr(j *job, format string, args ...any) *api.Error {
	return &api.Error{
		Code: api.ErrDaemonSpawnFail,
		Detail: fmt.Sprintf("work item %s: the epic's children snapshot could not be written: "+format,
			append([]any{j.wi}, args...)...),
	}
}

// epicChildrenProvider answers the runtime's hub read of j's children —
// the closure the driver's running re-check calls before every dispatch
// (AgentStartInput.EpicChildren). nil on a capsule that is not an
// epic's: the re-check is an epic's own.
func epicChildrenProvider(r *Runtime, j *job) func() ([]capsule.EpicChild, error) {
	if !capsuleIsEpic(j.capsuleDir) {
		return nil
	}
	return func() ([]capsule.EpicChild, error) {
		items, err := r.hub.ListChildren(j.wi)
		if err != nil {
			return nil, err
		}
		return snapshotChildren(items), nil
	}
}
