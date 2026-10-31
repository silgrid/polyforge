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
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/supervisor"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
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
	// answer is delivered to the WI's live driver; with no live driver it
	// refuses. A step process never gets here (refuseStepOrigin).
	mux.HandleFunc("POST /jobs/{wi}/answer", func(w http.ResponseWriter, r *http.Request) {
		// Canonicalize once (slug → id via the runtime aliases, else aihub)
		// so the handle lookup, the pending-question lookup and the
		// liveness check all address the same job (polyforge#249).
		wi := d.canonWI(r.PathValue("wi"))
		if h := d.rt.DriverAnswerHandle(wi); h != nil {
			// driver path: decode the same wire shape, deliver to bridge
			var payload struct {
				QuestionID string `json:"question_id"`
				AnswerText string `json:"answer_text"`
				AnsweredBy string `json:"answered_by"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				writeErr(w, api.ErrDaemonBadInput, "decode: "+err.Error())
				return
			}
			if payload.AnsweredBy == "" {
				payload.AnsweredBy = r.Header.Get("X-PF-User")
			}
			// No question named: the daemon resolves the pending one from
			// the capsule's execution.json — accepted ONLY when exactly one
			// question is pending (audit B10). Zero or several is a typed
			// refusal pointing at --question: a guess across several open
			// questions would deliver the answer to an arbitrary one (the
			// steps map's iteration order) and is never honest.
			if payload.QuestionID == "" {
				ids := d.pendingQuestionIDs(wi)
				switch len(ids) {
				case 0:
					writeErr(w, api.ErrDaemonBadInput, "no pending question found for this work item")
					return
				case 1:
					payload.QuestionID = ids[0]
				default:
					writeErr(w, api.ErrDaemonBadInput,
						fmt.Sprintf("this work item has %d pending questions (%s) — answer a specific one with --question",
							len(ids), strings.Join(ids, ", ")))
					return
				}
			}
			// The reserved cancel answer ends the work item: the hub's
			// cancel gate decides it (probed with the daemon's own
			// credential, never the self-asserted answered_by) before
			// anything is written; a refusal leaves the question pending.
			if driver.IsCancelAnswer(payload.AnswerText) {
				if err := d.authorizeCancelAnswer(wi, payload.QuestionID, payload.AnswerText); err != nil {
					writeErrOf(w, err)
					return
				}
			}
			if err := h.Answer(payload.QuestionID, payload.AnswerText, payload.AnsweredBy); err != nil {
				writeErr(w, api.ErrDaemonConflict, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "question_id": payload.QuestionID})
			return
		}
		if _, live := d.rt.Info(wi); !live {
			writeErr(w, api.ErrDaemonNotFound, "work item is not running on this daemon")
			return
		}
		writeErr(w, api.ErrDaemonConflict,
			"no live driver for work item "+wi+": the workflow is between derivations — retry after the daemon re-derives it")
	})
	// revise-binding is the owner's correction of done e2e-author binding
	// commands. The route sits behind
	// refuseStepOrigin: a step (or task) process tree is refused
	// DAEMON-403 before this handler, so only a human session reaches the
	// driver's revision transition. One request carries the whole batch of
	// {index, command} corrections, applied in one step_revision write. A
	// running driver revises through its live handle; a retained paused
	// record revises through a record-only driver and the runtime commits
	// the corrected record to the chain.
	mux.HandleFunc("POST /jobs/{wi}/revise-binding", func(w http.ResponseWriter, r *http.Request) {
		wi := d.canonWI(r.PathValue("wi"))
		var payload cli.ReviseBindingPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeErr(w, api.ErrDaemonBadInput, "decode: "+err.Error())
			return
		}
		if payload.By == "" {
			payload.By = r.Header.Get("X-PF-User")
		}
		if payload.StepID == "" {
			writeErr(w, api.ErrDaemonBadInput, "revise-binding: step_id is required")
			return
		}
		if len(payload.Corrections) == 0 {
			writeErr(w, api.ErrDaemonBadInput, "revise-binding: corrections must name at least one binding")
			return
		}
		corrections := make([]capsule.BindingCorrection, 0, len(payload.Corrections))
		for _, c := range payload.Corrections {
			if c.Index < 0 {
				writeErr(w, api.ErrDaemonBadInput, "revise-binding: binding index must be zero or greater")
				return
			}
			if strings.TrimSpace(c.Command) == "" {
				writeErr(w, api.ErrDaemonBadInput, "revise-binding: command is required for every correction")
				return
			}
			corrections = append(corrections, capsule.BindingCorrection{Index: c.Index, Command: c.Command})
		}
		if err := d.rt.ReviseBinding(d.runCtx, wi, payload.StepID, corrections, payload.By, payload.Reason); err != nil {
			writeErrOf(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "revised", "work_item_id": wi, "step": payload.StepID, "corrections": len(corrections),
		})
	})
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
	mux.HandleFunc("POST /chat/init", d.chatInitRoute)
	// The explicit, read-only message analyzer. A POST
	// outside the step-write allowlist: the whole UDS face sits behind
	// refuseStepOrigin, so a step process is refused DAEMON-403 before
	// this handler; no loop ever starts it.
	mux.HandleFunc("POST /messages/{id}/analyze", d.analyzeRoute)
	// pf watch: the per-WI snapshot and the daemon-held chat subscriptions.
	mux.HandleFunc("GET /jobs/{wi}/watch", d.watchSnapshotRoute)
	mux.HandleFunc("GET /jobs/{wi}/artifact", d.artifactRoute)
	mux.HandleFunc("GET /chats/{id}/subscriptions", d.subscriptionsRoute)
	mux.HandleFunc("POST /chats/{id}/subscribe", d.subscribeRoute(true))
	mux.HandleFunc("POST /chats/{id}/unsubscribe", d.subscribeRoute(false))
	// `pf watch adopt <wi>`: an eligible same-workspace chat
	// takes over the WI's wake-target role. A POST outside the step-write
	// allowlist: the whole UDS face sits behind refuseStepOrigin, so a
	// step process tree is refused DAEMON-403 by construction — adopt
	// runs only on an explicit human request, never automatic.
	mux.HandleFunc("POST /chats/{id}/adopt", d.adoptRoute)
	mux.HandleFunc("POST /chats/{id}/ack", d.ackRoute)
	mux.HandleFunc("POST /chats/{id}/subscribe-owned", d.subscribeOwnedRoute)
	// the step agent's memory face (stepmem.go), scoped to the calling
	// step's own work item and project
	mux.HandleFunc("GET /step/memories/search", d.stepSearchRoute)
	mux.HandleFunc("GET /step/memories/{id}", d.stepMemoryRoute)
	mux.HandleFunc("POST "+stepMemoriesPath, d.stepRememberRoute)
	// the step agent's result delivery (stepresult.go), stamped with
	// and landed at the calling step's own dispatch identity
	mux.HandleFunc("POST "+stepResultPath, d.stepResultRoute)
	// the one-shot task agent (tasks.go,): the spawn/cancel
	// POSTs sit behind refuseStepOrigin, so a step OR task process tree is
	// refused DAEMON-403; the two /task/memories GETs serve a task agent
	// only and scope the read to its own project.
	mux.HandleFunc("POST /task/spawn", d.taskSpawnRoute)
	mux.HandleFunc("GET /task/list", d.taskListRoute)
	mux.HandleFunc("GET /task/show/{id}", d.taskShowRoute)
	mux.HandleFunc("POST /task/cancel/{id}", d.taskCancelRoute)
	mux.HandleFunc("GET /chats/{id}/task-notices", d.taskNoticesRoute)
	mux.HandleFunc("POST /chats/{id}/task-notices/ack", d.taskNoticesAckRoute)
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

// submitRoute serves POST /jobs/{wi}/claim. A submit is accepted once
// its entry is on disk; the dispatcher claims it within one tick,
// priority-desc against the concurrency cap.
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
				if berr := d.rt.CheckBundle(canonical, *probe.BundleName); berr != nil {
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
		cursor := d.lastSeq(canonical)
		if body.Resume {
			// The daemon's run context, NOT the HTTP request's: the claim's
			// ctx is the driver's parent (bridge.Start derives from it) — a
			// request-scoped ctx would cancel the driver mid-dispatch the
			// moment the CLI hangs up on this answer.
			origin := body.Origin.origin()
			err := d.rt.Start(d.runCtx, canonical, runtime.StartOpts{Origin: &origin})
			if err == nil {
				d.autoSubscribe(body.Origin, canonical, cursor)
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
		// entry is on disk (v2 §5).
		if err := d.q.Enqueue(qEntry{WorkItemID: canonical, Priority: body.Priority, Models: body.Models, Origin: body.Origin.origin()}); err != nil {
			writeErr(w, api.ErrDaemonInternal, "submit queue could not be persisted: "+err.Error())
			return
		}
		// pf watch: the submitting chat is subscribed (subscription =
		// adoption); events from here on are its to consume.
		d.autoSubscribe(body.Origin, canonical, cursor)
		if body.Resume {
			writeJSON(w, http.StatusOK, resumeBlockedResponse{Resumed: true, WorkItemID: canonical, Deferred: true})
			return
		}
		ans := submitResponse{Accepted: true, WorkItemID: canonical}
		if placement != nil {
			ans.MachineID, ans.CapsulePath = placement.MachineID, placement.CapsulePath
			ans.MissingRepos = placement.MissingRepos
		}
		writeJSON(w, http.StatusOK, ans)
	}
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
		if ji.State != runtime.StatePaused {
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
		}
		jobs = append(jobs, sj)
	}
	return jobs, running
}

func (d *Daemon) statusRoute(w http.ResponseWriter, r *http.Request) {
	// D6: the per-job needs_input projection, derived from the observe
	// lane's monitors (keyed by the canonical id — the same key
	// rt.List() reports). F15c rides the same loop: the per-job progress
	// staleness for the LAST-PROGRESS column.
	jobs, running := d.statusJobs()
	var harnesses map[string]harnessStatus
	if r.URL.Query().Get("harnesses") == "1" {
		harnesses = d.harnessStatuses()
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Harnesses:       harnesses,
		MachineID:       d.cfg.MachineID,
		DisplayName:     d.cfg.DisplayName,
		Auto:            d.modes.Auto(),
		Workspaces:      d.plan().Workspaces(),
		WorkflowsDir:    d.cfg.WorkflowsDir,
		ShuttingDown:    d.modes.ShuttingDown(),
		EnvWarnings:     d.cfg.EnvWarnings,
		EnvNames:        supervisor.EnvNames(os.Environ()),
		PiModelsEnvRefs: d.piModelsEnvRefs(),
		Jobs:            jobs,
		Queue: queueStats{
			Pending:       d.q.LenPending(),
			Running:       running,
			MaxConcurrent: d.q.MaxConcurrent(),
			MaxConfigured: d.maxConfiguredSnapshot(),
		},
		ModelHealth: d.modelHealthSummary(),
		Supervision: supervisionStatus(d.live),
		Reload:      d.reloadStatusSnapshot(),
		MachineFace: d.faceStatus(),
		Tasks:       d.taskSummary(),
	})
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
	var req struct {
		Auto *bool `json:"auto"`
		Max  *int  `json:"max"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Auto == nil && req.Max == nil {
		writeErr(w, api.ErrDaemonBadInput, "ops config requires auto and/or max")
		return
	}
	if req.Max != nil && *req.Max < 1 {
		writeErr(w, api.ErrDaemonBadInput, "max must be >= 1 (DAEMON-400)")
		return
	}
	if req.Auto != nil {
		if err := d.modes.SetAuto(*req.Auto); err != nil {
			writeAdmissionErr(w, err)
			return
		}
		if !*req.Auto {
			// v2-converge §5 (audit B3b): auto off = only explicit pf
			// submit — the not-yet-started origin=auto entries leave the
			// queue (and the persisted file a restart replays); explicit
			// (cli/chat) entries ride untouched.
			d.autoOffPurge()
		}
		d.persistModeQuietly()
	}
	if req.Max != nil {
		if err := d.q.SetMaxConcurrent(*req.Max); err != nil {
			writeErr(w, api.ErrDaemonBadInput, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, opsConfigResponse{Auto: d.modes.Auto(), Max: d.q.MaxConcurrent()})
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

// adoptRoute serves POST /chats/{id}/adopt {"wi"} — `pf watch adopt
// <wi>`: an eligible same-workspace chat takes over the WI's
// wake-target role. Eligibility, in order:
//
//  1. a locally held job record (every state whose decisions can still
//     fire — running / paused / needs_input): DAEMON-404 otherwise (a
//     sealed-and-dropped WI has no wake-target role left to take over);
//  2. the WI's project-mapping workspace (job record Project → the
//     [workspace] inverse): DAEMON-412 when unmapped;
//  3. the adopting chat's workspace tag (the tag store): untagged or
//     another workspace's tag is a DAEMON-400 refusal naming the WI's
//     workspace — never a silent subscribe degradation, no mutation;
//  4. the current wake target (the job record's origin chat, the same
//     field a resume rewrites): the adopter itself, while it still
//     holds its subscription to the WI, is an idempotent success with
//     no state change — a self re-adopt after the chat unsubscribed is
//     NOT one (a bare success answer would leave the chat unsubscribed
//     and muted, its retained record unconsumed and the scoped
//     broadcast firing): it runs the full surgery, which with
//     target == chat inherits the chat's own retained record,
//     re-creates the subscription and clears the mute; a target that
//     still holds a subscription to the WI is a DAEMON-409 refusal
//     naming its chat id; an absent origin ("" — a chat-less submit, a
//     missing origin record) is eligible — nothing to inherit but the
//     adopter's own record.
//
// The surgery runs chat-subs FIRST (one atomic write: the record union,
// the owner-routed re-route) and the job record's origin rewrite SECOND
// (decision 7's order): a crash between them leaves the pre-adopt
// scoped broadcast firing — every recipient is still an eligible
// same-workspace live subscribed chat, nothing is lost and nothing
// reaches an ineligible chat; a re-run of the adopt by the adopter
// completes the rewrite idempotently. The reverse order would leave
// the old owner-wide fanout permanently resurrected — strictly worse.
// Origin.Owner is untouched: owner routing keys on the owner, never
// the chat.
//
// The whole eligibility-and-surgery sequence runs serialized under
// adoptMu (see adopt): two concurrent adopts can never interleave their
// read-check-act sequences — the origin field would otherwise name one
// adopter while the re-pointed subscriptions name the other.
//
// Step reachability: a POST outside the step-write allowlist — the
// step-origin guard (refuseStepOrigin) refuses a step process tree with
// DAEMON-403 by construction, so a step agent can never adopt; only a
// human session's chat may (docs/reference/uds-routes.md).
func (d *Daemon) adoptRoute(w http.ResponseWriter, r *http.Request) {
	id, ok := chatID(w, r)
	if !ok {
		return
	}
	var body struct {
		WI string `json:"wi"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.WI) == "" {
		writeErr(w, api.ErrDaemonBadInput, "wi is required")
		return
	}
	ans, err := d.adopt(id, d.canonWI(strings.TrimSpace(body.WI)))
	if err != nil {
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// adopt is the adopt route's serialized half: the eligibility
// reads and both writes run under adoptMu, start to finish, so two
// concurrent adopts can never interleave (the route answers outside
// the lock — no I/O under it). The refusals are typed api errors the
// route surfaces verbatim (writeErrOf); the surgery's order and crash
// window are adoptRoute's (decision 7).
func (d *Daemon) adopt(id, wi string) (protocol.ChatAdoptAnswer, error) {
	d.adoptMu.Lock()
	defer d.adoptMu.Unlock()
	ji, live := d.rt.Info(wi)
	if !live {
		return protocol.ChatAdoptAnswer{}, &api.Error{Code: api.ErrDaemonNotFound,
			Detail: "work item is not running on this daemon"}
	}
	ws, err := d.plan().WorkspaceFor(ji.Project)
	if err != nil {
		return protocol.ChatAdoptAnswer{}, &api.Error{Code: api.ErrDaemonUnmappedProject, Detail: err.Error()}
	}
	if tag := d.subs.WorkspaceOf(id); tag != ws {
		return protocol.ChatAdoptAnswer{}, &api.Error{Code: api.ErrDaemonBadInput, Detail: fmt.Sprintf(
			"chat %s is tagged with workspace %q, not the work item's workspace %s — only a chat of the work item's workspace may adopt it (a chat is tagged by `pf chat init` from its session cwd; re-init the chat from that workspace)",
			id, tag, ws)}
	}
	target := ""
	if ji.Origin != nil {
		target = ji.Origin.ChatID
	}
	slug := d.slugOf(wi)
	if id == target && d.subs.HoldsSubscription(id, wi) {
		// a self re-adopt while the chat still holds its subscription:
		// idempotent success, no state change
		return protocol.ChatAdoptAnswer{ChatID: id, WI: wi, Slug: slug, Adopted: true}, nil
	}
	if target != "" && d.subs.HoldsSubscription(target, wi) {
		return protocol.ChatAdoptAnswer{}, &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(
			"the work item's wake target chat %s still holds a subscription to %s — it must unsubscribe before another chat may adopt",
			target, api.DisplayName(slug, wi))}
	}
	if err := d.subs.Adopt(id, wi, slug, target); err != nil {
		return protocol.ChatAdoptAnswer{}, err
	}
	if err := d.rt.AdoptOrigin(wi, id); err != nil {
		return protocol.ChatAdoptAnswer{}, err
	}
	return protocol.ChatAdoptAnswer{ChatID: id, WI: wi, Slug: slug, Adopted: true}, nil
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
// /step/result, /step/memories or spawn another task. Its read face is GET
// only (/task/memories/*, watch snapshots, status).
func (d *Daemon) refuseStepOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if reason := d.taskOriginRefusal(r); reason != "" {
				writeErr(w, api.ErrDaemonForbidden, reason)
				return
			}
			// the two writes a step may send: its memory and its result,
			// which the routes themselves scope to the caller's own step
			// (stepmem.go, stepresult.go)
			stepWrite := r.Method == http.MethodPost && (r.URL.Path == stepMemoriesPath || r.URL.Path == stepResultPath)
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

// pendingQuestionIDs answers the ids of ALL currently open pending
// questions in the capsule's execution.json (empty when none), sorted.
// The answer route accepts a no-id answer only when exactly one id comes
// back (audit B10).
func (d *Daemon) pendingQuestionIDs(wi string) []string {
	wid := d.rt.CanonicalID(wi)
	if wid == "" {
		wid = wi
	}
	ex, _ := d.loadExecution(wid, nil)
	if ex == nil {
		return nil
	}
	var ids []string
	for _, st := range ex.Steps {
		if st != nil && st.PendingQuestion != nil && st.PendingQuestion.ID != "" {
			ids = append(ids, st.PendingQuestion.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// taskSummary is the additive tasks block of GET /status.
func (d *Daemon) taskSummary() *task.Summary {
	s := d.tasks.Summarize()
	return &s
}
