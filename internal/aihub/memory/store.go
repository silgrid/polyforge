package memory

// Store: the memories rows (the stored document lives in the content
// column), FTS5-trigram search (with the short-query widening for <3-rune
// tokens, kept from v0: trigram MATCH cannot answer those) and redact.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
)

// Entry is the memories row as served. ContentSHA and ContentLen
// describe the stored document (frontmatter + body).
type Entry struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	OwnerUserID string   `json:"owner_user_id"`
	WorkItemID  *string  `json:"work_item_id"`
	Type        string   `json:"type"`
	Title       *string  `json:"title"`
	Visibility  string   `json:"visibility"`
	ContentSHA  string   `json:"content_sha"`
	ContentLen  int64    `json:"content_len"`
	Tags        []string `json:"tags"`
	Related     []string `json:"related"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// Store is the memory subsystem.
type Store struct {
	db *core.DB
	// dataDir holds the memory/ tree of files a database from before the
	// content column carries: read only by the content migration
	// (migrate.go), never written.
	dataDir string

	// now is the search clock the freshness term ages entries against
	// (freshness.go); tests pin it.
	now func() time.Time
}

func NewStore(db *core.DB, dataDir string) *Store {
	return &Store{db: db, dataDir: dataDir, now: time.Now}
}

// CreateRequest is the POST /api/v1/memories body.
type CreateRequest struct {
	Project        string   `json:"project"`
	Type           string   `json:"type"`
	Title          *string  `json:"title"`
	Visibility     string   `json:"visibility"`
	Content        string   `json:"content"`
	Tags           []string `json:"tags"`
	Related        []string `json:"related"`
	WorkItemID     *string  `json:"work_item_id"`
	IdempotencyKey *string  `json:"idempotency_key"`
}

// Create renders the stored document (frontmatter + body) and writes the
// row and its FTS row in one Core transaction. A blank title is derived
// from the body for the row (rowTitle); the document's frontmatter keeps
// the title as requested.
func (s *Store) Create(req CreateRequest, owner string) (*Entry, error) {
	if strings.TrimSpace(req.Content) == "" {
		return nil, errf(api.ErrMemBadInput, "", "content required")
	}
	if err := checkType(req.Type); err != nil {
		return nil, err
	}
	if !visibilities[req.Visibility] {
		return nil, errf(api.ErrMemBadInput, "", "bad visibility %q", req.Visibility)
	}
	if len(req.Content) > maxContentBytes {
		return nil, errf(api.ErrMemTooLarge, "", "content exceeds 1 MiB")
	}
	var projectID string
	if err := s.db.QueryRow(`SELECT id FROM projects WHERE name=?1 OR id=?1`, req.Project).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errf(api.ErrMemNotFound, "", "project %q", req.Project)
		}
		return nil, err
	}
	// Idempotent create: same key + owner + project returns the original.
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		var existing, proj, own string
		qerr := s.db.QueryRow(`SELECT id, project_id, owner_user_id FROM memories WHERE idempotency_key=?1`,
			*req.IdempotencyKey).Scan(&existing, &proj, &own)
		if qerr == nil {
			if proj != projectID || own != owner {
				return nil, errf(api.ErrMemConflict, "IDEMPOTENCY_KEY_REUSED",
					"idempotency_key belongs to another owner or project")
			}
			return s.Get(existing)
		}
		if !errors.Is(qerr, sql.ErrNoRows) {
			return nil, qerr
		}
	}
	// A client frontmatter block seeds nothing: one carrying an id is
	// refused, any other is dropped whole — every field comes from the
	// request body, and the stored document gets the server's own block.
	var body string
	if block, rest, ok := splitFrontmatter(req.Content); ok {
		raw, err := parseFrontmatterBlock(block)
		if err != nil {
			return nil, err
		}
		if _, has := raw["id"]; has {
			return nil, errf(api.ErrMemBadInput, "CLIENT_ID_NOT_ALLOWED", "content frontmatter must not carry an id")
		}
		body = rest
	} else {
		body = req.Content
	}
	memID, err := core.NewID("mem_")
	if err != nil {
		return nil, err
	}
	now := core.Now()
	fm := &frontmatter{
		ID: memID, Type: req.Type, OwnerUserID: owner, Title: req.Title,
		Tags: req.Tags, Visibility: req.Visibility, WorkItemID: req.WorkItemID,
		ProjectID: projectID, IdempotencyKey: req.IdempotencyKey, Related: req.Related,
		CreatedAt: now, UpdatedAt: now,
	}
	if fm.Tags == nil {
		fm.Tags = []string{}
	}
	if fm.Related == nil {
		fm.Related = []string{}
	}
	doc, err := buildDocument(fm, body)
	if err != nil {
		return nil, err
	}
	if len(doc) > maxContentBytes {
		return nil, errf(api.ErrMemTooLarge, "", "authority document exceeds 1 MiB")
	}
	var idem any
	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		idem = *req.IdempotencyKey
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, docBody, _ := splitFrontmatter(doc)
	title := rowTitle(req.Title, docBody)
	if _, err := tx.Exec(`INSERT INTO memories(id, project_id, owner_user_id, work_item_id, type, title,
		visibility, authority_path, content_sha, content_len, tags, related, idempotency_key, status, created_at, updated_at,
		content)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?15,?8,?9,?10,?11,?12,'active',?13,?13,?14)`,
		memID, projectID, owner, anyPtr(req.WorkItemID), req.Type, anyPtr(title), req.Visibility,
		shaHex([]byte(doc)), len(doc), jsonOrEmpty(req.Tags), jsonOrEmpty(req.Related), idem, now,
		doc, authorityRel(projectID, memID)); err != nil {
		return nil, err
	}
	if err := ftsReplace(tx, memID, extractBody(doc)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.load(memID)
}

// Get serves one active entry.
func (s *Store) Get(memID string) (*Entry, error) {
	e, err := s.load(memID)
	if err != nil {
		return nil, err
	}
	if e.Status != "active" {
		return nil, errf(api.ErrMemNotFound, "", "memory %s", memID)
	}
	return e, nil
}

// Content returns an active entry's stored document (frontmatter + body).
func (s *Store) Content(memID string) ([]byte, error) {
	var doc string
	err := s.db.QueryRow(`SELECT content FROM memories WHERE id=?1 AND status='active'
		AND content IS NOT NULL`, memID).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errf(api.ErrMemNotFound, "", "memory %s", memID)
	}
	if err != nil {
		return nil, err
	}
	return []byte(doc), nil
}

// load reads one row. A row whose content was never filled (its memory
// file was missing at migration, migrate.go) does not exist as far as
// serving is concerned.
func (s *Store) load(memID string) (*Entry, error) {
	var e Entry
	var wi, title sql.NullString
	var tags, related string
	err := s.db.QueryRow(`SELECT id, project_id, owner_user_id, work_item_id, type, title, visibility,
		content_sha, content_len, tags, related, status, created_at, updated_at
		FROM memories WHERE id=?1 AND content IS NOT NULL`, memID).
		Scan(&e.ID, &e.ProjectID, &e.OwnerUserID, &wi, &e.Type, &title, &e.Visibility,
			&e.ContentSHA, &e.ContentLen, &tags, &related, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errf(api.ErrMemNotFound, "", "memory %s", memID)
	}
	if err != nil {
		return nil, err
	}
	if wi.Valid {
		e.WorkItemID = &wi.String
	}
	if title.Valid {
		e.Title = &title.String
	}
	_ = json.Unmarshal([]byte(tags), &e.Tags)
	if e.Tags == nil {
		e.Tags = []string{}
	}
	_ = json.Unmarshal([]byte(related), &e.Related)
	if e.Related == nil {
		e.Related = []string{}
	}
	return &e, nil
}

// Search runs the real-time text query: FTS5 trigram for ≥3-rune tokens,
// substring widening for shorter tokens (kept from v0; a trigram index
// cannot answer <3-rune queries, which are the common CJK shape). Hits
// come back in bm25 order weighted by per-type freshness (freshness.go).
// memType filters by memory type: a value ending in ".*" is a prefix
// match (X.* → prefix "X."), any other value is an exact match.
// allowed filters candidates (visibility, applied by the server layer).
func (s *Store) Search(q string, memType string, tags []string, limit int, allowed func(*Entry) bool) ([]Entry, error) {
	return s.SearchMode(MatchAll, nil, q, memType, tags, limit, allowed)
}

// SearchMode is the search face's single entry: match picks the mode
// (MatchAll / "" — Search's every-token AND; MatchAny — SearchAny's
// ranked any-term OR; anything else is MEM-400). projects narrows the
// candidates INSIDE the FTS / scan SQL — nil = every project, a
// non-nil empty set = nothing readable anywhere (short-circuits to the
// empty answer without touching the database) — a pure narrowing that
// replaced the caller's per-candidate membership predicate, so
// unreadable matches cost no load at all; the caller's
// allowed predicate still enforces the visibility tiers.
func (s *Store) SearchMode(match string, projects []string, q, memType string, tags []string, limit int, allowed func(*Entry) bool) ([]Entry, error) {
	switch match {
	case "", MatchAll, MatchAny:
	default:
		return nil, errf(api.ErrMemBadInput, "", "match must be %q or %q", MatchAll, MatchAny)
	}
	if strings.TrimSpace(q) == "" {
		return nil, errf(api.ErrMemBadInput, "", "q required")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	f := searchFilter{projects: projects, memType: memType, tags: tags, limit: limit, allowed: allowed,
		now: s.now()}
	if match == MatchAny {
		return s.searchAny(q, f)
	}
	var long, short []string
	for _, tok := range strings.Fields(q) {
		if utf8.RuneCountInString(tok) >= 3 {
			long = append(long, tok)
		} else {
			short = append(short, tok)
		}
	}
	fts := ""
	if len(long) > 0 {
		fts = ftsQuery(long)
	}
	return s.collect(fts, short, 0, f)
}

// searchFilter is one search's post-MATCH filter set plus its limit.
type searchFilter struct {
	projects []string // project ids narrowing the SQL candidates; nil = all, empty = none
	memType  string
	tags     []string
	limit    int
	allowed  func(*Entry) bool
	now      time.Time // the query time the freshness term ages against
}

// collect answers the filtered hits for one search. match is the FTS5
// MATCH expression ("" = no FTS step: every active body is scanned);
// short tokens must each appear as a substring of the indexed body.
// maxCands > 0 bounds the FTS candidates examined in total (one
// rank-ordered window, no further paging); 0 pages until the limit is
// filled or the matches run out.
func (s *Store) collect(match string, short []string, maxCands int, f searchFilter) ([]Entry, error) {
	memType, tags, limit, allowed := f.memType, f.tags, f.limit, f.allowed
	out := []Entry{}
	if f.projects != nil && len(f.projects) == 0 {
		// Nothing readable anywhere: no candidate query at all.
		return out, nil
	}
	// seen: a write between two FTS pages can shift ranks, so the same
	// entry may appear on consecutive pages — it is answered once.
	seen := map[string]bool{}
	// take filters one candidate batch into out; true once out is full.
	take := func(cands []candRow) bool {
		for _, c := range cands {
			if seen[c.id] {
				continue
			}
			seen[c.id] = true
			stale := false
			for _, tok := range short {
				if !strings.Contains(c.body, tok) {
					stale = true
					break
				}
			}
			if stale {
				continue
			}
			e, err := s.load(c.id)
			if err != nil || e.Status != "active" {
				continue
			}
			if !typeMatches(e.Type, memType) {
				continue
			}
			if len(tags) > 0 && !containsAll(e.Tags, tags) {
				continue
			}
			if allowed != nil && !allowed(e) {
				continue
			}
			out = append(out, *e)
			if len(out) >= limit {
				return true
			}
		}
		return false
	}
	if match == "" {
		scanned, serr := s.scanBodies(f.projects, f.now)
		if serr != nil {
			return nil, serr
		}
		take(scanned)
		return out, nil
	}
	// A bounded search examines one best-ranked window and stops there.
	if maxCands > 0 {
		cands, err := s.ftsPage(match, f.projects, f.now, maxCands, 0)
		if err != nil {
			return nil, err
		}
		take(cands)
		return out, nil
	}
	// FTS candidates are paged in rank order until limit entries survive
	// the filters or the matches run out: the type/tag/visibility filters
	// run after the MATCH, so one fixed-size candidate window
	// could silently answer fewer than limit hits while more exist.
	batch := limit * 4
	for offset := 0; ; offset += batch {
		cands, err := s.ftsPage(match, f.projects, f.now, batch, offset)
		if err != nil {
			return nil, err
		}
		if take(cands) || len(cands) < batch {
			return out, nil
		}
	}
}

// ftsPage reads one page of FTS candidates in freshness-weighted rank
// order (freshness.go; now is the query time). projects, when non-nil,
// keeps only those projects' rows — the narrowing sits in the ranked
// statement itself (a JOIN on memories with status='active' AND
// project_id IN (...)), so a caller's unreadable matches never enter
// the ranked list at all. nil projects keeps every
// project (admin) through the same statement shape.
//
// Cost shape: exactly two statements per page, whatever the matches
// look like. One ranked statement — the FTS MATCH joined to the
// memories row, answering every match's FTS rowid, bm25 rank, id, type
// and updated_at (the stored document is never read there) — and one
// bodies batch for the page alone. Scoring, ordering and the page cut
// all happen in Go over the whole (project-narrowed) match set, so
// the statement count cannot grow with the number of matches, tied or
// clustered ranks included (query count is bounded on
// the readable path too). The answer is the exact weighted order.
func (s *Store) ftsPage(match string, projects []string, now time.Time, limit, offset int) ([]candRow, error) {
	// The MATCH LHS keeps the table name: SQLite resolves it to the
	// aliased FTS table, and the planner scans the FTS virtual table
	// first, memories by key.
	clause, pargs := sqlProjectSet(projects, 2)
	rows, err := s.db.Query(`SELECT f.rowid, f.rank, m.id, m.type, m.updated_at
		FROM memory_fts f JOIN memories m ON m.id = f.mem_id
		WHERE memory_fts MATCH ?1 AND m.status='active'`+clause,
		append([]any{match}, pargs...)...)
	if err != nil {
		// A typed refusal, never a degrade to a full scan: answering
		// every memory for a query FTS could not parse is a leak.
		return nil, errf(api.ErrMemUnavailable, "", "fts: %v", err)
	}
	var all []scoredCand
	for rows.Next() {
		var c scoredCand
		var typ, updatedAt string
		var rank float64
		if err := rows.Scan(&c.rowid, &rank, &c.id, &typ, &updatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		c.typ = typ
		c.updated = parseUpdatedAt(updatedAt)
		c.score = rank * (1 + freshnessBoost*freshness(typ, c.updated, now))
		all = append(all, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortScored(all)
	want := offset + limit
	if offset >= len(all) {
		return nil, nil
	}
	all = all[offset:min(want, len(all))]
	keys := make([]int64, len(all))
	for i, c := range all {
		keys[i] = c.rowid
	}
	bodies, err := s.ftsBodies(keys)
	if err != nil {
		return nil, err
	}
	// An entry redacted since the ranked statement ran keeps its slot
	// with an empty body (the page length is collect's end-of-matches
	// signal); collect's status check drops it.
	out := make([]candRow, len(all))
	for i, c := range all {
		out[i] = candRow{c.id, bodies[c.rowid]}
	}
	return out, nil
}

// ftsBodies answers the indexed body per FTS rowid.
func (s *Store) ftsBodies(keys []int64) (map[int64]string, error) {
	rows, err := s.db.Query(`SELECT rowid, body FROM memory_fts WHERE rowid IN (` + rowidList(keys) + `)`)
	if err != nil {
		return nil, errf(api.ErrMemUnavailable, "", "fts: %v", err)
	}
	defer rows.Close()
	out := make(map[int64]string, len(keys))
	for rows.Next() {
		var k int64
		var body string
		if err := rows.Scan(&k, &body); err != nil {
			return nil, err
		}
		out[k] = body
	}
	return out, rows.Err()
}

// sqlProjectSet renders the readable-project narrowing for the
// memories joins: nil answers no clause (every project); a non-nil set
// answers an IN (...) list whose ?N placeholders start at firstArg,
// with the ids as the trailing query args. The empty set answers a
// constant-false clause — collect short-circuits before any query,
// this is only the defensive leg.
func sqlProjectSet(projects []string, firstArg int) (string, []any) {
	if projects == nil {
		return "", nil
	}
	if len(projects) == 0 {
		return " AND 0", nil
	}
	ph := make([]string, len(projects))
	args := make([]any, len(projects))
	for i, p := range projects {
		ph[i] = "?" + strconv.Itoa(firstArg+i)
		args[i] = p
	}
	return " AND m.project_id IN (" + strings.Join(ph, ",") + ")", args
}

// rowidList renders integer keys as an SQL IN list.
func rowidList(keys []int64) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = strconv.FormatInt(k, 10)
	}
	return strings.Join(parts, ",")
}

// scoredCand is one candidate with its score (lower is better).
type scoredCand struct {
	candRow
	rowid   int64 // FTS rowid (ftsPage)
	typ     string
	updated time.Time // parseUpdatedAt
	score   float64
}

// sortScored orders candidates by score, then updated_at DESC (as
// instants), then id.
func sortScored(all []scoredCand) {
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if !a.updated.Equal(b.updated) {
			return a.updated.After(b.updated)
		}
		return a.id < b.id
	})
}

// typeMatches answers the type filter for one entry. An empty filter
// passes every type. A value ending in ".*" is a prefix match on "X.":
// experience.* matches experience.debug and experience.approach but
// not fact.note (and not the bare type experience). Any other value
// stays an exact match. No whitelist — a new type prefix filters as
// soon as memories carry it.
func typeMatches(entryType, memType string) bool {
	if memType == "" {
		return true
	}
	if prefix, ok := strings.CutSuffix(memType, ".*"); ok {
		return strings.HasPrefix(entryType, prefix+".")
	}
	return entryType == memType
}

// candRow is one search candidate (mem id + indexed body).
type candRow struct{ id, body string }

// scanBodies returns every active entry's indexed body for substring
// filtering (the short-token path), freshest first (no rank exists here;
// freshness.go); projects, when non-nil, keeps only those projects'
// rows (nil = every project).
func (s *Store) scanBodies(projects []string, now time.Time) ([]candRow, error) {
	clause, pargs := sqlProjectSet(projects, 1)
	rows, err := s.db.Query(`SELECT f.mem_id, f.body, m.updated_at, m.type FROM memories m
		JOIN memory_fts f ON f.mem_id = m.id WHERE m.status='active'`+clause, pargs...)
	if err != nil {
		return nil, errf(api.ErrMemUnavailable, "", "scan: %v", err)
	}
	var all []scoredCand
	for rows.Next() {
		var c scoredCand
		var typ, updatedAt string
		if err := rows.Scan(&c.id, &c.body, &updatedAt, &typ); err != nil {
			rows.Close()
			return nil, err
		}
		c.updated = parseUpdatedAt(updatedAt)
		c.score = -freshness(typ, c.updated, now)
		all = append(all, c)
	}
	rows.Close()
	sortScored(all)
	out := make([]candRow, len(all))
	for i, c := range all {
		out[i] = c.candRow
	}
	return out, nil
}

// ftsQuery ANDs the tokens as quoted phrases for the trigram MATCH.
func ftsQuery(tokens []string) string { return ftsJoin(tokens, " AND ") }

// ftsJoin joins the tokens with op (" AND " / " OR ") as quoted phrases.
// Every token is an FTS5 string literal (embedded `"` doubled), so
// operator characters such as - : * ( ) never reach the FTS5 expression
// parser.
func ftsJoin(tokens []string, op string) string {
	parts := make([]string, len(tokens))
	for i, p := range tokens {
		parts[i] = `body:"` + strings.ReplaceAll(p, `"`, `""`) + `"`
	}
	return strings.Join(parts, op)
}

func containsAll(have, want []string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// Redact soft-deletes: the row turns redacted and its FTS row goes, in
// one Core transaction. The stored document stays in the row and is never
// served again.
func (s *Store) Redact(memID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRow(`SELECT status FROM memories WHERE id=?1 AND content IS NOT NULL`, memID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return errf(api.ErrMemNotFound, "", "memory %s", memID)
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return errf(api.ErrMemConflict, "", "memory is %s", status)
	}
	if _, err := tx.Exec(`UPDATE memories SET status='redacted', updated_at=?1 WHERE id=?2`, core.Now(), memID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE mem_id=?1`, memID); err != nil {
		return err
	}
	return tx.Commit()
}

// authorityRel is a row's authority_path, memory/<project>/<id>: where a
// database from before the content column kept the row's memory.md.
func authorityRel(projectID, memID string) string { return "memory/" + projectID + "/" + memID }

// ftsReplace enforces the delete-then-insert invariant: each mem_id has
// at most one retrievable row, consistent with the stored document.
func ftsReplace(tx *sql.Tx, memID, body string) error {
	if _, err := tx.Exec(`DELETE FROM memory_fts WHERE mem_id=?1`, memID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO memory_fts(mem_id, body) VALUES (?1, ?2)`, memID, body)
	return err
}

func anyPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func jsonOrEmpty(v []string) string {
	if v == nil {
		v = []string{}
	}
	b, _ := json.Marshal(v)
	return string(b)
}
