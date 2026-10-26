package daemon

// crash_window_test.go: kill -9 windows of the daemon. A crash is the
// on-disk state copied at an exact point of the run (what a kill -9 at
// that instant leaves); the assertion is what the next start finds.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
)

// snapshotProbeHub copies the submit queue file when the replay probes
// one work item: the file a kill -9 during that probe leaves.
type snapshotProbeHub struct {
	*fakeHub
	at, queuePath string
	mu            sync.Mutex
	snap          []byte
}

func (h *snapshotProbeHub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	h.mu.Lock()
	if idOrSlug == h.at && h.snap == nil {
		h.snap, _ = os.ReadFile(h.queuePath)
	}
	h.mu.Unlock()
	return h.fakeHub.GetWorkItem(idOrSlug)
}

func (h *snapshotProbeHub) SessionClaim(api.SessionClaimRequest) (*api.SessionClaimResponse, error) {
	return nil, &api.Error{Code: "CORE-409", Detail: "NOT_CLAIMABLE: held elsewhere"}
}

// TestCrashMidSubmitReplayKeepsQueue (CR-5): a kill -9 while the start
// replays the persisted submit queue must not lose an accepted submit:
// at every point the file still lists every entry not yet decided.
func TestCrashMidSubmitReplayKeepsQueue(t *testing.T) {
	setGitIdentity(t)
	ids := []string{"wi_cr5_a", "wi_cr5_b", "wi_cr5_c"}
	hub := &snapshotProbeHub{fakeHub: newFakeHub(), at: ids[2]}
	for _, id := range ids {
		hub.status[id] = api.StatusQueued
	}
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hub
		hub.queuePath = filepath.Join(c.DataDir, submitQueueFile)
		now := time.Now().UTC()
		var es []qEntry
		for _, id := range ids {
			es = append(es, qEntry{WorkItemID: id, SubmittedAt: now, EnqueuedAt: now})
		}
		blob, _ := json.Marshal(submitQueueState{Version: 1, Entries: es})
		if err := os.WriteFile(hub.queuePath, blob, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	waitFor(t, "the replayed submits refused", 10*time.Second, func() bool {
		for _, id := range ids {
			if !hasKind(eventKinds(td.Daemon, id), observe.EvSubmitRejected) {
				return false
			}
		}
		return true
	})
	hub.mu.Lock()
	snap := hub.snap
	hub.mu.Unlock()
	var st submitQueueState
	if err := json.Unmarshal(snap, &st); err != nil {
		t.Fatalf("queue file at the crash point: %v (%q)", err, snap)
	}
	have := map[string]bool{}
	for _, e := range st.Entries {
		have[e.WorkItemID] = true
	}
	for _, id := range ids {
		if !have[id] {
			t.Fatalf("a crash mid-replay loses the accepted submit %s: the file then held %v", id, st.Entries)
		}
	}
}
