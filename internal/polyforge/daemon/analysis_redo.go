package daemon

// analysis_redo.go (polyforge#233): the owner's explicit re-scheduling of a
// ticket's pre-submit analysis. `pf file analysis redo <id>` asks THIS
// daemon to run the same analysis prompt `pf file create` schedules, on a
// ticket whose old analysis was written before the current format or
// against a stale base. The old section is copied verbatim into the
// ticket's `## 历次分析` area and the active section gains the pending
// redo marker in ONE content CAS, then a persisted redo request carries
// the round to dispatch (and across a daemon restart). The narrow write
// path then replaces the section exactly as it does for a create-time
// analysis, so the task's first write turns the pending request id into
// its own task id, and `pf file analysis confirm` keeps its one meaning.
//
// Eligibility (owner rulings Q2=A, Q5=A): a queued or blocked execution
// ticket on a lane that requires acceptance, with no local execution job,
// no running analysis task and no pending analysis request. A running,
// paused or terminal ticket is refused; so is a ticket with any of those
// live rounds. Every refusal leaves the ticket byte-identical.
//
// Reach: the UDS face only, behind refuseStepOrigin like every POST here —
// a step or task process tree is refused DAEMON-403. The strong guarantee
// is THIS daemon's (owner ruling Q4=A): the route serializes with the
// auto-pull wave on drainGate, and a stale queue snapshot read before the
// redo is never enqueued locally; another machine relies on the ticket's
// unconfirmed marker alone.

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// newAnalysisRedoRequestID mints one redo round's id. Its `redo_` prefix
// keeps it distinguishable from a task id in the one marker token slot.
func newAnalysisRedoRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return workflow.AnalysisRedoPrefix + hex.EncodeToString(b[:]), nil
}

// runningAnalysisTask answers the id of a non-terminal analysis task this
// daemon holds for wi (ok=false when there is none).
func (d *Daemon) runningAnalysisTask(wi string) (string, bool) {
	ts, err := d.tasks.List()
	if err != nil {
		return "", false
	}
	for _, t := range ts {
		if t.Spec.AnalysisOf == wi && !t.State.Terminal() {
			return t.ID, true
		}
	}
	return "", false
}

// analysisRedoRefusal answers why item cannot start a redo round (empty =
// eligible). The checks are deliberately those the ticket naming can prove
// locally: the hub carries status, not a session history, so this never
// claims "never claimed" — only "not claimed now" (owner ruling Q2=A).
func (d *Daemon) analysisRedoRefusal(item *api.WorkItem) (code, detail string) {
	name := api.DisplayName(item.Slug, item.ID)
	if item.BundleName == nil || strings.TrimSpace(*item.BundleName) == "" {
		return api.ErrDaemonConflict, "analysis redo: " + name + " is not an execution ticket (it carries no bundle) — only a queued or blocked execution ticket can have its analysis redone"
	}
	switch item.Status {
	case api.StatusQueued, api.StatusBlocked:
	default:
		return api.ErrDaemonConflict, "analysis redo: " + name + " is " + item.Status + " — only a queued or blocked (unclaimed) ticket can have its analysis redone; a claimed, running, paused or terminal ticket is refused"
	}
	if !d.laneOf(item.ProjectName, *item.BundleName).RequiresAcceptance() {
		return api.ErrDaemonConflict, "analysis redo: " + name + "'s lane does not require a pre-submit analysis — there is nothing to redo"
	}
	if d.q.IsRunning(item.ID) || d.q.IsPending(item.ID) {
		return api.ErrDaemonConflict, "analysis redo: " + name + " has a local execution job pending or running — wait for it to finish or cancel it, then redo"
	}
	if tk, ok := d.runningAnalysisTask(item.ID); ok {
		return api.ErrDaemonConflict, "analysis redo: " + name + " already has a running analysis task " + tk + " — wait for it, then confirm or redo again"
	}
	if d.analysisQ.has(item.ID) {
		return api.ErrDaemonConflict, "analysis redo: " + name + " already has a pending analysis request — wait for it to be dispatched or confirm the ticket"
	}
	return "", ""
}

// analysisRedoRoute serves POST /analysis/redo {work_item_id, project}.
// The request is persisted BEFORE the ticket CAS: a persist failure changes
// nothing, and a CAS failure drops the request again, so the ticket and the
// queue can never keep half a round. The route holds drainGate across the
// eligibility check, the registration and the CAS, which is the same gate
// the auto-pull wave takes before enqueueing: a wave that already fetched a
// pre-redo snapshot either enqueued before this request (and the local job
// check refuses the redo) or finds the request here and skips the ticket.
func (d *Daemon) analysisRedoRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkItemID string `json:"work_item_id"`
		Project    string `json:"project"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.WorkItemID) == "" {
		writeErr(w, api.ErrDaemonBadInput, "analysis redo: work_item_id is required")
		return
	}
	d.drainGate.Lock()
	defer d.drainGate.Unlock()
	item, err := d.hub.GetWorkItem(req.WorkItemID)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	if code, detail := d.analysisRedoRefusal(item); code != "" {
		writeErr(w, code, detail)
		return
	}
	requestID, err := newAnalysisRedoRequestID()
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "analysis redo: mint the request id: "+err.Error())
		return
	}
	entry := analysisEntry{
		WorkItemID:    item.ID,
		Project:       item.ProjectName,
		EnqueuedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		RedoRequestID: requestID,
	}
	if added, err := d.analysisQ.addRedo(entry); err != nil {
		writeErr(w, api.ErrDaemonInternal, "analysis redo: persist the request before touching the ticket: "+err.Error())
		return
	} else if !added {
		writeErr(w, api.ErrDaemonConflict, "analysis redo: "+api.DisplayName(item.Slug, item.ID)+" already has a pending analysis request — wait for it to be dispatched")
		return
	}
	content := ""
	if item.Content != nil {
		content = *item.Content
	}
	marked, err := workflow.PrepareAnalysisRedo(content, requestID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		d.analysisQ.dropRequest(item.ID, requestID)
		writeErr(w, api.ErrDaemonConflict, "analysis redo: "+err.Error())
		return
	}
	if err := d.hub.Do("PATCH", "/api/v1/work-items/"+item.ID,
		map[string]any{"content": marked, "expected_updated_at": item.UpdatedAt}, nil); err != nil {
		d.analysisQ.dropRequest(item.ID, requestID)
		writeErrOf(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "queued", "request_id": requestID})
}
