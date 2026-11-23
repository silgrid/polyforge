package runtime

// tier_precedence_test.go (polyforge#81 u3, AC3): the dispatch-time
// composition expands an overlay tier through the Host catalog and
// records one composition-scoped diagnostic event when a set tier is
// missing from (or empty in) the catalog — the affected step, the tier,
// the reason and the layer that now governs. `default` means unset and
// records nothing. The expected values come from AC3/Q7 B/Q12 A, not from
// this implementation's output.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

type tierTapEvent struct {
	wi   string
	kind string
	body map[string]any
}

func tierComposeEnv(t *testing.T, catalog map[string][]string) (*Runtime, *job, *[]tierTapEvent) {
	t.Helper()
	setGitIdentity(t)
	wfDir := laneBundle(t, w079Lane)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	var events []tierTapEvent
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
		StepTiers:     func() map[string][]string { return catalog },
		EventTap: func(wi, kind string, body map[string]any) {
			events = append(events, tierTapEvent{wi: wi, kind: kind, body: body})
		},
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir

	writeWorkspaceYaml(t, ws, "")
	if err := r.Start(context.Background(), "wi_tierp", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_tierp")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	return r, j, &events
}

func writeTierOverlay(t *testing.T, j *job, step, tier string) {
	t.Helper()
	j.mu.Lock()
	capDir := j.capsuleDir
	j.mu.Unlock()
	ov, err := capsule.ReadStepOverrides(capDir)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	if ov.Steps[step] == nil {
		ov.Steps[step] = &capsule.StepOverride{}
	}
	ov.Steps[step].Tier = tier
	ov.Steps[step].TierBy = "usr_t"
	if err := capsule.WriteStepOverrides(capDir, ov); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
}

func TestTierPrecedenceRuntimeCompose(t *testing.T) {
	r, j, events := tierComposeEnv(t, map[string][]string{
		"main":  {"pi:prov/tier-a", "claude:tier-b"},
		"empty": {},
	})

	writeTierOverlay(t, j, "fix", "main")
	got := liveCands(t, r, j)
	if len(got["fix"]) != 2 || got["fix"][0] != "pi:prov/tier-a" || got["fix"][1] != "claude:tier-b" {
		t.Fatalf("the overlay tier must expand over the lane step list: %v", got["fix"])
	}
	if len(*events) != 0 {
		t.Fatalf("a governing tier records no fallback event: %+v", *events)
	}

	// a missing tier falls through to the lane step list and records one
	// composition-scoped event naming the next layer.
	writeTierOverlay(t, j, "fix", "nosuch")
	got = liveCands(t, r, j)
	if got["fix"][0] != "pi:prov/flash" {
		t.Fatalf("a missing tier falls to the lane step list: %v", got["fix"])
	}
	if len(*events) != 1 || (*events)[0].kind != EventStepTierFallback {
		t.Fatalf("one fallback event expected: %+v", *events)
	}
	body := (*events)[0].body
	if body["scope"] != "composition" {
		t.Fatalf("the event's scope: %v", body)
	}
	steps, _ := body["steps"].([]map[string]any)
	if len(steps) != 1 || steps[0]["step"] != "fix" || steps[0]["tier"] != "nosuch" ||
		steps[0]["reason"] != "missing" || steps[0]["next_source"] != "bundle  step fix model_candidates" {
		t.Fatalf("the event's steps: %+v", steps)
	}

	// an empty catalog tier records reason "empty".
	writeTierOverlay(t, j, "release", "empty")
	liveCands(t, r, j)
	last := (*events)[len(*events)-1].body["steps"].([]map[string]any)
	found := false
	for _, st := range last {
		if st["step"] == "release" && st["reason"] == "empty" {
			found = true
		}
	}
	if len(*events) != 2 || !found {
		t.Fatalf("an empty catalog tier must be answered as empty: %+v", *events)
	}

	// `default` means unset: the lower layer governs, no event.
	writeTierOverlay(t, j, "grill", "default")
	writeTierOverlay(t, j, "fix", "default")
	writeTierOverlay(t, j, "release", "")
	got = liveCands(t, r, j)
	if got["grill"][0] != "pi:prov/weak-a" {
		t.Fatalf("default must leave the lane default governing: %v", got["grill"])
	}
	if len(*events) != 2 {
		t.Fatalf("default must record no fallback: %+v", *events)
	}

	if err := r.Stop(context.Background(), "wi_tierp", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
