package daemon

// progresslookup.go: the caller half of the cross-machine query face
//. The fixed decision order lives here:
//
//  1. local queue/job/capsule -> local snapshot (no remote call)
//  2. aihub cannot resolve the WI -> CORE-404
//  3. queued/terminal/paused -> local hub-data snapshot (no remote call)
//  4. running/blocked held by this machine without a local job -> local
//     registered-window snapshot
//  5. running/blocked, fresh holder != this machine -> signed 5 s query
//     and the holder's snapshot plus holder_machine, served from the
//     remote-snapshot cache for remoteSnapTTL ( A3)
//  6. stale holder -> CORE-504 (never stale data)
//  7. deregistered holder -> CORE-410
//  8. holder without the capability -> CORE-501

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// The remote-snapshot cache ( A3). A resident `pf watch`
// polls every 2 s, and every poll of a WI held elsewhere used to cost the
// holder a signed relay: ≈300 per 605 s relay window per watcher, close
// to aihub's 320-per-member budget and two thirds of its 448-per-holder
// one. With the cache one daemon relays a WI at most once per
// remoteSnapTTL however many chats and watchers poll it — ≤61 per window
// — so several members' daemons watching one holder stay far inside 448
// (seven would still fit). 10 s is five watcher polls: remote progress,
// and a remote human-decision wake, lag by at most 10 s, which is far
// below aihub's minutes-scale stale-holder verdict and short enough for
// a human glancing at a progress line. remoteSnapCap bounds memory (each
// snapshot's events are bounded by observe.HistoryCap); expired entries
// are swept on every store and the oldest entry is evicted at the cap.
const (
	remoteSnapTTL = 10 * time.Second
	remoteSnapCap = 64
)

// remoteSnapCache holds the last relayed snapshot per WI. An entry is
// served only while it is younger than the TTL AND the WI's aihub status
// and updated_at still equal what they were at the relay, so any
// aihub-visible change (a claim, a session end, a pause, a block, a
// patch) misses and relays again. Only successful relays are stored:
// an error is never cached, so a failing holder is re-asked on the next
// poll exactly as before. A served snapshot shares its slices with the
// entry: callers only encode it.
type remoteSnapCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	now        func() time.Time // the clock (tests inject one)
	entries    map[string]remoteSnapEntry
}

type remoteSnapEntry struct {
	status, updatedAt string
	at                time.Time
	snap              protocol.WatchSnapshot
}

func newRemoteSnapCache(ttl time.Duration, maxEntries int) *remoteSnapCache {
	return &remoteSnapCache{ttl: ttl, maxEntries: maxEntries, now: time.Now, entries: map[string]remoteSnapEntry{}}
}

// get answers the cached snapshot for item, dropping an entry that
// expired or no longer matches the WI's aihub state.
func (c *remoteSnapCache) get(item *api.WorkItem) (protocol.WatchSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[item.ID]
	if !ok {
		return protocol.WatchSnapshot{}, false
	}
	if c.now().Sub(e.at) >= c.ttl || e.status != item.Status || e.updatedAt != item.UpdatedAt {
		delete(c.entries, item.ID)
		return protocol.WatchSnapshot{}, false
	}
	return e.snap, true
}

// put stores one successful relay, sweeping expired entries first and
// evicting the oldest entry when the cache is still at its cap.
func (c *remoteSnapCache) put(item *api.WorkItem, snap protocol.WatchSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for id, e := range c.entries {
		if now.Sub(e.at) >= c.ttl {
			delete(c.entries, id)
		}
	}
	if _, ok := c.entries[item.ID]; !ok && len(c.entries) >= c.maxEntries {
		var oldest string
		var oldestAt time.Time
		for id, e := range c.entries {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = id, e.at
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[item.ID] = remoteSnapEntry{status: item.Status, updatedAt: item.UpdatedAt, at: now, snap: snap}
}

// drop forgets wi's entry: the local side knows the WI is no longer a
// remote running/blocked one.
func (c *remoteSnapCache) drop(wi string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, wi)
}

func (d *Daemon) wouldRemote(item *api.WorkItem, local protocol.WatchSnapshot) bool {
	if item == nil || local.Known || local.Queued {
		return false
	}
	if item.Status != api.StatusRunning && item.Status != api.StatusBlocked {
		return false
	}
	return d.faceClient() != nil && d.faceAvailable()
}

func (d *Daemon) maybeRemoteSnapshot(item *api.WorkItem, local protocol.WatchSnapshot) (protocol.WatchSnapshot, error) {
	// A WI the local side knows is not remote — a local job or queue
	// entry, queued/terminal/paused at aihub, or no face — never reads
	// the cache, and its entry goes.
	if !d.wouldRemote(item, local) {
		if item != nil {
			d.remoteSnaps.drop(item.ID)
		}
		return local, nil
	}
	if snap, ok := d.remoteSnaps.get(item); ok {
		return snap, nil
	}
	client := d.faceClient()
	ans, err := client.ProgressQuery(item.ID, "")
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) {
			switch ae.Code {
			case api.ErrCoreStaleHolder, api.ErrCoreGone, api.ErrCoreUnsupported, api.ErrCoreUpstream:
				return local, ae
			}
		}
		return local, err
	}
	if ans.Local {
		return local, nil // this machine is the holder but has no job yet
	}
	if len(ans.Snapshot) == 0 {
		return local, nil
	}
	var remote protocol.WatchSnapshot
	if err := json.Unmarshal(ans.Snapshot, &remote); err != nil {
		return local, &api.Error{Code: api.ErrDaemonInternal, Detail: "relayed snapshot is unreadable: " + err.Error()}
	}
	remote.HolderMachine = ans.HolderMachine
	d.remoteSnaps.put(item, remote)
	return remote, nil
}

// artifactRoute serves GET /jobs/{wi}/artifact?path=<relpath>: the same
// gate and decision order as progress, never cached (an artifact is
// fetched on demand, not polled — A3). A local hit answers from its own
// capsule first (no aihub dependency); a queued/terminal WI answers only
// the pointer; only a running/blocked WI held elsewhere is relayed.
func (d *Daemon) artifactRoute(w http.ResponseWriter, r *http.Request) {
	// The artifact face is never reachable from a step process.
	if reason := d.stepOriginRefusal(r); reason != "" {
		writeErr(w, api.ErrDaemonForbidden, reason)
		return
	}
	rel := r.URL.Query().Get("path")
	wi := r.PathValue("wi")
	canonical, item := d.resolveWI(wi)
	if canonical == "" {
		canonical = wi
	}
	// 1. local hit first: a local job or queue entry answers from its own
	// capsule without asking aihub and without a remote call.
	if d.holdsWI(canonical) || d.holdsWI(wi) {
		if c := d.rt.Resolve(canonical); c != "" {
			canonical = c
		}
		ans, err := d.readArtifact(canonical, rel)
		if err != nil {
			writeErrOf(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ans)
		return
	}
	// 2. aihub could not resolve the work item.
	if item == nil {
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("work item %s is unknown or unreadable at aihub", wi))
		return
	}
	// 3. queued/terminal: the pointer only, never content.
	if item.Status != api.StatusRunning && item.Status != api.StatusBlocked {
		writeJSON(w, http.StatusOK, api.ArtifactAnswer{
			WorkItem: canonical, Slug: item.Slug, Status: item.Status, Pointer: true, Path: rel,
		})
		return
	}
	// 4-8. running/blocked: the holder answers; typed refusals surface.
	client := d.faceClient()
	if client == nil || !d.faceAvailable() {
		writeErr(w, api.ErrCoreUnsupported, "this machine cannot relay a cross-machine artifact query")
		return
	}
	ans, err := client.ProgressQuery(item.ID, rel)
	if err != nil {
		writeErrOf(w, err)
		return
	}
	if ans.Local {
		// This machine is the holder but holds no local job/capsule.
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("work item %s is not held by machine %s", canonical, d.cfg.MachineID))
		return
	}
	if ans.Artifact == nil {
		writeErr(w, api.ErrCoreNotFound, fmt.Sprintf("artifact %q is unavailable from the holder", rel))
		return
	}
	writeJSON(w, http.StatusOK, ans.Artifact)
}
