package memory

// browse.go: the memory store's browse mode the
// empty-query list face behind GET /api/v1/memories/search and the
// web console's memories module. Browse answers pages of the caller's readable, active
// entries in descending updated_at (default) or created_at order,
// narrowed in SQL (readable projects, status, content, type, tags)
// BEFORE the page cut so a page costs constant work no matter how many
// candidates exist (the guarantee: no per-candidate
// membership query). Paging is a strict keyset walk behind an opaque
// cursor: the cursor carries the sort name, the sort-key timestamp and
// the id of the last returned entry; the next page continues strictly
// after that (sort key, id) position, so a write between pages can
// neither duplicate nor skip an unmodified entry.

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// browseSortKeys are the two legal sort values, in error-message order.
const browseSortKeys = `"updated" or "created"`

// browseCursorVersion tags the opaque cursor's encoding so a value this
// store did not issue (or one from a changed encoding) is refused
// instead of misread.
const browseCursorVersion = "1"

// defaultBrowseLimit is the page size when the caller sends none.
const defaultBrowseLimit = 20

// Browse answers one page of the caller's readable active memories in
// descending sort order. projects narrows the candidates in SQL exactly
// like SearchMode (nil = every project, an empty non-nil set = nothing
// readable anywhere and answers without a candidate query); sort is
// "updated" (the default) or "created", anything else is MEM-400 naming
// both; memType filters by exact type or "X.*" prefix ("" = no filter,
// "*" is normalized to no filter — the browse spelling of "every
// type"); tags (every listed tag) narrows in SQL via json_each. after is
// the opaque cursor of a previous page (refused when malformed,
// unversioned or issued under another sort); allowed enforces the
// visibility tiers over the narrowed page. The answer carries the next
// page's cursor whenever a further visible entry exists ("" on the last
// page).
func (s *Store) Browse(projects []string, sort, after, memType string, tags []string, limit int, allowed func(*Entry) bool) ([]Entry, string, error) {
	if sort == "" {
		sort = "updated"
	}
	if sort != "updated" && sort != "created" {
		return nil, "", errf(api.ErrMemBadInput, "", "sort must be %s", browseSortKeys)
	}
	if memType == "*" {
		memType = ""
	}
	if limit <= 0 {
		limit = defaultBrowseLimit
	}
	cursorSort, key, afterID := sort, "", ""
	if after != "" {
		var err error
		cursorSort, key, afterID, err = decodeBrowseCursor(after)
		if err != nil {
			return nil, "", err
		}
		if cursorSort != sort {
			return nil, "", errf(api.ErrMemBadInput, "",
				"invalid cursor: issued for sort %q, requested %q", cursorSort, sort)
		}
	}
	if projects != nil && len(projects) == 0 {
		// Nothing readable anywhere: no candidate query at all.
		return []Entry{}, "", nil
	}
	column := "updated_at"
	if sort == "created" {
		column = "created_at"
	}
	// The narrowing happens in one candidate statement, ordered
	// descending by the sort key with the id as the deterministic
	// tie-break; rows stream through the visibility predicate (pure,
	// no I/O) until limit+1 survived — the probe row proves whether a
	// next page exists.
	where := " WHERE m.status='active' AND m.content IS NOT NULL"
	args := []any{}
	if len(projects) > 0 {
		ph := make([]string, len(projects))
		for i, p := range projects {
			ph[i] = "?" + strconv.Itoa(len(args)+i+1)
			args = append(args, p)
		}
		where += " AND m.project_id IN (" + strings.Join(ph, ",") + ")"
	}
	if after != "" {
		n := len(args)
		where += " AND (julianday(m." + column + ") < julianday(?" + strconv.Itoa(n+1) + ")" +
			" OR (julianday(m." + column + ") = julianday(?" + strconv.Itoa(n+1) + ")" +
			" AND m.id < ?" + strconv.Itoa(n+2) + "))"
		args = append(args, key, afterID)
	}
	if memType != "" {
		if prefix, ok := strings.CutSuffix(memType, ".*"); ok {
			n := len(args)
			where += " AND substr(m.type,1,?" + strconv.Itoa(n+1) + ")=?" + strconv.Itoa(n+2)
			args = append(args, len(prefix)+1, prefix+".")
		} else {
			where += " AND m.type=?" + strconv.Itoa(len(args)+1)
			args = append(args, memType)
		}
	}
	for _, tag := range tags {
		where += " AND EXISTS (SELECT 1 FROM json_each(m.tags) je WHERE je.value=?" +
			strconv.Itoa(len(args)+1) + ")"
		args = append(args, tag)
	}
	rows, err := s.db.Query(`SELECT m.id, m.project_id, m.owner_user_id, m.work_item_id, m.type, m.title,
		m.visibility, m.content_sha, m.content_len, m.tags, m.related, m.status, m.created_at, m.updated_at
		FROM memories m`+where+" ORDER BY julianday(m."+column+") DESC, m.id DESC", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scanBrowseRow(rows)
		if err != nil {
			return nil, "", err
		}
		if allowed != nil && !allowed(e) {
			continue
		}
		out = append(out, *e)
		if len(out) > limit {
			break // the limit+1 probe row: a next page exists
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		key := last.UpdatedAt
		if sort == "created" {
			key = last.CreatedAt
		}
		next = encodeBrowseCursor(sort, key, last.ID)
		out = out[:limit]
	}
	return out, next, nil
}

// browseScanner is the row-scan interface *sql.Rows and *sql.Row share.
type browseScanner interface{ Scan(dest ...any) error }

// scanBrowseRow fills one Entry from a candidate row (load's column
// set over the browse candidate statement).
func scanBrowseRow(scan browseScanner) (*Entry, error) {
	var e Entry
	var wi, title sql.NullString
	var tags, related string
	if err := scan.Scan(&e.ID, &e.ProjectID, &e.OwnerUserID, &wi, &e.Type, &title, &e.Visibility,
		&e.ContentSHA, &e.ContentLen, &tags, &related, &e.Status, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	if wi.Valid {
		e.WorkItemID = &wi.String
	}
	if title.Valid {
		e.Title = &title.String
	}
	_ = jsonUnmarshalStrings(tags, &e.Tags)
	_ = jsonUnmarshalStrings(related, &e.Related)
	return &e, nil
}

// jsonUnmarshalStrings decodes a stored JSON string array, never
// leaving a nil slice (the answer fields carry [] today).
func jsonUnmarshalStrings(raw string, out *[]string) error {
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			return err
		}
	}
	if *out == nil {
		*out = []string{}
	}
	return nil
}

// encodeBrowseCursor renders the opaque cursor: base64url over a
// version tag, the sort name, the sort-key timestamp and the id of the
// last returned entry. The encoding stays unspecified in the docs; only
// the round-trip behaviour is contract.
func encodeBrowseCursor(sort, key, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(browseCursorVersion + "|" + sort + "|" + key + "|" + id))
}

// decodeBrowseCursor parses a cursor this store issued (see
// encodeBrowseCursor). Anything else — undecodable, unversioned, a
// non-timestamp key or a missing id — is MEM-400.
func decodeBrowseCursor(cursor string) (sort, key, id string, err error) {
	raw, derr := base64.RawURLEncoding.DecodeString(cursor)
	if derr != nil {
		return "", "", "", errf(api.ErrMemBadInput, "", "invalid cursor")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != browseCursorVersion {
		return "", "", "", errf(api.ErrMemBadInput, "", "invalid cursor")
	}
	sort, key, id = parts[1], parts[2], parts[3]
	if id == "" {
		return "", "", "", errf(api.ErrMemBadInput, "", "invalid cursor")
	}
	if _, perr := time.Parse(time.RFC3339Nano, key); perr != nil {
		return "", "", "", errf(api.ErrMemBadInput, "", "invalid cursor")
	}
	return sort, key, id, nil
}
