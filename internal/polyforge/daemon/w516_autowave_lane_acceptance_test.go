package daemon

// w516_autowave_lane_acceptance_test.go: the auto-pull
// wave skips an execution ticket that lists no acceptance criterion only
// while its lane requires one — the lane's `acceptance` declaration,
// absent = required. The lanes are the test's own: nolist declares none,
// plain declares nothing, strict declares required.

import (
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func TestW516AutoWaveFollowsTheLaneAcceptance(t *testing.T) {
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "nolist", "acceptance: none\nsteps:\n  - { id: work }\n")
	workflowtest.WriteLane(t, wf, "plain", "steps:\n  - { id: work }\n")
	workflowtest.WriteLane(t, wf, "strict", "acceptance: required\nsteps:\n  - { id: work }\n")
	td := newTestDaemon(t, func(c *Config) {
		c.WorkflowsDir = wf
		c.DrainInterval = time.Hour
		c.DispatchTick = time.Hour
	})
	item := func(id, lane string) api.WorkItem {
		return api.WorkItem{ID: id, Slug: id, ProjectName: "proj", Status: api.StatusQueued,
			Priority: api.PriorityNormal, BundleName: &lane}
	}
	td.hub.mu.Lock()
	td.hub.queues = append(td.hub.queues,
		item("wi_w516_nolist", "nolist"),
		item("wi_w516_plain", "plain"),
		item("wi_w516_strict", "strict"),
	)
	td.hub.mu.Unlock()
	if err := td.modes.SetAuto(true); err != nil {
		t.Fatalf("enable auto: %v", err)
	}
	td.fetchAutoWave()
	for id, want := range map[string]bool{
		"wi_w516_nolist": true,
		"wi_w516_plain":  false,
		"wi_w516_strict": false,
	} {
		if got := td.q.IsPending(id); got != want {
			t.Errorf("%s claimed by the wave = %v, want %v (a ticket without acceptance is skipped only on a lane requiring acceptance)", id, got, want)
		}
	}
}
