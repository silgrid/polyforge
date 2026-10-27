package core

import (
	"fmt"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestMessagingDirectHistorySecretAndFlood(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("u2", "u2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	first, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "first"})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "second"})
	if err != nil || second.ConversationID != first.ConversationID {
		t.Fatalf("second send reused conversation? %+v %v", second, err)
	}
	before := w11Rows(t, env, "conversations")
	if _, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "key pf_k1_ABCDEFGHIJKLMNOP"}); !IsCode(err, api.ErrCoreSecret) {
		t.Fatalf("secret not refused: %v", err)
	}
	if got := w11Rows(t, env, "conversations"); got != before {
		t.Fatalf("secret created a conversation: %d -> %d", before, got)
	}
	page, err := env.Admin.Page(u2.ID, first.ConversationID, false, 0, 0, 10)
	if err != nil || len(page.Messages) != 2 {
		t.Fatalf("member page: %+v %v", page, err)
	}
	after, _ := env.Admin.Page(u2.ID, first.ConversationID, false, first.Seq, 0, 10)
	if len(after.Messages) != 1 || after.Messages[0].Text != "second" {
		t.Fatalf("after cursor: %+v", after.Messages)
	}
	var ref int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&ref); err != nil || ref != 2 {
		t.Fatalf("stored messages = %d err=%v", ref, err)
	}
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	_ = env.DB.QueryRow(`SELECT last_read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, first.ConversationID, u2.ID).Scan(&cursor)
	if cursor != second.Seq {
		t.Fatalf("cursor moved backwards: %d", cursor)
	}
}

func w11Rows(t *testing.T, env *testEnv, table string) int {
	t.Helper()
	var n int
	if err := env.DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMessagingFloodIsARollingWindow: exactly 60 sends per sender per
// rolling 60 s pass, the excess is CORE-429 with no row, and a refusal
// does NOT reset the bucket (the next request is still refused).
func TestMessagingFloodIsARollingWindow(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("flood-u2", "flood2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < sendCap; i++ {
		if _, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: fmt.Sprintf("m-%d", i)}); err != nil {
			t.Fatalf("send %d within the cap: %v", i, err)
		}
	}
	before := w11Rows(t, env, "messages")
	for i := 0; i < 2; i++ {
		if _, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "excess"}); !IsCode(err, api.ErrCoreTooMany) {
			t.Fatalf("excess send %d must be CORE-429, got %v", i, err)
		}
		if got := w11Rows(t, env, "messages"); got != before {
			t.Fatalf("a refused excess stored a row: %d -> %d", before, got)
		}
	}
}

// TestMessagingVisibilityAndPerMessageCursor: a non-member is refused,
// an admin reads, and the read cursor advances per message (so a killed
// daemon replays at most the one message it had not acked).
func TestMessagingVisibilityAndPerMessageCursor(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("vis-u2", "vis2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := env.Admin.CreateUser("vis-u3", "vis3@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	first, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Admin.Page(outsider.ID, first.ConversationID, false, 0, 0, 10); !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("non-member read must be AUTH-403, got %v", err)
	}
	if page, err := env.Admin.Page(env.UserID, first.ConversationID, true, 0, 0, 10); err != nil || len(page.Messages) != 2 {
		t.Fatalf("admin read: %+v %v", page, err)
	}
	unread, err := env.Admin.UnreadFor(u2.ID, 10)
	if err != nil || len(unread) != 2 {
		t.Fatalf("unread before ack: %d %v", len(unread), err)
	}
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	unread, err = env.Admin.UnreadFor(u2.ID, 10)
	if err != nil || len(unread) != 1 || unread[0].ID != second.ID {
		t.Fatalf("per-message ack must leave exactly the unacked one: %+v %v", unread, err)
	}
	// Ack is idempotent and never moves backwards.
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.AdvanceCursor(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	if unread, _ := env.Admin.UnreadFor(u2.ID, 10); len(unread) != 0 {
		t.Fatalf("cursor moved backwards: %+v", unread)
	}
}

// TestMessagingBackwardPagingReturnsThePageJustBelowTheCursor: with more
// than 2x the limit stored, before=<seq> must answer the `limit` messages
// immediately below the cursor (ascending), not the conversation's oldest
// page.
func TestMessagingBackwardPagingReturnsThePageJustBelowTheCursor(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("back-u2", "back2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	seqs := make([]int64, 0, 25)
	var conv string
	for i := 0; i < 25; i++ {
		msg, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: fmt.Sprintf("back-%02d", i)})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		seqs = append(seqs, msg.Seq)
		conv = msg.ConversationID
	}
	// Cursor = the 21st message: the page must be the five just below it.
	cursor := seqs[20]
	page, err := env.Admin.Page(env.UserID, conv, false, 0, cursor, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 5 {
		t.Fatalf("before page = %d messages, want 5: %+v", len(page.Messages), page.Messages)
	}
	for i, msg := range page.Messages {
		if want := seqs[15+i]; msg.Seq != want {
			t.Fatalf("before page[%d].seq = %d, want %d (page %v, all %v)", i, msg.Seq, want, page.Messages, seqs)
		}
	}
	if page.LastSeq != seqs[19] {
		t.Fatalf("before page last_seq = %d, want %d", page.LastSeq, seqs[19])
	}
}
