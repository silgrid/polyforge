package git

// Table-driven HTTP-level tests for the git face's malformed-input
// classes: .git-suffix shape variants, path traversal, smart-HTTP
// method/target mismatch, and empty/absent Basic credentials — each
// rejected with its typed GIT-* envelope BEFORE the CGI is exec'd. The
// typed JSON envelope is itself the "CGI never ran" witness: http-backend
// answers in its own wire format, never as {"code":...,"detail":...}.

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitFaceMalformedRequestClasses(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)

	// The traversal class is asserted against a direct handler mount.
	// http.ServeMux (which the harness and production mount under) folds
	// a literal ".." segment into a 301 redirect before the proxy sees
	// it, so the proxy-level fence under test — the CGI must never run —
	// is exercised raw here; the %-encoded variant below proves the same
	// fence through the production-shaped mux mount, where no folding
	// happens (the mux cleans the escaped path, and %2e is not ".." to
	// it, while the proxy's r.URL.Path carries the decoded dots).
	raw := httptest.NewServer(http.HandlerFunc(e.mgr.HandleGit))
	t.Cleanup(raw.Close)

	const (
		refs   = "/info/refs"
		upload = "/git-upload-pack"
	)

	type tc struct {
		name        string
		raw         bool // hit the unmuxed direct mount
		method      string
		path        string
		noAuth      bool   // omit the Authorization header entirely
		basicUser   string // Basic username (when !noAuth)
		basicSecret string // Basic secret (when !noAuth)
		wantStatus  int
		wantCode    string
	}

	cases := []tc{
		// .git-suffix variants — valid credentials, so the rejection is
		// provably the path shape, not auth: typed GIT-404.
		{name: "missing .git suffix", method: "GET", path: "/git/" + wiID, basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "doubled .git suffix", method: "GET", path: "/git/" + wiID + ".git.git", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "empty repo segment", method: "GET", path: "/git/", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "garbage repo segment", method: "GET", path: "/git/garbage", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "missing wi_ prefix", method: "GET", path: "/git/foo.git", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},

		// Path traversal with VALID credentials — typed GIT-404 at the
		// proxy, never CGI.
		{name: "leading traversal", raw: true, method: "GET", path: "/git/../" + wiID + ".git", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "embedded traversal after repo", raw: true, method: "GET", path: "/git/" + wiID + ".git/../../etc", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "embedded traversal suffix-less repo", raw: true, method: "GET", path: "/git/" + wiID + "/../../etc/info/refs", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "encoded traversal through the mux", method: "GET", path: "/git/" + wiID + ".git/%2e%2e/%2e%2e/etc" + refs, basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},
		{name: "dot segment", raw: true, method: "GET", path: "/git/" + wiID + ".git/./info/refs", basicUser: wiID, basicSecret: secret, wantStatus: 404, wantCode: "GIT-404"},

		// Method/target mismatch — typed GIT-400.
		{name: "POST to info/refs", method: "POST", path: "/git/" + wiID + ".git" + refs, basicUser: wiID, basicSecret: secret, wantStatus: 400, wantCode: "GIT-400"},
		{name: "GET to git-upload-pack", method: "GET", path: "/git/" + wiID + ".git" + upload, basicUser: wiID, basicSecret: secret, wantStatus: 400, wantCode: "GIT-400"},
		{name: "PUT to info/refs", method: "PUT", path: "/git/" + wiID + ".git" + refs, basicUser: wiID, basicSecret: secret, wantStatus: 400, wantCode: "GIT-400"},
		{name: "unknown endpoint", method: "GET", path: "/git/" + wiID + ".git/garbage", basicUser: wiID, basicSecret: secret, wantStatus: 400, wantCode: "GIT-400"},

		// Empty/absent credentials — typed GIT-401.
		{name: "no Authorization header", method: "GET", path: "/git/" + wiID + ".git" + refs, noAuth: true, wantStatus: 401, wantCode: "GIT-401"},
		{name: "empty username", method: "GET", path: "/git/" + wiID + ".git" + refs, basicSecret: secret, wantStatus: 401, wantCode: "GIT-401"},
		{name: "empty secret", method: "GET", path: "/git/" + wiID + ".git" + refs, basicUser: wiID, wantStatus: 401, wantCode: "GIT-401"},
		{name: "empty username and secret", method: "GET", path: "/git/" + wiID + ".git" + refs, wantStatus: 401, wantCode: "GIT-401"},

		// Legitimate control: the smart-HTTP advertisement still answers.
		{name: "GET info/refs with valid credentials", method: "GET", path: "/git/" + wiID + ".git" + refs, basicUser: wiID, basicSecret: secret, wantStatus: 200},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := e.ts.URL
			if c.raw {
				base = raw.URL
			}
			req, err := http.NewRequest(c.method, base+c.path, nil)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if !c.noAuth {
				pair := c.basicUser + ":" + c.basicSecret
				req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(pair)))
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if resp.StatusCode != c.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", resp.StatusCode, c.wantStatus, body)
			}
			if c.wantStatus == http.StatusOK {
				return
			}
			if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json (CGI leak?)", ct)
			}
			var env struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("body %q is not the typed envelope: %v", body, err)
			}
			if env.Code != c.wantCode {
				t.Fatalf("envelope code = %q, want %q (body %q)", env.Code, c.wantCode, body)
			}
			if env.Detail == "" {
				t.Fatalf("envelope carries no detail: %q", body)
			}
			if c.wantCode == "GIT-401" {
				if ra := resp.Header.Get("WWW-Authenticate"); ra != `Basic realm="polyforge"` {
					t.Fatalf("WWW-Authenticate = %q, want Basic realm=\"polyforge\"", ra)
				}
			}
		})
	}
}

// TestGitFaceTraversalNeverReachesCGI pins the ordering fence directly:
// with VALID credentials a traversal-shaped path is answered by the
// proxy's typed 404 and execs no CGI process at all. The witness is the
// response body: http-backend's own 404 for a nonexistent repo is plain
// text, never the {"code":"GIT-404",...} JSON envelope.
func TestGitFaceTraversalNeverReachesCGI(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	raw := httptest.NewServer(http.HandlerFunc(e.mgr.HandleGit))
	t.Cleanup(raw.Close)
	req, err := http.NewRequest("GET", raw.URL+"/git/"+wiID+".git/../../etc/info/refs", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.SetBasicAuth(wiID, secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404 (body %q)", resp.StatusCode, body)
	}
	if !jsonCodeOf(t, body, "GIT-404") {
		t.Fatalf("body %q is not the typed GIT-404 envelope", body)
	}
}

// jsonCodeOf asserts body parses as the typed envelope carrying code.
func jsonCodeOf(t *testing.T, body []byte, code string) bool {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return false
	}
	return env.Code == code
}
