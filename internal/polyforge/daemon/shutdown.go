package daemon

// shutdown.go: the daemon process's shutdown (reference.md §6.2). Every
// state change is durable when it happens — a kill -9 loses nothing the
// restart path cannot recover — so the shutdown only stops the work and
// saves what exists nowhere else: cancel the actors and the loops → terminate
// each live agent tree → push each job's task-repo WIP to refs/pf-wip
// (all jobs in parallel) → drain the runtime actors (their accepted
// operations, each with its state persist, run to the end; the entry
// takes no new one) → persist state.json once → close the face. The WIP
// step and the drain share one wipLimit deadline, so Run (which returns
// after the shutdown) releases the data-dir lock only once no actor
// writes runtime-state.json any more. Running work items are not
// session-ended: they stay running at aihub with their session, and the
// next start attaches them.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// wipLimit bounds the waiting part of the shutdown: terminating the
// agent trees (10s each), the parallel WIP pushes and the runtime drain,
// counted from the cancel.
const wipLimit = 30 * time.Second

// Shutdown runs the shutdown once; concurrent callers wait for the first
// run (or their ctx). cause names the trigger for the log.
// obsDrainLimit bounds the shutdown's wait for the observe lane.
const obsDrainLimit = 10 * time.Second

func (d *Daemon) Shutdown(ctx context.Context, cause string) error {
	first := false
	d.shutOnce.Do(func() { first = true })
	if !first {
		select {
		case <-d.shutDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	fmt.Fprintf(os.Stderr, "daemon: shutdown: cause=%s\n", cause)
	d.modes.SetShuttingDown()
	// Cancel the runtime actors' context, then the loops: the actors'
	// entry closes, no actor starts an agent or a retry any more, an
	// in-flight claim defers itself (its journal entry replays at the
	// next start), and every driver — its context is the run context —
	// terminates its step process.
	d.rt.Close()
	d.cancelRun()
	d.tasks.Close()
	wctx, cancel := context.WithTimeout(context.Background(), wipLimit)
	defer cancel()
	var wg sync.WaitGroup
	for _, ji := range d.rt.List() {
		if ji.Agent == nil || ji.State == runtime.StateSealing || ji.State == runtime.StateDone {
			continue
		}
		wg.Add(1)
		go func(ji runtime.JobInfo) {
			defer wg.Done()
			if err := runtime.TerminateAgentHandle(ji.Agent, 10*time.Second); err != nil {
				// The old driver/step may still write the task repos:
				// snapshotting them now would race a live writer. The next
				// daemon start reaps the tree first and then recovers.
				fmt.Fprintf(os.Stderr, "daemon: %s: agent tree teardown incomplete: %v — WIP snapshot skipped (the old writer may still be alive)\n", ji.WorkItem, err)
				return
			}
			d.rt.SaveTaskWIP(wctx, ji.WorkItem)
		}(ji)
	}
	wg.Wait()
	// The runtime drain, on the same deadline (the WIP step's time counts
	// against it): every operation an actor accepted before the cancel —
	// a close parked in a hub call, a reconcile mid-push — runs to its
	// end, its state persist included, before the final persist below and
	// before Run releases the data-dir lock. No lock is held while it
	// waits.
	if err := d.drainRuntime(wctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: shutdown: the runtime's actors did not stop within %s: %v\n", wipLimit, err)
	}
	// the observe lane drains (its context is the run context): its last
	// event and decision-feed writes land before the shutdown returns
	if d.obsStarted.Load() {
		select {
		case <-d.obsDone:
		case <-time.After(obsDrainLimit):
			fmt.Fprintf(os.Stderr, "daemon: shutdown: the observe lane did not stop within %s\n", obsDrainLimit)
		}
	}
	if d.ownsDataDir() {
		if err := d.rt.Persist(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon: state.json persist failed: %v\n", err)
		}
	}
	// Run assigns the tracker once the data-dir lock is held;
	// the getter takes the same lock, so a shutdown racing startup cannot
	// race the assignment.
	if mh := d.modelHealthTracker(); mh != nil {
		_ = mh.Close()
	}
	d.closeFace()
	close(d.shutDone)
	return nil
}
