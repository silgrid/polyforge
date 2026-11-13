package aihubclient

// w359_workitem_ref_test.go: pins the one encoding on the wire for a
// work-item reference. The CLI's client escapes a caller's
// reference into one path segment; with the data-plane proxy forwarding the
// escaped path byte for byte, a pre-encoded spelling ("workbench%231") must
// be decoded once here before that single escape, or it reaches the Hub as
// "workbench%25231" and stops resolving. These tests fail on the pre-fix
// client (the "workbench%231" rows reached the Hub double-escaped).

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestW359WorkItemRefSingleEncoding(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI())
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "k")

	if _, err := c.GetWorkItemRaw("alpha#1"); err != nil {
		t.Fatalf("GetWorkItemRaw(raw slug): %v", err)
	}
	if _, err := c.GetWorkItemRaw("alpha%231"); err != nil {
		t.Fatalf("GetWorkItemRaw(pre-encoded): %v", err)
	}
	if _, err := c.AddComment("alpha%231", api.AddCommentRequest{Body: "x"}); err != nil {
		t.Fatalf("AddComment(pre-encoded): %v", err)
	}
	if _, err := c.GetWorkItemRaw("a/b?c"); err != nil {
		t.Fatalf("GetWorkItemRaw(reserved chars): %v", err)
	}
	if _, err := c.GetWorkItemRaw("alpha%zz"); err != nil {
		t.Fatalf("GetWorkItemRaw(malformed escape): %v", err)
	}

	want := []string{
		"GET /api/v1/work-items/alpha%231",
		"GET /api/v1/work-items/alpha%231",
		"POST /api/v1/work-items/alpha%231/comments",
		"GET /api/v1/work-items/a%2Fb%3Fc",
		"GET /api/v1/work-items/alpha%25zz",
	}
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d = %q, want %q (one escape on the wire)", i, got[i], want[i])
		}
	}
}
