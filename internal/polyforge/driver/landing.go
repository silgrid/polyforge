package driver

// landing.go: the lane-declared landing-lock interval (polyforge#245,
// 245-lane). A lane whose workflow.yaml declares
// `lease_interval: {enter, exit}` runs the closed step range [enter, exit]
// under one server landing lease of its task repositories at the ticket's
// target branch: the driver takes the lease before the enter step first
// dispatches, holds it across the interval's dispatches (the daemon renews
// it), and releases it when the interval ends or the workflow leaves it.
//
// Reachability: the driver asks Config.IntervalLeases (the daemon's private
// manager); tokens never reach a step, the capsule, an event or a log. A
// step only checks the lock through `pf step lock-check`.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// IntervalLeases is the daemon's landing-lease manager (daemon/landing.go).
type IntervalLeases interface {
	// Hold takes the lease of every repository at the ticket's target
	// branch, waiting in the server's FIFO queue; queued reports one
	// repository's queue position while it waits.
	Hold(ctx context.Context, wi, capsuleDir string, repos []string, queued func(LandingQueue)) (IntervalLease, error)
}

// LandingQueue is one repository's queue observation during Hold: the
// normalized repository and target branch, the 1-based position and the
// server's first-seen stamp. It is non-secret.
type LandingQueue struct {
	Repo, Branch string
	Position     int
	Since        time.Time
	// Cycle is the manager's non-secret cycle id; empty on a fake that
	// does not expose one, where the driver's local id stands in.
	Cycle string
}

// LandingKey is one held lock key of a group, with the queue facts the
// events carry. A manager may expose them through Keys; a test fake
// without the method simply emits no key list.
type LandingKey struct {
	Repo, Branch string
	Position     int
	Since        time.Time
	QueuedMS     int64
}

// IntervalLease is one held lease group.
type IntervalLease interface {
	// Err answers a lost lease (a renewal failed): the driver releases the
	// group, stops dispatching the interval and re-enters at its enter
	// step.
	Err() error
	// Check renews the group and answers its state.
	Check(ctx context.Context) error
	// Release gives the group back (idempotent).
	Release()
}

// Landing-lock events (non-secret: the ticket, the interval, the queue
// position and the reason; never a token or another holder).
const (
	EvLeaseQueued      = "merge_lock_queued"
	EvLeaseGranted     = "merge_lock_granted"
	EvLeaseRenewFailed = "merge_lock_renew_failed"
	EvLeaseReleased    = "merge_lock_released"
	EvLeaseLost        = "merge_lock_lost"
)

// leaseCycleInfo is the optional manager face the events read: a non-secret
// cycle id and the held keys. A fake in a unit test need not implement it.
type leaseCycleInfo interface {
	Cycle() string
	Keys() []LandingKey
}

// leaseInfo answers the group's cycle id and keys, or a driver-local id and
// no key list for a manager that does not expose them.
func leaseInfo(hold IntervalLease, fallbackCycle string) (string, []LandingKey) {
	if info, ok := hold.(leaseCycleInfo); ok {
		if c := info.Cycle(); c != "" {
			return c, info.Keys()
		}
		return fallbackCycle, info.Keys()
	}
	return fallbackCycle, nil
}

// leaseEventKeys maps landing keys onto the event body's non-secret list.
func leaseEventKeys(keys []LandingKey) []map[string]any {
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		row := map[string]any{"repo": k.Repo, "branch": k.Branch, "position": k.Position, "queued_ms": k.QueuedMS}
		if !k.Since.IsZero() {
			row["since"] = k.Since.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, row)
	}
	return out
}

// leaseInterval answers the lane's landing interval as step indexes.
func (d *Driver) leaseInterval() (enter, exit int, ok bool) {
	if d.workflow == nil || d.workflow.LockInterval == nil {
		return 0, 0, false
	}
	enter, eok := d.workflow.indexOf(d.workflow.LockInterval.Enter)
	exit, xok := d.workflow.indexOf(d.workflow.LockInterval.Exit)
	if !eok || !xok || enter > exit {
		return 0, 0, false
	}
	return enter, exit, true
}

// inLeaseInterval reports whether stepID's dispatch runs under the
// interval's lease.
func (d *Driver) inLeaseInterval(stepID string) bool {
	enter, exit, ok := d.leaseInterval()
	if !ok {
		return false
	}
	i, ok := d.workflow.indexOf(stepID)
	return ok && i >= enter && i <= exit
}

// releaseLease gives the held lease group back (idempotent) and names
// why in the event stream.
func (d *Driver) releaseLease(reason string) {
	if d.leaseHold == nil {
		return
	}
	hold := d.leaseHold
	cycle, keys := leaseInfo(hold, "")
	hold.Release()
	d.leaseHold = nil
	body := map[string]any{"reason": reason, "requeue_count": d.leaseRequeues()}
	if cycle != "" {
		body["cycle_id"] = cycle
	}
	if len(keys) > 0 {
		body["keys"] = leaseEventKeys(keys)
	}
	d.emit(EvLeaseReleased, body)
}

// emitLeaseLoss writes the pair a renewal failure produces (Q15): a
// renew_failed fact, then one lost for the whole group. Both name the cycle
// and keys and a controlled reason; the raw server error never rides them.
func (d *Driver) emitLeaseLoss(stepID string) {
	if d.leaseHold == nil {
		return
	}
	cycle, keys := leaseInfo(d.leaseHold, "")
	body := map[string]any{"step": stepID, "requeue_count": d.leaseRequeues()}
	if cycle != "" {
		body["cycle_id"] = cycle
	}
	if len(keys) > 0 {
		body["keys"] = leaseEventKeys(keys)
	}
	renew := map[string]any{}
	for k, v := range body {
		renew[k] = v
	}
	renew["reason"] = "renew_failed"
	d.emit(EvLeaseRenewFailed, renew)
	lost := map[string]any{}
	for k, v := range body {
		lost[k] = v
	}
	lost["reason"] = "lost"
	d.emit(EvLeaseLost, lost)
}

// leaseRequeues is the number of times this driver re-entered the interval
// after a release: 0 on the first cycle. It is memory-only (a restart
// starts a new cycle, and the events stay honest about that).
func (d *Driver) leaseRequeues() int {
	if d.leaseHolds <= 1 {
		return 0
	}
	return d.leaseHolds - 1
}

// releaseLeaseForAsk releases the interval's lease before the driver
// parks on the owner's question; the answer re-enters the interval at its
// declared enter step (Q8/Q14: waiting on a person never holds the lock).
func (d *Driver) releaseLeaseForAsk(stepID string) {
	if !d.inLeaseInterval(stepID) {
		return
	}
	if d.leaseHold != nil {
		// Owner ruling 2026-11-24 (1A): a gate's ask (verification's
		// waiver, the human merge's person) resumes that gate in place; an
		// ask of a non-gate step re-enters at the declared enter step after
		// the reply, and that intent is persisted so a restart lands it too.
		if spec := d.workflow.step(stepID); spec == nil || !spec.Gate {
			d.markLeaseReentry()
			d.leaseReentry = true
		}
		d.releaseLease("ask")
	}
}

// markLeaseReentry persists the interval's enter step as the re-entry
// point of a release that re-queues (an ask of a non-gate step, a lost
// lease, a blocked workflow): a restart or resume lands it in
// consumeLeaseReentry, charged to no rewind budget (Q8).
func (d *Driver) markLeaseReentry() {
	enter, exit, ok := d.leaseInterval()
	if !ok {
		return
	}
	cur := d.Snapshot().CurrentStep
	i, ok := d.workflow.indexOf(cur)
	if !ok || i < enter || i > exit || i == enter {
		return
	}
	_ = d.transition(TransitionRequest{
		Kind:         TransLeaseMark,
		LeaseReentry: d.workflow.Steps[enter].ID,
		SpawnN:       d.currentSpawnN(),
	})
}

// consumeLeaseReentry lands a persisted re-entry once no ask is open: the
// steps from the declared enter step through the current one re-run. It
// answers true when it wrote something, so the caller plans again.
func (d *Driver) consumeLeaseReentry() (bool, error) {
	if d.Snapshot().LeaseReentry == "" {
		return false, nil
	}
	if d.parkedAsk() != nil {
		return false, nil
	}
	enter, exit, ok := d.leaseInterval()
	cur := d.Snapshot().CurrentStep
	i, in := d.workflow.indexOf(cur)
	if !ok || !in || i < enter || i > exit || i == enter {
		_ = d.transition(TransitionRequest{Kind: TransLeaseMark, SpawnN: d.currentSpawnN()})
		return true, nil
	}
	if err := d.transition(TransitionRequest{Kind: TransLeaseReentry, StepID: cur, SpawnN: d.currentSpawnN()}); err != nil {
		return false, err
	}
	return true, nil
}

// watchLease stops the running dispatch when the interval's lease is lost
// (Q7: a renewal failure voids the cycle and the current dispatch must
// stop, not run to completion under a lease someone else holds). Bounded:
// one poll a second, and the caller closes stop when the dispatch ends.
func (d *Driver) watchLease(run StepRun, hold IntervalLease, stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if hold != nil && hold.Err() != nil {
				_ = run.Terminate(5 * time.Second)
				return
			}
		}
	}
}

// ensureLease takes the interval's lease before a dispatch inside it,
// releases it when the planned step is outside, and lands a lock-loss or
// ask re-entry as a rewind to the declared enter step (charged to no
// rewind budget). It answers true when it already wrote that rewind, so
// the caller plans again.
func (d *Driver) ensureLease(ctx context.Context, stepID string) (bool, error) {
	enter, _, ok := d.leaseInterval()
	if !ok {
		d.releaseLease("lane declares no lease_interval")
		return false, nil
	}
	if !d.inLeaseInterval(stepID) {
		d.releaseLease("left the landing-lock interval")
		return false, nil
	}
	if d.leaseHold != nil {
		if err := d.leaseHold.Err(); err == nil {
			return false, nil
		} else {
			d.emitLeaseLoss(stepID)
			d.markLeaseReentry()
			d.releaseLease("lost")
			d.leaseReentry = true
		}
	}
	if d.leaseReentry {
		d.leaseReentry = false
		target := d.workflow.Steps[enter].ID
		if stepID != target {
			if err := d.transition(TransitionRequest{
				Kind:   TransLeaseReentry,
				StepID: stepID,
				Reason: "the landing lock was released (an ask, a lost lease or a step failure): the interval re-enters at " + target,
				SpawnN: d.currentSpawnN(),
			}); err != nil {
				return false, fmt.Errorf("driver: landing-lock re-entry failed: %w", err)
			}
			return true, nil
		}
		// Already standing on the declared enter step: no rewind is needed,
		// but the re-entry still takes the lease BEFORE it dispatches. A
		// return here would run the enter step (develop's sync) outside the
		// lock, the very race the interval exists to prevent (review r4#1).
	}
	// A driver built without the daemon's manager (the unit tests' lanes)
	// runs no lease; the production daemon always binds one, whose own Hold
	// refuses naming a missing aihub client.
	if d.cfg.IntervalLeases == nil {
		return false, nil
	}
	repos := append([]string(nil), d.claimRepos...)
	next := d.leaseHolds + 1
	cycleID := d.wi.ID + "#" + strconv.Itoa(next)
	announced := map[string]bool{}
	announcedAny := false
	position := 0
	var since time.Time
	queued := func(q LandingQueue) {
		id := q.Repo + "\n" + q.Branch
		if announced[id] {
			return
		}
		announced[id] = true
		if q.Cycle != "" {
			cycleID = q.Cycle
		}
		position, since = q.Position, q.Since
		body := map[string]any{"step": stepID, "cycle_id": cycleID, "position": q.Position,
			"requeue_count": next - 1, "reason": "waiting"}
		if !q.Since.IsZero() {
			body["since"] = q.Since.UTC().Format(time.RFC3339Nano)
		}
		body["keys"] = []map[string]any{{"repo": q.Repo, "branch": q.Branch, "position": q.Position, "queued_ms": 0}}
		d.emit(EvLeaseQueued, body)
		if !announcedAny {
			announcedAny = true
			d.fact(supervise.StepQueued{Step: stepID, Reason: "waiting for the landing lock"})
		}
	}
	hold, err := d.cfg.IntervalLeases.Hold(ctx, d.wi.ID, d.wi.CapsuleDir, repos, queued)
	if err != nil {
		return false, fmt.Errorf("taking the landing lock: %w", err)
	}
	d.leaseHolds = next
	d.leaseHold = hold
	cycle, keys := leaseInfo(hold, cycleID)
	// An immediately granted key still formed a 0 ms queued→granted pair
	// (Q4); announce each key this cycle did not see queue.
	for _, k := range keys {
		id := k.Repo + "\n" + k.Branch
		if announced[id] {
			continue
		}
		announced[id] = true
		body := map[string]any{"step": stepID, "cycle_id": cycle, "requeue_count": next - 1,
			"reason": "waiting", "position": k.Position, "queued_ms": 0,
			"keys": []map[string]any{{"repo": k.Repo, "branch": k.Branch, "position": k.Position, "queued_ms": 0}}}
		if !k.Since.IsZero() {
			body["since"] = k.Since.UTC().Format(time.RFC3339Nano)
		}
		d.emit(EvLeaseQueued, body)
	}
	body := map[string]any{"step": stepID, "cycle_id": cycle, "reason": "granted", "requeue_count": next - 1}
	if len(keys) > 0 {
		body["keys"] = leaseEventKeys(keys)
		body["position"] = keys[0].Position
		body["queued_ms"] = keys[0].QueuedMS
		for _, k := range keys {
			if k.QueuedMS > 0 {
				body["queued_ms"] = k.QueuedMS
				break
			}
		}
	} else if announcedAny {
		body["position"] = position
		if !since.IsZero() {
			body["queued_ms"] = d.now().Sub(since).Milliseconds()
		}
	}
	d.emit(EvLeaseGranted, body)
	return false, nil
}
