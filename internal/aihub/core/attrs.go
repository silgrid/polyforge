package core

// attrs.go: the generic attrs write and filter (workflow contract 1,
// design §3.4). aihub keeps no lane's business rules: a lane
// keeps its own record in one top-level attrs key and its own scripts
// judge what goes in it; aihub only
//
//   - writes one top-level key whole (WriteAttr: PATCH
//     /work-items/{id}/attrs), on a work item of any status — a terminal one
//     included, whose record a later lane moves on — fenced by the caller's
//     optional `if` conditions (compare-and-swap: every named attrs path
//     must hold the named JSON value, an absent path reading as null, or
//     the write is CORE-409 ATTRS_IF_MISMATCH and nothing is written), and
//     records one attrs_written event per write carrying the key, the
//     machine that signed the request and, when the write carries out the
//     owner's word, that owner message (copied, never read);
//   - filters a list on attrs paths (ParseAttrFilters: GET
//     /work-items?attr=<path>:<value>, repeatable; the attr at the path is
//     that string).
//
// The key Core writes itself (checkAttrs: the blocked seal) is refused
// here as at create.
//
// Reachability: server.go serves WriteAttr as a signed machine route only.
// A step agent cannot reach it directly — a step process holds no aihub
// key and no machine key; it reaches it only through the daemon proxy
// (`pf step hub attrs`, a write recorded pending that the daemon makes
// when the step round hands off done, the key on the step's whitelist).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/silgrid/polyforge/internal/common/api"
)

// AttrKeyPattern is the shape of a top-level attrs key WriteAttr writes,
// and AttrPathPattern the shape of an attrs path (`if` and the list
// filter): dot-separated segments of lower-case letters, digits and _.
var (
	AttrKeyPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	AttrPathPattern = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)
)

// ReasonAttrsIfMismatch is the stable reason of a write whose `if`
// conditions do not hold (CORE-409).
const ReasonAttrsIfMismatch = "ATTRS_IF_MISMATCH"

// WriteAttr writes one top-level attrs key of the work item (see the file
// comment) and answers the work item. A null value deletes the key.
func (m *WIManager) WriteAttr(idOrSlug string, req api.AttrsWriteRequest, actor, machine string) (*WIView, error) {
	if !AttrKeyPattern.MatchString(req.Key) {
		return nil, Fail(api.ErrCoreBadInput, "", "key %q is not an attrs key (%s)", req.Key, AttrKeyPattern)
	}
	if err := checkAttrs(map[string]any{req.Key: nil}); err != nil {
		return nil, err
	}
	if len(req.Value) == 0 || !json.Valid(req.Value) {
		return nil, Fail(api.ErrCoreBadInput, "", "value is one JSON document")
	}
	var value any
	if err := json.Unmarshal(req.Value, &value); err != nil {
		return nil, Fail(api.ErrCoreBadInput, "", "value is one JSON document")
	}
	conds := make(map[string]any, len(req.If))
	for path, raw := range req.If {
		if !AttrPathPattern.MatchString(path) {
			return nil, Fail(api.ErrCoreBadInput, "", "if: %q is not an attrs path (%s)", path, AttrPathPattern)
		}
		var want any
		if len(raw) == 0 || json.Unmarshal(raw, &want) != nil {
			return nil, Fail(api.ErrCoreBadInput, "", "if: %s names no JSON value", path)
		}
		conds[path] = want
	}
	if om := req.OwnerMessage; om != nil && (strings.TrimSpace(om.Words) == "" || strings.TrimSpace(om.By) == "" || strings.TrimSpace(om.At) == "") {
		return nil, Fail(api.ErrCoreBadInput, "", "owner_message carries words, by and at")
	}
	id, err := m.Resolve(idOrSlug)
	if err != nil {
		return nil, err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRow(`SELECT status FROM work_items WHERE id=?1`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, Fail(api.ErrCoreNotFound, "", "work item %s", id)
		}
		return nil, err
	}
	attrs, err := loadAttrsTx(tx, id)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(conds))
	for p := range conds {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if got := attrAt(attrs, p); !reflect.DeepEqual(got, conds[p]) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(conds[p])
			return nil, Fail(api.ErrCoreConflict, ReasonAttrsIfMismatch, "attrs.%s is %s, not %s: the write is fenced on it and nothing was written", p, gotJSON, wantJSON)
		}
	}
	if value == nil {
		delete(attrs, req.Key)
	} else {
		attrs[req.Key] = value
	}
	blob, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	now := Now()
	if _, err := tx.Exec(`UPDATE work_items SET attrs=?1, updated_at=?2 WHERE id=?3`, string(blob), now, id); err != nil {
		return nil, err
	}
	payload := map[string]any{"key": req.Key, "machine_id": machine}
	if om := req.OwnerMessage; om != nil {
		payload["owner_message"] = map[string]any{"words": om.Words, "by": om.By, "at": om.At}
	}
	if err := appendEventTx(tx, id, now, actor, "attrs_written", payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return m.load(id)
}

// attrAt answers the value at a dot path of attrs (nil when any segment is
// absent or its parent is no object).
func attrAt(attrs map[string]any, path string) any {
	var cur any = attrs
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[seg]
	}
	return cur
}

// AttrMatch is one list filter on attrs: the attr at Path is the string
// Value.
type AttrMatch struct {
	Path, Value string
}

// ParseAttrFilters reads the list's `attr` parameters, each
// `<path>:<value>` (the path's shape is AttrPathPattern; the value is the
// rest, the empty string included).
func ParseAttrFilters(raw []string) ([]AttrMatch, error) {
	var out []AttrMatch
	for _, r := range raw {
		path, value, ok := strings.Cut(r, ":")
		if !ok || !AttrPathPattern.MatchString(path) {
			return nil, Fail(api.ErrCoreBadInput, "", "attr=%q is not <path>:<value> with an attrs path (%s)", r, AttrPathPattern)
		}
		out = append(out, AttrMatch{Path: path, Value: value})
	}
	return out, nil
}

// jsonPath renders an attrs path as SQLite's JSON path, each segment
// quoted (a segment may start with a digit).
func jsonPath(path string) string {
	return `$."` + strings.ReplaceAll(path, ".", `"."`) + `"`
}
