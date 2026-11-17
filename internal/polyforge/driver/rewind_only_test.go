package driver

// rewind_only_test.go (polyforge#116 u3): a `rewind_only` step — one a
// gate's [on_fail target, gate) range covers — runs only after a rewind
// covers it. The first round records it skipped without a dispatch (a
// step_skipped event, no model session); once any rewind that covers it
// resets it to pending it runs — a gate FAIL's (automatic or
// --per-owner) and an owner's rewind alike, both recording the same
// archives on the step's own attempts — and it runs on every later round
// another rewind resets it to. A project skip of a `skippable` it
// declares wins over the activation, and the step never reaches the
// lane's end, so the last step an agent runs and the wrap attrs of a
// skipped tail move nowhere. The activation is the durable ledger's
// (execution.json): a restarted driver dispatches the activated step too.

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The lanes: fix sits between the on_fail target build and its gate
// review (the covered position), or IS the target.
const (
	rewindOnlyMidLane    = "max_rewinds: 1\nsteps:\n  - { id: build }\n  - { id: fix, rewind_only: true }\n  - { id: review, gate: true, on_fail: { target: build } }\n"
	rewindOnlyTargetLane = "max_rewinds: 1\nsteps:\n  - { id: build, rewind_only: true }\n  - { id: review, gate: true, on_fail: { target: build } }\n"
	rewindOnlySkipLane   = "max_rewinds: 1\nsteps:\n  - { id: build }\n  - { id: fix, skippable: true, rewind_only: true }\n  - { id: review, gate: true, on_fail: { target: build } }\n"
)

// rewindEnv lays out a capsule whose lane snapshot is the front given and
// builds a driver over it with the scripted spawner (the lane read from
// the snapshot, as a claim's driver does); skipped, when not nil, is the
// capsule's skipped-step set file.
func rewindEnv(t *testing.T, front string, script []fakeEntry, skipped string) (*Driver, *fakeSpawner, *eventLog, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "capsules", "wi_rewind")
	workflowtest.WriteLane(t, filepath.Join(dir, capsule.DirWorkflows), "demo", front)
	if skipped != "" {
		writeCapsuleFile(t, dir, capsule.SkippedStepsJSON, skipped)
	}
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_rewind","slug":"demo-9","project":"proj","goal":"g"}`)
	sp := &fakeSpawner{script: script}
	log := &eventLog{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_rewind", CapsuleDir: dir, BundleName: "demo"},
		Spawner:  sp,
		Log:      log,
		Language: func() string { return stepcall.LangZhCN },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, dir
}

func rewindDispatched(sp *fakeSpawner) []string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	out := make([]string, 0, len(sp.spawns))
	for _, s := range sp.spawns {
		out = append(out, s.StepID)
	}
	return out
}

func rewindFailCiting(t *testing.T, dir, at string) fakeEntry {
	t.Helper()
	ownerSaid(t, dir, "review", at, "redo it")
	return fakeEntry{res: &StepResult{Handoff: &stepcall.Handoff{Status: stepcall.StatusFail, PerOwner: at, Reason: "the owner wants it redone"}}}
}

func rewindTo(t *testing.T, dir, at, to string) fakeEntry {
	t.Helper()
	ownerSaid(t, dir, "review", at, "back to "+to)
	return fakeEntry{res: &StepResult{Handoff: &stepcall.Handoff{Status: stepcall.StatusRewind, To: to, PerOwner: at}}}
}

// The first round records the step skipped and dispatches nothing for
// it: the lane completes over it, one step_skipped event, and the step
// order of the hand-off document names it a plain step (only a project
// skip is annotated), the last step an agent runs unmoved.
func TestRewindOnlyFirstRoundSkipped(t *testing.T) {
	d, sp, log, dir := rewindEnv(t, rewindOnlyMidLane, []fakeEntry{done(`{}`), done(`{}`)}, "")
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review"}) {
		t.Fatalf("dispatched %v, want [build review] (fix is skipped on the first round)", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", x.WorkflowStatus)
	}
	if x.Steps["fix"].Status != capsule.StepSkipped || x.Steps["fix"].Attempts != nil {
		t.Fatalf("fix %q with %+v attempts, want skipped with none", x.Steps["fix"].Status, x.Steps["fix"].Attempts)
	}
	if n := log.count("step_skipped"); n != 1 || !log.has("step_skipped", "step", "fix") {
		t.Fatalf("events %v: want one step_skipped {step: fix}", log.kinds())
	}
	if !d.workflow.lastAgentStep("review") {
		t.Fatal("the last step an agent runs is review, unmoved by the skip")
	}
	doc := handoffDocOf(t, dir)
	if want := "1. `build`\n2. `fix`\n3. `review`（最后一步）"; !strings.Contains(doc, want) {
		t.Fatalf("the opening's step order lacks the plain %q:\n%s", want, doc)
	}
}

// TestRewindOnlyDispatch: every rewind that covers the step runs it.
func TestRewindOnlyDispatch(t *testing.T) {
	t.Run("the on_fail target runs after the gate FAIL covers it", func(t *testing.T) {
		d, sp, log, dir := rewindEnv(t, rewindOnlyTargetLane, []fakeEntry{gateFAIL(), done(`{}`), done(`{}`)}, "")
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"review", "build", "review"}) {
			t.Fatalf("dispatched %v, want [review build review]", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.WorkflowStatus != StatusCompleted || x.Steps["build"].Status != capsule.StepDone || x.RewindStats.Count != 1 {
			t.Fatalf("the target ran after the FAIL: %q %+v", x.WorkflowStatus, x.RewindStats)
		}
		if n := log.count("step_skipped"); n != 1 || !log.has("step_skipped", "step", "build") {
			t.Fatalf("events %v: want one step_skipped {step: build}", log.kinds())
		}
	})

	t.Run("a step between target and gate runs after the FAIL covers it", func(t *testing.T) {
		d, sp, _, dir := rewindEnv(t, rewindOnlyMidLane, []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{}`), done(`{}`)}, "")
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review", "build", "fix", "review"}) {
			t.Fatalf("dispatched %v, want fix on the second round only", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.WorkflowStatus != StatusCompleted || x.Steps["fix"].Status != capsule.StepDone {
			t.Fatalf("the covered step ran after the FAIL: %q fix %q", x.WorkflowStatus, x.Steps["fix"].Status)
		}
	})

	t.Run("a FAIL the owner's words routed activates it too", func(t *testing.T) {
		at := "2026-11-17T09:00:00.000000001Z"
		d, sp, _, dir := rewindEnv(t, rewindOnlyMidLane, nil, "")
		sp.script = []fakeEntry{done(`{}`), rewindFailCiting(t, dir, at), done(`{}`), done(`{}`), done(`{}`)}
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review", "build", "fix", "review"}) {
			t.Fatalf("dispatched %v, want the --per-owner FAIL to run fix too", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if r := x.RewindStats.Rewinds; len(r) != 1 || r[0].Class != capsule.RewindOwner {
			t.Fatalf("the owner-routed FAIL: %+v", x.RewindStats)
		}
	})

	t.Run("an owner rewind to the target runs it", func(t *testing.T) {
		at := "2026-11-17T09:00:00.000000001Z"
		d, sp, _, dir := rewindEnv(t, rewindOnlyMidLane, nil, "")
		sp.script = []fakeEntry{done(`{}`), rewindTo(t, dir, at, "build"), done(`{}`), done(`{}`), done(`{}`)}
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review", "build", "fix", "review"}) {
			t.Fatalf("dispatched %v, want the owner rewind to run fix too", got)
		}
	})

	t.Run("an activated step runs on every later rewind that resets it", func(t *testing.T) {
		front := "max_rewinds: 2\nsteps:\n  - { id: build }\n  - { id: fix, rewind_only: true }\n  - { id: review, gate: true, on_fail: { target: build } }\n"
		script := []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{}`), gateFAIL(), done(`{}`), done(`{}`), done(`{}`)}
		d, sp, _, dir := rewindEnv(t, front, script, "")
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review", "build", "fix", "review", "build", "fix", "review"}) {
			t.Fatalf("dispatched %v, want fix on both rewound rounds", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.WorkflowStatus != StatusCompleted || x.Steps["fix"].Status != capsule.StepDone {
			t.Fatalf("the activated step ran on both rewinds: %q fix %q", x.WorkflowStatus, x.Steps["fix"].Status)
		}
	})

	t.Run("a project skip wins over the activation", func(t *testing.T) {
		d, sp, log, dir := rewindEnv(t, rewindOnlySkipLane, []fakeEntry{done(`{}`), gateFAIL(), done(`{}`), done(`{}`)}, `["fix"]`+"\n")
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "review", "build", "review"}) {
			t.Fatalf("dispatched %v, want the skipped step never dispatched", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.Steps["fix"].Status != capsule.StepSkipped {
			t.Fatalf("fix %q, want skipped on every round (the project skip wins)", x.Steps["fix"].Status)
		}
		if n := log.count("step_skipped"); n != 2 {
			t.Fatalf("step_skipped events %v, want one per round", log.kinds())
		}
	})
}

// The activation is the durable ledger's: a fresh driver over the same
// execution.json — the restart after a daemon death — dispatches the
// activated step with no replay of the first round.
func TestRewindOnlyActivationSurvivesTheRestart(t *testing.T) {
	d1, _, _, dir := rewindEnv(t, rewindOnlyMidLane, nil, "")
	mustInit(t, d1)
	mustStart(t, d1, "build", 1)
	if err := d1.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1, Result: completedResult("build", 1, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	mustStart(t, d1, "review", 2)
	if err := d1.transition(TransitionRequest{Kind: TransStepResult, StepID: "review", SpawnN: 2, Result: &StepResult{StepID: "review", SpawnN: 2, Outcome: OutcomeGateFailed}}); err != nil {
		t.Fatalf("gate_fail review: %v", err)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if len(x.Steps["fix"].Attempts) != 1 || x.Steps["fix"].Attempts[0].Cause != CauseSupersededByRewind {
		t.Fatalf("the FAIL's rewind archived fix: %+v", x.Steps["fix"].Attempts)
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`), done(`{}`)}}
	d2, err := New(Config{
		WI:       WIInfo{ID: "wi_rewind", CapsuleDir: dir, BundleName: "demo"},
		Spawner:  sp,
		Log:      &eventLog{},
		SpawnN:   3,
		Language: func() string { return stepcall.LangZhCN },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); !reflect.DeepEqual(got, []string{"build", "fix", "review"}) {
		t.Fatalf("dispatched %v, want the restarted driver to run the activated fix", got)
	}
}
