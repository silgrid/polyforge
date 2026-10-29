package driver

// phantom_interrupted_repro_test.go: A1 — a dispatch the
// ledger already settled must not gain a second, phantom infra_failure
// archive when a restart lands in the backoff (or the
// step_failed→step_start gap) after the settlement.
//
// The settled shape: step_failed wrote its attempt and cleared
// Step.Dispatch. The successor's Config.Interrupted archive does not
// test Dispatch, so it writes a daemon_restart attempt for a dispatch
// that never existed. prepareDispatch derives the retry's inputs from
// the LAST attempt, so the real ended-without-result fact is masked
// (PriorEndedWithoutResult false instead of true) and the phantom's
// tree hash feeds the fast path.
//
// Written RED against the current driver: the attempts come back
// `ewr, daemon_restart, accepted` and PriorEndedWithoutResult is false.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestSettledDispatchRestartArchivesNoPhantomInterrupted(t *testing.T) {
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "phantom-interrupted-hash", nil }

	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	// A dispatch that ended without a result and was already settled by
	// step_failed: the ledger holds its attempt, Step.Dispatch is 0, and
	// no process is in flight.
	seedExecution(t, execPath, "fix", capsule.StepInProgress, []capsule.Attempt{{
		Cause:         CauseInfraFailure,
		FailureCause:  FailEndedWithoutResult,
		AttemptSeq:    1,
		Candidate:     "pi:eff-a",
		InterruptedAt: "2026-09-20T08:00:00Z",
	}})

	sp := &fakeSpawner{script: []fakeEntry{done(`{"summary":"ok"}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{{ID: "fix", ContextPolicy: "fresh"}}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  sp,
		Log:      &eventLog{},
		Compose:  composeOf(map[string][]string{"fix": {"pi:eff-a"}}),
		// The restart lands after the settlement: nothing is in flight.
		Interrupted: &InterruptedDispatch{Detail: FailDaemonRestart},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	x := readExec(t, execPath)
	st := x.Steps["fix"]
	if st == nil {
		t.Fatal("the step record is missing")
	}
	// One settled EWR attempt + the accepted result: no third, phantom
	// daemon_restart archive for a dispatch that never ran.
	if len(st.Attempts) != 2 {
		t.Fatalf("attempts %+v, want [ended-without-result, accepted]", st.Attempts)
	}
	if st.Attempts[0].FailureCause != FailEndedWithoutResult {
		t.Fatalf("attempt 0 %+v, want the real ended-without-result death", st.Attempts[0])
	}
	if st.Attempts[1].Cause != "accepted" {
		t.Fatalf("attempt 1 %+v, want the accepted result", st.Attempts[1])
	}

	// The retry must still carry the real ended-without-result fact: the
	// short resume depends on it, and the phantom's tree hash
	// must not stand in for the retry's fast-path input.
	if !sp.spawnAt(0).PriorEndedWithoutResult {
		t.Fatalf("the retry must see PriorEndedWithoutResult=true; spawn %+v", sp.spawnAt(0))
	}
	if got := sp.spawnAt(0).RecordedTreeHash; got != "" {
		t.Fatalf("the retry recorded a tree hash %q from a dispatch that never ran", got)
	}
}
