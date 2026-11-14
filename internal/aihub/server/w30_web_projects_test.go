package server

// w30_web_projects_test.go — the console face's deactivation halves
// (polyforge#30, AC7): /web/api/context hides the deactivated project
// by default and names its status and the caller's restore right per row
// when ?include_deactivated=1 asks for it; POST /web/api/projects/
// {name}/restore is the console's ONE write route — it requires the
// console's own custom header (a cross-site form cannot carry one, and
// the face holds no ambient credential beyond Basic auth), the Basic
// key's user must be a maintainer of the project or an admin, and every
// other /web path keeps the strict GET/HEAD method gate.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// w30WebCtxRow is one row of the context answer as the SPA reads it.
type w30WebCtxRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	CanRestore bool   `json:"can_restore"`
}

// w30WebContext runs GET /web/api/context and answers its rows.
func w30WebContext(t *testing.T, e *srvEnv, key, query string) (int, []w30WebCtxRow) {
	t.Helper()
	path := "/web/api/context"
	if query != "" {
		path += "?" + query
	}
	code, _, body := webReq(t, e, "GET", path, webBasic("u", key))
	if code != http.StatusOK {
		t.Fatalf("context %s: %d %s", query, code, body)
	}
	var ctx struct {
		Projects []w30WebCtxRow `json:"projects"`
	}
	if err := json.Unmarshal([]byte(body), &ctx); err != nil {
		t.Fatalf("context %s is not JSON: %v (%s)", query, err, body)
	}
	return code, ctx.Projects
}

// w30WebPostRestore issues the console restore write with or without the
// console's own header.
func w30WebPostRestore(t *testing.T, e *srvEnv, key, name string, withHeader bool) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/web/api/projects/"+name+"/restore", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", webBasic("u", key))
	if withHeader {
		req.Header.Set("X-PF-Console-Write", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// w30WebNames answers the rows' names.
func w30WebNames(rows []w30WebCtxRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// w30Row answers the named row.
func w30Row(rows []w30WebCtxRow, name string) (w30WebCtxRow, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return w30WebCtxRow{}, false
}

// TestW30WebProjectsConsoleFace: the context default-hides the
// deactivated project and the restore write is the one gated console
// door.
func TestW30WebProjectsConsoleFace(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "ghost"}); err != nil {
		t.Fatal(err)
	}
	ghostID, err := e.admin.GetProject("ghost")
	if err != nil {
		t.Fatal(err)
	}
	if code, body := e.do(t, "POST", "/api/v1/projects/ghost/deactivate", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("deactivate ghost: %d %s", code, body)
	}
	// The membership matrix (ghost-only; the admin key carries no
	// membership).
	_, maint := w30WebMember(t, e, "w30-web-maint", "maintainer", ghostID.ID)
	_, writer := w30WebMember(t, e, "w30-web-writer", "writer", ghostID.ID)
	_, viewer := w30WebMember(t, e, "w30-web-viewer", "viewer", ghostID.ID)
	_, outsider := memberUser(t, e, "w30-web-out", "")

	// The default context hides the deactivated project for every key.
	for _, key := range []string{e.key, maint, writer, viewer, outsider} {
		_, rows := w30WebContext(t, e, key, "")
		if ghost, ok := w30Row(rows, "ghost"); ok {
			t.Fatalf("the default context shows the deactivated project to %s: %+v", key, ghost)
		}
	}

	// ?include_deactivated=1: the deactivated row appears, with its
	// status, and can_restore is the caller's own right (admin and the
	// project's maintainer alone).
	_, rows := w30WebContext(t, e, writer, "include_deactivated=1")
	if len(rows) != 1 {
		t.Fatalf("writer include rows: %v", w30WebNames(rows))
	}
	if g, _ := w30Row(rows, "ghost"); g.Status != "deactivated" || g.CanRestore {
		t.Fatalf("writer's ghost row: %+v, want status deactivated and no restore right", g)
	}
	_, rows = w30WebContext(t, e, viewer, "include_deactivated=1")
	if g, _ := w30Row(rows, "ghost"); g.Status != "deactivated" || g.CanRestore {
		t.Fatalf("viewer's ghost row: %+v, want status deactivated and no restore right", g)
	}
	_, rows = w30WebContext(t, e, maint, "include_deactivated=1")
	if g, _ := w30Row(rows, "ghost"); g.Status != "deactivated" || !g.CanRestore {
		t.Fatalf("maintainer's ghost row: %+v, want the restore right", g)
	}
	_, rows = w30WebContext(t, e, e.key, "include_deactivated=1")
	if g, _ := w30Row(rows, "ghost"); g.Status != "deactivated" || !g.CanRestore {
		t.Fatalf("admin's ghost row: %+v, want the restore right", g)
	}

	// The write door: without the console's own header, nothing happens
	// for any key (the CSRF guard — a cross-site form cannot set one).
	for _, key := range []string{e.key, maint, writer} {
		if code, body := w30WebPostRestore(t, e, key, "ghost", false); code != http.StatusForbidden {
			t.Fatalf("restore without the header by %s: %d %s, want 403", key, code, body)
		}
	}
	// With the header: a viewer and a non-member are refused, the
	// maintainer restores, and the project is back in the default view.
	if code, _ := w30WebPostRestore(t, e, viewer, "ghost", true); code != http.StatusForbidden {
		t.Fatalf("restore by the viewer: %d, want 403", code)
	}
	if code, _ := w30WebPostRestore(t, e, outsider, "ghost", true); code != http.StatusNotFound {
		t.Fatalf("restore by a non-member: %d, want the concealed 404", code)
	}
	code, body := w30WebPostRestore(t, e, maint, "ghost", true)
	if code != http.StatusOK || !strings.Contains(body, `"status":"active"`) {
		t.Fatalf("restore by the maintainer: %d %s, want 200 active", code, body)
	}
	_, rows = w30WebContext(t, e, writer, "")
	if g, ok := w30Row(rows, "ghost"); !ok || g.Status != "active" {
		t.Fatalf("the restored project must be back in the default view: %v", w30WebNames(rows))
	}

	// The typed conflicts and the unknown project answer through the door.
	if code, body := w30WebPostRestore(t, e, e.key, "ghost", true); code != http.StatusConflict || !strings.Contains(body, "NOT_DEACTIVATED") {
		t.Fatalf("restore of an active project: %d %s, want the typed 409", code, body)
	}
	if code, body := w30WebPostRestore(t, e, e.key, "prj_nope", true); code != http.StatusNotFound {
		t.Fatalf("restore of an unknown project: %d %s, want 404", code, body)
	}

	// The method gate keeps its shape everywhere else: a POST to a read
	// route answers 405 (authenticated), and the write path itself
	// answers nothing but POST.
	if code, _, _ := webReq(t, e, "POST", "/web/api/context", webBasic("u", e.key)); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /web/api/context: %d, want 405 (the write gate opens for the restore route alone)", code)
	}
	if code, _, _ := webReq(t, e, "GET", "/web/api/projects/ghost/restore", webBasic("u", e.key)); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on the restore path: %d, want 405 (the door opens for POST alone)", code)
	}
}

// w30WebMember creates a member of the named project with an api key.
func w30WebMember(t *testing.T, e *srvEnv, name, role, projectID string) (string, string) {
	t.Helper()
	u, err := e.admin.CreateUser(name, name+"@w30.invalid", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := e.admin.CreateAPIKey(u.ID, name, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.admin.AddMember(projectID, u.ID, role); err != nil {
		t.Fatalf("member %s: %v", name, err)
	}
	return u.ID, issued.Plain
}
