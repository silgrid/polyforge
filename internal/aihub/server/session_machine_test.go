package server

// session_machine_test.go — over HTTP: claim and
// session-attach record only a machine id owned by the caller. Another
// operator's machine is refused AUTH-403 with no state change (admins
// follow the same rule); an unknown machine records NULL and the
// claim succeeds, after which a progress query gets the existing
// CORE-410 missing-holder answer.
//
// The file is self-contained against the pre-change tree, so the
// external oracle can run it red on the base and green on the branch.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w74mSeedMachine inserts a machines row owned by operatorID ("" =
// NULL operator), the fixture the ownership rule reads.
func w74mSeedMachine(t *testing.T, e *srvEnv, machine, operatorID string) {
	t.Helper()
	var op any
	if operatorID != "" {
		op = operatorID
	}
	if _, err := e.db.Exec(`INSERT OR IGNORE INTO machines(id, last_heartbeat_at, role, operator_id)
		VALUES (?1, ?2, 'runner', ?3)`, machine, "2026-01-01T00:00:00Z", op); err != nil {
		t.Fatalf("seed machine %s: %v", machine, err)
	}
}

// w74mRow reads the session columns of a work item straight from the
// store.
func w74mRow(t *testing.T, e *srvEnv, wiID string) (status, hash, machine string) {
	t.Helper()
	if err := e.db.QueryRow(`SELECT status, COALESCE(session_hash,''), COALESCE(session_machine,'')
		FROM work_items WHERE id=?1`, wiID).Scan(&status, &hash, &machine); err != nil {
		t.Fatalf("row of %s: %v", wiID, err)
	}
	return
}

func TestClaimAndAttachMachineOwnershipOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	ownerID, _ := memberUser(t, e, "w74m-owner", "writer")
	callerID, callerKey := memberUser(t, e, "w74m-caller", "writer")
	w74mSeedMachine(t, e, "m-w74m-own", ownerID)

	// Another operator's machine on claim: AUTH-403, the work item
	// stays queued with no session and no event.
	wi := createExecWIOverHTTP(t, e, "w74m foreign claim")
	events := wiaEventCount(t, e, wi)
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi+"/claim", callerKey,
		map[string]string{"session_id": strings.Repeat("77", 16), "machine_id": "m-w74m-own"}, nil)
	if code != 403 {
		t.Fatalf("foreign-machine claim: %d %s", code, raw)
	}
	if c, _ := errEnvelope(t, raw); c != api.ErrAuthForbidden {
		t.Fatalf("foreign-machine claim code: %s", raw)
	}
	if st, hash, machine := w74mRow(t, e, wi); st != api.StatusQueued || hash != "" || machine != "" {
		t.Fatalf("rejected claim changed the row: %q/%q/%q", st, hash, machine)
	}
	if got := wiaEventCount(t, e, wi); got != events {
		t.Fatalf("rejected claim wrote events: %d -> %d", events, got)
	}
	// An admin names another operator's machine: the same refusal.
	code, raw = e.do(t, "POST", "/api/v1/work-items/"+wi+"/claim", e.key,
		map[string]string{"session_id": strings.Repeat("88", 16), "machine_id": "m-w74m-own"}, nil)
	if code != 403 || errCodeOf(raw) != api.ErrAuthForbidden {
		t.Fatalf("admin foreign-machine claim: %d %s", code, raw)
	}

	// A caller-owned machine is recorded; an unknown machine records
	// NULL while the claim succeeds.
	w74mSeedMachine(t, e, "m-w74m-mine", callerID)
	var resp struct {
		Claim struct {
			MachineID string `json:"machine_id"`
		} `json:"claim"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi+"/claim", callerKey,
		map[string]string{"session_id": strings.Repeat("11", 16), "machine_id": "m-w74m-mine"}, &resp); code != 200 || resp.Claim.MachineID != "m-w74m-mine" {
		t.Fatalf("caller-owned claim: %d %s %+v", code, raw, resp.Claim)
	}
	if _, _, machine := w74mRow(t, e, wi); machine != "m-w74m-mine" {
		t.Fatalf("caller-owned claim stored %q", machine)
	}

	unknown := createExecWIOverHTTP(t, e, "w74m unknown claim")
	secret := strings.Repeat("22", 16)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+unknown+"/claim", callerKey,
		map[string]string{"session_id": secret, "machine_id": "m-w74m-nobody"}, &resp); code != 200 || resp.Claim.MachineID != "" {
		t.Fatalf("unknown-machine claim: %d %s %+v", code, raw, resp.Claim)
	}
	if _, _, machine := w74mRow(t, e, unknown); machine != "" {
		t.Fatalf("unknown-machine claim stored %q, want NULL", machine)
	}
	// A progress query for the NULL-machine run: the existing CORE-410
	// missing-holder answer.
	priv, _ := signedMachineFor(t, e, "m-w74m-caller", callerID)
	if code, body := signedPost(t, e, callerKey, "m-w74m-caller", priv,
		"/api/v1/work-items/"+unknown+"/progress-query", api.ProgressQuery{WorkItem: unknown}); code != 410 || errCodeOf(body) != api.ErrCoreGone {
		t.Fatalf("progress query of a NULL-machine run: %d %s, want 410 %s", code, body, api.ErrCoreGone)
	}

	// Attach: another operator's machine is refused with the paused WI
	// untouched; an unknown machine records NULL.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+unknown+"/session-end", callerKey,
		map[string]any{"session_id": secret, "status": "paused", "clean_pause": false}, nil); code != 200 {
		t.Fatalf("pause: %d %s", code, raw)
	}
	code, raw = e.do(t, "POST", "/api/v1/work-items/"+unknown+"/session-attach", callerKey,
		map[string]string{"session_id": secret, "machine_id": "m-w74m-own"}, nil)
	if code != 403 || errCodeOf(raw) != api.ErrAuthForbidden {
		t.Fatalf("foreign-machine attach: %d %s", code, raw)
	}
	if st, hash, machine := w74mRow(t, e, unknown); st != api.StatusPaused || hash == "" || machine != "" {
		t.Fatalf("rejected attach changed the row: %q/%q/%q", st, hash, machine)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+unknown+"/session-attach", callerKey,
		map[string]string{"session_id": secret, "machine_id": "m-w74m-nobody2"}, &resp); code != 200 || resp.Claim.MachineID != "" {
		t.Fatalf("unknown-machine attach: %d %s %+v", code, raw, resp.Claim)
	}
	if st, _, machine := w74mRow(t, e, unknown); st != api.StatusRunning || machine != "" {
		t.Fatalf("unknown-machine attach left %q/%q, want running/NULL", st, machine)
	}
}
