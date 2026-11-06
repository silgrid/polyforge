package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
