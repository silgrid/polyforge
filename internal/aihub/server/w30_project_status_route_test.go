package server

// w30_project_status_route_test.go — the project deactivation doors
// over real HTTP (POST /api/v1/projects/{id}/deactivate and /restore,
// polyforge#30): the role matrix (global admin and the project's
// maintainer may; a writer, a viewer, a non-member and a key scoped to
// another project are refused; an unknown project answers CORE-404),
// the typed refusal (CORE-409 NON_TERMINAL_WORK_ITEMS listing every
// blocking slug, nothing deleted), the state round trip, and the
// data-preservation contract (work items and the project row survive
// both directions — AC3, AC4, AC5).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// w30RouteUser is one principal of the matrix: a user, its project
// role ("" = not a member) and its api key.
type w30RouteUser struct {
	id   string
	role string
	key  string
}

// w30RouteEnv assembles the matrix fixture: project w30 (empty), the
// admin key, a maintainer, a writer, a viewer, a non-member, and a
// maintainer key scoped to the OTHER project (the scope leak).
func w30RouteEnv(t *testing.T) (*srvEnv, map[string]w30RouteUser) {
	t.Helper()
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w30"}); err != nil {
		t.Fatalf("create w30: %v", err)
	}
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "elsewhere"}); err != nil {
		t.Fatalf("create elsewhere: %v", err)
	}
	w30ID := w30RouteProjectID(t, e, "w30")
	elsewhereID := w30RouteProjectID(t, e, "elsewhere")
	users := map[string]w30RouteUser{}
	for name, role := range map[string]string{
		"w30-maintainer": "maintainer", "w30-writer": "writer", "w30-viewer": "viewer", "w30-outsider": "",
	} {
		u, err := e.admin.CreateUser(name, name+"@w30.invalid", "writer", "human")
		if err != nil {
			t.Fatalf("user %s: %v", name, err)
		}
		issued, err := e.admin.CreateAPIKey(u.ID, name, "")
		if err != nil {
			t.Fatalf("key %s: %v", name, err)
		}
		if role != "" {
			if err := e.admin.AddMember(w30ID, u.ID, role); err != nil {
				t.Fatalf("member %s: %v", name, err)
			}
		}
		users[name] = w30RouteUser{id: u.ID, role: role, key: issued.Plain}
	}
	// The scoped leak: a second maintainer key that may only reach
	// "elsewhere".
	scoped, err := e.admin.CreateAPIKey(users["w30-maintainer"].id, "scoped", elsewhereID)
	if err != nil {
		t.Fatalf("scoped key: %v", err)
	}
	users["scoped-away"] = w30RouteUser{key: scoped.Plain}
	return e, users
}

// w30RouteProjectID answers a project's id through the admin read.
func w30RouteProjectID(t *testing.T, e *srvEnv, name string) string {
	t.Helper()
	p, err := e.admin.GetProject(name)
	if err != nil {
		t.Fatalf("project %s: %v", name, err)
	}
	return p.ID
}

// w30RouteStatus answers the project's status through the member read
// face (the maintainer key), so the assertions read the same field a
// client sees.
func w30RouteStatus(t *testing.T, e *srvEnv, key string) string {
	t.Helper()
	var p struct {
		Status string `json:"status"`
	}
	code, body := e.do(t, "GET", "/api/v1/projects/w30", key, nil, &p)
	if code != http.StatusOK {
		t.Fatalf("read w30: %d %s", code, body)
	}
	return p.Status
}

// TestW30ProjectDeactivateRouteAuthz: AC4 — the global admin and the
// project's maintainer may deactivate and restore; a project writer, a
// viewer, a non-member and a key scoped to another project are refused
// AUTH-403; an unknown project answers CORE-404 from both doors. Every
// case starts from the door's own entry state (the admin key resets
// it), so one principal's success cannot answer for the next.
func TestW30ProjectDeactivateRouteAuthz(t *testing.T) {
	e, users := w30RouteEnv(t)
	type want struct {
		who  string
		code int
	}
	for _, door := range []string{"deactivate", "restore"} {
		for _, w := range []want{
			{"admin", http.StatusOK},
			{"w30-maintainer", http.StatusOK},
			{"w30-writer", http.StatusForbidden},
			{"w30-viewer", http.StatusForbidden},
			{"w30-outsider", http.StatusForbidden},
			{"scoped-away", http.StatusForbidden},
		} {
			key := e.key
			if w.who != "admin" {
				key = users[w.who].key
			}
			// Reset to the door's entry state: deactivate ignores a typed
			// conflict, so the admin key converges both directions.
			if door == "deactivate" {
				e.do(t, "POST", "/api/v1/projects/w30/deactivate", e.key, nil, nil)
				e.do(t, "POST", "/api/v1/projects/w30/restore", e.key, nil, nil)
			} else {
				e.do(t, "POST", "/api/v1/projects/w30/deactivate", e.key, nil, nil)
			}
			code, body := e.do(t, "POST", "/api/v1/projects/w30/"+door, key, nil, nil)
			if code != w.code {
				t.Fatalf("%s by %s: %d %s, want %d", door, w.who, code, body, w.code)
			}
			if w.code == http.StatusForbidden && w.who != "scoped-away" && !strings.Contains(string(body), "maintainer") {
				t.Fatalf("%s by %s: refusal %s does not name the required role", door, w.who, body)
			}
		}
		// Unknown project: CORE-404 for the admin key too.
		code, body := e.do(t, "POST", "/api/v1/projects/prj_nope/"+door, e.key, nil, nil)
		if code != http.StatusNotFound {
			t.Fatalf("%s unknown project: %d %s, want 404", door, code, body)
		}
	}
}

// TestW30ProjectDeactivateRouteConflictAndRoundTrip: a queued work
// item blocks the door with the typed CORE-409 listing its slug and
// deleting nothing (AC3); once terminal, deactivate → the reads keep
// answering, restore → the project is active again and new work items
// are accepted immediately (AC5).
func TestW30ProjectDeactivateRouteConflictAndRoundTrip(t *testing.T) {
	e, users := w30RouteEnv(t)
	maint := users["w30-maintainer"].key
	var created map[string]any
	code, body := e.do(t, "POST", "/api/v1/work-items", users["w30-writer"].key,
		map[string]string{"project": "w30", "goal": "w30 route blocker"}, &created)
	if code != http.StatusCreated {
		t.Fatalf("create blocker: %d %s", code, body)
	}
	id, _ := created["id"].(string)

	code, body = e.do(t, "POST", "/api/v1/projects/w30/deactivate", maint, nil, nil)
	if code != http.StatusConflict {
		t.Fatalf("deactivate with a queued item: %d %s, want 409", code, body)
	}
	var refusal struct {
		Code   string          `json:"code"`
		Detail string          `json:"detail"`
		Extra  json.RawMessage `json:"extra"`
	}
	if err := json.Unmarshal(body, &refusal); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	var extra struct {
		Reason    string   `json:"reason"`
		WorkItems []string `json:"work_items"`
	}
	if err := json.Unmarshal(refusal.Extra, &extra); err != nil {
		t.Fatalf("decode refusal extra: %v", err)
	}
	if refusal.Code != api.ErrCoreConflict || extra.Reason != "NON_TERMINAL_WORK_ITEMS" {
		t.Fatalf("refusal %s/%s, want CORE-409 NON_TERMINAL_WORK_ITEMS", refusal.Code, extra.Reason)
	}
	if len(extra.WorkItems) != 1 || !strings.HasPrefix(extra.WorkItems[0], "w30#1") {
		t.Fatalf("refusal work_items %v, want the one blocking slug", extra.WorkItems)
	}
	if !strings.Contains(refusal.Detail, extra.WorkItems[0]) {
		t.Fatalf("refusal detail %q does not list the slug", refusal.Detail)
	}
	if got := w30RouteStatus(t, e, maint); got != api.ProjectStatusActive {
		t.Fatalf("status after the refused deactivation: %q, want active", got)
	}

	// Terminal the blocker through its own doors, then the round trip.
	var verdict map[string]any
	code, body = e.do(t, "POST", "/api/v1/work-items/"+id+"/cancel", maint,
		map[string]string{}, &verdict)
	if code != http.StatusOK {
		t.Fatalf("cancel blocker: %d %s", code, body)
	}

	var deactivated struct {
		Status string `json:"status"`
	}
	code, body = e.do(t, "POST", "/api/v1/projects/w30/deactivate", maint, nil, &deactivated)
	if code != http.StatusOK || deactivated.Status != api.ProjectStatusDeactivated {
		t.Fatalf("deactivate: %d %s (%q), want 200 deactivated", code, body, deactivated.Status)
	}
	// Reads keep answering with the original permissions while deactivated.
	if got := w30RouteStatus(t, e, users["w30-viewer"].key); got != api.ProjectStatusDeactivated {
		t.Fatalf("viewer read while deactivated: %q", got)
	}
	var restored struct {
		Status string `json:"status"`
	}
	code, body = e.do(t, "POST", "/api/v1/projects/w30/restore", maint, nil, &restored)
	if code != http.StatusOK || restored.Status != api.ProjectStatusActive {
		t.Fatalf("restore: %d %s (%q), want 200 active", code, body, restored.Status)
	}
	code, body = e.do(t, "POST", "/api/v1/work-items", users["w30-writer"].key,
		map[string]string{"project": "w30", "goal": "w30 after restore"}, nil)
	if code != http.StatusCreated {
		t.Fatalf("create after restore: %d %s, want 201 (AC5)", code, body)
	}
}
