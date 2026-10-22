package core

// upgrade_v2170_test.go — the deploy/sql/v2.17.0 window SQL: the
// admin_audit table of the project hard delete. The
// fixture is a real v2.16-shape database: the pre-#149 DDL with every
// earlier upgrade applied (the current schema minus admin_audit — no
// release before v2.17.0 creates it). Pinned: the probe's run-once
// answers (apply on the v2.16 shape, skip once applied, apply again
// after the rollback), the upgraded file opening with the current Open,
// the rollback removing the table, and a fresh database already being
// in the applied shape.

import (
	"database/sql"
	"testing"
)

// v216SchemaDB answers a populated v2.16-shape database (data dir + raw
// handle): the pre-#149 DDL plus every upgrade before v2.17.0 — the
// current schema minus admin_audit.
func v216SchemaDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, true)
	applySQLText(t, raw, loadSQLFile(t, "upgrade.sql"))
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer250, "upgrade.sql"))
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2140, "upgrade.sql"))
	if rawTableExists(t, raw, "admin_audit") {
		t.Fatal("fixture: the v2.16 shape must not carry admin_audit")
	}
	return dataDir, raw
}

// probe2170 executes v2.17.0's probe.sql final statement.
func probe2170(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, sqlVer2170, "probe.sql"))
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v", err)
	}
	return v
}

// TestUpgradeV2170ProbeRunOnce: apply on the v2.16 shape (the table is
// absent), skip once applied, the rollback removes the table so a retry
// upgrade is allowed, and the retry works.
func TestUpgradeV2170ProbeRunOnce(t *testing.T) {
	_, raw := v216SchemaDB(t)
	if v := probe2170(t, raw); v == 0 {
		t.Fatal("probe answers 0 (applied) on the v2.16 shape - admin_audit is absent")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2170, "upgrade.sql"))
	if !rawTableExists(t, raw, "admin_audit") {
		t.Fatal("admin_audit missing after the v2.17.0 upgrade")
	}
	if v := probe2170(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the upgrade, want 0", v)
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2170, "rollback.sql"))
	if rawTableExists(t, raw, "admin_audit") {
		t.Fatal("rollback did not remove admin_audit")
	}
	if v := probe2170(t, raw); v == 0 {
		t.Fatal("probe answers 0 after the rollback - a retry upgrade must be allowed")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2170, "upgrade.sql"))
	if !rawTableExists(t, raw, "admin_audit") {
		t.Fatal("admin_audit missing after the retry upgrade")
	}
}

// TestUpgradeV2170OpensWithCurrentOpen: the upgraded file opens with the
// current Open (the same DDL, CREATE TABLE IF NOT EXISTS) and carries the
// table — the delete path's INSERT lands on it.
func TestUpgradeV2170OpensWithCurrentOpen(t *testing.T) {
	dataDir, raw := v216SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2170, "upgrade.sql"))
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("current Open on the upgraded v2.16 file: %v", err)
	}
	defer db.Close()
	if !rawTableExists(t, db.DB, "admin_audit") {
		t.Fatal("admin_audit missing after the current Open")
	}
	if v := probe2170(t, db.DB); v != 0 {
		t.Fatalf("probe answers %d on the reopened upgraded file, want 0", v)
	}
}

// TestFreshDBIsV2170Shape: a database the current Open creates is
// already in the applied shape — the probe skips it.
func TestFreshDBIsV2170Shape(t *testing.T) {
	env := newEnv(t)
	if !rawTableExists(t, env.DB.DB, "admin_audit") {
		t.Fatal("a fresh database carries no admin_audit")
	}
	if v := probe2170(t, env.DB.DB); v != 0 {
		t.Fatalf("probe answers %d on a fresh database, want 0", v)
	}
}
