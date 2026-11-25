package daemon

// landing_lock_projection_test.go: the watch-side landing-lock projection
// and its freshness (polyforge#245 epic 3/3, Q1/Q2/Q3). A waiting ticket
// shows its position; a cached projection older than 15 s is stale, not
// renewed; a terminal hub status outranks a leftover wait; and a remote
// progress failure keeps the independently readable lock with a controlled
// progress_error.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func landingFixture(wi string, pos int) *api.WorkItem {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return &api.WorkItem{ID: wi, Slug: "alpha#1", Status: api.StatusRunning,
		LeaseProjection: &api.LeaseProjection{State: api.LeaseWaiting, ObservedAt: now, AsOf: now,
			Keys: []api.LeaseProjectionKey{{Repo: "github.com/o/app", Branch: "epic/245", Position: pos, Since: now}}}}
}

func TestLandingLockStatusWatchFreshness(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_lock"
	item := landingFixture(wi, 3)
	td.hub.mu.Lock()
	if td.hub.items == nil {
		td.hub.items = map[string]*api.WorkItem{}
	}
	td.hub.items[wi] = item
	td.hub.mu.Unlock()

	snap := td.watchSnapshot(wi, nil, true, 0)
	if snap.LeaseProjection == nil || snap.LeaseProjection.State != api.LeaseWaiting ||
		len(snap.LeaseProjection.Keys) != 1 || snap.LeaseProjection.Keys[0].Position != 3 {
		t.Fatalf("fresh watch projection = %+v", snap.LeaseProjection)
	}

	// The source record ages past 15 s; the next hub read fails, so the
	// last successful projection is served as stale with its old position.
	base := time.Now()
	td.leaseSnaps.now = func() time.Time { return base.Add(20 * time.Second) }
	td.hub.mu.Lock()
	td.hub.probeFails = 1
	td.hub.mu.Unlock()
	snap = td.watchSnapshot(wi, nil, true, 0)
	if snap.LeaseProjection == nil || snap.LeaseProjection.State != api.LeaseStale ||
		len(snap.LeaseProjection.Keys) != 1 || snap.LeaseProjection.Keys[0].Position != 3 || snap.LeaseProjection.AgeMS < 15000 {
		t.Fatalf("stale watch projection = %+v", snap.LeaseProjection)
	}

	// A terminal hub status clears the leftover waiting entry.
	td.hub.mu.Lock()
	td.hub.items[wi].Status = api.StatusWrapped
	td.hub.probeFails = 0
	td.hub.mu.Unlock()
	if snap := td.watchSnapshot(wi, nil, true, 0); snap.LeaseProjection == nil || snap.LeaseProjection.State != api.LeaseNone ||
		len(snap.LeaseProjection.Keys) != 0 {
		t.Fatalf("terminal watch projection = %+v", snap.LeaseProjection)
	}

	// No record and no cache is unknown, never fresh-none.
	td.hub.mu.Lock()
	if td.hub.unknown == nil {
		td.hub.unknown = map[string]bool{}
	}
	td.hub.unknown["wi_absent"] = true
	td.hub.mu.Unlock()
	if snap := td.watchSnapshot("wi_absent", nil, true, 0); snap.LeaseProjection == nil || snap.LeaseProjection.State != api.LeaseUnavailable {
		t.Fatalf("unreadable watch projection = %+v", snap.LeaseProjection)
	}
}

func TestLandingLockRemotePartialSnapshot(t *testing.T) {
	for _, code := range []string{api.ErrCoreStaleHolder, api.ErrCoreUpstream} {
		face := &fakeFace{err: &api.Error{Code: code, Detail: "typed"}}
		td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
		enableFace(td)
		item := landingFixture("wi_remote_lock", 4)
		td.hub.mu.Lock()
		if td.hub.items == nil {
			td.hub.items = map[string]*api.WorkItem{}
		}
		td.hub.items[item.ID] = item
		td.hub.mu.Unlock()
		local := protocol.WatchSnapshot{WorkItem: item.ID, Slug: item.Slug, LeaseProjection: item.LeaseProjection,
			Events: []protocol.EventEntry{}}
		got, err := td.Daemon.maybeRemoteSnapshot(item, local)
		if err != nil {
			t.Fatalf("%s: a lock-only partial snapshot must answer, got %v", code, err)
		}
		if !got.LockOnly || got.ProgressError != code || got.Known || got.Execution != nil ||
			got.LeaseProjection == nil || got.LeaseProjection.Keys[0].Position != 4 {
			t.Fatalf("%s: partial snapshot = %+v", code, got)
		}
	}
	// gone and unsupported still answer their typed errors: nothing about
	// the ticket is readable enough to project.
	for _, code := range []string{api.ErrCoreGone, api.ErrCoreUnsupported} {
		face := &fakeFace{err: &api.Error{Code: code, Detail: "typed"}}
		td := newTestDaemon(t, func(c *Config) { c.MachineFace = face })
		enableFace(td)
		item := landingFixture("wi_gone", 1)
		local := protocol.WatchSnapshot{WorkItem: item.ID, Events: []protocol.EventEntry{}}
		if _, err := td.Daemon.maybeRemoteSnapshot(item, local); err == nil {
			t.Fatalf("%s: must keep the typed error", code)
		}
	}
}
