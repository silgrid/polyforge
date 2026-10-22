package core

// admin_delete_test.go — the project hard delete:
// AdminManager.DeleteProject's refusals (404 by id and name, 409 for
// every non-terminal status — bundled AND bundle-less — 409 for an
// unrevoked project-scoped key), the dry run (nothing written, the
// right count), the full cascade over every project-owned table (zero
// orphan references, PRAGMA foreign_key_check empty, the audit row
// present, a sibling project untouched), and the replay convergence
// (a re-run answers 404). The bare-repo removal itself is the server
// face's job (server/project_delete_test.go); DeletedWorkItemIDs is
// pinned here because the startup replay consumes it.

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// deleteEnv is one admin-delete fixture: two projects (alpha the
// keeper, doomed the delete target) plus the admin-delete manager.
func deleteEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newEnv(t)
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "doomed"}); err != nil {
		t.Fatalf("create doomed: %v", err)
	}
	return env
}

// isCode answers whether err is the typed error with the given code.
func isCode(err error, code string) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == code
}

// execWI files one bundled execution ticket in the given project.
func execWI(t *testing.T, env *testEnv, project, goal string) string {
	t.Helper()
	wi, err := env.WIs.Create(CreateRequest{
		Project: project, Goal: goal, Reporter: env.UserID, BundleName: strPtrTest("demo-bundle"),
	})
	if err != nil {
		t.Fatalf("create exec wi in %s: %v", project, err)
	}
	return wi.ID
}

// driveTo drives a bundled execution ticket of the given project into a
// non-terminal status through the real state machine: running is a
// plain claim; paused/blocked add the session-end verdict.
func driveTo(t *testing.T, env *testEnv, project, goal, status string) string {
	t.Helper()
	id := execWI(t, env, project, goal)
	sess := "del-" + id
	if _, err := NewSessionManager(env.DB).Claim(id, api.SessionClaimRequest{
		SessionID: sess, MachineID: "m-del",
	}, env.UserID); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	if status == api.StatusRunning {
		return id
	}
	if err := NewSessionManager(env.DB).End(id, api.SessionEndRequest{
		SessionID: sess, Status: status,
	}, env.UserID); err != nil {
		t.Fatalf("end %s as %s: %v", id, status, err)
	}
	return id
}

// TestDeleteProjectUnknown: an unknown id or name answers CORE-404.
func TestDeleteProjectUnknown(t *testing.T) {
	env := deleteEnv(t)
	for _, ref := range []string{"prj_nope", "never-existed"} {
		_, _, err := env.Admin.DeleteProject(ref, env.UserID, false)
		if !isCode(err, api.ErrCoreNotFound) {
			t.Fatalf("delete %q: %v, want CORE-404", ref, err)
		}
	}
}

// TestDeleteProjectNonTerminalBlocked: every non-terminal status —
// queued, running, paused, blocked, each a bundled execution ticket,
// plus a queued bundle-less registry ticket — blocks the delete with
// CORE-409 listing EVERY offending slug, and nothing is deleted (no
// work item, no project row, no audit row).
func TestDeleteProjectNonTerminalBlocked(t *testing.T) {
	env := deleteEnv(t)
	queued := execWI(t, env, "doomed", "queued bundled ticket")
	running := driveTo(t, env, "doomed", "running bundled ticket", api.StatusRunning)
	paused := driveTo(t, env, "doomed", "paused bundled ticket", api.StatusPaused)
	blocked := driveTo(t, env, "doomed", "blocked bundled ticket", api.StatusBlocked)
	// The bundle-less door: a registry ticket without a bundle.
	less, err := env.WIs.Create(CreateRequest{Project: "doomed", Goal: "queued registry ticket", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("bundle-less create: %v", err)
	}
	_, _, err = env.Admin.DeleteProject("doomed", env.UserID, false)
	if !isCode(err, api.ErrCoreConflict) {
		t.Fatalf("delete doomed: %v, want CORE-409", err)
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Detail == "" {
		t.Fatalf("delete doomed: no detail")
	}
	// Every offending slug is listed, by seq.
	for _, seq := range []int{1, 2, 3, 4, 5} {
		if want := "doomed#" + strconv.Itoa(seq); !strings.Contains(ae.Detail, want) {
			t.Fatalf("CORE-409 detail %q does not list %s", ae.Detail, want)
		}
	}
	doomedID := projectIDOfTest(t, env, "doomed")
	if n := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, doomedID); n != 5 {
		t.Fatalf("409 deleted work items: %d remain, want 5", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE name='doomed'`); n != 1 {
		t.Fatalf("409 deleted the project: %d remain, want 1", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM admin_audit`); n != 0 {
		t.Fatalf("a refused delete wrote %d audit rows", n)
	}
	for _, id := range []string{queued, running, paused, blocked, less.ID} {
		if _, err := env.WIs.Get(id, GetOptions{}); err != nil {
			t.Fatalf("409 removed work item %s: %v", id, err)
		}
	}
}

// TestDeleteProjectScopedKeyBlocked: an unrevoked key scoped to the
// project blocks the delete with CORE-409 listing the key id, and
// nothing is deleted — no key, no row, no audit row.
func TestDeleteProjectScopedKeyBlocked(t *testing.T) {
	env := deleteEnv(t)
	driveTo(t, env, "doomed", "terminal so only the key check can refuse", api.StatusWrapped)
	doomedID := projectIDOfTest(t, env, "doomed")
	issued, err := env.Admin.CreateAPIKey(env.UserIDB, "scoped writer", doomedID)
	if err != nil {
		t.Fatalf("mint scoped key: %v", err)
	}
	_, _, err = env.Admin.DeleteProject("doomed", env.UserID, false)
	if !isCode(err, api.ErrCoreConflict) {
		t.Fatalf("delete doomed: %v, want CORE-409", err)
	}
	var ae *api.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Detail, issued.Token.ID) {
		t.Fatalf("CORE-409 detail does not list the unrevoked key id: %v", err)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM api_keys WHERE id=?`, issued.Token.ID); n != 1 {
		t.Fatalf("409 removed the scoped key: %d remain, want 1", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE name='doomed'`); n != 1 {
		t.Fatalf("409 deleted the project: %d remain, want 1", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM admin_audit`); n != 0 {
		t.Fatalf("a refused delete wrote %d audit rows", n)
	}
}

// TestDeleteProjectDryRun: a dry run answers the same result (the right
// count) and writes nothing — no row, no audit.
func TestDeleteProjectDryRun(t *testing.T) {
	env := deleteEnv(t)
	driveTo(t, env, "doomed", "dry-run terminal ticket", api.StatusWrapped)
	doomedID := projectIDOfTest(t, env, "doomed")
	res, wiIDs, err := env.Admin.DeleteProject("doomed", env.UserID, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || res.Name != "doomed" || res.ID != doomedID || res.WorkItems != 1 {
		t.Fatalf("dry-run result: %+v, want {doomed %s 1 dry}", *res, doomedID)
	}
	if len(wiIDs) != 1 {
		t.Fatalf("dry-run wi ids: %v", wiIDs)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE name='doomed'`); n != 1 {
		t.Fatalf("dry run deleted the project: %d remain, want 1", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM work_items WHERE project_id=?`, doomedID); n != 1 {
		t.Fatalf("dry run deleted work items: %d remain, want 1", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM admin_audit`); n != 0 {
		t.Fatalf("dry run wrote %d audit rows", n)
	}
}

// TestDeleteProjectFullCascade: a delete that passes the checks removes
// every project-owned row — the project (incl. its repos JSON), work
// items (parent and child in one statement), events, dependency edges,
// memories with their FTS rows, memberships, the revoked scoped key —
// leaves zero orphan references (PRAGMA foreign_key_check empty),
// writes exactly one audit row, and touches nothing of the sibling
// project. Every check is asserted non-vacuously: the pre-delete
// counts prove the rows existed.
func TestDeleteProjectFullCascade(t *testing.T) {
	env := deleteEnv(t)
	doomedID := projectIDOfTest(t, env, "doomed")
	// An unscoped key of the keeper project's admin: the cascade must
	// not touch it (only project-scoped rows go).
	unscoped, err := env.Admin.CreateAPIKey(env.UserID, "unscoped keeper key", "")
	if err != nil {
		t.Fatalf("mint unscoped key: %v", err)
	}
	parent := driveTo(t, env, "doomed", "cascade terminal bundled ticket", api.StatusWrapped)
	// A bundle-less terminal ticket.
	less, err := env.WIs.Create(CreateRequest{Project: "doomed", Goal: "cascade terminal registry ticket", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("bundle-less create: %v", err)
	}
	if _, err := env.WIs.Wrap(less.ID, env.UserID, api.WrapRequest{}); err != nil {
		t.Fatalf("wrap bundle-less: %v", err)
	}
	// A child of the terminal registry ticket (a parent must be a
	// registry ticket): the one-statement work_items delete must take
	// parent and child together (the parent_id self-reference inside
	// the deleted set).
	child, err := env.WIs.Create(CreateRequest{Project: "doomed", Goal: "cascade child", Reporter: env.UserID,
		ParentID: strPtrTest(less.ID), BundleName: strPtrTest("demo-bundle")})
	if err != nil {
		t.Fatalf("child create: %v", err)
	}
	if _, err := env.WIs.Cancel(child.ID, env.UserID); err != nil {
		t.Fatalf("child cancel: %v", err)
	}
	// A memory row + its FTS row (raw SQL: the memory store imports core,
	// so its Create cannot run from a core test; the row shape is the
	// store's own INSERT).
	memID := "mem_cascade"
	now := Now()
	if _, err := env.DB.Exec(`INSERT INTO memories(id, project_id, owner_user_id, work_item_id, type, title, visibility,
		authority_path, content_sha, content_len, tags, related, status, created_at, updated_at, content)
		VALUES (?1,?2,?3,NULL,'fact.note','cascade memory','project','memory/doomed/mem_cascade',?4,19,'[]','[]','active',?5,?5,'doc')`,
		memID, doomedID, env.UserID, strings.Repeat("c", 64), now); err != nil {
		t.Fatalf("memory row: %v", err)
	}
	if _, err := env.DB.Exec(`INSERT INTO memory_fts(mem_id, body) VALUES (?1,'cascade memory body')`, memID); err != nil {
		t.Fatalf("memory fts row: %v", err)
	}
	// A dependency edge, a membership, and a revoked scoped key.
	if err := env.WIs.AddDependency(parent, child.ID, "blocks"); err != nil {
		t.Fatalf("dependency edge: %v", err)
	}
	if err := env.Admin.AddMember(doomedID, env.UserIDB, "writer"); err != nil {
		t.Fatalf("membership: %v", err)
	}
	revoked, err := env.Admin.CreateAPIKey(env.UserIDB, "cascade revoked scoped", doomedID)
	if err != nil {
		t.Fatalf("mint revoked scoped key: %v", err)
	}
	if err := env.Admin.RevokeAPIKey(revoked.Token.ID); err != nil {
		t.Fatalf("revoke scoped key: %v", err)
	}
	// The doomed project's repos JSON (the registration the cascade
	// must take with the row).
	if _, err := env.DB.Exec(`UPDATE projects SET repos='[{"name":"doomed-src","url":"https://example.invalid/doomed.git"}]' WHERE id=?`, doomedID); err != nil {
		t.Fatalf("repos json: %v", err)
	}
	// Non-vacuous preconditions: every table the cascade must empty
	// carries rows.
	for _, check := range []struct {
		name, q string
		args    []any
		want    int
	}{
		{"work items", `SELECT COUNT(*) FROM work_items WHERE project_id=?`, []any{doomedID}, 3},
		{"events", `SELECT COUNT(*) FROM wi_events WHERE work_item_id IN (?,?,?)`, []any{parent, less.ID, child.ID}, 1},
		{"dependency edges", `SELECT COUNT(*) FROM wi_dependencies WHERE blocking_wi_id=?`, []any{parent}, 1},
		{"memories", `SELECT COUNT(*) FROM memories WHERE project_id=?`, []any{doomedID}, 1},
		{"memory FTS rows", `SELECT COUNT(*) FROM memory_fts WHERE mem_id=?`, []any{memID}, 1},
		{"memberships", `SELECT COUNT(*) FROM project_members WHERE project_id=?`, []any{doomedID}, 1},
	} {
		if n := countOf(t, env, check.q, check.args...); n < check.want {
			t.Fatalf("pre-delete %s: %d, want at least %d", check.name, n, check.want)
		}
	}

	res, wiIDs, err := env.Admin.DeleteProject("doomed", env.UserID, false)
	if err != nil {
		t.Fatalf("delete doomed: %v", err)
	}
	if res.DryRun || res.Name != "doomed" || res.ID != doomedID || res.WorkItems != 3 {
		t.Fatalf("delete result: %+v, want {doomed 3 no-dry}", *res)
	}
	if len(wiIDs) != 3 {
		t.Fatalf("delete wi ids: %v", wiIDs)
	}
	for _, check := range []struct {
		name, q string
		args    []any
	}{
		{"the project row (incl. its repos JSON)", `SELECT COUNT(*) FROM projects WHERE id=? OR name='doomed'`, []any{doomedID}},
		{"work_items rows", `SELECT COUNT(*) FROM work_items WHERE project_id=?`, []any{doomedID}},
		{"wi_events rows", `SELECT COUNT(*) FROM wi_events WHERE work_item_id IN (?,?,?)`, []any{parent, less.ID, child.ID}},
		{"wi_dependencies edges", `SELECT COUNT(*) FROM wi_dependencies WHERE blocking_wi_id=? OR blocked_wi_id=?`, []any{parent, child.ID}},
		{"memories rows", `SELECT COUNT(*) FROM memories WHERE project_id=?`, []any{doomedID}},
		{"memory_fts rows", `SELECT COUNT(*) FROM memory_fts WHERE mem_id=?`, []any{memID}},
		{"project_members rows", `SELECT COUNT(*) FROM project_members WHERE project_id=?`, []any{doomedID}},
		{"the revoked scoped api_keys row", `SELECT COUNT(*) FROM api_keys WHERE id=?`, []any{revoked.Token.ID}},
	} {
		if n := countOf(t, env, check.q, check.args...); n != 0 {
			t.Fatalf("the cascade left %s: %d remain", check.name, n)
		}
	}
	// Zero orphan references anywhere: foreign_key_check answers no rows.
	if err := env.DB.QueryRow(`PRAGMA foreign_key_check`).Scan(new(int)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign_key_check is not clean: %v", err)
	}
	// Exactly one audit row, recording actor, project id and name, and
	// every deleted work-item id.
	if n := countOf(t, env, `SELECT COUNT(*) FROM admin_audit`); n != 1 {
		t.Fatalf("admin_audit rows after one delete: %d, want 1", n)
	}
	var actor, projID, projName, blob string
	if err := env.DB.QueryRow(`SELECT actor, project_id, project_name, wi_ids FROM admin_audit`).Scan(&actor, &projID, &projName, &blob); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if actor != env.UserID || projID != doomedID || projName != "doomed" {
		t.Fatalf("audit row: actor=%s proj=%s/%s, want %s %s/doomed", actor, projID, projName, env.UserID, doomedID)
	}
	for _, id := range []string{parent, less.ID, child.ID} {
		if !strings.Contains(blob, `"`+id+`"`) {
			t.Fatalf("audit wi_ids %q does not carry the deleted id %q", blob, id)
		}
	}
	// DeletedWorkItemIDs answers the union of the recorded ids.
	ids, err := env.Admin.DeletedWorkItemIDs()
	if err != nil {
		t.Fatalf("DeletedWorkItemIDs: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("DeletedWorkItemIDs: %v, want the 3 deleted ids", ids)
	}
	// The sibling project is untouched (no over-deletion): its rows and
	// the unscoped admin key survive.
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE name='alpha'`); n != 1 {
		t.Fatalf("the cascade deleted the sibling project: %d remain", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM api_keys WHERE id=? AND revoked_at IS NULL`, unscoped.Token.ID); n != 1 {
		t.Fatalf("the cascade touched the unscoped admin key: %d remain, want 1", n)
	}
}

// TestDeleteProjectReplayConverges: a delete requested again after a
// completed delete answers CORE-404 (nothing resurrected), and still
// exactly one audit row exists (only performed deletions are recorded).
func TestDeleteProjectReplayConverges(t *testing.T) {
	env := deleteEnv(t)
	driveTo(t, env, "doomed", "replay terminal ticket", api.StatusWrapped)
	if _, _, err := env.Admin.DeleteProject("doomed", env.UserID, false); err != nil {
		t.Fatalf("delete #1: %v", err)
	}
	if _, _, err := env.Admin.DeleteProject("doomed", env.UserID, false); !isCode(err, api.ErrCoreNotFound) {
		t.Fatalf("replay delete: %v, want CORE-404", err)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM projects WHERE name='doomed'`); n != 0 {
		t.Fatalf("the replay resurrected the project: %d rows", n)
	}
	if n := countOf(t, env, `SELECT COUNT(*) FROM admin_audit`); n != 1 {
		t.Fatalf("admin_audit rows after the replay: %d, want 1", n)
	}
}

// projectIDOfTest answers a project's id by name (the testEnv's own
// Project is alpha's).
func projectIDOfTest(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	var id string
	if err := env.DB.QueryRow(`SELECT id FROM projects WHERE name=?`, name).Scan(&id); err != nil {
		t.Fatalf("project id of %s: %v", name, err)
	}
	return id
}

// countOf answers one scalar query.
func countOf(t *testing.T, env *testEnv, q string, args ...any) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}
