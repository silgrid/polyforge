package server

// w30_projects_list_test.go — the admin project list's deactivation
// default (polyforge#30, AC6): GET /api/v1/projects lists only active
// projects, ?include_deactivated=1 adds the deactivated ones (each row
// carrying its status), and the total stays the true project count —
// the same page semantics a capped list already has.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// TestW30ProjectsListDefaultHidesDeactivated: the default admin list
// hides the deactivated project; the include flag brings it back with
// its status.
func TestW30ProjectsListDefaultHidesDeactivated(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w30-live"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "w30-ghost"}); err != nil {
		t.Fatal(err)
	}
	if code, body := e.do(t, "POST", "/api/v1/projects/w30-ghost/deactivate", e.key, nil, nil); code != http.StatusOK {
		t.Fatalf("deactivate w30-ghost: %d %s", code, body)
	}

	code, body := e.do(t, "GET", "/api/v1/projects", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("default list: %d %s", code, body)
	}
	if strings.Contains(string(body), "w30-ghost") {
		t.Fatalf("the default list shows the deactivated project: %s", body)
	}
	if !strings.Contains(string(body), "w30-live") {
		t.Fatalf("the default list misses the active project: %s", body)
	}
	if !strings.Contains(string(body), `"total":3`) { // alpha + w30-live + w30-ghost
		t.Fatalf("the total must stay the true project count: %s", body)
	}

	code, body = e.do(t, "GET", "/api/v1/projects?include_deactivated=1", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("include list: %d %s", code, body)
	}
	if !strings.Contains(string(body), "w30-ghost") || !strings.Contains(string(body), `"status":"deactivated"`) {
		t.Fatalf("the include list misses the deactivated project with its status: %s", body)
	}
	if !strings.Contains(string(body), `"status":"active"`) {
		t.Fatalf("the include list misses the active statuses: %s", body)
	}
}
