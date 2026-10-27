package daemon

// progresslookup.go: the caller half of the cross-machine query face
// (workbench#11). The fixed decision order lives here:
//
//  1. local queue/job/capsule -> local snapshot (no remote call)
//  2. aihub cannot resolve the WI -> CORE-404
//  3. queued/terminal/paused -> local hub-data snapshot (no remote call)
//  4. running/blocked held by this machine without a local job -> local
//     registered-window snapshot
//  5. running/blocked, fresh holder != this machine -> signed 5 s query
//     and the holder's snapshot plus holder_machine
//  6. stale holder -> CORE-504 (never stale data)
//  7. deregistered holder -> CORE-410
//  8. holder without the capability -> CORE-501

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func (d *Daemon) wouldRemote(item *api.WorkItem, local protocol.WatchSnapshot) bool {
	if item == nil || local.Known || local.Queued {
		return false
	}
	if item.Status != api.StatusRunning && item.Status != api.StatusBlocked {
		return false
	}
	return d.faceClient() != nil && d.faceAvailable()
}

func (d *Daemon) maybeRemoteSnapshot(item *api.WorkItem, local protocol.WatchSnapshot) (protocol.WatchSnapshot, error) {
	if !d.wouldRemote(item, local) {
		return local, nil
	}
	client := d.faceClient()
	ans, err := client.ProgressQuery(item.ID, "")
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			switch ae.Code {
			case api.ErrCoreStaleHolder, api.ErrCoreGone, api.ErrCoreUnsupported, api.ErrCoreUpstream:
				return local, ae
			}
		}
		return local, err
	}
	if ans.Local {
		return local, nil // this machine is the holder but has no job yet
	}
	if len(ans.Snapshot) == 0 {
		return local, nil
	}
	var remote protocol.WatchSnapshot
	if err := json.Unmarshal(ans.Snapshot, &remote); err != nil {
		return local, &api.Error{Code: api.ErrDaemonInternal, Detail: "relayed snapshot is unreadable: " + err.Error()}
	}
	remote.HolderMachine = ans.HolderMachine
	return remote, nil
}

// artifactRoute serves GET /jobs/{wi}/artifact?path=<relpath>: the same
// gate and decision order as progress. A local hit answers from its own
// capsule first (no aihub dependency); a queued/terminal WI answers only
// the pointer; only a running/blocked WI held elsewhere is relayed.
func (d *Daemon) artifactRoute(w http.ResponseWriter, r *http.Request) {
	// The artifact face is never reachable from a step process.
	if reason := d.stepOriginRefusal(r); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	rel := r.URL.Query().Get("path")
	wi := r.PathValue("wi")
	canonical, item := d.resolveWI(wi)
	if canonical == "" {
		canonical = wi
	}
	// 1. local hit first: a local job or queue entry answers from its own
	// capsule without asking aihub and without a remote call.
	if d.holdsWI(canonical) || d.holdsWI(wi) {
		if c := d.rt.Resolve(canonical); c != "" {
			canonical = c
		}
		ans, err := d.readArtifact(canonical, rel)
		if err != nil {
			writeErrOf(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ans)
		return
	}
	// 2. aihub could not resolve the work item.
	if item == nil {
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("work item %s is unknown or unreadable at aihub", wi))
		return
	}
	// 3. queued/terminal: the pointer only, never content.
	if item.Status != api.StatusRunning && item.Status != api.StatusBlocked {
		writeJSON(w, http.StatusOK, api.ArtifactAnswer{
			WorkItem: canonical, Slug: item.Slug, Status: item.Status, Pointer: true, Path: rel,
		})
		return
	}
	// 4-8. running/blocked: the holder answers; typed refusals surface.
	client := d.faceClient()
	if client == nil || !d.faceAvailable() {
		writeErr(w, api.ErrCoreUnsupported, "this machine cannot relay a cross-machine artifact query")
		return
	}
	ans, err := client.ProgressQuery(item.ID, rel)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	if ans.Local {
		// This machine is the holder but holds no local job/capsule.
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("work item %s is not held by machine %s", canonical, d.cfg.MachineID))
		return
	}
	if ans.Artifact == nil {
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("artifact %q is unavailable from the holder", rel))
		return
	}
	writeJSON(w, http.StatusOK, ans.Artifact)
}
