package daemon

// stepresult.go: the step agent's result delivery (polyforge#214). Inside
// a step `pf result submit` posts the envelope's payload here; the daemon
// resolves the calling step from its process tree (peerStepOrigin, the
// same fence as /step/memories), stamps the dispatch identity itself and
// lands the envelope atomically at the derivation's drop path — the one
// the driver collects from. The caller's cwd and any harness sandbox have
// no say in where the file lands.
//
//	POST /step/result   {outcome, output[, question_id, question][, command_evidence]}
//
// Reachability: step processes of this daemon only (stepCaller refuses
// every other caller); the step-origin guard lets this POST through
// beside /step/memories (routes.go). The route checks identity and shape
// only (driver.CheckEnvelopeShape); the deep checks stay in the driver's
// accept gate.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

const stepResultPath = "/step/result"

// stepResultRoute stamps and drops the calling step's result envelope.
func (d *Daemon) stepResultRoute(w http.ResponseWriter, r *http.Request) {
	j, ok := d.stepCaller(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeErr(w, api.ErrDaemonBadInput, "body read: "+err.Error())
		return
	}
	var res driver.StepResult
	if err := json.Unmarshal(raw, &res); err != nil {
		writeErr(w, api.ErrDaemonBadInput, "the result is not a valid JSON envelope: "+err.Error())
		return
	}
	switch {
	case res.WIID != "" && res.WIID != j.wi && res.WIID != j.slug:
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("the calling step belongs to work item %s (the result names %q)", j.wi, res.WIID))
		return
	case res.StepID != "" && res.StepID != j.step:
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("the calling step is %q (the result names step %q)", j.step, res.StepID))
		return
	case res.SpawnN != 0 && res.SpawnN != int64(j.attempt):
		writeErr(w, api.ErrDaemonForbidden, fmt.Sprintf("the calling step is spawn %d (the result names spawn_n %d)", j.attempt, res.SpawnN))
		return
	}
	if err := driver.CheckEnvelopeShape(&res); err != nil {
		writeErr(w, api.ErrDaemonBadInput, strings.TrimPrefix(err.Error(), "driver: "))
		return
	}
	res.WIID, res.StepID, res.SpawnN = j.wi, j.step, int64(j.attempt)
	res.SubmittedAt = time.Now().UTC()
	blob, err := json.Marshal(&res)
	if err != nil {
		writeErr(w, api.ErrDaemonInternal, "envelope encode: "+err.Error())
		return
	}
	path := driver.PendingResultPath(j.capsuleDir, res.SpawnN)
	if err := atomicfile.Write(path, blob, 0o644); err != nil {
		writeErr(w, api.ErrDaemonInternal, "the result was not delivered: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"wi_id": res.WIID, "step_id": res.StepID, "spawn_n": res.SpawnN, "outcome": res.Outcome, "path": path,
	})
}
