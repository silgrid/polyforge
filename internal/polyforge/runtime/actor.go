package runtime

// actor.go: one goroutine and one mailbox per work item. Every operation
// that changes a work item's job — a start, a close, a resume, a
// recovery, a supersession, a reconciliation or its retry — is a message
// to that work item's mailbox, and the messages run one at a time on its
// goroutine, which exists while the mailbox holds work. Reads (Info,
// List, persist's snapshot) take only the job-table lock and the job's
// own mutex, so status and watch never wait on an actor.
//
// Lock order: the I/O leaf locks (gitops' per-work-item lock, claimMu,
// manualMu, persistMu — none nested in another), then the job-table lock
// mu, then a job's mu. Neither of the last two is held across hub, git or
// disk I/O, an EventTap call or a send.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// mailbox is one work item's queue of messages; its goroutine drains it.
type mailbox struct{ q []func() }

// post queues fn on wi's mailbox, starting the work item's goroutine when
// the mailbox was empty. It never blocks.
func (r *Runtime) post(wi string, fn func()) {
	r.mu.Lock()
	if mb := r.boxes[wi]; mb != nil {
		mb.q = append(mb.q, fn)
		r.mu.Unlock()
		return
	}
	mb := &mailbox{q: []func(){fn}}
	r.boxes[wi] = mb
	r.mu.Unlock()
	go func() {
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
}

// call posts fn on wi's mailbox and waits for its answer: a door gets the
// final answer once the operations queued before it settled. A caller
// whose ctx ends while fn still waits its turn gets errAbandoned and fn
// never runs; once fn runs, call waits for it.
func (r *Runtime) call(ctx context.Context, wi string, fn func() error) error {
	done := make(chan error, 1)
	var turn atomic.Int32 // 0 waiting, 1 running, 2 abandoned
	r.post(wi, func() {
		if turn.CompareAndSwap(0, 1) {
			done <- fn()
		}
	})
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

// retry reconciles j's work item again after a backoff — the one retry
// every pending hub step (a close, a verify, a claim replay) shares: the
// first after EndRetryBase, each next one twice as long, up to
// EndRetryMax, then every EndRetryMax. This is the ONLY scheduler of a
// retry: an operation that schedules one and the reconcile running it
// must not clear it (a caller that cleared it pinned the chain at the
// base —). A step the hub REFUSED (a typed 4xx) never
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
// (after Close: every actor has finished its last message).
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
