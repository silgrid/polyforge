package test

// w105_web_console_acceptance_test.go: the Go black-box acceptance suite for
// — the read-only aihub web console on /web/.
//
// Authored BEFORE `code` against artifacts/spec.md only (the plan pins the
// exact /web/api shapes). Every scenario drives the REAL aihub binary on
// loopback through real HTTP, with real users, keys, projects, work items,
// memories and machine rows. Nothing here references a symbol the
// pre-implementation tree lacks, so every baseline red is assertion-level
// (all /web/ paths are the mux's plain 404 today) and never build-level.
//
// Scenario document: artifacts/e2e-scenarios.md, in the same order as the
// TestW105* functions below. Browser-rendered halves (hostile corpus, layout,
// colour bands, disabled search box, #103 visual spec, N6 href escaping) are
// document-only scenarios driven by hand against the real binary; their
// baseline is the same "no /web/ page exists" red.

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

const (
	w105CSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
	// The 11 event types aihub records (grill evidence).
	w105EventTypes = "work_item_filed,wi_content_updated,session_claimed,session_attached,session_paused,session_ended,session_superseded,wi_resumed,work_item_canceled,work_item_closed,blocked_seal_abandoned"
)

var w105HTTP = &http.Client{
	Timeout: 30 * time.Second,
	// A redirect is a failure of the console's no-redirect rule: never follow.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func w105Basic(user, key string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+key))
}

// w105Req answers status, headers and body for one request. An empty auth
// means no Authorization header at all.
func w105Req(t *testing.T, inst *v1Install, method, path, auth, body string) (int, http.Header, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, inst.url+path, rdr)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := w105HTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, string(raw)
}

func w105Web(t *testing.T, inst *v1Install, method, path, key, body string) (int, http.Header, string) {
	t.Helper()
	auth := ""
	if key != "" {
		auth = w105Basic("anyone", key)
	}
	return w105Req(t, inst, method, path, auth, body)
}

func w105JSON(t *testing.T, what, raw string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("%s is not a JSON object: %v (%s)", what, err, raw)
	}
	return v
}

func w105Arr(t *testing.T, what string, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("%s is not an array: %#v", what, v)
	}
	return a
}

func w105Str(v any) string {
	s, _ := v.(string)
	return s
}

func w105Project(t *testing.T, inst *v1Install, name string) string {
	t.Helper()
	raw, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{Name: name})
	if err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
	var p struct{ ID string }
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" {
		t.Fatalf("project %s answer: %v (%s)", name, err, raw)
	}
	return p.ID
}

// w105UserKey mints one human user and one api key. scope, when set, is the
// key's project scope; memberProject/memberRole grant a project membership.
func w105UserKey(t *testing.T, inst *v1Install, name, role, scope, memberProject, memberRole string) (string, string) {
	t.Helper()
	raw, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: name + "@w105.invalid", UserType: "human", Role: role,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	var user struct{ ID string }
	if err := json.Unmarshal(raw, &user); err != nil || user.ID == "" {
		t.Fatalf("user %s answer: %v (%s)", name, err, raw)
	}
	req := aihubclient.ApiKeyCreateRequest{UserID: user.ID, Name: name + "-web"}
	if scope != "" {
		req.ProjectID = &scope
	}
	rawKey, err := inst.client.CreateApiKey(req)
	if err != nil {
		t.Fatalf("mint key for %s: %v", name, err)
	}
	var key struct{ Plain string }
	if err := json.Unmarshal(rawKey, &key); err != nil || key.Plain == "" {
		t.Fatalf("key answer for %s: %v (%s)", name, err, rawKey)
	}
	if memberProject != "" {
		members := []aihubclient.ProjectMember{{UserID: user.ID, Role: memberRole}}
		if _, err := inst.client.PatchProject(memberProject, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
			t.Fatalf("add %s to %s: %v", name, memberProject, err)
		}
	}
	return user.ID, key.Plain
}

// w105WI creates one work item and answers its id and slug.
func w105WI(t *testing.T, inst *v1Install, project, goal, content string, labels []string, priority string) (string, string) {
	t.Helper()
	req := aihubclient.CreateWorkItemRequest{Project: project, Goal: goal, Labels: labels, Priority: priority}
	if content != "" {
		req.Content = &content
	}
	raw, err := inst.client.CreateWorkItem(req)
	if err != nil {
		t.Fatalf("create work item %q: %v", goal, err)
	}
	var wi struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("work item %q answer: %v (%s)", goal, err, raw)
	}
	inst.trackWI(wi.ID)
	return wi.ID, wi.Slug
}

// w105WIBundle creates one work item that is an execution ticket (a bundle
// makes it a legal `blocks` blocker for the dependency read).
func w105WIBundle(t *testing.T, inst *v1Install, project, goal, bundle string) (string, string) {
	t.Helper()
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: project, Goal: goal, BundleName: &bundle,
	})
	if err != nil {
		t.Fatalf("create bundled work item %q: %v", goal, err)
	}
	var wi struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil || wi.ID == "" {
		t.Fatalf("work item %q answer: %v (%s)", goal, err, raw)
	}
	inst.trackWI(wi.ID)
	return wi.ID, wi.Slug
}

func w105Memory(t *testing.T, inst *v1Install, project, typ, title, content string) string {
	t.Helper()
	var out struct{ ID string }
	if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
		"project": project, "type": typ, "title": title, "content": content,
		"tags": []string{"w105"}, "visibility": "project",
	}, &out); err != nil || out.ID == "" {
		t.Fatalf("create memory %q: %v", title, err)
	}
	return out.ID
}

func w105DB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func w105Exec(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("db exec %q: %v", stmt, err)
	}
}

func w105HeadersOK(t *testing.T, where string, h http.Header) {
	t.Helper()
	if got := h.Get("Content-Security-Policy"); got != w105CSP {
		t.Fatalf("%s: CSP = %q, want the exact literal %q", where, got, w105CSP)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Robots-Tag":           "noindex",
	} {
		if got := h.Get(name); got != want {
			t.Fatalf("%s: %s = %q, want %q", where, name, got, want)
		}
	}
}

func w105Asset(t *testing.T, shell string) string {
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

func w105Items(t *testing.T, raw string) ([]map[string]any, *string) {
	t.Helper()
	var page struct {
		Items      []map[string]any `json:"items"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("list answer: %v (%s)", err, raw)
	}
	return page.Items, page.NextCursor
}

func w105ContainsFold(hay, needle string) bool {
	return strings.Contains(strings.ToLower(hay), strings.ToLower(needle))
}

// ---------------------------------------------------------------------------
// S1 — the old /view/ prefix is gone: it answers like any unknown path.
// ---------------------------------------------------------------------------

func TestW105OldViewPrefixIsIndistinguishableFromUnknownPath(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-view-gone", "admin", "", "", "")

	refCode, _, refBody := w105Web(t, inst, "GET", "/w105-unrelated-unknown-path", admin, "")
	if refCode != http.StatusNotFound {
		t.Fatalf("unrelated unknown path = %d, want 404: %.300s", refCode, refBody)
	}
	for _, p := range []string{"/view/", "/view/e2e", "/view/e2e/mem_w105"} {
		code, h, body := w105Web(t, inst, "GET", p, admin, "")
		if code == http.StatusMovedPermanently || code == http.StatusPermanentRedirect ||
			code == http.StatusFound || code == http.StatusTemporaryRedirect {
			t.Fatalf("GET %s = %d %q: the old prefix must not redirect", p, code, h.Get("Location"))
		}
		if code != refCode || body != refBody {
			t.Fatalf("GET %s = %d %.200q; unrelated unknown path = %d %.200q: the old prefix must answer identically",
				p, code, body, refCode, refBody)
		}
		hcode, hh, hbody := w105Web(t, inst, "HEAD", p, admin, "")
		if hcode != refCode || hbody != "" || hh.Get("Content-Security-Policy") != h.Get("Content-Security-Policy") {
			t.Fatalf("HEAD %s = %d body %q: must match GET status/headers with no body", p, hcode, hbody)
		}
	}
}

// ---------------------------------------------------------------------------
// S2 — every /web/ response class carries the fixed header set.
// ---------------------------------------------------------------------------

func TestW105EveryResponseClassCarriesTheSecurityHeaders(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-headers", "admin", "", "", "")
	wi, _ := w105WI(t, inst, v1Project, "w105 header item", "", nil, "normal")

	code, shellH, shell := w105Web(t, inst, "GET", "/web/", admin, "")
	if code != http.StatusOK {
		t.Fatalf("GET /web/ = %d, want an authenticated shell: %.300s", code, shell)
	}
	asset := w105Asset(t, shell)
	classes := []struct {
		name, method, path, key string
		want                    int
	}{
		{"200 document", "GET", "/web/", admin, http.StatusOK},
		{"200 asset", "GET", asset, admin, http.StatusOK},
		{"200 JSON", "GET", "/web/api/context", admin, http.StatusOK},
		{"401 challenge", "GET", "/web/", "", http.StatusUnauthorized},
		{"404 unknown object", "GET", "/web/api/work-items/wi_w105_missing?project=" + url.QueryEscape(v1Project), admin, http.StatusNotFound},
		{"405 unsafe method", "POST", "/web/api/context", admin, http.StatusMethodNotAllowed},
	}
	for _, c := range classes {
		code, h, body := w105Web(t, inst, c.method, c.path, c.key, "")
		if code != c.want {
			t.Fatalf("%s: %s %s = %d, want %d: %.300s", c.name, c.method, c.path, code, c.want, body)
		}
		w105HeadersOK(t, c.name, h)
		if strings.Contains(body, admin) || strings.Contains(fmt.Sprint(h), admin) {
			t.Fatalf("%s: the presented api key appears in the response", c.name)
		}
	}
	_, jsonH, jsonBody := w105Web(t, inst, "GET", "/web/api/work-items/"+wi+"?project="+url.QueryEscape(v1Project), admin, "")
	if jsonH.Get("Content-Type") == "" {
		t.Fatalf("readable work-item detail answered without a content type: %s", jsonBody)
	}
	_, _, shellAgain := w105Web(t, inst, "GET", "/web/", admin, "")
	logRaw, err := os.ReadFile(inst.aihubLog)
	if err != nil {
		t.Fatalf("read the aihub log: %v", err)
	}
	for _, surface := range []string{shell, shellAgain, jsonBody, fmt.Sprint(shellH), string(logRaw)} {
		if strings.Contains(surface, admin) {
			t.Fatalf("the presented api key leaks into a response or the aihub log")
		}
	}
}

// ---------------------------------------------------------------------------
// S3 — missing and wrong Basic credentials are one class; no session state.
// ---------------------------------------------------------------------------

func TestW105BasicAuthGuidesAndReauthenticates(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-auth", "admin", "", "", "")
	// The wrong-password fixture (owner-approved rebaseline): a fixed
	// invalid string, NOT a minted key — a valid key must authenticate
	// (S4 asserts the identical key shape answers 200 on /web/api/context).
	wrong := "w105-definitely-not-an-api-key"

	for _, p := range []string{"/web/", "/web/assets/w105-missing.js", "/web/api/context"} {
		c1, h1, b1 := w105Web(t, inst, "GET", p, "", "")
		c2, h2, b2 := w105Web(t, inst, "GET", p, wrong, "")
		if c1 != http.StatusUnauthorized || c2 != http.StatusUnauthorized {
			t.Fatalf("%s: no credential = %d, wrong password = %d; both must be 401", p, c1, c2)
		}
		if b1 != b2 {
			t.Fatalf("%s: missing-credential body %.200q != wrong-credential body %.200q", p, b1, b2)
		}
		if h1.Get("WWW-Authenticate") == "" || h1.Get("WWW-Authenticate") != h2.Get("WWW-Authenticate") {
			t.Fatalf("%s: challenges differ or are absent: %q vs %q", p, h1.Get("WWW-Authenticate"), h2.Get("WWW-Authenticate"))
		}
		if !strings.Contains(strings.ToLower(h1.Get("WWW-Authenticate")), "basic") {
			t.Fatalf("%s: challenge is not Basic: %q", p, h1.Get("WWW-Authenticate"))
		}
		if h1.Get("Set-Cookie") != "" || h2.Get("Set-Cookie") != "" {
			t.Fatalf("%s: a refused request set a cookie", p)
		}
	}

	code, h, body := w105Web(t, inst, "GET", "/web/api/context", admin, "")
	if code != http.StatusOK {
		t.Fatalf("valid Basic key = %d, want 200: %.300s", code, body)
	}
	if h.Get("Set-Cookie") != "" {
		t.Fatalf("v1 must not issue a session cookie, got %q", h.Get("Set-Cookie"))
	}
	if c, _, _ := w105Web(t, inst, "GET", "/web/api/context", "", ""); c != http.StatusUnauthorized {
		t.Fatalf("a second request without a credential = %d; every request must re-authenticate", c)
	}
	if c, _, _ := w105Req(t, inst, "GET", "/web/api/context", "Bearer "+admin, ""); c != http.StatusUnauthorized {
		t.Fatalf("Bearer-only credential on /web = %d, want the Basic 401 challenge", c)
	}
}

// ---------------------------------------------------------------------------
// S4 — a valid key sees exactly its CLI/API read gates (context/picker set).
// ---------------------------------------------------------------------------

func TestW105ReadGatesMatchTheApi(t *testing.T) {
	inst := newV1Install(t)
	foreign := w105Project(t, inst, "w105-other")
	_, admin := w105UserKey(t, inst, "w105-gate-admin", "admin", "", "", "")
	_, member := w105UserKey(t, inst, "w105-gate-member", "writer", "", v1Project, "viewer")
	_, scoped := w105UserKey(t, inst, "w105-gate-scoped", "writer", foreign, foreign, "viewer")
	_, nobody := w105UserKey(t, inst, "w105-gate-nobody", "writer", "", "", "")

	names := func(code int, raw, what string) map[string]bool {
		t.Helper()
		if code != http.StatusOK {
			t.Fatalf("%s = %d, want 200: %.300s", what, code, raw)
		}
		out := map[string]bool{}
		for _, p := range w105Arr(t, what+".projects", w105JSON(t, what, raw)["projects"]) {
			out[w105Str(p.(map[string]any)["name"])] = true
		}
		return out
	}

	code, _, raw := w105Web(t, inst, "GET", "/web/api/context", admin, "")
	if got := names(code, raw, "admin context"); !got[v1Project] || !got["w105-other"] {
		t.Fatalf("admin context = %v, want both projects", got)
	}
	code, _, raw = w105Web(t, inst, "GET", "/web/api/context", member, "")
	if got := names(code, raw, "member context"); !got[v1Project] || got["w105-other"] {
		t.Fatalf("member context = %v, want only %s", got, v1Project)
	}
	code, _, raw = w105Web(t, inst, "GET", "/web/api/context", scoped, "")
	if got := names(code, raw, "scoped context"); !got["w105-other"] || got[v1Project] {
		t.Fatalf("scoped context = %v, want only w105-other", got)
	}
	code, _, raw = w105Web(t, inst, "GET", "/web/api/context", nobody, "")
	if got := names(code, raw, "nobody context"); len(got) != 0 {
		t.Fatalf("a key with no readable project lists %v", got)
	}
}

// ---------------------------------------------------------------------------
// S5 — unknown and unreadable share one byte-identical 404 / one not-found.
// ---------------------------------------------------------------------------

func TestW105UnknownAndUnreadableShareOne404(t *testing.T) {
	inst := newV1Install(t)
	foreignProject := w105Project(t, inst, "w105-secret")
	foreignWI, _ := w105WI(t, inst, "w105-secret", "w105 foreign secret goal", "", nil, "normal")
	ownWI, _ := w105WI(t, inst, v1Project, "w105 own readable goal", "", nil, "normal")
	_, member := w105UserKey(t, inst, "w105-nonleak", "writer", "", v1Project, "viewer")

	code, _, raw := w105Web(t, inst, "GET", "/web/api/work-items/"+ownWI+"?project="+url.QueryEscape(v1Project), member, "")
	if code != http.StatusOK {
		t.Fatalf("member's readable item = %d, want 200: %.300s", code, raw)
	}
	q := "?project=" + url.QueryEscape("w105-secret") + "&"
	cUnread, _, bUnread := w105Web(t, inst, "GET", "/web/api/work-items/"+foreignWI+q, member, "")
	cMissing, _, bMissing := w105Web(t, inst, "GET", "/web/api/work-items/wi_w105_absent"+q, member, "")
	if cUnread != http.StatusNotFound || cMissing != http.StatusNotFound || bUnread != bMissing {
		t.Fatalf("unreadable = %d %.200q, missing = %d %.200q: both must be the identical 404",
			cUnread, bUnread, cMissing, bMissing)
	}
	if strings.Contains(bUnread, "foreign secret goal") || strings.Contains(bUnread, foreignWI) {
		t.Fatalf("the 404 leaks the hidden object: %s", bUnread)
	}

	// A non-admin asking for machines gets that same 404 class.
	cMach, _, bMach := w105Web(t, inst, "GET", "/web/api/machines", member, "")
	if cMach != http.StatusNotFound || bMach != bUnread {
		t.Fatalf("non-admin machines = %d %.200q, want the identical 404 %.200q", cMach, bMach, bUnread)
	}

	// Document deep links serve one shell; the client renders one not-found view.
	d1, _, s1 := w105Web(t, inst, "GET", "/web/w105-secret/work-items/"+foreignWI, member, "")
	d2, _, s2 := w105Web(t, inst, "GET", "/web/w105-secret/work-items/wi_w105_absent", member, "")
	if d1 != http.StatusOK || d2 != http.StatusOK || s1 != s2 {
		t.Fatalf("document deep links = %d/%d: both must be the one shell with 200", d1, d2)
	}
	_ = foreignProject
}

// ---------------------------------------------------------------------------
// S6 — GET/HEAD only, authentication before the method gate, HEAD mirrors GET.
// ---------------------------------------------------------------------------

func TestW105MethodGateAfterAuthAndHeadMirrorsGet(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-methods", "admin", "", "", "")
	code, _, shell := w105Web(t, inst, "GET", "/web/", admin, "")
	if code != http.StatusOK {
		t.Fatalf("GET /web/ = %d, want 200: %.300s", code, shell)
	}
	asset := w105Asset(t, shell)

	for _, p := range []string{"/web/", asset, "/web/api/context"} {
		gc, gh, _ := w105Web(t, inst, "GET", p, admin, "")
		hc, hh, hb := w105Web(t, inst, "HEAD", p, admin, "")
		if hc != gc || hb != "" {
			t.Fatalf("HEAD %s = %d body %q; GET = %d", p, hc, hb, gc)
		}
		for _, name := range []string{"Content-Type", "Content-Length", "Content-Security-Policy", "Cache-Control", "Referrer-Policy", "X-Robots-Tag", "X-Content-Type-Options"} {
			if gh.Get(name) != hh.Get(name) {
				t.Fatalf("HEAD %s: header %s = %q, GET = %q", p, name, hh.Get(name), gh.Get(name))
			}
		}
	}

	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, p := range []string{"/web/", "/web/api/context"} {
			c, h, _ := w105Web(t, inst, m, p, admin, "{}")
			if c != http.StatusMethodNotAllowed {
				t.Fatalf("authenticated %s %s = %d, want 405", m, p, c)
			}
			if allow := h.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "HEAD") {
				t.Fatalf("%s %s: Allow = %q, want GET, HEAD", m, p, allow)
			}
			w105HeadersOK(t, m+" "+p, h)
		}
	}
	if c, _, _ := w105Web(t, inst, "POST", "/web/api/context", "", "{}"); c != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d, want the 401 challenge before the method gate", c)
	}
}

// ---------------------------------------------------------------------------
// S7 — embedded shell, assets, deep links; assets are not shadowed by shell.
// ---------------------------------------------------------------------------

func TestW105EmbeddedShellAssetsAndDeepLinks(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-shell", "admin", "", "", "")
	wi, _ := w105WI(t, inst, v1Project, "w105 deep link item", "", nil, "normal")

	code, _, shell := w105Web(t, inst, "GET", "/web/", admin, "")
	if code != http.StatusOK || !strings.Contains(strings.ToLower(shell), "<!doctype html") {
		t.Fatalf("GET /web/ = %d, want the embedded HTML shell: %.300s", code, shell)
	}
	for _, ext := range []string{`src="http://`, `src="https://`, `href="http://`, `href="https://`} {
		if strings.Contains(shell, ext) {
			t.Fatalf("the shell references an external origin (%s); no request may leave the serving origin", ext)
		}
	}
	for _, p := range []string{
		"/web/" + v1Project + "/work-items/" + wi,
		"/web/" + v1Project + "/no-such-view",
	} {
		c, _, b := w105Web(t, inst, "GET", p, admin, "")
		if c != http.StatusOK || b != shell {
			t.Fatalf("deep link %s = %d, want the one shell with 200", p, c)
		}
	}

	asset := w105Asset(t, shell)
	c, h, body := w105Web(t, inst, "GET", asset, admin, "")
	if c != http.StatusOK || body == shell {
		t.Fatalf("existing asset %s = %d, want its own bytes: %.200s", asset, c, body)
	}
	ct := h.Get("Content-Type")
	if !strings.Contains(ct, "javascript") && !strings.Contains(ct, "css") && !strings.Contains(ct, "font") && !strings.Contains(ct, "image") {
		t.Fatalf("asset %s Content-Type = %q", asset, ct)
	}
	miss, _, mb := w105Web(t, inst, "GET", "/web/assets/w105-does-not-exist.js", admin, "")
	if miss != http.StatusNotFound || strings.Contains(strings.ToLower(mb), "<!doctype html") {
		t.Fatalf("missing asset = %d %.200q, want 404 bytes, never the shell", miss, mb)
	}
	w105HeadersOK(t, "missing asset", h)
}

// ---------------------------------------------------------------------------
// S8 — work-item list: status filter, case-insensitive goal/content search,
// both composed; no cross-project rows.
// ---------------------------------------------------------------------------

func TestW105WorkItemListFiltersAndSearch(t *testing.T) {
	inst := newV1Install(t)
	w105Project(t, inst, "w105-list-foreign")
	alpha, _ := w105WI(t, inst, v1Project, "W105 Alpha Needle goal", "", nil, "normal")
	beta, _ := w105WI(t, inst, v1Project, "W105 beta title", "body with NEEDLE inside", nil, "high")
	_, _ = w105WI(t, inst, v1Project, "W105 gamma title", "", nil, "low")
	labelOnly, _ := w105WI(t, inst, v1Project, "W105 delta title", "", []string{"needle-only-label"}, "normal")
	foreignWI, _ := w105WI(t, inst, "w105-list-foreign", "W105 foreign needle", "", nil, "normal")
	if _, err := inst.client.CancelWorkItem(beta); err != nil {
		t.Fatalf("cancel beta: %v", err)
	}
	if _, err := inst.client.WrapWorkItem(alpha, api.WrapRequest{}); err != nil {
		t.Fatalf("wrap alpha: %v", err)
	}

	_, admin := w105UserKey(t, inst, "w105-list", "admin", "", "", "")
	code, _, raw := w105Web(t, inst, "GET", "/web/api/work-items?project="+url.QueryEscape(v1Project), admin, "")
	if code != http.StatusOK {
		t.Fatalf("unfiltered list = %d, want 200: %.300s", code, raw)
	}
	items, _ := w105Items(t, raw)
	byID := map[string]map[string]any{}
	statuses := map[string]bool{}
	var lastCreated string
	for _, it := range items {
		id := w105Str(it["id"])
		byID[id] = it
		statuses[w105Str(it["status"])] = true
		created := w105Str(it["created_at"])
		if lastCreated != "" && created > lastCreated {
			t.Fatalf("list is not newest-first: %s came after %s", created, lastCreated)
		}
		lastCreated = created
		if id == foreignWI {
			t.Fatalf("the project list leaked a foreign work item: %s", raw)
		}
	}
	if !statuses["queued"] || !statuses["canceled"] || !statuses["wrapped"] {
		t.Fatalf("default list statuses = %v, want all statuses present", statuses)
	}

	code, _, raw = w105Web(t, inst, "GET", "/web/api/work-items?project="+url.QueryEscape(v1Project)+"&status=canceled", admin, "")
	if code != http.StatusOK {
		t.Fatalf("status filter = %d: %.300s", code, raw)
	}
	items, _ = w105Items(t, raw)
	if len(items) != 1 || w105Str(items[0]["id"]) != beta {
		t.Fatalf("status=canceled = %v, want only %s", items, beta)
	}

	for _, q := range []string{"needle", "NEEDLE", "nEeDlE"} {
		code, _, raw = w105Web(t, inst, "GET", "/web/api/work-items?project="+url.QueryEscape(v1Project)+"&q="+url.QueryEscape(q), admin, "")
		if code != http.StatusOK {
			t.Fatalf("search %q = %d: %.300s", q, code, raw)
		}
		items, _ = w105Items(t, raw)
		got := map[string]bool{}
		for _, it := range items {
			got[w105Str(it["id"])] = true
		}
		if !got[alpha] || !got[beta] || got[labelOnly] || got[foreignWI] {
			t.Fatalf("search %q = %v, want alpha+beta, not the label-only or foreign item (%s)", q, got, raw)
		}
	}

	code, _, raw = w105Web(t, inst, "GET", "/web/api/work-items?project="+url.QueryEscape(v1Project)+"&status=canceled&q=needle", admin, "")
	if code != http.StatusOK {
		t.Fatalf("composed filter = %d: %.300s", code, raw)
	}
	items, _ = w105Items(t, raw)
	if len(items) != 1 || w105Str(items[0]["id"]) != beta {
		t.Fatalf("status+search = %v, want only the canceled beta item", items)
	}
}

// ---------------------------------------------------------------------------
// S9 — a 45-item list pages as 20/20/5, exactly once, stable across a change.
// ---------------------------------------------------------------------------

func TestW105WorkItemListPagesExactlyOnce(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-pages", "admin", "", "", "")
	ids := make([]string, 0, 45)
	for i := 0; i < 45; i++ {
		id, _ := w105WI(t, inst, v1Project, fmt.Sprintf("w105 page item %02d", i), "", nil, "normal")
		ids = append(ids, id)
	}

	seen := map[string]int{}
	sizes := []int{}
	cursor := ""
	for page := 1; page <= 6; page++ {
		p := "/web/api/work-items?project=" + url.QueryEscape(v1Project)
		if cursor != "" {
			p += "&after=" + url.QueryEscape(cursor)
		}
		code, _, raw := w105Web(t, inst, "GET", p, admin, "")
		if code != http.StatusOK {
			t.Fatalf("page %d = %d: %.300s", page, code, raw)
		}
		items, next := w105Items(t, raw)
		sizes = append(sizes, len(items))
		for _, it := range items {
			id := w105Str(it["id"])
			seen[id]++
			if seen[id] > 1 {
				t.Fatalf("item %s appears on two pages (duplicate in the walk)", id)
			}
		}
		if page == 1 && len(items) > 0 {
			// An item changing between pages must neither duplicate nor hide another.
			if _, err := inst.client.CancelWorkItem(w105Str(items[0]["id"])); err != nil {
				t.Fatalf("change the first page's item between pages: %v", err)
			}
		}
		if next == nil {
			if page != 3 {
				t.Fatalf("walk ended on page %d with sizes %v, want 20/20/5", page, sizes)
			}
			break
		}
		cursor = *next
	}
	if fmt.Sprint(sizes) != "[20 20 5]" {
		t.Fatalf("page sizes = %v, want [20 20 5]", sizes)
	}
	if len(seen) != 45 {
		t.Fatalf("the walk covered %d distinct items, want 45", len(seen))
	}
	for _, id := range ids {
		if seen[id] == 0 {
			t.Fatalf("the walk skipped item %s", id)
		}
	}
}

// ---------------------------------------------------------------------------
// S10 — dependencies are shown both ways; an unreadable target is id-only.
// ---------------------------------------------------------------------------

func TestW105DependenciesPointBothWaysWithoutLeaking(t *testing.T) {
	inst := newV1Install(t)
	foreign := w105Project(t, inst, "w105-dep-foreign")
	// The origin is the OUTGOING blocker (owner-approved rebaseline): the
	// bundled execution ticket is the blocking side the blocks rule
	// requires, so the detail's `blocking` holds the readable target and
	// `blocked_by` the SQL-seeded hidden blocker — the one-incoming,
	// one-outgoing state the assertions describe.
	origin, _ := w105WIBundle(t, inst, v1Project, "w105 dependency origin", "autopilot")
	target, targetSlug := w105WI(t, inst, v1Project, "w105 readable target", "", nil, "normal")
	hidden, _ := w105WI(t, inst, "w105-dep-foreign", "w105 hidden secret blocker", "", nil, "normal")
	if _, err := inst.client.AddBlocksDependency(origin, target); err != nil {
		t.Fatalf("add readable dependency: %v", err)
	}
	db := w105DB(t, inst)
	w105Exec(t, db, `INSERT INTO wi_dependencies(blocking_wi_id, blocked_wi_id, kind, created_at) VALUES (?1, ?2, 'blocks', ?3)`,
		hidden, origin, time.Now().UTC().Format(time.RFC3339Nano))
	_ = foreign

	_, member := w105UserKey(t, inst, "w105-dep-member", "writer", "", v1Project, "viewer")
	code, _, raw := w105Web(t, inst, "GET", "/web/api/work-items/"+origin+"?project="+url.QueryEscape(v1Project), member, "")
	if code != http.StatusOK {
		t.Fatalf("detail = %d, want 200: %.300s", code, raw)
	}
	detail := w105JSON(t, "detail", raw)
	blocking := w105Arr(t, "blocking", detail["blocking"])
	if len(blocking) != 1 {
		t.Fatalf("outgoing dependencies = %v, want the readable target", blocking)
	}
	if got := w105Str(blocking[0].(map[string]any)["id"]); got != target {
		t.Fatalf("outgoing target = %q, want %s", got, target)
	}
	if !strings.Contains(raw, targetSlug) {
		t.Fatalf("a readable dependency target is not linked (%s missing): %s", targetSlug, raw)
	}
	blockedBy := w105Arr(t, "blocked_by", detail["blocked_by"])
	if len(blockedBy) != 1 {
		t.Fatalf("incoming dependencies = %v, want the hidden blocker", blockedBy)
	}
	edge := blockedBy[0].(map[string]any)
	if got := w105Str(edge["id"]); got != hidden {
		t.Fatalf("incoming target = %q, want %s", got, hidden)
	}
	for _, leak := range []string{"slug", "goal", "title", "project", "status"} {
		if _, ok := edge[leak]; ok {
			t.Fatalf("an unreadable dependency target carries %q: %#v", leak, edge)
		}
	}
	if strings.Contains(raw, "hidden secret blocker") {
		t.Fatalf("the detail leaks the unreadable target's goal: %s", raw)
	}
}

// ---------------------------------------------------------------------------
// S11 — timeline covers the 11 aihub events; sensitive columns stay hidden.
// ---------------------------------------------------------------------------

func TestW105TimelineCoversEventsAndHidesSensitiveColumns(t *testing.T) {
	inst := newV1Install(t)
	const (
		hashSentinel    = "w105hashsentinel0123456789abcdef"
		fingerSentinel  = "w105fingerprintsentinel"
		machineSentinel = "w105machinesentinel"
		attrsSentinel   = "w105attrssentinel"
	)
	wi, _ := w105WI(t, inst, v1Project, "w105 hostile <script>alert(1)</script> goal",
		"# Heading\n\n<script>alert(2)</script>\n\n[click](javascript:alert(3))\n\n<img src=x onerror=alert(4)>", nil, "normal")
	db := w105DB(t, inst)
	w105Exec(t, db, `UPDATE work_items SET session_hash=?2, session_fingerprint=?3, session_machine=?4, attrs=?5 WHERE id=?1`,
		wi, hashSentinel, fingerSentinel, machineSentinel, `{"w105":"`+attrsSentinel+`"}`)

	var actor string
	if err := db.QueryRow(`SELECT reporter FROM work_items WHERE id=?1`, wi).Scan(&actor); err != nil {
		t.Fatalf("read reporter: %v", err)
	}
	types := strings.Split(w105EventTypes, ",")
	for i, typ := range types {
		created := time.Now().UTC().Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano)
		payload := fmt.Sprintf(`{"note":"payload %02d <script>alert(9)</script>","session_hash":"%s","machine_id":"%s"}`,
			i, hashSentinel, machineSentinel)
		w105Exec(t, db, `INSERT INTO wi_events(work_item_id, created_at, actor, event_type, payload) VALUES (?1, ?2, ?3, ?4, ?5)`,
			wi, created, actor, typ, payload)
	}

	_, admin := w105UserKey(t, inst, "w105-timeline", "admin", "", "", "")
	code, _, raw := w105Web(t, inst, "GET", "/web/api/work-items/"+wi+"?project="+url.QueryEscape(v1Project), admin, "")
	if code != http.StatusOK {
		t.Fatalf("detail = %d, want 200: %.300s", code, raw)
	}
	detail := w105JSON(t, "detail", raw)
	html := w105Str(detail["html"])
	if html == "" {
		t.Fatalf("detail carries no sanitized html: %s", raw)
	}
	for _, active := range []string{"<script", "<img", "href=\"javascript:", "onerror="} {
		if strings.Contains(strings.ToLower(html), active) {
			t.Fatalf("server-sanitized html is not inert (%q present): %s", active, html)
		}
	}
	events := w105Arr(t, "events", detail["events"])
	if len(events) < len(types) {
		t.Fatalf("timeline has %d events, want all %d: %s", len(events), len(types), raw)
	}
	got := map[string]bool{}
	var last string
	for _, e := range events {
		m := e.(map[string]any)
		typ := w105Str(m["type"])
		if typ == "" {
			typ = w105Str(m["event_type"])
		}
		got[typ] = true
		at := w105Str(m["created_at"])
		if at == "" {
			at = w105Str(m["at"])
		}
		if at == "" {
			at = w105Str(m["time"])
		}
		if last != "" && at < last {
			t.Fatalf("timeline is not ascending: %s came after %s", at, last)
		}
		last = at
		if w105Str(m["actor"]) == "" {
			t.Fatalf("timeline event %q has no actor: %#v", typ, m)
		}
		if _, ok := m["payload"]; !ok {
			t.Fatalf("timeline event %q has no escaped payload: %#v", typ, m)
		}
	}
	for _, typ := range types {
		if !got[typ] {
			t.Fatalf("timeline misses aihub event type %q; got %v", typ, got)
		}
	}
	if strings.Contains(raw, "step") && strings.Contains(raw, "progress") {
		t.Fatalf("the timeline appears to carry daemon step progress: %s", raw)
	}
	for _, leak := range []string{hashSentinel, fingerSentinel, machineSentinel, attrsSentinel} {
		if strings.Contains(raw, leak) {
			t.Fatalf("the detail leaks sensitive column value %q", leak)
		}
	}
	for _, key := range []string{`"session_hash"`, `"attrs"`, `"session_fingerprint"`, `"session_machine"`} {
		if strings.Contains(raw, key) {
			t.Fatalf("the detail JSON carries sensitive key %s: %s", key, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// S12 — global search is grouped, capped at 20 per group, project-scoped.
// ---------------------------------------------------------------------------

func TestW105GlobalSearchGroupsAndCaps(t *testing.T) {
	inst := newV1Install(t)
	foreign := w105Project(t, inst, "w105-search-foreign")
	for i := 0; i < 30; i++ {
		w105WI(t, inst, v1Project, fmt.Sprintf("w105 searchable item %02d", i), "needle body", nil, "normal")
	}
	for i := 0; i < 30; i++ {
		w105Memory(t, inst, v1Project, "fact.note", fmt.Sprintf("w105 memory needle %02d", i), "needle memory body")
	}
	foreignID, _ := w105WI(t, inst, "w105-search-foreign", "w105 foreign needle", "needle", nil, "normal")
	_ = foreign

	_, admin := w105UserKey(t, inst, "w105-search", "admin", "", "", "")
	code, _, raw := w105Web(t, inst, "GET", "/web/api/search?project="+url.QueryEscape(v1Project)+"&q=needle", admin, "")
	if code != http.StatusOK {
		t.Fatalf("global search = %d, want 200: %.300s", code, raw)
	}
	result := w105JSON(t, "search", raw)
	wis := w105Arr(t, "work_items", result["work_items"])
	mems := w105Arr(t, "memories", result["memories"])
	if len(wis) != 20 || len(mems) != 20 {
		t.Fatalf("search groups = %d work items / %d memories, want 20/20", len(wis), len(mems))
	}
	for _, it := range wis {
		m := it.(map[string]any)
		if w105Str(m["id"]) == foreignID {
			t.Fatalf("search leaked a foreign-project work item: %s", raw)
		}
		text := w105Str(m["goal"]) + " " + w105Str(m["content"]) + " " + w105Str(m["html"])
		if !w105ContainsFold(text, "needle") {
			t.Fatalf("work-item search hit %q does not match the query: %#v", text, m)
		}
	}
	for _, it := range mems {
		m := it.(map[string]any)
		if !w105ContainsFold(w105Str(m["title"])+" "+w105Str(m["content"])+" "+w105Str(m["html"]), "needle") {
			t.Fatalf("memory search hit does not match the query: %#v", m)
		}
	}
}

// ---------------------------------------------------------------------------
// S13 — machines are admin-only, admin sees the health fields, no secrets.
// ---------------------------------------------------------------------------

func TestW105MachinesAreAdminOnlyAndReportHealth(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-machines-admin", "admin", "", "", "")
	_, member := w105UserKey(t, inst, "w105-machines-member", "writer", "", v1Project, "viewer")
	operator, _ := w105UserKey(t, inst, "w105-machine-operator", "writer", "", "", "")
	wi, wiSlug := w105WI(t, inst, v1Project, "w105 live machine item", "", nil, "normal")

	db := w105DB(t, inst)
	now := time.Now().UTC()
	for _, m := range []struct {
		id     string
		age    time.Duration
		super  bool
		stale  bool
		launch string
	}{
		{"w105-m-green", time.Minute, false, false, "pi-web"},
		{"w105-m-yellow", 20 * time.Minute, false, true, "pf-cli"},
		{"w105-m-red", 2 * time.Hour, true, true, "pi-web"},
	} {
		var super any
		if m.super {
			super = now.Add(-time.Hour).Format(time.RFC3339Nano)
		}
		w105Exec(t, db, `INSERT INTO machines(id, runtime_version, mode, live_wi_ids, last_heartbeat_at, public_key, launcher, role, operator_id, superseded_at, capability)
			VALUES (?1, 'v9.9.9-w105', 'runner', ?2, ?3, 'w105-public-key-sentinel', ?4, 'runner', ?5, ?6, 'w105-capability-sentinel')`,
			m.id, fmt.Sprintf(`["%s"]`, wi), now.Add(-m.age).Format(time.RFC3339Nano), m.launch, operator, super)
	}

	code, _, raw := w105Web(t, inst, "GET", "/web/api/machines", admin, "")
	if code != http.StatusOK {
		t.Fatalf("admin machines = %d, want 200: %.300s", code, raw)
	}
	for _, leak := range []string{"w105-public-key-sentinel", "w105-capability-sentinel", `"public_key"`, `"capability"`} {
		if strings.Contains(raw, leak) {
			t.Fatalf("machines JSON leaks %s: %s", leak, raw)
		}
	}
	result := w105JSON(t, "machines", raw)
	machines := w105Arr(t, "machines", result["machines"])
	if len(machines) != 3 {
		t.Fatalf("machines = %d, want the 3 seeded rows: %s", len(machines), raw)
	}
	byID := map[string]map[string]any{}
	for _, m := range machines {
		row := m.(map[string]any)
		byID[w105Str(row["id"])] = row
		for _, field := range []string{"id", "operator_id", "role", "mode", "runtime_version", "launcher", "last_heartbeat_at", "heartbeat_age_seconds", "stale", "live_wis"} {
			if _, ok := row[field]; !ok {
				t.Fatalf("machine %v misses health field %q: %s", row["id"], field, raw)
			}
		}
		if live := w105Arr(t, "live_wis", row["live_wis"]); len(live) == 0 || w105Str(live[0].(map[string]any)["id"]) != wi {
			t.Fatalf("machine %v live work items = %v, want %s", row["id"], live, wi)
		}
	}
	if stale, _ := byID["w105-m-green"]["stale"].(bool); stale {
		t.Fatalf("a 1-minute-old machine is marked stale: %#v", byID["w105-m-green"])
	}
	for _, id := range []string{"w105-m-yellow", "w105-m-red"} {
		if stale, _ := byID[id]["stale"].(bool); !stale {
			t.Fatalf("%s is not marked stale: %#v", id, byID[id])
		}
	}
	if super, _ := byID["w105-m-red"]["superseded"].(bool); !super {
		t.Fatalf("a superseded machine is not marked: %#v", byID["w105-m-red"])
	}
	_ = wiSlug

	cNon, _, bNon := w105Web(t, inst, "GET", "/web/api/machines", member, "")
	_, _, bMissing := w105Web(t, inst, "GET", "/web/api/work-items/wi_w105_absent?project="+url.QueryEscape(v1Project), member, "")
	if cNon != http.StatusNotFound || bNon != bMissing {
		t.Fatalf("non-admin machines = %d %.200q, want the identical 404 %.200q", cNon, bNon, bMissing)
	}
}

// ---------------------------------------------------------------------------
// S14 — odd/redundant path segments never escape the header set (N5).
// ---------------------------------------------------------------------------

func TestW105OddPathsNeverEscapeTheHeaderSet(t *testing.T) {
	inst := newV1Install(t)
	_, admin := w105UserKey(t, inst, "w105-oddpaths", "admin", "", "", "")
	paths := []string{
		"/web//",
		"/web//api/context",
		"/web/./",
		"/web/" + v1Project + "/work-items/",
		"/web/" + v1Project + "%2Fwork-items",
		"/web/%2e%2e/",
		"/web/assets//missing.js",
	}
	for _, p := range paths {
		code, h, body := w105Web(t, inst, "GET", p, admin, "")
		if code >= 300 && code < 400 {
			loc := h.Get("Location")
			if loc == "" || !strings.HasPrefix(loc, "/web/") || strings.Contains(loc, "..") || strings.Contains(loc, "//") {
				t.Fatalf("GET %s = %d Location %q: a redirect target must be a sanitized, escaped /web/ path", p, code, loc)
			}
		}
		w105HeadersOK(t, "GET "+p, h)
		_ = body
	}
}

// ---------------------------------------------------------------------------
// S15 — the documented build runs the locked frontend build first, dist and
// node_modules stay out of git, runtime deps stay React-only and node pinned.
// ---------------------------------------------------------------------------

func TestW105BuildTargetFrontendFirstAndTreeClean(t *testing.T) {
	root := v1Root(t)
	cmd := exec.Command("make", "-n", "build")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n build: %v: %s", err, out)
	}
	printed := string(out)
	npmCI := strings.Index(printed, "npm ci")
	npmBuild := strings.Index(printed, "npm run build")
	goBuild := strings.Index(printed, "go build")
	if npmCI < 0 || npmBuild < 0 || goBuild < 0 {
		t.Fatalf("make build does not perform the locked frontend install+build before the Go build:\n%s", printed)
	}
	if npmCI > goBuild || npmBuild > goBuild {
		t.Fatalf("the Go build runs before the frontend build:\n%s", printed)
	}

	for _, f := range []string{"webui/package.json", "webui/package-lock.json", ".nvmrc"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Fatalf("%s is missing: the build chain needs it", f)
		}
	}
	rawPkg, err := os.ReadFile(filepath.Join(root, "webui/package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(rawPkg, &pkg); err != nil {
		t.Fatalf("webui/package.json: %v", err)
	}
	for dep := range pkg.Dependencies {
		if dep != "react" && dep != "react-dom" {
			t.Fatalf("runtime dependency %q is outside the approved react + react-dom surface", dep)
		}
	}
	if len(pkg.Dependencies) != 2 {
		t.Fatalf("runtime dependencies = %v, want exactly react and react-dom", pkg.Dependencies)
	}

	rawIgnore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"webui/dist", "webui/node_modules"} {
		if !strings.Contains(string(rawIgnore), want) {
			t.Fatalf(".gitignore does not keep %s out of the repository:\n%s", want, rawIgnore)
		}
	}
	tracked := func(args ...string) string {
		c := exec.Command("git", append([]string{"ls-files", "--"}, args...)...)
		c.Dir = root
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git ls-files %v: %v: %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	if got := tracked("webui/dist", "webui/node_modules"); got != "" {
		t.Fatalf("build output/node_modules is tracked: %s", got)
	}
	if got := tracked("webui/package-lock.json"); got == "" {
		t.Fatalf("the single lockfile webui/package-lock.json is not tracked")
	}
}

// ---------------------------------------------------------------------------
// S16 — the documents describe the shipped console and state reachability.
// ---------------------------------------------------------------------------

func TestW105DocumentsDescribeTheConsoleAndReachability(t *testing.T) {
	root := v1Root(t)
	docs := []string{
		"docs/architecture.md",
		"docs/user-guide.md",
		"docs/design/polyforge-vnext/reference.md",
		"docs/design/polyforge-vnext/06-memory-search.md",
	}
	var all strings.Builder
	for _, d := range docs {
		raw, err := os.ReadFile(filepath.Join(root, d))
		if err != nil {
			t.Fatalf("document %s is missing: %v", d, err)
		}
		all.Write(raw)
		all.WriteString("\n")
	}
	text := all.String()
	lower := strings.ToLower(text)
	for _, want := range []string{"/web/", "/web/api/", "default-src 'none'", "connect-src 'self'", "no-store", "memories", "machines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the documents do not describe %q", want)
		}
	}
	if !strings.Contains(lower, "daemon") || !strings.Contains(lower, "agent") || !strings.Contains(lower, "key") {
		t.Fatalf("the documents do not state the new entry point's reachability (agent holds no key; daemon proxies no web route)")
	}
}
