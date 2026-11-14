package server

// w30_deactivated_readonly_test.go — AC2/AC5 (polyforge#30): a
// deactivated project is READ-ONLY on the server. Every write that does
// work on it — creating or modifying work items, comments, submits and
// claims (the auto-claim rides the same claim door), resume execution,
// the terminal verdicts, the recovery door, dependency edges, the
// machine face's attrs write and memory writes — answers CORE-403 with
// the typed PROJECT_DEACTIVATED reason, BEFORE any state changes; every
// read keeps answering under the existing permissions (a non-member's
// read stays the concealed CORE-404); and once the project is restored
// the same operations are accepted again immediately (AC5).
//
// The deactivated project carries one real wrapped work item (its
// readable history) and one SQL-seeded queued work item — the state the
// 409 deactivation rule makes unreachable through the API alone, seeded
// directly so the claim/verdict doors have a live target to be refused
// on.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// w30FrozenEnv assembles the deactivated fixture: project "frozen" with
// a wrapped execution ticket, a memory and a comment (its history), then
// deactivated through the real door, then a queued work item seeded
// through SQL (the only way it can exist — AC3's 409 keeps the API from
// creating one). It answers the env, the admin key, the queued work
// item's id, the wrapped work item's id and the seeded memory's id.
func w30FrozenEnv(t *testing.T) (*srvEnv, string, string, string) {
	t.Helper()
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "frozen"}); err != nil {
		t.Fatalf("create frozen: %v", err)
	}
	// A real wrapped history: create, claim, comment, end.
	var created map[string]any
	code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "frozen", "goal": "frozen history", "bundle_name": "demo-bundle"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create history wi: %d %s", code, body)
	}
	wrapped, _ := created["id"].(string)
	sess := "w30-" + wrapped
	code, body = e.do(t, "POST", "/api/v1/work-items/"+wrapped+"/claim", e.key,
		map[string]string{"session_id": sess, "machine_id": "m-w30-hist"}, nil)
	if code != http.StatusOK {
		t.Fatalf("claim history wi: %d %s", code, body)
	}
	code, body = e.do(t, "POST", "/api/v1/work-items/"+wrapped+"/comments", e.key,
		map[string]string{"body": "frozen history comment"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("comment history wi: %d %s", code, body)
	}
	code, body = e.do(t, "POST", "/api/v1/work-items/"+wrapped+"/session-end", e.key,
		map[string]string{"session_id": sess, "status": "wrapped"}, nil)
	if code != http.StatusOK {
		t.Fatalf("end history wi: %d %s", code, body)
	}
	var mem map[string]any
	code, body = e.do(t, "POST", "/api/v1/memories", e.key,
		map[string]any{"project": "frozen", "type": "fact.note", "title": "frozen memory",
			"content": "frozen body", "tags": []string{"w30"}, "visibility": "project"}, &mem)
	if code != http.StatusCreated {
		t.Fatalf("create memory: %d %s", code, body)
	}
	memID, _ := mem["id"].(string)
	// Deactivate through the real door (every work item is terminal).
	code, body = e.do(t, "POST", "/api/v1/projects/frozen/deactivate", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("deactivate frozen: %d %s", code, body)
	}
	// The queued target the API cannot create anymore.
	frozen, err := e.admin.GetProject("frozen")
	if err != nil {
		t.Fatal(err)
	}
	queued := "wi_w30_frozen"
	if _, err := e.db.Exec(`INSERT INTO work_items(id,seq,project_id,goal,reporter,bundle_name,created_at,updated_at)
		VALUES(?1,2,?2,'w30 readonly target',?3,'demo-bundle',?4,?4)`,
		queued, frozen.ID, e.adminID, core.Now()); err != nil {
		t.Fatalf("seed queued wi: %v", err)
	}
	return e, queued, wrapped, memID
}

// w30AssertDeactivated asserts one refusal is the typed PROJECT_DEACTIVATED
// CORE-403 naming the project.
func w30AssertDeactivated(t *testing.T, door string, code int, body []byte) {
	t.Helper()
	if code != http.StatusForbidden {
		t.Fatalf("%s: %d %s, want 403", door, code, body)
	}
	var refusal struct {
		Code  string          `json:"code"`
		Detail string         `json:"detail"`
		Extra json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(body, &refusal); err != nil {
		t.Fatalf("%s: decode refusal: %v (%s)", door, err, body)
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(refusal.Extra, &extra)
	if refusal.Code != api.ErrCoreForbidden || extra.Reason != "PROJECT_DEACTIVATED" {
		t.Fatalf("%s: refusal %s/%s, want CORE-403 PROJECT_DEACTIVATED", door, refusal.Code, extra.Reason)
	}
	if !strings.Contains(refusal.Detail, "deactivated") || !strings.Contains(refusal.Detail, "frozen") {
		t.Fatalf("%s: detail %q does not name the deactivated project", door, refusal.Detail)
	}
}

// TestW30DeactivatedReadOnlyMatrix: every write door of a deactivated
// project answers the typed refusal and changes nothing; every read keeps
// answering; restoring the project makes the same operations work
// again at once (AC5).
func TestW30DeactivatedReadOnlyMatrix(t *testing.T) {
	e, queued, wrapped, memID := w30FrozenEnv(t)

	type door struct {
		name   string
		method string
		path   string
		body   any
	}
	sess := "w30-readonly-sess"
	doors := []door{
		{"create wi", "POST", "/api/v1/work-items", map[string]string{"project": "frozen", "goal": "nope"}},
		{"patch wi", "PATCH", "/api/v1/work-items/" + queued, map[string]string{"priority": "high"}},
		{"comment", "POST", "/api/v1/work-items/" + wrapped + "/comments", map[string]string{"body": "nope"}},
		{"claim", "POST", "/api/v1/work-items/" + queued + "/claim", map[string]string{"session_id": sess}},
		{"attach", "POST", "/api/v1/work-items/" + queued + "/session-attach", map[string]string{"session_id": sess}},
		{"session-end", "POST", "/api/v1/work-items/" + queued + "/session-end", map[string]string{"session_id": sess, "status": "wrapped"}},
		{"cancel", "POST", "/api/v1/work-items/" + queued + "/cancel", nil},
		{"wrap", "POST", "/api/v1/work-items/" + queued + "/wrap", nil},
		{"force-end", "POST", "/api/v1/work-items/" + queued + "/force-end-session", map[string]string{"reason": "nope"}},
		{"resume-blocked", "POST", "/api/v1/work-items/" + queued + "/resume-blocked", nil},
		{"add dep", "POST", "/api/v1/work-items/" + queued + "/dependencies", map[string]string{"blocking_wi_id": wrapped, "kind": "related"}},
		{"del dep", "DELETE", "/api/v1/work-items/" + queued + "/dependencies/" + wrapped + "/related", nil},
		{"create memory", "POST", "/api/v1/memories", map[string]any{"project": "frozen", "type": "fact.note", "title": "nope", "content": "nope", "visibility": "project"}},
		{"redact memory", "POST", "/api/v1/memories/" + memID + "/redact", nil},
	}
	for _, d := range doors {
		code, body := e.do(t, d.method, d.path, e.key, d.body, nil)
		w30AssertDeactivated(t, d.name, code, body)
	}
	// The machine face's attrs write, signed like the daemon's: the same
	// typed refusal.
	priv, _ := signedMachineFor(t, e, "m_w30_ro", e.adminID)
	code, body := e.doSigned(t, "PATCH", "/api/v1/work-items/"+queued+"/attrs",
		e.key, "m_w30_ro", priv, 0, map[string]any{"key": "x", "value": "y"})
	w30AssertDeactivated(t, "machine attrs", code, body)

	// Nothing changed: the target is still queued and unheld.
	var view struct {
		Status string `json:"status"`
	}
	if code, body := e.do(t, "GET", "/api/v1/work-items/"+queued, e.key, nil, &view); code != http.StatusOK || view.Status != api.StatusQueued {
		t.Fatalf("the refused doors changed the work item: %d %s (%q)", code, body, view.Status)
	}

	// The reads keep answering under the existing permissions.
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if code, body := e.do(t, "GET", "/api/v1/work-items?project=frozen", e.key, nil, &list); code != http.StatusOK || len(list.Items) != 2 {
		t.Fatalf("list while deactivated: %d %s (%d items), want the two rows", code, body, len(list.Items))
	}
	if code, body := e.do(t, "GET", "/api/v1/work-items/"+wrapped+"?include=events", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("detail+events while deactivated: %d %s", code, body)
	}
	if code, body := e.do(t, "GET", "/api/v1/projects/frozen", e.key, nil, nil); code != http.StatusOK || !strings.Contains(string(body), `"status":"deactivated"`) {
		t.Fatalf("project read while deactivated: %d %s", code, body)
	}
	var hits struct {
		Items []map[string]any `json:"items"`
	}
	if code, body := e.do(t, "GET", "/api/v1/memories/search?project=frozen&q=frozen", e.key, nil, &hits); code != http.StatusOK || len(hits.Items) == 0 {
		t.Fatalf("memory search while deactivated: %d %s (%d hits)", code, body, len(hits.Items))
	}
	if code, body := e.do(t, "GET", "/api/v1/memories/"+memID, e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("memory read while deactivated: %d %s", code, body)
	}
	// The permission boundary is unchanged: a non-member's read stays the
	// concealed CORE-404, never the deactivation answer.
	okey := w30NonMemberKey(t, e)
	if code, _ := e.do(t, "GET", "/api/v1/work-items/"+wrapped, okey, nil, nil); code != http.StatusNotFound {
		t.Fatalf("a non-member's read of a deactivated project answers %d, want the concealed 404", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/work-items", okey,
		map[string]string{"project": "frozen", "goal": "nope"}, nil); code != http.StatusForbidden {
		t.Fatalf("a non-member's write of a deactivated project answers %d, want the membership 403 first", code)
	}

	// Restore: the same operations are accepted again immediately (AC5).
	if code, body := e.do(t, "POST", "/api/v1/projects/frozen/restore", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("restore frozen: %d %s", code, body)
	}
	var again map[string]any
	if code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "frozen", "goal": "w30 after restore"}, &again); code != http.StatusCreated {
		t.Fatalf("create after restore: %d %s, want 201", code, body)
	}
	if code, body := e.do(t, "POST", "/api/v1/memories", e.key,
		map[string]any{"project": "frozen", "type": "fact.note", "title": "w30 after restore",
			"content": "ok", "visibility": "project"}, nil); code != http.StatusCreated {
		t.Fatalf("memory create after restore: %d %s, want 201", code, body)
	}
}

// w30NonMemberKey creates a user outside every project and answers its
// api key.
func w30NonMemberKey(t *testing.T, e *srvEnv) string {
	t.Helper()
	u, err := e.admin.CreateUser("w30-outsider", "w30-outsider@w30.invalid", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := e.admin.CreateAPIKey(u.ID, "w30-outsider", "")
	if err != nil {
		t.Fatal(err)
	}
	return issued.Plain
}
