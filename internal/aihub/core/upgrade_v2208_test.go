package core

// upgrade_v2208_test.go — the deploy/sql/v2.20.8 window SQL (workbench#57,
// the machine-heartbeat permission model). The fixture is a real
// pre-heartbeat-shape database: the pre-#149 DDL with every earlier upgrade
// applied explicitly (NOT applyLaterUpgrades — the appended v2.20.8 entry
// turns that helper into the post-change shape this fixture must not be),
// so it carries the v2.20.7 shape with `machines` still in its v2.20.6 form
// populated with the three machine-user classes the migration must tell
// apart (an unreferenced heartbeat-minted one, a referenced one, an
// administrator-created one) and machines rows that carry an operator.
// Pinned: the probe's run-once answers, that only the unreferenced
// heartbeat-minted user is deleted, that every machines row survives with
// its operator, and that the rollback restores the v2.20.6 binary's
// user_id NOT NULL shape without resurrecting the deleted users.

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The fixture's identities.
const (
	v2208OperatorID = "u-v2208-op"
	v2208OrphanUser = "mac_v2208_orphan"
	v2208RefUser    = "mac_v2208_referenced"
	v2208AdminUser  = "mac_v2208_admin"
)

// v2206SchemaDB answers a populated database in the shape the last release
// before the heartbeat one ships (data dir + raw handle): every deploy/sql
// dir before v2.20.8 applied, so the messaging cursors are already split
// (v2.20.7) and `machines` still carries the v2.20.6 shape (`user_id`
// NOT NULL) that the migration changes. PRAGMA foreign_keys is ON, like the
// repo's Open: the migration must be safe under the constraint the running
// server enforces.
func v2206SchemaDB(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dataDir, raw := oldSchemaDB(t, false)
	if _, err := raw.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("fixture: PRAGMA foreign_keys: %v", err)
	}
	for _, v := range sqlVers {
		if v == sqlVer2208 {
			break
		}
		applySQLText(t, raw, loadSQLFileOf(t, v, "upgrade.sql"))
	}
	if !rawColumnExists(t, raw, "machines", "user_id") {
		t.Fatal("fixture: the v2.20.6 shape must still carry machines.user_id")
	}
	now := Now()
	stmts := []struct {
		stmt string
		args []any
	}{
		{`INSERT INTO users(id,display_name,email,kind,role,created_at)
			VALUES(?1,'v2208 operator','op@v2208.invalid','human','admin',?2)`,
			[]any{v2208OperatorID, now}},
		// A heartbeat-minted machine user nothing references.
		{`INSERT INTO users(id,display_name,email,kind,role,created_at)
			VALUES(?1,'orphan machine',?1||'@machines.local','machine','writer',?2)`,
			[]any{v2208OrphanUser, now}},
		// A heartbeat-minted machine user an api_keys row references.
		{`INSERT INTO users(id,display_name,email,kind,role,created_at)
			VALUES(?1,'referenced machine',?1||'@machines.local','machine','writer',?2)`,
			[]any{v2208RefUser, now}},
		{`INSERT INTO api_keys(id,user_id,project_id,name,key_hash,created_at)
			VALUES('k-v2208-ref',?1,NULL,'ref','deadbeef',?2)`,
			[]any{v2208RefUser, now}},
		// An administrator-created machine user (pf users create --user-type
		// machine): kind machine, but not a @machines.local mint.
		{`INSERT INTO users(id,display_name,email,kind,role,created_at)
			VALUES(?1,'admin machine','ops-machine@v2208.invalid','machine','writer',?2)`,
			[]any{v2208AdminUser, now}},
		// Four machines rows: one per user class plus a NULL-operator row.
		{`INSERT INTO machines(id,user_id,last_heartbeat_at,role,operator_id)
			VALUES('m-v2208-orphan',?1,?2,'runner',?3)`, []any{v2208OrphanUser, now, v2208OperatorID}},
		{`INSERT INTO machines(id,user_id,last_heartbeat_at,role,operator_id)
			VALUES('m-v2208-ref',?1,?2,'runner',?3)`, []any{v2208RefUser, now, v2208OperatorID}},
		{`INSERT INTO machines(id,user_id,last_heartbeat_at,role,operator_id)
			VALUES('m-v2208-admin',?1,?2,'runner',?1)`, []any{v2208AdminUser, now}},
		{`INSERT INTO machines(id,user_id,last_heartbeat_at,role,operator_id)
			VALUES('m-v2208-nullop',?1,?2,'runner',NULL)`, []any{v2208OrphanUser, now}},
	}
	for _, s := range stmts {
		if _, err := raw.Exec(s.stmt, s.args...); err != nil {
			t.Fatalf("fixture: %v\n%s", err, s.stmt)
		}
	}
	return dataDir, raw
}

// probe2208 executes v2.20.8's probe.sql final statement.
func probe2208(t *testing.T, db *sql.DB) int {
	t.Helper()
	stmts := upgradeStatements(loadSQLFileOf(t, sqlVer2208, "probe.sql"))
	var v int
	if err := db.QueryRow(stmts[len(stmts)-1]).Scan(&v); err != nil {
		t.Fatalf("probe statement failed: %v", err)
	}
	return v
}

// v2208MachineRows answers id → operator_id for every machines row.
func v2208MachineRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT id, COALESCE(operator_id,'') FROM machines`)
	if err != nil {
		t.Fatalf("read machines: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, op string
		if err := rows.Scan(&id, &op); err != nil {
			t.Fatalf("scan machines: %v", err)
		}
		out[id] = op
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk machines: %v", err)
	}
	return out
}

// v2208UserExists answers whether one users row survives.
func v2208UserExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id=?1`, id).Scan(&n); err != nil {
		t.Fatalf("count users %s: %v", id, err)
	}
	return n > 0
}

// columnNotNull answers the pragma notnull flag of one column.
func columnNotNull(t *testing.T, db *sql.DB, table, column string) (found, notNull bool, dflt sql.NullString) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("pragma %s: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var nn, pk int
		var d sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &nn, &d, &pk); err != nil {
			t.Fatalf("pragma scan %s: %v", table, err)
		}
		if name == column {
			return true, nn == 1, d
		}
	}
	return false, false, sql.NullString{}
}

// TestUpgradeV2208ProbeRunOnce: apply on the v2.20.6 shape, skip once
// applied, the rollback restores the column so a retry upgrade is allowed,
// and the retry works. The upgrade is deliberately not re-runnable (the
// second DROP COLUMN fails): the probe carries the run-once guarantee.
func TestUpgradeV2208ProbeRunOnce(t *testing.T) {
	_, raw := v2206SchemaDB(t)
	if v := probe2208(t, raw); v == 0 {
		t.Fatal("probe answers 0 (already applied) on the v2.20.6 shape - machines.user_id exists")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))
	if rawColumnExists(t, raw, "machines", "user_id") {
		t.Fatal("machines.user_id still present after the v2.20.8 upgrade")
	}
	if v := probe2208(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the upgrade, want 0", v)
	}
	failed := false
	for _, stmt := range upgradeStatements(loadSQLFileOf(t, sqlVer2208, "upgrade.sql")) {
		if _, err := raw.Exec(stmt); err != nil {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("re-running the v2.20.8 upgrade must fail - the probe is the run-once guard")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "rollback.sql"))
	if v := probe2208(t, raw); v == 0 {
		t.Fatal("probe answers 0 after the rollback - a retry upgrade must be allowed")
	}
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))
	if v := probe2208(t, raw); v != 0 {
		t.Fatalf("probe answers %d after the retry upgrade, want 0", v)
	}
}

// TestUpgradeV2208DeletesOnlyUnreferencedMintUsers: the migration drops the
// column first and then deletes exactly the heartbeat-minted users nothing
// references; every machines row survives, with its operator.
func TestUpgradeV2208DeletesOnlyUnreferencedMintUsers(t *testing.T) {
	_, raw := v2206SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))

	if v2208UserExists(t, raw, v2208OrphanUser) {
		t.Errorf("the unreferenced heartbeat-minted user %s survived the upgrade", v2208OrphanUser)
	}
	if !v2208UserExists(t, raw, v2208RefUser) {
		t.Errorf("the referenced heartbeat-minted user %s was deleted (its api_keys row would dangle)", v2208RefUser)
	}
	if !v2208UserExists(t, raw, v2208AdminUser) {
		t.Errorf("the administrator-created machine user %s was deleted (out of scope, requirement 12)", v2208AdminUser)
	}
	got := v2208MachineRows(t, raw)
	want := map[string]string{
		"m-v2208-orphan": v2208OperatorID,
		"m-v2208-ref":    v2208OperatorID,
		"m-v2208-admin":  v2208AdminUser,
		"m-v2208-nullop": "",
	}
	if len(got) != len(want) {
		t.Fatalf("machines rows after the upgrade = %v, want %d rows", got, len(want))
	}
	for id, op := range want {
		gotOp, ok := got[id]
		if !ok {
			t.Errorf("machines row %s did not survive the upgrade", id)
			continue
		}
		if gotOp != op {
			t.Errorf("machines row %s operator_id = %q, want %q", id, gotOp, op)
		}
	}
}

// TestUpgradeV2208RollbackRestoresV2206Shape: the rollback re-adds
// user_id NOT NULL filled from operator_id (” when there is none), the
// deleted users stay deleted, and the v2.20.6 binary's INSERT shape works.
func TestUpgradeV2208RollbackRestoresV2206Shape(t *testing.T) {
	_, raw := v2206SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "rollback.sql"))

	found, notNull, dflt := columnNotNull(t, raw, "machines", "user_id")
	if !found {
		t.Fatal("the rollback did not re-add machines.user_id")
	}
	if !notNull {
		t.Fatal("the rollback left machines.user_id nullable; the v2.20.6 binary declares and writes it NOT NULL")
	}
	if !dflt.Valid || dflt.String != "''" {
		t.Errorf("machines.user_id default = %+v, want ''", dflt)
	}
	if v2208UserExists(t, raw, v2208OrphanUser) {
		t.Error("the rollback resurrected the deleted heartbeat-minted user")
	}
	wantOperators := v2208MachineRows(t, raw)
	rows, err := raw.Query(`SELECT id, user_id FROM machines ORDER BY id`)
	if err != nil {
		t.Fatalf("read machines after the rollback: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, userID string
		if err := rows.Scan(&id, &userID); err != nil {
			t.Fatalf("scan machines: %v", err)
		}
		seen++
		if userID != wantOperators[id] {
			t.Errorf("machines row %s user_id = %q after the rollback, want the operator %q", id, userID, wantOperators[id])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk machines: %v", err)
	}
	if seen != 4 {
		t.Fatalf("machines rows after the rollback = %d, want 4", seen)
	}
	// The v2.20.6 registration shape (user_id NOT NULL named explicitly).
	now := Now()
	if _, err := raw.Exec(`INSERT INTO machines(id,user_id,last_heartbeat_at,role,operator_id)
		VALUES('m-v2208-oldshape',?1,?2,'runner',?1)`, v2208OperatorID, now); err != nil {
		t.Fatalf("a v2.20.6-shaped machines INSERT failed after the rollback: %v", err)
	}
}

// TestUpgradeV2208MigratedSchemaColumns pins the migrated machines column
// set: the fresh DDL's columns minus user_id (a fresh database is compared
// with the migrated one by the drop_machine_user_identity unit, which also
// changes the fresh DDL).
func TestUpgradeV2208MigratedSchemaColumns(t *testing.T) {
	_, raw := v2206SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))
	for _, c := range []string{
		"id", "runtime_version", "mode", "live_wi_ids", "last_heartbeat_at",
		"public_key", "launcher", "role", "operator_id", "superseded_at", "capability",
	} {
		if !rawColumnExists(t, raw, "machines", c) {
			t.Errorf("machines.%s missing after the v2.20.8 upgrade", c)
		}
	}
	if rawColumnExists(t, raw, "machines", "user_id") {
		t.Error("machines.user_id present after the v2.20.8 upgrade")
	}
}

// machinesPragma answers the machines table's full pragma (one string per
// column) for a structural comparison.
func machinesPragma(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(machines)`)
	if err != nil {
		t.Fatalf("pragma machines: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cid, nn, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &nn, &dflt, &pk); err != nil {
			t.Fatalf("pragma scan machines: %v", err)
		}
		out = append(out, fmt.Sprintf("%d:%s:%s:%d:%v:%d", cid, name, ctype, nn, dflt, pk))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("walk pragma machines: %v", err)
	}
	return out
}

// TestUpgradeV2208FreshAndMigratedSchemaAgree: a database the current Open
// creates and one migrated from v2.20.6 end with the same machines schema —
// and neither carries user_id.
func TestUpgradeV2208FreshAndMigratedSchemaAgree(t *testing.T) {
	dataDir, raw := v2206SchemaDB(t)
	applySQLText(t, raw, loadSQLFileOf(t, sqlVer2208, "upgrade.sql"))
	migrated := machinesPragma(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dataDir)
	if err != nil {
		t.Fatalf("current Open on the migrated file: %v", err)
	}
	defer reopened.Close()
	if got := machinesPragma(t, reopened.DB); !reflect.DeepEqual(got, migrated) {
		t.Fatalf("the current Open changed the migrated machines schema:\n got %v\nwant %v", got, migrated)
	}

	fresh, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("fresh Open: %v", err)
	}
	defer fresh.Close()
	freshPragma := machinesPragma(t, fresh.DB)
	if !reflect.DeepEqual(freshPragma, migrated) {
		t.Fatalf("fresh and migrated machines schemas differ:\n fresh    %v\n migrated %v", freshPragma, migrated)
	}
	for _, col := range migrated {
		if strings.HasPrefix(col, "6:user_id:") || strings.Contains(col, ":user_id:") {
			t.Fatalf("machines.user_id present in the agreed schema: %v", migrated)
		}
	}
}
