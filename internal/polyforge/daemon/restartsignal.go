package daemon

// restartsignal.go: the restart-side events of v2-converge §5/§6 — the
// submit_rejected event and the daemon_restarted signal (the queue
// replay and its submit_dropped event live with the queue, modes.go).

import (
	"errors"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// announceRestart writes the durable daemon_restarted event into every WI
// the daemon recovered (v2 §6 bug 8).
func (d *Daemon) announceRestart() {
	if d.obs == nil {
		return
	}
	for _, ji := range d.rt.List() {
		body := map[string]any{"state": string(ji.State)}
		_ = d.obs.Events().Append(ji.WorkItem, observe.EvDaemonRestarted, body)
	}
}

func (d *Daemon) recordSubmitRejected(e qEntry, err error) {
	if d.obs == nil {
		return
	}
	body := map[string]any{
		"error":        err.Error(),
		"submitted_at": e.SubmittedAt.UTC().Format(time.RFC3339Nano),
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		body["code"] = ae.Code
	}
	if e.Origin.Kind != "" {
		body["origin"] = e.Origin
	}
	_ = d.obs.Events().Append(e.WorkItemID, observe.EvSubmitRejected, body)
}
