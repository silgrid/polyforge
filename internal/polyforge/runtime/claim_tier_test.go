package runtime

// claim_tier_test.go (polyforge#81 u7, AC2 / Q14 A): the claim's
// consumption of a queued work item's capability tier. The claim-time
// lane snapshot is the judge: a pending tier whose step the fresh
// capsule's lane no longer declares never enters the overlay (the step
// cannot dispatch), one visible step_tier_dropped diagnosis names the
// work item, the lane, the step and the tier and asks the operator to set
// it again, the claim goes on, and every valid step's tier merges as
// usual. The expected values come from Q14 A, never from this
// implementation's output.

import (
	"context"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestClaimTierRevalidatesLane(t *testing.T) {
	r, hub, _ := w79ParamsEnv(t)
	const wi = "wi_tierclaim"
	hub.items = map[string]*api.WorkItem{wi: {ID: wi, ProjectName: "proj"}}
	type tapEvent struct {
		wi   string
		kind string
		body map[string]any
	}
	var events []tapEvent
	r.cfg.EventTap = func(wi, kind string, body map[string]any) {
		events = append(events, tapEvent{wi: wi, kind: kind, body: body})
	}
	// the pending record a queued ticket's write left: two steps the lane
	// still declares and one it no longer does.
	if err := r.savePendingOverrides(wi, map[string]*capsule.StepOverride{
		"build":  {Tier: "main", TierBy: "usr_q", TierAt: "2026-11-23T01:00:00Z"},
		"review": {Tier: "main", TierBy: "usr_q", TierAt: "2026-11-23T01:00:00Z"},
		"gone":   {Tier: "judgment", TierBy: "usr_q", TierAt: "2026-11-23T01:00:00Z"},
	}); err != nil {
		t.Fatalf("seed the pending record: %v", err)
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("the claim must go on (Q14 A never blocks it): %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if b := ov.Steps["build"]; b == nil || b.Tier != "main" {
		t.Fatalf("a valid step's tier must merge: %+v", ov.Steps["build"])
	}
	if rv := ov.Steps["review"]; rv == nil || rv.Tier != "main" {
		t.Fatalf("a valid step's tier must merge: %+v", ov.Steps["review"])
	}
	if g := ov.Steps["gone"]; g != nil && g.Tier != "" {
		t.Fatalf("a tier whose step the lane no longer declares must not enter the overlay: %+v", g)
	}

	// one visible diagnosis names the work item, the lane, the step and
	// the tier it dropped.
	var dropped map[string]any
	for _, e := range events {
		if e.kind == "step_tier_dropped" {
			if e.wi != wi {
				t.Fatalf("the diagnosis's work item = %q, want %q", e.wi, wi)
			}
			dropped = e.body
		}
	}
	if dropped == nil {
		t.Fatalf("no step_tier_dropped diagnosis: %+v", events)
	}
	if dropped["work_item"] != wi || dropped["lane"] != "demo-bundle" {
		t.Fatalf("the diagnosis names the work item and the lane: %+v", dropped)
	}
	steps, _ := dropped["steps"].([]map[string]any)
	if len(steps) != 1 || steps[0]["step"] != "gone" || steps[0]["tier"] != "judgment" {
		t.Fatalf("the diagnosis names the dropped step and tier: %+v", dropped["steps"])
	}

	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
