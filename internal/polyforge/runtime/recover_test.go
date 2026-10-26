package runtime

// Recovery-engine regressions (reference.md §6.2) driven with REAL child
// processes as the "agents": transcript present → resume; absent → fresh;
// two consecutive resume failures → fresh; the single consecutive-failures
// ledger (C7) → bounded fail (RTFinished(blocked)).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func recoveryMsg(j *job, reason string) protocol.OBMsg {
	j.mu.Lock()
	spawn := j.spawnN
	j.mu.Unlock()
	return protocol.OBMsg{
		Kind: protocol.OBNeedsRecovery, SpawnN: spawn,
		WorkItem: j.wi, Reason: reason,
	}
}

// TestRecoverBudgetTwoFailuresBoundedFail pins the ruled budget semantics
// (§三/§七): the budget counts FAILED recoveries; the ladder retries until
// MaxRecoveries (default 2) failures, then bounded-fails — never earlier,
// and never on the first failure alone.
func TestRecoverBudgetTwoFailuresBoundedFail(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	wi := "wi_budget"
	j := seedJob(r, wi)
	installOldAgent(t, sp, j)
	sp.mu.Lock()
	sp.failFresh = 2 // no transcript → fresh; both fresh attempts fail
	sp.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))

	// two failed ladder attempts, then the budget is spent
	if n := sp.callCount(); n != 3 { // install + 2 failed fresh derivations
		t.Fatalf("expected two failed derivations before bounded fail, got %d calls", n)
	}
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("expected RTFinished(blocked), got %+v", msg)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != protocol.FinishBlocked {
		t.Fatalf("bounded fail must end the generation blocked, got %+v", hub.lastEnd())
	}
	if r.jobOf(wi) != nil {
		t.Fatal("bounded-failed job must leave the live set")
	}
	if st, _ := j.snapshot(); st != StateDone {
		t.Fatalf("bounded fail must reach DONE, got %s", st)
	}
}

// TestRecoverBudgetReservesAcrossEpisodes: a pre-spent budget (a crash
// mid-recovery left one failure on the record) plus one new failed ladder
// reaches the bound — the storm guard never lets a poisoned WI relaunch
// forever.
func TestRecoverBudgetSpentAfterPersistedFailure(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	wi := "wi_storm"
	j := seedJob(r, wi)
	j.mu.Lock()
	j.recoverFails = 1 // one failed recovery already on the record
	j.mu.Unlock()
	installOldAgent(t, sp, j)
	sp.mu.Lock()
	sp.failFresh = 1
	sp.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))

	if n := sp.callCount(); n != 2 { // install + ONE failed relaunch: the bound hit
		t.Fatalf("a pre-spent budget must bounded-fail after one new failure, got %d calls", n)
	}
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("expected RTFinished(blocked), got %+v", msg)
	}
}

// TestRecoverBudgetResetsOnAcceptedResult: one failed ladder attempt
// followed by a successful relaunch leaves the failed attempt on the
// ledger (C7: only an accepted result resets); once a step's result IS
// accepted, the next death episode starts from zero.
func TestRecoverBudgetResetsOnAcceptedResult(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_reset"
	j := seedJob(r, wi)
	installOldAgent(t, sp, j)
	sp.mu.Lock()
	sp.failFresh = 1 // first ladder attempt fails, the retry goes live
	sp.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))

	if n := sp.callCount(); n != 3 { // install + failed fresh + retry fresh
		t.Fatalf("expected one failed then one successful derivation, got %d calls", n)
	}
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	j.mu.Lock()
	fails := j.recoverFails
	j.mu.Unlock()
	if fails != 1 {
		t.Fatalf("the relaunch alone must not reset the ledger (C7), got %d", fails)
	}
	// the re-dispatched step's result is accepted: real progress resets
	writeAcceptedExecutionState(t, j.capsuleDir, 1)
	// a SECOND death episode starts from a clean budget: one scripted
	// failure is absorbed by a retry, not a bounded fail
	sp.mu.Lock()
	sp.failFresh = 1
	sp.mu.Unlock()
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	msg2 := recvRT(t, toOB)
	if msg2.Kind != protocol.RTRecovered {
		t.Fatalf("the reset ledger must absorb an isolated failure, got %+v", msg2)
	}
	j.mu.Lock()
	fails = j.recoverFails
	j.mu.Unlock()
	if fails != 1 {
		t.Fatalf("the fresh episode must hold exactly its own failed attempt, got %d", fails)
	}
	if h := j.agent; h != nil {
		_ = h.Terminate(2e9)
	}
}

// TestRecoverSingleFreshFailureIsRetriedNotTerminal: ONE failed ladder
// attempt spends one unit of the budget and is RETRIED — a transient
// spawn failure is not a terminal verdict; only the exhausted budget is.
func TestRecoverSingleFreshFailureIsRetriedNotTerminal(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	wi := "wi_freshfail"
	j := seedJob(r, wi) // no transcript → fresh; fresh is scripted to fail once
	installOldAgent(t, sp, j)
	sp.mu.Lock()
	sp.failFresh = 1
	sp.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))

	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTRecovered {
		t.Fatalf("a single fresh failure must be retried, got %+v", msg)
	}
	if hub.endCount() != 0 {
		t.Fatalf("no generation may end on a retried failure, got %+v", hub.lastEnd())
	}
	j.mu.Lock()
	fails := j.recoverFails
	j.mu.Unlock()
	if fails != 1 {
		t.Fatalf("the relaunch must not reset the ledger (C7: only an accepted result does); the failed attempt is on the record, got %d", fails)
	}
	if h := j.agent; h != nil {
		_ = h.Terminate(2e9)
	}
}

// TestRecoverNoProgressRelaunchesBound: the #97 shape — every relaunch
// SUCCEEDS as a spawn and dies within seconds, refiring recovery with no
// accepted step result ever landing. The legacy budget never saw these
// (each relaunch reset it); the single consecutive-failures ledger (C7)
// counts each progress-less recovery relaunch and bounded-fails the WI at
// MaxRecoveries.
func TestRecoverNoProgressRelaunchesBound(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_stormrate"
	j := seedJob(r, wi)
	installOldAgent(t, sp, j)

	// the original claim's death is a trigger, not a recovery failure:
	// the first recovery relaunches from a zero ledger
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("the first recovery must relaunch, got %+v", msg)
	}
	// the second progress-less recovery counts the failed first relaunch
	// (ledger 1 — below the bound of MaxRecoveries=2) and relaunches
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("recovery 2 must still relaunch (ledger below the bound), got %+v", msg)
	}
	// the third progress-less recovery counts the second relaunch and
	// reaches the bound: bounded fail, never a fourth relaunch
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("the third progress-less recovery must bounded-fail, got %+v", msg)
	}
	if n := sp.callCount(); n != 3 { // install + two relaunches, NOT three
		t.Fatalf("the progress-less loop must stop at two relaunches, got %d spawns", n)
	}
}

// TestRecoverAcceptedResultResetsLedger: real progress is the only reset
// (C7) — a recovery that follows an accepted step result starts from a
// zero ledger, so an isolated later failure is never poisoned by earlier
// ones; and the reset is the RESULT, never the relaunch itself.
func TestRecoverAcceptedResultResetsLedger(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_stormgap"
	j := seedJob(r, wi)
	installOldAgent(t, sp, j)

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("first recovery must relaunch, got %+v", msg)
	}
	// a progress-less re-entry counts (ledger 1) — still below the bound
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("recovery 2 must still relaunch below the bound, got %+v", msg)
	}
	// a step's result is accepted (the driver's R14 commit shape): the
	// next recovery entry resets the ledger — the episode is fresh
	writeAcceptedExecutionState(t, j.capsuleDir, 1)
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("a post-progress recovery must relaunch with a reset ledger, got %+v", msg)
	}
	j.mu.Lock()
	fails := j.recoverFails
	j.mu.Unlock()
	if fails != 0 {
		t.Fatalf("an accepted step result must reset the ledger, got %d", fails)
	}
	if h := j.agent; h != nil {
		_ = h.Terminate(2e9)
	}
}

// writeAcceptedExecutionState records n accepted step results in the
// capsule's execution state (the driver's R14 commit shape) — the real
// progress signal the C7 ledger reads.
func writeAcceptedExecutionState(t *testing.T, capDir string, n int) {
	t.Helper()
	dir := filepath.Join(capDir, "runtime-state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	attempts := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		attempts = append(attempts, fmt.Sprintf(
			`{"cause":"accepted","attempt_seq":%d,"started_at":"2026-10-13T10:00:00Z"}`, i))
	}
	blob := `{"work_item_id":"wi","current_step":"grill","workflow_status":"running",` +
		`"steps":{"grill":{"status":"done","attempts":[` + strings.Join(attempts, ",") + `]}}}`
	if err := os.WriteFile(filepath.Join(dir, "execution.json"), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordingHandle is a minimal AgentHandle that records Terminate calls —
// the shape of the driver bridge handle (agentPID 0, teardown through the
// handle itself).
type recordingHandle struct {
	terminated bool
}

func (h *recordingHandle) PID() int                            { return 0 }
func (h *recordingHandle) StartedAt() string                   { return "" }
func (h *recordingHandle) Alive() bool                         { return !h.terminated }
func (h *recordingHandle) Done() bool                          { return h.terminated }
func (h *recordingHandle) Terminate(bound time.Duration) error { h.terminated = true; return nil }

// TestRecoverDriverTerminatesOldHandleAndSeedsFromJournal (the #99
// verification lesson): a driver-path recovery must (1) TERMINATE the old
// bridge handle — the pid-keyed cleanup is a no-op at agentPID 0 and the
// old driver goroutine otherwise survives as a second envelope collector
// on execution.json — and (2) seed the relaunch ordinal past the CAPSULE
// SPAWN JOURNAL max, not the job counter, so the new driver's step
// ordinals never collide with the dead driver's.
func TestRecoverDriverTerminatesOldHandleAndSeedsFromJournal(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_drvreseed"
	j := seedJob(r, wi)
	// journal max 7 > job counter 1: the dead driver derived step
	// ordinals ahead of the relaunch counter
	journal := filepath.Join(j.capsuleDir, "runtime-state", "spawn-journal.jsonl")
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := ""
	for n := 1; n <= 7; n++ {
		lines += fmt.Sprintf(`{"spawn_n":%d,"transcript":"t%d"}%s`, n, n, "\n")
	}
	if err := os.WriteFile(journal, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	old := &recordingHandle{}
	j.mu.Lock()
	j.agent = old
	j.agentPID = 0 // the driver shape
	j.mu.Unlock()
	var gotSpawn int64
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		gotSpawn = in.SpawnN
		return &recordingHandle{}, nil
	}

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("driver recovery must relaunch, got %+v", msg)
	}
	if !old.terminated {
		t.Fatal("the old bridge handle must be terminated on driver recovery")
	}
	if gotSpawn != 8 {
		t.Fatalf("relaunch must seed past the journal max (want 8, got %d)", gotSpawn)
	}
}

// TestRecoverTerminalWorkflowCloses is the #99-finale regression: a
// driver whose workflow reached a terminal state has Run returning nil
// as its COMPLETION signal — the monitor reports a plain exit, and the
// recovery entry must translate the capsule's terminal verdict into the
// WI's close instead of relaunching: completed wraps (RTFinished wrapped,
// SessionEnd wrapped, no relaunch), failed_* fails, blocked hands the
// owner the unblock door. Without the wiring the exit bounced through
// death-recovery into the storm guard and closed the WI blocked.
func TestRecoverTerminalWorkflowCloses(t *testing.T) {
	for _, tc := range []struct {
		status  string
		endKind protocol.RTMsgKind
		wantEnd string
	}{
		{capsule.WorkflowStatusCompleted, protocol.RTFinished, api.StatusWrapped},
		{capsule.WorkflowStatusFailedGate, protocol.RTFinished, api.StatusFailed},
		{capsule.WorkflowStatusBlocked, protocol.RTFinished, api.StatusBlocked},
	} {
		t.Run(tc.status, func(t *testing.T) {
			hub := newFakeHub()
			sp := &scriptedSpawner{}
			r, toOB, _ := newTestRuntime(t, hub, sp)
			wi := "wi_term_" + strings.ReplaceAll(tc.status, "_", "")
			j := seedJob(r, wi)
			// the terminal closes seal-push the capsule (B-7): give it a chain
			initGitCapsule(t, j)
			execDir := filepath.Join(j.capsuleDir, "runtime-state")
			if err := os.MkdirAll(execDir, 0o755); err != nil {
				t.Fatal(err)
			}
			blob, _ := json.Marshal(map[string]string{"workflow_status": tc.status})
			if err := os.WriteFile(filepath.Join(execDir, "execution.json"), blob, 0o644); err != nil {
				t.Fatal(err)
			}
			j.mu.Lock()
			j.agent = &recordingHandle{} // the dead driver handle (pid-less shape)
			j.agentPID = 0
			j.mu.Unlock()
			starterCalls := 0
			r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
				starterCalls++
				return &recordingHandle{}, nil
			}

			r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
			msg := recvRT(t, toOB)
			if msg.Kind != protocol.RTFinished || msg.Reason != tc.wantEnd {
				t.Fatalf("terminal workflow must close as %s, got %+v", tc.wantEnd, msg)
			}
			if starterCalls != 0 {
				t.Fatalf("a terminal workflow must NOT relaunch the driver (starter called %d times)", starterCalls)
			}
			if r.jobOf(wi) != nil {
				t.Fatal("the closed job must leave the live set")
			}
		})
	}
}

// TestRecoverNonTerminalWorkflowRecovers: "running" (and a missing
// execution.json — the legacy WI-Agent shape) fall through to the
// ordinary recovery machinery.
func TestRecoverNonTerminalWorkflowRecovers(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	wi := "wi_term_running"
	j := seedJob(r, wi)
	j.mu.Lock()
	j.agent = &recordingHandle{}
	j.agentPID = 0
	j.mu.Unlock()
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		return &recordingHandle{}, nil
	}
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("a running workflow must recover normally, got %+v", msg)
	}
	if h := j.agent; h != nil {
		_ = h.Terminate(2e9)
	}
}

// TestRecoverDeadOldAgentWithOrphans drives the recovery against an old
// agent that DIED leaving orphaned children in its group: the six-layer
// cleanup must prove the tree empty before the re-derivation starts.
func TestRecoverDeadOldAgentWithOrphans(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_orphans"
	j := seedJob(r, wi)

	// an "agent" that forks a long-lived child and dies without reaping it
	old, err := spawnOrphaningSleeper(t, wi)
	if err != nil {
		t.Fatal(err)
	}
	orphan := old.orphanPID
	j.mu.Lock()
	j.agent = old.handle
	j.agentPID = old.handle.PID()
	j.agentStartedAt = old.handle.StartedAt()
	j.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))

	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	waitDead(t, orphan) // the orphan died with the tree, not just the leader
	if h := j.agent; h != nil {
		_ = h.Terminate(2e9)
	}
}

// TestRecoverLedgerExemptsDaemonRestart: restarts and attaches are
// lifecycle events, not failures (v2-converge §2.3) — any number of them
// leaves the WI running and the consecutive-failures ledger untouched,
// while progress-less death recoveries still bounded-fail afterwards.
func TestRecoverLedgerExemptsDaemonRestart(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	j := seedJob(r, "wi_restartloop")
	installOldAgent(t, sp, j)

	for i, cause := range []string{causeDaemonRestart, causeOperatorAttach, causeDaemonRestart, causeDaemonRestart} {
		r.recoverOp(context.Background(), j, cause, "")
		if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
			t.Fatalf("restart-class recovery %d must relaunch, got %+v", i+1, msg)
		}
	}
	j.mu.Lock()
	fails := j.recoverFails
	j.mu.Unlock()
	if fails != 0 {
		t.Fatalf("restart causes must not touch the consecutive-failures ledger, got %d", fails)
	}
	// progress-less death recoveries still bound: the restart relaunch's
	// own lack of progress counts (it was a recovery relaunch), and the
	// next one reaches MaxRecoveries
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("the first death recovery must relaunch (ledger below the bound), got %+v", msg)
	}
	r.recoverOp(context.Background(), j, causeDaemonRestart, "") // exempt: no count, no reset
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("got %+v", msg)
	}
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("progress-less recoveries must still bounded-fail after restarts, got %+v", msg)
	}
}
