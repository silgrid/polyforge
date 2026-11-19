package daemon

// upgradedrain.go (polyforge#160, AC3, Q5 A): the Host-level upgrade
// drain. `pf ops drain --upgrade` sets it; from then on every Cell's
// drainRefusal refuses new work (submits, automatic claims, task spawns
// — the analysis queue rides the same refusal) with DAEMON-409 while
// running work goes on. The state lives in memory and ends with the
// daemon restart the upgrade itself performs; an upgrade called off
// leaves the refusal in place until the daemon is restarted (the
// documented recovery: `pf ops shutdown` then `pf daemon ensure`, or
// the container supervisor restarting it).
//
// The busy judgment ("work is still running, refuse the upgrade")
// shares each Cell's drainGate with every admission: the drain is set
// FIRST, so an admission either finds it (and is refused) or has
// already landed and is seen by the same-gate safe-idle check — the
// race window tools/install.sh:670 documents for its own check cannot
// open here.

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// upgradeDrain is the Host-wide upgrade drain state, shared by every
// Cell (the Host injects it into each Daemon's Config; nil on the
// unhosted test seam).
type upgradeDrain struct {
	mu  sync.Mutex
	set bool
}

// setUpgrade sets the drain (idempotent).
func (u *upgradeDrain) setUpgrade() {
	u.mu.Lock()
	u.set = true
	u.mu.Unlock()
}

// upgrading answers whether new work is refused for the upgrade.
func (u *upgradeDrain) upgrading() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.set
}

// refusal answers the DAEMON-409 every admission of new work gets while
// the drain is set (nil when it is not).
func (u *upgradeDrain) refusal() error {
	if !u.upgrading() {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonConflict, Detail: "this Host is draining for an upgrade (`pf ops drain --upgrade`): new submits, automatic claims and task spawns are refused and nothing is queued — running work goes on; the refusal ends with the daemon restart (an upgrade called off: `pf ops shutdown` then `pf daemon ensure`, or the container supervisor restarting it)"}
}

// busyCells answers which serving Cells hold work (jobs of any state,
// queued submits or in-flight admissions, held one-off tasks), each
// judged under its own drainGate so no admission can slip between the
// drain being set and this check: one that passed its pending check
// before the drain is seen here (its reservation or queue entry counts),
// one that comes after finds the drain and is refused.
func (h *Host) busyCells() []string {
	var busy []string
	for _, e := range h.sortedCells() {
		h.mu.Lock()
		d := e.d
		h.mu.Unlock()
		if d == nil {
			continue
		}
		d.drainGate.Lock()
		idle := d.safeIdle()
		d.drainGate.Unlock()
		if !idle {
			busy = append(busy, e.profile)
		}
	}
	return busy
}

// opsDrainRoute serves POST /ops/drain — the Host-level upgrade drain
// (`pf ops drain --upgrade [--force]`, human-only): it sets the drain
// first (new work is refused from here on), then judges the Host. An
// idle Host answers 200 (safe to upgrade); one still holding work
// answers 409 naming the busy Cells — with the drain KEPT, so the
// caller can wait and re-run — unless --force accepts the in-flight
// work (its sessions are interrupted by the shutdown that follows,
// crash-equals-shutdown, and resume after the restart).
func (h *Host) opsDrainRoute(w http.ResponseWriter, r *http.Request) {
	if p := r.Header.Get(protocol.ProfileHeader); p != "" {
		writeErr(w, api.ErrDaemonBadInput, fmt.Sprintf("drain is Host-level: it stops every Cell of this Host from taking new work and takes no profile (got %q) — run `pf ops drain --upgrade` alone", p))
		return
	}
	var req struct {
		Upgrade bool `json:"upgrade"`
		Force   bool `json:"force"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !req.Upgrade {
		writeErr(w, api.ErrDaemonBadInput, "ops drain requires --upgrade (`pf ops drain --upgrade [--force]`): the upgrade drain is the one drain it sets")
		return
	}
	h.drain.setUpgrade()
	busy := h.busyCells()
	if len(busy) > 0 && !req.Force {
		writeErr(w, api.ErrDaemonConflict, fmt.Sprintf("the upgrade drain is set, but Cell(s) %s still hold work (jobs of any state, queued submits or in-flight admissions) — new work is refused from now on; wait for them to end and re-run `pf ops drain --upgrade`, or re-run with --force to upgrade anyway (their sessions are interrupted and resume after the restart)", strings.Join(busy, ", ")))
		return
	}
	detail := "the upgrade drain is set and the Host is idle: no Cell holds work, new work is refused from now on — the refusal ends with the daemon restart; upgrade now"
	if len(busy) > 0 {
		detail = fmt.Sprintf("the upgrade drain is set and --force accepts the in-flight work of Cell(s) %s (their sessions are interrupted by the shutdown and resume after the restart); new work is refused from now on", strings.Join(busy, ", "))
	}
	writeJSON(w, http.StatusOK, map[string]any{"upgrading": true, "forced": req.Force, "detail": detail})
}
