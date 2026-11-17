package runtime

// w79_claim_params_test.go (u4, polyforge#79): the claim's consumption of
// a queued work item's step_params attrs — the values any machine's
// daemon wrote on its machine key while the work item was held by no one.
// The claim-time lane snapshot (the fresh capsule's own copy) is their
// judge: an entry whose step and param the snapshot declares with a
// legal value merges into the overlay record (the per-ticket layer,
// later than any pending aspect of the same param); anything else — a
// step or param the lane no longer declares, a planted shape — is
// dropped with one stderr line and NEVER refuses the claim (a ticket is
// not stranded by params its lane drifted away from). The merged record
// is published at once and only that publish's success clears the aihub
// key (the writePendingStepOverrides rule); a failed publish keeps the
// key — the job's own publishes carry the merged overlay.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

const w79ParamsLane = `name: demo-bundle
max_rewinds: 3
steps:
  - { id: build, params: { image: base-img } }
  - { id: review, gate: true, on_fail: { target: build }, params: { image: base-img, flag: "" } }
  - { id: merge, skippable: true }
`

// w79ParamsEnv answers a runtime over a params-declaring lane, its fake
// hub and the mapped workspace.
func w79ParamsEnv(t *testing.T) (*Runtime, *fakeHub, string) {
	t.Helper()
	setGitIdentity(t)
	wfDir := laneBundle(t, w79ParamsLane)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	return r, hub, ws
}

// w79SeedStepParams plants the attrs key on the hub's item.
func w79SeedStepParams(hub *fakeHub, wi string, key map[string]any) {
	hub.items = map[string]*api.WorkItem{wi: {ID: wi, ProjectName: "proj",
		Attrs: map[string]any{api.StepParamsKey: key}}}
}

// w79GitLog answers the pushed chain's commit subjects (the claim's own
// publish reached the hub's bare repo).
func w79GitLog(t *testing.T, hub *fakeHub, wi string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", filepath.Join(hub.capsuleRepoBase, wi+".git"),
		"log", "--format=%s", "-10").CombinedOutput()
	if err != nil {
		t.Fatalf("git log of the pushed chain: %v\n%s", err, out)
	}
	return string(out)
}

// The claim merges the attrs' declared, legal entries into the fresh
// capsule's overlay record, publishes them at once and clears the key.
func TestW79ClaimMergesStepParams(t *testing.T) {
	r, hub, _ := w79ParamsEnv(t)
	const wi = "wi_w79c"
	w79SeedStepParams(hub, wi, map[string]any{
		"build":  map[string]any{"image": "reg/w79:1"},
		"review": map[string]any{"image": "reg/w79:2", "flag": "on"},
		// a step the lane does not declare, and a param the declared step
		// does not carry: both dropped, never a refused claim
		"nosuch": map[string]any{"x": "y"},
		"merge":  map[string]any{"image": "z"},
	})
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if b := ov.Steps["build"]; b == nil || b.Params["image"] == nil ||
		b.Params["image"].Value != "reg/w79:1" || b.Params["image"].By != "step_params" || b.Params["image"].At == "" {
		t.Fatalf("the merged build params: %+v", ov.Steps["build"])
	}
	if rv := ov.Steps["review"]; rv == nil || rv.Params["image"] == nil || rv.Params["image"].Value != "reg/w79:2" ||
		rv.Params["flag"] == nil || rv.Params["flag"].Value != "on" {
		t.Fatalf("the merged review params: %+v", ov.Steps["review"])
	}
	if _, has := ov.Steps["nosuch"]; has {
		t.Fatalf("a step the lane does not declare never enters the overlay: %+v", ov.Steps["nosuch"])
	}
	if m := ov.Steps["merge"]; m != nil && len(m.Params) != 0 {
		t.Fatalf("an undeclared param never enters the overlay: %+v", m.Params)
	}
	if log := w79GitLog(t, hub, wi); !strings.Contains(log, "step params:") {
		t.Fatalf("the claim's publish of the merged params: %s", log)
	}
	if _, has := hub.items[wi].Attrs[api.StepParamsKey]; has {
		t.Fatalf("a successful publish clears the key: %+v", hub.items[wi].Attrs)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A key that carries nothing the lane declares is consumed (dropped with
// a line, never a refused claim) and cleared the same.
func TestW79ClaimDropsUndeclaredStepParams(t *testing.T) {
	r, hub, _ := w79ParamsEnv(t)
	const wi = "wi_w79d"
	w79SeedStepParams(hub, wi, map[string]any{
		"nosuch": map[string]any{"image": "x"},
		"build":  map[string]any{"nosuch": "x"},
		"review": map[string]any{"image": strings.Repeat("x", 257)},
	})
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if len(ov.Steps) != 0 {
		t.Fatalf("nothing the lane declares, nothing merged: %+v", ov.Steps)
	}
	if _, has := hub.items[wi].Attrs[api.StepParamsKey]; has {
		t.Fatalf("the consumed key is cleared: %+v", hub.items[wi].Attrs)
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A failed publish keeps the key (the next claim merges the values
// again); the merged overlay itself stays — the job's own publishes
// carry it.
func TestW79ClaimKeepsKeyOnPublishFailure(t *testing.T) {
	r, hub, _ := w79ParamsEnv(t)
	const wi = "wi_w79e"
	w79SeedStepParams(hub, wi, map[string]any{
		"build": map[string]any{"image": "reg/w79:1"},
	})
	real := pendingPublishFn
	t.Cleanup(func() { pendingPublishFn = real })
	pendingPublishFn = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
		return "", false, gitops.PushTimings{}, errors.New("the push was refused")
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if b := ov.Steps["build"]; b == nil || b.Params["image"] == nil || b.Params["image"].Value != "reg/w79:1" {
		t.Fatalf("the merged params stay in the capsule: %+v", ov.Steps["build"])
	}
	if _, has := hub.items[wi].Attrs[api.StepParamsKey]; !has {
		t.Fatal("a failed publish keeps the key for the next claim")
	}
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
