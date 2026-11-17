package core

// carries: deploy/sql/v0.2.3/upgrade.sql, deploy/sql/v0.2.3/probe.sql, deploy/sql/v0.2.3/rollback.sql, internal/aihub/core/store.go, internal/aihub/core/upgrade_test.go, internal/aihub/core/upgrade_v023_test.go

// upgrade_v023_test.go — the deploy/sql/v0.2.3 window SQL (the memory
// writer columns, polyforge#107). The fixture is a real database of the
// v0.2.2 shape: the pre-#149 DDL with every earlier deploy/sql version
// applied (the loop breaks BEFORE v0.2.3, the same discipline
// upgrade_v021_test.go keeps its fixture below its own release) plus the
// memories.content column the v0.2.2 binary's startup content migration
// adds, populated with the memory rows the backfill must and must not
// touch. Pinned: a fresh database carries both columns (the DDL installs
// them), the probe's run-once answers (nonzero on the v0.2.2 shape, 0
// after the upgrade — and still 0 after the no-op rollback, because the
// columns stay), that the upgrade adds the two columns and rewrites no
// pre-existing row beyond them, that the backfill marks only
// pf-step-memory:-keyed rows as step with the details parsed from the
// key, that the v0.2.3 read shape (the users JOIN + the two new columns)
// fails on the un-migrated shape and works on the migrated one, that the
// v0.2.2 write and read shapes keep working on the migrated database
// (the old binary lists its columns by name), and that the rollback
// keeps the columns (owner ruling, polyforge#107 Q7: the writer
// attribution is durable data, never dropped).

import (
	"database/sql"
	"testing"
)

// w107SQLVer is the release directory carrying polyforge#107's window
// SQL. It deliberately breaks the fixture loop before its own upgrade:
// at the red commit sqlVers does not know it yet, and once it does the
// fixture keeps its v0.2.2 shape either way.
const w107SQLVer = "v0.2.3"

// w107V022ShapeDB answers a populated database in the v0.2.2 shape
// (data dir + raw handle): every deploy/sql version before v0.2.3
// applied, the memories.content column the v0.2.2 binary's startup
// content migration adds, and the three seeded memory rows the backfill
// must sort — one keyed pf-step-memory: (the daemon's step write, its
// key carrying the work item, step and attempt), one keyed anything
// else, one with no key at all.
func w107V022ShapeDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, true)
	for _, v := range sqlVers {
		if v == w107SQLVer {
			break
		}
		applySQLText(t, raw, loadSQLFileOf(t, v, "upgrade.sql"))
	}
	// The content column the v0.2.2 binary serves with (memory/migrate.go
	// adds it on start to a database from before it).
	applySQLText(t, raw, `ALTER TABLE memories ADD COLUMN content TEXT`)
	if rawColumnExists(t, raw, "memories", "writer_kind") {
		t.Fatal("fixture: the v0.2.2 shape must not carry memories.writer_kind")
	}
	now := Now()
	for _, row := range []struct{ id, key sql.NullString }{
		{sql.NullString{String: "mem_w107_step", Valid: true},
			sql.NullString{String: "pf-step-memory:wi_olda:build:2", Valid: true}},
		{sql.NullString{String: "mem_w107_plain", Valid: true},
			sql.NullString{String: "session-idem-key", Valid: true}},
		{sql.NullString{String: "mem_w107_bare", Valid: true},
			sql.NullString{}},
	} {
		if _, err := raw.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, visibility, authority_path,
			content_sha, content_len, status, created_at, updated_at, content, idempotency_key)
			VALUES(?1,'p-old','u-old','experience.wrap','project','memory/p-old/'||?1,?2,3,'active',?3,?3,'doc '||?1,?4)`,
			row.id.String, "sha_"+row.id.String, now, row.key); err != nil {
			t.Fatalf("fixture: seed memory %s: %v", row.id.String, err)
		}
	}
	return dataDir, raw
}

// w107ProbeValue executes deploy/sql/v0.2.3/probe.sql's final statement
// and answers its scalar (the deploy.sh contract: 0 = already applied,
// nonzero = apply).
func w107ProbeValue(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, w107SQLVer, "probe.sql"))
	if len(stmts) == 0 {
		t.Fatal("probe.sql carries no statements")
	}
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v\n%s", err, stmts[len(stmts)-1])
	}
	return v
}

// w107MemDump answers every memories row's identity tuple — every column
// except the two the migration adds — the values the migration must not
// move, one line per row.
func w107MemDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT id || ':' || project_id || ':' || owner_user_id || ':' ||
		COALESCE(work_item_id,'') || ':' || type || ':' || COALESCE(title,'') || ':' || visibility || ':' ||
		authority_path || ':' || content_sha || ':' || content_len || ':' || tags || ':' || related || ':' ||
		COALESCE(idempotency_key,'') || ':' || status || ':' || created_at || ':' || updated_at || ':' ||
		COALESCE(content,'')
		FROM memories ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := ""
	for rows.Next() {
		var one string
		if err := rows.Scan(&one); err != nil {
			t.Fatal(err)
		}
		out += one + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// w107Writer answers one row's writer columns as (kind, details).
func w107Writer(t *testing.T, db *sql.DB, id string) (sql.NullString, sql.NullString) {
	t.Helper()
	var kind, details sql.NullString
	if err := db.QueryRow(`SELECT writer_kind, writer_details FROM memories WHERE id=?1`, id).
		Scan(&kind, &details); err != nil {
		t.Fatalf("read %s writer: %v", id, err)
	}
	return kind, details
}

// TestW107Upgrade — AC4: the DDL installs the two writer columns on a
// fresh database; on the v0.2.2 shape the probe decides the run-once,
// the upgrade adds the columns without touching a pre-existing row, the
// backfill marks only the pf-step-memory:-keyed row (its details parsed
// from the key), the current read shape needs the SQL and works with it,
// the v0.2.2 write/read shapes keep working on the migrated database,
// and the rollback keeps the columns (the writer attribution is durable).
func TestW107Upgrade(t *testing.T) {
	// A fresh database carries both columns: the DDL installs them, so
	// only a database from before v0.2.3 needs the window SQL.
	fresh, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	if !rawColumnExists(t, fresh, "memories", "writer_kind") ||
		!rawColumnExists(t, fresh, "memories", "writer_details") {
		t.Fatal("a fresh database lacks the writer columns — the DDL must install writer_kind and writer_details")
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	dataDir, raw := w107V022ShapeDB(t)

	// The probe: apply on the v0.2.2 shape.
	if v := w107ProbeValue(t, raw); v == 0 {
		t.Fatal("probe answers 0 (already applied) on a v0.2.2-shape database")
	}
	before := w107MemDump(t, raw)

	// The current read shape — the store's load/browse SELECT joins the
	// users display name and reads both writer columns — fails on the
	// un-migrated shape (no auto migration is the contract; the SQL is
	// mandatory before the new server starts). The memory store imports
	// core, so a core test pins the statement shape, not the call.
	if _, err := raw.Exec(`SELECT m.id, u.display_name, m.writer_kind, m.writer_details
		FROM memories m JOIN users u ON u.id = m.owner_user_id
		WHERE m.id='mem_w107_step' AND m.content IS NOT NULL`); err == nil {
		t.Fatal("the v0.2.3 memory read shape works on an un-migrated v0.2.2-shape database; the v0.2.3 SQL is mandatory before the new server starts")
	}

	// The upgrade: both columns land and no pre-existing row changes
	// beyond them.
	applySQLText(t, raw, loadSQLFileOf(t, w107SQLVer, "upgrade.sql"))
	if !rawColumnExists(t, raw, "memories", "writer_kind") ||
		!rawColumnExists(t, raw, "memories", "writer_details") {
		t.Fatal("upgrade did not add the writer columns")
	}
	if after := w107MemDump(t, raw); after != before {
		t.Errorf("memories rows changed across the upgrade beyond the writer columns:\nbefore:\n%safter:\n%s", before, after)
	}

	// The backfill: only the pf-step-memory:-keyed row is a step write,
	// its details parsed from the key (wi / step / attempt); the other
	// two rows stay NULL on both columns.
	kind, details := w107Writer(t, raw, "mem_w107_step")
	if !kind.Valid || kind.String != "step" {
		t.Fatalf("the pf-step-memory:-keyed row's writer_kind = %v, want step", kind)
	}
	if !details.Valid || details.String != `{"work_item":"wi_olda","step":"build","attempt":2}` {
		t.Fatalf("the pf-step-memory:-keyed row's writer_details = %q, want the wi/step/attempt parsed from the key",
			details.String)
	}
	for _, id := range []string{"mem_w107_plain", "mem_w107_bare"} {
		kind, details = w107Writer(t, raw, id)
		if kind.Valid || details.Valid {
			t.Fatalf("%s gained a writer across the backfill (%q, %q) — only pf-step-memory:-keyed rows are touched",
				id, kind.String, details.String)
		}
	}

	// The probe: skip after the upgrade (the run-once guard), and the
	// upgrade SQL itself is not re-runnable — a duplicate column fails.
	if v := w107ProbeValue(t, raw); v != 0 {
		t.Fatalf("probe answers %d (apply) after the upgrade — a re-deploy would run the SQL twice", v)
	}
	if _, err := raw.Exec(`ALTER TABLE memories ADD COLUMN writer_kind TEXT`); err == nil {
		t.Fatal("re-adding the column must fail — the probe, not the SQL, carries the run-once guarantee")
	}

	// The current read shape works on the migrated database, and the
	// v0.2.2 binary's shapes keep working beside it: its INSERT and
	// SELECT name their columns, so the two added nullable columns are
	// no obstacle (the old-binary pin of Q7).
	if _, err := raw.Exec(`SELECT m.id, u.display_name, m.writer_kind, m.writer_details
		FROM memories m JOIN users u ON u.id = m.owner_user_id
		WHERE m.id='mem_w107_step' AND m.content IS NOT NULL`); err != nil {
		t.Fatalf("the v0.2.3 memory read shape fails on the migrated database: %v", err)
	}
	now := Now()
	if _, err := raw.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, visibility, authority_path,
		content_sha, content_len, status, created_at, updated_at, content)
		VALUES('mem_w107_oldwrite','p-old','u-old','fact.note','project','memory/p-old/mem_w107_oldwrite',
		'sha_oldwrite',4,'active',?1,?1,'doc')`, now); err != nil {
		t.Fatalf("the v0.2.2 write shape failed on the migrated database: %v", err)
	}
	if _, err := raw.Exec(`SELECT id, project_id, owner_user_id, work_item_id, type, title, visibility,
		content_sha, content_len, tags, related, status, created_at, updated_at FROM memories WHERE id='mem_w107_step'`); err != nil {
		t.Fatalf("the v0.2.2 read shape failed on the migrated database: %v", err)
	}

	// The rollback: a no-op by decision (Q7) — the columns stay, the
	// probe still answers applied (a re-deploy skips the SQL, correctly:
	// the columns are there), and the old write shape still works.
	applySQLText(t, raw, loadSQLFileOf(t, w107SQLVer, "rollback.sql"))
	if !rawColumnExists(t, raw, "memories", "writer_kind") ||
		!rawColumnExists(t, raw, "memories", "writer_details") {
		t.Fatal("the rollback dropped the writer columns — the writer attribution is durable data (Q7)")
	}
	if v := w107ProbeValue(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the rollback — the columns are still present, so a re-deploy must skip the SQL", v)
	}
	kind, details = w107Writer(t, raw, "mem_w107_step")
	if !kind.Valid || kind.String != "step" || !details.Valid {
		t.Fatal("the rollback lost the backfilled writer attribution")
	}
	if _, err := raw.Exec(`INSERT INTO memories(id, project_id, owner_user_id, type, visibility, authority_path,
		content_sha, content_len, status, created_at, updated_at, content)
		VALUES('mem_w107_oldwrite2','p-old','u-old','fact.note','project','memory/p-old/mem_w107_oldwrite2',
		'sha_oldwrite2',5,'active',?1,?1,'doc2')`, now); err != nil {
		t.Fatalf("the v0.2.2 write shape failed after the rollback: %v", err)
	}
}
