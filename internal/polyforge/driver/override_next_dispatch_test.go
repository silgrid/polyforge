package driver

// override_next_dispatch_test.go (polyforge#270 u1): the future-dispatch
// predicate the overlay write path asks before it accepts a models/tier
// change on a step that already ran or is running right now, plus the
// regression facts its answer rests on — every dispatch re-composes its
// candidates (so the first composition after a rewind sees a chain written
// in between), a write after that composition does not reach it (Q4 A),
// and the candidate pointer keeps its anchor when the old candidate is
// still on the new chain (Q5 A).

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// fdExec answers an execution record with the given step statuses and
// archived attempts (the rewind-only activation reads the latter).
func fdExec(statuses map[string]string, attempts map[string][]capsule.Attempt) *capsule.Execution {
	ex := &capsule.Execution{WorkflowStatus: capsule.WorkflowStatusRunning, Steps: map[string]*capsule.Step{}}
	for id, status := range statuses {
		st := &capsule.Step{Status: status}
		if a := attempts[id]; len(a) > 0 {
			st.Attempts = a
		}
		ex.Steps[id] = st
	}
	return ex
}

// fdLane answers a lane whose two gates (review, verification) both rewind
// to build — the shape the ticket's own develop lane has.
func fdLane() *Workflow {
	return testLane(3,
		spec("build"),
		gateSpec("review", "build"),
		gateSpec("verification", "build"),
		spec("publish"),
		spec("merge"))
}

// TestFutureDispatchable pins the owner's Q1 B definition: a step counts as
// dispatchable again only when the current position can still reach it
// through the lane's forward order or a rewind of a reachable gate; done
// steps are not generally dispatchable, an effective skip removes a step,
// a rewind-only step needs a rewind that covers it, and a completed
// workflow dispatches nothing.
func TestFutureDispatchable(t *testing.T) {
	rewound := []capsule.Attempt{{Cause: CauseGateFailRewind}}
	for _, tc := range []struct {
		name  string
		wf    *Workflow
		exec  *capsule.Execution
		skips []string
		step  string
		want  bool
	}{
		{
			name: "done build is reachable while a running gate can rewind to it",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepInProgress}, nil),
			step: "build", want: true,
		},
		{
			name: "done build stays reachable through a later gate after review passed",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "verification": capsule.StepInProgress}, nil),
			step: "build", want: true,
		},
		{
			name: "done build past the last rewind is not reachable",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "verification": capsule.StepDone, "publish": capsule.StepInProgress}, nil),
			step: "build", want: false,
		},
		{
			name: "a completed workflow dispatches nothing again",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "verification": capsule.StepDone, "publish": capsule.StepDone, "merge": capsule.StepDone}, nil),
			step: "build", want: false,
		},
		{
			name: "the running current step can be preset for its next dispatch",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepInProgress}, nil),
			step: "build", want: true,
		},
		{
			name: "a pending step ahead is dispatchable",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepInProgress}, nil),
			step: "publish", want: true,
		},
		{
			name: "an effective skip removes a step (and its gate cannot rewind)",
			wf: testLane(3,
				spec("build"),
				gateSpec("review", "build"),
				StepSpec{ID: "verification", Gate: true, Skippable: true, OnFail: &OnFail{Target: "build"}},
				spec("merge")),
			exec:  fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "verification": capsule.StepInProgress}, nil),
			skips: []string{"verification"},
			step:  "verification", want: false,
		},
		{
			name: "a skipped rewind target never counts",
			wf: testLane(3,
				spec("build"),
				StepSpec{ID: "review", Skippable: true},
				gateSpec("audit", "review")),
			exec:  fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "audit": capsule.StepInProgress}, nil),
			skips: []string{"review"},
			step:  "review", want: false,
		},
		{
			name: "a rewind-only step on its first pass is skipped",
			wf: testLane(3,
				spec("build"),
				StepSpec{ID: "audit", RewindOnly: true},
				spec("merge")),
			exec: fdExec(map[string]string{"build": capsule.StepDone}, nil),
			step: "audit", want: false,
		},
		{
			name: "a rewind-only step a reachable gate rewinds over runs",
			wf: testLane(3,
				spec("build"),
				gateSpec("review", "build"),
				StepSpec{ID: "audit", RewindOnly: true},
				spec("merge")),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepInProgress}, nil),
			step: "audit", want: true,
		},
		{
			name: "a rewind-only step already covered by its own archive runs",
			wf: testLane(3,
				spec("build"),
				StepSpec{ID: "audit", RewindOnly: true},
				spec("merge")),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "audit": capsule.StepPending}, map[string][]capsule.Attempt{"audit": rewound}),
			step: "audit", want: true,
		},
		{
			name: "a done publish before the last step has no rewind left",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepDone, "review": capsule.StepDone, "verification": capsule.StepDone, "publish": capsule.StepDone, "merge": capsule.StepInProgress}, nil),
			step: "publish", want: false,
		},
		{
			name: "an unknown step is never dispatchable",
			wf:   fdLane(),
			exec: fdExec(map[string]string{"build": capsule.StepInProgress}, nil),
			step: "nosuch", want: false,
		},
	} {
		if got := tc.wf.FutureDispatchable(tc.step, tc.exec, tc.skips); got != tc.want {
			t.Errorf("%s: FutureDispatchable(%q) = %v, want %v", tc.name, tc.step, got, tc.want)
		}
	}
}

// overrideRewindEnv builds a driver whose Compose closure reads the current
// list at every composition (the owner's edit between dispatches).
func overrideRewindEnv(t *testing.T, list *[]string) (*Driver, string) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_od", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(3, spec("build"), gateSpec("review", "build")),
		Spawner:  &fakeSpawner{},
		Log:      &safeLog{},
		Compose: func() (map[string][]string, error) {
			return map[string][]string{"build": *list, "review": {"pi:prov/rv"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, execPath
}

// overrideRewindToBuild drives build to done and review to its gate FAIL, so
// the next plan dispatch is build's rewind re-entry.
func overrideRewindToBuild(t *testing.T, d *Driver) {
	t.Helper()
	mustInit(t, d)
	if err := d.transition(TransitionRequest{Kind: TransStepStart, StepID: "build", Model: "m", Candidate: "pi:prov/old", SpawnN: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1,
		Result: &StepResult{WIID: "wi_od", StepID: "build", SpawnN: 1, Outcome: OutcomeCompleted, Output: json.RawMessage(`{"summary":"round one"}`)}}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, d, "review", 2)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "review", SpawnN: 2,
		Result: &StepResult{WIID: "wi_od", StepID: "review", SpawnN: 2, Outcome: OutcomeGateFailed, Output: json.RawMessage(structuralVerdict)}}); err != nil {
		t.Fatal(err)
	}
}

// TestOverrideNextDispatchAfterRewind pins the dispatch facts AC1 rests on:
// the re-dispatch after a gate FAIL composes the chain written while the
// step was done; a chain change after that composition waits for the next
// one (Q4 A); and the candidate pointer keeps an old candidate that is
// still on the new chain while a chain that dropped it starts at its head
// (Q5 A). Every expectation is the list the test itself sets.
func TestOverrideNextDispatchAfterRewind(t *testing.T) {
	t.Run("a chain written after the step finished governs its re-dispatch", func(t *testing.T) {
		list := []string{"pi:prov/old"}
		d, _ := overrideRewindEnv(t, &list)
		overrideRewindToBuild(t, d)
		list = []string{"pi:prov/new-b", "pi:prov/new-c"}
		if _, err := d.composeDispatch("build"); err != nil {
			t.Fatal(err)
		}
		sp, _, err := d.prepareDispatch("build")
		if err != nil {
			t.Fatal(err)
		}
		if sp.Candidate != "pi:prov/new-b" || sp.Model != "prov/new-b" {
			t.Fatalf("the re-dispatch candidate %q/%q, want the new chain's head pi:prov/new-b", sp.Candidate, sp.Model)
		}
	})
	t.Run("a chain change after the composition waits for the next one", func(t *testing.T) {
		list := []string{"pi:prov/old"}
		d, _ := overrideRewindEnv(t, &list)
		overrideRewindToBuild(t, d)
		list = []string{"pi:prov/b"}
		if _, err := d.composeDispatch("build"); err != nil {
			t.Fatal(err)
		}
		sp, _, err := d.prepareDispatch("build")
		if err != nil {
			t.Fatal(err)
		}
		if sp.Candidate != "pi:prov/b" {
			t.Fatalf("the dispatch after the composition %q, want pi:prov/b", sp.Candidate)
		}
		list = []string{"pi:prov/c"}
		if sp, _, err = d.prepareDispatch("build"); err != nil {
			t.Fatal(err)
		}
		if sp.Candidate != "pi:prov/b" {
			t.Fatalf("a write after the snapshot changed the dispatch to %q, want the snapshot's pi:prov/b", sp.Candidate)
		}
		if _, err := d.composeDispatch("build"); err != nil {
			t.Fatal(err)
		}
		if sp, _, err = d.prepareDispatch("build"); err != nil {
			t.Fatal(err)
		}
		if sp.Candidate != "pi:prov/c" {
			t.Fatalf("the next composition %q, want pi:prov/c", sp.Candidate)
		}
	})
	t.Run("the anchored candidate survives when it is still on the chain", func(t *testing.T) {
		list := []string{"pi:prov/old"}
		d, _ := overrideRewindEnv(t, &list)
		overrideRewindToBuild(t, d)
		list = []string{"pi:prov/new-b", "pi:prov/old"}
		if _, err := d.composeDispatch("build"); err != nil {
			t.Fatal(err)
		}
		sp, _, err := d.prepareDispatch("build")
		if err != nil {
			t.Fatal(err)
		}
		if sp.Candidate != "pi:prov/old" {
			t.Fatalf("the anchored candidate %q, want the old candidate it still names", sp.Candidate)
		}
	})
}
