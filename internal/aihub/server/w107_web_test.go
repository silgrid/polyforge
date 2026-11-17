package server

// carries: internal/aihub/server/web_api.go, internal/aihub/server/web_read.go, internal/aihub/server/w107_web_test.go, docs/design/polyforge-vnext/reference.md

// w107_web_test.go — polyforge#107 AC7: the console's read projection
// (/web/api/memories and /web/api/memories/{id}) returns the author's
// display name and the writer. The assertions work on the served JSON,
// so the file compiles on the pre-change tree and the red is the
// missing fields, never a missing symbol. The page's own rendering is
// ③ (polyforge#120); this pins the server's projection alone.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestW107Web(t *testing.T) {
	e := newSrvEnv(t)
	_, writer := memberUser(t, e, "w107-web-writer", "writer")

	// One attributed row and one writerless row.
	makeRow := func(kind string) string {
		t.Helper()
		body := map[string]any{
			"project": "alpha", "type": "fact.note", "visibility": "project",
			"content": "w107 web console needle " + kind,
		}
		if kind != "" {
			body["writer_kind"] = kind
			body["writer_details"] = map[string]any{"work_item": "wi_107", "step": "build", "attempt": 2}
		}
		code, raw := e.do(t, "POST", "/api/v1/memories", writer, body, nil)
		if code != http.StatusCreated {
			t.Fatalf("seed %q: %d %s", kind, code, raw)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
			t.Fatalf("seed %q answer: %s", kind, raw)
		}
		return created.ID
	}
	stepID := makeRow("step")
	noneID := makeRow("")

	// The console's browse list and keyword page both project the author
	// display name and the writer; the writerless row answers null.
	for _, q := range []string{"", "q=needle"} {
		code, body := webJSONReq(t, e, "/web/api/memories?project=alpha"+extraAmp(q), e.key, nil)
		if code != http.StatusOK {
			t.Fatalf("console list %q: %d %s", q, code, body)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("console list %q is not JSON: %v (%s)", q, err, body)
		}
		rows := map[string]map[string]any{}
		for _, it := range page.Items {
			if id, _ := it["id"].(string); id != "" {
				rows[id] = it
			}
		}
		row, ok := rows[stepID]
		if !ok {
			t.Fatalf("console list %q misses the attributed row: %s", q, body)
		}
		if row["owner_display_name"] != "w107-web-writer" || row["writer_kind"] != "step" {
			t.Fatalf("console list %q row = %v, want the author display name and the step writer: %s", q, row, body)
		}
		if d, ok := row["writer_details"].(map[string]any); !ok || d["work_item"] != "wi_107" {
			t.Fatalf("console list %q writer_details = %v, want the detail object: %s", q, row["writer_details"], body)
		}
		row, ok = rows[noneID]
		if !ok {
			t.Fatalf("console list %q misses the writerless row: %s", q, body)
		}
		if row["writer_kind"] != nil {
			t.Fatalf("console list %q writerless row = %v, want writer_kind null: %s", q, row, body)
		}
	}

	// The detail projection carries the same fields.
	code, body := webJSONReq(t, e, "/web/api/memories/"+stepID+"?project=alpha", e.key, nil)
	if code != http.StatusOK {
		t.Fatalf("console detail: %d %s", code, body)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatalf("console detail is not JSON: %v (%s)", err, body)
	}
	if detail["owner_display_name"] != "w107-web-writer" || detail["writer_kind"] != "step" {
		t.Fatalf("console detail = %v, want the author display name and the step writer: %s", detail, body)
	}
	code, body = webJSONReq(t, e, "/web/api/memories/"+noneID+"?project=alpha", e.key, nil)
	if code != http.StatusOK {
		t.Fatalf("console detail (writerless): %d %s", code, body)
	}
	detail = nil
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["writer_kind"] != nil || detail["writer_details"] != nil {
		t.Fatalf("console detail (writerless) = %v, want null and null: %s", detail, body)
	}
}

// extraAmp answers the query suffix for an optional parameter string
// ("" = no suffix).
func extraAmp(q string) string {
	if q == "" {
		return ""
	}
	return "&" + q
}
