package server

// carries: internal/aihub/memory/store.go, internal/aihub/memory/browse.go, internal/aihub/server/w107_ownername_test.go, docs/design/polyforge-vnext/06-memory-search.md

// w107_ownername_test.go — polyforge#107 AC1: every read face (by id,
// keyword with an excerpt, browse) serves the owner's display name, read
// live from the users table — an admin rename shows on the next read,
// because no snapshot is stored. The assertions work on the served JSON,
// so the file compiles on the pre-change tree and the red is the missing
// field, never a missing symbol.

import (
	"encoding/json"
	"net/http"
	"testing"
)

// w107OwnerNameOf answers the owner_display_name of one served row.
func w107OwnerNameOf(t *testing.T, raw []byte, item map[string]any) string {
	t.Helper()
	v, ok := item["owner_display_name"]
	if !ok {
		t.Fatalf("the served row carries no owner_display_name: %s", raw)
	}
	name, ok := v.(string)
	if !ok {
		t.Fatalf("owner_display_name = %v, want a string: %s", v, raw)
	}
	return name
}

// w107FirstItem answers the first item of a list-shaped answer.
func w107FirstItem(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("not a list answer: %v (%s)", err, raw)
	}
	if len(page.Items) == 0 {
		t.Fatalf("the list answer carries no item: %s", raw)
	}
	return page.Items[0]
}

func TestW107OwnerName(t *testing.T) {
	e := newSrvEnv(t)
	// A second project member writes the memory, so the name checked is
	// not the browsing admin's own.
	writer, writerKey := memberUser(t, e, "w107-owner-writer", "writer")
	_ = writer
	var created struct {
		ID string `json:"id"`
	}
	if code, raw := e.do(t, "POST", "/api/v1/memories", writerKey, map[string]any{
		"project": "alpha", "type": "fact.note", "visibility": "project",
		"content": "w107 owner name needle body",
	}, &created); code != http.StatusCreated || created.ID == "" {
		t.Fatalf("create: %d %s", code, raw)
	}

	// The three read faces, each answering the writer's display name.
	faces := map[string]func() map[string]any{
		"by id": func() map[string]any {
			code, raw := e.do(t, "GET", "/api/v1/memories/"+created.ID, e.key, nil, nil)
			if code != http.StatusOK {
				t.Fatalf("by id: %d %s", code, raw)
			}
			var item map[string]any
			if err := json.Unmarshal(raw, &item); err != nil {
				t.Fatalf("by id: %v (%s)", err, raw)
			}
			return item
		},
		"keyword with excerpt": func() map[string]any {
			code, raw := e.do(t, "GET", "/api/v1/memories/search?q=needle&excerpt=64", e.key, nil, nil)
			if code != http.StatusOK {
				t.Fatalf("keyword: %d %s", code, raw)
			}
			item := w107FirstItem(t, raw)
			// r1#4: the excerpt flag must be seen working — the answer
			// carries the bounded content excerpt, so this face really is
			// the excerpt path, not the plain one.
			ce, ok := item["content_excerpt"].(string)
			if !ok || ce == "" {
				t.Fatalf("the excerpt answer carries no content_excerpt: %s", raw)
			}
			return item
		},
		"browse": func() map[string]any {
			code, raw := e.do(t, "GET", "/api/v1/memories/search?limit=20", e.key, nil, nil)
			if code != http.StatusOK {
				t.Fatalf("browse: %d %s", code, raw)
			}
			return w107FirstItem(t, raw)
		},
	}
	for face, read := range faces {
		item := read()
		raw, _ := json.Marshal(item)
		if name := w107OwnerNameOf(t, raw, item); name != "w107-owner-writer" {
			t.Fatalf("%s serves owner_display_name %q, want the writer's display name", face, name)
		}
	}

	// The admin renames the writer: the very next read answers the new
	// name on every face — the display name is joined at read time, no
	// snapshot.
	if code, raw := e.do(t, "PATCH", "/api/v1/users/"+writer, e.key,
		map[string]any{"display_name": "Renamed Owner"}, nil); code != http.StatusOK {
		t.Fatalf("rename: %d %s", code, raw)
	}
	for face, read := range faces {
		item := read()
		raw, _ := json.Marshal(item)
		if name := w107OwnerNameOf(t, raw, item); name != "Renamed Owner" {
			t.Fatalf("%s serves owner_display_name %q after the rename, want the new name", face, name)
		}
	}

	// The browse answer is still exactly the one memory — the page and
	// the total both; the rename rewrote no row (r1#4: exact, not
	// "at least").
	code, raw := e.do(t, "GET", "/api/v1/memories/search?limit=20", e.key, nil, nil)
	if code != http.StatusOK {
		t.Fatalf("browse after rename: %d %s", code, raw)
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("browse after the rename: %v (%s)", err, raw)
	}
	if len(page.Items) != 1 || page.Total != 1 || page.Items[0]["id"] != created.ID {
		t.Fatalf("browse after the rename = %d item(s) / total %d, want exactly the one memory: %s",
			len(page.Items), page.Total, raw)
	}
}
