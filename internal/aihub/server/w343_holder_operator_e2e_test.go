// carries: internal/aihub/server/*, internal/aihub/core/*, internal/common/api/types.go

package server

// w343_holder_operator_e2e_test.go — the HTTP contract suite for the widened
// blocked owner-decision gate (artifacts/spec.md, "## Acceptance criteria").
// Authored BEFORE `code`, against the spec alone: every scenario drives a real
// aihub server over HTTP on a real sqlite store, and every kept one is red on
// the pre-implementation tree for an assertion reason, never a build one.
//
// Run (targeted):
//
//	GOWORK=off go test ./internal/aihub/server -run '^TestW343' -count=1 -v
//
// The scenario document is artifacts/e2e-scenarios.md. Every scenario prints
// one `PASS S<n>` marker line when it passes, so verification binds each
// scenario to its own output line.
//
// Scope note: the spec leaves the AUTH-403 wording open, so S9 pins the facts
// it fixes — the holder machine id, the word "operator", the door's own verb —
// and never a literal sentence.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w343Held files a work item as the admin (so the base bases do not apply to
// the caller under test), claims it on machine with its operator's own key and
// closes it blocked — with head as the blocked seal when it is not empty. The
// machine's current operator is then the blocked work item's only holder.
func w343Held(t *testing.T, e *srvEnv, goal, secret, machine, operator, key, head string) string {
	t.Helper()
	seedSessionMachine(t, e, machine, operator)
	id := createExecWIOverHTTP(t, e, goal)
	var claim api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", key,
		map[string]any{"session_id": secret, "machine_id": machine}, &claim); code != 200 {
		t.Fatalf("fixture %q: claim on %s answered %d %s", goal, machine, code, raw)
	}
	body := map[string]any{"session_id": secret, "status": "blocked"}
	if head != "" {
		body["head_sha"] = head
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", key, body, nil); code != 200 {
		t.Fatalf("fixture %q: blocked close answered %d %s", goal, code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusBlocked {
		t.Fatalf("fixture %q: status %q, want blocked", goal, st)
	}
	return id
}

// w343StaleMachine seeds a machines row whose last heartbeat is far older than
// any normal cadence, keeping the holder right (the spec has no liveness test).
func w343StaleMachine(t *testing.T, e *srvEnv, machine, operator string) {
	t.Helper()
	if _, err := e.db.Exec(`INSERT INTO machines(id,last_heartbeat_at,role,operator_id)
		VALUES (?1,?2,'runner',?3)`, machine,
		time.Now().Add(-720*time.Hour).UTC().Format(time.RFC3339Nano), operator); err != nil {
		t.Fatalf("seed stale machine %s: %v", machine, err)
	}
}

// w343SetOperator hands a machine row to another operator (the handover case).
func w343SetOperator(t *testing.T, e *srvEnv, machine, operator string) {
	t.Helper()
	if _, err := e.db.Exec(`UPDATE machines SET operator_id=?2 WHERE id=?1`, machine, operator); err != nil {
		t.Fatalf("hand %s to %s: %v", machine, operator, err)
	}
}

// w343Status answers the work item's status straight from the store.
func w343Status(t *testing.T, e *srvEnv, id string) string {
	t.Helper()
	var st string
	if err := e.db.QueryRow(`SELECT status FROM work_items WHERE id=?1`, id).Scan(&st); err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	return st
}

// w343WaitingOn answers the work item's waiting_on ("" for NULL).
func w343WaitingOn(t *testing.T, e *srvEnv, id string) string {
	t.Helper()
	var wo string
	if err := e.db.QueryRow(`SELECT COALESCE(waiting_on,'') FROM work_items WHERE id=?1`, id).Scan(&wo); err != nil {
		t.Fatalf("waiting_on of %s: %v", id, err)
	}
	return wo
}

// w343Event answers the LAST event of typ, read back over HTTP.
func w343Event(t *testing.T, e *srvEnv, id, typ string) map[string]any {
	t.Helper()
	_, events := wiEvents(t, e, id)
	evs := eventsOfType(events, typ)
	if len(evs) == 0 {
		t.Fatalf("%s carries no %s event: %v", id, typ, events)
	}
	return evs[len(evs)-1]
}

// w343Payload answers an event's payload object ({} for a null payload).
func w343Payload(t *testing.T, ev map[string]any) map[string]any {
	t.Helper()
	if ev["payload"] == nil {
		return map[string]any{}
	}
	p, ok := ev["payload"].(map[string]any)
	if !ok {
		t.Fatalf("event payload is not an object: %v", ev)
	}
	return p
}

// w343Pass prints a scenario's marker only when none of its assertions failed,
// so a marker line can never appear on a red run.
func w343Pass(t *testing.T, marker string) {
	t.Helper()
	if t.Failed() {
		t.FailNow()
	}
	fmt.Println(marker)
}

// w343Refusal decodes a typed refusal body.
func w343Refusal(t *testing.T, raw []byte) api.Error {
	t.Helper()
	var e api.Error
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("bad error body: %s", raw)
	}
	return e
}

// w343Resume and w343Cancel drive the two widened doors.
func w343Resume(t *testing.T, e *srvEnv, id, key string, body any) (int, []byte) {
	t.Helper()
	return e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", key, body, nil)
}

func w343Cancel(t *testing.T, e *srvEnv, id, key string) (int, []byte) {
	t.Helper()
	return e.do(t, "POST", "/api/v1/work-items/"+id+"/cancel", key, map[string]any{}, nil)
}

// S1 — the holder operator resumes a blocked work item: 200, queued,
// waiting_on cleared, wi_resumed.actor is the caller.
func TestW343HolderOperatorResumesBlocked(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s1-operator", "writer")
	id := w343Held(t, e, "s1 holder resume", strings.Repeat("a1", 16), "m-s1", op, opKey, "")

	if code, raw := w343Resume(t, e, id, opKey, map[string]any{}); code != 200 {
		t.Fatalf("holder operator resume answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusQueued {
		t.Errorf("the resumed work item is %q, want queued", st)
	}
	if wo := w343WaitingOn(t, e, id); wo != "" {
		t.Errorf("the resumed work item still waits on %q, want cleared", wo)
	}
	if actor := w343Event(t, e, id, "wi_resumed")["actor"]; actor != op {
		t.Errorf("wi_resumed actor is %v, want the holder operator %s", actor, op)
	}
	w343Pass(t, "PASS S1")
}

// S2 — the holder operator clears a blocked seal with accept_lost_work: 200,
// queued, seal cleared, blocked_seal_abandoned records the head and machine.
func TestW343HolderOperatorClearsSeal(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s2-operator", "writer")
	id := w343Held(t, e, "s2 holder accept_lost_work", strings.Repeat("a1", 16), "m-s2", op, opKey, "abc123")

	if code, raw := w343Resume(t, e, id, opKey, map[string]any{"accept_lost_work": true}); code != 200 {
		t.Fatalf("holder operator accept_lost_work answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusQueued {
		t.Errorf("the resumed work item is %q, want queued", st)
	}
	attrs, events := wiEvents(t, e, id)
	if _, has := attrs["blocked_seal"]; has {
		t.Errorf("accept_lost_work must clear the seal: %v", attrs["blocked_seal"])
	}
	abandoned := eventsOfType(events, "blocked_seal_abandoned")
	if len(abandoned) != 1 {
		t.Fatalf("want one blocked_seal_abandoned event, got %v", events)
	}
	if p := w343Payload(t, abandoned[0]); p["head_sha"] != "abc123" || p["machine_id"] != "m-s2" {
		t.Errorf("the abandoned event must name the head and machine: %v", p)
	}
	w343Pass(t, "PASS S2")
}

// S3 — a machine's new operator holds the right after an operator handover:
// the previous operator gets 403, the new one 200 and the WI is queued.
func TestW343OperatorHandoverMovesRight(t *testing.T) {
	e := newSrvEnv(t)
	oldOp, oldKey := memberUser(t, e, "w343-s3-old", "writer")
	newOp, newKey := memberUser(t, e, "w343-s3-new", "writer")
	id := w343Held(t, e, "s3 handover", strings.Repeat("a1", 16), "m-s3", oldOp, oldKey, "")
	w343SetOperator(t, e, "m-s3", newOp)

	if code, raw := w343Resume(t, e, id, oldKey, map[string]any{}); code != 403 {
		t.Errorf("the previous operator's resume answered %d %s, want 403", code, raw)
	}
	if code, raw := w343Resume(t, e, id, newKey, map[string]any{}); code != 200 {
		t.Fatalf("the new operator's resume answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusQueued {
		t.Errorf("the resumed work item is %q, want queued", st)
	}
	w343Pass(t, "PASS S3")
}

// S4 — an offline machine's operator keeps the right: a long-silent holder
// machine's operator still gets 200; no liveness state withdraws it.
func TestW343OfflineMachineOperatorKeepsRight(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s4-operator", "writer")
	w343StaleMachine(t, e, "m-s4", op)
	id := w343Held(t, e, "s4 offline holder", strings.Repeat("a1", 16), "m-s4", op, opKey, "")

	if code, raw := w343Resume(t, e, id, opKey, map[string]any{}); code != 200 {
		t.Fatalf("a long-silent machine's operator answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusQueued {
		t.Errorf("the resumed work item is %q, want queued", st)
	}
	w343Pass(t, "PASS S4")
}

// S5 — a later claim moves the holder: the earlier machine's operator gets
// 403, the later machine's operator 200.
func TestW343LaterClaimMovesHolder(t *testing.T) {
	e := newSrvEnv(t)
	opA, keyA := memberUser(t, e, "w343-s5-a", "writer")
	opB, keyB := memberUser(t, e, "w343-s5-b", "writer")
	id := w343Held(t, e, "s5 first holder", strings.Repeat("a1", 16), "m-s5a", opA, keyA, "")

	// the admin (the reporter) resumes, then machine B claims and blocks it.
	if code, raw := w343Resume(t, e, id, e.key, map[string]any{}); code != 200 {
		t.Fatalf("fixture: admin resume answered %d %s", code, raw)
	}
	seedSessionMachine(t, e, "m-s5b", opB)
	secret := strings.Repeat("b2", 16)
	var claim api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", keyB,
		map[string]any{"session_id": secret, "machine_id": "m-s5b"}, &claim); code != 200 {
		t.Fatalf("fixture: claim on m-s5b answered %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", keyB,
		map[string]any{"session_id": secret, "status": "blocked"}, nil); code != 200 {
		t.Fatalf("fixture: blocked close on m-s5b answered %d %s", code, raw)
	}

	if code, raw := w343Resume(t, e, id, keyA, map[string]any{}); code != 403 {
		t.Errorf("the earlier machine's operator answered %d %s, want 403", code, raw)
	}
	if code, raw := w343Resume(t, e, id, keyB, map[string]any{}); code != 200 {
		t.Fatalf("the later machine's operator answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusQueued {
		t.Errorf("the resumed work item is %q, want queued", st)
	}
	w343Pass(t, "PASS S5")
}

// S6 — the holder operator cancels a blocked work item: 200, canceled, and
// work_item_canceled.actor is the caller.
func TestW343HolderOperatorCancelsBlocked(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s6-operator", "writer")
	id := w343Held(t, e, "s6 holder cancel", strings.Repeat("a1", 16), "m-s6", op, opKey, "")

	if code, raw := w343Cancel(t, e, id, opKey); code != 200 {
		t.Fatalf("holder operator cancel answered %d %s, want 200", code, raw)
	}
	if st := w343Status(t, e, id); st != api.StatusCanceled {
		t.Errorf("the canceled work item is %q, want canceled", st)
	}
	if actor := w343Event(t, e, id, "work_item_canceled")["actor"]; actor != op {
		t.Errorf("work_item_canceled actor is %v, want the holder operator %s", actor, op)
	}
	w343Pass(t, "PASS S6")
}

// S7 — a holder-only resume records the basis and machine id on wi_resumed.
func TestW343HolderOnlyResumeAudit(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s7-operator", "writer")
	id := w343Held(t, e, "s7 resume audit", strings.Repeat("a1", 16), "m-s7", op, opKey, "")

	if code, raw := w343Resume(t, e, id, opKey, map[string]any{}); code != 200 {
		t.Fatalf("holder operator resume answered %d %s, want 200", code, raw)
	}
	ev := w343Event(t, e, id, "wi_resumed")
	p := w343Payload(t, ev)
	if p["basis"] != "holder_operator" || p["machine_id"] != "m-s7" {
		t.Errorf("wi_resumed must carry basis and machine_id: %v", p)
	}
	if p["from"] != api.StatusBlocked || p["to"] != api.StatusQueued {
		t.Errorf("wi_resumed must keep from/to: %v", p)
	}
	if ev["actor"] != op {
		t.Errorf("wi_resumed actor is %v, want the caller %s", ev["actor"], op)
	}

	// guard rail: an overlapping reporter basis leaves the payload exactly
	// today's shape — no basis, no machine_id (the holder is not the sole
	// basis).
	overlap := w343Held(t, e, "s7 overlap resume", strings.Repeat("b2", 16), "m-s7-admin", e.adminID, e.key, "")
	if code, raw := w343Resume(t, e, overlap, e.key, map[string]any{}); code != 200 {
		t.Fatalf("the reporter's own resume answered %d %s, want 200", code, raw)
	}
	opPayload := w343Payload(t, w343Event(t, e, overlap, "wi_resumed"))
	if _, has := opPayload["basis"]; has {
		t.Errorf("an existing basis must not gain basis: %v", opPayload)
	}
	if _, has := opPayload["machine_id"]; has {
		t.Errorf("an existing basis must not gain machine_id: %v", opPayload)
	}
	w343Pass(t, "PASS S7")
}

// S8 — a holder-only cancel records the basis and machine id on
// work_item_canceled.
func TestW343HolderOnlyCancelAudit(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s8-operator", "writer")
	id := w343Held(t, e, "s8 cancel audit", strings.Repeat("a1", 16), "m-s8", op, opKey, "")

	if code, raw := w343Cancel(t, e, id, opKey); code != 200 {
		t.Fatalf("holder operator cancel answered %d %s, want 200", code, raw)
	}
	ev := w343Event(t, e, id, "work_item_canceled")
	p := w343Payload(t, ev)
	if p["basis"] != "holder_operator" || p["machine_id"] != "m-s8" {
		t.Errorf("work_item_canceled must carry basis and machine_id: %v", p)
	}
	if ev["actor"] != op {
		t.Errorf("work_item_canceled actor is %v, want the caller %s", ev["actor"], op)
	}
	w343Pass(t, "PASS S8")
}

// S9 — a refused member is told which machine holds the work item, on both
// doors; the detail names the machine and its operator's right and never the
// holder operator's user id.
func TestW343RefusalNamesHoldingMachine(t *testing.T) {
	e := newSrvEnv(t)
	op, opKey := memberUser(t, e, "w343-s9-operator", "writer")
	id := w343Held(t, e, "s9 refusal detail", strings.Repeat("a1", 16), "m-s9", op, opKey, "")
	_, wKey := memberUser(t, e, "w343-s9-outsider", "writer")

	doors := []struct {
		name, verb string
		call       func() (int, []byte)
	}{
		{"resume-blocked", "resume", func() (int, []byte) { return w343Resume(t, e, id, wKey, map[string]any{}) }},
		{"cancel", "cancel", func() (int, []byte) { return w343Cancel(t, e, id, wKey) }},
	}
	for _, d := range doors {
		code, raw := d.call()
		if code != 403 {
			t.Fatalf("%s: a non-holder writer answered %d %s, want 403", d.name, code, raw)
		}
		refusal := w343Refusal(t, raw)
		if refusal.Code != api.ErrAuthForbidden {
			t.Errorf("%s: refusal code %q, want %s", d.name, refusal.Code, api.ErrAuthForbidden)
		}
		if !strings.Contains(refusal.Detail, "m-s9") {
			t.Errorf("%s: the detail must name the holding machine: %q", d.name, refusal.Detail)
		}
		low := strings.ToLower(refusal.Detail)
		if !strings.Contains(low, "operator") || !strings.Contains(low, d.verb) {
			t.Errorf("%s: the detail must say its operator may %s: %q", d.name, d.verb, refusal.Detail)
		}
		if strings.Contains(refusal.Detail, op) {
			t.Errorf("%s: the detail must not carry the operator's user id: %q", d.name, refusal.Detail)
		}
	}
	if st := w343Status(t, e, id); st != api.StatusBlocked {
		t.Errorf("a refused call must leave the work item blocked, got %q", st)
	}
	if _, events := wiEvents(t, e, id); len(eventsOfType(events, "wi_resumed")) != 0 ||
		len(eventsOfType(events, "work_item_canceled")) != 0 {
		t.Errorf("a refused call must write no decision event: %v", events)
	}
	w343Pass(t, "PASS S9")
}
