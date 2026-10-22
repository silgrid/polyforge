package test

// project_delete_acceptance_test.go: the e2e
// acceptance suite — the project hard delete (DELETE
// /api/v1/projects/{id}, `pf projects delete`, the admin_audit table and
// the full cascade) — authored against the capsule's
// artifacts/e2e-scenarios.md (which mirrors spec.md's Acceptance
// criteria and Scenario blocks) BEFORE the delete lands. Every
// assertion below is an assertion-level RED on the pre-implementation
// tree (the route and the verb do not exist: a raw DELETE answers 404
// with Go's plain-text page, `pf projects delete` is the unknown-action
// refusal) and turns green only on the delivered feature. Driven
// through the project's real e2e surfaces only — the REAL aihub + pf
// binaries (newV1Install), real HTTP, the real `pf` CLI, real git state
// on disk, and the running aihub's on-disk aihub.db (the s24
// direct-sqlite precedent) — never engine internals.
//
// One scenario per new-behaviour criterion (the full mapping, the
// unscenario'd criteria and the TTY-confirmation document scenario live
// in artifacts/e2e-scenarios.md):
//   - Refusals, each with NOTHING deleted: non-admin key (403
//     AUTH-403), admin key scoped to another project (403 AUTH-403),
//     unknown project (404 CORE-404 typed envelope), non-terminal work
//     items bundled and bundle-less alike (409 CORE-409 listing every
//     offending slug), unrevoked scoped key (409 CORE-409 listing key
//     ids).
//   - The delete: full cascade (every project-owned row + the per-WI
//     bare git repos gone, zero orphans, unscoped keys intact),
//     idempotent replay, one admin_audit row per performed deletion.
//   - The CLI: --yes deletes without prompting, non-TTY without --yes
//     refuses naming the escape hatch with no write, typed server
//     errors render through the shared typed envelope (--json prints
//     the raw response).
//   - Docs/help: README admin section, docs/user-guide.md and
//     `pf help projects` cover the verb + the backup-first instruction;
//     the design route table gains the DELETE row.
//
// The interactive TTY confirmation (typed project name) is a
// document-only scenario: this suite has no pty harness, and a piped
// stdin is by spec a refusal, so the prompt flow is driven by hand on a
// real terminal and judged against the same spec.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Surfaces: raw HTTP against the delete route, the real pf CLI, sqlite.
// ---------------------------------------------------------------------------

// projDelDeleteProject drives DELETE /api/v1/projects/{id-or-name} over real
// HTTP under the given bearer key and answers status + raw body.
func projDelDeleteProject(t *testing.T, inst *v1Install, key, idOrName string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("DELETE",
		inst.url+"/api/v1/projects/"+url.PathEscape(idOrName), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", idOrName, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("delete %s body: %v", idOrName, err)
	}
	return resp.StatusCode, string(raw)
}

// projDelProjectStatus answers GET /api/v1/projects/{name} under the given
// key (the read face every authenticated key reaches).
func projDelProjectStatus(t *testing.T, inst *v1Install, key, name string) int {
	t.Helper()
	req, err := http.NewRequest("GET",
		inst.url+"/api/v1/projects/"+url.PathEscape(name), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get project %s: %v", name, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// projDelTyped decodes the typed error envelope ({"code","detail"}) from a
// raw body; ok=false when the body is not one (Go's plain-text 404 page).
func projDelTyped(body string) (code, detail string, ok bool) {
	var e struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Code == "" {
		return "", "", false
	}
	return e.Code, e.Detail, true
}

// projDelPf runs the REAL pf binary (the pfExit env shape) and answers
// stdout, stderr and the exit code — the non-TTY stdin face included.
func projDelPf(t *testing.T, inst *v1Install, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url,
		"PF_API_KEY="+inst.adminKey,
		"PF_USER_ID=usr_root",
	)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

// projDelDB opens the RUNNING aihub's on-disk aihub.db (the s24 precedent:
// WAL with the same busy_timeout pragma).
func projDelDB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite",
		"file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// projDelCount answers one scalar COUNT query.
func projDelCount(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

// projDelAuditText answers every admin_audit row's whole textual projection
// (all columns, space-joined) — the assertion surface when the spec
// names the recorded facts but not the column names.
func projDelAuditText(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM admin_audit`)
	if err != nil {
		t.Fatalf("read admin_audit: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("admin_audit columns: %v", err)
	}
	var text string
	for rows.Next() {
		vals := make([]any, len(cols))
		for i := range vals {
			vals[i] = new(sql.RawBytes)
		}
		if err := rows.Scan(vals...); err != nil {
			t.Fatalf("scan admin_audit: %v", err)
		}
		for _, v := range vals {
			if raw, ok := v.(*sql.RawBytes); ok {
				text += string(*raw) + " "
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read admin_audit: %v", err)
	}
	return text
}

// ---------------------------------------------------------------------------
// Fixtures: projects, work items (terminal via the real claim/wrap
// doors), keys, one memory, the admin actor.
// ---------------------------------------------------------------------------

// projDelProjectID creates one scratch project and answers its id.
func projDelProjectID(t *testing.T, inst *v1Install, name string) string {
	t.Helper()
	raw, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: name, Description: "pd scratch project",
	})
	if err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" {
		t.Fatalf("create project %s answer: %v (%s)", name, err, raw)
	}
	return p.ID
}

// projDelCreateWI files one work item (bundle nil = a bundle-less
// registry/epic ticket) and answers id + slug.
func projDelCreateWI(t *testing.T, inst *v1Install, project, goal string, bundle *string) (string, string) {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: project, Goal: goal, BundleName: bundle,
	})
	if err != nil {
		t.Fatalf("create work item %s: %v", goal, err)
	}
	var wi struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	return wi.ID, wi.Slug
}

// projDelTerminalBundledWI files a bundled execution ticket and drives it to
// the terminal wrapped verdict through the REAL doors — claim (which
// creates the bare repo at <data>/git/<wi>.git) then session-end — so the
// delete's terminal check passes and the cascade has a repo to remove.
func projDelTerminalBundledWI(t *testing.T, inst *v1Install, project, goal string) string {
	t.Helper()
	id, _ := projDelCreateWI(t, inst, project, goal, strPtr("autopilot"))
	sess := "pd-" + id
	if _, err := inst.client.SessionClaim(api.SessionClaimRequest{
		WorkItemID: id, SessionID: sess, MachineID: "m-pd",
	}); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	if err := inst.client.SessionEnd(id, api.SessionEndRequest{
		SessionID: sess, Status: api.StatusWrapped,
	}); err != nil {
		t.Fatalf("session-end %s: %v", id, err)
	}
	return id
}

// projDelWrapBundlelessWI files a bundle-less ticket and wraps it (the
// bundle-less terminal door).
func projDelWrapBundlelessWI(t *testing.T, inst *v1Install, project, goal string) string {
	t.Helper()
	id, _ := projDelCreateWI(t, inst, project, goal, nil)
	if _, err := inst.client.WrapWorkItem(id, api.WrapRequest{}); err != nil {
		t.Fatalf("wrap %s: %v", id, err)
	}
	return id
}

// projDelUserKey mints one user plus API key (role admin|writer, scope the
// project id when non-nil) and answers user id, key id and the plaintext.
func projDelUserKey(t *testing.T, inst *v1Install, name, role string, scope *string) (string, string, string) {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: name + "@pd.invalid",
		UserType: "human", Role: role,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	var user struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == "" {
		t.Fatalf("user answer for %s: %v (%s)", name, err, raw)
	}
	raw, err = inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{
		UserID: user.ID, Name: name, ProjectID: scope,
	})
	if err != nil {
		t.Fatalf("mint key for %s: %v", name, err)
	}
	var key struct {
		Plain string `json:"plain"`
		Token struct {
			ID string `json:"id"`
		} `json:"token"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || key.Plain == "" {
		t.Fatalf("key answer for %s: %v (%s)", name, err, raw)
	}
	return user.ID, key.Token.ID, key.Plain
}

// projDelMemory creates one project memory (row + FTS row) through the wire
// face and answers its id.
func projDelMemoryID(t *testing.T, inst *v1Install, project string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
		"project":    project,
		"type":       "fact.note",
		"visibility": "project",
		"title":      "pd cascade memory",
		"content":    "pd cascade memory body",
	}, &out)
	if err != nil {
		t.Fatalf("create memory in %s: %v", project, err)
	}
	if out.ID == "" {
		t.Fatalf("create memory answer: no id")
	}
	return out.ID
}

// projDelAdminUserID answers the bootstrap admin's user id (the actor the
// admin-key delete records).
func projDelAdminUserID(t *testing.T, inst *v1Install) string {
	t.Helper()
	raw, err := inst.client.ListUsers()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("list users answer: %v (%s)", err, raw)
	}
	for _, u := range list.Items {
		if u.Role == "admin" {
			return u.ID
		}
	}
	t.Fatalf("no admin user in %s", raw)
	return ""
}

// ---------------------------------------------------------------------------
// Scenarios: refusals.
// ---------------------------------------------------------------------------

// TestProjectDeleteNonAdminRefused: a non-admin key is refused with 403
// AUTH-403 and nothing is deleted.
func TestProjectDeleteNonAdminRefused(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdref"
	projDelProjectID(t, inst, proj)
	_, _, writerPlain := projDelUserKey(t, inst, "pd refused writer", "writer", nil)
	code, body := projDelDeleteProject(t, inst, writerPlain, proj)
	if code != http.StatusForbidden {
		t.Fatalf("non-admin delete %s: status %d body %q, want 403", proj, code, body)
	}
	if c, _, ok := projDelTyped(body); !ok || c != "AUTH-403" {
		t.Fatalf("non-admin delete envelope: %q, want AUTH-403", body)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("403 deleted the project: GET status %d", s)
	}
}

// TestProjectDeleteScopedAdminRefused: an admin key scoped to a different
// project is refused with 403 AUTH-403 (the PATCH scope check) and
// nothing is deleted.
func TestProjectDeleteScopedAdminRefused(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdtgt"
	projDelProjectID(t, inst, proj)
	other := projDelProjectID(t, inst, "pdother")
	_, _, scopedPlain := projDelUserKey(t, inst, "pd scoped admin", "admin", &other)
	code, body := projDelDeleteProject(t, inst, scopedPlain, proj)
	if code != http.StatusForbidden {
		t.Fatalf("scoped-admin delete %s: status %d body %q, want 403", proj, code, body)
	}
	if c, _, ok := projDelTyped(body); !ok || c != "AUTH-403" {
		t.Fatalf("scoped-admin delete envelope: %q, want AUTH-403", body)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("403 deleted the project: GET status %d", s)
	}
}

// TestProjectDeleteUnknownProject: an unknown project id/name answers the
// typed 404 envelope (CORE-404), not a bare page.
func TestProjectDeleteUnknownProject(t *testing.T) {
	inst := newV1Install(t)
	code, body := projDelDeleteProject(t, inst, inst.adminKey, "pdnever")
	if code != http.StatusNotFound {
		t.Fatalf("unknown-project delete: status %d body %q, want 404", code, body)
	}
	if c, _, ok := projDelTyped(body); !ok || c != "CORE-404" {
		t.Fatalf("unknown-project delete envelope: %q, want the typed CORE-404", body)
	}
}

// TestProjectDeleteBlockedByNonTerminal: every non-terminal work item —
// bundled execution tickets AND bundle-less registry/epic tickets alike
// — blocks the delete with 409 CORE-409 listing every offending slug,
// and nothing is deleted.
func TestProjectDeleteBlockedByNonTerminal(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdblk"
	projDelProjectID(t, inst, proj)
	queuedID, queuedSlug := projDelCreateWI(t, inst, proj, "queued bundled ticket", strPtr("autopilot"))
	regID, regSlug := projDelCreateWI(t, inst, proj, "queued registry ticket", nil)
	code, body := projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusConflict {
		t.Fatalf("non-terminal delete %s: status %d body %q, want 409", proj, code, body)
	}
	c, detail, ok := projDelTyped(body)
	if !ok || c != "CORE-409" {
		t.Fatalf("non-terminal delete envelope: %q, want CORE-409", body)
	}
	for _, slug := range []string{queuedSlug, regSlug} {
		if !strings.Contains(detail, slug) {
			t.Fatalf("CORE-409 detail %q does not list the offending slug %q", detail, slug)
		}
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("409 deleted the project: GET status %d", s)
	}
	for _, id := range []string{queuedID, regID} {
		if _, err := inst.client.GetWorkItem(id); err != nil {
			t.Fatalf("409 removed work item %s: %v", id, err)
		}
	}
}

// TestProjectDeleteBlockedByScopedKey: an unrevoked key scoped to the project
// blocks the delete with 409 CORE-409 listing the key id, and nothing is
// deleted (no key, no row).
func TestProjectDeleteBlockedByScopedKey(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdscope"
	projID := projDelProjectID(t, inst, proj)
	projDelTerminalBundledWI(t, inst, proj, "all-terminal so only the key check can refuse")
	_, keyID, scopedPlain := projDelUserKey(t, inst, "pd blocking scoped writer", "writer", &projID)
	code, body := projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusConflict {
		t.Fatalf("scoped-key delete %s: status %d body %q, want 409", proj, code, body)
	}
	c, detail, ok := projDelTyped(body)
	if !ok || c != "CORE-409" {
		t.Fatalf("scoped-key delete envelope: %q, want CORE-409", body)
	}
	if !strings.Contains(detail, keyID) {
		t.Fatalf("CORE-409 detail %q does not list the unrevoked key id %q", detail, keyID)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("409 deleted the project: GET status %d", s)
	}
	// The key itself is untouched and still authenticates: the GET
	// must not answer 401 (a revoked or removed key would). 404 is this
	// caller's correct answer — a non-member's read of an object it
	// cannot see 404-folds (the key scope narrows, never grants; owner
	// rebaseline u_5dFjeaMZ, scoped-key-get-probe).
	if s := projDelProjectStatus(t, inst, scopedPlain, proj); s == http.StatusUnauthorized {
		t.Fatalf("409 revoked or removed the scoped key: GET status %d", s)
	}
}

// ---------------------------------------------------------------------------
// Scenarios: the delete itself.
// ---------------------------------------------------------------------------

// TestProjectDeleteFullCascade: a delete that passes the checks removes
// every project-owned row — the project (incl. its repos JSON), work
// items, events, dependency edges, memories + FTS rows, memberships, the
// revoked scoped key — and the per-WI bare git repos from disk, leaving
// zero orphans while the unscoped admin key and sibling projects
// survive.
func TestProjectDeleteFullCascade(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdcascade"
	projID := projDelProjectID(t, inst, proj)
	wiT := projDelTerminalBundledWI(t, inst, proj, "cascade terminal bundled ticket")
	wiR := projDelWrapBundlelessWI(t, inst, proj, "cascade terminal registry ticket")
	memID := projDelMemoryID(t, inst, proj)
	if _, err := inst.client.AddBlocksDependency(wiT, wiR); err != nil {
		t.Fatalf("dependency edge: %v", err)
	}
	writerID, _, _ := projDelUserKey(t, inst, "pd cascade writer", "writer", nil)
	if _, err := inst.client.PatchProject(proj, aihubclient.ProjectPatchRequest{
		Members: &[]api.ProjectMember{{UserID: writerID, Role: "writer"}},
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	_, revokedKeyID, _ := projDelUserKey(t, inst, "pd cascade revoked scoped", "writer", &projID)
	if _, err := inst.client.RevokeApiKey(revokedKeyID); err != nil {
		t.Fatalf("revoke scoped key: %v", err)
	}

	code, body := projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusOK {
		t.Fatalf("cascade delete %s: status %d body %q, want 200", proj, code, body)
	}
	if !strings.Contains(body, proj) {
		t.Fatalf("delete answer does not identify the deleted project: %q", body)
	}

	db := projDelDB(t, inst)
	for _, check := range []struct {
		name string
		q    string
		args []any
	}{
		{"the project row (incl. its repos JSON)",
			`SELECT COUNT(*) FROM projects WHERE id=?1 OR name=?1`, []any{proj}},
		{"work_items rows", `SELECT COUNT(*) FROM work_items WHERE project_id=?1`, []any{projID}},
		{"wi_events rows",
			`SELECT COUNT(*) FROM wi_events WHERE work_item_id IN (?1,?2)`, []any{wiT, wiR}},
		{"wi_dependencies edges",
			`SELECT COUNT(*) FROM wi_dependencies WHERE blocking_wi_id IN (?1,?2) OR blocked_wi_id IN (?1,?2)`,
			[]any{wiT, wiR}},
		{"memories rows", `SELECT COUNT(*) FROM memories WHERE project_id=?1`, []any{projID}},
		{"memory_fts rows", `SELECT COUNT(*) FROM memory_fts WHERE mem_id=?1`, []any{memID}},
		{"project_members rows", `SELECT COUNT(*) FROM project_members WHERE project_id=?1`, []any{projID}},
		{"the revoked scoped api_keys row", `SELECT COUNT(*) FROM api_keys WHERE id=?1`, []any{revokedKeyID}},
	} {
		if n := projDelCount(t, db, check.q, check.args...); n != 0 {
			t.Fatalf("the cascade left %s: %d remain", check.name, n)
		}
	}
	// Only project-scoped rows go: the unscoped admin key survives.
	sum := sha256.Sum256([]byte(inst.adminKey))
	if n := projDelCount(t, db, `SELECT COUNT(*) FROM api_keys WHERE key_hash=?1`,
		hex.EncodeToString(sum[:])); n != 1 {
		t.Fatalf("the unscoped admin key row: %d remain, want 1", n)
	}
	// The per-WI bare git repo is gone from disk.
	if _, err := os.Stat(filepath.Join(inst.dataDir, "git", wiT+".git")); !os.IsNotExist(err) {
		t.Fatalf("the bare repo %s.git is still on disk (stat err %v)", wiT, err)
	}
	// No over-deletion: the install's own sibling project survives.
	if s := projDelProjectStatus(t, inst, inst.adminKey, v1Project); s != http.StatusOK {
		t.Fatalf("sibling project %s: GET status %d, want 200", v1Project, s)
	}
}

// TestProjectDeleteReplayConverges: a delete requested again after a
// completed (or interrupted and re-run) delete converges to the same end
// state — the typed unknown-project refusal, nothing resurrected, no
// second audit row.
func TestProjectDeleteReplayConverges(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdreplay"
	projID := projDelProjectID(t, inst, proj)
	wiT := projDelTerminalBundledWI(t, inst, proj, "replay terminal ticket")
	code, body := projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusOK {
		t.Fatalf("delete #1 %s: status %d body %q, want 200", proj, code, body)
	}
	code, body = projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusNotFound {
		t.Fatalf("replay delete %s: status %d body %q, want 404", proj, code, body)
	}
	if c, _, ok := projDelTyped(body); !ok || c != "CORE-404" {
		t.Fatalf("replay delete envelope: %q, want the typed CORE-404", body)
	}
	db := projDelDB(t, inst)
	if n := projDelCount(t, db, `SELECT COUNT(*) FROM projects WHERE id=?1 OR name=?1`, proj); n != 0 {
		t.Fatalf("the replay resurrected the project: %d rows", n)
	}
	if n := projDelCount(t, db, `SELECT COUNT(*) FROM work_items WHERE project_id=?1`, projID); n != 0 {
		t.Fatalf("the replay resurrected work items: %d rows", n)
	}
	if n := projDelCount(t, db, `SELECT COUNT(*) FROM admin_audit`); n != 1 {
		t.Fatalf("admin_audit rows after the replay: %d, want 1 (only performed deletions)", n)
	}
	if _, err := os.Stat(filepath.Join(inst.dataDir, "git", wiT+".git")); !os.IsNotExist(err) {
		t.Fatalf("the replay resurrected the bare repo %s.git (stat err %v)", wiT, err)
	}
}

// TestProjectDeleteAuditRow: each deletion writes exactly one admin_audit
// row — the actor, and the deleted project's id and name — that survives
// the cascade (the table has no FK to projects).
func TestProjectDeleteAuditRow(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdaudit"
	projID := projDelProjectID(t, inst, proj)
	projDelTerminalBundledWI(t, inst, proj, "audit terminal ticket")
	code, body := projDelDeleteProject(t, inst, inst.adminKey, proj)
	if code != http.StatusOK {
		t.Fatalf("audit delete %s: status %d body %q, want 200", proj, code, body)
	}
	db := projDelDB(t, inst)
	if n := projDelCount(t, db, `SELECT COUNT(*) FROM admin_audit`); n != 1 {
		t.Fatalf("admin_audit rows after one delete: %d, want 1", n)
	}
	// The row records the actor and the deleted project's id and name
	// (the column names are the implementation's; the row's whole text
	// is the assertion surface).
	text := projDelAuditText(t, db)
	for _, cell := range []string{projDelAdminUserID(t, inst), projID, proj} {
		if !strings.Contains(text, cell) {
			t.Fatalf("the admin_audit row does not record %q: %q", cell, text)
		}
	}
}

// ---------------------------------------------------------------------------
// Scenarios: the CLI.
// ---------------------------------------------------------------------------

// TestProjectDeleteCLIYes: `pf projects delete <id-or-name> --yes` deletes
// without prompting (exit 0, project gone, bare repo gone).
func TestProjectDeleteCLIYes(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdcli"
	projDelProjectID(t, inst, proj)
	wiT := projDelTerminalBundledWI(t, inst, proj, "cli terminal ticket")
	_, se, code := projDelPf(t, inst, "projects", "delete", proj, "--yes")
	if code != 0 {
		t.Fatalf("pf projects delete --yes: exit %d (stderr %q), want 0", code, se)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusNotFound {
		t.Fatalf("pf projects delete --yes left the project: GET status %d", s)
	}
	if _, err := os.Stat(filepath.Join(inst.dataDir, "git", wiT+".git")); !os.IsNotExist(err) {
		t.Fatalf("pf projects delete --yes left the bare repo %s.git (stat err %v)", wiT, err)
	}
}

// TestProjectDeleteCLINonTTYRefuses: without --yes on a non-TTY stdin the
// CLI refuses outright — an error naming the --yes escape hatch, exit 1,
// no write (the project intact, no audit row).
func TestProjectDeleteCLINonTTYRefuses(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdntty"
	projDelProjectID(t, inst, proj)
	projDelTerminalBundledWI(t, inst, proj, "ntty terminal ticket")
	_, se, code := projDelPf(t, inst, "projects", "delete", proj)
	if code != 1 {
		t.Fatalf("non-TTY refusal: exit %d (stderr %q), want 1", code, se)
	}
	// The wording is free; the refusal must name the --yes escape hatch.
	if !strings.Contains(se, "--yes") {
		t.Fatalf("the non-TTY refusal does not name the --yes escape hatch: %q", se)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("the non-TTY refusal deleted the project: GET status %d", s)
	}
	if n := projDelCount(t, projDelDB(t, inst), `SELECT COUNT(*) FROM admin_audit`); n != 0 {
		t.Fatalf("the non-TTY refusal performed a deletion: %d audit rows", n)
	}
}

// TestProjectDeleteCLITypedErrorAndJSON: a typed server refusal renders
// through the shared typed error envelope (exit 1, the code and the
// offending slug prominent), and --json prints the raw response instead.
func TestProjectDeleteCLITypedErrorAndJSON(t *testing.T) {
	inst := newV1Install(t)
	const proj = "pdtyped"
	projDelProjectID(t, inst, proj)
	_, slug := projDelCreateWI(t, inst, proj, "typed-error queued ticket", strPtr("autopilot"))
	_, se, code := projDelPf(t, inst, "projects", "delete", proj, "--yes")
	if code != 1 {
		t.Fatalf("typed refusal: exit %d (stderr %q), want 1", code, se)
	}
	if !strings.Contains(se, "CORE-409") {
		t.Fatalf("the typed refusal stderr lacks the CORE-409 code: %q", se)
	}
	if !strings.Contains(se, slug) {
		t.Fatalf("the typed refusal stderr lacks the offending slug %q: %q", slug, se)
	}
	_, se, code = projDelPf(t, inst, "--json", "projects", "delete", proj, "--yes")
	if code != 1 {
		t.Fatalf("--json typed refusal: exit %d (stderr %q), want 1", code, se)
	}
	var envl struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(se), &envl); err != nil || envl.Code != "CORE-409" {
		t.Fatalf("the --json refusal stderr is not the raw typed envelope: %q", se)
	}
	if s := projDelProjectStatus(t, inst, inst.adminKey, proj); s != http.StatusOK {
		t.Fatalf("the typed refusal deleted the project: GET status %d", s)
	}
}

// ---------------------------------------------------------------------------
// Scenarios: docs and help.
// ---------------------------------------------------------------------------

// TestProjectDeleteDocsCover: `pf help projects`, the README admin
// section and docs/user-guide.md cover the delete verb with the
// backup-first instruction, and the design route table gains the
// DELETE /projects/{id} row (spec-pinned tokens, never wording).
func TestProjectDeleteDocsCover(t *testing.T) {
	inst := newV1Install(t)
	so, _, code := projDelPf(t, inst, "help", "projects")
	if code != 0 {
		t.Fatalf("pf help projects: exit %d", code)
	}
	help := w241Norm(so)
	if !strings.Contains(help, "delete") {
		t.Fatalf("`pf help projects` does not cover the delete verb: %q", so)
	}
	if !strings.Contains(help, "backup") {
		t.Fatalf("`pf help projects` does not carry the backup-first instruction: %q", so)
	}
	for _, doc := range []struct{ name, path string }{
		{"the commands reference", filepath.Join("..", "docs", "reference", "commands.md")},
		{"the user guide", filepath.Join("..", "docs", "user-guide.md")},
	} {
		raw, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("read %s: %v", doc.name, err)
		}
		body := w241Norm(string(raw))
		if !strings.Contains(body, "projects delete") {
			t.Fatalf("%s does not cover pf projects delete", doc.name)
		}
		// The delete pairs with the backup-first instruction (the
		// wording is free; delete and backup sit in one breath).
		if !regexp.MustCompile(`projects delete.{0,400}backup|backup.{0,400}projects delete`).
			MatchString(body) {
			t.Fatalf("%s does not pair the delete with the backup-first instruction", doc.name)
		}
	}
	refRaw, err := os.ReadFile(filepath.Join("..", "docs", "design", "polyforge-vnext", "reference.md"))
	if err != nil {
		t.Fatalf("read the design reference: %v", err)
	}
	routeRow := false
	for _, line := range strings.Split(string(refRaw), "\n") {
		if strings.Contains(line, "DELETE") && strings.Contains(line, "/projects/{id}") {
			routeRow = true
		}
	}
	if !routeRow {
		t.Fatalf("the design route table has no DELETE /projects/{id} row")
	}
}
