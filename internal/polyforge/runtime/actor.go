package runtime

// actor.go: one goroutine and one mailbox per work item. Every operation
// that changes a work item's job — a start, a close, a resume, a
// recovery, a supersession, a reconciliation or its retry — is a message
// to that work item's mailbox, and the messages run one at a time on its
// goroutine, which exists while the mailbox holds work. Reads (Info,
// List, persist's snapshot) take only the job-table lock and the job's
// own mutex, so status and watch never wait on an actor.
//
// The shutdown closes the entry: once the actors' context is cancelled a
// mailbox takes no new operation (a call answers errStopped), while the
// operations already accepted run to their end; Drain waits for them.
//
// Lock order: the I/O leaf locks (gitops' per-work-item lock, claimMu,
// manualMu, pendingMu, persistMu — none nested in another), then the job-table lock
// mu, then a job's mu. Neither of the last two is held across hub, git or
// disk I/O, an EventTap call or a send.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// mailbox is one work item's queue of messages; its goroutine drains it.
type mailbox struct{ q []func() }

// post queues fn on wi's mailbox, starting the work item's goroutine when
// the mailbox was empty, and reports true. It never blocks. Once the
// actors' context is cancelled (the shutdown) it refuses fn and reports
// false. The refusal and a new goroutine's registration in r.actors
// happen under the one hold of mu, so Drain — which cancels, then passes
// mu — waits for every goroutine ever accepted and no Add races its Wait.
func (r *Runtime) post(wi string, fn func()) bool {
	r.mu.Lock()
	if r.ctx.Err() != nil {
		r.mu.Unlock()
		return false
	}
	if mb := r.boxes[wi]; mb != nil {
		mb.q = append(mb.q, fn)
		r.mu.Unlock()
		return true
	}
	mb := &mailbox{q: []func(){fn}}
	r.boxes[wi] = mb
	r.actors.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.actors.Done()
		for {
			r.mu.Lock()
			if len(mb.q) == 0 {
				delete(r.boxes, wi)
				r.mu.Unlock()
				return
			}
			next := mb.q[0]
			mb.q = mb.q[1:]
			r.mu.Unlock()
			next()
		}
	}()
	return true
}

// call posts fn on wi's mailbox and waits for its answer: a door gets the
// final answer once the operations queued before it settled. A caller
// whose ctx ends while fn still waits its turn gets errAbandoned and fn
// never runs; once fn runs, call waits for it. After the shutdown the
// mailbox refuses fn: call answers errStopped at once and fn never runs.
func (r *Runtime) call(ctx context.Context, wi string, fn func() error) error {
	done := make(chan error, 1)
	var turn atomic.Int32 // 0 waiting, 1 running, 2 abandoned
	if !r.post(wi, func() {
		if turn.CompareAndSwap(0, 1) {
			done <- fn()
		}
	}) {
		return errStopped
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if turn.CompareAndSwap(0, 2) {
			return errAbandoned
		}
		return <-done
	}
}

// errAbandoned answers a call whose caller gave up before its turn.
var errAbandoned = errors.New("runtime: the caller gave up before the work item's queued operations settled; nothing was done")

// errStopped answers a call the shutdown refused (DAEMON-410): the
// actors' context is cancelled, so the work item's mailbox takes no new
// operation. Start maps it to the shutdown deferral, like errAbandoned.
var errStopped = &api.Error{Code: api.ErrDaemonGone, Detail: "runtime: the daemon is shutting down — the work item's actor takes no new operation; nothing was done"}

// Drain closes the actors' entry (it cancels their context, as Close
// does) and waits until every operation accepted before that has run to
// its end — its persist included. It answers nil once no actor goroutine
// is left, or ctx's error when ctx ends first (the goroutines keep
// running; a later Drain waits for them again). Concurrent and repeated
// calls are safe.
func (r *Runtime) Drain(ctx context.Context) error {
	r.cancel()
	// barrier: a post that saw the context live registered its goroutine
	// under mu before releasing it; every later post sees it cancelled
	r.mu.Lock()
	r.mu.Unlock()
	done := make(chan struct{})
	go func() { r.actors.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// retry reconciles j's work item again after a backoff — the one retry
// every pending hub step (a close, a verify, a claim replay) shares: the
// first after EndRetryBase, each next one twice as long, up to
// EndRetryMax, then every EndRetryMax. This is the ONLY scheduler of a
// retry: an operation that schedules one and the reconcile running it
// must not clear it (a caller that cleared it pinned the chain at the
// base). A step the hub REFUSED (a typed 4xx) never
// reaches it; that is a final answer (parkRefused).
//
// The chain is BOUNDED by time, not by a count: the
// first scheduled retry stamps the chain's start, and the failed attempt
// that finds EndRetryWindow (default 24h) elapsed since then schedules
// nothing more — the record stays in its state, marked exhausted for the
// operator's doors and named by a retry_exhausted event; a daemon start
// reconciles it afresh. A count bound of eight attempts gave up after
// about four minutes, so any longer aihub outage parked every closing
// record until a restart. The shutdown stops the chain (its waiting
// goroutine ends). Only j's actor calls it.
func (r *Runtime) retry(j *job) {
	if r.ctx.Err() != nil {
		return
	}
	now := time.Now()
	j.mu.Lock()
	if j.retryN == 0 {
		j.retrySince = now
	} else if elapsed := now.Sub(j.retrySince); elapsed >= r.cfg.EndRetryWindow {
		st := string(j.state)
		attempts := j.retryN + 1 // the chain's first attempt plus every retry
		j.retryExhausted = true
		j.mu.Unlock()
		reason := fmt.Sprintf("the retry chain gave up after %d attempts over %s without the hub settling this record (latest state %s): nothing more is retried and the record stays %s for a human — a daemon start reconciles it afresh", attempts, elapsed.Round(time.Second), st, st)
		r.tap(j.wi, EventRetryExhausted, map[string]any{
			"work_item":       j.wi,
			"state":           st,
			"attempts":        attempts,
			"elapsed_seconds": int(elapsed.Seconds()),
			"reason":          reason,
		})
		fmt.Fprintf(os.Stderr, "runtime: %s: %s\n", j.wi, reason)
		return
	}
	j.retryN++
	j.retryExhausted = false
	j.retryIn = min(max(2*j.retryIn, r.cfg.EndRetryBase), r.cfg.EndRetryMax)
	j.retryPending = true
	wait := j.retryIn
	j.mu.Unlock()
	go func(wait time.Duration) {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-t.C:
			j.mu.Lock()
			j.retryPending = false // consumed: the attempt runs now
			j.mu.Unlock()
			r.post(j.wi, func() { r.reconcile(j.wi) })
		case <-r.ctx.Done():
		}
	}(wait)
}

// retryChainNote is the operator-facing tail of an answer about an
// operation the actor's retry chain owns (a close, a claim replay): live
// while the chain lives and retries by itself; once exhausted nothing
// does, and the answer must say so.
func (j *job) retryChainNote(live string) string {
	j.mu.Lock()
	exhausted := j.retryExhausted
	j.mu.Unlock()
	if exhausted {
		return "its retry chain is exhausted (retry_exhausted) and nothing retries it any more — restart the daemon (`pf ops shutdown`, then `pf daemon ensure`), whose start reconciles the record afresh"
	}
	return live
}

// closeRetryNote is retryChainNote for a close.
func (j *job) closeRetryNote() string {
	return j.retryChainNote("retried with backoff until the chain's bound; a typed 4xx refusal parks the record")
}

// retryOutstanding reports whether the actor's next retry is already on a
// timer: the reconcile that runs an operation must not clear the backoff
// such an operation just scheduled.
func (j *job) retryOutstanding() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.retryPending
}

// Idle reports whether no work item has an operation queued or running
// (once Drain answered nil it stays true: the entry is closed).
func (r *Runtime) Idle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.boxes) == 0
}

// Busy reports whether wi (any id form) has an operation queued or
// running: a door of it is answered once that settled.
func (r *Runtime) Busy(wi string) bool {
	id := r.CanonicalID(wi)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.boxes[id] != nil
}
