package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// TestProxyForwardsThroughTheRoutedCell: a business request reaches the
// Hub of the Cell its header names, with that Cell's credential, and the
// status and body come back unchanged; an unreachable Hub is an explicit
// DAEMON-503 naming the Cell (and marks it degraded) while the other Cell
// keeps serving; only /api/v1/ paths are forwarded.
func TestProxyForwardsThroughTheRoutedCell(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	var gotAuth, gotPath string
	alphaHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.RequestURI()
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"echo":` + string(body) + `}`))
	}))
	defer alphaHub.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	th.cell("alpha").d.hub = aihubclient.New(alphaHub.URL, "alpha-key")
	th.cell("default").d.hub = aihubclient.New(deadURL, "def-key")

	call := func(profile, method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://daemon/aihub"+path, strings.NewReader(body))
		if profile != "" {
			req.Header.Set("X-PF-Profile", profile)
		}
		req.Header.Set("Authorization", "Bearer client-must-not-leak")
		resp, err := th.client("").HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	code, body := call("alpha", "POST", "/api/v1/projects?x=1", `{"name":"p"}`)
	if code != http.StatusTeapot || body != `{"echo":{"name":"p"}}` || gotAuth != "Bearer alpha-key" || gotPath != "/api/v1/projects?x=1" {
		t.Fatalf("forward: %d %s auth=%q path=%q", code, body, gotAuth, gotPath)
	}
	code, body = call("", "GET", "/api/v1/projects", "")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "DAEMON-503") || !strings.Contains(body, `Cell \"default\"`) {
		t.Fatalf("an unreachable Hub must be explicit: %d %s", code, body)
	}
	code, body = call("", "POST", "/api/v1/projects", `{}`)
	if !strings.Contains(body, "outcome of this side-effecting request is unknown") {
		t.Fatalf("a side-effecting request with an unknown outcome must say so: %d %s", code, body)
	}
	resp, err := th.client("").Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range resp.Profiles {
		switch p.Profile {
		case "default":
			if p.State != "degraded" || !strings.Contains(p.Reason, "unreachable") {
				t.Fatalf("default must read degraded: %+v", p)
			}
		case "alpha":
			if p.State != "serving" {
				t.Fatalf("alpha must keep serving: %+v", p)
			}
		}
	}
	if code, _ := call("alpha", "GET", "/web/index.html", ""); code != http.StatusBadRequest {
		t.Fatalf("a non-/api/v1/ path must not be forwarded: %d", code)
	}
	if code, body := call("ghost", "GET", "/api/v1/projects", ""); code != http.StatusNotFound || !strings.Contains(body, "cli-ghost.toml") {
		t.Fatalf("an unknown profile: %d %s", code, body)
	}
	var _ = cli.ProfilesResponse{}
}

// w107stamp is one request a recording test Hub kept.
type w107stamp struct {
	auth, path, body string
}

// TestW107SessionWriterStamp (polyforge#119 AC1, the session half): a
// session's memory create through the /aihub proxy reaches the Hub with
// the daemon's writer stamp — writer_kind "session" with the ROUTED
// Cell's machine and profile, so a forged writer in the request body is
// overridden, never relayed — and every other business field rides byte
// for byte (numbers keep their precision). The stamp keys on the DECODED
// path (the Hub decodes; "/api/v1/m%65mories" is the Hub's accepted
// alias and must not dodge it), not on the query, and never on another
// path or method. A body that is no complete JSON object is refused
// DAEMON-400 and nothing is forwarded (the owner's Q6 ruling: there is
// no stamping failure that silently forwards — unlike the exclusive
// create, which relays a non-object as sent).
func TestW107SessionWriterStamp(t *testing.T) {
	th := newTestHost(t, hostCell{profile: "default", machineID: "m-def"}, hostCell{profile: "alpha", machineID: "m-alpha"})
	var mu sync.Mutex
	var posts []w107stamp
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		posts = append(posts, w107stamp{auth: r.Header.Get("Authorization"), path: r.URL.RequestURI(), body: string(body)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mem_x"}`))
	}))
	defer hub.Close()
	for _, p := range []string{"alpha", "default"} {
		th.cell(p).d.hub = aihubclient.New(hub.URL, p+"-key")
	}
	call := func(profile, method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, "http://daemon/aihub"+path, strings.NewReader(body))
		if profile != "" {
			req.Header.Set(protocol.ProfileHeader, profile)
		}
		resp, err := th.client("").HTTP.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(posts)
	}
	// served parses one recorded POST body into raw fields.
	served := func(i int) map[string]json.RawMessage {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		var f map[string]json.RawMessage
		if err := json.Unmarshal([]byte(posts[i].body), &f); err != nil {
			t.Fatalf("recorded body %d is not JSON: %s", i, posts[i].body)
		}
		return f
	}
	kind := func(f map[string]json.RawMessage) string {
		t.Helper()
		var k string
		if err := json.Unmarshal(f["writer_kind"], &k); err != nil {
			t.Fatalf("writer_kind is not a string: %s", f["writer_kind"])
		}
		return k
	}
	details := func(f map[string]json.RawMessage) map[string]any {
		t.Helper()
		var d map[string]any
		if err := json.Unmarshal(f["writer_details"], &d); err != nil {
			t.Fatalf("writer_details is not an object: %s", f["writer_details"])
		}
		return d
	}

	// A session write that forges a step writer: the stamp overrides the
	// whole writer identity; the business fields — project, type, content,
	// tags, a number beyond float64 precision — ride byte for byte.
	const forged = `{"project":"proj","type":"experience.debug","content":"c","tags":["x"],` +
		`"n":12345678901234567890,"writer_kind":"step",` +
		`"writer_details":{"work_item":"wi_fake","step":"build","attempt":9,"model":"fake"}}`
	code, resp := call("alpha", "POST", "/api/v1/memories", forged)
	if code != 200 || !strings.Contains(resp, `"id":"mem_x"`) {
		t.Fatalf("session memory create = %d %s", code, resp)
	}
	mu.Lock()
	got := posts[0]
	mu.Unlock()
	if got.auth != "Bearer alpha-key" || got.path != "/api/v1/memories" {
		t.Fatalf("forwarded as %q %s", got.auth, got.path)
	}
	f := served(0)
	if kind(f) != "session" {
		t.Fatalf("writer_kind = %q, want session (the request's forged kind is overridden)", kind(f))
	}
	if d := details(f); d["machine"] != "m-alpha" || d["cell"] != "alpha" {
		t.Fatalf("writer_details = %v, want the ROUTED Cell alpha on m-alpha", d)
	}
	if string(f["project"]) != `"proj"` || string(f["type"]) != `"experience.debug"` ||
		string(f["content"]) != `"c"` || string(f["tags"]) != `["x"]` ||
		string(f["n"]) != "12345678901234567890" {
		t.Fatalf("business fields were not relayed byte for byte: %s", got.body)
	}
	if _, still := f["exclusive"]; still {
		t.Fatalf("the stamp must not touch unrelated fields: %s", got.body)
	}

	// The default Cell: its own machine id and the "default" profile —
	// the stamp follows the actual routing, never a hardcoded Cell.
	code, _ = call("", "POST", "/api/v1/memories", `{"project":"proj","type":"fact.note","content":"c2"}`)
	if code != 200 {
		t.Fatalf("default-Cell memory create = %d", code)
	}
	if f := served(1); kind(f) != "session" || details(f)["machine"] != "m-def" || details(f)["cell"] != "default" {
		t.Fatalf("default-Cell stamp: %s %s", f["writer_kind"], f["writer_details"])
	}

	// A query rides and never turns off the stamp; an escaped segment the
	// Hub decodes to the same route does not dodge it either.
	code, _ = call("alpha", "POST", "/api/v1/memories?foo=1", `{"project":"proj","type":"fact.note","content":"c3"}`)
	if code != 200 {
		t.Fatalf("memory create with a query = %d", code)
	}
	if f := served(2); kind(f) != "session" {
		t.Fatalf("a query must not dodge the stamp: %s", f["writer_kind"])
	}
	mu.Lock()
	if p := posts[2].path; p != "/api/v1/memories?foo=1" {
		t.Fatalf("the query was altered: %s", p)
	}
	mu.Unlock()
	code, _ = call("alpha", "POST", "/api/v1/m%65mories", `{"project":"proj","type":"fact.note","content":"c4"}`)
	if code != 200 {
		t.Fatalf("memory create through the escaped alias = %d", code)
	}
	if f := served(3); kind(f) != "session" {
		t.Fatalf("the escaped alias dodged the stamp: %s", f["writer_kind"])
	}

	// Nothing else is stamped: another POST path relays byte for byte
	// (the work-item create keeps its exclusive semantics), a GET of the
	// same path is untouched, and the redact subpath is not a create.
	before := count()
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/work-items", `{"project":"proj","bundle_name":"develop","goal":"g"}`},
		{"GET", "/api/v1/memories", `{"never":"parsed"}`},
		{"POST", "/api/v1/memories/mem_x/redact", `{"writer_kind":"step"}`},
	} {
		code, resp = call("alpha", tc.method, tc.path, tc.body)
		if code != 200 {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, code, resp)
		}
	}
	mu.Lock()
	for _, p := range posts[before:] {
		if p.body != `{"never":"parsed"}` && strings.Contains(p.body, `"writer_kind":"session"`) {
			t.Fatalf("%s was stamped: %s", p.path, p.body)
		}
	}
	mu.Unlock()

	// Q6: a body that is no complete JSON object is refused DAEMON-400 and
	// never forwarded — illegal JSON, an array, and JSON null alike.
	for _, body := range []string{`nope{`, `[1,2]`, `null`, ``} {
		before = count()
		code, resp = call("alpha", "POST", "/api/v1/memories", body)
		if code != http.StatusBadRequest || !strings.Contains(resp, "DAEMON-400") {
			t.Fatalf("body %q: %d %s, want DAEMON-400", body, code, resp)
		}
		if after := count(); after != before {
			t.Fatalf("body %q was forwarded (%d → %d)", body, before, after)
		}
	}
}
