package capsule

// epicchildren.go: the epic's children snapshot and its resume marker —
// the runtime-state files an epic's claim writes (runtime/
// epicchildren.go, polyforge#117) and the driver reads back (its running
// re-check and the resume reset). The shapes are the Go/Python dual
// contract: the scripts read the same files through pfstep/children.py,
// and the landing record each child carries is the hub attrs record the
// child's own lane wrapped it with, verbatim (Q6).

// ChildrenJSON is the epic's children snapshot: written at the epic's
// first claim, immutable while it runs (a re-claim over unchanged
// children keeps it byte for byte), a new version beside EpicResumeJSON
// when the children changed.
const ChildrenJSON = "runtime-state/children.json"

// EpicResumeJSON is the resume marker a changed children snapshot writes:
// the driver consumes it at its next plan pass — an owner-class rewind
// to the step it names (the affected steps re-run, charged to no budget;
// Q5 is the owner's settled word) — and removes it once applied.
const EpicResumeJSON = "runtime-state/epic-resume.json"

// EpicChild is one child of the snapshot.
type EpicChild struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
	// Landing is the child's landing record as the hub carried it,
	// verbatim — a wrapped child's landed_on_target evidence (Q6): the
	// PR links and merge commits review reads and the epic's own landing
	// record attaches. A child without one is JSON null (the
	// verify-children step judges it, never the snapshot).
	Landing []byte `json:"landing"`
}

// EpicChildren is the snapshot file's shape.
type EpicChildren struct {
	Version  int         `json:"version"`
	Children []EpicChild `json:"children"`
}

// EpicResume is the resume marker's shape: the step the driver re-runs
// from (review) and the snapshot version that moved.
type EpicResume struct {
	ResetFrom       string `json:"reset_from"`
	SnapshotVersion int    `json:"snapshot_version"`
}
