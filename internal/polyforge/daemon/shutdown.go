package daemon

// shutdown.go: the daemon process's shutdown (reference.md §6.2). Every
// state change is durable when it happens — a kill -9 loses nothing the
// restart path cannot recover — so the shutdown only stops the work and
// saves what exists nowhere else: cancel the actors and the loops → terminate
// each live agent tree → push each job's task-repo WIP to refs/pf-wip
// (all jobs in parallel) → persist state.json once → close the face.
// The waiting shares one wipLimit deadline. Running work
// items are not session-ended: they stay running at aihub with their
// session, and the next start attaches them.

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// wipLimit bounds the waiting part of the shutdown: terminating the
// agent trees (10s each) and the parallel WIP pushes.
const wipLimit = 30 * time.Second

// Shutdown runs the shutdown once; concurrent callers wait for the first
// run (or their ctx). cause names the trigger for the log.
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
	// Cancel the runtime actors' context, then the loops: no actor starts
	// an agent or a retry any more, an in-flight claim defers itself (its
	// journal entry replays at the next start), and every driver — its
	// context is the run context — terminates its step process.
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
			if err := ji.Agent.Terminate(10 * time.Second); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: %s: agent tree teardown incomplete: %v\n", ji.WorkItem, err)
			}
			d.rt.SaveTaskWIP(wctx, ji.WorkItem)
		}(ji)
	}
	wg.Wait()
	if d.ownsDataDir() {
		if err := d.rt.Persist(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon: state.json persist failed: %v\n", err)
		}
	}
	// workbench#14: Run assigns the tracker once the data-dir lock is held;
	// the getter takes the same lock, so a shutdown racing startup cannot
	// race the assignment.
	if mh := d.modelHealthTracker(); mh != nil {
		_ = mh.Close()
	}
	d.closeFace()
	close(d.shutDone)
	return nil
}
