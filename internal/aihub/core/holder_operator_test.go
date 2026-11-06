package core

// holder_operator_test.go — the holder of a work item is
// the machine on its latest session_claimed/session_attached event and
// that machine row's CURRENT operator, with no liveness test.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// holderEvent appends a claim/attach event recording machine (a nil
// machine records JSON null, as a claim naming no owned machine does).
func holderEvent(t *testing.T, env *testEnv, wiID, typ string, machine any) {
	t.Helper()
	if _, err := env.DB.Exec(`INSERT INTO wi_events(work_item_id, created_at, actor, event_type, payload)
		VALUES (?1,?2,?3,?4,json_object('session_fingerprint','fp','machine_id',?5))`,
		wiID, Now(), env.UserID, typ, machine); err != nil {
		t.Fatalf("append %s: %v", typ, err)
	}
}

func wantHolder(t *testing.T, env *testEnv, wiID, machine, operator string) {
	t.Helper()
	gotM, gotO, err := env.WIs.HolderMachine(wiID)
	if err != nil {
		t.Fatalf("holder of %s: %v", wiID, err)
	}
	if gotM != machine || gotO != operator {
		t.Fatalf("holder of %s = (%q, %q), want (%q, %q)", wiID, gotM, gotO, machine, operator)
	}
}

func TestHolderMachineResolution(t *testing.T) {
	env := newEnv(t)
	seedMachineRow(t, env, "m-a", env.UserID, "")
	seedMachineRow(t, env, "m-b", env.UserIDB, "")
	seedMachineRow(t, env, "m-none", "", "")

	// No claim/attach event: no holder.
	none := mustCreateExecWI(t, env, "holder none", nil, "")
	wantHolder(t, env, none, "", "")

	// The latest claim names the machine and its operator; a later attach
	// moves the holder.
	moved := mustCreateExecWI(t, env, "holder moved", nil, "")
	holderEvent(t, env, moved, "session_claimed", "m-a")
	wantHolder(t, env, moved, "m-a", env.UserID)
	holderEvent(t, env, moved, "session_attached", "m-b")
	wantHolder(t, env, moved, "m-b", env.UserIDB)

	// A machine row with no operator: no holder.
	noOp := mustCreateExecWI(t, env, "holder no operator", nil, "")
	holderEvent(t, env, noOp, "session_claimed", "m-none")
	wantHolder(t, env, noOp, "", "")

	// A machine with no row at all: no holder.
	noRow := mustCreateExecWI(t, env, "holder no row", nil, "")
	holderEvent(t, env, noRow, "session_claimed", "m-gone")
	wantHolder(t, env, noRow, "", "")

	// The latest event recorded no machine (NULL or empty): no holder,
	// even though an earlier claim named one.
	for _, empty := range []any{nil, ""} {
		cleared := mustCreateExecWI(t, env, "holder cleared", nil, "")
		holderEvent(t, env, cleared, "session_claimed", "m-a")
		holderEvent(t, env, cleared, "session_claimed", empty)
		wantHolder(t, env, cleared, "", "")
	}

	// An operator handover on the same machine row moves the right.
	if _, err := env.DB.Exec(`UPDATE machines SET operator_id=?2 WHERE id=?1`, "m-a", env.UserIDB); err != nil {
		t.Fatal(err)
	}
	handed := mustCreateExecWI(t, env, "holder handover", nil, "")
	holderEvent(t, env, handed, "session_claimed", "m-a")
	wantHolder(t, env, handed, "m-a", env.UserIDB)

	// A long-silent machine keeps its operator's right (no liveness test).
	if _, err := env.DB.Exec(`UPDATE machines SET last_heartbeat_at='2000-01-01T00:00:00Z' WHERE id='m-a'`); err != nil {
		t.Fatal(err)
	}
	wantHolder(t, env, handed, "m-a", env.UserIDB)
}

// blockedHeld claims a fresh work item on m1 and closes it blocked, with
// head as the blocked seal when it is not empty.
func blockedHeld(t *testing.T, env *testEnv, m *SessionManager, goal, head string) string {
	t.Helper()
	id := mustCreateExecWI(t, env, goal, nil, "")
	secret := strings.Repeat("c3", 16)
	mustSessionClaim(t, m, env, id, secret)
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusBlocked,
		Note: "stop", HeadSHA: head}, env.UserID); err != nil {
		t.Fatalf("blocked close: %v", err)
	}
	return id
}

func payloadOf(t *testing.T, ev Event) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("payload %s: %v", ev.Payload, err)
	}
	return p
}

func TestHolderBasisRidesOnlyTheSoleBasisEvents(t *testing.T) {
	env, m := sessionEnv(t)
	holder := api.ResumeBlockedRequest{Basis: api.BasisHolderOperator, MachineID: "m1"}

	// Sole-basis resume: basis + machine_id beside from/to; the seal's
	// abandoned event keeps exactly its head and machine.
	sealed := blockedHeld(t, env, m, "sole basis resume", "abc123")
	opts := holder
	opts.AcceptLostWork = true
	if _, err := env.WIs.ResumeBlockedWith(sealed, env.UserIDB, opts); err != nil {
		t.Fatalf("sole-basis resume: %v", err)
	}
	ev := lastEventOf(t, env, sealed, "wi_resumed")
	p := payloadOf(t, ev)
	if len(p) != 4 || p["basis"] != api.BasisHolderOperator || p["machine_id"] != "m1" ||
		p["from"] != api.StatusBlocked || p["to"] != api.StatusQueued || ev.Actor != env.UserIDB {
		t.Fatalf("sole-basis wi_resumed: actor %q payload %v", ev.Actor, p)
	}
	ab := payloadOf(t, lastEventOf(t, env, sealed, "blocked_seal_abandoned"))
	if len(ab) != 2 || ab["head_sha"] != "abc123" || ab["machine_id"] != "m1" {
		t.Fatalf("blocked_seal_abandoned must keep its shape: %v", ab)
	}

	// An unset basis (an existing basis admitted the caller): today's shape.
	plain := blockedHeld(t, env, m, "existing basis resume", "")
	if _, err := env.WIs.ResumeBlockedWith(plain, env.UserID, api.ResumeBlockedRequest{}); err != nil {
		t.Fatalf("plain resume: %v", err)
	}
	if p := payloadOf(t, lastEventOf(t, env, plain, "wi_resumed")); len(p) != 2 {
		t.Fatalf("an existing basis keeps from/to only: %v", p)
	}

	// Sole-basis cancel: basis + machine_id beside the owner's words.
	canceled := blockedHeld(t, env, m, "sole basis cancel", "")
	if _, err := env.WIs.Cancel(canceled, env.UserIDB, api.CancelRequest{Reason: "stop",
		QuestionID: "q1", Basis: api.BasisHolderOperator, MachineID: "m1"}); err != nil {
		t.Fatalf("sole-basis cancel: %v", err)
	}
	ev = lastEventOf(t, env, canceled, "work_item_canceled")
	p = payloadOf(t, ev)
	if len(p) != 4 || p["basis"] != api.BasisHolderOperator || p["machine_id"] != "m1" ||
		p["reason"] != "stop" || p["question_id"] != "q1" || ev.Actor != env.UserIDB {
		t.Fatalf("sole-basis work_item_canceled: actor %q payload %v", ev.Actor, p)
	}
	bare := blockedHeld(t, env, m, "sole basis bare cancel", "")
	if _, err := env.WIs.Cancel(bare, env.UserIDB, api.CancelRequest{Basis: api.BasisHolderOperator, MachineID: "m1"}); err != nil {
		t.Fatalf("sole-basis bare cancel: %v", err)
	}
	if p := payloadOf(t, lastEventOf(t, env, bare, "work_item_canceled")); len(p) != 2 || p["basis"] != api.BasisHolderOperator {
		t.Fatalf("a bare sole-basis cancel carries basis + machine_id only: %v", p)
	}

	// An unset basis keeps a plain cancel's empty body.
	empty := blockedHeld(t, env, m, "existing basis cancel", "")
	if _, err := env.WIs.Cancel(empty, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatalf("plain cancel: %v", err)
	}
	if ev := lastEventOf(t, env, empty, "work_item_canceled"); string(ev.Payload) != "{}" {
		t.Fatalf("a plain cancel keeps its empty body, got %s", ev.Payload)
	}
}

// A holder-basis cancel is blocked-only inside the transaction: the server
// gate read the status outside it, so a WI a concurrent resume already moved
// to queued is refused with CORE-409, keeps its status and gets no event.
func TestHolderBasisCancelRefusesAnUnblockedWorkItem(t *testing.T) {
	env, m := sessionEnv(t)
	id := blockedHeld(t, env, m, "resumed under the holder's cancel", "")
	if _, err := env.WIs.ResumeBlocked(id, env.UserID); err != nil {
		t.Fatalf("concurrent resume: %v", err)
	}
	_, err := env.WIs.Cancel(id, env.UserIDB, api.CancelRequest{Basis: api.BasisHolderOperator, MachineID: "m1"})
	if !IsCode(err, api.ErrCoreConflict) || !strings.Contains(err.Error(), "state changed concurrently") {
		t.Fatalf("holder-basis cancel of a queued WI: want CORE-409 state changed concurrently, got %v", err)
	}
	if v, err := env.WIs.Get(id, GetOptions{}); err != nil || v.Status != api.StatusQueued {
		t.Fatalf("status after the refused cancel: %v %v, want queued", v, err)
	}
	if n := countEvents(t, env, id, "work_item_canceled"); n != 0 {
		t.Fatalf("a refused holder-basis cancel wrote %d work_item_canceled events", n)
	}
}
