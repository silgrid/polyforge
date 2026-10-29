package server

// list_wis_test.go: the WI list filters by the caller's
// readable-project set BEFORE the limit — a non-admin with readable
// items never gets an empty page behind newer unreadable rows, total
// stays "the returned count" for admin and non-admin alike, the
// membership lookup is one query per request (statement counts pinned by
// the counting driver), and ?project= on a non-member project still
// answers empty.

import (
	"fmt"
	"net/http"
	"testing"
)

// wiList runs one work-items list request and answers status, item ids
// and the total.
func wiList(t *testing.T, e *srvEnv, key, params string) (int, []string, int) {
	t.Helper()
	var r struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	code, _ := e.do(t, "GET", "/api/v1/work-items"+params, key, nil, &r)
	ids := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		ids = append(ids, it.ID)
	}
	return code, ids, r.Total
}

// newWI files one registration ticket in project.
func newWI(t *testing.T, e *srvEnv, project, goal string) string {
	t.Helper()
	var wi struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key,
		map[string]string{"project": project, "goal": goal}, &wi); code != http.StatusCreated || wi.ID == "" {
		t.Fatalf("create WI in %s: %d %s", project, code, raw)
	}
	return wi.ID
}

// TestListReadablePageNeverEmptyAndTotalsAgree: the non-admin member's
// page carries its readable item even when newer unreadable items fill
// the unfiltered window, with total == len(items) == min(limit,
// readable); the admin's total agrees with its own item count on the
// same request. Red on the pre- tree: the old list
// truncated first and filtered row by row, answering an empty page.
func TestListReadablePageNeverEmptyAndTotalsAgree(t *testing.T) {
	e := newCountingSrvEnv(t)
	seedBeta(t, e)
	readable := newWI(t, e, "alpha", "w81 the member's readable work item")
	for i := 0; i < 3; i++ {
		newWI(t, e, "beta", fmt.Sprintf("w81 newer unreadable item %d", i))
	}
	_, member := memberUser(t, e, "member", "viewer")

	counts.reset()
	code, ids, total := wiList(t, e, member, "?limit=2")
	if code != http.StatusOK {
		t.Fatalf("member list: %d", code)
	}
	if len(ids) == 0 || ids[0] != readable {
		t.Fatalf("member page = %v, want it to carry the readable item %s", ids, readable)
	}
	if total != len(ids) || len(ids) != 1 { // min(2, 1 readable) = 1
		t.Fatalf("member page = %v total %d, want 1 item with total == len(items)", ids, total)
	}
	totalStmts, membership := counts.snapshot()
	if membership != 1 {
		t.Fatalf("member list made %d membership statements, want exactly 1", membership)
	}

	// The admin sees everything on the same request; its total agrees
	// with its own item count the same way.
	code, adminIDs, adminTotal := wiList(t, e, e.key, "?limit=2")
	if code != http.StatusOK || len(adminIDs) != 2 || adminTotal != 2 {
		t.Fatalf("admin page = %v total %d, want 2 items with total == len(items)", adminIDs, adminTotal)
	}
	// The counting driver proves the admin path needs no membership
	// query at all.
	_, membership = counts.snapshot()
	if membership != 1 {
		t.Fatalf("admin list made %d membership statements, want the one from the member request above only", membership)
	}
	_ = totalStmts
}

// TestListStatementCountConstantAcrossUnreadableRows: the statement and
// membership-query counts of one list request do not grow with the
// number of unreadable rows (3 vs 30). Red on the pre- tree:
// every row cost one canRead membership lookup.
func TestListStatementCountConstantAcrossUnreadableRows(t *testing.T) {
	var perUnreadable [2]struct{ total, membership int }
	for i, unreadable := range []int{3, 30} {
		e := newCountingSrvEnv(t)
		seedBeta(t, e)
		newWI(t, e, "alpha", "w81 readable item")
		for j := 0; j < unreadable; j++ {
			newWI(t, e, "beta", fmt.Sprintf("w81 unreadable item %d", j))
		}
		_, member := memberUser(t, e, "member", "viewer")
		counts.reset()
		code, ids, total := wiList(t, e, member, "?limit=2")
		if code != http.StatusOK || len(ids) != 1 || total != 1 {
			t.Fatalf("member list: %d, %v total %d, want the one readable item", code, ids, total)
		}
		totalStmts, membership := counts.snapshot()
		perUnreadable[i].total, perUnreadable[i].membership = totalStmts, membership
		if membership != 1 {
			t.Fatalf("%d unreadable rows: %d membership statements, want exactly 1", unreadable, membership)
		}
	}
	if perUnreadable[0] != perUnreadable[1] {
		t.Fatalf("statement count grew with unreadable rows: 3 → %+v, 30 → %+v (total/membership)", perUnreadable[0], perUnreadable[1])
	}
}

// TestListProjectFilterStillNarrows: ?project=<non-member project>
// answers the empty page with total 0 — the permission narrowing is not
// bypassed by the project filter.
func TestListProjectFilterStillNarrows(t *testing.T) {
	e := newSrvEnv(t)
	seedBeta(t, e)
	newWI(t, e, "alpha", "w81 alpha item")
	newWI(t, e, "beta", "w81 beta item")
	_, member := memberUser(t, e, "member", "viewer")
	code, ids, total := wiList(t, e, member, "?project=beta&limit=10")
	if code != http.StatusOK || len(ids) != 0 || total != 0 {
		t.Fatalf("member ?project=beta = %v total %d (code %d), want the empty page", ids, total, code)
	}
	// ...and the member's own project still lists its item.
	if code, ids, total = wiList(t, e, member, "?project=alpha&limit=10"); code != http.StatusOK || len(ids) != 1 || total != 1 {
		t.Fatalf("member ?project=alpha = %v total %d (code %d), want the readable item", ids, total, code)
	}
}
