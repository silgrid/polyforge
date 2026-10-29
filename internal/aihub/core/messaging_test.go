package core

import (
	"encoding/json"
	"fmt"
	"strings"
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
	// The admin's wide row carries names for exactly those members too,
	// with no admin-specific branch in the enrichment.
	if len(adminList[0].MemberNames) != 2 ||
		adminList[0].MemberNames[u2.ID] != "member-u2" ||
		adminList[0].MemberNames[u3.ID] != "member-u3" {
		t.Fatalf("admin row member_names = %+v", adminList[0].MemberNames)
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

// TestMessagingMemberNamesMatchMembersExactly: member_names' scope is
// membership and nothing else the key set equals members
// for an admin's non-member row and for a member's own, a non-member's
// display name never appears, the wire shape carries "member_names", and
// an empty display name keeps its key with an empty value.
func TestMessagingMemberNamesMatchMembersExactly(t *testing.T) {
	env := newEnv(t)
	u2, err := env.Admin.CreateUser("names-u2", "names2@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	u3, err := env.Admin.CreateUser("names-u3", "names3@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := env.Admin.CreateUser("names-outsider", "outsider@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	// u2 sets an empty display name straight in the store: display_name is
	// NOT NULL but an empty string is legitimate data.
	if _, err := env.DB.Exec(`UPDATE users SET display_name='' WHERE id=?1`, u2.ID); err != nil {
		t.Fatal(err)
	}
	conv, err := env.Admin.Send(u2.ID, api.SendMessage{To: u3.ID, Text: "names"})
	if err != nil {
		t.Fatal(err)
	}

	// The key sets equal members exactly, for the admin's non-member row
	// and for a member's own, with no admin branch anywhere.
	checkKeysEqualMembers := func(label string, row api.Conversation) {
		t.Helper()
		if row.ID != conv.ConversationID {
			t.Fatalf("%s: wrong conversation %+v", label, row)
		}
		if len(row.MemberNames) != len(row.Members) {
			t.Fatalf("%s: member_names %v vs members %v", label, row.MemberNames, row.Members)
		}
		for _, id := range row.Members {
			if _, ok := row.MemberNames[id]; !ok {
				t.Fatalf("%s: member %q missing from member_names %v", label, id, row.MemberNames)
			}
		}
		for id := range row.MemberNames {
			found := false
			for _, m := range row.Members {
				if m == id {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: member_names key %q is not a member (%v)", label, id, row.Members)
			}
		}
	}
	adminList, err := env.Admin.ListConversations(env.UserID, true)
	if err != nil || len(adminList) != 1 {
		t.Fatalf("admin list: %+v %v", adminList, err)
	}
	checkKeysEqualMembers("admin row", adminList[0])
	memberList, err := env.Admin.ListConversations(u2.ID, false)
	if err != nil || len(memberList) != 1 {
		t.Fatalf("member list: %+v %v", memberList, err)
	}
	checkKeysEqualMembers("member row", memberList[0])

	// The empty display name keeps its key with an empty value, and the
	// other member's name is joined verbatim.
	if got := memberList[0].MemberNames[u2.ID]; got != "" {
		t.Fatalf("empty display name reads as %q", got)
	}
	if got := memberList[0].MemberNames[u3.ID]; got != "names-u3" {
		t.Fatalf("u3 name = %q", got)
	}

	// The wire shape carries member_names (and never a wider scope).
	wire, err := json.Marshal(memberList)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"member_names"`) {
		t.Fatalf("wire carries no member_names: %s", wire)
	}
	if strings.Contains(string(wire), "names-outsider") || strings.Contains(string(wire), outsider.ID) {
		t.Fatalf("non-member leaks into the wire: %s", wire)
	}

	// A non-admin non-member gets neither the conversation nor any name
	// from it.
	other, err := env.Admin.ListConversations(outsider.ID, false)
	if err != nil || len(other) != 0 {
		t.Fatalf("outsider list: %+v %v", other, err)
	}
	otherWire, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(otherWire), "names-u3") || strings.Contains(string(otherWire), conv.ConversationID) {
		t.Fatalf("outsider wire leaks a name or conversation: %s", otherWire)
	}
}
