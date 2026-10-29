package core

// workitem_web.go: the web console's work-item list read
// — a read model separate from the /api/v1 List, shaped for the console
// page: fixed 20-row pages, newest first, keyset cursor, one status
// filter and a case-insensitive goal/content substring search. The
// readable-project set is pushed into the candidate SQL BEFORE the
// page cut (the shipped anti-pressure property,): no
// unreadable row costs a per-row membership lookup and none can appear
// on a page.
//
// Ordering is julianday(created_at) DESC, id DESC — never a string
// comparison of created_at: RFC3339Nano strings of different precision
// (".138Z" vs ".138881157Z") order wrong lexicographically, and the
// id tiebreak keeps a same-millisecond group deterministic. The opaque
// cursor is the last row's created_at + id, so the walk covers the list
// exactly once even across a concurrent change.

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// Web page sizes (the console's fixed shape; no flag, no knob).
const (
	webListLimit = 20
	// webCursorVersion tags the cursor encoding so a value this code
	// did not issue is refused instead of misread.
	webCursorVersion = "1"
)

// WebListFilters is the console list read's input.
type WebListFilters struct {
	ProjectID string // the RESOLVED project id (the server resolves the name)
	Status    string // "" = all; one of the api statuses otherwise
	Search    string // case-insensitive substring over goal and content
	After     string // the opaque cursor of a previous page

	// ReadableProjects narrows the candidate SQL (nil = no narrowing —
	// an unscoped admin; a non-nil empty set = nothing readable).
	ReadableProjects []string
}

// WebWorkItem is one console list row. The list answer omits Content;
// the search face reuses the read and projects its own rows with it.
type WebWorkItem struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Seq       int64  `json:"seq"`
	Goal      string `json:"goal"`
	Status    string `json:"status"`
	Priority  string `json:"priority"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content,omitempty"` // the search face's rows carry it
}

// webListSelect reads one console list row.
const webListSelect = `SELECT w.id, p.name, w.seq, w.goal, w.status, w.priority, w.created_at, COALESCE(w.content,'')
	FROM work_items w JOIN projects p ON p.id=w.project_id`

// ListWeb answers one console page: at most 20 rows, newest first,
// plus the next page's opaque cursor whenever a further row exists.
func (m *WIManager) ListWeb(f WebListFilters) ([]WebWorkItem, string, error) {
	if f.Status != "" && !webValidStatus(f.Status) {
		return nil, "", Fail(api.ErrCoreBadInput, "", "unknown status %q", f.Status)
	}
	var afterTS, afterID string
	if f.After != "" {
		var err error
		afterTS, afterID, err = decodeWebCursor(f.After)
		if err != nil {
			return nil, "", Fail(api.ErrCoreBadInput, "", "invalid cursor")
		}
	}
	if f.ReadableProjects != nil && len(f.ReadableProjects) == 0 {
		// Nothing readable anywhere: the empty page, no query.
		return []WebWorkItem{}, "", nil
	}
	q := webListSelect + ` WHERE w.project_id=?`
	args := []any{f.ProjectID}
	if f.Status != "" {
		q += ` AND w.status=?`
		args = append(args, f.Status)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		// Case-insensitive substring over goal and content ONLY: no
		// other field joins the match (labels never match).
		q += ` AND (instr(lower(w.goal), lower(?)) OR instr(lower(COALESCE(w.content,'')), lower(?)))`
		args = append(args, s, s)
	}
	if f.ReadableProjects != nil {
		q += ` AND w.project_id IN (`
		for i, p := range f.ReadableProjects {
			if i > 0 {
				q += `,`
			}
			q += `?`
			args = append(args, p)
		}
		q += `)`
	}
	if f.After != "" {
		q += ` AND (julianday(w.created_at) < julianday(?)` +
			` OR (julianday(w.created_at) = julianday(?) AND w.id < ?))`
		args = append(args, afterTS, afterTS, afterID)
	}
	// 20 + one probe row: the probe proves whether a next page exists.
	q += ` ORDER BY julianday(w.created_at) DESC, w.id DESC LIMIT ?`
	args = append(args, webListLimit+1)
	rows, err := m.db.Query(q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []WebWorkItem{}
	for rows.Next() {
		var it WebWorkItem
		var seq int64
		var project string
		if err := rows.Scan(&it.ID, &project, &seq, &it.Goal, &it.Status, &it.Priority, &it.CreatedAt, &it.Content); err != nil {
			return nil, "", err
		}
		it.Seq = seq
		it.Slug = project + "#" + strconv.FormatInt(seq, 10)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > webListLimit {
		last := out[webListLimit-1]
		var err error
		if next, err = encodeWebCursor(last.CreatedAt, last.ID); err != nil {
			return nil, "", err
		}
		out = out[:webListLimit]
	}
	return out, next, nil
}

// webValidStatus answers whether s is one of the api's work-item
// statuses (the console exposes the api vocabulary, nothing wider).
func webValidStatus(s string) bool {
	switch s {
	case api.StatusQueued, api.StatusRunning, api.StatusPaused, api.StatusBlocked,
		api.StatusWrapped, api.StatusFailed, api.StatusCanceled:
		return true
	}
	return false
}

// encodeWebCursor answers the opaque cursor of one page's last row:
// base64url of "<version>|<created_at>|<id>", unpadded.
func encodeWebCursor(createdAt, id string) (string, error) {
	raw := webCursorVersion + "|" + createdAt + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw)), nil
}

// decodeWebCursor parses a cursor this code issued.
func decodeWebCursor(cursor string) (createdAt, id string, err error) {
	raw, derr := base64.RawURLEncoding.DecodeString(cursor)
	if derr != nil {
		return "", "", derr
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] != webCursorVersion || parts[1] == "" || parts[2] == "" {
		return "", "", Fail(api.ErrCoreBadInput, "", "malformed cursor")
	}
	return parts[1], parts[2], nil
}
