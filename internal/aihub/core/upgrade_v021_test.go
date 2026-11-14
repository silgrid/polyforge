package core

// upgrade_v021_test.go — the deploy/sql/v0.2.1 window SQL (the project
// deactivation status column, polyforge#30). The fixture is a real
// database of the FIRST PUBLIC SHAPE (v0.2.0): the pre-#149 DDL with
// every earlier deploy/sql version applied explicitly (the loop breaks
// BEFORE v0.2.1, the same discipline upgrade_v2208_test.go keeps its
// fixture below its own release), populated with projects, work items
// and memberships the migration must not touch. Pinned: the probe's
// run-once answers (nonzero on the v0.2.0 shape, 0 after the upgrade,
// nonzero again after the rollback), that the upgrade adds
// projects.status and leaves EVERY pre-existing row 'active' with no
// row lost or rewritten (AC1), that the current code's project paths
// FAIL on the un-migrated v0.2.0 shape and succeed on the migrated one,
// and that the rollback restores the v0.2.0 binary's write shape.

import (
	"database/sql"
	"testing"
)

// w30SQLVer is the release directory carrying polyforge#30's window SQL.
// It deliberately breaks the fixture loop before its own upgrade: at the
// red commit sqlVers does not know it yet, and once it does the fixture
// keeps its v0.2.0 shape either way.
const w30SQLVer = "v0.2.1"

// w30V020ShapeDB answers a populated database in the first public release's
// shape (data dir + raw handle): the pre-#149 DDL, every deploy/sql
// version before v0.2.1 applied (so the messaging cursors are split and
// machines.user_id is gone), and the rows the migration must preserve —
// two projects, their work items and memberships — seeded through raw SQL.
func w30V020ShapeDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, true)
	for _, v := range sqlVers {
		if v == w30SQLVer {
			break
		}
		applySQLText(t, raw, loadSQLFileOf(t, v, "upgrade.sql"))
	}
	if rawColumnExists(t, raw, "projects", "status") {
		t.Fatal("fixture: the v0.2.0 shape must not carry projects.status")
	}
	// A second project with a member and a work item: the rows the
	// migration must leave byte-for-byte (beyond the new column).
	now := Now()
	for _, stmt := range []string{
		`INSERT INTO projects(id,name,description,repos,created_at,updated_at)
			VALUES('p-w30','w30-keeper','kept','[]','` + now + `','` + now + `')`,
		`INSERT INTO project_members(project_id,user_id,role,created_at)
			VALUES('p-w30','u-old','maintainer','` + now + `')`,
		`INSERT INTO work_items(id,seq,project_id,goal,reporter,bundle_name,created_at,updated_at)
			VALUES('wi_w30',1,'p-w30','w30 keeper row','u-old','demo-bundle','` + now + `','` + now + `')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("fixture: populate (%s): %v", stmt, err)
		}
	}
	return dataDir, raw
}

// w30ProbeValue executes deploy/sql/v0.2.1/probe.sql's final statement and
// answers its scalar: 0 = already applied (skip), nonzero = apply — the
// deploy.sh contract, the same one probeValue pins for v2.4.0.
func w30ProbeValue(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, w30SQLVer, "probe.sql"))
	if len(stmts) == 0 {
		t.Fatal("probe.sql carries no statements")
	}
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v\n%s", err, stmts[len(stmts)-1])
	}
	return v
}

// w30Counts answers the preservation evidence: per-table row counts and
// the projects' identity tuples, the values the migration must not move.
func w30Counts(t *testing.T, db *sql.DB) (map[string]int, string) {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"projects", "work_items", "project_members", "users"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	var ids string
	rows, err := db.Query(`SELECT id || ':' || name || ':' || description || ':' || created_at
		FROM projects ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var one string
		if err := rows.Scan(&one); err != nil {
			t.Fatal(err)
		}
		ids += one + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out, ids
}

// TestW30UpgradeV021ProjectsStatus — AC1: the probe decides the run-once;
// the upgrade adds projects.status with every existing row 'active' and
// loses nothing; the current code needs the SQL (its project paths fail
// on the un-migrated shape) and works on the migrated one; the rollback
// restores the v0.2.0 binary's write shape and the probe's apply answer.
func TestW30UpgradeV021ProjectsStatus(t *testing.T) {
	dataDir, raw := w30V020ShapeDB(t)

	// The probe: apply on the v0.2.0 shape.
	if v := w30ProbeValue(t, raw); v == 0 {
		t.Fatal("probe answers 0 (already applied) on a v0.2.0-shape database")
	}
	beforeCounts, beforeProjects := w30Counts(t, raw)

	// The current code on the UN-migrated shape: the project read must
	// fail (no auto migration is the contract; the SQL is mandatory).
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("open un-migrated: %v", err)
	}
	if _, err := NewAdminManager(db).GetProject("legacy"); err == nil {
		t.Fatal("GetProject succeeded on an un-migrated v0.2.0-shape database; the v0.2.1 SQL is mandatory before the new server starts")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err = sql.Open("sqlite", "file:"+dataDir+"/aihub.db")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)

	// The upgrade: status lands, every existing row 'active', nothing lost.
	applySQLText(t, raw, loadSQLFileOf(t, w30SQLVer, "upgrade.sql"))
	if !rawColumnExists(t, raw, "projects", "status") {
		t.Fatal("upgrade did not add projects.status")
	}
	var bad int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM projects WHERE status <> 'active'`).Scan(&bad); err != nil {
		t.Fatalf("read statuses: %v", err)
	}
	if bad != 0 {
		t.Fatalf("%d pre-existing project(s) are not 'active' after the upgrade", bad)
	}
	afterCounts, afterProjects := w30Counts(t, raw)
	for table, n := range beforeCounts {
		if afterCounts[table] != n {
			t.Errorf("table %s: %d rows before the upgrade, %d after — the migration must not lose or add rows", table, n, afterCounts[table])
		}
	}
	if afterProjects != beforeProjects {
		t.Errorf("projects identity changed across the upgrade:\nbefore:\n%safter:\n%s", beforeProjects, afterProjects)
	}
	// The probe: skip after the upgrade (the run-once guard), and the
	// upgrade SQL itself is not re-runnable — a duplicate column fails.
	if v := w30ProbeValue(t, raw); v != 0 {
		t.Fatalf("probe answers %d (apply) after the upgrade — a re-deploy would run the SQL twice", v)
	}
	if _, err := raw.Exec(`ALTER TABLE projects ADD COLUMN status TEXT`); err == nil {
		t.Fatal("re-adding the column must fail — the probe, not the SQL, carries the run-once guarantee")
	}

	// The current code on the migrated shape: reads and writes work and
	// every project answers status 'active'.
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dataDir)
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	defer db.Close()
	admin := NewAdminManager(db)
	for _, name := range []string{"legacy", "w30-keeper"} {
		p, err := admin.GetProject(name)
		if err != nil {
			t.Fatalf("post-upgrade GetProject(%s): %v", name, err)
		}
		if p.Status != "active" {
			t.Fatalf("post-upgrade GetProject(%s).Status = %q, want active", name, p.Status)
		}
	}
	created, err := admin.CreateProject(CreateProjectRequest{Name: "w30-fresh"})
	if err != nil {
		t.Fatalf("post-upgrade CreateProject: %v", err)
	}
	if created.Status != "active" {
		t.Fatalf("post-upgrade CreateProject().Status = %q, want active", created.Status)
	}

	// The rollback: the v0.2.0 shape back, the probe allows a retry, and
	// the old binary's write shape (no status column, no default needed)
	// works again.
	raw2, err := sql.Open("sqlite", "file:"+dataDir+"/aihub.db")
	if err != nil {
		t.Fatal(err)
	}
	defer raw2.Close()
	raw2.SetMaxOpenConns(1)
	applySQLText(t, raw2, loadSQLFileOf(t, w30SQLVer, "rollback.sql"))
	if rawColumnExists(t, raw2, "projects", "status") {
		t.Fatal("rollback left projects.status behind")
	}
	if v := w30ProbeValue(t, raw2); v == 0 {
		t.Fatal("probe answers 0 (already applied) after the rollback — a retry upgrade must be allowed")
	}
	now := Now()
	if _, err := raw2.Exec(`INSERT INTO projects(id,name,description,repos,created_at,updated_at)
		VALUES('p-w30-rt','w30-roundtrip','','[]',?1,?1)`, now); err != nil {
		t.Fatalf("old-version (v0.2.0) project write failed after the rollback: %v", err)
	}
}
