package cli

// landing_lock_test.go: `pf queue`'s landing-lock cell (polyforge#245 epic
// 3/3, Q11). The table gains one LANDING column only when a row waits; a
// stale snapshot keeps its old position and says so, and a row without the
// projection keeps the old table shape.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func landingWaiting(pos int) *api.LeaseProjection {
	return &api.LeaseProjection{State: api.LeaseWaiting, Keys: []api.LeaseProjectionKey{
		{Repo: "github.com/o/app", Branch: "epic/245", Position: pos}}}
}

func TestQueueLandingLock(t *testing.T) {
	var b bytes.Buffer
	renderWITable(&b, []WIView{
		{WorkItem: api.WorkItem{Slug: "alpha#1", ProjectName: "alpha", Status: "running", LeaseProjection: landingWaiting(3)}},
		{WorkItem: api.WorkItem{Slug: "alpha#2", ProjectName: "alpha", Status: "running"}},
	})
	out := b.String()
	if !strings.Contains(out, "LANDING") || !strings.Contains(out, "等待合入锁 #3") {
		t.Fatalf("queue table lacks the landing-lock cell:\n%s", out)
	}
	if !strings.Contains(out, "alpha#2") || strings.Count(out, "等待合入锁") != 1 {
		t.Fatalf("a row without the projection changed shape:\n%s", out)
	}

	// Multi-key positions stay in the projection's order.
	b.Reset()
	multi := &api.LeaseProjection{State: api.LeaseWaiting, Keys: []api.LeaseProjectionKey{
		{Repo: "github.com/o/a", Branch: "epic/245", Position: 2},
		{Repo: "github.com/o/b", Branch: "epic/245", Position: 4}}}
	renderWITable(&b, []WIView{{WorkItem: api.WorkItem{Slug: "alpha#3", Status: "running", LeaseProjection: multi}}})
	if !strings.Contains(b.String(), "等待合入锁 #2,#4") {
		t.Fatalf("multi-key positions:\n%s", b.String())
	}

	// A stale snapshot is not presented as fresh, and it keeps the source
	// position instead of inventing one.
	b.Reset()
	stale := &api.LeaseProjection{State: api.LeaseStale, AgeMS: 20000, Keys: []api.LeaseProjectionKey{
		{Repo: "github.com/o/app", Branch: "epic/245", Position: 3}}}
	renderWITable(&b, []WIView{{WorkItem: api.WorkItem{Slug: "alpha#4", Status: "running", LeaseProjection: stale}}})
	if !strings.Contains(b.String(), "等待合入锁 #3（陈旧）") {
		t.Fatalf("stale row:\n%s", b.String())
	}

	// No projection keeps the pre-#249 table exactly: no LANDING header.
	b.Reset()
	renderWITable(&b, []WIView{{WorkItem: api.WorkItem{Slug: "alpha#5", Status: "queued"}}})
	if strings.Contains(b.String(), "LANDING") {
		t.Fatalf("a table with no waiting row gained the column:\n%s", b.String())
	}
}
