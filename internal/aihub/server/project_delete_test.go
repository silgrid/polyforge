package server

// project_delete_test.go — the project hard delete route
// (DELETE /api/v1/projects/{id}) over real HTTP: the
// auth matrix (writer key 403, admin key scoped elsewhere 403, admin key
// scoped to the target 409 — the flagged reading: such a caller must
// revoke its own key first), the typed refusals (404 unknown, 409
// non-terminal listing the slug), the dry run (nothing written, no repo
// removed), the real delete (200 body identifying the project, the
// per-WI bare repo gone from disk, the audit row present, the sibling
// project untouched), and the crash replay (a delete whose repo removal
// never ran is converged by ReplayDeletedRepos, idempotently).

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// deleteFixture answers a srvEnv carrying a second project "doomed"
// (the delete target; alpha is the keeper sibling).
func deleteFixture(t *testing.T) *srvEnv {
	t.Helper()
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "doomed"}); err != nil {
		t.Fatalf("create doomed: %v", err)
	}
	return e
}

// deleteTerminalWI files a bundled execution ticket of the given
// project, drives it terminal through the real doors (claim — which
// creates the bare repo at <data>/git/<wi>.git — then session-end), and
// answers its id.
func deleteTerminalWI(t *testing.T, e *srvEnv, project, goal string) string {
	t.Helper()
	var created map[string]any
	code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": project, "goal": goal, "bundle_name": "autopilot"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create %s wi: %d %s", project, code, body)
	}
	id, _ := created["id"].(string)
	sess := "del-" + id
	code, body = e.do(t, "POST", "/api/v1/work-items/"+id+"/claim", e.key,
		map[string]string{"session_id": sess, "machine_id": "m-del"}, nil)
	if code != http.StatusOK {
		t.Fatalf("claim %s: %d %s", id, code, body)
	}
	code, body = e.do(t, "POST", "/api/v1/work-items/"+id+"/session-end", e.key,
		map[string]string{"session_id": sess, "status": "wrapped"}, nil)
	if code != http.StatusOK {
		t.Fatalf("session-end %s: %d %s", id, code, body)
	}
	return id
}

// deleteWI answers a queued bundled work item's id + slug.
func deleteQueuedWI(t *testing.T, e *srvEnv, project, goal string) (string, string) {
	t.Helper()
	var created struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": project, "goal": goal, "bundle_name": "autopilot"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create %s wi: %d %s", project, code, body)
	}
	return created.ID, created.Slug
}

// envelope answers the typed error envelope of a raw body.
func deleteEnvelope(t *testing.T, raw []byte) (code, detail string) {
	t.Helper()
	var e struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &e); err != nil || e.Code == "" {
		t.Fatalf("body is not the typed envelope: %s", raw)
	}
	return e.Code, e.Detail
}

// TestDeleteProjectAuthMatrix: a writer key and an admin key scoped to
// another project are refused with 403 AUTH-403 (nothing deleted); an
// admin key scoped to the TARGET project is refused by the unrevoked
// scoped-key check — 409 CORE-409 listing its own key id (the flagged
// reading: use an unscoped admin key or revoke the scoped key first).
func TestDeleteProjectAuthMatrix(t *testing.T) {
	e := deleteFixture(t)
	// A writer key.
	writer, err := e.admin.CreateUser("writer", "writer@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("writer user: %v", err)
	}
	writerKey, err := e.admin.CreateAPIKey(writer.ID, "writer key", "")
	if err != nil {
		t.Fatalf("writer key: %v", err)
	}
	code, body := e.do(t, "DELETE", "/api/v1/projects/doomed", writerKey.Plain, nil, nil)
	if code != http.StatusForbidden {
		t.Fatalf("writer delete: %d %s, want 403", code, body)
	}
	if c, _ := deleteEnvelope(t, body); c != api.ErrAuthForbidden {
		t.Fatalf("writer delete envelope: %s", body)
	}
	// An admin key scoped to ANOTHER project.
	admin2, err := e.admin.CreateUser("scoped admin", "scoped@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("scoped admin user: %v", err)
	}
	other, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "elsewhere"})
	if err != nil {
		t.Fatalf("other project: %v", err)
	}
	scopedKey, err := e.admin.CreateAPIKey(admin2.ID, "scoped elsewhere", other.ID)
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}
	code, body = e.do(t, "DELETE", "/api/v1/projects/doomed", scopedKey.Plain, nil, nil)
	if code != http.StatusForbidden {
		t.Fatalf("scoped-admin delete: %d %s, want 403", code, body)
	}
	if c, _ := deleteEnvelope(t, body); c != api.ErrAuthForbidden {
		t.Fatalf("scoped-admin delete envelope: %s", body)
	}
	// Neither refusal deleted the project.
	if code, _ := e.do(t, "GET", "/api/v1/projects/doomed", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("a 403 deleted the project: GET %d", code)
	}
	// An admin key scoped to the TARGET project: the scope check passes,
	// the unrevoked scoped-key check refuses (the caller's own key).
	targetKey, err := e.admin.CreateAPIKey(admin2.ID, "scoped to target", "doomed")
	if err != nil {
		t.Fatalf("target-scoped key: %v", err)
	}
	code, body = e.do(t, "DELETE", "/api/v1/projects/doomed", targetKey.Plain, nil, nil)
	if code != http.StatusConflict {
		t.Fatalf("target-scoped delete: %d %s, want 409", code, body)
	}
	c, detail := deleteEnvelope(t, body)
	if c != api.ErrCoreConflict {
		t.Fatalf("target-scoped delete envelope: %s", body)
	}
	if !strings.Contains(detail, targetKey.Token.ID) {
		t.Fatalf("CORE-409 detail %q does not list the caller's own key id", detail)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/doomed", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("the 409 deleted the project: GET %d", code)
	}
}

// TestDeleteProjectTypedRefusals: an unknown project answers the typed
// 404 CORE-404 envelope; a non-terminal work item answers 409 CORE-409
// listing the offending slug, with nothing deleted.
func TestDeleteProjectTypedRefusals(t *testing.T) {
	e := deleteFixture(t)
	code, body := e.do(t, "DELETE", "/api/v1/projects/never-existed", e.key, nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown delete: %d %s, want 404", code, body)
	}
	if c, _ := deleteEnvelope(t, body); c != api.ErrCoreNotFound {
		t.Fatalf("unknown delete envelope: %s", body)
	}
	queuedID, slug := deleteQueuedWI(t, e, "doomed", "queued blocks the delete")
	code, body = e.do(t, "DELETE", "/api/v1/projects/doomed", e.key, nil, nil)
	if code != http.StatusConflict {
		t.Fatalf("non-terminal delete: %d %s, want 409", code, body)
	}
	c, detail := deleteEnvelope(t, body)
	if c != api.ErrCoreConflict {
		t.Fatalf("non-terminal delete envelope: %s", body)
	}
	if !strings.Contains(detail, slug) {
		t.Fatalf("CORE-409 detail %q does not list the offending slug %q", detail, slug)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/doomed", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("the 409 deleted the project: GET %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/work-items/"+queuedID, e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("the 409 removed the work item: GET %d", code)
	}
}

// TestDeleteProjectDryRunAndCascade: a dry run answers the same body
// with dry_run set and writes nothing (rows and the bare repo stay); the
// real delete answers 200 identifying the project, removes the bare
// repo from disk, writes exactly one audit row, and touches nothing of
// the sibling project.
func TestDeleteProjectDryRunAndCascade(t *testing.T) {
	e := deleteFixture(t)
	wi := deleteTerminalWI(t, e, "doomed", "cascade terminal ticket")
	repo := filepath.Join(e.dataDir, "git", wi+".git")
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("the claim did not create the bare repo %s: %v", repo, err)
	}

	var res api.ProjectDeleteResult
	code, body := e.do(t, "DELETE", "/api/v1/projects/doomed?dry_run=1", e.key, nil, &res)
	if code != http.StatusOK {
		t.Fatalf("dry run: %d %s, want 200", code, body)
	}
	if !res.DryRun || res.Name != "doomed" || res.WorkItems != 1 {
		t.Fatalf("dry-run body: %s", body)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/doomed", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("the dry run deleted the project: GET %d", code)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("the dry run removed the bare repo: %v", err)
	}
	if n := deleteCount(t, e, `SELECT COUNT(*) FROM admin_audit`); n != 0 {
		t.Fatalf("the dry run wrote %d audit rows", n)
	}

	code, body = e.do(t, "DELETE", "/api/v1/projects/doomed", e.key, nil, &res)
	if code != http.StatusOK {
		t.Fatalf("delete: %d %s, want 200", code, body)
	}
	if res.DryRun || res.Name != "doomed" || res.WorkItems != 1 {
		t.Fatalf("delete body: %s", body)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/doomed", e.key, nil, nil); code != http.StatusNotFound {
		t.Fatalf("the delete left the project: GET %d", code)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("the delete left the bare repo %s (stat err %v)", repo, err)
	}
	if n := deleteCount(t, e, `SELECT COUNT(*) FROM admin_audit`); n != 1 {
		t.Fatalf("admin_audit rows after one delete: %d, want 1", n)
	}
	// No over-deletion: the sibling project survives.
	if code, _ := e.do(t, "GET", "/api/v1/projects/alpha", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("the delete touched the sibling project: GET %d", code)
	}
}

// TestDeleteProjectCrashReplay: a delete whose repo removal never ran
// (the crash between commit and removal) is converged by
// ReplayDeletedRepos; a second replay is a no-op.
func TestDeleteProjectCrashReplay(t *testing.T) {
	e := deleteFixture(t)
	wi := deleteTerminalWI(t, e, "doomed", "replay terminal ticket")
	repo := filepath.Join(e.dataDir, "git", wi+".git")
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("the claim did not create the bare repo: %v", err)
	}
	// The crash: the core delete commits, the process dies before the
	// repo removal.
	if _, _, err := e.admin.DeleteProject("doomed", "usr_root", false); err != nil {
		t.Fatalf("core delete: %v", err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("the fixture lost the bare repo before the replay: %v", err)
	}
	e.srv.ReplayDeletedRepos()
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("the replay left the bare repo %s (stat err %v)", repo, err)
	}
	// Idempotent: a second replay is a no-op.
	e.srv.ReplayDeletedRepos()
}

// deleteCount answers one scalar COUNT query against the server's DB.
func deleteCount(t *testing.T, e *srvEnv, q string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(q).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}
