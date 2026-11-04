package core

// workitem_comment.go: append-only work-item comments.
// A comment carries its author, a server-assigned time, the body and an
// optional reply_to that must name a comment on the SAME work item.
// There is no edit and no delete: Append is the only write, and Comments
// is the only read.

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// AddComment appends one comment to a work item and answers the stored
// row. A blank body, an unknown work item and a reply_to that names no
// comment on that same work item are refused before anything is written.
func (m *WIManager) AddComment(wiID, author string, req api.AddCommentRequest) (*api.Comment, error) {
	if strings.TrimSpace(req.Body) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "comment body required")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var one int
	if err := tx.QueryRow(`SELECT 1 FROM work_items WHERE id=?1`, wiID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, Fail(api.ErrCoreNotFound, "", "work item %s", wiID)
		}
		return nil, err
	}
	if strings.TrimSpace(author) == "" {
		return nil, Fail(api.ErrCoreBadInput, "", "comment author required")
	}
	if req.ReplyTo != "" {
		var parentWI string
		err := tx.QueryRow(`SELECT work_item_id FROM wi_comments WHERE id=?1`, req.ReplyTo).Scan(&parentWI)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && parentWI != wiID) {
			return nil, Fail(api.ErrCoreNotFound, "",
				"reply_to comment %s is not on work item %s", req.ReplyTo, wiID)
		}
		if err != nil {
			return nil, err
		}
	}

	id, err := NewID("wic_")
	if err != nil {
		return nil, err
	}
	now := Now()
	var reply any
	if req.ReplyTo != "" {
		reply = req.ReplyTo
	}
	if _, err := tx.Exec(`INSERT INTO wi_comments(id, work_item_id, author, body, reply_to, created_at)
		VALUES (?1,?2,?3,?4,?5,?6)`, id, wiID, author, req.Body, reply, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &api.Comment{ID: id, Author: author, Body: req.Body, ReplyTo: req.ReplyTo, CreatedAt: now}, nil
}

// Comments reads one work item's comment thread in insertion (seq) order.
// An absent thread is an empty list, never an error.
func (m *WIManager) Comments(wiID string) ([]api.Comment, error) {
	rows, err := m.db.Query(`SELECT id, author, body, COALESCE(reply_to,''), created_at
		FROM wi_comments WHERE work_item_id=?1 ORDER BY seq`, wiID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Comment{}
	for rows.Next() {
		var c api.Comment
		if err := rows.Scan(&c.ID, &c.Author, &c.Body, &c.ReplyTo, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
