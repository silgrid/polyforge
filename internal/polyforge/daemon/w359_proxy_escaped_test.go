package daemon

// w359_proxy_escaped_test.go: pins the data-plane proxy's byte-faithful path
// forwarding. The proxy used to hand Forward the DECODED
// r.PathValue("path"); Forward parses that string as a URL again, so an
// escaped reserved byte in a business path became URL syntax: "%23" a
// fragment (a slug's "#…" dropped), "%3F" a query, "%2F" a path separator,
// "%25" an invalid escape that read as "Hub unreachable" and degraded the
// Cell. The proxy must forward the request's escaped path unchanged.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// w359EscapedHub records what the proxy hands the Cell's forwarder and
// answers a successful business result.
type w359EscapedHub struct {
	*fakeHub
	got []string
}

func (h *w359EscapedHub) Forward(_ context.Context, method, pathQuery string, _ []byte, _ string) (int, string, []byte, error) {
	h.got = append(h.got, method+" "+pathQuery)
	return http.StatusOK, "application/json", []byte(`{}`), nil
}

func TestW359ProxyForwardsEscapedPath(t *testing.T) {
	cases := []struct {
		method, target, want string
	}{
		// the reported slug, read and write
		{http.MethodGet, "/aihub/api/v1/work-items/workbench%231", "GET /api/v1/work-items/workbench%231"},
		{http.MethodPost, "/aihub/api/v1/work-items/workbench%231/comments", "POST /api/v1/work-items/workbench%231/comments"},
		// the rest of the reserved class the same decoded-path step corrupted
		{http.MethodGet, "/aihub/api/v1/projects/a%3Fb/ready-queue?status=queued", "GET /api/v1/projects/a%3Fb/ready-queue?status=queued"},
		{http.MethodGet, "/aihub/api/v1/projects/a%2Fb/ready-queue", "GET /api/v1/projects/a%2Fb/ready-queue"},
		{http.MethodGet, "/aihub/api/v1/projects/a%25b/ready-queue", "GET /api/v1/projects/a%25b/ready-queue"},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			hub := &w359EscapedHub{fakeHub: newFakeHub()}
			// Server deliberately invalid: missingCredential's normalize check
			// returns "" here, and the test drives forwarding only.
			d := &Daemon{cfg: Config{Server: "://invalid"}, hub: hub}
			req := httptest.NewRequest(c.method, c.target, nil)
			// what the ServeMux wildcard carries after its one decode
			req.SetPathValue("path", strings.TrimPrefix(req.URL.Path, "/aihub/"))
			rec := httptest.NewRecorder()
			d.serveProxy(rec, req, "default")
			if rec.Code != http.StatusOK {
				t.Fatalf("proxy answered %d: %s", rec.Code, rec.Body.String())
			}
			if len(hub.got) != 1 {
				t.Fatalf("forwarder calls = %v, want one", hub.got)
			}
			if hub.got[0] != c.want {
				t.Fatalf("forwarded %q, want %q (the escaped business path, decoded nowhere)", hub.got[0], c.want)
			}
		})
	}
}
