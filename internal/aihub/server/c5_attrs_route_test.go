package server

// c5_attrs_route_test.go (workflow contract 5, design §3.4):
// PATCH /work-items/{id}/attrs is a signed machine route for a project
// writer — an unsigned request, or one signed for a viewer, is refused
// before anything is written; a write whose `if` does not hold answers
// CORE-409 with its stable reason; the write's event carries the signing
// machine and the owner message. The daemon's own client reaches it by a
// work item's slug (the signature covers the unescaped path). The list
// filters on attrs paths (?attr=<path>:<value>, repeatable). The removed
// integration routes answer nothing.

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

func TestC5AttrsRouteIsASignedWritersRoute(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "c5 attrs route")
	path := "/api/v1/work-items/" + id + "/attrs"
	body := map[string]any{"key": "integration", "value": map[string]any{"state": "landed"}}
	if code, raw := e.do(t, "PATCH", path, e.key, body, nil); code != 401 {
		t.Fatalf("an unsigned write is refused: %d %s", code, raw)
	}
	readerID, readerKey := memberUser(t, e, "c5-viewer", "viewer")
	rpriv, _ := signedMachineFor(t, e, "m_c5_reader", readerID)
	if code, raw := e.doSigned(t, "PATCH", path, readerKey, "m_c5_reader", rpriv, 0, body); code != 403 {
		t.Fatalf("a viewer's machine is refused: %d %s", code, raw)
	}
	priv, _ := signedMachineFor(t, e, "m_c5", e.adminID)
	if code, raw := e.doSigned(t, "PATCH", path, e.key, "m_c5", priv, 0, body); code != 200 || !strings.Contains(string(raw), `"integration":{"state":"landed"}`) {
		t.Fatalf("a writer's machine writes: %d %s", code, raw)
	}
	fenced := map[string]any{"key": "integration", "value": map[string]any{"state": "merged"}, "if": map[string]any{"integration.state": "merged"},
		"owner_message": map[string]any{"words": "w", "by": "owner", "at": "t"}}
	if code, raw := e.doSigned(t, "PATCH", path, e.key, "m_c5", priv, 0, fenced); code != 409 || !strings.Contains(string(raw), "ATTRS_IF_MISMATCH") {
		t.Fatalf("a fence that does not hold: %d %s", code, raw)
	}
	fenced["if"] = map[string]any{"integration.state": "landed"}
	if code, raw := e.doSigned(t, "PATCH", path, e.key, "m_c5", priv, 0, fenced); code != 200 {
		t.Fatalf("a fence that holds: %d %s", code, raw)
	}
	var written []map[string]any
	for _, ev := range eventsOf(t, e, id) {
		if ev["type"] == "attrs_written" {
			p, _ := ev["payload"].(map[string]any)
			written = append(written, p)
		}
	}
	if len(written) != 2 || written[0]["machine_id"] != "m_c5" || written[0]["owner_message"] != nil {
		t.Fatalf("one event per write, naming the machine: %v", written)
	}
	if om, _ := written[1]["owner_message"].(map[string]any); om["words"] != "w" || om["by"] != "owner" || om["at"] != "t" {
		t.Fatalf("the owner message rides the event: %v", written[1])
	}
	for _, gone := range []string{"/api/v1/work-items/" + id + "/integration", "/api/v1/work-items/" + id + "/integration/clear-hold"} {
		if code, raw := e.do(t, "POST", gone, e.key, map[string]any{"from": "landed", "reason": "r"}, nil); code != 404 && code != 405 {
			t.Fatalf("%s is gone: %d %s", gone, code, raw)
		}
	}
}

// TestC5AttrsRouteThroughTheDaemonsClient: the daemon's client writes by a
// work item's slug — its '#' escaped on the wire — and the signature
// covers the path the server verifies; a fence refusal is the typed
// CORE-409.
func TestC5AttrsRouteThroughTheDaemonsClient(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "c5 client route")
	var wi struct {
		Slug string `json:"slug"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 || !strings.Contains(wi.Slug, "#") {
		t.Fatalf("the slug: %d %s", code, raw)
	}
	priv, _ := signedMachineFor(t, e, "m_c5c", e.adminID)
	c := aihubclient.New(e.ts.URL, e.key)
	c.SetMachineIdentity("m_c5c", priv)
	got, err := c.WriteAttrs(wi.Slug, api.AttrsWriteRequest{Key: "integration", Value: json.RawMessage(`{"state":"landed"}`)})
	if err != nil {
		t.Fatalf("a write by slug: %v", err)
	}
	if in, _ := got.Attrs["integration"].(map[string]any); in["state"] != "landed" {
		t.Fatalf("the answer: %+v", got)
	}
	_, err = c.WriteAttrs(wi.Slug, api.AttrsWriteRequest{Key: "integration", Value: json.RawMessage(`{"state":"merged"}`),
		If: map[string]json.RawMessage{"integration.state": json.RawMessage(`"merged"`)}})
	if ae, ok := err.(*api.Error); !ok || ae.Code != api.ErrCoreConflict {
		t.Fatalf("a fence refusal is CORE-409: %v", err)
	}
}

func TestC5ListFiltersOnAttrsOverHTTP(t *testing.T) {
	e := newSrvEnv(t)
	priv, _ := signedMachineFor(t, e, "m_c5l", e.adminID)
	landed := createExecWIOverHTTP(t, e, "landed")
	other := createExecWIOverHTTP(t, e, "other")
	createExecWIOverHTTP(t, e, "bare")
	for wi, v := range map[string]any{landed: map[string]any{"state": "landed", "batch": "alpha#9"}, other: map[string]any{"state": "landed", "batch": "alpha#8"}} {
		if code, raw := e.doSigned(t, "PATCH", "/api/v1/work-items/"+wi+"/attrs", e.key, "m_c5l", priv, 0, map[string]any{"key": "integration", "value": v}); code != 200 {
			t.Fatalf("seed %s: %d %s", wi, code, raw)
		}
	}
	list := func(q url.Values) (int, []string, string) {
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		code, raw := e.do(t, "GET", "/api/v1/work-items?"+q.Encode(), e.key, nil, &page)
		var ids []string
		for _, it := range page.Items {
			ids = append(ids, it.ID)
		}
		return code, ids, string(raw)
	}
	if code, ids, raw := list(url.Values{"project": {"alpha"}, "attr": {"integration.state:landed"}}); code != 200 || len(ids) != 2 {
		t.Fatalf("one filter: %d %v %s", code, ids, raw)
	}
	if code, ids, raw := list(url.Values{"project": {"alpha"}, "attr": {"integration.state:landed", "integration.batch:alpha#9"}}); code != 200 || len(ids) != 1 || ids[0] != landed {
		t.Fatalf("two filters, both holding: %d %v %s", code, ids, raw)
	}
	if code, _, raw := list(url.Values{"attr": {"integration.state"}}); code != 400 {
		t.Fatalf("a filter without its value: %d %s", code, raw)
	}
	if code, ids, raw := list(url.Values{"project": {"alpha"}, "integration": {"landed"}}); code != 200 || len(ids) != 3 {
		t.Fatalf("the removed integration filter filters nothing: %d %v %s", code, ids, raw)
	}
}
