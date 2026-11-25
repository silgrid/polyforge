package daemon

// landingcache.go: the daemon's last successful landing-lock projection per
// work item (polyforge#245 epic 3/3, Q1/Q2). A watch snapshot reads the
// projection from the hub on every request; when that read fails, the last
// successful snapshot still answers while it is younger than the ticket's
// 15 s window and is marked stale after it. The age is the server's own
// observation gap (AsOf - ObservedAt) plus this daemon's monotonic time
// since receipt, so two machines' wall clocks are never subtracted.
//
// Reachability: daemon code only; a step or task agent sees the projection
// through the watch route, never this cache.

import (
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

type leaseSnapEntry struct {
	lock *api.LeaseProjection
	at   time.Time
}

type leaseSnapCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]leaseSnapEntry
}

func newLeaseSnapCache() *leaseSnapCache {
	return &leaseSnapCache{now: time.Now, entries: map[string]leaseSnapEntry{}}
}

// put stores a copy of one fresh projection (never a stale one: only the
// hub's answer is stored, so the cache cannot renew itself).
func (c *leaseSnapCache) put(wi string, lock *api.LeaseProjection) {
	if c == nil || wi == "" || lock == nil {
		return
	}
	cp := *lock
	cp.Keys = append([]api.LeaseProjectionKey(nil), lock.Keys...)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[wi] = leaseSnapEntry{lock: &cp, at: c.now()}
}

// get answers the cached projection and its age (the server observation gap
// plus the local elapsed time).
func (c *leaseSnapCache) get(wi string) (*api.LeaseProjection, time.Duration, bool) {
	if c == nil {
		return nil, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[wi]
	if !ok {
		return nil, 0, false
	}
	age := c.now().Sub(e.at)
	if d, err := time.Parse(time.RFC3339Nano, e.lock.AsOf); err == nil {
		if o, err := time.Parse(time.RFC3339Nano, e.lock.ObservedAt); err == nil && d.After(o) {
			age += d.Sub(o)
		}
	}
	cp := *e.lock
	cp.Keys = append([]api.LeaseProjectionKey(nil), e.lock.Keys...)
	return &cp, age, true
}

// drop forgets a ticket's cached projection (a terminal hub status outranks
// a leftover waiting snapshot).
func (c *leaseSnapCache) drop(wi string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, wi)
}
