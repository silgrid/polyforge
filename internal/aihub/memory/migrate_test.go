package memory

// migrate_test.go: the one-time content migration from memory files into
// memories.content — the column added on a database that predates it,
// every file shape a pre-migration data dir holds (active, redacted, a
// file that drifted from content_sha, a redact interrupted after its file
// move, a missing file), and idempotency.

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyEntry writes a memory the way the file-authority store did: the
// document as memory.md under memory/<project>/[_redacted/]<id>/, the row
// without content, the FTS row from the document. It answers the
// document bytes.
func legacyEntry(t *testing.T, e *env, id, body, status string, redactedDir bool) string {
	t.Helper()
	var projectID string
	if err := e.db.QueryRow(`SELECT id FROM projects WHERE name='alpha'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	fm := &frontmatter{ID: id, Type: "experience.debug", OwnerUserID: e.user, Tags: []string{},
		Visibility: "project", ProjectID: projectID, Related: []string{},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z"}
	doc, err := buildDocument(fm, body)
	if err != nil {
		t.Fatal(err)
	}
	rel := "memory/" + projectID + "/" + id
	dir := filepath.Join(e.store.dataDir, "memory", projectID, id)
	if redactedDir {
		dir = filepath.Join(e.store.dataDir, "memory", projectID, "_redacted", id)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.md"), []byte(doc), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, title, visibility,
		authority_path, content_sha, content_len, status, created_at, updated_at)
		VALUES (?1,?2,?3,'experience.debug',?4,'project',?5,?6,?7,?8,?9,?9)`,
		id, projectID, e.user, anyPtr(deriveTitle(body)), rel, shaHex([]byte(doc)), len(doc), status, fm.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if status == "active" {
		if _, err := e.db.Exec(`INSERT INTO memory_fts(mem_id, body) VALUES (?1, ?2)`, id, extractBody(doc)); err != nil {
			t.Fatal(err)
		}
	}
	return doc
}

// legacyEnv is an env whose database predates memories.content.
func legacyEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	if _, err := e.db.Exec(`ALTER TABLE memories DROP COLUMN content`); err != nil {
		t.Fatal(err)
	}
	return e
}

func searchIDs(t *testing.T, e *env, q string) []string {
	t.Helper()
	hits, err := e.store.Search(q, "", nil, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return ids
}

func TestMigrateContentFromFiles(t *testing.T) {
	e := legacyEnv(t)
	activeDoc := legacyEntry(t, e, "mem_active", "# Retry ladder\nback off on quokka errors", "active", false)
	redactedDoc := legacyEntry(t, e, "mem_redacted", "a leaked secret wombat", "redacted", true)
	legacyEntry(t, e, "mem_drifted", "the old drifted platypus text", "active", false)
	legacyEntry(t, e, "mem_halfredacted", "half redacted echidna", "active", true)
	legacyEntry(t, e, "mem_missing", "a missing numbat file", "active", false)

	// The drifted entry's file was rewritten after its row (the file was
	// the authority); the missing entry's file is gone.
	var projectID string
	if err := e.db.QueryRow(`SELECT project_id FROM memories WHERE id='mem_drifted'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	driftedPath := filepath.Join(e.store.dataDir, "memory", projectID, "mem_drifted", "memory.md")
	drifted, err := os.ReadFile(driftedPath)
	if err != nil {
		t.Fatal(err)
	}
	newDrifted := strings.Replace(string(drifted), "the old drifted platypus text", "the corrected kiwi text", 1)
	if err := os.WriteFile(driftedPath, []byte(newDrifted), 0o640); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(e.store.dataDir, "memory", projectID, "mem_missing", "memory.md")
	if err := os.Remove(missingPath); err != nil {
		t.Fatal(err)
	}

	rep, err := e.store.MigrateContent()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Copied != 3 || strings.Join(rep.Adopted, ",") != "mem_drifted" ||
		strings.Join(rep.Redacted, ",") != "mem_halfredacted" || strings.Join(rep.Missing, ",") != "mem_missing" {
		t.Fatalf("report = %+v", rep)
	}

	// An active entry serves the file's exact bytes and keeps its digest.
	got, err := e.store.Content("mem_active")
	if err != nil || string(got) != activeDoc {
		t.Fatalf("active content = %q (%v), want the file bytes", got, err)
	}
	entry, err := e.store.Get("mem_active")
	if err != nil || entry.ContentSHA != shaHex([]byte(activeDoc)) || entry.ContentLen != int64(len(activeDoc)) {
		t.Fatalf("active entry = %+v (%v)", entry, err)
	}
	if Body(got) != "# Retry ladder\nback off on quokka errors" {
		t.Fatalf("active body = %q", Body(got))
	}
	if ids := searchIDs(t, e, "quokka"); strings.Join(ids, ",") != "mem_active" {
		t.Fatalf("search quokka = %v", ids)
	}

	// A redacted entry holds its document but is never served.
	var stored, status string
	if err := e.db.QueryRow(`SELECT content, status FROM memories WHERE id='mem_redacted'`).Scan(&stored, &status); err != nil {
		t.Fatal(err)
	}
	if stored != redactedDoc || status != "redacted" {
		t.Fatalf("redacted row: status %q content %q", status, stored)
	}
	if _, err := e.store.Content("mem_redacted"); err == nil {
		t.Fatal("redacted content served")
	}
	if ids := searchIDs(t, e, "wombat"); len(ids) != 0 {
		t.Fatalf("redacted entry searchable: %v", ids)
	}

	// The drifted entry adopts the file: digest, length and index follow.
	entry, err = e.store.Get("mem_drifted")
	if err != nil || entry.ContentSHA != shaHex([]byte(newDrifted)) || entry.ContentLen != int64(len(newDrifted)) {
		t.Fatalf("drifted entry = %+v (%v)", entry, err)
	}
	if ids := searchIDs(t, e, "kiwi"); strings.Join(ids, ",") != "mem_drifted" {
		t.Fatalf("search kiwi = %v", ids)
	}
	if ids := searchIDs(t, e, "platypus"); len(ids) != 0 {
		t.Fatalf("drifted entry still found by its old text: %v", ids)
	}

	// The interrupted redact is completed.
	if err := e.db.QueryRow(`SELECT status FROM memories WHERE id='mem_halfredacted'`).Scan(&status); err != nil || status != "redacted" {
		t.Fatalf("half-redacted row status %q (%v)", status, err)
	}
	if ids := searchIDs(t, e, "echidna"); len(ids) != 0 {
		t.Fatalf("half-redacted entry searchable: %v", ids)
	}

	// The missing entry is not served anywhere.
	if _, err := e.store.Get("mem_missing"); err == nil || err.(*APIError).Code != "MEM-404" {
		t.Fatalf("missing entry Get = %v, want MEM-404", err)
	}
	if _, err := e.store.Content("mem_missing"); err == nil {
		t.Fatal("missing entry content served")
	}
	if err := e.store.Redact("mem_missing"); err == nil || err.(*APIError).Code != "MEM-404" {
		t.Fatalf("missing entry Redact = %v, want MEM-404", err)
	}
	if ids := searchIDs(t, e, "numbat"); len(ids) != 0 {
		t.Fatalf("missing entry searchable: %v", ids)
	}

	// The files stay.
	for _, id := range []string{"mem_active", "mem_drifted"} {
		if _, err := os.Stat(filepath.Join(e.store.dataDir, "memory", projectID, id, "memory.md")); err != nil {
			t.Fatalf("%s file removed: %v", id, err)
		}
	}
}

func TestMigrateContentIdempotent(t *testing.T) {
	e := legacyEnv(t)
	legacyEntry(t, e, "mem_one", "first legacy body", "active", false)
	missingDoc := legacyEntry(t, e, "mem_two", "second legacy body", "active", false)
	var projectID string
	if err := e.db.QueryRow(`SELECT project_id FROM memories WHERE id='mem_two'`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	twoPath := filepath.Join(e.store.dataDir, "memory", projectID, "mem_two", "memory.md")
	if err := os.Remove(twoPath); err != nil {
		t.Fatal(err)
	}
	if rep, err := e.store.MigrateContent(); err != nil || rep.Copied != 1 || len(rep.Missing) != 1 {
		t.Fatalf("first pass = %+v (%v)", rep, err)
	}
	// A database created after the migration (new rows carry content).
	e.create(t, "a db-only body")

	var before, after int64
	if err := e.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	rep, err := e.store.MigrateContent()
	if err != nil || rep.Copied != 0 || len(rep.Adopted) != 0 || len(rep.Redacted) != 0 || len(rep.Missing) != 1 {
		t.Fatalf("second pass = %+v (%v)", rep, err)
	}
	if err := e.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("second pass wrote %d rows, want 0", after-before)
	}

	// A file restored later is picked up by the next pass.
	if err := os.WriteFile(twoPath, []byte(missingDoc), 0o640); err != nil {
		t.Fatal(err)
	}
	if rep, err := e.store.MigrateContent(); err != nil || rep.Copied != 1 || len(rep.Missing) != 0 {
		t.Fatalf("pass after the restore = %+v (%v)", rep, err)
	}
	if got, err := e.store.Content("mem_two"); err != nil || string(got) != missingDoc {
		t.Fatalf("restored content = %q (%v)", got, err)
	}
}

// TestCreateStoresInDBOnly: a create stores the document in the row and
// writes no file under memory/; an idempotent replay answers the original
// entry and body; a redact is one row update and a second one is MEM-409.
func TestCreateStoresInDBOnly(t *testing.T) {
	e := newEnv(t)
	key := "step-summary-1"
	body := "## Lesson\n\nthe step summary body\n---\nnot frontmatter\n"
	first, err := e.store.Create(CreateRequest{Project: "alpha", Type: "experience.wrap",
		Visibility: "project", Content: body, IdempotencyKey: &key}, e.user)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := e.store.Content(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if Body(doc) != body || first.ContentSHA != shaHex(doc) || first.ContentLen != int64(len(doc)) {
		t.Fatalf("stored document %q does not match the row (%+v)", doc, first)
	}
	if _, err := os.Stat(filepath.Join(e.store.dataDir, "memory")); !os.IsNotExist(err) {
		t.Fatalf("a create wrote under memory/ (%v), want the database only", err)
	}
	again, err := e.store.Create(CreateRequest{Project: "alpha", Type: "experience.wrap",
		Visibility: "project", Content: "a different replayed body", IdempotencyKey: &key}, e.user)
	if err != nil || again.ID != first.ID || again.ContentSHA != first.ContentSHA {
		t.Fatalf("replay = %+v (%v), want the original", again, err)
	}
	if replayed, err := e.store.Content(again.ID); err != nil || Body(replayed) != body {
		t.Fatalf("replayed body = %q (%v), want the original", Body(replayed), err)
	}
	if err := e.store.Redact(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Content(first.ID); err == nil {
		t.Fatal("redacted entry served")
	}
	if _, err := os.Stat(filepath.Join(e.store.dataDir, "memory")); !os.IsNotExist(err) {
		t.Fatalf("a redact wrote under memory/ (%v), want the database only", err)
	}
	if err := e.store.Redact(first.ID); err == nil || err.(*APIError).Code != "MEM-409" {
		t.Fatalf("second redact = %v, want MEM-409", err)
	}
}

// TestMigrateFillsBlankTitles: blank titles of active rows are derived
// from the stored document — rows filled by this pass and rows that
// already had content alike; an explicit frontmatter title wins; redacted
// rows keep none; a second pass writes nothing.
func TestMigrateFillsBlankTitles(t *testing.T) {
	e := legacyEnv(t)
	legacyEntry(t, e, "mem_legacy", "- legacy lesson one\nmore", "active", false)
	legacyEntry(t, e, "mem_legacyred", "# redacted heading", "redacted", true)
	if _, err := e.db.Exec(`UPDATE memories SET title=NULL`); err != nil {
		t.Fatal(err)
	}
	if rep, err := e.store.MigrateContent(); err != nil || rep.Titled != 1 {
		t.Fatalf("first pass = %+v (%v), want one title", rep, err)
	}
	filled := e.create(t, "### 遗留记忆二\n正文")
	explicit, err := e.store.Create(CreateRequest{Project: "alpha", Type: "fact.note", Visibility: "project",
		Title: sp("Keep me"), Content: "# not this"}, e.user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE memories SET title='  ' WHERE id IN (?1, ?2)`, filled.ID, explicit.ID); err != nil {
		t.Fatal(err)
	}
	if rep, err := e.store.MigrateContent(); err != nil || rep.Titled != 2 {
		t.Fatalf("second pass = %+v (%v), want two titles", rep, err)
	}
	want := map[string]string{"mem_legacy": "legacy lesson one", filled.ID: "遗留记忆二", explicit.ID: "Keep me"}
	for id, w := range want {
		got, err := e.store.Get(id)
		if err != nil || deref(got.Title) != w {
			t.Fatalf("%s title = %v (%v), want %q", id, deref(got.Title), err, w)
		}
	}
	var red sql.NullString
	if err := e.db.QueryRow(`SELECT title FROM memories WHERE id='mem_legacyred'`).Scan(&red); err != nil || red.Valid {
		t.Fatalf("redacted row title = %v (%v), want none", red, err)
	}
	var before, after int64
	e.db.QueryRow(`SELECT total_changes()`).Scan(&before)
	if rep, err := e.store.MigrateContent(); err != nil || rep.Titled != 0 {
		t.Fatalf("third pass = %+v (%v)", rep, err)
	}
	e.db.QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before {
		t.Fatalf("third pass wrote %d rows", after-before)
	}
}
