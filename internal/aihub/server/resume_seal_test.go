package server

// The owner's override of the blocked seal on the resume-blocked door:
// accept_lost_work clears attrs.blocked_seal in the door's transaction and
// records a blocked_seal_abandoned event, so the next claim — from any
// machine — carries no seal; without it the seal stands (a different
// machine's daemon refuses the continue, DAEMON-518). The door's
// authorization is unchanged.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// sealedBlockedWI files a WI, claims it on machine m1 and closes it
// blocked with capsule head abc123 (the seal {abc123, m1}).
func sealedBlockedWI(t *testing.T, e *srvEnv, goal string) string {
	t.Helper()
	id := createExecWIOverHTTP(t, e, goal)
	secret := strings.Repeat("a1", 16)
	if code, raw, _ := sessClaim(t, e, id, secret, ""); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": secret, "status": "blocked", "head_sha": "abc123"}, nil); code != 200 {
		t.Fatalf("blocked close: %d %s", code, raw)
	}
	return id
}

// claimOn claims id from machine (a fresh secret) and answers the claim.
func claimOn(t *testing.T, e *srvEnv, id, machine, secret string) api.SessionClaim {
	t.Helper()
	var out api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", e.key,
		map[string]string{"session_id": secret, "machine_id": machine}, &out); code != 200 {
		t.Fatalf("claim on %s: %d %s", machine, code, raw)
	}
	return out.Claim
}

// wiEvents reads the WI's event list (GET ?include=events).
func wiEvents(t *testing.T, e *srvEnv, id string) (attrs map[string]any, events []map[string]any) {
	t.Helper()
	var wi struct {
		Attrs map[string]any `json:"attrs"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id+"?include=events", e.key, nil, &wi); code != 200 {
		t.Fatalf("get: %d %s", code, raw)
	}
	raw, _ := json.Marshal(wi.Attrs["events"])
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("events: %v (%s)", err, raw)
	}
	return wi.Attrs, events
}

func eventsOfType(events []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev["type"] == typ {
			out = append(out, ev)
		}
	}
	return out
}

func TestResumeBlockedAcceptLostWorkClearsSeal(t *testing.T) {
	e := newSrvEnv(t)
	id := sealedBlockedWI(t, e, "accept lost work")

	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", e.key,
		map[string]any{"accept_lost_work": true}, nil); code != 200 {
		t.Fatalf("resume-blocked accept_lost_work: %d %s", code, raw)
	}
	attrs, events := wiEvents(t, e, id)
	if _, has := attrs["blocked_seal"]; has {
		t.Fatalf("the override must clear the seal: %v", attrs)
	}
	abandoned := eventsOfType(events, "blocked_seal_abandoned")
	if len(abandoned) != 1 {
		t.Fatalf("want one blocked_seal_abandoned event, got %v", events)
	}
	ev := abandoned[0]
	payload, _ := ev["payload"].(map[string]any)
	if payload["head_sha"] != "abc123" || payload["machine_id"] != "m1" {
		t.Fatalf("the event must name the abandoned head and machine: %v", ev)
	}
	var adminID string
	if err := e.db.QueryRow(`SELECT id FROM users WHERE display_name='root'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if ev["actor"] != adminID || ev["created_at"] == "" {
		t.Fatalf("the event must say who and when: %v", ev)
	}
	// the claim from another machine carries no seal: its daemon continues
	// from aihub's chain instead of refusing DAEMON-518.
	if c := claimOn(t, e, id, "m2", strings.Repeat("b2", 16)); c.SealedHead != "" || c.SealedMachine != "" {
		t.Fatalf("a claim after the override must carry no seal: %+v", c)
	}
}

func TestResumeBlockedWithoutAcceptKeepsSeal(t *testing.T) {
	e := newSrvEnv(t)
	id := sealedBlockedWI(t, e, "plain resume")

	// an empty body (the pre-override client) and an explicit false are
	// the plain door.
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/work-items/"+id+"/resume-blocked", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("resume-blocked with no body: %d", resp.StatusCode)
	}
	c := claimOn(t, e, id, "m2", strings.Repeat("b2", 16))
	if c.SealedHead != "abc123" || c.SealedMachine != "m1" {
		t.Fatalf("without the override the seal must stand: %+v", c)
	}
	// the daemon abandons that claim back to blocked (no head): the seal
	// still stands, and an explicit false keeps it too.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": strings.Repeat("b2", 16), "status": "blocked"}, nil); code != 200 {
		t.Fatalf("abandon: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", e.key,
		map[string]any{"accept_lost_work": false}, nil); code != 200 {
		t.Fatalf("resume-blocked false: %d %s", code, raw)
	}
	if c := claimOn(t, e, id, "m2", strings.Repeat("c3", 16)); c.SealedHead != "abc123" {
		t.Fatalf("accept_lost_work=false must keep the seal: %+v", c)
	}
	if _, events := wiEvents(t, e, id); len(eventsOfType(events, "blocked_seal_abandoned")) != 0 {
		t.Fatalf("no override, no abandon event: %v", events)
	}
}

func TestResumeBlockedAcceptLostWorkGate(t *testing.T) {
	e := newSrvEnv(t)
	id := sealedBlockedWI(t, e, "gated override")

	// a writer member who is not the reporter: refused, nothing changes.
	u, err := e.admin.CreateUser("other", "other@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	k, err := e.admin.CreateAPIKey(u.ID, "o", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.admin.AddMember(e.adminProjectID(t, "alpha"), u.ID, "writer"); err != nil {
		t.Fatal(err)
	}
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", k.Plain,
		map[string]any{"accept_lost_work": true}, nil)
	if c, _ := errEnvelope(t, raw); code != 403 || c != string(api.ErrAuthForbidden) {
		t.Fatalf("non-owner override: %d %s", code, raw)
	}
	// a malformed body is refused before anything changes.
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/work-items/"+id+"/resume-blocked", bytes.NewReader([]byte("{")))
	req.Header.Set("Authorization", "Bearer "+e.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("malformed body: %d", resp.StatusCode)
	}
	attrs, events := wiEvents(t, e, id)
	if _, has := attrs["blocked_seal"]; !has || len(eventsOfType(events, "blocked_seal_abandoned")) != 0 {
		t.Fatalf("a refused override must leave the seal: %v %v", attrs, events)
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusBlocked {
		t.Fatalf("a refused override must leave the WI blocked: %d %s", code, raw)
	}
	// a WI no longer blocked: NOT_BLOCKED, as before.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", e.key,
		map[string]any{"accept_lost_work": true}, nil); code != 200 {
		t.Fatalf("owner override: %d %s", code, raw)
	}
	code, raw = e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", e.key,
		map[string]any{"accept_lost_work": true}, nil)
	if c, r := errEnvelope(t, raw); code != 409 || c != string(api.ErrCoreConflict) || r != "NOT_BLOCKED" {
		t.Fatalf("override on a queued WI: %d %s", code, raw)
	}
}
