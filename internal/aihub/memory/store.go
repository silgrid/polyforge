package memory

// Store: the memories rows (the stored document lives in the content
// column), FTS5-trigram search (with the short-query widening for <3-rune
// tokens, kept from v0: trigram MATCH cannot answer those) and redact.

import (
	"container/heap"
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
	// Client frontmatter is allowed to seed fields, but never the id.
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
// allowed filters candidates (membership/visibility, applied by the
// server layer).
func (s *Store) Search(q string, memType string, tags []string, limit int, allowed func(*Entry) bool) ([]Entry, error) {
	return s.SearchMode(MatchAll, "", q, memType, tags, limit, allowed)
}

// SearchMode is the search face's single entry: match picks the mode
// (MatchAll / "" — Search's every-token AND; MatchAny — SearchAny's
// ranked any-term OR; anything else is MEM-400). project, when
// non-empty, is a project id narrowing the candidates INSIDE the FTS /
// scan SQL — a pure narrowing (the caller's allowed predicate still
// enforces membership, visibility and the same project), so the answer
// is identical to filtering afterwards, minus the per-candidate load
// of every other project's matches.
func (s *Store) SearchMode(match, project, q, memType string, tags []string, limit int, allowed func(*Entry) bool) ([]Entry, error) {
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
	f := searchFilter{project: project, memType: memType, tags: tags, limit: limit, allowed: allowed,
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
	project string // project id narrowing the SQL candidates; "" = all
	memType string
	tags    []string
	limit   int
	allowed func(*Entry) bool
	now     time.Time // the query time the freshness term ages against
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
		scanned, serr := s.scanBodies(f.project, f.now)
		if serr != nil {
			return nil, serr
		}
		take(scanned)
		return out, nil
	}
	// A bounded search examines one best-ranked window and stops there.
	if maxCands > 0 {
		cands, err := s.ftsPage(match, f.project, f.now, maxCands, 0)
		if err != nil {
			return nil, err
		}
		take(cands)
		return out, nil
	}
	// FTS candidates are paged in rank order until limit entries survive
	// the filters or the matches run out: the type/tag/membership/project
	// filters run after the MATCH, so one fixed-size candidate window
	// could silently answer fewer than limit hits while more exist.
	batch := limit * 4
	for offset := 0; ; offset += batch {
		cands, err := s.ftsPage(match, f.project, f.now, batch, offset)
		if err != nil {
			return nil, err
		}
		if take(cands) || len(cands) < batch {
			return out, nil
		}
	}
}

// ftsPage reads one page of FTS candidates in freshness-weighted rank
// order (freshness.go; now is the query time). project, when non-empty,
// keeps only that project's rows.
//
// Cost shape: FTS5 answers every match's rowid and bm25 rank from its
// index alone, but reaching an entry's mem_id — and so its memories row
// — reads the entry's stored FTS row, and re-reading rank costs a second
// bm25 evaluation. So the ranks are read once for every match, and
// stored rows are read only down the rank order as far as the page can
// still change: the weighted score is bounded below by
// (1+freshnessBoost) × rank, so once offset+limit candidates are held
// and the next match's bound is worse than the worst of them, no later
// match can enter the page. Bodies are read for the page alone. The
// answer is the exact weighted order.
func (s *Store) ftsPage(match, project string, now time.Time, limit, offset int) ([]candRow, error) {
	type ranked struct {
		rowid int64
		rank  float64
	}
	rows, err := s.db.Query(`SELECT rowid, rank FROM memory_fts WHERE memory_fts MATCH ?1`, match)
	if err != nil {
		// A typed refusal, never a degrade to a full scan: answering
		// every memory for a query FTS could not parse is a leak.
		return nil, errf(api.ErrMemUnavailable, "", "fts: %v", err)
	}
	var matches []ranked
	for rows.Next() {
		var r ranked
		if err := rows.Scan(&r.rowid, &r.rank); err != nil {
			rows.Close()
			return nil, err
		}
		matches = append(matches, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].rowid < matches[j].rowid
	})
	want := offset + limit
	var all []scoredCand
	best := &worstFirst{} // the want best scores so far, worst on top
	bounded := func(rank float64) bool {
		return best.Len() == want && (1+freshnessBoost)*rank > (*best)[0]
	}
	// The first batch is the page's own size; later ones are smaller so
	// the stop overshoots the bound by few stored-row reads.
	step := want
	for i := 0; i < len(matches) && !bounded(matches[i].rank); step = max(16, want/4) {
		batch := matches[i:min(i+step, len(matches))]
		i += len(batch)
		keys := make([]int64, len(batch))
		for k, r := range batch {
			keys[k] = r.rowid
		}
		meta, err := s.ftsMeta(keys, project)
		if err != nil {
			return nil, err
		}
		for _, r := range batch {
			if bounded(r.rank) {
				break
			}
			c, ok := meta[r.rowid]
			if !ok {
				continue // inactive or another project's
			}
			c.score = r.rank * (1 + freshnessBoost*freshness(c.typ, c.updated, now))
			all = append(all, c)
			if best.Len() < want {
				heap.Push(best, c.score)
			} else if c.score < (*best)[0] {
				(*best)[0] = c.score
				heap.Fix(best, 0)
			}
		}
	}
	sortScored(all)
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
	// An entry redacted since its meta was read keeps its slot with an
	// empty body (the page length is collect's end-of-matches signal);
	// collect's status check drops it.
	out := make([]candRow, len(all))
	for i, c := range all {
		out[i] = candRow{c.id, bodies[c.rowid]}
	}
	return out, nil
}

// ftsMeta answers, per FTS rowid, the active entry's id, type and
// updated_at (project, when non-empty, keeps only that project's rows).
func (s *Store) ftsMeta(keys []int64, project string) (map[int64]scoredCand, error) {
	rows, err := s.db.Query(`SELECT f.rowid, m.id, m.type, m.updated_at
		FROM memory_fts f JOIN memories m ON m.id = f.mem_id
		WHERE f.rowid IN (`+rowidList(keys)+`) AND m.status='active'
		AND (?1 = '' OR m.project_id = ?1)`, project)
	if err != nil {
		return nil, errf(api.ErrMemUnavailable, "", "fts: %v", err)
	}
	defer rows.Close()
	out := make(map[int64]scoredCand, len(keys))
	for rows.Next() {
		var c scoredCand
		var updatedAt string
		if err := rows.Scan(&c.rowid, &c.id, &c.typ, &updatedAt); err != nil {
			return nil, err
		}
		c.updated = parseUpdatedAt(updatedAt)
		out[c.rowid] = c
	}
	return out, rows.Err()
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

// worstFirst is a max-heap of scores: the worst (largest) on top.
type worstFirst []float64

func (h worstFirst) Len() int           { return len(h) }
func (h worstFirst) Less(i, j int) bool { return h[i] > h[j] }
func (h worstFirst) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *worstFirst) Push(x any)        { *h = append(*h, x.(float64)) }
func (h *worstFirst) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
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
// freshness.go); project, when non-empty, keeps only that project's rows.
func (s *Store) scanBodies(project string, now time.Time) ([]candRow, error) {
	rows, err := s.db.Query(`SELECT f.mem_id, f.body, m.updated_at, m.type FROM memories m
		JOIN memory_fts f ON f.mem_id = m.id WHERE m.status='active'
		AND (?1 = '' OR m.project_id = ?1)`, project)
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
