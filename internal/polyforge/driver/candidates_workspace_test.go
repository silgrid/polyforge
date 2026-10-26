package driver

// candidates_workspace_test.go: the driver's consumption of the
// polyforge#79 claim-time candidate map — the recomputation indexes that
// list (the only candidate source, present-empty governance), and the
// recovery integration's interrupted-dispatch archive lands through the
// single execution.json writer. Rotation semantics themselves are
// untouched (INV-1g et al. — provider-class infra failures alone
// advance, bounded, capped at list end; a gate-FAIL rewind resets).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// seedPendingQuestion patches one step's pending_question into the
// seeded execution.json (a dispatch that delivered needs_input before
// the death the recovery cause records).
func seedPendingQuestion(t *testing.T, execPath, stepID, qid, q string) {
	t.Helper()
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	var exec capsule.Execution
	if err := json.Unmarshal(raw, &exec); err != nil {
		t.Fatal(err)
	}
	exec.Steps[stepID].PendingQuestion = &capsule.PendingQuestion{ID: qid, Step: stepID, Question: q}
	blob, err := json.MarshalIndent(exec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedExecution writes an execution.json with one step in the given
// state: status + archived attempts + current_step.
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
	st := &capsule.Step{Status: status, Attempts: attempts}
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

func infraAttempt(n int, failureCause string) capsule.Attempt {
	return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: failureCause, AttemptSeq: n}
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
		Workflow: &Workflow{Steps: []StepSpec{
			{ID: "fix", ContextPolicy: "fresh"},
		}, MaxTotal: 4, MaxPerStep: 2},
		Spawner: sp,
		Log:     &eventLog{},
		StepCandidates: map[string][]string{
			"fix": {"pi:eff-a", "pi:eff-b", "pi:eff-c"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	spawn, _, err := d.prepareDispatch("fix")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if spawn.Model != "eff-a" {
		t.Fatalf("model %q, want the claim-time head eff-a", spawn.Model)
	}
}

// Rotation advances along the EFFECTIVE list and caps at its end.
func TestRotationAdvancesAndCapsOnEffectiveList(t *testing.T) {
	cases := []struct {
		name     string
		list     []string
		attempts []capsule.Attempt
		want     string
	}{
		{
			"two provider failures pin the third entry",
			[]string{"pi:eff-a", "pi:eff-b", "pi:eff-c"},
			[]capsule.Attempt{infraAttempt(1, FailExitModel), infraAttempt(2, FailExitModel)},
			"eff-c",
		},
		{
			"two failures on a two-entry list cap at the end",
			[]string{"pi:eff-a", "pi:eff-b"},
			[]capsule.Attempt{infraAttempt(1, FailExitModel), infraAttempt(2, FailExitModel)},
			"eff-b",
		},
		{
			"host-class failures never advance",
			[]string{"pi:eff-a", "pi:eff-b"},
			[]capsule.Attempt{infraAttempt(1, FailExitHost), infraAttempt(2, FailExitHost)},
			"eff-a",
		},
		{
			"a gate-fail rewind resets to the head",
			[]string{"pi:eff-a", "pi:eff-b"},
			[]capsule.Attempt{infraAttempt(1, FailExitModel), {Cause: CauseGateFailRewind, AttemptSeq: 2}},
			"eff-a",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			execPath := filepath.Join(dir, capsule.ExecutionJSON)
			seedExecution(t, execPath, "fix", capsule.StepInProgress, c.attempts)
			sp := &fakeSpawner{}
			d, err := New(Config{
				WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
				ExecPath: execPath,
				Workflow: &Workflow{Steps: []StepSpec{{ID: "fix", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
				Spawner:  sp,
				Log:      &eventLog{},
				StepCandidates: map[string][]string{
					"fix": c.list,
				},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			spawn, _, err := d.prepareDispatch("fix")
			if err != nil {
				t.Fatalf("prepareDispatch: %v", err)
			}
			if spawn.Model != c.want {
				t.Fatalf("model %q, want %q", spawn.Model, c.want)
			}
		})
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
		Workflow: &Workflow{Steps: []StepSpec{
			{ID: "fix", ContextPolicy: "fresh"},
		}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:        sp,
		Log:            &eventLog{},
		StepCandidates: map[string][]string{"fix": {}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{{ID: "fix", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  sp,
		Log:      &eventLog{},
		StepCandidates: map[string][]string{
			"fix": {"pi:eff-a", "pi:eff-b"},
		},
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
// through a nil Interrupted), and a dispatch that ended legitimately (a
// parked question, a non-in_progress step) archives nothing either.
func TestInterruptedDispatchNoArchiveShapes(t *testing.T) {
	// No Interrupted at all: a plain claim never archives.
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	seedExecution(t, execPath, "fix", capsule.StepInProgress, nil)
	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{{ID: "fix", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
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

	// An Interrupted signal with the step ALREADY parked on a question
	// (the previous dispatch delivered needs_input before dying): the
	// archive guard sees the pending question and archives nothing.
	dir2 := t.TempDir()
	execPath2 := filepath.Join(dir2, capsule.ExecutionJSON)
	seedExecution(t, execPath2, "fix", capsule.StepInProgress, nil)
	seedPendingQuestion(t, execPath2, "fix", "q1", "what?")
	d2, err := New(Config{
		WI:          WIInfo{ID: "wi_run", CapsuleDir: dir2, BundleName: "b"},
		ExecPath:    execPath2,
		Workflow:    &Workflow{Steps: []StepSpec{{ID: "fix", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:     &fakeSpawner{},
		Log:         &eventLog{},
		Interrupted: &InterruptedDispatch{Detail: "agent_death"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Run parks on the open question — drive it with a cancellable
	// context and observe the durable file once the question persists.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d2.Run(ctx)
	}()
	waitUntil(t, "the parked question", func() bool {
		raw, rerr := os.ReadFile(execPath2)
		return rerr == nil && strings.Contains(string(raw), "pending_question")
	})
	cancel()
	<-done
	raw2, _ := os.ReadFile(execPath2)
	if strings.Contains(string(raw2), "infra_failure") {
		t.Fatalf("a parked question archives nothing: %s", raw2)
	}
}

// The bundle's model_candidates reach a dispatch only through the claim's
// composition (workspace.ComposeCandidates, which reads every bundle
// level): the driver reads no list of its own, so a step without a
// claim-time entry runs on the harness default.
func TestBundleListIsNotADriverCandidateSource(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle")
	fm := "---\ndefaults:\n  model_candidates: [pi:prov/a]\nsteps:\n  - id: fix\n    context_policy: fresh\n    model_candidates: [pi:prov/b]\n---\n"
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(fm), 0o644); err != nil {
		t.Fatal(err)
	}
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
	spawn, _, err := d.prepareDispatch("fix")
	if err != nil {
		t.Fatalf("prepareDispatch: %v", err)
	}
	if spawn.Model != "" || spawn.Harness != "pi" {
		t.Fatalf("dispatch %s:%s, want the harness default (no claim-time list)", spawn.Harness, spawn.Model)
	}
}
