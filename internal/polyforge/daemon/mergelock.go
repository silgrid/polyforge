package daemon

// mergelock.go: the per-repo merge lock and the daemon's forge binding
// (driver.Forge). A work item's merge-pr step holds the lock of every repo
// it merges while it checks the default branch and merges its PRs (so two
// items of this daemon never check-then-merge one repository at once:
// the second sees the first's merge on the default branch), and a revert
// step (a lane's step declaring the merge lock) holds it while it merges a
// revert. Across
// daemons the forge serializes the merges, and `--match-head-commit`
// refuses a merge whose PR moved.
//
// The lock is keyed by the repository's identity (the driver passes its
// normalized origin URL), one FIFO queue per key, exactly one holder. It
// lives in this daemon's memory only: a restart drops every grant and
// every waiter, and the restarted drivers queue again — a merge is
// recorded before and after (artifacts/merge.json), so the next holder
// re-reads what landed. It is separate from the job-table lock; mu
// guards the queues only — no I/O runs under it, and a
// waiter blocks on its own channel outside it.
//
// Reachability: daemon code only — the drivers this daemon runs call it
// in-process through Config.Forge; no route serves it and no step agent
// can reach it. The forge binding's command environment is the daemon's
// own (the machine's gh account), never a step's.

import (
	"context"
	"sync"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// mergeLocks is the per-repo merge lock.
type mergeLocks struct {
	mu sync.Mutex
	// queues holds, per repo key, the holder first and its waiters in
	// arrival order; a key with no entry is free.
	queues map[string][]*mergeTicket
}

// mergeTicket is one place in a repo's queue; ready closes when it holds
// the lock.
type mergeTicket struct {
	ready chan struct{}
}

func newMergeLocks() *mergeLocks {
	return &mergeLocks{queues: map[string][]*mergeTicket{}}
}

// Lock waits until repo's lock is this caller's (FIFO) or ctx ends, and
// answers its release (idempotent).
func (l *mergeLocks) Lock(ctx context.Context, repo string) (func(), error) {
	t := &mergeTicket{ready: make(chan struct{})}
	l.mu.Lock()
	l.queues[repo] = append(l.queues[repo], t)
	if len(l.queues[repo]) == 1 {
		close(t.ready)
	}
	l.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { l.leave(repo, t) }) }
	select {
	case <-t.ready:
		return release, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

// leave takes t out of repo's queue; when it held the lock, the next
// waiter gets it.
func (l *mergeLocks) leave(repo string, t *mergeTicket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q := l.queues[repo]
	for i, x := range q {
		if x != t {
			continue
		}
		q = append(q[:i:i], q[i+1:]...)
		if i == 0 && len(q) > 0 {
			close(q[0].ready)
		}
		break
	}
	if len(q) == 0 {
		delete(l.queues, repo)
		return
	}
	l.queues[repo] = q
}

// cellForge is the production driver.Forge: the daemon's command
// environment and its merge lock.
type cellForge struct{ d *Daemon }

var _ driver.Forge = cellForge{}

// CommandEnv implements driver.Forge.
func (f cellForge) CommandEnv() []string { return f.d.commandEnv() }

// LockRepo implements driver.Forge.
func (f cellForge) LockRepo(ctx context.Context, repo string) (func(), error) {
	return f.d.merges.Lock(ctx, repo)
}
