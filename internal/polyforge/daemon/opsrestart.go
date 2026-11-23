package daemon

// opsrestart.go (polyforge#190, 84-inplace-upgrade): the
// restart-for-an-upgrade's trigger — the one half #175 left to this
// ticket. The supervisor already relaunches a daemon that exits 75 AT
// ONCE, on the components volume's new current (polyforge#175); what
// was missing is the way the upgrade orchestration asks a RUNNING
// daemon for that end. POST /ops/restart is it: Host-level like the
// drain, answering at once, running the same graceful shutdown
// POST /shutdown runs — only the exit differs, and the supervisor's
// relaunch turns it into the version switch's second half.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// errRestartForUpgrade is Run's ending after a restart ask: the
// shutdown completed and the process should exit
// supervisor.UpgradeRestartExit (75), which main maps.
var errRestartForUpgrade = errors.New("restarting for an upgrade: the components volume's current was switched")

// opsRestartRoute serves POST /ops/restart — the restart-for-an-upgrade
// ask (polyforge#190): the upgrade orchestration (and a cancelled one)
// calls it after the components volume's switch. It is Host-level like
// the drain, answers at once, and the shutdown it starts is the same
// graceful one /shutdown runs — only the exit differs.
func (h *Host) opsRestartRoute(w http.ResponseWriter, r *http.Request) {
	if p := r.Header.Get(protocol.ProfileHeader); p != "" {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("the restart-for-an-upgrade is Host-level: it restarts every Cell's daemon and takes no profile (got %q)", p))
		return
	}
	cause := "route: POST /ops/restart uds peer credential unavailable"
	if cred, ok := requestPeerCred(r.Context()); ok {
		cause = fmt.Sprintf("route: POST /ops/restart uds peer pid=%d uid=%d", cred.pid, cred.uid)
	}
	h.restartForUpgrade.Store(true)
	// Answer immediately; the shutdown runs in the background and Run
	// ends in errRestartForUpgrade once it finishes.
	writeJSON(w, http.StatusOK, map[string]any{"restarting_for_upgrade": true})
	go func() { _ = h.Shutdown(context.Background(), cause) }()
}

// exitOfRun maps Run's ending to the process exit status (main's own
// rule, stated once here): a restart-for-an-upgrade is 75, an
// operator-only failure 78, anything else 1, a clean end 0.
func exitOfRun(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errRestartForUpgrade):
		return ExitUpgradeRestart
	default:
		var no *needsOperatorError
		if errors.As(err, &no) {
			return ExitNeedsOperator
		}
		return 1
	}
}
