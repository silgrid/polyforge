package daemon

// landing_cache_test.go: the cached projection's freshness boundaries
// (polyforge#249 Q1/Q2; review r1#3/#7). A cached fresh-none must never turn
// into a fabricated stale wait, and the 15 s boundary is inclusive: at
// exactly 15 s the projection is stale, below it still fresh.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func cacheFixture(t *testing.T) (*testDaemon, *api.LeaseProjection) {
	t.Helper()
	td := newTestDaemon(t, nil)
	lock := &api.LeaseProjection{State: api.LeaseNone,
		ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), AsOf: time.Now().UTC().Format(time.RFC3339Nano)}
	td.hub.mu.Lock()
	if td.hub.items == nil {
		td.hub.items = map[string]*api.WorkItem{}
	}
	td.hub.items["wi_cache"] = &api.WorkItem{ID: "wi_cache", Slug: "alpha#1", Status: api.StatusRunning, LeaseProjection: lock}
	td.hub.mu.Unlock()
	return td, lock
}

func TestLandingLockCacheNoneNeverBecomesAWait(t *testing.T) {
	td, _ := cacheFixture(t)
	base := time.Now()
	td.leaseSnaps.now = func() time.Time { return base }
	if got := td.leaseLockFor("wi_cache", nil); got == nil || got.State != api.LeaseNone {
		t.Fatalf("seed none = %+v", got)
	}
	td.leaseSnaps.now = func() time.Time { return base.Add(20 * time.Second) }
	td.hub.mu.Lock()
	td.hub.probeFails = 1
	td.hub.mu.Unlock()
	got := td.leaseLockFor("wi_cache", nil)
	if got == nil || got.State != api.LeaseUnavailable || len(got.Keys) != 0 {
		t.Fatalf("an aged none must answer unavailable, not a wait: %+v", got)
	}
}

func TestLandingLockCacheBoundaryIsInclusive(t *testing.T) {
	td, lock := cacheFixture(t)
	lock.State = api.LeaseWaiting
	lock.Keys = []api.LeaseProjectionKey{{Repo: "github.com/o/app", Branch: "epic/245", Position: 3}}
	base := time.Now()
	td.leaseSnaps.now = func() time.Time { return base }
	if got := td.leaseLockFor("wi_cache", nil); got == nil || got.State != api.LeaseWaiting {
		t.Fatalf("seed waiting = %+v", got)
	}
	td.hub.mu.Lock()
	td.hub.probeFails = 2
	td.hub.mu.Unlock()
	td.leaseSnaps.now = func() time.Time { return base.Add(api.LeaseStaleAfter - time.Second) }
	if got := td.leaseLockFor("wi_cache", nil); got == nil || got.State != api.LeaseWaiting {
		t.Fatalf("14s must stay fresh: %+v", got)
	}
	td.leaseSnaps.now = func() time.Time { return base.Add(api.LeaseStaleAfter) }
	if got := td.leaseLockFor("wi_cache", nil); got == nil || got.State != api.LeaseStale || len(got.Keys) != 1 {
		t.Fatalf("exactly 15s must be stale with its key: %+v", got)
	}
}
