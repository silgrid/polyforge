package server

// w174_create_attrs_http_test.go (polyforge#174 u2): the raw API face
// of the create boundary — POST /api/v1/work-items with a flat dotted
// attrs key (the `--stdin` body path, an old CLI, any API client) is
// CORE-400 naming the nested form, so no new flat dotted row can
// appear behind the fixed CLI. Authored red on the accepting server.

import (
	"strings"
	"testing"
)

// TestW174CreateRejectsFlatDottedAttrsOverHTTP: the wire keeps the one
// accepted shape; the nested path and a plain key keep creating.
func TestW174CreateRejectsFlatDottedAttrsOverHTTP(t *testing.T) {
	e := newSrvEnv(t)

	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
		"project": "alpha", "goal": "flat over http",
		"attrs": map[string]any{"split.origin": "wi_O", "split.unit": "bg-ui"},
	}, nil); code != 400 || !strings.Contains(string(raw), "CORE-400") ||
		!strings.Contains(string(raw), `"split.origin"`) {
		t.Fatalf("create with flat dotted attrs over HTTP: %d %s, want 400 CORE-400 naming the key", code, raw)
	}

	var wi struct {
		ID    string         `json:"id"`
		Attrs map[string]any `json:"attrs"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/work-items", e.key, map[string]any{
		"project": "alpha", "goal": "nested over http",
		"attrs": map[string]any{"split": map[string]any{"origin": "wi_O", "unit": "bg-ui"}},
	}, &wi); code != 201 || wi.ID == "" {
		t.Fatalf("create with the nested shape over HTTP: %d %s, want 201", code, raw)
	}
	// the created row is listable by the same dot paths (the round trip
	// the flat key could never make).
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items?project=alpha&attr=split.origin:wi_O&attr=split.unit:bg-ui", e.key, nil, &list); code != 200 || len(list.Items) != 1 || list.Items[0].ID != wi.ID {
		t.Fatalf("list by the same attr paths: %d %s (items %+v), want exactly %s", code, raw, list.Items, wi.ID)
	}
}
