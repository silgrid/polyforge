package server

// polyforge#161: the dependency endpoints accept a slug on BOTH sides
// (path and blocking_wi_id), report the other side's slug and status on
// every edge, surface CORE-409 DEPENDENCY_CYCLE / CORE-400 cross-project
// refusals as typed envelopes, and the WI view carries the derived
// waiting_for list.

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

func slugOf(t *testing.T, e *srvEnv, id string) string {
	t.Helper()
	var wi struct {
		Slug string `json:"slug"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || wi.Slug == "" {
		t.Fatalf("get: %d %s", code, raw)
	}
	return wi.Slug
}

func TestDependencyEndpointsBySlug(t *testing.T) {
	e := newSrvEnv(t)
	c := aihubclient.New(e.ts.URL, e.key)
	aID := createExecWIOverHTTP(t, e, "deps blocker")
	bID := createExecWIOverHTTP(t, e, "deps blocked")
	a, b := slugOf(t, e, aID), slugOf(t, e, bID)

	raw, err := c.AddBlocksDependency(a, b)
	if err != nil {
		t.Fatalf("add by slug: %v", err)
	}
	var deps core.Deps
	if err := json.Unmarshal(raw, &deps); err != nil {
		t.Fatal(err)
	}
	if len(deps.BlockedBy) != 1 || deps.BlockedBy[0].WI != aID || deps.BlockedBy[0].Slug != a ||
		deps.BlockedBy[0].Status != api.StatusQueued || deps.BlockedBy[0].Kind != "blocks" {
		t.Fatalf("blocked_by = %+v", deps.BlockedBy)
	}
	raw, err = c.DependenciesRaw(a)
	if err != nil {
		t.Fatal(err)
	}
	deps = core.Deps{}
	if err := json.Unmarshal(raw, &deps); err != nil {
		t.Fatal(err)
	}
	if len(deps.Blocking) != 1 || deps.Blocking[0].Slug != b {
		t.Fatalf("blocking = %+v", deps.Blocking)
	}

	// The WI view (detail and list) carries waiting_for.
	var view core.WIView
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+url.PathEscape(b), e.key, nil, &view); code != 200 ||
		len(view.WaitingFor) != 1 || view.WaitingFor[0].Slug != a || view.WaitingFor[0].Status != api.StatusQueued {
		t.Fatalf("detail waiting_for: %d %s", code, raw)
	}
	var list struct {
		Items []core.WIView `json:"items"`
	}
	e.do(t, "GET", "/api/v1/work-items?project=alpha", e.key, nil, &list)
	found := false
	for _, it := range list.Items {
		if it.ID == bID {
			found = len(it.WaitingFor) == 1 && it.WaitingFor[0].Slug == a
		}
		if it.ID == aID && len(it.WaitingFor) != 0 {
			t.Fatalf("the blocker itself carries waiting_for: %+v", it.WaitingFor)
		}
	}
	if !found {
		t.Fatalf("list view lacks waiting_for for %s: %+v", b, list.Items)
	}

	// The detail has no include=deps: the dependencies endpoint is the
	// one dependency read path.
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+url.PathEscape(b)+"?include=deps", e.key, nil, nil); code != 400 {
		t.Fatalf("include=deps: %d %s, want 400", code, raw)
	}

	// Cycle: CORE-409 DEPENDENCY_CYCLE.
	if _, err := c.AddBlocksDependency(b, a); !isWire(err, "CORE-409", "DEPENDENCY_CYCLE") {
		t.Fatalf("cycle: %v", err)
	}
	// Unknown blocker slug: CORE-404.
	if _, err := c.AddBlocksDependency("alpha#999", b); !isWire(err, "CORE-404", "") {
		t.Fatalf("unknown blocker: %v", err)
	}
	// Cross-project: CORE-400.
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	var other struct {
		Slug string `json:"slug"`
	}
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "beta", "goal": "elsewhere"}, &other)
	if _, err := c.AddBlocksDependency(other.Slug, b); !isWire(err, "CORE-400", "") {
		t.Fatalf("cross-project: %v", err)
	}

	// Remove by slug on both sides.
	if _, err := c.RemoveBlocksDependency(a, b); err != nil {
		t.Fatalf("rm by slug: %v", err)
	}
	if _, err := c.RemoveBlocksDependency(a, b); !isWire(err, "CORE-404", "") {
		t.Fatalf("second rm: %v", err)
	}
	view = core.WIView{}
	if e.do(t, "GET", "/api/v1/work-items/"+url.PathEscape(b), e.key, nil, &view); len(view.WaitingFor) != 0 {
		t.Fatalf("waiting_for survived the edge removal: %+v", view.WaitingFor)
	}
}

func isWire(err error, code, reason string) bool {
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != code {
		return false
	}
	if reason == "" {
		return true
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ae.Extra, &extra)
	return extra.Reason == reason
}

// Re-adding an existing edge is idempotent (201 + current view), never a
// CORE-500 carrying raw SQLite text.
func TestDependencyDuplicateEdgeIdempotent(t *testing.T) {
	e := newSrvEnv(t)
	c := aihubclient.New(e.ts.URL, e.key)
	a := createExecWIOverHTTP(t, e, "dup blocker")
	b := createExecWIOverHTTP(t, e, "dup blocked")
	for i := 0; i < 2; i++ {
		raw, err := c.AddBlocksDependency(a, b)
		if err != nil {
			t.Fatalf("add #%d: %v", i+1, err)
		}
		var deps core.Deps
		if err := json.Unmarshal(raw, &deps); err != nil || len(deps.BlockedBy) != 1 {
			t.Fatalf("add #%d view: %v %s", i+1, err, raw)
		}
	}
}

// A `blocks` blocker must be an execution ticket: a registration ticket
// or an epic runs nothing that would release the edge (POST and create
// body alike).
func TestDependencyBlockerMustBeExecutionTicket(t *testing.T) {
	e := newSrvEnv(t)
	c := aihubclient.New(e.ts.URL, e.key)
	var reg, epic, child struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "alpha", "goal": "registration"}, &reg)
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "alpha", "goal": "epic"}, &epic)
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "alpha", "goal": "child",
		"parent_id": epic.ID, "bundle_name": "demo-bundle"}, &child); code != 201 {
		t.Fatalf("child: %d %s", code, raw)
	}
	b := createExecWIOverHTTP(t, e, "after a registration ticket")
	for _, blocker := range []string{reg.Slug, epic.Slug} {
		if _, err := c.AddBlocksDependency(blocker, b); !isWire(err, "CORE-400", "BLOCKER_NOT_EXECUTABLE") {
			t.Fatalf("blocker %s: %v", blocker, err)
		}
	}
	// The child waiting on its own epic is the same refusal.
	if _, err := c.AddBlocksDependency(epic.Slug, child.Slug); !isWire(err, "CORE-400", "BLOCKER_NOT_EXECUTABLE") {
		t.Fatalf("child after epic: %v", err)
	}
}

// --after in the create body is atomic: the edges land with the work
// item, and any bad entry creates nothing.
func TestCreateWithAfterAtomic(t *testing.T) {
	e := newSrvEnv(t)
	c := aihubclient.New(e.ts.URL, e.key)
	a := createExecWIOverHTTP(t, e, "atomic blocker")
	aSlug := slugOf(t, e, a)
	count := func() int {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM work_items`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	raw, err := c.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: "alpha", Goal: "after a",
		BundleName: strPtrSrv("demo-bundle"), After: []string{aSlug, a}})
	if err != nil {
		t.Fatalf("create --after: %v", err)
	}
	var view core.WIView
	if err := json.Unmarshal(raw, &view); err != nil || len(view.WaitingFor) != 1 || view.WaitingFor[0].Slug != aSlug {
		t.Fatalf("created view: %v %s", err, raw)
	}

	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	var other, reg struct {
		Slug string `json:"slug"`
	}
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "beta", "goal": "elsewhere", "bundle_name": "x"}, &other)
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "alpha", "goal": "registration"}, &reg)
	before := count()
	for _, tc := range []struct {
		after        []string
		code, reason string
	}{
		{[]string{aSlug, "alpha#999"}, "CORE-404", ""},
		{[]string{other.Slug}, "CORE-400", ""},
		{[]string{aSlug, reg.Slug}, "CORE-400", "BLOCKER_NOT_EXECUTABLE"},
	} {
		_, err := c.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: "alpha", Goal: "refused",
			BundleName: strPtrSrv("demo-bundle"), After: tc.after})
		if !isWire(err, tc.code, tc.reason) {
			t.Fatalf("after %v: %v", tc.after, err)
		}
	}
	if got := count(); got != before {
		t.Fatalf("a refused create wrote %d work item(s)", got-before)
	}
}

// Dependency writes are no existence oracle: a work item in a project the
// caller cannot read answers exactly like an unknown one.
func TestDependencyNoCrossProjectOracle(t *testing.T) {
	e := newSrvEnv(t)
	if _, err := e.admin.CreateProject(core.CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	var hidden struct {
		Slug string `json:"slug"`
	}
	e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{"project": "beta", "goal": "hidden", "bundle_name": "x"}, &hidden)
	b := createExecWIOverHTTP(t, e, "alpha blocked")
	bSlug := slugOf(t, e, b)
	u, err := e.admin.CreateUser("w161", "w161@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	key, err := e.admin.CreateAPIKey(u.ID, "w161", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.admin.AddMember(e.adminProjectID(t, "alpha"), u.ID, "writer"); err != nil {
		t.Fatal(err)
	}
	c := aihubclient.New(e.ts.URL, key.Plain)
	detail := func(err error) string {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != "CORE-404" {
			t.Fatalf("want CORE-404, got %v", err)
		}
		return ae.Detail
	}
	unknown := strings.Replace(hidden.Slug, "beta#", "beta#9", 1)
	for _, op := range []func(ref string) error{
		func(ref string) error { _, err := c.AddBlocksDependency(ref, bSlug); return err },
		func(ref string) error { _, err := c.RemoveBlocksDependency(ref, bSlug); return err },
		func(ref string) error {
			_, err := c.CreateWorkItem(aihubclient.CreateWorkItemRequest{Project: "alpha", Goal: "g", After: []string{ref}})
			return err
		},
	} {
		got, want := detail(op(hidden.Slug)), detail(op(unknown))
		if got != strings.Replace(want, unknown, hidden.Slug, 1) {
			t.Fatalf("hidden %q vs unknown %q", got, want)
		}
	}
}

func strPtrSrv(s string) *string { return &s }
