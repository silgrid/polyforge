package server

// carries: internal/aihub/memory/store.go, internal/aihub/memory/browse.go, internal/aihub/server/server.go, internal/aihub/server/w107_writer_test.go, docs/design/polyforge-vnext/06-memory-search.md

// w107_writer_test.go — polyforge#107 AC2/AC3/AC5 at the HTTP face. The
// server's writer wiring landed with the store's filter unit (its
// signature and implementation commits forward the URL parameter on
// both search modes and pass the body through), so these cases are a
// regression pin green from birth, not a red-first unit: TestW107Writer
// and TestW107Filter pin the wire contract end to end, and
// TestW107Compat pins AC5's compatibility — an old-shape request (no
// writer fields) must succeed before and after the change alike, so it
// has no red first leg by definition.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// w107Write posts one memory and answers (status, the raw body).
func w107Write(t *testing.T, e *srvEnv, key string, body map[string]any) (int, []byte) {
	t.Helper()
	return e.do(t, "POST", "/api/v1/memories", key, body, nil)
}

// w107RowOf answers the row's writer_kind / writer_details / display
// name, from one served JSON item.
func w107RowOf(t *testing.T, raw []byte) (kind, details any, name any) {
	t.Helper()
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("not a JSON object: %v (%s)", err, raw)
	}
	if v, ok := item["items"]; ok {
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			t.Fatalf("the list answer carries no item: %s", raw)
		}
		item, _ = list[0].(map[string]any)
		if item == nil {
			t.Fatalf("the list answer's first item is not an object: %s", raw)
		}
	}
	return item["writer_kind"], item["writer_details"], item["owner_display_name"]
}

func TestW107Writer(t *testing.T) {
	e := newSrvEnv(t)
	_, writer := memberUser(t, e, "w107-writer", "writer")

	// Create with the writer: the answer and every read face serve it.
	code, raw := w107Write(t, e, writer, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content":        "w107 http step write needle",
		"writer_kind":    "step",
		"writer_details": map[string]any{"work_item": "wi_107", "step": "build", "attempt": 1},
	})
	if code != http.StatusCreated {
		t.Fatalf("create with writer: %d %s", code, raw)
	}
	kind, details, name := w107RowOf(t, raw)
	if kind != "step" || name != "w107-writer" {
		t.Fatalf("create answer writer_kind/owner = %v / %v, want step / the writer's name: %s", kind, name, raw)
	}
	det, ok := details.(map[string]any)
	if !ok || det["work_item"] != "wi_107" || det["step"] != "build" || det["attempt"] != float64(1) {
		t.Fatalf("create answer writer_details = %v, want the step detail object: %s", details, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("create answer carries no id: %s", raw)
	}

	for face, path := range map[string]string{
		"by id":   "/api/v1/memories/" + created.ID,
		"keyword": "/api/v1/memories/search?q=needle&excerpt=32",
		"browse":  "/api/v1/memories/search?limit=20",
	} {
		code, raw := e.do(t, "GET", path, e.key, nil, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", face, code, raw)
		}
		kind, details, name := w107RowOf(t, raw)
		if kind != "step" || name != "w107-writer" {
			t.Fatalf("%s serves writer_kind/owner %v / %v, want step / the writer's name: %s", face, kind, name, raw)
		}
		if d, ok := details.(map[string]any); !ok || d["step"] != "build" {
			t.Fatalf("%s serves writer_details %v, want the step detail object: %s", face, details, raw)
		}
	}

	// Create without the writer: NULL stored, null served.
	code, raw = w107Write(t, e, writer, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content": "w107 http writerless needle",
	})
	if code != http.StatusCreated {
		t.Fatalf("create without writer: %d %s", code, raw)
	}
	kind, details, _ = w107RowOf(t, raw)
	if kind != nil || details != nil {
		t.Fatalf("writerless create serves %v / %v, want null and null: %s", kind, details, raw)
	}

	// A kind outside the enum is MEM-400, never silently stored.
	code, raw = w107Write(t, e, writer, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content": "w107 http bogus kind", "writer_kind": "daemon",
	})
	if code != http.StatusBadRequest || !w107IsMEM400(raw) {
		t.Fatalf("writer_kind=daemon = %d %s, want MEM-400", code, raw)
	}
}

// w107IsMEM400 answers whether the error body carries MEM-400.
func w107IsMEM400(raw []byte) bool {
	return strings.Contains(string(raw), "MEM-400")
}

// TestW107Compat — AC5: the old request shape (no writer fields at all)
// succeeds unchanged on the new server, and its memory reads back.
func TestW107Compat(t *testing.T) {
	e := newSrvEnv(t)
	_, writer := memberUser(t, e, "w107-compat-writer", "writer")
	code, raw := w107Write(t, e, writer, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content": "w107 compat old shape body",
	})
	if code != http.StatusCreated {
		t.Fatalf("old-shape create: %d %s", code, raw)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("old-shape create answer: %s", raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/"+created.ID, writer, nil, nil); code != http.StatusOK {
		t.Fatalf("old-shape memory read: %d %s", code, raw)
	}
	if code, raw := e.do(t, "GET", "/api/v1/memories/search?q=compat&limit=5", writer, nil, nil); code != http.StatusOK {
		t.Fatalf("old-shape memory search: %d %s", code, raw)
	}
}

// TestW107Filter — AC3 at the HTTP face: the writer_kind URL parameter
// narrows both modes, the browse total counts only the matching
// entries, and a value outside the enum is MEM-400 on both.
func TestW107Filter(t *testing.T) {
	e := newSrvEnv(t)
	_, writer := memberUser(t, e, "w107-filter-writer", "writer")
	for _, w := range []struct {
		kind, content string
	}{
		{"step", "w107 http filter needle step row"},
		{"session", "w107 http filter needle session row"},
		{"", "w107 http filter needle writerless row"},
	} {
		body := map[string]any{
			"project": "alpha", "type": "fact.note", "visibility": "project",
			"content": w.content,
		}
		if w.kind != "" {
			body["writer_kind"] = w.kind
		}
		if code, raw := w107Write(t, e, writer, body); code != http.StatusCreated {
			t.Fatalf("seed %q: %d %s", w.kind, code, raw)
		}
	}

	// Keyword mode.
	for kind, want := range map[string]int{"step": 1, "session": 1} {
		code, raw := e.do(t, "GET", "/api/v1/memories/search?q=needle&writer_kind="+kind, e.key, nil, nil)
		if code != http.StatusOK {
			t.Fatalf("keyword writer_kind=%s: %d %s", kind, code, raw)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != want {
			t.Fatalf("keyword writer_kind=%s = %d items, want %d: %s", kind, len(page.Items), want, raw)
		}
		for _, it := range page.Items {
			if it["writer_kind"] != kind {
				t.Fatalf("keyword writer_kind=%s answered a %v row: %s", kind, it["writer_kind"], raw)
			}
		}
	}

	// Browse mode: the page and the total share the narrowing.
	code, raw := e.do(t, "GET", "/api/v1/memories/search?writer_kind=step&limit=20", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("browse writer_kind=step: %d %s", code, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Total != 1 {
		t.Fatalf("browse writer_kind=step = %d items / total %d, want 1 / 1 (only the matching entries): %s", len(page.Items), page.Total, raw)
	}
	if page.Items[0]["writer_kind"] != "step" {
		t.Fatalf("browse writer_kind=step answered a %v row: %s", page.Items[0]["writer_kind"], raw)
	}

	// A value outside the enum is MEM-400 on both modes.
	for _, q := range []string{"q=needle&writer_kind=daemon", "writer_kind=daemon"} {
		code, raw := e.do(t, "GET", "/api/v1/memories/search?"+q, e.key, nil, nil)
		if code != http.StatusBadRequest || !w107IsMEM400(raw) {
			t.Fatalf("search %q = %d %s, want MEM-400", q, code, raw)
		}
	}
}
