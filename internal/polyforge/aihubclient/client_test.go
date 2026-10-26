package aihubclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestQueueReturnsItems pins that the daemon's auto-pull reads only the
// ready queue's items segment (running/paused are visibility-only).
func TestQueueReturnsItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": "p",
			"items":   []map[string]any{{"id": "wi_a", "status": "queued", "goal": "a"}},
			"running": []map[string]any{{"id": "wi_r", "status": "running", "goal": "r"}},
		})
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	got, err := c.Queue("p")
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(got) != 1 || got[0].ID != "wi_a" {
		t.Fatalf("want only items segment, got %+v", got)
	}
}

// TestListMachinesPinsRouteAndDecoding: the machines list client call
// hits GET /api/v1/machines with the bearer key; the typed decode covers
// runtime_version / live_wi_ids / stale / heartbeat_age_seconds; and the
// raw variant passes the server body through byte-for-byte.
func TestListMachinesPinsRouteAndDecoding(t *testing.T) {
	// Rows with and without runtime_version, live_wi_ids empty and
	// non-empty, plus an unknown field and non-canonical key order — a
	// body the raw variant must hand through untouched.
	const body = `{"machines":[` +
		`{"stale":false,"live_wi_ids":["wi_a","wi_b"],"mode":"agent","id":"m_1",` +
		`"heartbeat_age_seconds":42,"last_heartbeat_at":"t1",` +
		`"runtime_version":"go1.99","user_id":"usr_1","future_field":true},` +
		`{"id":"m_2","user_id":"usr_2","live_wi_ids":[],` +
		`"last_heartbeat_at":"t2","stale":true}` +
		`],"count":2,"unknown_top":"kept","stale_after_seconds":120}`
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")

	raw, err := c.ListMachinesRaw()
	if err != nil {
		t.Fatalf("ListMachinesRaw: %v", err)
	}
	var out MachinesList
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/v1/machines" || gotAuth != "Bearer k" {
		t.Fatalf("request shape: %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	if out.Count != 2 || out.StaleAfterSeconds != 120 || len(out.Machines) != 2 {
		t.Fatalf("list decode: %+v", out)
	}
	m0 := out.Machines[0]
	if m0.ID != "m_1" || m0.RuntimeVersion != "go1.99" || m0.Mode != "agent" ||
		m0.UserID != "usr_1" || len(m0.LiveWIIDs) != 2 || m0.LiveWIIDs[0] != "wi_a" ||
		m0.LiveWIIDs[1] != "wi_b" || m0.HeartbeatAgeSeconds == nil ||
		*m0.HeartbeatAgeSeconds != 42 || m0.LastHeartbeatAt != "t1" || m0.Stale {
		t.Fatalf("m_1 decode: %+v", m0)
	}
	m1 := out.Machines[1]
	if m1.ID != "m_2" || m1.RuntimeVersion != "" || m1.Mode != "" ||
		len(m1.LiveWIIDs) != 0 || m1.HeartbeatAgeSeconds != nil || !m1.Stale {
		t.Fatalf("m_2 decode: %+v", m1)
	}

	if string(raw) != body {
		t.Fatalf("raw passthrough altered the body:\n got: %s\nwant: %s", raw, body)
	}
}

// TestGetMemoryPinsRouteAndRawBody (polyforge#132): the per-id memory
// read hits GET /api/v1/memories/{id} with the bearer key and hands the
// metadata JSON through raw; the ?format=content body call answers
// text/markdown and must come back UNDECODED (plain markdown is not
// JSON); a 404 surfaces the server's typed envelope.
func TestGetMemoryPinsRouteAndRawBody(t *testing.T) {
	const meta = `{"id":"mem_1","project":"p","type":"experience.debug","title":"the title",` +
		`"visibility":"project","content_sha":"abc","content_len":9,` +
		`"created_at":"2026-10-01T00:00:00Z","updated_at":"2026-10-02T00:00:00Z","future_field":true}`
	const body = "# heading\n\nbody text — not JSON at all\n"
	var gotMethod, gotURI, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotURI, gotAuth = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if r.URL.Query().Get("format") == "content" {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(meta))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")

	raw, err := c.GetMemory("mem_1")
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if gotMethod != "GET" || gotURI != "/api/v1/memories/mem_1" || gotAuth != "Bearer k" {
		t.Fatalf("metadata request shape: %s %s auth=%q", gotMethod, gotURI, gotAuth)
	}
	if string(raw) != meta {
		t.Fatalf("metadata passthrough altered the body:\n got: %s\nwant: %s", raw, meta)
	}

	got, err := c.GetMemoryContent("mem_1")
	if err != nil {
		t.Fatalf("GetMemoryContent: %v", err)
	}
	if gotURI != "/api/v1/memories/mem_1?format=content" {
		t.Fatalf("body request uri = %q, want /api/v1/memories/mem_1?format=content", gotURI)
	}
	if string(got) != body {
		t.Fatalf("content body altered:\n got: %q\nwant: %q", got, body)
	}
	if gotAccept != "" {
		t.Logf("note: body request carried Accept %q", gotAccept)
	}

	// 404: the typed envelope surfaces as *api.Error, not a generic error.
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"MEM-404","detail":"no such memory"}`))
	}))
	defer errSrv.Close()
	c2 := New(errSrv.URL, "k")
	if _, err := c2.GetMemory("ghost"); err == nil {
		t.Fatalf("GetMemory(ghost): want typed 404, got nil")
	} else if e, ok := err.(*api.Error); !ok || e.Code != "MEM-404" {
		t.Fatalf("GetMemory(ghost) error = %v (%T), want *api.Error MEM-404", err, err)
	}
	if _, err := c2.GetMemoryContent("ghost"); err == nil {
		t.Fatalf("GetMemoryContent(ghost): want typed 404, got nil")
	} else if e, ok := err.(*api.Error); !ok || e.Code != "MEM-404" {
		t.Fatalf("GetMemoryContent(ghost) error = %v (%T), want *api.Error MEM-404", err, err)
	}
}

// TestForceEndSessionPinsRouteAndBody: the client method POSTs
// /api/v1/work-items/{wi}/force-end-session with the bearer key and the
// {"reason"} body, and surfaces a refusal envelope as an error.
func TestForceEndSessionPinsRouteAndBody(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"id":"wi_x","status":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"code":"CORE-409","message":"not held","reason":"NOT_HELD"}}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")

	if err := c.ForceEndSession("wi_x", "holder machine lost"); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/api/v1/work-items/wi_x/force-end-session" || gotAuth != "Bearer k" {
		t.Fatalf("request shape: %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	if gotBody["reason"] != "holder machine lost" {
		t.Fatalf("body: %v", gotBody)
	}
	status = http.StatusConflict
	if err := c.ForceEndSession("wi_x", "again"); err == nil {
		t.Fatalf("refusal envelope must surface as an error")
	}
}

// TestCreateApiKeySendsName: the mint body carries the key name the
// server stores (an unnamed key keeps the field off the wire).
func TestCreateApiKeySendsName(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	if _, err := c.CreateApiKey(ApiKeyCreateRequest{UserID: "usr_1", Name: "ci"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateApiKey(ApiKeyCreateRequest{UserID: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0]["name"] != "ci" || bodies[0]["user_id"] != "usr_1" {
		t.Fatalf("named mint body = %v", bodies)
	}
	if _, ok := bodies[1]["name"]; ok {
		t.Fatalf("unnamed mint body carries name: %v", bodies[1])
	}
}
