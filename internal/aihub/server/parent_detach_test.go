// carries: internal/aihub/core/workitem.go

package server

// parent_detach_test.go — polyforge#77 (AC5): detaching a child over the
// PATCH door. parent_id may only be cleared; a running child is refused;
// a detach records wi_detached and the parent stops waiting for it.

import (
	"testing"
)

func TestDetachChildOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	type wiFace struct {
		ID         string  `json:"id"`
		Slug       string  `json:"slug"`
		ParentID   *string `json:"parent_id"`
		WaitingFor []struct {
			Slug string `json:"slug"`
			Via  string `json:"via"`
		} `json:"waiting_for"`
	}
	create := func(body map[string]any) wiFace {
		t.Helper()
		var wi wiFace
		if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, body, &wi); code != 201 {
			t.Fatalf("create: %d %s", code, raw)
		}
		return wi
	}
	parent := create(map[string]any{"project": "alpha", "goal": "bound parent", "bundle_name": "demo-bundle"})
	running := create(map[string]any{"project": "alpha", "goal": "running child", "parent_id": parent.ID, "bundle_name": "demo-bundle"})
	queued := create(map[string]any{"project": "alpha", "goal": "queued child", "parent_id": parent.ID, "bundle_name": "demo-bundle"})
	other := create(map[string]any{"project": "alpha", "goal": "another parent"})
	patch := func(id string, value any) (int, []byte) {
		t.Helper()
		return e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key, map[string]any{"parent_id": value}, nil)
	}

	// Re-parenting: CORE-400 IMMUTABLE.
	if code, raw := patch(queued.ID, other.ID); code != 400 {
		t.Fatalf("re-parent: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-400" || r != "IMMUTABLE" {
		t.Fatalf("re-parent: %s/%s, want CORE-400/IMMUTABLE", c, r)
	}

	// A running child: CORE-409 CHILD_RUNNING.
	if code, raw, _ := sessClaim(t, e, running.ID, "e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1e1", ""); code != 200 {
		t.Fatalf("claim child: %d %s", code, raw)
	}
	if code, raw := patch(running.ID, nil); code != 409 {
		t.Fatalf("detach running child: %d %s", code, raw)
	} else if c, r := errEnvelope(t, raw); c != "CORE-409" || r != "CHILD_RUNNING" {
		t.Fatalf("detach running child: %s/%s, want CORE-409/CHILD_RUNNING", c, r)
	}

	// The queued child detaches: parent_id null, one wi_detached event.
	var got wiFace
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+queued.ID, e.key, map[string]any{"parent_id": nil}, &got); code != 200 {
		t.Fatalf("detach queued child: %d %s", code, raw)
	}
	if got.ParentID != nil {
		t.Fatalf("detached child parent_id = %q, want null", *got.ParentID)
	}
	n := 0
	for _, ev := range eventsOf(t, e, queued.ID) {
		if ev["type"] == "wi_detached" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("wi_detached events = %d, want 1", n)
	}
	// The parent waits only for the running child now.
	var p wiFace
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+parent.ID, e.key, nil, &p); code != 200 {
		t.Fatalf("get parent: %d %s", code, raw)
	}
	if len(p.WaitingFor) != 1 || p.WaitingFor[0].Slug != running.Slug || p.WaitingFor[0].Via != "child" {
		t.Fatalf("parent waiting_for = %+v, want only %s via child", p.WaitingFor, running.Slug)
	}
}
