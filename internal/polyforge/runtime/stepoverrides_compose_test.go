package runtime

// stepoverrides_compose_test.go (u2): the dispatch-time composition's
// overlay layer (polyforge#25) — a step whose capsule overlay
// (runtime-state/step-overrides.json) names a model chain runs that chain
// as its whole effective list, over the manual `--models` list and the
// workspace yaml alike (AC1); a step without an overlay keeps the
// composition exactly as it was, yaml re-read fresh on every dispatch
// (AC2). The overlay is read at every composition, so a later clear or
// edit governs the very next dispatch (AC3's dispatch half).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// overrideComposeEnv claims a work item over w079Lane with a yaml step
// entry for fix and the given manual list, and answers the live composer.
func overrideComposeEnv(t *testing.T, manual []string, yaml string) (*Runtime, *job) {
	t.Helper()
	setGitIdentity(t)
	wfDir := laneBundle(t, w079Lane)
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

	writeWorkspaceYaml(t, ws, yaml)
	if len(manual) > 0 {
		r.saveManualModels("wi_ov2", manual)
	}
	if err := r.Start(context.Background(), "wi_ov2", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_ov2")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	return r, j
}

func writeOverlay(t *testing.T, j *job, step string, models []string) {
	t.Helper()
	j.mu.Lock()
	capDir := j.capsuleDir
	j.mu.Unlock()
	ov, err := capsule.ReadStepOverrides(capDir)
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	yes := true
	if ov.Steps[step] == nil {
		ov.Steps[step] = &capsule.StepOverride{}
	}
	if models != nil {
		ov.Steps[step].Models = models
		ov.Steps[step].ModelsBy = "usr_t"
	} else {
		ov.Steps[step].Models = nil
	}
	ov.Steps[step].Skip = &yes // an unrelated aspect must not disturb the compose
	if err := capsule.WriteStepOverrides(capDir, ov); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
}

// AC1: the overlay chain replaces the step's whole effective list — over
// the yaml step entry and the manual --models list alike — and the chain
// head is the overlay's first candidate.
func TestStepOverrideComposeTopsManualAndYaml(t *testing.T) {
	yaml := "steps:\n  proj:\n    fix: { models: [pi:w79/code-a, pi:w79/code-b] }\n"
	r, j := overrideComposeEnv(t, []string{"pi:ov/manual-a", "pi:ov/manual-b"}, yaml)

	got := liveCands(t, r, j)
	if got["fix"][0] != "pi:ov/manual-a" {
		t.Fatalf("before the overlay the manual list governs fix: %v", got["fix"])
	}

	chain := []string{"pi:ov/chain-a", "pi:ov/chain-b"}
	writeOverlay(t, j, "fix", chain)
	got = liveCands(t, r, j)
	if len(got["fix"]) != 2 || got["fix"][0] != "pi:ov/chain-a" || got["fix"][1] != "pi:ov/chain-b" {
		t.Fatalf("fix effective list %v, want the whole overlay chain [ov/chain-a ov/chain-b] (over manual and yaml)", got["fix"])
	}
	// a step the overlay does not name keeps the manual list unchanged
	if got["release"][0] != "pi:ov/manual-a" || got["grill"][0] != "pi:ov/manual-a" {
		t.Fatalf("steps without an overlay must keep the manual list: release=%v grill=%v", got["release"], got["grill"])
	}

	// clearing the overlay restores the source below it (AC3's dispatch half)
	writeOverlay(t, j, "fix", nil)
	got = liveCands(t, r, j)
	if got["fix"][0] != "pi:ov/manual-a" {
		t.Fatalf("after the clear the manual list governs fix again: %v", got["fix"])
	}
	if err := r.Stop(context.Background(), "wi_ov2", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// AC2: without an overlay the yaml governs and is re-read at every
// composition — an edit between two dispatches reaches the next one.
func TestComposeWithoutOverrideRereadsYaml(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "ws")
	yamlPath := filepath.Join(ws, ".polyforge.yaml")
	yaml := "steps:\n  proj:\n    fix: { models: [pi:w79/code-a] }\n"
	setGitIdentity(t)
	wfDir := laneBundle(t, w079Lane)
	hub := newFakeHub()
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

	writeWorkspaceYaml(t, ws, yaml)
	if err := r.Start(context.Background(), "wi_ov2b", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_ov2b")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	got := liveCands(t, r, j)
	if got["fix"][0] != "pi:w79/code-a" {
		t.Fatalf("fix effective list %v, want the yaml entry", got["fix"])
	}
	// an edit of the yaml between two compositions governs the next one
	if err := os.WriteFile(yamlPath, []byte("steps:\n  proj:\n    fix: { models: [pi:w79/code-new] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = liveCands(t, r, j)
	if got["fix"][0] != "pi:w79/code-new" {
		t.Fatalf("fix effective list %v after the yaml edit, want the fresh entry (the yaml is re-read per dispatch)", got["fix"])
	}
	// and an overlay written now replaces that fresh entry (the layers stack)
	writeOverlay(t, j, "fix", []string{"pi:ov/chain-z"})
	got = liveCands(t, r, j)
	if got["fix"][0] != "pi:ov/chain-z" {
		t.Fatalf("fix effective list %v, want the overlay over the edited yaml", got["fix"])
	}
	if err := r.Stop(context.Background(), "wi_ov2b", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

var _ = workflowtest.Header
