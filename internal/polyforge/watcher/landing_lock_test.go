package watcher

// landing_lock_test.go: `pf watch --once`'s landing line (polyforge#245
// epic 3/3, Q11/Q2). A waiting ticket shows its position and lock key; a
// stale snapshot keeps the old position and says it is stale; unavailable
// says so; a ticket not waiting adds no line.

import (
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestLandingLockRender(t *testing.T) {
	snap := &protocol.WatchSnapshot{WorkItem: "wi_q", Slug: "alpha#1", Known: true,
		LeaseProjection: &api.LeaseProjection{State: api.LeaseWaiting, Keys: []api.LeaseProjectionKey{
			{Repo: "github.com/o/app", Branch: "epic/245", Position: 3}}}}
	out := FormatOnce(snap, time.Now(), 5)
	if !strings.Contains(out, "landing:    等待合入锁 #3") {
		t.Fatalf("waiting watch line:\n%s", out)
	}
	if keys := LeaseKeysText(snap.LeaseProjection); keys != "github.com/o/app (epic/245)" {
		t.Fatalf("keys = %q", keys)
	}

	snap.LeaseProjection.State = api.LeaseStale
	snap.LeaseProjection.AgeMS = 20000
	out = FormatOnce(snap, time.Now(), 5)
	if !strings.Contains(out, "等待合入锁 #3（陈旧）") {
		t.Fatalf("stale watch line:\n%s", out)
	}

	snap.LeaseProjection = &api.LeaseProjection{State: api.LeaseUnavailable}
	out = FormatOnce(snap, time.Now(), 5)
	if !strings.Contains(out, "锁状态不可用") || strings.Contains(out, "#0") {
		t.Fatalf("unavailable watch line:\n%s", out)
	}

	snap.LeaseProjection = &api.LeaseProjection{State: api.LeaseNone}
	if out := FormatOnce(snap, time.Now(), 5); strings.Contains(out, "landing:") {
		t.Fatalf("fresh-none added a landing line:\n%s", out)
	}
}
