package memory

// migrate.go: the one-time move of memory content from the files under
// <data-dir>/memory/ into the memories.content column. A database written
// before the column existed carries each memory's document only as
// memory/<project>/<id>/memory.md (memory/<project>/_redacted/<id>/memory.md
// once redacted); the migration copies those bytes into the row. The
// files are left in place. The same pass gives blank-titled active rows
// the title their document derives (rowTitle).

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/silgrid/polyforge/internal/aihub/core"
)

// MigrateReport is one migration pass's outcome.
type MigrateReport struct {
	// Copied counts rows filled from a file whose digest equals the row's
	// content_sha.
	Copied int
	// Adopted lists rows filled from a file whose digest differs from
	// content_sha: the file was the authority, so its bytes are taken and
	// the row's content_sha/content_len (and an active row's FTS body) are
	// recomputed from them.
	Adopted []string
	// Redacted lists active rows whose file was found only under
	// _redacted/ (a redact interrupted between its file move and its row
	// update): the redact is completed.
	Redacted []string
	// Missing lists rows left without content (no readable file); they
	// are not served and the next pass retries them.
	Missing []string
	// Titled counts active rows given a derived title.
	Titled  int
	Elapsed time.Duration
}

// migrateBatchBytes bounds the document bytes one migration transaction
// writes.
const migrateBatchBytes = 16 << 20

// MigrateContent adds the content column when the database predates it,
// fills every row whose content is NULL from its memory file, then fills
// blank titles. It is idempotent: a filled row
// is never selected again, so a pass over a migrated database reads no
// memory file and writes nothing. Run it before serving (no create is in
// flight, and a row without content answers MEM-404).
func (s *Store) MigrateContent() (MigrateReport, error) {
	start := time.Now()
	var rep MigrateReport
	var has int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('memories') WHERE name='content'`).Scan(&has); err != nil {
		return rep, err
	}
	if has == 0 {
		if _, err := s.db.Exec(`ALTER TABLE memories ADD COLUMN content TEXT`); err != nil {
			return rep, err
		}
	}
	type cand struct{ id, status, apath, sha string }
	rows, err := s.db.Query(`SELECT id, status, authority_path, content_sha FROM memories
		WHERE content IS NULL ORDER BY id`)
	if err != nil {
		return rep, err
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.status, &c.apath, &c.sha); err != nil {
			rows.Close()
			return rep, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	type fill struct {
		cand
		doc     string
		adopted bool
		redact  bool
	}
	var batch []fill
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, f := range batch {
			if _, err := tx.Exec(`UPDATE memories SET content=?1, content_sha=?2, content_len=?3
				WHERE id=?4 AND content IS NULL`, f.doc, shaHex([]byte(f.doc)), len(f.doc), f.id); err != nil {
				return err
			}
			switch {
			case f.redact:
				if _, err := tx.Exec(`UPDATE memories SET status='redacted', updated_at=?1 WHERE id=?2`, core.Now(), f.id); err != nil {
					return err
				}
				if _, err := tx.Exec(`DELETE FROM memory_fts WHERE mem_id=?1`, f.id); err != nil {
					return err
				}
			case f.adopted && f.status == "active":
				if err := ftsReplace(tx, f.id, extractBody(f.doc)); err != nil {
					return err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		batch, size = batch[:0], 0
		return nil
	}
	for _, c := range cands {
		raw, redacted, ok := s.readMemoryFile(c.apath, c.status)
		if !ok {
			rep.Missing = append(rep.Missing, c.id)
			continue
		}
		f := fill{cand: c, doc: string(raw), adopted: shaHex(raw) != c.sha,
			redact: redacted && c.status == "active"}
		if f.redact {
			rep.Redacted = append(rep.Redacted, c.id)
		}
		if f.adopted {
			rep.Adopted = append(rep.Adopted, c.id)
		} else {
			rep.Copied++
		}
		batch = append(batch, f)
		if size += len(raw); size >= migrateBatchBytes {
			if err := flush(); err != nil {
				return rep, err
			}
		}
	}
	if err := flush(); err != nil {
		return rep, err
	}
	if rep.Titled, err = s.fillBlankTitles(); err != nil {
		return rep, err
	}
	rep.Elapsed = time.Since(start)
	return rep, nil
}

// fillBlankTitles gives every active row with content and a blank title
// the title its document derives: the frontmatter title when it has
// content, else the body's first usable line (rowTitle, as Create does).
// A row whose document derives none stays blank and is reconsidered on
// the next pass; the guarded UPDATE writes nothing twice.
func (s *Store) fillBlankTitles() (int, error) {
	rows, err := s.db.Query(`SELECT id FROM memories WHERE status='active'
		AND content IS NOT NULL AND (title IS NULL OR trim(title) = '')`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for i := 0; i < len(ids); i += 256 {
		tx, err := s.db.Begin()
		if err != nil {
			return n, err
		}
		for _, id := range ids[i:min(i+256, len(ids))] {
			var doc string
			if err := tx.QueryRow(`SELECT content FROM memories WHERE id=?1`, id).Scan(&doc); err != nil {
				tx.Rollback()
				return n, err
			}
			var fm frontmatter
			block, body, ok := splitFrontmatter(doc)
			if ok && yaml.Unmarshal([]byte(block), &fm) != nil {
				continue
			}
			t := rowTitle(fm.Title, body)
			if t == nil {
				continue
			}
			if _, err := tx.Exec(`UPDATE memories SET title=?1 WHERE id=?2
				AND (title IS NULL OR trim(title) = '')`, *t, id); err != nil {
				tx.Rollback()
				return n, err
			}
			n++
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// readMemoryFile reads a row's memory file and reports whether it was
// found under _redacted/: memory/<project>/<id> holds an active entry,
// memory/<project>/_redacted/<id> a redacted one. The place matching the
// row's status is tried first, the other second.
func (s *Store) readMemoryFile(apath, status string) (raw []byte, redacted, ok bool) {
	apath = filepath.ToSlash(apath)
	i := strings.LastIndexByte(apath, '/')
	if i < 0 || !filepath.IsLocal(apath) {
		return nil, false, false
	}
	order := []bool{false, true}
	if status != "active" {
		order = []bool{true, false}
	}
	for _, red := range order {
		p := apath
		if red {
			p = apath[:i] + "/_redacted/" + apath[i+1:]
		}
		if raw, err := os.ReadFile(filepath.Join(s.dataDir, filepath.FromSlash(p), "memory.md")); err == nil {
			return raw, red, true
		}
	}
	return nil, false, false
}
