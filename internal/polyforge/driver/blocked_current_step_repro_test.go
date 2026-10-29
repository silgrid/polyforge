package driver

// blocked_current_step_repro_test.go: C5 — a prompt-
// assembly failure (dispatch-time candidate composition) parks the
// workflow BLOCKED before the target step's step_start, so
// execution.json's current_step still points at the PREVIOUS, already
// done step and the target stays pending. The panel and observe face
// report the wrong step; the owner resume's Supervision reset then
// lands on the completed step.
//
// Written RED against the current driver: blocked with current_step
// "build" (done) while the step that failed to assemble is "fix".

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestCompositionFailureBlocksWithThePositionOnTheFailingStep(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)

	calls := 0
	compose := func() (map[string][]string, error) {
		calls++
		if calls == 1 {
			// the first step's dispatch composes fine
			return map[string][]string{"build": {"pi:m/a"}}, nil
		}
		// the second step's dispatch-time composition fails
		return nil, errors.New("the bundle yaml is unreadable")
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{spec("build"), spec("fix")}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  sp,
		Log:      &eventLog{},
		Compose:  compose,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked", x.WorkflowStatus)
	}
	if !strings.Contains(x.BlockedReason, "fix") {
		t.Fatalf("the blocked reason must name the step that could not be assembled: %q", x.BlockedReason)
	}
	if x.CurrentStep != "fix" {
		t.Fatalf("current_step %q, want \"fix\" — the position must name the dispatch that failed to assemble, not the completed step", x.CurrentStep)
	}
	if st := x.Steps["fix"]; st == nil || st.Status != capsule.StepPending {
		t.Fatalf("the failing step stays pending: %+v", st)
	}
	if st := x.Steps["build"]; st == nil || st.Status != capsule.StepDone {
		t.Fatalf("the completed step stays done: %+v", st)
	}
}
