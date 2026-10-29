package server

// web_test.go: the /web/ console wrapper — Basic
// authentication on /web/ only (username ignored, password = an api key
// through the shared key query; the JSON face stays Bearer-only), the
// 401/405 ordering, HEAD = GET headers with no body, the security
// header set on every response class (including odd, non-canonical
// paths — the N5 fix), the one embedded shell for every document path,
// and asset answers that are never the shell.

import (
	"encoding/base64"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// webReq issues one console request with an explicit Authorization
// header ("" = none) and answers status, headers and body.
func webReq(t *testing.T, e *srvEnv, method, path, authz string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, string(raw)
}

func webBasic(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// webHeadersOK asserts the exact header set on one /web/ response.
func webHeadersOK(t *testing.T, label string, h http.Header) {
	t.Helper()
	if got := h.Get("Content-Security-Policy"); got != webCSP {
		t.Fatalf("%s CSP = %q, want the exact literal", label, got)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Robots-Tag":           "noindex",
	} {
		if got := h.Get(name); got != want {
			t.Fatalf("%s %s = %q, want %q", label, name, got, want)
		}
	}
}

// webShellAsset answers the href of one asset the shell references.
func webShellAsset(t *testing.T, shell string) string {
	t.Helper()
	m := regexp.MustCompile(`(?:src|href)="([^"]*assets/[^"]+)"`).FindStringSubmatch(shell)
	if len(m) != 2 {
		t.Fatalf("shell references no assets/ file: %.400s", shell)
	}
	p := strings.ReplaceAll(m[1], "&amp;", "&")
	if !strings.HasPrefix(p, "/web/") {
		p = "/web/" + strings.TrimPrefix(p, "./")
	}
	return p
}

func TestWebAuthenticateBasicOnlyBeforeAnyWork(t *testing.T) {
	e := newSrvEnv(t)

	// No credential, a wrong password and a Bearer credential are one
	// byte-identical 401 class with the same challenge.
	for _, label := range []string{"document", "asset", "api"} {
		p := "/web/"
		switch label {
		case "asset":
			p = "/web/assets/missing.js"
		case "api":
			p = "/web/api/context"
		}
		c1, h1, b1 := webReq(t, e, "GET", p, "")
		c2, h2, b2 := webReq(t, e, "GET", p, webBasic("anyone", "not-a-key"))
		c3, _, b3 := webReq(t, e, "GET", p, "Bearer "+e.key)
		if c1 != http.StatusUnauthorized || c2 != http.StatusUnauthorized || c3 != http.StatusUnauthorized {
			t.Fatalf("%s: no credential = %d, wrong = %d, bearer = %d; want all 401", label, c1, c2, c3)
		}
		if b1 != b2 || b1 != b3 || b1 != webBodyAuth {
			t.Fatalf("%s: 401 bodies differ: %.80q %.80q %.80q", label, b1, b2, b3)
		}
		if h1.Get("WWW-Authenticate") == "" || h1.Get("WWW-Authenticate") != h2.Get("WWW-Authenticate") {
			t.Fatalf("%s: challenges differ or absent: %q vs %q", label, h1.Get("WWW-Authenticate"), h2.Get("WWW-Authenticate"))
		}
		if h1.Get("Set-Cookie") != "" {
			t.Fatalf("%s: a refused request set a cookie", label)
		}
		webHeadersOK(t, "401 "+label, h1)
	}

	// The username is ignored; the password is the api key.
	code, _, body := webReq(t, e, "GET", "/web/", webBasic("anyone", e.key))
	if code != http.StatusOK {
		t.Fatalf("valid key = %d: %.200s", code, body)
	}
	if c, _, _ := webReq(t, e, "GET", "/web/api/context", ""); c != http.StatusUnauthorized {
		t.Fatalf("every request re-authenticates; second request = %d", c)
	}
}

func TestWebMethodGateAfterAuthentication(t *testing.T) {
	e := newSrvEnv(t)
	// Authentication comes first: an unauthenticated POST answers 401.
	if c, _, _ := webReq(t, e, "POST", "/web/api/context", ""); c != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d, want 401", c)
	}
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, p := range []string{"/web/", "/web/api/context"} {
			req, err := http.NewRequest(m, e.ts.URL+p, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", webBasic("u", e.key))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s = %d, want 405", m, p, resp.StatusCode)
			}
			if allow := resp.Header.Get("Allow"); allow != "GET, HEAD" {
				t.Fatalf("%s %s Allow = %q", m, p, allow)
			}
			if string(body) != webBodyMethod {
				t.Fatalf("%s %s body = %q", m, p, body)
			}
			webHeadersOK(t, m+" "+p, resp.Header)
		}
	}
}

func TestWebShellAssetsAndDeepLinks(t *testing.T) {
	e := newSrvEnv(t)
	code, h, shell := webReq(t, e, "GET", "/web/", webBasic("u", e.key))
	if code != http.StatusOK || !strings.Contains(strings.ToLower(shell), "<!doctype html") {
		t.Fatalf("GET /web/ = %d, want the embedded shell: %.300s", code, shell)
	}
	webHeadersOK(t, "shell", h)

	// Every authenticated document path shares the one shell.
	for _, p := range []string{"/web", "/web/alpha/work-items", "/web/alpha/work-items/wi_1", "/web/no-such-view"} {
		c, _, b := webReq(t, e, "GET", p, webBasic("u", e.key))
		if c != http.StatusOK || b != shell {
			t.Fatalf("document %s = %d, want the one shell with 200", p, c)
		}
	}

	// An existing asset answers its own bytes with its content type.
	asset := webShellAsset(t, shell)
	c, ah, abody := webReq(t, e, "GET", asset, webBasic("u", e.key))
	if c != http.StatusOK || abody == shell {
		t.Fatalf("asset %s = %d, want its own bytes", asset, c)
	}
	if ct := ah.Get("Content-Type"); ct == "" {
		t.Fatalf("asset %s carries no content type", asset)
	}
	webHeadersOK(t, "asset", ah)

	// A missing asset is 404 bytes, never the shell.
	mc, mh, mbody := webReq(t, e, "GET", "/web/assets/web-missing.js", webBasic("u", e.key))
	if mc != http.StatusNotFound || strings.Contains(strings.ToLower(mbody), "<!doctype html") {
		t.Fatalf("missing asset = %d %.100q, want 404 bytes", mc, mbody)
	}
	webHeadersOK(t, "missing asset", mh)

	// /web/api is not shadowed by the shell (its routes land in later
	// steps; unknown api paths are the static 404 JSON).
	ac, _, ab := webReq(t, e, "GET", "/web/api/unknown", webBasic("u", e.key))
	if ac != http.StatusNotFound || ab != webBodyNotFound {
		t.Fatalf("unknown api path = %d %q, want the static 404", ac, ab)
	}
}

func TestWebHeadMirrorsGet(t *testing.T) {
	e := newSrvEnv(t)
	_, _, shell := webReq(t, e, "GET", "/web/", webBasic("u", e.key))
	asset := webShellAsset(t, shell)
	for _, p := range []string{"/web/", asset, "/web/api/context", "/web/assets/none.js"} {
		gc, gh, _ := webReq(t, e, "GET", p, webBasic("u", e.key))
		hc, hh, hb := webReq(t, e, "HEAD", p, webBasic("u", e.key))
		if hc != gc || hb != "" {
			t.Fatalf("HEAD %s = %d body %q; GET = %d", p, hc, hb, gc)
		}
		for _, name := range []string{"Content-Type", "Content-Length", "Content-Security-Policy", "Cache-Control", "Referrer-Policy", "X-Robots-Tag", "X-Content-Type-Options"} {
			if gh.Get(name) != hh.Get(name) {
				t.Fatalf("HEAD %s: header %s = %q, GET = %q", p, name, hh.Get(name), gh.Get(name))
			}
		}
	}
}

func TestWebOddPathsNeverEscapeTheHeaderSet(t *testing.T) {
	e := newSrvEnv(t)
	for _, p := range []string{
		"/web//",
		"/web//api/context",
		"/web/./",
		"/web/alpha/work-items/",
		"/web/alpha%2Fwork-items",
		"/web/%2e%2e/",
		"/web/assets//missing.js",
	} {
		req, err := http.NewRequest("GET", e.ts.URL+p, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", webBasic("u", e.key))
		resp, err := (&http.Client{
			// A redirect would be a failure of the no-redirect rule.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			t.Fatalf("GET %s = %d Location %q: the face emits no redirects", p, resp.StatusCode, resp.Header.Get("Location"))
		}
		webHeadersOK(t, "GET "+p, resp.Header)
	}
}

func TestWebOldViewPrefixAnswersLikeAnUnknownPath(t *testing.T) {
	e := newSrvEnv(t)
	// The deleted /view/ prefix is indistinguishable
	// from an unknown path: no redirect, no alias, no compatibility
	// shim — the plain mux 404, byte-identical with /nope.
	refCode, refH, refBody := webReq(t, e, "GET", "/w105-nope", webBasic("u", e.key))
	if refCode != http.StatusNotFound {
		t.Fatalf("unknown path = %d, want 404", refCode)
	}
	for _, p := range []string{"/view", "/view/", "/view/alpha", "/view/alpha/mem_1"} {
		code, h, body := webReq(t, e, "GET", p, webBasic("u", e.key))
		if code >= 300 && code < 400 {
			t.Fatalf("GET %s = %d Location %q: the old prefix must not redirect", p, code, h.Get("Location"))
		}
		if code != refCode || body != refBody {
			t.Fatalf("GET %s = %d %.100q; unknown path = %d %.100q: must be identical", p, code, body, refCode, refBody)
		}
		if h.Get("Content-Type") != refH.Get("Content-Type") {
			t.Fatalf("GET %s content type %q != %q", p, h.Get("Content-Type"), refH.Get("Content-Type"))
		}
	}
}
