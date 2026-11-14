package daemon

// overrides.go: the step-overlay face (polyforge#25) — the per-work-item
// routes
//
//	GET  /jobs/{wi}/overrides
//	POST /jobs/{wi}/overrides
//
// The view answers one row per declared step (its status, the lane's
// skippable declaration, the overlay aspects, the effective model chain
// and its source); the write applies one change through the runtime's
// per-work-item actor. Reach: the write sits behind refuseStepOrigin like
// every POST (a step or task process tree is refused DAEMON-403); the
// view is a read any local peer may make. Stub (polyforge#25 u4).

import (
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
)

func (d *Daemon) overrideViewRoute(w http.ResponseWriter, r *http.Request) {
	writeErr(w, api.ErrDaemonInternal, "the step-override face is not implemented yet")
}

func (d *Daemon) overrideWriteRoute(w http.ResponseWriter, r *http.Request) {
	writeErr(w, api.ErrDaemonInternal, "the step-override face is not implemented yet")
}
