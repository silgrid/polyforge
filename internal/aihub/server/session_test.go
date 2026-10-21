package server

// session_test.go — the v1.5 session endpoints over real HTTP: the five
// reference.md §3 routes, their typed refusals, the paused verdict on
// session-end (the M3 pause transition), and the m4 read surface (public
// session block on every WorkItem JSON; never a hash, never the secret).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// sessClaim performs a session claim over HTTP and returns the decoded
// envelope plus the raw body.
func sessClaim(t *testing.T, e *srvEnv, wiID, secret, key string) (int, []byte, api.SessionClaimResponse) {
	t.Helper()
	body := map[string]string{}
	if secret != "" {
		body["session_id"] = secret
	}
	if key != "" {
		body["idempotency_key"] = key
	}
	body["machine_id"] = "m1"
	var out api.SessionClaimResponse
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+wiID+"/claim", e.key, body, &out)
	return code, raw, out
}

// errEnvelope decodes the typed error body.
func errEnvelope(t *testing.T, raw []byte) (string, string) {
	t.Helper()
	var envl struct {
		Code  string          `json:"code"`
		Extra json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(raw, &envl); err != nil {
		t.Fatalf("bad error envelope: %s", raw)
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(envl.Extra, &extra)
	return envl.Code, extra.Reason
}

// createExecWIOverHTTP creates an execution ticket and returns its id.
func createExecWIOverHTTP(t *testing.T, e *srvEnv, goal string) string {
	t.Helper()
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": goal, "bundle_name": "demo-bundle"}, &wi); code != 201 || wi.ID == "" {
		t.Fatalf("create: %d %s", code, raw)
	}
	return wi.ID
}

// eventsOf reads the WI timeline over HTTP.
func eventsOf(t *testing.T, e *srvEnv, wiID string) []map[string]any {
	t.Helper()
	var wi struct {
		Attrs struct {
			Events []map[string]any `json:"events"`
		} `json:"attrs"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+wiID+"?include=events", e.key, nil, &wi); code != 200 {
		t.Fatalf("get events: %d %s", code, raw)
	}
	return wi.Attrs.Events
}

func TestSessionVerifyOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "verify over http")
	secret := "21212121212121212121212121212121"

	// Unheld: matches=false, no current fingerprint, clean_pause=false.
	var resp api.SessionVerifyResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key,
		map[string]string{"session_id": secret}, &resp); code != 200 {
		t.Fatalf("verify unheld: %d %s", code, raw)
	} else if resp.Matches || resp.CurrentFingerprint != "" || resp.Status != "queued" || resp.CleanPause {
		t.Fatalf("unheld verify wrong: %+v", resp)
	} else if strings.Contains(string(raw), secret) {
		t.Fatalf("verify echoes the secret: %s", raw)
	}
	// Claim, then verify both ways.
	if code, raw, _ := sessClaim(t, e, id, secret, "sv-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key,
		map[string]string{"session_id": secret}, &resp); code != 200 || !resp.Matches ||
		resp.Status != "running" || resp.CurrentFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("verify match wrong: %d %+v", code, resp)
	} else if strings.Contains(string(raw), secret) {
		t.Fatalf("verify echoes the secret on match: %s", raw)
	}
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key,
		map[string]string{"session_id": "33333333333333333333333333333333"}, &resp); code != 200 || resp.Matches ||
		resp.CurrentFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("verify mismatch wrong: %+v", resp)
	}
	// Unknown WI: 404; empty body: 400.
	if code, _ := e.do(t, "POST", "/api/v1/work-items/wi_nosuchthing/session-verify", e.key,
		map[string]string{"session_id": secret}, nil); code != 404 {
		t.Fatalf("unknown WI verify: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key,
		map[string]string{}, nil); code != 400 {
		t.Fatalf("empty verify body: %d", code)
	}
}

func TestSessionAttachOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "attach over http")
	secret := "41414141414141414141414141414141"
	if code, raw, _ := sessClaim(t, e, id, secret, "sa-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	// Pause via session-end's paused verdict (the merge amend; daemon
	// shutdown shape: clean_pause stays false).
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]any{"status": "paused", "session_id": secret, "clean_pause": false}, nil); code != 200 {
		t.Fatalf("session pause: %d %s", code, raw)
	}
	// Attach: back to running under the same session.
	var resp api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-attach", e.key,
		map[string]string{"session_id": secret, "machine_id": "m1"}, &resp); code != 200 {
		t.Fatalf("attach: %d %s", code, raw)
	}
	if resp.Claim.SessionFingerprint != api.SessionFingerprint(secret) || resp.WorkItem.Status != "running" {
		t.Fatalf("attach response wrong: %+v", resp)
	}
	// Pause again with clean_pause=true (operator pause): attach refuses.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]any{"status": "paused", "session_id": secret, "clean_pause": true}, nil); code != 200 {
		t.Fatalf("operator pause: %d %s", code, raw)
	}
	var wi struct {
		CleanPause bool `json:"clean_pause"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || !wi.CleanPause {
		t.Fatalf("clean_pause not visible on the WI: %d %s %+v", code, raw, wi)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-attach", e.key,
		map[string]string{"session_id": secret}, nil); code != 409 {
		t.Fatalf("attach of clean-paused WI: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-409" || r != "NOT_ATTACHABLE" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// A stale session cannot attach either.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-attach", e.key,
		map[string]string{"session_id": "42424242424242424242424242424242"}, nil); code != 403 {
		t.Fatalf("stale attach: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-403" || r != "STALE_SESSION" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
}

func TestForceEndSessionOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "force-end over http")
	oldSecret := "51515151515151515151515151515151"
	if code, raw, _ := sessClaim(t, e, id, oldSecret, "ft-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	path := "/api/v1/work-items/" + id + "/force-end-session"
	// A non-member writer is refused AUTH-403.
	outsider, _ := e.admin.CreateUser("plainw", "pw@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "pw", "")
	if code, raw := e.do(t, "POST", path, okey.Plain, map[string]string{"reason": "x"}, nil); code != 403 {
		t.Fatalf("non-member force-end: %d %s", code, raw)
	} else if c, _ := errEnvelope(t, raw); c != "AUTH-403" {
		t.Fatalf("wrong refusal class: %s", raw)
	}
	// Empty reason: 400.
	if code, _ := e.do(t, "POST", path, e.key, map[string]string{}, nil); code != 400 {
		t.Fatalf("empty reason accepted: %d", code)
	}
	// Admin force-end: WI back to queued, session cleared.
	var view struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", path, e.key, map[string]string{"reason": "machine lost"}, &view); code != 200 {
		t.Fatalf("force-end: %d %s", code, raw)
	}
	if view.ID != id || view.Status != "queued" {
		t.Fatalf("force-end response wrong: %+v", view)
	}
	// The old secret no longer verifies.
	var v api.SessionVerifyResponse
	e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key, map[string]string{"session_id": oldSecret}, &v)
	if v.Matches {
		t.Fatalf("old secret still matches after force-end")
	}
	// The old secret can no longer end the WI (fenced).
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": oldSecret, "status": "wrapped"}, nil); code != 403 {
		t.Fatalf("old secret ended the WI after force-end: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-403" || r != "STALE_SESSION" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// The session_superseded event carries the OLD fingerprint and reason.
	found := false
	for _, ev := range eventsOf(t, e, id) {
		if ev["type"] != "session_superseded" {
			continue
		}
		found = true
		payload, _ := ev["payload"].(map[string]any)
		if payload["old_session_fingerprint"] != api.SessionFingerprint(oldSecret) || payload["reason"] != "machine lost" {
			t.Fatalf("superseded event payload wrong: %v", ev)
		}
	}
	if !found {
		t.Fatalf("session_superseded event missing")
	}
	// A second force-end: the WI no longer holds a session -> 409 NOT_HELD.
	if code, raw := e.do(t, "POST", path, e.key, map[string]string{"reason": "again"}, nil); code != 409 {
		t.Fatalf("force-end of queued WI: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-409" || r != "NOT_HELD" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// The WI is claimable by a fresh claim, and the new secret verifies.
	newSecret := "53535353535353535353535353535353"
	if code, raw, _ := sessClaim(t, e, id, newSecret, "ft-2"); code != 200 {
		t.Fatalf("claim after force-end: %d %s", code, raw)
	}
	e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key, map[string]string{"session_id": newSecret}, &v)
	if !v.Matches {
		t.Fatalf("new secret does not match after re-claim")
	}
}

// TestForceEndSessionAuthorization: the recovery door is a human decision —
// admin or project maintainer only. A project writer and a MACHINE
// credential (even with project membership) are refused AUTH-403; a
// maintainer is admitted.
func TestForceEndSessionAuthorization(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "force-end authz")
	if code, raw, _ := sessClaim(t, e, id, "57151515151515151515151515151515", "fa-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	path := "/api/v1/work-items/" + id + "/force-end-session"
	alpha := e.adminProjectID(t, "alpha")
	addMember := func(userID, role string) {
		t.Helper()
		if code, raw := e.do(t, "PATCH", "/api/v1/projects/"+alpha, e.key,
			map[string]any{"members": []map[string]string{{"user_id": userID, "role": role}}}, nil); code != 200 {
			t.Fatalf("membership: %d %s", code, raw)
		}
	}
	// A registered machine (heartbeat shape) with writer membership and an
	// admin-issued key for its identity.
	var hb struct {
		UserID string `json:"user_id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/machines/heartbeat", e.key,
		map[string]any{"machine_id": "m-self", "live_wi_ids": []string{}}, &hb); code != 200 || hb.UserID == "" {
		t.Fatalf("heartbeat registration: %d %s", code, raw)
	}
	machineKey, err := e.admin.CreateAPIKey(hb.UserID, "m-self", "")
	if err != nil {
		t.Fatalf("machine key: %v", err)
	}
	addMember(hb.UserID, "writer")
	if code, raw := e.do(t, "POST", path, machineKey.Plain, map[string]string{"reason": "x"}, nil); code != 403 {
		t.Fatalf("machine credential force-end: %d %s", code, raw)
	} else if c, _ := errEnvelope(t, raw); c != "AUTH-403" {
		t.Fatalf("wrong refusal class: %s", raw)
	}
	// A human writer member is refused too.
	human, _ := e.admin.CreateUser("humanw", "hw@example.com", "writer", "human")
	hkey, _ := e.admin.CreateAPIKey(human.ID, "hw", "")
	addMember(human.ID, "writer")
	if code, raw := e.do(t, "POST", path, hkey.Plain, map[string]string{"reason": "x"}, nil); code != 403 {
		t.Fatalf("human writer force-end: %d %s", code, raw)
	} else if c, _ := errEnvelope(t, raw); c != "AUTH-403" {
		t.Fatalf("wrong refusal class: %s", raw)
	}
	// Still held: nothing above changed the WI.
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != "running" {
		t.Fatalf("refused force-end mutated the WI: %d %s", code, raw)
	}
	// A project maintainer is admitted.
	maint, _ := e.admin.CreateUser("maintfe", "mfe@example.com", "writer", "human")
	mkey, _ := e.admin.CreateAPIKey(maint.ID, "mfe", "")
	addMember(maint.ID, "maintainer")
	var view struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", path, mkey.Plain, map[string]string{"reason": "holder machine lost"}, &view); code != 200 || view.Status != "queued" {
		t.Fatalf("maintainer force-end: %d %s %+v", code, raw, view)
	}
}

func TestSessionEndOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "end over http")
	secret := "61616161616161616161616161616161"
	if code, raw, _ := sessClaim(t, e, id, secret, "se-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	// Fence: a foreign secret answers CORE-403 STALE_SESSION.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": "62626262626262626262626262626262", "status": "wrapped"}, nil); code != 403 {
		t.Fatalf("unfenced session-end: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-403" || r != "STALE_SESSION" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// Bad status: 400 (paused is legal since the merge amend — use an
	// illegal one).
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": secret, "status": "queued"}, nil); code != 400 {
		t.Fatalf("bad status accepted: %d", code)
	}
	// Real end: wrapped, session fields cleared on the read surface.
	var ended struct {
		Status             string `json:"status"`
		SessionFingerprint string `json:"session_fingerprint"`
		ClosedAt           string `json:"closed_at"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": secret, "status": "wrapped", "note": "done"}, &ended); code != 200 ||
		ended.Status != "wrapped" || ended.SessionFingerprint != "" || ended.ClosedAt == "" {
		t.Fatalf("session-end: %d %s %+v", code, raw, ended)
	}
}

func TestSessionClaimGatesOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	// Terminal: CORE-410.
	id := createExecWIOverHTTP(t, e, "gates terminal")
	if code, raw, _ := sessClaim(t, e, id, "71717171717171717171717171717171", "g1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": "71717171717171717171717171717171", "status": "wrapped"}, nil)
	if code, raw, _ := sessClaim(t, e, id, "72727272727272727272727272727272", "g2"); code != 410 {
		t.Fatalf("terminal claim: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-410" || r != "TERMINAL_STATE" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// Dependency-blocked: CORE-409 DEPENDENCY_BLOCKED.
	blocker := createExecWIOverHTTP(t, e, "gates blocker")
	blocked := createExecWIOverHTTP(t, e, "gates blocked")
	e.do(t, "POST", "/api/v1/work-items/"+blocked+"/dependencies", e.key,
		map[string]string{"blocking_wi_id": blocker, "kind": "blocks"}, nil)
	if code, raw, _ := sessClaim(t, e, blocked, "73737373737373737373737373737373", "g3"); code != 409 {
		t.Fatalf("dependency-blocked claim: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-409" || r != "DEPENDENCY_BLOCKED" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// No bundle: CORE-409 NOT_CLAIMABLE.
	var reg struct {
		ID string `json:"id"`
	}
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "alpha", "goal": "gates registry"}, &reg)
	if code, raw, _ := sessClaim(t, e, reg.ID, "74747474747474747474747474747474", "g4"); code != 409 {
		t.Fatalf("registration claim: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-409" || r != "NOT_CLAIMABLE" {
		t.Fatalf("wrong refusal: %s %s", c, r)
	}
	// Missing session_id: CORE-400.
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+blocker+"/claim", e.key,
		map[string]string{"machine_id": "m1"}, nil); code != 400 {
		t.Fatalf("secretless claim accepted: %d", code)
	}
}

func TestReadyQueueCarriesSessionBlock(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "queue session block")
	secret := "81818181818181818181818181818181"
	if code, raw, _ := sessClaim(t, e, id, secret, "q-1"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	var q struct {
		Running []struct {
			ID                 string `json:"id"`
			SessionFingerprint string `json:"session_fingerprint"`
			SessionMachine     string `json:"session_machine"`
			CleanPause         bool   `json:"clean_pause"`
		} `json:"running"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/projects/alpha/ready-queue", e.key, nil, &q); code != 200 {
		t.Fatalf("ready queue: %d %s", code, raw)
	}
	if len(q.Running) != 1 || q.Running[0].ID != id ||
		q.Running[0].SessionFingerprint != api.SessionFingerprint(secret) ||
		q.Running[0].SessionMachine != "m1" || q.Running[0].CleanPause {
		t.Fatalf("running segment lost the session block: %+v", q.Running)
	}
}

// TestBlockedExitsUnifiedAuthorization (v2-converge §7, §11 item 7): the
// blocked exits — resume-blocked (continue) — and cancel (abandon) are
// open to exactly the work item's reporter, a project maintainer, or an
// admin. A plain project member (writer) who is none of those is refused
// on EVERY door — resume-blocked used to check the key scope only.
func TestBlockedExitsUnifiedAuthorization(t *testing.T) {
	e := newSrvEnv(t)
	mk := func(name, role string) (string, string) {
		u, err := e.admin.CreateUser(name, name+"@example.com", "writer", "human")
		if err != nil {
			t.Fatal(err)
		}
		k, err := e.admin.CreateAPIKey(u.ID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID, k.Plain
	}
	repID, repKey := mk("reporterx", "writer")
	otherID, otherKey := mk("memberx", "writer")
	maintID, maintKey := mk("maintx", "maintainer")
	_, outKey := mk("outsiderx", "writer")
	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{
			{UserID: repID, Role: "writer"},
			{UserID: otherID, Role: "writer"},
			{UserID: maintID, Role: "maintainer"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	block := func(goal, secret string) string {
		var wi struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
			map[string]any{"project": "alpha", "goal": goal, "bundle_name": "demo-bundle"}, &wi); code != 201 {
			t.Fatalf("reporter create: %d %s", code, raw)
		}
		if code, raw, _ := sessClaim(t, e, wi.ID, secret, "k-"+secret[:4]); code != 200 {
			t.Fatalf("claim: %d %s", code, raw)
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/session-end", e.key,
			map[string]string{"session_id": secret, "status": "blocked"}, nil); code != 200 {
			t.Fatalf("block: %d %s", code, raw)
		}
		return wi.ID
	}
	s := func(n byte) string { return strings.Repeat(string(rune('a'+n)), 32) }
	id := block("blocked auth", s(1))

	for _, door := range []string{"resume-blocked"} {
		for who, key := range map[string]string{"plain member": otherKey, "outsider": outKey} {
			if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/"+door, key, map[string]any{}, nil); code != 403 && code != 404 {
				t.Fatalf("%s on %s must be refused, got %d %s", who, door, code, raw)
			}
		}
	}
	// cancel (abandon) answers the same set
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/cancel", otherKey, map[string]any{}, nil); code != 403 {
		t.Fatalf("plain member cancel: %d %s", code, raw)
	}

	// the reporter continues
	var got struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", repKey, map[string]any{}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("reporter resume-blocked: %d %s", code, raw)
	}
	// a maintainer continues another blocked WI
	id2 := block("blocked auth 2", s(2))
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id2+"/resume-blocked", maintKey, map[string]any{}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("maintainer resume-blocked: %d %s", code, raw)
	}
	// an admin abandons a third
	id3 := block("blocked auth 3", s(3))
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id3+"/cancel", e.key, map[string]any{}, &got); code != 200 || got.Status != "canceled" {
		t.Fatalf("admin abandon: %d %s", code, raw)
	}
	// the reporter abandons a fourth
	id4 := block("blocked auth 4", s(4))
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id4+"/cancel", repKey, map[string]any{}, &got); code != 200 || got.Status != "canceled" {
		t.Fatalf("reporter cancel of a blocked WI: %d %s", code, raw)
	}
}

// TestBlockedSealHeadRoundTrip (v2-converge §7): the blocked verdict's
// head_sha is recorded with the sealing machine and answered on the next
// claim; a non-blocked close clears it (no stale seal on later claims); a
// blocked verdict without a head (an abandoned claim) keeps the standing
// seal.
func TestBlockedSealHeadRoundTrip(t *testing.T) {
	e := newSrvEnv(t)
	// a create cannot plant the seal: only the blocked close writes it
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
		"project": "alpha", "goal": "fake seal", "bundle_name": "demo-bundle",
		"attrs": map[string]any{"blocked_seal": map[string]any{"head": "abc123", "machine": "m9"}},
	}, nil); code != 400 || !strings.Contains(string(raw), "EXECUTION_STATE_REJECTED") {
		t.Fatalf("create with attrs.blocked_seal: %d %s, want 400 EXECUTION_STATE_REJECTED", code, raw)
	}
	id := createExecWIOverHTTP(t, e, "sealed head")
	end := func(secret string, body map[string]string) {
		t.Helper()
		body["session_id"] = secret
		if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key, body, nil); code != 200 {
			t.Fatalf("session-end %v: %d %s", body, code, raw)
		}
	}
	unblock := func() {
		t.Helper()
		if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/resume-blocked", e.key, map[string]any{}, nil); code != 200 {
			t.Fatalf("resume-blocked: %d %s", code, raw)
		}
	}
	s1, s2, s3 := strings.Repeat("a1", 16), strings.Repeat("b2", 16), strings.Repeat("c3", 16)

	_, _, first := sessClaim(t, e, id, s1, "seal-1")
	if first.Claim.SealedHead != "" {
		t.Fatalf("a fresh WI has no seal: %+v", first.Claim)
	}
	end(s1, map[string]string{"status": "blocked", "head_sha": "abc123"})
	unblock()
	code, raw, second := sessClaim(t, e, id, s2, "seal-2")
	if code != 200 || second.Claim.SealedHead != "abc123" || second.Claim.SealedMachine != "m1" {
		t.Fatalf("the claim must answer the sealed head + machine: %d %s", code, raw)
	}
	// an abandoned claim (blocked, no head) keeps the standing seal
	end(s2, map[string]string{"status": "blocked", "note": "refused"})
	unblock()
	_, _, third := sessClaim(t, e, id, s3, "seal-3")
	if third.Claim.SealedHead != "abc123" {
		t.Fatalf("a head-less blocked verdict must keep the seal: %+v", third.Claim)
	}
	// a pause clears it
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]any{"session_id": s3, "status": "paused", "clean_pause": true}, nil); code != 200 {
		t.Fatalf("pause: %d %s", code, raw)
	}
	var wi struct {
		Attrs map[string]any `json:"attrs"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 {
		t.Fatalf("get: %d %s", code, raw)
	}
	if _, has := wi.Attrs["blocked_seal"]; has {
		t.Fatalf("a pause must clear the seal: %v", wi.Attrs)
	}
}

// TestSessionClaimRequiresWriter: the claim route enforces writer
// membership like every sibling write route — an outsider key is refused
// (typed 403), a member writer and a global admin are accepted.
func TestSessionClaimRequiresWriter(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "claim authz")

	outsider, err := e.admin.CreateUser("outsider", "outsider@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	okey, err := e.admin.CreateAPIKey(outsider.ID, "o", "")
	if err != nil {
		t.Fatal(err)
	}
	claim := func(key string) (int, []byte) {
		return e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", key,
			map[string]string{"machine_id": "m1", "session_id": "s-" + key[:8], "idempotency_key": "k-" + key[:8]}, nil)
	}
	// Outsider: the WI is visible to wiProject, but the claim is refused.
	code, raw := claim(okey.Plain)
	if code != 403 {
		t.Fatalf("outsider claim: %d %s", code, raw)
	}
	if c, _ := errEnvelope(t, raw); c != string(api.ErrAuthForbidden) {
		t.Fatalf("outsider code = %q, want %s: %s", c, api.ErrAuthForbidden, raw)
	}
	// Viewer membership is not enough either.
	if err := e.admin.AddMember(e.adminProjectID(t, "alpha"), outsider.ID, "viewer"); err != nil {
		t.Fatal(err)
	}
	if code, raw := claim(okey.Plain); code != 403 {
		t.Fatalf("viewer claim: %d %s", code, raw)
	}
	// Writer member: accepted.
	if err := e.admin.AddMember(e.adminProjectID(t, "alpha"), outsider.ID, "writer"); err != nil {
		t.Fatal(err)
	}
	if code, raw := claim(okey.Plain); code != 200 && code != 201 {
		t.Fatalf("writer claim: %d %s", code, raw)
	}
	// Admin: accepted (a fresh WI so the CAS has no holder).
	id = createExecWIOverHTTP(t, e, "claim authz admin")
	if code, raw := claim(e.key); code != 200 && code != 201 {
		t.Fatalf("admin claim: %d %s", code, raw)
	}
}

// TestReporterGateContinueAbandonOnly (polyforge#149 D5, ruling B): the
// task owner IS the work item's reporter. A non-maintainer/non-admin
// REPORTER may continue a resume-blocked WI and abandon (cancel) a
// queued/paused/blocked WI; a project maintainer and a global admin may
// do the same; the reporter is still DENIED ForceEnd and every other
// admin/maintainer-only recovery gate (no gate widening).
func TestReporterGateContinueAbandonOnly(t *testing.T) {
	e := newSrvEnv(t)
	alpha := e.adminProjectID(t, "alpha")

	mk := func(name, role string) (string, string) {
		u, err := e.admin.CreateUser(name, name+"@example.com", role, "human")
		if err != nil {
			t.Fatal(err)
		}
		k, err := e.admin.CreateAPIKey(u.ID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u.ID, k.Plain
	}
	repID, repKey := mk("repd5", "writer")
	maintID, maintKey := mk("maintd5", "writer")
	_, outKey := mk("outd5", "writer")
	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{
			{UserID: repID, Role: "writer"},
			{UserID: maintID, Role: "maintainer"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	secret := func(n byte) string { return strings.Repeat(string(rune('0'+n)), 32) }

	// The reporter creates and parks a WI blocked (the frozen-ticket
	// shape); continue (resume-blocked) is the reporter's door.
	block := func(goal string, n byte) string {
		var wi struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
			map[string]any{"project": "alpha", "goal": goal, "bundle_name": "demo-bundle"}, &wi); code != 201 {
			t.Fatalf("reporter create: %d %s", code, raw)
		}
		if code, raw, _ := sessClaim(t, e, wi.ID, secret(n), "d5-"+string(rune('0'+n))); code != 200 {
			t.Fatalf("claim: %d %s", code, raw)
		}
		if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/session-end", e.key,
			map[string]string{"session_id": secret(n), "status": "blocked"}, nil); code != 200 {
			t.Fatalf("block: %d %s", code, raw)
		}
		return wi.ID
	}
	var got struct {
		Status string `json:"status"`
	}

	// The REPORTER continues a parked WI (blocked → queued).
	c := block("d5 reporter continue", 1)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+c+"/resume-blocked", repKey, map[string]any{}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("reporter continue: %d %s %s", code, raw, got.Status)
	}
	// A maintainer and an admin may continue too.
	c2 := block("d5 maintainer continue", 2)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+c2+"/resume-blocked", maintKey, map[string]any{}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("maintainer continue: %d %s %s", code, raw, got.Status)
	}
	c3 := block("d5 admin continue", 3)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+c3+"/resume-blocked", e.key, map[string]any{}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("admin continue: %d %s %s", code, raw, got.Status)
	}

	// The REPORTER abandons (cancel) a blocked WI (blocked → canceled).
	ab := block("d5 reporter abandon blocked", 4)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+ab+"/cancel", repKey, map[string]any{}, &got); code != 200 || got.Status != "canceled" {
		t.Fatalf("reporter abandon (blocked): %d %s %s", code, raw, got.Status)
	}
	// The reporter abandons a QUEUED WI.
	var qw struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
		map[string]any{"project": "alpha", "goal": "d5 queued abandon", "bundle_name": "demo-bundle"}, &qw); code != 201 {
		t.Fatalf("queued create: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+qw.ID+"/cancel", repKey, map[string]any{}, &got); code != 200 || got.Status != "canceled" {
		t.Fatalf("reporter abandon (queued): %d %s %s", code, raw, got.Status)
	}
	// An outsider (plain writer, not the reporter) is refused on both
	// exits.
	x := block("d5 outsider refused", 5)
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+x+"/resume-blocked", outKey, map[string]any{}, nil); code != 403 {
		t.Fatalf("outsider continue: want 403, got %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/work-items/"+x+"/cancel", outKey, map[string]any{}, nil); code != 403 {
		t.Fatalf("outsider abandon: want 403, got %d", code)
	}

	// The reporter is DENIED the admin/maintainer-only recovery gates:
	// force-end-session on their own RUNNING WI.
	var ow struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
		map[string]any{"project": "alpha", "goal": "d5 reporter force-end denied", "bundle_name": "demo-bundle"}, &ow); code != 201 {
		t.Fatalf("own create: %d %s", code, raw)
	}
	own := ow.ID
	if code, raw, _ := sessClaim(t, e, own, secret(7), "d5-fe"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+own+"/force-end-session", repKey,
		map[string]string{"reason": "holder lost"}, nil); code != 403 {
		t.Fatalf("reporter force-end-session: want 403, got %d %s", code, raw)
	}
	// ...and the project PATCH gate.
	if code, raw := e.do(t, "PATCH", "/api/v1/projects/"+alpha, repKey,
		map[string]any{"description": "no"}, nil); code != 403 {
		t.Fatalf("reporter project PATCH: want 403, got %d %s", code, raw)
	}
	// A maintainer may force-end (the recovery gate itself stays open to
	// its intended audience).
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+own+"/force-end-session", maintKey,
		map[string]string{"reason": "holder lost"}, &got); code != 200 || got.Status != "queued" {
		t.Fatalf("maintainer force-end-session: %d %s %s", code, raw, got.Status)
	}
}

// TestClaimRefusedWithoutChainCommitsNothing (CR-8): the bare repo is
// created before the claim transaction, so a hub that cannot create it
// refuses the claim with nothing committed — the work item stays queued
// for the retry, no claim waits on an idempotency-cache replay.
func TestClaimRefusedWithoutChainCommitsNothing(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "no chain")
	// a file where the bare repo must go: git init fails
	if err := os.MkdirAll(e.srv.Git.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.srv.Git.RepoDir(id), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, raw, _ := sessClaim(t, e, id, "81818181818181818181818181818181", "cr8"); code != 500 {
		t.Fatalf("claim without a chain: %d %s", code, raw)
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 {
		t.Fatalf("get: %d %s", code, raw)
	}
	if wi.Status != api.StatusQueued {
		t.Fatalf("a refused claim must commit nothing: status %q", wi.Status)
	}
	if err := os.Remove(e.srv.Git.RepoDir(id)); err != nil {
		t.Fatal(err)
	}
	if code, raw, _ := sessClaim(t, e, id, "81818181818181818181818181818181", "cr8"); code != 200 {
		t.Fatalf("the retry must claim: %d %s", code, raw)
	}
}
