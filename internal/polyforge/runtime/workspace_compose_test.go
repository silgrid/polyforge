package runtime

// workspace_compose_test.go: the claim-time compose wiring — the
// precedence runs ONCE per claim over the pinned bundle's
// frontmatter (read from the daemon checkout) and the claim's workspace
// yaml, and the composed map rides the job.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// frontmatterBundle writes a demo bundle whose workflow.md carries real
// frontmatter (candidate layers) and answers the workflows dir.
func frontmatterBundle(t *testing.T, frontmatter string) (workflowsDir string) {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "demo-bundle")
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(frontmatter), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "steps", "step.md"), []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const w079Frontmatter = `---
name: demo-bundle
defaults:
  model_candidates: [pi:prov/weak-a, pi:prov/weak-b]
steps:
  - { id: grill }
  - { id: fix, model_candidates: [pi:prov/flash, pi:prov/glm] }
  - { id: release, model_candidates: [pi:prov/glm] }
---

# demo
`

// A claim composes the effective lists from the bundle frontmatter and
// the workspace yaml, keyed by step id.
func TestClaimComposeRidesTheJob(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
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

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/code-a, pi:w79/code-b] }\n")
	if err := r.Start(context.Background(), "wi_w079comp", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_w079comp")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	got := liveCands(t, r, j)
	if got["fix"] == nil || got["fix"][0] != "pi:w79/code-a" || got["fix"][1] != "pi:w79/code-b" {
		t.Fatalf("fix effective list %v, want [w79/code-a w79/code-b] (the per-step pin replaces the bundle step-level list)", got["fix"])
	}
	if got["release"] == nil || got["release"][0] != "pi:prov/glm" {
		t.Fatalf("release effective list %v, want its own [prov/glm] (a step-id key governs that step only)", got["release"])
	}
	if got["grill"] == nil || got["grill"][0] != "pi:prov/weak-a" {
		t.Fatalf("grill effective list %v, want the bundle weak default (grill unlisted)", got["grill"])
	}
	if err := r.Stop(context.Background(), "wi_w079comp", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A no-yaml claim composes the bundle's own resolution — identical to
// the driver's parsed step lists, so substitution is a no-op.
func TestClaimComposeWithoutYamlIsBundleOnly(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
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

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir

	if err := r.Start(context.Background(), "wi_w079comp2", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_w079comp2")
	got := liveCands(t, r, j)
	if got["fix"] == nil || got["fix"][0] != "pi:prov/flash" {
		t.Fatalf("fix effective list %v, want the bundle step-level head prov/flash", got["fix"])
	}
	if got["grill"] == nil || got["grill"][0] != "pi:prov/weak-a" {
		t.Fatalf("grill effective list %v, want the bundle weak default", got["grill"])
	}
	if err := r.Stop(context.Background(), "wi_w079comp2", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// liveCands answers the candidate lists the job's next dispatch composes
// (polyforge#33: composition is live, nothing is snapshotted at claim).
func liveCands(t *testing.T, r *Runtime, j *job) map[string][]string {
	t.Helper()
	j.mu.Lock()
	project, bundle := j.project, j.bundleName
	j.mu.Unlock()
	got, err := r.composeFor(project, bundle, r.loadManualModels(j.wi))()
	if err != nil {
		t.Fatalf("dispatch-time composition: %v", err)
	}
	return got
}
