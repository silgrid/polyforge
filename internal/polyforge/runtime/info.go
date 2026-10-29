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
	}
}

// wiScope is the wake-target scope of one work item: the
// origin chat id and the project of its job record — the two facts the
// scoped decision wake resolves a WI's audience from.
type wiScope struct {
	origin  string
	project string
}

// ScopeOf answers the wake-target scope of a work item — the origin
// chat id and the project of its job record — for a job the runtime
// still holds (the live record) and for one it already dropped (the
// retained facts of the record dropJob kept): a terminal close moves
// the job to DONE and drops it (Info answers false then), and the
// scoped decision wake must still resolve both facts after
// it — the terminal decision is exactly the one that fires it. ok=false
// when this runtime never held the work item. In-memory only: the
// job's own mutex, then the table lock, no I/O under either.
func (r *Runtime) ScopeOf(wi string) (originChat, project string, ok bool) {
	if ji, held := r.Info(wi); held {
		origin := ""
		if ji.Origin != nil {
			origin = ji.Origin.ChatID
		}
		return origin, ji.Project, true
	}
	r.mu.Lock()
	sc, held := r.dropped[wi]
	r.mu.Unlock()
	return sc.origin, sc.project, held
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

// SummarizeLane answers the summarize lane's configuration of a project
// read LIVE from its workspace yaml (polyforge#33 supersedes the
// claim-time snapshot of #224/#243/#250): the root summarize.models list
// (nil = the lane is off), the narration language and the per-WI minimum
// call spacing in seconds. A yaml that cannot be read turns the lane off
// for this request — the work item is dispatch-blocked on the same config
// anyway, and the lane is best-effort narration.
func (r *Runtime) SummarizeLane(project string) (models []string, language string, intervalSec int) {
	wcfg, err := r.LoadWorkspace(project)
	if err != nil {
		return nil, workspace.DefaultSummarizeLanguage, workspace.DefaultSummarizeIntervalSec
	}
	return workspace.SummarizeModels(wcfg), workspace.SummarizeLanguage(wcfg), workspace.SummarizeIntervalSec(wcfg)
}
