package memory

// carries: internal/aihub/memory/store.go, internal/aihub/memory/w107_writer_test.go, docs/design/polyforge-vnext/06-memory-search.md

// w107_writer_test.go — polyforge#107 AC2 (the store's half): creation
// accepts the writer (kind + details) and the read faces serve it back;
// a kind outside the enum is MEM-400, details without a kind are
// MEM-400, details that are not a JSON object are MEM-400, and a request
// without the writer stores NULL and reads back null. The assertions
// work on the served JSON (the wire contract ②/③ build on), not on the
// struct, so the file compiles on the pre-change tree and the red is the
// missing fields, never a missing symbol.

import (
	"encoding/json"
	"strings"
	"testing"
)

// w107Create runs one create from a JSON request body (the wire shape a
// caller sends) and answers the returned entry's served JSON.
func w107Create(t *testing.T, e *env, body string) (map[string]any, error) {
	t.Helper()
	var req CreateRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("bad request fixture: %v", err)
	}
	entry, err := e.store.Create(req, e.user)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	var served map[string]any
	if err := json.Unmarshal(raw, &served); err != nil {
		t.Fatalf("served entry is not JSON: %v (%s)", err, raw)
	}
	return served, nil
}

// w107ErrCode answers err's API error code.
func w107ErrCode(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected an APIError, got %T: %v", err, err)
	}
	return ae.Code
}

func TestW107Writer(t *testing.T) {
	e := newEnv(t)

	// A step write: the kind and the details come back on the returned
	// entry and on a fresh Get (the id, keyword and browse read faces all
	// serve the same Entry).
	served, err := w107Create(t, e, `{"project":"alpha","type":"experience.debug",
		"visibility":"project","content":"w107 step write body",
		"writer_kind":"step","writer_details":{"work_item":"wi_107","step":"build","attempt":1,"model":"glm-5.3"}}`)
	if err != nil {
		t.Fatalf("create with writer: %v", err)
	}
	if served["writer_kind"] != "step" {
		t.Fatalf("writer_kind = %v, want step", served["writer_kind"])
	}
	details, ok := served["writer_details"].(map[string]any)
	if !ok || details["work_item"] != "wi_107" || details["step"] != "build" || details["attempt"] != float64(1) {
		t.Fatalf("writer_details = %v, want the step detail object", served["writer_details"])
	}
	id := served["id"].(string)
	got, err := e.store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	raw, _ := json.Marshal(got)
	var reread map[string]any
	if err := json.Unmarshal(raw, &reread); err != nil {
		t.Fatal(err)
	}
	if reread["writer_kind"] != "step" || reread["writer_details"].(map[string]any)["step"] != "build" {
		t.Fatalf("Get serves writer %v / %v, want the stored one", reread["writer_kind"], reread["writer_details"])
	}

	// A session write carries the kind alone: the details answer null.
	served, err = w107Create(t, e, `{"project":"alpha","type":"fact.note",
		"visibility":"project","content":"w107 session write body","writer_kind":"session"}`)
	if err != nil {
		t.Fatalf("create with kind alone: %v", err)
	}
	if served["writer_kind"] != "session" {
		t.Fatalf("writer_kind = %v, want session", served["writer_kind"])
	}
	if v, has := served["writer_details"]; !has || v != nil {
		t.Fatalf("writer_details = %v, want null", v)
	}

	// No writer at all: both fields store NULL and read back null — the
	// old request shape keeps working unchanged (AC5's store half).
	served, err = w107Create(t, e, `{"project":"alpha","type":"fact.note",
		"visibility":"project","content":"w107 writerless body"}`)
	if err != nil {
		t.Fatalf("create without writer: %v", err)
	}
	if served["writer_kind"] != nil || served["writer_details"] != nil {
		t.Fatalf("writer fields = %v / %v, want null and null", served["writer_kind"], served["writer_details"])
	}

	// A kind outside the enum is refused.
	if _, err := w107Create(t, e, `{"project":"alpha","type":"fact.note",
		"visibility":"project","content":"w107 bogus kind","writer_kind":"daemon"}`); err == nil ||
		w107ErrCode(t, err) != "MEM-400" {
		t.Fatalf("writer_kind=daemon = %v, want MEM-400", err)
	}

	// Details without a kind are refused: the detail object belongs to a
	// writer class.
	if _, err := w107Create(t, e, `{"project":"alpha","type":"fact.note",
		"visibility":"project","content":"w107 orphan details","writer_details":{"model":"x"}}`); err == nil ||
		w107ErrCode(t, err) != "MEM-400" {
		t.Fatalf("writer_details without writer_kind = %v, want MEM-400", err)
	}

	// Details that are not a JSON object are refused.
	for _, bad := range []string{`"a string"`, `[1,2]`, `17`} {
		if _, err := w107Create(t, e, `{"project":"alpha","type":"fact.note","visibility":"project",
			"content":"w107 bad details","writer_kind":"step","writer_details":`+bad+`}`); err == nil ||
			w107ErrCode(t, err) != "MEM-400" {
			t.Fatalf("writer_details=%s = %v, want MEM-400", bad, err)
		}
	}

	// The detail object is bounded: a serialized body over the cap is
	// refused rather than stored.
	big := `{"project":"alpha","type":"fact.note","visibility":"project","content":"w107 huge details",
		"writer_kind":"step","writer_details":{"blob":"` + strings.Repeat("x", 4097) + `"}}`
	if _, err := w107Create(t, e, big); err == nil || w107ErrCode(t, err) != "MEM-400" {
		t.Fatalf("writer_details over the cap = %v, want MEM-400", err)
	}

	// The idempotent replay answers the original entry, writer included.
	key := "w107-idem-key"
	served, err = w107Create(t, e, `{"project":"alpha","type":"fact.note","visibility":"project",
		"content":"w107 idem body","idempotency_key":"`+key+`",
		"writer_kind":"step","writer_details":{"work_item":"wi_107","step":"review","attempt":2}}`)
	if err != nil {
		t.Fatalf("idem create: %v", err)
	}
	if served["writer_kind"] != "step" {
		t.Fatalf("idem writer_kind = %v, want step", served["writer_kind"])
	}
	req := CreateRequest{Project: "alpha", Type: "fact.note", Visibility: "project",
		Content: "w107 idem body replay", IdempotencyKey: &key}
	replay, err := e.store.Create(req, e.user)
	if err != nil {
		t.Fatalf("idem replay: %v", err)
	}
	raw, _ = json.Marshal(replay)
	var replayed map[string]any
	if err := json.Unmarshal(raw, &replayed); err != nil {
		t.Fatal(err)
	}
	if replayed["id"] != served["id"] {
		t.Fatalf("idem replay answered %v, want the original %v", replayed["id"], served["id"])
	}
	if replayed["writer_kind"] != "step" {
		t.Fatalf("idem replay writer = %v, want the stored step", replayed["writer_kind"])
	}

	// The stored document's frontmatter never carries the writer (Q8):
	// the writer is server-side metadata, like authority_path.
	content, err := e.store.Content(id)
	if err != nil {
		t.Fatalf("content: %v", err)
	}
	if strings.Contains(string(content), "writer") {
		t.Fatalf("the stored document carries the writer:\n%s", content)
	}
}
