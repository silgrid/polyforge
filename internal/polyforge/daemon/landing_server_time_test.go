package daemon

// landing_server_time_test.go: the lease events carry the server's queue
// facts (polyforge#249 Q15; review r1#4). The hub's grant already answers
// QueuedMS and HeldSince; the manager must keep them instead of its own
// wall clock.

import (
	"context"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

type serverTimeHub struct {
	*fakeLeaseHub
	heldSince string
	queuedMS  int64
	polls     int
}

func (h *serverTimeHub) AcquireLease(_ string, req api.LeaseAcquire) (*api.LeaseGrant, error) {
	h.polls++
	if h.polls == 1 {
		return &api.LeaseGrant{Kind: leaseKind, Key: "k/" + req.Repo, RequestID: req.RequestID, Status: "queued", Position: 1}, nil
	}
	return &api.LeaseGrant{Kind: leaseKind, Key: "k/" + req.Repo, RequestID: req.RequestID,
		Status: "granted", Token: "tok", HeldSince: h.heldSince, QueuedMS: h.queuedMS}, nil
}

func TestLandingEventsTakeTheServerQueueFacts(t *testing.T) {
	hub := &serverTimeHub{fakeLeaseHub: newFakeLeaseHub(), heldSince: "2026-11-25T00:00:12Z", queuedMS: 12000}
	l := newLeaseManager(hub, t.TempDir()+"/journal.json")
	l.poll = time.Millisecond
	hold, err := l.Hold(context.Background(), "wi_1", landingCapsule(t), []string{"o/app"}, nil)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	defer hold.Release()
	info, ok := hold.(interface{ Keys() []driver.LeaseKey })
	if !ok {
		t.Fatal("the held group exposes its keys")
	}
	keys := info.Keys()
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	wantSince, _ := time.Parse(time.RFC3339Nano, "2026-11-25T00:00:00Z")
	if keys[0].QueuedMS != 12000 {
		t.Fatalf("queued_ms = %d, want the server's 12000", keys[0].QueuedMS)
	}
	if !keys[0].Since.Equal(wantSince) {
		t.Fatalf("since = %s, want the server's %s", keys[0].Since, wantSince)
	}
}
