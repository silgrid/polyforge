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
	if err := env.Admin.Acknowledge(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.Acknowledge(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	var cursor, delivered int64
	_ = env.DB.QueryRow(`SELECT read_seq, delivered_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, first.ConversationID, u2.ID).Scan(&cursor, &delivered)
	if cursor != second.Seq {
		t.Fatalf("read cursor moved backwards: %d", cursor)
	}
	if delivered != 0 {
		t.Fatalf("acknowledgment moved the delivery cursor: %d", delivered)
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
// an admin reads, and the delivery cursor advances per message (so a killed
// daemon replays at most the one message it had not delivered).
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
	pending, err := env.Admin.PendingDelivery(u2.ID, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending before delivery: %d %v", len(pending), err)
	}
	if err := env.Admin.MarkDelivered(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	pending, err = env.Admin.PendingDelivery(u2.ID, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != second.ID {
		t.Fatalf("per-message delivery must leave exactly the undelivered one: %+v %v", pending, err)
	}
	// Delivery is idempotent and never moves backwards.
	if err := env.Admin.MarkDelivered(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.MarkDelivered(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: second.Seq}); err != nil {
		t.Fatal(err)
	}
	if err := env.Admin.MarkDelivered(u2.ID, api.AckMessage{ConversationID: first.ConversationID, Seq: first.Seq}); err != nil {
		t.Fatal(err)
	}
	if pending, _ := env.Admin.PendingDelivery(u2.ID, 10); len(pending) != 0 {
		t.Fatalf("delivery cursor moved backwards: %+v", pending)
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

// TestMessagingSplitCursorsAndUnread: delivery and read advance independently,
// own messages are never unread, delivered-but-unacknowledged stays unread,
// and the feed replays nothing at or below the delivery cursor.
func TestMessagingSplitCursorsAndUnread(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("split-u2", "split2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	own, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "own"})
	if err != nil {
		t.Fatal(err)
	}
	peerA, err := env.Admin.Send(u2.ID, api.SendMessage{To: env.UserID, Text: "peer-a"})
	if err != nil {
		t.Fatal(err)
	}
	peerB, err := env.Admin.Send(u2.ID, api.SendMessage{To: env.UserID, Text: "peer-b"})
	if err != nil {
		t.Fatal(err)
	}
	conv := own.ConversationID
	unreadOf := func(uid string) int {
		t.Helper()
		list, err := env.Admin.ListConversations(uid, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range list {
			if c.ID == conv {
				return c.Unread
			}
		}
		t.Fatalf("conversation %s is not in %s's member-only list", conv, uid)
		return -1
	}
	if got := unreadOf(env.UserID); got != 2 {
		t.Fatalf("unread = %d, want 2 (own message excluded)", got)
	}
	if got := unreadOf(u2.ID); got != 1 {
		t.Fatalf("peer unread = %d, want 1 (only the caller's own message)", got)
	}
	// Machine delivery advances only the delivery cursor.
	if err := env.Admin.MarkDelivered(env.UserID, api.AckMessage{ConversationID: conv, Seq: peerB.Seq}); err != nil {
		t.Fatal(err)
	}
	var delivered, read int64
	if err := env.DB.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, conv, env.UserID).Scan(&delivered, &read); err != nil {
		t.Fatal(err)
	}
	if delivered != peerB.Seq || read != 0 {
		t.Fatalf("cursors = delivered %d / read %d, want %d / 0", delivered, read, peerB.Seq)
	}
	if got := unreadOf(env.UserID); got != 2 {
		t.Fatalf("delivery cleared unread: %d", got)
	}
	if pending, err := env.Admin.PendingDelivery(env.UserID, 10); err != nil || len(pending) != 0 {
		t.Fatalf("delivery is not replay-safe: %+v %v", pending, err)
	}
	// Acknowledging an older sequence leaves the newer one unread; a
	// repeated/backward ack is a successful no-op.
	if err := env.Admin.Acknowledge(env.UserID, api.AckMessage{ConversationID: conv, Seq: peerA.Seq}); err != nil {
		t.Fatal(err)
	}
	if got := unreadOf(env.UserID); got != 1 {
		t.Fatalf("unread after acking peer-a = %d, want 1", got)
	}
	if err := env.Admin.Acknowledge(env.UserID, api.AckMessage{ConversationID: conv, Seq: peerA.Seq}); err != nil {
		t.Fatalf("repeated ack must succeed: %v", err)
	}
	if err := env.Admin.Acknowledge(env.UserID, api.AckMessage{ConversationID: conv, Seq: own.Seq}); err != nil {
		t.Fatalf("backward ack must succeed: %v", err)
	}
	if got := unreadOf(env.UserID); got != 1 {
		t.Fatalf("backward ack moved the read cursor: unread %d", got)
	}
	if pending, err := env.Admin.PendingDelivery(env.UserID, 10); err != nil || len(pending) != 0 {
		t.Fatalf("acknowledgment changed the delivery feed: %+v %v", pending, err)
	}
}

// TestMessagingAckBoundsAndAccess: a non-positive or past-the-newest sequence
// is CORE-400 with nothing written, an unknown conversation is CORE-404, and
// an admin non-member cannot acknowledge another member's row.
func TestMessagingAckBoundsAndAccess(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("bound-u2", "bound2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	u3, err := env.Admin.CreateUser("bound-u3", "bound3@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := env.Admin.Send(u2.ID, api.SendMessage{To: u3.ID, Text: "private"})
	if err != nil {
		t.Fatal(err)
	}
	// env.UserID is an admin but not a member: AUTH-403, no row written.
	if err := env.Admin.Acknowledge(env.UserID, api.AckMessage{ConversationID: msg.ConversationID, Seq: msg.Seq}); !IsCode(err, api.ErrAuthForbidden) {
		t.Fatalf("admin non-member ack = %v, want AUTH-403", err)
	}
	// Unknown conversation: CORE-404.
	if err := env.Admin.Acknowledge(u2.ID, api.AckMessage{ConversationID: "conv_nope", Seq: 1}); !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("unknown conversation ack = %v, want CORE-404", err)
	}
	for _, seq := range []int64{0, -3, msg.Seq + 1000} {
		if err := env.Admin.Acknowledge(u2.ID, api.AckMessage{ConversationID: msg.ConversationID, Seq: seq}); !IsCode(err, api.ErrCoreBadInput) {
			t.Fatalf("ack seq %d = %v, want CORE-400", seq, err)
		}
	}
	var read int64
	if err := env.DB.QueryRow(`SELECT read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, msg.ConversationID, u2.ID).Scan(&read); err != nil {
		t.Fatal(err)
	}
	if read != 0 {
		t.Fatalf("a refused ack wrote read_seq %d", read)
	}
	if err := env.Admin.Acknowledge(u2.ID, api.AckMessage{ConversationID: msg.ConversationID, Seq: msg.Seq}); err != nil {
		t.Fatalf("member ack: %v", err)
	}
}

// TestMessagingListKeepsTheAdminWideListing: an admin still sees every
// conversation (existing /api/v1/conversations behaviour), a member sees only
// its own, and the wire carries the member ids rather than caller annotations
// (pi-web derives peer/mine server-side from pf config).
func TestMessagingListKeepsTheAdminWideListing(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("member-u2", "member2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	u3, err := env.Admin.CreateUser("member-u3", "member3@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	// A conversation the admin is not a member of.
	if _, err := env.Admin.Send(u2.ID, api.SendMessage{To: u3.ID, Text: "not the admin's"}); err != nil {
		t.Fatal(err)
	}
	adminList, err := env.Admin.ListConversations(env.UserID, true)
	if err != nil || len(adminList) != 1 {
		t.Fatalf("admin-wide list: %+v %v", adminList, err)
	}
	if len(adminList[0].Members) != 2 {
		t.Fatalf("admin row lost its member ids: %+v", adminList[0])
	}
	if list, err := env.Admin.ListConversations(env.UserID, false); err != nil || len(list) != 0 {
		t.Fatalf("member list includes a conversation it is not in: %+v %v", list, err)
	}
	if list, err := env.Admin.ListConversations(u2.ID, false); err != nil || len(list) != 1 {
		t.Fatalf("member list: %+v %v", list, err)
	}
	own, err := env.Admin.Send(env.UserID, api.SendMessage{To: u2.ID, Text: "mine"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := env.Admin.ListConversations(env.UserID, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("admin member list: %+v %v", list, err)
	}
	if list[0].Kind != api.ConversationKindDirect || len(list[0].Members) != 2 {
		t.Fatalf("conversation wire = %+v", list[0])
	}
	page, err := env.Admin.Page(env.UserID, own.ConversationID, false, 0, 0, 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("page: %+v %v", page, err)
	}
}
