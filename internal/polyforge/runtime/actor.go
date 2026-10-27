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
// EndRetryMax. The shutdown stops it (its waiting goroutine ends). Only
// j's actor calls it.
func (r *Runtime) retry(j *job) {
	if r.ctx.Err() != nil {
		return
	}
	j.retryIn = min(max(2*j.retryIn, r.cfg.EndRetryBase), r.cfg.EndRetryMax)
	go func(wait time.Duration) {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-t.C:
			r.post(j.wi, func() { r.reconcile(j.wi) })
		case <-r.ctx.Done():
		}
	}(j.retryIn)
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
