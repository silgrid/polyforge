package runtime

// info.go: the exported per-job READ face the phase-3 daemon assembly
// needs (the only surface the daemon may use beside Start/Stop/Restart
// and the protocol channels): the local status projection (GET /status),
// the RT→Attach bridge's observe-attachment synthesis and the shutdown
// sequence's live-job enumeration. Purely additive: the state machine
// and every writer stay untouched.

import (
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// JobInfo is a point-in-time snapshot of one locally managed job.
// Agent is the live supervised handle (nil when the job holds none —
// blocked after a bounded fail, or a record mid-seal). Everything else
// mirrors the persisted jobRecord fields (state.go), which is what the
// restart reconciliation and the daemon bookkeeping share.
type JobInfo struct {
	WorkItem string
	Project  string
	Slug     string // "<project>#<seq>" display name; "" on a pre-slug record
	// SessionFingerprint is the PUBLIC session handle (B3); the secret
	// never leaves state.json and the one-shot askpass, but it is exposed
	// here for the daemon's in-process faces (the M4 revalidation seam
	// rides RTMsg, not this struct).
	SessionFingerprint string
	State              JobState
	StopReason         string
	CapsuleDir         string
	Git                gitops.Opts
	SessionID          string // the SECRET — in-process consumers only, never a log surface
	SpawnN             int
	Transcript         string
	BundleName         string
	Agent              AgentHandle
	AgentPID           int
	AgentStartedAt     string
	Origin             *Origin
	// AwaitingOwner: the live driver's step waits on the owner (`pf ask`,
	// AgentStartInput.OwnerWait): the job holds no running slot while it
	// does (the daemon's slot accounting). Never set without a live agent.
	AwaitingOwner bool
}

// Info returns the snapshot of one job; ok=false when the work item has
// no locally managed job (never claimed here, or already sealed and
// dropped).
func (r *Runtime) Info(wi string) (JobInfo, bool) {
	j := r.resolveJob(wi)
	if j == nil {
		return JobInfo{}, false
	}
	return j.info(), true
}

// List returns a snapshot of every locally managed job.
func (r *Runtime) List() []JobInfo {
	r.mu.Lock()
	js := make([]*job, 0, len(r.jobs))
	for _, j := range r.jobs {
		js = append(js, j)
	}
	r.mu.Unlock()
	out := make([]JobInfo, 0, len(js))
	for _, j := range js {
		out = append(out, j.info())
	}
	return out
}

func (j *job) info() JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	return JobInfo{
		WorkItem:           j.wi,
		Project:            j.project,
		Slug:               j.slug,
		SessionFingerprint: j.sessionFingerprint,
		State:              j.state,
		StopReason:         j.stopReason,
		CapsuleDir:         j.capsuleDir,
		Git:                j.git,
		SessionID:          j.sessionID,
		SpawnN:             j.spawnN,
		Transcript:         j.transcript,
		BundleName:         j.bundleName,
		Agent:              j.agent,
		AgentPID:           j.agentPID,
		AgentStartedAt:     j.agentStartedAt,
		Origin:             j.origin,
		AwaitingOwner:      j.ownerWait && j.agent != nil,
	}
}

// Persist rewrites state.json on demand. The daemon's graceful-shutdown
// sequence calls it as its final durability step (reference.md §6.2: the persisted
// bookkeeping must reflect reality before the process exits).
func (r *Runtime) Persist() error { return r.persist() }

// resolveJob answers the job behind any id form: the canonical key first,
// then the alias table — so daemon-side route lookups by the operator's
// submitted spelling (a slug, most often) find the live job without an
// aihub round-trip.
func (r *Runtime) resolveJob(wi string) *job {
	if j := r.jobOf(wi); j != nil {
		return j
	}
	r.mu.Lock()
	canonical := r.aliases[wi]
	r.mu.Unlock()
	if canonical == "" {
		return nil
	}
	return r.jobOf(canonical)
}

// TaskModels answers the one-shot task lane's candidate list of a
// project read LIVE from its workspace yaml: the
// bundle-less `steps.<project>.task.models` list, else
// `steps.<project>.default.models`. It is the daemon's single read of
// that fact (workspace.TaskModels over a fresh LoadWorkspace), so an
// edit governs the next spawn with no daemon restart and nothing is
// snapshotted. A yaml the shared loader refuses is answered as its typed
// error (DAEMON-414). When neither list is declared the error is the
// named refusal from workspace.MissingTaskModelsError.
func (r *Runtime) TaskModels(project string) ([]string, string, error) {
	wcfg, err := r.LoadWorkspace(project)
	if err != nil {
		return nil, "", err
	}
	list, from := workspace.TaskModels(wcfg, project)
	if list == nil {
		return nil, "", workspace.MissingTaskModelsError(project)
	}
	return list, from, nil
}

// SummarizeLane answers the summarize lane's configuration from the
// daemon's loaded cli.toml snapshot: the [summarize]
// table's models (nil = the table is absent: the lane is off), the
// narration language and the per-WI minimum call spacing in seconds, the
// two optional keys at their documented defaults when absent. It is one
// machine-level table for every project; no file is read per request — a
// `pf config set` or `pf config reload` swaps the snapshot (class live).
func (r *Runtime) SummarizeLane() (models []string, language string, intervalSec int) {
	return r.cfg.Live.Summarize()
}
