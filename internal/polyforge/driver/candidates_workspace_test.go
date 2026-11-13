package driver

// candidates_workspace_test.go: the driver's consumption of the
// polyforge#79 claim-time candidate map — the recomputation indexes that
// list (the only candidate source, present-empty governance), and the
// recovery integration's interrupted-dispatch archive lands through the
// single execution.json writer. Rotation semantics themselves (INV-1g et
// al. — provider-class infra failures alone advance, bounded, capped at
// list end; a gate-FAIL rewind resets) are candidates_test.go's
// TestCandidatePointer table; the crash window of a decided switch is
// candidate_anchor_regression_test.go's.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

func seedExecution(t *testing.T, execPath, stepID, status string, attempts []capsule.Attempt) {
	t.Helper()
	exec := capsule.Execution{
		CapsuleID:      "cap_wi_run",
		WorkItemID:     "wi_run",
		WorkflowStatus: capsule.WorkflowStatusRunning,
		CurrentStep:    stepID,
		Steps:          map[string]*capsule.Step{},
		Version:        1,
	}
	// Step.Candidate is the pointer's anchor: a seeded archive implies the
	// model its last dispatch ran
	st := &capsule.Step{Status: status, Attempts: attempts, Candidate: persistedAnchor(attempts)}
	if status == capsule.StepInProgress {
		st.StepStartedAt = time.Now().UTC().Format(time.RFC3339)
	}
	exec.Steps[stepID] = st
	blob, err := json.MarshalIndent(exec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedDispatch sets the step's awaited dispatch ordinal: the real in-flight
// shape step_start always writes (Step.Dispatch = SpawnN).
func seedDispatch(t *testing.T, execPath, stepID string, n int64) {
	t.Helper()
	exec := loadExecFile(t, execPath)
	exec.Steps[stepID].Dispatch = n
	blob, err := json.MarshalIndent(exec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// infraAttempt is the archive of dispatch n's death; cand is the
// candidate string the dying dispatch ran (polyforge#33 stamps it).
func infraAttempt(n int, failureCause, cand string) capsule.Attempt {
	return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: failureCause, AttemptSeq: n, Candidate: cand}
}

// The archived-attempt recomputation indexes the claim-time list.
func TestClaimListHeadIsDispatched(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{
			{ID: "fix"},
		}...),
		Spawner: sp,
		Log:     &eventLog{},
		Compose: composeOf(map[string][]string{
			"fix": {"pi:eff-a", "pi:eff-b", "pi:eff-c"},
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.composeDispatch("fix")
	spawn, _, err := d.prepareDispatch("fix")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if spawn.Model != "eff-a" {
		t.Fatalf("model %q, want the claim-time head eff-a", spawn.Model)
	}
}

// A present-empty claim-time entry governs: no switchable chain — the
// harness adapter's default stays in effect.
func TestPresentEmptyEffectiveEntryGoverns(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	sp := &fakeSpawner{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{
			{ID: "fix"},
		}...),
		Spawner: sp,
		Log:     &eventLog{},
		Compose: composeOf(map[string][]string{"fix": {}}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.composeDispatch("fix")
	spawn, _, err := d.prepareDispatch("fix")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if spawn.Model != "" {
		t.Fatalf("model %q, want \"\" (a present-empty list governs; no invented pin)", spawn.Model)
	}
}

// The recovery integration's interrupted-dispatch archive: ONE
// audit-only infra_failure attempt lands on
// the in_progress step before the re-dispatch, and the re-dispatch's
// model stays the chain head.
func TestInterruptedDispatchArchivesOnce(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	seedDispatch(t, execPath, "fix", 1)
	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{{ID: "fix"}}...),
		Spawner:  sp,
		Log:      &eventLog{},
		Compose: composeOf(map[string][]string{
			"fix": {"pi:eff-a", "pi:eff-b"},
		}),
		Interrupted: &InterruptedDispatch{Detail: "daemon_restart"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	var exec capsule.Execution
	if err := json.Unmarshal(raw, &exec); err != nil {
		t.Fatal(err)
	}
	attempts := exec.Steps["fix"].Attempts
	if len(attempts) != 2 {
		t.Fatalf("fix attempts %d, want 2 (the interrupted archive + the accepted result)", len(attempts))
	}
	if attempts[0].Cause != CauseInfraFailure || attempts[0].FailureCause != "daemon_restart" {
		t.Fatalf("first attempt %+v, want the archived infra_failure", attempts[0])
	}
	if !strings.Contains(attempts[0].Detail, "daemon_restart") {
		t.Fatalf("the archive must carry the recovery cause as its detail: %+v", attempts[0])
	}
	if attempts[1].Cause != "accepted" {
		t.Fatalf("second attempt %+v, want the accepted result", attempts[1])
	}
	// The interruption is audit only: the pointer stays on the head.
	if got := sp.spawnAt(0).Model; got != "eff-a" {
		t.Fatalf("re-dispatch model %q, want eff-a (an interruption never advances)", got)
	}
}

// An operator-shaped interruption archives nothing (the bridge filters
// it before the driver ever sees it — pinned at the unit boundary here
// through a nil Interrupted). (A dispatch settled by its open `pf ask`
// archives nothing either: ask_test.go,
// TestAskRestartReconciliationParksWithoutDispatch.)
func TestInterruptedDispatchNoArchiveShapes(t *testing.T) {
	// No Interrupted at all: a plain claim never archives.
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{{ID: "fix"}}...),
		Spawner:  sp,
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, _ := os.ReadFile(execPath)
	if strings.Contains(string(raw), "infra_failure") {
		t.Fatalf("a claim re-derivation must archive nothing: %s", raw)
	}
}

// The bundle's model_candidates reach a dispatch only through the
// dispatch-time composition (Config.Compose, which reads every bundle
// level): the driver reads no list of its own, so a step the composition
// gives no entry runs on the harness default.
func TestBundleListIsNotADriverCandidateSource(t *testing.T) {
	dir := t.TempDir()
	bundle := workflowtest.WriteLane(t, dir, "bundle", "defaults:\n  model_candidates: [pi:prov/a]\nsteps:\n  - id: fix\n    model_candidates: [pi:prov/b]\n")
	d, err := New(Config{
		WI:        WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		BundleDir: bundle,
		ExecPath:  filepath.Join(dir, capsule.ExecutionJSON),
		Spawner:   &fakeSpawner{},
		Log:       &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.composeDispatch("fix")
	spawn, _, err := d.prepareDispatch("fix")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if spawn.Model != "" || spawn.Harness != "pi" {
		t.Fatalf("dispatch %s:%s, want the harness default (no claim-time list)", spawn.Harness, spawn.Model)
	}
}

// A composition failure at dispatch blocks the workflow (the owner fixes the
// config and resumes); a candidate change between two dispatches is one
// candidate_switched event.
func TestComposeFailureBlocksAndChangeEmitsSwitch(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	log := &eventLog{}
	var fail error
	list := []string{"pi:a", "pi:b"}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{{ID: "fix"}}...),
		Spawner:  &fakeSpawner{},
		Log:      log,
		Compose: func() (map[string][]string, error) {
			return map[string][]string{"fix": list}, fail
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fail = errors.New("DAEMON-414: broken yaml")
	if blocked, err := d.composeDispatch("fix"); err != nil || !blocked {
		t.Fatalf("composeDispatch = %v, %v, want blocked", blocked, err)
	}
	snap := d.Snapshot()
	if snap.WorkflowStatus != StatusBlocked || !strings.Contains(snap.BlockedReason, "DAEMON-414") {
		t.Fatalf("status %q reason %q, want blocked naming DAEMON-414", snap.WorkflowStatus, snap.BlockedReason)
	}
	if err := d.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	fail = nil
	if blocked, err := d.composeDispatch("fix"); err != nil || blocked {
		t.Fatalf("composeDispatch after the fix = %v, %v", blocked, err)
	}
	start := func(cand string) {
		t.Helper()
		if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "fix", Model: cand, Candidate: "pi:" + cand, SpawnN: d.currentSpawnN() + 1}); err != nil {
			t.Fatal(err)
		}
	}
	start("a")
	start("a") // same candidate: no event
	list = []string{"pi:b"}
	start("b") // the list lost a: one config_changed switch
	n := 0
	for _, e := range log.entries {
		if e.kind == "candidate_switched" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("candidate_switched events = %d, want exactly 1", n)
	}
}
