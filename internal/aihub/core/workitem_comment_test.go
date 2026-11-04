package core

// workitem_comment_test.go — work-item comments are
// append-only (two different authors both persist, in insertion order)
// and reply_to may only name a comment on the SAME work item. The file is
// self-contained against the pre-change tree, so the external oracle can
// run it red on the base and green on the branch.

import (
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// mustComment appends one comment and fails the test on a refusal.
func mustComment(t *testing.T, env *testEnv, wiID, author, body, replyTo string) *api.Comment {
	t.Helper()
	c, err := env.WIs.AddComment(wiID, author, api.AddCommentRequest{Body: body, ReplyTo: replyTo})
	if err != nil {
		t.Fatalf("add comment on %s: %v", wiID, err)
	}
	return c
}

// TestCommentAppendOnlyTwoAuthors: two different users comment on the
// same work item; both rows are stored (neither overwrites the other) and
// the read answers them in insertion order.
func TestCommentAppendOnlyTwoAuthors(t *testing.T) {
	env := newEnv(t)
	wi, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "comment target", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	first := mustComment(t, env, wi.ID, env.UserID, "first note", "")
	second := mustComment(t, env, wi.ID, env.UserIDB, "second note", first.ID)

	list, err := env.WIs.Comments(wi.ID)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("comments = %+v, want both rows", list)
	}
	if list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("comments are not in insertion order: %+v", list)
	}
	if list[0].Author != env.UserID || list[1].Author != env.UserIDB {
		t.Fatalf("comments lost their authors: %+v", list)
	}
	if list[0].Body != "first note" || list[1].Body != "second note" {
		t.Fatalf("comments lost their bodies: %+v", list)
	}
	if list[1].ReplyTo != first.ID {
		t.Fatalf("reply_to = %q, want %q", list[1].ReplyTo, first.ID)
	}
	if list[0].CreatedAt == "" || list[1].CreatedAt == "" {
		t.Fatalf("comments lost their server time: %+v", list)
	}
	// The detail read carries the same thread.
	got, err := env.WIs.Get(wi.ID, GetOptions{})
	if err != nil {
		t.Fatalf("get work item: %v", err)
	}
	if len(got.Comments) != 2 || got.Comments[1].Body != "second note" {
		t.Fatalf("detail comments = %+v, want the same two rows", got.Comments)
	}
}

// TestCommentReplyToRefused: a missing reply_to id and an id from another
// work item are both refused, and nothing is written.
func TestCommentReplyToRefused(t *testing.T) {
	env := newEnv(t)
	a, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "comment target a", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "comment target b", Reporter: env.UserID})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	foreign := mustComment(t, env, b.ID, env.UserID, "on b", "")

	for _, replyTo := range []string{"wic_missing", foreign.ID} {
		_, err := env.WIs.AddComment(a.ID, env.UserID, api.AddCommentRequest{Body: "x", ReplyTo: replyTo})
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrCoreNotFound {
			t.Fatalf("reply_to %q: err = %v, want CORE-404", replyTo, err)
		}
	}
	list, err := env.WIs.Comments(a.ID)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("a refused reply_to wrote a comment: %+v", list)
	}
	// A blank body is refused before any row too.
	if _, err := env.WIs.AddComment(a.ID, env.UserID, api.AddCommentRequest{Body: "  \n\t"}); err == nil {
		t.Fatalf("blank body: err = nil, want a refusal")
	}
}
