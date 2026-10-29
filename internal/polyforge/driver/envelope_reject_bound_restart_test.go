package driver

// envelope_reject_bound_restart_test.go: B3, the restart half —
// the consecutive-envelope-rejection count is durable, so a restart neither
// resets the bound nor counts an already-rejected archived envelope twice.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// Two rejections before a crash survive the restart: the next rejection is
// the third and blocks, instead of restarting from zero.
func TestEnvelopeRejectBoundSurvivesRestart(t *testing.T) {
	e := newCrashEnv(t, spec("fix"))
	// Two rejected dispatches, then the killed in-flight third.
	e.crashHeld([]fakeEntry{badQuestion(), badQuestion()}, func() {})

	crashed := readExec(t, e.execPath)
	st := crashed.Steps["fix"]
	if st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 3 {
		t.Fatalf("the crash must leave the third dispatch unsettled: %+v", st)
	}
	if st.EnvelopeRejects != 2 {
		t.Fatalf("the two rejections must be durable, got %d", st.EnvelopeRejects)
	}

	sp := &fakeSpawner{script: []fakeEntry{badQuestion(), badQuestion(), badQuestion()}}
	if err := e.driver(sp, 3, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if n := len(sp.spawns); n != 1 {
		t.Fatalf("the restarted driver must block on the third rejection (one more dispatch), got %d", n)
	}
	x := readExec(t, e.execPath)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, FailResultInvalid) {
		t.Fatalf("status %q reason %q, want blocked with %s", x.WorkflowStatus, x.BlockedReason, FailResultInvalid)
	}
	if got := x.Steps["fix"].EnvelopeRejects; got != maxEnvelopeRejects {
		t.Fatalf("the durable count must end at the bound %d, got %d", maxEnvelopeRejects, got)
	}
}

// A restart that re-collects the archived envelope it already rejected does
// not advance the count for it again: the same ordinal counts once.
func TestRejectedEnvelopeRecollectedAfterRestartIsNotCountedTwice(t *testing.T) {
	e := newCrashEnv(t, spec("fix"))
	seedExecution(t, e.execPath, "fix", capsule.StepInProgress, nil)
	e.archived("fix", 1, StepResult{Outcome: OutcomeNeedsInput})

	x := readExec(t, e.execPath)
	x.Steps["fix"].Dispatch = 1
	x.Steps["fix"].EnvelopeRejects = maxEnvelopeRejects - 1
	x.Steps["fix"].EnvelopeRejectSpawnN = 1
	blob, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &fakeSpawner{script: []fakeEntry{badQuestion(), done(`{}`)}}
	if err := e.driver(sp, 1, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	// The re-collected envelope is rejection #2's ordinal: counting it again
	// would block before any new dispatch and leave zero spawns.
	if n := len(sp.spawns); n != 1 {
		t.Fatalf("the re-collected envelope must not be double-counted (want one fresh dispatch), got %d", n)
	}
	got := readExec(t, e.execPath).Steps["fix"]
	if got.EnvelopeRejects != maxEnvelopeRejects || got.EnvelopeRejectSpawnN != 2 {
		t.Fatalf("count/ordinal %d/%d, want %d/2", got.EnvelopeRejects, got.EnvelopeRejectSpawnN, maxEnvelopeRejects)
	}
}
