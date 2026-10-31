package core

// session_test.go — the v1.5 session model's core-level red lines: CAS
// race, replay by the session secret and the stale-session refusal,
// compare-only verify, attach gates, force-end
// + audit event, session-end fence + full field clear, claim gates, the
// pause/clean_pause distinction, and the git credential's session path.

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func sessionEnv(t *testing.T) (*testEnv, *SessionManager) {
	t.Helper()
	env := newEnv(t)
	return env, NewSessionManager(env.DB)
}

// mustSessionClaim claims wiID with a fresh secret. The shared "m1"
// machine fixture carries a machines row owned by the caller
// (only a caller-owned machine id is recorded).
func mustSessionClaim(t *testing.T, m *SessionManager, env *testEnv, wiID, secret string) *api.SessionClaim {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT OR IGNORE INTO machines(id, last_heartbeat_at, role, operator_id)
		VALUES ('m1', ?1, 'runner', ?2)`, Now(), env.UserID); err != nil {
		t.Fatalf("seed m1: %v", err)
	}
	claim, err := m.Claim(wiID, api.SessionClaimRequest{
		SessionID: secret, MachineID: "m1",
	}, env.UserID)
	if err != nil {
		t.Fatalf("session claim: %v", err)
	}
	return claim
}

// rowSession reads the raw session columns for direct assertions
// (nil pointers = NULL columns).
func rowSession(t *testing.T, env *testEnv, wiID string) (status string, hash, fp, machine, started *string, cleanPause bool) {
	t.Helper()
	var h, f, m, st sql.NullString
	err := env.DB.QueryRow(`SELECT status, session_hash, session_fingerprint, session_machine,
		session_started_at, clean_pause FROM work_items WHERE id=?1`, wiID).
		Scan(&status, &h, &f, &m, &st, &cleanPause)
	if err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if h.Valid {
		hash = &h.String
	}
	if f.Valid {
		fp = &f.String
	}
	if m.Valid {
		machine = &m.String
	}
	if st.Valid {
		started = &st.String
	}
	return
}

// TestSessionClaimCASRace: two concurrent claims of one queued WI —
// exactly one wins the CAS; the loser answers CORE-409 ALREADY_HELD and
// the stored session is the winner's.
func TestSessionClaimCASRace(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "race goal", nil, "")

	const racers = 2
	var wg sync.WaitGroup
	results := make([]*api.SessionClaim, racers)
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = m.Claim(id, api.SessionClaimRequest{
				SessionID: api.SessionFingerprint("racer") + strings.Repeat(string(rune('a'+i)), 60),
				MachineID: "m",
			}, env.UserID)
		}(i)
	}
	wg.Wait()

	wins := 0
	var winner *api.SessionClaim
	for i := 0; i < racers; i++ {
		switch {
		case errs[i] == nil:
			wins++
			if winner == nil {
				winner = results[i]
			}
		case IsCode(errs[i], api.ErrCoreConflict) && ErrorReason(errs[i]) == "ALREADY_HELD":
		default:
			t.Fatalf("racer %d: wrong error %v", i, errs[i])
		}
	}
	if wins != 1 || winner == nil {
		t.Fatalf("CAS race: %d winners, want exactly 1 (errs: %v)", wins, errs)
	}
	// The winner's write landed: running, hash = sha256 of the winner's
	// secret (the claim itself never exposes the secret, so recompute via
	// the known racer-secret scheme is impossible here — assert shape).
	status, hash, fp, _, _, _ := rowSession(t, env, id)
	if status != api.StatusRunning || hash == nil || *hash == "" || fp == nil || *fp != winner.SessionFingerprint {
		t.Fatalf("winner's write did not land: status=%s hash=%v fp=%v", status, *hash, *fp)
	}
}

// TestSessionClaimReplayBySecret (D4): the holding session's repeated
// claim answers the same claim and writes nothing; another secret is refused ALREADY_HELD
// (exclusivity).
func TestSessionClaimReplayBySecret(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "idem goal", nil, "")
	secret := "0123456789abcdef0123456789abcdef"
	c1 := mustSessionClaim(t, m, env, id, secret)
	for i := 0; i < 2; i++ {
		c2, err := m.Claim(id, api.SessionClaimRequest{SessionID: secret, MachineID: "m1"}, env.UserID)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !reflect.DeepEqual(c1, c2) {
			t.Fatalf("replay %d answered %+v, want the same claim %+v", i, c2, c1)
		}
	}
	if n := countEvents(t, env, id, "session_claimed"); n != 1 {
		t.Fatalf("a replay must write nothing: %d session_claimed events", n)
	}
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: "ffffffffffffffffffffffffffffffff"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "ALREADY_HELD" {
		t.Fatalf("another secret must be refused ALREADY_HELD: %v", err)
	}
}

// TestSessionReplayAfterForceEndIsRefused: a session that was force-ended,
// superseded or ended never claims the work item again, and the refusal
// writes nothing.
func TestSessionReplayAfterForceEndIsRefused(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "stale goal", nil, "")
	old := "0123456789abcdef0123456789abcdef"
	mustSessionClaim(t, m, env, id, old)
	if err := m.ForceEnd(id, "machine lost", env.UserID); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	refused := func(when string) {
		t.Helper()
		_, err := m.Claim(id, api.SessionClaimRequest{SessionID: old, MachineID: "m1"}, env.UserID)
		if !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
			t.Fatalf("%s: the old session's claim must be refused STALE_SESSION: %v", when, err)
		}
	}
	refused("after force-end")
	if status, hash, _, _, _, _ := rowSession(t, env, id); status != api.StatusQueued || hash != nil {
		t.Fatalf("a refused stale claim wrote the row: status=%s hash=%v", status, hash)
	}
	// Another daemon claims the freed work item; the old session stays out.
	next := "fedcba9876543210fedcba9876543210"
	mustSessionClaim(t, m, env, id, next)
	refused("after supersession")
	// The new holder ends the work item: neither session claims it again.
	if err := m.End(id, api.SessionEndRequest{SessionID: next, Status: api.StatusBlocked, Note: "stop"}, env.UserID); err != nil {
		t.Fatalf("end: %v", err)
	}
	refused("after the holder's end")
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: next}, env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("the ended session's claim must be refused STALE_SESSION: %v", err)
	}
}

// countEvents counts the work item's events of one type.
func countEvents(t *testing.T, env *testEnv, wiID, eventType string) int {
	t.Helper()
	events, err := env.WIs.Events(wiID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	n := 0
	for _, e := range events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}

// TestSessionVerifyCompareOnly (B3): match, mismatch and unheld answers;
// verify never mutates anything and never echoes the secret.
func TestSessionVerifyCompareOnly(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "feedfacefeedfacefeedfacefeedface"
	// Unheld: matches=false, no current fingerprint, status served.
	id := mustCreateExecWI(t, env, "verify goal", nil, "")
	resp, err := m.Verify(id, secret)
	if err != nil {
		t.Fatalf("verify unheld: %v", err)
	}
	if resp.Matches || resp.CurrentFingerprint != "" || resp.Status != api.StatusQueued || resp.CleanPause {
		t.Fatalf("unheld verify wrong: %+v", resp)
	}
	// Held + matching secret.
	mustSessionClaim(t, m, env, id, secret)
	resp, err = m.Verify(id, secret)
	if err != nil {
		t.Fatalf("verify held: %v", err)
	}
	if !resp.Matches || resp.Status != api.StatusRunning || resp.CurrentFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("held verify wrong: %+v", resp)
	}
	// Held + foreign secret: the answer names the current holder.
	resp, err = m.Verify(id, "deadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("verify mismatch: %v", err)
	}
	if resp.Matches || resp.CurrentFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("mismatch verify wrong: %+v", resp)
	}
	// Compare-only: no event was written and the status did not move.
	events, err := env.WIs.Events(id)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if strings.HasPrefix(e.Type, "session_") && e.Type != "session_claimed" {
			t.Fatalf("verify wrote a session event: %+v", e)
		}
	}
	// Unknown WI: typed 404.
	if _, err := m.Verify("wi_nosuchthing", secret); !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("unknown WI verify: %v", err)
	}
}

// TestSessionAttachGates: attach requires hash match AND status==paused
// AND clean_pause==0; each violation gets its typed refusal.
func TestSessionAttachGates(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "12341234123412341234123412341234"
	id := mustCreateExecWI(t, env, "attach goal", nil, "")
	mustSessionClaim(t, m, env, id, secret)

	// Running (not paused): NOT_ATTACHABLE.
	if _, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_ATTACHABLE" {
		t.Fatalf("attach of running WI allowed: %v", err)
	}
	// Wrong secret on a paused WI: STALE_SESSION.
	if err := m.Pause(id, secret, false, "", env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := m.Attach(id, api.SessionAttachRequest{SessionID: "wrong"}, env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("attach with wrong secret allowed: %v", err)
	}
	// Happy path: same session resumes.
	claim, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret, MachineID: "m1"}, env.UserID)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if claim.SessionFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("attach answered a foreign fingerprint: %+v", claim)
	}
	// The session_attached event is on the timeline.
	found := false
	events, _ := env.WIs.Events(id)
	for _, e := range events {
		if e.Type == "session_attached" {
			found = true
		}
	}
	if !found {
		t.Fatalf("session_attached event missing")
	}
	// Operator pause (clean_pause): attach refuses, only a force-end (or a
	// CLI unpause) may resume.
	if err := m.Pause(id, secret, true, "operator pause", env.UserID); err != nil {
		t.Fatalf("clean pause: %v", err)
	}
	st, _, _, _, _, cp := rowSession(t, env, id)
	if st != api.StatusPaused || !cp {
		t.Fatalf("clean pause did not land: status=%s clean_pause=%v", st, cp)
	}
	if _, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_ATTACHABLE" {
		t.Fatalf("attach of clean-paused WI allowed: %v", err)
	}
}

// TestSessionForceEnd: a running WI holding a session is force-ended back to
// queued, EVERY session field is cleared, the old secret dies everywhere
// (git face, End, Pause, Verify), the OLD fingerprint/machine ride the
// session_superseded event, and a paused holder is ended the same way.
func TestSessionForceEnd(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "force-end goal", nil, "")
	oldSecret := "01010101010101010101010101010101"
	mustSessionClaim(t, m, env, id, oldSecret)

	if err := m.ForceEnd(id, "machine lost", env.UserID); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	st, hash, fp, _, _, cp := rowSession(t, env, id)
	if st != api.StatusQueued || cp || (hash != nil && *hash != "") || (fp != nil && *fp != "") {
		t.Fatalf("force-end did not clear the session: status=%s clean=%v hash=%v fp=%v", st, cp, hash, fp)
	}
	if err := env.Owners.CheckSessionCredential(id, id, oldSecret); err == nil {
		t.Fatal("old secret still authenticates")
	}
	if err := m.End(id, api.SessionEndRequest{SessionID: oldSecret, Status: api.StatusWrapped}, env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("old secret can still end: %v", err)
	}
	if err := m.Pause(id, oldSecret, false, "", env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("old secret can still pause: %v", err)
	}
	if v, err := m.Verify(id, oldSecret); err != nil || v.Matches {
		t.Fatalf("old secret still verifies: %+v %v", v, err)
	}
	// The audit event carries the OLD fingerprint and the transition.
	found := false
	events, _ := env.WIs.Events(id)
	for _, e := range events {
		if e.Type != "session_superseded" {
			continue
		}
		found = true
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("superseded payload: %v", err)
		}
		if p["old_session_fingerprint"] != api.SessionFingerprint(oldSecret) ||
			p["from"] != api.StatusRunning || p["to"] != api.StatusQueued || p["reason"] != "machine lost" {
			t.Fatalf("superseded event payload wrong: %s", e.Payload)
		}
	}
	if !found {
		t.Fatalf("session_superseded event missing")
	}
	// The WI is claimable again by a fresh session.
	mustSessionClaim(t, m, env, id, "02020202020202020202020202020202")

	// A paused holder is force-ended too.
	pid := mustCreateExecWI(t, env, "force-end paused", nil, "")
	pSecret := "05050505050505050505050505050505"
	mustSessionClaim(t, m, env, pid, pSecret)
	if err := m.Pause(pid, pSecret, true, "operator", env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := m.ForceEnd(pid, "operator walked away", env.UserID); err != nil {
		t.Fatalf("force-end of paused WI: %v", err)
	}
	st, hash, _, _, _, cp = rowSession(t, env, pid)
	if st != api.StatusQueued || cp || (hash != nil && *hash != "") {
		t.Fatalf("paused force-end wrong: status=%s clean=%v hash=%v", st, cp, hash)
	}
	if err := m.End(pid, api.SessionEndRequest{SessionID: pSecret, Status: api.StatusWrapped}, env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("paused-holder old secret can still end: %v", err)
	}
}

// TestSessionForceEndRefusals: reason required, a queued (unheld) WI is
// NOT_HELD, a terminal WI is Gone.
func TestSessionForceEndRefusals(t *testing.T) {
	env, m := sessionEnv(t)
	qid := mustCreateExecWI(t, env, "queued force-end", nil, "")
	if err := m.ForceEnd(qid, "r", env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_HELD" {
		t.Fatalf("force-end of queued WI allowed: %v", err)
	}
	id := mustCreateExecWI(t, env, "refusals", nil, "")
	secret := "06060606060606060606060606060606"
	mustSessionClaim(t, m, env, id, secret)
	for _, reason := range []string{"", "   "} {
		if err := m.ForceEnd(id, reason, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
			t.Fatalf("empty reason %q accepted: %v", reason, err)
		}
	}
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped}, env.UserID); err != nil {
		t.Fatalf("end: %v", err)
	}
	if err := m.ForceEnd(id, "r", env.UserID); !IsCode(err, api.ErrCoreGone) || ErrorReason(err) != "TERMINAL_STATE" {
		t.Fatalf("force-end of terminal WI allowed: %v", err)
	}
}

// TestSessionEndFenceAndClear: the end is fenced on the live session
// (a foreign secret answers CORE-403 STALE_SESSION), a successful end
// clears EVERY session field, blocked is excluded from the claim gate
// (M2), and the session_ended event is recorded.
func TestSessionEndFenceAndClear(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	id := mustCreateExecWI(t, env, "end goal", nil, "")
	mustSessionClaim(t, m, env, id, secret)

	// Fence: a foreign secret cannot end the WI.
	if err := m.End(id, api.SessionEndRequest{SessionID: "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", Status: api.StatusWrapped}, env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("session-end fence broken: %v", err)
	}
	// Bad status: 400 (paused is LEGAL now — merge amend: it routes to the
	// M3 pause transition; use a genuinely illegal status).
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: "queued"}, env.UserID); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("bad status accepted: %v", err)
	}
	// Blocked (M2): session cleared + status blocked.
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked, Note: "bounded fail"}, env.UserID); err != nil {
		t.Fatalf("session-end blocked: %v", err)
	}
	status, hash, fp, machine, started, clean := rowSession(t, env, id)
	if status != api.StatusBlocked || hash != nil || fp != nil || machine != nil || started != nil || clean {
		t.Fatalf("blocked did not clear the session row: status=%s hash=%v fp=%v machine=%v started=%v clean=%v",
			status, hash, fp, machine, started, clean)
	}
	// Blocked is NOT claimable (M2): excluded until a human unblocks.
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: "0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c0c"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_CLAIMABLE" {
		t.Fatalf("blocked WI is claimable: %v", err)
	}
	// The session_ended event carries the ended fingerprint + status.
	found := false
	events, _ := env.WIs.Events(id)
	for _, e := range events {
		if e.Type == "session_ended" {
			found = true
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			if p["status"] != api.StatusBlocked || p["session_fingerprint"] != api.SessionFingerprint(secret) {
				t.Fatalf("session_ended payload wrong: %s", e.Payload)
			}
		}
	}
	if !found {
		t.Fatalf("session_ended event missing")
	}
}

// TestOwnerDoorsOnBlockedWI (polyforge#116): the §6.4 owner exits on a
// workflow parked blocked on an honest incomplete result. Continue
// (resume: blocked → queued for the owner's re-claim — the claim gate
// itself stays queued-only, so a plain claim still refuses) and abandon
// (cancel: blocked → canceled). The resume door refuses a non-blocked
// work item and records its event.
func TestOwnerDoorsOnBlockedWI(t *testing.T) {
	env, m := sessionEnv(t)
	id := mustCreateExecWI(t, env, "owner doors on blocked", nil, "")

	// The resume door refuses a non-blocked work item (the parked shape only).
	if _, err := env.WIs.ResumeBlocked(id, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_BLOCKED" {
		t.Fatalf("the resume door accepted a queued WI: %v", err)
	}

	// Park blocked the way the runtime's bounded close does: session
	// cleared, waiting_on=owner.
	secret := "1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a"
	mustSessionClaim(t, m, env, id, secret)
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked, Note: "incomplete"}, env.UserID); err != nil {
		t.Fatalf("session-end blocked: %v", err)
	}

	// The claim gate stays queued-only (M2): a plain claim of the parked
	// WI still refuses — the owner moves go through their own doors.
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: "1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_CLAIMABLE" {
		t.Fatalf("a plain claim of a parked WI must still refuse: %v", err)
	}

	// The resume door: blocked → queued (wi_resumed), waiting_on cleared.
	if _, err := env.WIs.ResumeBlocked(id, env.UserID); err != nil {
		t.Fatalf("resume-blocked: %v", err)
	}
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wi.Status != api.StatusQueued {
		t.Fatalf("the resume door must land queued: %s", wi.Status)
	}
	if wi.WaitingOn != nil {
		t.Fatalf("the resume door must clear waiting_on: %v", *wi.WaitingOn)
	}
	// The owner's re-claim now goes through the unchanged claim gate.
	reclaim := "1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c"
	mustSessionClaim(t, m, env, id, reclaim)

	// Park blocked again; the abandon exit (cancel) lands canceled with
	// closed_at and the session row cleared (M2 holds on every terminal
	// transition).
	if err := m.End(id, api.SessionEndRequest{SessionID: reclaim, Status: api.StatusBlocked, Note: "incomplete again"}, env.UserID); err != nil {
		t.Fatalf("second session-end blocked: %v", err)
	}
	if _, err := env.WIs.Cancel(id, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("abandon (cancel) of the parked WI: %v", err)
	}
	wi, err = env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wi.Status != api.StatusCanceled {
		t.Fatalf("the abandon exit must land canceled: %s", wi.Status)
	}
	if wi.ClosedAt == nil {
		t.Fatalf("the abandon exit must set closed_at")
	}
	status, hash, _, _, _, _ := rowSession(t, env, id)
	if status != api.StatusCanceled || hash != nil {
		t.Fatalf("the abandon exit must leave the session cleared: status=%s hash=%v", status, hash)
	}
	// The exits record their events.
	found := map[string]bool{}
	events, _ := env.WIs.Events(id)
	for _, e := range events {
		found[e.Type] = true
	}
	if !found["wi_resumed"] || !found["work_item_canceled"] {
		t.Fatalf("the exits' events missing: %v", found)
	}
}

// TestSessionClaimGates: the M1 joint gate's typed refusals — terminal
// (CORE-410), no bundle / not queued (CORE-409 NOT_CLAIMABLE),
// dependency-blocked (CORE-409 DEPENDENCY_BLOCKED).
func TestSessionClaimGates(t *testing.T) {
	env, m := sessionEnv(t)
	// Terminal: a wrapped WI answers CORE-410 whatever else is true.
	tid := mustCreateExecWI(t, env, "terminal gate", nil, "")
	mustSessionClaim(t, m, env, tid, "12121212121212121212121212121212")
	if err := m.End(tid, api.SessionEndRequest{SessionID: "12121212121212121212121212121212", Status: api.StatusWrapped}, env.UserID); err != nil {
		t.Fatalf("end: %v", err)
	}
	if _, err := m.Claim(tid, api.SessionClaimRequest{SessionID: "13131313131313131313131313131313"}, env.UserID); !IsCode(err, api.ErrCoreGone) || ErrorReason(err) != "TERMINAL_STATE" {
		t.Fatalf("terminal claim not refused: %v", err)
	}
	// No bundle: NOT_CLAIMABLE.
	rid := mustCreateWI(t, env, "registry gate", nil, "")
	if _, err := m.Claim(rid, api.SessionClaimRequest{SessionID: "14141414141414141414141414141414"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "NOT_CLAIMABLE" {
		t.Fatalf("registration claim allowed: %v", err)
	}
	// Dependency-blocked: DEPENDENCY_BLOCKED wins over NOT_CLAIMABLE for
	// a WI with both problems (blockers first, then bundle).
	blocker := mustCreateExecWI(t, env, "blocker exec", nil, "")
	blockedReg := mustCreateWI(t, env, "blocked registry", nil, "")
	if err := env.WIs.AddDependency(blocker, blockedReg, "blocks"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(blockedReg, api.SessionClaimRequest{SessionID: "15151515151515151515151515151515"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_BLOCKED" {
		t.Fatalf("dependency diagnosis lost: %v", err)
	}
	// A blocked dependency on an execution ticket: DEPENDENCY_BLOCKED.
	blockedExec := mustCreateExecWI(t, env, "blocked exec", nil, "")
	if err := env.WIs.AddDependency(blocker, blockedExec, "blocks"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(blockedExec, api.SessionClaimRequest{SessionID: "16161616161616161616161616161616"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_BLOCKED" {
		t.Fatalf("dependency-blocked exec claim allowed: %v", err)
	}
}

// TestCheckSessionCredentialSessionPath: the git credential check validates
// hex(sha256(password)) == session_hash AND status==running (M3); the
// mismatch names the current holder fingerprint.
func TestCheckSessionCredentialSessionPath(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a"
	id := mustCreateExecWI(t, env, "push auth", nil, "")
	mustSessionClaim(t, m, env, id, secret)

	if err := env.Owners.CheckSessionCredential(id, id, secret); err != nil {
		t.Fatalf("valid session secret rejected: %v", err)
	}
	// Mismatch: GIT-401, detail names the current holder.
	err := env.Owners.CheckSessionCredential(id, id, "1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b")
	if !IsCode(err, api.ErrGitUnauthorized) || ErrorReason(err) != "BAD_CREDENTIAL" {
		t.Fatalf("wrong secret accepted: %v", err)
	}
	if !strings.Contains(err.Error(), api.SessionFingerprint(secret)) {
		t.Fatalf("mismatch does not name the holder: %v", err)
	}
	// Paused (M3): GIT-403, no push while paused.
	if err := m.Pause(id, secret, false, "", env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	err = env.Owners.CheckSessionCredential(id, id, secret)
	if !IsCode(err, api.ErrGitForbidden) || ErrorReason(err) != "PAUSED" {
		t.Fatalf("paused push not refused: %v", err)
	}
	// After a force-end the old secret is dead.
	if _, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret}, env.UserID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := m.ForceEnd(id, "swap", env.UserID); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	if err := env.Owners.CheckSessionCredential(id, id, secret); !IsCode(err, api.ErrGitUnauthorized) {
		t.Fatalf("superseded secret still valid: %v", err)
	}
	// Wrong username never authenticates.
	if err := env.Owners.CheckSessionCredential(id, "otheruser", "1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c"); !IsCode(err, api.ErrGitUnauthorized) {
		t.Fatalf("wrong username accepted: %v", err)
	}
}

// TestSessionPauseCleanFlagDistinction (M3): operator pause sets
// clean_pause=1 (restart must not auto-resume), daemon shutdown keeps 0;
// the session survives the pause either way.
func TestSessionPauseCleanFlagDistinction(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d"
	id := mustCreateExecWI(t, env, "pause flags", nil, "")
	mustSessionClaim(t, m, env, id, secret)

	// Daemon shutdown pause: clean_pause stays 0, session kept.
	if err := m.Pause(id, secret, false, "", env.UserID); err != nil {
		t.Fatalf("shutdown pause: %v", err)
	}
	st, hash, _, _, _, cp := rowSession(t, env, id)
	if st != api.StatusPaused || cp || hash == nil || *hash != api.SessionHashHex(secret) {
		t.Fatalf("shutdown pause wrong: status=%s clean=%v hash=%v", st, cp, hash)
	}
	// Operator pause: attach back first (a paused WI pauses no further),
	// then clean_pause=1, session kept.
	if _, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret}, env.UserID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := m.Pause(id, secret, true, "operator", env.UserID); err != nil {
		t.Fatalf("operator pause: %v", err)
	}
	st, hash, _, _, _, cp = rowSession(t, env, id)
	if st != api.StatusPaused || !cp || hash == nil {
		t.Fatalf("operator pause wrong: status=%s clean=%v", st, cp)
	}
	// Both pauses write their session_paused event with the flag.
	n := 0
	events, _ := env.WIs.Events(id)
	for _, e := range events {
		if e.Type == "session_paused" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("session_paused events = %d, want 2", n)
	}
	// A foreign session cannot pause (fenced).
	if err := m.Pause(id, "1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e", true, "", env.UserID); !IsCode(err, api.ErrCoreForbidden) || ErrorReason(err) != "STALE_SESSION" {
		t.Fatalf("foreign pause allowed: %v", err)
	}
}

// TestWorkItemCarriesPublicSessionBlockNeverHash: the m4 read surface —
// the work item view carries the public audit fields, and no view
// anywhere contains session_hash.
func TestWorkItemCarriesPublicSessionBlockNeverHash(t *testing.T) {
	env, m := sessionEnv(t)
	secret := "1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f1f"
	id := mustCreateExecWI(t, env, "view goal", nil, "")
	mustSessionClaim(t, m, env, id, secret)

	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	view, err := env.WIs.Get(wi.ID, GetOptions{})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.SessionFingerprint != api.SessionFingerprint(secret) ||
		view.SessionMachine != "m1" ||
		view.SessionStartedAt == "" || view.CleanPause {
		t.Fatalf("view session block wrong: %+v", view.SessionPublic)
	}
	blob, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "session_hash") || strings.Contains(string(blob), secret) {
		t.Fatalf("view leaks the hash or the secret: %s", blob)
	}
	// A second WI keeps a zero block.
	id2 := mustCreateExecWI(t, env, "unheld view", nil, "")
	wi2, _ := env.WIs.Get(id2, GetOptions{})
	view2, err := env.WIs.Get(wi2.ID, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if view2.SessionFingerprint != "" || view2.CleanPause {
		t.Fatalf("unheld view block not empty: %+v", view2.SessionPublic)
	}
}

// TestRacePatchBundleVsClaim: PATCH bundle vs session Claim on one
// queued, never-claimed, childless ticket (the w140 race pair 1). The
// single-writer connection serializes the two transactions and no extra
// arbitration exists: the Claim always succeeds; the PATCH succeeds iff
// it was serialized first (the claim then froze the renamed bundle) and
// is otherwise a typed refusal that wrote nothing. Either way the end
// state is consistent and the bundle is frozen afterwards.
func TestRacePatchBundleVsClaim(t *testing.T) {
	env, m := sessionEnv(t)
	const original, renamed = "w140-race1-original", "w140-race1-renamed"
	patchWins, claimFirst := 0, 0
	for i := 0; i < 10; i++ {
		wi, err := env.WIs.Create(CreateRequest{
			Project: "alpha", Goal: "w140 race1", Reporter: env.UserID,
			BundleName: strPtrTest(original),
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var patchErr, claimErr error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, patchErr = env.WIs.Patch(wi.ID, map[string]json.RawMessage{"bundle_name": json.RawMessage(`"` + renamed + `"`)}, env.UserID)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, claimErr = m.Claim(wi.ID, api.SessionClaimRequest{SessionID: "w140-race1-secret"}, env.UserID)
		}()
		close(start)
		wg.Wait()

		if claimErr != nil {
			t.Fatalf("round %d: a concurrent bundle PATCH must never refuse the claim: %v", i, claimErr)
		}
		got, err := env.WIs.Get(wi.ID, GetOptions{})
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != api.StatusRunning {
			t.Fatalf("round %d: status = %s, want running", i, got.Status)
		}
		want := original
		if patchErr == nil {
			patchWins++
			want = renamed
		} else {
			claimFirst++
			if !IsCode(patchErr, api.ErrCoreBadInput) && !IsCode(patchErr, api.ErrCoreConflict) {
				t.Fatalf("round %d: a PATCH serialized after the claim is not a typed refusal: %v", i, patchErr)
			}
		}
		if got.BundleName == nil || *got.BundleName != want {
			t.Fatalf("round %d: bundle = %v, want %s (patchErr=%v)", i, got.BundleName, want, patchErr)
		}
		// frozen at the claim: any later bundle PATCH is refused.
		if _, err := env.WIs.Patch(wi.ID, map[string]json.RawMessage{"bundle_name": json.RawMessage(`"w140-race1-late"`)}, env.UserID); err == nil {
			t.Fatalf("round %d: bundle PATCH after the claim succeeded; the bundle must be frozen", i)
		}
	}
	t.Logf("w140 race1: PATCH serialized first %d, claim first %d of 10 rounds", patchWins, claimFirst)
}

// polyforge#161: only a WRAPPED blocker releases its dependent. A failed
// or canceled blocker keeps the dependent out of the ready queue, claim
// keeps refusing DEPENDENCY_BLOCKED and the derived waiting_for names the
// blocker; removing the edge is the way forward.
func TestDependencyOnlyWrappedBlockerReleases(t *testing.T) {
	env, m := sessionEnv(t)
	inItems := func(id string) bool {
		t.Helper()
		q, err := env.WIs.ReadyQueue("alpha")
		if err != nil {
			t.Fatalf("ready queue: %v", err)
		}
		for _, wi := range q.Items {
			if wi.ID == id {
				return true
			}
		}
		return false
	}
	waitingFor := func(id string) []WaitingBlocker {
		t.Helper()
		wi, err := env.WIs.Get(id, GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		v, err := env.WIs.Get(wi.ID, GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return v.WaitingFor
	}

	// failed blocker
	failedBlocker := mustCreateExecWI(t, env, "blocker that fails", nil, "")
	depF := mustCreateExecWI(t, env, "after the failing one", nil, "")
	if err := env.WIs.AddDependency(failedBlocker, depF, "blocks"); err != nil {
		t.Fatal(err)
	}
	if w := waitingFor(depF); len(w) != 1 || w[0].Status != api.StatusQueued || w[0].Slug == "" {
		t.Fatalf("waiting_for on a queued blocker = %+v", w)
	}
	secret := "21212121212121212121212121212121"
	mustSessionClaim(t, m, env, failedBlocker, secret)
	if err := m.End(failedBlocker, api.SessionEndRequest{SessionID: secret, Status: api.StatusFailed}, env.UserID); err != nil {
		t.Fatalf("end failed: %v", err)
	}
	if inItems(depF) {
		t.Fatalf("a failed blocker released its dependent into the ready queue")
	}
	if _, err := m.Claim(depF, api.SessionClaimRequest{SessionID: "22222222222222222222222222222222"}, env.UserID); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_BLOCKED" {
		t.Fatalf("claim behind a failed blocker: %v", err)
	}
	if w := waitingFor(depF); len(w) != 1 || w[0].Status != api.StatusFailed {
		t.Fatalf("waiting_for behind a failed blocker = %+v", w)
	}
	// Removing the edge releases it.
	if err := env.WIs.RemoveDependency(failedBlocker, depF, "blocks"); err != nil {
		t.Fatal(err)
	}
	if !inItems(depF) || len(waitingFor(depF)) != 0 {
		t.Fatalf("edge removal did not release the dependent")
	}

	// canceled blocker
	canceledBlocker := mustCreateExecWI(t, env, "blocker that is canceled", nil, "")
	depC := mustCreateExecWI(t, env, "after the canceled one", nil, "")
	if err := env.WIs.AddDependency(canceledBlocker, depC, "blocks"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.Cancel(canceledBlocker, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	if inItems(depC) {
		t.Fatalf("a canceled blocker released its dependent into the ready queue")
	}
	if w := waitingFor(depC); len(w) != 1 || w[0].Status != api.StatusCanceled {
		t.Fatalf("waiting_for behind a canceled blocker = %+v", w)
	}

	// wrapped blocker releases
	wrappedBlocker := mustCreateExecWI(t, env, "blocker that wraps", nil, "")
	depW := mustCreateExecWI(t, env, "after the wrapped one", nil, "")
	if err := env.WIs.AddDependency(wrappedBlocker, depW, "blocks"); err != nil {
		t.Fatal(err)
	}
	secret = "23232323232323232323232323232323"
	mustSessionClaim(t, m, env, wrappedBlocker, secret)
	if inItems(depW) {
		t.Fatalf("a running blocker released its dependent")
	}
	if err := m.End(wrappedBlocker, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped}, env.UserID); err != nil {
		t.Fatalf("end wrapped: %v", err)
	}
	if !inItems(depW) || len(waitingFor(depW)) != 0 {
		t.Fatalf("a wrapped blocker did not release its dependent")
	}
	mustSessionClaim(t, m, env, depW, "24242424242424242424242424242424")
	// A claimed (non-queued) WI carries no waiting_for.
	if w := waitingFor(depW); len(w) != 0 {
		t.Fatalf("running WI carries waiting_for %+v", w)
	}
}

// polyforge#161: a work item that still blocks another cannot unbind its
// bundle (it would run nothing to release them); rebinding stays allowed, and unbinding
// works once the edge is gone.
func TestUnbindRefusedWhileBlocking(t *testing.T) {
	env := newEnv(t)
	a := mustCreateExecWI(t, env, "blocker", nil, "")
	b := mustCreateExecWI(t, env, "dependent", nil, "")
	if err := env.WIs.AddDependency(a, b, "blocks"); err != nil {
		t.Fatal(err)
	}
	bSlug := func() string {
		wi, err := env.WIs.Get(b, GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return wi.Slug
	}()
	_, err := env.WIs.Patch(a, map[string]json.RawMessage{"bundle_name": json.RawMessage(`null`)}, env.UserID)
	if !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "BLOCKER_STILL_BLOCKS" ||
		!strings.Contains(err.Error(), bSlug) || !strings.Contains(err.Error(), "remove the edges first") {
		t.Fatalf("unbind of a blocker: %v", err)
	}
	if _, err := env.WIs.Patch(a, map[string]json.RawMessage{"bundle_name": json.RawMessage(`"other-bundle"`)}, env.UserID); err != nil {
		t.Fatalf("rebind of a blocker refused: %v", err)
	}
	if err := env.WIs.RemoveDependency(a, b, "blocks"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.WIs.Patch(a, map[string]json.RawMessage{"bundle_name": json.RawMessage(`null`)}, env.UserID); err != nil {
		t.Fatalf("unbind after edge removal: %v", err)
	}
}
