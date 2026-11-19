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

// upgradeDrain is the Host-wide upgrade drain state, shared by every
// Cell (the Host injects it into each Daemon's Config; nil on the
// unhosted test seam).
type upgradeDrain struct{}

// setUpgrade sets the drain (idempotent).
func (u *upgradeDrain) setUpgrade() {}

// upgrading answers whether new work is refused for the upgrade.
func (u *upgradeDrain) upgrading() bool { return false }

// refusal answers the DAEMON-409 every admission of new work gets while
// the drain is set (nil when it is not).
func (u *upgradeDrain) refusal() error { return nil }
