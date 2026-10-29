package server

// Server tests: the auth matrix (no key / wrong key / right key), the
// typed error-envelope shape, and the claim → git-credential →
// session-end flow over real HTTP.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/machinesign"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

type srvEnv struct {
	adminID string
	srv     *Server
	ts      *httptest.Server
	db      *core.DB
	dataDir string
	admin   *core.AdminManager
	key     string // admin api key plaintext
	wiID    string
}

func newSrvEnv(t *testing.T) *srvEnv {
	t.Helper()
	if _, err := http.NewRequest("GET", "/", nil); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return newSrvEnvWithDB(t, dataDir, db)
}

// newSrvEnvWithDB assembles the server env over an existing database
// handle (querycount_test.go opens the core schema through the counting
// driver and hands it in; the caller owns the handle's cleanup).
func newSrvEnvWithDB(t *testing.T, dataDir string, db *core.DB) *srvEnv {
	t.Helper()
	admin := core.NewAdminManager(db)
	u, err := admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	issued, err := admin.CreateAPIKey(u.ID, "test", "")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	if _, err := admin.CreateProject(core.CreateProjectRequest{Name: "alpha"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	gitMgr, err := git.NewManager(dataDir, core.NewOwnerManager(db))
	if err != nil {
		t.Fatalf("git: %v", err)
	}
	srv := New(db, dataDir, gitMgr)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &srvEnv{srv: srv, ts: ts, db: db, dataDir: dataDir, admin: admin, key: issued.Plain, adminID: u.ID}
}

// do performs an authenticated request; out receives the decoded body.
func (e *srvEnv) do(t *testing.T, method, path, key string, body any, out any) (int, []byte) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	if out != nil && buf.Len() > 0 {
		if err := json.Unmarshal(buf.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", buf.String(), err)
		}
	}
	return resp.StatusCode, buf.Bytes()
}

func TestAuthMatrix(t *testing.T) {
	e := newSrvEnv(t)
	// Seed one WI as admin.
	var created map[string]any
	code, body := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": "auth matrix"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}

	// No key → 401 with the typed envelope.
	code, raw := e.do(t, "GET", "/api/v1/work-items", "", nil, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", code)
	}
	var envl map[string]any
	if err := json.Unmarshal(raw, &envl); err != nil {
		t.Fatalf("envelope not JSON: %s", raw)
	}
	if envl["code"] != "AUTH-401" || envl["detail"] == nil {
		t.Fatalf("wrong envelope: %s", raw)
	}

	// Wrong key → 401.
	if code, _ := e.do(t, "GET", "/api/v1/work-items", "bogus-key", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d", code)
	}
	// Revoked key → 401.
	u2, _ := e.admin.CreateUser("temp", "t@example.com", "writer", "human")
	k2, _ := e.admin.CreateAPIKey(u2.ID, "t", "")
	if code, _ := e.do(t, "GET", "/api/v1/work-items", k2.Plain, nil, nil); code != http.StatusOK {
		t.Fatalf("live second key: %d", code)
	}
	_ = e.admin.RevokeAPIKey(k2.Token.ID)
	if code, _ := e.do(t, "GET", "/api/v1/work-items", k2.Plain, nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked key accepted: %d", code)
	}
	// Right key → 200.
	if code, _ := e.do(t, "GET", "/api/v1/work-items", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("right key: %d", code)
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	e := newSrvEnv(t)
	// 404 on an unknown WI: typed CORE-404, code+detail in the body.
	code, raw := e.do(t, "GET", "/api/v1/work-items/wi_nosuchthing", e.key, nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("status: %d %s", code, raw)
	}
	var envl struct {
		Code   string          `json:"code"`
		Detail string          `json:"detail"`
		Extra  json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(raw, &envl); err != nil || envl.Code != "CORE-404" || envl.Detail == "" {
		t.Fatalf("bad envelope: %s", raw)
	}
	// 400 with a machine-readable reason rides in Extra.
	code, raw = e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": ""}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("bad create status: %d %s", code, raw)
	}
	if err := json.Unmarshal(raw, &envl); err != nil || envl.Code != "CORE-400" {
		t.Fatalf("bad envelope: %s", raw)
	}
	// Non-admin on the admin face → AUTH-403.
	outsider, _ := e.admin.CreateUser("out", "out@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "o", "")
	if code, raw = e.do(t, "POST", "/api/v1/users", okey.Plain,
		map[string]string{"display_name": "x", "email": "x@y", "user_type": "human"}, nil); code != http.StatusForbidden {
		t.Fatalf("non-admin on admin face: %d %s", code, raw)
	}
	if err := json.Unmarshal(raw, &envl); err != nil || envl.Code != "AUTH-403" {
		t.Fatalf("admin-face envelope: %s", raw)
	}
}

func TestClaimFlowOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	// The claim flow asserts a recorded session machine, so the "m1"
	// fixture needs a machines row owned by the admin caller
	// (only a caller-owned machine id is recorded).
	seedSessionMachine(t, e, "m1", e.adminID)
	// Create an EXECUTION ticket (bundle_name set): only execution tickets
	// are claimable (#17).
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": "claim flow", "bundle_name": "demo-bundle"}, &wi); code != 201 || wi.ID == "" {
		t.Fatalf("create: %d %s", code, raw)
	}
	// A REGISTRATION ticket answers CORE-409 NOT_CLAIMABLE (#17).
	var reg struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": "registry only"}, &reg); code != 201 {
		t.Fatalf("create reg: %d %s", code, raw)
	}
	var envl struct {
		Code  string          `json:"code"`
		Extra json.RawMessage `json:"extra"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+reg.ID+"/claim", e.key,
		map[string]any{"session_id": "aaaa1111bbbb2222cccc3333dddd4444"}, &envl); code != 409 || envl.Code != "CORE-409" || !strings.Contains(string(envl.Extra), "NOT_CLAIMABLE") {
		t.Fatalf("registration claim: %d %s", code, raw)
	}
	// Session claim over HTTP: the response carries claim + work_item and
	// NEVER the secret (B3) nor any hash.
	secret := "0123456789abcdef0123456789abcdef"
	var resp struct {
		Claim struct {
			WorkItemID         string `json:"work_item_id"`
			SessionFingerprint string `json:"session_fingerprint"`
			MachineID          string `json:"machine_id"`
			Owner              string `json:"owner"`
			BundleName         string `json:"bundle_name"`
			ClaimedAt          string `json:"claimed_at"`
		} `json:"claim"`
		WorkItem struct {
			Status             string `json:"status"`
			SessionFingerprint string `json:"session_fingerprint"`
			SessionMachine     string `json:"session_machine"`
			SessionStartedAt   string `json:"session_started_at"`
			CleanPause         bool   `json:"clean_pause"`
		} `json:"work_item"`
	}
	// idempotency_key: an older daemon still sends it; the claim accepts
	// and ignores it.
	code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/claim", e.key,
		map[string]string{"session_id": secret, "machine_id": "m1", "idempotency_key": "http-1"}, &resp)
	if code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if resp.Claim.SessionFingerprint != api.SessionFingerprint(secret) || resp.Claim.WorkItemID != wi.ID {
		t.Fatalf("claim shape wrong: %+v", resp.Claim)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "session_hash") {
		t.Fatalf("claim response leaks the secret or the hash: %s", raw)
	}
	// #5: the bundle name rides the claim object itself (the runtime
	// reads it from resp.Claim, never from resp.WorkItem).
	if resp.Claim.BundleName != "demo-bundle" {
		t.Fatalf("claim bundle name wrong: %+v", resp.Claim)
	}
	if resp.WorkItem.Status != "running" {
		t.Fatalf("work item not running after claim: %+v", resp.WorkItem)
	}
	// The WorkItem JSON carries the m4 public audit block (never a hash).
	if resp.WorkItem.SessionFingerprint != resp.Claim.SessionFingerprint ||
		resp.WorkItem.SessionMachine != "m1" ||
		resp.WorkItem.SessionStartedAt == "" || resp.WorkItem.CleanPause {
		t.Fatalf("work item session audit fields wrong: %+v", resp.WorkItem)
	}
	// Idempotent replay (D4): the SAME key answers the SAME claim.
	var replay struct {
		Claim struct {
			SessionFingerprint string `json:"session_fingerprint"`
		} `json:"claim"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/claim", e.key,
		map[string]string{"session_id": secret, "machine_id": "m1", "owner": "op", "idempotency_key": "http-1"}, &replay); code != 200 {
		t.Fatalf("idempotent replay: %d %s", code, raw)
	}
	if replay.Claim.SessionFingerprint != resp.Claim.SessionFingerprint {
		t.Fatalf("idempotent replay returned a different session: %+v", replay.Claim)
	}
	// A second claim without a key: ALREADY_HELD.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/claim", e.key,
		map[string]string{"session_id": "ffffffffffffffffffffffffffffffff"}, &envl); code != 409 || !strings.Contains(string(envl.Extra), "ALREADY_HELD") {
		t.Fatalf("double claim not refused: %d %s", code, raw)
	}
	// The bare repo exists and the git face answers with the session
	// secret as the push password (info/refs over the same server).
	gitURL := fmt.Sprintf("%s/git/%s.git/info/refs?service=git-upload-pack", e.ts.URL, wi.ID)
	req, _ := http.NewRequest("GET", strings.Replace(gitURL, "http://", "http://"+wi.ID+":"+secret+"@", 1), nil)
	gresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("git info/refs: %v", err)
	}
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusOK {
		t.Fatalf("git info/refs with session secret: %d", gresp.StatusCode)
	}
	// Wrong secret -> 401.
	req, _ = http.NewRequest("GET", strings.Replace(gitURL, "http://", "http://"+wi.ID+":nope@", 1), nil)
	gresp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("git info/refs: %v", err)
	}
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("git info/refs with bad secret: %d", gresp.StatusCode)
	}
	// session-end: wrapped; the session row is fully cleared, then the
	// git face refuses (no live session).
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/session-end", e.key,
		map[string]string{"session_id": secret, "status": "wrapped", "note": "done"}, nil); code != 200 {
		t.Fatalf("session-end: %d %s", code, raw)
	}
	var ended struct {
		Status             string `json:"status"`
		SessionFingerprint string `json:"session_fingerprint"`
		CleanPause         bool   `json:"clean_pause"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+wi.ID, e.key, nil, &ended); code != 200 || ended.Status != "wrapped" || ended.SessionFingerprint != "" {
		t.Fatalf("WI after session-end: %d %s %+v", code, raw, ended)
	}
	req, _ = http.NewRequest("GET", strings.Replace(gitURL, "http://", "http://"+wi.ID+":"+secret+"@", 1), nil)
	gresp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("git after wrap: %v", err)
	}
	gresp.Body.Close()
	if gresp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("git face alive after session-end: %d", gresp.StatusCode)
	}
}

func TestMembershipBoundary(t *testing.T) {
	e := newSrvEnv(t)
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": "hidden object"}, &wi); code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	// An outsider with a valid key sees nothing: 404, not 403 (no
	// existence leak).
	outsider, _ := e.admin.CreateUser("out2", "out2@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "o2", "")
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+wi.ID, okey.Plain, nil, nil); code != 404 {
		t.Fatalf("outsider detail: %d %s", code, raw)
	}
	// The list is trimmed for the outsider.
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items?project=alpha", okey.Plain, nil, &list); code != 200 || list.Total != 0 {
		t.Fatalf("outsider list: %d %s", code, raw)
	}
	// Membership (writer) grants read + create.
	_ = e.admin.AddMember(e.adminProjectID(t, "alpha"), outsider.ID, "writer")
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+wi.ID, okey.Plain, nil, nil); code != 200 {
		t.Fatalf("member detail: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items?project=alpha", okey.Plain, nil, &list); code != 200 || list.Total != 1 {
		t.Fatalf("member list: %d %s total=%d", code, raw, list.Total)
	}
}

func (e *srvEnv) adminProjectID(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name=?1`, name).Scan(&id); err != nil {
		t.Fatalf("project id: %v", err)
	}
	return id
}

func TestMemoryOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	var created struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "project",
		"content": "the quick brown fox over http",
	}, &created); code != 201 || created.ID == "" {
		t.Fatalf("create memory: %d %s", code, raw)
	}
	var search struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=brown+fox", e.key, nil, &search); code != 200 || search.Total != 1 {
		t.Fatalf("search: %d %s", code, raw)
	}
	// Per-id read (polyforge#132, the faces `pf memory show` consumes):
	// the default face is metadata only (no body); ?format=content is the
	// bare body as text/markdown (no front-matter; metadata lives on the
	// JSON face).
	var meta map[string]any
	if code, raw := e.do(t, "GET", "/api/v1/memories/"+created.ID, e.key, nil, &meta); code != 200 || meta["id"] != created.ID {
		t.Fatalf("get metadata: %d %s", code, raw)
	}
	if _, has := meta["content"]; has {
		t.Fatalf("metadata face must not carry the body: %v", meta)
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/"+created.ID+"?format=content", e.key, nil, nil); code != 200 || string(raw) != "the quick brown fox over http" {
		t.Fatalf("get content: %d %q", code, raw)
	}
	// A body that itself contains a "---" line is returned whole.
	const dashed = "intro\n---\nnot a delimiter\n---\ntail\n"
	var dcreated struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "project",
		"content": dashed,
	}, &dcreated); code != 201 || dcreated.ID == "" {
		t.Fatalf("create dashed memory: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/"+dcreated.ID+"?format=content", e.key, nil, nil); code != 200 || string(raw) != dashed {
		t.Fatalf("dashed body: %d %q, want %q", code, raw, dashed)
	}
	// Redact → GET is a 404.
	if code, _ := e.do(t, "POST", "/api/v1/memories/"+created.ID+"/redact", e.key, nil, nil); code != 200 {
		t.Fatalf("redact: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/memories/"+created.ID, e.key, nil, nil); code != 404 {
		t.Fatalf("redacted memory served: %d", code)
	}
}

// TestMemoryDBOnlyOverHTTP: a memory is served from the database alone,
// and an idempotent replay with the same key
// answers the original entry whose body reads back verbatim (the step
// summary write compares it with what it sent); after a redact the body
// is never served and the entry leaves search.
func TestMemoryDBOnlyOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	const body = "## Step summary\n\nthe wrap lesson about wallabies\n"
	post := func(content string) (int, map[string]any) {
		t.Helper()
		var out map[string]any
		code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": "alpha", "type": "experience.wrap", "visibility": "project",
			"content": content, "idempotency_key": "wi-1/step-3",
		}, &out)
		if code != 201 {
			t.Fatalf("create: %d %s", code, raw)
		}
		return code, out
	}
	_, first := post(body)
	_, replay := post("a different body sent on the replay")
	if replay["id"] != first["id"] || replay["content_sha"] != first["content_sha"] || replay["content_len"] != first["content_len"] {
		t.Fatalf("replay = %v, want the original %v", replay, first)
	}
	id, _ := first["id"].(string)
	if code, raw := e.do(t, "GET", "/api/v1/memories/"+id+"?format=content", e.key, nil, nil); code != 200 || string(raw) != body {
		t.Fatalf("content after the replay: %d %q, want %q", code, raw, body)
	}
	var search struct {
		Total int `json:"total"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=wallabies", e.key, nil, &search); code != 200 || search.Total != 1 {
		t.Fatalf("search: %d %s", code, raw)
	}
	if code, _ := e.do(t, "POST", "/api/v1/memories/"+id+"/redact", e.key, nil, nil); code != 200 {
		t.Fatalf("redact: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/memories/"+id+"?format=content", e.key, nil, nil); code != 404 {
		t.Fatalf("redacted body served: %d", code)
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=wallabies", e.key, nil, &search); code != 200 || search.Total != 0 {
		t.Fatalf("redacted entry searchable: %d %s", code, raw)
	}
}

// TestMemoryWorkItemSlug (polyforge#155): the attributing work item of a
// memory write accepts the "<project>#<seq>" slug the other faces accept
// and stores the canonical id; an unknown id or slug answers a typed
// CORE-404, never the insert's foreign-key failure as a 500.
func TestMemoryWorkItemSlug(t *testing.T) {
	e := newSrvEnv(t)
	var wi struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": "attribution"}, &wi); code != 201 || wi.ID == "" || wi.Slug == "" {
		t.Fatalf("create wi: %d %s", code, raw)
	}
	for _, spelling := range []string{wi.Slug, wi.ID} {
		var m struct {
			ID         string `json:"id"`
			WorkItemID string `json:"work_item_id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": "alpha", "type": "fact.note", "visibility": "project",
			"content": "attributed via " + spelling, "work_item_id": spelling,
		}, &m); code != 201 || m.WorkItemID != wi.ID {
			t.Fatalf("create memory with work_item_id %q: %d %s (want canonical %s)", spelling, code, raw, wi.ID)
		}
	}
	var bare struct {
		WorkItemID *string `json:"work_item_id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content": "unattributed", "work_item_id": "",
	}, &bare); code != 201 || bare.WorkItemID != nil {
		t.Fatalf("empty work_item_id is no attribution: %d %s", code, raw)
	}
	type envelope struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	remember := func(key, project, wiRef string) (int, envelope, []byte) {
		var envl envelope
		code, raw := e.do(t, "POST", "/api/v1/memories", key, map[string]any{
			"project": project, "type": "fact.note", "visibility": "project",
			"content": "orphan", "work_item_id": wiRef,
		}, &envl)
		return code, envl, raw
	}
	for _, unknown := range []string{"alpha#999", "wi_doesnotexist", "nope#1", "garbage"} {
		if code, envl, raw := remember(e.key, "alpha", unknown); code != 404 || envl.Code != api.ErrCoreNotFound ||
			envl.Detail != "work item "+unknown {
			t.Fatalf("unknown work_item_id %q: %d %s, want 404 %s", unknown, code, raw, api.ErrCoreNotFound)
		}
	}

	// A memory belongs to one project: a readable work item of another
	// project is refused as bad input.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("project beta: %v", err)
	}
	for _, spelling := range []string{wi.Slug, wi.ID} {
		if code, envl, raw := remember(e.key, "beta", spelling); code != 400 || envl.Code != api.ErrCoreBadInput {
			t.Fatalf("cross-project work_item_id %q: %d %s, want 400 %s", spelling, code, raw, api.ErrCoreBadInput)
		}
	}

	// No existence leak: a writer on beta who is NOT a member of alpha
	// gets, for alpha's existing work item (slug or id), exactly the
	// answer an unknown work item gets — before the project check.
	outsider, err := e.admin.CreateUser("out3", "out3@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("outsider: %v", err)
	}
	okey, err := e.admin.CreateAPIKey(outsider.ID, "o3", "")
	if err != nil {
		t.Fatalf("outsider key: %v", err)
	}
	if err := e.admin.AddMember(e.adminProjectID(t, "beta"), outsider.ID, "writer"); err != nil {
		t.Fatalf("member: %v", err)
	}
	for _, pair := range [][2]string{{wi.Slug, "alpha#999"}, {wi.ID, "wi_doesnotexist"}} {
		existing, unknown := pair[0], pair[1]
		code, got, raw := remember(okey.Plain, "beta", existing)
		ucode, want, uraw := remember(okey.Plain, "beta", unknown)
		if code != 404 || got.Code != api.ErrCoreNotFound || ucode != 404 || want.Code != api.ErrCoreNotFound {
			t.Fatalf("outsider: existing %q → %d %s; unknown %q → %d %s", existing, code, raw, unknown, ucode, uraw)
		}
		if strings.Replace(got.Detail, existing, "X", 1) != strings.Replace(want.Detail, unknown, "X", 1) {
			t.Fatalf("existence leak: hidden %q detail %q vs unknown %q detail %q", existing, got.Detail, unknown, want.Detail)
		}
	}

	// No existence leak through the key scope either ( A1):
	// a key scoped to beta, held by a writer of BOTH projects, names
	// alpha's existing work item in a beta memory and gets exactly the
	// unknown-work-item answer — the scope, not the membership, conceals
	// it (never the scope's AUTH-403, which only an existing one got).
	both, err := e.admin.CreateUser("both3", "both3@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("two-project writer: %v", err)
	}
	for _, p := range []string{"alpha", "beta"} {
		if err := e.admin.AddMember(e.adminProjectID(t, p), both.ID, "writer"); err != nil {
			t.Fatalf("member of %s: %v", p, err)
		}
	}
	skey, err := e.admin.CreateAPIKey(both.ID, "s3", e.adminProjectID(t, "beta"))
	if err != nil {
		t.Fatalf("beta-scoped key: %v", err)
	}
	for _, pair := range [][2]string{{wi.Slug, "alpha#999"}, {wi.ID, "wi_doesnotexist"}} {
		existing, unknown := pair[0], pair[1]
		code, got, raw := remember(skey.Plain, "beta", existing)
		ucode, want, uraw := remember(skey.Plain, "beta", unknown)
		if code != ucode || code != 404 || got.Code != api.ErrCoreNotFound || want.Code != api.ErrCoreNotFound ||
			strings.Replace(string(raw), existing, "X", 1) != strings.Replace(string(uraw), unknown, "X", 1) {
			t.Fatalf("scoped key: existing %q → %d %s; unknown %q → %d %s", existing, code, raw, unknown, ucode, uraw)
		}
	}
}

// ---------------------------------------------------------------------------
// Management face over HTTP (#10): the six endpoints the CLI verbs call.

func TestAdminFaceEndpointsOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	// A non-admin caller for the 403 matrix.
	outsider, _ := e.admin.CreateUser("plain", "plain@example.com", "writer", "human")
	okey, _ := e.admin.CreateAPIKey(outsider.ID, "o", "")

	// users get / patch.
	var u struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/users/"+outsider.ID, e.key, nil, &u); code != 200 || u.ID != outsider.ID {
		t.Fatalf("users get: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/users/usr_nosuch", e.key, nil, nil); code != 404 {
		t.Fatalf("users get unknown: %d %s", code, raw)
	}
	if code, raw := e.do(t, "PATCH", "/api/v1/users/"+outsider.ID, e.key,
		map[string]string{"display_name": "renamed", "role": "admin"}, &u); code != 200 || u.DisplayName != "renamed" || u.Role != "admin" {
		t.Fatalf("users patch: %d %s", code, raw)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/users/"+outsider.ID, e.key, map[string]string{}, nil); code != 400 {
		t.Fatalf("users patch empty body: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/users/"+outsider.ID, e.key, map[string]string{"role": "bogus"}, nil); code != 400 {
		t.Fatalf("users patch bad role: %d", code)
	}
	// Back to writer: the 403 matrix below depends on the non-admin role.
	if code, _ := e.do(t, "PATCH", "/api/v1/users/"+outsider.ID, e.key, map[string]string{"role": "writer"}, nil); code != 200 {
		t.Fatalf("users patch back to writer: %d", code)
	}

	// api-keys list: metadata only, never material or hash.
	issued, _ := e.admin.CreateAPIKey(outsider.ID, "listed", "")
	var keys struct {
		Items []struct {
			ID     string `json:"id"`
			UserID string `json:"user_id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	code, raw := e.do(t, "GET", "/api/v1/api-keys", e.key, nil, &keys)
	if code != 200 || keys.Total < 2 {
		t.Fatalf("api-keys list: %d %s total=%d", code, raw, keys.Total)
	}
	if strings.Contains(string(raw), issued.Plain) || strings.Contains(string(raw), "key_hash") {
		t.Fatalf("api-keys list leaks material: %s", raw)
	}
	found := false
	for _, k := range keys.Items {
		if k.ID == issued.Token.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("issued key not listed: %s", raw)
	}

	// projects patch: the description first.
	var p struct {
		ID          string `json:"id"`
		Description string `json:"description"`
		Members     []struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if code, raw := e.do(t, "PATCH", "/api/v1/projects/alpha", e.key,
		map[string]any{"description": "first"}, &p); code != 200 || p.Description != "first" {
		t.Fatalf("projects patch description: %d %s", code, raw)
	}
	// Membership: upsert u2 as maintainer; removal runs after upsert.
	u2, _ := e.admin.CreateUser("maint", "maint@example.com", "writer", "human")
	mk, _ := e.admin.CreateAPIKey(u2.ID, "m", "")
	if code, raw := e.do(t, "PATCH", "/api/v1/projects/alpha", e.key,
		map[string]any{"members": []map[string]string{{"user_id": u2.ID, "role": "maintainer"}}}, &p); code != 200 || len(p.Members) != 1 {
		t.Fatalf("members add: %d %s", code, raw)
	}
	// The maintainer (non-admin) may patch; a writer member may not.
	if code, raw := e.do(t, "PATCH", "/api/v1/projects/alpha", mk.Plain,
		map[string]any{"description": "by the maintainer"}, &p); code != 200 || p.Description != "by the maintainer" {
		t.Fatalf("maintainer patch: %d %s", code, raw)
	}
	// projects get: membership OR admin (the doctor projectsCheck and the
	// repo-units scan read it with the config's key; a non-member's read
	// stays hidden behind the not-found detail, like every other read).
	if code, raw := e.do(t, "GET", "/api/v1/projects/alpha", mk.Plain, nil, &p); code != 200 || p.ID == "" {
		t.Fatalf("member gets project: %d %s", code, raw)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/alpha", okey.Plain, nil, nil); code != 404 {
		t.Fatalf("outsider gets project: %d (want the hidden 404)", code)
	}
	// A bad member role or unknown user is a clean 400/404.
	if code, _ := e.do(t, "PATCH", "/api/v1/projects/alpha", e.key,
		map[string]any{"members": []map[string]string{{"user_id": u2.ID, "role": "owner"}}}, nil); code != 400 {
		t.Fatalf("bad member role: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/projects/alpha", e.key,
		map[string]any{"members": []map[string]string{{"user_id": "usr_nosuch", "role": "writer"}}}, nil); code != 404 {
		t.Fatalf("unknown member user: %d", code)
	}
	// Explicit remove list.
	if code, raw := e.do(t, "PATCH", "/api/v1/projects/alpha", e.key,
		map[string]any{"remove_members": []string{u2.ID}}, &p); code != 200 || len(p.Members) != 0 {
		t.Fatalf("members remove: %d %s", code, raw)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/projects/alpha", mk.Plain, map[string]any{}, nil); code != 403 {
		t.Fatalf("removed maintainer still patches: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/v1/projects/alpha", mk.Plain, nil, nil); code != 404 {
		t.Fatalf("removed member still gets project: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/projects/alpha", okey.Plain, map[string]any{}, nil); code != 403 {
		t.Fatalf("writer patches project: %d", code)
	}
	if code, _ := e.do(t, "PATCH", "/api/v1/projects/nope", e.key, map[string]any{}, nil); code != 404 {
		t.Fatalf("unknown project patch: %d", code)
	}

	// backup: snapshot file lands under <dataDir>/backups.
	var bk struct {
		Backup string `json:"backup"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/admin/backup", e.key, map[string]any{}, &bk); code != 200 || bk.Backup == "" {
		t.Fatalf("backup: %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir, "backups", bk.Backup)); err != nil {
		t.Fatalf("snapshot file missing: %v", err)
	}

	// Non-admin callers are refused on every new admin endpoint.
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/users/" + outsider.ID, nil},
		{"PATCH", "/api/v1/users/" + outsider.ID, map[string]string{"display_name": "x"}},
		{"GET", "/api/v1/api-keys", nil},
		{"POST", "/api/v1/admin/backup", map[string]any{}},
	} {
		if code, raw := e.do(t, tc.method, tc.path, okey.Plain, tc.body, nil); code != 403 {
			t.Fatalf("non-admin %s %s: %d %s", tc.method, tc.path, code, raw)
		}
	}
}

// TestApiKeyScopeField pins #11: the CLI sends the scope as project_id and
// the key must actually be scoped (not minted GLOBAL).
func TestApiKeyScopeField(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	// Seed one WI per project.
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "alpha", "goal": "alpha wi", "bundle_name": "b"}, &wi); code != 201 {
		t.Fatalf("alpha wi: %d %s", code, raw)
	}
	alphaID := wi.ID
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "beta", "goal": "beta wi", "bundle_name": "b"}, &wi); code != 201 {
		t.Fatalf("beta wi: %d %s", code, raw)
	}
	betaID := wi.ID

	target, _ := e.admin.CreateUser("scoped", "scoped@example.com", "writer", "human")
	// The target is a writer on alpha: scoping must be the ONLY thing that
	// keeps the key off beta (a plain membership denial would 404-fold).
	if err := e.admin.AddMember(e.adminProjectID(t, "alpha"), target.ID, "writer"); err != nil {
		t.Fatal(err)
	}
	if err := e.admin.AddMember(e.adminProjectID(t, "beta"), target.ID, "writer"); err != nil {
		t.Fatal(err)
	}
	// BIDIRECTIONAL: the real client (which sends project_id) against the
	// real server.
	client := aihubclient.New(e.ts.URL, e.key)
	raw, err := client.CreateApiKey(aihubclient.ApiKeyCreateRequest{UserID: target.ID, ProjectID: strPtrTest("alpha")})
	if err != nil {
		t.Fatalf("client create key: %v", err)
	}
	var issued struct {
		Plain string `json:"plain"`
	}
	if err := json.Unmarshal(raw, &issued); err != nil || issued.Plain == "" {
		t.Fatalf("no plaintext in issue response: %s", raw)
	}
	// The key is genuinely scoped: its own project reads fine; on another
	// project's work item the key sees the same missing-work-item answer as
	// an unknown reference (the scope mismatch conceals the
	// object, it does not announce it).
	if code, _ := e.do(t, "GET", "/api/v1/work-items/"+alphaID, issued.Plain, nil, nil); code != 200 {
		t.Fatalf("scoped key denied on its own project: %d", code)
	}
	code, raw := e.do(t, "GET", "/api/v1/work-items/"+betaID, issued.Plain, nil, nil)
	if code != 404 {
		t.Fatalf("scoped key reached another project: %d %s", code, raw)
	} else if c, _ := errEnvelope(t, raw); c != api.ErrCoreNotFound {
		t.Fatalf("scoped key on another project's WI: %s", raw)
	}
}

func strPtrTest(s string) *string { return &s }

// TestClaimResponseCarriesProjectRepos: the claim endpoint serves the
// project's task-repo inventory on the claim object (the runtime's
// claim→clone lane reads it from resp.Claim).
func TestClaimResponseCarriesProjectRepos(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{
		Name:  "bravo",
		Repos: []api.ProjectRepo{{Name: "app", URL: "file:///srv/app.git"}},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]any{"project": "bravo", "goal": "repos over http", "bundle_name": "demo-bundle"}, &wi); code != 201 || wi.ID == "" {
		t.Fatalf("create: %d %s", code, raw)
	}
	var resp struct {
		Claim struct {
			Repos []api.ProjectRepo `json:"repos"`
		} `json:"claim"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+wi.ID+"/claim", e.key,
		map[string]string{"session_id": "55556666777788885555666677778888"}, &resp); code != 200 {
		t.Fatalf("claim: %d %s", code, raw)
	}
	if len(resp.Claim.Repos) != 1 || resp.Claim.Repos[0].Name != "app" || resp.Claim.Repos[0].URL != "file:///srv/app.git" {
		t.Fatalf("claim repos wrong over HTTP: %+v", resp.Claim.Repos)
	}
}

// TestMemorySearchExcerpt (F6): the search face's additive excerpt
// extension — a caller that names an excerpt length gets each hit with a
// bounded content prefix attached (content_excerpt); a plain search
// keeps the exact v0 answer shape (no excerpt key at all).
func TestMemorySearchExcerpt(t *testing.T) {
	e := newSrvEnv(t)
	if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
		"project": "alpha", "type": "experience.debug", "visibility": "project",
		"content": "the lesson about bounded retry ladders",
	}, nil); code != 201 {
		t.Fatalf("create memory: %d %s", code, raw)
	}

	// Plain search: the v0 shape, no excerpt key on the row.
	var plain struct {
		Items []map[string]any `json:"items"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=ladders", e.key, nil, &plain); code != 200 || len(plain.Items) != 1 {
		t.Fatalf("plain search: %d %s", code, raw)
	}
	if _, has := plain.Items[0]["content_excerpt"]; has {
		t.Fatalf("plain search must not carry an excerpt: %v", plain.Items[0])
	}

	// GET with an excerpt: a bounded rune prefix of the authority content.
	var with struct {
		Items []struct {
			ID             string `json:"id"`
			ContentExcerpt string `json:"content_excerpt"`
		} `json:"items"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=ladders&excerpt=9", e.key, nil, &with); code != 200 || len(with.Items) != 1 {
		t.Fatalf("excerpt search: %d %s", code, raw)
	}
	if with.Items[0].ContentExcerpt != "the lesso" {
		t.Fatalf("content_excerpt = %q, want the first 9 runes", with.Items[0].ContentExcerpt)
	}

}

// TestMemorySearchTypeWildcard: the type filter's trailing-".*" prefix
// interpretation (X.* → prefix "X.") applies to the GET search face; non-wildcard values keep exact matching.
func TestMemorySearchTypeWildcard(t *testing.T) {
	e := newSrvEnv(t)
	mk := func(memType string) {
		t.Helper()
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": "alpha", "type": memType, "visibility": "project",
			"content": "the shared xylophone token",
		}, nil); code != 201 {
			t.Fatalf("create %s: %d %s", memType, code, raw)
		}
	}
	mk("experience.debug")
	mk("experience.approach")
	mk("fact.note")

	var res struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
		Total int `json:"total"`
	}
	// GET face: experience.* returns both experience.* rows, not fact.note.
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=xylophone&type=experience.*", e.key, nil, &res); code != 200 || len(res.Items) != 2 {
		t.Fatalf("GET experience.*: %d %s (%d items)", code, raw, len(res.Items))
	}
	for _, it := range res.Items {
		if !strings.HasPrefix(it.Type, "experience.") {
			t.Fatalf("GET experience.* matched type %q", it.Type)
		}
	}
	// GET face, non-wildcard: exact type only.
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=xylophone&type=experience.debug", e.key, nil, &res); code != 200 || len(res.Items) != 1 || res.Items[0].Type != "experience.debug" {
		t.Fatalf("GET exact: %d %s (%+v)", code, raw, res.Items)
	}
	// fact.* never matches experience.* rows.
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=xylophone&type=fact.*", e.key, nil, &res); code != 200 || len(res.Items) != 1 || res.Items[0].Type != "fact.note" {
		t.Fatalf("GET fact.*: %d %s (%+v)", code, raw, res.Items)
	}
}

// doSigned sends one machine-face-signed request over the exact bytes it
// writes (METHOD\nPATH\nsha256(body)\nTS\nmachine_id) to the environment's
// own listener. priv == nil sends the request with no X-PF-* header at
// all — the heartbeat transition shape.
func (e *srvEnv) doSigned(t *testing.T, method, path, key, machineID string, priv ed25519.PrivateKey, ts int64, body any) (int, []byte) {
	t.Helper()
	return e.doSignedTo(t, e.ts.URL, method, path, key, machineID, priv, ts, body)
}

// doSignedTo is doSigned against an explicit base URL (httpserver_test
// drives signed requests at a second listener bound by the server
// constructor's pressure bounds).
func (e *srvEnv) doSignedTo(t *testing.T, baseURL, method, path, key, machineID string, priv ed25519.PrivateKey, ts int64, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		raw = b
	}
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if priv != nil {
		if ts == 0 {
			ts = time.Now().Unix()
		}
		// The signature covers r.URL.Path (machineface.go), never the query.
		signPath := path
		if i := strings.IndexByte(signPath, '?'); i >= 0 {
			signPath = signPath[:i]
		}
		req.Header.Set(machinesign.HeaderMachineID, machineID)
		req.Header.Set(machinesign.HeaderTimestamp, strconv.FormatInt(ts, 10))
		req.Header.Set(machinesign.HeaderSignature, machinesign.Sign(priv, method, signPath, raw, ts, machineID))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	return resp.StatusCode, buf.Bytes()
}

// machineKey answers a fresh Ed25519 identity and its base64 wire spelling
// (the machine face's public_key encoding).
func machineKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

// TestPatchRecordsRequestingUserAsActor pins a PATCH records
// the authenticated requesting user as the wi_content_updated event's
// actor — read back through GET ?include=events — with the payload
// unchanged (empty; no backfill of historical events).
func TestPatchRecordsRequestingUserAsActor(t *testing.T) {
	e := newSrvEnv(t)
	var created map[string]any
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": "alpha", "goal": "patch actor"}, &created); code != http.StatusCreated {
		t.Fatalf("create WI: %d %s", code, raw)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create WI answer: %v", created)
	}
	patcherID, patcherKey := memberUser(t, e, "w82-patcher", "writer")
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, patcherKey,
		map[string]any{"priority": api.PriorityHigh}, nil); code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", code, raw)
	}
	_, events := wiEvents(t, e, id)
	updated := eventsOfType(events, "wi_content_updated")
	if len(updated) != 1 {
		t.Fatalf("wi_content_updated events: %v, want exactly one", events)
	}
	if got := updated[0]["actor"]; got != patcherID {
		t.Fatalf("wi_content_updated actor = %#v, want the requesting user %q", got, patcherID)
	}
	if payload, _ := updated[0]["payload"].(map[string]any); len(payload) != 0 {
		t.Fatalf("wi_content_updated payload changed: %v", updated[0]["payload"])
	}
}

// TestAdminListTotalIsTheRowCount (C9e): an admin list answers one capped
// page, and its total is the true row count — never the page length — so
// a capped answer is distinguishable from a complete one.
func TestAdminListTotalIsTheRowCount(t *testing.T) {
	e := newSrvEnv(t)
	var before struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/projects", e.key, nil, &before); code != 200 {
		t.Fatalf("list projects: %d %s", code, raw)
	}
	for i := 0; len(before.Items)+i < 201; i++ {
		if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: fmt.Sprintf("cap-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/projects", e.key, nil, &list); code != 200 {
		t.Fatalf("list projects: %d %s", code, raw)
	}
	if len(list.Items) != 200 || list.Total != 201 {
		t.Fatalf("projects list: %d items total %d, want the 200 page and total 201", len(list.Items), list.Total)
	}
}
