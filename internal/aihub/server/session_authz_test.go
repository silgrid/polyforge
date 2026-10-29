package server

// session_authz_test.go — the session and relay routes
// behind the shared work-item grant. session-verify requires read
// access, session-attach/session-end require writer like claim, and the
// signed progress/artifact relay requires read — all before body
// decoding, so a non-member (even holding the correct live session
// secret) gets the missing-work-item answer and no refusal writes
// state. A de-membered holder stays running until a maintainer
// force-end.
//
// The file is self-contained against the pre-change tree, so the
// external oracle can run it red on the base and green on the branch.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// wibSession reads the raw session columns of a work item.
func wibSession(t *testing.T, e *srvEnv, wiID string) (status, hash, fp, machine string) {
	t.Helper()
	if err := e.db.QueryRow(`SELECT status, COALESCE(session_hash,''), COALESCE(session_fingerprint,''),
		COALESCE(session_machine,'') FROM work_items WHERE id=?1`, wiID).
		Scan(&status, &hash, &fp, &machine); err != nil {
		t.Fatalf("session row of %s: %v", wiID, err)
	}
	return
}

// TestSessionRoutesConcealLeakedSecret: a non-member holding the
// correct live session secret gets the missing-work-item answer on
// verify, attach and end, and nothing changes.
func TestSessionRoutesConcealLeakedSecret(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74b leaked secret")
	secret := strings.Repeat("f6", 16)
	if code, raw, _ := sessClaim(t, e, id, secret, "w74b-ls"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	st, hash, fp, machine := wibSession(t, e, id)
	events := wiaEventCount(t, e, id)

	_, outKey := memberUser(t, e, "w74b-attacker", "")
	missing := "wi_w74b_missing_secret"
	for _, r := range []struct {
		name, suffix string
		body         any
	}{
		{"session-verify", "/session-verify", map[string]any{"session_id": secret}},
		{"session-attach", "/session-attach", map[string]any{"session_id": secret, "machine_id": "m-w74b"}},
		{"session-end", "/session-end", map[string]any{"session_id": secret, "status": "failed"}},
	} {
		wiaConcealed(t, e, outKey, id, missing, r.name, "POST", r.suffix, r.body)
	}
	if st2, hash2, fp2, machine2 := wibSession(t, e, id); st2 != st || hash2 != hash || fp2 != fp || machine2 != machine {
		t.Fatalf("refused leaked-secret calls changed the session row: %q/%q/%q/%q -> %q/%q/%q/%q",
			st, hash, fp, machine, st2, hash2, fp2, machine2)
	}
	if got := wiaEventCount(t, e, id); got != events {
		t.Fatalf("refused leaked-secret calls wrote events: %d -> %d", events, got)
	}
}

// TestSessionRoleGatesAndNormalFlows: a reader verifies but is AUTH-403
// on attach and end with no state change; a writer member and an admin
// keep the existing verify/attach/end behaviour.
func TestSessionRoleGatesAndNormalFlows(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74b role gates")
	secret := strings.Repeat("17", 16)
	if code, raw, _ := sessClaim(t, e, id, secret, "w74b-rg"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	events := wiaEventCount(t, e, id)
	_, readerKey := memberUser(t, e, "w74b-reader", "viewer")
	_, writerKey := memberUser(t, e, "w74b-writer", "writer")

	// A reader verifies normally (the normal verify answer, no leak of
	// anything a non-reader could not see — the reader is a member).
	var resp api.SessionVerifyResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", readerKey,
		map[string]string{"session_id": secret}, &resp); code != 200 || !resp.Matches ||
		resp.Status != api.StatusRunning || resp.CurrentFingerprint != api.SessionFingerprint(secret) {
		t.Fatalf("reader verify: %d %s %+v", code, raw, resp)
	}
	// A reader cannot attach or end: the route's AUTH-403, before any
	// core diagnosis, with no state change.
	for _, r := range []struct {
		name, suffix string
		body         any
	}{
		{"session-attach", "/session-attach", map[string]any{"session_id": secret, "machine_id": "m-w74b"}},
		{"session-end", "/session-end", map[string]any{"session_id": secret, "status": "wrapped"}},
	} {
		code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+r.suffix, readerKey, r.body, nil)
		if code != 403 {
			t.Fatalf("reader %s: %d %s, want the route's 403", r.name, code, raw)
		}
		if c, _ := errEnvelope(t, raw); c != api.ErrAuthForbidden {
			t.Fatalf("reader %s code: %s", r.name, raw)
		}
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", readerKey,
		map[string]string{"session_id": secret}, &resp); code != 200 || !resp.Matches {
		t.Fatalf("reader verify after refusals: %d %s %+v", code, raw, resp)
	}
	if got := wiaEventCount(t, e, id); got != events {
		t.Fatalf("refused reader session writes wrote events: %d -> %d", events, got)
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusRunning {
		t.Fatalf("refused reader session writes changed the WI: %d %s %+v", code, raw, wi)
	}

	// A writer member keeps the existing session flow: pause, attach back
	// under the same secret, then end wrapped.
	paused := createExecWIOverHTTP(t, e, "w74b writer flow")
	psecret := strings.Repeat("28", 16)
	if code, raw, _ := sessClaim(t, e, paused, psecret, "w74b-wf"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+paused+"/session-end", writerKey,
		map[string]any{"session_id": psecret, "status": "paused", "clean_pause": false}, nil); code != 200 {
		t.Fatalf("writer pause: %d %s", code, raw)
	}
	var att api.SessionClaimResponse
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+paused+"/session-attach", writerKey,
		map[string]string{"session_id": psecret, "machine_id": "m-w74b"}, &att); code != 200 || att.WorkItem.Status != api.StatusRunning {
		t.Fatalf("writer attach: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+paused+"/session-end", writerKey,
		map[string]string{"session_id": psecret, "status": "wrapped"}, nil); code != 200 {
		t.Fatalf("writer end: %d %s", code, raw)
	}

	// A global admin outside the project is admitted by the admin role as
	// before (verify on the first WI).
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/session-verify", e.key,
		map[string]string{"session_id": secret}, &resp); code != 200 || !resp.Matches {
		t.Fatalf("admin verify: %d %s %+v", code, raw, resp)
	}
}

// TestDeMemberedHolderStaysRunningUntilForceEnd: a holder whose
// membership is revoked sees a missing work item on verify and end; the
// work item stays running with its session and events unchanged until a
// maintainer force-ends it.
func TestDeMemberedHolderStaysRunningUntilForceEnd(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74b de-membered holder")
	holderID, holderKey := memberUser(t, e, "w74b-holder", "writer")
	_, maintKey := memberUser(t, e, "w74b-maintainer", "maintainer")
	secret := strings.Repeat("39", 16)
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", holderKey,
		map[string]string{"session_id": secret, "machine_id": "m-w74b-hold"}, nil); code != 200 {
		t.Fatalf("holder claim: %d %s", code, raw)
	}
	st, hash, fp, machine := wibSession(t, e, id)
	events := wiaEventCount(t, e, id)

	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		RemoveMembers: &[]string{holderID},
	}); err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	missing := "wi_w74b_missing_holder"
	for _, r := range []struct {
		name, suffix string
		body         any
	}{
		{"session-verify", "/session-verify", map[string]any{"session_id": secret}},
		{"session-end", "/session-end", map[string]any{"session_id": secret, "status": "failed"}},
	} {
		wiaConcealed(t, e, holderKey, id, missing, "de-membered "+r.name, "POST", r.suffix, r.body)
	}
	if st2, hash2, fp2, machine2 := wibSession(t, e, id); st2 != api.StatusRunning || hash2 != hash || fp2 != fp || machine2 != machine {
		t.Fatalf("de-membered holder calls changed the WI: %q/%q/%q/%q -> %q/%q/%q/%q",
			st, hash, fp, machine, st2, hash2, fp2, machine2)
	}
	if got := wiaEventCount(t, e, id); got != events {
		t.Fatalf("de-membered holder calls wrote events: %d -> %d", events, got)
	}

	// The accepted recovery: a maintainer force-end returns it to queued.
	var view struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/force-end-session", maintKey,
		map[string]string{"reason": "operator left the project"}, &view); code != 200 || view.Status != api.StatusQueued {
		t.Fatalf("maintainer force-end: %d %s %+v", code, raw, view)
	}
	if st3, hash3, _, _ := wibSession(t, e, id); st3 != api.StatusQueued || hash3 != "" {
		t.Fatalf("force-end left status %q session %q", st3, hash3)
	}
}

// TestRelayConcealsCallersWithoutAccess: the signed progress/artifact
// relay answers a non-member and a scoped-away key exactly as a missing
// reference, while a member keeps the existing holder-resolution path.
func TestRelayConcealsCallersWithoutAccess(t *testing.T) {
	e := newSrvEnv(t)
	wi := newRunningWI(t, e, "m-w74b-hold")
	outID, outKey := memberUser(t, e, "w74b-relayout", "")
	priv, _ := signedMachineFor(t, e, "m-w74b-out", outID)
	missing := "wi_w74b_missing_relay"

	signedConcealed := func(name, suffix string, body any) {
		t.Helper()
		sm, bm := signedPost(t, e, outKey, "m-w74b-out", priv,
			"/api/v1/work-items/"+missing+suffix, body)
		se, be := signedPost(t, e, outKey, "m-w74b-out", priv,
			"/api/v1/work-items/"+wi+suffix, body)
		if sm != 404 || !strings.Contains(string(bm), api.ErrCoreNotFound) {
			t.Errorf("%s: missing reference answered %d %s", name, sm, bm)
		}
		if se != sm || wiaNorm(be, wi) != wiaNorm(bm, missing) {
			t.Errorf("%s: existing answered %d %s; missing answered %d %s", name, se, be, sm, bm)
		}
	}
	signedConcealed("progress-query", "/progress-query", api.ProgressQuery{WorkItem: wi})
	signedConcealed("artifact-query", "/artifact-query", api.ProgressQuery{WorkItem: wi, ArtifactPath: "spec.md"})

	// A key scoped to another project is concealed the same way, while
	// its own project route stays unchanged.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w74b-scope"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	var scopeID string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='w74b-scope'`).Scan(&scopeID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	scopedUser, _ := memberUser(t, e, "w74b-scoped", "writer")
	if _, err := e.admin.PatchProject("w74b-scope", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{{UserID: scopedUser, Role: "writer"}},
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	scoped, err := e.admin.CreateAPIKey(scopedUser, "w74b-scopekey", scopeID)
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}
	spriv, _ := signedMachineFor(t, e, "m-w74b-scoped", scopedUser)
	sm, bm := signedPost(t, e, scoped.Plain, "m-w74b-scoped", spriv,
		"/api/v1/work-items/"+missing+"/progress-query", api.ProgressQuery{WorkItem: missing})
	se, be := signedPost(t, e, scoped.Plain, "m-w74b-scoped", spriv,
		"/api/v1/work-items/"+wi+"/progress-query", api.ProgressQuery{WorkItem: wi})
	if sm != 404 || se != sm || wiaNorm(be, wi) != wiaNorm(bm, missing) {
		t.Errorf("scoped progress-query: existing %d %s; missing %d %s", se, be, sm, bm)
	}
	if code, raw := e.do(t, "GET", "/api/v1/projects/w74b-scope", scoped.Plain, nil, nil); code != 200 {
		t.Fatalf("scoped key lost its own project route: %d %s", code, raw)
	}

	// A member keeps the existing relay path: the gate admits, the holder
	// resolution answers its typed branch (the seeded holder machine has
	// no machines row, so CORE-410 — not concealment).
	adminPriv, _ := signedMachineFor(t, e, "m-w74b-admin", e.adminID)
	if code, body := signedPost(t, e, e.key, "m-w74b-admin", adminPriv,
		"/api/v1/work-items/"+wi+"/progress-query", api.ProgressQuery{WorkItem: wi}); code != 410 || errCodeOf(body) != api.ErrCoreGone {
		t.Fatalf("member relay must reach holder resolution: %d %s", code, body)
	}
}
