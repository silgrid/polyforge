package core

// messaging.go: user-to-user conversations with permanent append-only
// history and per-user read cursors (workbench#11). One direct 1:1
// conversation per sorted user pair; no group is ever created. Text is
// bounded, secret-shaped content is refused BEFORE any conversation or
// message write, and the anti-flood cap is per sender per process.

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/secretpat"
)

const (
	sendWindow      = 60 * time.Second
	sendCap         = 60
	defaultPageSize = 50
	maxPageSize     = 200
	aboutWIMaxBytes = 200
)

// Send stores one message after the secret scan, address/bound checks and
// the flood cap; it answers the stored row.
func (m *AdminManager) Send(senderID string, req api.SendMessage) (*api.Message, error) {
	if strings.TrimSpace(senderID) == "" {
		return nil, Fail(api.ErrAuthUnauthorized, "", "sender required")
	}
	if strings.TrimSpace(req.To) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "to (a human user id) is required")
	}
	if len([]byte(req.Text)) > api.MessageTextMaxBytes {
		return nil, Fail(api.ErrCoreTooLarge, "", "message text is %d bytes; the bound is %d", len([]byte(req.Text)), api.MessageTextMaxBytes)
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "text is required")
	}
	if len([]byte(req.AboutWI)) > aboutWIMaxBytes {
		return nil, Fail(api.ErrCoreTooLarge, "", "about_wi is %d bytes; the anchor bound is %d", len([]byte(req.AboutWI)), aboutWIMaxBytes)
	}
	// Secret scan FIRST: a refused secret leaves no conversation and no row.
	if name, hit := secretpat.Scan(req.Text); hit {
		return nil, Fail(api.ErrCoreSecret, "", "message refused: secret-shaped content (%s) is never stored or sent", name)
	}
	if name, hit := secretpat.Scan(req.AboutWI); hit {
		return nil, Fail(api.ErrCoreSecret, "", "message refused: secret-shaped content (%s) is never stored or sent", name)
	}
	// The recipient must be a human user (machines are not addresses).
	var kind string
	err := m.db.QueryRow(`SELECT kind FROM users WHERE id=?1`, req.To).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "human") {
		return nil, Fail(api.ErrCoreNotFound, "", "to must name an existing human user (got %q)", req.To)
	}
	if err != nil {
		return nil, err
	}
	if req.To == senderID {
		return nil, Fail(api.ErrCoreBadInput, "", "a direct message needs a different recipient")
	}
	if !m.allowSend(senderID, time.Now()) {
		return nil, Fail(api.ErrCoreTooMany, "", "message refused: %d messages per sender per %s", sendCap, sendWindow)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	convID, err := findOrCreateDirectTx(tx, senderID, req.To)
	if err != nil {
		return nil, err
	}
	if req.ReplyTo != "" {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE id=?1 AND conversation_id=?2`,
			req.ReplyTo, convID).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, Fail(api.ErrCoreNotFound, "", "reply_to message %s is not in this conversation", req.ReplyTo)
		}
	}
	id, err := NewID("msg_")
	if err != nil {
		return nil, err
	}
	now := Now()
	var about, reply any
	if req.AboutWI != "" {
		about = req.AboutWI
	}
	if req.ReplyTo != "" {
		reply = req.ReplyTo
	}
	res, err := tx.Exec(`INSERT INTO messages(id, conversation_id, sender_id, text, about_wi, reply_to, created_at)
		VALUES (?1,?2,?3,?4,?5,?6,?7)`, id, convID, senderID, req.Text, about, reply, now)
	if err != nil {
		return nil, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &api.Message{ID: id, Seq: seq, ConversationID: convID, SenderID: senderID,
		Text: req.Text, AboutWI: req.AboutWI, ReplyTo: req.ReplyTo, CreatedAt: now}, nil
}

// allowSend applies the rolling per-sender cap.
func (m *AdminManager) allowSend(senderID string, now time.Time) bool {
	m.floodMu.Lock()
	defer m.floodMu.Unlock()
	if m.flood == nil {
		m.flood = map[string][]time.Time{}
	}
	cutoff := now.Add(-sendWindow)
	list := m.flood[senderID]
	kept := list[:0]
	for _, at := range list {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= sendCap {
		// Keep the rolling window intact: the refused request does not
		// reset it, so the cap stays a true 60 per sender per 60 s.
		m.flood[senderID] = kept
		return false
	}
	m.flood[senderID] = append(kept, now)
	return true
}

func findOrCreateDirectTx(tx *sql.Tx, a, b string) (string, error) {
	pair := []string{a, b}
	sort.Strings(pair)
	key := pair[0] + "\x00" + pair[1]
	var id string
	err := tx.QueryRow(`SELECT id FROM conversations WHERE direct_key=?1`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	id, err = NewID("conv_")
	if err != nil {
		return "", err
	}
	now := Now()
	if _, err := tx.Exec(`INSERT INTO conversations(id, kind, direct_key, created_at)
		VALUES (?1,'direct',?2,?3)`, id, key, now); err != nil {
		return "", err
	}
	for _, u := range pair {
		if _, err := tx.Exec(`INSERT INTO conversation_members(conversation_id,user_id,last_read_seq,joined_at)
			VALUES (?1,?2,0,?3)`, id, u, now); err != nil {
			return "", err
		}
	}
	return id, nil
}

// conversationAccess answers the conversation row and whether caller may
// read it (member or admin).
func (m *AdminManager) conversationAccess(callerID, convID string, isAdmin bool) error {
	var n int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id=?1`, convID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return Fail(api.ErrCoreNotFound, "", "conversation %q", convID)
	}
	if isAdmin {
		return nil
	}
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM conversation_members WHERE conversation_id=?1 AND user_id=?2`,
		convID, callerID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return Fail(api.ErrAuthForbidden, "", "conversation %s is visible to its members and admins only", convID)
	}
	return nil
}

// Page answers one stable page of history (seq cursor, default 50/max 200).
func (m *AdminManager) Page(callerID, convID string, isAdmin bool, after, before int64, limit int) (*api.MessagePage, error) {
	if err := m.conversationAccess(callerID, convID, isAdmin); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	q := `SELECT id, seq, conversation_id, sender_id, text, COALESCE(about_wi,''), COALESCE(reply_to,''), created_at
		FROM messages WHERE conversation_id=?1`
	args := []any{convID}
	if after > 0 {
		q += ` AND seq>?2`
		args = append(args, after)
	} else if before > 0 {
		q += ` AND seq<?2`
		args = append(args, before)
	}
	// Backwards paging must take the n messages immediately below the
	// cursor, so it selects from the cursor downwards and is reversed into
	// the ascending page order the caller reads.
	order := "ASC"
	if before > 0 && after <= 0 {
		order = "DESC"
	}
	q += ` ORDER BY seq ` + order + ` LIMIT ?`
	args = append(args, limit)
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &api.MessagePage{Messages: []api.Message{}, AfterSeq: after}
	for rows.Next() {
		var msg api.Message
		if err := rows.Scan(&msg.ID, &msg.Seq, &msg.ConversationID, &msg.SenderID, &msg.Text,
			&msg.AboutWI, &msg.ReplyTo, &msg.CreatedAt); err != nil {
			return nil, err
		}
		page.Messages = append(page.Messages, msg)
		if msg.Seq > page.LastSeq {
			page.LastSeq = msg.Seq
		}
	}
	if order == "DESC" {
		for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
			page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
		}
	}
	return page, rows.Err()
}

// Get reads one message the caller may see.
func (m *AdminManager) Get(callerID, messageID string, isAdmin bool) (*api.Message, error) {
	var msg api.Message
	err := m.db.QueryRow(`SELECT id, seq, conversation_id, sender_id, text, COALESCE(about_wi,''), COALESCE(reply_to,''), created_at
		FROM messages WHERE id=?1`, messageID).
		Scan(&msg.ID, &msg.Seq, &msg.ConversationID, &msg.SenderID, &msg.Text, &msg.AboutWI, &msg.ReplyTo, &msg.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Fail(api.ErrCoreNotFound, "", "message %q", messageID)
	}
	if err != nil {
		return nil, err
	}
	if err := m.conversationAccess(callerID, msg.ConversationID, isAdmin); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ListConversations answers the caller's conversations with member ids,
// last seq and unread count.
func (m *AdminManager) ListConversations(callerID string, isAdmin bool) ([]api.Conversation, error) {
	q := `SELECT c.id, c.kind, c.created_at, COALESCE(MAX(msg.seq),0)
		FROM conversations c
		LEFT JOIN messages msg ON msg.conversation_id=c.id`
	args := []any{}
	if !isAdmin {
		q += ` JOIN conversation_members cm ON cm.conversation_id=c.id AND cm.user_id=?1`
		args = append(args, callerID)
	}
	q += ` GROUP BY c.id, c.kind, c.created_at ORDER BY c.created_at ASC`
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	out := []api.Conversation{}
	for rows.Next() {
		var c api.Conversation
		if err := rows.Scan(&c.ID, &c.Kind, &c.CreatedAt, &c.LastSeq); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// Enrich after the cursor is closed: the single-writer connection must
	// not be held while another statement runs (no nested queries on rows).
	for i := range out {
		c := &out[i]
		c.Members, err = m.conversationMembers(c.ID)
		if err != nil {
			return nil, err
		}
		var read int64
		_ = m.db.QueryRow(`SELECT COALESCE(last_read_seq,0) FROM conversation_members
			WHERE conversation_id=?1 AND user_id=?2`, c.ID, callerID).Scan(&read)
		if c.LastSeq > read {
			if err := m.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=?1 AND seq>?2`,
				c.ID, read).Scan(&c.Unread); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func (m *AdminManager) conversationMembers(convID string) ([]string, error) {
	rows, err := m.db.Query(`SELECT user_id FROM conversation_members WHERE conversation_id=?1 ORDER BY user_id`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AdvanceCursor advances a member's read cursor (max-advance, idempotent).
func (m *AdminManager) AdvanceCursor(callerID string, req api.AckMessage) error {
	if req.ConversationID == "" || req.Seq <= 0 {
		return Fail(api.ErrCoreBadInput, "", "conversation_id and a positive seq are required")
	}
	if err := m.conversationAccess(callerID, req.ConversationID, false); err != nil {
		return err
	}
	res, err := m.db.Exec(`UPDATE conversation_members SET last_read_seq=?3
		WHERE conversation_id=?1 AND user_id=?2 AND last_read_seq<?3`,
		req.ConversationID, callerID, req.Seq)
	if err != nil {
		return err
	}
	_ = res
	return nil
}

// UnreadFor answers the messages for operatorID's machines that sit above
// the operator's cursor (the live inbox feed).
func (m *AdminManager) UnreadFor(userID string, limit int) ([]api.Message, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	rows, err := m.db.Query(`SELECT msg.id, msg.seq, msg.conversation_id, msg.sender_id, msg.text,
		COALESCE(msg.about_wi,''), COALESCE(msg.reply_to,''), msg.created_at
		FROM messages msg
		JOIN conversation_members cm ON cm.conversation_id=msg.conversation_id AND cm.user_id=?1
		WHERE msg.seq > cm.last_read_seq AND msg.sender_id<>?1
		ORDER BY msg.seq ASC LIMIT ?2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Message{}
	for rows.Next() {
		var msg api.Message
		if err := rows.Scan(&msg.ID, &msg.Seq, &msg.ConversationID, &msg.SenderID, &msg.Text,
			&msg.AboutWI, &msg.ReplyTo, &msg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}
