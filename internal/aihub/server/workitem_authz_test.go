package server

// workitem_authz_test.go — the shared work-item grant.
// Every work-item-addressed route answers a caller without access — a
// non-member with no retained reporter right, or an api key scoped to
// another project — exactly as it answers a missing reference; a member
// whose role is too low keeps the route's AUTH-403; a reporter who left
// the project keeps cancel/wrap/resume-blocked; no refusal writes state.
//
// The file is self-contained against the pre-change tree (it uses only
// helpers that already exist there), so the external oracle can run it
// red on the base and green on the branch.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// wiaNorm blanks the echoed reference so the existing and the missing
// answer compare byte-for-byte.
func wiaNorm(body []byte, ref string) string { return strings.ReplaceAll(string(body), ref, "<ref>") }

// wiaConcealed proves one route's answer for an existing work item is
// byte-identical (modulo the echoed reference) to its answer for a
// missing reference: 404 CORE-404, no work-item state or holder data.
func wiaConcealed(t *testing.T, e *srvEnv, key, ref, missing, name, method, suffix string, body any) {
	t.Helper()
	sm, bm := e.do(t, method, "/api/v1/work-items/"+missing+suffix, key, body, nil)
	if sm != 404 || !strings.Contains(string(bm), api.ErrCoreNotFound) {
		t.Errorf("%s: missing reference answered %d %s, want 404 %s", name, sm, bm, api.ErrCoreNotFound)
	}
	se, be := e.do(t, method, "/api/v1/work-items/"+ref+suffix, key, body, nil)
	if se != sm || wiaNorm(be, ref) != wiaNorm(bm, missing) {
		t.Errorf("%s: existing answered %d %s; missing answered %d %s", name, se, be, sm, bm)
	}
	for _, leak := range []string{`"session_fingerprint"`, `"holder_machine"`} {
		if strings.Contains(string(be), leak) {
			t.Errorf("%s: concealed answer leaked %s: %s", name, leak, be)
		}
	}
}

// wiaEventCount answers the WI's event count straight from the store.
func wiaEventCount(t *testing.T, e *srvEnv, wiID string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM wi_events WHERE work_item_id=?1`, wiID).Scan(&n); err != nil {
		t.Fatalf("count events of %s: %v", wiID, err)
	}
	return n
}

// TestWorkItemConcealmentMatrix: a non-member's answer for an existing
// work item equals the route's answer for a missing reference on every
// non-session route, and a key scoped to another project is concealed
// the same way while its own project routes stay unchanged.
func TestWorkItemConcealmentMatrix(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74a concealment matrix")
	_, outKey := memberUser(t, e, "w74a-nonmember", "")
	missing := "wi_w74a_missing"
	sid := strings.Repeat("a1", 16)
	body := func(v any) any { return v }
	routes := []struct {
		name, method, suffix string
		body                 any
	}{
		{"get", "GET", "", nil},
		{"patch", "PATCH", "", body(map[string]any{"goal": "x"})},
		{"cancel", "POST", "/cancel", body(map[string]any{})},
		{"wrap", "POST", "/wrap", body(map[string]any{})},
		{"claim", "POST", "/claim", body(map[string]any{"session_id": sid, "machine_id": ""})},
		{"force-end-session", "POST", "/force-end-session", body(map[string]any{"reason": "probe"})},
		{"resume-blocked", "POST", "/resume-blocked", body(map[string]any{})},
		{"dependencies-get", "GET", "/dependencies", nil},
		{"dependencies-add", "POST", "/dependencies", body(map[string]any{"blocking_wi_id": "wi_w74a_blocker", "kind": "blocks"})},
		{"dependencies-delete", "DELETE", "/dependencies/wi_w74a_blocker/blocks", nil},
	}
	for _, r := range routes {
		wiaConcealed(t, e, outKey, id, missing, r.name, r.method, r.suffix, r.body)
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusQueued {
		t.Fatalf("concealment probes changed the work item: %d %s %+v", code, raw, wi)
	}

	// A key scoped to ANOTHER project is concealed on the work item even
	// though its user is a project member; its own project routes stay
	// unchanged.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w74aother"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	var otherID string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='w74aother'`).Scan(&otherID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	scopedUser, _ := memberUser(t, e, "w74a-scoped", "writer")
	// The scoped user is a member of its key's own project too (the e2e
	// shape): the scope, not the membership, is what conceals alpha.
	if _, err := e.admin.PatchProject("w74aother", core.ProjectPatchRequest{
		Members: &[]api.ProjectMember{{UserID: scopedUser, Role: "writer"}},
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	scoped, err := e.admin.CreateAPIKey(scopedUser, "w74a-scoped", otherID)
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}
	for _, r := range routes {
		wiaConcealed(t, e, scoped.Plain, id, missing, "scoped-"+r.name, r.method, r.suffix, r.body)
	}
	if code, raw := e.do(t, "GET", "/api/v1/projects/w74aother", scoped.Plain, nil, nil); code != 200 {
		t.Fatalf("scoped key lost its own project route: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items?project=w74aother", scoped.Plain, nil, nil); code != 200 {
		t.Fatalf("scoped key lost its own project list: %d %s", code, raw)
	}
}

// TestWorkItemRoleGates: a member keeps the route answers — a viewer
// reads but every write is the route's AUTH-403; a writer who is not
// the reporter cannot cancel; a writer patches. The scoped-key write is
// concealed even for a member.
func TestWorkItemRoleGates(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74a role gates")
	_, readerKey := memberUser(t, e, "w74a-reader", "viewer")
	_, writerKey := memberUser(t, e, "w74a-writer", "writer")

	// Viewer: reads pass, writes are the route's AUTH-403, not concealment.
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, readerKey, nil, nil); code != 200 {
		t.Fatalf("viewer read: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id+"/dependencies", readerKey, nil, nil); code != 200 {
		t.Fatalf("viewer deps read: %d %s", code, raw)
	}
	for _, r := range []struct {
		name, method, suffix string
		body                 any
	}{
		{"patch", "PATCH", "", map[string]any{"goal": "x"}},
		{"claim", "POST", "/claim", map[string]any{"session_id": strings.Repeat("b2", 16), "machine_id": ""}},
		{"dependencies-add", "POST", "/dependencies", map[string]any{"blocking_wi_id": "wi_w74a_blocker", "kind": "blocks"}},
		{"cancel", "POST", "/cancel", map[string]any{}},
	} {
		code, raw := e.do(t, r.method, "/api/v1/work-items/"+id+r.suffix, readerKey, r.body, nil)
		if code != 403 {
			t.Fatalf("viewer %s: %d %s, want the route's 403", r.name, code, raw)
		}
		if c, _ := errEnvelope(t, raw); c != api.ErrAuthForbidden {
			t.Fatalf("viewer %s code: %s", r.name, raw)
		}
	}
	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusQueued {
		t.Fatalf("refused viewer writes changed the work item: %d %s %+v", code, raw, wi)
	}

	// Writer who is not the reporter: cancel is refused, PATCH passes.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/cancel", writerKey, map[string]any{}, nil); code != 403 {
		t.Fatalf("writer cancel: %d %s", code, raw)
	} else if c, _ := errEnvelope(t, raw); c != api.ErrAuthForbidden {
		t.Fatalf("writer cancel code: %s", raw)
	}
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, writerKey,
		map[string]any{"goal": "writer edit"}, nil); code != 200 {
		t.Fatalf("writer patch: %d %s", code, raw)
	}

	// The scoped-key write is concealed even for a member: the same
	// missing-work-item answer as an unknown reference.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w74a-scope"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	var scopeID string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='w74a-scope'`).Scan(&scopeID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	writerID, _ := memberUser(t, e, "w74a-writer-scoped", "writer")
	scoped, err := e.admin.CreateAPIKey(writerID, "w74a-scopekey", scopeID)
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}
	wiaConcealed(t, e, scoped.Plain, id, "wi_w74a_missing", "scoped-write", "PATCH", "", map[string]any{"goal": "x"})
}

// TestRefusedWorkItemRequestLeavesState: every refused non-session route
// answers the missing-work-item 404 and leaves a running work item's
// status, session row and event timeline untouched.
func TestRefusedWorkItemRequestLeavesState(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w74a refused state")
	secret := strings.Repeat("c3", 16)
	if code, raw, _ := sessClaim(t, e, id, secret, "w74a-rs"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	events := wiaEventCount(t, e, id)
	var hash, fp, machine string
	if err := e.db.QueryRow(`SELECT COALESCE(session_hash,''), COALESCE(session_fingerprint,''), COALESCE(session_machine,'')
		FROM work_items WHERE id=?1`, id).Scan(&hash, &fp, &machine); err != nil {
		t.Fatalf("session row: %v", err)
	}

	_, outKey := memberUser(t, e, "w74a-refused", "")
	missing := "wi_w74a_missing_refused"
	for _, r := range []struct {
		name, method, suffix string
		body                 any
	}{
		{"patch", "PATCH", "", map[string]any{"goal": "x"}},
		{"cancel", "POST", "/cancel", map[string]any{}},
		{"wrap", "POST", "/wrap", map[string]any{}},
		{"claim", "POST", "/claim", map[string]any{"session_id": strings.Repeat("d4", 16), "machine_id": ""}},
		{"force-end-session", "POST", "/force-end-session", map[string]any{"reason": "probe"}},
		{"resume-blocked", "POST", "/resume-blocked", map[string]any{}},
		{"dependencies-add", "POST", "/dependencies", map[string]any{"blocking_wi_id": "wi_w74a_blocker", "kind": "blocks"}},
		{"dependencies-delete", "DELETE", "/dependencies/wi_w74a_blocker/blocks", nil},
	} {
		wiaConcealed(t, e, outKey, id, missing, r.name, r.method, r.suffix, r.body)
	}

	var wi struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Status != api.StatusRunning {
		t.Fatalf("refused probes changed the status: %d %s %+v", code, raw, wi)
	}
	var h2, f2, m2 string
	if err := e.db.QueryRow(`SELECT COALESCE(session_hash,''), COALESCE(session_fingerprint,''), COALESCE(session_machine,'')
		FROM work_items WHERE id=?1`, id).Scan(&h2, &f2, &m2); err != nil {
		t.Fatalf("session row after: %v", err)
	}
	if h2 != hash || f2 != fp || m2 != machine {
		t.Fatalf("refused probes changed the session row: %q/%q/%q -> %q/%q/%q", hash, fp, machine, h2, f2, m2)
	}
	if got := wiaEventCount(t, e, id); got != events {
		t.Fatalf("refused probes wrote events: %d -> %d", events, got)
	}
}

// TestReporterRetainedAfterMembershipLoss: a reporter who left the
// project keeps cancel, wrap and resume-blocked; an unrelated non-member
// gets the byte-identical missing-work-item answer on the same doors.
func TestReporterRetainedAfterMembershipLoss(t *testing.T) {
	e := newSrvEnv(t)
	repID, repKey := memberUser(t, e, "w74a-reporter", "writer")

	// The reporter files two plain tickets and one execution ticket
	// while still a member.
	cancelID := createPlainWI(t, e, repKey, "w74a departed cancel")
	wrapID := createPlainWI(t, e, repKey, "w74a departed wrap")
	var exec struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", repKey,
		map[string]any{"project": "alpha", "goal": "w74a departed resume", "bundle_name": "demo-bundle"}, &exec); code != 201 || exec.ID == "" {
		t.Fatalf("exec create: %d %s", code, raw)
	}
	// A blocked exit the reporter must be able to continue.
	if code, raw, _ := sessClaim(t, e, exec.ID, strings.Repeat("e5", 16), "w74a-dep"); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+exec.ID+"/session-end", e.key,
		map[string]string{"session_id": strings.Repeat("e5", 16), "status": "blocked"}, nil); code != 200 {
		t.Fatalf("block: %d %s", code, raw)
	}
	// The reporter leaves the project.
	if _, err := e.admin.PatchProject("alpha", core.ProjectPatchRequest{
		RemoveMembers: &[]string{repID},
	}); err != nil {
		t.Fatalf("remove membership: %v", err)
	}

	var got struct {
		Status string `json:"status"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+cancelID+"/cancel", repKey, map[string]any{}, &got); code != 200 || got.Status != api.StatusCanceled {
		t.Fatalf("departed reporter cancel: %d %s %s", code, raw, got.Status)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wrapID+"/wrap", repKey, map[string]any{}, &got); code != 200 || got.Status != api.StatusWrapped {
		t.Fatalf("departed reporter wrap: %d %s %s", code, raw, got.Status)
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+exec.ID+"/resume-blocked", repKey, map[string]any{}, &got); code != 200 || got.Status != api.StatusQueued {
		t.Fatalf("departed reporter resume-blocked: %d %s %s", code, raw, got.Status)
	}

	// An unrelated non-member is concealed on the same three doors —
	// byte-identical to a missing reference.
	_, outKey := memberUser(t, e, "w74a-unrelated", "")
	stranger := createPlainWI(t, e, e.key, "w74a unrelated target")
	for _, r := range []struct {
		name, suffix string
		body         any
	}{
		{"cancel", "/cancel", map[string]any{}},
		{"wrap", "/wrap", map[string]any{}},
		{"resume-blocked", "/resume-blocked", map[string]any{}},
	} {
		wiaConcealed(t, e, outKey, stranger, "wi_w74a_missing_unrelated", r.name, "POST", r.suffix, r.body)
	}
}
