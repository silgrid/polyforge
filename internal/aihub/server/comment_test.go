package server

// comment_test.go — the append-only work-item comment
// door over real HTTP. Two different users comment on the same work item
// and both persist; the detail read (plain and `?include=events`) answers
// author, time, body and reply_to; a reply_to that names no comment on
// that work item is refused and nothing is written; a caller without
// write access (the same gate as `pf file edit`) cannot comment. The file
// is self-contained against the pre-change tree, so the external oracle
// can run it red on the base and green on the branch.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// w284CommentRow is one comment row as the wire carries it.
type w284CommentRow struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	ReplyTo   string `json:"reply_to"`
	CreatedAt string `json:"created_at"`
}

// w284Comment is the detail read's comment list shape.
type w284Comment struct {
	Comments []w284CommentRow `json:"comments"`
}

// w284GetComments reads one work item's comments over HTTP (optionally
// with ?include=events).
func w284GetComments(t *testing.T, e *srvEnv, key, id, query string) []w284CommentRow {
	t.Helper()
	var out w284Comment
	if code, raw := e.do(t, "GET", "/api/v1/work-items/"+id+query, key, nil, &out); code != 200 {
		t.Fatalf("get %s%s: %d %s", id, query, code, raw)
	}
	return out.Comments
}

// w284Send posts one comment from its own goroutine without touching
// testing.T (a Fatalf from a non-test goroutine is illegal).
func w284Send(e *srvEnv, key, id, body, replyTo string) (int, []byte, error) {
	payload := map[string]any{"body": body}
	if replyTo != "" {
		payload["reply_to"] = replyTo
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest("POST", e.ts.URL+"/api/v1/work-items/"+id+"/comments", bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, buf.Bytes(), nil
}

// TestWorkItemCommentsTwoUsersAppend: two different users both persist,
// in time (insertion) order, and the read faces answer their fields.
func TestWorkItemCommentsTwoUsersAppend(t *testing.T) {
	e := newSrvEnv(t)
	id := createExecWIOverHTTP(t, e, "comment target")
	_, memberKey := memberUser(t, e, "w284-commenter", "writer")

	var first w284CommentRow
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/comments", e.key,
		map[string]any{"body": "first note"}, &first); code != 201 {
		t.Fatalf("admin comment: %d %s", code, raw)
	}
	var second w284CommentRow
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+id+"/comments", memberKey,
		map[string]any{"body": "second note", "reply_to": first.ID}, &second); code != 201 {
		t.Fatalf("member comment: %d %s", code, raw)
	}
	if second.ReplyTo != first.ID {
		t.Fatalf("stored reply_to = %q, want %q", second.ReplyTo, first.ID)
	}

	// Both rows persist, in order, with author/time/body/reply_to.
	for _, query := range []string{"", "?include=events"} {
		list := w284GetComments(t, e, e.key, id, query)
		if len(list) != 2 {
			t.Fatalf("%q comments = %+v, want both rows", query, list)
		}
		if list[0].ID != first.ID || list[1].ID != second.ID {
			t.Fatalf("%q comments are not in insertion order: %+v", query, list)
		}
		if list[0].Author != e.adminID || list[1].Author == "" || list[0].CreatedAt == "" {
			t.Fatalf("%q comments lost author/time: %+v", query, list)
		}
		if list[1].ReplyTo != first.ID {
			t.Fatalf("%q reply_to = %q, want %q", query, list[1].ReplyTo, first.ID)
		}
	}

	// A concurrent pair (one comment per user) appends too: neither
	// overwrites the other.
	_, memberKey2 := memberUser(t, e, "w284-commenter2", "writer")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		code, raw, err := w284Send(e, e.key, id, "concurrent admin", "")
		if err == nil && code != 201 {
			err = fmt.Errorf("comment refused %d: %s", code, strings.TrimSpace(string(raw)))
		}
		errs <- err
	}()
	go func() {
		defer wg.Done()
		code, raw, err := w284Send(e, memberKey2, id, "concurrent member", "")
		if err == nil && code != 201 {
			err = fmt.Errorf("comment refused %d: %s", code, strings.TrimSpace(string(raw)))
		}
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent comment: %v", err)
		}
	}
	if list := w284GetComments(t, e, e.key, id, ""); len(list) != 4 {
		t.Fatalf("after concurrent comments = %+v, want 4 rows", list)
	}
}

// TestWorkItemCommentsRefusals: a bad reply_to writes nothing, a caller
// without write access is refused, and a blank body is refused.
func TestWorkItemCommentsRefusals(t *testing.T) {
	e := newSrvEnv(t)
	a := createExecWIOverHTTP(t, e, "comment target a")
	b := createExecWIOverHTTP(t, e, "comment target b")
	var foreign w284CommentRow
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+b+"/comments", e.key,
		map[string]any{"body": "on b"}, &foreign); code != 201 {
		t.Fatalf("comment on b: %d %s", code, raw)
	}

	for _, replyTo := range []string{"wic_missing", foreign.ID} {
		code, raw := e.do(t, "POST", "/api/v1/work-items/"+a+"/comments", e.key,
			map[string]any{"body": "x", "reply_to": replyTo}, nil)
		if code != 404 || !strings.Contains(string(raw), api.ErrCoreNotFound) {
			t.Fatalf("reply_to %q: %d %s, want 404 %s", replyTo, code, raw, api.ErrCoreNotFound)
		}
	}
	if list := w284GetComments(t, e, e.key, a, ""); len(list) != 0 {
		t.Fatalf("a refused reply_to wrote a comment: %+v", list)
	}

	// A caller with no relationship to the project gets the missing-work-
	// item answer (the same gate as PATCH); the writer role is the gate.
	_, outKey := memberUser(t, e, "w284-nonmember", "")
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+a+"/comments", outKey,
		map[string]any{"body": "from outside"}, nil); code != 404 {
		t.Fatalf("non-member comment: %d %s, want the concealed 404", code, raw)
	}
	viewerID, viewerKey := memberUser(t, e, "w284-viewer", "viewer")
	_ = viewerID
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+a+"/comments", viewerKey,
		map[string]any{"body": "from a viewer"}, nil); code != 403 || !strings.Contains(string(raw), api.ErrAuthForbidden) {
		t.Fatalf("viewer comment: %d %s, want 403 %s", code, raw, api.ErrAuthForbidden)
	}

	// Blank body: refused before any row.
	if code, raw := e.do(t, "POST", "/api/v1/work-items/"+a+"/comments", e.key,
		map[string]any{"body": "   "}, nil); code != 400 {
		t.Fatalf("blank body: %d %s, want 400", code, raw)
	}
	if list := w284GetComments(t, e, e.key, a, ""); len(list) != 0 {
		t.Fatalf("refused writes left comments: %+v", list)
	}
}
