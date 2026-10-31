// recover.go: the recovery engine (reference.md §6.2) and the recovery
// lifecycle entry (Recover). Every relaunch kills the agent tree, commits
// the dirty capsule and starts a fresh in-process driver derivation that
// continues from execution.json (no transcript resume→fresh cascade,
// polyforge#106; a step's own retry shape is the driver's business):
//
//	relaunch failing → one FAILED recovery: the budget counts failures and
//	  a successful recovery RESETS it. After MaxRecoveries (default 2)
//	  consecutive failed recoveries the WI terminates
//	  RTFinished(blocked) — the consecutive-failures ledger: never a
//	  third relaunch without an accepted step result
//	  attempt after two failed ones, and never a poisoned budget after
//	  a healthy relaunch.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// cleanupBound bounds one teardown's waits inside the six-layer cleanup.
// A variable (not a const) so the recovery park's bounded wait is testable
// without a 20s test; production never changes it.
var cleanupBound = 10 * time.Second

// recoverCore is the recovery sequence, run by the work item's actor
// across its pushes and the relaunch. cause
// names the recovery trigger (the interrupted-dispatch archive's class
// input, and the consecutive-failures ledger's exempt-cause key);
// detail is the observe
// lane's free-text elaboration ("" when none).
func (r *Runtime) recoverCore(ctx context.Context, j *job, cause, detail string) {
	if j.closeParked() {
		return // a parked close or release owns the job until the old Run exits
	}
	if err := j.transition(StateRecovering); err != nil {
		return // lost the race to SEALING/DONE: the fence did its job
	}
	// Mid-flight vanish-after-start guard (#103): the engine below
	// re-materializes capsule residue (work-item file, bundle set) and
	// re-derives agents — against an ABSENT capsule that is a silent
	// rebuild, exactly what the contract forbids for unpublished local
	// state. Refuse instead with the loud bounded-fail verdict naming
	// the WI and the missing path: the operator restores the capsule,
	// then unblocks (M2 — the human is the only remediation door).
	if err := r.requireMidflightCapsule(j.wi, j.project, j.capsuleDir); err != nil {
		if r.cfg.EventTap != nil {
			r.cfg.EventTap(j.wi, "missing_midflight_capsule", map[string]any{
				"capsule_dir": j.capsuleDir,
				"cause":       cause,
				"note":        "mid-flight capsule absent at the active workspace; no rebuild from remote — restore the capsule, then unblock",
			})
		}
		fmt.Fprintf(os.Stderr, "runtime: %s: %v\n", j.wi, err)
		r.boundedFail(ctx, j, err.Error())
		return
	}
	// v2 terminal wiring (the #99 finale): Run returning nil IS the
	// driver's completion signal, and the
	// monitor's onExit rightly reports "agent process exited" without
	// knowing the difference. Before any recovery machinery runs, the
	// capsule's terminal workflow_status is translated into the WI's
	// close: completed wraps, failed fails, blocked hands the WI to its
	// owner (the hub's blocked verdict is the unblock door). Without
	// this the terminal exit bounced through death-recovery and closed
	// the WI as blocked instead of wrapped.
	switch capsuleWorkflowStatus(j.capsuleDir) {
	case capsule.WorkflowStatusCompleted:
		fmt.Fprintf(os.Stderr, "runtime: %s: driver workflow completed — wrapping (recovery cause %q is the completion signal, not a death)\n", j.wi, cause)
		// The ordinary seal push runs: the driver does no git, and
		// the observe flush that would carry the terminal execution.json
		// may have failed or timed out. On a clean tree CommitPush only
		// re-pushes HEAD; a failed push degrades the close to blocked like
		// every other terminal close.
		if err := r.stopOp(ctx, j, protocol.FinishWrapped, false, ""); err != nil {
			logTerminalClose(j, "wrap", err)
		}
		return
	case capsule.WorkflowStatusFailedGate, capsule.WorkflowStatusFailedRewindExhausted:
		fmt.Fprintf(os.Stderr, "runtime: %s: driver workflow failed (%s) — closing failed (recovery cause %q)\n", j.wi, capsuleWorkflowStatus(j.capsuleDir), cause)
		if err := r.stopOp(ctx, j, protocol.FinishFailed, false, ""); err != nil {
			logTerminalClose(j, "fail close", err)
		}
		return
	case capsule.WorkflowStatusBlocked:
		fmt.Fprintf(os.Stderr, "runtime: %s: driver workflow blocked on an incomplete result — the owner holds the next move (cause %q)\n", j.wi, cause)
		// The parked verdict's chain durability (polyforge#116): the bounded
		// close below drops the job with NO seal — unlike the completed and
		// failed branches, nothing publishes the blocked execution.json — so
		// the verdict would reach the chain only through the monitor's next
		// tick, a race the owner's resume face must not inherit (a later
		// re-materialization would clone pre-blocked state and silently
		// discard the verdict — the workspace where the same re-claim both
		// wedges and escapes depending on a timing race). Seal the parked
		// capsule explicitly: kill the (already returned) driver's tree, then
		// commit+push the parked state — journal, step output and the blocked
		// verdict together. The §6.4 owner moves (resume / cancel) and
		// every re-materialization read exactly this state.
		r.boundedFail(ctx, j, blockedWorkflowNote(j.capsuleDir))
		return
	}
	// The single recovery ledger (C7): ONE counter of consecutive recovery
	// failures. A recovery whose relaunch made no real progress — no step
	// result accepted since — is a failure, counted at the NEXT re-entry
	// (that re-entry is its proof); a re-entry that follows an accepted
	// result resets the counter (real progress is the only reset). The
	// progress signal is the capsule execution state's accepted-result
	// attempts (the driver's R14 commit points — the same surface the
	// observe lane polls); a dead agent writes nothing, so a dying relaunch
	// can never fake its way out of the ledger. A daemon restart / operator
	// attach is NOT a failure (v2-converge §2.3): those causes are exempt
	// from the ledger entirely — neither counted nor resetting — so a deploy
	// retry or a systemd restart loop cannot bounded-fail every running WI.
	if !LifecycleCause(cause) {
		accepted := acceptedResultCount(j.capsuleDir) // disk read: outside the job's mutex
		j.mu.Lock()
		switch {
		case accepted > j.acceptedAtRelaunch:
			// real progress since the last relaunch (a step's result was
			// accepted): the consecutive ledger resets (C7 — real
			// progress is the only reset).
			j.recoverFails = 0
		case j.lastRelaunchRecovery:
			// no accepted result since the last RECOVERY relaunch: that
			// relaunch failed to stabilize the WI — one consecutive
			// unit. (The original claim's death is a trigger, not a
			// recovery failure; only a recovery's own relaunch can fail.)
			j.recoverFails++
		}
		j.acceptedAtRelaunch = accepted
		j.lastRelaunchRecovery = false // the current relaunch re-arms it on success
		fails := j.recoverFails
		j.mu.Unlock()
		if fails >= r.cfg.MaxRecoveries {
			r.boundedFail(ctx, j, fmt.Sprintf(
				"recovery budget exhausted (%d consecutive recovery failures without an accepted step result, latest cause %q)",
				fails, cause))
			return
		}
	}
	// Kill the old tree first (§一 Recover): six layers, no writer may
	// survive next to the re-derivation. A handle whose Run is still alive
	// after its bounded teardown is a live writer: the relaunch must not
	// happen next to it. Park the WI in RECOVERING with the handle in place
	// and re-enter recovery once that Run exits (parkRecovery); the
	// persisted record also resumes at the next daemon start, where no old
	// Run exists.
	if err := r.killAgentTree(j); err != nil {
		j.mu.Lock()
		h := j.agent
		j.mu.Unlock()
		r.persistLog(j.wi)
		r.parkRecovery(j, h, cause, detail)
		return
	}

	// commit-dirty-on-recover (§六): the dead agent's unflushed capsule
	// state is committed and pushed BEFORE the new instance can touch the
	// worktree. Best-effort by design: a failed push surfaces at the next
	// boundary; the local commit already keeps the bytes.
	//
	// F1 repair: a capsule without runtime-state/work-item.json (a pre-F1
	// scene) gets it re-materialized before the next derivation wakes up
	// blind to its own goal (an aihub read when the claim snapshot is
	// gone). Best-effort, idempotent.
	// A push the hub refused because the session no longer holds the work
	// item stops it here (the lazy holder check of a restart, too).
	if _, perr := commitDirty(r, j); r.refusedNotOurs(j, perr) {
		return
	}
	r.ensureWorkItemFile(j)

	// The bundle is pinned at claim (v2-converge §2.4): recovery relaunches
	// from the capsule's own materialized snapshot and never re-pins or
	// re-materializes from the current checkout. A missing or untrusted
	// snapshot is a loud terminal condition, not a silent rebuild.
	if r.cfg.WorkflowsDir != "" && j.bundleName != "" && !r.materializationComplete(j) {
		r.boundedFail(ctx, j, fmt.Sprintf(
			"the capsule's materialized snapshot of bundle %s is missing or incomplete; recovery never re-pins to the current checkout (the bundle is pinned at claim)",
			j.bundleName))
		return
	}

	// The engine binding is the driver or NOTHING (polyforge#106): there
	// is no other engine, so a nil starter is a misassembled runtime, not
	// an engine choice — refused loudly, never fallen back from.
	if r.cfg.AgentStarter == nil {
		r.boundedFail(ctx, j, "runtime misassembly: no execution engine bound (Config.AgentStarter is nil) — the legacy in-package WI-Agent engine was deleted (polyforge#106); the daemon binds the driver bridge starter unconditionally at assembly")
		return
	}

	// The ladder's own spend (§三): each FAILED relaunch attempt is one
	// consecutive recovery failure on the SAME single counter (C7); only
	// an accepted step result resets it (the entry block above). Only
	// when the counter reaches MaxRecoveries does the WI bounded-fail —
	// never merely because it has recovered before.
	//
	// The interrupted dispatch (the death that forced this recovery) is
	// archived by the driver before the re-dispatch, which composes the
	// candidate lists live (polyforge#33) and recomputes the pointer from
	// the persisted step candidate plus the archive. The archive signal is the raw cause — the bridge
	// classifies it and filters operator-shaped stops (a deliberate stop is
	// not a model verdict).
	interruptedCause := cause
	for {
		// Restart the Go Driver via the bound engine starter (it reads
		// execution.json and continues from the current step; the
		// recovery steps above — killTree, commitDirty — already ran).
		if r.stopped(ctx) {
			// the shutdown: no agent starts behind it. The job stays
			// RECOVERING with no agent; the next daemon start recovers
			// the persisted record.
			r.persistLog(j.wi)
			fmt.Fprintf(os.Stderr, "runtime: %s: recovery stopped by the shutdown before its relaunch (the next start recovers it)\n", j.wi)
			return
		}
		h, err := r.startEngine(ctx, j, interruptedCause)
		if errors.Is(err, errEngineStopped) {
			// the record stays RECOVERING for the next start
			r.persistLog(j.wi)
			return
		}
		if err == nil {
			r.persistLog(j.wi) // persisted with the advanced counter (crash-safe)
			r.recovered(j, h, cause)
			return
		}
		fmt.Fprintf(os.Stderr, "runtime: %s: engine relaunch failed: %v\n", j.wi, err)
		j.mu.Lock()
		j.recoverFails++
		fails := j.recoverFails
		j.mu.Unlock()
		r.persistLog(j.wi)
		if fails >= r.cfg.MaxRecoveries {
			r.boundedFail(ctx, j, fmt.Sprintf("recovery budget exhausted (%d consecutive recovery failures, cause %q)", fails, cause))
			return
		}
		fmt.Fprintf(os.Stderr, "runtime: %s: recovery failed (%d/%d), retrying the ladder\n", j.wi, fails, r.cfg.MaxRecoveries)
	}
}

// logTerminalClose logs a terminal close (the completed workflow's wrap,
// the failed one's fail close) that did not finish here: parked on a
// driver Run that outlived its teardown — it finishes by itself once that
// Run exits — or failed, the record kept sealing for the retry.
func logTerminalClose(j *job, what string, err error) {
	if j.closeParked() {
		fmt.Fprintf(os.Stderr, "runtime: %s: terminal %s parked: the driver Run outlived its bounded teardown; the close finishes once that Run exits: %v\n", j.wi, what, err)
		return
	}
	fmt.Fprintf(os.Stderr, "runtime: %s: terminal %s failed (record kept sealing, retrying): %v\n", j.wi, what, err)
}

// errEngineStopped is startEngine's answer when the shutdown came while
// the engine started: it was torn down again and nothing runs.
var errEngineStopped = errors.New("runtime: the shutdown came while the engine started")

// startEngine starts the execution engine for j as a fresh derivation
// (B2) and binds its handle. The counter continues past the capsule
// spawn journal's max, not just the job's own counter (the #99
// verification lesson): the driver's step ordinals run ahead of the job
// counter without writing back, and the journal — the one ledger every
// derivation appends to, carried across sessions by the capsule — is
// what keeps a new ordinal off a drop path an earlier driver used. A
// shutdown that lands while the engine starts tears it down again.
func (r *Runtime) startEngine(ctx context.Context, j *job, interruptedCause string) (AgentHandle, error) {
	m := spawnJournalMaxN(j.capsuleDir) // disk read: outside the job's mutex
	j.mu.Lock()
	j.spawnN = max(j.spawnN, m) + 1
	spawnN := j.spawnN
	project, bundle := j.project, j.bundleName
	j.mu.Unlock()
	// the manual `pf submit --models` list is frozen at the daemon-private
	// record (never the agent-writable capsule): a relaunch reads it back
	manual := r.loadManualModels(j.wi)
	h, err := r.cfg.AgentStarter(ctx, AgentStartInput{
		WI:               j.wi,
		Project:          j.project,
		CapsuleDir:       j.capsuleDir,
		BundleName:       j.bundleName,
		SessionHandle:    j.sessionFingerprint,
		SpawnN:           int64(spawnN),
		Spawner:          r.spawner,
		StepEnded:        r.stepEnded(j.wi),
		Compose:          r.composeFor(project, bundle, manual),
		InterruptedCause: interruptedCause,
	})
	if err != nil {
		return nil, err
	}
	// A bridge handle fronts no child process group (its PID() answers
	// the hosting daemon's own pid — never signalable), so its pid is
	// recorded as 0; a scripted test engine's real child pid is recorded
	// verbatim for the tree killer.
	pid := h.PID()
	if pid == os.Getpid() {
		pid = 0
	}
	j.mu.Lock()
	j.agent, j.agentPID, j.agentStartedAt = h, pid, h.StartedAt()
	j.mu.Unlock()
	if r.stopped(ctx) {
		r.killAgentTree(j)
		return nil, errEngineStopped
	}
	return h, nil
}

// composeFor builds the dispatch-time candidate composition of one job
// (polyforge#33): the exact claim-time chain — the workspace yaml (fresh,
// mapping-aware, DAEMON-414 when invalid) and the bundle's frontmatter read
// again at every call, composed through workspace.ComposeCandidates with
// the work item's frozen manual list. File reads only; the driver calls it
// outside its own lock. A WI without a bundle has no list.
func (r *Runtime) composeFor(project, bundle string, manual []string) func() (map[string][]string, error) {
	return func() (map[string][]string, error) {
		if bundle == "" {
			return nil, nil
		}
		fm, err := r.readBundleFrontmatter(bundle)
		if err != nil {
			return nil, err
		}
		wcfg, err := r.LoadWorkspace(project)
		if err != nil {
			return nil, err
		}
		return workspace.ComposeCandidates(fm, wcfg, project, manual), nil
	}
}

// recovered finishes a successful re-derivation. A successful recovery
// RESETS the failure budget — the counter is spent by failures only.
// v1.5: RTRecovered carries SpawnN (the B2 fence key) and the in-process
// session secret (the observe lane's M4 revalidation reads it); identity
// otherwise changes NOT AT ALL — no new attempt, no generation bump. (There
// is no resume-failure bookkeeping — polyforge#106: every relaunch is a
// fresh driver derivation.)
func (r *Runtime) recovered(j *job, h AgentHandle, cause string) {
	accepted := acceptedResultCount(j.capsuleDir) // disk read: outside the job's mutex
	j.mu.Lock()
	// C7: the ledger's reset is the accepted-result check at the next
	// entry, never the relaunch itself (a relaunch that dies before its
	// step's result is accepted must keep counting). The relaunch marks
	// the job recovery-stabilized-so-far and notes the accepted-result
	// count it starts from.
	j.lastRelaunchRecovery = true
	j.acceptedAtRelaunch = accepted
	spawnN := j.spawnN
	pid := j.agentPID
	j.mu.Unlock()
	if err := j.transition(StateRunning); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: recovery could not return to RUNNING: %v\n", j.wi, err)
	}
	if err := r.persist(); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: state persist failed: %v\n", j.wi, err)
	}
	r.send(protocol.RTMsg{
		Kind:      protocol.RTRecovered,
		SpawnN:    spawnN,
		WorkItem:  j.wi,
		AgentPID:  pid,
		Detail:    cause,
		SessionID: j.sessionID,
	})
}

// capsuleExecution reads the capsule's execution.json (the zero value
// when absent or unreadable — a legacy WI-Agent capsule or a pre-init
// scene).
func capsuleExecution(capsuleDir string) capsule.Execution {
	var exec capsule.Execution
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.ExecutionJSON))
	if err != nil {
		return capsule.Execution{}
	}
	if json.Unmarshal(raw, &exec) != nil {
		return capsule.Execution{}
	}
	return exec
}

// capsuleWorkflowStatus answers the capsule's execution.json
// workflow_status ("" when absent or unreadable; the caller treats that
// as "no terminal verdict" and recovers as before).
func capsuleWorkflowStatus(capsuleDir string) string {
	return capsuleExecution(capsuleDir).WorkflowStatus
}

// blockedReasonNoteMax bounds the driver's blocked_reason inside the
// blocked close's note (runes): the note rides the SessionEnd, the
// persisted record and RTFinished, and a reason the driver copied from a
// step's output must not balloon them.
const blockedReasonNoteMax = 500

// blockedWorkflowNote is the hub note of a workflow the driver parked
// blocked: the owner's moves, plus the driver's own blocked_reason when it
// recorded one (a host-class fault, a failure counter at its bound) — the
// owner reads why from the hub without opening the capsule. An honest
// incomplete result records no reason and keeps the plain note.
func blockedWorkflowNote(capsuleDir string) string {
	const note = "workflow blocked: the owner holds the next move (resume / cancel)"
	reason := strings.TrimSpace(capsuleExecution(capsuleDir).BlockedReason)
	if reason == "" {
		return note
	}
	if rs := []rune(reason); len(rs) > blockedReasonNoteMax {
		reason = string(rs[:blockedReasonNoteMax]) + "…"
	}
	return note + "; blocked reason: " + reason
}

// boundedFail terminates the WI without a relaunch: SessionEnd(blocked)
// — the hub clears the session row and marks the WI blocked, which the
// claim gate excludes until a human intervenes (M2). The ledger's
// "never a third relaunch without progress" contract is preserved:
// restart sees blocked and leaves the record untouched. RTFinished(blocked) follows. Before the verdict the
// agent tree is killed and the dirty capsule committed+pushed best-effort
// (polyforge#116: the parked verdict must reach the chain; v2-converge §6
// bug 4: every branch, not just the blocked one). A SessionEnd the hub does
// not acknowledge keeps the record end_pending and retries (§6 bug 5,
// restart.go closeBlocked) — the WI is never orphaned running at aihub.
func (r *Runtime) boundedFail(ctx context.Context, j *job, detail string) {
	// No writer may outlive the verdict, and the last local work must
	// reach aihub before the WI is handed to its owner (v2-converge §6
	// bug 4): every bounded-fail branch — missing capsule, budget
	// exhaustion — kills the driver/agent tree
	// and commits+pushes best-effort here, not in the callers. The head
	// the capsule ends at rides the blocked verdict (cross-daemon continue
	// check, §7). The observe monitor goes with the tree: none of its
	// pushes may run after the SessionEnd(blocked) below. A driver Run
	// that outlives its bounded teardown is still a writer: the verdict
	// parks with the record as it is (the same state a crash right here
	// leaves) and its waiter re-runs this close once that Run exits.
	if err := r.killAgentTree(j); err != nil {
		_ = r.parkClose(j, "blocked", protocol.FinishBlocked, func() { r.boundedFail(r.ctx, j, detail) })
		return
	}
	r.releaseMonitor(j.wi)
	head := ""
	chainPushed := true // nothing local to push without a capsule
	if dirExists(j.capsuleDir) {
		var perr error
		head, perr = commitDirty(r, j)
		chainPushed = perr == nil
		// The re-claim the owner's resume runs restores the task repos
		// from the carrier: their uncommitted work rides refs/pf-wip
		// (polyforge#167). Before the verdict — the session credential
		// dies with it.
		r.saveTaskWIP(j, true, chainPushed)
	}
	j.mu.Lock()
	j.endNote, j.endHead = detail, head
	j.mu.Unlock()
	// When boundedFail's own push did not land, the close goes through
	// closeBlocked's push-then-close, so a hub blip never lets
	// SessionEnd(blocked) land with unpushed commits.
	outcome := r.closeBlocked(j, !chainPushed)
	r.finishBlocked(j, detail, outcome)
	if outcome == closeSettled {
		r.cancelIfRequested(j.wi, j.capsuleDir)
	}
}

// cancelIfRequested completes an owner's reserved cancel answer once the
// hub took the blocked close: the capsule's durable cancel intent is
// handed to the daemon's cancel door (Config.CancelRequested). This
// blocked branch is the single orchestration point — the answer itself
// only wrote the intent and parked the workflow blocked — and a close
// replayed after a restart reaches it again (the door is idempotent), as
// does reconcile's settle of a close whose answer was lost. A cancel that
// does not land leaves the work item blocked for `pf cancel`.
func (r *Runtime) cancelIfRequested(wi, capsuleDir string) {
	if r.cfg.CancelRequested == nil || capsuleDir == "" {
		return
	}
	if intent := capsuleExecution(capsuleDir).CancelRequested; intent != nil {
		r.cfg.CancelRequested(wi, *intent)
	}
}

// commitDirty is the commit-dirty-on-recover step (§六): stage everything,
// commit with the seq trailer, push. A commit that landed locally but
// failed to reach the server is pushed once more directly (the standing
// HEAD push), so the next boundary sees an honest remote state. Errors
// are the caller's to log; the LOCAL commit is the durable half and it
// stands on its own.
func commitDirty(r *Runtime, j *job) (string, error) {
	r.syncLiveTranscript(j.capsuleDir)
	sha, _, _, perr := gitops.CommitPush(context.Background(), j.git, "recover: commit dirty capsule before re-derivation")
	if perr != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: commit-dirty-on-recover push failed (local commit %q stands): %v\n", j.wi, sha, perr)
	}
	return sha, perr
}

// saveTaskWIP runs the task-repo WIP lane (polyforge#167) at a close
// point where the step process is already gone: keep snapshots each
// dirty task repo to refs/pf-wip/<name> (a clean one drops its stale
// WIP ref); keep=false — the wrapped close — only drops stale WIP refs.
// chainOK reports whether the close's chain push landed (the carrier
// alert gate). Best-effort: never fails the close.
func (r *Runtime) saveTaskWIP(j *job, keep, chainOK bool) {
	if !dirExists(j.capsuleDir) {
		return
	}
	pushTaskWIPFn(j.git, keep, chainOK)
}

// pushTaskWIPFn is the close's task-repo WIP push (a seam for the
// hung-call tests).
var pushTaskWIPFn = gitops.PushTaskWIP

// SaveTaskWIP snapshots wi's task-repo uncommitted work to refs/pf-wip
// once its step process is gone (the shutdown, after terminating the
// agent tree; the driver, at each step end), so a re-claim restores it.
// ctx bounds the lane (gitops.Opts.Ctx): when it ends — its deadline or
// a cancellation — the running git call's process group is killed and
// the rest skipped, so the per-WI git lock is released promptly. The
// caller is released at ctx's end at the latest. No runtime lock is held across
// the push; a failure is logged and never fails the caller.
func (r *Runtime) SaveTaskWIP(ctx context.Context, wi string) {
	j := r.jobOf(wi)
	if j == nil || !dirExists(j.capsuleDir) {
		return
	}
	o := j.git
	o.Ctx = ctx
	done := make(chan struct{})
	go func() {
		defer close(done)
		pushTaskWIPFn(o, true, true)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// stepWIPBound bounds the step-end WIP push: it holds the step's result
// write and the next dispatch.
const stepWIPBound = 30 * time.Second

// stepEnded is the driver's step-end sequence, run synchronously before the
// dispatch's result/failure is written: first the escapee sweep, then the
// WIP push of the task-repo work the step left uncommitted, so a kill -9
// after a step end loses none of it on a re-claim. The driver dispatches
// one step at a time per WI, so no successor exists while this runs; and
// the dispatch's wall ends before this hook (driver.Run's endedAt), so
// neither the sweep nor the push is booked into the step's T.
func (r *Runtime) stepEnded(wi string) func(context.Context) {
	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, stepWIPBound)
		defer cancel()
		r.sweepStepEscapees(wi)
		r.SaveTaskWIP(ctx, wi)
	}
}

// sweepStepEscapees kills what the ended step left outside its process
// group (a child that called setsid or double-forked): the same L4
// env-marker layer the WI-level teardown runs.
func (r *Runtime) sweepStepEscapees(wi string) {
	rep, err := Cleanup(CleanupTarget{WIID: wi, DaemonID: r.cfg.MachineID}, cleanupBound)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: step-end escapee sweep incomplete: %v (warnings: %v)\n", wi, err, rep.Warnings)
	}
}

// EventRecoveryParked is the durable, owner-visible record of a recovery
// that could not prove the old driver Run gone: the WI stays RECOVERING
// with the old handle in place until that Run exits (parkRecovery), so no
// second driver writes execution.json next to it.
const EventRecoveryParked = "recovery_parked"

// parkRecovery parks j's recovery on a live old handle and arranges its one
// exit: as soon as that Run reports Done, the WI's actor re-enters recovery
// (resumeRecord kills the now-done handle and relaunches). The wait is a
// goroutine, never the actor, and it ends with the shutdown — the persisted
// RECOVERING record then resumes at the next daemon start. The park emits an
// owner-visible event so a WI that waits is visible, not silent.
//
// The re-entry is only for the JOB THAT IS STILL PARKED: while it waits the
// owner may close the WI (Stop → stopOp: RECOVERING→SEALING, the close itself
// parked on the same handle until it exits — parkClose) or it may be
// superseded / no longer ours (a release, parked the same way while the Run
// lives, then dropJob), so j.agent may still name h; re-entering then would
// resumeRecord a closing or dropped record to RUNNING and relaunch a driver
// for a closed or quarantined WI. Hence all three checks below (same job in
// the table, still RECOVERING, same handle); a release parked while the job
// stays RECOVERING is refused by resumeRecord itself, before any change.
func (r *Runtime) parkRecovery(j *job, h AgentHandle, cause, detail string) {
	if h == nil {
		return
	}
	r.tap(j.wi, EventRecoveryParked, map[string]any{
		"cause":  cause,
		"detail": detail,
		"note":   "the old driver Run outlived its bounded teardown; the WI stays RECOVERING until it exits, then recovery re-enters (a daemon restart resumes it too)",
	})
	fmt.Fprintf(os.Stderr, "runtime: %s: recovery parked: the old driver Run outlived its bounded teardown; re-entering recovery when it exits\n", j.wi)
	go func() {
		if !waitHandleDone(r.ctx, h) {
			return // the shutdown: the persisted record waits for the next start
		}
		r.post(j.wi, func() {
			if r.ctx.Err() != nil {
				return
			}
			if r.jobOf(j.wi) != j {
				return // closed or superseded while parked: the job is gone
			}
			if stateOf(j) != StateRecovering {
				return // the close (SEALING/DONE) or another transition ended the park
			}
			j.mu.Lock()
			parked := j.agent == h
			j.mu.Unlock()
			if !parked {
				return // a different handle (or none) owns the job now
			}
			_, _ = r.resumeRecord(j, cause, detail)
		})
	}()
}

// EventCloseParked is the durable, owner-visible record of a close (a
// seal, a pause, a blocked verdict, or a release of a session that is no
// longer ours) that could not prove the old driver Run gone: nothing
// pushes, saves WIP, seals or quarantines the capsule, and the record is
// not dropped, until that Run exits; then the close finishes by itself
// (parkClose).
const EventCloseParked = "close_parked"

// parkKindRelease is the park kind of a release (supersede, notOurs): its
// operator answer names the release, not a close the operator asked for.
const parkKindRelease = "release"

// closePark names one parked close: the live handle it waits on and its
// kind (close, pause, blocked, release).
type closePark struct {
	h    AgentHandle
	kind string
}

// closeParked reports whether a close of j is parked on a live Run.
func (j *job) closeParked() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closePark != nil
}

// parkedError is the operator-facing answer of j's parked close.
func (j *job) parkedError(wi string) error {
	j.mu.Lock()
	kind := ""
	if j.closePark != nil {
		kind = j.closePark.kind
	}
	j.mu.Unlock()
	return closeParkedError(wi, kind)
}

// closeParkedError is the operator-facing answer of a parked close of the
// given kind.
func closeParkedError(wi, kind string) error {
	if kind == parkKindRelease {
		return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(
			"%s: the release of this work item is parked — its session is no longer this daemon's, but the previous driver Run outlived its bounded teardown and may still write the capsule, so the capsule is not quarantined and the record not dropped next to it; the release finishes by itself once that Run exits (event %s; a daemon restart finishes it too)",
			wi, EventCloseParked)}
	}
	return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(
		"%s: the close is parked — the previous driver Run outlived its bounded teardown and may still write the capsule, so nothing is pushed, saved or sealed next to it; the close finishes by itself once that Run exits (event %s; a daemon restart finishes it too)",
		wi, EventCloseParked)}
}

// parkClose parks a close of j whose teardown left the driver Run alive,
// the close-side mirror of parkRecovery: the record keeps the state the
// close already persisted (a crash here would leave the same record, and
// the restart finishes it with no old Run around), the handle stays in
// place, an owner-visible event names the park, and a waiter goroutine —
// never the actor — runs finish on the actor once that Run exits. finish
// runs only for the park still standing: the same job in the table, this
// park not replaced, the same handle. It answers the operator's 409.
func (r *Runtime) parkClose(j *job, kind, reason string, finish func()) error {
	j.mu.Lock()
	h := j.agent
	p := &closePark{h: h, kind: kind}
	j.closePark = p
	j.mu.Unlock()
	st, _ := j.snapshot()
	r.persistLog(j.wi)
	r.tap(j.wi, EventCloseParked, map[string]any{
		"kind":   kind,
		"reason": reason,
		"state":  string(st),
		"note":   "the old driver Run outlived its bounded teardown; the close waits (no push, WIP save or seal next to it) and finishes once that Run exits (a daemon restart finishes it too)",
	})
	fmt.Fprintf(os.Stderr, "runtime: %s: %s parked (%s): the old driver Run outlived its bounded teardown; finishing it when that Run exits\n", j.wi, kind, reason)
	if h != nil {
		go func() {
			if !waitHandleDone(r.ctx, h) {
				return // the shutdown: the persisted record finishes at the next start
			}
			r.post(j.wi, func() {
				if r.ctx.Err() != nil || r.jobOf(j.wi) != j {
					return
				}
				j.mu.Lock()
				standing := j.closePark == p && j.agent == h
				if standing {
					j.closePark = nil
				}
				j.mu.Unlock()
				if standing {
					finish()
				}
			})
		}()
	}
	return closeParkedError(j.wi, kind)
}

// waitHandleDone waits for h's Run to end, bounded only by the shutdown. A
// handle that exposes Exit is selected on it; one that only answers Done is
// polled. It reports false when ctx ended first.
func waitHandleDone(ctx context.Context, h AgentHandle) bool {
	if ex, ok := h.(interface{ Exit() <-chan struct{} }); ok {
		select {
		case <-ex.Exit():
			return true
		case <-ctx.Done():
			return false
		}
	}
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		if h.Done() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}

// TerminateAgentHandle terminates h within bound and, when Terminate reports
// that the handle outlived it, waits up to another bound for the handle's
// own Done. It answers an error only when the handle is still alive at the
// end: a done handle is a proven teardown whatever error its Run returned.
// A caller that must not relaunch next to a live writer treats that error as
// "park", never as a licence to proceed.
func TerminateAgentHandle(h AgentHandle, bound time.Duration) error {
	if h == nil {
		return nil
	}
	terr := h.Terminate(bound)
	if h.Done() {
		return nil
	}
	deadline := time.Now().Add(bound)
	for !h.Done() {
		if time.Now().After(deadline) {
			if terr == nil {
				terr = errors.New("agent handle did not report Done")
			}
			return fmt.Errorf("agent handle still alive after the teardown wait: %w", terr)
		}
		sleepFn(cleanupPoll)
	}
	return nil
}

// killAgentTree tears the current agent's whole tree down through the six
// layers. It clears the handle only once the teardown is proven: a live
// driver Run is a live writer, and its handle stays in place so recovery
// cannot mistake the job for agentless.
func (r *Runtime) killAgentTree(j *job) error {
	j.mu.Lock()
	pid := j.agentPID
	start := j.agentStartedAt
	h := j.agent
	if pid > 0 || h == nil {
		j.agent = nil
	}
	j.mu.Unlock()
	// v2 driver shape (the #99 verification lesson): a bridge handle
	// carries no process id (agentPID 0 — the handle's PID is the daemon
	// itself, which must never be signaled), so the pid-keyed cleanup
	// below is a no-op and the old driver goroutine SURVIVED the
	// recovery: it kept collecting envelopes at its own ordinals while
	// the relaunch dispatched new ones — two writers on one
	// execution.json. The handle's own Terminate is the kill switch:
	// canceling the driver context unwinds Run, which terminates its
	// live step process on the way out.
	if h != nil && pid <= 0 {
		if err := TerminateAgentHandle(h, cleanupBound); err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: driver handle teardown incomplete: %v (old Run still alive; the caller parks)\n", j.wi, err)
			return err
		}
		j.mu.Lock()
		if j.agent == h {
			j.agent = nil
		}
		j.mu.Unlock()
		r.reapOrphans(j.wi, j.capsuleDir)
		return nil
	}
	if pid <= 0 {
		// driver job with no live handle (the daemon crashed and the
		// record carries pid 0): the journal + env-marker reap is the
		// only way to find the step processes.
		r.reapOrphans(j.wi, j.capsuleDir)
		return nil
	}
	rep, err := Cleanup(CleanupTarget{
		PID:        pid,
		Pgid:       pid, // the agent spawns as its own group leader
		StartedAt:  start,
		WIID:       j.wi,
		DaemonID:   r.cfg.MachineID,
		CapsuleDir: j.capsuleDir,
	}, cleanupBound)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: agent tree cleanup incomplete: %v (warnings: %v)\n", j.wi, err, rep.Warnings)
	}
	return nil
}

// LifecycleCause reports whether a recovery cause is a daemon restart,
// an operator attach or an operator resume — a lifecycle event, not a
// relaunch the system ordered after a hang, stall or death.
//
// Destinations: the consecutive-recovery-failures budget is spent only by
// failures — a process-lifecycle event neither counts nor resets it, so a
// deploy or a restart loop cannot bounded-fail a healthy work item; and
// the step's runtime T books the interrupted dispatch of a system
// relaunch only (the bridge's interruptedFromCause), so a pause or a
// restart's downtime never counts while a hang→relaunch loop still
// reaches T.
//
// The bridge's audit archive is a different list with a different
// destination (every derivation that ended without a result is recorded
// in attempts[]); a daemon restart is exempt here and recorded there.
func LifecycleCause(cause string) bool {
	return cause == causeDaemonRestart || cause == causeOperatorAttach || cause == causeOperatorResume
}

// acceptedResultCount reads the capsule's execution state and counts the
// accepted-result attempts across steps (the driver's R14 commit
// points): the runtime's real-progress signal for the consecutive-
// failures ledger (C7 — the counter resets when a step's result is
// accepted). 0 when the file is absent or unparsable: nothing proven,
// never a reset. The file is agent-writable, but the ledger's adversary
// is death — a dead relaunch writes nothing, so the signal cannot be
// gamed by the failure mode the ledger bounds.
func acceptedResultCount(capsuleDir string) int64 {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.ExecutionJSON))
	if err != nil {
		return 0
	}
	var exec capsule.Execution
	if json.Unmarshal(raw, &exec) != nil {
		return 0
	}
	var n int64
	for _, st := range exec.Steps {
		if st == nil {
			continue
		}
		for _, at := range st.Attempts {
			if at.Cause == "accepted" {
				n++
			}
		}
	}
	return n
}
