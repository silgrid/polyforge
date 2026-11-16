package daemon

// unclassified.go (polyforge#51): the Host-level list of the error texts
// that fell into the classification FALLBACK — the texts no built-in row
// and no Host-configured additional rule named, the ones the operator
// should turn into a transient_error_patterns / model_error_patterns
// entry. GET /ops/unclassified scans every serving Cell's events, so the
// report is the Host's, cross-Cell.

import (
	"net/http"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// scanUnclassifiedEvents reads one Cell's per-work-item event logs and
// answers the fallback-classified step_failed texts of the window,
// deduplicated by the raw text with a count each (nil map = none). A scan
// error degrades to whatever was read so one damaged file never hides the
// rest; the event log is news, never state (events.go).
func scanUnclassifiedEvents(dataDir string, window time.Duration, now time.Time) map[string]*cli.UnclassifiedItem {
	return nil
}

// unclassifiedRoute serves GET /ops/unclassified (Host-level, read-only).
// Reach: a human session only — a step or task agent's process tree is
// refused DAEMON-403 (agentRefusal, like GET /profiles): a step agent may
// read /status and /models but never this list (the texts are raw provider
// errors, and the route exists to serve the operator's rule curation).
func (h *Host) unclassifiedRoute(w http.ResponseWriter, r *http.Request) {
	if reason := agentRefusal(r, h.machineIDs()...); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	writeJSON(w, http.StatusOK, cli.UnclassifiedReport{WindowDays: int(cli.UnclassifiedWindow / (24 * time.Hour)), Items: []cli.UnclassifiedItem{}})
}
