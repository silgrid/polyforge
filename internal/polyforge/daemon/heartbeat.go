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
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
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
				d.noteHeartbeat("", err)
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
	if why := d.recordIdentity(res.OperatorID); why != "" {
		d.noteIdentityConflict(why)
		return errors.New(why)
	}
	d.noteHeartbeat(res.OperatorID, nil)
	d.applyHeartbeat(res)
	return nil
}

// heartbeatState is the Cell's last heartbeat outcome, reported on
// GET /profiles: "ok" after a success, the error after a failure, "" before
// the first beat (or with the heartbeat disabled). operator is the user the
// hub answered for this Cell's credential (the identity domain's user).
type heartbeatState struct {
	mu       sync.Mutex
	last     string
	operator string
	// degraded is the last Hub failure (proxy transport error or a failed
	// heartbeat), "" after the next success.
	degraded string
	// identity is a Hub-user mismatch a heartbeat revealed: sticky (no
	// later success clears it), it degrades the Cell and the data-plane
	// proxy refuses business on it.
	identity string
}

// noteIdentityConflict isolates the Cell for a Hub-user mismatch.
func (d *Daemon) noteIdentityConflict(why string) {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	d.hb.identity = why
}

// identityConflict answers the Cell's Hub-user mismatch ("" = none).
func (d *Daemon) identityConflict() string {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	return d.hb.identity
}

// noteHeartbeat records one heartbeat outcome (memory only, no I/O).
func (d *Daemon) noteHeartbeat(operator string, err error) {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	if err != nil {
		d.hb.last = "error: " + err.Error()
		if hubFailure(err) {
			d.hb.degraded = err.Error()
		}
		return
	}
	d.hb.last = "ok"
	d.hb.degraded = ""
	if operator != "" {
		d.hb.operator = operator
	}
}

// heartbeatSnapshot answers the last outcome and the hub-answered operator.
func (d *Daemon) heartbeatSnapshot() (last, operator string) {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	return d.hb.last, d.hb.operator
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

// noteHubFailure records a failed Hub round trip on this Cell (the proxy's
// transport error): the Cell reads degraded until the next success.
func (d *Daemon) noteHubFailure(err error) {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	d.hb.degraded = err.Error()
}

// noteHubOK clears the degraded mark after a successful Hub round trip.
func (d *Daemon) noteHubOK() {
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	d.hb.degraded = ""
}

// degradedReason answers why the Cell reads degraded ("" = healthy).
func (d *Daemon) degradedReason() string {
	if why := d.missingCredential(); why != "" {
		return why
	}
	d.hb.mu.Lock()
	defer d.hb.mu.Unlock()
	if d.hb.identity != "" {
		return d.hb.identity
	}
	return d.hb.degraded
}

// hubFailure reports whether a Hub round trip's error degrades the Cell:
// the Hub unreachable (a transport error, a 5xx) or the Cell's credential
// refused (401). A typed refusal of one request (a machine-binding 403, a
// 409) says nothing about the Cell's connection and degrades nothing.
func hubFailure(err error) bool {
	var ae *api.Error
	if !errors.As(err, &ae) {
		return true
	}
	return ae.HTTPStatus() == http.StatusUnauthorized || ae.HTTPStatus() >= 500
}
