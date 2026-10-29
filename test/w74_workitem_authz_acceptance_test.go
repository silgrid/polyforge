package test

// w74_workitem_authz_acceptance_test.go: the e2e acceptance suite for
// (aihub work-item authorization gaps), authored BEFORE `code`
// against artifacts/spec.md only. Every scenario drives the shipped aihub
// binary as a real process over real HTTP with real users, API keys, project
// memberships, work items, sessions and machine rows; no implementation
// internal and no symbol the pre-implementation tree lacks is referenced, so
// every baseline red is assertion-level, never build-level.
//
// Scenario document: artifacts/e2e-scenarios.md (W74-S1..W74-S7).

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

type w74Signer struct {
	machine string
	priv    ed25519.PrivateKey
}

func w74JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func w74Secret(seed byte) string { return strings.Repeat(fmt.Sprintf("%02x", seed), 32) }

func w74Raw(t *testing.T, inst *v1Install, key, method, path string, body []byte, signer *w74Signer) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, inst.url+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if signer != nil {
		ts := time.Now().Unix()
		req.Header.Set("X-PF-Machine-ID", signer.machine)
		req.Header.Set("X-PF-Timestamp", fmt.Sprint(ts))
		req.Header.Set("X-PF-Signature", w57Sign(method, path, body, ts, signer.machine, signer.priv))
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, string(raw)
}

func w74Send(t *testing.T, inst *v1Install, key, ref, suffix, method string, body []byte, signer *w74Signer) (int, string) {
	t.Helper()
	return w74Raw(t, inst, key, method, "/api/v1/work-items/"+url.PathEscape(ref)+suffix, body, signer)
}

func w74Norm(body, ref string) string { return strings.ReplaceAll(body, ref, "<ref>") }

// w74Concealed compares one route's answer for an existing work item with its
// answer for a missing reference: 404 CORE-404 and the same body modulo the
// reference, with no work-item state or holder data.
func w74Concealed(t *testing.T, inst *v1Install, key, ref, missing, name, method, suffix string, body []byte, signer *w74Signer) {
	t.Helper()
	sm, bm := w74Send(t, inst, key, missing, suffix, method, body, signer)
	if sm != http.StatusNotFound || !strings.Contains(bm, api.ErrCoreNotFound) {
		t.Errorf("%s: missing reference answered %d %s, want 404 %s", name, sm, bm, api.ErrCoreNotFound)
	}
	se, be := w74Send(t, inst, key, ref, suffix, method, body, signer)
	if se != sm || w74Norm(be, ref) != w74Norm(bm, missing) {
		t.Errorf("%s: existing answered %d %s; missing answered %d %s", name, se, be, sm, bm)
	}
	for _, leak := range []string{`"session_fingerprint"`, `"status":"running"`, `"holder_machine"`} {
		if strings.Contains(be, leak) {
			t.Errorf("%s: concealed answer leaked %s: %s", name, leak, be)
		}
	}
}

func w74Scalar(t *testing.T, inst *v1Install, q string, args ...any) string {
	t.Helper()
	db := w57DB(t, inst)
	defer db.Close()
	var v any
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if v == nil {
		return "<null>"
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

func w74Project(t *testing.T, inst *v1Install, name string) string {
	t.Helper()
	raw, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{Name: name})
	if err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" {
		t.Fatalf("project %s answer: %v (%s)", name, err, raw)
	}
	return p.ID
}

func w74Member(t *testing.T, inst *v1Install, project, userID, role string) {
	t.Helper()
	members := []api.ProjectMember{{UserID: userID, Role: role}}
	if _, err := inst.client.PatchProject(project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
		t.Fatalf("add %s as %s of %s: %v", userID, role, project, err)
	}
}

func w74RemoveMember(t *testing.T, inst *v1Install, project, userID string) {
	t.Helper()
	ids := []string{userID}
	if _, err := inst.client.PatchProject(project, aihubclient.ProjectPatchRequest{RemoveMembers: &ids}); err != nil {
		t.Fatalf("remove %s from %s: %v", userID, project, err)
	}
}

func w74User(t *testing.T, inst *v1Install, name, role, memberRole string) (string, string) {
	t.Helper()
	id, _, key := w57User(t, inst, name, role, memberRole)
	return id, key
}

func w74ScopedKey(t *testing.T, inst *v1Install, userID, projectID string) string {
	t.Helper()
	raw, err := inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: userID, ProjectID: &projectID})
	if err != nil {
		t.Fatalf("mint scoped key: %v", err)
	}
	var key struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || key.Plain == "" {
		t.Fatalf("scoped key answer: %v (%s)", err, raw)
	}
	return key.Plain
}

func w74NewWI(t *testing.T, inst *v1Install, c *aihubclient.Client, project, goal string, bundle *string) string {
	t.Helper()
	raw, err := c.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: project, Goal: goal, BundleName: bundle})
	if err != nil {
		t.Fatalf("create work item %q: %v", goal, err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("work item answer: %v (%s)", err, raw)
	}
	inst.trackWI(wi.ID)
	return wi.ID
}

func w74ErrCode(err error) string { return w11ErrCode(err) }

// ---------------------------------------------------------------------------
// W74-S1: every work-item-addressed route conceals an existing work item from
// a non-member exactly as a missing reference is answered.

func TestW74NonMemberRouteMatrixConcealsWorkItem(t *testing.T) {
	inst := newV1Install(t)
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 concealment matrix", strPtr("autopilot"))
	w74Project(t, inst, "w74-other")
	userID, key := w74User(t, inst, "w74-nonmember", "writer", "")
	w74Member(t, inst, "w74-other", userID, "viewer")
	pub, priv := w57Key(t, 0x74)
	if ans := w57Register(t, inst, key, "w74-nonmember-machine", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("non-member machine registration: %s", ans)
	}
	signer := &w74Signer{machine: "w74-nonmember-machine", priv: priv}
	missing := "wi_w74_missing_matrix"
	sid := w74Secret(0x01)
	body := func(v any) []byte { return w74JSON(t, v) }

	routes := []struct {
		name, method, suffix string
		body                 []byte
		signed               bool
	}{
		{"get", "GET", "", nil, false},
		{"patch", "PATCH", "", body(map[string]any{"goal": "x"}), false},
		{"cancel", "POST", "/cancel", body(map[string]any{}), false},
		{"wrap", "POST", "/wrap", body(map[string]any{}), false},
		{"claim", "POST", "/claim", body(map[string]any{"session_id": sid, "machine_id": ""}), false},
		{"session-verify", "POST", "/session-verify", body(map[string]any{"session_id": sid}), false},
		{"session-attach", "POST", "/session-attach", body(map[string]any{"session_id": sid, "machine_id": ""}), false},
		{"session-end", "POST", "/session-end", body(map[string]any{"session_id": sid, "status": "paused"}), false},
		{"force-end-session", "POST", "/force-end-session", body(map[string]any{"reason": "probe"}), false},
		{"resume-blocked", "POST", "/resume-blocked", body(map[string]any{}), false},
		{"dependencies-get", "GET", "/dependencies", nil, false},
		{"dependencies-add", "POST", "/dependencies", body(map[string]any{"blocking_wi_id": "wi_w74_blocker", "kind": "blocks"}), false},
		{"dependencies-delete", "DELETE", "/dependencies/wi_w74_blocker/blocks", nil, false},
		{"progress-query", "POST", "/progress-query", body(map[string]any{}), true},
		{"artifact-query", "POST", "/artifact-query", body(map[string]any{}), true},
	}
	for _, r := range routes {
		var s *w74Signer
		if r.signed {
			s = signer
		}
		w74Concealed(t, inst, key, wi, missing, r.name, r.method, r.suffix, r.body, s)
	}
	if st := inst.wiStatus(t, wi); st != api.StatusQueued {
		t.Fatalf("concealment probe changed the work item: status %q", st)
	}
}

// ---------------------------------------------------------------------------
// W74-S2: a non-member holding the correct live session secret still sees a
// missing work item and cannot move it.

func TestW74LeakedSessionSecretDoesNotHelpNonMember(t *testing.T) {
	inst := newV1Install(t)
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 leaked secret", strPtr("autopilot"))
	w74Project(t, inst, "w74-other")
	attackerID, attackerKey := w74User(t, inst, "w74-attacker", "writer", "")
	w74Member(t, inst, "w74-other", attackerID, "viewer")
	_, writerKey := w74User(t, inst, "w74-secret-writer", "", "writer")
	sid := w74Secret(0x22)
	if _, err := aihubclient.New(inst.url, writerKey).SessionClaim(api.SessionClaimRequest{
		WorkItemID: wi, SessionID: sid, MachineID: "w74-secret-machine",
	}); err != nil {
		t.Fatalf("premise: writer claim: %v", err)
	}
	fp := inst.sessionOf(t, wi).Fingerprint
	events := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi)

	missing := "wi_w74_missing_secret"
	for _, r := range []struct {
		name, suffix string
		body         []byte
	}{
		{"session-verify", "/session-verify", w74JSON(t, map[string]any{"session_id": sid})},
		{"session-attach", "/session-attach", w74JSON(t, map[string]any{"session_id": sid, "machine_id": "w74-secret-machine"})},
		{"session-end", "/session-end", w74JSON(t, map[string]any{"session_id": sid, "status": "failed"})},
	} {
		w74Concealed(t, inst, attackerKey, wi, missing, r.name, "POST", r.suffix, r.body, nil)
	}
	if st := inst.wiStatus(t, wi); st != api.StatusRunning {
		t.Fatalf("refused leaked-secret calls changed status to %q", st)
	}
	if got := inst.sessionOf(t, wi).Fingerprint; got != fp {
		t.Fatalf("refused leaked-secret calls changed the session fingerprint: %q -> %q", fp, got)
	}
	if got := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi); got != events {
		t.Fatalf("refused leaked-secret calls wrote events: %s -> %s", events, got)
	}
}

// ---------------------------------------------------------------------------
// W74-S3: an API key scoped to another project gets the missing-work-item
// answer on work-item routes, while its own project routes stay unchanged.

func TestW74ScopedKeyConcealsWorkItemsOnly(t *testing.T) {
	inst := newV1Install(t)
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 scoped key", strPtr("autopilot"))
	scopeID := w74Project(t, inst, "w74-scope")
	userID, _ := w74User(t, inst, "w74-scoped", "writer", "")
	w74Member(t, inst, "w74-scope", userID, "writer")
	key := w74ScopedKey(t, inst, userID, scopeID)
	pub, priv := w57Key(t, 0x75)
	if ans := w57Register(t, inst, key, "w74-scoped-machine", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("scoped-key machine registration: %s", ans)
	}
	signer := &w74Signer{machine: "w74-scoped-machine", priv: priv}
	missing := "wi_w74_missing_scope"
	w74Concealed(t, inst, key, wi, missing, "get", "GET", "", nil, nil)
	w74Concealed(t, inst, key, wi, missing, "progress-query", "POST", "/progress-query",
		w74JSON(t, map[string]any{}), signer)
	// The body-addressed reference conceals too ( A1): a
	// memory in the key's own project attributed to the foreign work item
	// answers exactly as one attributed to a missing reference.
	remember := func(ref string) (int, string) {
		return w74Raw(t, inst, key, "POST", "/api/v1/memories", w74JSON(t, map[string]any{
			"project": "w74-scope", "type": "fact.note", "visibility": "project",
			"content": "w74 scoped attribution probe", "work_item_id": ref,
		}), nil)
	}
	sm, bm := remember(missing)
	se, be := remember(wi)
	if sm != http.StatusNotFound || !strings.Contains(bm, api.ErrCoreNotFound) ||
		se != sm || w74Norm(be, wi) != w74Norm(bm, missing) {
		t.Errorf("memory work_item_id: existing answered %d %s; missing answered %d %s", se, be, sm, bm)
	}
	if st, body := w74Raw(t, inst, key, "GET", "/api/v1/projects/w74-scope", nil, nil); st != http.StatusOK {
		t.Fatalf("scoped key lost its own project route: %d %s", st, body)
	}
}

// ---------------------------------------------------------------------------
// W74-S4: a reader verifies a live session but cannot attach, end, claim or
// PATCH; the session and work item are unchanged.

func TestW74ReaderVerifiesButCannotWriteSession(t *testing.T) {
	inst := newV1Install(t)
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 reader role", strPtr("autopilot"))
	_, writerKey := w74User(t, inst, "w74-role-writer", "", "writer")
	_, readerKey := w74User(t, inst, "w74-role-reader", "", "viewer")
	sid := w74Secret(0x33)
	if _, err := aihubclient.New(inst.url, writerKey).SessionClaim(api.SessionClaimRequest{
		WorkItemID: wi, SessionID: sid, MachineID: "w74-role-machine",
	}); err != nil {
		t.Fatalf("premise: writer claim: %v", err)
	}
	fp := inst.sessionOf(t, wi).Fingerprint
	events := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi)

	st, body := w74Raw(t, inst, readerKey, "POST", "/api/v1/work-items/"+wi+"/session-verify",
		w74JSON(t, map[string]any{"session_id": sid}), nil)
	if st != http.StatusOK || !strings.Contains(body, `"matches":true`) {
		t.Fatalf("reader verify = %d %s, want the normal verify answer", st, body)
	}
	for _, r := range []struct {
		name, suffix string
		body         []byte
	}{
		{"session-attach", "/session-attach", w74JSON(t, map[string]any{"session_id": sid, "machine_id": "w74-role-machine"})},
		{"session-end", "/session-end", w74JSON(t, map[string]any{"session_id": sid, "status": "wrapped"})},
		{"claim", "/claim", w74JSON(t, map[string]any{"session_id": w74Secret(0x34), "machine_id": ""})},
	} {
		st, body = w74Raw(t, inst, readerKey, "POST", "/api/v1/work-items/"+wi+r.suffix, r.body, nil)
		if st != http.StatusForbidden || !strings.Contains(body, api.ErrAuthForbidden) {
			t.Fatalf("reader %s = %d %s, want 403 %s", r.name, st, body, api.ErrAuthForbidden)
		}
	}
	st, body = w74Raw(t, inst, readerKey, "PATCH", "/api/v1/work-items/"+wi,
		w74JSON(t, map[string]any{"goal": "reader edit"}), nil)
	if st != http.StatusForbidden || !strings.Contains(body, api.ErrAuthForbidden) {
		t.Fatalf("reader PATCH = %d %s, want 403 %s", st, body, api.ErrAuthForbidden)
	}
	if got := inst.wiStatus(t, wi); got != api.StatusRunning {
		t.Fatalf("reader writes changed status to %q", got)
	}
	if got := inst.sessionOf(t, wi).Fingerprint; got != fp {
		t.Fatalf("reader writes changed the session fingerprint: %q -> %q", fp, got)
	}
	if got := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi); got != events {
		t.Fatalf("reader writes wrote events: %s -> %s", events, got)
	}
}

// ---------------------------------------------------------------------------
// W74-S5: a holder whose membership is revoked sees a missing work item; the
// work item stays running until a maintainer force-ends it.

func TestW74DeMemberedHolderStaysRunningUntilForceEnd(t *testing.T) {
	inst := newV1Install(t)
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 de-membered holder", strPtr("autopilot"))
	holderID, holderKey := w74User(t, inst, "w74-holder", "", "writer")
	_, maintainerKey := w74User(t, inst, "w74-maintainer", "", "maintainer")
	sid := w74Secret(0x44)
	if _, err := aihubclient.New(inst.url, holderKey).SessionClaim(api.SessionClaimRequest{
		WorkItemID: wi, SessionID: sid, MachineID: "w74-holder-machine",
	}); err != nil {
		t.Fatalf("premise: holder claim: %v", err)
	}
	fp := inst.sessionOf(t, wi).Fingerprint
	events := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi)
	w74RemoveMember(t, inst, v1Project, holderID)

	missing := "wi_w74_missing_holder"
	w74Concealed(t, inst, holderKey, wi, missing, "de-membered verify", "POST", "/session-verify",
		w74JSON(t, map[string]any{"session_id": sid}), nil)
	w74Concealed(t, inst, holderKey, wi, missing, "de-membered end", "POST", "/session-end",
		w74JSON(t, map[string]any{"session_id": sid, "status": "failed"}), nil)
	if got := inst.wiStatus(t, wi); got != api.StatusRunning {
		t.Fatalf("de-membered holder calls changed status to %q", got)
	}
	if got := inst.sessionOf(t, wi).Fingerprint; got != fp {
		t.Fatalf("de-membered holder calls changed the session: %q -> %q", fp, got)
	}
	if got := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi); got != events {
		t.Fatalf("de-membered holder calls wrote events: %s -> %s", events, got)
	}
	st, body := w74Raw(t, inst, maintainerKey, "POST", "/api/v1/work-items/"+wi+"/force-end-session",
		w74JSON(t, map[string]any{"reason": "operator left the project"}), nil)
	if st != http.StatusOK {
		t.Fatalf("maintainer force-end = %d %s", st, body)
	}
	if got := inst.wiStatus(t, wi); got != api.StatusQueued {
		t.Fatalf("maintainer force-end left status %q, want queued", got)
	}
	if got := inst.sessionOf(t, wi).Fingerprint; got != "" {
		t.Fatalf("maintainer force-end left session %q", got)
	}
}

// ---------------------------------------------------------------------------
// W74-S6: the heartbeat machine-id collision still refuses with the typed
// AUTH-403 and --machine-id advice, but names no operator; the admin machine
// inventory still maps the machine to its operator.

func TestW74HeartbeatCollisionNamesNoOperator(t *testing.T) {
	inst := newV1Install(t)
	opID, _, opKey := w57User(t, inst, "w74-hb-owner", "", "maintainer")
	_, _, otherKey := w57User(t, inst, "w74-hb-other", "", "writer")
	pub, priv := w57Key(t, 0x66)
	if ans := w57Register(t, inst, opKey, "w74-hb-machine", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: owner registers w74-hb-machine: %s", ans)
	}
	body := w57JSON(t, map[string]any{"machine_id": "w74-hb-machine", "live_wi_ids": []string{"wi-other"}})
	ans := w57Beat(t, inst, otherKey, body, "w74-hb-machine", priv, 0)
	if ans.Code != api.ErrAuthForbidden {
		t.Fatalf("collision = %s, want %s", ans, api.ErrAuthForbidden)
	}
	if !strings.Contains(ans.Detail, "--machine-id") {
		t.Fatalf("collision no longer suggests a different --machine-id: %s", ans.Detail)
	}
	if !strings.Contains(strings.ToLower(ans.Detail), "another operator") {
		t.Fatalf("collision no longer says the machine belongs to another operator: %s", ans.Detail)
	}
	if strings.Contains(ans.Detail, opID) || strings.Contains(ans.Detail, "w74-hb-owner") {
		t.Fatalf("collision leaked the existing operator %s: %s", opID, ans.Detail)
	}
	w57CheckRow(t, inst, "w74-hb-machine", map[string]any{"operator_id": opID, "public_key": pub})
	if got, _ := w57Machines(t, inst)["w74-hb-machine"]["operator_id"]; got != opID {
		t.Fatalf("admin machine inventory lost the operator mapping: %v", got)
	}
}

// ---------------------------------------------------------------------------
// W74-S7: claim and attach record only a caller-owned machine; another
// operator's machine is refused with AUTH-403 and no state change; an unknown
// machine is recorded as NULL while the claim/attach succeeds.

func TestW74MachineOwnershipOnClaimAndAttach(t *testing.T) {
	inst := newV1Install(t)
	ownerID, _, _ := w57User(t, inst, "w74-machine-owner", "", "writer")
	_, _, callerKey := w57User(t, inst, "w74-machine-caller", "", "writer")
	w57SeedRow(t, inst, "w74-owned-machine", ownerID, "")
	caller := aihubclient.New(inst.url, callerKey)

	// Another operator's machine on claim, as a writer and as a global admin.
	wi := w74NewWI(t, inst, inst.client, v1Project, "w74 foreign machine claim", strPtr("autopilot"))
	before := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi)
	if _, err := caller.SessionClaim(api.SessionClaimRequest{
		WorkItemID: wi, SessionID: w74Secret(0x77), MachineID: "w74-owned-machine",
	}); w74ErrCode(err) != api.ErrAuthForbidden {
		t.Fatalf("foreign-machine claim code = %q (%v), want %s", w74ErrCode(err), err, api.ErrAuthForbidden)
	}
	if st := inst.wiStatus(t, wi); st != api.StatusQueued {
		t.Fatalf("rejected claim left status %q, want queued", st)
	}
	if got := w74Scalar(t, inst, `SELECT COALESCE(session_hash,'<null>') FROM work_items WHERE id=?1`, wi); got != "<null>" {
		t.Fatalf("rejected claim opened a session: %s", got)
	}
	if got := w74Scalar(t, inst, `SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wi); got != before {
		t.Fatalf("rejected claim wrote events: %s -> %s", before, got)
	}
	wiAdmin := w74NewWI(t, inst, inst.client, v1Project, "w74 admin foreign machine claim", strPtr("autopilot"))
	if _, err := inst.client.SessionClaim(api.SessionClaimRequest{
		WorkItemID: wiAdmin, SessionID: w74Secret(0x78), MachineID: "w74-owned-machine",
	}); w74ErrCode(err) != api.ErrAuthForbidden {
		t.Fatalf("admin foreign-machine claim code = %q (%v), want %s", w74ErrCode(err), err, api.ErrAuthForbidden)
	}

	// An unknown machine records NULL, then the same session cannot attach
	// with another operator's machine id.
	wiUnknown := w74NewWI(t, inst, inst.client, v1Project, "w74 unknown machine claim", strPtr("autopilot"))
	sid := w74Secret(0x79)
	resp, err := caller.SessionClaim(api.SessionClaimRequest{
		WorkItemID: wiUnknown, SessionID: sid, MachineID: "w74-unknown-machine",
	})
	if err != nil {
		t.Fatalf("unknown-machine claim was refused: %v", err)
	}
	if resp.Claim.MachineID != "" {
		t.Fatalf("unknown-machine claim recorded %q, want NULL", resp.Claim.MachineID)
	}
	if got := w74Scalar(t, inst, `SELECT COALESCE(session_machine,'<null>') FROM work_items WHERE id=?1`, wiUnknown); got != "<null>" {
		t.Fatalf("unknown-machine claim stored %q, want NULL", got)
	}
	if err := caller.SessionEnd(wiUnknown, api.SessionEndRequest{SessionID: sid, Status: "paused"}); err != nil {
		t.Fatalf("premise: pause the claimed work item: %v", err)
	}
	if _, err := caller.SessionAttach(wiUnknown, api.SessionAttachRequest{
		WorkItemID: wiUnknown, SessionID: sid, MachineID: "w74-owned-machine",
	}); w74ErrCode(err) != api.ErrAuthForbidden {
		t.Fatalf("foreign-machine attach code = %q (%v), want %s", w74ErrCode(err), err, api.ErrAuthForbidden)
	}
	if st := inst.wiStatus(t, wiUnknown); st != api.StatusPaused {
		t.Fatalf("rejected attach left status %q, want paused", st)
	}
	if got := w74Scalar(t, inst, `SELECT COALESCE(session_machine,'<null>') FROM work_items WHERE id=?1`, wiUnknown); got != "<null>" {
		t.Fatalf("rejected attach changed the session machine to %q", got)
	}
}
