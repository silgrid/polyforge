package server

// messaging_test.go: the split cursor's two HTTP entry points. Both are
// Bearer routes: POST /api/v1/messages/ack moves the caller's read cursor,
// POST /api/v1/messages/delivered moves only the caller's delivery cursor.
// Neither is reachable without a key, and nothing but the caller's own member
// row moves.

import (
	"net/http"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestMessagingSplitCursorRoutes(t *testing.T) {
	e := newSrvEnv(t)
	alice, err := e.admin.CreateUser("route-alice", "route-alice@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := e.admin.CreateUser("route-bob", "route-bob@example.com", "writer", "human")
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, err := e.admin.CreateAPIKey(alice.ID, "route-alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bobKey, err := e.admin.CreateAPIKey(bob.ID, "route-bob", "")
	if err != nil {
		t.Fatal(err)
	}

	var sent api.Message
	if code, raw := e.do(t, "POST", "/api/v1/messages", bobKey.Plain,
		api.SendMessage{To: alice.ID, Text: "hello"}, &sent); code != http.StatusOK {
		t.Fatalf("send: %d %s", code, raw)
	}

	// Delivery and acknowledgment are both refused without a key.
	if code, _ := e.do(t, "POST", "/api/v1/messages/delivered", "",
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq}, nil); code != http.StatusUnauthorized {
		t.Fatalf("delivered without a key: %d", code)
	}
	if code, _ := e.do(t, "POST", "/api/v1/messages/ack", "",
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq}, nil); code != http.StatusUnauthorized {
		t.Fatalf("ack without a key: %d", code)
	}

	// Alice's own credential advances her delivery cursor only.
	if code, raw := e.do(t, "POST", "/api/v1/messages/delivered", aliceKey.Plain,
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq}, nil); code != http.StatusOK {
		t.Fatalf("delivered: %d %s", code, raw)
	}
	var delivered, read int64
	if err := e.db.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, sent.ConversationID, alice.ID).Scan(&delivered, &read); err != nil {
		t.Fatal(err)
	}
	if delivered != sent.Seq || read != 0 {
		t.Fatalf("alice cursors = delivered %d / read %d, want %d / 0", delivered, read, sent.Seq)
	}
	if list, err := e.admin.ListConversations(alice.ID, false); err != nil || len(list) != 1 || list[0].Unread != 1 {
		t.Fatalf("delivery cleared unread: %+v %v", list, err)
	}
	// Bob's delivery moves only Bob's row.
	if code, raw := e.do(t, "POST", "/api/v1/messages/delivered", bobKey.Plain,
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq}, nil); code != http.StatusOK {
		t.Fatalf("bob delivered: %d %s", code, raw)
	}
	if err := e.db.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, sent.ConversationID, alice.ID).Scan(&delivered, &read); err != nil {
		t.Fatal(err)
	}
	if delivered != sent.Seq || read != 0 {
		t.Fatalf("bob's delivery moved alice's row: delivered %d / read %d", delivered, read)
	}

	// The ack route moves the read cursor, forward only, and refuses a
	// sequence past the newest message with CORE-400.
	if code, raw := e.do(t, "POST", "/api/v1/messages/ack", aliceKey.Plain,
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq + 1}, nil); code != http.StatusBadRequest {
		t.Fatalf("ack past newest: %d %s", code, raw)
	}
	if code, raw := e.do(t, "POST", "/api/v1/messages/ack", aliceKey.Plain,
		api.AckMessage{ConversationID: sent.ConversationID, Seq: sent.Seq}, nil); code != http.StatusOK {
		t.Fatalf("ack: %d %s", code, raw)
	}
	if err := e.db.QueryRow(`SELECT delivered_seq, read_seq FROM conversation_members
		WHERE conversation_id=?1 AND user_id=?2`, sent.ConversationID, alice.ID).Scan(&delivered, &read); err != nil {
		t.Fatal(err)
	}
	if read != sent.Seq || delivered != sent.Seq {
		t.Fatalf("alice cursors after ack = delivered %d / read %d", delivered, read)
	}
	if list, err := e.admin.ListConversations(alice.ID, false); err != nil || len(list) != 1 || list[0].Unread != 0 {
		t.Fatalf("ack did not clear unread: %+v %v", list, err)
	}
}
