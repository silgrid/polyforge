package test

// w149_audit_subtraction_acceptance_test.go: the polyforge#149
// acceptance suite (audit subtraction C), driven through the real e2e
// surfaces only — the real aihub binary on a loopback port, its HTTP API
// (raw requests when a header or a route is the subject), the real `pf`
// CLI binary and the aihub sqlite data file. It keeps only what the unit
// suites do not already pin: the terminate-blocked door and the CLI
// `wrap --fail` branch are gone while continue/abandon recover a blocked
// WI, sessions serve no owner field, the bundle digest is gone from the
// wire while bundle_name binds/renames/unbinds, a fresh database carries
// none of the dead shapes, and X-API-Key no longer authenticates. The
// upgrade SQL, the waiting_on domain, the rune goal limit and the
// reporter gate are pinned in internal/aihub/{core,server}.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// ---------------------------------------------------------------------------
// Helpers (all w149-prefixed; the shared v1 harness — newV1Install,
// sessionClaimReq, strPtr, hermeticConfigPath, v1Root, stopAihub/
// restartAihub — is reused as-is).
// ---------------------------------------------------------------------------

// w149Bundle is an arbitrary bundle name: the aihub side stores the
// name as a scalar, so any name binds.
const w149Bundle = "autopilot"

// w149Setup boots the real aihub and one fresh scratch project.
func w149Setup(t *testing.T, project string) *v1Install {
	t.Helper()
	inst := newV1Install(t)
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{
		Name: project, Description: "w149 acceptance scratch",
	}); err != nil {
		t.Fatalf("create project %s: %v", project, err)
	}
	return inst
}

// w149Create files one work item through the install's admin face and
// returns its id.
func w149Create(t *testing.T, inst *v1Install, project, goal string, bundle *string) string {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: project, Goal: goal, BundleName: bundle,
	})
	if err != nil {
		t.Fatalf("create work item (%s): %v", goal, err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	return wi.ID
}

// w149View fetches one WI detail record as a generic map (raw wire: the
// assertions are about which KEYS the server serves).
func w149View(t *testing.T, inst *v1Install, id string) map[string]any {
	t.Helper()
	raw, err := inst.client.GetWorkItemRaw(id)
	if err != nil {
		t.Fatalf("get work item %s: %v", id, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("get work item %s answer: %v (%s)", id, err, raw)
	}
	return m
}

// w149Has reports whether the served record carries the key at all.
func w149Has(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// w149Str reads a string field ("" when absent or null).
func w149Str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// w149Code is the typed wire error code ("" when the error is not a
// typed envelope; "HTTP-404" & co. are synthesized by the client).
func w149Code(err error) string {
	if ae, ok := err.(*api.Error); ok {
		return ae.Code
	}
	return ""
}

// w149Patch attempts a PATCH and returns the wire error (nil = accepted).
func w149Patch(inst *v1Install, id string, fields map[string]any) error {
	_, err := inst.client.PatchWorkItem(id, fields)
	return err
}

// w149Raw performs one RAW HTTP request against the install: bearer ""
// sends no Authorization header (the header-contract scenarios), hdr
// adds/overrides request headers. Returns (status, body).
func w149Raw(t *testing.T, inst *v1Install, method, path, bearer string, body any, hdr map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, inst.url+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, data
}

// w149Block parks a work item blocked through the REAL session lane:
// claim with a fresh session, then the server-side blocked verdict
// (session-end status=blocked — the frozen-ticket transition).
func w149Block(t *testing.T, inst *v1Install, id string) {
	t.Helper()
	req := sessionClaimReq(id)
	if _, err := inst.client.SessionClaim(req); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	if err := inst.client.SessionEnd(id, api.SessionEndRequest{
		SessionID: req.SessionID, Status: api.StatusBlocked,
	}); err != nil {
		t.Fatalf("session-end(blocked) %s: %v", id, err)
	}
	if st := inst.wiStatus(t, id); st != api.StatusBlocked {
		t.Fatalf("work item %s is %s after the blocked verdict, want blocked", id, st)
	}
}

// w149Claim claims the WI with a fresh session and returns the session
// secret (the WI is left RUNNING, its session held). A claim records its
// machine only when the machines row is the claimer's own,
// so the probe machine is first registered to the install's admin — the
// very identity inst.client claims with — keeping session_machine on the
// wire.
func w149Claim(t *testing.T, inst *v1Install, id string) string {
	t.Helper()
	pub, priv := w57Key(t, 0x49)
	if ans := w57Register(t, inst, inst.adminKey, "m-e2e-probe", priv, pub); ans.Status != http.StatusOK {
		t.Fatalf("premise: register m-e2e-probe to the claimer: %s", ans)
	}
	req := sessionClaimReq(id)
	if _, err := inst.client.SessionClaim(req); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return req.SessionID
}

// w149Pf runs the REAL pf CLI against this install and returns
// (combined output, exit code) with NO assertion on the code: the
// scenarios assert the HUB-side effect, not the CLI's wording.
func w149Pf(t *testing.T, inst *v1Install, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url,
		"PF_API_KEY="+inst.adminKey,
		"PF_USER_ID=usr_root",
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v (output %q)", args, err, out.String())
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// ---------------------------------------------------------------------------
// sqlite data-file inspection (the DDL face: "a fresh DB created by the
// new Open").
// ---------------------------------------------------------------------------

func w149OpenDB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite",
		"file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func w149TableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?1`, table).Scan(&n); err != nil {
		t.Fatalf("sqlite_master lookup of %s: %v", table, err)
	}
	return n > 0
}

func w149ColumnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?1) WHERE name=?2`, table, column).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info(%s) lookup of %s: %v", table, column, err)
	}
	return n > 0
}

// ---------------------------------------------------------------------------
// C9 — the terminate-blocked entry is removed whole-chain.
// ---------------------------------------------------------------------------

// TestW149C9TerminateBlockedRemoved covers "Aihub refuses no blocked
// terminations" plus the kept recovery doors (continue / abandon).
func TestW149C9TerminateBlockedRemoved(t *testing.T) {
	const project = "w149-c9"
	inst := w149Setup(t, project)

	a := w149Create(t, inst, project, "C9 parked ticket a", strPtr(w149Bundle))
	w149Block(t, inst, a)

	status, body := w149Raw(t, inst, "POST", "/api/v1/work-items/"+a+"/terminate-blocked",
		inst.adminKey, map[string]any{}, nil)
	if status != http.StatusNotFound {
		t.Errorf("POST .../terminate-blocked answered %d (%.200s), want 404: the route must be gone", status, body)
	}
	if st := inst.wiStatus(t, a); st != api.StatusBlocked {
		t.Errorf("the blocked work item is %s after the terminate-blocked attempt, want blocked", st)
	}

	// Kept face: continue (resume-blocked) recovers the parked WI.
	if _, err := inst.client.ResumeBlockedWorkItem(a); err != nil {
		t.Fatalf("continue (resume-blocked) of %s: %v", a, err)
	}
	if st := inst.wiStatus(t, a); st != api.StatusQueued {
		t.Errorf("after continue, %s is %s, want queued", a, st)
	}

	// Kept face: abandon (cancel) recovers a second parked WI.
	b := w149Create(t, inst, project, "C9 parked ticket b", strPtr(w149Bundle))
	w149Block(t, inst, b)
	if _, err := inst.client.CancelWorkItem(b); err != nil {
		t.Fatalf("abandon (cancel) of %s: %v", b, err)
	}
	if st := inst.wiStatus(t, b); st != api.StatusCanceled {
		t.Errorf("after abandon, %s is %s, want canceled", b, st)
	}
}

// TestW149C9CLIWrapFailBlockedBranchRemoved covers "CLI blocked branch
// gone": `pf wrap --fail` must no longer terminate a parked WI at the
// hub; the WI survives blocked and is recovered via cancel/resume only.
func TestW149C9CLIWrapFailBlockedBranchRemoved(t *testing.T) {
	const project = "w149-c9-cli"
	inst := w149Setup(t, project)

	id := w149Create(t, inst, project, "C9 CLI parked ticket", strPtr(w149Bundle))
	w149Block(t, inst, id)

	out, code := w149Pf(t, inst, "wrap", id, "--fail")
	if st := inst.wiStatus(t, id); st != api.StatusBlocked {
		t.Errorf("`pf wrap --fail` moved the blocked work item to %s (exit %d, output %q): the blocked-specific terminate branch must be deleted", st, code, out)
	}
	if code == 0 {
		t.Errorf("`pf wrap --fail` on a blocked work item exited 0 (output %q): a parked WI has no door for a failed verdict", out)
	}

	// Kept face: abandon still works from the CLI.
	out, code = w149Pf(t, inst, "cancel", id)
	if code != 0 {
		t.Fatalf("`pf cancel` of the parked work item: exit %d (output %q)", code, out)
	}
	if st := inst.wiStatus(t, id); st != api.StatusCanceled {
		t.Errorf("after `pf cancel`, %s is %s, want canceled", id, st)
	}
}

// ---------------------------------------------------------------------------
// C12 — session_owner is removed.
// ---------------------------------------------------------------------------

// TestW149C12SessionOwnerRemoved covers "Sessions carry no owner field".
func TestW149C12SessionOwnerRemoved(t *testing.T) {
	const project = "w149-c12"
	inst := w149Setup(t, project)

	id := w149Create(t, inst, project, "C12 session ticket", strPtr(w149Bundle))
	w149Claim(t, inst, id)

	view := w149View(t, inst, id)
	if w149Has(view, "session_owner") {
		t.Errorf("the work item view still serves session_owner=%v", view["session_owner"])
	}
	// Kept face: the rest of the public session audit block rides on.
	if w149Str(view, "session_fingerprint") == "" {
		t.Errorf("session_fingerprint missing from the view of a held work item: only the owner field is deleted")
	}
	if got := w149Str(view, "session_machine"); got != "m-e2e-probe" {
		t.Errorf("session_machine = %q, want the claiming machine: only the owner field is deleted", got)
	}

	// The collection faces must not carry it either.
	rawQueue, err := inst.client.ReadyQueueRaw(project)
	if err != nil {
		t.Fatalf("ready queue: %v", err)
	}
	if strings.Contains(string(rawQueue), "session_owner") {
		t.Errorf("the ready-queue payload still carries session_owner: %.400s", rawQueue)
	}
	rawList, err := inst.client.ListWorkItemsRaw(project, "", 0)
	if err != nil {
		t.Fatalf("list work items: %v", err)
	}
	if strings.Contains(string(rawList), "session_owner") {
		t.Errorf("the work-item list payload still carries session_owner: %.400s", rawList)
	}
}

// ---------------------------------------------------------------------------
// B9 — the bundle digest chain is removed, bundle_name is retained.
// ---------------------------------------------------------------------------

// TestW149B9BundleDigestRemovedNameKept covers "Create binds a name with
// no digest", "PATCH carries only the bundle_name half", "PATCH with
// bundle_digest is rejected" and (through the code face) "Runtime
// materialize does no digest verification".
func TestW149B9BundleDigestRemovedNameKept(t *testing.T) {
	const project = "w149-b9"
	inst := w149Setup(t, project)

	// Create with a name only: no stand-in digest is ever derived.
	id := w149Create(t, inst, project, "B9 named ticket", strPtr(w149Bundle))
	view := w149View(t, inst, id)
	if w149Has(view, "bundle_digest") {
		t.Errorf("the create/read face still serves bundle_digest=%v (the sha256(\"bundle-name:\"+name) stand-in must be gone)", view["bundle_digest"])
	}
	if got := w149Str(view, "bundle_name"); got != w149Bundle {
		t.Errorf("bundle_name = %q, want %q: the name half is retained", got, w149Bundle)
	}

	// Create with an explicit digest in the payload: the field does not
	// exist, so it is simply not persisted and never served back.
	var raw json.RawMessage
	if err := inst.client.Do("POST", "/api/v1/work-items", map[string]any{
		"project":       project,
		"goal":          "B9 ticket with a digest field in the payload",
		"bundle_name":   w149Bundle,
		"bundle_digest": strings.Repeat("b", 64),
	}, &raw); err != nil {
		t.Fatalf("create with a digest field in the payload: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("create answer: %v (%s)", err, raw)
	}
	if w149Has(created, "bundle_digest") {
		t.Errorf("the create answer still carries bundle_digest=%v", created["bundle_digest"])
	}
	if got := w149Str(created, "bundle_name"); got != w149Bundle {
		t.Errorf("bundle_name = %q after a create carrying a digest, want %q", got, w149Bundle)
	}

	// PATCH carrying bundle_digest is refused CORE-400-class, and binds
	// nothing (the digest half of parseBundlePatch and its whitelist
	// entry are deleted; the key is simply unpatchable).
	err := w149Patch(inst, id, map[string]any{
		"bundle_name":   "other-bundle",
		"bundle_digest": strings.Repeat("c", 64),
	})
	if code := w149Code(err); code != api.ErrCoreBadInput {
		t.Errorf("PATCH with bundle_digest answered %v (code %q), want %s", err, code, api.ErrCoreBadInput)
	}
	if got := w149Str(w149View(t, inst, id), "bundle_name"); got != w149Bundle {
		t.Errorf("the refused PATCH changed bundle_name to %q, want the original %q", got, w149Bundle)
	}

	// Kept face: the R2 window's name half — bind, rename, null-unbind.
	reg := w149Create(t, inst, project, "B9 registration ticket", nil)
	if err := w149Patch(inst, reg, map[string]any{"bundle_name": w149Bundle}); err != nil {
		t.Fatalf("PATCH bind bundle_name: %v", err)
	}
	if got := w149Str(w149View(t, inst, reg), "bundle_name"); got != w149Bundle {
		t.Errorf("after the bind PATCH, bundle_name = %q, want %q", got, w149Bundle)
	}
	if err := w149Patch(inst, reg, map[string]any{"bundle_name": "renamed-bundle"}); err != nil {
		t.Fatalf("PATCH rename bundle_name: %v", err)
	}
	if got := w149Str(w149View(t, inst, reg), "bundle_name"); got != "renamed-bundle" {
		t.Errorf("after the rename PATCH, bundle_name = %q, want renamed-bundle", got)
	}
	if err := w149Patch(inst, reg, map[string]any{"bundle_name": nil}); err != nil {
		t.Fatalf("PATCH null-unbind bundle_name: %v", err)
	}
	if v := w149View(t, inst, reg)["bundle_name"]; v != nil {
		t.Errorf("after the null-unbind PATCH, bundle_name = %v, want null", v)
	}
}

// ---------------------------------------------------------------------------
// Dead schema and the dead X-API-Key header.
// ---------------------------------------------------------------------------

// TestW149DeadSchemaAbsentFromFreshDB covers "Fresh schema matches
// reality".
func TestW149DeadSchemaAbsentFromFreshDB(t *testing.T) {
	const project = "w149-ddl"
	inst := w149Setup(t, project)
	// A real row in every live table the scenario talks about.
	_ = w149Create(t, inst, project, "DDL probe ticket", strPtr(w149Bundle))

	db := w149OpenDB(t, inst)
	for _, table := range []string{"core_meta", "wi_ship_ledger"} {
		if w149TableExists(t, db, table) {
			t.Errorf("fresh DDL still creates the dead table %s", table)
		}
	}
	dead := []struct{ table, column string }{
		{"projects", "scenario"},
		{"projects", "identifier_hash"},
		{"work_items", "workflow_budget_baseline"},
		{"work_items", "bundle_digest"},
		{"work_items", "session_owner"},
	}
	for _, d := range dead {
		if w149ColumnExists(t, db, d.table, d.column) {
			t.Errorf("fresh DDL still creates the dead column %s.%s", d.table, d.column)
		}
	}
	// Kept face: the live columns of the same tables stay.
	for _, k := range []struct{ table, column string }{
		{"projects", "repos"},
		{"work_items", "bundle_name"},
		{"work_items", "waiting_on"},
		{"work_items", "session_fingerprint"},
	} {
		if !w149ColumnExists(t, db, k.table, k.column) {
			t.Errorf("live column %s.%s disappeared", k.table, k.column)
		}
	}
}

// TestW149XAPIKeyNoLongerAuthenticates covers the dead-header half of
// "Dead endpoints/headers/constants gone": Bearer is the only key face.
func TestW149XAPIKeyNoLongerAuthenticates(t *testing.T) {
	const project = "w149-dead"
	inst := w149Setup(t, project)

	status, body := w149Raw(t, inst, "GET", "/api/v1/projects/"+project, "", nil,
		map[string]string{"X-API-Key": inst.adminKey})
	if status != http.StatusUnauthorized {
		t.Errorf("a request authenticated ONLY by X-API-Key answered %d (%.200s), want 401", status, body)
	}
	status, body = w149Raw(t, inst, "GET", "/api/v1/projects/"+project, inst.adminKey, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("Bearer-authenticated GET project answered %d (%.200s), want 200", status, body)
	}
}
