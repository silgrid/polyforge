package observe

// session.go: the v1.5 M4 session revalidation — the periodic holder
// check that bounds zombie discovery latency (the design record's
// ruling: push-cadence-dependent discovery is unbounded; a supersession
// (force-end + re-claim elsewhere) must be learned within ~30 seconds even
// when the zombie never pushes). A boundary push the git face refuses for
// its credential re-runs the check at once (onPushResult).
//
// Every monitor tick piggybacks a session-verify for each RUNNING job
// whose attachment carried a session id (RTMsg.SessionID travels
// runtime→observe IN-PROCESS; the secret never leaves the daemon). The
// verdict splits on the answer class:
//
//   - matches == false → the session was superseded (force-end
//     landed on another daemon). The monitor emits OBSuperseded once —
//     the runtime kills the tree, quarantines the capsule and releases
//     the record — and stops: no judging, no push into the capsule being
//     quarantined. The daemon makes NO hub transition:
//     superseded is not terminal, and the work item belongs to the new
//     owner.
//
//   - verify error (transport) → LOG ONLY, never act. A dead network
//     must not kill a live job: the check retries after a bounded
//     backoff (verifyErrorBackoff, so a down hub is probed at a sane
//     cadence instead of every 500ms poll tick).
//
// The aihub client seam is the narrow SessionVerifier interface
// (*aihubclient.Client satisfies it); nil — the default in every unit
// test and until the merge wires the shared client into observe.Config
// — simply disables the whole mechanism.

import (
	"errors"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// SessionVerifier is the M4 revalidation seam: everything the monitor
// needs of the aihub client for the periodic holder check and nothing
// more. The daemon binds the shared *aihubclient.Client (whose
// SessionVerify signature matches verbatim); tests bind fakes. nil on
// the config disables revalidation entirely.
type SessionVerifier interface {
	// SessionVerify is the compare-only holder check (reference.md §3.3):
	// the daemon presents its own session secret, the hub answers
	// matches plus the CURRENT holder's public fingerprint.
	SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error)
}

// Revalidation tunables.
const (
	// DefaultRevalidateEvery is the M4 cadence (per RUNNING job here;
	// the daemon hands the same cadence to the runtime for the retained
	// paused records).
	DefaultRevalidateEvery = 30 * time.Second
	// verifyErrorBackoff bounds the retry cadence after a TRANSPORT-class
	// verify failure: the next attempt waits this long (never the full
	// interval — the task's "retry next tick" — but never the raw poll
	// tick either, so a dead hub is probed at 30s, not 500ms).
	verifyErrorBackoff = 30 * time.Second
	// verifyCallBound bounds one verify call's wait from the monitor's
	// side: a hung transport must not pin verifyInFlight forever. The
	// bound is generous next to aihubclient's own 60s HTTP timeout — it
	// exists for the pathological hang, not the slow answer.
	verifyCallBound = 70 * time.Second
)

// verifyResult is the settle of one revalidation attempt.
type verifyResult struct {
	matches            bool
	currentFingerprint string // the new holder's public fingerprint (m4 audit)
	err                error
}

// sessionFingerprint derives the PUBLIC handle of this monitor's
// session secret (NEW-1 fence payload; stateMu-guarded read of the
// in-process secret — the fingerprint itself is public).
func (m *Monitor) sessionFingerprint() string {
	m.stateMu.Lock()
	id := m.sessionID
	m.stateMu.Unlock()
	if id == "" {
		return ""
	}
	return api.SessionFingerprint(id)
}

// checkRevalidate is the M4 tick rider: at most one verify in flight,
// never while quiet, only when the attachment carried a session id and
// the config bound a verifier. The call runs in a sub-goroutine — the
// select loop never blocks on the network.
func (m *Monitor) checkRevalidate() {
	if m.cfg.verify == nil || m.cfg.revalidate <= 0 || m.verifyInFlight {
		return
	}
	if m.sessionID == "" {
		return // attachment carries no session: nothing to verify
	}
	if m.isQuiet() {
		return
	}
	if !m.lastVerifyAt.IsZero() && time.Since(m.lastVerifyAt) < m.cfg.revalidate {
		return
	}
	m.verifyInFlight = true
	m.lastVerifyAt = time.Now()
	wi := m.wi.WorkItem
	sid := m.sessionID
	verify := m.cfg.verify
	go func() {
		type res struct {
			v   *api.SessionVerifyResponse
			err error
		}
		done := make(chan res, 1)
		go func() {
			v, err := verify.SessionVerify(wi, sid)
			done <- res{v, err}
		}()
		select {
		case r := <-done:
			if r.err != nil {
				m.verifyDone <- verifyResult{err: r.err}
				return
			}
			m.verifyDone <- verifyResult{matches: r.v.Matches, currentFingerprint: r.v.CurrentFingerprint}
		case <-time.After(verifyCallBound):
			m.verifyDone <- verifyResult{err: errors.New("session verify timed out")}
		}
	}()
}

// onVerifyResult settles one revalidation attempt (select-loop context).
func (m *Monitor) onVerifyResult(res verifyResult) {
	m.verifyInFlight = false
	if res.err != nil {
		// Transport-class failure: log and retry after the bounded
		// backoff — never act on a network error. lastVerifyAt is wound
		// back so the next attempt fires verifyErrorBackoff from now
		// (the full-interval slot is not consumed by a failure).
		backoff := verifyErrorBackoff
		if m.cfg.revalidate < backoff {
			backoff = m.cfg.revalidate
		}
		m.lastVerifyAt = time.Now().Add(backoff - m.cfg.revalidate)
		_ = m.log.Append(m.wi.WorkItem, EvSessionVerifyFailed, map[string]any{
			"error":  res.err.Error(),
			"detail": "session revalidation could not reach aihub; no action taken (transport error); retrying",
			// spawn_n is the audit handle; the session SECRET never
			// reaches an event body.
			"spawn_n": m.currentSpawn(),
		})
		return
	}
	if res.matches {
		return // still the holder: nothing to do
	}
	// matches == false: superseded. The monitor hands the
	// kill+quarantine+release to the runtime and stops (no judging, no
	// recovery request, no push into the capsule the runtime moves);
	// Observe makes NO hub transition (the new owner owns it). One
	// emission suffices: the OB queue never drops, and the runtime fences
	// OBSuperseded on the session fingerprint, not on the derivation
	// counter, so a recovery racing the verify cannot lose it.
	_ = m.log.Append(m.wi.WorkItem, EvSessionSuperseded, map[string]any{
		"current_fingerprint": res.currentFingerprint,
		"detail": "session revalidation learned this session no longer holds the work item (force-ended or claimed elsewhere); " +
			"local supervision stopped, runtime notified (no hub transition — superseded is not terminal)",
		"spawn_n": m.currentSpawn(),
	})
	m.sendOB(protocol.OBMsg{
		Kind: protocol.OBSuperseded, SpawnN: m.currentSpawn(),
		WorkItem: m.wi.WorkItem,
		Reason:   "session_superseded",
		Detail: "periodic session-verify answered matches=false; the current holder fingerprint is " +
			res.currentFingerprint,
		// NEW-1: the SUPERSEDED session's public fingerprint, so the
		// runtime never kills a successor job of the same WI with it.
		SessionFP: m.sessionFingerprint(),
	})
	m.requestStop()
}
