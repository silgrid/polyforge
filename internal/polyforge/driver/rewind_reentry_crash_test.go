package driver

// rewind_reentry_crash_test.go: item 6 — a crash after the
// rewind target's step_start (before its spawn journal line) must keep the
// first dispatch of the new round fresh and carrying the FAIL verdict.
// Without the durable ledger cause the restart resumed the PRE-rewind
// conversation with no rewind input.

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
)

var errSpawnFailed = errors.New("harness binary missing")

func TestCrashAfterFreshRewindStepStartKeepsFreshAndVerdict(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	// build (spawn 1) then review's FAIL verdict; the held dispatch is the
	// rewound build's spawn 3.
	e.crashHeld([]fakeEntry{done(`{}`), gateFAIL()}, func() {})

	crashed := readExec(t, e.execPath)
	if st := crashed.Steps["build"]; st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 3 {
		t.Fatalf("the crash must leave the rewind target's first dispatch unsettled: %+v", st)
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driver(sp, 3, nil).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "build" {
		t.Fatalf("the restart must dispatch the rewind target, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume {
		t.Fatalf("the first dispatch of the new round must not resume the pre-rewind conversation: %v", sp.spawnAt(0))
	}
	ri := sp.spawnAt(0).RewindInput
	if ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("the restart must carry the FAIL verdict to the target, got %+v", ri)
	}
	x := readExec(t, e.execPath)
	if x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
		t.Fatalf("the restarted lane must complete on the one rewind slot: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
}

// driverWithInterrupted builds the restarted driver exactly as the recovery
// lane does: Config.Interrupted set (daemon_restart or driver_relaunch), so
// Run first archives an audit-only infra_failure attempt before planning.
func (e *crashEnv) driverWithInterrupted(sp StepSpawner, spawnN int64, in *InterruptedDispatch) *Driver {
	e.t.Helper()
	d, err := New(Config{
		WI:          WIInfo{ID: "wi_crash", CapsuleDir: e.dir, BundleName: "b"},
		ExecPath:    e.execPath,
		Workflow:    e.wf,
		Spawner:     sp,
		SpawnN:      spawnN,
		Interrupted: in,
	})
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	return d
}

// A crash after a fresh rewind target's step_start reaches the restarted
// driver through the recovery lane (Config.Interrupted): the daemon_restart
// archive lands on the same unsettled step, so the restoration must key on
// the latest NON-infra_failure attempt, not the raw last one.
func TestInterruptedRestartAfterFreshRewindStepStartKeepsFreshAndVerdict(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt *InterruptedDispatch
	}{
		{"daemon restart", &InterruptedDispatch{Detail: FailDaemonRestart}},
		{"driver relaunch", &InterruptedDispatch{Detail: FailDriverRelaunch, BookWall: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
			e.crashHeld([]fakeEntry{done(`{}`), gateFAIL()}, func() {})

			sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
			if err := e.driverWithInterrupted(sp, 3, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "build" {
				t.Fatalf("the restart must dispatch the rewind target, got %v", sp.dispatched())
			}
			if sp.spawnAt(0).Resume {
				t.Fatalf("the first dispatch of the new round must not resume the pre-rewind conversation: %v", sp.spawnAt(0))
			}
			ri := sp.spawnAt(0).RewindInput
			if ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
				t.Fatalf("the restart must carry the FAIL verdict to the target, got %+v", ri)
			}
			if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
				t.Fatalf("the restarted lane must complete on the one rewind slot: %q %+v", x.WorkflowStatus, x.RewindStats)
			}
		})
	}
}

// The same crash path for a LIGHT re-entry: the daemon_restart attempt must
// not hide the light cause, and the restored order keeps the resume plus the
// light note.
func TestInterruptedRestartAfterLightRewindStepStartKeepsTheLightOrder(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	e.crashHeld([]fakeEntry{done(`{}`), lightReviewFAIL()}, func() {}) // the held dispatch is the light re-entry

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driverWithInterrupted(sp, 3, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 {
		t.Fatalf("the restart must dispatch the light target, got %v", sp.dispatched())
	}
	if !sp.spawnAt(0).Resume || sp.spawnAt(0).Fresh {
		t.Fatalf("the light re-entry must stay a resume: %v", sp.spawnAt(0))
	}
	ri := sp.spawnAt(0).RewindInput
	if ri == nil || ri.Note != LightRewindNote || !strings.Contains(ri.Reason, "comment wording only") {
		t.Fatalf("the restart must carry the light rewind input: %+v", ri)
	}
	if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
		t.Fatalf("the restarted lane must complete on the one light slot: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
}

// A spawn failure before the journal line settles the dispatch with a
// step_failed attempt (Dispatch=0, no journal line), and the recovery lane's
// restart must still restore the rewind order for the next dispatch.
func TestInterruptedRestartAfterPreJournalSpawnFailureKeepsTheRewindOrder(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	e.crashHeld([]fakeEntry{done(`{}`), gateFAIL()}, func() {})

	// The exact durable shape a pre-journal SpawnStep failure writes: the
	// step_failed attempt, Dispatch back to 0, no journal line for ordinal 4.
	d0 := e.driver(&fakeSpawner{}, 4, nil)
	if stop, err := d0.failDispatch(context.Background(), "build", 4, time.Now(), time.Now(), FailExitOther, -1, errSpawnFailed.Error(), nil); stop || err != nil {
		t.Fatalf("the pre-journal spawn failure write: stop=%v err=%v", stop, err)
	}
	if x := readExec(t, e.execPath); x.Steps["build"].Dispatch != 0 {
		t.Fatalf("the spawn failure must settle the dispatch: %+v", x.Steps["build"])
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driverWithInterrupted(sp, 4, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 {
		t.Fatalf("the restart must dispatch the rewind target, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume {
		t.Fatalf("a failed pre-journal spawn never created the new conversation: %v", sp.spawnAt(0))
	}
	ri := sp.spawnAt(0).RewindInput
	if ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("the restart must carry the FAIL verdict to the target, got %+v", ri)
	}
}

// Once a dispatch of the new round IS journaled, the ordinary resume is
// correct — and the round is not over yet, so that resume still carries the
// rewind verdict (it stays in pending_rewind_input until the
// step is accepted, instead of being consumed by the first dispatch).
func TestInterruptedRestartAfterJournalledRewindStepResumesWithVerdict(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	e.crashHeld([]fakeEntry{done(`{}`), gateFAIL()}, func() {})

	x := readExec(t, e.execPath)
	at, err := time.Parse(time.RFC3339Nano, x.Steps["build"].Attempts[len(x.Steps["build"].Attempts)-1].ArchivedAt)
	if err != nil {
		t.Fatalf("the rewind archive stamp must parse: %v", err)
	}
	entry := capsule.SpawnJournalEntry{
		At: at.Add(time.Second).UTC().Format(time.RFC3339Nano), WorkItem: "wi_crash",
		StepID: "build", SpawnN: 3, Transcript: filepath.Join(e.dir, "session", "build-3.jsonl"),
	}
	blob, _ := json.Marshal(entry)
	if err := os.MkdirAll(filepath.Join(e.dir, capsule.DirRuntimeState), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, capsule.DirRuntimeState, capsule.SpawnJournalFile), append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driverWithInterrupted(sp, 3, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	ri := sp.spawnAt(0).RewindInput
	if len(sp.spawns) == 0 || !sp.spawnAt(0).Resume ||
		ri == nil || !strings.Contains(ri.Reason, "FAIL: tests red") {
		t.Fatalf("a journaled new-round dispatch is an ordinary resume that still carries the round's verdict: %v", sp.spawnAt(0))
	}
}

// The gate that delivered the FAIL re-enters in fresh form (01 §6.2 item
// 6), and its order is consumed at the gate's step_start exactly like the
// target's: a crash before the journal line must not resume the session
// that delivered the FAIL (it could re-submit a stale verdict).
func TestInterruptedRestartAfterGateReentryStepStartIsFresh(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt *InterruptedDispatch
	}{
		{"daemon restart", &InterruptedDispatch{Detail: FailDaemonRestart}},
		{"driver relaunch", &InterruptedDispatch{Detail: FailDriverRelaunch, BookWall: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
			e.crashHeld([]fakeEntry{done(`{}`), gateFAIL(), done(`{}`)}, func() {})

			if st := readExec(t, e.execPath).Steps["review"]; st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 4 {
				t.Fatalf("the crash must leave the gate's first new-round dispatch unsettled: %+v", st)
			}
			sp := &fakeSpawner{script: []fakeEntry{done(`{"verdict":"PASS"}`)}}
			if err := e.driverWithInterrupted(sp, 4, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "review" {
				t.Fatalf("the restart must dispatch the gate, got %v", sp.dispatched())
			}
			if sp.spawnAt(0).Resume {
				t.Fatalf("the gate's re-entry must not resume the session that delivered the FAIL: %v", sp.spawnAt(0))
			}
			if sp.spawnAt(0).RewindInput != nil {
				t.Fatalf("the gate itself carries no rewind verdict: %+v", sp.spawnAt(0).RewindInput)
			}
			if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
				t.Fatalf("the restarted lane must complete on the one rewind slot: %q %+v", x.WorkflowStatus, x.RewindStats)
			}
		})
	}
}

// A pre-journal spawn failure has no journal line either, and its
// step_failed decision is not fresh: the gate's next dispatch must still
// start without resume.
func TestPreJournalSpawnFailureAfterGateStepStartStaysFresh(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	e.crashHeld([]fakeEntry{done(`{}`), gateFAIL(), done(`{}`)}, func() {})

	d0 := e.driver(&fakeSpawner{}, 4, nil)
	if stop, err := d0.failDispatch(context.Background(), "review", 4, time.Now(), time.Now(), FailExitOther, -1, errSpawnFailed.Error(), nil); stop || err != nil {
		t.Fatalf("the pre-journal spawn failure write: stop=%v err=%v", stop, err)
	}
	if st := readExec(t, e.execPath).Steps["review"]; st.Dispatch != 0 || st.Supervision == nil || st.Supervision.NextDispatch != "" {
		t.Fatalf("the non-fresh spawn failure must settle the gate with no fresh order: %+v", st)
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{"verdict":"PASS"}`)}}
	if err := e.driverWithInterrupted(sp, 4, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "review" {
		t.Fatalf("the restart must dispatch the gate, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume || sp.spawnAt(0).RewindInput != nil {
		t.Fatalf("the gate must re-enter fresh without a verdict: %v %+v", sp.spawnAt(0), sp.spawnAt(0).RewindInput)
	}
	if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the restarted lane must complete: %q", x.WorkflowStatus)
	}
}

// A step between the target and the gate was archived superseded_by_rewind:
// its first new-round dispatch is fresh only because it is pending, so a
// crash after its step_start must not resume the previous round's
// conversation either.
func TestInterruptedRestartAfterSupersededStepStartIsFresh(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt *InterruptedDispatch
	}{
		{"daemon restart", &InterruptedDispatch{Detail: FailDaemonRestart}},
		{"driver relaunch", &InterruptedDispatch{Detail: FailDriverRelaunch, BookWall: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCrashEnv(t, spec("build"), spec("check"), gateSpec("review", "build"))
			e.crashHeld([]fakeEntry{done(`{}`), done(`{}`), gateFAIL(), done(`{}`)}, func() {})

			if st := readExec(t, e.execPath).Steps["check"]; st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 5 {
				t.Fatalf("the crash must leave the superseded step's first new-round dispatch unsettled: %+v", st)
			}
			sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
			if err := e.driverWithInterrupted(sp, 5, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "check" {
				t.Fatalf("the restart must dispatch the superseded step, got %v", sp.dispatched())
			}
			if sp.spawnAt(0).Resume {
				t.Fatalf("the superseded step must not resume the previous round's conversation: %v", sp.spawnAt(0))
			}
			if sp.spawnAt(0).RewindInput != nil {
				t.Fatalf("only the rewind target carries the verdict: %+v", sp.spawnAt(0).RewindInput)
			}
			if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
				t.Fatalf("the restarted lane must complete on the one rewind slot: %q %+v", x.WorkflowStatus, x.RewindStats)
			}
		})
	}
}

// The superseded step's pre-journal spawn failure path, symmetric with the
// gate's: no journal line and no fresh order, still no resume.
func TestPreJournalSpawnFailureAfterSupersededStepStartStaysFresh(t *testing.T) {
	e := newCrashEnv(t, spec("build"), spec("check"), gateSpec("review", "build"))
	e.crashHeld([]fakeEntry{done(`{}`), done(`{}`), gateFAIL(), done(`{}`)}, func() {})

	d0 := e.driver(&fakeSpawner{}, 5, nil)
	if stop, err := d0.failDispatch(context.Background(), "check", 5, time.Now(), time.Now(), FailExitOther, -1, errSpawnFailed.Error(), nil); stop || err != nil {
		t.Fatalf("the pre-journal spawn failure write: stop=%v err=%v", stop, err)
	}
	if st := readExec(t, e.execPath).Steps["check"]; st.Dispatch != 0 || st.Supervision == nil || st.Supervision.NextDispatch != "" {
		t.Fatalf("the non-fresh spawn failure must settle the superseded step with no fresh order: %+v", st)
	}

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"PASS"}`)}}
	if err := e.driverWithInterrupted(sp, 5, &InterruptedDispatch{Detail: FailDriverRelaunch}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "check" {
		t.Fatalf("the restart must dispatch the superseded step, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume || sp.spawnAt(0).RewindInput != nil {
		t.Fatalf("the superseded step must re-enter fresh without a verdict: %v %+v", sp.spawnAt(0), sp.spawnAt(0).RewindInput)
	}
	if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the restarted lane must complete: %q", x.WorkflowStatus)
	}
}
