package server

// w72_patch_content_cas_test.go (polyforge#72, u2 — AC4): PATCH
// /work-items/{id} carries an optional `expected_updated_at` — the
// `updated_at` the writer read. A version that changed in between is
// refused CORE-409 CONTENT_STALE and the row is untouched; a fresh one
// writes; a PATCH without the key behaves exactly as before (the old
// callers never change). Expected values are hand-derived from AC4
// (「写入带上读到的版本，版本已变则拒绝」), never from the implementation.

import (
	"strings"
	"testing"
)

func w72Get(t *testing.T, e *srvEnv, id string) (updatedAt, content string) {
	t.Helper()
	var wi struct {
		UpdatedAt string  `json:"updated_at"`
		Content   *string `json:"content"`
	}
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id, e.key, nil, &wi); code != 200 {
		t.Fatalf("get: %d %s", code, raw)
	}
	if wi.Content != nil {
		content = *wi.Content
	}
	return wi.UpdatedAt, content
}

func TestW72PatchContentExpectedUpdatedAt(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "w72 content cas")

	// The version this writer read.
	read1, content1 := w72Get(t, e, id)

	// Someone else writes first (no CAS key: old callers unchanged).
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key,
		map[string]any{"goal": "w72 content cas — bumped"}, nil); code != 200 {
		t.Fatalf("the bumping patch: %d %s", code, raw)
	}
	read2, _ := w72Get(t, e, id)
	if read2 == read1 {
		t.Fatalf("the bump must move updated_at (read1=%s read2=%s)", read1, read2)
	}

	// A write carrying the STALE version: refused, content untouched.
	code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key,
		map[string]any{"content": "the stale write", "expected_updated_at": read1}, nil)
	if code != 409 || !strings.Contains(string(raw), "CONTENT_STALE") {
		t.Fatalf("a stale expected_updated_at must be refused 409 CONTENT_STALE; got %d %s", code, raw)
	}
	if _, content := w72Get(t, e, id); content != content1 {
		t.Fatalf("the refused write must change nothing: content = %q", content)
	}

	// A write carrying the FRESH version: accepted.
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key,
		map[string]any{"content": "the fresh write", "expected_updated_at": read2}, nil); code != 200 {
		t.Fatalf("a fresh expected_updated_at must write: %d %s", code, raw)
	}
	if _, content := w72Get(t, e, id); content != "the fresh write" {
		t.Fatalf("the fresh write must land: content = %q", content)
	}

	// No CAS key at all: the old shape, unchanged behaviour.
	if code, raw := e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key,
		map[string]any{"goal": "w72 content cas — again"}, nil); code != 200 {
		t.Fatalf("a patch without the key must keep the old behaviour: %d %s", code, raw)
	}

	// A malformed version value is a bad input, never a write.
	if code, _ := e.do(t, "PATCH", "/api/v1/work-items/"+id, e.key,
		map[string]any{"content": "x", "expected_updated_at": "not-a-time"}, nil); code != 400 {
		t.Fatalf("a malformed expected_updated_at must be refused 400, got %d", code)
	}
}
