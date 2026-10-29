package server

// web_api_test.go: the private JSON face — the context
// answer (exactly the caller's readable projects, the admin flag), the
// work-item list page (the readable gates, the typed 400s, the shared
// static 404) — all through the real wrapper (headers, Basic auth,
// method gate).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// webJSONReq issues one authenticated console JSON request and decodes
// the body when out is non-nil.
func webJSONReq(t *testing.T, e *srvEnv, path, key string, out any) (int, string) {
	t.Helper()
	code, _, body := webReq(t, e, "GET", path, webBasic("u", key))
	if out != nil && code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), out); err != nil {
			t.Fatalf("%s is not JSON: %v (%s)", path, err, body)
		}
	}
	return code, body
}

func TestWebAPIContextMatchesReadableSet(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "webother"}); err != nil {
		t.Fatal(err)
	}
	_, member := memberUser(t, e, "web-ctx-member", "viewer")
	var ctx webContext
	if code, body := webJSONReq(t, e, "/web/api/context", e.key, &ctx); code != http.StatusOK {
		t.Fatalf("admin context = %d: %s", code, body)
	}
	if !ctx.Admin || ctx.Role != "admin" || len(ctx.Projects) != 2 {
		t.Fatalf("admin context = %#v, want both projects and the admin flag", ctx)
	}
	if ctx.Projects[0].Name != "alpha" || ctx.Projects[0].ID == "" {
		t.Fatalf("context projects are not name-sorted with ids: %#v", ctx.Projects)
	}
	if code, body := webJSONReq(t, e, "/web/api/context", member, &ctx); code != http.StatusOK {
		t.Fatalf("member context = %d: %s", code, body)
	}
	if ctx.Admin || len(ctx.Projects) != 1 || ctx.Projects[0].Name != "alpha" {
		t.Fatalf("member context = %#v, want only alpha and no admin flag", ctx)
	}
	_, outKey := memberUser(t, e, "web-ctx-out", "")
	if code, body := webJSONReq(t, e, "/web/api/context", outKey, &ctx); code != http.StatusOK {
		t.Fatalf("non-member context = %d: %s", code, body)
	}
	if len(ctx.Projects) != 0 {
		t.Fatalf("a key with no readable project lists %#v", ctx.Projects)
	}
}

func TestWebAPIWorkItemsListGatesAndPage(t *testing.T) {
	e := newSrvEnv(t)
	wis := core.NewWIManager(e.db)
	own, err := wis.Create(core.CreateRequest{Project: "alpha", Goal: "web list own goal", Reporter: e.adminID})
	if err != nil {
		t.Fatal(err)
	}
	_, member := memberUser(t, e, "web-list-member", "viewer")

	// Missing project = 400.
	if code, body := webJSONReq(t, e, "/web/api/work-items", e.key, nil); code != http.StatusBadRequest || body != webBodyBadRequest {
		t.Fatalf("missing project = %d %q, want the static 400", code, body)
	}
	// Unknown status = 400.
	if code, _ := webJSONReq(t, e, "/web/api/work-items?project=alpha&status=nope", e.key, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown status = %d, want 400", code)
	}
	// Unknown/unreadable project = the static 404, byte-identical.
	c1, b1 := webJSONReq(t, e, "/web/api/work-items?project=web-none", e.key, nil)
	// An unreadable project with a VALID key answers the same bytes.
	_, outKey := memberUser(t, e, "web-list-out", "")
	c2, b2 := webJSONReq(t, e, "/web/api/work-items?project=alpha", outKey, nil)
	if c1 != http.StatusNotFound || c2 != http.StatusNotFound || b1 != webBodyNotFound || b1 != b2 {
		t.Fatalf("unknown = %d %q, unreadable = %d %q; want the identical static 404", c1, b1, c2, b2)
	}
	// A member of alpha reads the list (the viewer role suffices).
	if c, body := webJSONReq(t, e, "/web/api/work-items?project=alpha", member, nil); c != http.StatusOK {
		t.Fatalf("member list = %d: %s", c, body)
	}
	var page webWorkItemList
	if code, body := webJSONReq(t, e, "/web/api/work-items?project="+url.QueryEscape("alpha"), e.key, &page); code != http.StatusOK {
		t.Fatalf("list = %d: %s", code, body)
	}
	if len(page.Items) != 1 || page.Items[0].ID != own.ID || page.Items[0].Slug != "alpha#1" {
		t.Fatalf("list = %#v, want %s as alpha#1", page.Items, own.ID)
	}
	if page.Items[0].CreatedAt == "" || page.Items[0].Content != "" {
		t.Fatalf("list row shape wrong: %#v", page.Items[0])
	}
	// A scoped key of another project never reads this one.
	other, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "webscoped"})
	if err != nil {
		t.Fatal(err)
	}
	scopedUser, _ := memberUser(t, e, "web-list-scoped", "viewer")
	scoped, err := e.admin.CreateAPIKey(scopedUser, "scoped", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := webJSONReq(t, e, "/web/api/work-items?project=alpha", scoped.Plain, nil); code != http.StatusNotFound {
		t.Fatalf("scoped key list = %d: %s", code, body)
	}
}

func TestWebAPIWorkItemDetailProjection(t *testing.T) {
	e := newSrvEnv(t)
	wis := core.NewWIManager(e.db)
	_, member := memberUser(t, e, "web-detail-member", "viewer")
	hostile := "<script>alert(1)</script> goal"
	wi, err := wis.Create(core.CreateRequest{
		Project: "alpha", Goal: hostile, Reporter: e.adminID,
		Content:    strPtrTest("# Head\n\n<script>alert(2)</script>\n\n[click](javascript:alert(3))"),
		Labels:     []string{"web"},
		BundleName: strPtrTest("web-bundle"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A readable target this item blocks (same project) and an
	// unreadable cross-project blocker seeded in SQL (core refuses
	// cross-project edges; the console must still conceal the target).
	victim, err := wis.Create(core.CreateRequest{Project: "alpha", Goal: "web readable victim", Reporter: e.adminID})
	if err != nil {
		t.Fatal(err)
	}
	if err := wis.AddDependency(wi.ID, victim.ID, "blocks"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "websecret"}); err != nil {
		t.Fatal(err)
	}
	hidden, err := wis.Create(core.CreateRequest{Project: "websecret", Goal: "web hidden secret blocker", Reporter: e.adminID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO wi_dependencies(blocking_wi_id, blocked_wi_id, kind, created_at) VALUES (?1, ?2, 'blocks', ?3)`,
		hidden.ID, wi.ID, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	// Sensitive columns and a hostile timeline row.
	if _, err := e.db.Exec(`UPDATE work_items SET session_hash='webhashsentinel', session_fingerprint='webfpsentinel', session_machine='webmachinesentinel', attrs='{"web":"webattrssentinel"}' WHERE id=?1`, wi.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO wi_events(work_item_id, created_at, actor, event_type, payload) VALUES (?1, ?2, ?3, ?4, ?5)`,
		wi.ID, "2026-01-02T03:04:05Z", e.adminID, "wi_content_updated",
		`{"note":"web payload","session_hash":"webhashsentinel","machine_id":"webmachinesentinel"}`); err != nil {
		t.Fatal(err)
	}

	path := "/web/api/work-items/" + wi.ID + "?project=alpha"
	code, body := webJSONReq(t, e, path, member, nil)
	if code != http.StatusOK {
		t.Fatalf("detail = %d: %s", code, body)
	}
	var d webWIDetail
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("detail JSON: %v (%s)", err, body)
	}
	if d.HTML == "" || strings.Contains(strings.ToLower(d.HTML), "<script") || strings.Contains(strings.ToLower(d.HTML), `href="javascript:`) {
		t.Fatalf("content is not sanitized inert HTML: %q", d.HTML)
	}
	if len(d.Blocking) != 1 || d.Blocking[0].ID != victim.ID || d.Blocking[0].Slug == "" {
		t.Fatalf("readable target edge = %#v", d.Blocking)
	}
	if len(d.BlockedBy) != 1 || d.BlockedBy[0].ID != hidden.ID {
		t.Fatalf("hidden blocker edge = %#v", d.BlockedBy)
	}
	if d.BlockedBy[0].Slug != "" || d.BlockedBy[0].Status != "" || strings.Contains(body, "web hidden secret blocker") {
		t.Fatalf("the unreadable target leaks: %#v (%s)", d.BlockedBy[0], body)
	}
	if len(d.Events) == 0 || d.Events[0].Type != "wi_content_updated" || d.Events[0].Actor == "" {
		t.Fatalf("timeline = %#v", d.Events)
	}
	if !strings.Contains(string(d.Events[0].Payload), "web payload") ||
		strings.Contains(string(d.Events[0].Payload), "session_hash") ||
		strings.Contains(string(d.Events[0].Payload), "machine_id") {
		t.Fatalf("payload redaction wrong: %s", d.Events[0].Payload)
	}
	for _, leak := range []string{"webhashsentinel", "webfpsentinel", "webmachinesentinel", "webattrssentinel",
		`"session_hash"`, `"attrs"`, `"session_fingerprint"`, `"session_machine"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("the detail leaks %q: %s", leak, body)
		}
	}
}

func TestWebAPIWorkItemDetail404Classes(t *testing.T) {
	e := newSrvEnv(t)
	wis := core.NewWIManager(e.db)
	wi, err := wis.Create(core.CreateRequest{Project: "alpha", Goal: "web 404 target", Reporter: e.adminID})
	if err != nil {
		t.Fatal(err)
	}
	_, member := memberUser(t, e, "web-404-member", "viewer")
	_, outKey := memberUser(t, e, "web-404-out", "")
	base := "/web/api/work-items/"
	// Unknown id, unreadable id and a wrong project are the identical
	// static 404; missing project is the static 400.
	c1, b1 := webJSONReq(t, e, base+"wi_web_missing?project=alpha", member, nil)
	c2, b2 := webJSONReq(t, e, base+wi.ID+"?project=alpha", outKey, nil)
	c3, b3 := webJSONReq(t, e, base+wi.ID+"?project=websecret", member, nil)
	if c1 != http.StatusNotFound || c2 != http.StatusNotFound || c3 != http.StatusNotFound || b1 != webBodyNotFound || b1 != b2 || b1 != b3 {
		t.Fatalf("404 classes differ: %d/%d/%d %q/%q/%q", c1, c2, c3, b1, b2, b3)
	}
	if c, body := webJSONReq(t, e, base+wi.ID, member, nil); c != http.StatusBadRequest || body != webBodyBadRequest {
		t.Fatalf("missing project = %d %q, want the static 400", c, body)
	}
}

func TestWebAPIMemoriesBrowseSearchDetail(t *testing.T) {
	e := newSrvEnv(t)
	// 25 project memories: the browse page shows 20 with a cursor, the
	// walk covers the rest.
	for i := 0; i < 25; i++ {
		scopeMem(t, e, "alpha", "web memory body "+strings.Repeat("n", i+1))
	}
	_, member := memberUser(t, e, "web-mem-member", "viewer")

	var page webMemoryList
	if code, body := webJSONReq(t, e, "/web/api/memories?project=alpha", member, &page); code != http.StatusOK {
		t.Fatalf("browse = %d: %s", code, body)
	}
	if len(page.Items) != 20 || page.NextCursor == "" || page.RefineHint {
		t.Fatalf("browse page = %d rows, cursor %q, hint %v; want 20 + cursor, no hint", len(page.Items), page.NextCursor, page.RefineHint)
	}
	if page.Items[0].ID == "" || page.Items[0].Title == "" || page.Items[0].Type == "" || page.Items[0].UpdatedAt == "" {
		t.Fatalf("browse row shape: %#v", page.Items[0])
	}
	var next webMemoryList
	if code, body := webJSONReq(t, e, "/web/api/memories?project=alpha&after="+url.QueryEscape(page.NextCursor), member, &next); code != http.StatusOK {
		t.Fatalf("browse page 2 = %d: %s", code, body)
	}
	if len(next.Items) != 5 || next.NextCursor != "" {
		t.Fatalf("browse page 2 = %d rows, cursor %q; want 5, no cursor", len(next.Items), next.NextCursor)
	}
	// A cursor this face did not issue is the static 400.
	if code, body := webJSONReq(t, e, "/web/api/memories?project=alpha&after=zz", member, nil); code != http.StatusBadRequest || body != webBodyBadRequest {
		t.Fatalf("bad cursor = %d %q, want the static 400", code, body)
	}
	// The type filter narrows through the store's own semantics.
	if code, body := webJSONReq(t, e, "/web/api/memories?project=alpha&type=experience.*", member, &page); code != http.StatusOK {
		t.Fatalf("type filter = %d: %s", code, body)
	}
	if len(page.Items) == 0 {
		t.Fatalf("type filter matched nothing")
	}
	// Keyword: one page of the top 50, hint only when full.
	var kw webMemoryList
	if code, body := webJSONReq(t, e, "/web/api/memories?project=alpha&q=memory", member, &kw); code != http.StatusOK {
		t.Fatalf("keyword = %d: %s", code, body)
	}
	if len(kw.Items) != 25 || kw.NextCursor != "" || kw.RefineHint {
		t.Fatalf("keyword page = %d rows cursor %q hint %v; want all 25, no cursor, no hint", len(kw.Items), kw.NextCursor, kw.RefineHint)
	}
	// Detail: sanitized HTML + header-card fields.
	mem := scopeMem(t, e, "alpha", "# Web head\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(2))")
	var d webMemoryDetail
	if code, body := webJSONReq(t, e, "/web/api/memories/"+mem+"?project=alpha", member, &d); code != http.StatusOK {
		t.Fatalf("detail = %d: %s", code, body)
	}
	if d.HTML == "" || strings.Contains(strings.ToLower(d.HTML), "<script") || strings.Contains(strings.ToLower(d.HTML), `href="javascript:`) {
		t.Fatalf("detail html is not inert: %q", d.HTML)
	}
	if d.Type == "" || d.Visibility == "" || d.UpdatedAt == "" {
		t.Fatalf("detail shape: %#v", d)
	}
	// Unknown, unreadable and other-project answers are the identical
	// static 404.
	c1, b1 := webJSONReq(t, e, "/web/api/memories/mem_web_missing?project=alpha", member, nil)
	_, outKey := memberUser(t, e, "web-mem-out", "")
	c2, b2 := webJSONReq(t, e, "/web/api/memories/"+mem+"?project=alpha", outKey, nil)
	c3, b3 := webJSONReq(t, e, "/web/api/memories/"+mem+"?project=webother", member, nil)
	if c1 != http.StatusNotFound || c2 != http.StatusNotFound || c3 != http.StatusNotFound || b1 != webBodyNotFound || b1 != b2 || b1 != b3 {
		t.Fatalf("memory 404 classes differ: %d/%d/%d", c1, c2, c3)
	}
	if code, body := webJSONReq(t, e, "/web/api/memories/"+mem, member, nil); code != http.StatusBadRequest || body != webBodyBadRequest {
		t.Fatalf("missing project = %d %q, want the static 400", code, body)
	}
}

func TestWebAPISearchGroupsAndCaps(t *testing.T) {
	e := newSrvEnv(t)
	wis := core.NewWIManager(e.db)
	for i := 0; i < 25; i++ {
		if _, err := wis.Create(core.CreateRequest{
			Project: "alpha", Goal: "web searchable item", Reporter: e.adminID,
			Content: strPtrTest("needle body"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 25; i++ {
		var out struct {
			ID string `json:"id"`
		}
		if code, raw := e.do(t, "POST", "/api/v1/memories", e.key, map[string]any{
			"project": "alpha", "type": "experience.debug", "visibility": "project",
			"title":   fmt.Sprintf("web needle memory %02d", i),
			"content": "needle body " + strings.Repeat("m", i+1),
		}, &out); code != http.StatusCreated || out.ID == "" {
			t.Fatalf("create search memory %d: %d %s", i, code, raw)
		}
	}
	// Another project's matching objects must never leak into the
	// selected project's groups.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "websearchother"}); err != nil {
		t.Fatal(err)
	}
	if _, err := wis.Create(core.CreateRequest{
		Project: "websearchother", Goal: "web foreign needle", Reporter: e.adminID,
	}); err != nil {
		t.Fatal(err)
	}
	_, member := memberUser(t, e, "web-search-member", "viewer")

	var res webSearchResults
	if code, body := webJSONReq(t, e, "/web/api/search?project=alpha&q=needle", member, &res); code != http.StatusOK {
		t.Fatalf("search = %d: %s", code, body)
	} else if strings.Contains(body, "content_excerpt") {
		t.Fatalf("a search memory row still carries a content excerpt: %s", body)
	}
	if len(res.WorkItems) != 20 || len(res.Memories) != 20 {
		t.Fatalf("groups = %d/%d, want 20/20", len(res.WorkItems), len(res.Memories))
	}
	for _, it := range res.WorkItems {
		if !strings.Contains(strings.ToLower(it.Goal+it.Content), "needle") {
			t.Fatalf("work-item hit does not match: %#v", it)
		}
		if it.Content == "" {
			t.Fatalf("a content match carries no content: %#v", it)
		}
	}
	for _, m := range res.Memories {
		if !strings.Contains(strings.ToLower(m.Title), "needle") {
			t.Fatalf("memory hit does not match: %#v", m)
		}
	}
	// Empty q and missing project are the static 400; an unreadable
	// project the static 404.
	if c, b := webJSONReq(t, e, "/web/api/search?project=alpha", member, nil); c != http.StatusBadRequest || b != webBodyBadRequest {
		t.Fatalf("empty q = %d %q, want 400", c, b)
	}
	if c, _ := webJSONReq(t, e, "/web/api/search?q=needle", member, nil); c != http.StatusBadRequest {
		t.Fatalf("missing project = %d, want 400", c)
	}
	_, outKey := memberUser(t, e, "web-search-out", "")
	if c, b := webJSONReq(t, e, "/web/api/search?project=alpha&q=needle", outKey, nil); c != http.StatusNotFound || b != webBodyNotFound {
		t.Fatalf("unreadable project search = %d %q, want the static 404", c, b)
	}
}

func TestWebAPIMachinesAdminOnlySnapshot(t *testing.T) {
	e := newSrvEnv(t)
	wis := core.NewWIManager(e.db)
	liveWI, err := wis.Create(core.CreateRequest{Project: "alpha", Goal: "web live machine item", Reporter: e.adminID})
	if err != nil {
		t.Fatal(err)
	}
	live := liveWI.ID
	if _, err := e.db.Exec(`INSERT INTO machines(id, runtime_version, mode, live_wi_ids, last_heartbeat_at, public_key, launcher, role, operator_id, superseded_at, capability)
		VALUES ('web-m-green','v9','runner','["`+live+`"]',?1,'web-pk-sentinel','pf-cli','runner',?2,NULL,'{"cap":"web-cap-sentinel"}')`,
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), e.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO machines(id, runtime_version, mode, live_wi_ids, last_heartbeat_at, launcher, role, operator_id)
		VALUES ('web-m-red','v9','runner','[]',?1,'pf-cli','runner',?2)`,
		time.Now().UTC().Add(-2*time.Hour).Format(time.RFC3339Nano), e.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE machines SET superseded_at=?1 WHERE id='web-m-red'`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	var snap webMachinesSnapshot
	if code, body := webJSONReq(t, e, "/web/api/machines", e.key, &snap); code != http.StatusOK {
		t.Fatalf("admin machines = %d: %s", code, body)
	}
	byID := map[string]webMachine{}
	for _, m := range snap.Machines {
		byID[m.ID] = m
	}
	if len(snap.Machines) != 2 {
		t.Fatalf("machines = %d, want the 2 seeded rows", len(snap.Machines))
	}
	if _, body := webJSONReq(t, e, "/web/api/machines", e.key, nil); strings.Contains(body, "web-pk-sentinel") || strings.Contains(body, "capability") || strings.Contains(body, "public_key") {
		t.Fatalf("machines JSON leaks identity material: %s", body)
	}
	green := byID["web-m-green"]
	if green.Stale || green.Superseded || green.HeartbeatAgeSecond == nil || *green.HeartbeatAgeSecond > 120 {
		t.Fatalf("a 1-minute-old machine is not fresh: %+v", green)
	}
	if len(green.LiveWIs) != 1 || green.LiveWIs[0].ID != live || green.LiveWIs[0].Slug == "" {
		t.Fatalf("live work-item link not projected: %+v", green.LiveWIs)
	}
	red := byID["web-m-red"]
	if !red.Stale || !red.Superseded {
		t.Fatalf("the 2-hour-old superseded machine is not marked: %+v", red)
	}

	// A non-admin gets the identical static 404 — byte-identical with
	// every other not-found answer.
	_, member := memberUser(t, e, "web-machines-member", "viewer")
	c1, b1 := webJSONReq(t, e, "/web/api/machines", member, nil)
	c2, b2 := webJSONReq(t, e, "/web/api/work-items/wi_web_missing?project=alpha", member, nil)
	if c1 != http.StatusNotFound || c2 != http.StatusNotFound || b1 != webBodyNotFound || b1 != b2 {
		t.Fatalf("non-admin machines = %d %q; want the identical static 404 (%d %q)", c1, b1, c2, b2)
	}
}
