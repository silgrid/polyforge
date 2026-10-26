package runtime

// restart.go: daemon-startup reconciliation and the one reconcile path
// (reference.md §6.2, re-keyed onto the v1.5 session model — reference.md
// §3.3). Restart itself asks aihub nothing: it seeds every state.json
// record and every claiming-journal entry into the job table and posts one
// reconcile message per work item to its actor, so the socket serves at
// once and a hung hub holds only the work items waiting on it. Each actor
// then fences its record on the hub's session verify before it relaunches
// an agent, pushes under the session or sends a SessionEnd — a record
// whose session the hub no longer holds (ended or taken over while the
// daemon was down) is never acted on:
//
//	a claiming-journal entry (a start under way) → its journaled session
//	  still holds the WI → the same claim replays and the start finishes;
//	  it does not → the entry goes (the submit queue's replay, which kept
//	  the accepted submit, claims afresh).
//	any other record → reconcileRecord: verify, then by the hub's answer
//	  recover a crash mid-run, finish a recorded close, retry a blocked
//	  close, replay a pending pause or terminal close, keep an operator
//	  pause, or attach and resume once the operator cleared clean_pause
//	  (M3); not ours → notOurs (no hub transition).
//
// An unreachable hub is not a verdict: the record is kept and its actor
// retries with backoff (retry). A mid-flight record whose capsule
// directory is missing, or whose project fell out of the [workspace]
// mapping, blocks that ONE work item (blockUnrecoverable — the ordinary
// bounded-fail close with a capsule_missing / project_unmapped event); the
// daemon starts and every other work item reconciles as above.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// MissingCapsuleError is the typed mid-flight failure (#103): a recorded
// non-terminal work item's capsule directory is absent under the active
// workspace — the operator has not moved it to the new base (or it was
// lost). The daemon never rebuilds such a capsule from the remote: the
// capsule git chain IS the revision chain, and a re-clone would silently
// discard unpublished local state. At restart it blocks ONLY that work item
// (v2-converge §6; it is not a startup failure); `pf resume <wi>` re-claims
// it from the capsule chain.
type MissingCapsuleError struct {
	WI  string
	Dir string
}

func (e *MissingCapsuleError) Error() string {
	if e.Dir == "" {
		return fmt.Sprintf("work item %s: its state record names no capsule directory (written before records carried capsule_dir) — the daemon derives none; `pf resume %s` re-claims it from the capsule chain, or give up with `pf cancel %s`", e.WI, e.WI, e.WI)
	}
	return fmt.Sprintf("work item %s: mid-flight capsule missing at %s — it is NOT recoverable from the remote (unpublished local state would be lost); restore the capsule to that path manually, then `pf resume %s` (or restart the daemon); or give up with `pf cancel %s`", e.WI, e.Dir, e.WI, e.WI)
}

// UnmappedMidflightError is the #113 mid-flight failure posture: a
// recorded work item's project has no workspace in the CURRENT mapping
// (the operator's config edit dropped its section). The capsule is NOT
// relocated; at restart ONLY that work item is blocked (v2-converge §6) —
// the operator re-adds the workspace section, then `pf resume <wi>`; the
// existing no-rebuild rule stands.
type UnmappedMidflightError struct {
	WI      string
	Project string
}

func (e *UnmappedMidflightError) Error() string {
	return fmt.Sprintf("work item %s: mid-flight project %q is not in the [workspace] mapping — the capsule is NOT relocated and the work item is not recoverable until its workspace is re-added to the mapping in the config, then `pf resume %s` (or restart the daemon) (`pf doctor` checks the mapping)", e.WI, e.Project, e.WI)
}

// SessionVerifyOutageError names a session verify of a paused record the hub
// REFUSED (a typed 4xx): the WI parks in the ordinary blocked state with
// the refusal named, and the owner's `pf resume` takes the ordinary
// blocked → resume path. An UNREACHABLE hub is not this case — the
// record is kept and its actor retries the verify (v2-converge §6).
type SessionVerifyOutageError struct {
	WI  string
	Err error
}

func (e *SessionVerifyOutageError) Error() string {
	return fmt.Sprintf("work item %s: aihub refused the restart's session verify (%v); the work item is parked blocked and `pf resume %s` re-claims it from its capsule chain", e.WI, e.Err, e.WI)
}

// requireMidflightCapsule enforces the no-silent-rebuild contract for a
// work item this daemon proceeds with (the #103 guard, carried onto the
// mapping): the record's project MUST be mapped (an unmapped mid-flight
// project fails loudly, naming it — never relocated, never rebuilt), and
// its recorded capsule directory must exist. The persisted record's
// CapsuleDir is the truth (in-flight capsules keep their homes across
// mapping re-adds and capsule-name changes); a record without one is a
// missing capsule — nothing is derived or probed.
func (r *Runtime) requireMidflightCapsule(wi, project, dir string) error {
	if _, merr := r.cfg.Mapping.WorkspaceFor(project); merr != nil {
		return &UnmappedMidflightError{WI: wi, Project: project}
	}
	if dir != "" && dirExists(dir) {
		return nil
	}
	return &MissingCapsuleError{WI: wi, Dir: dir}
}

// Restart loads state.json and the claiming journal and hands every work
// item to its actor (see the file comment); it never waits on aihub. A
// journal entry whose session a record already carries was settled
// before the crash and is dropped. ctx is the daemon's run context: its
// end cancels the actors' context.
func (r *Runtime) Restart(ctx context.Context) error {
	st, err := loadState(r.statePath)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, r.cancel)
	journal := r.loadClaiming()
	var ids, settled []string
	torn := false
	r.mu.Lock()
	for _, rec := range st.Jobs {
		if rec.SessionID == "" {
			// a torn write: no session, nothing to reconcile
			fmt.Fprintf(os.Stderr, "runtime: %s: record without a session dropped\n", rec.WorkItemID)
			torn = true
			continue
		}
		if r.jobs[rec.WorkItemID] == nil {
			r.jobs[rec.WorkItemID] = r.restoredJob(rec)
		}
		ids = append(ids, rec.WorkItemID)
	}
	for id, e := range journal {
		switch ex := r.jobs[id]; {
		case ex != nil && ex.sessionID == e.SessionID:
			settled = append(settled, id)
		case ex == nil:
			r.jobs[id] = &job{wi: id, sessionID: e.SessionID, sessionFingerprint: api.SessionFingerprint(e.SessionID), state: StateClaiming}
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range settled {
		r.clearClaiming(id)
	}
	if torn {
		r.persistLog("")
	}
	for _, id := range ids {
		r.post(id, func() {
			// the crashed daemon's step processes go first, whatever the
			// hub answers (single writer; local, no hub call) — a claiming
			// entry too: the crash may have hit between the starter
			// launching a step and the record's persist (no capsule
			// recorded yet: the env-marker sweep still finds them)
			if j := r.jobOf(id); j != nil {
				r.reapOrphans(id, j.capsuleDir)
			}
			r.reconcile(id)
		})
	}
	return nil
}

// reconcile is the one reconcile path of a work item (its actor): a
// pending claiming-journal entry first, then the job by its state.
func (r *Runtime) reconcile(wi string) {
	if r.ctx.Err() != nil {
		return
	}
	j := r.jobOf(wi)
	if e := r.loadClaiming()[wi]; e != nil && (j == nil || j.sessionID != e.SessionID || stateOf(j) == StateClaiming) {
		// a start under way when the daemon stopped: its journaled
		// session still holds the WI → finish the start (the same claim
		// replays; a claim over a retained paused record replaces the
		// record only once it landed); it does not → the intention is
		// dead, the entry goes
		err := r.replayable(e)
		if err == nil {
			err = r.claim(r.ctx, wi, wi, StartOpts{Models: e.Models})
		}
		if hubUnavailable(err) && j != nil && r.jobOf(wi) == j {
			r.retry(j)
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: claiming journal replay: %v\n", wi, err)
		}
		if j != nil && stateOf(j) == StateClaiming {
			r.dropJob(j) // the restart's placeholder: the claim settled otherwise
		}
		if r.jobOf(wi) != j {
			return // the claim's own job (or nothing) now: settled
		}
	}
	if j == nil {
		return
	}
	switch stateOf(j) {
	case StateClaiming, StateDone:
		return
	case StateRunning, StateRecovering:
		j.mu.Lock()
		live := j.agent != nil
		j.mu.Unlock()
		if live {
			return // live under this daemon
		}
	}
	if _, err := r.reconcileRecord(j, causeOperatorAttach); hubUnavailable(err) {
		r.retry(j)
		return
	}
	j.retryIn = 0 // the hub answered: a later episode backs off afresh
}

// replayable verifies a claiming-journal entry's session: nil when it
// still holds the work item; a stale entry is dropped.
func (r *Runtime) replayable(e *claimingRec) error {
	v, err := r.hub.SessionVerify(e.WorkItemID, e.SessionID)
	switch {
	case err != nil && !claimRefused(err):
		return &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: claiming entry kept (aihub verify failed): %v", e.WorkItemID, err)}
	case err == nil && v.Matches:
		return nil
	}
	r.clearClaimingSession(e.WorkItemID, e.SessionID)
	return fmt.Errorf("stale claiming entry dropped (the journaled session no longer holds the work item)")
}

func stateOf(j *job) JobState {
	st, _ := j.snapshot()
	return st
}

// resumeRecord recovers a recorded job through the engine (recoverCore
// kills and reaps the old tree first — single writer): recoveryCause names the trigger the
// engine logs and the interrupted-dispatch archive classifies (an
// operator-shaped cause archives nothing; a daemon_restart re-derivation
// archives the dispatch the crash interrupted). It answers the live
// resume door's outcome.
func (r *Runtime) resumeRecord(j *job, recoveryCause, detail string) (string, error) {
	j.mu.Lock()
	j.state = StateRunning // the hub is the authority: Paused/Recovering resume as RUNNING
	j.mu.Unlock()
	r.recoverCore(r.ctx, j, recoveryCause, detail)
	if r.jobOf(j.wi) != j {
		return ResumeClosed, nil
	}
	switch st, _ := j.snapshot(); {
	case st == StateRunning:
		return ResumeAttached, nil
	case st == StateRecovering && r.ctx.Err() != nil:
		// the relaunch met the shutdown: the record is kept for the next start
		return "", &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf("%s: the daemon is shutting down; nothing was resumed here (the next daemon start resumes it)", j.wi)}
	}
	return ResumeClosed, nil
}

// finishSeal finishes a recorded close (SEALING): the recorded verdict
// passes through verbatim. A reason the crashed stop never wrote is
// NOBODY's verdict, so the close finishes BLOCKED with a note naming the
// interrupted, cause-unknown seal and a human decides. A STALE_SESSION
// answer (the session no longer holds the work item: an earlier close
// landed with its answer lost, or an admin ended it) finishes it locally;
// any other failure is retried by stopOp.
func (r *Runtime) finishSeal(j *job) {
	j.mu.Lock()
	reason, note := j.stopReason, j.endNote
	j.mu.Unlock()
	if !isTerminalStopReason(reason) {
		reason = protocol.FinishBlocked
		note = "seal interrupted by a daemon crash before any verdict was recorded (cause unknown) — finished blocked for a human to decide"
		fmt.Fprintf(os.Stderr, "runtime: %s: interrupted seal with no recorded verdict finished blocked (the human decides)\n", j.wi)
		r.tap(j.wi, "interrupted_seal", map[string]any{"note": note})
		j.mu.Lock()
		j.stopReason, j.endNote = reason, note
		j.mu.Unlock()
		r.persistLog(j.wi)
	}
	if err := r.stopOp(r.ctx, j, reason, true, note); err != nil && isStaleSession(err) {
		r.finishSealLocally(j, reason, err)
	}
}

// isStaleSession reports the hub's typed "this session no longer holds
// the work item" refusal (CORE-403 STALE_SESSION).
func isStaleSession(err error) bool {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrCoreForbidden {
		return false
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	if len(ae.Extra) > 0 && json.Unmarshal(ae.Extra, &extra) == nil && extra.Reason == "STALE_SESSION" {
		return true
	}
	return strings.Contains(ae.Detail, "STALE_SESSION")
}

// finishSealLocally ends a SEALING job whose session the hub no longer
// holds: the hub's state stands, the scene is sealed read-only, the job
// is DONE and dropped, observe is released, and an event records that the
// local close never reached the hub under this session.
func (r *Runtime) finishSealLocally(j *job, reason string, cause error) {
	if err := sealReadOnly(j.capsuleDir); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: read-only seal incomplete: %v\n", j.wi, err)
	}
	if err := j.transition(StateDone); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: sealing job could not reach DONE: %v\n", j.wi, err)
	}
	r.dropJob(j)
	if reason != protocol.FinishBlocked {
		r.clearManualModels(j.wi) // as the ordinary terminal close does
	}
	r.persistLog(j.wi)
	fmt.Fprintf(os.Stderr, "runtime: %s: seal finished locally — the hub no longer holds this session: %v\n", j.wi, cause)
	r.tap(j.wi, "seal_finished_locally", map[string]any{
		"reason": reason,
		"note":   "the hub no longer holds this session (an earlier close landed with its answer lost, or an admin ended it): the work item's hub state is whatever was set there, not necessarily " + reason,
		"error":  cause.Error(),
	})
	r.send(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: j.wi, Reason: reason})
}

// endBlockedAtHub sends SessionEnd(blocked) fenced on the secret and
// reports whether the close is SETTLED: the hub accepted it, or a verify
// shows the session no longer holds the WI (our earlier close landed and
// only its answer was lost, or another session took over).
func (r *Runtime) endBlockedAtHub(wi, secret, note, head string) bool {
	err := r.hub.SessionEnd(wi, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked, Note: note, HeadSHA: head})
	if err == nil {
		return true
	}
	if v, verr := r.hub.SessionVerify(wi, secret); verr == nil && !v.Matches {
		fmt.Fprintf(os.Stderr, "runtime: %s: blocked close failed (%v) but the session no longer holds the work item (status %s) — settled\n", wi, err, v.Status)
		return true
	}
	fmt.Fprintf(os.Stderr, "runtime: %s: session-end(blocked) failed (will retry): %v\n", wi, err)
	return false
}

// closeBlocked sends one job's SessionEnd(blocked), pushing the local
// chain FIRST when push is set, with the session credential that dies
// with the close (boundedFail's own push most often failed with the same
// outage that fails the close). Reports whether the close is SETTLED.
// Push outcomes: landed → the close goes out; transient failure → not
// settled, unless the session no longer holds the WI (nothing can be
// pushed under it; settled); PERMANENT rejection (hook refusal,
// non-fast-forward, size limit) → stop pushing (persisted), raise
// push_failed with the local head, and close without it.
func (r *Runtime) closeBlocked(j *job, push bool) bool {
	j.mu.Lock()
	secret, note, head, chain, gaveUp := j.sessionID, j.endNote, j.endHead, j.git, j.endPushGaveUp
	j.mu.Unlock()
	if push && !gaveUp && dirExists(j.capsuleDir) && dirExists(chain.GitDir) {
		if _, perr := gitops.Push(chain); perr != nil {
			if !gitops.IsPermanentPushRejection(perr) {
				if v, verr := r.hub.SessionVerify(j.wi, secret); verr == nil && !v.Matches {
					fmt.Fprintf(os.Stderr, "runtime: %s: blocked-close chain push failed (%v) and the session no longer holds the work item (status %s) — settled without it\n", j.wi, perr, v.Status)
					return true
				}
				fmt.Fprintf(os.Stderr, "runtime: %s: blocked-close chain push failed (will retry before the close): %v\n", j.wi, perr)
				return false
			}
			j.mu.Lock()
			j.endPushGaveUp = true
			j.mu.Unlock()
			r.persistLog(j.wi) // durable: a restart's retry must not push again either
			local, _ := gitops.Head(chain)
			fmt.Fprintf(os.Stderr, "runtime: %s: blocked-close chain push permanently rejected — closing without it (local head %s stays in the capsule): %v\n", j.wi, local, perr)
			r.tap(j.wi, "push_failed", map[string]any{
				"phase":      "blocked-close",
				"permanent":  true,
				"local_head": local,
				"error":      perr.Error(),
				"note":       "the hub permanently refused the chain push; the blocked close goes out without it and the unpushed commits stay in the local capsule",
			})
		}
	}
	crashpoint.At("bounded.closing")
	return r.endBlockedAtHub(j.wi, secret, note, head)
}

// settleEndPending drops an end_pending job whose close is settled (and
// a claiming-journal entry of its session a failed persist kept).
func (r *Runtime) settleEndPending(j *job) {
	if err := j.transition(StateDone); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: end_pending job could not reach DONE: %v\n", j.wi, err)
	}
	r.dropJob(j)
	r.persistLog(j.wi)
	r.clearClaimingSession(j.wi, j.sessionID)
}

// fence verifies a job's session before a door pushes or ends under it,
// once per process for a record restored at restart (a claimed or
// verified job passes at once). A mismatch settles the job locally and
// answers DAEMON-409; an unanswered verify answers DAEMON-503 — nothing
// was done either way.
func (r *Runtime) fence(j *job) error {
	j.mu.Lock()
	ok := j.verified
	j.mu.Unlock()
	if ok {
		return nil
	}
	v, err := r.hub.SessionVerify(j.wi, j.sessionID)
	switch {
	case err != nil:
		return &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: the restart has not verified this work item's session yet (aihub: %v); nothing was done", j.wi, err)}
	case !v.Matches:
		r.notOurs(j, v)
		return conflict("the session no longer holds %s (current holder %q); the local record was released", j.wi, v.CurrentFingerprint)
	}
	j.mu.Lock()
	j.verified = true
	j.mu.Unlock()
	return nil
}

// finishBlocked is boundedFail's tail once the blocked close went out
// (settled reports whether the hub took it): success drops the job,
// failure parks it end_pending (durable) and its actor retries. Observe
// is released either way — the driver is already dead.
func (r *Runtime) finishBlocked(j *job, detail string, settled bool) {
	next := StateDone
	if !settled {
		next = StateEndPending
	}
	if err := j.transition(next); err != nil {
		// the verdict (or the pending close) is authoritative whatever
		// the edge table says: record it rather than leave a stale state
		fmt.Fprintf(os.Stderr, "runtime: %s: bounded fail: %v — forcing %s\n", j.wi, err, next)
		j.mu.Lock()
		j.state = next
		j.mu.Unlock()
	}
	if settled {
		r.dropJob(j)
	}
	r.persistLog(j.wi)
	r.send(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: j.wi, Reason: protocol.FinishBlocked, Detail: detail})
	if !settled {
		fmt.Fprintf(os.Stderr, "runtime: %s: blocked close not acknowledged by the hub — record kept end_pending, retrying\n", j.wi)
		r.retry(j)
	}
}

// notOurs settles a job whose session the hub no longer holds (a verify
// answered matches=false). A terminal, blocked or queued work item keeps
// its capsule — the close settled server-side, the bounded-fail verdict
// stands (M2) or the claim is simply gone — the local tree is collected
// and the record dropped, so the owner's `pf resume` re-claims through the
// ordinary Start. A work item another session holds is released
// (supersede: quarantine, session_released). No hub transition either way.
func (r *Runtime) notOurs(j *job, v *api.SessionVerifyResponse) {
	switch v.Status {
	case api.StatusWrapped, api.StatusFailed, api.StatusCanceled:
		r.clearManualModels(j.wi)
		fallthrough
	case api.StatusBlocked, api.StatusQueued:
		fmt.Fprintf(os.Stderr, "runtime: %s: the session no longer holds the work item (hub says %s) — record dropped\n", j.wi, v.Status)
		r.killAgentTree(j)
		r.dropJob(j)
		r.persistLog(j.wi)
	default:
		fmt.Fprintf(os.Stderr, "runtime: %s: held by session %q — local tree killed, capsule quarantined, record dropped (no hub transition)\n", j.wi, v.CurrentFingerprint)
		r.supersede(j, "session_released", "", v.CurrentFingerprint)
	}
}

// refusedNotOurs answers true when a failed capsule push was refused
// because the session no longer holds the work item (the owner ruling:
// such a refusal stops the work item at once): the git face refused the
// credential, a verify confirms the mismatch, the job is settled by
// notOurs and its observe monitor released.
func (r *Runtime) refusedNotOurs(j *job, perr error) bool {
	if !gitops.IsCredentialRefused(perr) {
		return false
	}
	v, err := r.hub.SessionVerify(j.wi, j.sessionID)
	if err != nil || v.Matches {
		return false
	}
	r.notOurs(j, v)
	r.release(j.wi)
	return true
}

// pausePending reports the operator pause whose SessionEnd(paused) never
// reached the hub: pauseOp persists the Paused record with clean_pause
// FIRST, and every attach clears the local clean_pause before it flips
// the hub back to running — so a clean Paused record facing a running
// hub is that pause, never an interrupted resume.
func pausePending(rec *jobRecord) bool {
	return rec.State == string(StatePaused) && rec.CleanPause && rec.StopReason == protocol.FinishPaused
}

// closePending reports a retained Paused record whose terminal close
// (wrap/fail/block after an operator pause) has not reached aihub: the
// close persists its stop_reason before its SessionEnd, so the record
// keeps that verdict until the SessionEnd lands. The verdict stands —
// the close is replayed, the record never resumes.
func closePending(rec *jobRecord) bool {
	return rec.State == string(StatePaused) && isTerminalStopReason(rec.StopReason)
}

// isTerminalStopReason reports whether a persisted stop_reason is a
// TERMINAL close verdict (wrapped|failed|blocked) — as opposed to the
// retained operator pause's own "paused" reason or an unset one.
func isTerminalStopReason(reason string) bool {
	return reason == protocol.FinishWrapped ||
		reason == protocol.FinishFailed ||
		reason == protocol.FinishBlocked
}

func conflict(format string, a ...any) error {
	return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf(format, a...)}
}

// reconcileRecord reconciles one recorded job against the hub's session
// verify — the per-work-item fence every restart step passes before it
// relaunches an agent, pushes under the session or sends a SessionEnd: a
// mismatch is never acted on. It is also the live-holder resume door's
// body (attachCause names that door's recovery cause). It answers the
// door's outcome; an unreachable hub answers DAEMON-503 (reconcile
// retries it).
//
//	verify refused (4xx) → the WI parks blocked naming the refusal.
//	not ours → an end_pending close is settled; anything else notOurs.
//	end_pending → push the chain and retry the blocked close.
//	running or paused + a pending close (closePending: the daemon died, or
//	  the SessionEnd failed, between a retained record's close persist and
//	  its SessionEnd) → the close is replayed, whatever clean_pause says.
//	running → a pending pause replays its network half; a sealing record
//	  finishes its close; anything else recovers in place.
//	paused + clean → the operator pause stands: nothing resumes.
//	paused + clean_pause cleared (the operator's pf resume) → attach and
//	  resume in place (M3).
//	terminal / anything else under our session → the record is dropped.
func (r *Runtime) reconcileRecord(j *job, attachCause string) (string, error) {
	rec := j.record()
	v, err := r.hub.SessionVerify(rec.WorkItemID, rec.SessionID)
	if err != nil {
		if claimRefused(err) {
			fmt.Fprintf(os.Stderr, "runtime: %s: session verify refused: %v\n", rec.WorkItemID, err)
			r.blockUnrecoverable(j, &SessionVerifyOutageError{WI: rec.WorkItemID, Err: err})
			return "", err
		}
		return "", &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: session verify failed (nothing was resumed; retrying): %v", rec.WorkItemID, err)}
	}
	if !v.Matches {
		switch rec.State {
		case string(StateEndPending):
			r.settleEndPending(j) // our close landed with its answer lost, or another session took over
			return "", nil
		case string(StateSealing):
			reason := rec.StopReason
			if !isTerminalStopReason(reason) {
				reason = protocol.FinishBlocked
			}
			r.finishSealLocally(j, reason, fmt.Errorf("session verify: the session no longer holds the work item (hub says %s)", v.Status))
			return "", nil
		}
		r.notOurs(j, v)
		return "", conflict("the session no longer holds %s (current holder %q); the local record was released", rec.WorkItemID, v.CurrentFingerprint)
	}
	j.mu.Lock()
	j.verified = true
	j.mu.Unlock()
	if r.ctx.Err() != nil {
		// the shutdown came while the verify was on the wire: the record
		// stays as it is for the next start
		return "", &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf("%s: the daemon is shutting down; nothing was resumed", rec.WorkItemID)}
	}
	if rec.State == string(StateEndPending) {
		if !r.closeBlocked(j, true) {
			return "", &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: rec.WorkItemID + ": the blocked close has not reached aihub yet (retrying)"}
		}
		r.settleEndPending(j)
		return ResumeClosed, nil
	}
	switch {
	case (v.Status == api.StatusRunning || v.Status == api.StatusPaused) && closePending(rec):
		r.tap(rec.WorkItemID, "pending_close_replay", map[string]any{
			"reason":              rec.StopReason,
			"session_fingerprint": rec.SessionFingerprint,
			"note":                "a retained record's terminal close persisted without its SessionEnd landing; the close is replayed",
		})
		if err := r.requireMidflightCapsule(rec.WorkItemID, rec.Project, rec.CapsuleDir); err != nil {
			r.blockUnrecoverable(j, err)
			return "", conflict("%s", err.Error())
		}
		if err := r.stopOp(r.ctx, j, rec.StopReason, true, ""); err != nil {
			r.tap(rec.WorkItemID, "pending_close_replay_failed", map[string]any{"reason": rec.StopReason, "error": err.Error()})
			return "", &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: pending close replay failed (record kept, retrying): %v", rec.WorkItemID, err)}
		}
		return ResumeClosed, nil
	case v.Status == api.StatusRunning && pausePending(rec):
		r.reapOrphans(rec.WorkItemID, j.capsuleDir)
		r.tap(rec.WorkItemID, "pending_pause_replay", map[string]any{
			"session_fingerprint": rec.SessionFingerprint,
			"note":                "crash between the pause record persist and SessionEnd(paused); the pause replayed at restart",
		})
		if err := r.pauseNetwork(j); err != nil {
			return "", &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: fmt.Sprintf("%s: pending pause replay failed (retrying): %v", rec.WorkItemID, err)}
		}
		return "", conflict("%s: its pending operator pause was replayed; the record stays paused", rec.WorkItemID)
	case v.Status == api.StatusRunning:
		if err := r.requireMidflightCapsule(rec.WorkItemID, rec.Project, rec.CapsuleDir); err != nil {
			// the typed mid-flight block (never a rebuild from the remote)
			r.blockUnrecoverable(j, err)
			return "", conflict("%s", err.Error())
		}
		if rec.State == string(StateSealing) {
			r.finishSeal(j)
			return ResumeClosed, nil
		}
		return r.resumeRecord(j, causeDaemonRestart, "daemon restart (crash mid-run)")
	case v.Status == api.StatusPaused && v.CleanPause:
		// the operator pause stands (a torn record reads Paused again —
		// the hub is the authority)
		j.mu.Lock()
		j.state, j.cleanPause = StatePaused, true
		j.mu.Unlock()
		return "", conflict("clean_pause is still set at the hub — the operator's resume (the clean_pause=false PATCH) has not landed; nothing was resumed")
	case v.Status == api.StatusPaused:
		if err := r.requireMidflightCapsule(rec.WorkItemID, rec.Project, rec.CapsuleDir); err != nil {
			r.blockUnrecoverable(j, err)
			return "", conflict("%s", err.Error())
		}
		if err := r.attach(j); err != nil {
			return "", err
		}
		return r.resumeRecord(j, attachCause, "session attach after the operator's pf resume")
	case v.Status == api.StatusWrapped, v.Status == api.StatusFailed, v.Status == api.StatusCanceled:
		r.notOurs(j, v)
		return ResumeClosed, nil
	}
	fmt.Fprintf(os.Stderr, "runtime: %s: hub says %s with our session; record dropped\n", rec.WorkItemID, v.Status)
	r.dropJob(j)
	r.persistLog(j.wi)
	return "", conflict("aihub says %s while the session still holds the work item; the local record was dropped", v.Status)
}

// attach is the same-session paused attach (M3): the local clean_pause
// mirror follows the hub BEFORE SessionAttach flips the hub to running
// (a crash between the two never reads as a pending operator pause),
// then SessionAttach under the retained secret.
func (r *Runtime) attach(j *job) error {
	j.mu.Lock()
	j.cleanPause = false
	j.mu.Unlock()
	if err := r.persist(); err != nil {
		return &api.Error{Code: api.ErrDaemonInternal, Detail: err.Error()}
	}
	_, err := r.hub.SessionAttach(j.wi, api.SessionAttachRequest{WorkItemID: j.wi, SessionID: j.sessionID, MachineID: r.cfg.MachineID})
	var ae *api.Error
	switch {
	case err != nil && errors.As(err, &ae):
		return err
	case err != nil:
		return &api.Error{Code: api.ErrDaemonHubUnavailable, Detail: "session attach: " + err.Error() + "; nothing was resumed"}
	case r.ctx.Err() != nil:
		// the shutdown came while the attach was on the wire: the hub
		// says running under our session, the next start recovers it
		return &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf("%s: the session was attached but the daemon is shutting down; nothing was resumed here (the next daemon start resumes it)", j.wi)}
	}
	return nil
}

// ResumePaused is the LIVE-holder half of the operator's `pf resume`
// (wi_seWTgHeKtaa8, v2-converge §7): the operator already cleared
// clean_pause at the hub and this daemon holds the retained paused
// record, so the reconciliation the daemon's startup runs for it
// (reconcileRecord: verify → attach → recovery continuation) runs NOW,
// as a message of the work item's actor. It answers ResumeAttached /
// ResumeAlreadyLive / ResumeClosed or a typed refusal: DAEMON-404 (no
// record here), DAEMON-409 (not a paused record, a terminal close of the
// record still pending, clean_pause still set, the session no longer
// holds the WI), DAEMON-503 (hub unreachable —
// nothing was resumed), DAEMON-410 (the shutdown came).
func (r *Runtime) ResumePaused(ctx context.Context, wi string) (string, error) {
	id := r.CanonicalID(wi)
	var out string
	err := r.call(ctx, id, func() error {
		j := r.jobOf(id)
		if j == nil {
			return &api.Error{Code: api.ErrDaemonNotFound, Detail: fmt.Sprintf("no paused work item %s held by this daemon", id)}
		}
		switch st := stateOf(j); st {
		case StateRunning:
			out = ResumeAlreadyLive // an earlier resume or a live dispatch owns the record
			return nil
		case StatePaused:
		default:
			return conflict("work item is %s; only a paused record resumes in place", st)
		}
		if rec := j.record(); closePending(rec) {
			// a resume must not race the close it would cancel
			return conflict("a %s close of the paused record is pending (its SessionEnd has not reached aihub); the daemon keeps retrying it — nothing was resumed", rec.StopReason)
		}
		var err error
		out, err = r.reconcileRecord(j, causeOperatorResume)
		return err
	})
	if errors.Is(err, errAbandoned) {
		err = &api.Error{Code: api.ErrDaemonGone, Detail: fmt.Sprintf("%s: the daemon is shutting down; nothing was resumed", id)}
	}
	return out, err
}

// revalidatePaused is the M4 revalidation of the retained PAUSED records
// (polyforge#217): a paused record has no observe monitor, and a hub-side
// change of its session — `pf cancel` (direct to aihub, from any
// machine), a hub-side session end — reaches the holder no other way, so
// without it the record stays paused until a restart. Each tick posts one
// check per paused record to its actor; a work item whose actor is busy
// is checked at a later tick (no backlog builds on a hung hub). Only the
// job-table lock is taken here; the verify runs on the actor.
func (r *Runtime) revalidatePaused() {
	r.mu.Lock()
	var ids []string
	for id, j := range r.jobs {
		if st, _ := j.snapshot(); st == StatePaused && r.boxes[id] == nil {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.post(id, func() { r.revalidatePausedRecord(id) })
	}
}

// revalidatePausedRecord verifies one retained paused record's session
// (wi's actor). A session the hub no longer holds settles exactly as
// reconcileRecord settles a paused record on a mismatch (notOurs: a
// canceled, wrapped, failed, blocked or queued work item drops the
// record; another holder supersedes it). A match changes nothing: the
// operator pause stands, and a cleared clean_pause still waits for the
// operator's `pf resume` on this machine or the next start (no remote
// resume). A verify error is not a verdict: the next tick asks again.
func (r *Runtime) revalidatePausedRecord(wi string) {
	j := r.jobOf(wi)
	if j == nil || stateOf(j) != StatePaused || r.ctx.Err() != nil {
		return
	}
	v, err := r.hub.SessionVerify(wi, j.record().SessionID)
	if err != nil || v.Matches {
		return
	}
	r.notOurs(j, v)
}

// blockUnrecoverable blocks ONE mid-flight record whose capsule is
// missing, whose project is unmapped, or whose session verify the hub
// refused: the ordinary bounded-fail path (SessionEnd(blocked) with a
// reason naming the cause, the orphan sweep, the durable end_pending
// retry), so the work item shows `blocked` at aihub — the owner's `pf
// resume` re-claims it from the capsule chain and the task-repo carrier —
// and nothing stays parked locally. A durable event names the cause.
func (r *Runtime) blockUnrecoverable(j *job, cause error) {
	kind := EventCapsuleMissing
	body := map[string]any{"work_item": j.wi, "project": j.project}
	var detail string
	var miss *MissingCapsuleError
	var unm *UnmappedMidflightError
	var outage *SessionVerifyOutageError
	switch {
	case errors.As(cause, &unm):
		kind = EventProjectUnmapped
		body["project"] = unm.Project
		detail = fmt.Sprintf("project %q is not in the daemon's [workspace] mapping — re-add it to the config, then `pf resume %s`", unm.Project, j.wi)
	case errors.As(cause, &outage):
		kind = EventSessionVerifyOutage
		detail = cause.Error()
	case errors.As(cause, &miss):
		body["path"] = miss.Dir
		detail = fmt.Sprintf("capsule missing at %s — it is never rebuilt from the remote; `pf resume %s` re-claims the work item from its capsule chain", miss.Dir, j.wi)
	default:
		detail = cause.Error()
	}
	body["reason"] = detail
	r.tap(j.wi, kind, body)
	fmt.Fprintf(os.Stderr, "runtime: %s: blocked at restart: %s\n", j.wi, detail)
	// enter the recovery state so boundedFail's closing edge
	// (-> done | end_pending) is a legal one
	j.mu.Lock()
	j.state = StateRecovering
	j.mu.Unlock()
	r.boundedFail(r.ctx, j, detail)
}

// Restart-time block reason kinds — also the durable event kinds.
const (
	EventCapsuleMissing      = "capsule_missing"
	EventProjectUnmapped     = "project_unmapped"
	EventSessionVerifyOutage = "session_verify_outage"
)

// Resume outcomes for the live-holder resume door (ResumePaused).
const (
	// ResumeAttached: the retained paused session was re-attached and
	// the work item continues IN PLACE on this daemon.
	ResumeAttached = "attached"
	// ResumeAlreadyLive: the work item is already running here — an
	// earlier resume or a live dispatch owns the record; idempotent.
	ResumeAlreadyLive = "already_live"
	// ResumeClosed: the session was re-attached, but the recovery
	// continuation closed the job (a terminal capsule verdict — wrapped,
	// failed, blocked — or an exhausted relaunch ladder); nothing
	// continues in place.
	ResumeClosed = "closed"
)

// CauseOperatorAttach is the recovery cause of a session attach after a
// graceful operator shutdown; the bridge (internal/polyforge/bridge) imports
// it so an interruption under this cause archives no attempt.
const CauseOperatorAttach = "operator daemon shutdown"

// Recovery causes that are lifecycle events, exempt from the
// consecutive-failures ledger.
const (
	causeDaemonRestart  = "daemon_restart"
	causeOperatorAttach = CauseOperatorAttach
	// causeOperatorResume is the live-holder operator resume
	// (wi_seWTgHeKtaa8): a deliberate stop's deliberate continuation —
	// a lifecycle event like the operator attach, never a failure the
	// consecutive-failures ledger should count.
	causeOperatorResume = "operator resume"
)
