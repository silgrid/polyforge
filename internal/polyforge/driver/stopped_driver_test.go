package driver

// stopped_driver_test.go: once Run has returned, the driver writes nothing
// more (stopWrites, ErrDriverStopped) — the runtime may already have handed
// the capsule to another writer (a relaunch), so a late write that reached
// the old handle fails loudly and never lands. Ported from the owner
// send-back's fence test when `pf rewind` was removed
// the fence outlives the command.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// stoppedBundle writes a two-step bundle and a record at build in
// progress; it answers the capsule dir.
func stoppedBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bundle := filepath.Join(dir, "workflows", "wf")
	writeStepHeader(t, filepath.Join(dir, "workflows"))
	if err := os.MkdirAll(filepath.Join(bundle, "steps"), 0o755); err != nil {
		t.Fatal(err)
	}
	front := workflowtest.Header("wf") + "defaults:\n  model_candidates: [pi:m]\nsteps:\n  - { id: plan }\n  - { id: build }\n"
	if err := os.WriteFile(filepath.Join(bundle, workflow.WorkflowFile), []byte(front), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"plan", "build"} {
		if err := os.WriteFile(filepath.Join(bundle, "steps", s+".md"), []byte(extDef(s, "do "+s+"\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ex := capsule.Execution{CapsuleID: "cap_wi_f", WorkItemID: "wi_f", CurrentStep: "build", WorkflowStatus: StatusRunning, Version: 4,
		Steps: map[string]*capsule.Step{
			"plan":  {Status: capsule.StepDone},
			"build": {Status: capsule.StepInProgress},
		}}
	raw, _ := json.Marshal(ex)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, capsule.ExecutionJSON)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A write that reaches a driver whose Run returned is refused with
// ErrDriverStopped and leaves execution.json as it was.
func TestStoppedDriverRefusesEveryWrite(t *testing.T) {
	dir := stoppedBundle(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, err := Start(ctx, StartConfig{WI: "wi_f", CapsuleDir: dir, BundleName: "wf", SessionHandle: "fp", SpawnN: 1, Spawner: &startSpawner{},
		Compose: composeOf(map[string][]string{"plan": {"pi:m"}, "build": {"pi:m"}})})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.Terminate(2 * time.Second); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, capsule.ExecutionJSON))
	err = h.drv.transition(TransitionRequest{Kind: TransBlocked, StepID: "plan", Reason: "a late write", SpawnN: h.drv.currentSpawnN()})
	if !errors.Is(err, ErrDriverStopped) {
		t.Fatalf("a write to a stopped driver = %v, want ErrDriverStopped", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, capsule.ExecutionJSON)); string(after) != string(before) {
		t.Fatal("a stopped driver wrote execution.json")
	}
}
