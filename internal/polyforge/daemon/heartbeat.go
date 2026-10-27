package daemon

// heartbeat.go: the M3 daemon liveness face. Every cfg.HeartbeatEvery the
// daemon POSTs /api/v1/machines/heartbeat: machine identity, current mode
// and the WI ids it holds live. The server stamps last_heartbeat_at itself —
// the payload carries no client timestamp (one clock, no skew; readers judge
// against the server-stamped value). Failures are log-only with a bounded
// backoff: a liveness signal must never take the daemon down, must never
// block Run, and must never hot-loop against a dead aihub. Disabled entirely
// by cfg.HeartbeatEvery <= 0 (flag -heartbeat-every 0).

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/version"
)

// heartbeatBackoffCap bounds the failure backoff at 16× the base cadence
// (16m at the 60s default). The operator still sees every failure in the
// log; only the retry pressure is throttled.
const heartbeatBackoffCap = 16

// heartbeatLoop sends one heartbeat per tick until ctx is canceled. The
// interval doubles per consecutive failure (bounded above), resetting on
// the first success — so a dead aihub costs one log line per backoff
// window instead of one POST per second, and a healthy aihub costs
// exactly one POST per HeartbeatEvery.
func (d *Daemon) heartbeatLoop(ctx context.Context) {
	base := d.cfg.HeartbeatEvery
	if base <= 0 {
		return // disabled
	}
	delay := base
	// First heartbeat immediately: registration and the machine-face
	// handshake must not wait one cadence (the first interactive daemon
	// registers before a second machine can query it).
	timer := time.NewTimer(0)
	defer timer.Stop()
	failStreak := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := d.sendHeartbeat(); err != nil {
				failStreak++
				delay = nextHeartbeatDelay(base, failStreak)
				fmt.Fprintf(os.Stderr, "daemon: heartbeat failed (%d consecutive): %v; retry in %s\n",
					failStreak, err, delay)
			} else {
				if failStreak > 0 {
					fmt.Fprintf(os.Stderr, "daemon: heartbeat recovered after %d failures\n", failStreak)
				}
				failStreak = 0
				delay = base
			}
			timer.Reset(delay)
		}
	}
}

// sendHeartbeat posts one heartbeat with the daemon's current liveness
// projection. Errors are returned (log-only at the caller); a first
// registration is noted.
func (d *Daemon) sendHeartbeat() error {
	role, capability, force := d.faceHeartbeat()
	res, err := d.hub.Heartbeat(aihubclient.MachineHeartbeat{
		MachineID:      d.cfg.MachineID,
		RuntimeVersion: version.Version,
		Mode:           d.modes.HeartbeatMode(),
		LiveWIIDs:      liveWIIDs(d.rt.List()),
		PublicKey:      d.publicKeyB64,
		Launcher:       d.cfg.Launcher,
		Role:           role,
		Force:          force,
		Capability:     capability,
	})
	if err != nil {
		return err
	}
	if res.Registered {
		fmt.Fprintf(os.Stderr, "daemon: heartbeat registered machine %s (operator %s)\n", res.MachineID, res.OperatorID)
	}
	d.applyHeartbeat(res)
	return nil
}

// nextHeartbeatDelay is the pure backoff kernel: delay after a tick that
// failed failStreak consecutive times at base cadence. Exposed for the
// unit test — the loop's timer arithmetic must not need a flaky sleep
// ladder to be pinned.
func nextHeartbeatDelay(base time.Duration, failStreak int) time.Duration {
	return cappedBackoff(base, failStreak, heartbeatBackoffCap)
}

// cappedBackoff doubles base once per failure, never past capMult×base
// (exact for any cap, power of two or not).
func cappedBackoff(base time.Duration, failStreak, capMult int) time.Duration {
	limit := base * time.Duration(capMult)
	d := base
	for i := 0; i < failStreak && d < limit; i++ {
		d *= 2
	}
	if d > limit {
		d = limit
	}
	return d
}

// liveWIIDs projects the runtime's job table to the WI ids the daemon
// currently holds live: claiming (B-8: the claim landed, the clone is
// under way), running, recovering or sealing. Paused jobs are
// retained records whose session is closed server-side — not held live.
// Never nil: an idle daemon states an empty list (see the client type's
// live_wi_ids contract).
func liveWIIDs(jobs []runtime.JobInfo) []string {
	out := []string{}
	for _, j := range jobs {
		switch j.State {
		case runtime.StateClaiming, runtime.StateRunning, runtime.StateRecovering, runtime.StateSealing:
			out = append(out, j.WorkItem)
		}
	}
	sort.Strings(out)
	return out
}
