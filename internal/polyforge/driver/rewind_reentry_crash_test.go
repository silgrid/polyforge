package driver

// rewind_reentry_crash_test.go: item 6 — a crash after the
// rewind target's step_start (before its spawn journal line) must keep the
// first dispatch of the new round's shape. Since the
// target's shape IS that resume (every rewind target that is not a gate
// resumes its previous round): the restart keeps it a rewind resume. The
// FAIL itself is no dispatch input: it is the gate's section of
// artifacts/handoff.md, which the step reads (workflow contract 4).

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

var errSpawnFailed = errors.New("harness binary missing")

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

// A crash after a rewind target's step_start reaches the restarted
// driver through the recovery lane (Config.Interrupted): the daemon_restart
// archive lands on the same unsettled step, so the restoration must key on
// the latest NON-infra_failure attempt, not the raw last one.
func TestInterruptedRestartAfterRewindStepStartKeepsTheRewindResume(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt *InterruptedDispatch
	}{
		{"daemon restart", &InterruptedDispatch{Detail: FailDaemonRestart}},
		{"driver relaunch", &InterruptedDispatch{Detail: FailDriverRelaunch, BookWall: true}},
		// no Interrupted: Run takes the claim re-entry branch
		// (TransClaimReentry / applyClaimReentryLocked), a distinct path
		{"claim re-entry (no Interrupted)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
			// build (spawn 1) then review's FAIL verdict; the held dispatch
			// is the rewound build's spawn 3
			e.crashHeld([]fakeEntry{done(`{}`), gateFAIL()}, func() {})
			if st := readExec(t, e.execPath).Steps["build"]; st == nil || st.Status != capsule.StepInProgress || st.Dispatch != 3 {
				t.Fatalf("the crash must leave the rewind target's first dispatch unsettled: %+v", st)
			}

			sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
			if err := e.driverWithInterrupted(sp, 3, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "build" {
				t.Fatalf("the restart must dispatch the rewind target, got %v", sp.dispatched())
			}
			if re := sp.spawnAt(0); !re.Resume || !re.RewindResume || re.Fresh {
				t.Fatalf("the first dispatch of the new round stays the rewind resume of the previous round: %v", re)
			}
			if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted || x.RewindStats.Count != 1 {
				t.Fatalf("the restarted lane must complete on the one rewind slot: %q %+v", x.WorkflowStatus, x.RewindStats)
			}
		})
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

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
	if err := e.driverWithInterrupted(sp, 4, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 {
		t.Fatalf("the restart must dispatch the rewind target, got %v", sp.dispatched())
	}
	if re := sp.spawnAt(0); !re.Resume || !re.RewindResume {
		t.Fatalf("a failed pre-journal spawn never created a new conversation: the re-entry still resumes the previous round: %v", re)
	}
}

// Once a dispatch of the new round IS journaled, the ordinary resume is
// correct: the restart resumes that dispatch's conversation.
func TestInterruptedRestartAfterJournalledRewindStepIsAnOrdinaryResume(t *testing.T) {
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

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
	if err := e.driverWithInterrupted(sp, 3, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || !sp.spawnAt(0).Resume {
		t.Fatalf("a journaled new-round dispatch is an ordinary resume: %v", sp.spawnAt(0))
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
			sp := &fakeSpawner{script: []fakeEntry{done(`{"verdict":"pass"}`)}}
			if err := e.driverWithInterrupted(sp, 4, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "review" {
				t.Fatalf("the restart must dispatch the gate, got %v", sp.dispatched())
			}
			if sp.spawnAt(0).Resume {
				t.Fatalf("the gate's re-entry must not resume the session that delivered the FAIL: %v", sp.spawnAt(0))
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

	sp := &fakeSpawner{script: []fakeEntry{done(`{"verdict":"pass"}`)}}
	if err := e.driverWithInterrupted(sp, 4, &InterruptedDispatch{Detail: FailDaemonRestart}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "review" {
		t.Fatalf("the restart must dispatch the gate, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume {
		t.Fatalf("the gate must re-enter fresh: %v", sp.spawnAt(0))
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
			sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
			if err := e.driverWithInterrupted(sp, 5, tc.interrupt).Run(context.Background()); err != nil {
				t.Fatalf("restarted Run: %v", err)
			}
			if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "check" {
				t.Fatalf("the restart must dispatch the superseded step, got %v", sp.dispatched())
			}
			if sp.spawnAt(0).Resume {
				t.Fatalf("the superseded step must not resume the previous round's conversation: %v", sp.spawnAt(0))
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

	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`)}}
	if err := e.driverWithInterrupted(sp, 5, &InterruptedDispatch{Detail: FailDriverRelaunch}).Run(context.Background()); err != nil {
		t.Fatalf("restarted Run: %v", err)
	}
	if len(sp.spawns) == 0 || sp.spawnAt(0).StepID != "check" {
		t.Fatalf("the restart must dispatch the superseded step, got %v", sp.dispatched())
	}
	if sp.spawnAt(0).Resume {
		t.Fatalf("the superseded step must re-enter fresh: %v", sp.spawnAt(0))
	}
	if x := readExec(t, e.execPath); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the restarted lane must complete: %q", x.WorkflowStatus)
	}
}
