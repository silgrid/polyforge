package test

// w103_memory_viewer_acceptance_test.go: the e2e acceptance suite for
// — the aihub read-only memory face — carried to the
// console prefix by. S1–S7 stay byte-identical: the
// /api/v1 memories browse face they drive is unchanged shipped
// surface. S8–S19 moved from the deleted /view/ HTML pages to the /web/
// documents + the private /web/api/memories JSON face, with the HTML
// assertions rewritten as JSON/document assertions and the same
// semantics (only prefix/stack differences). S17 (the old CSP literal)
// and S18 (zero JS) were retired with the owner's approval: S17's slot
// now asserts the NEW exact /web/ CSP literal on every response class,
// and the browser-rendered halves (visual spec, zero-JS layout) live in
// the authored Playwright suite. S9's guide survives the stack change:
// the one static 401 body still explains that an aihub api key is the
// password and that an administrator issues one, byte-identical for
// the missing and the wrong credential. S12's tag chips, S13's zero-hit
// empty state and S15's monospace id footer are DOM behaviour, kept
// by the console pages and asserted in the authored browser suite; no
// other assertion was relaxed in the port itself.
//
// Everything is driven only through the project's real e2e surfaces:
// the REAL aihub binary on loopback (newV1Install), real HTTP, the real
// on-disk aihub.db for the "entry changed between pages" writes (the
// s24 precedent), and the repo's own docs/source files where the
// acceptance criterion is a reach/doc contract.
//
// Scenario map (document order is artifacts/e2e-scenarios.md):
//   S1  Empty query browses instead of refusing               (unchanged)
//   S2  Browse orders by updated or created, descending only   (unchanged)
//   S3  Browse applies the same filters/gates as keyword       (unchanged)
//   S4  Browse bounds the page size to 1..200, default 20      (unchanged)
//   S5  Walking a 45-entry list covers it exactly once         (unchanged)
//   S6  Cursor edge cases never duplicate, skip or restart      (unchanged)
//   S7  A keyword query cannot be mixed with browse paging      (unchanged)
//   S8  Basic on /web/ only, with the key's existing gates
//   S9  No credential and a wrong credential yield the same 401
//   S10 The face answers GET and HEAD only
//   S11 The context answers exactly the caller's readable projects
//   S12 The memories page pages a project and honours type/tags
//   S13 Keyword search is one page with a refine hint
//   S14 Unknown and unreadable objects share one non-leaking 404
//   S15 The memory detail carries the header-card fields + demoted html
//   S16 A hostile corpus renders no executable markup (JSON half)
//   S17 Every /web/ response class carries the NEW exact CSP literal
//   S18 (retired: zero-JS/visual spec — the authored browser suite)
//   S19 The api key never appears in a response or the aihub log
//   S20 The documents document the console generically
//   S21 The reach statement exists and the proxies do not browse

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Surfaces: loopback HTTP with explicit auth, the admin client, the DB.
// ---------------------------------------------------------------------------

type w103MemoryItem struct {
	ID         string   `json:"id"`
	ProjectID  string   `json:"project_id"`
	Type       string   `json:"type"`
	Title      *string  `json:"title"`
	Visibility string   `json:"visibility"`
	Tags       []string `json:"tags"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type w103BrowsePage struct {
	Items      []w103MemoryItem `json:"items"`
	Total      int              `json:"total"`
	NextCursor *string          `json:"next_cursor"`
}

var w103HTTP = &http.Client{
	Timeout: 30 * time.Second,
	// A redirect is a failure of the "no redirect" rule, so never follow one.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func w103Basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// w103Req answers status, headers and body for one request with the given
// Authorization header (empty = none).
func w103Req(t *testing.T, inst *v1Install, method, path, authz, body string) (int, http.Header, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, inst.url+path, rdr)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := w103HTTP.Do(req)
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

func w103Bearer(t *testing.T, inst *v1Install, method, path, key string) (int, string) {
	t.Helper()
	code, _, body := w103Req(t, inst, method, path, "Bearer "+key, "")
	return code, body
}

func w103View(t *testing.T, inst *v1Install, key, path string) (int, http.Header, string) {
	t.Helper()
	return w103Req(t, inst, "GET", path, w103Basic("anyone", key), "")
}

func w103Project(t *testing.T, inst *v1Install, name string) {
	t.Helper()
	if _, err := inst.client.CreateProject(aihubclient.ProjectCreateRequest{Name: name}); err != nil {
		t.Fatalf("create project %s: %v", name, err)
	}
}

func w103ProjectID(t *testing.T, inst *v1Install, name string) string {
	t.Helper()
	raw, err := inst.client.GetProject(name)
	if err != nil {
		t.Fatalf("get project %s: %v", name, err)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == "" {
		t.Fatalf("project %s answer: %v (%s)", name, err, raw)
	}
	return p.ID
}

func w103Memory(t *testing.T, inst *v1Install, project, typ, title, content string, tags []string, visibility string, workItem *string) string {
	t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	if err := inst.client.Do("POST", "/api/v1/memories", map[string]any{
		"project": project, "type": typ, "title": title, "content": content,
		"tags": tags, "visibility": visibility, "work_item_id": workItem,
	}, &out); err != nil || out.ID == "" {
		t.Fatalf("create memory %q in %s: %v", title, project, err)
	}
	return out.ID
}

// w103UserKey mints a user and one api key, optionally project-scoped.
func w103UserKey(t *testing.T, inst *v1Install, name, userType, role string, scope *string) (string, string) {
	t.Helper()
	email := ""
	if userType == "" || userType == "human" {
		email = name + "@example.invalid"
	}
	var user struct {
		ID string `json:"id"`
	}
	rawUser, err := inst.client.CreateUser(aihubclient.UserCreateRequest{
		DisplayName: name, Email: email, UserType: userType, Role: role,
	})
	if err != nil || json.Unmarshal(rawUser, &user) != nil || user.ID == "" {
		t.Fatalf("create user %s: %v (%s)", name, err, rawUser)
	}
	var key struct {
		Plain string `json:"plain"`
	}
	rawKey, err := inst.client.CreateApiKey(aihubclient.ApiKeyCreateRequest{
		UserID: user.ID, Name: name + "-viewer", ProjectID: scope,
	})
	if err != nil || json.Unmarshal(rawKey, &key) != nil || key.Plain == "" {
		t.Fatalf("mint key for %s: %v (%s)", name, err, rawKey)
	}
	return user.ID, key.Plain
}

func w103Member(t *testing.T, inst *v1Install, project, userID, role string) {
	t.Helper()
	members := []aihubclient.ProjectMember{{UserID: userID, Role: role}}
	if _, err := inst.client.PatchProject(project, aihubclient.ProjectPatchRequest{Members: &members}); err != nil {
		t.Fatalf("add %s to %s: %v", userID, project, err)
	}
}

func w103Browse(t *testing.T, inst *v1Install, key, query string) (int, w103BrowsePage, string) {
	t.Helper()
	code, _, raw := w103Req(t, inst, "GET", "/api/v1/memories/search?"+query, "Bearer "+key, "")
	var page w103BrowsePage
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &page); err != nil {
			t.Fatalf("browse %q answer: %v (%s)", query, err, raw)
		}
		if page.Total != len(page.Items) {
			t.Fatalf("browse %q: total %d != len(items) %d: %s", query, page.Total, len(page.Items), raw)
		}
	}
	return code, page, raw
}

func w103ErrCode(raw string) string {
	var e struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(raw), &e)
	return e.Code
}

func w103Seed(t *testing.T, inst *v1Install, project, typ, prefix, content string, n int, tags []string, visibility string) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, w103Memory(t, inst, project, typ,
			fmt.Sprintf("%s %02d", prefix, i), fmt.Sprintf("%s entry %d", content, i),
			tags, visibility, nil))
	}
	return ids
}

func w103HasTitle(body, prefix string, n int) int {
	found := 0
	for i := 0; i < n; i++ {
		if strings.Contains(body, fmt.Sprintf("%s %02d", prefix, i)) {
			found++
		}
	}
	return found
}

// w103WebMemRow is one /web/api/memories row.
type w103WebMemRow struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Visibility string   `json:"visibility"`
	Tags       []string `json:"tags"`
	UpdatedAt  string   `json:"updated_at"`
	WorkItemID string   `json:"work_item_id"`
}

// w103WebMemPage is the /web/api/memories answer.
type w103WebMemPage struct {
	Items      []w103WebMemRow `json:"items"`
	NextCursor string          `json:"next_cursor"`
	RefineHint bool            `json:"refine_hint"`
}

// w103WebList answers one /web/api/memories request (project + query).
func w103WebList(t *testing.T, inst *v1Install, key, project, query string) (int, w103WebMemPage, string) {
	t.Helper()
	path := "/web/api/memories?project=" + url.QueryEscape(project)
	if query != "" {
		path += "&" + query
	}
	code, _, raw := w103View(t, inst, key, path)
	var page w103WebMemPage
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &page); err != nil {
			t.Fatalf("web memories %q answer: %v (%s)", query, err, raw)
		}
	}
	return code, page, raw
}

// w103WebDetail answers one /web/api/memories/{id}?project= request.
func w103WebDetail(t *testing.T, inst *v1Install, key, project, id string) (int, string) {
	t.Helper()
	return w103ViewCode(t, inst, key, "/web/api/memories/"+url.PathEscape(id)+"?project="+url.QueryEscape(project))
}

// w103ViewCode answers status and body for one authenticated GET.
func w103ViewCode(t *testing.T, inst *v1Install, key, path string) (int, string) {
	t.Helper()
	code, _, body := w103View(t, inst, key, path)
	return code, body
}

// w103DB opens the RUNNING aihub on-disk DB (the s24 precedent) for the
// two scenarios that need an entry to move between pages.
func w103DB(t *testing.T, inst *v1Install) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(inst.dataDir, "aihub.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open aihub.db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func w103Exec(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("db exec %q: %v", stmt, err)
	}
}

// w103WebCSP is the console's exact CSP literal: a
// same-origin SPA with hashed external script/style assets only — no
// inline script or style exists, so 'self' alone permits the shell.
const w103WebCSP = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

const w103Web404Body = "{\"error\":\"not found\"}\n"

// w103AssertWeb404 asserts the one static /web/ 404 (status, header set,
// body) and answers the body for a byte-comparison.
func w103AssertWeb404(t *testing.T, inst *v1Install, key, path string) string {
	t.Helper()
	code, h, body := w103View(t, inst, key, path)
	if code != http.StatusNotFound {
		t.Fatalf("GET %s = %d, want 404: %.300s", path, code, body)
	}
	w103HeadersOK(t, "404 "+path, h)
	if body != w103Web404Body {
		t.Fatalf("GET %s 404 body = %q, want the one static body", path, body)
	}
	return body
}

// w103HeadersOK asserts the exact header set on one /web/ response.
func w103HeadersOK(t *testing.T, label string, h http.Header) {
	t.Helper()
	if got := h.Get("Content-Security-Policy"); got != w103WebCSP {
		t.Fatalf("%s CSP = %q, want the exact literal %q", label, got, w103WebCSP)
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

// ---------------------------------------------------------------------------
// S1 — empty query browses.
// ---------------------------------------------------------------------------

func TestW103S01EmptyQueryBrowses(t *testing.T) {
	inst := newV1Install(t)
	w103Project(t, inst, "w103beta")
	a1 := w103Memory(t, inst, v1Project, "experience.wrap", "W103 browse A one", "body a1", []string{"x"}, "project", nil)
	a2 := w103Memory(t, inst, v1Project, "fact.decision", "W103 browse A two", "body a2", []string{"y"}, "project", nil)
	b1 := w103Memory(t, inst, "w103beta", "experience.wrap", "W103 browse B one", "body b1", nil, "project", nil)

	code, page, raw := w103Browse(t, inst, inst.adminKey, "limit=20")
	if code != http.StatusOK {
		t.Fatalf("empty q = %d, want 200 (browse): %s", code, raw)
	}
	seen := map[string]string{}
	for _, it := range page.Items {
		if it.ID == "" || it.ProjectID == "" || it.Type == "" || it.Visibility == "" ||
			it.Title == nil || it.CreatedAt == "" || it.UpdatedAt == "" || it.Tags == nil {
			t.Fatalf("browse item misses today's entry fields: %+v", it)
		}
		seen[it.ID] = it.ProjectID
	}
	for id, project := range map[string]string{a1: "e2e", a2: "e2e", b1: "w103beta"} {
		pid := w103ProjectID(t, inst, project)
		if seen[id] != pid {
			t.Fatalf("empty q did not list %s (project %s): %v", id, project, seen)
		}
	}

	// A project-scoped key browses its readable set only, with no project
	// parameter at all.
	scope := w103ProjectID(t, inst, v1Project)
	_, scoped := w103UserKey(t, inst, "w103-scoped-browse", "human", "admin", &scope)
	code, page, raw = w103Browse(t, inst, scoped, "limit=20")
	if code != http.StatusOK {
		t.Fatalf("scoped empty q = %d, want 200: %s", code, raw)
	}
	for _, it := range page.Items {
		if it.ProjectID != scope {
			t.Fatalf("scoped browse at the readable set leaked project %s: %s", it.ProjectID, raw)
		}
	}
	if len(page.Items) != 2 {
		t.Fatalf("scoped browse = %d items, want the two readable ones: %s", len(page.Items), raw)
	}
}

// ---------------------------------------------------------------------------
// S2 — descending sort by updated (default) or created.
// ---------------------------------------------------------------------------

func TestW103S02BrowseSortsDescending(t *testing.T) {
	inst := newV1Install(t)
	m1 := w103Memory(t, inst, v1Project, "fact.note", "W103 sort one", "sort body 1", nil, "project", nil)
	m2 := w103Memory(t, inst, v1Project, "fact.note", "W103 sort two", "sort body 2", nil, "project", nil)
	m3 := w103Memory(t, inst, v1Project, "fact.note", "W103 sort three", "sort body 3", nil, "project", nil)

	code, page, raw := w103Browse(t, inst, inst.adminKey, "limit=10")
	if code != http.StatusOK {
		t.Fatalf("default browse = %d, want 200: %s", code, raw)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	want := []string{m3, m2, m1}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("default order = %v, want newest-updated first %v", got, want)
	}

	// The third memory was created long ago but updated last: sort=created
	// must not put it first for being updated.
	db := w103DB(t, inst)
	w103Exec(t, db, `UPDATE memories SET created_at=?1 WHERE id=?2`, "2020-01-01T00:00:00Z", m3)
	code, page, raw = w103Browse(t, inst, inst.adminKey, "sort=created&limit=10")
	if code != http.StatusOK {
		t.Fatalf("sort=created = %d, want 200: %s", code, raw)
	}
	got = got[:0]
	for _, it := range page.Items {
		got = append(got, it.ID)
	}
	want = []string{m2, m1, m3}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sort=created order = %v, want creation-descending %v (the backdated entry last)", got, want)
	}

	for _, bad := range []string{"asc", "created_at", "UPDATED"} {
		code, _, raw = w103Browse(t, inst, inst.adminKey, "sort="+url.QueryEscape(bad))
		if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
			t.Fatalf("sort=%s = %d %s, want MEM-400 naming the legal values", bad, code, raw)
		}
		if !strings.Contains(raw, "updated") || !strings.Contains(raw, "created") {
			t.Fatalf("sort=%s refusal does not name the legal values: %s", bad, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// S3 — the same filters and read gates as keyword search.
// ---------------------------------------------------------------------------

func TestW103S03BrowseFiltersAndGates(t *testing.T) {
	inst := newV1Install(t)
	w103Project(t, inst, "w103beta")
	included := w103Memory(t, inst, v1Project, "experience.wrap", "W103 gate included", "gate body", []string{"a", "b"}, "project", nil)
	w103Memory(t, inst, v1Project, "experience.debug", "W103 gate wrong-tag", "gate body", []string{"a"}, "project", nil)
	w103Memory(t, inst, v1Project, "fact.decision", "W103 gate wrong-type", "gate body", []string{"a", "b"}, "project", nil)
	w103Memory(t, inst, v1Project, "experience.wrap", "W103 gate private", "gate body", []string{"a", "b"}, "private", nil)
	w103Memory(t, inst, v1Project, "experience.wrap", "W103 gate admin", "gate body", []string{"a", "b"}, "admin", nil)
	w103Memory(t, inst, "w103beta", "experience.wrap", "W103 gate foreign", "gate body", []string{"a", "b"}, "project", nil)

	user, memberKey := w103UserKey(t, inst, "w103-member", "human", "writer", nil)
	w103Member(t, inst, v1Project, user, "viewer")

	code, page, raw := w103Browse(t, inst, memberKey, "type=experience.*&tags=a,b&limit=50")
	if code != http.StatusOK {
		t.Fatalf("filtered member browse = %d, want 200: %s", code, raw)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != included {
		t.Fatalf("type+tags+visibility browse = %+v, want only %s (wrong tag/type/private/admin/foreign excluded): %s",
			page.Items, included, raw)
	}

	// An unreadable and an unknown project answer the same non-leaking shape.
	codeUnreadable, _, rawUnreadable := w103Browse(t, inst, memberKey, "project=w103beta")
	codeUnknown, _, rawUnknown := w103Browse(t, inst, memberKey, "project=w103nosuch")
	if codeUnreadable != http.StatusNotFound || codeUnknown != http.StatusNotFound ||
		w103ErrCode(rawUnreadable) != "CORE-404" || w103ErrCode(rawUnknown) != "CORE-404" {
		t.Fatalf("unreadable project = %d %s, unknown project = %d %s; want both CORE-404",
			codeUnreadable, rawUnreadable, codeUnknown, rawUnknown)
	}

	// A caller that can read nothing gets an empty page, not an error.
	_, nobody := w103UserKey(t, inst, "w103-nobody", "machine", "writer", nil)
	code, page, raw = w103Browse(t, inst, nobody, "limit=20")
	if code != http.StatusOK || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("nothing-readable browse = %d %+v, want an empty 200 page: %s", code, page, raw)
	}
}

// ---------------------------------------------------------------------------
// S4 — page-size bounds.
// ---------------------------------------------------------------------------

func TestW103S04BrowsePageSize(t *testing.T) {
	inst := newV1Install(t)
	w103Seed(t, inst, v1Project, "fact.note", "W103 limit", "limit body", 25, nil, "project")

	code, page, raw := w103Browse(t, inst, inst.adminKey, "limit=1")
	if code != http.StatusOK || len(page.Items) != 1 {
		t.Fatalf("limit=1 = %d with %d items, want 200 with one: %s", code, len(page.Items), raw)
	}
	code, page, raw = w103Browse(t, inst, inst.adminKey, "limit=200")
	if code != http.StatusOK || len(page.Items) != 25 {
		t.Fatalf("limit=200 = %d with %d items, want all 25: %s", code, len(page.Items), raw)
	}
	code, _, raw = w103Browse(t, inst, inst.adminKey, "limit=0")
	if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
		t.Fatalf("limit=0 = %d %s, want MEM-400 (not a silent default)", code, raw)
	}
	code, _, raw = w103Browse(t, inst, inst.adminKey, "limit=201")
	if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
		t.Fatalf("limit=201 = %d %s, want MEM-400 (not a silent clamp)", code, raw)
	}
	code, _, raw = w103Browse(t, inst, inst.adminKey, "limit=ten")
	if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
		t.Fatalf("limit=ten = %d %s, want MEM-400", code, raw)
	}
	// Absent limit means 20: seed 25, so a full default page is 20.
	code, page, raw = w103Browse(t, inst, inst.adminKey, "")
	if code != http.StatusOK || len(page.Items) != 20 || page.NextCursor == nil {
		t.Fatalf("default limit = %d with %d items / cursor %v, want 20 and a cursor: %s",
			code, len(page.Items), page.NextCursor, raw)
	}
}

// ---------------------------------------------------------------------------
// S5 — walking 45 entries at 20 per page.
// ---------------------------------------------------------------------------

func TestW103S05BrowseWalksTheWholeList(t *testing.T) {
	inst := newV1Install(t)
	w103Seed(t, inst, v1Project, "fact.note", "W103 walk", "walk body", 45, nil, "project")

	seen := map[string]bool{}
	sizes := []int{}
	cursor := ""
	for pageNo := 1; pageNo <= 5; pageNo++ {
		q := "limit=20"
		if cursor != "" {
			q += "&after=" + url.QueryEscape(cursor)
		}
		code, page, raw := w103Browse(t, inst, inst.adminKey, q)
		if code != http.StatusOK {
			t.Fatalf("page %d = %d: %s", pageNo, code, raw)
		}
		sizes = append(sizes, len(page.Items))
		for _, it := range page.Items {
			if seen[it.ID] {
				t.Fatalf("page %d repeats entry %s (duplicate in the walk): %s", pageNo, it.ID, raw)
			}
			seen[it.ID] = true
		}
		if page.NextCursor == nil {
			if pageNo != 3 {
				t.Fatalf("walk ended on page %d after sizes %v, want 20/20/5", pageNo, sizes)
			}
			break
		}
		cursor = *page.NextCursor
	}
	if fmt.Sprint(sizes) != "[20 20 5]" {
		t.Fatalf("page sizes = %v, want [20 20 5]", sizes)
	}
	if len(seen) != 45 {
		t.Fatalf("the three pages hold %d distinct entries, want 45 (a gap)", len(seen))
	}
}

// ---------------------------------------------------------------------------
// S6 — cursor edge cases.
// ---------------------------------------------------------------------------

func TestW103S06BrowseCursorEdges(t *testing.T) {
	inst := newV1Install(t)
	ids := w103Seed(t, inst, v1Project, "fact.note", "W103 cursor", "cursor body", 5, nil, "project")
	db := w103DB(t, inst)

	code, page, raw := w103Browse(t, inst, inst.adminKey, "limit=2")
	if code != http.StatusOK || len(page.Items) != 2 || page.NextCursor == nil {
		t.Fatalf("first page = %d %d items cursor %v, want 2 and a cursor: %s", code, len(page.Items), page.NextCursor, raw)
	}
	firstPage := map[string]bool{}
	for _, it := range page.Items {
		firstPage[it.ID] = true
	}
	cursor := *page.NextCursor

	// Move the oldest entry (below the page boundary) to the top. It must
	// not reappear in this pass; the unmoved remainder must not be skipped.
	moved := ids[0]
	w103Exec(t, db, `UPDATE memories SET updated_at=?1 WHERE id=?2`, time.Now().UTC().Format(time.RFC3339Nano), moved)
	code, page, raw = w103Browse(t, inst, inst.adminKey, "limit=2&after="+url.QueryEscape(cursor))
	if code != http.StatusOK {
		t.Fatalf("second page = %d: %s", code, raw)
	}
	second := map[string]bool{}
	for _, it := range page.Items {
		if firstPage[it.ID] {
			t.Fatalf("second page repeats first-page entry %s: %s", it.ID, raw)
		}
		if it.ID == moved {
			t.Fatalf("the entry moved to the top reappeared below the boundary: %s", raw)
		}
		second[it.ID] = true
	}
	if len(second) != 2 {
		t.Fatalf("second page holds %d entries, want the two unmoved ones: %s", len(second), raw)
	}
	for _, id := range ids[1:3] {
		if !second[id] {
			t.Fatalf("second page skipped unmoved entry %s: %s", id, raw)
		}
	}

	// Malformed cursor and a cursor issued under another sort are refused.
	code, _, raw = w103Browse(t, inst, inst.adminKey, "after=not-a-cursor")
	if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
		t.Fatalf("malformed cursor = %d %s, want MEM-400 (no restart)", code, raw)
	}
	code, createdPage, raw := w103Browse(t, inst, inst.adminKey, "sort=created&limit=2")
	if code != http.StatusOK || createdPage.NextCursor == nil {
		t.Fatalf("created-sort first page = %d cursor %v: %s", code, createdPage.NextCursor, raw)
	}
	code, _, raw = w103Browse(t, inst, inst.adminKey,
		"sort=updated&limit=2&after="+url.QueryEscape(*createdPage.NextCursor))
	if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
		t.Fatalf("sort-mismatched cursor = %d %s, want MEM-400 (no restart)", code, raw)
	}

	// A valid cursor positioned after the last active entry answers an
	// empty page with no cursor: redact everything it still points at.
	code, page, raw = w103Browse(t, inst, inst.adminKey, "limit=1")
	if code != http.StatusOK || page.NextCursor == nil {
		t.Fatalf("past-end setup page = %d cursor %v: %s", code, page.NextCursor, raw)
	}
	pastEnd := *page.NextCursor
	for _, id := range ids {
		if code, body := w103Bearer(t, inst, "POST", "/api/v1/memories/"+id+"/redact", inst.adminKey); code != http.StatusOK {
			t.Fatalf("redact %s = %d: %s", id, code, body)
		}
	}
	code, page, raw = w103Browse(t, inst, inst.adminKey, "limit=2&after="+url.QueryEscape(pastEnd))
	if code != http.StatusOK || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("cursor past the end = %d %d items cursor %v, want an empty 200 page: %s",
			code, len(page.Items), page.NextCursor, raw)
	}
}

// ---------------------------------------------------------------------------
// S7 — keyword and browse parameters cannot be mixed.
// ---------------------------------------------------------------------------

func TestW103S07KeywordCannotMixWithBrowse(t *testing.T) {
	inst := newV1Install(t)
	hit := w103Memory(t, inst, v1Project, "fact.note", "W103 keyword hit", "w103kw needle", nil, "project", nil)

	code, page, raw := w103Browse(t, inst, inst.adminKey, "q=w103kw")
	if code != http.StatusOK || page.Total != 1 || page.Items[0].ID != hit {
		t.Fatalf("keyword search = %d %+v, want the one hit: %s", code, page, raw)
	}
	if page.NextCursor != nil {
		t.Fatalf("keyword answer carries a browse cursor: %s", raw)
	}
	for _, q := range []string{
		"q=w103kw&after=not-a-cursor",
		"q=w103kw&sort=created",
		"q=w103kw&after=not-a-cursor&sort=created",
	} {
		code, _, raw = w103Browse(t, inst, inst.adminKey, q)
		if code != http.StatusBadRequest || w103ErrCode(raw) != "MEM-400" {
			t.Fatalf("%s = %d %s, want MEM-400 (never a relevance page that ignores the extras)", q, code, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// S8 — Basic on /web/ only, with the key's existing gates.
// ---------------------------------------------------------------------------

func TestW103S08BasicAuthAndGates(t *testing.T) {
	inst := newV1Install(t)
	w103Project(t, inst, "w103beta")
	w103Memory(t, inst, v1Project, "fact.note", "W103 auth A", "auth body", nil, "project", nil)
	w103Memory(t, inst, "w103beta", "fact.note", "W103 auth B", "auth body", nil, "project", nil)

	code, _, body := w103View(t, inst, inst.adminKey, "/web/")
	if code != http.StatusOK {
		t.Fatalf("valid Basic key (username ignored) on /web/ = %d, want 200: %.300s", code, body)
	}
	code, _, _ = w103Req(t, inst, "GET", "/web/", w103Basic("anyone", "not-a-key"), "")
	if code != http.StatusUnauthorized {
		t.Fatalf("wrong Basic password = %d, want 401", code)
	}
	// The JSON face stays Bearer-only: Basic is not read there.
	code, _, _ = w103Req(t, inst, "GET", "/api/v1/memories/search?q=w103", w103Basic("anyone", inst.adminKey), "")
	if code != http.StatusUnauthorized {
		t.Fatalf("Basic-only /api/v1 request = %d, want 401 as today", code)
	}
	// The console does not accept Bearer.
	code, _, _ = w103Req(t, inst, "GET", "/web/", "Bearer "+inst.adminKey, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("Bearer-only /web/ request = %d, want 401", code)
	}

	// A project-scoped admin key sees exactly its project; the unscoped
	// admin sees both; a machine key with no membership sees neither.
	scope := w103ProjectID(t, inst, v1Project)
	_, scoped := w103UserKey(t, inst, "w103-scoped-viewer", "human", "admin", &scope)
	if code, page, raw := w103WebList(t, inst, scoped, v1Project, ""); code != http.StatusOK || len(page.Items) != 1 {
		t.Fatalf("scoped key on its project = %d (%d rows), want 200 with its one memory: %s", code, len(page.Items), raw)
	}
	if code, _, raw := w103WebList(t, inst, scoped, "w103beta", ""); code != http.StatusNotFound || raw != w103Web404Body {
		t.Fatalf("scoped key on another project = %d %q, want the static 404", code, raw)
	}
	var ctx struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	code, _, raw := w103View(t, inst, inst.adminKey, "/web/api/context")
	if code != http.StatusOK || json.Unmarshal([]byte(raw), &ctx) != nil {
		t.Fatalf("unscoped admin context = %d: %s", code, raw)
	}
	names := map[string]bool{}
	for _, p := range ctx.Projects {
		names[p.Name] = true
	}
	if !names["e2e"] || !names["w103beta"] {
		t.Fatalf("unscoped admin context misses a readable project: %s", raw)
	}
	_, machine := w103UserKey(t, inst, "w103-machine", "machine", "writer", nil)
	if code, _, raw := w103View(t, inst, machine, "/web/api/context"); code != http.StatusOK {
		t.Fatalf("machine key context = %d, want 200 (empty readable set): %s", code, raw)
	}
	if code, _, raw := w103WebList(t, inst, machine, v1Project, ""); code != http.StatusNotFound || raw != w103Web404Body {
		t.Fatalf("machine key on a project = %d %q, want the static 404", code, raw)
	}
}

// ---------------------------------------------------------------------------
// S9 — one 401 body for no credential and a wrong credential.
// ---------------------------------------------------------------------------

func TestW103S09UnauthenticatedGuide(t *testing.T) {
	inst := newV1Install(t)
	w103Memory(t, inst, v1Project, "fact.note", "W103 secret title", "secret body", nil, "project", nil)

	code, headers, noCred := w103Req(t, inst, "GET", "/web/e2e/memories", "", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d, want 401: %.300s", code, noCred)
	}
	challenge := headers.Get("WWW-Authenticate")
	if !strings.HasPrefix(strings.ToLower(challenge), "basic") {
		t.Fatalf("no-credential challenge = %q, want a Basic challenge", challenge)
	}
	code2, headers2, wrong := w103Req(t, inst, "GET", "/web/e2e/memories", w103Basic("anyone", "definitely-wrong"), "")
	if code2 != http.StatusUnauthorized {
		t.Fatalf("wrong credential = %d, want 401", code2)
	}
	if wrong != noCred {
		t.Fatalf("wrong-credential body differs from the no-credential body:\n--- no credential ---\n%s\n--- wrong credential ---\n%s", noCred, wrong)
	}
	if headers2.Get("WWW-Authenticate") != challenge {
		t.Fatalf("wrong-credential challenge = %q, want %q", headers2.Get("WWW-Authenticate"), challenge)
	}
	// The one static body is the #103 guide: it says the password is an
	// aihub api key and that an administrator issues one — and nothing
	// else (byte-identical for the missing and the wrong credential,
	// asserted above).
	lower := strings.ToLower(noCred)
	if !strings.Contains(lower, "key") || !strings.Contains(lower, "admin") {
		t.Fatalf("401 body does not explain that an api key is the password and an administrator issues one: %s", noCred)
	}
	for _, leak := range []string{"w103 secret title", "mem_", inst.adminKey} {
		if strings.Contains(noCred, leak) {
			t.Fatalf("401 body leaks %q: %s", leak, noCred)
		}
	}
	// An unauthenticated request sets no cookie, ever.
	if headers.Get("Set-Cookie") != "" || headers2.Get("Set-Cookie") != "" {
		t.Fatalf("a refused request set a cookie")
	}
}

// ---------------------------------------------------------------------------
// S10 — GET/HEAD only.
// ---------------------------------------------------------------------------

func TestW103S10ReadOnlyMethods(t *testing.T) {
	inst := newV1Install(t)
	hit := w103Memory(t, inst, v1Project, "fact.note", "W103 methods", "w103methods body", nil, "project", nil)
	countBefore := w103SearchTotal(t, inst, "w103methods")

	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE"} {
		for _, path := range []string{"/web/", "/web/api/memories?project=e2e"} {
			code, headers, body := w103Req(t, inst, method, path, w103Basic("anyone", inst.adminKey), "")
			if code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s = %d, want 405: %.300s", method, path, code, body)
			}
			allow := headers.Get("Allow")
			if !strings.Contains(allow, "GET") || !strings.Contains(allow, "HEAD") {
				t.Fatalf("%s %s Allow = %q, want GET, HEAD", method, path, allow)
			}
			if body != "{\"error\":\"method not allowed\"}\n" {
				t.Fatalf("%s %s body = %q", method, path, body)
			}
		}
	}
	// Authentication precedes the method gate.
	if code, _, _ := w103Req(t, inst, "POST", "/web/", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated POST = %d, want the 401 first", code)
	}
	if countAfter := w103SearchTotal(t, inst, "w103methods"); countAfter != countBefore {
		t.Fatalf("write methods changed data: %d -> %d memories", countBefore, countAfter)
	}
	if code, _ := w103Bearer(t, inst, "GET", "/api/v1/memories/"+hit, inst.adminKey); code != http.StatusOK {
		t.Fatalf("the memory disappeared after the method refusals: %d", code)
	}

	codeGet, getHeaders, getBody := w103View(t, inst, inst.adminKey, "/web/")
	codeHead, headHeaders, headBody := w103Req(t, inst, "HEAD", "/web/", w103Basic("anyone", inst.adminKey), "")
	if codeGet != http.StatusOK || codeHead != codeGet {
		t.Fatalf("GET = %d, HEAD = %d; want the same status", codeGet, codeHead)
	}
	if headBody != "" {
		t.Fatalf("HEAD answered a body of %d bytes", len(headBody))
	}
	if getBody == "" {
		t.Fatalf("GET answered an empty page")
	}
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Security-Policy",
		"X-Content-Type-Options", "Cache-Control", "Referrer-Policy", "X-Robots-Tag"} {
		if getHeaders.Get(name) != headHeaders.Get(name) {
			t.Fatalf("HEAD header %s = %q, GET = %q", name, headHeaders.Get(name), getHeaders.Get(name))
		}
	}
}

func w103SearchTotal(t *testing.T, inst *v1Install, q string) int {
	t.Helper()
	code, body := w103Bearer(t, inst, "GET", "/api/v1/memories/search?q="+url.QueryEscape(q)+"&limit=200", inst.adminKey)
	if code != http.StatusOK {
		t.Fatalf("search %q = %d: %s", q, code, body)
	}
	var page w103BrowsePage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("search %q answer: %v", q, err)
	}
	return page.Total
}

// ---------------------------------------------------------------------------
// S11 — the context is exactly the readable set, with no default.
// ---------------------------------------------------------------------------

func TestW103S11PickerReadableProjects(t *testing.T) {
	inst := newV1Install(t)
	w103Project(t, inst, "w103beta")
	w103Project(t, inst, "w103gamma")
	w103Memory(t, inst, v1Project, "fact.note", "W103 picker A memory", "picker body", nil, "project", nil)
	w103Memory(t, inst, "w103beta", "fact.note", "W103 picker B memory", "picker body", nil, "project", nil)
	user, key := w103UserKey(t, inst, "w103-picker-member", "human", "writer", nil)
	w103Member(t, inst, v1Project, user, "viewer")
	w103Member(t, inst, "w103beta", user, "viewer")

	var ctx struct {
		Admin    bool `json:"admin"`
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	code, _, raw := w103View(t, inst, key, "/web/api/context")
	if code != http.StatusOK {
		t.Fatalf("context = %d, want 200 (no redirect/default): %.300s", code, raw)
	}
	if err := json.Unmarshal([]byte(raw), &ctx); err != nil {
		t.Fatalf("context answer: %v (%s)", err, raw)
	}
	if ctx.Admin {
		t.Fatalf("a writer member context carries the admin flag: %s", raw)
	}
	got := map[string]bool{}
	for _, p := range ctx.Projects {
		got[p.Name] = true
		if p.ID == "" {
			t.Fatalf("context project carries no id: %s", raw)
		}
	}
	if !got["e2e"] || !got["w103beta"] || got["w103gamma"] {
		t.Fatalf("context = %v, want exactly e2e + w103beta", got)
	}

	// No JSON route was added beyond the private face: projects stay
	// admin-only on /api/v1.
	if code, body := w103Bearer(t, inst, "GET", "/api/v1/projects", key); code != http.StatusForbidden {
		t.Fatalf("non-admin GET /api/v1/projects = %d, want 403 as today: %s", code, body)
	}
}

// ---------------------------------------------------------------------------
// S12 — list paging and the type/tags filters.
// ---------------------------------------------------------------------------

func TestW103S12ListPagesAndTypeFilter(t *testing.T) {
	inst := newV1Install(t)
	w103Seed(t, inst, v1Project, "experience.wrap", "W103 list exp", "list body", 22, []string{"topic"}, "project")
	w103Seed(t, inst, v1Project, "fact.decision", "W103 list fact", "list body", 5, nil, "project")

	code, page, raw := w103WebList(t, inst, inst.adminKey, v1Project, "")
	if code != http.StatusOK || len(page.Items) != 20 || page.NextCursor == "" {
		t.Fatalf("first page = %d (%d rows, cursor %q), want 20 and a cursor: %s", code, len(page.Items), page.NextCursor, raw)
	}
	for _, it := range page.Items {
		if it.ID == "" || it.Title == "" || it.Type == "" || it.Visibility == "" || it.UpdatedAt == "" {
			t.Fatalf("list row misses fields: %+v", it)
		}
	}
	if n := w103WebHasTitle(page, "W103 list exp", 22) + w103WebHasTitle(page, "W103 list fact", 5); n != 20 {
		t.Fatalf("first page shows %d seeded rows, want 20", n)
	}

	// type= narrows the page and rides on the cursor.
	code, page, raw = w103WebList(t, inst, inst.adminKey, v1Project, "type=experience.*")
	if code != http.StatusOK || w103WebHasTitle(page, "W103 list exp", 22) != 20 || w103WebHasTitle(page, "W103 list fact", 5) != 0 {
		t.Fatalf("type filter did not narrow to 20 experience rows and no fact rows: %s", raw)
	}
	if page.NextCursor == "" {
		t.Fatalf("typed page lost its cursor: %s", raw)
	}
	code, page, raw = w103WebList(t, inst, inst.adminKey, v1Project, "type=experience.*&after="+url.QueryEscape(page.NextCursor))
	if code != http.StatusOK || w103WebHasTitle(page, "W103 list exp", 22) != 2 || w103WebHasTitle(page, "W103 list fact", 5) != 0 {
		t.Fatalf("typed second page = %d, want the remaining 2 experience rows: %s", code, raw)
	}
	if page.NextCursor != "" {
		t.Fatalf("last typed page still carries a cursor: %s", raw)
	}

	// The web face's tags parameter filters (the plan pins it in the
	// route table; the old viewer kept tags as chips only).
	code, page, raw = w103WebList(t, inst, inst.adminKey, v1Project, "tags=topic")
	if code != http.StatusOK || w103WebHasTitle(page, "W103 list fact", 5) != 0 {
		t.Fatalf("tags=topic did not narrow to the tagged rows: %s", raw)
	}
	if w103WebHasTitle(page, "W103 list exp", 22) != 20 {
		t.Fatalf("tags filter page = %d tagged rows, want 20: %s", w103WebHasTitle(page, "W103 list exp", 22), raw)
	}
}

func w103WebHasTitle(page w103WebMemPage, prefix string, n int) int {
	found := 0
	for i := 0; i < n; i++ {
		for _, it := range page.Items {
			if it.Title == fmt.Sprintf("%s %02d", prefix, i) {
				found++
				break
			}
		}
	}
	return found
}

// ---------------------------------------------------------------------------
// S13 — one-page keyword search, refine hint, zero-hit answer.
// ---------------------------------------------------------------------------

func TestW103S13SearchOnePage(t *testing.T) {
	inst := newV1Install(t)
	for i := 0; i < 55; i++ {
		w103Memory(t, inst, v1Project, "fact.note", fmt.Sprintf("W103 find %02d", i),
			fmt.Sprintf("w103searchable entry %d", i), nil, "project", nil)
	}
	for i := 0; i < 3; i++ {
		w103Memory(t, inst, v1Project, "fact.note", fmt.Sprintf("W103 rare %02d", i),
			fmt.Sprintf("w103rare entry %d", i), nil, "project", nil)
	}

	code, page, raw := w103WebList(t, inst, inst.adminKey, v1Project, "q=w103searchable")
	if code != http.StatusOK {
		t.Fatalf("keyword page = %d, want 200: %s", code, raw)
	}
	if n := w103WebHasTitle(page, "W103 find", 55); n != 50 {
		t.Fatalf("full keyword page shows %d of 55 matches, want the top 50: %s", n, raw)
	}
	if !page.RefineHint {
		t.Fatalf("full keyword page lacks the refine hint: %s", raw)
	}
	if page.NextCursor != "" {
		t.Fatalf("keyword answer carries a browse cursor: %s", raw)
	}

	code, page, raw = w103WebList(t, inst, inst.adminKey, v1Project, "q=w103rare")
	if code != http.StatusOK || w103WebHasTitle(page, "W103 rare", 3) != 3 {
		t.Fatalf("short keyword page = %d, want the 3 rare rows: %s", code, raw)
	}
	if page.RefineHint {
		t.Fatalf("short keyword page shows the refine hint: %s", raw)
	}

	code, page, raw = w103WebList(t, inst, inst.adminKey, v1Project, "q=w103nomatch")
	if code != http.StatusOK || len(page.Items) != 0 || page.RefineHint || page.NextCursor != "" {
		t.Fatalf("zero-hit keyword answer = %d (%d rows, hint %v), want the empty 200 page: %s",
			code, len(page.Items), page.RefineHint, raw)
	}
}

// ---------------------------------------------------------------------------
// S14 — one non-leaking 404 body.
// ---------------------------------------------------------------------------

func TestW103S14NonLeaking404(t *testing.T) {
	inst := newV1Install(t)
	w103Project(t, inst, "w103beta")
	foreign := w103Memory(t, inst, "w103beta", "fact.note", "W103 hidden title", "hidden body", nil, "project", nil)
	user, memberKey := w103UserKey(t, inst, "w103-404-member", "human", "writer", nil)
	w103Member(t, inst, v1Project, user, "viewer")

	unreadableProject := w103AssertWeb404(t, inst, memberKey, "/web/api/memories?project=w103beta")
	unknownProject := w103AssertWeb404(t, inst, memberKey, "/web/api/memories?project=w103nosuch")
	unreadableMemory := w103AssertWeb404(t, inst, memberKey, "/web/api/memories/"+foreign+"?project=w103beta")
	foreignViaOwnProject := w103AssertWeb404(t, inst, memberKey, "/web/api/memories/"+foreign+"?project=e2e")
	unknownMemory := w103AssertWeb404(t, inst, memberKey, "/web/api/memories/mem_doesnotexist?project=e2e")

	for name, body := range map[string]string{
		"unknown project":         unknownProject,
		"unreadable memory":       unreadableMemory,
		"foreign via own project": foreignViaOwnProject,
		"unknown memory":          unknownMemory,
	} {
		if body != unreadableProject {
			t.Fatalf("%s 404 body differs from the unreadable-project 404:\n--- want ---\n%s\n--- got ---\n%s",
				name, unreadableProject, body)
		}
	}
	for _, leak := range []string{"w103beta", "W103 hidden title", foreign} {
		if strings.Contains(unreadableProject, leak) {
			t.Fatalf("404 body leaks %q: %s", leak, unreadableProject)
		}
	}

	// The document deep links share the one shell — the client renders
	// the same not-found view for both classes (no project/object data
	// in the shell by construction).
	d1, _, s1 := w103View(t, inst, memberKey, "/web/w103beta/memories/"+foreign)
	d2, _, s2 := w103View(t, inst, memberKey, "/web/e2e/memories/mem_doesnotexist")
	if d1 != http.StatusOK || d2 != http.StatusOK || s1 != s2 {
		t.Fatalf("document deep links = %d/%d: both must be the one shell with 200", d1, d2)
	}
}

// ---------------------------------------------------------------------------
// S15 — the detail's header-card fields and demoted Markdown.
// ---------------------------------------------------------------------------

func TestW103S15DetailHeaderCard(t *testing.T) {
	inst := newV1Install(t)
	rawWI, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project: v1Project, Goal: "w103 detail card", BundleName: strPtr("autopilot"),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rawWI, &wi); err != nil || wi.ID == "" {
		t.Fatalf("work item answer: %v (%s)", err, rawWI)
	}
	id := w103Memory(t, inst, v1Project, "experience.wrap", "W103 Detail Title",
		"# Big heading\n\nBody paragraph.\n\n## Sub heading\n\nmore text", []string{"alpha", "beta"}, "project", &wi.ID)

	code, raw := w103WebDetail(t, inst, inst.adminKey, v1Project, id)
	if code != http.StatusOK {
		t.Fatalf("detail = %d, want 200: %.300s", code, raw)
	}
	var d struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Type       string   `json:"type"`
		Visibility string   `json:"visibility"`
		Tags       []string `json:"tags"`
		UpdatedAt  string   `json:"updated_at"`
		WorkItemID string   `json:"work_item_id"`
		HTML       string   `json:"html"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatalf("detail answer: %v (%s)", err, raw)
	}
	if d.ID != id || d.Title != "W103 Detail Title" || d.Type != "experience.wrap" ||
		d.Visibility != "project" || d.WorkItemID != wi.ID || len(d.Tags) != 2 || d.UpdatedAt == "" {
		t.Fatalf("detail header card misses fields: %+v", d)
	}
	if !strings.Contains(d.HTML, "Body paragraph.") {
		t.Fatalf("detail carries no body: %s", d.HTML)
	}
	if !regexp.MustCompile(`(?is)<h2[^>]*>Big heading`).MatchString(d.HTML) {
		t.Fatalf("body heading was not demoted to h2: %s", d.HTML)
	}
	if !regexp.MustCompile(`(?is)<h3[^>]*>Sub heading`).MatchString(d.HTML) {
		t.Fatalf("body sub-heading was not demoted to h3: %s", d.HTML)
	}
}

// ---------------------------------------------------------------------------
// S16 — hostile Markdown corpus (the server-side JSON half; the browser
// DOM half is the authored Playwright suite).
// ---------------------------------------------------------------------------

func TestW103S16HostileMarkdown(t *testing.T) {
	inst := newV1Install(t)
	corpus := `# Hostile corpus

<script>alert('x')</script>

<img src="https://evil.example/pixel.png" onerror="alert(1)">

<iframe src="https://evil.example/frame"></iframe>

<object data="https://evil.example/o"></object>

<svg onload="alert(1)"></svg>

<form action="https://evil.example/post"><input name="x"></form>

<div onclick="alert(1)">raw html block</div>

[click me](https://evil.example/x)

[bad](javascript:alert(1))

![alt text](https://evil.example/i.png)

![badimg](data:text/html;base64,PHNjcmlwdD4=)
`
	id := w103Memory(t, inst, v1Project, "fact.note", "W103 Hostile", corpus, []string{"xss"}, "project", nil)
	code, headers, body := w103View(t, inst, inst.adminKey, "/web/api/memories/"+id+"?project=e2e")
	if code != http.StatusOK {
		t.Fatalf("hostile detail = %d, want 200: %.300s", code, body)
	}
	w103HeadersOK(t, "hostile detail", headers)
	var d struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal([]byte(body), &d); err != nil || d.HTML == "" {
		t.Fatalf("detail answer carries no html: %v (%s)", err, body)
	}
	lower := strings.ToLower(d.HTML)
	for _, bad := range []string{"<script", "<iframe", "<object", "<svg", "<form", "<img"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("hostile body produced %q: %.1200s", bad, d.HTML)
		}
	}
	if m := regexp.MustCompile(`(?is)<[^>]*\son[a-z]+\s*=`).FindString(d.HTML); m != "" {
		t.Fatalf("hostile body produced an event-handler attribute %q: %.1200s", m, d.HTML)
	}
	if m := regexp.MustCompile(`(?is)<[^>]*(href|src)\s*=\s*["']\s*((https?:)?//|javascript:|data:)`).FindString(d.HTML); m != "" {
		t.Fatalf("hostile body produced an external/executable URL attribute %q: %.1200s", m, d.HTML)
	}
	for _, want := range []string{"click me", "https://evil.example/x", "alt text"} {
		if !strings.Contains(d.HTML, want) {
			t.Fatalf("hostile body does not render %q as text: %.1200s", want, d.HTML)
		}
	}
}

// ---------------------------------------------------------------------------
// S17 — (retired: the old CSP literal) the NEW exact CSP literal on
// every /web/ response class.
// ---------------------------------------------------------------------------

func TestW103S17SecurityHeaders(t *testing.T) {
	inst := newV1Install(t)
	w103Memory(t, inst, v1Project, "fact.note", "W103 headers", "headers body", nil, "project", nil)

	// One asset the shell references (an existing file, its own bytes).
	_, _, shell := w103View(t, inst, inst.adminKey, "/web/")
	asset := regexp.MustCompile(`(?:src|href)="([^"]*assets/[^"]+)"`).FindStringSubmatch(shell)
	if len(asset) != 2 {
		t.Fatalf("the shell references no assets/ file: %.400s", shell)
	}
	assetPath := strings.ReplaceAll(asset[1], "&amp;", "&")
	if !strings.HasPrefix(assetPath, "/web/") {
		assetPath = "/web/" + strings.TrimPrefix(assetPath, "./")
	}

	type pageResp struct {
		label string
		code  int
		h     http.Header
		body  string
	}
	var cases []pageResp
	addCase := func(label string, code int, h http.Header, body string) {
		cases = append(cases, pageResp{label, code, h, body})
	}
	c, h, b := w103View(t, inst, inst.adminKey, "/web/")
	addCase("200 document", c, h, b)
	c, h, b = w103View(t, inst, inst.adminKey, assetPath)
	addCase("200 asset", c, h, b)
	c, h, b = w103View(t, inst, inst.adminKey, "/web/api/context")
	addCase("200 JSON", c, h, b)
	c, h, b = w103View(t, inst, inst.adminKey, "/web/api/memories?project=nosuch")
	addCase("404 JSON", c, h, b)
	c, h, b = w103Req(t, inst, "GET", "/web/", "", "")
	addCase("401 body", c, h, b)
	c, h, b = w103Req(t, inst, "POST", "/web/e2e/memories", w103Basic("anyone", inst.adminKey), "")
	addCase("405 refusal", c, h, b)
	for _, c := range cases {
		w103HeadersOK(t, c.label, c.h)
		if strings.Contains(strings.ToLower(c.body), "<!doctype") {
			if !regexp.MustCompile(`(?is)<meta[^>]+name=["']robots["'][^>]*>`).MatchString(c.body) ||
				!strings.Contains(strings.ToLower(c.body), "noindex") {
				t.Fatalf("%s shell has no robots noindex meta: %.600s", c.label, c.body)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// S18 — retired with the owner's approval: the zero-JS/visual-spec
// assertions moved to the authored Playwright browser suite
// (webui/e2e/run.mjs drives the layout, token and zebra scenarios).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// S19 — no plaintext key in a response or the aihub log.
// ---------------------------------------------------------------------------

func TestW103S19NoKeyLeak(t *testing.T) {
	inst := newV1Install(t)
	id := w103Memory(t, inst, v1Project, "fact.note", "W103 leak", "w103leak body", nil, "project", nil)
	scope := w103ProjectID(t, inst, v1Project)
	_, sentinel := w103UserKey(t, inst, "w103-leak-key", "human", "admin", &scope)

	type got struct {
		label  string
		code   int
		header http.Header
		body   string
	}
	responses := []got{}
	add := func(label string, code int, h http.Header, body string) {
		responses = append(responses, got{label, code, h, body})
	}
	for _, path := range []string{"/web/", "/web/e2e/memories", "/web/api/memories/" + id + "?project=e2e", "/web/api/context"} {
		c, h, b := w103View(t, inst, sentinel, path)
		add(path, c, h, b)
	}
	c, h, b := w103View(t, inst, sentinel, "/web/api/memories?project=w103nosuch")
	add("404", c, h, b)
	c, h, b = w103Req(t, inst, "POST", "/web/e2e/memories", w103Basic("anyone", sentinel), "")
	add("405", c, h, b)
	c, h, b = w103Req(t, inst, "GET", "/web/", w103Basic("anyone", "wrong"), "")
	add("401", c, h, b)
	c, h, b = w103Req(t, inst, "GET", "/api/v1/memories/search?limit=1", "Bearer "+sentinel, "")
	add("browse API", c, h, b)
	c, h, b = w103Req(t, inst, "GET", "/api/v1/memories/search?q=w103leak", "Bearer "+sentinel, "")
	add("search API", c, h, b)

	for _, r := range responses {
		switch r.label {
		case "401":
			if r.code != http.StatusUnauthorized {
				t.Fatalf("401 class = %d", r.code)
			}
		case "404":
			if r.code != http.StatusNotFound {
				t.Fatalf("404 class = %d", r.code)
			}
		case "405":
			if r.code != http.StatusMethodNotAllowed {
				t.Fatalf("405 class = %d", r.code)
			}
		default:
			if r.code != http.StatusOK {
				t.Fatalf("%s = %d, want 200 (a vacuous 404 proves nothing): %.300s", r.label, r.code, r.body)
			}
		}
		if strings.Contains(r.body, sentinel) {
			t.Fatalf("%s body carries the plaintext key: %.600s", r.label, r.body)
		}
		for name, vals := range r.header {
			for _, v := range vals {
				if strings.Contains(v, sentinel) {
					t.Fatalf("%s response header %s carries the plaintext key", r.label, name)
				}
			}
		}
	}

	time.Sleep(250 * time.Millisecond)
	logRaw, err := os.ReadFile(inst.aihubLog)
	if err != nil {
		t.Fatalf("read aihub log: %v", err)
	}
	if strings.Contains(string(logRaw), sentinel) {
		t.Fatalf("the aihub log carries the plaintext key")
	}
}

// ---------------------------------------------------------------------------
// S20 — the documents.
// ---------------------------------------------------------------------------

func w103Doc(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

func w103Section(t *testing.T, raw, heading string) string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	var out []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			if strings.EqualFold(strings.TrimSpace(line), heading) {
				in = true
			}
		}
		if in {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %q section", heading)
	}
	return strings.Join(out, "\n")
}

func TestW103S20Docs(t *testing.T) {
	arch := strings.ToLower(w103Doc(t, "docs/architecture.md"))
	if !strings.Contains(arch, "console") || !strings.Contains(arch, "basic") ||
		(!strings.Contains(arch, "read-only") && !strings.Contains(arch, "read only")) {
		t.Fatalf("docs/architecture.md does not state the read-only console behind Basic auth")
	}
	ref := strings.ToLower(w103Doc(t, "docs/design/polyforge-vnext/reference.md"))
	for _, want := range []string{"/web/", "console", "browse"} {
		if !strings.Contains(ref, want) {
			t.Fatalf("the route reference lacks %q", want)
		}
	}
	mem := strings.ToLower(w103Doc(t, "docs/design/polyforge-vnext/06-memory-search.md"))
	for _, want := range []string{"next_cursor", "after", "sort", "200"} {
		if !strings.Contains(mem, want) {
			t.Fatalf("06-memory-search.md lacks %q", want)
		}
	}
	guide := strings.ToLower(w103Section(t, w103Doc(t, "docs/user-guide.md"), "## Memory"))
	for _, want := range []string{"/web/", "password", "private"} {
		if !strings.Contains(guide, want) {
			t.Fatalf("the user guide's Memory section lacks %q", want)
		}
	}
	commands := strings.ToLower(w103Doc(t, "docs/reference/commands.md"))
	if strings.Contains(commands, "pf memory list") || strings.Contains(commands, "memory list") {
		t.Fatalf("the command reference documents a CLI browse verb (out of scope)")
	}
}

// ---------------------------------------------------------------------------
// S21 — the reach statement; the step/task proxies stay non-browsing.
// ---------------------------------------------------------------------------

func TestW103S21ReachStatement(t *testing.T) {
	server := w103Doc(t, "internal/aihub/server/server.go")
	if !strings.Contains(server, `"/web"`) {
		t.Fatalf("the aihub server owns no /web prefix")
	}
	stepmem := w103Doc(t, "internal/polyforge/daemon/stepmem.go")
	tasks := w103Doc(t, "internal/polyforge/daemon/tasks.go")
	if !strings.Contains(strings.ToLower(stepmem), "web") && !strings.Contains(strings.ToLower(tasks), "web") {
		t.Fatalf("the daemon step/task memory proxies carry no reach statement for the console")
	}
	for name, src := range map[string]string{"stepmem.go": stepmem, "tasks.go": tasks} {
		for _, parameter := range []string{`q.Get("after")`, `q.Get("sort")`, `"after":`, `"sort":`} {
			if strings.Contains(src, parameter) {
				t.Fatalf("%s forwards the browse parameter %q (the proxy shape must stay unchanged)", name, parameter)
			}
		}
	}
	docs := strings.ToLower(w103Doc(t, "docs/architecture.md") + w103Doc(t, "docs/user-guide.md"))
	if !strings.Contains(docs, "console") || !strings.Contains(docs, "step") {
		t.Fatalf("the docs do not state that a step agent cannot reach the console")
	}
}
