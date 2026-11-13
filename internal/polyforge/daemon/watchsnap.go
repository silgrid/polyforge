package daemon

// watchsnap.go: the per-WI watch snapshot (v2-converge §3) — GET
// /jobs/{wi}/watch?after=<seq>: execution.json authority + events after a
// cursor + job bookkeeping. `pf watch <wi> --once` and the Pi Web panel's
// job detail read it. (The chat-subscription store that once shared this
// file is retired,; human decisions are the decision feed,
// decisions.go.)

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
)

// ---------------------------------------------------------------------------
// GET /jobs/{wi}/watch?after=<seq>
// ---------------------------------------------------------------------------

func (d *Daemon) watchSnapshotRoute(w http.ResponseWriter, r *http.Request) {
	var after int64
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, api.ErrDaemonBadInput, "after must be a non-negative integer cursor")
			return
		}
		after = n
	}
	wi := r.PathValue("wi")
	canonical, item := d.resolveWI(wi)
	// the resolution already read aihub (record or failure): the snapshot
	// does not read it again
	hubRead := item != nil || canonical == ""
	if canonical == "" {
		canonical = wi
	}
	// polyforge#224: the demand marker — exactly the value 1 marks this
	// snapshot request as human-side demand for the summarize lane; any
	// other value is unmarked (ignored, no error — the route stays a
	// read). The marker is honored only when the UDS peer credential is
	// available AND the peer is not a step process tree of this daemon
	// (the same machinery and posture as the step-origin guard, never
	// anything from the request body): an unavailable cred cannot prove
	// human-side, so the marker is ignored — fail closed, no error, the
	// route still answers as a read.
	if r.URL.Query().Get("demand") == "1" && d.humanSidePeer(r) {
		d.summarizeDemand(canonical)
	}
	snap := d.watchSnapshot(canonical, item, hubRead, after)
	// Decision order step 2: aihub could not resolve the work item and no
	// local queue/job/capsule answers — CORE-404, never a fabricated
	// empty snapshot.
	if !snap.Known && !snap.Queued && item == nil && canonical == wi {
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("work item %s is unknown or unreadable at aihub", wi))
		return
	}
	// A step process may read its own local snapshot, but must never be
	// the caller that routes a remote query: refused
	// BEFORE the request leaves this daemon.
	if d.wouldRemote(item, snap) {
		if reason := d.stepOriginRefusal(r); reason != "" {
			writeErr(w, api.ErrDaemonForbidden, reason)
			return
		}
	}
	snap, err := d.maybeRemoteSnapshot(item, snap)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// humanSidePeer reports whether the request's UDS peer may mark demand:
// the peer credential is available and the peer is NEITHER a step process
// tree NOR a task process tree of this daemon (runtime.StepProcessOrigin /
// runtime.TaskProcessOrigin — the same machinery the write guard uses; a
// task's GET may read a snapshot, but arming the summarize lane is a
// dispatch, not a read). Unavailable → false (fail closed: the marker is
// ignored, never an error).
func (d *Daemon) humanSidePeer(r *http.Request) bool {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return false
	}
	if _, isStep := runtime.StepProcessOrigin(cred.pid, d.cfg.MachineID); isStep {
		return false
	}
	if _, isTask := runtime.TaskProcessOrigin(cred.pid, d.cfg.MachineID); isTask {
		return false
	}
	return true
}

// summarizeDemand arms the lane for one human-side demand-marked
// snapshot request: the demand-marked request IS the demand — its gates
// run on it (no timestamp is recorded; the demand is fresh by
// construction) — over the caller-resolved dispatch facts, and the route
// answers immediately with the pre-call state: the call runs in the
// background, off the request path, and no lock guarding daemon shared
// data is held across it or its landing.
func (d *Daemon) summarizeDemand(wi string) {
	ji, ok := d.rt.Info(wi)
	if !ok || ji.CapsuleDir == "" {
		return
	}
	// the lane config is the daemon's live cli.toml snapshot,
	// never a claim-time copy
	models, language, intervalSec := d.rt.SummarizeLane()
	d.lane.Marked(wi, models, language, time.Duration(intervalSec)*time.Second, d.resolveDispatch(ji))
}

// resolveDispatch answers the current dispatch's live-transcript facts
// (token, path, size) for the job — gated on a LIVE RUNNING STEP
// DISPATCH (assumption A11: a summarize call happens only inside a
// running step attempt). The gate is stepDispatchLive (the job runs
// with a live driver, the execution record's current step in_progress
// and not parked on a question) plus the dispatch's own process alive
// (the spawn journal's latest entry's pid): a needs_input (parked),
// paused, between-steps or finished WI has no live dispatch — its
// journal's latest entry, however resolvable, is no source (the growth
// gate alone cannot see that: a finished dispatch's entry and
// transcript still resolve, and a fresh lane's zero position makes any
// size a growth) — so the zero Dispatch answers (no source → no growth
// → no call). The transcript resolves through the shared per-dispatch
// rule (runtime.LiveTranscript — the same function the observe lane's
// commit-boundary sync sources from: a resumed pi dispatch's live
// transcript is the capsule file itself, every other dispatch's the
// session store / claude native session). The journal is
// agent-writable, so the entry's spawn token is only the reset key
// (never a trust boundary — the transcript path comes from that rule);
// a missing journal, a dead pid, no resolved transcript or an unreadable
// stat answers the zero Dispatch.
func (d *Daemon) resolveDispatch(ji runtime.JobInfo) summarize.Dispatch {
	if !stepDispatchLive(ji) {
		return summarize.Dispatch{}
	}
	entries, err := capsule.ReadSpawnJournal(ji.CapsuleDir)
	if err != nil || len(entries) == 0 {
		return summarize.Dispatch{}
	}
	last := entries[len(entries)-1]
	// the dispatch's own process (signal-0 probe; EPERM = alive)
	if !pidAlive(last.PID) {
		return summarize.Dispatch{} // the dispatch's process is gone: a dispatch that already ended
	}
	path := d.locateTranscript(ji.CapsuleDir, entries)
	if path == "" {
		return summarize.Dispatch{}
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return summarize.Dispatch{}
	}
	return summarize.Dispatch{Token: last.SpawnToken, Path: path, Size: st.Size()}
}

// stepDispatchLive reports whether the job's current step attempt is
// live and running — the A11 gate's attempt half: the job is running
// with a live driver (the same posture pendingQuestions reads — a
// paused, recovering or sealed job is not running), and the execution
// record's current step is in_progress (execution.json is the driver's
// single-writer authority on step state: a between-steps WI's current
// step is pending, a finished one done). A job with no execution record
// yet (the claim window before the driver's first write) has no running
// step either.
func stepDispatchLive(ji runtime.JobInfo) bool {
	if ji.State != runtime.StateRunning || ji.Agent == nil {
		return false
	}
	ex := readExecutionFile(filepath.Join(ji.CapsuleDir, capsule.ExecutionJSON))
	if ex == nil {
		return false
	}
	st := ex.Steps[ex.CurrentStep]
	return st != nil && st.Status == capsule.StepInProgress
}

// watchSnapshot answers one work item's snapshot by its canonical id.
// item is its aihub record when the caller already read it; hubRead
// says the caller already asked aihub (so a nil item is a failed read,
// not one still to make).
func (d *Daemon) watchSnapshot(wi string, item *api.WorkItem, hubRead bool, after int64) protocol.WatchSnapshot {
	snap := protocol.WatchSnapshot{WorkItem: wi, Events: []protocol.EventEntry{}}
	snap.Queued = d.q != nil && d.q.IsPending(wi)
	jobs, _ := d.statusJobs()
	localLive, held := false, false
	for _, j := range jobs {
		if j.WorkItemID != wi {
			continue
		}
		snap.Known, held = true, true
		snap.Slug = j.Slug
		localLive = j.Status != string(runtime.StatePaused)
		wj := &protocol.WatchJob{Status: j.Status, Origin: j.Origin,
			QuestionID: j.QuestionID, LastProgressAgoSec: j.LastWorkAgoSec}
		snap.Job = wj
	}
	// The hub record (read at most once per snapshot, by the resolution
	// or here) serves only when no live local job answers: it names a
	// closed WI's capsule (project + slug), its display name,
	// and — a manual close (wrap / wrap --fail / cancel / pause) leaves
	// execution.json at `running` — its status, the authority for how
	// the WI ended.
	if localLive || (snap.Queued && held) {
		item = nil
	} else if !hubRead && d.hub != nil {
		if it, err := d.hub.GetWorkItem(wi); err == nil && it != nil {
			item = it
		}
	}
	if item != nil {
		if snap.Slug == "" {
			snap.Slug = item.Slug
		}
		if !snap.Queued {
			snap.HubStatus = item.Status
		}
	}
	if exec := d.readExecution(wi, item); exec != nil {
		snap.Known = true
		snap.Execution = exec
	}
	if d.obs != nil {
		// live progress of the in-flight dispatch (current command, recent
		// files, tool calls); absent between steps / when nothing is monitored
		if lp, ok := d.obs.Progress(wi); ok && lp.InDispatch {
			snap.Progress = watchProgress(lp)
		}
		snap.LastSeq = d.lastSeq(wi)
		snap.Events = append(snap.Events, d.obs.Events().SinceAfter(wi, after)...)
	}
	// polyforge#224: the summarize lane's held narration — the four
	// fields projected under the lane's mutex. Nothing waits on a call
	// in flight: the projection answers the pre-call state (which may
	// lag by one call), and a slow or hung call never delays this
	// response.
	if d.lane != nil {
		p := d.lane.Projection(wi)
		snap.Summary, snap.SummaryError, snap.SummaryAgeSec, snap.SummaryCapped = p.Summary, p.SummaryError, p.SummaryAgeSec, p.SummaryCapped
	}
	return snap
}

// watchProgress projects the observe lane's LiveProgress onto the wire
// block; nil when the dispatch has nothing to show yet.
func watchProgress(lp observe.LiveProgress) *protocol.WatchProgress {
	p := &protocol.WatchProgress{Command: lp.Command, RecentFiles: lp.RecentFiles, Context: lp.Context}
	if lp.Command != "" {
		age := lp.CommandAgeSec
		p.CommandAgeSec = &age
	}
	if lp.ToolCalls > 0 {
		n := int64(lp.ToolCalls)
		p.ToolCalls = &n
	}
	if lp.WaitCalls > 0 {
		n, secs := int64(lp.WaitCalls), lp.WaitSeconds
		p.WaitCalls, p.WaitSeconds = &n, &secs
	}
	if p.Command == "" && len(p.RecentFiles) == 0 && p.ToolCalls == nil && p.WaitCalls == nil && p.Context == nil {
		return nil
	}
	return p
}

// loadExecution reads the WI's execution.json (the authority), from the
// job record's capsule or, for a WI the daemon no longer holds, from the
// capsule its hub record names: capsules/<project>-<seq> under the
// project's mapped workspace (item nil = no record to fall back on). It
// is the one execution.json reader of the daemon (the watch projection
// and the no-id answer both use it), and answers the capsule dir it read
// it from too ("" with a nil execution).
func (d *Daemon) loadExecution(wi string, item *api.WorkItem) (*capsule.Execution, string) {
	var candidates []string
	if d.rt != nil {
		if ji, ok := d.rt.Info(wi); ok && ji.CapsuleDir != "" {
			candidates = append(candidates, ji.CapsuleDir)
		}
	}
	if len(candidates) == 0 && item != nil && d.plan() != nil {
		if ws, err := d.plan().WorkspaceFor(item.ProjectName); err == nil {
			candidates = append(candidates, filepath.Join(ws, "capsules", api.PathName(item.Slug, wi)))
		}
	}
	for _, dir := range candidates {
		if ex := readExecutionFile(filepath.Join(dir, capsule.ExecutionJSON)); ex != nil {
			return ex, dir
		}
	}
	return nil, ""
}

// declaredSteps answers the step sequence of the workflow the capsule's
// driver executes (capsuleWorkflow: its pinned bundle, parsed by the
// driver's own loader) in frontmatter order. nil when that workflow is
// unreadable: the snapshot then omits the field and nothing else changes.
func declaredSteps(wf *driver.Workflow) []protocol.WatchDeclaredStep {
	if wf == nil {
		return nil
	}
	out := make([]protocol.WatchDeclaredStep, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		ds := protocol.WatchDeclaredStep{ID: s.ID}
		if s.OnFail != nil {
			target := s.OnFail.Target
			ds.OnFail = &target
		}
		out = append(out, ds)
	}
	return out
}

// capsuleWorkflow parses the capsule's pinned bundle (capsule.json's
// bundle name under the capsule's workflows/); nil when unreadable.
func capsuleWorkflow(capsuleDir string) *driver.Workflow {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return nil
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil || idn.BundleName == "" {
		return nil
	}
	wf, err := driver.LoadWorkflow(filepath.Join(capsuleDir, capsule.DirWorkflows, idn.BundleName))
	if err != nil {
		return nil
	}
	return wf
}

// capsuleLaneGraph is capsuleWorkflow for the `pf step` checks: the
// snapshot's step graph (its workflow.yaml), readable even when a step
// definition is broken (driver.LoadWorkflowGraph); nil when workflow.yaml
// itself cannot be read or judged. The work item's skipped-step set (fixed
// at the first claim) is applied as the driver applies it
// (driver.Workflow.WithSkipped): the wrap attrs of a skipped tail are the
// last running step's to register.
func capsuleLaneGraph(capsuleDir string) *driver.Workflow {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return nil
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil || idn.BundleName == "" {
		return nil
	}
	wf, err := driver.LoadWorkflowGraph(filepath.Join(capsuleDir, capsule.DirWorkflows, idn.BundleName))
	if err != nil {
		return nil
	}
	return wf.WithSkipped(driver.ReadSkippedSteps(capsuleDir))
}

// readExecutionFile parses one execution.json; nil when absent or torn.
func readExecutionFile(path string) *capsule.Execution {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var ex capsule.Execution
	if json.Unmarshal(raw, &ex) != nil {
		return nil
	}
	return &ex
}

// execFace is one running job's status-face reading of its
// execution.json (the authority).
type execFace struct {
	// QuestionID is the stamp of the current dispatch's open `pf ask` ("" =
	// none).
	QuestionID string
	// RetryIn / RetryCause: what is left of a decided backoff before the
	// current step's next dispatch, and the death cause that owes it
	// (driver.RetryOwed; zero = none owed).
	RetryIn    time.Duration
	RetryCause string
}

// execFaces answers, per running job with a live driver, what its
// execution.json holds at the current step: the open `pf ask` of its
// current dispatch (the capsule's asks.jsonl, workflow contract v2 §5.2)
// — the status face presents such a job as needs_input (D6) — and a
// pending retry backoff (the status face shows "retry in 3m: <cause>").
// Before the driver is (re-)derived nothing could take a reply or serve
// the wait, so nothing is shown.
func (d *Daemon) execFaces(now time.Time) map[string]execFace {
	out := map[string]execFace{}
	for _, ji := range d.rt.List() {
		if ji.State != runtime.StateRunning || ji.Agent == nil || ji.CapsuleDir == "" {
			continue
		}
		ex := readExecutionFile(filepath.Join(ji.CapsuleDir, capsule.ExecutionJSON))
		if ex == nil {
			continue
		}
		var f execFace
		if ask := capsule.OpenAskOf(ji.CapsuleDir, ex); ask != nil {
			f.QuestionID = ask.At
		}
		f.RetryIn, f.RetryCause = driver.RetryOwed(ex, now)
		if f != (execFace{}) {
			out[ji.WorkItem] = f
		}
	}
	return out
}

// readExecution projects loadExecution onto the watch wire shape.
func (d *Daemon) readExecution(wi string, item *api.WorkItem) *protocol.WatchExecution {
	ex, dir := d.loadExecution(wi, item)
	if ex == nil {
		return nil
	}
	wf := capsuleWorkflow(dir)
	out := &protocol.WatchExecution{
		WorkflowStatus: ex.WorkflowStatus, CurrentStep: ex.CurrentStep, UpdatedAt: ex.UpdatedAt,
		Steps: map[string]protocol.WatchStep{}, DeclaredSteps: declaredSteps(wf),
	}
	// the rewind tally: the automatic rewinds and the owner's apart
	if wf != nil {
		out.Rewinds = &protocol.WatchRewinds{
			Agent: driver.AgentRewindCount(ex.RewindStats.Rewinds), MaxRewinds: driver.MaxRewinds(wf),
			Owner: driver.OwnerRewindCount(ex.RewindStats.Rewinds),
		}
	}
	ids := make([]string, 0, len(ex.Steps))
	for id := range ex.Steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := ex.Steps[id]
		if st == nil {
			continue
		}
		out.Steps[id] = protocol.WatchStep{Status: st.Status, StartedAt: st.StepStartedAt}
	}
	// workflow contract v2 §5.2: the current dispatch's open `pf ask` (the
	// capsule's asks.jsonl) is the step's pending question — its id and
	// asked_at the ask's own stamp, the question verbatim.
	if ask := capsule.OpenAskOf(dir, ex); ask != nil {
		out.PendingQuestion = &protocol.WatchPendingQuestion{ID: ask.At, Step: ask.Step, Question: ask.Text, AskedAt: ask.At}
	}
	out.Blocked = watchBlocked(ex)
	return out
}

// pidAlive reports whether pid names a live process (signal 0; EPERM
// means it exists under another uid).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// lastSeq is the highest retained event seq of a WI (0 = none).
func (d *Daemon) lastSeq(wi string) int64 {
	if d.obs == nil {
		return 0
	}
	return d.obs.Events().LastSeq(wi)
}

// watchBlocked projects a blocked workflow's cause (nil unless blocked)
// from the driver's blocked reason: a step's own `blocked` hand-off (kind
// incomplete: its reason and its section of artifacts/handoff.md), a lane
// snapshot of another contract, the hub writes of a done hand-off that
// failed, or any other park the driver made (kind driver).
func watchBlocked(ex *capsule.Execution) *protocol.WatchBlocked {
	if ex.WorkflowStatus != driver.StatusBlocked {
		return nil
	}
	switch reason := ex.BlockedReason; {
	case driver.StepBlocked(reason):
		why, evidence := driver.StepBlockedParts(reason)
		return &protocol.WatchBlocked{Kind: driver.BlockerKindStepBlocked, Step: ex.CurrentStep, Reason: why,
			RequiredAction: driver.StepBlockedRequiredAction, Evidence: evidence}
	case driver.ContractBlocked(reason):
		return &protocol.WatchBlocked{Kind: driver.BlockerKindContract, Step: ex.CurrentStep, Reason: reason,
			RequiredAction: driver.ContractRequiredAction}
	case driver.WriteFailedBlocked(reason):
		return &protocol.WatchBlocked{Kind: driver.BlockerKindWriteFailed, Step: ex.CurrentStep, Reason: reason,
			RequiredAction: driver.WriteFailedRequiredAction}
	}
	return &protocol.WatchBlocked{Kind: "driver", Step: ex.CurrentStep, Reason: ex.BlockedReason,
		RequiredAction: "resolve the cause, then resume the work item (`pf resume`) or cancel it"}
}
