package daemon

// answer.go: POST /jobs/{wi}/answer — the owner's words to a work item's
// step (workflow contract v2 §5.2, the owner-message relay) and, since
// workflow contract 4 (design #483 §7), the wake of a
// parked one.
//
// A running work item: the live driver records the message and delivers it
// into the step's conversation (driver.OwnerMessage).
//
// A parked one — blocked (its driver parked it and the close dropped the
// job) or paused (the operator's pause retained it) — is woken by the
// message instead of refusing it. The wake is two requests of the CLI,
// with the owner's door between them (ruling R9: the daemon never reopens
// a work item at aihub on its own key):
//
//  1. without `wake`: the daemon classifies the park (driver.WakeOf) and
//     answers `{status: "parked", park, wake, step, spawn_n}` — nothing is
//     written. A park whose agent cannot start (no usable model, a failed
//     composition, a claim that never started its driver) is refused
//     DAEMON-409 WAKE_CANNOT_START naming why and `pf resume` / `pf cancel`
//     — a lane snapshot of another contract (a `contract` park, or a park
//     of the previous contract made before the upgrade) WAKE_CANNOT_START_
//     CONTRACT naming `pf cancel` alone — and the CLI stops there;
//  2. the CLI opens the door `pf resume` opens, with the caller's own key
//     (blocked: aihub's resume-blocked door; paused: clean_pause cleared);
//  3. with `wake: <park>`: the daemon records the message in the capsule's
//     asks.jsonl (the record first, as the relay does) with its
//     owner_message event, then claims the work item (blocked: the claim
//     whose start re-dispatches the parked step, driver.ResumeCurrent;
//     paused: the in-place resume of the retained session). The step's
//     next dispatch carries the undelivered message; a step that handed
//     off `blocked` resumes its session with the message alone.
//
// Reach: behind refuseStepOrigin like every POST (a step or task process
// tree is refused DAEMON-403); the owner's `pf answer` and Pi Web's job
// card (which runs `pf answer`) reach it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// The parks a message wakes (protocol.OwnerMessageAck.Park, the answer
// request's `wake`).
const (
	ParkBlocked = "blocked"
	ParkPaused  = "paused"
)

// The typed refusals of a wake (api.Error extra `reason`).
const (
	// WakeRefusedCannotStart: the park's agent cannot start (DAEMON-409):
	// `pf resume` once the cause is fixed, or `pf cancel`.
	WakeRefusedCannotStart = "WAKE_CANNOT_START"
	// WakeRefusedContract: the park's lane snapshot targets another
	// workflow contract (DAEMON-409): a resume parks it again, so `pf
	// cancel` (and file it again) is the one move.
	WakeRefusedContract = "WAKE_CANNOT_START_CONTRACT"
	// WakeRefusedParkChanged: the wake named a park the work item is no
	// longer in (DAEMON-409): `pf answer` again.
	WakeRefusedParkChanged = "WAKE_PARK_CHANGED"
)

// answerPayload is the answer route's body.
type answerPayload struct {
	AnswerText string `json:"answer_text"`
	AnsweredBy string `json:"answered_by"`
	// Wake names the park the caller opened the door of (step 3 above);
	// "" asks for the classification (step 1) or relays to a live step.
	Wake   string     `json:"wake,omitempty"`
	Origin *reqOrigin `json:"origin,omitempty"`
}

func (d *Daemon) answerRoute(w http.ResponseWriter, r *http.Request) {
	// Canonicalize once (slug → id via the runtime aliases, else aihub)
	// so the handle lookup and the liveness check address the same job
	// (polyforge#249).
	wi, item := d.resolveWI(r.PathValue("wi"))
	if wi == "" {
		wi = r.PathValue("wi")
	}
	var payload answerPayload
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeErr(w, api.ErrDaemonBadInput, "decode: "+err.Error())
		return
	}
	if payload.AnsweredBy == "" {
		payload.AnsweredBy = r.Header.Get("X-PF-User")
	}
	// the message continues a run: a disabled Cell refuses it
	if err := d.execDisabledRefusal(); err != nil {
		writeErrOf(w, err)
		return
	}
	ji, live := d.rt.Info(wi)
	if payload.Wake == "" && !(live && ji.State == runtime.StatePaused) {
		if h := d.rt.DriverAnswerHandle(wi); h != nil {
			// The owner-message relay (workflow contract v2 §5.2): the words
			// go to the current step's agent, whether it waits on an ask (a
			// reply) or runs (a message), and the daemon reads none of them.
			ack, err := h.OwnerMessage(r.Context(), payload.AnswerText, payload.AnsweredBy)
			switch {
			case err == nil:
				writeJSON(w, http.StatusOK, ack)
				return
			case errors.Is(err, driver.ErrInvalidOwnerMessage):
				writeErr(w, api.ErrDaemonBadInput, err.Error())
				return
			case errors.Is(err, driver.ErrParked):
				// the driver parked the workflow and the close is under way:
				// the park's wake below answers it
			case ack.At == "":
				writeErr(w, api.ErrDaemonConflict, err.Error()) // nothing recorded: no step agent to talk to
				return
			default:
				// recorded, not delivered to the live process: the next
				// dispatch carries it — answered as accepted with the refusal
				// beside it
				ack.Disposition, ack.Note = "resume", err.Error()
				writeJSON(w, http.StatusOK, ack)
				return
			}
		}
	}
	d.answerParked(w, wi, item, ji, live, payload)
}

// answerParked serves the answer to a work item without a live driver
// that takes it: the park's classification (no `wake`) or its wake.
func (d *Daemon) answerParked(w http.ResponseWriter, wi string, item *api.WorkItem, ji runtime.JobInfo, live bool, p answerPayload) {
	if err := driver.ValidateOwnerMessage(p.AnswerText); err != nil {
		writeErr(w, api.ErrDaemonBadInput, err.Error())
		return
	}
	var (
		park, dir string
		ex        *capsule.Execution
		wk        driver.Wake
	)
	switch {
	case live && ji.State == runtime.StatePaused:
		park, dir = ParkPaused, ji.CapsuleDir
		if ex = readExecutionFile(filepath.Join(dir, capsule.ExecutionJSON)); ex == nil {
			writeErr(w, api.ErrDaemonConflict, wi+" is paused before its workflow started: there is no step to talk to — `pf resume "+wi+"` continues it")
			return
		}
		wk = driver.Wake{Kind: driver.WakePaused, Step: ex.CurrentStep, Spawn: driver.ParkedDispatch(dir, ex)}
	case live:
		writeErr(w, api.ErrDaemonConflict,
			"no live driver for work item "+wi+": the workflow is between derivations, or its park is still closing — retry in a moment")
		return
	default:
		if item == nil {
			item, _ = d.hub.GetWorkItem(wi)
		}
		if item != nil && item.Status == api.StatusPaused {
			// paused at aihub, and this daemon holds no paused session of it
			// (a restart dropped it, or another machine paused it): there is
			// no scene to wake in place
			writeErr(w, api.ErrDaemonConflict, pausedWithoutSession(wi))
			return
		}
		ex, dir = d.loadExecution(wi, item)
		if ex == nil || dir == "" {
			if item != nil && item.Status == api.StatusBlocked {
				d.refuseCannotStart(w, wi, driver.Wake{Kind: driver.WakeCannotStart,
					Why: "it was parked blocked before its workflow ran here (this daemon holds no execution record of it)"})
				return
			}
			writeErr(w, api.ErrDaemonNotFound, "work item is not running on this daemon")
			return
		}
		if ex.WorkflowStatus != capsule.WorkflowStatusBlocked {
			if ex.WorkflowStatus != capsule.WorkflowStatusRunning {
				writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("driver: the workflow is %s; no step agent to talk to", ex.WorkflowStatus))
				return
			}
			if item != nil && item.Status == api.StatusBlocked {
				d.refuseCannotStart(w, wi, driver.Wake{Kind: driver.WakeCannotStart,
					Why: "it was parked blocked at aihub while its workflow had not parked (a claim or recovery that never started its driver)"})
				return
			}
			writeErr(w, api.ErrDaemonConflict,
				"no live driver for work item "+wi+": the workflow is between derivations — retry after the daemon re-derives it")
			return
		}
		park, wk = ParkBlocked, driver.WakeOf(dir, ex)
	}
	if wk.Kind == driver.WakeCannotStart {
		d.refuseCannotStart(w, wi, wk)
		return
	}
	// the wake ends in a claim: a machine that admits none refuses it
	// before the caller opens the door
	if err := d.modes.AdmitSubmit(); err != nil {
		writeAdmissionErr(w, err)
		return
	}
	if p.Wake == "" {
		writeJSON(w, http.StatusOK, protocol.OwnerMessageAck{
			Status: "parked", Park: park, Wake: string(wk.Kind), Step: wk.Step, SpawnN: wk.Spawn,
		})
		return
	}
	if p.Wake != park {
		writeTypedRefusal(w, api.ErrDaemonConflict, WakeRefusedParkChanged,
			fmt.Sprintf("%s is %s now, not %s: nothing was recorded — run pf answer again", wi, park, p.Wake))
		return
	}
	// The record first, then the wake: a paused work item keeps its scene,
	// so the line lands in it before the in-place resume; a blocked one's
	// claim starts from a fresh clone, so its line rides the claim and lands
	// in the claimed scene before the engine starts (StartOpts.OwnerMessage).
	at := time.Now().UTC().Format(time.RFC3339Nano)
	line, err := driver.OwnerMessageLine(dir, wk.Step, wk.Spawn, p.AnswerText, p.AnsweredBy, at)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, err.Error())
		return
	}
	origin := p.Origin.origin()
	if werr := d.wake(wi, park, dir, line, origin); werr != nil {
		writeErrOf(w, werr)
		return
	}
	body := driver.OwnerMessageEvent(line)
	body["park"], body["wake"] = park, string(wk.Kind)
	d.tapRuntimeEvent(wi, driver.EvOwnerMessage, body)
	writeJSON(w, http.StatusOK, protocol.OwnerMessageAck{Status: "accepted", At: line.At, Kind: line.Kind, AskAt: line.AskAt,
		Step: line.Step, SpawnN: line.Spawn, Disposition: dispositionWake, Park: park, Wake: string(wk.Kind)})
}

// pausedWithoutSession is the refusal of a message to a work item paused
// at aihub that this daemon holds no paused session of: nothing is woken
// and nothing recorded; `pf resume` continues it and the message follows.
func pausedWithoutSession(wi string) string {
	return fmt.Sprintf("%s is paused, and this daemon holds no paused session of it (a daemon restart dropped it, or another machine paused it), so a message cannot wake it in place: nothing was recorded — `pf resume %s` continues it, then send the message", wi, wi)
}

// dispositionWake is the ack's disposition of a message that woke a park.
const dispositionWake = "wake"

// refuseCannotStart answers a message to a park whose agent cannot start.
func (d *Daemon) refuseCannotStart(w http.ResponseWriter, wi string, wk driver.Wake) {
	reason := WakeRefusedCannotStart
	if driver.ContractBlocked(wk.Why) {
		reason = WakeRefusedContract
	}
	writeTypedRefusal(w, api.ErrDaemonConflict, reason, driver.CannotStartRefusal(wi, wk))
}

// wake records the owner's line and continues the parked work item once
// its door is open: a paused one's line lands in its retained scene, then
// the session resumes in place (resumePausedRoute's continuation); a
// blocked one is claimed with the line (submitRoute's resume claim, whose
// start re-dispatches the parked step). The daemon's run context, never
// the request's: the driver must outlive the answer. An error means the
// message is not recorded — except a paused session that did not resume,
// whose line stays in the scene for the next resume (the error says so).
func (d *Daemon) wake(wi, park, dir string, line capsule.AskLine, origin runtime.Origin) error {
	if park == ParkPaused {
		if err := capsule.AppendAsk(dir, line); err != nil {
			return &api.Error{Code: api.ErrDaemonInternal, Detail: "the owner message was not recorded (nothing woken): " + err.Error()}
		}
		outcome, err := d.rt.ResumePaused(d.runCtx, wi)
		if err != nil {
			return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("the message is recorded, but the paused session did not resume (%v): `pf resume %s` continues it with the message", err, wi)}
		}
		if outcome != runtime.ResumeAttached {
			return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("the message is recorded, but the paused session's resume answered %s: `pf resume %s` continues it with the message", outcome, wi)}
		}
		return nil
	}
	err := d.rt.Start(d.runCtx, wi, runtime.StartOpts{Origin: &origin, OwnerMessage: &line})
	if err == nil {
		return nil
	}
	detail := err.Error()
	if errors.Is(err, runtime.ErrClaimClosed) {
		detail = "a pf wrap/pause landed while the claim was materializing: " + detail
	}
	return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("the claim that wakes %s did not start (%s), so the message is not recorded; its door is open — `pf answer` again, or `pf resume %s`", wi, detail, wi)}
}

// writeTypedRefusal writes an api.Error whose extra carries the typed
// reason (the CORE-409 NOT_BLOCKED convention).
func writeTypedRefusal(w http.ResponseWriter, code, reason, detail string) {
	extra, _ := json.Marshal(map[string]string{"reason": reason})
	ae := api.Error{Code: code, Detail: detail, Extra: extra}
	writeJSON(w, ae.HTTPStatus(), ae)
}
