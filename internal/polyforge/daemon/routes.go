package daemon

// routes.go: the daemon's UDS HTTP face. Every route translates the CLI
// contract (internal/polyforge/cli/verbs_daemon.go — the pinned route
// family) into Runtime/observe calls. Every error
// answer is the TOP-LEVEL api.Error envelope {code, detail} (the same
// typed shape the aihub face uses; DAEMON-* codes per reference.md §5).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
	"github.com/silgrid/polyforge/internal/version"
)

// The wire answer shapes the CLI decodes are the CLI's own types: one
// definition per shape.
type (
	submitResponse       = cli.SubmitResponse
	statusJob            = cli.StatusJob
	statusResponse       = cli.StatusResponse
	queueStats           = cli.QueueStats
	resumePausedResponse = cli.ResumePausedResponse
)

type opsConfigResponse struct {
	Auto bool `json:"auto"`
	Max  int  `json:"max"`
}

type closeResponse struct {
	WorkItemID string `json:"work_item_id"`
	Slug       string `json:"slug,omitempty"` // display name, from the job record
	// Outcome: sealed | already_terminal | already_paused.
	Outcome string `json:"outcome"`
	Status  string `json:"status,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// resumeBlockedResponse is the POST /jobs/{id}/claim {"resume":true}
// answer (polyforge#116): the §6.4 owner move re-established execution —
// the parked workflow is unblocked and the frozen step re-dispatches in
// place.
type resumeBlockedResponse struct {
	Resumed    bool   `json:"resumed"`
	WorkItemID string `json:"work_item_id"`
	// Deferred: the hub was unreachable (DAEMON-503) when the claim ran;
	// the claim is queued in the persisted submit queue and retried with
	// backoff, exactly like a submit.
	Deferred bool `json:"deferred,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code, detail string) {
	ae := api.Error{Code: code, Detail: detail}
	writeJSON(w, ae.HTTPStatus(), ae)
}

// writeErrOf surfaces a typed api error verbatim (CORE-* transparency)
// and wraps everything else as DAEMON-500.
func writeErrOf(w http.ResponseWriter, err error) {
	var ae *api.Error
	if errors.As(err, &ae) {
		writeJSON(w, ae.HTTPStatus(), ae)
		return
	}
	writeErr(w, api.ErrDaemonInternal, err.Error())
}

func writeAdmissionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errShuttingDown):
		writeErr(w, api.ErrDaemonGone, err.Error())
	default:
		writeErrOf(w, err)
	}
}

// handler assembles the full route table behind the step-origin guard.
func (d *Daemon) handler() http.Handler {
	return d.refuseStepOrigin(d.routes())
}

// routes is the bare route table.
func (d *Daemon) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// answer: the owner's words to the WI's live step, or the wake of its
	// park (answer.go). A step process never gets here (refuseStepOrigin).
	mux.HandleFunc("POST /jobs/{wi}/answer", d.answerRoute)
	mux.HandleFunc("POST /jobs/{wi}/claim", d.submitRoute())
	// wi_seWTgHeKtaa8 (v2-converge §7): the live-holder half of the
	// operator's `pf resume` on a PAUSED work item — the CLI has already
	// cleared clean_pause at the hub; this route makes that observable
	// by the ONLINE holder (the startup reconciliation is the only other
	// consumer of that hub state, and it runs once per daemon start).
	mux.HandleFunc("POST /jobs/{wi}/resume-paused", d.resumePausedRoute)
	mux.HandleFunc("POST /jobs/{wi}/pause", d.pauseRoute)
	mux.HandleFunc("POST /jobs/{wi}/wrap", d.wrapRoute)
	mux.HandleFunc("GET /status", d.statusRoute)
	mux.HandleFunc("GET /profiles", d.profilesRoute)
	// GET /models: the read-only model+effort × step
	// profile beside GET /status. The method pattern makes every non-GET
	// method on this path a 405; there is no write face, no config, no
	// mutation. A step process may read it like GET /status (the
	// step-origin guard refuses only mutating methods).
	mux.HandleFunc("GET /models", d.modelsRoute)
	mux.HandleFunc("POST /ops/config", d.opsConfigRoute)
	// cli.toml hot reload: both writes sit behind the
	// step-origin guard (a step process tree is refused DAEMON-403) and
	// are reachable only over the local UDS.
	mux.HandleFunc("POST /config/reload", d.configReloadRoute)
	mux.HandleFunc("POST /config/check", d.configCheckRoute)
	mux.HandleFunc("POST /shutdown", d.shutdownRoute)
	// the operator's machine-role move (identity.go): behind
	// refuseStepOrigin like every POST here, so a step or task process
	// tree is refused DAEMON-403.
	mux.HandleFunc("POST /machine/role", d.machineRoleRoute)
	// The explicit, read-only message analyzer (; one task in
	// the task lane since body {caller_id}, it blocks until
	// the task is terminal and answers the structured result). A POST
	// outside the step-write allowlist: the whole UDS face sits behind
	// refuseStepOrigin, so a step or task process is refused DAEMON-403
	// before this handler; no loop ever starts it.
	mux.HandleFunc("POST /messages/{id}/analyze", d.analyzeRoute)
	// The explicit, read-only message-to-prompt task: body
	// {caller_id}, one task in the task lane like analyze, blocking until it
	// is terminal and answering {prompt, task_id, model, duration}. Same
	// reach as analyze: local UDS only, behind refuseStepOrigin (a step or
	// task process is refused DAEMON-403 before this handler).
	mux.HandleFunc("POST /messages/{id}/prompt", d.promptRoute)
	// pf watch: the per-WI snapshot and the machine's decision feed
	// (decisions.go,). GET /decisions is a read any local
	// peer may make; the dismiss POST sits behind refuseStepOrigin, so a
	// step or task process tree is refused DAEMON-403.
	mux.HandleFunc("GET /jobs/{wi}/watch", d.watchSnapshotRoute)
	mux.HandleFunc("GET /jobs/{wi}/artifact", d.artifactRoute)
	mux.HandleFunc("GET /decisions", d.decisionsRoute)
	mux.HandleFunc("POST /decisions/dismiss", d.decisionsDismissRoute)
	// `pf step`, the step's one entry point (stepcall.go, workflow
	// contract 4): every call recorded on the capsule's call ledger first
	mux.HandleFunc("POST "+stepCallPath, d.stepCallRoute)
	// the one-shot task agent (tasks.go,): the spawn/cancel
	// POSTs sit behind refuseStepOrigin, so a step OR task process tree is
	// refused DAEMON-403; the two /task/memories GETs serve a task agent
	// only and scope the read to its own project.
	mux.HandleFunc("POST /task/spawn", d.taskSpawnRoute)
	mux.HandleFunc("GET /task/list", d.taskListRoute)
	mux.HandleFunc("GET /task/show/{id}", d.taskShowRoute)
	mux.HandleFunc("POST /task/cancel/{id}", d.taskCancelRoute)
	mux.HandleFunc("GET "+taskMemSearchPath, d.taskSearchRoute)
	mux.HandleFunc("GET "+taskMemPrefix+"{id}", d.taskMemoryRoute)
	return mux
}

// decodeBody reads one bounded JSON body; an empty body is not an error
// (callers that tolerate {} keep tolerating it).
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, api.ErrDaemonBadInput, "body read: "+err.Error())
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, v); err != nil {
		writeErr(w, api.ErrDaemonBadInput, "body: "+err.Error())
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// claim: the one start route — `pf submit` (queued) and `pf resume` of a
// work item parked blocked (started now).

// submitRoute serves POST /jobs/{wi}/claim. A submit's entry lands on
// disk, then the dispatcher is run at once (priority-desc against the
// concurrency cap, as on its tick): an entry it takes is answered with
// its claim's verdict state "claimed" once aihub
// answered the claim (never after the capsule clone), or aihub's own
// typed refusal with nothing left queued; an entry left waiting for a
// slot is answered state "queued" with the reason, and a later refusal
// is the submit_rejected event.
//
// "resume": true is the claim half of `pf resume` on a workflow parked
// blocked (polyforge#116). The CLI has already landed the hub's
// resume-blocked door with the human's own credential (its
// owner/maintainer/admin check is about the human, never the daemon's
// machine key), so the work item is queued again; this route claims it
// synchronously and the claim/start path's owner move (bridge.Start →
// driver.ResumeCurrent) re-dispatches the frozen step in place. The
// claim's verdict is answered directly; only an unreachable hub
// (DAEMON-503) falls back to the queue, so the work item is not stranded
// queued at the hub.
func (d *Daemon) submitRoute() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wi := r.PathValue("wi")
		var body struct {
			Priority string     `json:"priority"`
			Models   []string   `json:"models,omitempty"` // manual candidate list (pf submit --models)
			Origin   *reqOrigin `json:"origin,omitempty"`
			Resume   bool       `json:"resume,omitempty"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Models != nil {
			m, merr := workspace.NormalizeModels(body.Models)
			if merr != nil {
				writeErr(w, api.ErrDaemonBadInput, "models: "+merr.Error())
				return
			}
			body.Models = m
		}
		if err := d.modes.AdmitSubmit(); err != nil {
			writeAdmissionErr(w, err)
			return
		}
		if err := d.drainRefusal(); err != nil && !body.Resume {
			writeErrOf(w, err)
			return
		}
		if err := d.execDisabledRefusal(); err != nil {
			writeErrOf(w, err)
			return
		}
		// Canonical id (F2): the queue, the dispatcher and the runtime
		// bookkeeping all key by the canonical id the claim resolves to;
		// a slug spelling would leave the queue's slot marks unable to
		// reconcile against the runtime's canonical job keys. The
		// operator's spelling is registered as an alias so later route
		// calls by it resolve without another aihub round-trip. An
		// unresolvable spelling passes through unchanged — the claim
		// path resolves (or refuses) server-side, exactly as before.
		canonical, resolved := d.resolveWI(wi)
		if canonical == "" {
			canonical = wi
		}
		d.rt.NoteAlias(wi, canonical)
		// The live check is the runtime's own job set (the queue's slot
		// marks are accounting, reconciled a tick later); a pending
		// duplicate is refused on the queue.
		if ji, live := d.rt.Info(canonical); live {
			// Finding 2 (v1.5 review): a RETAINED paused job is not an active
			// run — a submit over it flows to the hub's own gate. A plain
			// claim answers ALREADY_HELD there while the paused session still
			// holds the work item; once an admin force-ended it (WI queued)
			// the claim replaces the paused record and proceeds.
			if ji.State != runtime.StatePaused {
				writeErr(w, api.ErrDaemonConflict, "work item is already active on this daemon")
				return
			}
		}
		if d.q.IsPending(canonical) {
			writeErr(w, api.ErrDaemonConflict, "work item is already queued on this daemon")
			return
		}
		// Pre-claim placement + workspace-yaml check, synchronous on the
		// submit face: an unmapped project (DAEMON-412) or an invalid
		// workspace yaml (DAEMON-414) reaches the operator's CLI BEFORE
		// the job is enqueued and before any capsule or server-side claim
		// state exists. The runtime's own pre-claim check is the
		// backstop. A failed probe refuses nothing here (the submit is
		// durable-queued; the dispatcher retries the probe at Start). A
		// resume skips it: its Start answers the same refusals directly.
		// The record the resolution read serves as the probe.
		var probe *api.WorkItem
		if !body.Resume {
			probe = resolved
			if probe == nil {
				if p, err := d.hub.GetWorkItem(canonical); err == nil {
					probe = p
				}
			}
		}
		if probe != nil && probe.ProjectName != "" {
			if _, lerr := d.rt.LoadWorkspace(probe.ProjectName); lerr != nil {
				writeErrOf(w, lerr)
				return
			}
			// polyforge#175: the work item's installed bundle must load
			// (every candidate `<harness>:<model>`) — refused here so
			// `pf submit` sees it directly
			if probe.BundleName != nil {
				if berr := d.rt.CheckBundle(canonical, probe.ProjectName, *probe.BundleName); berr != nil {
					writeErrOf(w, berr)
					return
				}
			}
		}
		// Past the gate (probe resolved), the answer
		// carries the claim's placement — machine id + capsule landing
		// path — and every repo clone missing from the mapped workspace
		// (warn-only: the submit never blocks on it). A probe that did not
		// resolve answers no placement: nothing is invented.
		var placement *submitPlacement
		if probe != nil {
			placement = d.placementFor(probe)
		}
		if body.Resume {
			// The daemon's run context, NOT the HTTP request's: the claim's
			// ctx is the driver's parent (bridge.Start derives from it) — a
			// request-scoped ctx would cancel the driver mid-dispatch the
			// moment the CLI hangs up on this answer.
			origin := body.Origin.origin()
			err := d.rt.Start(d.runCtx, canonical, runtime.StartOpts{Origin: &origin})
			if err == nil {
				writeJSON(w, http.StatusOK, resumeBlockedResponse{Resumed: true, WorkItemID: canonical})
				return
			}
			var ae *api.Error
			if !errors.As(err, &ae) || ae.Code != api.ErrDaemonHubUnavailable {
				if errors.Is(err, runtime.ErrClaimClosed) {
					// a pf wrap/pause landed while this claim was materializing
					writeErr(w, api.ErrDaemonConflict, err.Error())
					return
				}
				writeErrOf(w, err)
				return
			}
			fmt.Fprintf(os.Stderr, "daemon: resume %s deferred, queued for the claim: %v\n", canonical, err)
		}
		// `accepted` (or a deferred resume) is answered only once the
		// entry is on disk (v2 §5). A new submit re-checks the drain under
		// drainGate together with its enqueue: a rotation that became
		// pending during the checks above refuses it, or the switch sees
		// the admitting reservation (then the entry) and waits. The
		// persist runs outside the gate.
		// The submit's waiter is armed before its entry can be taken.
		var sw *submitWait
		if !body.Resume {
			if sw = d.armSubmitWait(canonical); sw == nil {
				writeErr(w, api.ErrDaemonConflict, "work item is already queued on this daemon")
				return
			}
		}
		d.drainGate.Lock()
		if !body.Resume {
			if err := d.drainRefusal(); err != nil {
				d.drainGate.Unlock()
				d.dropSubmitWait(canonical, sw)
				writeErrOf(w, err)
				return
			}
		}
		d.admitting.Add(1)
		d.drainGate.Unlock()
		err := d.q.Enqueue(qEntry{WorkItemID: canonical, Priority: body.Priority, Models: body.Models, Origin: body.Origin.origin()})
		d.admitting.Add(-1)
		if err != nil {
			d.dropSubmitWait(canonical, sw)
			writeErr(w, api.ErrDaemonInternal, "submit queue could not be persisted: "+err.Error())
			return
		}
		if body.Resume {
			writeJSON(w, http.StatusOK, resumeBlockedResponse{Resumed: true, WorkItemID: canonical, Deferred: true})
			return
		}
		d.dispatch()
		if d.q.IsPending(canonical) && d.dropSubmitWait(canonical, sw) {
			writeJSON(w, http.StatusOK, queuedSubmit(canonical, placement, "waiting for a free execution slot"))
			return
		}
		var outcome error
		select {
		case outcome = <-sw.ch:
		case <-r.Context().Done():
			// a later refusal is the submit_rejected event
			d.dropSubmitWait(canonical, sw)
			sw.abandon()
			return
		}
		var ae *api.Error
		switch {
		case outcome == nil:
			ans := submitResponse{Accepted: true, WorkItemID: canonical, State: cli.SubmitStateClaimed}
			if placement != nil {
				ans.MachineID, ans.CapsulePath = placement.MachineID, placement.CapsulePath
				ans.MissingRepos = placement.MissingRepos
			}
			writeJSON(w, http.StatusOK, ans)
		case errors.As(outcome, &ae) && ae.Code == api.ErrDaemonHubUnavailable:
			writeJSON(w, http.StatusOK, queuedSubmit(canonical, placement, "aihub could not be reached; the claim is retried with backoff"))
		case runtime.IsClaimDeferred(outcome):
			writeJSON(w, http.StatusOK, queuedSubmit(canonical, placement, "the daemon is shutting down; the claim replays at its next start"))
		case errors.Is(outcome, runtime.ErrClaimClosed):
			// a pf wrap/pause landed while this claim was materializing
			writeErr(w, api.ErrDaemonConflict, outcome.Error())
		default:
			writeErrOf(w, outcome)
		}
	}
}

// queuedSubmit is the answer of a submit whose entry waits on the queue:
// nothing is claimed, so no placement is stated; the missing-clone
// warning still rides it.
func queuedSubmit(wi string, placement *submitPlacement, reason string) submitResponse {
	ans := submitResponse{Accepted: true, WorkItemID: wi, State: cli.SubmitStateQueued, QueuedReason: reason}
	if placement != nil {
		ans.MissingRepos = placement.MissingRepos
	}
	return ans
}

// ---------------------------------------------------------------------------
// resume-paused: the live-holder continuation (wi_seWTgHeKtaa8).

// resumePausedRoute serves POST /jobs/{id}/resume-paused — the
// live-holder half of the operator's `pf resume` on a work item this
// daemon holds PAUSED. The CLI has already PATCHed clean_pause=false at
// the hub (the resume's hub-side effect). The startup reconciliation
// (runtime/restart.go) runs exactly once per daemon process, so without
// this route a LIVE holder would never re-observe the cleared flag and a
// paused work item would sit parked until its holder restarted. This
// route runs the same reconciliation for the ONE retained paused record
// NOW: verify the session still holds the WI (paused, clean_pause
// cleared), re-attach it (SessionAttach) and continue the current step
// IN PLACE through the recovery engine — the exact semantics attaching
// after a restart has, including re-posing a pending unanswered question
// and waiting for the human answer before any dispatch.
//
// The daemon's run context, NOT the HTTP request's (see
// submitRoute): the recovery continuation must survive the CLI
// hanging up on this answer.
func (d *Daemon) resumePausedRoute(w http.ResponseWriter, r *http.Request) {
	wi := r.PathValue("wi")
	var body struct {
		Origin *reqOrigin `json:"origin,omitempty"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := d.modes.AdmitSubmit(); err != nil {
		writeAdmissionErr(w, err)
		return
	}
	if err := d.execDisabledRefusal(); err != nil {
		writeErrOf(w, err) // a resume continues a run: refused before any re-attach
		return
	}
	canonical := d.canonWI(wi)
	d.rt.NoteAlias(wi, canonical)
	// the pause close released the job's observe monitor; the resumed
	// derivation's RTRecovered rebuilds it (observe builds a monitor for
	// a recovery nothing watches)
	outcome, err := d.rt.ResumePaused(d.runCtx, canonical)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resumePausedResponse{
		Resumed: outcome == runtime.ResumeAttached, WorkItemID: canonical, Outcome: outcome,
	})
}

// ---------------------------------------------------------------------------
// pause / wrap: the CLI close paths. A close is a message of the work
// item's runtime actor: of two racing closes the second gets the final
// answer once the first settled, and a close that finds the work item
// already terminal answers gracefully (the idempotency contract).

// parkedWIClose handles a close that finds no local job because the
// workflow is parked blocked on an honest incomplete result (polyforge#116):
// the bounded close dropped the job, so a wrap (success) or a `--fail` of
// a parked workflow has nothing to seal — refused with the owner-move
// copy: the blocked exits are continue (`pf resume`) and abandon
// (`pf cancel`). Returns true when it answered; a non-blocked work item
// falls through to the caller's terminalOf/404.
func (d *Daemon) parkedWIClose(w http.ResponseWriter, wi string) bool {
	item, err := d.hub.GetWorkItem(wi)
	if err != nil || item == nil || item.Status != api.StatusBlocked {
		return false
	}
	writeErr(w, api.ErrDaemonConflict,
		"the work item is parked blocked (an incomplete result, or a pause that landed while its claim was still materializing — see its claim_paused_ended_blocked event) — the owner's moves are `pf resume <id>` to continue or `pf cancel <id>` to abandon; there is no completed run to wrap or fail")
	return true
}

// closeJob runs one CLI-side close through Runtime.Stop.
func (d *Daemon) closeJob(w http.ResponseWriter, ctx context.Context, wi, reason string) {
	wi = d.canonWI(wi) // a slug spelling closes the canonical job (F2)
	ji, ok := d.rt.Info(wi)
	if !ok && !d.rt.Busy(wi) {
		d.noJobClose(w, wi)
		return
	}
	// (a start of the work item still on its claim's hub calls has no job
	// yet: the close queues behind it in the runtime and answers once it
	// settled.)
	// Finding 2 (v1.5 review): a repeat pause of a RETAINED paused job is
	// the graceful idempotency answer, not a conflict — the close already
	// happened and the record is exactly where the contract wants it.
	if reason == reasonPaused && ji.State == runtime.StatePaused {
		writeJSON(w, http.StatusOK, closeResponse{
			WorkItemID: wi, Slug: ji.Slug, Outcome: "already_paused", Status: string(ji.State),
		})
		return
	}
	// The agent's death in this close is expected: arm the monitor's
	// suppression BEFORE the teardown starts, so the exit signal can
	// never win the race against the RTFinished teardown and journal a
	// spurious needs_recovery for a healthy run (the runtime's SEALING
	// fence drops the OB message either way — the event log is the
	// observable lie this closes).
	d.obs.MarkClosing(wi)
	if err := d.rt.Stop(ctx, wi, reason); err != nil {
		graceful, ae := d.mapCloseErr(err)
		if graceful && !ok {
			// the operation it queued behind left no job here
			d.noJobClose(w, wi)
			return
		}
		if graceful {
			writeJSON(w, http.StatusOK, closeResponse{WorkItemID: wi, Slug: ji.Slug, Outcome: "already_terminal"})
			return
		}
		writeJSON(w, ae.HTTPStatus(), ae)
		return
	}
	writeJSON(w, http.StatusOK, closeResponse{
		WorkItemID: wi, Slug: ji.Slug, Outcome: "sealed", Reason: reason,
	})
}

// noJobClose answers a close that finds no local job: a work item parked
// blocked, one already terminal at the hub, or none held here (404).
func (d *Daemon) noJobClose(w http.ResponseWriter, wi string) {
	if d.parkedWIClose(w, wi) {
		return
	}
	if st, terr := d.terminalOf(wi); terr == nil {
		writeJSON(w, http.StatusOK, closeResponse{
			WorkItemID: wi, Outcome: "already_terminal", Status: st,
		})
		return
	}
	writeErr(w, api.ErrDaemonNotFound, "no local job for "+wi+" on this daemon")
}

// terminalOf looks the work item up at aihub and reports its status when
// terminal ("" + error otherwise).
func (d *Daemon) terminalOf(wi string) (string, error) {
	item, err := d.hub.GetWorkItem(wi)
	if err != nil {
		return "", err
	}
	if terminalStatus(item.Status) {
		return item.Status, nil
	}
	return "", errors.New("work item is " + item.Status + " elsewhere")
}

// reasonPaused is the pause close reason, passed through SessionEnd's
// status field: the hub marks the work item paused (the operator pause —
// clean_pause semantics live hub-side). Since the v1.5 review Finding 2
// the pause close RETAINS the runtime record (StatePaused): the session
// secret survives locally so the holder daemon's restart can attach
// once the operator clears clean_pause (see runtime.pauseOp).
const reasonPaused = protocol.FinishPaused

func (d *Daemon) pauseRoute(w http.ResponseWriter, r *http.Request) {
	d.closeJob(w, r.Context(), r.PathValue("wi"), reasonPaused)
}

func (d *Daemon) wrapRoute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	reason := protocol.FinishWrapped
	switch body.Reason {
	case "", "wrap":
	case "fail":
		reason = protocol.FinishFailed
	default:
		writeErr(w, api.ErrDaemonBadInput, "unknown wrap reason: "+body.Reason)
		return
	}
	d.closeJob(w, r.Context(), r.PathValue("wi"), reason)
}

// ---------------------------------------------------------------------------
// ops face: status / ops config (auto + max) / shutdown.

// statusJobs builds the jobs[] bookkeeping (with the running-slot count);
// the status route and the watch lane share it. The per-job needs_input
// projection (D6) and the pending retry backoff are read from each job's
// execution.json; the same loop carries the per-job liveness reading
// (step age + last real-work-signal age).
func (d *Daemon) statusJobs() ([]statusJob, int) {
	faces := d.execFaces(time.Now())
	activity := d.obs.Activities()
	jobs := make([]statusJob, 0, 8)
	// running slots: a retained PAUSED job (Finding 2) holds no agent and
	// no queue slot — the ops face must not count it as running work.
	running := 0
	for _, ji := range d.rt.List() {
		// a queued daemon-run verification holds no slot (occupiedSlots),
		// nor does a step waiting on the owner's reply to its `pf ask`
		if ji.State != runtime.StatePaused && (d.verifyQ == nil || !d.verifyQ.queuedFor(ji.WorkItem)) && !ji.AwaitingOwner {
			running++
		}
		sj := statusJob{
			WorkItemID: ji.WorkItem,
			Slug:       ji.Slug,
			Status:     string(ji.State),
			// the rendered identity is the PUBLIC session handle plus the
			// derivation counter (B2/B3) — never the secret.
			SpawnN:             ji.SpawnN,
			SessionFingerprint: ji.SessionFingerprint,
			AgentPID:           ji.AgentPID,
			Origin:             ji.Origin,
		}
		face := faces[ji.WorkItem]
		if face.RetryIn > 0 {
			// whole seconds, rounded up: a wait still owed never reads 0
			in := int64((face.RetryIn + time.Second - 1) / time.Second)
			sj.RetryInSec, sj.RetryCause = &in, face.RetryCause
		}
		if qid := face.QuestionID; qid != "" {
			sj.QuestionID = qid
			// the presentation rule: runtime says running but the monitor
			// holds a pending question → needs_input wins. Any other
			// runtime state (sealing, recovering) keeps its own name — a
			// close in progress outranks the question it may carry.
			if ji.State == runtime.StateRunning {
				sj.Status = protocol.JobNeedsInput
				sj.NeedsInput = true
			}
		}
		if d.verifyQ != nil {
			sj.VerifyQueue = d.verifyQ.statusFace(ji.WorkItem)
		}
		if act, ok := activity[ji.WorkItem]; ok && act.InDispatch {
			sj.Candidate = act.Candidate
			if !act.StepStartedAt.IsZero() {
				age := int64(time.Since(act.StepStartedAt) / time.Second)
				sj.StepAgeSec = &age
			}
			if !act.LastWorkAt.IsZero() {
				ago := int64(time.Since(act.LastWorkAt) / time.Second)
				sj.LastWorkAgoSec = &ago
			}
			if act.WaitCalls > 0 {
				calls, secs := int64(act.WaitCalls), act.WaitSeconds
				sj.WaitCalls, sj.WaitSeconds = &calls, &secs
			}
			sj.Context = act.Context
		}
		jobs = append(jobs, sj)
	}
	return jobs, running
}

func (d *Daemon) statusRoute(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.statusSnapshot(r.URL.Query().Get("harnesses") == "1"))
}

// statusSnapshot assembles this Cell's GET /status answer (the Host's
// aggregate starts from the default Cell's).
func (d *Daemon) statusSnapshot(withHarnesses bool) statusResponse {
	// D6: the per-job needs_input projection, derived from the observe
	// lane's monitors (keyed by the canonical id — the same key
	// rt.List() reports). F15c rides the same loop: the per-job progress
	// staleness for the LAST-PROGRESS column.
	jobs, running := d.statusJobs()
	var harnesses map[string]harnessStatus
	if withHarnesses {
		harnesses = d.harnessStatuses()
	}
	return statusResponse{
		Harnesses:       harnesses,
		MachineID:       d.cfg.MachineID,
		Version:         version.Version,
		DisplayName:     d.cfg.DisplayName,
		Auto:            d.modes.Auto(),
		Workspaces:      d.plan().Workspaces(),
		WorkflowsDir:    d.cfg.WorkflowsDir,
		ShuttingDown:    d.modes.ShuttingDown(),
		EnvWarnings:     d.cfg.EnvWarnings,
		EnvNames:        supervisor.EnvNames(os.Environ()),
		PiModelsEnvRefs: d.piModelsEnvRefs(),
		CommandPath:     d.commandPathNow(),
		StepPath:        d.stepPathNow(),
		Jobs:            jobs,
		Queue: queueStats{
			Pending:       d.q.LenPending(),
			Running:       running,
			MaxConcurrent: d.q.MaxConcurrent(),
			MaxConfigured: d.maxConfiguredSnapshot(),
		},
		ModelHealth:       d.modelHealthSummary(),
		Supervision:       supervisionStatus(d.live),
		Reload:            d.reloadStatusSnapshot(),
		MachineFace:       d.faceStatus(),
		Tasks:             d.taskSummary(),
		ExecutionDisabled: d.executionDisabled(),
	}
}

// configReloadRoute is POST /config/reload: re-read the daemon's own
// cli.toml and apply each key by its class. A failure applies nothing and
// answers a typed error; the daemon keeps running.
func (d *Daemon) configReloadRoute(w http.ResponseWriter, r *http.Request) {
	resp, err := d.reloadConfig()
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// configCheckRoute is POST /config/check: a proposed mapping is checked
// against the held jobs without writing anything (the CLI's pre-write
// preflight).
func (d *Daemon) configCheckRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mapping []cli.ConfigMappingEntry `json:"mapping"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := d.checkMappingProposal(req.Mapping); err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cli.ConfigCheckResponse{OK: true})
}

// maxConfiguredSnapshot answers the last applied/startup cap under the
// short-held reload bookkeeping lock (no I/O under it).
func (d *Daemon) maxConfiguredSnapshot() int {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	return d.maxConfigured
}

// reloadStatusSnapshot answers a copy of the reload bookkeeping.
func (d *Daemon) reloadStatusSnapshot() *cli.ReloadStatus {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	s := d.reloadStatus
	return &s
}

// modelsRoute serves GET /models: the current model-health profile. The
// payload holds only model/effort names, step ids, counts, rates,
// durations and the window — no paths, prompts, transcripts or
// credentials.
func (d *Daemon) modelsRoute(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	mh := d.modelHealthTracker()
	if mh == nil {
		writeJSON(w, http.StatusOK, modelhealth.Profile{
			Window: modelhealth.Window{Duration: observe.EventsRetention.String(),
				From: now.Add(-observe.EventsRetention).UTC().Format(time.RFC3339Nano),
				To:   now.UTC().Format(time.RFC3339Nano)},
			Cells: []modelhealth.Cell{}, Warnings: []modelhealth.Warning{},
		})
		return
	}
	writeJSON(w, http.StatusOK, mh.Profile(now))
}

// modelHealthSummary answers the additive GET /status object (nil when
// no tracker is assembled, so the field stays omitted).
func (d *Daemon) modelHealthSummary() *modelhealth.Summary {
	mh := d.modelHealthTracker()
	if mh == nil {
		return nil
	}
	s := mh.Summary(time.Now())
	return &s
}

// supervisionStatus reports the EFFECTIVE supervision bounds GET /status
// carries: an unset (or invalid) T reads its documented default, and an
// unset N reads the default while a negative N keeps its "disabled"
// meaning. This is what the running daemon judges with, not the raw file
// value.
func supervisionStatus(live *liveconfig.State) *cli.SupervisionStatus {
	stepTimeout := supervise.Defaults{StepTimeout: live.StepTimeout()}.Fill().StepTimeout
	stallWindow := live.StallWindow()
	if stallWindow == 0 {
		stallWindow = observe.DefaultDriverStall
	}
	return &cli.SupervisionStatus{
		StepTimeout: stepTimeout.String(),
		StallWindow: stallWindow.String(),
	}
}

// opsConfigRoute is POST /ops/config {"auto":bool?,"max":int?}: the one
// switch and the one number, at least one present. The switch persists;
// the cap lasts until the daemon restarts or a reload that changes
// max_concurrent_jobs takes over (last action wins).
func (d *Daemon) opsConfigRoute(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, api.ErrDaemonBadInput, "read body: "+err.Error())
		return
	}
	resp, err := d.applyOpsConfig(body)
	if err != nil {
		if errors.Is(err, errShuttingDown) {
			writeAdmissionErr(w, err)
			return
		}
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyOpsConfig applies one POST /ops/config body {"auto","max"} to this
// Cell (the Host applies a profile-less one to every Cell).
func (d *Daemon) applyOpsConfig(body []byte) (opsConfigResponse, error) {
	var req struct {
		Auto *bool `json:"auto"`
		Max  *int  `json:"max"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return opsConfigResponse{}, &api.Error{Code: api.ErrDaemonBadInput, Detail: "decode: " + err.Error()}
	}
	if req.Auto == nil && req.Max == nil {
		return opsConfigResponse{}, &api.Error{Code: api.ErrDaemonBadInput, Detail: "ops config requires auto and/or max"}
	}
	if req.Max != nil && *req.Max < 1 {
		return opsConfigResponse{}, &api.Error{Code: api.ErrDaemonBadInput, Detail: "max must be >= 1 (DAEMON-400)"}
	}
	if req.Auto != nil {
		if err := d.modes.SetAuto(*req.Auto); err != nil {
			return opsConfigResponse{}, err
		}
		if !*req.Auto {
			// v2-converge §5 (audit B3b): auto off = only explicit pf
			// submit — the not-yet-started origin=auto entries leave the
			// queue (and the persisted file a restart replays); explicit
			// (interactive) entries ride untouched.
			d.autoOffPurge()
		}
		d.persistModeQuietly()
	}
	if req.Max != nil {
		if err := d.q.SetMaxConcurrent(*req.Max); err != nil {
			return opsConfigResponse{}, &api.Error{Code: api.ErrDaemonBadInput, Detail: err.Error()}
		}
	}
	return opsConfigResponse{Auto: d.modes.Auto(), Max: d.q.MaxConcurrent()}, nil
}

func (d *Daemon) shutdownRoute(w http.ResponseWriter, r *http.Request) {
	// Attribute the shutdown to the dialing process (polyforge#110):
	// the UDS peer credential — the kernel's peer-credential record
	// (SO_PEERCRED on Linux, LOCAL_PEERCRED + LOCAL_PEERPID on darwin)
	// of who is on the other end, never anything from the request body
	// (caller-supplied; the CLI sends an empty object).
	cause := "route: POST /shutdown uds peer credential unavailable"
	if cred, ok := requestPeerCred(r.Context()); ok {
		cause = fmt.Sprintf("route: POST /shutdown uds peer pid=%d uid=%d", cred.pid, cred.uid)
	}
	// Answer immediately; the shutdown runs in the background and Run
	// returns when it finishes (reference.md §6.2).
	writeJSON(w, http.StatusOK, map[string]any{"shutting_down": true})
	go func() { _ = d.Shutdown(context.Background(), cause) }()
}

// refuseStepOrigin is the step-origin guard on the whole UDS face
// (v2-converge.md Appendix C(b)): the agent executing a work item
// must never drive the daemon. Every request that is not a read (GET/HEAD —
// status, watch snapshot, subscriptions stay open) is refused
// DAEMON-403 when its UDS peer — the kernel's peer-credential record
// (peercred_linux.go / peercred_darwin.go), never anything from the
// request body — belongs to a step process tree of this
// daemon (runtime.StepProcessOrigin: the PF_WI_ID/PF_DAEMON_ID markers on
// the peer, an ancestor or its process-group leader). A request whose peer
// credential is unavailable is refused too: the guard fails closed. An
// answer keeps its own #122 wording (answerFromStepProcess).
//
// a one-shot task agent's process tree (PF_TASK_ID +
// PF_DAEMON_ID) is refused EVERY non-GET/HEAD request — before the step
// allowlist is consulted and with none of its own, so a task cannot send
// /step/call or spawn another task. Its read face is GET only
// (/task/memories/*, watch snapshots, status).
func (d *Daemon) refuseStepOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if reason := d.taskOriginRefusal(r); reason != "" {
				writeErr(w, api.ErrDaemonForbidden, reason)
				return
			}
			// the one write a step may send: its `pf step` call, which the
			// route itself scopes to the caller's own step (stepcall.go)
			stepWrite := r.Method == http.MethodPost && r.URL.Path == stepCallPath
			if !stepWrite {
				if reason := d.stepOriginRefusal(r); reason != "" {
					writeErr(w, api.ErrDaemonForbidden, reason)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// taskOriginRefusal answers the refusal reason for a mutating request from
// a task agent's process tree ("" = not a task peer). It runs before the
// step guard: a task is not a step, so it never earns the step-write
// allowlist.
func (d *Daemon) taskOriginRefusal(r *http.Request) string {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return "" // the step guard refuses the unidentifiable peer (fail closed)
	}
	if id, isTask := runtime.TaskProcessOrigin(cred.pid, d.cfg.MachineID); isTask {
		return fmt.Sprintf("%s %s refused: the caller (pid %d) belongs to the task process tree of %s — a task agent reads memories and watch snapshots only and never drives the daemon; only a human session may", r.Method, r.URL.Path, cred.pid, id)
	}
	return ""
}

// stepOriginRefusal answers the refusal reason for a mutating request
// from a step process (or from an unidentifiable peer), "" when the peer
// may proceed.
func (d *Daemon) stepOriginRefusal(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/jobs/") && strings.HasSuffix(r.URL.Path, "/answer") {
		return d.answerFromStepProcess(r)
	}
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return fmt.Sprintf("%s %s refused: the caller's identity (UDS peer credential) is unavailable — only a human session may drive the daemon", r.Method, r.URL.Path)
	}
	if stepWI, isStep := runtime.StepProcessOrigin(cred.pid, d.cfg.MachineID); isStep {
		return fmt.Sprintf("%s %s refused: the caller (pid %d) belongs to the step process tree of work item %s — a step agent must never drive the daemon; only a human session may", r.Method, r.URL.Path, cred.pid, stepWI)
	}
	return ""
}

// answerFromStepProcess is the #122 wording of the guard for the answer
// route: a question is a human's to answer. It answers the refusal
// reason, "" when the peer may answer.
func (d *Daemon) answerFromStepProcess(r *http.Request) string {
	cred, ok := requestPeerCred(r.Context())
	if !ok {
		return "answer refused: the caller's identity (UDS peer credential) is unavailable — only a human session may answer"
	}
	if stepWI, isStep := runtime.StepProcessOrigin(cred.pid, d.cfg.MachineID); isStep {
		return fmt.Sprintf("answer refused: the caller (pid %d) belongs to the step process tree of work item %s — a step agent must never answer a question; only a human session may (polyforge#122)", cred.pid, stepWI)
	}
	return ""
}

// taskSummary is the additive tasks block of GET /status.
func (d *Daemon) taskSummary() *task.Summary {
	s := d.tasks.Summarize()
	return &s
}
