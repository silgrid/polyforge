package daemon

// w461_autowave_acceptance_test.go (AC1): the auto-pull
// wave claims only an execution ticket whose content lists an acceptance
// criterion (`## Acceptance` or `## 验收`, the same predicate `pf submit`
// applies); one without — no content, or a heading with no criterion —
// stays queued for interaction instead of being claimed.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w461AcceptanceContent is the content the auto-pull fixtures give a
// ticket the wave must claim.
const w461AcceptanceContent = "## Acceptance\n\n- AC1 the wave claims it — carried by build\n"

func TestW461AutoWaveSkipsATicketWithoutAcceptance(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) {
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	bundle := "demo-bundle"
	item := func(id string, content *string) api.WorkItem {
		return api.WorkItem{ID: id, Slug: id, ProjectName: "proj", Status: api.StatusQueued,
			Priority: api.PriorityNormal, BundleName: &bundle, Content: content}
	}
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues,
		item("wi_w461_en", strp("## Acceptance\n\n- AC1 x — carried by build\n")),
		item("wi_w461_zh", strp("## 验收\n\n- AC1 y\n")),
		item("wi_w461_none", nil),
		item("wi_w461_empty", strp("## Acceptance\n\nprose only\n")),
	)
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	for id, want := range map[string]bool{
		"wi_w461_en":    true,
		"wi_w461_zh":    true,
		"wi_w461_none":  false,
		"wi_w461_empty": false,
	} {
		if got := td.q.IsPending(id); got != want {
			t.Errorf("%s claimed by the wave = %v, want %v (a ticket is claimed only when its content lists an acceptance criterion)", id, got, want)
		}
	}
}
