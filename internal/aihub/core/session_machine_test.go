package core

// session_machine_test.go — claim and attach record only
// a machine id owned by the caller. A row owned by another operator is
// refused AUTH-403 inside the claim/attach transaction with nothing
// written; an absent or operator-less row records NULL and the
// claim/attach succeeds; admins follow the same rule.
//
// The file is self-contained against the pre-change tree, so the
// external oracle can run it red on the base and green on the branch.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w74mEventMachine reads the machine_id of the work item's latest
// session_claimed/session_attached event payload.
func w74mEventMachine(t *testing.T, env *testEnv, wiID, event string) string {
	t.Helper()
	var payload string
	if err := env.DB.QueryRow(`SELECT payload FROM wi_events
		WHERE work_item_id=?1 AND event_type=?2 ORDER BY rowid DESC LIMIT 1`, wiID, event).Scan(&payload); err != nil {
		t.Fatalf("event %s of %s: %v", event, wiID, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("payload of %s: %v", event, err)
	}
	machine, _ := m["machine_id"].(string)
	return machine
}

func TestClaimRecordsOnlyCallerOwnedMachine(t *testing.T) {
	env, m := sessionEnv(t)
	secret := func(b byte) string { return strings.Repeat(string(rune('a'+b)), 32) }

	// A caller-owned row is recorded, on the row and the event.
	registerMachine(t, env, "m-own", env.UserID, "pk-own")
	id := mustCreateExecWI(t, env, "machine ownership caller-owned", nil, "")
	claim, err := m.Claim(id, api.SessionClaimRequest{SessionID: secret(1), MachineID: "m-own"}, env.UserID)
	if err != nil {
		t.Fatalf("caller-owned claim: %v", err)
	}
	if claim.MachineID != "m-own" {
		t.Fatalf("claim recorded %q, want m-own", claim.MachineID)
	}
	if _, _, _, machine, _, _ := rowSession(t, env, id); machine == nil || *machine != "m-own" {
		t.Fatalf("session_machine = %v, want m-own", machine)
	}
	if got := w74mEventMachine(t, env, id, "session_claimed"); got != "m-own" {
		t.Fatalf("session_claimed event machine_id = %q, want m-own", got)
	}

	// Another operator's row is refused AUTH-403 with nothing written —
	// for a plain caller and for a global admin alike (each names the
	// OTHER user's machine).
	registerMachine(t, env, "m-foreign", env.UserID, "pk-foreign")
	registerMachine(t, env, "m-foreign2", env.UserIDB, "pk-foreign2")
	for _, tc := range []struct {
		caller, machine string
	}{
		{"caller", "m-foreign"}, // the writer names the admin's machine
		{"admin", "m-foreign2"}, // the admin names the writer's machine
	} {
		caller, who := tc.caller, env.UserIDB
		if caller == "admin" {
			who = env.UserID
		}
		foreign := mustCreateExecWI(t, env, "machine ownership foreign "+caller, nil, "")
		var events int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, foreign).Scan(&events); err != nil {
			t.Fatalf("count events: %v", err)
		}
		_, err := m.Claim(foreign, api.SessionClaimRequest{SessionID: secret(2), MachineID: tc.machine}, who)
		if !IsCode(err, api.ErrAuthForbidden) {
			t.Fatalf("%s foreign-machine claim: %v, want %s", caller, err, api.ErrAuthForbidden)
		}
		if status, hash, _, _, _, _ := rowSession(t, env, foreign); status != api.StatusQueued || hash != nil {
			t.Fatalf("%s rejected claim wrote the row: status=%s hash=%v", caller, status, hash)
		}
		var after int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, foreign).Scan(&after); err != nil || after != events {
			t.Fatalf("%s rejected claim wrote events: %d -> %d", caller, events, after)
		}
	}

	// An absent row and an operator-less row record NULL and succeed.
	seedMachineRow(t, env, "m-free", "", "")
	for _, tc := range []struct {
		machine, name string
	}{
		{"m-unknown", "absent row"},
		{"m-free", "operator-less row"},
	} {
		wi := mustCreateExecWI(t, env, "machine ownership "+tc.name, nil, "")
		claim, err := m.Claim(wi, api.SessionClaimRequest{SessionID: secret(3), MachineID: tc.machine}, env.UserID)
		if err != nil {
			t.Fatalf("%s claim: %v", tc.name, err)
		}
		if claim.MachineID != "" {
			t.Fatalf("%s recorded %q, want NULL", tc.name, claim.MachineID)
		}
		if _, _, _, machine, _, _ := rowSession(t, env, wi); machine != nil {
			t.Fatalf("%s stored session_machine %q, want NULL", tc.name, *machine)
		}
	}
}

func TestAttachRecordsOnlyCallerOwnedMachine(t *testing.T) {
	env, m := sessionEnv(t)
	secret := strings.Repeat("b", 32)
	id := mustCreateExecWI(t, env, "machine ownership attach", nil, "")
	mustSessionClaim(t, m, env, id, secret)
	if err := m.Pause(id, secret, false, "", env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Another operator's row: AUTH-403, the paused WI untouched — for a
	// plain caller and for a global admin alike.
	registerMachine(t, env, "m-att-foreign", env.UserID, "pk-att")
	registerMachine(t, env, "m-att-foreign2", env.UserIDB, "pk-att2")
	for _, tc := range []struct {
		caller, machine string
	}{
		{"caller", "m-att-foreign"}, // the writer names the admin's machine
		{"admin", "m-att-foreign2"}, // the admin names the writer's machine
	} {
		caller, who := tc.caller, env.UserIDB
		if caller == "admin" {
			who = env.UserID
		}
		_, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret, MachineID: tc.machine}, who)
		if !IsCode(err, api.ErrAuthForbidden) {
			t.Fatalf("%s foreign-machine attach: %v, want %s", caller, err, api.ErrAuthForbidden)
		}
		status, _, _, machine, _, cp := rowSession(t, env, id)
		if status != api.StatusPaused || cp || machine == nil || *machine != "m1" {
			t.Fatalf("%s rejected attach changed the row: status=%s machine=%v clean_pause=%v", caller, status, machine, cp)
		}
	}

	// An unknown machine records NULL and the attach succeeds.
	claim, err := m.Attach(id, api.SessionAttachRequest{SessionID: secret, MachineID: "m-att-unknown"}, env.UserID)
	if err != nil {
		t.Fatalf("unknown-machine attach: %v", err)
	}
	if claim.MachineID != "" {
		t.Fatalf("unknown-machine attach answered %q, want NULL", claim.MachineID)
	}
	if _, _, _, machine, _, _ := rowSession(t, env, id); machine != nil {
		t.Fatalf("unknown-machine attach stored %q, want NULL", *machine)
	}
	if got := w74mEventMachine(t, env, id, "session_attached"); got != "" {
		t.Fatalf("session_attached event machine_id = %q, want empty", got)
	}
}
